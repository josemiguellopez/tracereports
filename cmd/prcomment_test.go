package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
)

// prEnv clears the CI variables that would make the test detect a real pull request.
func prEnv(t *testing.T, vars map[string]string) {
	for _, k := range []string{"GITHUB_ACTIONS", "GITHUB_REF", "GITHUB_EVENT_PATH", "GITHUB_TOKEN", "GITHUB_REPOSITORY", "GITHUB_API_URL",
		"GITHUB_SHA", "GITHUB_STEP_SUMMARY", "GITLAB_CI", "CI_MERGE_REQUEST_IID", "CI_COMMIT_SHA", "TRACEREPORTS_COMMIT", "PUBLIC_URL",
		"TRACEREPORTS_GITHUB_TOKEN", "GITLAB_TOKEN", "GEMINI_API_KEY"} {
		t.Setenv(k, "")
	}
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

// fakeGitHub keeps the comments of one pull request.
func fakeGitHub(t *testing.T) (*httptest.Server, map[int64]string) {
	var mu sync.Mutex
	comments := map[int64]string{}
	var next int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var in struct{ Body string }
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &in)
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/shop/issues/9/comments":
			var out []map[string]any
			for id, body := range comments {
				out = append(out, map[string]any{"id": id, "body": body})
			}
			json.NewEncoder(w).Encode(out)
		case r.Method == "POST" && r.URL.Path == "/repos/acme/shop/issues/9/comments":
			next++
			comments[next] = in.Body
			json.NewEncoder(w).Encode(map[string]any{"id": next, "html_url": "https://github/c/1"})
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/repos/acme/shop/issues/comments/"):
			comments[1] = in.Body
			json.NewEncoder(w).Encode(map[string]any{"id": 1, "html_url": "https://github/c/1"})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 400)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, comments
}

// serverWithRun starts a TraceReports server with a failed run of commit sha.
func serverWithRun(t *testing.T, sha string) (*httptest.Server, int64) {
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: dir, Web: fstest.MapFS{"index.html": {}}, Auth: api.Auth{Token: "tok"}}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	old, _ := store.CreateRunWithMeta("E2E main", "qa", db.RunMeta{Project: "shop", Commit: "0000000000"})
	id, _ := store.CreateTest(old, "test_pay", "", "")
	store.FinishTest(id, "PASS", "", "")
	store.FinishRun(old)
	run, _ := store.CreateRunWithMeta("E2E main", "qa", db.RunMeta{Project: "shop", Commit: sha})
	id, _ = store.CreateTest(run, "test_pay", "", "")
	store.FinishTest(id, "FAIL", "TimeoutError", "")
	id, _ = store.CreateTest(run, "test_cart", "", "")
	store.FinishTest(id, "PASS", "", "")
	store.CloseRun(run, false)
	return ts, run
}

func TestPRCommentOnGitHubUpdatesTheSameComment(t *testing.T) {
	sha := "abcdef1234567890"
	ts, runID := serverWithRun(t, sha)
	gh, comments := fakeGitHub(t)
	summary := filepath.Join(t.TempDir(), "summary.md")
	prEnv(t, map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REF": "refs/pull/9/merge", "GITHUB_TOKEN": "ghs_x",
		"GITHUB_REPOSITORY": "acme/shop", "GITHUB_API_URL": gh.URL, "GITHUB_SHA": sha, "GITHUB_STEP_SUMMARY": summary})
	args := []string{"-url", ts.URL, "-token", "tok", "-wait", "0", "-lang", "es"}
	if err := runPRComment(args); err != nil {
		t.Fatal(err)
	}
	if err := runPRComment(args); err != nil { // segundo push
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("one comment, updated: %d", len(comments))
	}
	body := comments[1]
	for _, want := range []string{"<!-- tracereports:shop/E2E main -->", "1 de 2 fallaron", "Fallos nuevos frente a #1", "`test_pay`",
		ts.URL + "/#run=" + strconv.FormatInt(runID, 10) + "&view=dashboard"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment misses %q:\n%s", want, body)
		}
	}
	if b, _ := os.ReadFile(summary); !strings.Contains(string(b), "TraceReports · E2E main") {
		t.Fatal("job summary not written")
	}
}

func TestPRCommentFindsNoRunForTheCommit(t *testing.T) {
	ts, _ := serverWithRun(t, "abcdef1234567890")
	prEnv(t, map[string]string{"GITHUB_SHA": "ffffffffffffffff"})
	err := runPRComment([]string{"-url", ts.URL, "-token", "tok", "-wait", "0"})
	if err == nil || !strings.Contains(err.Error(), "no run for commit ffffffffffffffff") {
		t.Fatalf("error: %v", err)
	}
}

func TestPRCommentFromJUnitWithoutAServer(t *testing.T) {
	prEnv(t, nil)
	out := captureStdout(t, func() {
		if err := runPRComment([]string{"-from", filepath.Join("..", "internal", "junit", "testdata", "pytest.xml"), "-name", "Sin servidor", "-lang", "en", "-dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"TraceReports · Sin servidor — 2 of 4 failed", "**Failures**", "`test_login_admin[chromium]`"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Open the report") {
		t.Error("without a server there is no report link unless -link")
	}
}

func TestSameCommit(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"abcdef1", "abcdef1234", true}, {"abcdef1234", "abcdef1", true}, {"abc", "abcdef", false}, {"", "x", false}, {"abcdef12", "abcdef13", false}} {
		if sameCommit(c.a, c.b) != c.want {
			t.Errorf("sameCommit(%q, %q) != %v", c.a, c.b, c.want)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}
