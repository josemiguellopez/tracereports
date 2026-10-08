package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/tracker"
)

// La respuesta de la API ante un error del tracker no lleva su token (ni en el rechazo
// confirmado ni en el incierto), y la marca de creación se borra o se conserva como antes.
func TestTicketErrorsNeverReturnTheTrackerToken(t *testing.T) {
	const token = "fake-trk+token/with=chars_123456"
	for _, c := range []struct {
		code      int
		uncertain bool
	}{{401, false}, {503, true}} {
		clearAIEnv(t)
		srv, _ := newTestServer(t)
		gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.code)
			w.Write([]byte(`{"message":"Bad credentials for ` + token + ` (` + r.Header.Get("Authorization") + `)"}`))
		}))
		srv.Trackers = []tracker.Provider{&tracker.GitHub{API: gh.URL, Repo: "acme/shop", Token: token, HTTP: &http.Client{Timeout: 2 * time.Second}}}
		runID, testID := failingTestIn(t, srv, "shop")
		r := askTicket(t, srv, runID, testID, false)
		gh.Close()
		if r.Code != http.StatusBadGateway || r.Uncertain != c.uncertain || strings.Contains(r.Error, token) || !strings.Contains(r.Error, "Bad credentials") {
			t.Fatalf("%d: %+v", c.code, r)
		}
		marks := 0
		if c.uncertain {
			marks = 1
		}
		if got := creatingMarks(t, srv, runID, testID); got != marks {
			t.Fatalf("%d: creating marks %d, want %d", c.code, got, marks)
		}
	}
}
