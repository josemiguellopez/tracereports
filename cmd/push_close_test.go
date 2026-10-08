package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/offline"
)

// interruptedRecording: una grabación que se cortó antes del finish (ejecución, test y un paso).
func interruptedRecording(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tracereports-offline.json"), []byte(`{"format":"tracereports-offline","version":1,"id":"cut-1"}`), 0o600)
	events := strings.Join([]string{
		`{"seq":1,"ts":1790000000000,"method":"POST","path":"/api/v1/runs","body":{"name":"cortada"},"local_id":-1}`,
		`{"seq":2,"ts":1790000000100,"method":"POST","path":"/api/v1/runs/-1/tests","body":{"name":"t1"},"local_id":-2}`,
		`{"seq":3,"ts":1790000000200,"method":"POST","path":"/api/v1/tests/-2/logs","body":{"status":"INFO","message":"paso"}}`,
	}, "\n") + "\n"
	os.WriteFile(filepath.Join(dir, "events-cut.jsonl"), []byte(events), 0o600)
	return dir
}

// Si el cierre automático de una grabación cortada falla, push no puede decir que salió bien; al
// repetirlo, termina el cierre sin duplicar nada.
func TestPushFailsWhenTheAutoCloseFailsAndCanBeRepeated(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: dir, Web: fstest.MapFS{"index.html": {}}}
	var failClose atomic.Bool
	failClose.Store(true)
	router := srv.Router()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failClose.Load() && r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/finish") {
			http.Error(w, `{"error":"close rejected"}`, http.StatusInternalServerError)
			return
		}
		router.ServeHTTP(w, r)
	}))
	defer ts.Close()
	rec := interruptedRecording(t)

	if err := runPush([]string{"-url", ts.URL, rec}); err == nil {
		t.Fatal("push must fail when the run could not be closed")
	}
	r, _ := offline.Open(rec)
	res, err := r.Replay(offline.Target{Doer: http.DefaultClient, BaseURL: ts.URL})
	if err != nil || res.Rejected != 1 || len(res.Errors) == 0 {
		t.Fatalf("the failed close is counted as rejected: %+v %v", res, err)
	}
	runs, _ := store.ListRuns(10)
	if len(runs) != 1 || runs[0].Status != "RUNNING" {
		t.Fatalf("run left open: %+v", runs)
	}

	// el servidor ya acepta el cierre: repetir el push lo cierra y no duplica evidencia
	failClose.Store(false)
	if err := runPush([]string{"-url", ts.URL, rec}); err != nil {
		t.Fatalf("second push: %v", err)
	}
	runs, _ = store.ListRuns(10)
	if len(runs) != 1 || runs[0].Status == "RUNNING" || !runs[0].Incomplete || runs[0].Total != 1 {
		t.Fatalf("closed once, as incomplete, without duplicates: %+v", runs)
	}
	got, _ := store.GetRunDetail(runs[0].ID)
	if tt, _ := store.GetTest(got.Tests[0].ID); len(tt.Logs) != 1 {
		t.Fatalf("the step is not duplicated: %d", len(tt.Logs))
	}
}
