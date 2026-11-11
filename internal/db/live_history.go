package db

import (
	"cmp"
	"database/sql"
	"slices"
	"strings"
)

// RunCounts are the results of a run's tests, counted in the database (the history of Live does not
// load the tests of every past run).
type RunCounts struct {
	Total, Passed, Failed, Running int
}

// HistoryRuns returns the runs that started in [from, to) and, with q, whose name, project, environment,
// branch or execution_id contains it (case-insensitive), oldest first and at most max (the newest ones).
// It reads from the read-only pool: it never waits for, nor holds, the writer.
func (s *Store) HistoryRuns(from, to int64, q string, max int) ([]LiveRun, error) {
	where, args := `r.started_at >= ? AND r.started_at < ?`, []any{from, to}
	if q = strings.TrimSpace(q); q != "" {
		// lo que se escribe, literal (un "_" o un "%" no son comodines) y sin distinguir mayúsculas
		like := "%" + likeEscape(strings.ToLower(q)) + "%"
		where += ` AND (LOWER(r.name) LIKE ? ESCAPE '\' OR LOWER(r.project) LIKE ? ESCAPE '\' OR LOWER(r.environment) LIKE ? ESCAPE '\'
			OR LOWER(r.branch) LIKE ? ESCAPE '\' OR LOWER(r.execution_id) LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like, like)
	}
	rows, err := s.reader().Query(`SELECT `+liveRunCols+` FROM runs r WHERE `+where+`
		ORDER BY r.started_at DESC, r.id DESC LIMIT ?`, append(args, max)...)
	if err != nil {
		return nil, err
	}
	return scanRunsOldestFirst(rows)
}

// RunsOfExecutions returns every run of these execution ids, oldest first.
func (s *Store) RunsOfExecutions(ids []string) ([]LiveRun, error) {
	var out []LiveRun
	for start := 0; start < len(ids); start += purgeChunk {
		chunk := ids[start:min(start+purgeChunk, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		// "!= ''": así SQLite usa el índice parcial idx_runs_execution; el orden se pone después (en Go):
		// con ORDER BY started_at prefería recorrer la tabla entera por el índice de la fecha
		rows, err := s.reader().Query(`SELECT `+liveRunCols+` FROM runs r WHERE r.execution_id != '' AND r.execution_id IN (?`+strings.Repeat(",?", len(chunk)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		runs, err := scanRunsOldestFirst(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, runs...)
	}
	slices.SortFunc(out, func(a, b LiveRun) int {
		if a.StartedAt != b.StartedAt {
			return cmp.Compare(a.StartedAt, b.StartedAt)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}

// RunCountsOf counts the tests of these runs by result.
func (s *Store) RunCountsOf(ids []int64) (map[int64]RunCounts, error) {
	out := make(map[int64]RunCounts, len(ids))
	for start := 0; start < len(ids); start += purgeChunk {
		in, args := inList(ids[start:min(start+purgeChunk, len(ids))])
		rows, err := s.reader().Query(`SELECT run_id, COUNT(*), SUM(status = 'PASS'), SUM(status IN ('FAIL', 'INTERRUPTED')), SUM(status = 'RUNNING')
			FROM tests WHERE run_id IN `+in+` GROUP BY run_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var c RunCounts
			if err := rows.Scan(&id, &c.Total, &c.Passed, &c.Failed, &c.Running); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = c
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scanRunsOldestFirst reads runs listed newest first and returns them oldest first (as GroupExecutions
// expects); it closes rows.
func scanRunsOldestFirst(rows *sql.Rows) ([]LiveRun, error) {
	defer rows.Close()
	var runs []LiveRun
	for rows.Next() {
		r, err := scanLiveRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	return runs, nil
}
