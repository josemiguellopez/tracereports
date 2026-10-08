package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// closedFailingRun: ejecución cerrada con un test que falla (clave fija) y otro que pasa.
func closedFailingRun(t *testing.T, srv *Server, project string) (runID, failID int64) {
	t.Helper()
	runID, _ = srv.Store.CreateRunWithMeta("nightly", "qa", db.RunMeta{Project: project})
	failID, _ = srv.Store.CreateTestWithMeta(runID, "login", "", "", db.TestMeta{Key: "tests/test_login.py::test_login"})
	srv.Store.FinishTest(failID, "FAIL", "boom", "")
	okID, _ := srv.Store.CreateTestWithMeta(runID, "home", "", "", db.TestMeta{Key: "tests/test_home.py::test_home"})
	srv.Store.FinishTest(okID, "PASS", "", "")
	srv.Store.FinishRun(runID)
	return runID, failID
}

type runView struct {
	ID          int64  `json:"id"`
	Status      string `json:"status"`
	Failed      int    `json:"failed"`
	Quarantined int    `json:"quarantined"`
}

// statuses devuelve, por ejecución, lo que muestran el listado y el detalle (y verifica que coincidan).
func statuses(t *testing.T, srv *Server, ids ...int64) map[int64]runView {
	t.Helper()
	var list []runView
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs", "").Body.Bytes(), &list)
	byID := map[int64]runView{}
	for _, r := range list {
		byID[r.ID] = r
	}
	out := map[int64]runView{}
	for _, id := range ids {
		var detail runView
		json.Unmarshal(call(t, srv, "GET", fmt.Sprintf("/api/v1/runs/%d", id), "").Body.Bytes(), &detail)
		if byID[id] != detail {
			t.Fatalf("run %d: list %+v and detail %+v disagree", id, byID[id], detail)
		}
		// el estado y los contadores dicen lo mismo
		if (detail.Failed > detail.Quarantined) != (detail.Status == "FAIL") {
			t.Fatalf("run %d: status %s with failed=%d quarantined=%d", id, detail.Status, detail.Failed, detail.Quarantined)
		}
		out[id] = detail
	}
	return out
}

func TestQuarantineKeepsStatusesCoherentAcrossRuns(t *testing.T) {
	srv, _ := newTestServer(t)
	run1, fail1 := closedFailingRun(t, srv, "shop")
	run2, _ := closedFailingRun(t, srv, "shop")
	other, _ := closedFailingRun(t, srv, "blog") // otro proyecto, misma clave de test
	if s := statuses(t, srv, run1, run2, other); s[run1].Status != "FAIL" || s[run2].Status != "FAIL" {
		t.Fatalf("before: %+v", s)
	}

	// crear la cuarentena desde run1 afecta a todas las ejecuciones del proyecto, no a otro proyecto
	if rec := call(t, srv, "POST", "/api/v1/ui/quarantine", fmt.Sprintf(`{"test_id":%d,"reason":"flaky conocido"}`, fail1)); rec.Code != 200 {
		t.Fatalf("quarantine: %d %s", rec.Code, rec.Body)
	}
	s := statuses(t, srv, run1, run2, other)
	if s[run1].Status != "WARNING" || s[run2].Status != "WARNING" || s[run1].Quarantined != 1 {
		t.Fatalf("quarantined runs: %+v", s)
	}
	if s[other].Status != "FAIL" || s[other].Quarantined != 0 {
		t.Fatalf("another project must not change: %+v", s[other])
	}

	// retirarla desde run1 deja coherentes las dos
	if rec := call(t, srv, "DELETE", fmt.Sprintf("/api/v1/ui/quarantine/%d", fail1), `{}`); rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	s = statuses(t, srv, run1, run2, other)
	if s[run1].Status != "FAIL" || s[run2].Status != "FAIL" || s[run2].Quarantined != 0 {
		t.Fatalf("after removing from run1, run2 must not stay WARNING: %+v", s)
	}
}

func TestQuarantineExpiryAfterTheClose(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := rawDB(t, srv)
	run1, fail1 := closedFailingRun(t, srv, "shop")
	call(t, srv, "POST", "/api/v1/ui/quarantine", fmt.Sprintf(`{"test_id":%d,"reason":"x"}`, fail1))
	// ejecución cerrada durante la cuarentena
	run2, _ := closedFailingRun(t, srv, "shop")
	if s := statuses(t, srv, run1, run2); s[run2].Status != "WARNING" {
		t.Fatalf("closed during the quarantine: %+v", s[run2])
	}
	// vence (fecha controlada: ya pasó), sin que nadie toque la cuarentena
	if _, err := conn.Exec(`UPDATE quarantine SET until = 1`); err != nil {
		t.Fatal(err)
	}
	s := statuses(t, srv, run1, run2)
	if s[run2].Status != "FAIL" || s[run2].Failed != 1 || s[run2].Quarantined != 0 || s[run1].Status != "FAIL" {
		t.Fatalf("after expiry: %+v", s)
	}
}

func TestDerivedStatusLeavesOpenAndIncompleteRunsAlone(t *testing.T) {
	srv, _ := newTestServer(t)
	run1, fail1 := closedFailingRun(t, srv, "shop")
	// abierta, con el mismo test fallado
	open, _ := srv.Store.CreateRunWithMeta("open", "qa", db.RunMeta{Project: "shop"})
	ot, _ := srv.Store.CreateTestWithMeta(open, "login", "", "", db.TestMeta{Key: "tests/test_login.py::test_login"})
	srv.Store.FinishTest(ot, "FAIL", "boom", "")
	// cerrada como incompleta, sin fallos
	inc, _ := srv.Store.CreateRunWithMeta("inc", "qa", db.RunMeta{Project: "shop"})
	it, _ := srv.Store.CreateTestWithMeta(inc, "home", "", "", db.TestMeta{Key: "tests/test_home.py::test_home"})
	srv.Store.FinishTest(it, "PASS", "", "")
	srv.Store.CloseRun(inc, true)

	call(t, srv, "POST", "/api/v1/ui/quarantine", fmt.Sprintf(`{"test_id":%d,"reason":"x"}`, fail1))
	if r, _ := srv.Store.GetRun(open); r.Status != "RUNNING" {
		t.Fatalf("open run keeps RUNNING: %s", r.Status)
	}
	if r, _ := srv.Store.GetRun(inc); r.Status != "WARNING" || !r.Incomplete {
		t.Fatalf("incomplete run keeps WARNING: %+v", r)
	}
	if r, _ := srv.Store.GetRun(run1); r.Status != "WARNING" {
		t.Fatalf("closed: %s", r.Status)
	}
}
