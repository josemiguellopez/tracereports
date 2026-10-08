package db

// Runs abandoned in RUNNING: a client that died (killed CI job, lost machine) never calls
// finish. Activity is the server time of the last write received for the run (not the times the
// client reports: an offline client replays old ones), so a long run that keeps sending is never
// closed.

// staleMigration fills the activity of the runs still open from older versions (once).
const staleMigration = `
UPDATE runs SET last_activity = MAX(started_at,
	COALESCE((SELECT MAX(COALESCE(t.ended_at, t.started_at)) FROM tests t WHERE t.run_id = runs.id), 0),
	COALESCE((SELECT MAX(l.timestamp) FROM logs l JOIN tests t ON t.id = l.test_id WHERE t.run_id = runs.id), 0))
	WHERE last_activity = 0 AND status = 'RUNNING';
CREATE INDEX IF NOT EXISTS idx_runs_running ON runs(status, last_activity);
`

// TouchRun records that the run received something now.
func (s *Store) TouchRun(runID int64) error {
	_, err := s.db.Exec(`UPDATE runs SET last_activity = ? WHERE id = ? AND last_activity < ?`, NowMs(), runID, NowMs())
	return err
}

// StaleRuns lists the runs still RUNNING with no activity since cutoff (Unix ms).
func (s *Store) StaleRuns(cutoff int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM runs WHERE status = 'RUNNING' AND last_activity < ? ORDER BY id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CloseIfIdle closes a run as incomplete (its RUNNING tests become interrupted failures) only if
// it is still RUNNING and still without activity since cutoff, checked in the same transaction:
// a write that arrives meanwhile keeps it open. closed reports whether it was closed now.
func (s *Store) CloseIfIdle(runID, cutoff int64) (closed, first bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	var idle bool
	if err := tx.QueryRow(`SELECT status = 'RUNNING' AND last_activity < ? FROM runs WHERE id = ?`, cutoff, runID).Scan(&idle); err != nil || !idle {
		return false, false, err
	}
	if first, err = closeRunTx(tx, runID, true); err != nil {
		return false, false, err
	}
	return true, first, tx.Commit()
}
