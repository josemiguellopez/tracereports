package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/lab"
	"github.com/josemiguellopez/tracereports/internal/mcp"
	"github.com/josemiguellopez/tracereports/internal/redact"
	"github.com/josemiguellopez/tracereports/internal/repro"
)

// `tracereports mcp`: servidor MCP (stdio) para que un agente como Claude Code lea los fallos de
// TraceReports y compruebe sus arreglos. Lee el servidor (TRACEREPORTS_URL) sin escribir nada; para
// reproducir y verificar reenvía las llamadas desde esta máquina, como `repro`/`verify`, con los
// secretos de TRACEREPORTS_LAB_<NOMBRE>: el agente nunca los ve.

const mcpInstructions = `TraceReports holds the evidence of failed test runs (steps, network, console, AI diagnosis) grouped into incidents.
Typical flow: list_runs → get_incidents(run_id) → get_test(test_id) for the evidence → reproduce(call_id) to resend the failing
backend call with a fresh session → after changing the code, verify_fix(run_id, incident_key) runs the saved checks.
All evidence is masked. Secrets for reproducing are read by this server from TRACEREPORTS_LAB_<NAME> environment variables;
if one is missing, ask the user to set it in the MCP server configuration. Never weaken a test (longer timeouts, skipped
assertions) to make it pass: fix the cause.`

type mcpClient struct {
	base   string
	getenv func(string) string
	http   *http.Client
}

