package db

import (
	"strings"
	"testing"
	"time"
)

// Borrar una ejecución borra sus claves de idempotencia en cascada: con el índice, sin recorrer la tabla.
func TestIdempotencyIsIndexedByRun(t *testing.T) {
	s := openTestStore(t)
	var plan, detail string
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT key FROM idempotency WHERE run_id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, parent, unused int
		rows.Scan(&id, &parent, &unused, &detail)
		plan += detail + "\n"
	}
	rows.Close()
	if !strings.Contains(plan, "idx_idempotency_run") {
		t.Fatalf("deleting a run scans the idempotency table:\n%s", plan)
	}
}

// La limpieza de muchas ejecuciones va de a tandas: entre una y otra, los clientes pueden escribir (no
// esperan a que termine todo), y al final se borró todo con sus archivos.
func TestPurgeGoesInBatchesAndLetsWritersIn(t *testing.T) {
	s := openTestStore(t)
	const n = 3*purgeBatch + 5
	for i := range n {
		id, _ := s.CreateRun("viejo", "")
		tid, _ := s.CreateTest(id, "t", "", "")
		s.AddLog(tid, "INFO", "shot", 0, "/screenshots/s"+string(rune('a'+i%26))+".png")
		s.FinishTest(tid, "PASS", "", "")
		s.FinishRun(id)
	}
	s.db.Exec(`UPDATE runs SET started_at = 1000`)
	batches := 0
	wrote := make(chan time.Time, 1)
	purgeHook = func() {
		batches++
		if batches == 1 {
			go func() { s.CreateRun("un cliente escribe", ""); wrote <- time.Now() }()
		}
		time.Sleep(40 * time.Millisecond)
	}
	defer func() { purgeHook = nil }()
	deleted, files, err := s.PurgeRunsBefore(time.Now().Add(-time.Hour).UnixMilli())
	end := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	if deleted != n || len(files) != n {
		t.Fatalf("deleted %d runs and %d files, want %d", deleted, len(files), n)
	}
	if batches != 4 {
		t.Fatalf("%d transactions, want 4 batches", batches)
	}
	if at := <-wrote; !at.Before(end) {
		t.Fatal("a client write waited for the whole cleanup")
	}
}
