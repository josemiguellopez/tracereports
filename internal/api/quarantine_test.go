package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// runWith closes a run of a project with a failing "checkout" test and a passing one.
func runWith(t *testing.T, srv *Server, project string) (runID, failID int64) {
	t.Helper()
	runID, _ = srv.Store.CreateRunWithMeta("E2E", "qa", db.RunMeta{Project: project})
	failID, _ = srv.Store.CreateTestWithMeta(runID, "checkout", "", "", db.TestMeta{Key: "tests/test_shop.py::test_checkout"})
	srv.Store.FinishTest(failID, "FAIL", "timeout", "")
	ok, _ := srv.Store.CreateTestWithMeta(runID, "login", "", "", db.TestMeta{Key: "tests/test_shop.py::test_login"})
	srv.Store.FinishTest(ok, "PASS", "", "")
	srv.Store.CloseRun(runID, false)
	return runID, failID
}

func quarantine(t *testing.T, srv *Server, body map[string]any, want int) map[string]json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := call(t, srv, "POST", "/api/v1/ui/quarantine", string(raw))
	if rec.Code != want {
		t.Fatalf("quarantine %v: %d %s", body, rec.Code, rec.Body)
	}
	var out map[string]json.RawMessage
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func status(t *testing.T, srv *Server, runID int64) (string, int) {
	t.Helper()
	run, err := srv.Store.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	return run.Status, run.Quarantined
}

func TestQuarantineKeepsRunsOutOfRedButVisible(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	run1, fail1 := runWith(t, srv, "shop")
	if st, _ := status(t, srv, run1); st != "FAIL" {
		t.Fatalf("before: %s", st)
	}

	out := quarantine(t, srv, map[string]any{"test_id": fail1, "reason": "flaky: timeout del proveedor de pagos", "owner": "Equipo pagos", "days": 7}, 200)
	var run db.Run
	json.Unmarshal(out["run"], &run)
	if run.Status != "WARNING" || run.Quarantined != 1 || run.Failed != 1 {
		t.Fatalf("the run being looked at turns yellow, the failure still counts: %+v", run)
	}
	tst, _ := srv.Store.GetTest(fail1)
	if q := tst.Quarantine; q == nil || !q.Active || q.Owner != "Equipo pagos" || time.Until(time.UnixMilli(q.Until)) < 6*24*time.Hour {
		t.Fatalf("test quarantine: %+v", tst.Quarantine)
	}

	// la próxima ejecución del proyecto: el mismo fallo no la pone en rojo
	run2, _ := runWith(t, srv, "shop")
	if st, q := status(t, srv, run2); st != "WARNING" || q != 1 {
		t.Fatalf("next run: %s %d", st, q)
	}
	// otro proyecto con la misma identidad de test: la cuarentena no aplica
	other, _ := runWith(t, srv, "billing")
	if st, _ := status(t, srv, other); st != "FAIL" {
		t.Fatalf("another project: %s", st)
	}

	var list []db.Quarantine
	json.Unmarshal(call(t, srv, "GET", "/api/v1/quarantine?project=shop", "").Body.Bytes(), &list)
	if len(list) != 1 || list[0].Key != "tests/test_shop.py::test_checkout" || !list[0].Active {
		t.Fatalf("list: %+v", list)
	}

	// quitarla: la ejecución vuelve a rojo
	if rec := call(t, srv, "DELETE", "/api/v1/ui/quarantine/"+itoa(fail1), "{}"); rec.Code != 200 {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	if st, q := status(t, srv, run1); st != "FAIL" || q != 0 {
		t.Fatalf("after removing: %s %d", st, q)
	}
}

func TestExpiredQuarantineStopsApplying(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Store.SetQuarantine(&db.Quarantine{Project: "shop", Key: "tests/test_shop.py::test_checkout", Reason: "old",
		Until: time.Now().Add(-time.Hour).UnixMilli()})
	run, fail := runWith(t, srv, "shop")
	if st, q := status(t, srv, run); st != "FAIL" || q != 0 {
		t.Fatalf("an expired quarantine does not hide failures: %s %d", st, q)
	}
	tst, _ := srv.Store.GetTest(fail)
	if tst.Quarantine == nil || tst.Quarantine.Active {
		t.Fatalf("shown as expired: %+v", tst.Quarantine)
	}
}

func TestQuarantineValidationAndAccess(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	_, fail := runWith(t, srv, "shop")
	quarantine(t, srv, map[string]any{"test_id": fail}, 400)                             // sin motivo
	quarantine(t, srv, map[string]any{"test_id": fail, "reason": "x", "days": 500}, 400) // demasiado
	quarantine(t, srv, map[string]any{"test_id": 9999, "reason": "x"}, 404)
	out := quarantine(t, srv, map[string]any{"test_id": fail, "reason": "token=SECRET123 en el log"}, 200) // 14 días por defecto
	var q db.Quarantine
	json.Unmarshal(out["quarantine"], &q)
	if d := time.Until(time.UnixMilli(q.Until)); d < 13*24*time.Hour || d > 15*24*time.Hour {
		t.Fatalf("default 14 days: %v", d)
	}
	if q.Reason == "" || strings.Contains(q.Reason, "SECRET123") {
		t.Fatalf("reason is masked: %q", q.Reason)
	}
	raw, _ := json.Marshal(map[string]any{"test_id": fail, "reason": "x"})
	if rec := call(t, srv, "POST", "/api/v1/ui/quarantine", string(raw), fromAddr("10.0.0.9:1")); rec.Code != 403 {
		t.Fatalf("remote without login: %d", rec.Code)
	}
	if rec := call(t, srv, "DELETE", "/api/v1/ui/quarantine/"+itoa(fail), "{}", fromAddr("10.0.0.9:1")); rec.Code != 403 {
		t.Fatalf("remote remove: %d", rec.Code)
	}
}
