package db

import (
	"testing"
)

// bulkTests inserts n finished tests with the same status into a run (fast: one statement).
func bulkTests(t *testing.T, s *Store, runID int64, n int, status, key, tags string, at int64) {
	t.Helper()
	if _, err := s.db.Exec(`WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n < ?)
		INSERT INTO tests(run_id, name, test_key, category, status, started_at, ended_at)
		SELECT ?, 'bulk ' || n, ? || n, ?, ?, ?, ? FROM seq`, n, runID, key, tags, status, at, at+5); err != nil {
		t.Fatal(err)
	}
}

func runAt(t *testing.T, s *Store, name string, at int64) int64 {
	t.Helper()
	id, err := s.CreateRun(name, "qa")
	if err != nil {
		t.Fatal(err)
	}
	s.SetRunStarted(id, at)
	return id
}

func finishedTest(t *testing.T, s *Store, runID int64, name, tags, status string) int64 {
	t.Helper()
	id, err := s.CreateTestWithMeta(runID, name, tags, "", TestMeta{Key: name})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishTest(id, status, "boom "+name, ""); err != nil {
		t.Fatal(err)
	}
	return id
}

// Con más de 100.000 resultados en el período, las métricas siguen siendo las de todos: el
// fallo más reciente no desaparece (KPIs, días, ejecuciones, tests que más fallan, etiquetas,
// resumen semanal y filtros).
func TestMetricsCountEveryResultOfThePeriod(t *testing.T) {
	s := openTestStore(t)
	now := NowMs()
	old := runAt(t, s, "nightly", now-3600_000)
	bulkTests(t, s, old, 100_000, "PASS", "bulk-", "smoke", now-3600_000)
	s.FinishRun(old)
	latest := runAt(t, s, "nightly", now-60_000)
	finishedTest(t, s, latest, "checkout", "smoke", "FAIL")
	s.FinishRun(latest)

	for _, q := range []MetricsQuery{{Days: 7}, {Days: 30, Suite: "nightly"}, {Days: 30, Env: "qa"}, {Days: 30, Tag: "smoke"}} {
		m, err := s.Metrics(q)
		if err != nil {
			t.Fatal(err)
		}
		c := m.Current
		if c.Tests != 100_001 || c.Passed != 100_000 || c.Failed != 1 { // PassRate: 99,999 % se muestra 100,0
			t.Fatalf("%+v: KPIs over everything: %+v", q, c)
		}
		passed, failed := 0, 0
		for _, d := range m.Daily {
			passed, failed = passed+d.Passed, failed+d.Failed
		}
		if passed != c.Passed || failed != c.Failed {
			t.Fatalf("%+v: days add up to the KPIs: %d/%d vs %+v", q, passed, failed, c)
		}
		total := 0
		for _, r := range m.Runs {
			total += r.Total
		}
		if total != c.Tests || m.Runs[len(m.Runs)-1].Failed != 1 {
			t.Fatalf("%+v: runs add up: %d, last %+v", q, total, m.Runs[len(m.Runs)-1])
		}
		if len(m.TopFailing) != 1 || m.TopFailing[0].Name != "checkout" {
			t.Fatalf("%+v: the recent failure is listed: %+v", q, m.TopFailing)
		}
		if len(m.Tags) != 1 || m.Tags[0].Tests != 100_001 || m.Tags[0].Failed != 1 {
			t.Fatalf("%+v: tag stats: %+v", q, m.Tags)
		}
	}
	// otro tag: nada
	if m, _ := s.Metrics(MetricsQuery{Days: 30, Tag: "api"}); m.Current.Tests != 0 {
		t.Fatalf("tag filter: %+v", m.Current)
	}
}

// El período anterior (para la variación) también cuenta todo.
func TestMetricsPreviousPeriodCountsEverything(t *testing.T) {
	s := openTestStore(t)
	now := NowMs()
	day := int64(86_400_000)
	prev := runAt(t, s, "nightly", now-10*day)
	bulkTests(t, s, prev, 100_000, "PASS", "bulk-", "", now-10*day)
	finishedTest(t, s, prev, "late fail", "", "FAIL")
	s.FinishRun(prev)
	cur := runAt(t, s, "nightly", now-day)
	finishedTest(t, s, cur, "ok", "", "PASS")
	s.FinishRun(cur)
	m, err := s.Metrics(MetricsQuery{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Previous; p.Tests != 100_001 || p.Failed != 1 || p.Runs != 1 {
		t.Fatalf("previous period: %+v", p)
	}
	if c := m.Current; c.Tests != 1 || c.PassRate != 100 {
		t.Fatalf("current: %+v", c)
	}
}

// Una sola política de "tasa de éxito" en todos los indicadores comparables: PASS sobre los
// tests ejecutados (todo menos SKIP). WARNING se ejecutó y no pasó; SKIP no cuenta.
func TestPassRateIsTheSameEverywhere(t *testing.T) {
	cases := []struct {
		statuses []string
		want     float64
	}{
		{[]string{"PASS", "WARNING"}, 50},
		{[]string{"PASS", "FAIL"}, 50},
		{[]string{"PASS", "SKIP"}, 100},
		{[]string{"PASS", "WARNING", "FAIL", "SKIP"}, 33.3},
		{[]string{"WARNING", "SKIP"}, 0},
		{[]string{"SKIP"}, 0},
		{[]string{"PASS", "PASS", "PASS", "WARNING"}, 75},
	}
	for _, c := range cases {
		s := openTestStore(t)
		run := runAt(t, s, "suite", NowMs()-60_000)
		for i, st := range c.statuses {
			finishedTest(t, s, run, "t"+itoaDB(i), "smoke", st)
		}
		s.FinishRun(run)
		m, err := s.Metrics(MetricsQuery{Days: 7})
		if err != nil {
			t.Fatal(err)
		}
		var day DayStat
		for _, d := range m.Daily {
			if d.Runs > 0 {
				day = d
			}
		}
		got := map[string]float64{"kpi": m.Current.PassRate, "day": day.PassRate, "run": m.Runs[0].PassRate, "tag": m.Tags[0].PassRate}
		for where, v := range got {
			if v != c.want {
				t.Errorf("%v: %s pass rate %v, want %v (all: %v)", c.statuses, where, v, c.want, got)
			}
		}
	}
}

func itoaDB(i int) string { return string(rune('a' + i)) }
