package db

import (
	"testing"
	"time"
	_ "time/tzdata" // zonas con cambio de horario aunque el sistema no las tenga
)

// atClock fixes the metrics clock (and optionally the local zone) for the test.
func atClock(t *testing.T, now time.Time, loc *time.Location) {
	t.Helper()
	prevNow, prevLoc := metricsNow, time.Local
	if loc != nil {
		time.Local = loc
	}
	metricsNow = func() time.Time { return now.In(time.Local) }
	t.Cleanup(func() { metricsNow, time.Local = prevNow, prevLoc })
}

// resultAt stores one finished run started at `at` with a test of that status.
func resultAt(t *testing.T, s *Store, at time.Time, status string) {
	t.Helper()
	run, err := s.CreateRun("suite", "qa")
	if err != nil {
		t.Fatal(err)
	}
	s.SetRunStarted(run, at.UnixMilli())
	id, _ := s.CreateTest(run, "t "+at.Format(time.RFC3339Nano), "", "")
	s.FinishTest(id, status, "", "")
	s.FinishRun(run)
}

// dailyMatchesKPIs checks that the days add up to the KPIs of the same period.
func dailyMatchesKPIs(t *testing.T, m *Metrics) {
	t.Helper()
	passed, failed, runs := 0, 0, 0
	for _, d := range m.Daily {
		passed, failed, runs = passed+d.Passed, failed+d.Failed, runs+d.Runs
	}
	if passed != m.Current.Passed || failed != m.Current.Failed || runs != m.Current.Runs {
		t.Fatalf("days %d/%d/%d runs vs KPIs %+v; days %v", passed, failed, runs, m.Current, m.Daily)
	}
}

func dayOf(m *Metrics, day string) *DayStat {
	for i := range m.Daily {
		if m.Daily[i].Day == day {
			return &m.Daily[i]
		}
	}
	return nil
}

// "Últimos 7 días" sigue siendo móvil (desde esta hora hace 7 días hasta ahora): el gráfico
// incluye el día parcial más antiguo que toca, y suma lo mismo que los KPIs.
func TestRollingPeriodChartsEveryDayItTouches(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 8, 10, 30, 0, 0, loc)
	atClock(t, now, loc)
	s := openTestStore(t)
	from := now.AddDate(0, 0, -7)
	resultAt(t, s, from, "FAIL")                         // justo en el inicio (inclusivo)
	resultAt(t, s, from.Add(time.Minute), "FAIL")        // primer día parcial
	resultAt(t, s, from.Add(-time.Millisecond), "FAIL")  // fuera: período anterior
	resultAt(t, s, now, "PASS")                          // el último instante (incluido)
	resultAt(t, s, now.Add(time.Millisecond), "WARNING") // futuro: fuera
	m, err := s.Metrics(MetricsQuery{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if m.Days != 7 || m.Current.Tests != 3 || m.Current.Failed != 2 || m.Previous.Failed != 1 {
		t.Fatalf("period: days %d, current %+v, previous %+v", m.Days, m.Current, m.Previous)
	}
	if len(m.Daily) != 8 || m.Daily[0].Day != "2026-10-01" || m.Daily[7].Day != "2026-10-08" {
		t.Fatalf("8 dates, from the partial first day to today: %v", m.Daily)
	}
	if d := dayOf(m, "2026-10-01"); d.Failed != 2 || d.Runs != 2 {
		t.Fatalf("first partial day: %+v", d)
	}
	dailyMatchesKPIs(t, m)
}

// A medianoche exacta el período empieza en un día completo: no aparece un día vacío extra
// al principio.
func TestRollingPeriodAtMidnight(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, loc)
	atClock(t, now, loc)
	s := openTestStore(t)
	resultAt(t, s, now.AddDate(0, 0, -7), "FAIL")
	m, _ := s.Metrics(MetricsQuery{Days: 7})
	if m.Daily[0].Day != "2026-10-01" || m.Daily[0].Failed != 1 {
		t.Fatalf("first day: %v", m.Daily)
	}
	dailyMatchesKPIs(t, m)
}

// Rango propio por fechas (como lo arma la API: medianoche a medianoche, "to" exclusivo).
func TestCustomRangeDays(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	atClock(t, time.Date(2026, 10, 20, 12, 0, 0, 0, loc), loc)
	s := openTestStore(t)
	from, to := time.Date(2026, 10, 1, 0, 0, 0, 0, loc), time.Date(2026, 10, 4, 0, 0, 0, 0, loc)
	resultAt(t, s, from, "FAIL")
	resultAt(t, s, to.Add(-time.Millisecond), "PASS")
	resultAt(t, s, to, "FAIL") // fuera (exclusivo)
	m, _ := s.Metrics(MetricsQuery{From: from.UnixMilli(), To: to.UnixMilli()})
	if m.Days != 3 || len(m.Daily) != 3 || m.Daily[0].Day != "2026-10-01" || m.Daily[2].Day != "2026-10-03" {
		t.Fatalf("custom range: days %d, %v", m.Days, m.Daily)
	}
	if m.Current.Tests != 2 {
		t.Fatalf("KPIs: %+v", m.Current)
	}
	dailyMatchesKPIs(t, m)
}

// Con un cambio de horario dentro del período, cada fecha aparece una vez y cada resultado cae
// en su día (Santiago adelanta la hora a medianoche en septiembre; Nueva York la atrasa en
// noviembre).
func TestPeriodAcrossDaylightSavingChanges(t *testing.T) {
	for _, c := range []struct {
		zone string
		now  time.Time
	}{
		{"America/Santiago", time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC)},
		{"America/New_York", time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)},
	} {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		now := c.now.In(loc)
		atClock(t, now, loc)
		s := openTestStore(t)
		from := now.AddDate(0, 0, -7)
		for d := 0; d <= 7; d++ { // un resultado cada día del período, a las 12:00 locales
			y, mo, dd := from.Date()
			at := time.Date(y, mo, dd+d, 12, 0, 0, 0, loc)
			if !at.Before(from) && !at.After(now) {
				resultAt(t, s, at, "FAIL")
			}
		}
		m, _ := s.Metrics(MetricsQuery{Days: 7})
		seen := map[string]bool{}
		for _, d := range m.Daily {
			if seen[d.Day] {
				t.Fatalf("%s: %s twice: %v", c.zone, d.Day, m.Daily)
			}
			seen[d.Day] = true
		}
		dailyMatchesKPIs(t, m)
	}
}
