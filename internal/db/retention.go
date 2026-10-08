package db

import "strings"

// PurgeRunsBefore deletes the finished runs that started before cutoff (Unix ms) with all their
// evidence (tests, steps, network, DOM, diagnoses, escalations, artifacts: ON DELETE CASCADE). It
// returns how many runs were deleted and the URLs ("/screenshots/<file>") of the screenshots and
// artifacts (traces, videos) they referenced, so the caller can delete the files.
//
// It is one transaction that takes the write lock from its first statement: the set of runs is
// fixed, their files are read and exactly those runs are deleted, while no other writer can close
// a run or add evidence in between (that left files nobody would ever delete). A run closed after
// the purge started is left for the next pass, with its files.
func (s *Store) PurgeRunsBefore(cutoff int64) (int, []string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	// una escritura que no cambia nada: toma el lock de escritura ya, no al llegar al DELETE
	if _, err := tx.Exec(`DELETE FROM runs WHERE 0`); err != nil {
		return 0, nil, err
	}
	ids, err := int64s(tx, `SELECT id FROM runs WHERE started_at < ? AND status != 'RUNNING' ORDER BY id`, cutoff)
	if err != nil || len(ids) == 0 {
		return 0, nil, err
	}
	var files []string
	for start := 0; start < len(ids); start += purgeChunk {
		chunk := ids[start:min(start+purgeChunk, len(ids))]
		in, args := inList(chunk)
		urls, err := strs(tx, `SELECT l.screenshot FROM logs l JOIN tests t ON t.id = l.test_id
			WHERE t.run_id IN `+in+` AND l.screenshot != ''
			UNION ALL
			SELECT a.url FROM artifacts a JOIN tests t ON t.id = a.test_id WHERE t.run_id IN `+in, append(args, args...)...)
		if err != nil {
			return 0, nil, err
		}
		files = append(files, urls...)
	}
	if purgeHook != nil {
		purgeHook()
	}
	deleted := 0
	for start := 0; start < len(ids); start += purgeChunk {
		in, args := inList(ids[start:min(start+purgeChunk, len(ids))])
		res, err := tx.Exec(`DELETE FROM runs WHERE id IN `+in, args...)
		if err != nil {
			return 0, nil, err
		}
		n, _ := res.RowsAffected()
		deleted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return deleted, files, nil
}

// purgeChunk keeps each IN (...) well below SQLite's limit of bound parameters.
const purgeChunk = 500

func inList(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(?" + strings.Repeat(",?", len(ids)-1) + ")", args
}

func int64s(q txHandle, query string, args ...any) ([]int64, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func strs(q txHandle, query string, args ...any) ([]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// purgeHook runs between choosing the evidence and deleting the runs (tests only).
var purgeHook func()
