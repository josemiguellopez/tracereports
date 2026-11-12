package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/josemiguellopez/tracereports/internal/errtext"
	"github.com/josemiguellopez/tracereports/internal/locator"
	"image"
	_ "image/gif"  // capturas: los formatos que acepta el servidor
	_ "image/jpeg" //
	_ "image/png"  //
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai/builtin"
	"github.com/josemiguellopez/tracereports/internal/atrest"
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
	// AISkipped: why the AI was not called although it is configured ("no_failures": nothing to
	// explain, and a model asked to explain a failure invents one).
	AISkipped string `json:"ai_skipped,omitempty"`
	// Kind: KindSuccess for an AI summary of a run without failures ("" = of a failure).
	Kind      string `json:"kind,omitempty"`
	CreatedAt int64  `json:"created_at"`
	Facts     Facts  `json:"facts"`
	// Timing is how long an AI summary took, split between the provider and TraceReports (shown
	// under the card in the UI, not shared). Nil for the template.
	Timing *AITiming `json:"timing,omitempty"`
	// loadingEvidence: the template's line of evidence about the loading page (kept on an AI answer).
	loadingEvidence string
}

// AITiming splits how long an AI answer took: waiting for the provider (every call, retries
// included), pausing between retries, and the rest (TraceReports: evidence, prompt, redaction,
// parsing the answer).
type AITiming struct {
	TotalMs    int64       `json:"total_ms"`
	ProviderMs int64       `json:"provider_ms"`
	WaitMs     int64       `json:"wait_ms"`
	OwnMs      int64       `json:"own_ms"`
	Calls      int         `json:"calls"`
	Attempts   []AIAttempt `json:"attempts"` // cada llamada al proveedor, en orden
}

// AIAttempt is one call to the provider while writing an answer: how long it took, how it ended
// and the pause before the next one.
type AIAttempt struct {
	Ms        int64  `json:"ms"`
	OK        bool   `json:"ok"`
	Status    int    `json:"status,omitempty"` // HTTP del error (0: sin respuesta HTTP)
	Reason    string `json:"reason,omitempty"` // rate_limit | server | auth | timeout | network | other
	WaitMs    int64  `json:"wait_ms,omitempty"`
	WaitAsked bool   `json:"wait_asked,omitempty"` // la espera la pidió el proveedor
}

// Finish sets the total (measured by the caller, from the request) and what TraceReports took.
func (t *AITiming) Finish(total time.Duration) {
	if t == nil {
		return
	}
	t.TotalMs = total.Milliseconds()
	t.OwnMs = max(0, t.TotalMs-t.ProviderMs-t.WaitMs)
}

