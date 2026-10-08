package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// hookServer responde según el canal: el script de cada ruta se consume en orden; vacío = 200.
type hookServer struct {
	*httptest.Server
	mu     sync.Mutex
	script map[string][]func(w http.ResponseWriter)
	posts  map[string]int
}

func newHook(t *testing.T) *hookServer {
	h := &hookServer{script: map[string][]func(http.ResponseWriter){}, posts: map[string]int{}}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.posts[r.URL.Path]++
		var next func(http.ResponseWriter)
		if s := h.script[r.URL.Path]; len(s) > 0 {
			next, h.script[r.URL.Path] = s[0], s[1:]
		}
		h.mu.Unlock()
		if next != nil {
			next(w)
		}
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *hookServer) on(path string, fns ...func(http.ResponseWriter)) {
	h.mu.Lock()
	h.script[path] = append(h.script[path], fns...)
	h.mu.Unlock()
}

func (h *hookServer) count(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.posts[path]
}

func status(code int, headers ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.WriteHeader(code)
	}
}

func failedRun(t *testing.T) (*db.Store, int64) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runID, _ := store.CreateRun("Regresión", "qa")
	id, _ := store.CreateTest(runID, "Login", "", "")
	store.FinishTest(id, "FAIL", "boom", "")
	store.FinishRun(runID)
	return store, runID
}

func setHooks(t *testing.T, h *hookServer) {
	t.Setenv("TEAMS_WEBHOOK_URL", h.URL+"/teams")
	t.Setenv("SLACK_WEBHOOK_URL", h.URL+"/slack")
	t.Setenv("NOTIFY_ON", "")
}

func noBackoff(t *testing.T) {
	old := Backoff
	Backoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { Backoff = old })
}

