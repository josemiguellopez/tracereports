package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func budgetRows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ai_budget`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Al actualizar, los análisis ya gastados cuentan una vez (como antes); después nunca se vuelve a
// rellenar, así que un Re-analizar manual no termina contado.
func TestAIBudgetLedgerMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(v1Schema); err != nil {
		t.Fatal(err)
	}
	// la ejecución 1 tiene un test (id 1) con diagnóstico hecho; agregamos uno SKIPPED
	raw.Exec(`INSERT INTO tests(id, run_id, name, status, started_at) VALUES (5, 1, 'b', 'FAIL', 1000)`)
	raw.Exec(`INSERT INTO ai_triage(test_id, state, updated_at) VALUES (1, 'DONE', 1), (5, 'SKIPPED', 1)`)
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := budgetRows(t, s); n != 1 {
		t.Fatalf("the analysis already spent counts once (SKIPPED does not): %d", n)
	}
	if out, _, _ := s.ReserveTriage(5, 1); out != TriageOverBudget {
		t.Fatalf("budget 1 already used by the old diagnosis: %s", out)
	}
	s.SetTriagePending(5) // Re-analizar a mano
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := budgetRows(t, s); n != 1 {
		t.Fatalf("a restart never backfills again: %d", n)
	}
}
