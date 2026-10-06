package prcomment

import (
	"context"
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
)

func run(t *testing.T, raw string) *Run {
	t.Helper()
	var r Run
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

const failing = `{"id":8,"name":"E2E main","environment":"staging","project":"shop","status":"FAIL","started_at":1000,"ended_at":126000,
	"total":40,"passed":36,"failed":3,"skipped":1,
	"tests":[{"id":1,"name":"test_login","status":"FAIL"},{"id":2,"name":"test_pay","status":"FAIL"},{"id":3,"name":"test_cart","status":"FAIL"},
		{"id":4,"name":"test_search","status":"PASS","flaky":true},{"id":5,"name":"test_coupon","status":"PASS","attempts":2}],
	"summary":{"state":"DONE","headline":"POST /auth/login respondió 500 @team",
		"incidents":[{"title":"Login 500","test_names":["test_login","test_pay"],"cause":"auth sin conexiones","action":"pool de BD"},
			{"title":"b","test_names":["x"]},{"title":"c","test_names":["y"]},{"title":"d","test_names":["z"]}]}}`

func TestMarkdownWithComparison(t *testing.T) {
	cmp := &Comparison{NewFailures: []Item{{Name: "test_pay"}}, Fixed: []Item{{Name: "test_old`x"}}}
	cmp.BaseRun = &struct {
		ID int64 `json:"id"`
	}{ID: 7}
	md := Markdown(run(t, failing), cmp, "es", "https://reports/#run=8")
	for _, want := range []string{
		"<!-- tracereports:shop/E2E main -->",
		"### ❌ TraceReports · E2E main · staging — 3 de 40 fallaron",
		"| 36 | 3 | 1 | 2m5s |",
		"**Diagnóstico:** POST /auth/login respondió 500 @​team", // sin mencionar a nadie
		"- **Login 500** (2 tests) — Causa probable: auth sin conexiones · Revisar: pool de BD",
		"- **b** (1 test)", // singular
		"- _y 1 más_",      // 4 incidentes: se muestran 3
		"**Fallos nuevos frente a #7**\n- `test_pay`",
		"**Arreglados**\n- `test_old'x`",
		"- `test_search`", "- `test_coupon (pasó tras reintento)`",
		"[Ver el reporte →](https://reports/#run=8)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "**Fallos**\n") {
		t.Error("with a base run it lists what changed, not every failure")
	}
}

func TestMarkdownWithoutBaseListsFailures(t *testing.T) {
	md := Markdown(run(t, failing), &Comparison{}, "en", "")
	if !strings.Contains(md, "**Failures**\n- `test_login`\n- `test_pay`\n- `test_cart`") || strings.Contains(md, "Open the report") {
		t.Fatalf("no base run:\n%s", md)
	}
	if !strings.Contains(md, "3 of 40 failed") {
		t.Fatal("english")
	}
}

func TestMarkdownGreenAndIncomplete(t *testing.T) {
	green := run(t, `{"name":"Smoke","project":"p","total":5,"passed":5,"summary":{"headline":"todo bien"}}`)
	md := Markdown(green, nil, "xx", "") // idioma desconocido: inglés
	if !strings.Contains(md, "### ✅ TraceReports · Smoke — all green") || strings.Contains(md, "Diagnosis") || !strings.Contains(md, "| 5 | 0 | 0 | — |") {
		t.Fatalf("green:\n%s", md)
	}
	inc := run(t, `{"name":"Smoke","project":"p","total":5,"passed":4,"failed":1,"incomplete":true}`)
	if md := Markdown(inc, nil, "en", ""); !strings.Contains(md, "⚠️") || !strings.Contains(md, "incomplete run") {
		t.Fatalf("incomplete:\n%s", md)
	}
	var many []Item
	for i := 0; i < 15; i++ {
		many = append(many, Item{Name: "t" + strconv.Itoa(i)})
	}
	cmp := &Comparison{NewFailures: many}
	cmp.BaseRun = &struct {
		ID int64 `json:"id"`
	}{ID: 1}
	if md := Markdown(inc, cmp, "en", ""); !strings.Contains(md, "- `t9`\n- _and 5 more_") {
		t.Fatalf("long lists are cut:\n%s", md)
	}
}

func TestMarkdownQuarantined(t *testing.T) {
	only := run(t, `{"name":"E2E","project":"p","total":10,"passed":9,"failed":1,"quarantined":1}`)
	if md := Markdown(only, nil, "es", ""); !strings.Contains(md, "### ⚠️ TraceReports · E2E — todo en verde · 1 en cuarentena") {
		t.Fatalf("only quarantined failures:\n%s", md)
	}
	mixed := run(t, `{"name":"E2E","project":"p","total":10,"passed":7,"failed":3,"quarantined":1}`)
	if md := Markdown(mixed, nil, "en", ""); !strings.Contains(md, "### ❌ TraceReports · E2E — 2 of 10 failed · 1 quarantined") {
		t.Fatalf("real failures plus quarantined:\n%s", md)
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDetect(t *testing.T) {
	event := filepath.Join(t.TempDir(), "event.json")
	os.WriteFile(event, []byte(`{"pull_request":{"number":42}}`), 0o644)
	p, err := Detect(env(map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_PATH": event, "GITHUB_TOKEN": "t", "GITHUB_REPOSITORY": "acme/shop"}), nil)
	if g, ok := p.(*GitHub); err != nil || !ok || g.Number != 42 || g.Repo != "acme/shop" || g.API != "https://api.github.com" {
		t.Fatalf("github event: %v %+v", err, p)
	}
	p, _ = Detect(env(map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REF": "refs/pull/7/merge", "GITHUB_TOKEN": "t"}), nil)
	if g, ok := p.(*GitHub); !ok || g.Number != 7 {
		t.Fatalf("github ref: %+v", p)
	}
	if p, err := Detect(env(map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REF": "refs/heads/main"}), nil); p != nil || err != nil {
		t.Fatalf("a push to main is not a PR: %v %v", p, err)
	}
	if _, err := Detect(env(map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REF": "refs/pull/7/merge"}), nil); err == nil || !strings.Contains(err.Error(), "pull-requests: write") {
		t.Fatalf("missing token explains how: %v", err)
	}
	p, _ = Detect(env(map[string]string{"GITLAB_CI": "true", "CI_MERGE_REQUEST_IID": "5", "CI_PROJECT_ID": "12", "GITLAB_TOKEN": "g",
		"CI_API_V4_URL": "https://git.acme/api/v4", "CI_MERGE_REQUEST_PROJECT_URL": "https://git.acme/shop"}), nil)
	if g, ok := p.(*GitLab); !ok || g.MR != 5 || g.Project != "12" || g.API != "https://git.acme/api/v4" || g.MRURL() != "https://git.acme/shop/-/merge_requests/5" {
		t.Fatalf("gitlab: %+v", p)
	}
	if _, err := Detect(env(map[string]string{"GITLAB_CI": "true", "CI_MERGE_REQUEST_IID": "5"}), nil); err == nil {
		t.Fatal("gitlab without a token")
	}
	if p, err := Detect(env(map[string]string{}), nil); p != nil || err != nil {
		t.Fatal("outside CI there is no PR")
	}
}

// fakeComments is a minimal GitHub/GitLab comments API with state.
type fakeComments struct {
	mu       sync.Mutex
	comments map[int64]string
	next     int64
	methods  []string
	auth     string
}

func (f *fakeComments) server(t *testing.T, gitlab bool) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.methods = append(f.methods, r.Method)
		if gitlab {
			f.auth = r.Header.Get("PRIVATE-TOKEN")
		} else {
			f.auth = r.Header.Get("Authorization")
		}
		var in struct{ Body string }
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &in)
		switch r.Method {
		case http.MethodGet:
			var list []map[string]any
			for id, body := range f.comments {
				list = append(list, map[string]any{"id": id, "body": body})
			}
			json.NewEncoder(w).Encode(list)
		case http.MethodPost:
			f.next++
			f.comments[f.next] = in.Body
			json.NewEncoder(w).Encode(map[string]any{"id": f.next, "html_url": "https://gh/c/" + strconv.FormatInt(f.next, 10)})
		case http.MethodPatch, http.MethodPut:
			id, _ := strconv.ParseInt(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], 10, 64)
			f.comments[id] = in.Body
			json.NewEncoder(w).Encode(map[string]any{"id": id, "html_url": "https://gh/c/" + strconv.FormatInt(id, 10)})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubUpsertUpdatesInsteadOfDuplicating(t *testing.T) {
	f := &fakeComments{comments: map[int64]string{100: "a human comment"}, next: 100}
	srv := f.server(t, false)
	g := &GitHub{API: srv.URL, Repo: "acme/shop", Number: 42, Token: "tok", HTTP: srv.Client()}
	marker := "<!-- tracereports:shop/E2E -->"
	u1, err := g.Upsert(context.Background(), marker, marker+"\nfirst")
	if err != nil || u1 != "https://gh/c/101" {
		t.Fatalf("create: %v %q", err, u1)
	}
	u2, err := g.Upsert(context.Background(), marker, marker+"\nsecond")
	if err != nil || u2 != u1 {
		t.Fatalf("update the same comment: %v %q", err, u2)
	}
	if len(f.comments) != 2 || f.comments[101] != marker+"\nsecond" || f.comments[100] != "a human comment" {
		t.Fatalf("comments: %+v", f.comments)
	}
	if strings.Join(f.methods, ",") != "GET,POST,GET,PATCH" || f.auth != "Bearer tok" {
		t.Fatalf("calls: %v auth=%q", f.methods, f.auth)
	}
	// otra suite (otro marcador) tiene su propio comentario
	g.Upsert(context.Background(), "<!-- tracereports:shop/Smoke -->", "smoke")
	if len(f.comments) != 3 {
		t.Fatalf("one comment per suite: %+v", f.comments)
	}
}

func TestGitLabUpsert(t *testing.T) {
	f := &fakeComments{comments: map[int64]string{}}
	srv := f.server(t, true)
	g := &GitLab{API: srv.URL, Project: "acme/shop", MR: 5, Token: "glpat", WebURL: "https://git/acme/shop", HTTP: srv.Client()}
	marker := "<!-- tracereports:x -->"
	u, err := g.Upsert(context.Background(), marker, marker+" 1")
	if err != nil || u != "https://git/acme/shop/-/merge_requests/5#note_1" {
		t.Fatalf("create: %v %q", err, u)
	}
	g.Upsert(context.Background(), marker, marker+" 2")
	if len(f.comments) != 1 || f.comments[1] != marker+" 2" || f.auth != "glpat" || f.methods[len(f.methods)-1] != http.MethodPut {
		t.Fatalf("update: %+v %v", f.comments, f.methods)
	}
}

func TestUpsertReportsTheAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	g := &GitHub{API: srv.URL, Repo: "a/b", Number: 1, Token: "secret-token", HTTP: srv.Client()}
	_, err := g.Upsert(context.Background(), "m", "b")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "not accessible") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error: %v", err)
	}
}
