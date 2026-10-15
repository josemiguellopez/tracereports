package db

import (
	"database/sql"
	"encoding/json"
)

// FixLab: checks saved from the Request Lab and the verifications of an incident. A check is a
// captured call (optionally edited) plus what a good answer looks like; "Verify" runs every check
// of an incident and records whether all passed, with the before/after evidence. Secrets are never
// stored: the request keeps its {{variables}} and they are typed again to verify.
const labSchema = `
CREATE TABLE IF NOT EXISTS lab_checks (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id       INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	test_id      INTEGER NOT NULL,
	conn_id      INTEGER NOT NULL,
	incident_key TEXT    NOT NULL DEFAULT '',
	name         TEXT    NOT NULL,
	request      TEXT    NOT NULL DEFAULT '', -- JSON de la petición editada ('' = la capturada)
	base_url     TEXT    NOT NULL DEFAULT '',
	expect       TEXT    NOT NULL,            -- JSON: {status, contains}
	created_at   INTEGER NOT NULL,
	last_ok      INTEGER,                     -- NULL = nunca verificado
	last_at      INTEGER NOT NULL DEFAULT 0,
	last_result  TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_lab_checks_run ON lab_checks(run_id, incident_key);
CREATE TABLE IF NOT EXISTS lab_verifications (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id       INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	incident_key TEXT    NOT NULL,
	ok           INTEGER NOT NULL,
	results      TEXT    NOT NULL, -- JSON: resultado de cada check (antes / ahora)
	author       TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lab_verifications_run ON lab_verifications(run_id, incident_key, id);
`

// LabCheck is a saved check.
type LabCheck struct {
	ID          int64           `json:"id"`
	RunID       int64           `json:"run_id"`
	TestID      int64           `json:"test_id"`
	ConnID      int64           `json:"conn_id"`
	IncidentKey string          `json:"incident_key"`
	Name        string          `json:"name"`
	Request     json.RawMessage `json:"request,omitempty"` // nil = la llamada capturada
	BaseURL     string          `json:"base_url,omitempty"`
	Expect      json.RawMessage `json:"expect"`
	CreatedAt   int64           `json:"created_at"`
	LastOK      *bool           `json:"last_ok"`
	LastAt      int64           `json:"last_at,omitempty"`
	LastResult  json.RawMessage `json:"last_result,omitempty"`
}

// LabVerification is one run of an incident's checks.
type LabVerification struct {
	ID          int64           `json:"id"`
	RunID       int64           `json:"run_id"`
	IncidentKey string          `json:"incident_key"`
	OK          bool            `json:"ok"`
	Results     json.RawMessage `json:"results"`
	Author      string          `json:"author,omitempty"`
	CreatedAt   int64           `json:"created_at"`
}

func rawOrEmpty(r json.RawMessage) string {
	if len(r) == 0 || string(r) == "null" {
		return ""
	}
	return string(r)
}

// SaveLabCheck stores a new check.
func (s *Store) SaveLabCheck(c *LabCheck) error {
	if c.CreatedAt == 0 {
		c.CreatedAt = NowMs()
	}
	res, err := s.db.Exec(`INSERT INTO lab_checks(run_id, test_id, conn_id, incident_key, name, request, base_url, expect, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, c.RunID, c.TestID, c.ConnID, c.IncidentKey, c.Name, rawOrEmpty(c.Request), c.BaseURL, string(c.Expect), c.CreatedAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

// DeleteLabCheck removes a check (ErrNotFound if it does not exist).
func (s *Store) DeleteLabCheck(id int64) error {
	res, err := s.db.Exec(`DELETE FROM lab_checks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LabCheckResult records the last result of a check.
func (s *Store) LabCheckResult(id int64, ok bool, result json.RawMessage) error {
	_, err := s.db.Exec(`UPDATE lab_checks SET last_ok = ?, last_at = ?, last_result = ? WHERE id = ?`, ok, NowMs(), rawOrEmpty(result), id)
	return err
}

// LabChecks returns the checks of a run, oldest first; incidentKey "" means all of them.
func (s *Store) LabChecks(runID int64, incidentKey string) ([]LabCheck, error) {
	q := `SELECT id, run_id, test_id, conn_id, incident_key, name, request, base_url, expect, created_at, last_ok, last_at, last_result
		FROM lab_checks WHERE run_id = ?`
	args := []any{runID}
	if incidentKey != "" {
		q += ` AND incident_key = ?`
		args = append(args, incidentKey)
	}
	rows, err := s.db.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LabCheck{}
	for rows.Next() {
		var c LabCheck
		var req, exp, last string
		var lastOK sql.NullBool
		if err := rows.Scan(&c.ID, &c.RunID, &c.TestID, &c.ConnID, &c.IncidentKey, &c.Name, &req, &c.BaseURL, &exp, &c.CreatedAt, &lastOK, &c.LastAt, &last); err != nil {
			return nil, err
		}
		if req != "" {
			c.Request = json.RawMessage(req)
		}
		c.Expect = json.RawMessage(exp)
		if last != "" {
			c.LastResult = json.RawMessage(last)
		}
		if lastOK.Valid {
			ok := lastOK.Bool
			c.LastOK = &ok
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveLabVerification records one verification of an incident.
func (s *Store) SaveLabVerification(v *LabVerification) error {
	if v.CreatedAt == 0 {
		v.CreatedAt = NowMs()
	}
	res, err := s.db.Exec(`INSERT INTO lab_verifications(run_id, incident_key, ok, results, author, created_at) VALUES(?,?,?,?,?,?)`,
		v.RunID, v.IncidentKey, v.OK, string(v.Results), v.Author, v.CreatedAt)
	if err != nil {
		return err
	}
	v.ID, err = res.LastInsertId()
	return err
}

// LatestLabVerifications returns the last verification of each incident of a run, by key.
func (s *Store) LatestLabVerifications(runID int64) (map[string]LabVerification, error) {
	rows, err := s.db.Query(`SELECT id, run_id, incident_key, ok, results, author, created_at FROM lab_verifications
		WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]LabVerification{}
	for rows.Next() {
		var v LabVerification
		var results string
		if err := rows.Scan(&v.ID, &v.RunID, &v.IncidentKey, &v.OK, &results, &v.Author, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Results = json.RawMessage(results)
		out[v.IncidentKey] = v // ordenadas: queda la última
	}
	return out, rows.Err()
}
