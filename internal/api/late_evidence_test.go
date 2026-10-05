package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
)

// Regresiones de la revalidación de producción: evidencia que llega tarde.

func waitAI(t *testing.T, srv *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.AI.Wait(ctx)
}

func runSummary(t *testing.T, srv *Server, runID int64) *db.RunTriage {
	t.Helper()
	rt, err := srv.Store.GetRunTriage(runID)
	if err != nil || rt == nil {
		t.Fatalf("run summary: %v %+v", err, rt)
	}
	return rt
}

// Un resultado tardío que cambia una ejecución cerrada reconstruye su resumen (con la IA apagada:
// agrupación determinista); un replay idéntico no lo toca.
func TestLateResultRebuildsSummaryWithoutAI(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	rid, _ := srv.Store.CreateRun("tardío", "qa")
	tid, _ := srv.Store.CreateTest(rid, "pagar", "", "")
	finish := func(body string) {
		t.Helper()
		if rec := call(t, srv, "PATCH", "/api/v1/tests/"+itoa(tid)+"/finish", body); rec.Code != 200 {
			t.Fatalf("finish test: %d %s", rec.Code, rec.Body)
		}
	}
	finish(`{"status":"PASS"}`)
	call(t, srv, "PATCH", "/api/v1/runs/"+itoa(rid)+"/finish", `{}`)
	waitAI(t, srv)
	if rt := runSummary(t, srv, rid); rt.State != "DONE" || len(rt.Incidents) != 0 {
		t.Fatalf("summary of the passing run: %+v", rt)
	}

	finish(`{"status":"FAIL","error_message":"TimeoutError: pago no respondió"}`)
	if rt := runSummary(t, srv, rid); rt.State != "PENDING" {
		t.Fatalf("while it is rebuilt the summary must say so: %+v", rt)
	}
	waitAI(t, srv)
	failed := runSummary(t, srv, rid)
	if failed.State != "DONE" || len(failed.Incidents) != 1 {
		t.Fatalf("late FAIL left a stale summary: %+v", failed)
	}

	finish(`{"status":"FAIL","error_message":"TimeoutError: pago no respondió"}`) // replay idéntico
	waitAI(t, srv)
	if rt := runSummary(t, srv, rid); rt.UpdatedAt != failed.UpdatedAt {
		t.Fatal("an identical replay must not rebuild the summary")
	}

	finish(`{"status":"PASS"}`) // el reintento terminó bien
	waitAI(t, srv)
	if rt := runSummary(t, srv, rid); rt.State != "DONE" || len(rt.Incidents) != 0 {
		t.Fatalf("FAIL -> PASS must clear the incident: %+v", rt)
	}
	if r, _ := srv.Store.GetRun(rid); r.Status != "PASS" {
		t.Fatalf("run status: %+v", r)
	}
}

