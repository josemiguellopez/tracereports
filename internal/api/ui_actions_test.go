package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/notify"
)

// failingRun crea una ejecución terminada con un test que falla por el backend (con captura y red).
func failingRun(t *testing.T, srv *Server) (runID, testID int64) {
	t.Helper()
	runID, _ = srv.Store.CreateRun("Checkout", "staging")
	ok, _ := srv.Store.CreateTest(runID, "Login", "login", "")
	srv.Store.FinishTest(ok, "PASS", "", "")
	testID, _ = srv.Store.CreateTest(runID, "Pago con tarjeta", "checkout, pagos", "El cliente paga")
	srv.Store.AddLog(testID, "INFO", "Click en Pagar", 0, "/screenshots/pago.png")
	srv.Store.AddNetwork(testID, []db.NetConn{{Method: "POST", URL: "https://shop.test/api/payments/charge", Status: 503, ResponseBody: `{"error":"down"}`}})
	srv.Store.FinishTest(testID, "FAIL", "TimeoutError: no llegó la confirmación", "")
	srv.Store.FinishRun(runID)
	return runID, testID
}

func TestEscalateTemplateAndAccess(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	runID, testID := failingRun(t, srv)
	body := func(aud, lang string, test int64) string {
		b, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": test, "audience": aud, "lang": lang})
		return string(b)
	}

	rec := call(t, srv, "POST", "/api/v1/ui/escalate", body("business", "es", testID))
	var e ai.Escalation
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &e) != nil {
		t.Fatalf("escalate: %d %s", rec.Code, rec.Body)
	}
	if e.Source != "template" || e.Severity != "high" || !strings.Contains(e.Title, "Pago con tarjeta") || e.Facts.Screenshot != "/screenshots/pago.png" {
		t.Fatalf("template escalation: %+v", e)
	}
	for _, ev := range append(e.Evidence, e.NextSteps...) { // negocio: sin códigos HTTP ni endpoints
		if strings.Contains(ev, "503") || strings.Contains(ev, "/api/") {
			t.Fatalf("business evidence must not be technical: %q", ev)
		}
	}
	var dev ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", body("dev", "en", testID)).Body.Bytes(), &dev)
	if !strings.Contains(strings.Join(dev.Evidence, " "), "/api/payments/charge") || !strings.Contains(dev.Title, "failed") {
		t.Fatalf("dev escalation in English with the failing endpoint: %+v", dev)
	}
	var run ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", body("qa", "es", 0)).Body.Bytes(), &run)
	if run.TestID != 0 || len(run.Facts.FailedTests) != 1 || run.Facts.Screenshot == "" {
		t.Fatalf("run escalation (uses the first failure as visual evidence): %+v", run.Facts)
	}

	if rec := call(t, srv, "POST", "/api/v1/ui/escalate", body("ceo", "es", 0)); rec.Code != 400 {
		t.Fatalf("unknown audience: %d", rec.Code)
	}
	if rec := call(t, srv, "POST", "/api/v1/ui/escalate", body("qa", "es", 0), fromAddr("10.0.0.9:1")); rec.Code != 403 {
		t.Fatalf("remote without login must not escalate: %d", rec.Code)
	}
	srv.Auth = Auth{Token: "tok"}
	if rec := call(t, srv, "POST", "/api/v1/ui/escalate", body("qa", "es", 0)); rec.Code != 403 {
		t.Fatalf("with a token the same machine needs credentials too: %d", rec.Code)
	}
	srv.Auth = Auth{Token: "tok", LocalAdmin: true}
	if rec := call(t, srv, "POST", "/api/v1/ui/escalate", body("qa", "es", 0)); rec.Code != 200 {
		t.Fatalf("TRACEREPORTS_LOCAL_ADMIN lets the same machine act: %d", rec.Code)
	}
	srv.Auth = Auth{}

	// sin IA: re-analizar responde 409; sin canal: enviar responde 502
	if rec := call(t, srv, "POST", "/api/v1/ui/tests/"+itoa(testID)+"/analyze", "{}"); rec.Code != 409 {
		t.Fatalf("reanalyze without AI: %d", rec.Code)
	}
	srv.Notify = notify.New(srv.Store)
	send, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "qa", "lang": "es", "channel": "teams"})
	if rec := call(t, srv, "POST", "/api/v1/ui/escalate/send", string(send)); rec.Code != 502 {
		t.Fatalf("send without webhook: %d %s", rec.Code, rec.Body)
	}
}

