package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Ejecuciones abiertas de una versión anterior (sin last_activity): su actividad sale de la
// evidencia que ya tienen, para no cerrarlas por error al actualizar.
func TestStaleMigrationFillsActivityOfOpenRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(v1Schema); err != nil {
		t.Fatal(err)
	}
	// la ejecución 3 sigue abierta: empezó en 1000, su último paso llegó en 7000
	if _, err := raw.Exec(`INSERT INTO runs(id, name, status, started_at) VALUES (3, 'abierta', 'RUNNING', 1000);
		INSERT INTO tests(id, run_id, name, status, started_at, ended_at) VALUES (9, 3, 'x', 'PASS', 2000, 5000);
		INSERT INTO logs(test_id, status, message, timestamp) VALUES (9, 'INFO', 'paso', 7000);`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var open, done int64
	s.db.QueryRow(`SELECT last_activity FROM runs WHERE id = 3`).Scan(&open)
	s.db.QueryRow(`SELECT last_activity FROM runs WHERE id = 1`).Scan(&done)
	if open != 7000 || done != 0 {
		t.Fatalf("activity: open=%d (want 7000) finished=%d (untouched)", open, done)
	}
	if ids, _ := s.StaleRuns(6000); len(ids) != 0 {
		t.Fatal("not stale before its last evidence")
	}
	if ids, _ := s.StaleRuns(8000); len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("stale after it: %v", ids)
	}
}
