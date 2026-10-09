package db

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// RunSearchQuery is the bounded, read-only query used by the run explorer.
// Dates are Unix milliseconds at the beginning/end of their respective days.
type RunSearchQuery struct {
	Q, Project, Environment, Branch, Tag, Owner, Sort string
	Status                                            []string
	Incomplete, Flaky                                 *bool
	From, To                                          int64
	Limit                                             int
	Cursor                                            string
}

// RunSearchItem deliberately contains only list data. Opening a run still uses GetRunDetail.
type RunSearchItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Branch      string `json:"branch"`
	Commit      string `json:"commit"`
	Status      string `json:"status"`
	Incomplete  bool   `json:"incomplete"`
	StartedAt   int64  `json:"started_at"`
	Duration    int64  `json:"duration"`
	Total       int    `json:"total"`
	Passed      int    `json:"passed"`
	Failed      int    `json:"failed"`
	Skipped     int    `json:"skipped"`
	Warning     int    `json:"warning"`
	Quarantined int    `json:"quarantined"`
	FlakyCount  int    `json:"flaky_count"`
}

type runSearchCursor struct{ Value, StartedAt, ID int64 }

func decodeRunSearchCursor(raw string) (runSearchCursor, error) {
	if raw == "" {
		return runSearchCursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return runSearchCursor{}, fmt.Errorf("invalid cursor")
	}
	var c runSearchCursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID <= 0 {
		return runSearchCursor{}, fmt.Errorf("invalid cursor")
	}
	return c, nil
}