func TestEscalateWithAICacheAndSend(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	runID, testID := failingRun(t, srv)
	calls := 0
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"message":{"content":"{\"title\":\"Pagos caídos\",\"severity\":\"critical\",\"headline\":\"Nadie puede pagar.\",\"what_happened\":\"El servicio de pagos no respondió.\",\"impact\":\"Ventas detenidas.\",\"evidence\":[\"Captura\"],\"root_cause\":\"Servicio de pagos caído.\",\"next_steps\":[\"Revisar pagos\"],\"owner\":\"Backend\"}"}}`))
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: llm.URL})

	body, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "business", "lang": "es"})
	var e ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(body)).Body.Bytes(), &e)
	if e.Source != "ai" || e.Title != "Pagos caídos" || e.Severity != "critical" {
		t.Fatalf("AI escalation: %+v", e)
	}
	call(t, srv, "POST", "/api/v1/ui/escalate", string(body)) // de la caché: no vuelve a llamar a la IA
	if calls != 1 {
		t.Fatalf("cached escalation should not call the AI again: %d calls", calls)
	}
	if rec := call(t, srv, "GET", "/api/v1/runs/"+itoa(runID)+"/escalation?test="+itoa(testID)+"&audience=business&lang=es", ""); rec.Code != 200 {
		t.Fatalf("get cached: %d", rec.Code)
	}

	var got map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewDecoder(r.Body).Decode(&got) }))
	defer hook.Close()
	t.Setenv("SLACK_WEBHOOK_URL", hook.URL)
	t.Setenv("PUBLIC_URL", "https://tracereports.example.com")
	srv.Notify = notify.New(srv.Store)
	send, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "business", "lang": "es", "channel": "slack"})
	rec := call(t, srv, "POST", "/api/v1/ui/escalate/send", string(send))
	raw, _ := json.Marshal(got)
	if rec.Code != 200 || !strings.Contains(string(raw), "Pagos caídos") || !strings.Contains(string(raw), "https://tracereports.example.com/screenshots/pago.png") ||
		!strings.Contains(string(raw), "https://tracereports.example.com/#run=") {
		t.Fatalf("slack escalation: %d %s", rec.Code, raw)
	}
}

func TestRecurrence(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	inc := []db.Incident{{Key: "POST /api/payments/charge → HTTP 503", Title: "pagos"}}
	var last int64
	for i := 0; i < 3; i++ {
		last, _ = failingRun(t, srv)
		srv.Store.SaveRunTriage(last, &db.RunTriage{State: "DONE", Incidents: inc})
	}
	var rec map[string]db.Recurrence
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs/"+itoa(last)+"/recurrence", "").Body.Bytes(), &rec)
	if r := rec[inc[0].Key]; r.Seen != 2 || r.Of != 2 {
		t.Fatalf("recurrence: %+v", rec)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestEscalateNoAISkipsTheAI(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	runID, testID := failingRun(t, srv)
	calls := 0
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: llm.URL})
	body, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "qa", "lang": "es", "no_ai": true})
	var e ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(body)).Body.Bytes(), &e)
	if calls != 0 || e.Source != "template" || e.Title == "" {
		t.Fatalf("no_ai must use the template without calling the AI: calls=%d %+v", calls, e)
	}
}

func TestEscalateWithoutAIOnAPassingRunIsGreen(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	runID, _ := srv.Store.CreateRun("Checkout", "staging")
	for _, name := range []string{"Login", "Pago"} {
		id, _ := srv.Store.CreateTest(runID, name, "", "")
		srv.Store.FinishTest(id, "PASS", "", "")
	}
	srv.Store.FinishRun(runID)
	for _, aud := range []string{"business", "qa", "dev"} {
		body, _ := json.Marshal(map[string]any{"run_id": runID, "audience": aud, "lang": "es", "no_ai": true})
		var e ai.Escalation
		json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(body)).Body.Bytes(), &e)
		if e.Severity != "low" || e.Source != "template" || !strings.Contains(e.Headline, "pasaron") ||
			strings.Contains(e.WhatHappened, "no pudo") || len(e.NextSteps) == 0 {
			t.Fatalf("%s: a run without failures must be a green success summary: %+v", aud, e)
		}
	}
}

func TestEscalateWithoutAIUsesTheRunIncidents(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	runID, _ := srv.Store.CreateRun("Checkout", "staging")
	for i, msg := range []string{"AssertionError: total incorrecto", "AssertionError: total incorrecto", "ValueError: fecha inválida"} {
		id, _ := srv.Store.CreateTest(runID, "T"+itoa(int64(i)), "", "")
		srv.Store.FinishTest(id, "FAIL", msg, "")
	}
	srv.Store.FinishRun(runID)
	srv.Store.SaveRunTriage(runID, &db.RunTriage{State: "DONE", Incidents: []db.Incident{
		{Key: "a", Title: "AssertionError: total incorrecto", Kind: "error", TestIDs: []int64{1, 2}},
		{Key: "b", Title: "ValueError: fecha inválida", Kind: "error", TestIDs: []int64{3}},
	}})
	body, _ := json.Marshal(map[string]any{"run_id": runID, "audience": "qa", "lang": "es", "no_ai": true})
	var e ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(body)).Body.Bytes(), &e)
	if !strings.Contains(e.WhatHappened, "2 problemas distintos") || !strings.Contains(strings.Join(e.Evidence, " "), "fecha inválida (1 test)") ||
		!strings.Contains(strings.Join(e.NextSteps, " "), "total incorrecto") {
		t.Fatalf("run escalation without AI must explain the incidents: %+v", e)
	}
}
