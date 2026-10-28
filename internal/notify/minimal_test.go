package notify

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata/minimal")

// Textos únicos de la evidencia: en NOTIFY_DETAIL=minimal ninguno puede llegar al canal.
var leaks = []string{"TEST-UNICO-checkout-7f3a", "ERROR-UNICO-db-pool-9c1e", "backend-unico-4b2d.test", "IA-UNICO-headline",
	"IA-UNICO-summary", "IA-UNICO-action", "IA-UNICO-title", "IA-UNICO-cause", "IA-UNICO-evidence", "/screenshots/unico.png"}

// hook captura lo que recibe cada canal.
func hook(t *testing.T) (*httptest.Server, func(path string) string) {
	t.Helper()
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.URL.Path] = string(raw)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, func(p string) string { mu.Lock(); defer mu.Unlock(); return got[p] }
}

// golden compara el payload (JSON indentado) con testdata/minimal/<name>.json.
func golden(t *testing.T, name, payload string) {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(payload), &v); err != nil {
		t.Fatalf("%s: JSON inválido: %s", name, payload)
	}
	pretty, _ := json.MarshalIndent(v, "", "  ")
	path := filepath.Join("testdata", "minimal", name+".json")
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, append(pretty, '\n'), 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (go test -run Minimal -update)", name, err)
	}
	if strings.TrimSpace(strings.ReplaceAll(string(want), "\r\n", "\n")) != strings.TrimSpace(string(pretty)) {
		t.Errorf("%s cambió:\n%s", name, pretty)
	}
}

func noLeaks(t *testing.T, name, payload string) {
	t.Helper()
	for _, s := range leaks {
		if strings.Contains(payload, s) {
			t.Errorf("%s contiene %q en NOTIFY_DETAIL=minimal: %s", name, s, payload)
		}
	}
}

func minimalEnv(t *testing.T, url string) {
	t.Setenv("TEAMS_WEBHOOK_URL", url+"/teams")
	t.Setenv("SLACK_WEBHOOK_URL", url+"/slack")
	t.Setenv("PUBLIC_URL", "https://reports.example.com")
	t.Setenv("NOTIFY_ON", "")
	t.Setenv("NOTIFY_DETAIL", "minimal")
}

func TestMinimalRunNotification(t *testing.T) {
	srv, got := hook(t)
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runID, _ := store.CreateRun("Regresión web", "qa")
	id, _ := store.CreateTest(runID, "TEST-UNICO-checkout-7f3a", "", "")
	store.AddNetwork(id, []db.NetConn{{Method: "POST", URL: "https://backend-unico-4b2d.test/api/pay", Status: 500}})
	store.FinishTest(id, "FAIL", "ERROR-UNICO-db-pool-9c1e", "")
	ok, _ := store.CreateTest(runID, "pasa", "", "")
	store.FinishTest(ok, "PASS", "", "")
	store.FinishRun(runID)
	store.SetRunTimes(runID, 1_790_000_000_000, 1_790_000_042_000)
	store.SaveRunTriage(runID, &db.RunTriage{State: "DONE", Headline: "IA-UNICO-headline", Summary: "IA-UNICO-summary",
		Incidents: []db.Incident{{Key: "k", Title: "Backend: POST backend-unico-4b2d.test/api/pay → HTTP 500", TestIDs: []int64{id},
			Cause: "IA-UNICO-cause", Action: "IA-UNICO-action"}}})

	minimalEnv(t, srv.URL)
	n := New(store)
	if !n.Minimal() {
		t.Fatal("NOTIFY_DETAIL=minimal")
	}
	n.RunFinished(runID)
	for _, ch := range []string{"teams", "slack"} {
		p := got("/" + ch)
		noLeaks(t, ch, p)
		for _, want := range []string{"Regresión web", "2 tests · 1 OK · 1 fallidos", "#run=1"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s sin %q: %s", ch, want, p)
			}
		}
		golden(t, "run_"+ch, p)
	}
}

func TestMinimalEscalation(t *testing.T) {
	srv, got := hook(t)
	minimalEnv(t, srv.URL)
	n := New(nil)
	e := &Escalation{Title: "IA-UNICO-title", Severity: "Severidad alta", Critical: true, Headline: "IA-UNICO-headline",
		Sections: [][2]string{{"Qué pasó", "ERROR-UNICO-db-pool-9c1e"}, {"Causa", "IA-UNICO-cause"}},
		Lists:    [][2]any{{"Evidencia", []string{"IA-UNICO-evidence", "POST https://backend-unico-4b2d.test/api/pay → 500"}}},
		ImageURL: "https://reports.example.com/screenshots/unico.png", URL: "https://reports.example.com/#run=1&test=2",
		LinkLabel: "Ver en TraceReports", Lang: "es"}
	for _, ch := range []string{"teams", "slack"} {
		if err := n.SendEscalation(context.Background(), ch, e); err != nil {
			t.Fatalf("%s: %v", ch, err)
		}
		p := got("/" + ch)
		noLeaks(t, ch, p)
		for _, want := range []string{"Escalamiento para revisar", "Severidad alta", "#run=1"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s sin %q: %s", ch, want, p)
			}
		}
		golden(t, "escalation_"+ch, p)
	}
	// el escalamiento original no se modifica (la UI lo sigue mostrando completo)
	if e.Title != "IA-UNICO-title" || len(e.Sections) != 2 {
		t.Fatal("forChannels modificó el escalamiento")
	}
}

func TestMinimalWeeklySummary(t *testing.T) {
	srv, got := hook(t)
	minimalEnv(t, srv.URL)
	n := New(nil)
	m := &db.Metrics{Days: 7}
	m.Current.Runs, m.Current.PassRate, m.Current.Failed, m.Current.FlakyTests = 12, 91.5, 3, 1
	m.Previous.Runs, m.Previous.PassRate = 10, 88
	m.TopFailing = []db.TestStat{{Name: "TEST-UNICO-checkout-7f3a", Fails: 3, Runs: 12, Recent: []string{"FAIL"}}}
	m.Flaky = []db.TestStat{{Name: "TEST-UNICO-checkout-7f3a"}}
	e := Weekly(m, "es", "https://reports.example.com")
	if _, err := n.SendWeekly(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []string{"teams", "slack"} {
		p := got("/" + ch)
		noLeaks(t, ch, p)
		for _, want := range []string{"Resumen semanal de pruebas", "91.5%", "#view=metrics"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s sin %q: %s", ch, want, p)
			}
		}
		golden(t, "weekly_"+ch, p)
	}
}

// Sin NOTIFY_DETAIL (full, el valor por defecto) todo sigue igual: el escalamiento va completo.
func TestFullDetailIsTheDefault(t *testing.T) {
	srv, got := hook(t)
	minimalEnv(t, srv.URL)
	t.Setenv("NOTIFY_DETAIL", "")
	n := New(nil)
	if n.Minimal() {
		t.Fatal("full por defecto")
	}
	e := &Escalation{Title: "IA-UNICO-title", Severity: "Severidad alta", Headline: "h", Sections: [][2]string{{"Causa", "IA-UNICO-cause"}}, Lang: "es"}
	if err := n.SendEscalation(context.Background(), "slack", e); err != nil {
		t.Fatal(err)
	}
	if p := got("/slack"); !strings.Contains(p, "IA-UNICO-title") || !strings.Contains(p, "IA-UNICO-cause") {
		t.Fatalf("full: %s", p)
	}
}