// Facts is the evidence an escalation is built from (also what the summary card shows).
type Facts struct {
	RunName     string `json:"run_name"`
	Env         string `json:"env"`
	RunStatus   string `json:"run_status"`
	StartedAt   int64  `json:"started_at"`
	DurationMs  int64  `json:"duration_ms"`
	Total       int    `json:"total"`
	Passed      int    `json:"passed"`
	Failed      int    `json:"failed"`
	Skipped     int    `json:"skipped"`
	Warning     int    `json:"warning,omitempty"`    // tests que terminaron con advertencia
	Running     int    `json:"running,omitempty"`    // tests que todavía no terminaron
	Incomplete  bool   `json:"incomplete,omitempty"` // se cerró sin que terminaran todos sus tests
	TestName    string `json:"test_name,omitempty"`
	TestTags    string `json:"test_tags,omitempty"`
	TestDesc    string `json:"test_desc,omitempty"`
	Error       string `json:"error,omitempty"`
	Category    string `json:"category,omitempty"`
	AISummary   string `json:"ai_summary,omitempty"`
	AISuggest   string `json:"ai_suggestion,omitempty"`
	LocatorPick string `json:"locator_pick,omitempty"`
	NoRule      bool   `json:"-"` // triage del motor local sin regla que concluya: el LLM diagnostica solo
	// TriageRule: the local engine's rule that wrote the test's triage ("" when an LLM or nothing did).
	// Its text is in the language the engine chose then; LocalizedFacts writes it again in the escalation's.
	TriageRule  string   `json:"-"`
	Steps       []string `json:"steps,omitempty"`
	Screenshot  string   `json:"screenshot,omitempty"` // URL relativa de la captura del fallo
	ShotCaption string   `json:"shot_caption,omitempty"`
	// ShotBlank: every screenshot of the test is blank (the page never rendered). The screenshot is
	// still the evidence, but shown as a notice: a big white box looks like an image that failed to load.
	ShotBlank   bool         `json:"shot_blank,omitempty"`
	Network     []NetFact    `json:"network,omitempty"`
	FailedTests []FailedFact `json:"failed_tests,omitempty"`
	// Where: where the (first) failure happened, from the data (never from the model): for QA.
	Where *WhereFailed `json:"where,omitempty"`
	// Loading: what said the page was still loading when the (first) failed test failed, in the
	// escalation's language (loadingBy has both; newEscalation picks one).
	Loading     []string `json:"loading,omitempty"`
	loadingBy   map[string][]string
	Incidents   []db.Incident `json:"incidents,omitempty"`
	RunHeadline string        `json:"run_headline,omitempty"`
	RunSummary  string        `json:"run_summary,omitempty"`
	Flaky       string        `json:"flaky,omitempty"` // "falló 3 de 8 ejecuciones"
	ReportPath  string        `json:"report_path"`     // "#run=1&view=tests&test=4"
	// Dev: technical detail for the developer (audience dev): the chosen test, or the first failed
	// tests of the run. Built on every response, never cached nor sent to the AI.
	Dev []*DevFacts `json:"dev,omitempty"`
	// Assessment: for a run without failures, the QA verdict (clean, observations, risks) and its
	// signals, computed from what the tests delivered (assessGreen). Shown on the card.
	Assessment *Assessment `json:"assessment,omitempty"`
	// Validated: for a run without failures, what its tests validated (names, descriptions, steps):
	// what the AI summarizes instead of inventing a failure.
	Validated []ValidatedFact `json:"-"`
}

// ValidatedFact is a test of a run without failures, with what it did.
type ValidatedFact struct {
	Name, Status, Tags, Description string
	DurationMs                      int64
	Steps                           []string
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
// WhereFailed is where a test failed: the test, the step it was on, its error (the useful part) and
// the selector it could not find.
type WhereFailed struct {
	Test     string `json:"test"`
	Step     string `json:"step,omitempty"`
	Error    string `json:"error,omitempty"`
	Selector string `json:"selector,omitempty"`
}

// whereFailed: the step is the FAIL one; when that step is only the screenshot taken on failure or
// repeats the error, the one before it says what the test was doing.
func whereFailed(t *db.Test) *WhereFailed {
	w := &WhereFailed{Test: t.Name, Error: errtext.Short(t.ErrorMessage, 300)}
	for i, l := range t.Logs {
		if l.Status != "FAIL" {
			continue
		}
		step := errtext.Short(l.Message, 200)
		if (l.Screenshot != "" || step == errtext.Short(t.ErrorMessage, 200)) && i > 0 {
			step = errtext.Short(t.Logs[i-1].Message, 200)
		}
		if step != "" && step != w.Error && step != errtext.Short(t.ErrorMessage, 200) {
			w.Step = step
		}
		break
	}
	// del error sin su envoltorio: si no, el selector arrastra el cierre de la lista ("]")
	w.Selector = locator.ExtractSelector(errtext.Useful(t.ErrorMessage), t.ErrorTrace)
	if w.Selector == "" {
		w.Selector = locator.ExtractSelector(w.Step)
	}
	w.Selector = errtext.Cut(w.Selector, 400)
	return w
}

type FailedFact struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
	Error    string `json:"error,omitempty"`
}

// clip cuts on a word, by letters (never in the middle of one).
func clip(s string, n int) string { return errtext.Cut(s, n) }

// firstLine is the informative line of an error, without the framework's wrapper (file name and list).
func firstLine(s string) string { return errtext.Short(s, 300) }

