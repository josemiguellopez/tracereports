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
		if !reflect.DeepEqual(r.Run, *one) {
			t.Fatalf("run %d differs from GetRun:\n got %+v\nwant %+v", r.ID, r.Run, *one)
		}
		if r.Quarantined != 1 || r.Total != 4 {
			t.Fatalf("counters: %+v", r.Counters)
		}
		if r.Shared != 1 { // la actual solo tiene el test "a"
			t.Fatalf("shared tests: %d", r.Shared)
		}
	}
}

// Comparar con una ejecución de otra suite no dice "sin cambios": no tienen tests en común.
// Con tests en común, cada uno cae en un solo grupo, y los de la base que no corrieron se listan.
func TestCompareRunsCommonUnchangedMissing(t *testing.T) {
	s := openTestStore(t)
	mk := func(tests map[string]string) int64 {
		id, _ := s.CreateRunWithMeta("r", "qa", RunMeta{Project: "shop"})
		for _, key := range []string{"login", "buscar", "pagar", "extra", "otra"} {
			if st, ok := tests[key]; ok {
				tid, _ := s.CreateTestWithMeta(id, key, "", "", TestMeta{Key: key})
				s.FinishTest(tid, st, "", "")
			}
		}
		s.FinishRun(id)
		return id
	}
	base := mk(map[string]string{"login": "PASS", "buscar": "PASS", "pagar": "PASS", "extra": "PASS"})
	cur := mk(map[string]string{"login": "PASS", "buscar": "FAIL", "pagar": "SKIP"})
	other := mk(map[string]string{"otra": "PASS"}) // otra suite, y posterior

	c, err := s.CompareRuns(cur, base)
	if err != nil {
		t.Fatal(err)
	}
	// pagar pasó de PASS a SKIP: cambió, no está "igual"
	if c.Common != 3 || len(c.NewFailures) != 1 || len(c.Unchanged) != 1 || len(c.Changed) != 1 || c.Changed[0].Name != "pagar" || len(c.Missing) != 1 || c.Missing[0].Name != "extra" || c.BaseLater {
		t.Fatalf("with tests in common: %+v", c)
	}
	c, _ = s.CompareRuns(cur, other)
	if c.Common != 0 || len(c.NewTests) != 3 || len(c.Missing) != 1 || !c.BaseLater {
		t.Fatalf("another suite: %+v", c)
	}
}
