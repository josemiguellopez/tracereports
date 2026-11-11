package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

// La causa probable del resumen sale del diagnóstico del motor local, que elige su idioma por los
// nombres de los tests: un test con nombre en inglés quedaba diagnosticado en inglés y el resumen en
// español lo mostraba así. El resumen la vuelve a escribir en su idioma (la misma regla); el
// diagnóstico guardado no cambia.
func TestEscalationWritesTheLocalDiagnosisInItsLanguage(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	runID, _ := srv.Store.CreateRun("shop/test_checkout.py", "qa")
	testID, _ := srv.Store.CreateTest(runID, "test_pay_with_saved_card", "", "")
	srv.Store.AddLog(testID, "FAIL", "TimeoutException waiting for the Pay button", 0, "")
	srv.Store.FinishTest(testID, "FAIL", "TimeoutException waiting for the Pay button", "")
	srv.Store.FinishRun(runID)
	_ = srv.AI.SetConfig(ai.Config{Provider: ai.ProviderBuiltin})
	if rec := call(t, srv, "POST", "/api/v1/ui/tests/"+itoa(testID)+"/analyze", "{}"); rec.Code != 202 {
		t.Fatalf("analyze: %d", rec.Code)
	}
	srv.AI.Wait(context.Background())
	stored, _ := srv.Store.GetTest(testID)
	if stored.Triage == nil || stored.Triage.Provider != ai.ProviderBuiltin || !strings.HasPrefix(stored.Triage.Summary, "The test ran out of time") {
		t.Fatalf("the engine should have diagnosed it in English (from the test name): %+v", stored.Triage)
	}
	escalate := func(lang string, noAI bool) ai.Escalation {
		body, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "qa", "lang": lang, "no_ai": noAI})
		rec := call(t, srv, "POST", "/api/v1/ui/escalate", string(body))
		var e ai.Escalation
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &e) != nil {
			t.Fatalf("escalate %s: %d %s", lang, rec.Code, rec.Body)
		}
		return e
	}
	for _, noAI := range []bool{true, false} { // sin IA, y con el motor local como proveedor
		es := escalate("es", noAI)
		if !strings.HasPrefix(es.RootCause, "El test agotó su tiempo de espera") || strings.Contains(es.RootCause, "The test") {
			t.Errorf("Spanish escalation (no_ai=%v): the probable cause should be in Spanish: %q", noAI, es.RootCause)
		}
		if en := escalate("en", noAI); !strings.HasPrefix(en.RootCause, "The test ran out of time") {
			t.Errorf("English escalation (no_ai=%v): %q", noAI, en.RootCause)
		}
	}
	if again, _ := srv.Store.GetTest(testID); again.Triage.Summary != stored.Triage.Summary {
		t.Fatalf("the stored diagnosis must not change: %q", again.Triage.Summary)
	}
}
