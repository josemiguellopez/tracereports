package tracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func sample() *Issue {
	return &Issue{
		Title:    "Checkout: el pago devuelve 500\n(segunda línea)",
		Headline: "El backend de pagos falla",
		Sections: [][2]string{{"Qué pasó", "POST /api/pay respondió 500"}, {"Vacía", "  "}},
		Lists:    []List{{"Evidencia", []string{"captura", "<script>alert(1)</script>"}}, {"Sin items", nil}},
		Code:     [][2]string{{"Error", "AssertionError: 500 ``` {noformat}"}},
		Link:     "https://reports.acme/#run=1&view=tests&test=2",
		LinkText: "Ver reporte",
		Footer:   "Creado por TraceReports",
		Image:    png,
	}
}

type captured struct {
	method, path, query, auth, ctype string
	headers                          http.Header
	body                             []byte
}

// fake records every request and answers with the JSON of reply(path).
func fake(t *testing.T, status int, reply func(path string) string) (*httptest.Server, *[]captured) {
	var mu sync.Mutex
	var reqs []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs = append(reqs, captured{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header, b})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply(r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func TestGitHub(t *testing.T) {
	srv, reqs := fake(t, 201, func(string) string { return `{"number":12,"html_url":"https://github.com/acme/shop/issues/12"}` })
	g := &GitHub{API: srv.URL, Repo: "acme/shop", Token: "ghp_SECRET", Labels: []string{"bug", "e2e"}, HTTP: srv.Client()}
	tk, err := g.Create(context.Background(), sample())
	if err != nil {
		t.Fatal(err)
	}
	if tk.Key != "#12" || tk.URL != "https://github.com/acme/shop/issues/12" || tk.Provider != "github" {
		t.Fatalf("ticket: %+v", tk)
	}
	r := (*reqs)[0]
	if r.method != "POST" || r.path != "/repos/acme/shop/issues" || r.auth != "Bearer ghp_SECRET" {
		t.Fatalf("request: %+v", r)
	}
	var body struct {
		Title, Body string
		Labels      []string
	}
	json.Unmarshal(r.body, &body)
	if !strings.HasPrefix(body.Title, "Checkout: el pago devuelve 500") || strings.Join(body.Labels, ",") != "bug,e2e" {
		t.Fatalf("body: %+v", body)
	}
	for _, want := range []string{"**El backend de pagos falla**", "### Qué pasó", "- captura", "```\nAssertionError: 500 ''' {noformat}\n```", "[Ver reporte](https://reports.acme/#run=1&view=tests&test=2)"} {
		if !strings.Contains(body.Body, want) {
			t.Errorf("markdown misses %q:\n%s", want, body.Body)
		}
	}
	if strings.Contains(body.Body, "### Vacía") || strings.Contains(body.Body, "### Sin items") {
		t.Error("empty sections must be skipped")
	}
}

func TestJiraCloudWithScreenshot(t *testing.T) {
	srv, reqs := fake(t, 201, func(p string) string {
		if strings.HasSuffix(p, "/attachments") {
			return `[{"id":"1"}]`
		}
		return `{"id":"100","key":"SHOP-34"}`
	})
	j := &Jira{URL: srv.URL + "/", Project: "SHOP", Email: "qa@acme.com", Token: "SECRET", IssueType: "Bug", Labels: []string{"trace reports"}, HTTP: srv.Client()}
	tk, err := j.Create(context.Background(), sample())
	if err != nil {
		t.Fatal(err)
	}
	if tk.Key != "SHOP-34" || tk.URL != srv.URL+"/browse/SHOP-34" {
		t.Fatalf("ticket: %+v", tk)
	}
	if len(*reqs) != 2 {
		t.Fatalf("issue + attachment: %d requests", len(*reqs))
	}
	create, attach := (*reqs)[0], (*reqs)[1]
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("qa@acme.com:SECRET"))
	if create.path != "/rest/api/2/issue" || create.auth != wantAuth {
		t.Fatalf("create: %+v", create)
	}
	var body struct {
		Fields struct {
			Project     struct{ Key string }
			Summary     string
			Description string
			Issuetype   struct{ Name string }
			Labels      []string
		}
	}
	json.Unmarshal(create.body, &body)
	f := body.Fields
	if f.Project.Key != "SHOP" || f.Issuetype.Name != "Bug" || f.Summary != "Checkout: el pago devuelve 500 (segunda línea)" || f.Labels[0] != "trace-reports" {
		t.Fatalf("fields: %+v", f)
	}
	for _, want := range []string{"*El backend de pagos falla*", "h3. Qué pasó", "* captura", "{noformat}\nAssertionError: 500 ``` { noformat}\n{noformat}", "[Ver reporte|https://reports.acme/"} {
		if !strings.Contains(f.Description, want) {
			t.Errorf("wiki misses %q:\n%s", want, f.Description)
		}
	}
	if attach.path != "/rest/api/2/issue/SHOP-34/attachments" || attach.headers.Get("X-Atlassian-Token") != "no-check" ||
		!strings.HasPrefix(attach.ctype, "multipart/form-data") || !strings.Contains(string(attach.body), string(png)) {
		t.Fatalf("attachment: %+v", attach)
	}
}

func TestJiraServerUsesBearer(t *testing.T) {
	srv, reqs := fake(t, 201, func(string) string { return `{"key":"OPS-1"}` })
	j := &Jira{URL: srv.URL, Project: "OPS", Token: "PAT", IssueType: "Task", HTTP: srv.Client()}
	is := sample()
	is.Image = nil
	if _, err := j.Create(context.Background(), is); err != nil {
		t.Fatal(err)
	}
	if len(*reqs) != 1 || (*reqs)[0].auth != "Bearer PAT" {
		t.Fatalf("server/DC auth, no attachment: %+v", *reqs)
	}
}

func TestAzureBugWithScreenshot(t *testing.T) {
	srv, reqs := fake(t, 200, func(p string) string {
		if strings.HasSuffix(p, "/attachments") {
			return `{"id":"a1","url":"https://dev.azure.com/acme/_apis/wit/attachments/a1"}`
		}
		return `{"id":56,"_links":{"html":{"href":"https://dev.azure.com/acme/Shop/_workitems/edit/56"}}}`
	})
	a := &Azure{URL: srv.URL + "/acme", Project: "Shop Web", Token: "PAT", Type: "Bug", Tags: []string{"tracereports", "e2e"}, HTTP: srv.Client()}
	tk, err := a.Create(context.Background(), sample())
	if err != nil {
		t.Fatal(err)
	}
	if tk.Key != "Bug 56" || tk.URL != "https://dev.azure.com/acme/Shop/_workitems/edit/56" {
		t.Fatalf("ticket: %+v", tk)
	}
	upload, create := (*reqs)[0], (*reqs)[1]
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(":PAT"))
	if upload.path != "/acme/Shop Web/_apis/wit/attachments" || upload.auth != wantAuth || string(upload.body) != string(png) {
		t.Fatalf("upload: %+v", upload)
	}
	if create.path != "/acme/Shop Web/_apis/wit/workitems/$Bug" || create.ctype != "application/json-patch+json" || !strings.Contains(create.query, "api-version=7.1") {
		t.Fatalf("create: %+v", create)
	}
	var ops []struct {
		Op, Path string
		Value    any
	}
	json.Unmarshal(create.body, &ops)
	got := map[string]any{}
	for _, o := range ops {
		got[o.Path] = o.Value
	}
	steps, _ := got["/fields/Microsoft.VSTS.TCM.ReproSteps"].(string)
	if got["/fields/System.Title"] != "Checkout: el pago devuelve 500 (segunda línea)" || got["/fields/System.Tags"] != "tracereports; e2e" {
		t.Fatalf("ops: %+v", got)
	}
	if !strings.Contains(steps, "<h3>Qué pasó</h3>") || !strings.Contains(steps, "&lt;script&gt;") || strings.Contains(steps, "<script>") {
		t.Fatalf("html must be escaped: %s", steps)
	}
	if rel, ok := got["/relations/-"].(map[string]any); !ok || rel["url"] != "https://dev.azure.com/acme/_apis/wit/attachments/a1" {
		t.Fatalf("attachment relation: %+v", got["/relations/-"])
	}
}

