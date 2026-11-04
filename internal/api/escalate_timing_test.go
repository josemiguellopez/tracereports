package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

type escTiming struct {
	Source string `json:"source"`
	Timing *struct {
		TotalMs    int64 `json:"total_ms"`
		ProviderMs int64 `json:"provider_ms"`
		WaitMs     int64 `json:"wait_ms"`
		OwnMs      int64 `json:"own_ms"`
		Calls      int   `json:"calls"`
		Attempts   []struct {
			Ms        int64  `json:"ms"`
			OK        bool   `json:"ok"`
			Status    int    `json:"status"`
			Reason    string `json:"reason"`
			WaitMs    int64  `json:"wait_ms"`
			WaitAsked bool   `json:"wait_asked"`
		} `json:"attempts"`
	} `json:"timing"`
}

const escReply = `{"choices":[{"message":{"content":"{\"title\":\"t\",\"severity\":\"high\",\"headline\":\"h\",\"what_happened\":\"w\",\"impact\":\"i\",\"evidence\":[\"e\"],\"root_cause\":\"r\",\"next_steps\":[\"n\"],\"owner\":\"o\"}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`

func escalateWith(t *testing.T, srv *Server, runID, testID int64, body string) escTiming {
	t.Helper()
	if body == "" {
		raw, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "dev", "lang": "es", "regenerate": true})
		body = string(raw)
	}
	rec := call(t, srv, "POST", "/api/v1/ui/escalate", body)
	var out escTiming
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("escalate: %d %s", rec.Code, rec.Body)
	}
	return out
}

// Escalar con IA informa cuánto tardó, separando al proveedor (que aquí tarda 250 ms) de
// TraceReports; la plantilla (sin IA) no trae tiempos.
func TestEscalationReportsHowLongTheAITook(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(escReply))
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: llm.URL, Model: "gpt-x"})
	runID, testID := failingTestIn(t, srv, "shop")
	e := escalateWith(t, srv, runID, testID, "")
	tm := e.Timing
	if e.Source != "ai" || tm == nil || tm.Calls != 1 || tm.ProviderMs < 250 || tm.TotalMs < tm.ProviderMs || tm.WaitMs != 0 {
		t.Fatalf("timing: %+v %+v", e, tm)
	}
	if tm.OwnMs != tm.TotalMs-tm.ProviderMs-tm.WaitMs || tm.OwnMs > 2000 {
		t.Fatalf("TraceReports' own time: %+v", tm)
	}
	raw, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "dev", "lang": "es", "no_ai": true})
	if tpl := escalateWith(t, srv, runID, testID, string(raw)); tpl.Source != "template" || tpl.Timing != nil {
		t.Fatalf("template: %+v", tpl)
	}
}

// Con un reintento (el proveedor saturado responde 503), se cuentan las dos llamadas y la pausa.
func TestEscalationTimingCountsRetriesAndPauses(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	var n atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		w.Write([]byte(escReply))
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: llm.URL, Model: "gpt-x"})
	runID, testID := failingTestIn(t, srv, "shop")
	tm := escalateWith(t, srv, runID, testID, "").Timing
	if tm == nil || tm.Calls != 2 || tm.WaitMs < 4900 || tm.TotalMs < tm.WaitMs {
		t.Fatalf("retry timing: %+v", tm)
	}
	if at := tm.Attempts; len(at) != 2 || at[0].OK || at[0].Status != 503 || at[0].Reason != "server" || at[0].WaitMs < 4900 || at[0].WaitAsked || !at[1].OK {
		t.Fatalf("each call: %+v", tm.Attempts)
	}
}

