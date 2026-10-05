package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMetricsAndSettings(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 4 ejecuciones: "Carrito" alterna (flaky), "Login" siempre pasa, "Pago" falla siempre.
	cart := []string{"PASS", "FAIL", "PASS", "FAIL"}
	for i, st := range cart {
		run, _ := s.CreateRun("Checkout", "qa")
		for _, tc := range []struct{ name, tags, status string }{
			{"Carrito", "checkout, smoke", st}, {"Login", "login", "PASS"}, {"Pago", "checkout", "FAIL"},
		} {
			id, _ := s.CreateTest(run, tc.name, tc.tags, "")
			if _, err := s.FinishTest(id, tc.status, "AssertionError: total\nstack", ""); err != nil {
				t.Fatal(err)
			}
			if tc.name == "Pago" && i == 0 {
				s.SetTriagePending(id)
				s.SaveTriage(id, "BACKEND_TIMEOUT", "s", "s", "", "")
			}
		}
		if _, err := s.FinishRun(run); err != nil {
			t.Fatal(err)
		}
	}
	other, _ := s.CreateRun("Otra suite", "qa")
	id, _ := s.CreateTest(other, "X", "", "")
	s.FinishTest(id, "PASS", "", "")
	s.FinishRun(other)
	open, _ := s.CreateRun("Abierta", "qa") // en curso: no cuenta
	s.CreateTest(open, "Y", "", "")

	m, err := s.Metrics(MetricsQuery{Days: 30, Suite: "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Current.Runs != 4 || m.Current.Tests != 12 || m.Current.Failed != 6 || m.Current.PassRate != 50 || m.Current.FlakyTests != 1 {
		t.Fatalf("kpis: %+v", m.Current)
	}
	if len(m.Suites) != 3 || len(m.Daily) != 30 || m.Daily[29].Runs != 4 || len(m.Runs) != 4 {
		t.Fatalf("suites=%v daily=%+v runs=%d", m.Suites, m.Daily[29], len(m.Runs))
	}
	if m.TopFailing[0].Name != "Pago" || m.TopFailing[0].Fails != 4 || m.TopFailing[0].LastError != "AssertionError: total" {
		t.Fatalf("top failing: %+v", m.TopFailing[0])
	}
	if len(m.Flaky) != 1 || m.Flaky[0].Name != "Carrito" || m.Flaky[0].Flips != 3 {
		t.Fatalf("flaky: %+v", m.Flaky)
	}
	if len(m.Causes) != 2 || m.Causes[0].Category != "" || m.Causes[0].Count != 5 || m.Causes[1].Category != "BACKEND_TIMEOUT" {
		t.Fatalf("causes: %+v", m.Causes)
	}
	if m.Tags[0].Tag != "checkout" || m.Tags[0].PassRate != 25 {
		t.Fatalf("tags (peor primero): %+v", m.Tags)
	}
	// filtro por tag: solo los tests con ese tag, y las ejecuciones se recalculan con ellos
	smoke, _ := s.Metrics(MetricsQuery{Days: 30, Suite: "Checkout", Tag: "smoke"})
	if smoke.Current.Tests != 4 || smoke.Current.Failed != 2 || smoke.Current.Runs != 4 || smoke.Runs[0].Total != 1 {
		t.Fatalf("tag filter: %+v runs=%+v", smoke.Current, smoke.Runs)
	}
	if len(smoke.TagSet) != 3 { // checkout, smoke, login: las opciones no se recortan por el filtro
		t.Fatalf("tag options: %v", smoke.TagSet)
	}
	if e, _ := s.Metrics(MetricsQuery{Days: 30, Env: "otro"}); e.Current.Runs != 0 || len(e.Envs) != 1 {
		t.Fatalf("env filter: %+v envs=%v", e.Current, e.Envs)
	}
	now := time.Now()
	c, _ := s.Metrics(MetricsQuery{From: now.AddDate(0, 0, -6).UnixMilli(), To: now.Add(time.Hour).UnixMilli()})
	if !c.Custom || c.Days != 7 || len(c.Daily) != 7 || c.Current.Runs != 5 {
		t.Fatalf("custom range: custom=%v days=%d daily=%d runs=%d", c.Custom, c.Days, len(c.Daily), c.Current.Runs)
	}
	all, _ := s.Metrics(MetricsQuery{Days: 30})
	if all.Current.Runs != 5 {
		t.Fatalf("all suites runs = %d (la ejecución abierta no cuenta)", all.Current.Runs)
	}

	// settings
	if err := s.SaveSettings(map[string]string{"ai.provider": "ollama", "ai.model": "llama3.1", "ui.language": "en"}); err != nil {
		t.Fatal(err)
	}
	s.SaveSettings(map[string]string{"ai.model": ""}) // vacío = borrar
	if err := s.DeleteSettings("ui."); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Settings()
	if len(got) != 1 || got["ai.provider"] != "ollama" {
		t.Fatalf("settings: %v", got)
	}
}

// Regresiones de la validación: el contexto completo llega hasta la lista de flaky.
func TestMetricsFlakyKeepsProjectAndBranch(t *testing.T) {
	s := openTestStore(t)
	add := func(project, branch, status string) {
		id, _ := s.CreateRunWithMeta("suite", "qa", RunMeta{Project: project, Branch: branch})
		tid, _ := s.CreateTestWithMeta(id, "login", "", "", TestMeta{Key: "t.py::test_login"})
		s.FinishTest(tid, status, "", "")
		s.FinishRun(id)
	}
	// A alterna; B (otro proyecto, misma clave) siempre pasa
	for i := 0; i < 6; i++ {
		add("A", "main", map[bool]string{true: "PASS", false: "FAIL"}[i%2 == 0])
		add("B", "main", "PASS")
	}
	m, err := s.Metrics(MetricsQuery{Days: 30})
	if err != nil {
		t.Fatal(err)
	}
	if m.Current.FlakyTests != 1 || len(m.Flaky) != 1 || m.Flaky[0].Project != "A" {
		t.Fatalf("only project A is flaky: kpi=%d list=%+v", m.Current.FlakyTests, m.Flaky)
	}

	// mismo proyecto: main siempre pasa y feature siempre falla, intercalados
	s2 := openTestStore(t)
	s = s2
	for i := 0; i < 3; i++ {
		add("C", "main", "PASS")
		add("C", "feature", "FAIL")
	}
	m, _ = s.Metrics(MetricsQuery{Days: 30})
	if m.Current.FlakyTests != 0 || len(m.Flaky) != 0 {
		t.Fatalf("two stable branches are not flakiness: kpi=%d list=%+v", m.Current.FlakyTests, m.Flaky)
	}
	branches := map[string]int{}
	for _, st := range m.TopFailing {
		branches[st.Branch] += st.Fails
	}
	if branches["feature"] != 3 || branches["main"] != 0 {
		t.Fatalf("rows must keep their branch: %+v", m.TopFailing)
	}
}
