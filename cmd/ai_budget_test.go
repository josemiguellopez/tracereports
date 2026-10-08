package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Un registro sin terminar (versión anterior de la migración) solo se completa con la decisión
// explícita de TRACEREPORTS_AI_BUDGET_RECOVERY.
func TestAIBudgetRecoveryOnStart(t *testing.T) {
	for _, choice := range []string{"", "keep", "backfill"} {
		path := filepath.Join(t.TempDir(), "t.db")
		s, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		run, _ := s.CreateRun("legacy", "")
		id, _ := s.CreateTest(run, "legacy", "", "")
		s.FinishTest(id, "FAIL", "old failure", "")
		s.SetTriagePending(id)
		s.SaveTriage(id, "LOGIC_BUG", "old diagnosis", "x", "", "")
		s.Close()
		raw, _ := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
		// lo que dejaba la versión anterior interrumpida: la tabla vacía y sin marca
		for _, q := range []string{`DROP TABLE ai_budget_init`, `DELETE FROM ai_budget`} {
			if _, err := raw.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		raw.Close()

		t.Setenv("TRACEREPORTS_AI_BUDGET_RECOVERY", choice)
		s, err = db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := settleAIBudget(s); err != nil {
			t.Fatal(err)
		}
		pending, missing, _ := s.AIBudgetMigration()
		_, used, _, _ := s.TriageBudget(id)
		s.Close()
		switch choice {
		case "":
			if !pending || missing != 1 || used != 0 {
				t.Fatalf("undecided: reported, nothing guessed: %v %d %d", pending, missing, used)
			}
		case "keep":
			if pending || used != 0 {
				t.Fatalf("keep: %v %d", pending, used)
			}
		case "backfill":
			if pending || used != 1 {
				t.Fatalf("backfill: %v %d", pending, used)
			}
		}
	}
}
