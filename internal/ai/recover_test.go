package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// llmMock answers like Ollama with a JSON valid for both the test triage and the run summary.
func llmMock(t *testing.T, calls *int32, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		time.Sleep(delay)
		w.Write([]byte(`{"message":{"content":"{\"category\":\"LOGIC_BUG\",\"summary\":\"s\",\"suggestion\":\"x\",\"headline\":\"h\",\"incidents\":[]}"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitAll(t *testing.T, a *Analyzer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.Wait(ctx)
}

func TestRecoverResumesPendingWork(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "Pago", "AssertionError: x", "", 0)
	store.FinishRun(runID)
	// estado que deja un reinicio a mitad de camino
	store.SetTriagePending(id)
	store.SetRunTriagePending(runID)

	var calls int32
	a := New(store)
	a.SetConfig(Config{Provider: "ollama", BaseURL: llmMock(t, &calls, 0).URL})
	notified := make(chan int64, 1)
	a.Recover(func(r int64) { notified <- r })
	waitAll(t, a)
	got, _ := store.GetTest(id)
	rt, _ := store.GetRunTriage(runID)
	if got.Triage.State != "DONE" || rt.State != "DONE" {
		t.Fatalf("pending work must be resumed: test=%+v run=%+v", got.Triage, rt)
	}
	select {
	case r := <-notified:
		if r != runID {
			t.Fatalf("notified run %d", r)
		}
	default:
		t.Fatal("the recovered run diagnosis must notify like a finished run")
	}

	// sin IA configurada: el pendiente se cierra con un error explicado, no queda colgado
	store.SetTriagePending(id)
	clearEnv(t)
	New(store).Recover(nil)
	if got, _ := store.GetTest(id); got.Triage.State != "ERROR" || got.Triage.Error == "" {
		t.Fatalf("pending without AI must become ERROR: %+v", got.Triage)
	}
}

func TestNoDuplicateAnalysesAndBudget(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	a1 := failAt(t, store, runID, "A", "AssertionError: a", "", 0)
	a2 := failAt(t, store, runID, "B", "AssertionError: b", "", 0)

	var calls int32
	a := New(store)
	a.MaxPerRun = 1
	a.SetConfig(Config{Provider: "ollama", BaseURL: llmMock(t, &calls, 200*time.Millisecond).URL})
	if !a.AnalyzeAsync(a1) || a.AnalyzeAsync(a1) || a.ReanalyzeAsync(a1) {
		t.Fatal("a test being analyzed must not be queued twice")
	}
	waitAll(t, a)
	if a.AnalyzeAsync(a1) {
		t.Fatal("an automatic request for an already diagnosed test must not pay again")
	}
	if a.AnalyzeAsync(a2) {
		t.Fatal("past the per-run budget the test must not be analyzed automatically")
	}
	if got, _ := store.GetTest(a2); got.Triage == nil || got.Triage.State != "SKIPPED" {
		t.Fatalf("budget exceeded must be explicit: %+v", got.Triage)
	}
	if !a.ReanalyzeAsync(a2) { // a pedido de la persona sí
		t.Fatal("Re-analizar ignores the budget")
	}
	waitAll(t, a)
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("AI calls: %d (want 2)", n)
	}
}

func TestRunSummaryIsRewrittenWhenLateAnalysesFinish(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "A", "AssertionError: a", "", 0)
	store.FinishRun(runID)
	// el resumen se escribió mientras el test seguía pendiente
	store.SetTriagePending(id)
	store.SaveRunTriage(runID, &db.RunTriage{State: "DONE", PendingTests: 1, Incidents: []db.Incident{}})

	var calls int32
	a := New(store)
	a.SetConfig(Config{Provider: "ollama", BaseURL: llmMock(t, &calls, 0).URL})
	a.ReanalyzeAsync(id)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		waitAll(t, a)
		if rt, _ := store.GetRunTriage(runID); rt.State == "DONE" && rt.PendingTests == 0 && len(rt.Incidents) == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	rt, _ := store.GetRunTriage(runID)
	t.Fatalf("the run summary must be rebuilt once no analysis is pending: %+v", rt)
}

func TestRunSummaryKeepsIncidentsBeyondTheAILimit(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	for i := 0; i < 11; i++ {
		failAt(t, store, runID, string(rune('A'+i)), "ValueError: field "+string(rune('a'+i)), "", 0)
	}
	store.FinishRun(runID)
	var calls int32
	a := New(store)
	a.SetConfig(Config{Provider: "ollama", BaseURL: llmMock(t, &calls, 0).URL})
	a.AnalyzeRunAsync(runID, nil)
	waitAll(t, a)
	rt, _ := store.GetRunTriage(runID)
	if !rt.AI || len(rt.Incidents) != 11 || rt.AIIncidents != maxIncidentsForAI {
		t.Fatalf("all incidents kept, only %d sent to the AI: ai=%v total=%d ai_incidents=%d", maxIncidentsForAI, rt.AI, len(rt.Incidents), rt.AIIncidents)
	}
}