func deliveriesOf(t *testing.T, store *db.Store, ids ...int64) []*db.Delivery {
	t.Helper()
	var out []*db.Delivery
	for _, id := range ids {
		d, err := store.GetDelivery(id)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func TestTransientFailureIsRetriedOnlyOnThatChannel(t *testing.T) {
	noBackoff(t)
	h := newHook(t)
	setHooks(t, h)
	h.on("/slack", status(503), status(502))
	store, runID := failedRun(t)
	n := New(store)
	n.RunFinished(runID)
	if h.count("/teams") != 1 || h.count("/slack") != 1 {
		t.Fatalf("first attempt: teams=%d slack=%d", h.count("/teams"), h.count("/slack"))
	}
	n.RetryDue(context.Background()) // 502: sigue pendiente
	n.RetryDue(context.Background()) // ahora sí
	n.RetryDue(context.Background()) // nada más que hacer
	if h.count("/teams") != 1 || h.count("/slack") != 3 {
		t.Fatalf("teams must not be resent; slack retried until it worked: teams=%d slack=%d", h.count("/teams"), h.count("/slack"))
	}
	for _, d := range deliveriesOf(t, store, 1, 2) {
		if d.State != db.DeliverySent {
			t.Fatalf("delivery %d: %+v", d.ID, d)
		}
	}
	// el mismo resumen de la misma ejecución no se envía otra vez
	n.RunFinished(runID)
	if h.count("/teams") != 1 || h.count("/slack") != 3 {
		t.Fatal("the same run summary is not sent twice")
	}
}

func TestPermanentErrorsAreNotRetried(t *testing.T) {
	noBackoff(t)
	h := newHook(t)
	setHooks(t, h)
	h.on("/teams", status(404))
	h.on("/slack", status(400))
	store, runID := failedRun(t)
	n := New(store)
	n.RunFinished(runID)
	n.RetryDue(context.Background())
	if h.count("/teams") != 1 || h.count("/slack") != 1 {
		t.Fatalf("4xx must not be retried: teams=%d slack=%d", h.count("/teams"), h.count("/slack"))
	}
	for _, d := range deliveriesOf(t, store, 1, 2) {
		if d.State != db.DeliveryFailed || !strings.Contains(d.LastError, "HTTP 4") {
			t.Fatalf("failed with its reason: %+v", d)
		}
	}
}

func TestRetryAfterIsHonored(t *testing.T) {
	noBackoff(t)
	h := newHook(t)
	setHooks(t, h)
	t.Setenv("SLACK_WEBHOOK_URL", "")
	h.on("/teams", status(429, "Retry-After", "120"))
	store, runID := failedRun(t)
	before := time.Now()
	New(store).RunFinished(runID)
	d := deliveriesOf(t, store, 1)[0]
	wait := time.UnixMilli(d.NextAt).Sub(before)
	if d.State != db.DeliveryPending || wait < 115*time.Second || wait > 130*time.Second {
		t.Fatalf("429 + Retry-After 120: state=%s wait=%s", d.State, wait)
	}
	// antes de tiempo no se reintenta
	New(store).RetryDue(context.Background())
	if h.count("/teams") != 1 {
		t.Fatal("must wait for Retry-After")
	}
	if got := parseRetryAfter(time.Now().Add(90*time.Second).UTC().Format(http.TimeFormat), time.Now()); got < 85*time.Second || got > 91*time.Second {
		t.Fatalf("HTTP date: %s", got)
	}
	if parseRetryAfter("99999999", time.Now()) != maxRetryAfter {
		t.Fatal("capped")
	}
}

func TestPendingDeliveriesSurviveRestart(t *testing.T) {
	noBackoff(t)
	h := newHook(t)
	setHooks(t, h)
	t.Setenv("SLACK_WEBHOOK_URL", "")
	h.on("/teams", status(500))
	store, runID := failedRun(t)
	New(store).RunFinished(runID)
	// "reinicio": un Notifier nuevo sobre la misma base retoma lo pendiente
	n2 := New(store)
	n2.RetryDue(context.Background())
	if h.count("/teams") != 2 || deliveriesOf(t, store, 1)[0].State != db.DeliverySent {
		t.Fatalf("resumed after restart: posts=%d", h.count("/teams"))
	}
}

func TestGivesUpAfterMaxAttemptsAndWhenTheChannelIsRemoved(t *testing.T) {
	noBackoff(t)
	h := newHook(t)
	setHooks(t, h)
	for i := 0; i < maxDeliveryAttempts+2; i++ {
		h.on("/teams", status(503))
	}
	h.on("/slack", status(503))
	store, runID := failedRun(t)
	n := New(store)
	n.RunFinished(runID)
	t.Setenv("SLACK_WEBHOOK_URL", "") // slack se quitó de la configuración
	n = New(store)
	for i := 0; i < maxDeliveryAttempts+2; i++ {
		n.RetryDue(context.Background())
	}
	ds := deliveriesOf(t, store, 1, 2)
	if ds[0].State != db.DeliveryFailed || ds[0].Attempts != maxDeliveryAttempts || h.count("/teams") != maxDeliveryAttempts {
		t.Fatalf("teams gives up after %d attempts: %+v posts=%d", maxDeliveryAttempts, ds[0], h.count("/teams"))
	}
	if ds[1].State != db.DeliveryFailed || !strings.Contains(ds[1].LastError, "ya no está configurado") {
		t.Fatalf("removed channel: %+v", ds[1])
	}
}

func TestWebhookURLIsNotStored(t *testing.T) {
	h := newHook(t)
	setHooks(t, h)
	t.Setenv("TEAMS_WEBHOOK_URL", h.URL+"/teams?token=SECRET-WEBHOOK-TOKEN")
	store, runID := failedRun(t)
	New(store).RunFinished(runID)
	for _, d := range deliveriesOf(t, store, 1, 2) {
		if strings.Contains(string(d.Payload), "SECRET-WEBHOOK-TOKEN") || strings.Contains(d.DedupKey, "SECRET") {
			t.Fatal("the webhook URL (a credential) must not be stored")
		}
	}
}

func TestWeeklyScheduledOncePerSlotAndOnDemandAgain(t *testing.T) {
	h := newHook(t)
	setHooks(t, h)
	store, _ := failedRun(t)
	n := New(store)
	e := &Escalation{Title: "Resumen semanal", Headline: "h"}
	slot := time.Date(2026, 10, 5, 9, 0, 0, 0, time.Local)
	for i := 0; i < 2; i++ {
		if sent, err := n.SendWeeklyScheduled(context.Background(), e, slot); err != nil || len(sent) != 2 {
			t.Fatalf("scheduled: %v %v", sent, err)
		}
	}
	if h.count("/teams") != 1 || h.count("/slack") != 1 {
		t.Fatalf("one send per slot: teams=%d slack=%d", h.count("/teams"), h.count("/slack"))
	}
	// "Enviar ahora" dos veces: la persona lo pidió dos veces
	n.SendWeekly(context.Background(), e)
	n.SendWeekly(context.Background(), e)
	if h.count("/teams") != 3 {
		t.Fatalf("on demand sends again: %d", h.count("/teams"))
	}
	// un fallo pasajero en "Enviar ahora" lo dice y queda en cola
	h.on("/slack", status(503))
	sent, err := n.SendWeekly(context.Background(), &Escalation{Title: "otro"})
	if err != nil || len(sent) != 1 || sent[0] != "teams" {
		t.Fatalf("partial: %v %v", sent, err)
	}
	if err := n.SendEscalation(context.Background(), "slack", &Escalation{Title: "x"}); err != nil {
		t.Fatalf("escalation ok: %v", err)
	}
	h.on("/slack", status(503))
	err = n.SendEscalation(context.Background(), "slack", &Escalation{Title: "y"})
	if err == nil || !strings.Contains(err.Error(), "se reintentará") {
		t.Fatalf("transient escalation failure says it is queued: %v", err)
	}
}