func (m *mcpClient) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, m.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if tok := m.getenv("TRACEREPORTS_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if u, p := m.getenv("TRACEREPORTS_UI_USER"), m.getenv("TRACEREPORTS_UI_PASSWORD"); u != "" && p != "" {
		req.SetBasicAuth(u, p)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("TraceReports at %s is not reachable (TRACEREPORTS_URL): %w", m.base, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s %s", path, res.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

func toJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}

func iso(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func (m *mcpClient) tools() []mcp.Tool {
	return []mcp.Tool{
		{Name: "list_runs", Description: "Lists the most recent test runs (newest first) with their pass/fail counters.",
			InputSchema: schema(map[string]any{"limit": intProp("how many runs (default 10, max 50)"), "project": strProp("only runs of this project")}),
			Call:        m.listRuns},
		{Name: "get_incidents", Description: "Failures of a run grouped into incidents: the shared evidence (e.g. the backend call that failed), the AI's probable cause, the tests, the saved checks and whether the fix was verified.",
			InputSchema: schema(map[string]any{"run_id": intProp("run id (from list_runs)")}, "run_id"),
			Call:        m.getIncidents},
		{Name: "get_test", Description: "Evidence of one test: error and stack trace, last steps, failed network calls (with their call_id for reproduce), browser console errors and the command that runs it locally.",
			InputSchema: schema(map[string]any{"test_id": intProp("test id (from get_incidents)")}, "test_id"),
			Call:        m.getTest},
		{Name: "reproduce", Description: "Resends a captured backend call from this machine with a fresh session (repeats the recorded login, reads CSRF tokens live) and says whether the failure still happens: reproduced, fixed, session, different or network.",
			InputSchema: schema(map[string]any{"call_id": intProp("network call id (failed_calls[].id from get_test, or causal_call_id from get_incidents)"),
				"base_url": strProp("send to another environment, e.g. http://localhost:3000 (default: the run's host)")}, "call_id"),
			Call: m.reproduce},
		{Name: "verify_fix", Description: "Runs the checks saved for an incident (from this machine) and tells whether all of them pass now. Use it after changing the code to confirm the fix.",
			InputSchema: schema(map[string]any{"run_id": intProp("run id"), "incident_key": strProp("incident key (from get_incidents)"),
				"base_url": strProp("send to another environment, e.g. http://localhost:3000")}, "run_id", "incident_key"),
			Call: m.verifyFix},
	}
}

func (m *mcpClient) listRuns(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Limit   int    `json:"limit"`
		Project string `json:"project"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Limit <= 0 {
		a.Limit = 10
	}
	a.Limit = min(a.Limit, 50)
	var runs []db.Run
	if err := m.get(fmt.Sprintf("/api/v1/runs?limit=%d", max(a.Limit, 50)), &runs); err != nil {
		return "", err
	}
	out := []map[string]any{}
	for _, r := range runs {
		if a.Project != "" && r.Project != a.Project {
			continue
		}
		out = append(out, map[string]any{"id": r.ID, "name": r.Name, "status": r.Status, "project": r.Project, "environment": r.Environment,
			"branch": r.Branch, "commit": r.Commit, "started_at": iso(r.StartedAt), "total": r.Total, "passed": r.Passed, "failed": r.Failed})
		if len(out) == a.Limit {
			break
		}
	}
	return toJSON(out)
}

type runLab struct {
	Checks []struct {
		ID          int64           `json:"id"`
		TestID      int64           `json:"test_id"`
		ConnID      int64           `json:"conn_id"`
		IncidentKey string          `json:"incident_key"`
		Name        string          `json:"name"`
		Expect      json.RawMessage `json:"expect"`
		LastOK      *bool           `json:"last_ok"`
	} `json:"checks"`
	Verifications map[string]struct {
		OK        bool  `json:"ok"`
		CreatedAt int64 `json:"created_at"`
	} `json:"verifications"`
}

func (m *mcpClient) getIncidents(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.RunID <= 0 {
		return "", errors.New("run_id is required")
	}
	var run db.RunDetail
	if err := m.get(fmt.Sprintf("/api/v1/runs/%d", a.RunID), &run); err != nil {
		return "", err
	}
	var rl runLab
	_ = m.get(fmt.Sprintf("/api/v1/runs/%d/lab", a.RunID), &rl)
	res := map[string]any{"run": map[string]any{"id": run.ID, "name": run.Name, "status": run.Status, "project": run.Project,
		"environment": run.Environment, "branch": run.Branch, "commit": run.Commit, "framework": run.Framework, "failed": run.Failed, "total": run.Total}}
	if run.Summary == nil {
		res["incidents"] = []any{}
		res["note"] = "the run has no diagnosis yet (still running, or nothing failed)"
		return toJSON(res)
	}
	res["headline"] = run.Summary.Headline
	var incs []map[string]any
	for _, inc := range run.Summary.Incidents {
		v := map[string]any{"key": inc.Key, "title": inc.Title, "kind": inc.Kind}
		if inc.Cause != "" {
			v["probable_cause"] = inc.Cause + " (AI hypothesis, not confirmed)"
		}
		if inc.Action != "" {
			v["suggested_action"] = inc.Action
		}
		var tests []map[string]any
		inTest := map[int64]bool{}
		for i, id := range inc.TestIDs {
			inTest[id] = true
			name := ""
			if i < len(inc.TestNames) {
				name = inc.TestNames[i]
			}
			tests = append(tests, map[string]any{"id": id, "name": name})
		}
		v["tests"] = tests
		var evidence []string
		for _, e := range inc.Evidence {
			if e.Kind == "screenshot" {
				continue
			}
			evidence = append(evidence, e.Text)
			if e.Kind == "network" && e.Ref != 0 && v["causal_call_id"] == nil {
				v["causal_call_id"] = e.Ref
			}
		}
		v["evidence"] = evidence
		var checks []map[string]any
		for _, c := range rl.Checks {
			if c.IncidentKey == inc.Key || inTest[c.TestID] {
				checks = append(checks, map[string]any{"name": c.Name, "expect": c.Expect, "last_ok": c.LastOK})
			}
		}
		if checks != nil {
			v["checks"] = checks
		}
		if ver, ok := rl.Verifications[inc.Key]; ok {
			v["verification"] = map[string]any{"fixed": ver.OK, "at": iso(ver.CreatedAt)}
		}
		incs = append(incs, v)
	}
	res["incidents"] = incs
	return toJSON(res)
}

func (m *mcpClient) getTest(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		TestID int64 `json:"test_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.TestID <= 0 {
		return "", errors.New("test_id is required")
	}
	var t db.Test
	if err := m.get(fmt.Sprintf("/api/v1/tests/%d", a.TestID), &t); err != nil {
		return "", err
	}
	var conns []db.NetConn
	if err := m.get(fmt.Sprintf("/api/v1/tests/%d/network", a.TestID), &conns); err != nil {
		return "", err
	}
	var run db.Run
	_ = m.get(fmt.Sprintf("/api/v1/runs/%d", t.RunID), &run)
	res := map[string]any{"id": t.ID, "run_id": t.RunID, "name": t.Name, "status": t.Status, "key": t.Key,
		"error": t.ErrorMessage, "stack_trace": cut(t.ErrorTrace, 4000)}
	if cmds := repro.Commands(run.Framework, t.Key, run.Commit); len(cmds) > 0 {
		res["run_locally"] = cmds
	}
	if t.Triage != nil && t.Triage.State == "DONE" {
		res["ai_diagnosis"] = map[string]any{"category": t.Triage.Category, "summary": t.Triage.Summary, "suggestion": t.Triage.Suggestion}
	}
	var steps []string
	from := max(0, len(t.Logs)-30)
	for _, l := range t.Logs[from:] {
		steps = append(steps, fmt.Sprintf("[%s] %s", l.Status, cut(l.Message, 300)))
	}
	res["last_steps"] = steps
	var failed []map[string]any
	for _, c := range conns {
		if !(c.Failed || c.Status >= 400) || c.Expected {
			continue
		}
		f := map[string]any{"id": c.ID, "method": c.Method, "url": c.URL}
		if c.Failed || c.Status == 0 {
			f["error"] = c.ErrorText
		} else {
			f["status"] = c.Status
		}
		if c.ResponseBody != "" {
			f["response"] = cut(c.ResponseBody, 2000)
		}
		failed = append(failed, f)
		if len(failed) == 20 {
			break
		}
	}
	res["failed_calls"] = failed
	var console []string
	for _, e := range t.Console {
		if e.Level == "error" || e.Level == "pageerror" {
			console = append(console, cut(e.Text, 500))
		}
		if len(console) == 20 {
			break
		}
	}
	if console != nil {
		res["console_errors"] = console
	}
	return toJSON(res)
}

// loadCapsule reads a capsule from the server.
func (m *mcpClient) loadCapsule(path string) (*lab.Capsule, error) {
	var raw json.RawMessage
	if err := m.get(path, &raw); err != nil {
		return nil, err
	}
	return lab.ReadCapsule(raw)
}

// values are the secrets of TRACEREPORTS_LAB_<NAME>; never typed by the agent.
func (m *mcpClient) values(needs []string) (map[string]string, error) {
	vals := map[string]string{}
	var missing []string
	for _, n := range needs {
		if v := m.getenv(labEnv(n)); v != "" {
			vals[n] = v
		} else {
			missing = append(missing, labEnv(n))
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing secrets: ask the user to set %s in the environment of the TraceReports MCP server (the agent must not type them)", strings.Join(missing, ", "))
	}
	return vals, nil
}

var verdictMeaning = map[string]string{
	lab.VerdictReproduced: "the failure still happens exactly like in the run",
	lab.VerdictFixed:      "the call answers fine now; if nobody changed anything, the failure was transient or environmental",
	lab.VerdictSession:    "the backend asked for a login (401/403 or a redirect to the login): the secret may be wrong",
	lab.VerdictDifferent:  "it fails with a different error than in the run",
	lab.VerdictNetwork:    "the backend could not be reached from this machine",
	lab.VerdictOK:         "it answers fine and the original call had not failed either",
}

func (m *mcpClient) reproduce(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		CallID  int64  `json:"call_id"`
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.CallID <= 0 {
		return "", errors.New("call_id is required")
	}
	c, err := m.loadCapsule(fmt.Sprintf("/api/v1/network/%d/capsule", a.CallID))
	if err != nil {
		return "", err
	}
	if len(c.Calls) == 0 {
		return "", errors.New("the capsule has no call")
	}
	call := c.Calls[0]
	vals, err := m.values(call.Plan.Needs(nil))
	if err != nil {
		return "", err
	}
	res, err := lab.Run(ctx, call.Plan, lab.Options{Values: vals, BaseURL: a.BaseURL})
	if err != nil {
		return "", err
	}
	var steps []string
	for _, s := range res.Steps {
		line := fmt.Sprintf("%s %s %s → %d", s.Role, s.Method, s.URL, s.Status)
		if s.Error != "" {
			line = fmt.Sprintf("%s %s %s → %s", s.Role, s.Method, s.URL, s.Error)
		}
		steps = append(steps, line)
	}
	out := map[string]any{"verdict": res.Verdict, "meaning": verdictMeaning[res.Verdict], "steps": steps,
		"in_the_run": originalText(call.Plan.Original), "now": map[string]any{"status": res.Response.Status, "error": res.Response.Error,
			"response": cut(scrub(res.Response.Body, vals), 2000)}}
	if call.Plan.Original.Simulated {
		out["note"] = "the original answer has no date nor server headers: the test itself probably made it up (a mock)"
	}
	return toJSON(out)
}

func (m *mcpClient) verifyFix(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		RunID       int64  `json:"run_id"`
		IncidentKey string `json:"incident_key"`
		BaseURL     string `json:"base_url"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.RunID <= 0 || a.IncidentKey == "" {
		return "", errors.New("run_id and incident_key are required")
	}
	c, err := m.loadCapsule(fmt.Sprintf("/api/v1/runs/%d/lab/capsule?incident=%s", a.RunID, url.QueryEscape(a.IncidentKey)))
	if err != nil {
		return "", err
	}
	vals, err := m.values(capsuleNeeds(c, true))
	if err != nil {
		return "", err
	}
	var results []map[string]any
	all := true
	for _, call := range c.Calls {
		for _, ch := range call.Checks {
			base := a.BaseURL
			if base == "" {
				base = ch.BaseURL
			}
			r := map[string]any{"check": ch.Name, "expect": ch.Expect}
			res, err := lab.Run(ctx, call.Plan, lab.Options{Values: vals, BaseURL: base, Target: ch.Request})
			var fails []lab.Failure
			if err != nil {
				fails = []lab.Failure{{Kind: "network", Got: err.Error()}}
			} else {
				fails = ch.Expect.Eval(res.Response)
				r["status"] = res.Response.Status
			}
			r["passed"] = len(fails) == 0
			if len(fails) > 0 {
				r["failures"] = fails
				all = false
			}
			results = append(results, r)
		}
	}
	if results == nil {
		return "", errors.New("this incident has no saved checks: ask the user to save one from the TraceReports Lab (Save as a check), or use reproduce")
	}
	return toJSON(map[string]any{"fixed": all, "checks": results})
}

func scrub(body string, vals map[string]string) string {
	body = redact.Default().Text(body)
	for _, v := range vals {
		if len(v) >= 4 {
			body = strings.ReplaceAll(body, v, redact.Mask)
		}
	}
	return body
}

// version is set by the release build (-ldflags "-X main.version=v0.3.0").
var version string

// buildVersion is the release version, the module version of `go install`, or "dev".
func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func newMCPClient(getenv func(string) string) *mcpClient {
	base := strings.TrimRight(getenv("TRACEREPORTS_URL"), "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	return &mcpClient{base: base, getenv: getenv, http: &http.Client{Timeout: 60 * time.Second}}
}

// runMCP serves the FixLab tools to an agent over stdio.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: tracereports mcp\nMCP server (stdio) for agents such as Claude Code: reads runs, incidents and tests from\nTRACEREPORTS_URL (default http://localhost:8080; TRACEREPORTS_TOKEN or TRACEREPORTS_UI_USER/_PASSWORD if needed)\nand reproduces or verifies from this machine with the secrets of TRACEREPORTS_LAB_<NAME>.\nClaude Code: claude mcp add tracereports -e TRACEREPORTS_URL=http://localhost:8080 -- tracereports mcp")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	m := newMCPClient(os.Getenv)
	s := mcp.NewServer("tracereports", buildVersion(), mcpInstructions, m.tools()...)
	return s.Serve(context.Background(), os.Stdin, os.Stdout)
}
