package db

import (
	"database/sql"
	"errors"
)

// Quarantine of known flaky tests: while a test is quarantined in its project, its failures do
// not turn a run red (the run is at most WARNING) but they are still shown and counted apart.
// Each quarantine has a reason, an owner and an expiry date: when it expires it stops applying,
// so a test cannot stay hidden forever.
const quarantineSchema = `
CREATE TABLE IF NOT EXISTS quarantine (
	project    TEXT    NOT NULL DEFAULT '',
	test_key   TEXT    NOT NULL,
	reason     TEXT    NOT NULL DEFAULT '',
	owner      TEXT    NOT NULL DEFAULT '',
	until      INTEGER NOT NULL,            -- vence (Unix ms)
	created_at INTEGER NOT NULL,
	PRIMARY KEY (project, test_key)
);
`

// activeQuarantine is the SQL condition "test t of run r is quarantined right now".
const activeQuarantine = `EXISTS (SELECT 1 FROM quarantine q WHERE q.project = r.project AND q.test_key = t.test_key AND q.until > ?)`

// Quarantine is the quarantine of one test.
type Quarantine struct {
	Project   string `json:"project"`
	Key       string `json:"key"`
	Reason    string `json:"reason"`
	Owner     string `json:"owner"`
	Until     int64  `json:"until"`
	CreatedAt int64  `json:"created_at"`
	Active    bool   `json:"active"` // no venció
}

// SetQuarantine quarantines (or updates the quarantine of) a test of a project.
func (s *Store) SetQuarantine(q *Quarantine) error {
	if q.CreatedAt == 0 {
		q.CreatedAt = NowMs()
	}
	_, err := s.db.Exec(`INSERT INTO quarantine(project, test_key, reason, owner, until, created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(project, test_key) DO UPDATE SET reason=excluded.reason, owner=excluded.owner, until=excluded.until, created_at=excluded.created_at`,
		q.Project, q.Key, q.Reason, q.Owner, q.Until, q.CreatedAt)
	return err
}

// RemoveQuarantine lifts the quarantine of a test; false if it had none.
func (s *Store) RemoveQuarantine(project, key string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM quarantine WHERE project=? AND test_key=?`, project, key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListQuarantine lists the quarantined tests of a project ("" = every project), active first.
func (s *Store) ListQuarantine(project string, all bool) ([]Quarantine, error) {
	q := `SELECT project, test_key, reason, owner, until, created_at FROM quarantine`
	var args []any
	if !all {
		q += ` WHERE project = ?`
		args = append(args, project)
	}
	rows, err := s.db.Query(q+` ORDER BY until DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := NowMs()
	out := []Quarantine{}
	for rows.Next() {
		var x Quarantine
		if err := rows.Scan(&x.Project, &x.Key, &x.Reason, &x.Owner, &x.Until, &x.CreatedAt); err != nil {
			return nil, err
		}
		x.Active = x.Until > now
		out = append(out, x)
	}
	return out, rows.Err()
}

// GetQuarantine returns the quarantine of a test (active or expired), or nil.
func (s *Store) GetQuarantine(project, key string) (*Quarantine, error) {
	var x Quarantine
	err := s.db.QueryRow(`SELECT project, test_key, reason, owner, until, created_at FROM quarantine WHERE project=? AND test_key=?`, project, key).
		Scan(&x.Project, &x.Key, &x.Reason, &x.Owner, &x.Until, &x.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	x.Active = x.Until > NowMs()
	return &x, nil
}

// RefreshRun recomputes the status of a closed run (after a quarantine change, for example).
func (s *Store) RefreshRun(runID int64) error {
	return refreshClosedRun(s.db, runID)
}
