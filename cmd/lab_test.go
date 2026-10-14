package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/lab"
	"github.com/josemiguellopez/tracereports/internal/repro"
)

const cliPassword = "cli-s3cret!"

// cliBackend: POST /login gives a token; GET /orders needs it and fails while broken.
func cliBackend(t *testing.T) (*httptest.Server, *atomic.Bool) {
	var broken atomic.Bool
	broken.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), cliPassword) {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"accessToken":"tok-123456"}`)
		case "/orders":
			if r.Header.Get("Authorization") != "Bearer tok-123456" {
				w.WriteHeader(401)
				return
			}
			if broken.Load() {
				w.WriteHeader(500)
				fmt.Fprint(w, `{"error":"pool exhausted"}`)
				return
			}
			fmt.Fprint(w, `{"orders":[1]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &broken
}

func testCapsule(base string, withCheck bool) *lab.Capsule {
	plan := &lab.Plan{ConnID: 2,
		Target: lab.Request{Method: "GET", URL: base + "/orders", Headers: map[string]string{"authorization": "{{authorization}}"}},
		Login: &lab.Login{ConnID: 1, TokenPath: []string{"accessToken"}, Request: lab.Request{Method: "POST", URL: base + "/login",
			Headers: map[string]string{"content-type": "application/json"}, Body: `{"password":"{{password}}"}`}},
		Vars: []lab.Var{{Name: "authorization", Kind: lab.KindSession}, {Name: "password", Kind: lab.KindSecret}},
		Original: lab.Original{Status: 500}}
	call := lab.CapsuleCall{Plan: plan, Test: lab.CapsuleTest{ID: 7, Name: "ver pedidos", Key: "tests/orders.spec.js > ver pedidos",
		Commands: repro.Commands("playwright", "tests/orders.spec.js > ver pedidos", "")}}
	if withCheck {
		call.Checks = []lab.CapsuleCheck{{Name: "orders load", Expect: lab.Expect{Status: 200, Contains: []string{"orders"}}}}
	}
	return &lab.Capsule{Format: lab.CapsuleFormat, Version: lab.CapsuleVersion, Run: lab.CapsuleRun{ID: 3, Name: "nightly", Project: "shop"},
		Calls: []lab.CapsuleCall{call}}
}

