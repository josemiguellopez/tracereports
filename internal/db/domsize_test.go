package db

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// En 163542e, medir lo que ocupan las páginas de UN run unía test_dom y test_dom_steps con UNION ALL:
// SQLite copiaba todas las páginas de la base a una tabla temporal en cada pedido (10-11 min por run y
// decenas de GB escritos en un servidor real), y las conexiones de lectura quedaban tomadas: «En vivo» no
// cargaba. Ahora el tamaño se anota al guardar cada página y medir nunca lee las páginas.

func TestMeasuringPagesNeverReadsThem(t *testing.T) {
	s := openTestStore(t)
	for _, q := range []string{runDOMSizeQuery,
		`SELECT t.run_id, r.name, COUNT(*), COALESCE(SUM(z.bytes), 0) FROM dom_sizes z
		JOIN tests t ON t.id = z.test_id JOIN runs r ON r.id = t.run_id GROUP BY t.run_id ORDER BY 4 DESC`} {
		arg := []any{}
		if strings.Contains(q, "?") {
			arg = append(arg, 1)
		}
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, arg...)
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
		if strings.Contains(joined, "test_dom") || strings.Contains(joined, "MATERIALIZE") || strings.Contains(joined, "COMPOUND") {
			t.Fatalf("the size query reads the pages: %s", joined)
		}
	}
}

// Con 2 000 runs y 50 000 páginas guardadas por una versión anterior (sin su tamaño anotado): la medición
// en segundo plano las mide de a lotes cortos, y después medir un run, la base y las ejecuciones de «En
// vivo» toma menos de un segundo.
func TestSizesAndLiveAreFastWithManyPages(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: 50 000 pages")
	}
	s := openTestStore(t)
	page := `{"url":"https://app/x","nodes":[` + strings.Repeat(`{"i":0,"p":-1,"tag":"div","text":"fila"},`, 8) + `{"i":0,"p":-1,"tag":"body"}]}`
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	now := NowMs()
	var lastRun int64
	for r := 0; r < 2000; r++ {
		res, err := tx.Exec(`INSERT INTO runs(name, environment, status, started_at, ended_at) VALUES(?, 'qa', 'PASS', ?, ?)`, fmt.Sprintf("run %d", r), now-int64(r)*60_000, now-int64(r)*60_000+1000)
		if err != nil {
			t.Fatal(err)
		}
		lastRun, _ = res.LastInsertId()
		res, err = tx.Exec(`INSERT INTO tests(run_id, name, status, started_at, ended_at) VALUES(?, 'test', 'FAIL', ?, ?)`, lastRun, now, now)
		if err != nil {
			t.Fatal(err)
		}
		tid, _ := res.LastInsertId()
		tx.Exec(`INSERT INTO test_dom(test_id, snapshot, created_at) VALUES(?,?,?)`, tid, page, now)
		for seq := 1; seq <= 24; seq++ {
			tx.Exec(`INSERT INTO test_dom_steps(test_id, seq, label, at, snapshot, created_at, kind) VALUES(?,?,'',0,?,?,'step')`, tid, seq, page, now)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	measured, err := s.MeasureAllDOMSizes(500, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if measured != 50_000 {
		t.Fatalf("measured %d pages", measured)
	}
	timed := func(name string, f func() error) {
		t.Helper()
		start := time.Now()
		if err := f(); err != nil {
			t.Fatal(name, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s took %s", name, d)
		}
	}
	timed("run size", func() error {
		r, err := s.RunDOMSize(lastRun)
		if err == nil && (r.Pages != 25 || r.Bytes != int64(25*len(page))) {
			err = fmt.Errorf("%+v", r)
		}
		return err
	})
	timed("all sizes", func() error {
		_, _, pages, _, err := s.DOMSizes(10)
		if err == nil && pages != 50_000 {
			err = fmt.Errorf("pages %d", pages)
		}
		return err
	})
	timed("live", func() error { _, err := s.LiveCandidates(now - 15*60_000); return err })
}
