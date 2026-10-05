package db

// PurgeRunsBefore deletes the finished runs that started before cutoff (Unix ms) with all their
// evidence (tests, steps, network, DOM, diagnoses, escalations: ON DELETE CASCADE). It returns
// how many runs were deleted and the screenshot URLs ("/screenshots/<file>") they referenced, so
// the caller can delete the files.
func (s *Store) PurgeRunsBefore(cutoff int64) (int, []string, error) {
	rows, err := s.db.Query(`SELECT l.screenshot FROM logs l JOIN tests t ON t.id = l.test_id JOIN runs r ON r.id = t.run_id
		WHERE r.started_at < ? AND r.status != 'RUNNING' AND l.screenshot != ''`, cutoff)
	if err != nil {
		return 0, nil, err
	}
	var shots []string
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			rows.Close()
			return 0, nil, err
		}
		shots = append(shots, url)
	}
	rows.Close()
	res, err := s.db.Exec(`DELETE FROM runs WHERE started_at < ? AND status != 'RUNNING'`, cutoff)
	if err != nil {
		return 0, nil, err
	}
	n, _ := res.RowsAffected()
	return int(n), shots, nil
}
