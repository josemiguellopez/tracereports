package api

import (
	"encoding/json"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

// failingTestIn crea una ejecución del proyecto con un test que falla, con la misma clave.
func failingTestIn(t *testing.T, srv *Server, project string) (runID, testID int64) {
	t.Helper()
	runID, _ = srv.Store.CreateRunWithMeta("Nightly", "qa", db.RunMeta{Project: project})
	testID, _ = srv.Store.CreateTestWithMeta(runID, "test_login", "", "", db.TestMeta{Key: "tests/test_login.py::test_login"})
	srv.Store.FinishTest(testID, "FAIL", "boom", "")
	srv.Store.FinishRun(runID)
	return runID, testID
}

func TestTicketsAreNotSharedAcrossProjects(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	gh := &fakeTracker{id: "github", target: "acme/shop"}
	srv.Trackers = []tracker.Provider{gh}

	shopRun, shopTest := failingTestIn(t, srv, "shop")
	first := ticket(t, srv, map[string]any{"run_id": shopRun, "test_id": shopTest, "provider": "github", "no_ai": true}, 201)

	// otro proyecto, misma clave de test: ticket propio, no el de "shop"
	blogRun, blogTest := failingTestIn(t, srv, "blog")
	other := ticket(t, srv, map[string]any{"run_id": blogRun, "test_id": blogTest, "provider": "github", "no_ai": true}, 201)
	if other.Existing || other.Ticket.Key == first.Ticket.Key || other.Ticket.Project != "blog" {
		t.Fatalf("another project must get its own ticket: %+v", other)
	}
	// el mismo proyecto, mañana: reutiliza el suyo
	shop2, shopTest2 := failingTestIn(t, srv, "shop")
	again := ticket(t, srv, map[string]any{"run_id": shop2, "test_id": shopTest2, "provider": "github", "no_ai": true}, 200)
	if !again.Existing || again.Ticket.Key != first.Ticket.Key {
		t.Fatalf("same project must reuse: %+v", again)
	}
	// la lista de una ejecución solo trae los del proyecto
	var list []db.Ticket
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs/"+itoa(blogRun)+"/tickets", "").Body.Bytes(), &list)
	if len(list) != 1 || list[0].Key != other.Ticket.Key {
		t.Fatalf("blog run lists only blog tickets: %+v", list)
	}
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs/"+itoa(shop2)+"/tickets", "").Body.Bytes(), &list)
	if len(list) != 1 || list[0].Key != first.Ticket.Key {
		t.Fatalf("shop run lists the shop ticket: %+v", list)
	}

	// el tracker ahora apunta a otro repositorio: no se reutiliza el del repo anterior
	gh.target = "acme/other-repo"
	shop3, shopTest3 := failingTestIn(t, srv, "shop")
	moved := ticket(t, srv, map[string]any{"run_id": shop3, "test_id": shopTest3, "provider": "github", "no_ai": true}, 201)
	if moved.Existing {
		t.Fatalf("a ticket of another destination must not be reused: %+v", moved)
	}
}
