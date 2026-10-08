package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/notify"
)

// fakeClock drives the scheduler: each After call is handed to the test, which moves the time
// and fires it when it wants (no real waiting).
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits chan fakeWait
}

type fakeWait struct {
	d    time.Duration
	fire chan time.Time
}

func (c *fakeClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) set(t time.Time) { c.mu.Lock(); c.now = t; c.mu.Unlock() }
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	w := fakeWait{d: d, fire: make(chan time.Time, 1)}
	c.waits <- w
	return w.fire
}

func (c *fakeClock) nextWait(t *testing.T) fakeWait {
	t.Helper()
	select {
	case w := <-c.waits:
		return w
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduler did not wait for its next slot")
	}
	return fakeWait{}
}

// weeklyHarness starts the real scheduler (startWeekly) with a controlled clock, the real outbox
// and a fake Teams webhook that counts the summaries it receives.
func weeklyHarness(t *testing.T, start time.Time) (*fakeClock, *atomic.Int32, context.CancelFunc) {
	t.Helper()
	var posts atomic.Int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		posts.Add(1)
	}))
	t.Cleanup(hook.Close)
	t.Setenv("TEAMS_WEBHOOK_URL", hook.URL)
	t.Setenv("SLACK_WEBHOOK_URL", "")
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "weekly.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: dir, Web: fstest.MapFS{"index.html": {}}}
	clock := &fakeClock{now: start, waits: make(chan fakeWait)}
	prev := schedClock
	schedClock = clock
	t.Cleanup(func() { schedClock = prev })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := startWeekly(ctx, "mon 09:00", srv, notify.New(store)); err != nil {
		t.Fatal(err)
	}
	return clock, &posts, cancel
}

// Un despertar tardío (más de un minuto) envía el resumen de SU turno; el de la semana siguiente
// se envía igual a su hora: dos semanas, dos resúmenes.
func TestWeeklyLateWakeKeepsItsSlot(t *testing.T) {
	loc := time.FixedZone("CL", -3*3600)
	clock, posts, _ := weeklyHarness(t, time.Date(2026, 10, 7, 10, 0, 0, 0, loc)) // miércoles
	monday := time.Date(2026, 10, 12, 9, 0, 0, 0, loc)

	w := clock.nextWait(t)
	if w.d != monday.Sub(clock.Now()) {
		t.Fatalf("first wait %v, want until %s", w.d, monday)
	}
	clock.set(monday.Add(2 * time.Minute)) // el timer despierta 2 minutos tarde
	w.fire <- clock.Now()

	w = clock.nextWait(t) // ya envió el primero y espera el próximo turno
	if posts.Load() != 1 {
		t.Fatalf("first summary: %d posts", posts.Load())
	}
	next := monday.AddDate(0, 0, 7)
	if got := clock.Now().Add(w.d); !got.Equal(next) {
		t.Fatalf("next slot %s, want %s", got, next)
	}
	clock.set(next) // esta vez a la hora justa
	w.fire <- clock.Now()
	clock.nextWait(t)
	if posts.Load() != 2 {
		t.Fatalf("both weeks are sent: %d posts", posts.Load())
	}
}

// Suspendido varias semanas: al despertar se envía un resumen (el del turno que esperaba) y
// sigue con el próximo turno futuro; las semanas perdidas no se recuperan una tras otra.
func TestWeeklyAfterASuspensionSendsOnceAndGoesOn(t *testing.T) {
	loc := time.UTC
	clock, posts, _ := weeklyHarness(t, time.Date(2026, 10, 7, 10, 0, 0, 0, loc))
	w := clock.nextWait(t)
	woke := time.Date(2026, 11, 4, 15, 0, 0, 0, loc) // cuatro semanas después, miércoles
	clock.set(woke)
	w.fire <- woke
	w = clock.nextWait(t)
	if posts.Load() != 1 {
		t.Fatalf("one summary after the suspension: %d", posts.Load())
	}
	if got := woke.Add(w.d); !got.Equal(time.Date(2026, 11, 9, 9, 0, 0, 0, loc)) {
		t.Fatalf("next slot %s", got)
	}
}

// La cancelación detiene el planificador mientras espera.
func TestWeeklyStopsOnCancel(t *testing.T) {
	clock, posts, cancel := weeklyHarness(t, time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC))
	w := clock.nextWait(t)
	cancel()
	time.Sleep(50 * time.Millisecond)
	w.fire <- clock.Now() // nadie lo escucha ya
	select {
	case <-clock.waits:
		t.Fatal("the scheduler kept running after the cancel")
	case <-time.After(200 * time.Millisecond):
	}
	if posts.Load() != 0 {
		t.Fatalf("nothing sent: %d", posts.Load())
	}
}
