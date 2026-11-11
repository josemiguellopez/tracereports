package db

import (
	"strings"
	"testing"
)

// The history of Live reads by date and completes executions by execution_id: both through an index,
// so a team with tens of thousands of runs gets each page without scanning the whole table.
func TestHistoryQueriesUseIndexes(t *testing.T) {
	s := openTestStore(t)
	plan := func(q string, args ...any) string {
		t.Helper()
		rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			rows.Scan(&id, &parent, &unused, &detail)
			out += detail + "\n"
		}
		return out
	}
	if p := plan(`SELECT `+liveRunCols+` FROM runs r WHERE r.execution_id != '' AND r.execution_id IN (?, ?)`, "a", "b"); !strings.Contains(p, "idx_runs_execution") {
		t.Fatalf("completing an execution scans runs:\n%s", p)
	}
	if p := plan(`SELECT `+liveRunCols+` FROM runs r WHERE r.started_at >= ? AND r.started_at < ? ORDER BY r.started_at DESC, r.id DESC LIMIT 10`, 1, 2); !strings.Contains(p, "idx_runs_search_started") {
		t.Fatalf("a page of the history scans runs:\n%s", p)
	}
}

// HistoryRuns and RunCountsOf: the runs of a period, oldest first, and their tests counted by result.
func TestHistoryRunsAndCounts(t *testing.T) {
	s := openTestStore(t)
	a, _ := s.CreateRunWithMeta("shop/test_a.py", "qa", RunMeta{ExecutionID: "e1"})
	b, _ := s.CreateRunWithMeta("shop/test_b.py", "qa", RunMeta{ExecutionID: "e1"})
	for i, st := range []string{"PASS", "FAIL", "INTERRUPTED", "RUNNING"} {
		id, _ := s.CreateTest(a, "t"+string(rune('0'+i)), "", "")
		if st != "RUNNING" {
			s.FinishTest(id, st, "", "")
		}
	}
	s.SetRunTimes(a, 1000, 2000)
	s.SetRunTimes(b, 3000, 4000)
	runs, err := s.HistoryRuns(0, 10_000, "", 100)
	if err != nil || len(runs) != 2 || runs[0].ID != a || runs[1].ID != b {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	if runs, _ := s.HistoryRuns(0, 10_000, "TEST_B", 100); len(runs) != 1 || runs[0].ID != b {
		t.Fatalf("search: %v", runs)
	}
	if runs, _ := s.HistoryRuns(0, 10_000, "", 1); len(runs) != 1 || runs[0].ID != b {
		t.Fatalf("max keeps the newest: %v", runs)
	}
	all, err := s.RunsOfExecutions([]string{"e1"})
	if err != nil || len(all) != 2 {
		t.Fatalf("of e1: %v %v", all, err)
	}
	c, err := s.RunCountsOf([]int64{a, b})
	if err != nil || c[a] != (RunCounts{Total: 4, Passed: 1, Failed: 2, Running: 1}) || c[b] != (RunCounts{}) {
		t.Fatalf("counts = %+v, %v", c, err)
	}
}
