package api

import (
	"encoding/json"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func inZone(t *testing.T, zone string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
	return loc
}

func finishedRunAt(t *testing.T, srv *Server, suite string, at time.Time, status string) {
	t.Helper()
	run, err := srv.Store.CreateRun(suite, "")
	if err != nil {
		t.Fatal(err)
	}
	srv.Store.SetRunStarted(run, at.UnixMilli())
	id, _ := srv.Store.CreateTest(run, at.Format(time.RFC3339), "", "")
	srv.Store.FinishTest(id, status, "", "")
	srv.Store.FinishRun(run)
}

func metricsFor(t *testing.T, srv *Server, query string) db.Metrics {
	t.Helper()
	rec := call(t, srv, "GET", "/api/v1/metrics?"+query, "")
	var m db.Metrics
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil {
		t.Fatalf("metrics %s: %d %s", query, rec.Code, rec.Body)
	}
	return m
}

// Un rango propio por fechas va desde el primer instante de la fecha "from" hasta el primer
// instante del día siguiente a "to" (inclusivo en la URL, exclusivo adentro), también los días
// en que la medianoche no existe o se repite por el cambio de horario.
func TestCustomRangeFollowsCalendarDatesAcrossDST(t *testing.T) {
	cases := []struct {
		name, zone, day string
		start, end      [5]int // año, mes, día, hora, minuto locales de los límites
	}{
		// Santiago adelanta la hora: el 6 de septiembre empieza a la 01:00
		{"santiago spring forward", "America/Santiago", "2026-09-06", [5]int{2026, 9, 6, 1, 0}, [5]int{2026, 9, 7, 0, 0}},
		// Santiago atrasa la hora: el 4 de abril dura 25 horas
		{"santiago fall back", "America/Santiago", "2026-04-04", [5]int{2026, 4, 4, 0, 0}, [5]int{2026, 4, 5, 0, 0}},
		// Nueva York (el cambio es a las 02:00): medianoches normales
		{"new york fall back", "America/New_York", "2026-11-01", [5]int{2026, 11, 1, 0, 0}, [5]int{2026, 11, 2, 0, 0}},
		{"normal day", "Europe/Madrid", "2026-10-07", [5]int{2026, 10, 7, 0, 0}, [5]int{2026, 10, 8, 0, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loc := inZone(t, c.zone)
			srv, _ := newTestServer(t)
			start := time.Date(c.start[0], time.Month(c.start[1]), c.start[2], c.start[3], c.start[4], 0, 0, loc)
			end := time.Date(c.end[0], time.Month(c.end[1]), c.end[2], c.end[3], c.end[4], 0, 0, loc)
			finishedRunAt(t, srv, "s", start.Add(-30*time.Minute), "FAIL") // la noche anterior: fuera
			finishedRunAt(t, srv, "s", start, "PASS")                      // el primer instante: dentro
			finishedRunAt(t, srv, "s", start.Add(11*time.Hour), "PASS")    // el día: dentro
			finishedRunAt(t, srv, "s", end.Add(-30*time.Minute), "FAIL")   // su última media hora: dentro
			finishedRunAt(t, srv, "s", end, "FAIL")                        // el día siguiente: fuera
			m := metricsFor(t, srv, "from="+c.day+"&to="+c.day+"&suite=s")
			if m.From != start.UnixMilli() || m.To != end.UnixMilli() {
				t.Fatalf("bounds %s - %s, want %s - %s", time.UnixMilli(m.From).In(loc), time.UnixMilli(m.To).In(loc), start, end)
			}
			if m.Current.Tests != 3 || m.Current.Passed != 2 || m.Current.Failed != 1 {
				t.Fatalf("selected results: %+v", m.Current)
			}
			if len(m.Daily) != 1 || m.Daily[0].Day != c.day || m.Daily[0].Passed != 2 || m.Daily[0].Failed != 1 {
				t.Fatalf("one day bucket: %+v", m.Daily)
			}
		})
	}
}

// Varios días seguidos que incluyen el cambio de horario: límites y días exactos.
func TestCustomRangeOfSeveralDaysAcrossDST(t *testing.T) {
	loc := inZone(t, "America/Santiago")
	srv, _ := newTestServer(t)
	for d := 4; d <= 8; d++ {
		finishedRunAt(t, srv, "s", time.Date(2026, 9, d, 12, 0, 0, 0, loc), "PASS")
	}
	m := metricsFor(t, srv, "from=2026-09-05&to=2026-09-07&suite=s")
	if m.Current.Tests != 3 || len(m.Daily) != 3 || m.Daily[0].Day != "2026-09-05" || m.Daily[2].Day != "2026-09-07" {
		t.Fatalf("3 days: %+v %+v", m.Current, m.Daily)
	}
	if m.From != time.Date(2026, 9, 5, 0, 0, 0, 0, loc).UnixMilli() || m.To != time.Date(2026, 9, 8, 0, 0, 0, 0, loc).UnixMilli() {
		t.Fatalf("bounds: %s - %s", time.UnixMilli(m.From).In(loc), time.UnixMilli(m.To).In(loc))
	}
}
