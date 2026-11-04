package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func newRunWithTest(t *testing.T, srv *Server, opts ...reqOpt) (runID, testID int64) {
	t.Helper()
	var r map[string]int64
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"nightly"}`, opts...).Body.Bytes(), &r)
	runID = r["run_id"]
	json.Unmarshal(call(t, srv, "POST", fmt.Sprintf("/api/v1/runs/%d/tests", runID), `{"name":"t1"}`, opts...).Body.Bytes(), &r)
	return runID, r["test_id"]
}

func TestStaleRunsAreClosedOnlyWithoutRealActivity(t *testing.T) {
	srv, _ := newTestServer(t)
	abandoned, abandonedTest := newRunWithTest(t, srv)
	// un cliente offline reenvía con horas viejas: cuenta la hora en que llega, no la del cliente
	old := strconv.FormatInt(time.Now().Add(-72*time.Hour).UnixMilli(), 10)
	offline, _ := newRunWithTest(t, srv, header("X-TraceReports-Timestamp", old))
	alive, aliveTest := newRunWithTest(t, srv)

	const idle = 500 * time.Millisecond // margen amplio: con -race todo corre varias veces más lento
	time.Sleep(idle + 100*time.Millisecond)
	// la ejecución larga sigue enviando
	call(t, srv, "POST", fmt.Sprintf("/api/v1/tests/%d/logs", aliveTest), `{"message":"sigo vivo"}`)
	// y la offline termina de subir su grabación ahora
	call(t, srv, "POST", fmt.Sprintf("/api/v1/runs/%d/tests", offline), `{"name":"t2"}`, header("X-TraceReports-Timestamp", old))

	n, err := srv.CloseStaleRuns(idle)
	if err != nil || n != 2 { // la abandonada y la que crea newTestServer (tampoco recibe nada)
		t.Fatalf("closed %d (want the two without activity): %v", n, err)
	}
	run, _ := srv.Store.GetRun(abandoned)
	if run.Status == "RUNNING" || !run.Incomplete || run.EndedAt == nil {
		t.Fatalf("abandoned run closed as incomplete: %+v", run)
	}
	if tt, _ := srv.Store.GetTest(abandonedTest); tt.Status != "INTERRUPTED" || !strings.Contains(tt.ErrorMessage, "sin recibir datos") {
		t.Fatalf("its RUNNING test is interrupted, saying why: %s %q", tt.Status, tt.ErrorMessage)
	}
	if run.ClosedBy != db.ClosedByInactivity || run.Interrupted != 1 || run.Failed != 0 {
		t.Fatalf("closed by inactivity, without counting a failure: %+v", run)
	}
	for _, id := range []int64{alive, offline} {
		if r, _ := srv.Store.GetRun(id); r.Status != "RUNNING" {
			t.Fatalf("run %d with real activity must stay open: %s", id, r.Status)
		}
	}
	// otra pasada no vuelve a cerrar ni a notificar (las activas siguen enviando)
	srv.Store.TouchRun(alive)
	srv.Store.TouchRun(offline)
	if n, _ := srv.CloseStaleRuns(idle); n != 0 {
		t.Fatalf("second pass: %d", n)
	}

	// el cliente "vuelve": su finish y la evidencia tardía se aceptan
	if rec := call(t, srv, "POST", fmt.Sprintf("/api/v1/tests/%d/logs", abandonedTest), `{"message":"tarde"}`); rec.Code != 201 {
		t.Fatalf("late evidence: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, srv, "PATCH", fmt.Sprintf("/api/v1/runs/%d/finish", abandoned), `{}`); rec.Code != 200 {
		t.Fatalf("late finish: %d %s", rec.Code, rec.Body)
	}
	if run, _ := srv.Store.GetRun(abandoned); !run.Incomplete {
		t.Fatal("it stays marked incomplete: it was interrupted")
	}
}

// Cerrar a mano desde la interfaz: solo administración, con el motivo (sin secretos) y quién; una
// ejecución ya cerrada responde 409.
func TestCloseRunByHandFromTheUI(t *testing.T) {
	srv, _ := newTestServer(t)
	runID, testID := newRunWithTest(t, srv)
	srv.Auth = Auth{UIUser: "admin", UIPass: "clave-admin", ViewerUser: "equipo", ViewerPass: "clave-lectura"}
	path := fmt.Sprintf("/api/v1/ui/runs/%d/close", runID)
	body := `{"reason":"se cayó el CI, token=Sup3rSecreto123456"}`
	if rec := call(t, srv, "POST", path, body, basicAuth("equipo", "clave-lectura")); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only user: %d", rec.Code)
	}
	rec := call(t, srv, "POST", path, body, basicAuth("admin", "clave-admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("close: %d %s", rec.Code, rec.Body)
	}
	var run db.Run
	json.Unmarshal(rec.Body.Bytes(), &run)
	if run.Status != "WARNING" || !run.Incomplete || run.Interrupted != 1 || run.ClosedBy != "admin" || !strings.Contains(run.CloseReason, "se cayó el CI") {
		t.Fatalf("closed run: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "Sup3rSecreto123456") {
		t.Fatal("the reason goes through the redaction")
	}
	if tt, _ := srv.Store.GetTest(testID); tt.Status != "INTERRUPTED" {
		t.Fatalf("its RUNNING test: %s", tt.Status)
	}
	if rec := call(t, srv, "POST", path, `{}`, basicAuth("admin", "clave-admin")); rec.Code != http.StatusConflict {
		t.Fatalf("second close: %d", rec.Code)
	}
	if rec := call(t, srv, "POST", "/api/v1/ui/runs/99999/close", `{}`, basicAuth("admin", "clave-admin")); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown run: %d", rec.Code)
	}
}

func TestIdleText(t *testing.T) {
	for d, want := range map[time.Duration]string{20 * time.Second: "1 min", 45 * time.Minute: "45 min", 24 * time.Hour: "24 h", 90 * time.Minute: "1 h 30 min"} {
		if got := idleText(d); got != want {
			t.Errorf("idleText(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestCloseIfIdleRechecksInsideTheTransaction(t *testing.T) {
	srv, _ := newTestServer(t)
	runID, _ := newRunWithTest(t, srv)
	time.Sleep(20 * time.Millisecond)
	cutoff := time.Now().UnixMilli()
	ids, _ := srv.Store.StaleRuns(cutoff)
	if len(ids) == 0 {
		t.Fatal("the run is stale for this cutoff")
	}
	// llega algo justo entre listar y cerrar
	time.Sleep(5 * time.Millisecond)
	srv.Store.TouchRun(runID)
	if closed, _, err := srv.Store.CloseIfIdle(runID, cutoff, db.InterruptedMessage); err != nil || closed {
		t.Fatalf("a run that just got activity must not be closed: %v %v", closed, err)
	}
}

func TestStaleClosedRunIsEligibleForRetention(t *testing.T) {
	srv, _ := newTestServer(t)
	runID, _ := newRunWithTest(t, srv)
	time.Sleep(20 * time.Millisecond)
	if _, _, err := srv.Store.PurgeRunsBefore(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if r, err := srv.Store.GetRun(runID); err != nil || r.Status != "RUNNING" {
		t.Fatal("retention never deletes a RUNNING run")
	}
	srv.CloseStaleRuns(10 * time.Millisecond)
	if _, _, err := srv.Store.PurgeRunsBefore(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.GetRun(runID); err == nil {
		t.Fatal("once closed, retention can delete it")
	}
}
