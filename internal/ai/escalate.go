package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"  // capturas: los formatos que acepta el servidor
	_ "image/jpeg" //
	_ "image/png"  //
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Audiences of an escalation summary.
var Audiences = []string{"business", "qa", "dev"}

// Escalation is a failure summary written for one audience, ready to share (Teams, Slack, email).
type Escalation struct {
	RunID        int64    `json:"run_id"`
	TestID       int64    `json:"test_id"` // 0 = la ejecución completa
	Audience     string   `json:"audience"`
	Lang         string   `json:"lang"`
	Title        string   `json:"title"`
	Severity     string   `json:"severity"` // critical | high | medium | low
	Headline     string   `json:"headline"`
	WhatHappened string   `json:"what_happened"`
	Impact       string   `json:"impact"`
	Evidence     []string `json:"evidence"`
	RootCause    string   `json:"root_cause"`
	NextSteps    []string `json:"next_steps"`
	Owner        string   `json:"owner"`
	Source       string   `json:"source"`          // ai | template
	Model        string   `json:"model,omitempty"` // modelo que lo escribió
	AIError      string   `json:"ai_error,omitempty"`
	CreatedAt    int64    `json:"created_at"`
	Facts        Facts    `json:"facts"`
}

// Facts is the evidence an escalation is built from (also what the summary card shows).
type Facts struct {
	RunName     string        `json:"run_name"`
	Env         string        `json:"env"`
	RunStatus   string        `json:"run_status"`
	StartedAt   int64         `json:"started_at"`
	DurationMs  int64         `json:"duration_ms"`
	Total       int           `json:"total"`
	Passed      int           `json:"passed"`
	Failed      int           `json:"failed"`
	Skipped     int           `json:"skipped"`
	TestName    string        `json:"test_name,omitempty"`
	TestTags    string        `json:"test_tags,omitempty"`
	TestDesc    string        `json:"test_desc,omitempty"`
	Error       string        `json:"error,omitempty"`
	Category    string        `json:"category,omitempty"`
	AISummary   string        `json:"ai_summary,omitempty"`
	AISuggest   string        `json:"ai_suggestion,omitempty"`
	LocatorPick string        `json:"locator_pick,omitempty"`
	Steps       []string      `json:"steps,omitempty"`
	Screenshot  string        `json:"screenshot,omitempty"` // URL relativa de la captura del fallo
	ShotCaption string        `json:"shot_caption,omitempty"`
	Network     []NetFact     `json:"network,omitempty"`
	FailedTests []FailedFact  `json:"failed_tests,omitempty"`
	Incidents   []db.Incident `json:"incidents,omitempty"`
	RunHeadline string        `json:"run_headline,omitempty"`
	RunSummary  string        `json:"run_summary,omitempty"`
	Flaky       string        `json:"flaky,omitempty"` // "falló 3 de 8 ejecuciones"
	ReportPath  string        `json:"report_path"`     // "#run=1&view=tests&test=4"
	// Dev: technical detail for the developer (audience dev): the chosen test, or the first failed
	// tests of the run. Built on every response, never cached nor sent to the AI.
	Dev []*DevFacts `json:"dev,omitempty"`
}

// NetFact is a backend call that failed.
type NetFact struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Host       string `json:"host"`
	Status     int    `json:"status"`
	Outcome    string `json:"outcome"` // "HTTP 503" | "sin respuesta (net::ERR_…)"
	DurationMs int64  `json:"duration_ms"`
	Body       string `json:"body,omitempty"`
	StartedAt  int64  `json:"started_at,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`   // para buscarla en los logs del backend
	RequestID  string `json:"request_id,omitempty"` // X-Request-Id y similares
}

// FailedFact is one failed test of the run.
type FailedFact struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
	Error    string `json:"error,omitempty"`
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clip(s, 300)
}