func TestAzureTaskUsesDescription(t *testing.T) {
	srv, reqs := fake(t, 200, func(string) string { return `{"id":1,"_links":{"html":{"href":"x"}}}` })
	a := &Azure{URL: srv.URL, Project: "P", Token: "T", Type: "Task", HTTP: srv.Client()}
	is := sample()
	is.Image = nil
	if _, err := a.Create(context.Background(), is); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string((*reqs)[0].body), "/fields/System.Description") {
		t.Fatalf("a Task has no repro steps field: %s", (*reqs)[0].body)
	}
}

func TestErrorsSayWhatTheTrackerAnsweredButNotTheToken(t *testing.T) {
	srv, _ := fake(t, 401, func(string) string { return `{"message":"Bad credentials"}` })
	g := &GitHub{API: srv.URL, Repo: "a/b", Token: "ghp_SECRET", HTTP: srv.Client()}
	_, err := g.Create(context.Background(), sample())
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "Bad credentials") || strings.Contains(err.Error(), "ghp_SECRET") {
		t.Fatalf("error: %v", err)
	}
}

func TestJiraAttachmentFailureKeepsTheTicket(t *testing.T) {
	attached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/attachments") { // el adjunto falla
			attached = true
			http.Error(w, "too big", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(201)
		io.WriteString(w, `{"key":"SHOP-9"}`)
	}))
	defer srv.Close()
	j := &Jira{URL: srv.URL, Project: "SHOP", Token: "T", IssueType: "Bug", HTTP: srv.Client()}
	tk, err := j.Create(context.Background(), sample())
	if err != nil || tk.Key != "SHOP-9" || !attached {
		t.Fatalf("ticket: %v %+v attached=%v", err, tk, attached)
	}
}

