package tracereports

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientFlow(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/v1/runs":
			w.Write([]byte(`{"run_id":7}`))
		case strings.HasSuffix(r.URL.Path, "/tests"):
			w.Write([]byte(`{"test_id":3}`))
		case strings.HasSuffix(r.URL.Path, "/screenshot"):
			if !strings.Contains(string(body), "PNGDATA") {
				t.Errorf("screenshot body missing file")
			}
			w.Write([]byte(`{"url":"/screenshots/a.png"}`))
		case strings.HasSuffix(r.URL.Path, "/network"):
			var in struct{ Connections []Conn }
			json.Unmarshal(body, &in)
			if len(in.Connections) != 1 || in.Connections[0].BodySize != 4 {
				t.Errorf("network payload: %s", body)
			}
			w.Write([]byte(`{"stored":1,"errors":0}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.Token = "tok"
	if id, err := c.StartRun("suite", "qa"); err != nil || id != 7 {
		t.Fatalf("StartRun: %d %v", id, err)
	}
	tc, err := c.StartTest("Login", "smoke", "")
	if err != nil || tc.ID != 3 {
		t.Fatalf("StartTest: %+v %v", tc, err)
	}
	tc.Info("abrir login")
	if url, err := tc.Screenshot([]byte("PNGDATA"), "login", ""); err != nil || url != srv.URL+"/screenshots/a.png" {
		t.Errorf("Screenshot: %s %v", url, err)
	}
	if err := tc.Network([]Conn{{Method: "GET", URL: "u", Status: 200, ResponseBody: "body"}}); err != nil {
		t.Errorf("Network: %v", err)
	}
	tc.Finish(Pass, "", "")
	c.FinishRun()
	want := "POST /api/v1/runs|POST /api/v1/runs/7/tests|POST /api/v1/tests/3/logs|POST /api/v1/tests/3/screenshot|POST /api/v1/tests/3/network|PATCH /api/v1/tests/3/finish|PATCH /api/v1/runs/7/finish"
	if got := strings.Join(calls, "|"); got != want {
		t.Errorf("calls:\n got %s\nwant %s", got, want)
	}
}

func TestUnreachableServerDisablesClient(t *testing.T) {
	c := New("http://127.0.0.1:1") // nadie escucha
	c.Offline = "off"              // sin grabación local: la grabación tiene sus tests en offline_test.go
	for i := 0; i < maxConsecutiveFailures; i++ {
		c.StartRun("x", "")
	}
	if _, err := c.StartRun("x", ""); err != ErrDisabled {
		t.Fatalf("after %d failures the client must disable itself, got %v", maxConsecutiveFailures, err)
	}
	var nilTest *Test
	if err := nilTest.Info("no panic"); err != ErrDisabled {
		t.Errorf("nil *Test must be safe: %v", err)
	}
}

func TestRetriesKeepTheIdempotencyKeyAndContext(t *testing.T) {
	t.Setenv("TRACEREPORTS_BRANCH", "main")
	t.Setenv("TRACEREPORTS_COMMIT", "abc")
	t.Setenv("TRACEREPORTS_PROJECT", "shop")
	var keys []string
	var run map[string]string
	fails := 2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/runs" {
			json.NewDecoder(r.Body).Decode(&run)
			w.Write([]byte(`{"run_id":7}`))
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if fails > 0 {
			fails--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"test_id":3}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	c.StartRun("suite", "qa")
	if run["project"] != "shop" || run["branch"] != "main" || run["commit"] != "abc" || run["framework"] != "go" {
		t.Fatalf("run context: %v", run)
	}
	tc, err := c.StartTestWithKey(Key("shop/login", "TestAdmin/ok"), "Login", "", "")
	if err != nil || tc.ID != 3 {
		t.Fatalf("a 503 must be retried: %v", err)
	}
	if len(keys) != 3 || keys[0] == "" || keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("retries must reuse the Idempotency-Key: %v", keys)
	}
}

func TestConfigFromEnvironment(t *testing.T) {
	t.Setenv("TRACEREPORTS_URL", "http://new:2/")
	t.Setenv("TRACEREPORTS_TOKEN", "tok")
	if c := New(""); c.BaseURL != "http://new:2" || c.Token != "tok" {
		t.Fatalf("TRACEREPORTS_URL and TRACEREPORTS_TOKEN configure the client: %+v", c)
	}
}
