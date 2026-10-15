package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/mcp"
)

// mcpFixture: a real TraceReports server (in memory) with a failed test recorded against the
// JWT backend, its incident and a saved check.
func mcpFixture(t *testing.T) (trURL string, backendBroken func(bool), callID int64) {
	t.Helper()
	t.Setenv("GEMINI_API_KEY", "")
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: t.TempDir(), Web: fstest.MapFS{"index.html": {Data: []byte("x")}}}
	tr := httptest.NewServer(srv.Router())
	t.Cleanup(tr.Close)
	backend, broken := cliBackend(t)

	post := func(path, body string) map[string]any {
		res, err := http.Post(tr.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode >= 300 {
			t.Fatalf("POST %s: %d %v", path, res.StatusCode, out)
		}
		return out
	}
	run := int64(post("/api/v1/runs", `{"name":"nightly","project":"shop","framework":"playwright","commit":"abc1234"}`)["run_id"].(float64))
	test := int64(post(fmt.Sprintf("/api/v1/runs/%d/tests", run), `{"name":"ver pedidos","key":"tests/orders.spec.js > ver pedidos"}`)["test_id"].(float64))
	hdr := `{"date":"x","server":"shop","content-type":"application/json"}`
	post(fmt.Sprintf("/api/v1/tests/%d/network", test), fmt.Sprintf(`{"connections":[
		{"method":"POST","url":"%[1]s/login","status":200,"resource_type":"fetch","request_headers":{"content-type":"application/json"},
		 "post_data":"{\"password\":\"%[2]s\"}","response_body":"{\"accessToken\":\"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.old\"}","response_headers":%[3]s},
		{"method":"GET","url":"%[1]s/orders","status":500,"resource_type":"fetch","request_headers":{"authorization":"Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.old"},
		 "response_body":"{\"error\":\"pool exhausted\"}","response_headers":%[3]s}]}`, backend.URL, cliPassword, hdr))
	conns, _ := store.ListNetwork(test)
	callID = conns[1].ID
	req, _ := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/api/v1/tests/%d/finish", tr.URL, test), strings.NewReader(`{"status":"FAIL","error_message":"Error: orders did not load"}`))
	req.Header.Set("Content-Type", "application/json")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("finish test: %v", err)
	}
	if err := store.SaveRunTriage(run, &db.RunTriage{State: "DONE", Headline: "1 test failed", Incidents: []db.Incident{{Key: "backend:GET /orders 500",
		Title: "Backend: GET /orders → 500", Kind: "backend", TestIDs: []int64{test}, TestNames: []string{"ver pedidos"},
		Evidence: []db.Evidence{{TestID: test, Kind: "network", Ref: callID, Text: "GET /orders → 500"}}}}}); err != nil {
		t.Fatal(err)
	}
	post("/api/v1/ui/lab/checks", fmt.Sprintf(`{"conn_id":%d,"name":"orders load","expect":{"status":200,"contains":["orders"]}}`, callID))
	return tr.URL, broken.Store, callID
}

// agent sends MCP requests (one per line) and returns the text of each tool result.
type agentResult struct {
	Text    string
	IsError bool
}

func agent(t *testing.T, env map[string]string, calls ...string) []agentResult {
	t.Helper()
	m := newMCPClient(func(k string) string { return env[k] })
	s := mcp.NewServer("tracereports", "test", mcpInstructions, m.tools()...)
	var in bytes.Buffer
	for i, c := range calls {
		fmt.Fprintf(&in, `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`+"\n", i+1, c)
	}
	var out bytes.Buffer
	if err := s.Serve(context.Background(), &in, &out); err != nil {
		t.Fatal(err)
	}
	var res []agentResult
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var r struct {
			Result struct {
				Content []struct{ Text string }
				IsError bool
			}
			Error *struct{ Message string }
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil || r.Error != nil {
			t.Fatalf("bad response %q", line)
		}
		res = append(res, agentResult{Text: r.Result.Content[0].Text, IsError: r.Result.IsError})
	}
	return res
}

func TestMCPAgentFlow(t *testing.T) {
	trURL, setBroken, callID := mcpFixture(t)
	env := map[string]string{"TRACEREPORTS_URL": trURL, "TRACEREPORTS_LAB_PASSWORD": cliPassword}
	res := agent(t, env,
		`{"name":"list_runs","arguments":{"limit":5}}`,
		`{"name":"get_incidents","arguments":{"run_id":1}}`,
		`{"name":"get_test","arguments":{"test_id":1}}`,
		fmt.Sprintf(`{"name":"reproduce","arguments":{"call_id":%d}}`, callID),
		`{"name":"verify_fix","arguments":{"run_id":1,"incident_key":"backend:GET /orders 500"}}`,
	)
	for i, r := range res {
		if r.IsError {
			t.Fatalf("call %d failed: %s", i+1, r.Text)
		}
		if strings.Contains(r.Text, cliPassword) || strings.Contains(r.Text, "tok-123456") {
			t.Fatalf("call %d shows a secret to the agent: %s", i+1, r.Text)
		}
	}
	checks := []struct {
		i    int
		want []string
	}{
		{0, []string{`"name": "nightly"`, `"project": "shop"`}},
		{1, []string{`"key": "backend:GET /orders 500"`, fmt.Sprintf(`"causal_call_id": %d`, callID), `"name": "orders load"`}},
		{2, []string{`"failed_calls"`, fmt.Sprintf(`"id": %d`, callID), "npx playwright test", "git checkout abc1234"}},
		{3, []string{`"verdict": "reproduced"`, "pool exhausted"}},
		{4, []string{`"fixed": false`, `"passed": false`}},
	}
	for _, c := range checks {
		for _, w := range c.want {
			if !strings.Contains(res[c.i].Text, w) {
				t.Errorf("call %d lacks %q:\n%s", c.i+1, w, res[c.i].Text)
			}
		}
	}

	setBroken(false) // el agente arregla el backend
	res = agent(t, env, `{"name":"verify_fix","arguments":{"run_id":1,"incident_key":"backend:GET /orders 500"}}`)
	if res[0].IsError || !strings.Contains(res[0].Text, `"fixed": true`) {
		t.Fatalf("after the fix: %s", res[0].Text)
	}
}

func TestMCPAgentNeverTypesSecrets(t *testing.T) {
	trURL, _, callID := mcpFixture(t)
	res := agent(t, map[string]string{"TRACEREPORTS_URL": trURL}, fmt.Sprintf(`{"name":"reproduce","arguments":{"call_id":%d,"password":"x"}}`, callID))
	if !res[0].IsError || !strings.Contains(res[0].Text, "TRACEREPORTS_LAB_PASSWORD") || !strings.Contains(res[0].Text, "must not type") {
		t.Fatalf("missing secret: %+v", res[0])
	}
	res = agent(t, map[string]string{"TRACEREPORTS_URL": "http://127.0.0.1:1"}, `{"name":"list_runs","arguments":{}}`)
	if !res[0].IsError || !strings.Contains(res[0].Text, "not reachable") {
		t.Fatalf("server down: %+v", res[0])
	}
	res = agent(t, map[string]string{"TRACEREPORTS_URL": trURL}, `{"name":"get_test","arguments":{}}`)
	if !res[0].IsError || !strings.Contains(res[0].Text, "test_id is required") {
		t.Fatalf("missing argument: %+v", res[0])
	}
}