func writeCapsuleFile(t *testing.T, c *lab.Capsule) string {
	t.Helper()
	b, _ := json.Marshal(c)
	p := filepath.Join(t.TempDir(), "capsule.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func testCLI(env map[string]string) (*labCLI, *strings.Builder) {
	out := &strings.Builder{}
	c := newLabCLI(out)
	c.errOut = io.Discard
	c.getenv = func(k string) string { return env[k] }
	c.isTerm = func() bool { return false }
	return c, out
}

func TestReproCapsule(t *testing.T) {
	backend, _ := cliBackend(t)
	path := writeCapsuleFile(t, testCapsule(backend.URL, false))

	c, out := testCLI(nil)
	if err := c.repro([]string{"-var", "password=" + cliPassword, path}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`run #3 "nightly"`, "Test: ver pedidos", "npx playwright test", "REPRODUCED", "token: accessToken", "pool exhausted"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, cliPassword) || strings.Contains(got, "tok-123456") {
		t.Fatalf("the output shows a secret:\n%s", got)
	}
}

func TestReproValuesFromEnvAndPrompt(t *testing.T) {
	backend, broken := cliBackend(t)
	broken.Store(false)
	path := writeCapsuleFile(t, testCapsule(backend.URL, false))

	c, out := testCLI(map[string]string{"TRACEREPORTS_LAB_PASSWORD": cliPassword})
	if err := c.repro([]string{path}); err != nil || !strings.Contains(out.String(), "NO LONGER FAILS") {
		t.Fatalf("env value: %v\n%s", err, out)
	}

	c, out = testCLI(nil)
	c.isTerm = func() bool { return true }
	c.readPwd = func() ([]byte, error) { return []byte(cliPassword), nil }
	if err := c.repro([]string{path}); err != nil || !strings.Contains(out.String(), "NO LONGER FAILS") {
		t.Fatalf("prompt value: %v\n%s", err, out)
	}

	c, _ = testCLI(nil)
	err := c.repro([]string{path})
	if err == nil || !strings.Contains(err.Error(), "TRACEREPORTS_LAB_PASSWORD") || !strings.Contains(err.Error(), "-var password=") {
		t.Fatalf("missing value: %v", err)
	}
}

func TestVerifyCapsule(t *testing.T) {
	backend, broken := cliBackend(t)
	path := writeCapsuleFile(t, testCapsule(backend.URL, true))
	args := []string{"-var", "password=" + cliPassword, path}

	c, out := testCLI(nil)
	err := c.verify(args)
	if !errors.Is(err, errChecksFailed) || !strings.Contains(out.String(), "✗ orders load") || !strings.Contains(out.String(), "status 500, expected 200") {
		t.Fatalf("while broken: %v\n%s", err, out)
	}
	broken.Store(false)
	c, out = testCLI(nil)
	if err := c.verify(args); err != nil || !strings.Contains(out.String(), "✓ orders load  (500 → 200)") || !strings.Contains(out.String(), "Fixed and verified") {
		t.Fatalf("after the fix: %v\n%s", err, out)
	}

	c, _ = testCLI(nil)
	if err := c.verify([]string{writeCapsuleFile(t, testCapsule(backend.URL, false))}); err == nil || !strings.Contains(err.Error(), "no checks") {
		t.Fatalf("without checks: %v", err)
	}
}

func TestCapsuleFromURL(t *testing.T) {
	backend, _ := cliBackend(t)
	body, _ := json.Marshal(testCapsule(backend.URL, false))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-tok" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"missing token"}`)
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	c, _ := testCLI(nil)
	if _, err := c.load(server.URL + "/api/v1/network/2/capsule"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("without token: %v", err)
	}
	// el token solo va al servidor de TRACEREPORTS_URL: a una URL ajena no se manda
	c, _ = testCLI(map[string]string{"TRACEREPORTS_TOKEN": "admin-tok"})
	if _, err := c.load(server.URL + "/api/v1/network/2/capsule"); err == nil || !strings.Contains(err.Error(), "set TRACEREPORTS_URL="+server.URL) {
		t.Fatalf("token without TRACEREPORTS_URL: %v", err)
	}
	c, _ = testCLI(map[string]string{"TRACEREPORTS_TOKEN": "admin-tok", "TRACEREPORTS_URL": "http://elsewhere.example"})
	if _, err := c.load(server.URL + "/api/v1/network/2/capsule"); err == nil {
		t.Fatal("the token must not go to another host")
	}
	c, _ = testCLI(map[string]string{"TRACEREPORTS_TOKEN": "admin-tok", "TRACEREPORTS_URL": server.URL + "/"})
	if cap, err := c.load(server.URL + "/api/v1/network/2/capsule"); err != nil || cap.Run.Name != "nightly" {
		t.Fatalf("with token: %v %+v", err, cap)
	}
}

// Before a secret is asked for or used, the CLI says where it goes: a capsule from someone else
// could point its login to another server.
func TestSecretDestinationsAreShown(t *testing.T) {
	backend, _ := cliBackend(t)
	path := writeCapsuleFile(t, testCapsule(backend.URL, true))
	c, _ := testCLI(nil)
	var errOut strings.Builder
	c.errOut = &errOut
	_ = c.repro([]string{"-var", "password=" + cliPassword, path})
	if want := "password → POST " + backend.URL + "/login"; !strings.Contains(errOut.String(), want) {
		t.Fatalf("stderr lacks %q:\n%s", want, errOut.String())
	}
	if strings.Contains(errOut.String(), cliPassword) {
		t.Fatal("the destination list must not show the value")
	}
	errOut.Reset()
	_ = c.verify([]string{"-var", "password=" + cliPassword, path})
	if !strings.Contains(errOut.String(), "Secrets will be sent to:") {
		t.Fatalf("verify too:\n%s", errOut.String())
	}
}

func TestReadCapsuleRejects(t *testing.T) {
	for name, body := range map[string]string{
		"not json":     `nope`,
		"other format": `{"format":"x","version":1}`,
		"newer":        `{"format":"tracereports-capsule","version":99}`,
		"no request":   `{"format":"tracereports-capsule","version":1,"calls":[{"plan":{}}]}`,
		"bad expect":   `{"format":"tracereports-capsule","version":1,"calls":[{"plan":{"target":{"url":"http://x"}},"checks":[{"expect":{"status":7}}]}]}`,
	} {
		if _, err := lab.ReadCapsule([]byte(body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLabEnv(t *testing.T) {
	for in, want := range map[string]string{"password": "TRACEREPORTS_LAB_PASSWORD", "_token": "TRACEREPORTS_LAB_TOKEN", "x-api-key": "TRACEREPORTS_LAB_X_API_KEY", "--": "TRACEREPORTS_LAB_VALUE"} {
		if got := labEnv(in); got != want {
			t.Errorf("labEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

// The command is pasted from the report and the values go after it.
func TestFlagsAfterTheCapsule(t *testing.T) {
	backend, _ := cliBackend(t)
	path := writeCapsuleFile(t, testCapsule(backend.URL, false))
	c, out := testCLI(nil)
	if err := c.repro([]string{path, "-var", "password=" + cliPassword}); err != nil || !strings.Contains(out.String(), "REPRODUCED") {
		t.Fatalf("%v\n%s", err, out)
	}
	c, _ = testCLI(nil)
	if err := c.repro([]string{path, "other.json"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("two capsules: %v", err)
	}
}
