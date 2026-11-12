package db

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// En 972a20d la medición de las páginas viejas leía cada página entera (length(CAST(… AS BLOB))), volvía a
// recorrer la tabla en cada lote (NOT EXISTS … LIMIT) y lo hacía todo en la conexión que escribe: ~7 min con
// el servidor entero lento después de actualizar. Ahora mide por clave, con octet_length, leyendo por las
// conexiones de lectura y escribiendo cada lote en una escritura corta.

// oldPages stores n pages like a version before dom_sizes (TEXT in clear, not measured) in runs of
// perRun tests with 1 failure page and steps pages each.
func oldPages(t *testing.T, s *Store, runs, steps, size int) {
	t.Helper()
	page := `{"url":"https://app/x","nodes":[{"i":0,"p":-1,"tag":"body","text":"` + strings.Repeat("x", size) + `"}]}`
	now := NowMs()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for r := 0; r < runs; r++ {
		res, _ := tx.Exec(`INSERT INTO runs(name, environment, status, started_at, ended_at) VALUES(?, 'qa', 'PASS', ?, ?)`, fmt.Sprint("run ", r), now, now)
		run, _ := res.LastInsertId()
		res, _ = tx.Exec(`INSERT INTO tests(run_id, name, status, started_at, ended_at) VALUES(?, 'test', 'FAIL', ?, ?)`, run, now, now)
		tid, _ := res.LastInsertId()
		tx.Exec(`INSERT INTO test_dom(test_id, snapshot, created_at) VALUES(?,?,?)`, tid, page, now)
		for seq := 1; seq <= steps; seq++ {
			tx.Exec(`INSERT INTO test_dom_steps(test_id, seq, label, at, snapshot, created_at, kind) VALUES(?,?,'',0,?,?,'step')`, tid, seq, page, now)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHasOctetLength(t *testing.T) {
	s := openTestStore(t)
	var v string
	if err := s.db.QueryRow(`SELECT sqlite_version()`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	var major, minor int
	fmt.Sscanf(v, "%d.%d", &major, &minor)
	if major < 3 || major == 3 && minor < 43 {
		t.Fatalf("octet_length needs SQLite 3.43+, got %s", v)
	}
}

// Las consultas de la medición van por clave primaria (SEARCH), sin recorrer la tabla ni armar tablas
// temporales: ningún lote vuelve a mirar lo ya medido.
func TestMeasuringGoesByPrimaryKey(t *testing.T) {
	s := openTestStore(t)
	for _, q := range []string{
		`SELECT test_id, octet_length(snapshot) FROM test_dom WHERE test_id > 0 ORDER BY test_id LIMIT 200`,
		`SELECT test_id, seq, octet_length(snapshot) FROM test_dom_steps WHERE (test_id, seq) > (0, 0) ORDER BY test_id, seq LIMIT 200`,
	} {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN " + q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			rows.Scan(&id, &parent, &notused, &detail)
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, " | ")
		if !strings.HasPrefix(joined, "SEARCH") || strings.Contains(joined, "TEMP B-TREE") || strings.Contains(joined, "SCAN") {
			t.Fatalf("not by key: %s", joined)
		}
	}
}

// Medir no lee el contenido: 300 páginas de 400 KB se miden casi tan rápido como 300 de 4 KB (leerlas
// enteras, como antes, eran 120 MB).
func TestMeasuringDoesNotReadThePages(t *testing.T) {
	timeIt := func(size int) time.Duration {
		s := openTestStore(t)
		oldPages(t, s, 30, 9, size)
		start := time.Now()
		n, err := s.MeasureAllDOMSizes(200, 0, nil)
		if err != nil || n != 300 {
			t.Fatal(n, err)
		}
		return time.Since(start)
	}
	small, big := timeIt(4_000), timeIt(400_000)
	t.Logf("300 pages: 4 KB → %s, 400 KB → %s", small, big)
	if big > 3*small+150*time.Millisecond || big > 3*time.Second {
		t.Fatalf("measuring reads the pages: 4 KB → %s, 400 KB → %s", small, big)
	}
	// y los tamaños son los de lo guardado
	s := openTestStore(t)
	oldPages(t, s, 2, 1, 1000)
	if _, err := s.MeasureAllDOMSizes(200, 0, nil); err != nil {
		t.Fatal(err)
	}
	var sum, want int64
	s.db.QueryRow(`SELECT SUM(bytes) FROM dom_sizes`).Scan(&sum)
	s.db.QueryRow(`SELECT SUM(length(CAST(snapshot AS BLOB))) FROM (SELECT snapshot FROM test_dom UNION ALL SELECT snapshot FROM test_dom_steps)`).Scan(&want)
	if sum != want || sum == 0 {
		t.Fatalf("sizes %d, stored %d", sum, want)
	}
}

// No vuelve a recorrer: con 20 000 páginas, los últimos lotes tardan lo mismo que los primeros.
func TestMeasuringDoesNotRescan(t *testing.T) {
	s := openTestStore(t)
	oldPages(t, s, 1000, 19, 50)
	var cur MeasureCursor
	var took []time.Duration
	for !cur.Done() {
		t0 := time.Now()
		next, _, err := s.MeasureDOMSizes(cur, 100)
		if err != nil {
			t.Fatal(err)
		}
		took = append(took, time.Since(t0))
		cur = next
	}
	if len(took) < 190 {
		t.Fatalf("%d batches", len(took))
	}
	avg := func(d []time.Duration) time.Duration {
		var sum time.Duration
		for _, x := range d {
			sum += x
		}
		return sum / time.Duration(len(d))
	}
	first, last := avg(took[1:21]), avg(took[len(took)-21:len(took)-1])
	t.Logf("batches: %d, first %s, last %s", len(took), first, last)
	if last > 2*first+2*time.Millisecond {
		t.Fatalf("the last batches take longer (it rescans): first %s, last %s", first, last)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM dom_sizes`).Scan(&n)
	if n != 20_000 {
		t.Fatalf("measured %d", n)
	}
}

// No bloquea: mientras mide 2 000 páginas de 400 KB, una escritura de la ingesta (un paso) responde en
// menos de 200 ms (p99).
func TestMeasuringDoesNotHoldTheWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: 800 MB of pages")
	}
	s := openTestStore(t)
	oldPages(t, s, 200, 9, 400_000)
	run, _ := s.CreateRun("ingesta", "qa")
	test, _ := s.CreateTest(run, "t", "", "")
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		if _, err := s.MeasureAllDOMSizes(200, 200*time.Millisecond, nil); err != nil {
			t.Error(err)
		}
	}()
	var lat []time.Duration
	for {
		select {
		case <-done:
			wg.Wait()
			if len(lat) < 5 {
				t.Fatalf("only %d writes during the measuring", len(lat))
			}
			sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
			p99 := lat[len(lat)*99/100]
			t.Logf("%d writes during the measuring, p99 %s", len(lat), p99)
			if p99 > 200*time.Millisecond {
				t.Fatalf("an ingestion write waited %s (p99)", p99)
			}
			return
		default:
		}
		t0 := time.Now()
		if _, err := s.AddLog(test, "INFO", "paso", 0, ""); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
		time.Sleep(10 * time.Millisecond)
	}
}

// TRACEREPORTS_REMEASURE_PAGES: olvidar los tamaños y medir de nuevo deja los mismos totales.
func TestRemeasuringGivesTheSameTotals(t *testing.T) {
	s := openTestStore(t)
	oldPages(t, s, 20, 4, 2000)
	if _, err := s.MeasureAllDOMSizes(30, 0, nil); err != nil {
		t.Fatal(err)
	}
	before, _, pages, _, _ := s.DOMSizes(5)
	if err := s.ForgetDOMSizes(); err != nil {
		t.Fatal(err)
	}
	if n, err := s.MeasureAllDOMSizes(30, 0, nil); err != nil || n != 100 {
		t.Fatal(n, err)
	}
	after, _, pages2, _, _ := s.DOMSizes(5)
	if before != after || pages != pages2 || pages != 100 {
		t.Fatalf("before %d/%d, after %d/%d", before, pages, after, pages2)
	}
}