// Con IA: el FAIL tardío se diagnostica y el resumen se rehace, sin volver a notificar el cierre.
func TestLateResultRebuildsSummaryWithAI(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"message":{"content":"{\"category\":\"BACKEND_TIMEOUT\",\"summary\":\"El pago no respondió\",\"suggestion\":\"x\",\"headline\":\"Pagos caídos\",\"incidents\":[]}"}}`))
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: llm.URL})

	rid, _ := srv.Store.CreateRun("tardío", "qa")
	tid, _ := srv.Store.CreateTest(rid, "pagar", "", "")
	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(tid)+"/finish", `{"status":"PASS"}`)
	call(t, srv, "PATCH", "/api/v1/runs/"+itoa(rid)+"/finish", `{}`)
	waitAI(t, srv)
	before := calls.Load()

	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(tid)+"/finish", `{"status":"FAIL","error_message":"TimeoutError"}`)
	waitAI(t, srv)
	rt := runSummary(t, srv, rid)
	if rt.State != "DONE" || len(rt.Incidents) != 1 {
		t.Fatalf("late FAIL with AI: %+v", rt)
	}
	if calls.Load() == before {
		t.Fatal("the late failure was not diagnosed")
	}
	after := calls.Load()
	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(tid)+"/finish", `{"status":"FAIL","error_message":"TimeoutError"}`)
	waitAI(t, srv)
	if calls.Load() != after {
		t.Fatalf("an identical replay must not spend AI: %d -> %d", after, calls.Load())
	}
}

// Una instalación anterior tiene claves de idempotencia sin run_id. Al actualizar se vinculan a
// su ejecución: un spool reenviado días después no duplica nada, y al purgar la ejecución sus
// claves se van con ella.
func TestLegacyIdempotencyKeysSurviveUpgrade(t *testing.T) {
	clearAIEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Store: store, AI: ai.New(store), ScreenshotsDir: dir, Web: fstest.MapFS{}}

	var run struct {
		RunID int64 `json:"run_id"`
	}
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"r"}`, header("Idempotency-Key", "run-1")).Body.Bytes(), &run)
	rid := itoa(run.RunID)
	var created struct {
		TestID int64 `json:"test_id"`
	}
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs/"+rid+"/tests", `{"name":"t"}`, header("Idempotency-Key", "test-1")).Body.Bytes(), &created)
	tid := itoa(created.TestID)
	shot := func() *httptest.ResponseRecorder {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("file", "s.png")
		fw.Write([]byte("\x89PNG\r\n\x1a\n0000"))
		mw.Close()
		return call(t, srv, "POST", "/api/v1/tests/"+tid+"/screenshot", b.String(),
			header("Content-Type", mw.FormDataContentType()), header("Idempotency-Key", "shot-1"))
	}
	send := func() {
		t.Helper()
		for _, rec := range []*httptest.ResponseRecorder{
			call(t, srv, "POST", "/api/v1/runs", `{"name":"r"}`, header("Idempotency-Key", "run-1")),
			call(t, srv, "POST", "/api/v1/runs/"+rid+"/tests", `{"name":"t"}`, header("Idempotency-Key", "test-1")),
			call(t, srv, "POST", "/api/v1/tests/"+tid+"/logs", `{"status":"INFO","message":"first"}`, header("Idempotency-Key", "log-1")),
			call(t, srv, "POST", "/api/v1/tests/"+tid+"/network", `{"connections":[{"method":"GET","url":"https://x.test/a","status":200}]}`, header("Idempotency-Key", "net-1")),
			shot(),
		} {
			if rec.Code >= 300 {
				t.Fatalf("send: %d %s", rec.Code, rec.Body)
			}
		}
	}
	send()
	store.Close()

	// esquema anterior: la tabla sin run_id, con las respuestas de hace 3 días
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE idem_old AS SELECT key, status, body, created_at FROM idempotency`,
		`DROP TABLE idempotency`,
		`CREATE TABLE idempotency (key TEXT PRIMARY KEY, status INTEGER NOT NULL, body BLOB NOT NULL, created_at INTEGER NOT NULL)`,
		`INSERT INTO idempotency SELECT key, status, body, created_at FROM idem_old`,
		`DROP TABLE idem_old`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if _, err := conn.Exec(`UPDATE idempotency SET created_at=?`, db.NowMs()-3*24*3600*1000); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	// arranque de la versión nueva: migración + backfill (dos veces: es idempotente)
	for i := 0; i < 2; i++ {
		if store, err = db.Open(path); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			store.Close()
		}
	}
	defer store.Close()
	srv.Store, srv.AI = store, ai.New(store)
	call(t, srv, "POST", "/api/v1/runs/"+rid+"/tests", `{"name":"otro"}`, header("Idempotency-Key", "trigger-prune"))
	send() // reenvío del spool tras actualizar

	runs, _ := store.ListRuns(10)
	got, _ := store.GetTest(created.TestID)
	conns, _ := store.ListNetwork(created.TestID)
	if len(runs) != 1 || runs[0].Total != 2 || len(got.Logs) != 2 || len(conns) != 1 {
		t.Fatalf("legacy keys not migrated: runs=%d tests=%d logs=%d network=%d", len(runs), runs[0].Total, len(got.Logs), len(conns))
	}

	// la retención borra la ejecución y, con ella, sus claves
	store.FinishRun(run.RunID)
	if n, _, err := store.PurgeRunsBefore(db.NowMs() + 1); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	for _, k := range []string{"POST /api/v1/runs run-1", "POST /api/v1/tests/" + tid + "/logs log-1"} {
		if _, _, found, _ := store.IdempotentResponse(k); found {
			t.Fatalf("key %q survived the purge of its run", k)
		}
	}
}

// Un test ya diagnosticado que recibe otro FAIL con un error distinto se vuelve a diagnosticar
// (el diagnóstico de A no queda como vigente para B), también si B llega mientras se analiza A;
// un reenvío idéntico no gasta IA y FAIL -> PASS no deja un diagnóstico de fallo.
func TestChangedFailureReanalyzesTest(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	var testCalls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		if !strings.Contains(body, "Classify the root cause") { // resumen de la ejecución
			w.Write([]byte(`{"message":{"content":"{\"headline\":\"h\",\"summary\":\"s\",\"incidents\":[]}"}}`))
			return
		}
		testCalls.Add(1)
		time.Sleep(150 * time.Millisecond) // deja llegar el resultado nuevo durante el análisis
		cat, sum := "BACKEND_TIMEOUT", "old diagnosis"
		if strings.Contains(body, "NEW_FAILURE") {
			cat, sum = "LOCATOR_CHANGED", "new diagnosis"
		}
		w.Write([]byte(`{"message":{"content":"{\"category\":\"` + cat + `\",\"summary\":\"` + sum + `\",\"suggestion\":\"x\"}"}}`))
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: llm.URL})
	triage := func(id int64) *db.Triage {
		t.Helper()
		got, err := srv.Store.GetTest(id)
		if err != nil {
			t.Fatal(err)
		}
		return got.Triage
	}
	finish := func(id int64, body string) {
		t.Helper()
		if rec := call(t, srv, "PATCH", "/api/v1/tests/"+itoa(id)+"/finish", body); rec.Code != 200 {
			t.Fatalf("finish: %d %s", rec.Code, rec.Body)
		}
	}
	const oldFail = `{"status":"FAIL","error_message":"OLD_FAILURE: backend timeout"}`
	const newFail = `{"status":"FAIL","error_message":"NEW_FAILURE: locator missing"}`

	// A -> cierre -> B
	rid, _ := srv.Store.CreateRun("cambiado", "qa")
	tid, _ := srv.Store.CreateTest(rid, "pagar", "", "")
	finish(tid, oldFail)
	waitAI(t, srv)
	call(t, srv, "PATCH", "/api/v1/runs/"+itoa(rid)+"/finish", `{}`)
	waitAI(t, srv)
	finish(tid, newFail)
	waitAI(t, srv)
	if tr := triage(tid); tr == nil || tr.State != "DONE" || tr.Category != "LOCATOR_CHANGED" || tr.Summary != "new diagnosis" {
		t.Fatalf("changed failure kept the old diagnosis: %+v", tr)
	}
	calls := testCalls.Load()
	finish(tid, newFail) // reenvío idéntico
	waitAI(t, srv)
	if testCalls.Load() != calls {
		t.Fatalf("an identical replay must not spend AI: %d -> %d", calls, testCalls.Load())
	}
	// FAIL -> PASS: no queda un diagnóstico de fallo vigente
	finish(tid, `{"status":"PASS"}`)
	waitAI(t, srv)
	if tr := triage(tid); tr != nil {
		t.Fatalf("a passing test must not show a failure diagnosis: %+v", tr)
	}

	// B llega mientras se analiza A: gana el diagnóstico de B
	t2, _ := srv.Store.CreateTest(rid, "login", "", "")
	finish(t2, oldFail)
	time.Sleep(50 * time.Millisecond) // A está en el proveedor
	finish(t2, newFail)
	if tr := triage(t2); tr == nil || tr.State != "PENDING" {
		t.Fatalf("while B is analyzed the diagnosis must be pending: %+v", tr)
	}
	waitAI(t, srv)
	if tr := triage(t2); tr == nil || tr.State != "DONE" || tr.Category != "LOCATOR_CHANGED" {
		t.Fatalf("an analysis of the old result overwrote the new one: %+v", tr)
	}
}

// FAIL A -> FAIL B -> PASS mientras A sigue en el proveedor: el test aprobado no queda con un
// diagnóstico de fallo ni se pide un segundo diagnóstico (con respuesta correcta o con error del
// proveedor). Si B sigue en FAIL, el diagnóstico final es el de B. El orden se fija con canales.
func TestQueuedDiagnosisAfterPass(t *testing.T) {
	for _, tc := range []struct {
		name       string
		providerOK bool
		final      string // último resultado
	}{
		{"pass, provider ok", true, `{"status":"PASS"}`},
		{"pass, provider error", false, `{"status":"PASS"}`},
		{"still failing", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAIEnv(t)
			srv, _ := newTestServer(t)
			started, unblock := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(b), "Classify the root cause") {
					w.Write([]byte(`{"message":{"content":"{\"headline\":\"h\",\"summary\":\"s\",\"incidents\":[]}"}}`))
					return
				}
				first := calls.Add(1) == 1
				if first {
					close(started)
					<-unblock
					if !tc.providerOK {
						http.Error(w, "boom", http.StatusInternalServerError)
						return
					}
				}
				cat := "BACKEND_TIMEOUT"
				if strings.Contains(string(b), "ERROR_B") {
					cat = "LOCATOR_CHANGED"
				}
				w.Write([]byte(`{"message":{"content":"{\"category\":\"` + cat + `\",\"summary\":\"failure diagnosis\",\"suggestion\":\"x\"}"}}`))
			}))
			defer llm.Close()
			srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: llm.URL})

			rid, _ := srv.Store.CreateRun("encolado", "qa")
			tid, _ := srv.Store.CreateTest(rid, "test", "", "")
			endpoint := "/api/v1/tests/" + itoa(tid) + "/finish"
			call(t, srv, "PATCH", endpoint, `{"status":"FAIL","error_message":"ERROR_A"}`)
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				close(unblock)
				t.Fatal("the analysis of A did not start")
			}
			call(t, srv, "PATCH", endpoint, `{"status":"FAIL","error_message":"ERROR_B"}`)
			if tc.final != "" {
				call(t, srv, "PATCH", endpoint, tc.final)
			}
			close(unblock)
			waitAI(t, srv)

			got, err := srv.Store.GetTest(tid)
			if err != nil {
				t.Fatal(err)
			}
			if tc.final != "" {
				if got.Status != "PASS" || got.Triage != nil {
					t.Fatalf("a passing test kept a failure diagnosis: status=%s triage=%+v", got.Status, got.Triage)
				}
				if n := calls.Load(); n != 1 {
					t.Fatalf("no second diagnosis may be requested for the PASS: %d calls", n)
				}
				return
			}
			if got.Triage == nil || got.Triage.State != "DONE" || got.Triage.Category != "LOCATOR_CHANGED" {
				t.Fatalf("B still failing must be diagnosed: %+v", got.Triage)
			}
		})
	}
}
