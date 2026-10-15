package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
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
	if !strings.Contains(string(page), "Demo Shop") {
		t.Fatalf("shop page: %s", page)
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
	if !strings.Contains(out.String(), "All 13 scenarios pass") {
		t.Fatalf("output:\n%s", out.String())
	}
	if err := selftest(context.Background(), selftestOptions{only: "nope", out: &out}); err == nil || !strings.Contains(err.Error(), "unknown scenario") {
		t.Fatalf("unknown scenario: %v", err)
	}
}
