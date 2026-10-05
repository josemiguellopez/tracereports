package db

import (
	"database/sql"
	"encoding/json"
	"errors"
)

const escalationSchema = `
CREATE TABLE IF NOT EXISTS escalations (
	run_id     INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	test_id    INTEGER NOT NULL DEFAULT 0,   -- 0 = la ejecución completa
	audience   TEXT    NOT NULL,             -- business | qa | dev
	lang       TEXT    NOT NULL,             -- es | en
	payload    TEXT    NOT NULL,             -- JSON del resumen generado
	created_at INTEGER NOT NULL,
	PRIMARY KEY (run_id, test_id, audience, lang)
);
`

// SaveEscalation caches a generated escalation summary (so viewing it again costs no AI quota).
func (s *Store) SaveEscalation(runID, testID int64, audience, lang, payload string) error {
	_, err := s.db.Exec(`INSERT INTO escalations(run_id, test_id, audience, lang, payload, created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(run_id, test_id, audience, lang) DO UPDATE SET payload=excluded.payload, created_at=excluded.created_at`,
		runID, testID, audience, lang, payload, NowMs())
	return err
}

// GetEscalation returns a cached escalation payload, or "" if there is none.
func (s *Store) GetEscalation(runID, testID int64, audience, lang string) (string, error) {
	var payload string
	err := s.db.QueryRow(`SELECT payload FROM escalations WHERE run_id=? AND test_id=? AND audience=? AND lang=?`,
		runID, testID, audience, lang).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return payload, err
}

// Recurrence tells in how many recent runs of the same suite an incident (by key) showed up.
type Recurrence struct {
	Seen      int   `json:"seen"`        // ejecuciones anteriores con el mismo incidente
	Of        int   `json:"of"`          // ejecuciones anteriores revisadas
	LastRunID int64 `json:"last_run_id"` // la más reciente en que apareció
}

// IncidentRecurrence checks the incidents of a run against the diagnoses of the previous
// `window` runs with the same name. Keys missing from the result never appeared before.
func (s *Store) IncidentRecurrence(runID int64, window int) (map[string]Recurrence, error) {
	rt, err := s.GetRunTriage(runID)
	if err != nil || rt == nil || len(rt.Incidents) == 0 {
		return map[string]Recurrence{}, err
	}
	rows, err := s.db.Query(`SELECT r.id, COALESCE(t.incidents, '[]') FROM runs r
		LEFT JOIN run_triage t ON t.run_id = r.id
		WHERE r.name = (SELECT name FROM runs WHERE id = ?) AND r.id < ? AND r.status != 'RUNNING' AND `+sameContext+`
		ORDER BY r.id DESC LIMIT ?`, append(append([]any{runID, runID}, ctxArgs(runID)...), window)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Recurrence{}
	want := map[string]bool{}
	for _, in := range rt.Incidents {
		want[in.Key] = true
	}
	of := 0
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		of++
		var incs []Incident
		_ = json.Unmarshal([]byte(raw), &incs)
		seen := map[string]bool{}
		for _, in := range incs {
			if want[in.Key] && !seen[in.Key] {
				seen[in.Key] = true
				r := out[in.Key]
				r.Seen++
				if r.LastRunID == 0 {
					r.LastRunID = id
				}
				out[in.Key] = r
			}
		}
	}
	for k, r := range out {
		r.Of = of
		out[k] = r
	}
	for k := range want {
		if _, ok := out[k]; !ok {
			out[k] = Recurrence{Of: of}
		}
	}
	return out, rows.Err()
}
