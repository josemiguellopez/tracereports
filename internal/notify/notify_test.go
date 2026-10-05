package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestRunFinishedPostsToTeamsAndSlack(t *testing.T) {
	got := map[string]map[string]any{}
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("invalid JSON: %s", raw)
		}
		got[r.URL.Path] = payload
	}))
	defer hook.Close()

	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runID, _ := store.CreateRun("Regresión web", "qa")
	id, _ := store.CreateTest(runID, "Login", "", "")
	store.FinishTest(id, "FAIL", "boom", "")
	store.FinishRun(runID)
	store.SaveRunTriage(runID, &db.RunTriage{State: "DONE", Headline: "1 test falló: Backend caído.",
		Incidents: []db.Incident{{Key: "k", Title: "Backend: POST /auth → 500", TestIDs: []int64{id}, Action: "Revisar auth"}}})

	t.Setenv("TEAMS_WEBHOOK_URL", hook.URL+"/teams")
	t.Setenv("SLACK_WEBHOOK_URL", hook.URL+"/slack")
	t.Setenv("PUBLIC_URL", "https://reports.example.com/")
	n := New(store)
	n.RunFinished(runID)

	teams, _ := json.Marshal(got["/teams"])
	for _, want := range []string{"AdaptiveCard", "❌ Regresión web", "Backend: POST /auth → 500", "Revisar auth",
		"https://reports.example.com/#run=1\\u0026view=dashboard"} {
		if !strings.Contains(string(teams), want) {
			t.Errorf("teams payload missing %q: %s", want, teams)
		}
	}
	slack, _ := json.Marshal(got["/slack"])
	if !strings.Contains(string(slack), "1 test falló: Backend caído.") || !strings.Contains(string(slack), `"type":"button"`) {
		t.Errorf("slack payload: %s", slack)
	}

	// NOTIFY_ON=failures: una ejecución sin fallos no notifica.
	got = map[string]map[string]any{}
	okRun, _ := store.CreateRun("Verde", "")
	store.FinishRun(okRun)
	t.Setenv("NOTIFY_ON", "failures")
	New(store).RunFinished(okRun)
	if len(got) != 0 {
		t.Errorf("should not notify a passing run with NOTIFY_ON=failures: %v", got)
	}
}
