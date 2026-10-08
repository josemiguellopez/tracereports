package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

// slowTracker simula un tracker que tarda en crear: si dos solicitudes llegan a Create a la vez,
// ambas crean (el bug). Una implementación correcta serializa por identidad y no llega dos veces.
type slowTracker struct {
	id, target string
	delay      time.Duration
	creates    atomic.Int32
	inFlight   atomic.Int32
	overlap    atomic.Bool
	fail       atomic.Bool
}

func (f *slowTracker) ID() string     { return f.id }
func (f *slowTracker) Name() string   { return strings.ToUpper(f.id) }
func (f *slowTracker) Target() string { return f.id + ":" + f.target }
func (f *slowTracker) Create(ctx context.Context, is *tracker.Issue) (*tracker.Ticket, error) {
	if f.inFlight.Add(1) > 1 {
		f.overlap.Store(true)
	}
	defer f.inFlight.Add(-1)
	time.Sleep(f.delay)
	if f.fail.Load() {
		return nil, tracker.NotCreated(errors.New("tracker rejected the request")) // rechazo confirmado: no creó nada
	}
	n := f.creates.Add(1)
	return &tracker.Ticket{Provider: f.id, Key: fmt.Sprintf("T-%d", n), URL: fmt.Sprintf("https://tracker/%s/T-%d", f.target, n)}, nil
}

type ticketReply struct {
	Code     int
	Key      string
	Existing bool
}

func postTicket(t *testing.T, srv *Server, body map[string]any, idemKey string) ticketReply {
	raw, _ := json.Marshal(body)
	opts := []reqOpt{}
	if idemKey != "" {
		opts = append(opts, header("Idempotency-Key", idemKey))
	}
	rec := call(t, srv, "POST", "/api/v1/ui/tickets", string(raw), opts...)
	var out struct {
		Ticket   db.Ticket `json:"ticket"`
		Existing bool      `json:"existing"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return ticketReply{rec.Code, out.Ticket.Key, out.Existing}
}

func TestConcurrentTicketRequestsCreateOneTicket(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	tr := &slowTracker{id: "github", target: "acme/shop", delay: 150 * time.Millisecond}
	srv.Trackers = []tracker.Provider{tr}
	runID, testID := failingTestIn(t, srv, "shop")
	body := map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "no_ai": true}

	var wg sync.WaitGroup
	replies := make([]ticketReply, 2)
	for i := range replies {
		wg.Add(1)
		go func(i int) { defer wg.Done(); replies[i] = postTicket(t, srv, body, fmt.Sprintf("click-%d", i)) }(i)
	}
	wg.Wait()
	if n := tr.creates.Load(); n != 1 || tr.overlap.Load() {
		t.Fatalf("one ticket for one failure: %d creates (overlap %v), replies %+v", n, tr.overlap.Load(), replies)
	}
	created, reused := 0, 0
	for _, r := range replies {
		switch {
		case r.Code == 201 && !r.Existing:
			created++
		case r.Code == 200 && r.Existing:
			reused++
		}
		if r.Key != "T-1" {
			t.Errorf("both answer the same ticket: %+v", replies)
		}
	}
	if created != 1 || reused != 1 {
		t.Fatalf("one created, one reused: %+v", replies)
	}
	// después, se reutiliza
	if r := postTicket(t, srv, body, ""); r.Code != 200 || !r.Existing || r.Key != "T-1" || tr.creates.Load() != 1 {
		t.Fatalf("reuse: %+v", r)
	}
	// force crea otro, a propósito
	body["force"] = true
	if r := postTicket(t, srv, body, ""); r.Code != 201 || r.Key != "T-2" {
		t.Fatalf("force: %+v", r)
	}
}

func TestDifferentProjectsAndDestinationsDoNotWaitOnEachOther(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	tr := &slowTracker{id: "github", target: "acme/shop", delay: 150 * time.Millisecond}
	srv.Trackers = []tracker.Provider{tr}
	shopRun, shopTest := failingTestIn(t, srv, "shop")
	blogRun, blogTest := failingTestIn(t, srv, "blog")
	var wg sync.WaitGroup
	for _, b := range []map[string]any{
		{"run_id": shopRun, "test_id": shopTest, "provider": "github", "no_ai": true},
		{"run_id": blogRun, "test_id": blogTest, "provider": "github", "no_ai": true},
	} {
		wg.Add(1)
		go func(b map[string]any) { defer wg.Done(); postTicket(t, srv, b, "") }(b)
	}
	wg.Wait()
	if tr.creates.Load() != 2 {
		t.Fatalf("two projects, two tickets: %d", tr.creates.Load())
	}
}

func TestProviderErrorLeavesNothingAndCanBeRetried(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	tr := &slowTracker{id: "github", target: "acme/shop"}
	tr.fail.Store(true)
	srv.Trackers = []tracker.Provider{tr}
	runID, testID := failingTestIn(t, srv, "shop")
	body := map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "no_ai": true}
	if r := postTicket(t, srv, body, ""); r.Code != 502 {
		t.Fatalf("provider error: %+v", r)
	}
	if list, _ := srv.Store.TicketsOfRun(runID); len(list) != 0 {
		t.Fatalf("no ticket recorded after a provider error: %+v", list)
	}
	tr.fail.Store(false)
	if r := postTicket(t, srv, body, ""); r.Code != 201 || r.Key != "T-1" {
		t.Fatalf("retry after the error: %+v", r)
	}
}

// Una caída después de crear en el tracker y antes de guardarlo deja la duda: no se crea otro en
// silencio, se avisa; con force sí.
func TestInterruptedCreationIsReportedInsteadOfDuplicated(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	tr := &slowTracker{id: "github", target: "acme/shop"}
	srv.Trackers = []tracker.Provider{tr}
	runID, testID := failingTestIn(t, srv, "shop")
	// el servidor anterior empezó a crear y se cayó: queda la marca
	run, _ := srv.Store.GetRun(runID)
	got, _ := srv.Store.GetTest(testID)
	if _, err := srv.Store.BeginTicket(&db.Ticket{RunID: runID, TestID: testID, TestKey: got.Key, Project: run.Project,
		Target: tr.Target(), Provider: "github"}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "no_ai": true}
	rec := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		return call(t, srv, "POST", "/api/v1/ui/tickets", string(raw))
	}()
	if rec.Code != 409 || tr.creates.Load() != 0 || !strings.Contains(rec.Body.String(), "force") {
		t.Fatalf("an unfinished creation is reported: %d %s", rec.Code, rec.Body)
	}
	body["force"] = true
	if r := postTicket(t, srv, body, ""); r.Code != 201 || tr.creates.Load() != 1 {
		t.Fatalf("force creates anyway: %+v", r)
	}
	if r := postTicket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "no_ai": true}, ""); r.Code != 200 || !r.Existing {
		t.Fatalf("then it is reused: %+v", r)
	}
}
