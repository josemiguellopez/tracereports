package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/demo"
)

// restoreEnv puts back the environment the demo isolates (it clears TRACEREPORTS_* and others).
func restoreEnv(t *testing.T) {
	saved := os.Environ()
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range saved {
			k, v, _ := strings.Cut(kv, "=")
			os.Setenv(k, v)
		}
	})
}

func TestDemoEnvIsolates(t *testing.T) {
	restoreEnv(t)
	os.Setenv("TRACEREPORTS_TOKEN", "secret")
	os.Setenv("TRACEREPORTS_GITHUB_TOKEN", "gh")
	os.Setenv("TEAMS_WEBHOOK_URL", "https://teams")
	os.Setenv("ANTHROPIC_API_KEY", "sk")
	demoEnv(t.TempDir(), 8123, false)
	for _, k := range []string{"TRACEREPORTS_TOKEN", "TRACEREPORTS_GITHUB_TOKEN", "TEAMS_WEBHOOK_URL", "ANTHROPIC_API_KEY"} {
		if os.Getenv(k) != "" {
			t.Errorf("%s must be cleared", k)
		}
	}
	if os.Getenv("PORT") != "8123" || os.Getenv("TRACEREPORTS_BIND") != "127.0.0.1" {
		t.Fatalf("demo settings: PORT=%s BIND=%s", os.Getenv("PORT"), os.Getenv("TRACEREPORTS_BIND"))
	}
	os.Setenv("ANTHROPIC_API_KEY", "sk")
	demoEnv(t.TempDir(), 8123, true)
	if os.Getenv("ANTHROPIC_API_KEY") != "sk" {
		t.Fatal("-ai keeps the AI provider")
	}
}

func TestDemoEndToEnd(t *testing.T) {
	restoreEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out strings.Builder
	var dir string
	errCh := make(chan error, 1)
	readyCh := make(chan [2]string, 1)
	go func() {
		errCh <- demoMain(ctx, demoOptions{out: &out, ready: func(base, shop string, sum *demo.Summary) {
			dir = os.Getenv("DATA_DIR")
			readyCh <- [2]string{base, shop}
		}})
	}()
	var urls [2]string
	select {
	case urls = <-readyCh:
	case err := <-errCh:
		t.Fatalf("demo stopped: %v\n%s", err, out.String())
	case <-time.After(120 * time.Second):
		t.Fatal("the demo did not start")
	}
	res, err := http.Get(urls[0] + "/api/v1/runs?limit=50")
	if err != nil {
		t.Fatal(err)
	}
	var runs []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&runs)
	res.Body.Close()
	if len(runs) < 20 {
		t.Fatalf("runs: %d", len(runs))
	}
	res, err = http.Get(urls[1])
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(page), "TraceReports Playground") {
		t.Fatalf("playground page: %s", page)
	}
	res, err = http.Get(urls[1] + "/__scenarios")
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Scenarios []struct{ ID, TitleES string } `json:"scenarios"`
	}
	_ = json.NewDecoder(res.Body).Decode(&cat)
	res.Body.Close()
	visible := 0
	for _, s := range demo.Scenarios {
		if !s.Hidden {
			visible++
		}
	}
	if len(cat.Scenarios) != visible {
		t.Fatalf("playground scenarios: %d", len(cat.Scenarios))
	}
	for _, want := range []string{"sample data ready", "Lab", `password "demo"`, "Ctrl+C"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("guide lacks %q:\n%s", want, out.String())
		}
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the demo did not stop")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the sample data must be deleted: %s (%v)", dir, err)
	}
}

// selftest plays every scenario against a temporary server and passes.
func TestSelftest(t *testing.T) {
	restoreEnv(t)
	var out strings.Builder
	if err := selftest(context.Background(), selftestOptions{out: &out}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), fmt.Sprintf("All %d scenarios pass", len(demo.Scenarios))) {
		t.Fatalf("output:\n%s", out.String())
	}
	if err := selftest(context.Background(), selftestOptions{only: "nope", out: &out}); err == nil || !strings.Contains(err.Error(), "unknown scenario") {
		t.Fatalf("unknown scenario: %v", err)
	}
}

// A protected server: selftest -url sends TRACEREPORTS_TOKEN, and without it the scenarios fail.
func TestSelftestProtectedServer(t *testing.T) {
	restoreEnv(t)
	port, err := freePort(0, 8300)
	if err != nil {
		t.Fatal(err)
	}
	demoEnv(t.TempDir(), port, false)
	os.Setenv("TRACEREPORTS_TOKEN", "s3cret-token")
	ctx, stop := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runServer(ctx) }()
	t.Cleanup(func() { stop(); <-errCh })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitProtected(base, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	only := "pytest-nok-backend,junit-import"
	if err := selftest(context.Background(), selftestOptions{url: base, only: only, token: "s3cret-token", out: &out}); err != nil {
		t.Fatalf("with the token: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := selftest(context.Background(), selftestOptions{url: base, only: only, out: &out}); err == nil {
		t.Fatalf("without the token it should fail:\n%s", out.String())
	}
}

// waitProtected waits until a server with a token answers (401 without it).
func waitProtected(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if res, err := http.Get(base + "/api/v1/runs"); err == nil {
			res.Body.Close()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("the server did not start in time")
}

// A demo stopped by force is cleaned up on the next start; one kept with -keep is not.
func TestCleanOldDemosKeepsKept(t *testing.T) {
	restoreEnv(t)
	tmp := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		os.Setenv(k, tmp)
	}
	old := time.Now().Add(-13 * time.Hour)
	dirs := map[string]bool{"tracereports-demo-killed": false, "tracereports-demo-kept": true, "tracereports-demo-recent": false}
	for name, kept := range dirs {
		d := filepath.Join(tmp, name)
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if kept {
			os.WriteFile(filepath.Join(d, demoKept), nil, 0o644)
		}
		if name != "tracereports-demo-recent" {
			os.Chtimes(d, old, old)
		}
	}
	cleanOldDemos()
	for name, want := range map[string]bool{"tracereports-demo-killed": false, "tracereports-demo-kept": true, "tracereports-demo-recent": true} {
		_, err := os.Stat(filepath.Join(tmp, name))
		if got := err == nil; got != want {
			t.Errorf("%s exists = %v, want %v", name, got, want)
		}
	}
}
