package main

import (
	"encoding/json"
	"io"
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

func mirrorRecording(t *testing.T, complete, removed bool) string {
	t.Helper()
	dir := smallRecording(t)
	raw, err := json.Marshal(map[string]any{"format": "tracereports-offline", "version": 1, "id": "mirror-test", "raw_removed": removed,
		"mirror": map[string]any{"server": "https://original.example", "runs": []map[string]int64{{"local": -1, "server": 42}}, "complete": complete}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, offline.Marker), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestPushCompleteMirrorRefusesBeforeHTTP(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	err := runPush([]string{"--url", srv.URL, mirrorRecording(t, true, false)})
	if err == nil || !strings.Contains(err.Error(), "#42") || !strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "https://original.example") {
		t.Fatalf("error: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("complete mirror contacted server")
	}
}
func TestPushMirrorForceAndPartialCreateSeparateRuns(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(map[bool]string{true: "force", false: "partial"}[complete], func(t *testing.T) {
			t.Setenv("AI_PROVIDER", "")
			t.Setenv("GEMINI_API_KEY", "")
			dir := t.TempDir()
			store, err := db.Open(filepath.Join(dir, "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			analyzer := ai.New(store)
			server := &api.Server{Store: store, AI: analyzer, ScreenshotsDir: dir, Web: fstest.MapFS{"index.html": {}}}
			srv := httptest.NewServer(server.Router())
			defer srv.Close()
			rec := mirrorRecording(t, complete, false)
			args := []string{"--url", srv.URL, rec}
			if complete {
				args = append(args, "--force")
			}
			// El upload se puede repetir: mantiene las claves idempotentes de la grabación.
			stderr, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			original := os.Stderr
			os.Stderr = stderr
			err = runPush(args)
			os.Stderr = original
			stderr.Seek(0, 0)
			warning, _ := io.ReadAll(stderr)
			stderr.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !complete && (!strings.Contains(string(warning), "partial") || !strings.Contains(string(warning), "separate complete run")) {
				t.Fatalf("warning: %s", warning)
			}
			if err = runPush(args); err != nil {
				t.Fatal(err)
			}
			runs, err := store.ListRuns(10)
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs=%v err=%v", runs, err)
			}
		})
	}
}
func TestPushRawRemovedExplainsEvenWithForce(t *testing.T) {
	for _, force := range []bool{false, true} {
		dir := mirrorRecording(t, true, true)
		if err := os.Remove(filepath.Join(dir, "events-1.jsonl")); err != nil {
			t.Fatal(err)
		}
		args := []string{dir}
		if force {
			args = append(args, "--force")
		}
		err := runPush(args)
		if err == nil || !strings.Contains(err.Error(), "only the HTML report remains") {
			t.Fatalf("error: %v", err)
		}
	}
}
func TestReportMirrorMetadataDoesNotChangeReplay(t *testing.T) {
	for _, complete := range []bool{false, true} {
		rec := mirrorRecording(t, complete, false)
		opened, err := offline.Open(rec)
		if err != nil {
			t.Fatal(err)
		}
		if opened.Mirror == nil || opened.Mirror.Complete != complete || opened.Mirror.Runs[0].Server != 42 {
			t.Fatalf("mirror: %+v", opened.Mirror)
		}
		out := filepath.Join(t.TempDir(), "report")
		if err = runReport([]string{"-o", out, rec}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(readReport(t, out), "test_offline_login") {
			t.Fatal("test missing")
		}
	}
}
