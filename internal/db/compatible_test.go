package db

import (
	"path/filepath"
	"reflect"
	"testing"
)

// CompatibleRuns en una consulta devuelve lo mismo que pedir cada ejecución con GetRun: filtros
// (proyecto, ambiente, terminadas, sin la propia), orden, límite y contadores.
func TestCompatibleRunsMatchesGetRun(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mk := func(project, env string, statuses ...string) int64 {
		id, _ := s.CreateRunWithMeta("r", env, RunMeta{Project: project, Branch: "main"})
		for i, st := range statuses {
			tid, _ := s.CreateTestWithMeta(id, "t", "", "", TestMeta{Key: string(rune('a' + i))})
			s.FinishTest(tid, st, "", "")
		}
		return id
	}
	var want []int64
	for i := 0; i < 5; i++ {
		id := mk("shop", "qa", "PASS", "FAIL", "SKIP", "WARNING")
		s.FinishRun(id)
		want = append([]int64{id}, want...)
	}
	mk("shop", "prod", "PASS")            // otro ambiente
	s.FinishRun(mk("blog", "qa", "PASS")) // otro proyecto
	mk("shop", "qa", "PASS")              // en curso
	s.SetQuarantine(&Quarantine{Project: "shop", Key: "b", Reason: "flaky", Until: NowMs() + 3600_000})
	current := mk("shop", "qa", "FAIL")
	s.FinishRun(current)

	got, err := s.CompatibleRuns(current, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("limit: %d", len(got))
	}
	for i, r := range got {
		if r.ID != want[i] {
			t.Fatalf("order/filter: got %d want %d", r.ID, want[i])
		}
		one, err := s.GetRun(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r, *one) {
			t.Fatalf("run %d differs from GetRun:\n got %+v\nwant %+v", r.ID, r, *one)
		}
		if r.Quarantined != 1 || r.Total != 4 {
			t.Fatalf("counters: %+v", r.Counters)
		}
	}
}
