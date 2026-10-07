package db

import (
	"database/sql"
	"errors"
	"strings"
)

// Baseline is the same backend call in the last run where the test passed: what changed between
// "it worked" and "it failed" (status, headers, body) is usually the cause.
type Baseline struct {
	Conn   NetConn `json:"conn"`
	RunID  int64   `json:"run_id"`
	TestID int64   `json:"test_id"`
	// SameContext is false when no green run exists in the same project, environment and branch
	// and the baseline comes from another branch or environment of the project.
	SameContext bool `json:"same_context"`
}

// CallSignature is method + host + normalized path (NormalizeEndpoint): /orders/123 and
// /orders/456 are the same call.
func CallSignature(method, rawURL string) string {
	host, path := NormalizeEndpoint(rawURL)
	return strings.ToUpper(method) + " " + strings.ToLower(host) + path
}

// maxBaselineRuns is how many earlier green runs of the test are searched for the call.
const maxBaselineRuns = 10

// BaselineFor finds, for a connection of a failed test, the same call in the most recent earlier
// run where that test passed (same context first, then same project). nil when there is none.
func (s *Store) BaselineFor(connID int64) (*Baseline, error) {
	conns, err := s.queryNetwork(`WHERE id=?`, connID)
	if err != nil {
		return nil, err
	}
	if len(conns) == 0 {
		return nil, ErrNotFound
	}
	c := conns[0]
	var key string
	var runID int64
	if err := s.db.QueryRow(`SELECT test_key, run_id FROM tests WHERE id=?`, c.TestID).Scan(&key, &runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if key == "" {
		return nil, nil
	}
	sig := CallSignature(c.Method, c.URL)
	for _, sameCtx := range []bool{true, false} {
		where := `r.project = (SELECT project FROM runs WHERE id = ?)`
		args := []any{key, runID, runID}
		if sameCtx {
			where = sameContext
			args = append([]any{key, runID}, ctxArgs(runID)...)
		}
		rows, err := s.db.Query(`SELECT t.id, t.run_id FROM tests t JOIN runs r ON r.id = t.run_id
			WHERE t.test_key = ? AND t.run_id < ? AND t.status = 'PASS' AND `+where+`
			ORDER BY t.run_id DESC LIMIT ?`, append(args, maxBaselineRuns)...)
		if err != nil {
			return nil, err
		}
		var cands [][2]int64
		for rows.Next() {
			var tid, rid int64
			if err := rows.Scan(&tid, &rid); err != nil {
				rows.Close()
				return nil, err
			}
			cands = append(cands, [2]int64{tid, rid})
		}
		rows.Close()
		for _, cand := range cands {
			past, err := s.ListNetwork(cand[0])
			if err != nil {
				return nil, err
			}
			for _, p := range past {
				if CallSignature(p.Method, p.URL) == sig {
					return &Baseline{Conn: p, RunID: cand[1], TestID: cand[0], SameContext: sameCtx}, nil
				}
			}
		}
	}
	return nil, nil
}
