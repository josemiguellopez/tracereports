package db

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchRunsTwentyThousandRowsUnder150ms(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO runs(name,environment,status,started_at,project,branch,commit_sha,total,passed,failed) VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20000; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("nightly-%05d", i), "qa", "PASS", int64(i), fmt.Sprintf("project-%d", i%10), "main", "abcd", 10, 10, 0); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	items, _, total, err := s.SearchRuns(RunSearchQuery{Project: "project-3", Limit: 50, Sort: "recent"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2000 || len(items) != 50 {
		t.Fatalf("unexpected result: total=%d items=%d", total, len(items))
	}
	if _, err := s.RunFacets(RunSearchQuery{Limit: 20, Sort: "recent"}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("search and facets took %s (want <150ms)", elapsed)
	}
}
