package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestTestStateRules(t *testing.T) {
	cfg := liveConfig{noSignal: 3 * time.Minute}
	now := int64(10 * 60_000)
	running := func(started, signal, p50, p90 int64, hist int) db.LiveTest {
		return db.LiveTest{Status: "RUNNING", StartedAt: started, LastSignal: signal, P50: p50, P90: p90, Hist: hist}
	}
	cases := []struct {
		name string
		t    db.LiveTest
		want string
	}{
		{"recién empezado", running(now-10_000, now-1_000, 60_000, 90_000, 5), "running"},
		{"más que su p90", running(now-120_000, now-1_000, 60_000, 90_000, 5), "turtle"},
		{"poco historial: más del doble del p50", running(now-130_000, now-1_000, 60_000, 0, 1), "turtle"},
		{"poco historial, menos del doble", running(now-100_000, now-1_000, 60_000, 0, 1), "running"},
		{"unos segundos de más en un test corto", running(now-12_000, now-1_000, 4_000, 5_000, 5), "running"},
		{"la agenda de la demo: mediana 6 s, lleva 25 s", running(now-25_000, now-1_000, 6_000, 7_000, 3), "turtle"},
		{"sin historial nunca es tortuga", running(now-9*60_000, now-1_000, 0, 0, 0), "running"},
		{"sin señal gana a tortuga", running(now-9*60_000, now-4*60_000, 60_000, 90_000, 5), "no_signal"},
		{"terminado", db.LiveTest{Status: "FAIL", StartedAt: 0, EndedAt: &now, P50: 60_000}, "FAIL"},
	}
	for _, c := range cases {
		if got := testState(c.t, now, cfg); got.State != c.want {
			t.Errorf("%s: %s, want %s", c.name, got.State, c.want)
		}
	}
	if s := testState(running(now-120_000, now-1_000, 60_000, 90_000, 5), now, cfg); s.Factor != 2 || s.Elapsed != 120_000 || s.Idle != 1_000 {
		t.Fatalf("%+v", s)
	}
}

func TestLiveAlertsRules(t *testing.T) {
	cfg := liveConfig{alertTurtle: 2, alertFailing: 1}
	tests := []liveTest{
		{LiveTest: db.LiveTest{ID: 1, Name: "test_periodontograma", Worker: "gw1", Status: "RUNNING", LastStep: "Evidencia final"}, State: "no_signal", Idle: 4 * 60_000},
		{LiveTest: db.LiveTest{ID: 2, Name: "test_lento", Status: "RUNNING", P90: 60_000}, State: "turtle", Elapsed: 150_000, Factor: 2.5},
		{LiveTest: db.LiveTest{ID: 3, Name: "test_algo_lento", Status: "RUNNING", P90: 60_000}, State: "turtle", Elapsed: 90_000},
		{LiveTest: db.LiveTest{ID: 4, Status: "FAIL"}, State: "FAIL"},
		{LiveTest: db.LiveTest{ID: 5, Status: "FAIL"}, State: "FAIL"},
	}
	list := []liveExec{{Key: "x:b1", Title: "build 1", Running: true, Runs: []liveRunView{{LiveRun: db.LiveRun{ID: 9, Name: "test_ficha.py"}, Tests: tests}}},
		{Key: "g:2", Running: false, Runs: []liveRunView{{Tests: tests}}}} // terminada: no avisa
	got := liveAlerts(list, cfg, func(r, t int64) string { return fmt.Sprintf("u/%d/%d", r, t) })
	keys := []string{}
	for _, a := range got {
		keys = append(keys, a.key)
	}
	if strings.Join(keys, ",") != "nosignal:1,turtle:2,failures:x:b1" {
		t.Fatalf("alertas: %v", keys)
	}
	if m := got[0].msg; !strings.Contains(m.Title, "test_periodontograma") || !strings.Contains(m.Stats, "gw1") || !strings.Contains(m.Stats, "4 min") ||
		!strings.Contains(m.Summary, "Evidencia final") || m.URL != "u/9/1" {
		t.Fatalf("sin señal: %+v", m)
	}
	if got := liveAlerts(list, liveConfig{}, func(int64, int64) string { return "" }); len(got) != 1 {
		t.Fatalf("sin umbrales solo avisa lo sin señal: %d", len(got))
	}
}

