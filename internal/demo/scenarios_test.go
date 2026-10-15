package demo

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
)

// Every scenario recorded with a real client, sent to a real server: what TraceReports shows must
// match its expectations. When a change breaks the path from a client to the report, it fails here.
func TestScenarios(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: t.TempDir(), Web: fstest.MapFS{"index.html": {Data: []byte("x")}}}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.AI.Wait(ctx)
	}()
	tr := httptest.NewServer(srv.Router())
	defer tr.Close()
	shop := NewShop()
	shopSrv := httptest.NewServer(shop)
	defer shopSrv.Close()

	for _, sc := range Scenarios {
		t.Run(sc.ID, func(t *testing.T) {
			shop.Reset()
			ctx := context.Background()
			runID, err := Play(ctx, tr.URL, shopSrv.URL, sc, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range Verify(ctx, tr.URL, sc, runID) {
				if !c.OK {
					t.Errorf("%s: %s", c.Name, c.Detail)
				}
			}
		})
	}
}

// A scenario can be played again and again: each play is a new run.
func TestPlayTwice(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: t.TempDir(), Web: fstest.MapFS{"index.html": {Data: []byte("x")}}}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.AI.Wait(ctx)
	}()
	tr := httptest.NewServer(srv.Router())
	defer tr.Close()
	sc, _ := ScenarioByID("go-ok")
	a, err := Play(context.Background(), tr.URL, "http://127.0.0.1:1", sc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Play(context.Background(), tr.URL, "http://127.0.0.1:1", sc, time.Now())
	if err != nil || a == b {
		t.Fatalf("second play: run %d after %d (%v)", b, a, err)
	}
	run, _ := store.GetRun(b)
	if d := time.Since(time.UnixMilli(run.StartedAt)); d < 0 || d > time.Minute {
		t.Fatalf("the run must be dated now, it started %s ago", d)
	}
}
