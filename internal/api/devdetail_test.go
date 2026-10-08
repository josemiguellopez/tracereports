package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

// devRun: un test que pasó en la ejecución 1 y falló en la 2, con red, consola y stack trace.
func devRun(t *testing.T, srv *Server) (runID, testID int64) {
	t.Helper()
	meta := db.RunMeta{Project: "shop", Branch: "main", Commit: "abc123def4567890", Framework: "pytest"}
	tm := db.TestMeta{Key: "tests/test_pay.py::test_card"}
	green, _ := srv.Store.CreateRunWithMeta("Checkout", "staging", meta)
	okID, _ := srv.Store.CreateTestWithMeta(green, "Pago con tarjeta", "", "", tm)
	srv.Store.AddNetwork(okID, []db.NetConn{{Method: "POST", URL: "https://shop.test/api/payments/charge", Status: 200, ResponseBody: `{"ok":true}`}})
	srv.Store.FinishTest(okID, "PASS", "", "")
	srv.Store.FinishRun(green)

	runID, _ = srv.Store.CreateRunWithMeta("Checkout", "staging", meta)
	testID, _ = srv.Store.CreateTestWithMeta(runID, "Pago con tarjeta", "", "", tm)
	dur := int64(812)
	srv.Store.AddNetwork(testID, []db.NetConn{
		{Method: "GET", URL: "https://shop.test/api/cart", Status: 200},
		{Method: "POST", URL: "https://shop.test/api/payments/charge", Status: 503, StatusText: "Service Unavailable", DurationMs: &dur,
			RequestHeaders: map[string]string{"Authorization": "<masked>", "Content-Type": "application/json"},
			PostData:       `{"amount":10,"password":"<masked>"}`, ResponseBody: `{"error":"db pool exhausted"}`},
	})
	srv.Store.AddConsole(testID, []db.ConsoleEntry{
		{Level: "warning", Text: "deprecated"},
		{Level: "pageerror", Text: "TypeError: x is undefined", Location: "app.js:10:5"},
	})
	srv.Store.FinishTest(testID, "FAIL", "TimeoutError: no llegó la confirmación", "Traceback (most recent call last):\n  File \"test_pay.py\", line 12")
	srv.Store.FinishRun(runID)
	return runID, testID
}

func TestEscalationForDevelopersCarriesTheTechnicalDetail(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	gh := &fakeTracker{id: "github"}
	srv.Trackers = []tracker.Provider{gh}
	runID, testID := devRun(t, srv)
	body := func(aud string) string {
		b, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": aud, "lang": "es"})
		return string(b)
	}

	var e ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", body("dev")).Body.Bytes(), &e)
	if len(e.Facts.Dev) != 1 {
		t.Fatalf("dev escalation of one test: %+v", e.Facts.Dev)
	}
	d := e.Facts.Dev[0]
	if d.TestName != "Pago con tarjeta" {
		t.Fatalf("dev escalation without technical detail: %+v", e.Facts)
	}
	if d.Framework != "pytest" || d.Commit != "abc123def4567890" || d.Branch != "main" || !strings.Contains(d.ErrorTrace, "Traceback") {
		t.Fatalf("context: %+v", d)
	}
	if len(d.Repro) != 1 || d.Repro[0].Cmd != "git checkout abc123def456 && pytest 'tests/test_pay.py::test_card'" {
		t.Fatalf("repro: %+v", d.Repro)
	}
	if len(d.Console) != 1 || d.Console[0].Level != "pageerror" {
		t.Fatalf("only console errors: %+v", d.Console)
	}
	if len(d.Calls) != 1 {
		t.Fatalf("only failed calls: %+v", d.Calls)
	}
	c := d.Calls[0]
	if c.Outcome != "HTTP 503 Service Unavailable" || c.DurationMs != 812 || !strings.Contains(c.ResponseBody, "\"error\": \"db pool exhausted\"") {
		t.Fatalf("call: %+v", c)
	}
	for _, want := range []string{"curl -X POST 'https://shop.test/api/payments/charge'", `-H "Authorization: $AUTHORIZATION"`, `"password":"***"`} {
		if !strings.Contains(c.Curl, want) {
			t.Errorf("curl misses %q:\n%s", want, c.Curl)
		}
	}
	if c.Baseline == nil || c.Baseline.Status != 200 || !c.Baseline.SameContext {
		t.Fatalf("baseline: %+v", c.Baseline)
	}

	// negocio y QA no lo llevan
	var biz ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", body("business")).Body.Bytes(), &biz)
	if biz.Facts.Dev != nil {
		t.Fatal("business must not get the technical detail")
	}

	// el ticket para desarrollo lleva el cURL, el stack, la respuesta y la consola
	ticket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "audience": "dev", "provider": "github", "lang": "es", "no_ai": true}, 201)
	md := tracker.Markdown(gh.issues[0])
	for _, want := range []string{"### cURL · POST https://shop.test/api/payments/charge", `$AUTHORIZATION`, "Traceback", "### Reproducir en local",
		"db pool exhausted", "La última vez que el test pasó (ejecución #", "TypeError: x is undefined", "Commit: abc123def4567890"} {
		if !strings.Contains(md, want) {
			t.Errorf("ticket misses %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "<masked>") {
		t.Errorf("ticket leaks the mask placeholder:\n%s", md)
	}
}

func TestRunEscalationForDevelopersCarriesEachFailedTest(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	gh := &fakeTracker{id: "github"}
	srv.Trackers = []tracker.Provider{gh}
	runID, _ := devRun(t, srv)
	b, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": 0, "audience": "dev", "lang": "es"})
	var e ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(b)).Body.Bytes(), &e)
	if len(e.Facts.Dev) != 1 || e.Facts.Dev[0].TestName != "Pago con tarjeta" || len(e.Facts.Dev[0].Calls) != 1 ||
		!strings.Contains(e.Facts.Dev[0].Calls[0].Curl, "$AUTHORIZATION") {
		t.Fatalf("run summary for developers: %+v", e.Facts.Dev)
	}
	ticket(t, srv, map[string]any{"run_id": runID, "test_id": 0, "audience": "dev", "provider": "github", "lang": "es", "no_ai": true}, 201)
	if md := tracker.Markdown(gh.issues[0]); !strings.Contains(md, "### Pago con tarjeta · cURL · POST https://shop.test/api/payments/charge") {
		t.Errorf("run ticket names the test of each block:\n%s", md)
	}
}
