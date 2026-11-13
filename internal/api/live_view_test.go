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
		{LiveTest: db.LiveTest{ID: 1, Name: "test_wishlist", Worker: "gw1", Status: "RUNNING", LastStep: "Evidencia final"}, State: "no_signal", Idle: 4 * 60_000},
		{LiveTest: db.LiveTest{ID: 2, Name: "test_lento", Status: "RUNNING", P90: 60_000}, State: "turtle", Elapsed: 150_000, Factor: 2.5},
		{LiveTest: db.LiveTest{ID: 3, Name: "test_algo_lento", Status: "RUNNING", P90: 60_000}, State: "turtle", Elapsed: 90_000},
		{LiveTest: db.LiveTest{ID: 4, Status: "FAIL"}, State: "FAIL"},
		{LiveTest: db.LiveTest{ID: 5, Status: "FAIL"}, State: "FAIL"},
	}
	list := []liveExec{{Key: "x:b1", Title: "build 1", Running: true, Runs: []liveRunView{{LiveRun: db.LiveRun{ID: 9, Name: "test_checkout.py"}, Tests: tests}}},
		{Key: "g:2", Running: false, Runs: []liveRunView{{Tests: tests}}}} // terminada: no avisa
	got := liveAlerts(list, cfg, func(r, t int64) string { return fmt.Sprintf("u/%d/%d", r, t) })
	keys := []string{}
	for _, a := range got {
		keys = append(keys, a.key)
	}
	if strings.Join(keys, ",") != "nosignal:1,turtle:2,failures:x:b1" {
		t.Fatalf("alertas: %v", keys)
	}
	if m := got[0].msg; !strings.Contains(m.Title, "test_wishlist") || !strings.Contains(m.Stats, "gw1") || !strings.Contains(m.Stats, "4 min") ||
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
	r1, r2 := newRun("test_checkout.py"), newRun("test_cart.py")
	done := newTest(r1, "test_open_product")
	call(t, srv, "PATCH", fmt.Sprintf("/api/v1/tests/%d/finish", done), `{"status":"PASS"}`)
	stuck := newTest(r1, "test_wishlist")
	call(t, srv, "POST", fmt.Sprintf("/api/v1/tests/%d/logs", stuck), `{"status":"INFO","message":"Evidencia final"}`)
	alive := newTest(r2, "test_cart")

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
	if states["test_wishlist"] != "no_signal" || states["test_cart"] != "running" || states["test_open_product"] != "PASS" {
		t.Fatalf("estados: %v", states)
	}
	if steps["test_wishlist"] != "Evidencia final" {
		t.Fatalf("último paso: %v", steps)
	}
	if x := e.Execution; x.Total != 3 || x.Done != 1 || x.Planned != 4 || x.EtaMs <= 0 || x.Counts["no_signal"] != 1 || x.Title != "build-1" {
		t.Fatalf("resumen: %+v", x)
	}
	// lo que pide acción primero dentro de la corrida
	if first := e.Execution.Runs[0].Tests[0].Name; first != "test_wishlist" {
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

// Un lanzador con procesos en cola abre corridas después de cancelar: esas también se detienen, con y
// sin execution_id (la agrupación por contexto y tiempo).
func TestCancelReachesRunsCreatedLater(t *testing.T) {
	srv, _ := newTestServer(t)
	runOf := func(body string) int64 {
		var out struct {
			RunID int64 `json:"run_id"`
		}
		json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", body).Body.Bytes(), &out)
		return out.RunID
	}
	control := func(run int64) string { return call(t, srv, "GET", fmt.Sprintf("/api/v1/runs/%d/control", run), "").Body.String() }

	// con execution_id
	first := runOf(`{"name":"a.py","project":"box","environment":"qa","execution_id":"suite-1"}`)
	if rec := call(t, srv, "POST", "/api/v1/ui/live/cancel", `{"key":"x:suite-1","reason":"se cayó QA"}`); rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	later := runOf(`{"name":"b.py","project":"box","environment":"qa","execution_id":"suite-1"}`)
	if c := control(later); !strings.Contains(c, `"cancel":true`) || !strings.Contains(c, "se cayó QA") {
		t.Fatalf("la corrida nueva nace cancelada: %s", c)
	}
	other := runOf(`{"name":"c.py","project":"box","environment":"qa","execution_id":"suite-2"}`)
	if c := control(other); !strings.Contains(c, `"cancel":false`) {
		t.Fatalf("otra ejecución no: %s", c)
	}
	_ = first

	// sin execution_id: la misma ejecución por proyecto, ambiente, rama y tiempo
	g1 := runOf(`{"name":"x.py","project":"agenda","environment":"uat"}`)
	if rec := call(t, srv, "POST", "/api/v1/ui/live/cancel", fmt.Sprintf(`{"key":"g:%d","reason":"fin del turno"}`, g1)); rec.Code != 200 {
		t.Fatalf("cancel g: %d %s", rec.Code, rec.Body)
	}
	g2 := runOf(`{"name":"y.py","project":"agenda","environment":"uat"}`)
	if c := control(g2); !strings.Contains(c, `"cancel":true`) || !strings.Contains(c, "fin del turno") {
		t.Fatalf("sin execution_id también: %s", c)
	}
	// y En vivo muestra la ejecución cancelada
	rec := call(t, srv, "GET", "/api/v1/live/execution?key=x:suite-1", "")
	if !strings.Contains(rec.Body.String(), `"cancel_requested":true`) {
		t.Fatalf("live: %s", rec.Body)
	}
}

// Una corrida recién abierta, todavía sin tests, va con "tests": [] (con null, En vivo se rompía).
func TestLiveRunWithoutTestsIsAnEmptyList(t *testing.T) {
	srv, _ := newTestServer(t)
	call(t, srv, "POST", "/api/v1/runs", `{"name":"en cola.py","project":"p","environment":"qa","execution_id":"cola-1"}`)
	body := call(t, srv, "GET", "/api/v1/live/execution?key=x:cola-1", "").Body.String()
	if !strings.Contains(body, `"tests":[]`) || strings.Contains(body, `"tests":null`) {
		t.Fatalf("%s", body)
	}
}

// El backend de toda una ejecución (una corrida por archivo): los endpoints de todas sus corridas
// juntos, con errores primero; los de otra ejecución no entran y la query de la URL no se ve.
func TestLiveEndpointsAcrossRuns(t *testing.T) {
	srv, _ := newTestServer(t)
	runOf := func(name, exec string) string {
		var r struct {
			RunID int64 `json:"run_id"`
		}
		json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"`+name+`","project":"p","environment":"qa","execution_id":"`+exec+`"}`).Body.Bytes(), &r)
		var tr struct {
			TestID int64 `json:"test_id"`
		}
		json.Unmarshal(call(t, srv, "POST", fmt.Sprintf("/api/v1/runs/%d/tests", r.RunID), `{"name":"t_`+name+`"}`).Body.Bytes(), &tr)
		return fmt.Sprint(tr.TestID)
	}
	a, b, other := runOf("a.py", "ep-1"), runOf("b.py", "ep-1"), runOf("c.py", "ep-2")
	call(t, srv, "POST", "/api/v1/tests/"+a+"/network", `{"connections":[
		{"method":"GET","url":"https://app.test/api/customers/12?token=s3cr3t","status":200,"duration_ms":120,"resource_type":"fetch"},
		{"method":"GET","url":"https://app.test/api/agenda","status":200,"duration_ms":80,"resource_type":"xhr"}]}`)
	call(t, srv, "POST", "/api/v1/tests/"+b+"/network", `{"connections":[
		{"method":"GET","url":"https://app.test/api/customers/7","status":504,"duration_ms":30000,"resource_type":"fetch"},
		{"method":"GET","url":"https://app.test/static/app.js","status":200,"duration_ms":5,"resource_type":"script"}]}`)
	call(t, srv, "POST", "/api/v1/tests/"+other+"/network", `{"connections":[{"method":"POST","url":"https://otra.test/x","status":500,"resource_type":"fetch"}]}`)

	rec := call(t, srv, "GET", "/api/v1/live/endpoints?key=x:ep-1", "")
	var out struct {
		Endpoints []db.EndpointStat `json:"endpoints"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || len(out.Endpoints) != 2 || strings.Contains(rec.Body.String(), "s3cr3t") || strings.Contains(rec.Body.String(), "otra.test") ||
		strings.Contains(rec.Body.String(), "app.js") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e := out.Endpoints[0]; e.Path != "/api/customers/:id" || e.Count != 2 || e.Errors != 1 || e.MaxMs != 30000 {
		t.Fatalf("errores primero, de las dos corridas: %+v", e)
	}
	if rec := call(t, srv, "GET", "/api/v1/live/endpoints?key=x:no-existe", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown execution: %d", rec.Code)
	}
}

// Una ejecución que ya salió de la lista de En vivo (terminó hace más que la ventana) se abre igual
// por su clave o por uno de sus runs (r:<id>), marcada como anterior; su backend también. Desconocida: 404.
func TestLivePastExecution(t *testing.T) {
	srv, _ := newTestServer(t)
	runOf := func(name string) int64 {
		var r struct {
			RunID int64 `json:"run_id"`
		}
		json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"`+name+`","project":"box","environment":"qa","execution_id":"suite-888"}`).Body.Bytes(), &r)
		var tr struct {
			TestID int64 `json:"test_id"`
		}
		json.Unmarshal(call(t, srv, "POST", fmt.Sprintf("/api/v1/runs/%d/tests", r.RunID), `{"name":"t_`+name+`","worker":"gw3"}`).Body.Bytes(), &tr)
		call(t, srv, "PATCH", fmt.Sprintf("/api/v1/tests/%d/finish", tr.TestID), `{"status":"FAIL"}`)
		call(t, srv, "PATCH", fmt.Sprintf("/api/v1/runs/%d/finish", r.RunID), `{"status":"FAIL"}`)
		return r.RunID
	}
	a, b := runOf("a.py"), runOf("b.py")
	old := liveWindow
	liveWindow = time.Millisecond
	t.Cleanup(func() { liveWindow = old })
	time.Sleep(20 * time.Millisecond)

	if body := call(t, srv, "GET", "/api/v1/live", "").Body.String(); strings.Contains(body, "suite-888") {
		t.Fatalf("ya no está en la lista: %s", body)
	}
	for _, key := range []string{"x:suite-888", fmt.Sprintf("r:%d", b)} {
		rec := call(t, srv, "GET", "/api/v1/live/execution?key="+url.QueryEscape(key), "")
		var out struct {
			Execution struct {
				Key     string         `json:"key"`
				Past    bool           `json:"past"`
				Running bool           `json:"running"`
				Counts  map[string]int `json:"counts"`
				Runs    []struct {
					ID int64 `json:"id"`
				} `json:"runs"`
			} `json:"execution"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		e := out.Execution
		if rec.Code != 200 || e.Key != "x:suite-888" || !e.Past || e.Running || len(e.Runs) != 2 || e.Counts["FAIL"] != 2 {
			t.Fatalf("%s: %d %s", key, rec.Code, rec.Body)
		}
		if e.Runs[0].ID != a && e.Runs[1].ID != a {
			t.Fatalf("%s: falta la corrida %d", key, a)
		}
	}
	if rec := call(t, srv, "GET", fmt.Sprintf("/api/v1/live/endpoints?key=r:%d", a), ""); rec.Code != 200 {
		t.Fatalf("backend de una anterior: %d %s", rec.Code, rec.Body)
	}
	for _, key := range []string{"x:no-existe", "r:999999", "r:abc"} {
		if rec := call(t, srv, "GET", "/api/v1/live/execution?key="+url.QueryEscape(key), ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", key, rec.Code)
		}
	}
}