// blankImage reports a screenshot that is a single flat color (e.g. the empty page a browser shows
// when the backend is down): useless as evidence on an escalation card.
func blankImage(shots atrest.Dir, name string) bool {
	f, err := shots.Open(name)
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

// BuildFacts gathers the evidence of a run (testID 0) or of one of its tests. shots is where the
// screenshots live (encrypted or not), to skip blank ones.
func BuildFacts(store *db.Store, shots atrest.Dir, runID, testID int64) (*Facts, error) {
	run, err := store.GetRunDetail(runID)
	if err != nil {
		return nil, err
	}
	f := &Facts{RunName: run.Name, Env: run.Environment, RunStatus: run.Status, StartedAt: run.StartedAt,
		Total: run.Total, Passed: run.Passed, Failed: run.Failed, Skipped: run.Skipped,
		Warning: run.Warning, Running: run.Running, Incomplete: run.Incomplete,
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
		if testID == 0 && f.Failed == 0 && f.Total > 0 {
			if err := validatedFacts(store, run, f); err != nil {
				return nil, err
			}
			if f.Assessment, err = assessGreen(store, run); err != nil {
				return nil, err
			}
		}
		return f, nil
	}
	t, err := store.GetTest(focus)
	if err != nil {
		return nil, err
	}
	if t.RunID != runID {
		return nil, db.ErrNotFound
	}
	if t.Status == "FAIL" {
		f.Where = whereFailed(t)
	}
	if t.Status == "FAIL" {
		// ¿seguía cargando la página? Entonces "cambió el selector" sería falso (también en la IA)
		conns, err := store.NetworkTimeline(t.ID)
		if err != nil {
			return nil, err
		}
		sel := locator.ExtractSelector(errtext.Useful(t.ErrorMessage), t.ErrorTrace)
		f.loadingBy = map[string][]string{}
		for _, lang := range []string{"es", "en"} {
			l := loadingSignals(store, t, conns, lang)
			if s := selectorBefore(store, t, sel, lang); s != "" {
				l = append(l, s)
			}
			f.loadingBy[lang] = l
		}
	}
	if testID == 0 && f.Failed == 1 && t.Status == "FAIL" {
		// la ejecución completa con un solo fallo: la ficha nombra el test y la evidencia trae su error
		f.TestName, f.Error = t.Name, clip(t.ErrorMessage, 1500)
	}
	if testID != 0 {
		f.TestName, f.TestTags, f.TestDesc, f.Error = t.Name, t.Category, t.Description, clip(t.ErrorMessage, 1500)
		f.ReportPath = fmt.Sprintf("#run=%d&view=tests&test=%d", runID, t.ID)
		if t.Triage != nil && t.Triage.State == "DONE" {
			f.Category, f.AISummary, f.AISuggest, f.LocatorPick = t.Triage.Category, t.Triage.Summary, t.Triage.Suggestion, t.Triage.LocatorPick
			f.NoRule = t.Triage.Provider == ProviderBuiltin && t.Triage.Rule == builtin.DefaultRule
			if t.Triage.Provider == ProviderBuiltin {
				f.TriageRule = t.Triage.Rule
			}
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
			f.Screenshot, f.ShotCaption, f.ShotBlank = shot, clip(t.Logs[i].Message, 160), shots.Path != ""
		}
		if shots.Path == "" || !blankImage(shots, filepath.Base(shot)) {
			f.Screenshot, f.ShotCaption, f.ShotBlank = shot, clip(t.Logs[i].Message, 160), false
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

// maxValidated bounds the tests of a green run described to the AI (the prompt stays short).
const maxValidated = 12

// validatedFacts fills, for a run without failures, what its tests validated (the first ones, with
// the start and the end of their steps).
func validatedFacts(store *db.Store, run *db.RunDetail, f *Facts) error {
	for _, t := range run.Tests {
		if len(f.Validated) >= maxValidated {
			break
		}
		full, err := store.GetTest(t.ID)
		if err != nil {
			return err
		}
		v := ValidatedFact{Name: t.Name, Status: t.Status, Tags: t.Category, Description: clip(t.Description, 300)}
		if t.EndedAt != nil {
			v.DurationMs = *t.EndedAt - t.StartedAt
		}
		logs := full.Logs
		if len(logs) > 8 { // el comienzo y el final del flujo
			logs = append(append([]db.Log{}, logs[:3]...), logs[len(logs)-5:]...)
		}
		for _, l := range logs {
			v.Steps = append(v.Steps, fmt.Sprintf("[%s] %s", l.Status, clip(l.Message, 160)))
		}
		f.Validated = append(f.Validated, v)
	}
	return nil
}

// ---- generación ----

var audienceGuide = map[string]string{
	"business": `Audience: business stakeholders and product owners (non-technical).
- No jargon: no stack traces, selectors, HTTP codes, endpoints or class names: name the service or the step in plain words instead of its method and status code.
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

// loadingFacts: the loading signals in the escalation's language, or in English for a prompt built
// straight from BuildFacts (the chat).
func (f *Facts) loadingFacts() []string {
	if len(f.Loading) > 0 {
		return f.Loading
	}
	return f.loadingBy["en"]
}

func (f *Facts) promptBlock(scope string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run: %s · environment: %s · status: %s · %d tests: %d passed, %d failed, %d skipped.\n",
		f.RunName, f.Env, f.RunStatus, f.Total, f.Passed, f.Failed, f.Skipped)
	if f.RunStatus == "RUNNING" || f.Running > 0 || f.Incomplete {
		fmt.Fprintf(&b, "The run has not finished or was interrupted (%d tests did not finish): the results are partial, do not describe it as successful.\n", f.Running)
	}
	if f.RunHeadline != "" {
		fmt.Fprintf(&b, "Run diagnosis: %s %s\n", f.RunHeadline, f.RunSummary)
	}
	if scope == "test" {
		fmt.Fprintf(&b, "\nFailed test: %s (tags: %s)\nDescription: %s\nError: %s\n", f.TestName, f.TestTags, f.TestDesc, f.Error)
		if f.NoRule { // su consejo ("configura un proveedor de IA…") no le sirve al LLM que lee esto
			b.WriteString("Per-test triage: inconclusive (no rule of the local rules engine explains it): diagnose it yourself from the error and the steps.\n")
		} else if f.Category != "" {
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
	if l := f.loadingFacts(); len(l) > 0 {
		b.WriteString("Loading signals at the moment of the failure (from the page and its network): " + strings.Join(l, "; ") + ". " +
			"The element did not show up in time because the page was still loading: the cause is a wait in the test (TIMING). " +
			"Do not say that the selector or the UI changed; the next step is to wait for the load to finish.\n")
	}
	if f.ShotBlank {
		b.WriteString("Every screenshot of the test is blank: the page never rendered (it did not finish loading). Do not " +
			"conclude that the UI changed or that a selector broke: the element could never appear on a blank page.\n")
	} else if f.Screenshot != "" {
		fmt.Fprintf(&b, "A screenshot of the moment of the failure is attached to the report (%s).\n", f.ShotCaption)
	}
	return b.String()
}

// Escalate writes the summary for an audience. Without AI (or if the provider fails) it falls back
// to a template built from the same facts, so the feature always works.
// EscalateTemplate writes the summary from the facts only, without calling the AI (chosen in the
// UI to save quota or when the evidence must not leave the company).
func EscalateTemplate(f *Facts, runID, testID int64, audience, lang string) *Escalation {
	e := newEscalation(f, runID, testID, audience, lang)
	templateEscalation(e)
	return e
}

// newEscalation starts an escalation from its facts, with the QA signals in its language (a copy:
// the facts are not changed).
func newEscalation(f *Facts, runID, testID int64, audience, lang string) *Escalation {
	e := &Escalation{RunID: runID, TestID: testID, Audience: audience, Lang: lang, Facts: *f, CreatedAt: db.NowMs()}
	if l, ok := f.loadingBy[lang]; ok {
		e.Facts.Loading = l
	} else if l, ok := f.loadingBy["es"]; ok {
		e.Facts.Loading = l
	}
	if as := f.Assessment; as != nil {
		cp := *as
		cp.Signals = append([]Signal(nil), as.Signals...)
		cp.Localize(lang)
		e.Facts.Assessment = &cp
	}
	return e
}

// LocalizedFacts returns the facts with the test's diagnosis written in lang when the local engine
// wrote it: the engine picks its language from the test names, so an escalation in Spanish showed the
// probable cause in English. Its rules are deterministic and exist in both languages, so the same rule
// is written again in lang; if the evidence now concludes another rule (it changed since), the stored
// text stays, so the escalation never says something the triage did not. The facts are not changed.
func (a *Analyzer) LocalizedFacts(f *Facts, testID int64, lang string) *Facts {
	if f == nil || testID == 0 || f.TriageRule == "" || (lang != "es" && lang != "en") {
		return f
	}
	t, err := a.store.GetTest(testID)
	if err != nil {
		return f
	}
	in, err := a.builtinInput(t, lang)
	if err != nil {
		return f
	}
	d := a.redactDiagnosis(builtin.Diagnose(in))
	if d.Rule != f.TriageRule || d.Summary == "" {
		return f
	}
	c := *f
	c.AISummary, c.AISuggest = d.Summary, d.Suggestion
	return &c
}

func (a *Analyzer) Escalate(ctx context.Context, f *Facts, runID, testID int64, audience, lang string) *Escalation {
	e := newEscalation(f, runID, testID, audience, lang)
	write := a.escalateAI
	if NothingFailed(f, testID) {
		// sin fallos no hay fallo que explicar: un modelo al que se le pide escalar uno lo inventa (un
		// RUT inválido, un servicio caído...). Una ejecución terminada sin fallos se resume como tal
		// (qué se validó y qué conviene saber); una en curso, incompleta o toda omitida, con los datos.
		if !finishedGreen(f) || !a.UsesLLM() {
			if a.UsesLLM() {
				e.AISkipped = "no_failures"
			}
			templateEscalation(e)
			return e
		}
		write = a.escalateSuccessAI
	}
	if a.UsesLLM() { // el motor local no redacta: la plantilla ya es lo suyo
		start := time.Now()
		ctx, calls := withCallTiming(ctx)
		err := write(ctx, e)
		calls.mu.Lock()
		e.Timing = &AITiming{ProviderMs: calls.provider.Milliseconds(), WaitMs: calls.wait.Milliseconds(), Calls: calls.calls,
			Attempts: append([]AIAttempt{}, calls.attempts...)}
		calls.mu.Unlock()
		e.Timing.Finish(time.Since(start)) // el que llama lo vuelve a cerrar con su propio comienzo
		switch {
		case err == nil:
			return e
		case errors.Is(err, ErrInventedFailure):
			e.AISkipped = "invented_failure" // respondió, pero describió un fallo que no existe
		default:
			e.AIError = a.redactor().Text(err.Error() + HintText(err, a.Config()))
		}
	}
	templateEscalation(e)
	return e
}

// finishedGreen: the run finished complete, without failures, and something ran (not all skipped).
func finishedGreen(f *Facts) bool {
	return f.Failed == 0 && f.Total > 0 && f.RunStatus != "RUNNING" && f.Running == 0 && !f.Incomplete && f.Skipped < f.Total
}

// KindSuccess marks an AI summary written for a run without failures (with the success prompt).
const KindSuccess = "success"

// successGuide: what each audience wants to know of a run that passed.
var successGuide = map[string]string{
	"business": `Audience: business stakeholders (non-technical). Say which business flows were validated, in plain words
(from the test names, descriptions and steps), what the verdict means for the release and, if there are risks,
why they matter, without technical jargon.`,
	"qa": `Audience: QA. Say what was covered (tests and their flows), and explain each risk and observation as a senior
QA would: why it matters, whether it can hide a real defect, and what to check or harden in the test.`,
	"dev": `Audience: developers. Summarize what was validated and explain the technical signals (server errors the test
did not catch, uncaught exceptions, retries, aborted calls, slow tests) with their method and URL when given,
and what to look at in the code or the backend.`,
}

// verdictText: the verdict as the AI has to state it.
var verdictText = map[string]string{
	VerdictClean:        "PASSED CLEAN: nothing to object.",
	VerdictObservations: "PASSED WITH OBSERVATIONS: nothing broke the flows, but there are signals worth knowing.",
	VerdictRisks:        "PASSED WITH RISKS: every test passed, but there are signals a senior QA would not sign off without reviewing.",
}

// escalateSuccessAI writes, with the AI, the summary of a finished run without failures: what was
// validated and what is worth knowing. Severity, cause and owner are not the model's to choose:
// there is no failure. A text that still describes one is rejected (ErrInventedFailure).
func (a *Analyzer) escalateSuccessAI(ctx context.Context, e *Escalation) error {
	language := "Spanish"
	if e.Lang == "en" {
		language = "English"
	}
	verdict := VerdictClean
	if as := e.Facts.Assessment; as != nil {
		verdict = as.Verdict
	}
	prompt := fmt.Sprintf(`You are a senior QA lead writing the status summary of an automated test run that FINISHED
WITHOUT FAILURES, to be pasted in Teams, Slack or an email. Write it in %s.
No test failed: there is NO failure, NO bug and NO incident. Never describe, suggest or invent one, and do not
invent endpoints, errors, services or data: use only the evidence below. Call the calls with an error
"aborted" or "with an error", not "failed".

The QA verdict was already decided from the evidence and you must not change it: %s
Write it in the title with these exact words: "%s". Justify it in the headline with the signals listed
(risks first). A run that passed with risks or observations is not a plain "everything is fine"; and
observations are not risks: do not call them risks.
%s

Fields: "title" (max 80 chars: what was tested and "%s"), "headline" (one sentence with the conclusion),
"what_happened" (what was validated, from the tests, descriptions and steps), "impact" (what the verdict
means for the release), "next_steps" (1 to 4 concrete items for the signals; if the verdict is clean, say
there is nothing to do). The evidence list is added by TraceReports: do not write it.

Evidence:
%s`, language, verdictText[verdict], verdictLabel(verdict, e.Lang), successGuide[e.Audience], verdictLabel(verdict, e.Lang), e.Facts.successBlock())
	str := map[string]any{"type": "string"}
	list := map[string]any{"type": "array", "items": str}
	text, err := a.generate(ctx, UsageEscalation, prompt, map[string]any{
		"type":       "object",
		"properties": map[string]any{"title": str, "headline": str, "what_happened": str, "impact": str, "next_steps": list},
		"required":   []string{"title", "headline", "what_happened", "impact", "next_steps"},
	})
	if err != nil {
		return err
	}
	var out struct {
		Title, Headline string
		WhatHappened    string   `json:"what_happened"`
		Impact          string   `json:"impact"`
		NextSteps       []string `json:"next_steps"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return fmt.Errorf("decode escalation JSON: %w", err)
	}
	all := strings.Join(append([]string{out.Title, out.Headline, out.WhatHappened, out.Impact}, out.NextSteps...), "\n")
	if strings.TrimSpace(out.Title) == "" || ClaimsFailure(all) {
		return ErrInventedFailure
	}
	t := tpl[e.Lang]
	if t == nil {
		t = tpl["es"]
	}
	// el veredicto con las palabras del idioma, aunque el modelo lo copie en inglés
	title := englishVerdict.ReplaceAllString(out.Title, verdictLabel(verdict, e.Lang))
	e.Title, e.Headline, e.WhatHappened, e.Impact, e.NextSteps = title, out.Headline, out.WhatHappened, out.Impact, out.NextSteps
	e.Evidence = successEvidence(e, t) // de los datos, nunca del modelo
	e.Severity, e.RootCause, e.Owner = verdictSeverity(verdict), t["ok_cause"], t["owner_none"]
	if verdict != VerdictClean || e.Facts.Warning > 0 {
		e.Owner = t["owner_qa"]
	}
	e.Source, e.Model, e.Kind = "ai", a.Model(), KindSuccess
	return nil
}

// englishVerdict is the verdict as the prompt states it, copied by a model into a title in another
// language ("PASSED WITH OBSERVACIONES").
var englishVerdict = regexp.MustCompile(`(?i)passed\s+(?:with\s+(?:observ\p{L}*|risks?|riesgos?)|clean)`)

// successEvidence is the evidence of a run without failures: its counters and each QA signal.
func successEvidence(e *Escalation, t map[string]string) []string {
	f := e.Facts
	env := f.Env
	if env == "" {
		env = "—"
	}
	out := []string{fmt.Sprintf(t["ev_run"], f.RunName, env, f.Passed, f.Failed, f.Skipped)}
	if as := f.Assessment; as != nil {
		for _, s := range as.Signals {
			if len(out) == 6 {
				break
			}
			out = append(out, s.Text)
		}
	}
	return out
}

// verdictSeverity: a run that passed with risks is worth a look (medium); otherwise low.
func verdictSeverity(verdict string) string {
	if verdict == VerdictRisks {
		return "medium"
	}
	return "low"
}

// ErrInventedFailure: the AI described a failure in a run that has none (its text is discarded).
var ErrInventedFailure = errors.New("the AI described a failure that does not exist in this run, so the summary was built from the data")

// negatedFailure is a failure word that is denied ("sin fallos", "ningún test falló", "0 failed",
// "no failures"): it does not claim one.
var negatedFailure = regexp.MustCompile(`(?i)\b(?:no|ni|sin|ning[uú]n[oa]?|ninguno|0|cero|none|zero|without|nothing|never|nunca)\b(?:\W+[\p{L}\d]+){0,3}?\W+(?:fall\p{L}*|fail\p{L}*|bugs?|incident\p{L}*)`)

// callFailure is a network call or resource that errored ("3 llamadas fallaron", "requests failed"),
// or a test that failed an attempt and then passed ("falló en el primer intento", "failed on the first
// attempt", "historial: falló 2 de 10"): signals of a green run, not a failure of it.
var callFailure = regexp.MustCompile(`(?i)\b(?:calls?|llamadas?|requests?|peticiones?|solicitudes?|recursos?|resources?|im[aá]genes|images?|console)(?:\W+[\p{L}\d]+){0,3}?\W+(?:fall\p{L}*|fail\p{L}*)` +
	`|(?:fall\p{L}*|fail\p{L}*)(?:\W+[\p{L}\d]+){0,4}?\W+(?:intentos?|attempts?|reintent\p{L}*|retr\p{L}*|ejecuciones|runs|historial|history)\b` +
	`|\b(?:intentos?|attempts?|reintent\p{L}*|retr\p{L}*|historial|history|inestable|flaky|unstable)(?:\W+[\p{L}\d]+){0,4}?\W+(?:fall\p{L}*|fail\p{L}*)`)

// failureWord is a claim of a failure, a bug, an incident or a service down.
var failureWord = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:fall[oó]|fallaron|fallid[oa]s?|fallos?|failed|fails|failures?|bugs?|incidentes?|incidents?|ca[ií]d[oa]s?|is down|went down)(?:[^\p{L}]|$)`)

// ClaimsFailure reports whether a summary of a run without failures still describes one.
func ClaimsFailure(text string) bool {
	text = callFailure.ReplaceAllString(negatedFailure.ReplaceAllString(text, " "), " ")
	return failureWord.MatchString(text)
}

// successBlock is the evidence of a run without failures for the AI.
func (f *Facts) successBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run: %s · environment: %s · finished: %d tests, %d passed, %d with warnings, %d skipped, 0 failed",
		f.RunName, f.Env, f.Total, f.Passed, f.Warning, f.Skipped)
	if f.DurationMs > 0 {
		fmt.Fprintf(&b, " · took %s", (time.Duration(f.DurationMs) * time.Millisecond).Round(time.Second))
	}
	b.WriteString(".\n\nTests:\n")
	for _, v := range f.Validated {
		fmt.Fprintf(&b, "- %s [%s]", v.Name, v.Status)
		if v.Tags != "" {
			fmt.Fprintf(&b, " · tags: %s", v.Tags)
		}
		if v.Description != "" {
			fmt.Fprintf(&b, " · %s", v.Description)
		}
		b.WriteString("\n")
		for _, s := range v.Steps {
			fmt.Fprintf(&b, "    %s\n", s)
		}
	}
	if as := f.Assessment; as != nil && len(as.Signals) > 0 {
		b.WriteString("\nQA signals (none of them made a test fail):\n")
		for _, s := range as.Signals {
			fmt.Fprintf(&b, "- [%s] %s\n", strings.ToUpper(s.Level), signalText(s, "en"))
		}
	} else {
		b.WriteString("\nQA signals: none.\n")
	}
	return b.String()
}

// NothingFailed reports whether a run-wide escalation has no failure to explain: tests that ran
// and none failed (passed, warnings, skipped, still running or cut). Its summary comes from the
// facts only.
func NothingFailed(f *Facts, testID int64) bool {
	return testID == 0 && f.Failed == 0 && f.Total > 0
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
	fillFromTemplate(e)
	e.Source, e.Model = "ai", a.Model()
	return nil
}

// schemaWords are the field names of the escalation JSON: a small model sometimes copies the name
// of a field instead of filling it ("evidence": ["title"]).
var schemaWords = map[string]bool{"title": true, "severity": true, "headline": true, "what_happened": true, "impact": true,
	"evidence": true, "root_cause": true, "next_steps": true, "owner": true, "string": true, "n/a": true, "none": true, "-": true}

// usable: a field the model really wrote (not empty, not a field name nor a placeholder).
func usable(s string) bool {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(s), `"'.:`))
	return t != "" && !schemaWords[t] && len([]rune(t)) >= 3
}

// selectorBlameRe: a text that says the selector or the screen changed (or that it must be updated).
var selectorBlameRe = regexp.MustCompile(`(?i)(selector|locator|xpath|localizador)[^.]{0,80}(cambi|chang|actualiz|updat|ya no coincide|no longer match|roto|broken)|(cambi|chang)[^.]{0,60}(interfaz|\bui\b|pantalla|screen|selector|locator)|problema con (el )?selector|selector problem`)

var sentenceRe = regexp.MustCompile(`[.;|\n]`)

// selectorDeniedRe: the sentences that say the opposite (the template's own: "el selector no cambió").
var selectorDeniedRe = regexp.MustCompile(`(?i)(el|the) selector (no|did not|didn't) (cambió|cambio|change)d?|no cambiar el selector|do not change the selector|no (es )?(un )?cambio de la interfaz|not a UI change|no es un selector roto|not a broken selector`)

func blamesSelector(s string) bool {
	for _, part := range sentenceRe.Split(s, -1) { // frase por frase: una negación no tapa otra frase
		if !selectorDeniedRe.MatchString(part) && selectorBlameRe.MatchString(part) {
			return true
		}
	}
	return false
}

func usableList(xs []string) []string {
	var out []string
	for _, x := range xs {
		if usable(x) {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}

// fillFromTemplate replaces, with what the template builds from the evidence, every field the model
// left empty, filled with a field name, or wrote as a copy of the title (the executive summary
// repeating the title says nothing). The rest of the model's text stays.
func fillFromTemplate(e *Escalation) {
	tpl := *e
	templateEscalation(&tpl)
	same := func(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }
	if !usable(e.Title) {
		e.Title = tpl.Title
	}
	if !usable(e.Headline) || same(e.Headline, e.Title) {
		e.Headline = tpl.Headline
	}
	for _, f := range []struct {
		got *string
		tpl string
	}{{&e.WhatHappened, tpl.WhatHappened}, {&e.Impact, tpl.Impact},
		{&e.RootCause, tpl.RootCause}, {&e.Owner, tpl.Owner}} {
		if !usable(*f.got) {
			*f.got = f.tpl
		}
	}
	if e.Evidence = usableList(e.Evidence); len(e.Evidence) == 0 {
		e.Evidence = tpl.Evidence
	}
	// la página seguía cargando y el modelo igual culpa al selector o a la interfaz: gana la plantilla,
	// que sale de la evidencia (un modelo chico repite la categoría del error aunque se le diga)
	if len(e.Facts.Loading) > 0 {
		if blamesSelector(e.Headline) {
			e.Headline = tpl.Headline
		}
		if blamesSelector(e.WhatHappened) || blamesSelector(e.RootCause) || blamesSelector(strings.Join(e.NextSteps, " ")) {
			e.WhatHappened, e.RootCause, e.NextSteps = tpl.WhatHappened, tpl.RootCause, tpl.NextSteps
			e.Severity, e.Owner, e.Impact = tpl.Severity, tpl.Owner, tpl.Impact
		}
		if tpl.loadingEvidence != "" && !strings.Contains(strings.Join(e.Evidence, "\n"), tpl.loadingEvidence) {
			e.Evidence = append(e.Evidence, tpl.loadingEvidence) // un hecho, no una opinión del modelo
		}
	}
	if e.NextSteps = usableList(e.NextSteps); len(e.NextSteps) == 0 {
		e.NextSteps = tpl.NextSteps
	}
	switch e.Severity {
	case "critical", "high", "medium", "low":
	default:
		e.Severity = tpl.Severity
	}
}
