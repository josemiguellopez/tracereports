package db

import (
	"path/filepath"
	"testing"
)

// run creates a finished run in a context with tests key -> status (the visible name is the
// same for every key: "login").
func ctxRun(t *testing.T, s *Store, env, branch string, tests map[string]string) int64 {
	t.Helper()
	runID, err := s.CreateRunWithMeta("suite", env, RunMeta{Project: "shop", Branch: branch})
	if err != nil {
		t.Fatal(err)
	}
	for key, st := range tests {
		id, _ := s.CreateTestWithMeta(runID, "login", "", "", TestMeta{Key: key})
		if _, err := s.FinishTest(id, st, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	s.FinishRun(runID)
	return runID
}

func TestHomonymousTestsHaveIndependentHistory(t *testing.T) {
	s := openTestStore(t)
	a, b := "tests/test_admin.py::test_login", "tests/test_client.py::test_login"
	ctxRun(t, s, "staging", "main", map[string]string{a: "PASS", b: "FAIL"})
	last := ctxRun(t, s, "staging", "main", map[string]string{a: "PASS", b: "FAIL"})

	ha, _ := s.TestHistory(a, last, 10)
	hb, _ := s.TestHistory(b, last, 10)
	if len(ha) != 2 || len(hb) != 2 || ha[0].Status != "PASS" || hb[0].Status != "FAIL" {
		t.Fatalf("histories must not mix: a=%+v b=%+v", ha, hb)
	}
	cmp, _ := s.CompareRuns(last, 0)
	if len(cmp.StillFailing) != 1 || cmp.StillFailing[0].Key != b || len(cmp.NewTests) != 0 {
		t.Fatalf("compare by identity: %+v", cmp)
	}
}

func TestEnvironmentsAreNotComparedByDefault(t *testing.T) {
	s := openTestStore(t)
	k := "t.py::test_login"
	staging := ctxRun(t, s, "staging", "main", map[string]string{k: "FAIL"})
	prod := ctxRun(t, s, "production", "main", map[string]string{k: "PASS"})

	if h, _ := s.TestHistory(k, prod, 10); len(h) != 1 {
		t.Fatalf("production history must not include staging: %+v", h)
	}
	cmp, _ := s.CompareRuns(prod, 0)
	if cmp.BaseRun != nil {
		t.Fatalf("no base by default across environments, got #%d", cmp.BaseRun.ID)
	}
	// elegida explícitamente sí se compara, y se informa
	cmp, _ = s.CompareRuns(prod, staging)
	if cmp.BaseRun == nil || cmp.BaseReason != BaseChosen || len(cmp.Fixed) != 1 {
		t.Fatalf("explicit base: %+v", cmp)
	}
	// otra rama del mismo ambiente: base de respaldo, informada
	feat := ctxRun(t, s, "production", "feature/x", map[string]string{k: "FAIL"})
	cmp, _ = s.CompareRuns(feat, 0)
	if cmp.BaseRun == nil || cmp.BaseRun.ID != prod || cmp.BaseReason != BaseOtherBranch {
		t.Fatalf("fallback to another branch: %+v", cmp)
	}
	if runs, _ := s.CompatibleRuns(feat, 10); len(runs) != 1 || runs[0].ID != prod {
		t.Fatalf("compatible runs (same project and environment): %+v", runs)
	}
}

func TestLegacyRowsMigrateToNameKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := s.CreateRun("r", "")
	id, _ := s.CreateTest(runID, "Login", "", "")
	// simula una base anterior a la identidad estable
	if _, err := s.db.Exec(`UPDATE tests SET test_key = ''`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetTest(id)
	if err != nil || got.Key != "name:Login" || !got.KeyApprox || got.Attempts != 1 {
		t.Fatalf("legacy test after migration: %+v %v", got, err)
	}
}

func TestStabilityClassification(t *testing.T) {
	cases := []struct {
		in      []string // más nuevo primero
		retried int
		want    string
	}{
		{[]string{"FAIL", "FAIL", "FAIL", "PASS", "FAIL", "PASS"}, 0, StabilityPersistent}, // regresión, no flaky
		{[]string{"FAIL", "PASS", "FAIL", "PASS"}, 0, StabilityFlaky},
		{[]string{"PASS", "PASS", "PASS"}, 1, StabilityFlaky}, // pasó tras reintento
		{[]string{"PASS", "FAIL", "FAIL"}, 0, ""},             // falló y se arregló
		{[]string{"FAIL", "SKIP", "FAIL"}, 0, ""},
	}
	for _, c := range cases {
		if got, _, _ := Classify(c.in, c.retried); got != c.want {
			t.Errorf("Classify(%v, %d) = %q, want %q", c.in, c.retried, got, c.want)
		}
	}

	s := openTestStore(t)
	k := "t.py::test_pay"
	for _, st := range []string{"PASS", "FAIL", "PASS", "FAIL", "FAIL", "FAIL"} {
		ctxRun(t, s, "qa", "", map[string]string{k: st})
	}
	runID, _ := s.CreateRunWithMeta("suite", "qa", RunMeta{Project: "shop"})
	id, _ := s.CreateTestWithMeta(runID, "login", "", "", TestMeta{Key: k})
	s.FinishTest(id, "FAIL", "", "")
	d, _ := s.GetRunDetail(runID)
	fi := d.Tests[0].FlakyInfo
	if d.Tests[0].Flaky || fi == nil || fi.Kind != StabilityPersistent || fi.Streak != 4 || fi.FailRate != 71 || fi.LowData {
		t.Fatalf("persistent failure must not be labeled flaky: flaky=%v %+v", d.Tests[0].Flaky, fi)
	}

	// pasa tras reintento: evidencia directa de inestabilidad
	runID, _ = s.CreateRunWithMeta("suite", "retry", RunMeta{Project: "shop"})
	id, _ = s.CreateTestWithMeta(runID, "login", "", "", TestMeta{Key: k})
	s.SetAttempts(id, 2)
	s.FinishTest(id, "PASS", "", "")
	d, _ = s.GetRunDetail(runID)
	if fi := d.Tests[0].FlakyInfo; !d.Tests[0].Flaky || fi.Retried != 1 || fi.LowData || d.Tests[0].Attempts != 2 {
		t.Fatalf("passed after retry: %+v", fi)
	}
}

func TestFinishRunClosesUnfinishedTests(t *testing.T) {
	s := openTestStore(t)
	runID, _ := s.CreateRun("xdist", "")
	ok, _ := s.CreateTestWithMeta(runID, "a", "", "", TestMeta{Key: "a", Worker: "gw0"})
	s.FinishTest(ok, "PASS", "", "")
	lost, _ := s.CreateTestWithMeta(runID, "b", "", "", TestMeta{Key: "b", Worker: "gw1"})
	run, err := s.FinishRun(runID)
	if err != nil || !run.Incomplete || run.Status != "FAIL" || run.Failed != 1 {
		t.Fatalf("run with an unfinished test: %+v %v", run, err)
	}
	if got, _ := s.GetTest(lost); got.Status != "FAIL" || got.ErrorMessage != InterruptedMessage || got.Worker != "gw1" {
		t.Fatalf("unfinished test: %+v", got)
	}

	// interrumpida por el cliente sin fallos: nunca verde
	runID, _ = s.CreateRun("ctrl-c", "")
	id, _ := s.CreateTest(runID, "a", "", "")
	s.FinishTest(id, "PASS", "", "")
	if run, _ := s.FinishRunWith(runID, true); run.Status == "PASS" || !run.Incomplete {
		t.Fatalf("interrupted run must not be PASS: %+v", run)
	}
}

func TestPurgeRunsBefore(t *testing.T) {
	s := openTestStore(t)
	old, _ := s.CreateRun("old", "")
	id, _ := s.CreateTest(old, "a", "", "")
	s.AddLog(id, "FAIL", "x", 0, "/screenshots/a.png")
	s.FinishRun(old)
	s.db.Exec(`UPDATE runs SET started_at = 1000 WHERE id = ?`, old)
	keep, _ := s.CreateRun("new", "")
	n, shots, err := s.PurgeRunsBefore(NowMs() - 86400000)
	if err != nil || n != 1 || len(shots) != 1 || shots[0] != "/screenshots/a.png" {
		t.Fatalf("purge: n=%d shots=%v err=%v", n, shots, err)
	}
	if _, err := s.GetTest(id); err != ErrNotFound {
		t.Fatal("the evidence of a purged run must be deleted too")
	}
	if _, err := s.GetRun(keep); err != nil {
		t.Fatal("recent runs must stay")
	}
}