func encodeRunSearchCursor(c runSearchCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// runSearchWhere creates a parameterized predicate. Search terms use an explicit escape so a
// literal percent or underscore never becomes a wildcard.
func runSearchWhere(q RunSearchQuery) (string, []any) {
	parts, args := []string{"1=1"}, []any{}
	add := func(sql string, values ...any) { parts = append(parts, sql); args = append(args, values...) }
	if q.Project != "" {
		add("r.project = ?", q.Project)
	}
	if q.Environment != "" {
		add("r.environment = ?", q.Environment)
	}
	if q.Branch != "" {
		add("r.branch = ?", q.Branch)
	}
	if q.From > 0 {
		add("r.started_at >= ?", q.From)
	}
	if q.To > 0 {
		add("r.started_at < ?", q.To)
	}
	if q.Incomplete != nil {
		add("r.incomplete = ?", *q.Incomplete)
	}
	if len(q.Status) > 0 {
		marks := strings.TrimRight(strings.Repeat("?,", len(q.Status)), ",")
		values := make([]any, len(q.Status))
		for i := range q.Status {
			values[i] = q.Status[i]
		}
		add("r.status IN ("+marks+")", values...)
	}
	if q.Tag != "" {
		add("EXISTS (SELECT 1 FROM tests tagt WHERE tagt.run_id=r.id AND (tagt.category=? OR ',' || REPLACE(tagt.category, ' ', '') || ',' LIKE ? ESCAPE '\\'))", q.Tag, "%,"+likeEscape(q.Tag)+",%")
	}
	if q.Owner != "" {
		add("EXISTS (SELECT 1 FROM tests ownert JOIN quarantine oq ON oq.project=r.project AND oq.test_key=ownert.test_key WHERE ownert.run_id=r.id AND oq.owner=?)", q.Owner)
	}
	if q.Flaky != nil {
		if *q.Flaky {
			add("EXISTS (SELECT 1 FROM tests ft WHERE ft.run_id=r.id AND ft.attempts > 1)")
		} else {
			add("NOT EXISTS (SELECT 1 FROM tests ft WHERE ft.run_id=r.id AND ft.attempts > 1)")
		}
	}
	if q.Q != "" {
		needle := "%" + likeEscape(strings.ToLower(q.Q)) + "%"
		add(`(LOWER(r.name) LIKE ? ESCAPE '\' OR LOWER(r.project) LIKE ? ESCAPE '\' OR LOWER(r.branch) LIKE ? ESCAPE '\' OR LOWER(r.commit_sha) LIKE ? ESCAPE '\' OR LOWER(r.environment) LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM tests qt WHERE qt.run_id=r.id AND (LOWER(qt.name) LIKE ? ESCAPE '\' OR LOWER(qt.test_key) LIKE ? ESCAPE '\')))`, needle, needle, needle, needle, needle, needle, needle)
	}
	return strings.Join(parts, " AND "), args
}

func likeEscape(s string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(s)
}

func searchOrder(sort string) (value, direction string) {
	switch sort {
	case "oldest":
		return "r.started_at", "ASC"
	case "duration":
		return "CASE WHEN r.ended_at IS NULL THEN 0 ELSE r.ended_at-r.started_at END", "DESC"
	case "failures":
		return "r.failed", "DESC"
	default:
		return "r.started_at", "DESC"
	}
}

func scanSearchItem(sc interface{ Scan(...any) error }) (RunSearchItem, error) {
	var x RunSearchItem
	err := sc.Scan(&x.ID, &x.Name, &x.Project, &x.Environment, &x.Branch, &x.Commit, &x.Status, &x.Incomplete, &x.StartedAt, &x.Duration, &x.Total, &x.Passed, &x.Failed, &x.Skipped, &x.Warning, &x.Quarantined, &x.FlakyCount)
	return x, err
}

// SearchRuns uses a keyset cursor. Its cursor includes the selected order value plus started_at
// and id, preventing duplicate or skipped rows when a new run is inserted between requests.
func (s *Store) SearchRuns(q RunSearchQuery) ([]RunSearchItem, string, int, error) {
	where, args := runSearchWhere(q)
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM runs r WHERE "+where, args...).Scan(&total); err != nil {
		return nil, "", 0, err
	}
	c, err := decodeRunSearchCursor(q.Cursor)
	if err != nil {
		return nil, "", 0, err
	}
	value, dir := searchOrder(q.Sort)
	if q.Cursor != "" {
		cmp := "<"
		if dir == "ASC" {
			cmp = ">"
		}
		where += fmt.Sprintf(" AND (%s %s ? OR (%s = ? AND (r.started_at %s ? OR (r.started_at = ? AND r.id %s ?))))", value, cmp, value, cmp, cmp)
		args = append(args, c.Value, c.Value, c.StartedAt, c.StartedAt, c.ID)
	}
	// Quarantined counts are cheap and persistent; retries are the portable flakiness signal kept
	// in the runs table's related tests, without materializing any test rows in the API response.
	sqlq := `SELECT r.id,r.name,r.project,r.environment,r.branch,r.commit_sha,r.status,r.incomplete,r.started_at,
		CASE WHEN r.ended_at IS NULL THEN 0 ELSE r.ended_at-r.started_at END,
		r.total,r.passed,r.failed,r.skipped,r.warning,
		(SELECT COUNT(*) FROM tests qt JOIN quarantine qq ON qq.project=r.project AND qq.test_key=qt.test_key WHERE qt.run_id=r.id),
		(SELECT COUNT(*) FROM tests ft WHERE ft.run_id=r.id AND ft.attempts>1)
		FROM runs r WHERE ` + where + fmt.Sprintf(" ORDER BY %s %s, r.started_at %s, r.id %s LIMIT ?", value, dir, dir, dir)
	args = append(args, q.Limit+1)
	rows, err := s.db.Query(sqlq, args...)
	if err != nil {
		return nil, "", 0, err
	}
	defer rows.Close()
	items := []RunSearchItem{}
	for rows.Next() {
		x, err := scanSearchItem(rows)
		if err != nil {
			return nil, "", 0, err
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, err
	}
	next := ""
	if len(items) > q.Limit {
		last := items[q.Limit-1]
		v := last.StartedAt
		if q.Sort == "duration" {
			v = last.Duration
		}
		if q.Sort == "failures" {
			v = int64(last.Failed)
		}
		next = encodeRunSearchCursor(runSearchCursor{Value: v, StartedAt: last.StartedAt, ID: last.ID})
		items = items[:q.Limit]
	}
	return items, next, total, nil
}

type RunFacet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}
type RunFacets struct {
	Project, Environment, Branch, Tag []RunFacet `json:"-"`
}

// RunFacets returns the most frequent values for the explorer menus. The tag facet is built from
// the existing comma-separated test categories, preserving the historical schema.
func (s *Store) RunFacets(q RunSearchQuery) (map[string][]RunFacet, error) {
	where, args := runSearchWhere(q)
	result := map[string][]RunFacet{"project": {}, "environment": {}, "branch": {}, "tag": {}}
	for _, field := range []string{"project", "environment", "branch"} {
		rows, err := s.db.Query("SELECT r."+field+", COUNT(*) FROM runs r WHERE "+where+" AND r."+field+"<>'' GROUP BY r."+field+" ORDER BY COUNT(*) DESC, r."+field+" LIMIT 20", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var f RunFacet
			if err := rows.Scan(&f.Value, &f.Count); err != nil {
				rows.Close()
				return nil, err
			}
			result[field] = append(result[field], f)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	rows, err := s.db.Query(`SELECT t.category,COUNT(DISTINCT r.id) FROM runs r JOIN tests t ON t.run_id=r.id WHERE `+where+` AND t.category<>'' GROUP BY t.category ORDER BY COUNT(DISTINCT r.id) DESC,t.category LIMIT 20`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f RunFacet
		if err := rows.Scan(&f.Value, &f.Count); err != nil {
			return nil, err
		}
		result["tag"] = append(result["tag"], f)
	}
	return result, rows.Err()
}
