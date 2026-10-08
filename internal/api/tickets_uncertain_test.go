package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/tracker"
)

// fakeGitHub is a GitHub issues API whose answer to each creation is chosen by the test. It
// counts the issues it really created.
type fakeGitHub struct {
	created atomic.Int32
	mode    atomic.Value // string
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	f := &fakeGitHub{}
	f.mode.Store("ok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := f.mode.Load().(string)
		switch mode {
		case "reject": // validación: no se crea nada
			http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
			return
		case "unavailable": // 503 de un proxy: no se sabe si llegó a crearse
			http.Error(w, `{"message":"upstream"}`, http.StatusServiceUnavailable)
			return
		}
		n := f.created.Add(1)
		switch mode {
		case "lost": // se crea y la conexión se cierra sin respuesta
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		case "slow": // se crea y la respuesta llega tarde
			time.Sleep(400 * time.Millisecond)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"number": n, "html_url": "https://github.example/i/1"})
		case "garbage": // se crea y la respuesta no se entiende
			w.WriteHeader(201)
			w.Write([]byte("<html>proxy</html>"))
		case "empty": // se crea y la respuesta no dice cuál
			w.WriteHeader(201)
			w.Write([]byte("{}"))
		default:
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"number": n, "html_url": "https://github.example/i/" + itoa(int64(n))})
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func githubAt(url string, timeout time.Duration) *tracker.GitHub {
	return &tracker.GitHub{API: url, Repo: "acme/shop", Token: "fake-token", HTTP: &http.Client{Timeout: timeout}}
}

type trackerAnswer struct {
	Code      int
	Key       string
	Existing  bool
	Uncertain bool
	Error     string
}

func askTicket(t *testing.T, srv *Server, runID, testID int64, force bool) trackerAnswer {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "no_ai": true, "force": force})
	rec := call(t, srv, "POST", "/api/v1/ui/tickets", string(raw))
	var out struct {
		Ticket    struct{ Key string } `json:"ticket"`
		Existing  bool                 `json:"existing"`
		Uncertain bool                 `json:"uncertain"`
		Error     string               `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return trackerAnswer{rec.Code, out.Ticket.Key, out.Existing, out.Uncertain, out.Error}
}

// Cuando el tracker pudo haber creado el ticket (respuesta perdida, timeout, 5xx, respuesta de
// creación ilegible o sin número), un reintento normal no crea otro: avisa y pide revisar o force.
func TestUncertainCreationIsNotRetriedBlindly(t *testing.T) {
	for _, mode := range []string{"lost", "slow", "unavailable", "garbage", "empty"} {
		t.Run(mode, func(t *testing.T) {
			clearAIEnv(t)
			srv, _ := newTestServer(t)
			f, gh := newFakeGitHub(t)
			srv.Trackers = []tracker.Provider{githubAt(gh.URL, 200*time.Millisecond)}
			runID, testID := failingTestIn(t, srv, "shop")
			f.mode.Store(mode)
			first := askTicket(t, srv, runID, testID, false)
			if first.Code != http.StatusBadGateway || !first.Uncertain || !strings.Contains(first.Error, "force") {
				t.Fatalf("first answer reports the doubt: %+v", first)
			}
			f.mode.Store("ok")
			before := f.created.Load()
			again := askTicket(t, srv, runID, testID, false)
			if again.Code != http.StatusConflict || f.created.Load() != before {
				t.Fatalf("a normal retry does not create another: %+v (created %d -> %d)", again, before, f.created.Load())
			}
			if list, _ := srv.Store.TicketsOfRun(runID); len(list) != 0 {
				t.Fatalf("nothing is shown as created: %+v", list)
			}
			forced := askTicket(t, srv, runID, testID, true)
			if forced.Code != http.StatusCreated || f.created.Load() != before+1 {
				t.Fatalf("force creates one, on purpose: %+v", forced)
			}
			if r := askTicket(t, srv, runID, testID, false); r.Code != http.StatusOK || !r.Existing || r.Key != forced.Key {
				t.Fatalf("then it is reused: %+v", r)
			}
		})
	}
}

// Un rechazo confirmado (4xx) o un tracker al que no se pudo conectar no crean nada: el
// reintento normal funciona.
func TestConfirmedFailuresCanBeRetried(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	f, gh := newFakeGitHub(t)
	srv.Trackers = []tracker.Provider{githubAt(gh.URL, 2*time.Second)}
	runID, testID := failingTestIn(t, srv, "shop")
	f.mode.Store("reject")
	if r := askTicket(t, srv, runID, testID, false); r.Code != http.StatusBadGateway || r.Uncertain {
		t.Fatalf("rejected: %+v", r)
	}
	// puerto cerrado: la conexión no se estableció
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := "http://" + l.Addr().String()
	l.Close()
	srv.Trackers = []tracker.Provider{githubAt(closed, 2*time.Second)}
	if r := askTicket(t, srv, runID, testID, false); r.Code != http.StatusBadGateway || r.Uncertain {
		t.Fatalf("unreachable: %+v", r)
	}
	srv.Trackers = []tracker.Provider{githubAt(gh.URL, 2*time.Second)}
	f.mode.Store("ok")
	if r := askTicket(t, srv, runID, testID, false); r.Code != http.StatusCreated || f.created.Load() != 1 {
		t.Fatalf("retry after confirmed failures: %+v (created %d)", r, f.created.Load())
	}
}

// Solicitudes simultáneas con la respuesta perdida: el tracker recibe una sola creación.
func TestConcurrentRequestsWithALostReplyCreateOnce(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	f, gh := newFakeGitHub(t)
	srv.Trackers = []tracker.Provider{githubAt(gh.URL, 2*time.Second)}
	runID, testID := failingTestIn(t, srv, "shop")
	f.mode.Store("lost")
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Add(1)
		go func(i int) { defer wg.Done(); codes[i] = askTicket(t, srv, runID, testID, false).Code }(i)
	}
	wg.Wait()
	if f.created.Load() != 1 {
		t.Fatalf("one creation reached the tracker: %d (%v)", f.created.Load(), codes)
	}
}