// Una ejecución sin fallos (aunque tenga llamadas de red abortadas que no rompieron el flujo) no se
// le pide a la IA que escale un fallo: lo inventaba. Terminada en verde, la IA resume qué se validó,
// con la evidencia de sus tests y las observaciones; si aun así describe un fallo, se descarta. Uno
// inventado que haya quedado en caché de antes ya no se entrega.
func TestEscalationOfAGreenRunSummarizesWhatWasValidated(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	var calls atomic.Int32
	var prompts []string
	var reply atomic.Value
	reply.Store(`{"title":"Inscripción de usuario: PASSED WITH OBSERVACIONES","headline":"El flujo pasó completo.","what_happened":"Se inscribió un usuario con RUT normal.","impact":"La versión puede avanzar.","evidence":["1 de 1 tests pasaron","3 imágenes abortadas sin afectar el flujo"],"next_steps":["No se requiere acción"]}`)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) > 0 {
			prompts = append(prompts, body.Messages[0].Content)
		}
		out, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": reply.Load()}}}})
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: llm.URL, Model: "llama3.1:8b"})

	runID, testID := newRunWithTest(t, srv)
	call(t, srv, "POST", "/api/v1/tests/"+itoa(testID)+"/network",
		`{"connections":[{"method":"GET","url":"https://app.example/api/foto.png","failed":true,"error_text":"net::ERR_ABORTED"}]}`)
	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(testID)+"/finish", `{"status":"PASS"}`)
	call(t, srv, "PATCH", "/api/v1/runs/"+itoa(runID)+"/finish", `{}`)

	// un resumen inventado guardado por una versión anterior
	facts, err := ai.BuildFacts(srv.Store, srv.shots(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	invented, _ := json.Marshal(ai.Escalation{Title: "Automated test failure in login service", Source: "ai", Severity: "high"})
	if err := srv.Store.SaveEscalation(runID, 0, "business", "es", factsRevision(facts), string(invented)); err != nil {
		t.Fatal(err)
	}

	escalate := func(audience string, regenerate bool) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": 0, "audience": audience, "lang": "es", "regenerate": regenerate})
		rec := call(t, srv, "POST", "/api/v1/ui/escalate", string(raw))
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", audience, rec.Code, rec.Body)
		}
		return out
	}
	for _, audience := range []string{"business", "qa", "dev"} {
		out := escalate(audience, false)
		// el veredicto en el idioma pedido, aunque el modelo lo copie en inglés
		if out["source"] != "ai" || out["kind"] != ai.KindSuccess || out["severity"] != "low" || out["title"] != "Inscripción de usuario: pasó con observaciones" {
			t.Fatalf("%s: a summary of what was validated: %v", audience, out)
		}
		// la evidencia la arma TraceReports con las señales, no el modelo
		if ev := fmt.Sprint(out["evidence"]); !strings.Contains(ev, "net::ERR_ABORTED") || strings.Contains(ev, "3 imágenes abortadas") {
			t.Fatalf("%s: evidence from the data: %s", audience, ev)
		}
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("one AI call per audience (the invented cached one is not served): %d", n)
	}
	p := prompts[0]
	for _, want := range []string{"FINISHED\nWITHOUT FAILURES", "Never describe, suggest or invent one", "t1 [PASS]", "net::ERR_ABORTED", "none of them made a test fail", "PASSED WITH OBSERVATIONS"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the prompt carries %q:\n%s", want, p)
		}
	}
	// la del modelo que de todos modos inventa un fallo se descarta
	reply.Store(`{"title":"Automated test failure in login service","headline":"The login service failed.","what_happened":"The test failed.","impact":"x","evidence":["e"],"next_steps":["n"]}`)
	out := escalate("business", true)
	if out["source"] != "template" || out["severity"] != "low" || out["ai_skipped"] != "invented_failure" || out["ai_error"] != nil {
		t.Fatalf("invented failure discarded: %v", out)
	}
}

// Lo que cuenta como "describir un fallo" en el resumen de una ejecución verde, y lo que no.
func TestClaimsFailure(t *testing.T) {
	for text, want := range map[string]bool{
		"The test failed with status FAIL":                     true,
		"El test falló por un RUT inválido":                    true,
		"Hay un bug en la validación":                          true,
		"The login service is down":                            true,
		"Ningún test falló: la ejecución terminó sin fallos.": false,
		"1 pasaron, 0 fallaron, 0 omitidos":                    false,
		"No failures; 3 aborted calls did not affect the flow": false,
		"Se validó la inscripción de un usuario con RUT normal": false,
		"3 llamadas fallaron (net::ERR_ABORTED) sin afectar el flujo": false,
		"Two image requests failed to load, the flow passed":   false,
	} {
		if got := ai.ClaimsFailure(text); got != want {
			t.Errorf("ClaimsFailure(%q) = %v, want %v", text, got, want)
		}
	}
}

