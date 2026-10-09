package db

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchRunsTwentyThousandRowsUnder150ms(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing test: not meaningful with -race or -short")
	}
	s, err := Open(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`PRAGMA synchronous=OFF`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO runs(name,environment,status,started_at,project,branch,commit_sha,total,passed,failed) VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	testStmt, err := tx.Prepare(`INSERT INTO tests(run_id,name,category,started_at,test_key) VALUES(?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	tagStmt, err := tx.Prepare(`INSERT INTO run_tags(run_id,tag) VALUES(?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20000; i++ {
		res, err := stmt.Exec(fmt.Sprintf("nightly-%05d", i), "qa", "PASS", int64(i), fmt.Sprintf("project-%d", i%10), "main", "abcd", 30, 30, 0)
		if err != nil {
			t.Fatal(err)
		}
		runID, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 30; j++ {
			name := fmt.Sprintf("case-%05d-%02d", i, j)
			if i == 15000 && j == 7 {
				name = "needle-match-15000"
			}
			if _, err := testStmt.Exec(runID, name, "smoke", int64(i), name); err != nil {
				t.Fatal(err)
			}
		}
		for _, tag := range []string{"smoke", fmt.Sprintf("tag-%d", i%10)} {
			if _, err := tagStmt.Exec(runID, tag); err != nil {
				t.Fatal(err)
			}
		}
	}
	stmt.Close()
	testStmt.Close()
	tagStmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	measure := func(name string, fn func() error) {
		started := time.Now()
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(started)
		if elapsed > 150*time.Millisecond {
			t.Fatalf("%s took %s (want <150ms)", name, elapsed)
		}
		t.Logf("%s: %s", name, elapsed)
	}
	measure("q match", func() error {
		items, _, total, err := s.SearchRuns(RunSearchQuery{Q: "needle-match-15000", Limit: 50, Sort: "recent"})
		if err == nil && (total != 1 || len(items) != 1) {
			return fmt.Errorf("unexpected q match: total=%d items=%d", total, len(items))
		}
		return err
	})
	measure("q no match", func() error {
		items, _, total, err := s.SearchRuns(RunSearchQuery{Q: "definitely-not-present", Limit: 50, Sort: "recent"})
		if err == nil && (total != 0 || len(items) != 0) {
			return fmt.Errorf("unexpected q miss: total=%d items=%d", total, len(items))
		}
		return err
	})
	measure("tag", func() error {
		items, _, total, err := s.SearchRuns(RunSearchQuery{Tag: "tag-3", Limit: 50, Sort: "recent"})
		if err == nil && (total != 2000 || len(items) != 50) {
			return fmt.Errorf("unexpected tag: total=%d items=%d", total, len(items))
		}
		return err
	})
	measure("facets", func() error { _, err := s.RunFacets(RunSearchQuery{Limit: 20, Sort: "recent"}); return err })
}
