package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// openAIMock answers a valid diagnosis and counts the calls.
func openAIMock(t *testing.T, calls *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"category\":\"LOGIC_BUG\",\"summary\":\"s\",\"suggestion\":\"s\"}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func budgetAnalyzer(t *testing.T, store *db.Store, max int, calls *atomic.Int32) *Analyzer {
	a := New(store)
	a.MaxPerRun = max
	if err := a.SetConfig(Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: openAIMock(t, calls).URL, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	return a
}

// finish replaces the test result and asks for the automatic analysis, like the API does.
func finish(t *testing.T, a *Analyzer, store *db.Store, id int64, status, msg string) bool {
	t.Helper()
	_, change, err := store.FinishTestChange(id, status, msg, "")
	if err != nil {
		t.Fatal(err)
	}
	if change.Changed {
		a.Supersede(id)
	}
	accepted := a.AnalyzeAsync(id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.Wait(ctx)
	return accepted
}

// Reproducción de la auditoría: con MaxPerRun=1, tres resultados FAIL sucesivos del mismo test (con
// errores distintos, diagnóstico terminado entre medio) no pueden pagar tres análisis automáticos.
func TestReplacingResultsDoesNotRecycleTheBudget(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	id, _ := store.CreateTest(runID, "t", "", "")
	var calls atomic.Int32
	a := budgetAnalyzer(t, store, 1, &calls)
	for i := 0; i < 3; i++ {
		finish(t, a, store, id, "FAIL", fmt.Sprintf("error %d", i))
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("MaxPerRun=1 but %d automatic provider calls", n)
	}
	if got, _ := store.GetTest(id); got.Triage == nil || got.Triage.State != "SKIPPED" {
		t.Fatalf("the later revision is skipped, explicitly: %+v", got.Triage)
	}
}

func TestBudgetAcrossFailPassFailAndIdenticalResends(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	id, _ := store.CreateTest(runID, "t", "", "")
	var calls atomic.Int32
	a := budgetAnalyzer(t, store, 1, &calls)
	finish(t, a, store, id, "FAIL", "boom")
	finish(t, a, store, id, "FAIL", "boom") // reenvío idéntico: misma revisión, ya diagnosticado
	if calls.Load() != 1 {
		t.Fatalf("an identical resend must not pay again: %d", calls.Load())
	}
	finish(t, a, store, id, "PASS", "")
	finish(t, a, store, id, "FAIL", "boom") // un FAIL nuevo es otro resultado: ya no hay cupo
	if calls.Load() != 1 {
		t.Fatalf("FAIL/PASS/FAIL with budget 1: %d calls", calls.Load())
	}
	// con cupo 2, el FAIL nuevo sí se analiza
	a.MaxPerRun = 2
	finish(t, a, store, id, "PASS", "")
	finish(t, a, store, id, "FAIL", "boom again")
	if calls.Load() != 2 {
		t.Fatalf("budget 2: %d calls", calls.Load())
	}
}

// El consumo sobrevive a un reinicio (otra apertura de la base) y a cambios de resultado.
func TestBudgetSurvivesRestart(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "b.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := store.CreateRun("r", "")
	id, _ := store.CreateTest(runID, "t", "", "")
	var calls atomic.Int32
	finish(t, budgetAnalyzer(t, store, 1, &calls), store, id, "FAIL", "e1")
	store.Close()

	store, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	finish(t, budgetAnalyzer(t, store, 1, &calls), store, id, "FAIL", "e2")
	if calls.Load() != 1 {
		t.Fatalf("after a restart the spent budget is still spent: %d calls", calls.Load())
	}
}

// PENDING o ERROR de la misma revisión (reinicio, fallo del proveedor) se retoman con su cupo.
func TestPendingAndErrorReuseTheirSlot(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "A", "AssertionError: a", "", 0)
	if out, _, _ := store.ReserveTriage(id, 1); out != db.TriageReserved {
		t.Fatalf("first: %s", out)
	}
	// quedó PENDING (p. ej. reinicio): se retoma aunque el cupo esté lleno
	if out, _, _ := store.ReserveTriage(id, 1); out != db.TriageReserved {
		t.Fatalf("pending again: %s", out)
	}
	rev, _ := store.TestResultRev(id)
	store.SaveTriageErrorAt(id, rev, "provider down")
	if out, _, _ := store.ReserveTriage(id, 1); out != db.TriageReserved {
		t.Fatalf("error retried within its slot: %s", out)
	}
	other := failAt(t, store, runID, "B", "AssertionError: b", "", 0)
	if out, _, _ := store.ReserveTriage(other, 1); out != db.TriageOverBudget {
		t.Fatalf("another test past the budget: %s", out)
	}
}

// Re-analizar a mano no cuenta para el límite automático (excepción explícita).
func TestManualReanalysisIsOutsideTheAutomaticBudget(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	a1 := failAt(t, store, runID, "A", "AssertionError: a", "", 0)
	a2 := failAt(t, store, runID, "B", "AssertionError: b", "", 0)
	var calls atomic.Int32
	a := budgetAnalyzer(t, store, 1, &calls)
	if !a.ReanalyzeAsync(a1) {
		t.Fatal("manual")
	}
	waitAll(t, a)
	if !a.AnalyzeAsync(a2) { // el manual no gastó el cupo automático
		t.Fatal("the automatic slot is still free after a manual re-analysis")
	}
	waitAll(t, a)
	if calls.Load() != 2 {
		t.Fatalf("calls: %d", calls.Load())
	}
}
