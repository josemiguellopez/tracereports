package db

import (
	"errors"
	"path/filepath"
	"testing"
)

func openLive(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGroupExecutions(t *testing.T) {
	end := func(v int64) *int64 { return &v }
	min := int64(60_000)
	runs := []LiveRun{
		// un lanzador que crea una corrida por archivo, sin execution_id: se agrupan por contexto y tiempo
		{ID: 1, Project: "box", Branch: "main", Commit: "abc", StartedAt: 0, EndedAt: end(5 * min)},
		{ID: 2, Project: "box", Branch: "main", Commit: "abc", StartedAt: 6 * min, EndedAt: end(9 * min)},
		// otro proyecto al mismo tiempo: otra ejecución
		{ID: 3, Project: "web", Branch: "main", Commit: "abc", StartedAt: 7 * min},
		// con execution_id: juntas aunque cambie el contexto
		{ID: 4, Project: "box", ExecutionID: "build-77", StartedAt: 8 * min},
		{ID: 5, Project: "api", ExecutionID: "build-77", StartedAt: 30 * min},
		// el mismo contexto mucho después: otra ejecución
		{ID: 6, Project: "box", Branch: "main", Commit: "abc", StartedAt: 40 * min},
	}
	got := GroupExecutions(runs, 50*min)
	want := map[string][]int64{"g:1": {1, 2}, "g:3": {3}, "x:build-77": {4, 5}, "g:6": {6}}
	if len(got) != len(want) {
		t.Fatalf("ejecuciones: %+v", got)
	}
	for _, e := range got {
		ids := []int64{}
		for _, r := range e.Runs {
			ids = append(ids, r.ID)
		}
		if w := want[e.Key]; len(w) != len(ids) || (len(w) > 0 && w[0] != ids[0]) || w[len(w)-1] != ids[len(ids)-1] {
			t.Errorf("%s = %v, want %v", e.Key, ids, w)
		}
	}
}

func TestLiveTestsAndDurationStats(t *testing.T) {
	s := openLive(t)
	// historial: el mismo test terminó en 10, 20, 30, 40 y 100 s
	for _, d := range []int64{10, 20, 30, 40, 100} {
		run, _ := s.CreateRunWithMeta("prev", "qa", RunMeta{Project: "box"})
		id, _ := s.CreateTest(run, "test_a", "", "")
		s.db.Exec(`UPDATE tests SET test_key = 'k::a', status = 'PASS', started_at = 1000, ended_at = ? WHERE id = ?`, 1000+d*1000, id)
	}
	run, _ := s.CreateRunWithMeta("now", "qa", RunMeta{Project: "box", ExecutionID: "e1", Planned: 4})
	id, _ := s.CreateTest(run, "test_a", "", "")
	s.db.Exec(`UPDATE tests SET test_key = 'k::a', worker = 'gw1' WHERE id = ?`, id)
	s.AddLog(id, "INFO", "abre la ficha", 5000, "")
	s.AddLog(id, "INFO", "guarda el periodontograma", 9000, "")
	if err := s.TouchTest(id); err != nil {
		t.Fatal(err)
	}
	runs, err := s.LiveCandidates(0)
	if err != nil || len(runs) != 6 {
		t.Fatalf("candidatas: %d %v", len(runs), err)
	}
	last := runs[len(runs)-1:]
	if err := s.LiveTests(last); err != nil {
		t.Fatal(err)
	}
	lt := last[0].Tests[0]
	if lt.LastStep != "guarda el periodontograma" || lt.LastStepAt != 9000 || lt.Worker != "gw1" || lt.LastSignal < lt.StartedAt {
		t.Fatalf("test en vivo: %+v", lt)
	}
	if last[0].ExecutionID != "e1" || last[0].Planned != 4 {
		t.Fatalf("corrida: %+v", last[0])
	}
	p50, p90, n, err := s.DurationStats("k::a", "box", "qa", run, 20)
	if err != nil || n != 5 || p50 != 30_000 || p90 != 100_000 {
		t.Fatalf("p50=%d p90=%d n=%d err=%v", p50, p90, n, err)
	}
	if _, _, n, _ := s.DurationStats("k::a", "otro", "qa", run, 20); n != 0 {
		t.Fatal("el historial es del mismo proyecto")
	}
}

func TestCancelAndPlanned(t *testing.T) {
	s := openLive(t)
	run, _ := s.CreateRun("r", "")
	if c, err := s.GetRunControl(run); err != nil || c.Cancel {
		t.Fatalf("%+v %v", c, err)
	}
	if ok, err := s.RequestCancel(run, "ana", "se colgó el ambiente"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.RequestCancel(run, "otro", "x"); ok {
		t.Fatal("pedirlo dos veces no cambia quién lo pidió")
	}
	if c, _ := s.GetRunControl(run); !c.Cancel || c.By != "ana" || c.Reason != "se colgó el ambiente" {
		t.Fatalf("%+v", c)
	}
	if _, err := s.GetRunControl(999); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SetPlanned(run, 12); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlanned(999, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	closed, _ := s.CreateRun("cerrada", "")
	s.db.Exec(`UPDATE runs SET status = 'PASS' WHERE id = ?`, closed)
	if ok, _ := s.RequestCancel(closed, "ana", ""); ok {
		t.Fatal("una corrida terminada no se cancela")
	}
}

func TestExecutionCancel(t *testing.T) {
	s := openLive(t)
	if _, ok, err := s.ExecutionCancel("x:b1"); ok || err != nil {
		t.Fatal(ok, err)
	}
	if any, _ := s.AnyExecutionCancelled(); any {
		t.Fatal("nada cancelado todavía")
	}
	if err := s.CancelExecution("x:b1", "ana", "QA caído"); err != nil {
		t.Fatal(err)
	}
	s.CancelExecution("x:b1", "otro", "x") // el primero gana
	c, ok, err := s.ExecutionCancel("x:b1")
	if !ok || err != nil || !c.Cancel || c.By != "ana" || c.Reason != "QA caído" {
		t.Fatalf("%+v %v %v", c, ok, err)
	}
	// pasado el plazo ya no cuenta, y se puede volver a cancelar
	s.db.Exec(`UPDATE execution_cancels SET at = ? WHERE key = 'x:b1'`, NowMs()-ExecutionCancelTTL-1000)
	if _, ok, _ := s.ExecutionCancel("x:b1"); ok {
		t.Fatal("una cancelación vieja no cuenta")
	}
	if err := s.CancelExecution("x:b1", "otro", "de nuevo"); err != nil {
		t.Fatal(err)
	}
	c, ok, _ = s.ExecutionCancel("x:b1")
	if !ok || c.By != "otro" {
		t.Fatalf("%+v", c)
	}
	run, _ := s.CreateRunWithMeta("r", "", RunMeta{ExecutionID: "b1"})
	if id, _ := s.RunExecutionID(run); id != "b1" {
		t.Fatal(id)
	}
	s.MarkRunCancelled(run, c)
	if rc, _ := s.GetRunControl(run); !rc.Cancel || rc.Reason != "de nuevo" {
		t.Fatalf("%+v", rc)
	}
}
