package db

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// seedSearch writes runs with 30 tests each through the normal schema (triggers included).
func seedSearch(t *testing.T, s *Store, runs int) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	rs, _ := tx.Prepare(`INSERT INTO runs(id,name,environment,status,started_at,project,branch,commit_sha) VALUES(?,?,?,?,?,?,?,?)`)
	ts, _ := tx.Prepare(`INSERT INTO tests(run_id,name,status,started_at,test_key,category) VALUES(?,?,?,?,?,?)`)
	for i := 1; i <= runs; i++ {
		rs.Exec(i, fmt.Sprintf("nightly-%05d", i), "qa", "PASS", int64(i), "shop", "main", "abcd")
		for j := 0; j < 30; j++ {
			ts.Exec(i, fmt.Sprintf("test_case_%d", j), "PASS", int64(i), fmt.Sprintf("tests/test_%d.py::case_%d", j, j), "smoke, checkout")
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Deleting a run (retention, the UI) removes its rows from the search index without scanning it,
// and the index never returns a deleted run.
func TestRunSearchIndexFollowsDeletes(t *testing.T) {
	s := openTestStore(t)
	seedSearch(t, s, 3000)
	start := time.Now()
	if _, err := s.db.Exec(`DELETE FROM runs WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("deleting one run took %v: the index is scanned", took)
	}
	var left int
	s.db.QueryRow(`SELECT COUNT(*) FROM run_search_text WHERE CAST(run_id AS INTEGER) = 7`).Scan(&left)
	items, _, _, err := s.SearchRuns(RunSearchQuery{Q: "nightly-00007", Limit: 10, Sort: "recent"})
	if err != nil || left != 0 || len(items) != 0 {
		t.Fatalf("deleted run still indexed: rows=%d items=%v err=%v", left, items, err)
	}
}

// An existing database gets its search index filled once, on the first start; the next starts
// do not repeat it (a start with history must stay fast).
func TestRunSearchIndexIsFilledOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedSearch(t, s, 3000)
	// como una base de una versión anterior: sin índice ni marca
	for _, q := range []string{`DELETE FROM run_search_init`, `DELETE FROM run_search_text`, `DELETE FROM run_tags`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	items, _, total, err := s.SearchRuns(RunSearchQuery{Q: "case_29", Tag: "checkout", Limit: 5, Sort: "recent"})
	if err != nil || total != 3000 || len(items) != 5 {
		t.Fatalf("the first start fills text and tags: total=%d items=%d err=%v", total, len(items), err)
	}
	s.Close()
	start := time.Now()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a later start took %v: the index is filled again", took)
	}
}