func TestFromEnv(t *testing.T) {
	for _, k := range []string{"GITHUB_REPO", "GITHUB_TOKEN", "GITHUB_API", "GITHUB_LABELS", "JIRA_URL", "JIRA_PROJECT", "JIRA_TOKEN", "JIRA_EMAIL",
		"JIRA_ISSUE_TYPE", "JIRA_LABELS", "AZURE_URL", "AZURE_PROJECT", "AZURE_TOKEN", "AZURE_TYPE", "AZURE_TAGS"} {
		t.Setenv("TRACEREPORTS_"+k, "")
	}
	if len(FromEnv()) != 0 {
		t.Fatal("nothing configured")
	}
	t.Setenv("TRACEREPORTS_GITHUB_REPO", "acme/shop")
	if len(FromEnv()) != 0 {
		t.Fatal("a repo without a token is not configured")
	}
	t.Setenv("TRACEREPORTS_GITHUB_TOKEN", "x")
	t.Setenv("TRACEREPORTS_JIRA_URL", "https://acme.atlassian.net")
	t.Setenv("TRACEREPORTS_JIRA_PROJECT", "SHOP")
	t.Setenv("TRACEREPORTS_JIRA_TOKEN", "y")
	t.Setenv("TRACEREPORTS_AZURE_URL", "https://dev.azure.com/acme")
	t.Setenv("TRACEREPORTS_AZURE_PROJECT", "Shop")
	t.Setenv("TRACEREPORTS_AZURE_TOKEN", "z")
	t.Setenv("TRACEREPORTS_JIRA_LABELS", " a , ,b ")
	ps := FromEnv()
	if len(ps) != 3 || ps[0].ID() != "github" || ps[1].ID() != "jira" || ps[2].ID() != "azure" {
		t.Fatalf("providers: %v", ps)
	}
	g, j, a := ps[0].(*GitHub), ps[1].(*Jira), ps[2].(*Azure)
	if g.API != "https://api.github.com" || strings.Join(g.Labels, ",") != "bug" || j.IssueType != "Bug" ||
		strings.Join(j.Labels, ",") != "a,b" || a.Type != "Bug" || strings.Join(a.Tags, ",") != "tracereports" {
		t.Fatalf("defaults: %+v %+v %+v", g, j, a)
	}
}
