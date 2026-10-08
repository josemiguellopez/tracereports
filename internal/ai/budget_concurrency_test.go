package ai

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Muchos fallos de la misma ejecución piden análisis a la vez: el límite por ejecución se respeta
// exactamente (antes, comprobar y reservar eran dos pasos y dos tests podían tomar el último cupo).
func TestPerRunBudgetHoldsUnderConcurrency(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	const tests, budget = 24, 3
	ids := make([]int64, tests)
	for i := range ids {
		ids[i] = failAt(t, store, runID, fmt.Sprint("T", i), fmt.Sprint("AssertionError: ", i), "", 0)
	}
	var calls int32
	a := New(store)
	a.MaxPerRun = budget
	a.SetConfig(Config{Provider: "ollama", BaseURL: llmMock(t, &calls, 30*time.Millisecond).URL})

	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			<-start
			a.AnalyzeAsync(id)
		}(id)
	}
	close(start)
	wg.Wait()
	waitAll(t, a)

	done, skipped := 0, 0
	for _, id := range ids {
		got, _ := store.GetTest(id)
		switch got.Triage.State {
		case "DONE":
			done++
		case "SKIPPED":
			skipped++
		}
	}
	if done != budget || skipped != tests-budget || atomic.LoadInt32(&calls) != budget {
		t.Fatalf("budget %d: done=%d skipped=%d calls=%d", budget, done, skipped, calls)
	}
}

func TestReserveTriageIsAtomic(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	ids := make([]int64, 20)
	for i := range ids {
		ids[i] = failAt(t, store, runID, fmt.Sprint("R", i), "AssertionError: r", "", 0)
	}
	var reserved int32
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if out, _, err := store.ReserveTriage(id, 5); err == nil && out == db.TriageReserved {
				atomic.AddInt32(&reserved, 1)
			}
		}(id)
	}
	wg.Wait()
	if reserved != 5 {
		t.Fatalf("reserved %d of a budget of 5", reserved)
	}
	// sin límite, y un DONE se respeta
	if out, _, _ := store.ReserveTriage(ids[19], 0); out != db.TriageReserved {
		t.Fatalf("no limit: %s", out)
	}
	store.SetTriagePending(ids[0])
	store.SaveTriage(ids[0], "LOGIC_BUG", "s", "x", "", "")
	if out, _, _ := store.ReserveTriage(ids[0], 5); out != db.TriageDone {
		t.Fatalf("done: %s", out)
	}
	if _, _, err := store.ReserveTriage(999999, 5); err != db.ErrNotFound {
		t.Fatalf("missing test: %v", err)
	}
}
