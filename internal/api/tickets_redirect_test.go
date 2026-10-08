package api

import (
	"context"
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

// missingHost never resolves: the transport answers it like a DNS failure (no real DNS involved).
const missingHost = "tracker-missing.invalid"

func trackerClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, missingHost) {
				return nil, &net.OpError{Op: "dial", Net: network, Err: &net.DNSError{Err: "no such host", Name: missingHost, IsNotFound: true}}
			}
			return d.DialContext(ctx, network, addr)
		},
	}}
}

func closedURL(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u := "http://" + l.Addr().String()
	l.Close()
	return u
}

// redirectingTracker creates the issue (counts it) and then answers with a redirect to target.
// With target "" it answers 201 like GitHub.
type redirectingTracker struct {
	created atomic.Int32
	target  atomic.Value // string
	code    int
}

func newRedirectingTracker(t *testing.T, code int) (*redirectingTracker, *httptest.Server) {
	rt := &redirectingTracker{code: code}
	rt.target.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/acme/shop/issues" {
			http.NotFound(w, r)
			return
		}
		n := rt.created.Add(1)
		if target := rt.target.Load().(string); target != "" {
			http.Redirect(w, r, target, rt.code)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"number": n, "html_url": "https://github.example/i/" + itoa(int64(n))})
	}))
	t.Cleanup(srv.Close)
	return rt, srv
}

func setGitHub(srv *Server, api string, timeout time.Duration) {
	srv.Trackers = []tracker.Provider{&tracker.GitHub{API: api, Repo: "acme/shop", Token: "fake-token", HTTP: trackerClient(timeout)}}
}

// creatingMarks counts the unfinished creations left for a failure.
func creatingMarks(t *testing.T, srv *Server, runID, testID int64) int {
	t.Helper()
	got, _ := srv.Store.GetTest(testID)
	run, _ := srv.Store.GetRun(runID)
	p := srv.Trackers[0]
	m, err := srv.Store.CreatingTicket(runID, testID, got.Key, run.Project, p.ID(), p.Target())
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		return 0
	}
	return 1
}

// La solicitud llegó al tracker (que pudo crear el ticket) y falló al seguir su redirección:
// conexión rechazada, DNS o timeout en el destino nuevo. Es incierto: no se reintenta a ciegas.
func TestRedirectFailuresAfterACreationAreUncertain(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(500 * time.Millisecond) }))
	defer slow.Close()
	for name, target := range map[string]string{
		"refused": closedURL(t) + "/next",
		"dns":     "http://" + missingHost + "/next",
		"timeout": slow.URL + "/next",
	} {
		for _, code := range []int{http.StatusSeeOther, http.StatusTemporaryRedirect} {
			t.Run(name+"/"+itoa(int64(code)), func(t *testing.T) {
				clearAIEnv(t)
				srv, _ := newTestServer(t)
				rt, gh := newRedirectingTracker(t, code)
				setGitHub(srv, gh.URL, 250*time.Millisecond)
				runID, testID := failingTestIn(t, srv, "shop")
				rt.target.Store(target)
				first := askTicket(t, srv, runID, testID, false)
				if first.Code != http.StatusBadGateway || !first.Uncertain {
					t.Fatalf("uncertain: %+v", first)
				}
				if creatingMarks(t, srv, runID, testID) != 1 {
					t.Fatal("the creating mark is kept")
				}
				rt.target.Store("")
				if again := askTicket(t, srv, runID, testID, false); again.Code != http.StatusConflict || rt.created.Load() != 1 {
					t.Fatalf("no blind retry: %+v (created %d)", again, rt.created.Load())
				}
				forced := askTicket(t, srv, runID, testID, true)
				if forced.Code != http.StatusCreated || rt.created.Load() != 2 || creatingMarks(t, srv, runID, testID) != 0 {
					t.Fatalf("force creates one on purpose and clears the mark: %+v (created %d)", forced, rt.created.Load())
				}
			})
		}
	}
}

// Sin ningún envío previo (no se pudo conectar, el nombre no resuelve) o con un rechazo
// confirmado, no se creó nada: la marca se borra y el reintento normal funciona.
func TestFailuresBeforeAnythingWasSentCanBeRetried(t *testing.T) {
	for name, api := range map[string]string{"refused": closedURL(t), "dns": "http://" + missingHost} {
		t.Run(name, func(t *testing.T) {
			clearAIEnv(t)
			srv, _ := newTestServer(t)
			rt, gh := newRedirectingTracker(t, http.StatusSeeOther)
			setGitHub(srv, api, 2*time.Second)
			runID, testID := failingTestIn(t, srv, "shop")
			if r := askTicket(t, srv, runID, testID, false); r.Code != http.StatusBadGateway || r.Uncertain {
				t.Fatalf("confirmed failure: %+v", r)
			}
			if creatingMarks(t, srv, runID, testID) != 0 {
				t.Fatal("no mark left")
			}
			setGitHub(srv, gh.URL, 2*time.Second)
			if r := askTicket(t, srv, runID, testID, false); r.Code != http.StatusCreated || rt.created.Load() != 1 {
				t.Fatalf("normal retry: %+v (created %d)", r, rt.created.Load())
			}
		})
	}
}

// Una redirección que se sigue con éxito (p. ej. un repositorio renombrado: 307 al nuevo) crea
// el ticket como antes.
func TestRedirectThatSucceedsStillCreates(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	moved, newAPI := newRedirectingTracker(t, http.StatusTemporaryRedirect)
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, newAPIURL(newAPI)+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer old.Close()
	setGitHub(srv, old.URL, 2*time.Second)
	runID, testID := failingTestIn(t, srv, "shop")
	if r := askTicket(t, srv, runID, testID, false); r.Code != http.StatusCreated || moved.created.Load() != 1 {
		t.Fatalf("followed redirect creates: %+v (created %d)", r, moved.created.Load())
	}
}

func newAPIURL(s *httptest.Server) string { return s.URL }

// Solicitudes simultáneas con una redirección fallida: una sola creación llega al tracker.
func TestConcurrentRequestsWithAFailedRedirectCreateOnce(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	rt, gh := newRedirectingTracker(t, http.StatusSeeOther)
	setGitHub(srv, gh.URL, 2*time.Second)
	rt.target.Store(closedURL(t))
	runID, testID := failingTestIn(t, srv, "shop")
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Add(1)
		go func(i int) { defer wg.Done(); codes[i] = askTicket(t, srv, runID, testID, false).Code }(i)
	}
	wg.Wait()
	if rt.created.Load() != 1 {
		t.Fatalf("one creation: %d (%v)", rt.created.Load(), codes)
	}
}
