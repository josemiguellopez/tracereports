package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/notify"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

// fakeTracker records the issues it is asked to create.
type fakeTracker struct {
	id     string
	issues []*tracker.Issue
	fail   error
}

func (f *fakeTracker) ID() string   { return f.id }
func (f *fakeTracker) Name() string { return strings.ToUpper(f.id) }
func (f *fakeTracker) Create(_ context.Context, is *tracker.Issue) (*tracker.Ticket, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	f.issues = append(f.issues, is)
	n := len(f.issues)
	return &tracker.Ticket{Provider: f.id, Key: "T-" + itoa(int64(n)), URL: "https://tracker/T-" + itoa(int64(n))}, nil
}

type ticketAnswer struct {
	Ticket   db.Ticket `json:"ticket"`
	Existing bool      `json:"existing"`
}

func ticket(t *testing.T, srv *Server, body map[string]any, want int) ticketAnswer {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := call(t, srv, "POST", "/api/v1/ui/tickets", string(raw))
	if rec.Code != want {
		t.Fatalf("ticket %v: got %d %s, want %d", body, rec.Code, rec.Body, want)
	}
	var out ticketAnswer
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestCreateTicketFromAFailedTest(t *testing.T) {
	clearAIEnv(t)
	t.Setenv("PUBLIC_URL", "https://reports.acme")
	srv, _ := newTestServer(t)
	srv.Notify = notify.New(srv.Store)
	gh := &fakeTracker{id: "github"}
	srv.Trackers = []tracker.Provider{gh}
	runID, testID := failingRun(t, srv)
	os.WriteFile(filepath.Join(srv.ScreenshotsDir, "pago.png"), []byte("\x89PNG\r\n\x1a\nshot"), 0o644)

	got := ticket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "lang": "es"}, 201)
	if got.Existing || got.Ticket.Key != "T-1" || got.Ticket.URL != "https://tracker/T-1" || got.Ticket.TestID != testID {
		t.Fatalf("created: %+v", got)
	}
	is := gh.issues[0]
	md := tracker.Markdown(is)
	for _, want := range []string{"Pago con tarjeta", "TimeoutError: no llegó la confirmación", "POST shop.test/api/payments/charge → HTTP 503",
		"https://reports.acme/#run=" + itoa(runID)} {
		if !strings.Contains(md, want) {
			t.Errorf("issue misses %q:\n%s", want, md)
		}
	}
	if string(is.Image) != "\x89PNG\r\n\x1a\nshot" {
		t.Fatal("the failure screenshot travels with the issue")
	}

	// el mismo fallo otra vez: devuelve el ticket que ya existe, no crea otro
	again := ticket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "provider": "github"}, 200)
	if !again.Existing || again.Ticket.Key != "T-1" || len(gh.issues) != 1 {
		t.Fatalf("duplicate: %+v (%d issues)", again, len(gh.issues))
	}

	// la ejecución de mañana del mismo test: también apunta al ticket abierto
	run2, _ := srv.Store.CreateRun("Checkout", "staging")
	test2, _ := srv.Store.CreateTest(run2, "Pago con tarjeta", "", "")
	srv.Store.FinishTest(test2, "FAIL", "TimeoutError", "")
	tomorrow := ticket(t, srv, map[string]any{"run_id": run2, "test_id": test2, "provider": "github"}, 200)
	if !tomorrow.Existing || tomorrow.Ticket.Key != "T-1" {
		t.Fatalf("same test in another run: %+v", tomorrow)
	}
	// con force se crea igual
	forced := ticket(t, srv, map[string]any{"run_id": run2, "test_id": test2, "provider": "github", "force": true}, 201)
	if forced.Ticket.Key != "T-2" {
		t.Fatalf("forced: %+v", forced)
	}

	var list []db.Ticket
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs/"+itoa(run2)+"/tickets", "").Body.Bytes(), &list)
	if len(list) != 2 { // el de la ejecución anterior (mismo test) y el forzado
		t.Fatalf("tickets of the run: %+v", list)
	}
}

func TestCreateTicketForTheWholeRun(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	jira := &fakeTracker{id: "jira"}
	srv.Trackers = []tracker.Provider{jira}
	runID, _ := failingRun(t, srv)
	got := ticket(t, srv, map[string]any{"run_id": runID, "provider": "jira", "lang": "en"}, 201)
	if got.Ticket.TestID != 0 || len(jira.issues) != 1 || jira.issues[0].Link != "" {
		t.Fatalf("run ticket (no PUBLIC_URL: no link): %+v %+v", got, jira.issues[0])
	}
	if !strings.Contains(tracker.Markdown(jira.issues[0]), "Checkout") {
		t.Fatalf("run issue: %s", tracker.Markdown(jira.issues[0]))
	}
	if again := ticket(t, srv, map[string]any{"run_id": runID, "provider": "jira"}, 200); !again.Existing {
		t.Fatal("the run already has a ticket")
	}
}

func TestCreateTicketErrors(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	failing := &fakeTracker{id: "azure", fail: errors.New("HTTP 401: bad token")}
	srv.Trackers = []tracker.Provider{&fakeTracker{id: "github"}, failing}
	runID, testID := failingRun(t, srv)
	other, _ := srv.Store.CreateRun("otra", "")

	ticket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "provider": "jira"}, 400)   // no configurado
	ticket(t, srv, map[string]any{"run_id": other, "test_id": testID, "provider": "github"}, 400) // test de otra ejecución
	ticket(t, srv, map[string]any{"run_id": runID, "test_id": 99999, "provider": "github"}, 404)  // no existe
	ticket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "provider": "azure"}, 502)  // el tracker falló
	ticket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "audience": "x"}, 400)
	if tks, _ := srv.Store.TicketsOfRun(runID); len(tks) != 0 {
		t.Fatalf("a failed creation saves nothing: %+v", tks)
	}

	// mismas reglas de acceso que escalar: sin login, otra máquina no puede
	raw, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "provider": "github"})
	if rec := call(t, srv, "POST", "/api/v1/ui/tickets", string(raw), fromAddr("10.0.0.9:1")); rec.Code != 403 {
		t.Fatalf("remote without login: %d", rec.Code)
	}
}

func TestConfigListsTrackersWithoutSecrets(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Trackers = []tracker.Provider{&tracker.GitHub{Repo: "acme/shop", Token: "ghp_SECRET"}}
	rec := call(t, srv, "GET", "/api/v1/config", "")
	if !strings.Contains(rec.Body.String(), `"trackers":[{"id":"github","name":"GitHub"}]`) || strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("config: %s", rec.Body)
	}
}