func TestTestForWrite(t *testing.T) {
	for path, want := range map[string]int64{"/api/v1/tests/12/logs": 12, "/api/v1/tests/7": 7, "/api/v1/runs/3/tests": 0, "/api/v1/tests/x/logs": 0} {
		if got := testForWrite(path); got != want {
			t.Errorf("%s = %d", path, got)
		}
	}
}

type liveExecJSON struct {
	Execution struct {
		Key     string         `json:"key"`
		Title   string         `json:"title"`
		Planned int            `json:"planned"`
		Done    int            `json:"done"`
		Total   int            `json:"total"`
		EtaMs   int64          `json:"eta_ms"`
		Counts  map[string]int `json:"counts"`
		Cancel  bool           `json:"cancel_requested"`
		Runs    []struct {
			ID    int64 `json:"id"`
			Tests []struct {
				ID       int64  `json:"id"`
				Name     string `json:"name"`
				State    string `json:"state"`
				LastStep string `json:"last_step"`
			} `json:"tests"`
		} `json:"runs"`
	} `json:"execution"`
}

// Un lanzador que crea una corrida por archivo con el mismo execution_id: En vivo las muestra juntas,
// con progreso por los tests planificados, el último paso de cada test y "sin señal" para el que dejó
// de enviar; desde la interfaz se pide cancelar y el cliente lo ve en /control.
func TestLiveExecutionFlow(t *testing.T) {
	t.Setenv("TRACEREPORTS_NO_SIGNAL_MIN", "0.002") // 120 ms
	srv, _ := newTestServer(t)
	newRun := func(name string) int64 {
		rec := call(t, srv, "POST", "/api/v1/runs", `{"name":"`+name+`","project":"box","environment":"qa","execution_id":"build-1","planned":2}`)
		var out struct {
			RunID int64 `json:"run_id"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		if out.RunID == 0 {
			t.Fatalf("run: %d %s", rec.Code, rec.Body)
		}
		return out.RunID
	}
	newTest := func(run int64, name string) int64 {
		rec := call(t, srv, "POST", fmt.Sprintf("/api/v1/runs/%d/tests", run), `{"name":"`+name+`","key":"k::`+name+`","worker":"gw0"}`)
		var out struct {
			TestID int64 `json:"test_id"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out.TestID
	}
	r1, r2 := newRun("test_ficha.py"), newRun("test_agenda.py")
	done := newTest(r1, "test_abrir_ficha")
	call(t, srv, "PATCH", fmt.Sprintf("/api/v1/tests/%d/finish", done), `{"status":"PASS"}`)
	stuck := newTest(r1, "test_periodontograma")
	call(t, srv, "POST", fmt.Sprintf("/api/v1/tests/%d/logs", stuck), `{"status":"INFO","message":"Evidencia final"}`)
	alive := newTest(r2, "test_agenda")

	// la lista: las dos corridas en una sola ejecución, la más nueva primero (la otra es la corrida
	// que deja newTestServer, sin execution_id)
	rec := call(t, srv, "GET", "/api/v1/live", "")
	var list struct {
		Executions []struct {
			Key, Title string
			Planned    int
		}
	}
	if json.Unmarshal(rec.Body.Bytes(), &list); rec.Code != 200 || len(list.Executions) != 2 || list.Executions[0].Key != "x:build-1" || list.Executions[0].Planned != 4 {
		t.Fatalf("live: %d %s", rec.Code, rec.Body)
	}

	time.Sleep(200 * time.Millisecond)
	call(t, srv, "POST", fmt.Sprintf("/api/v1/tests/%d/logs", alive), `{"status":"INFO","message":"filtra la agenda"}`) // este sí envía
	get := func() liveExecJSON {
		rec := call(t, srv, "GET", "/api/v1/live/execution?key="+url.QueryEscape("x:build-1"), "")
		var e liveExecJSON
		if json.Unmarshal(rec.Body.Bytes(), &e); rec.Code != 200 {
			t.Fatalf("execution: %d %s", rec.Code, rec.Body)
		}
		return e
	}
	e := get()
	states := map[string]string{}
	steps := map[string]string{}
	for _, r := range e.Execution.Runs {
		for _, x := range r.Tests {
			states[x.Name], steps[x.Name] = x.State, x.LastStep
		}
	}
	if states["test_periodontograma"] != "no_signal" || states["test_agenda"] != "running" || states["test_abrir_ficha"] != "PASS" {
		t.Fatalf("estados: %v", states)
	}
	if steps["test_periodontograma"] != "Evidencia final" {
		t.Fatalf("último paso: %v", steps)
	}
	if x := e.Execution; x.Total != 3 || x.Done != 1 || x.Planned != 4 || x.EtaMs <= 0 || x.Counts["no_signal"] != 1 || x.Title != "build-1" {
		t.Fatalf("resumen: %+v", x)
	}
	// lo que pide acción primero dentro de la corrida
	if first := e.Execution.Runs[0].Tests[0].Name; first != "test_periodontograma" {
		t.Fatalf("orden: %s primero", first)
	}

	// tests planificados que llegan después (pytest_collection_finish)
	if rec := call(t, srv, "POST", fmt.Sprintf("/api/v1/runs/%d/plan", r2), `{"planned":5}`); rec.Code != 200 {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	if call(t, srv, "POST", fmt.Sprintf("/api/v1/runs/%d/plan", r2), `{"planned":-1}`).Code != 400 {
		t.Fatal("planned negativo")
	}
	if e := get(); e.Execution.Planned != 7 {
		t.Fatalf("planned: %d", e.Execution.Planned)
	}

	// cancelar desde la interfaz: el cliente lo ve en /control
	ctl := func(run int64) string {
		return call(t, srv, "GET", fmt.Sprintf("/api/v1/runs/%d/control", run), "").Body.String()
	}
	if !strings.Contains(ctl(r1), `"cancel":false`) {
		t.Fatal(ctl(r1))
	}
	rec = call(t, srv, "POST", "/api/v1/ui/live/cancel", `{"key":"x:build-1","reason":"el ambiente de QA se cayó"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"runs":2`) {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if c := ctl(r2); !strings.Contains(c, `"cancel":true`) || !strings.Contains(c, "el ambiente de QA se cayó") {
		t.Fatal(c)
	}
	if !get().Execution.Cancel {
		t.Fatal("la ejecución muestra la cancelación pedida")
	}
	if rec := call(t, srv, "POST", "/api/v1/ui/live/cancel", `{"key":"x:build-1"}`); rec.Code != http.StatusConflict {
		t.Fatalf("pedirlo otra vez: %d", rec.Code)
	}
	if rec := call(t, srv, "GET", "/api/v1/live/execution?key=x:nada", ""); rec.Code != 404 {
		t.Fatalf("no existe: %d", rec.Code)
	}
}

// Cancelar es una acción de administración: con login de solo lectura o sin el token, no.
func TestLiveCancelNeedsAdmin(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "admin", UIUser: "u", UIPass: "p", ViewerUser: "v", ViewerPass: "w"}
	rec := call(t, srv, "POST", "/api/v1/ui/live/cancel", `{"key":"g:1"}`, func(r *http.Request) { r.SetBasicAuth("v", "w") })
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer: %d %s", rec.Code, rec.Body)
	}
}
