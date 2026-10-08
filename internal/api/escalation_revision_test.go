package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

// fakeEscalationAI answers every call with a summary and counts them. hold, when set, makes the
// first call wait until it is closed (an AI that answers late).
func fakeEscalationAI(t *testing.T, srv *Server, hold chan struct{}) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	var once sync.Once
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 && hold != nil {
			once.Do(func() { <-hold })
		}
		content := `{"title":"fake summary","severity":"medium","headline":"fake headline","what_happened":"fake","impact":"fake","evidence":[],"root_cause":"fake cause","next_steps":[],"owner":"qa","category":"LOGIC_BUG","summary":"fake","suggestion":"inspect","incidents":[]}`
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	t.Cleanup(fake.Close)
	if err := srv.AI.SetConfig(ai.Config{Provider: "openai_compatible", BaseURL: fake.URL, APIKey: "fake-model-token", Model: "fake-model"}); err != nil {
		t.Fatal(err)
	}
	return &calls
}

func escalateAs(t *testing.T, srv *Server, run, test int64) ai.Escalation {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"run_id": run, "test_id": test, "audience": "dev", "lang": "en"})
	rec := call(t, srv, "POST", "/api/v1/ui/escalate", string(body))
	var e ai.Escalation
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &e) != nil {
		t.Fatalf("escalate: %d %s", rec.Code, rec.Body)
	}
	return e
}

func cachedCode(t *testing.T, srv *Server, run, test int64) int {
	t.Helper()
	return call(t, srv, "GET", "/api/v1/runs/"+itoa(run)+"/escalation?test="+itoa(test)+"&audience=dev&lang=en", "").Code
}

// El escalamiento guardado se reutiliza mientras la evidencia no cambia; si cambia (un resultado
// que llega tarde, más fallos en la ejecución) no se muestra ni se comparte el viejo: se escribe
// de nuevo. Para un test y para la ejecución completa.
func TestEscalationCacheFollowsTheEvidence(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	calls := fakeEscalationAI(t, srv, nil)
	run, test := failingRun(t, srv)
	for _, target := range []int64{test, 0} {
		before := calls.Load()
		escalateAs(t, srv, run, target)
		if e := escalateAs(t, srv, run, target); e.Source != "ai" || calls.Load() != before+1 {
			t.Fatalf("target %d: unchanged evidence reuses the cache (calls %d -> %d)", target, before, calls.Load())
		}
		if code := cachedCode(t, srv, run, target); code != 200 {
			t.Fatalf("target %d: cached summary: %d", target, code)
		}
	}
	// el resultado del test cambia: el escalamiento del test y el de la ejecución quedan viejos
	const updated = "new failure from late evidence"
	if _, err := srv.Store.FinishTest(test, "FAIL", updated, ""); err != nil {
		t.Fatal(err)
	}
	for _, target := range []int64{test, 0} {
		if code := cachedCode(t, srv, run, target); code != http.StatusNoContent {
			t.Fatalf("target %d: a summary of other evidence is not served: %d", target, code)
		}
		before := calls.Load()
		e := escalateAs(t, srv, run, target)
		if calls.Load() != before+1 || !strings.Contains(e.Facts.Error+fmtFailed(e.Facts), updated) {
			t.Fatalf("target %d: written again from the new evidence (calls %d -> %d, facts %+v)", target, before, calls.Load(), e.Facts)
		}
	}
	// otro fallo en la ejecución: cambia la de la ejecución
	other, _ := srv.Store.CreateTest(run, "Otro", "", "")
	srv.Store.FinishTest(other, "FAIL", "another failure", "")
	if code := cachedCode(t, srv, run, 0); code != http.StatusNoContent {
		t.Fatalf("run summary after a new failure: %d", code)
	}
	// compartir (Teams/Slack, tickets) usa la misma regla: no reutiliza el viejo
	e, err := srv.escalationFor(t.Context(), escalationRef{RunID: run, Audience: "dev", Lang: "en"}, false)
	if err != nil || e.Facts.Failed != 2 {
		t.Fatalf("shared summary from the current evidence: %v %+v", err, e)
	}
}

func fmtFailed(f ai.Facts) string {
	var b strings.Builder
	for _, x := range f.FailedTests {
		b.WriteString(x.Error)
	}
	return b.String()
}

// Una generación que empezó con la evidencia vieja y termina tarde no deja su resumen guardado.
func TestLateEscalationDoesNotCacheOldEvidence(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	hold := make(chan struct{})
	calls := fakeEscalationAI(t, srv, hold)
	run, test := failingRun(t, srv)
	done := make(chan ai.Escalation)
	go func() { done <- escalateAs(t, srv, run, test) }()
	for calls.Load() == 0 { // la IA ya está escribiendo con la evidencia vieja
		runtime.Gosched()
		select {
		case <-done:
			t.Fatal("the AI call was not held")
		default:
		}
	}
	if _, err := srv.Store.FinishTest(test, "FAIL", "late result", ""); err != nil {
		t.Fatal(err)
	}
	close(hold)
	<-done
	if code := cachedCode(t, srv, run, test); code != http.StatusNoContent {
		t.Fatalf("the late summary of old evidence was cached: %d", code)
	}
	if e := escalateAs(t, srv, run, test); e.Facts.Error != "late result" || calls.Load() != 2 {
		t.Fatalf("next view writes it from the current evidence: %q calls=%d", e.Facts.Error, calls.Load())
	}
}
