package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const idempotencySchema = `
CREATE TABLE IF NOT EXISTS idempotency (
	key        TEXT    PRIMARY KEY,   -- método + ruta + Idempotency-Key del cliente
	status     INTEGER NOT NULL,
	body       BLOB    NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_idempotency_created ON idempotency(created_at);
`

// IdempotentResponse returns the stored response of a write already applied with this key.
func (s *Store) IdempotentResponse(key string) (status int, body []byte, found bool, err error) {
	err = s.db.QueryRow(`SELECT status, body FROM idempotency WHERE key = ?`, key).Scan(&status, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, nil
	}
	return status, body, err == nil, err
}

// SaveIdempotentResponse remembers the response of a write so a retry gets it back instead of
// applying the write twice. A write that belongs to a run (runID > 0) is remembered as long as the
// run exists (deleted with it by retention): a spool replayed days later never duplicates its
// steps, screenshots or network batches. Entries without a run are dropped after 24 h.
func (s *Store) SaveIdempotentResponse(key string, status int, body []byte, runID int64) error {
	now := NowMs()
	if _, err := s.db.Exec(`DELETE FROM idempotency WHERE created_at < ? AND run_id IS NULL`, now-24*3600*1000); err != nil {
		return err
	}
	var run any
	if runID > 0 {
		run = runID
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO idempotency(key, status, body, created_at, run_id) VALUES(?,?,?,?,?)`,
		key, status, body, now, run)
	return err
}

var idemPathRe = regexp.MustCompile(`^/api/v1/(runs|tests)/(\d+)(?:/|$)`)

// runForWrite is the run an API write belongs to (0 if none or if it no longer exists): from the
// path (/runs/{id}/..., /tests/{id}/... through its test) or, for POST /api/v1/runs, from the
// response that created the run.
func runForWrite(q querier, method, path string, response []byte) int64 {
	var runID sql.NullInt64
	if m := idemPathRe.FindStringSubmatch(path); m != nil {
		id, _ := strconv.ParseInt(m[2], 10, 64)
		if m[1] == "runs" {
			q.QueryRow(`SELECT id FROM runs WHERE id = ?`, id).Scan(&runID)
		} else {
			q.QueryRow(`SELECT run_id FROM tests WHERE id = ?`, id).Scan(&runID)
		}
		return runID.Int64
	}
	if method == "POST" && path == "/api/v1/runs" {
		var out struct {
			RunID int64 `json:"run_id"`
		}
		if json.Unmarshal(response, &out) == nil && out.RunID > 0 {
			q.QueryRow(`SELECT id FROM runs WHERE id = ?`, out.RunID).Scan(&runID)
		}
	}
	return runID.Int64
}

// RunForWrite is runForWrite on the store (used by the idempotency middleware).
func (s *Store) RunForWrite(method, path string, response []byte) int64 {
	return runForWrite(s.db, method, path, response)
}

// backfillIdempotencyRuns links the entries stored before idempotency.run_id existed to their
// run, so a spool replayed after upgrading does not duplicate evidence. Entries whose run no
// longer exists (or that never belonged to one) stay unlinked and expire after 24 h. It only
// looks at unlinked rows, so running it on every start is cheap and idempotent.
func backfillIdempotencyRuns(sqldb *sql.DB) error {
	rows, err := sqldb.Query(`SELECT key, body FROM idempotency WHERE run_id IS NULL`)
	if err != nil {
		return err
	}
	type entry struct {
		key  string
		body []byte
	}
	var pending []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.key, &e.body); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, e)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, e := range pending {
		parts := strings.SplitN(e.key, " ", 3) // método, ruta, Idempotency-Key
		if len(parts) < 3 {
			continue
		}
		if runID := runForWrite(sqldb, parts[0], parts[1], e.body); runID > 0 {
			if _, err := sqldb.Exec(`UPDATE idempotency SET run_id = ? WHERE key = ?`, runID, e.key); err != nil {
				return err
			}
		}
	}
	return nil
}
