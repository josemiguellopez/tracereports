package db

import (
	"sync"
	"testing"
)

// Una ejecución cerrada nunca queda verde con tests FAIL o RUNNING, ni pierde la marca de
// incompleta en un cierre repetido (spool reenviado, reintento del cliente).

func mustRun(t *testing.T, s *Store, id int64) *Run {
	t.Helper()
	r, err := s.GetRun(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRepeatedCloseKeepsInterruption(t *testing.T) {
	s := openTestStore(t)
	rid, _ := s.CreateRun("interrumpida", "qa")
	tid, _ := s.CreateTest(rid, "test", "", "")
	s.FinishTest(tid, "PASS", "", "")
	if r, _ := s.FinishRunWith(rid, true); r.Status != "WARNING" || !r.Incomplete {
		t.Fatalf("interrupted close: %+v", r)
	}
	r, first, err := s.CloseRun(rid, false)
	if err != nil {
		t.Fatal(err)
	}
	if first {
		t.Fatal("a repeated close must not count as the first one")
	}
	if r.Status != "WARNING" || !r.Incomplete {
		t.Fatalf("second close forgot the interruption: %+v", r)
	}
}

func TestLateFailureAfterCloseTurnsRunRed(t *testing.T) {
	s := openTestStore(t)
	rid, _ := s.CreateRun("tardío", "qa")
	tid, _ := s.CreateTest(rid, "test", "", "")
	s.FinishTest(tid, "PASS", "", "")
	if r, _ := s.FinishRun(rid); r.Status != "PASS" {
		t.Fatalf("close: %+v", r)
	}
	s.FinishTest(tid, "FAIL", "late result", "")
	if r := mustRun(t, s, rid); r.Status != "FAIL" || r.Failed != 1 {
		t.Fatalf("green run with a failed test: %+v", r)
	}
}

func TestLatePassForInterruptedTestStaysIncomplete(t *testing.T) {
	s := openTestStore(t)
	rid, _ := s.CreateRun("interrumpida", "qa")
	tid, _ := s.CreateTest(rid, "test", "", "")
	if r, _ := s.FinishRun(rid); r.Status != "FAIL" || !r.Incomplete {
		t.Fatalf("close with a RUNNING test: %+v", r)
	}
	s.FinishTest(tid, "PASS", "", "") // llega tarde desde el spool
	if r := mustRun(t, s, rid); r.Status == "PASS" {
		t.Fatalf("incomplete run green after the late result: %+v", r)
	}
	if r, _ := s.FinishRun(rid); r.Status != "WARNING" || !r.Incomplete {
		t.Fatalf("incomplete run green after re-close: %+v", r)
	}
}

func TestTestCreatedAfterCloseIsNotGreen(t *testing.T) {
	s := openTestStore(t)
	rid, _ := s.CreateRun("cerrada", "qa")
	a, _ := s.CreateTest(rid, "a", "", "")
	s.FinishTest(a, "PASS", "", "")
	s.FinishRun(rid)
	b, _ := s.CreateTest(rid, "b", "", "")
	if r := mustRun(t, s, rid); r.Status == "PASS" {
		t.Fatalf("closed run green with a RUNNING test: %+v", r)
	}
	s.FinishTest(b, "PASS", "", "")
	if r := mustRun(t, s, rid); r.Status != "PASS" || r.Total != 2 {
		t.Fatalf("both tests passed: %+v", r)
	}
}

func TestConcurrentCloseAndResults(t *testing.T) {
	s := openTestStore(t)
	rid, _ := s.CreateRun("concurrente", "qa")
	ids := make([]int64, 20)
	for i := range ids {
		ids[i], _ = s.CreateTest(rid, "t", "", "")
	}
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			st := "PASS"
			if i == 7 {
				st = "FAIL"
			}
			s.FinishTest(id, st, "", "")
		}(i, id)
	}
	wg.Add(1)
	go func() { defer wg.Done(); s.FinishRun(rid) }()
	wg.Wait()
	s.FinishRun(rid) // cierre repetido, como el reenvío de un spool
	r := mustRun(t, s, rid)
	if r.Status != "FAIL" || r.Running != 0 {
		t.Fatalf("concurrent delivery: %+v", r)
	}
}