// blankImage reports a screenshot that is a single flat color (e.g. the empty page a browser shows
// when the backend is down): useless as evidence on an escalation card.
func blankImage(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return false
	}
	b := img.Bounds()
	lo, hi := uint32(1<<16), uint32(0)
	for y := b.Min.Y; y < b.Max.Y; y += max(1, b.Dy()/40) {
		for x := b.Min.X; x < b.Max.X; x += max(1, b.Dx()/40) {
			r, g, bl, _ := img.At(x, y).RGBA()
			l := (r*299 + g*587 + bl*114) / 1000
			lo, hi = min(lo, l), max(hi, l)
		}
	}
	return hi-lo < 0x0800 // menos de ~3% de contraste en toda la imagen
}

// BuildFacts gathers the evidence of a run (testID 0) or of one of its tests. shotsDir is where
// the screenshots live, to skip blank ones.
func BuildFacts(store *db.Store, shotsDir string, runID, testID int64) (*Facts, error) {
	run, err := store.GetRunDetail(runID)
	if err != nil {
		return nil, err
	}
	f := &Facts{RunName: run.Name, Env: run.Environment, RunStatus: run.Status, StartedAt: run.StartedAt,
		Total: run.Total, Passed: run.Passed, Failed: run.Failed, Skipped: run.Skipped,
		ReportPath: fmt.Sprintf("#run=%d&view=tests", runID)}
	if run.EndedAt != nil {
		f.DurationMs = *run.EndedAt - run.StartedAt
	}
	if run.Summary != nil {
		f.RunHeadline, f.RunSummary, f.Incidents = run.Summary.Headline, run.Summary.Summary, run.Summary.Incidents
	}
	for _, t := range run.Tests {
		if t.Status != "FAIL" {
			continue
		}
		ff := FailedFact{ID: t.ID, Name: t.Name, Error: firstLine(t.ErrorMessage)}
		if t.Triage != nil && t.Triage.State == "DONE" {
			ff.Category = t.Triage.Category
		}
		f.FailedTests = append(f.FailedTests, ff)
	}
	if len(f.FailedTests) > 12 {
		f.FailedTests = f.FailedTests[:12]
	}
	focus := testID
	if focus == 0 && len(f.FailedTests) > 0 {
		focus = f.FailedTests[0].ID // la evidencia visual de la ejecución: su primer fallo
	}
	if focus == 0 {
		return f, nil
	}
	t, err := store.GetTest(focus)
	if err != nil {
		return nil, err
	}
	if t.RunID != runID {
		return nil, db.ErrNotFound
	}
	if testID != 0 {
		f.TestName, f.TestTags, f.TestDesc, f.Error = t.Name, t.Category, t.Description, clip(t.ErrorMessage, 1500)
		f.ReportPath = fmt.Sprintf("#run=%d&view=tests&test=%d", runID, t.ID)
		if t.Triage != nil && t.Triage.State == "DONE" {
			f.Category, f.AISummary, f.AISuggest, f.LocatorPick = t.Triage.Category, t.Triage.Summary, t.Triage.Suggestion, t.Triage.LocatorPick
		}
		start := 0
		if len(t.Logs) > 10 {
			start = len(t.Logs) - 10
		}
		for _, l := range t.Logs[start:] {
			f.Steps = append(f.Steps, fmt.Sprintf("[%s] %s", l.Status, clip(l.Message, 200)))
		}
		if hist, err := store.TestHistory(t.Key, runID, 10); err == nil && len(hist) > 1 {
			fails, retried := 0, 0
			statuses := make([]string, len(hist))
			for i, h := range hist {
				statuses[i] = h.Status
				if h.Status == "FAIL" {
					fails++
				} else if h.Attempts > 1 {
					retried++
				}
			}
			if kind, _, _ := db.Classify(statuses, retried); kind == db.StabilityFlaky {
				f.Flaky = fmt.Sprintf("%d/%d", fails, len(hist))
			}
		}
	}
	for i := len(t.Logs) - 1; i >= 0; i-- { // la última captura con contenido antes del fallo
		shot := t.Logs[i].Screenshot
		if shot == "" {
			continue
		}
		if f.Screenshot == "" { // si todas están en blanco, se queda la última
			f.Screenshot, f.ShotCaption = shot, clip(t.Logs[i].Message, 160)
		}
		if shotsDir == "" || !blankImage(filepath.Join(shotsDir, filepath.Base(shot))) {
			f.Screenshot, f.ShotCaption = shot, clip(t.Logs[i].Message, 160)
			break
		}
	}
	conns, err := store.ListNetworkErrors(focus, 5)
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		nf := NetFact{Method: c.Method, Path: c.URL, Status: c.Status, Outcome: fmt.Sprintf("HTTP %d", c.Status), Body: clip(strings.Join(strings.Fields(c.ResponseBody), " "), 240),
			StartedAt: c.StartedAt, TraceID: c.TraceID, RequestID: c.RequestID}
		if u, err := url.Parse(c.URL); err == nil && u.Host != "" {
			nf.Host, nf.Path = u.Host, u.Path
		}
		if c.Failed || c.Status == 0 {
			nf.Outcome = "sin respuesta (" + c.ErrorText + ")"
		}
		if c.DurationMs != nil {
			nf.DurationMs = *c.DurationMs
		}
		f.Network = append(f.Network, nf)
	}
	return f, nil
}

