package ai

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Reproducción: un test SKIPPED por falta de cupo no se puede reservar mientras el cupo siga agotado.
func TestSkippedTestCannotTakeASlotPastTheBudget(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	first := failAt(t, store, runID, "A", "AssertionError: a", "", 0)
	second := failAt(t, store, runID, "B", "AssertionError: b", "", 0)
	if out, _, _ := store.ReserveTriage(first, 1); out != db.TriageReserved {
		t.Fatalf("first: %s", out)
	}
	store.SaveTriageSkipped(second, "sin cupo")
	if out, _, _ := store.ReserveTriage(second, 1); out != db.TriageOverBudget {
		t.Fatalf("a SKIPPED test must not take a slot past the budget: %s", out)
	}
	if got, _ := store.GetTest(second); got.Triage.State != "SKIPPED" {
		t.Fatalf("it stays SKIPPED: %+v", got.Triage)
	}
	// con cupo (máximo 2) sí se reserva
	if out, _, _ := store.ReserveTriage(second, 2); out != db.TriageReserved {
		t.Fatalf("with a free slot: %s", out)
	}
}

// PENDING y ERROR ya consumieron su cupo: se reservan de nuevo sin gastar otro. DONE se conserva.
func TestStatesThatAlreadyUsedTheirSlot(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	pending := failAt(t, store, runID, "P", "AssertionError: p", "", 0)
	failed := failAt(t, store, runID, "E", "AssertionError: e", "", 0)
	done := failAt(t, store, runID, "D", "AssertionError: d", "", 0)
	// cada uno gastó su cupo por la vía automática (la manual no cuenta para el límite)
	for _, id := range []int64{pending, failed, done} {
		if out, _, _ := store.ReserveTriage(id, 3); out != db.TriageReserved {
			t.Fatalf("setup: %s", out)
		}
	}
	rev, _ := store.TestResultRev(failed)
	store.SaveTriageErrorAt(failed, rev, "el proveedor falló")
	if got, _ := store.GetTest(failed); got.Triage.State != "ERROR" {
		t.Fatalf("setup: %+v", got.Triage)
	}
	store.SaveTriage(done, "LOGIC_BUG", "s", "x", "", "")
	// el cupo (3) está agotado por los tres
	if out, _, _ := store.ReserveTriage(pending, 3); out != db.TriageReserved {
		t.Fatalf("PENDING: %s", out)
	}
	if out, _, _ := store.ReserveTriage(failed, 3); out != db.TriageReserved {
		t.Fatalf("ERROR retries within its own slot: %s", out)
	}
	if out, _, _ := store.ReserveTriage(done, 3); out != db.TriageDone {
		t.Fatalf("DONE: %s", out)
	}
	newOne := failAt(t, store, runID, "N", "AssertionError: n", "", 0)
	if out, _, _ := store.ReserveTriage(newOne, 3); out != db.TriageOverBudget {
		t.Fatalf("a new test past the budget: %s", out)
	}
}

// Flujo automático: pedir de nuevo el análisis de un SKIPPED no gasta IA; Re-analizar sí (a propósito).
func TestAutomaticRepeatOfSkippedAndManualReanalysis(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	a1 := failAt(t, store, runID, "A", "AssertionError: a", "", 0)
	a2 := failAt(t, store, runID, "B", "AssertionError: b", "", 0)
	var calls int32
	a := New(store)
	a.MaxPerRun = 1
	a.SetConfig(Config{Provider: "ollama", BaseURL: llmMock(t, &calls, 0).URL})
	a.AnalyzeAsync(a1)
	waitAll(t, a)
	if a.AnalyzeAsync(a2) {
		t.Fatal("over budget")
	}
	for i := 0; i < 3; i++ { // reenvíos del cliente, resultado tardío...
		if a.AnalyzeAsync(a2) {
			t.Fatal("a SKIPPED test must not be analyzed automatically past the budget")
		}
	}
	waitAll(t, a)
	if got, _ := store.GetTest(a2); got.Triage.State != "SKIPPED" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("state=%s calls=%d", got.Triage.State, calls)
	}
	if !a.ReanalyzeAsync(a2) { // a pedido: ignora el límite
		t.Fatal("manual re-analysis keeps ignoring the budget")
	}
	waitAll(t, a)
	if got, _ := store.GetTest(a2); got.Triage.State != "DONE" || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("manual: state=%s calls=%d", got.Triage.State, calls)
	}
}

// Muchos SKIPPED pidiendo a la vez con el cupo agotado: ninguno pasa.
func TestConcurrentSkippedRequestsRespectTheBudget(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	first := failAt(t, store, runID, "A", "AssertionError: a", "", 0)
	store.ReserveTriage(first, 2)
	ids := make([]int64, 15)
	for i := range ids {
		ids[i] = failAt(t, store, runID, fmt.Sprint("S", i), "AssertionError: s", "", 0)
		store.SaveTriageSkipped(ids[i], "sin cupo")
	}
	var reserved int32
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if out, _, _ := store.ReserveTriage(id, 2); out == db.TriageReserved {
				atomic.AddInt32(&reserved, 1)
			}
		}(id)
	}
	wg.Wait()
	if reserved != 1 { // quedaba exactamente un cupo
		t.Fatalf("reserved %d of the one slot left", reserved)
	}
}
