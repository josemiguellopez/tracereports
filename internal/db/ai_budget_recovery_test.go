package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// oldDB is a database of a version without the ledger: run 1 has test 1 with a DONE diagnosis
// and test 5 SKIPPED. extra runs on it before closing (the state an earlier version may have left).
func oldDB(t *testing.T, extra string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, q := range []string{v1Schema,
		`INSERT INTO tests(id, run_id, name, status, started_at) VALUES (5, 1, 'b', 'FAIL', 1000)`,
		`INSERT INTO ai_triage(test_id, state, updated_at) VALUES (5, 'SKIPPED', 1)`, extra} { // v1Schema ya trae el DONE del test 1
		if q == "" {
			continue
		}
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func openOK(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Una interrupción en cualquier punto de la migración (después de crear la tabla, después de
// copiar el consumo) no deja una migración a medias: el arranque siguiente la hace entera.
func TestAIBudgetMigrationSurvivesAnInterruption(t *testing.T) {
	for _, stage := range []string{"created", "backfilled"} {
		path := oldDB(t, "")
		boom := errors.New("process killed")
		aiBudgetHook = func(s string) error {
			if s == stage {
				return boom
			}
			return nil
		}
		_, err := Open(path)
		aiBudgetHook = func(string) error { return nil }
		if !errors.Is(err, boom) {
			t.Fatalf("%s: the interrupted start fails: %v", stage, err)
		}
		s := openOK(t, path)
		if n := budgetRows(t, s); n != 1 {
			t.Fatalf("%s: after the interruption the previous consumption is kept: %d", stage, n)
		}
		if out, _, _ := s.ReserveTriage(5, 1); out != TriageOverBudget {
			t.Fatalf("%s: budget 1 already used by the old diagnosis: %s", stage, out)
		}
		if pending, _, _ := s.AIBudgetMigration(); pending {
			t.Fatalf("%s: migration finished", stage)
		}
	}
}

// Abrir varias veces no copia de nuevo: un Re-analizar manual posterior nunca queda contado.
func TestAIBudgetMigrationRunsOnce(t *testing.T) {
	path := oldDB(t, "")
	s := openOK(t, path)
	s.SetTriagePending(5) // Re-analizar a mano (no pasa por el registro)
	s.Close()
	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if n := budgetRows(t, s); n != 1 {
			t.Fatalf("reopen %d: %d rows", i, n)
		}
		s.Close()
	}
}

// Estado que dejaba la versión anterior (no atómica) de esta migración: la tabla existe sin la
// marca. Sin diagnósticos fuera del registro no hay nada que decidir.
func TestUnmarkedLedgerWithNothingMissingIsVerified(t *testing.T) {
	path := oldDB(t, aiBudgetSchema+`INSERT INTO ai_budget VALUES (1, 1, 0, 1)`)
	s := openOK(t, path)
	if pending, _, _ := s.AIBudgetMigration(); pending {
		t.Fatal("nothing missing: settled on its own")
	}
	if n := budgetRows(t, s); n != 1 {
		t.Fatalf("ledger untouched: %d", n)
	}
}

// Con diagnósticos fuera del registro no se puede saber si faltó la copia o son re-análisis
// manuales posteriores: no se inventa consumo. Se informa y se resuelve de forma explícita.
func TestUnmarkedLedgerWithMissingRowsIsSettledExplicitly(t *testing.T) {
	for _, backfill := range []bool{true, false} {
		path := oldDB(t, aiBudgetSchema)
		s := openOK(t, path)
		pending, missing, err := s.AIBudgetMigration()
		if err != nil || !pending || missing != 1 {
			t.Fatalf("reported: %v %d %v", pending, missing, err)
		}
		if n := budgetRows(t, s); n != 0 {
			t.Fatalf("nothing is guessed on start: %d", n)
		}
		added, err := s.ResolveAIBudgetMigration(backfill)
		want := int64(0)
		if backfill {
			want = 1
		}
		if err != nil || added != want || int64(budgetRows(t, s)) != want {
			t.Fatalf("backfill=%v: added %d, rows %d, %v", backfill, added, budgetRows(t, s), err)
		}
		if pending, _, _ := s.AIBudgetMigration(); pending {
			t.Fatal("settled")
		}
		if again, _ := s.ResolveAIBudgetMigration(true); again != 0 {
			t.Fatal("a second resolution does nothing")
		}
		s.Close()
		s = openOK(t, path)
		if int64(budgetRows(t, s)) != want {
			t.Fatal("the decision survives a restart")
		}
	}
}