// El veredicto de QA de una ejecución sin fallos: todo verde no es "todo bien". Un test que pasó al
// reintentar, un 500 que el test no detectó o una excepción de JavaScript son riesgos; llamadas
// abortadas o un console.error, observaciones. Sin IA, la plantilla lo dice con sus pruebas.
func TestGreenRunQAVerdict(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	mk := func(name string) (int64, func(string) int64) {
		var r map[string]int64
		json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"`+name+`"}`).Body.Bytes(), &r)
		run := r["run_id"]
		return run, func(test string) int64 {
			json.Unmarshal(call(t, srv, "POST", "/api/v1/runs/"+itoa(run)+"/tests", `{"name":"`+test+`"}`).Body.Bytes(), &r)
			id := r["test_id"]
			call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/logs", `{"status":"PASS","message":"paso"}`)
			return id
		}
	}
	escalate := func(run int64, lang string) map[string]any {
		raw, _ := json.Marshal(map[string]any{"run_id": run, "test_id": 0, "audience": "qa", "lang": lang, "no_ai": true})
		var out map[string]any
		json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(raw)).Body.Bytes(), &out)
		return out
	}

	// limpia
	clean, test := mk("limpia")
	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(test("login"))+"/finish", `{"status":"PASS"}`)
	call(t, srv, "PATCH", "/api/v1/runs/"+itoa(clean)+"/finish", `{}`)
	out := escalate(clean, "es")
	qa, _ := out["facts"].(map[string]any)["assessment"].(map[string]any)
	if qa["verdict"] != "clean" || out["severity"] != "low" || !strings.Contains(fmt.Sprint(out["title"]), "todos los tests pasaron") {
		t.Fatalf("clean run: %v", out)
	}

	// con riesgos y observaciones
	risky, test := mk("con riesgos")
	retried := test("reintentado")
	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(retried)+"/finish", `{"status":"PASS","attempts":2}`)
	backend := test("backend 500")
	call(t, srv, "POST", "/api/v1/tests/"+itoa(backend)+"/network", `{"connections":[
		{"method":"POST","url":"https://app.example/api/orders","status":500},
		{"method":"GET","url":"https://app.example/img/logo.png","failed":true,"error_text":"net::ERR_ABORTED"}]}`)
	call(t, srv, "POST", "/api/v1/tests/"+itoa(backend)+"/console", `{"entries":[
		{"level":"pageerror","text":"TypeError: Cannot read properties of undefined"},{"level":"error","text":"Failed to load resource"}]}`)
	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(backend)+"/finish", `{"status":"PASS"}`)
	call(t, srv, "PATCH", "/api/v1/runs/"+itoa(risky)+"/finish", `{}`)

	out = escalate(risky, "es")
	qa, _ = out["facts"].(map[string]any)["assessment"].(map[string]any)
	kinds := map[string]string{}
	for _, s := range qa["signals"].([]any) {
		m := s.(map[string]any)
		kinds[fmt.Sprint(m["kind"])] = fmt.Sprint(m["level"])
	}
	want := map[string]string{"retried": "risk", "server_error": "risk", "page_error": "risk", "aborted": "note", "console": "note"}
	for k, lvl := range want {
		if kinds[k] != lvl {
			t.Errorf("signal %s: %q (all: %v)", k, kinds[k], kinds)
		}
	}
	if qa["verdict"] != "risks" || out["severity"] != "medium" || !strings.Contains(fmt.Sprint(out["title"]), "pasó con riesgos") {
		t.Fatalf("risky run: %v", out)
	}
	ev := fmt.Sprint(out["evidence"])
	if !strings.Contains(ev, "pasó solo al reintentar (2 intentos)") || !strings.Contains(ev, "POST https://app.example/api/orders -> HTTP 500") {
		t.Fatalf("the evidence names each signal: %s", ev)
	}
	if steps := fmt.Sprint(out["next_steps"]); !strings.Contains(steps, "Agregar aserciones sobre las respuestas del backend") {
		t.Fatalf("next steps for the signals: %s", steps)
	}
	if en := escalate(risky, "en"); !strings.Contains(fmt.Sprint(en["title"]), "passed with risks") ||
		!strings.Contains(fmt.Sprint(en["evidence"]), "passed only after retrying (2 attempts)") {
		t.Fatalf("in English: %v", en)
	}
}