// ---- generación ----

var audienceGuide = map[string]string{
	"business": `Audience: business stakeholders and product owners (non-technical).
- No jargon: no stack traces, selectors, HTTP codes, endpoints or class names. Say "the login service did not respond", not "POST /auth 503".
- Focus on impact on users and on the business process, the risk for the release and what is being done.
- "owner": the area that should act (e.g. "Backend team", "QA automation", "Infrastructure").
- "next_steps": decisions or follow-ups a manager understands (2-3).`,
	"qa": `Audience: QA team.
- Explain what the test covered, the step where it failed and how to reproduce it.
- Classify it: product bug, environment/backend problem, test maintenance (locator) or flaky test, and say if it blocks the regression.
- Reference the evidence: screenshot, failed backend calls, history (flaky).
- "next_steps": concrete QA actions (re-run, update locator, open a bug with this evidence…).`,
	"dev": `Audience: developers.
- Be technical and precise: failing endpoint with method and status, error message, the relevant stack/log line, response body excerpts, suggested locator if any.
- "root_cause": the most likely technical cause based on the evidence, phrased as a hypothesis ("probably…"), never as confirmed.
- "next_steps": concrete debugging or fix steps (logs to check, request to replay, code area).`,
}

func (f *Facts) promptBlock(scope string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run: %s · environment: %s · status: %s · %d tests: %d passed, %d failed, %d skipped.\n",
		f.RunName, f.Env, f.RunStatus, f.Total, f.Passed, f.Failed, f.Skipped)
	if f.RunHeadline != "" {
		fmt.Fprintf(&b, "Run diagnosis: %s %s\n", f.RunHeadline, f.RunSummary)
	}
	if scope == "test" {
		fmt.Fprintf(&b, "\nFailed test: %s (tags: %s)\nDescription: %s\nError: %s\n", f.TestName, f.TestTags, f.TestDesc, f.Error)
		if f.Category != "" {
			fmt.Fprintf(&b, "Per-test AI triage: [%s] %s Suggestion: %s\n", f.Category, f.AISummary, f.AISuggest)
		}
		if f.LocatorPick != "" {
			fmt.Fprintf(&b, "Suggested replacement locator: %s\n", f.LocatorPick)
		}
		if f.Flaky != "" {
			fmt.Fprintf(&b, "History: flaky, failed %s of its recent runs.\n", f.Flaky)
		}
		if len(f.Steps) > 0 {
			b.WriteString("Last steps:\n- " + strings.Join(f.Steps, "\n- ") + "\n")
		}
	} else {
		b.WriteString("\nFailed tests:\n")
		for _, t := range f.FailedTests {
			fmt.Fprintf(&b, "- %s [%s]: %s\n", t.Name, t.Category, t.Error)
		}
		for _, in := range f.Incidents {
			fmt.Fprintf(&b, "Incident (%d tests): %s · cause: %s · action: %s\n", len(in.TestIDs), in.Title, in.Cause, in.Action)
		}
	}
	if len(f.Network) > 0 {
		b.WriteString("Failed backend calls:\n")
		for _, n := range f.Network {
			fmt.Fprintf(&b, "- %s %s%s -> %s after %d ms. Body: %s\n", n.Method, n.Host, n.Path, n.Outcome, n.DurationMs, n.Body)
		}
	}
	if f.Screenshot != "" {
		fmt.Fprintf(&b, "A screenshot of the moment of the failure is attached to the report (%s).\n", f.ShotCaption)
	}
	return b.String()
}

