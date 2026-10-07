package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func metrics() *db.Metrics {
	return &db.Metrics{
		Days:     7,
		Current:  db.KPIs{Runs: 14, Failed: 6, PassRate: 92.5, FlakyTests: 2},
		Previous: db.KPIs{Runs: 12, Failed: 3, PassRate: 96, FlakyTests: 1},
		TopFailing: []db.TestStat{
			{Name: "test_pago", Fails: 4, Runs: 14, Recent: []string{"FAIL", "FAIL"}},
			{Name: "test_login", Fails: 2, Runs: 14, Recent: []string{"FAIL", "PASS"}}, // ya se arregló
		},
		Flaky: []db.TestStat{{Name: "test_buscar"}},
	}
}

func list(e *Escalation, title string) []string {
	for _, l := range e.Lists {
		if l[0] == title {
			return l[1].([]string)
		}
	}
	return nil
}

func TestWeeklyContent(t *testing.T) {
	e := Weekly(metrics(), "es", "https://reports.acme/")
	if e.Title != "Resumen semanal de pruebas" || e.Severity != "Últimos 7 días" || !e.Critical {
		t.Fatalf("header: %+v", e)
	}
	if e.Headline != "La tasa de éxito bajó 3.5 puntos: 92.5%." || e.URL != "https://reports.acme/#view=metrics" {
		t.Fatalf("headline/url: %q %q", e.Headline, e.URL)
	}
	if e.Sections[1] != [2]string{"Tasa de éxito", "92.5% (semana anterior: 96%)"} {
		t.Fatalf("sections: %v", e.Sections)
	}
	if got := list(e, "Lo que más falla"); len(got) != 1 || got[0] != "test_pago — falló 4 de 14 veces" {
		t.Fatalf("top failing: %v", got)
	}
	if got := list(e, "Ya se arreglaron"); len(got) != 1 || got[0] != "test_login" {
		t.Fatalf("recovered: %v", got)
	}
	if got := list(e, "Inestables a vigilar"); len(got) != 1 {
		t.Fatalf("flaky: %v", got)
	}
}

func TestWeeklyVariants(t *testing.T) {
	m := metrics()
	m.Previous = db.KPIs{}
	if e := Weekly(m, "en", ""); e.Headline != "Pass rate of the week: 92.5%." || e.URL != "" || strings.Contains(e.Sections[0][1], "previous") {
		t.Fatalf("first week: %+v", e)
	}
	m = metrics()
	m.Current.PassRate = 99
	if e := Weekly(m, "en", ""); e.Headline != "The pass rate went up 3 points: 99%." || e.Critical {
		t.Fatalf("up: %+v", e)
	}
	m.Current.PassRate = 96.2
	if e := Weekly(m, "xx", ""); e.Headline != "La tasa de éxito se mantuvo en 96.2%." {
		t.Fatalf("same (unknown language: es): %q", e.Headline)
	}
	empty := Weekly(&db.Metrics{Days: 7}, "es", "")
	if empty.Headline != "Sin ejecuciones en los últimos 7 días." || len(empty.Sections) != 0 {
		t.Fatalf("no runs: %+v", empty)
	}
}

func TestSendWeekly(t *testing.T) {
	var got []map[string]any
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &m)
		got = append(got, m)
	}))
	defer ok.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) }))
	defer broken.Close()

	n := &Notifier{client: ok.Client()}
	if _, err := n.SendWeekly(context.Background(), Weekly(metrics(), "es", "")); err == nil {
		t.Fatal("no channel configured")
	}
	n.teams, n.slack = ok.URL, broken.URL
	sent, err := n.SendWeekly(context.Background(), Weekly(metrics(), "es", ""))
	if err != nil || len(sent) != 1 || sent[0] != "teams" || len(got) != 1 {
		t.Fatalf("one channel works: %v %v (%d posts)", sent, err, len(got))
	}
	n.teams = broken.URL
	if _, err := n.SendWeekly(context.Background(), Weekly(metrics(), "es", "")); err == nil {
		t.Fatal("every channel failed")
	}
}
