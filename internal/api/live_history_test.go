package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

type historyPage struct {
	Total      int               `json:"total"`
	Next       int64             `json:"next"`
	Executions []liveHistoryItem `json:"executions"`
}

func getHistory(t *testing.T, srv *Server, query string) historyPage {
	t.Helper()
	rec := do(t, srv, http.MethodGet, "/api/v1/live/history"+query, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("history%s: %d %s", query, rec.Code, rec.Body)
	}
	var p historyPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// historyFixture: runs at a given age, each with its tests (one per status).
func historyFixture(t *testing.T, srv *Server) func(name, exe string, ago int64, statuses ...string) {
	now := time.Now().UnixMilli()
	return func(name, exe string, ago int64, statuses ...string) {
		t.Helper()
		id, err := srv.Store.CreateRunWithMeta(name, "qa", db.RunMeta{Project: "acme_shop_web", Branch: "main", ExecutionID: exe})
		if err != nil {
			t.Fatal(err)
		}
		for i, st := range statuses {
			tid, _ := srv.Store.CreateTest(id, fmt.Sprintf("%s_t%d", name, i), "", "")
			srv.Store.FinishTest(tid, st, "", "")
		}
		srv.Store.FinishRun(id)
		start := now - ago
		if err := srv.Store.SetRunTimes(id, start, start+60_000); err != nil {
			t.Fatal(err)
		}
	}
}

const dayMs = int64(24 * time.Hour / time.Millisecond)

// The history of Live: past executions newest first, grouped like Live, with their counts; in pages
// with the total, by days, and searched in the database so a team with thousands still finds one.
func TestLiveHistoryPagesAndCounts(t *testing.T) {
	srv, _ := newTestServer(t)
	run := historyFixture(t, srv)
	run("shop/test_checkout.py", "suite-hace-3-dias", 3*dayMs, "PASS", "FAIL", "FAIL")
	run("shop/test_login.py", "suite-hace-3-dias", 3*dayMs-120_000, "PASS")
	run("shop/test_cart.py", "suite-ayer", dayMs, "PASS", "PASS")
	for i := 0; i < 5; i++ {
		run(fmt.Sprintf("shop/test_%d.py", i), fmt.Sprintf("ci-%d", i), int64(i+2)*3_600_000, "PASS")
	}
	run("shop/test_viejo.py", "suite-hace-100-dias", 100*dayMs, "FAIL")

	all := getHistory(t, srv, "?limit=100")
	byKey := map[string]liveHistoryItem{}
	for _, e := range all.Executions {
		byKey[e.Key] = e
	}
	// la de hace 100 días queda fuera de los 90; la del servidor de prueba (sin execution_id) cuenta
	if _, old := byKey["x:suite-hace-100-dias"]; old || all.Total != len(all.Executions) || all.Next != 0 {
		t.Fatalf("total %d next %d, 100 days ago listed: %v", all.Total, all.Next, old)
	}
	three := byKey["x:suite-hace-3-dias"]
	if three.Runs != 2 || three.Total != 4 || three.Done != 4 || three.Counts["PASS"] != 2 || three.Counts["FAIL"] != 2 ||
		three.Running || three.Title != "suite-hace-3-dias" || three.EndedAt == 0 || len(three.Files) != 2 {
		t.Fatalf("hace 3 días = %+v", three)
	}
	for i := 1; i < len(all.Executions); i++ { // la más nueva primero
		if all.Executions[i].StartedAt > all.Executions[i-1].StartedAt {
			t.Fatalf("not newest first at %d: %v", i, all.Executions)
		}
	}

	// de a 2: el total de siempre, sin repetir ni saltar ninguna
	var seen []string
	query := "?limit=2"
	for pages := 0; ; pages++ {
		p := getHistory(t, srv, query)
		if p.Total != all.Total || len(p.Executions) > 2 || pages > 10 {
			t.Fatalf("page %d: total %d, %d items", pages, p.Total, len(p.Executions))
		}
		for _, e := range p.Executions {
			seen = append(seen, e.Key)
		}
		if p.Next == 0 {
			break
		}
		query = fmt.Sprintf("?limit=2&before=%d", p.Next)
	}
	if len(seen) != all.Total {
		t.Fatalf("paged %d of %d: %v", len(seen), all.Total, seen)
	}
	for i, e := range all.Executions {
		if seen[i] != e.Key {
			t.Fatalf("paged order %v, want %v", seen, all.Executions)
		}
	}

	// por días: from/to en ms (la interfaz manda "ayer" o "10-11" así)
	now := time.Now().UnixMilli()
	p := getHistory(t, srv, fmt.Sprintf("?from=%d&to=%d", now-dayMs-3_600_000, now-dayMs+3_600_000))
	if p.Total != 1 || p.Executions[0].Key != "x:suite-ayer" {
		t.Fatalf("yesterday: %+v", p)
	}
}

// Searching finds an execution by any of its files, whole (with all its runs and counts), case-insensitive
// and literally (an "_" is not a wildcard).
func TestLiveHistorySearch(t *testing.T) {
	srv, _ := newTestServer(t)
	run := historyFixture(t, srv)
	run("shop/test_checkout.py", "nightly-42", 2*dayMs, "PASS", "FAIL")
	run("shop/test_login.py", "nightly-42", 2*dayMs-60_000, "PASS", "PASS")
	run("shop/testXcheckout.py", "otra", dayMs, "PASS")

	p := getHistory(t, srv, "?q=CHECKOUT")
	if p.Total != 2 {
		t.Fatalf("checkout: %+v", p)
	}
	got := map[string]liveHistoryItem{}
	for _, e := range p.Executions {
		got[e.Key] = e
	}
	// nightly-42 encontrada por un archivo: con sus dos runs y todos sus tests
	if n := got["x:nightly-42"]; n.Runs != 2 || n.Total != 4 || n.Counts["FAIL"] != 1 || n.Counts["PASS"] != 3 {
		t.Fatalf("nightly-42 = %+v", n)
	}
	if p := getHistory(t, srv, "?q=test_check"); p.Total != 1 || p.Executions[0].Key != "x:nightly-42" {
		t.Fatalf(`"_" taken as a wildcard: %+v`, p)
	}
	if p := getHistory(t, srv, "?q=nightly-4"); p.Total != 1 {
		t.Fatalf("by execution id: %+v", p)
	}
	if p := getHistory(t, srv, "?q=zzz-no-existe"); p.Total != 0 || len(p.Executions) != 0 || p.Next != 0 {
		t.Fatalf("nothing: %+v", p)
	}
}
