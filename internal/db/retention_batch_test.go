package db

import (
	"sort"
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

// La retención borra con secure_delete (reescribe con ceros cada página): con páginas pesadas, una tanda
// grande tenía tomada la conexión que escribe. En tandas chicas con pausa, una escritura de la ingesta
// mientras se limpia responde en menos de 300 ms (p99).
func TestPurgeOfHeavyRunsLetsIngestionIn(t *testing.T) {
	s := openTestStore(t)
	oldPages(t, s, 80, 4, 600_000) // 80 ejecuciones con 5 páginas de 600 KB (240 MB)
	s.db.Exec(`UPDATE runs SET started_at = 1000, status = 'PASS'`)
	run, _ := s.CreateRun("ingesta", "qa")
	test, _ := s.CreateTest(run, "t", "", "")
	done := make(chan error, 1)
	go func() {
		_, _, err := s.PurgeRunsBefore(time.Now().Add(-time.Hour).UnixMilli())
		done <- err
	}()
	var lat []time.Duration
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if len(lat) < 3 {
				t.Fatalf("only %d writes during the purge", len(lat))
			}
			sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
			p99 := lat[len(lat)*99/100]
			t.Logf("%d writes during the purge, p99 %s", len(lat), p99)
			if p99 > slowdown*300*time.Millisecond {
				t.Fatalf("an ingestion write waited %s (p99)", p99)
			}
			var left int
			s.db.QueryRow(`SELECT COUNT(*) FROM test_dom_steps`).Scan(&left)
			if left != 0 {
				t.Fatalf("%d pages left", left)
			}
			return
		default:
		}
		t0 := time.Now()
		if _, err := s.AddLog(test, "INFO", "paso", 0, ""); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
		time.Sleep(5 * time.Millisecond)
	}
}