// Escalate writes the summary for an audience. Without AI (or if the provider fails) it falls back
// to a template built from the same facts, so the feature always works.
// EscalateTemplate writes the summary from the facts only, without calling the AI (chosen in the
// UI to save quota or when the evidence must not leave the company).
func EscalateTemplate(f *Facts, runID, testID int64, audience, lang string) *Escalation {
	e := &Escalation{RunID: runID, TestID: testID, Audience: audience, Lang: lang, Facts: *f, CreatedAt: db.NowMs()}
	templateEscalation(e)
	return e
}

func (a *Analyzer) Escalate(ctx context.Context, f *Facts, runID, testID int64, audience, lang string) *Escalation {
	e := &Escalation{RunID: runID, TestID: testID, Audience: audience, Lang: lang, Facts: *f, CreatedAt: db.NowMs()}
	if a.Enabled() {
		if err := a.escalateAI(ctx, e); err == nil {
			return e
		} else {
			e.AIError = a.redactor().Text(err.Error() + HintText(err, a.Config()))
		}
	}
	templateEscalation(e)
	return e
}

func (a *Analyzer) escalateAI(ctx context.Context, e *Escalation) error {
	scope := "run"
	if e.TestID != 0 {
		scope = "test"
	}
	language := "Spanish"
	if e.Lang == "en" {
		language = "English"
	}
	prompt := fmt.Sprintf(`You are a senior QA lead writing an escalation about a failed automated test %s, to be pasted
in Teams, Slack or an email. Write it in %s. Be concrete and honest: use only the evidence below and do
not invent data. Keep it short: each field one to three sentences, lists of 2 to 4 items.

%s

Fields: "title" (max 80 chars), "severity" (critical: blocks users or the release; high: important
feature broken; medium: partial or workaround exists; low: test maintenance or cosmetic), "headline"
(one sentence with the conclusion), "what_happened", "impact", "evidence" (bullet facts that prove it),
"root_cause", "next_steps", "owner".

Evidence:
%s`, map[string]string{"run": "run", "test": "case"}[scope], language, audienceGuide[e.Audience], e.Facts.promptBlock(scope))
	str := map[string]any{"type": "string"}
	list := map[string]any{"type": "array", "items": str}
	text, err := a.generate(ctx, UsageEscalation, prompt, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": str, "severity": map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}},
			"headline": str, "what_happened": str, "impact": str, "evidence": list, "root_cause": str, "next_steps": list, "owner": str,
		},
		"required": []string{"title", "severity", "headline", "what_happened", "impact", "evidence", "root_cause", "next_steps", "owner"},
	})
	if err != nil {
		return err
	}
	var out struct {
		Title, Severity, Headline, Owner string
		WhatHappened                     string   `json:"what_happened"`
		Impact                           string   `json:"impact"`
		Evidence                         []string `json:"evidence"`
		RootCause                        string   `json:"root_cause"`
		NextSteps                        []string `json:"next_steps"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return fmt.Errorf("decode escalation JSON: %w", err)
	}
	e.Title, e.Severity, e.Headline, e.Owner = out.Title, out.Severity, out.Headline, out.Owner
	e.WhatHappened, e.Impact, e.Evidence, e.RootCause, e.NextSteps = out.WhatHappened, out.Impact, out.Evidence, out.RootCause, out.NextSteps
	e.Source, e.Model = "ai", a.Model()
	return nil
}
