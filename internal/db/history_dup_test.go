package db

import (
	"path/filepath"
	"testing"
)

const histKey = "tests/test_pay.py::test_card"

func histStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// histRun crea una ejecución del contexto con la clave repetida: un resultado por estado.
func histRun(t *testing.T, s *Store, meta RunMeta, statuses ...string) (runID int64, ids []int64) {
	t.Helper()
	runID, _ = s.CreateRunWithMeta("r", "qa", meta)
	for _, st := range statuses {
		id, _ := s.CreateTestWithMeta(runID, "pay", "", "", TestMeta{Key: histKey})
		if st != "RUNNING" {
			s.FinishTest(id, st, "", "")
		}
		ids = append(ids, id)
	}
	s.FinishRun(runID)
	return runID, ids
}

func repeat(st string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = st
	}
	return out
}

func runIDs(h []HistoryEntry) []int64 {
	out := make([]int64, len(h))
	for i, e := range h {
		out[i] = e.RunID
	}
	return out
}

func TestHistoryWithDuplicatedTests(t *testing.T) {
	s := histStore(t)
	ctx := RunMeta{Project: "shop", Branch: "main"}
	r1, _ := histRun(t, s, ctx, "PASS")
	r2, _ := histRun(t, s, ctx, "FAIL")
	r3, ids3 := histRun(t, s, ctx, repeat("PASS", 20)...)

	// reproducción: 1, 1 y 20 registros; pedir 10 devuelve las 3 ejecuciones
	h, err := s.TestHistory(histKey, r3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := runIDs(h); len(got) != 3 || got[0] != r3 || got[1] != r2 || got[2] != r1 {
		t.Fatalf("history: %v (want %d, %d, %d)", got, r3, r2, r1)
	}
	// de la tercera, su último resultado
	if h[0].TestID != ids3[19] {
		t.Fatalf("latest of the run: %d, want %d", h[0].TestID, ids3[19])
	}
	// más duplicados que el límite: el límite cuenta ejecuciones
	if h, _ := s.TestHistory(histKey, r3, 2); len(h) != 2 || h[0].RunID != r3 || h[1].RunID != r2 {
		t.Fatalf("limit 2: %v", runIDs(h))
	}
	// la ejecución máxima se respeta
	if h, _ := s.TestHistory(histKey, r2, 10); len(h) != 2 || h[0].RunID != r2 {
		t.Fatalf("up to r2: %v", runIDs(h))
	}
}

func TestHistoryLatestEligibleResultPerRun(t *testing.T) {
	s := histStore(t)
	ctx := RunMeta{Project: "shop", Branch: "main"}
	// el último registro sigue corriendo (ejecución abierta): cuenta el anterior terminado
	r, _ := s.CreateRunWithMeta("open", "qa", ctx)
	var ids []int64
	for _, st := range []string{"PASS", "FAIL", "RUNNING"} {
		id, _ := s.CreateTestWithMeta(r, "pay", "", "", TestMeta{Key: histKey})
		if st != "RUNNING" {
			s.FinishTest(id, st, "", "")
		}
		ids = append(ids, id)
	}
	h, _ := s.TestHistory(histKey, r, 10)
	if len(h) != 1 || h[0].TestID != ids[1] || h[0].Status != "FAIL" {
		t.Fatalf("latest finished result: %+v", h)
	}
}

func TestHistoryContextIsolationAndNormalCase(t *testing.T) {
	s := histStore(t)
	main := RunMeta{Project: "shop", Branch: "main"}
	var want []int64
	for i := 0; i < 4; i++ { // sin duplicados
		r, _ := histRun(t, s, main, "PASS")
		want = append([]int64{r}, want...)
		histRun(t, s, RunMeta{Project: "shop", Branch: "feature"}, repeat("FAIL", 5)...) // otra rama
		histRun(t, s, RunMeta{Project: "blog", Branch: "main"}, "FAIL")                  // otro proyecto
	}
	h, _ := s.TestHistory(histKey, want[0], 10)
	if got := runIDs(h); len(got) != 4 {
		t.Fatalf("only the same context: %v", got)
	}
	for i, e := range h {
		if e.RunID != want[i] || e.Status != "PASS" {
			t.Fatalf("order/context at %d: %+v (want run %d)", i, e, want[i])
		}
	}
	if h, _ := s.TestHistory(histKey, want[0], 3); len(h) != 3 {
		t.Fatalf("limit: %d", len(h))
	}
}
