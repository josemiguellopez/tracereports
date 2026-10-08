// Package ai classifies test failures with an LLM: Google Gemini, Anthropic Claude, OpenAI (or any
// OpenAI-compatible API) or a local Ollama model.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
	"github.com/josemiguellopez/tracereports/internal/locator"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

const (
	maxTraceChars = 6000
	maxConcurrent = 2 // keeps us under free-tier rate limits
	maxAttempts   = 3
	// network errors included in the prompt, and response-body chars per error
	maxNetworkErrors = 10
	maxNetBodyChars  = 300
)

// Categories accepted from the model.
var Categories = []string{"LOCATOR_CHANGED", "BACKEND_TIMEOUT", "LOGIC_BUG", "INFRA_ERROR"}

// Result is the structured diagnosis returned by the model.
type Result struct {
	Category           string `json:"category"`
	Summary            string `json:"summary"`
	Suggestion         string `json:"suggestion"`
	RecommendedLocator string `json:"recommended_locator"`
	LocatorReason      string `json:"locator_reason"`
}

// Analyzer dispatches asynchronous failure analyses. Its provider can be changed at runtime
// (Settings screen) with SetConfig.
type Analyzer struct {
	mu     sync.RWMutex
	cfg    Config
	lang   string // idioma de los diagnósticos: "" (el del test), "es" o "en"
	store  *db.Store
	client *http.Client
	sem    chan struct{}
	runSem chan struct{} // resúmenes de ejecución simultáneos
	wg     sync.WaitGroup

	// trabajos en curso: evitan análisis duplicados (y su costo) del mismo test o ejecución
	jobsMu    sync.Mutex
	testsBusy map[int64]bool
	runsBusy  map[int64]bool
	runsAgain map[int64]bool // pedido otra vez mientras corría: se repite al terminar
	// análisis automático pedido mientras el test se analizaba (su resultado cambió): se repite
	testsAgain map[int64]bool
	// cancela el análisis en curso de un test cuyo resultado cambió (no reintenta evidencia vieja)
	testsCancel map[int64]context.CancelFunc
	// Redact masks secrets in every prompt (nil = redact.Default()).
	Redact *redact.Policy
	// MaxPerRun limits the automatic per-test analyses of one run (TRACEREPORTS_AI_MAX_PER_RUN,
	// default 50); the rest are marked SKIPPED and can still be analyzed from the UI.
	MaxPerRun int

	// OnChange (opcional) se llama cuando termina un análisis: kind "triage" (test) o
	// "summary" (ejecución). La UI lo recibe en vivo por SSE.
	OnChange func(kind string, runID, testID int64)
}

func (a *Analyzer) redactor() *redact.Policy {
	if a.Redact == nil {
		return redact.Default()
	}
	return a.Redact
}

func (a *Analyzer) changed(kind string, runID, testID int64) {
	if a.OnChange == nil {
		return
	}
	if runID == 0 {
		runID, _ = a.store.TestRunID(testID)
	}
	a.OnChange(kind, runID, testID)
}

// New builds an Analyzer configured from the environment (see ConfigFromEnv). Without a provider
// it is a no-op.
func New(store *db.Store) *Analyzer {
	maxPerRun := 50
	if n, err := strconv.Atoi(env.Get("AI_MAX_PER_RUN")); err == nil && n >= 0 {
		maxPerRun = n
	}
	return &Analyzer{
		cfg:         ConfigFromEnv(),
		store:       store,
		client:      &http.Client{Timeout: 3 * time.Minute}, // los modelos locales (Ollama) pueden tardar
		sem:         make(chan struct{}, maxConcurrent),
		runSem:      make(chan struct{}, 1),
		testsBusy:   map[int64]bool{},
		runsBusy:    map[int64]bool{},
		runsAgain:   map[int64]bool{},
		testsAgain:  map[int64]bool{},
		testsCancel: map[int64]context.CancelFunc{},
		MaxPerRun:   maxPerRun,
	}
}

// SetConfig switches the provider for the next analyses.
func (a *Analyzer) SetConfig(c Config) error {
	c = c.withDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = c
	a.mu.Unlock()
	return nil
}

// Config returns the active configuration (with defaults applied).
func (a *Analyzer) Config() Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

// SetLanguage sets the language of the diagnoses: "es", "en" or "" (same as the test).
func (a *Analyzer) SetLanguage(lang string) {
	a.mu.Lock()
	a.lang = lang
	a.mu.Unlock()
}

func (a *Analyzer) language() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lang
}

// Enabled reports whether a usable provider is configured.
func (a *Analyzer) Enabled() bool {
	c := a.Config()
	return c.Provider != "" && c.Validate() == nil
}

// Model returns the configured model name.
func (a *Analyzer) Model() string { return a.Config().Model }

// Provider returns the configured provider id ("" when AI is off).
func (a *Analyzer) Provider() string { return a.Config().Provider }

// Test sends a tiny request with c and reports how long the provider took (Settings screen:
// "Probar conexión").
func (a *Analyzer) Test(ctx context.Context, c Config) (time.Duration, error) {
	c = c.withDefaults()
	if c.Provider == "" {
		return 0, errors.New("elige un proveedor")
	}
	if err := c.Validate(); err != nil {
		return 0, err
	}
	start := time.Now()
	text, u, err := call(ctx, a.client, c, `Health check. Reply with the JSON object {"ok": true}.`, map[string]any{
		"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"},
	})
	a.recordUsage(UsageTest, c, u, err)
	if err != nil {
		return 0, err
	}
	var r struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(text), &r) != nil {
		return 0, fmt.Errorf("el modelo respondió, pero no con JSON válido: %.120s", text)
	}
	return time.Since(start), nil
}

// AnalyzeAsync analyzes a failed test in the background (automatic: when the test finishes). It
// does nothing without AI, when the test is already being analyzed or was already diagnosed (a
// repeated finish does not pay twice), or past the per-run budget (the test is marked SKIPPED).
func (a *Analyzer) AnalyzeAsync(testID int64) bool { return a.analyzeAsync(testID, false) }

// ReanalyzeAsync analyzes a test again on request (Re-analizar): it ignores the previous
// diagnosis and the per-run budget, but never runs twice at the same time.
func (a *Analyzer) ReanalyzeAsync(testID int64) bool { return a.analyzeAsync(testID, true) }

func (a *Analyzer) analyzeAsync(testID int64, force bool) bool {
	if !a.Enabled() {
		return false
	}
	a.jobsMu.Lock()
	if a.testsBusy[testID] {
		if force {
			a.jobsMu.Unlock()
			return false
		}
		// automático con un análisis en curso: el resultado pudo cambiar (FAIL tardío con otro
		// error). Se repite al terminar; si el diagnóstico anterior se invalidó, queda pendiente
		// desde ya. Un reenvío idéntico encuentra el diagnóstico hecho y no gasta IA.
		a.testsAgain[testID] = true
		a.jobsMu.Unlock()
		if fail, err := a.store.NeedsDiagnosis(testID); err == nil && fail {
			// reserva atómica: respeta el límite por ejecución, también para un SKIPPED (si no hay
			// cupo, al terminar el análisis en curso se repite y queda SKIPPED)
			if state, _, _, err := a.store.TriageBudget(testID); err == nil && (state == "" || state == "SKIPPED") {
				_, _, _ = a.store.ReserveTriage(testID, a.MaxPerRun)
			}
		}
		return false // no se encola un segundo análisis: solo se revisa al terminar el actual
	}
	a.testsBusy[testID] = true
	a.jobsMu.Unlock()
	release := func() {
		a.jobsMu.Lock()
		again := a.testsAgain[testID]
		delete(a.testsBusy, testID)
		delete(a.testsAgain, testID)
		a.jobsMu.Unlock()
		if again {
			a.analyzeAsync(testID, false)
		}
	}
	// solo se diagnostica un fallo vigente: un análisis encolado (o recuperado tras un reinicio)
	// para un test que entretanto pasó a PASS/SKIP/WARNING no se inicia
	if fail, err := a.store.NeedsDiagnosis(testID); err != nil || !fail {
		release()
		if err == nil {
			a.changed("triage", 0, testID)
		}
		return false
	}
	if force {
		// Re-analizar a mano: no cuenta para el límite automático (comportamiento buscado)
		if err := a.store.SetTriagePending(testID); err != nil {
			slog.Error("ai: mark pending", "test_id", testID, "err", err)
			release()
			return false
		}
	} else {
		// comprobar el cupo y reservarlo es una sola transacción (ver db.ReserveTriage)
		outcome, runID, err := a.store.ReserveTriage(testID, a.MaxPerRun)
		switch {
		case err != nil:
			slog.Error("ai: triage budget", "test_id", testID, "err", err)
			release()
			return false
		case outcome == db.TriageDone:
			release()
			return false
		case outcome == db.TriageOverBudget:
			_ = a.store.SaveTriageSkipped(testID, fmt.Sprintf("No se analizó automáticamente: la ejecución #%d alcanzó el límite de %d diagnósticos con IA (TRACEREPORTS_AI_MAX_PER_RUN). Puedes analizarlo con Re-analizar.", runID, a.MaxPerRun))
			release()
			a.changed("triage", 0, testID)
			return false
		}
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer release()
		a.sem <- struct{}{}
		defer func() { <-a.sem }()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		a.jobsMu.Lock()
		a.testsCancel[testID] = cancel
		a.jobsMu.Unlock()
		defer func() {
			a.jobsMu.Lock()
			delete(a.testsCancel, testID)
			a.jobsMu.Unlock()
			cancel()
		}()
		// el resultado pudo cambiar mientras esperaba turno: solo se analiza un fallo vigente
		if fail, err := a.store.NeedsDiagnosis(testID); err != nil || !fail {
			a.changed("triage", 0, testID)
			return
		}
		// versión del resultado analizado: si cambia mientras tanto, este diagnóstico no se guarda
		rev, err := a.store.TestResultRev(testID)
		if err == nil {
			err = a.analyze(ctx, testID, rev)
		}
		if err != nil && ctx.Err() == context.Canceled {
			err = nil // reemplazado por un resultado nuevo (Supersede): no es un error del proveedor
		}
		if err != nil {
			slog.Warn("ai: analysis failed", "test_id", testID, "err", err)
			_ = a.store.SaveTriageErrorAt(testID, rev, a.redactor().Text(err.Error()+HintText(err, a.Config())))
		}
		a.changed("triage", 0, testID)
		a.refreshRunSummary(testID)
	}()
	return true
}

// Supersede tells the analyzer that a test's result changed: an analysis of the previous result
// still running is cancelled (no more provider calls or retries for old evidence). Whether the new
// result needs its own diagnosis is decided by AnalyzeAsync.
func (a *Analyzer) Supersede(testID int64) {
	a.jobsMu.Lock()
	cancel := a.testsCancel[testID]
	a.jobsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// refreshRunSummary rewrites the run diagnosis when it was written while this test's analysis
// was still pending and no analysis of the run is pending anymore (late evidence).
func (a *Analyzer) refreshRunSummary(testID int64) {
	runID, err := a.store.TestRunID(testID)
	if err != nil {
		return
	}
	rt, err := a.store.GetRunTriage(runID)
	if err != nil || rt == nil || rt.State != "DONE" || rt.PendingTests == 0 {
		return
	}
	if n, err := a.store.PendingTestTriage(runID); err == nil && n == 0 {
		a.AnalyzeRunAsync(runID, nil)
	}
}

// Recover resumes the work a restart interrupted: per-test analyses left PENDING are queued
// again (or closed as ERROR when AI is no longer configured) and run diagnoses left PENDING are
// rebuilt; onRunDone (optional) is called after each one, like when the run finished.
func (a *Analyzer) Recover(onRunDone func(runID int64)) {
	tests, runs, err := a.store.PendingWork()
	if err != nil {
		slog.Error("ai: recover pending work", "err", err)
		return
	}
	for _, id := range tests {
		if a.ReanalyzeAsync(id) {
			continue
		}
		if fail, err := a.store.NeedsDiagnosis(id); err == nil && fail {
			_ = a.store.SaveTriageError(id, "El servidor se reinició durante el análisis y la IA ya no está configurada.")
		}
	}
	for _, runID := range runs {
		runID := runID
		a.AnalyzeRunAsync(runID, func() {
			if onRunDone != nil {
				onRunDone(runID)
			}
		})
	}
	if len(tests)+len(runs) > 0 {
		slog.Info("ai: resumed work interrupted by a restart", "tests", len(tests), "runs", len(runs))
	}
}

// Wait blocks until in-flight analyses finish or ctx expires (used on shutdown).
func (a *Analyzer) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (a *Analyzer) analyze(ctx context.Context, testID, rev int64) error {
	t, err := a.store.GetTest(testID)
	if err != nil {
		return err
	}
	lastStep := "(no steps logged)"
	for i := len(t.Logs) - 1; i >= 0; i-- {
		if t.Logs[i].Message != "" {
			lastStep = fmt.Sprintf("[%s] %s", t.Logs[i].Status, t.Logs[i].Message)
			break
		}
	}
	netErrors, err := a.store.ListNetworkErrors(testID, maxNetworkErrors)
	if err != nil {
		return err
	}
	cands, failed, err := LocatorCandidates(a.store, t)
	if err != nil {
		return err
	}
	res, err := a.classify(ctx, buildPrompt(t, lastStep, netErrors, a.language())+consolePromptSection(t.Console)+locatorPromptSection(failed, cands), cands)
	if err != nil {
		return err
	}
	if saved, err := a.store.SaveTriageAt(testID, rev, res.Category, res.Summary, res.Suggestion, res.RecommendedLocator, res.LocatorReason); err != nil || !saved {
		return err // !saved: el resultado cambió; el análisis repetido guarda el del nuevo
	}
	return nil
}

// languageInstruction tells the model which language to answer in.
func languageInstruction(lang string) string {
	switch lang {
	case "es":
		return "Answer in Spanish."
	case "en":
		return "Answer in English."
	}
	return "Answer in the same language as the test name/description when it is clearly not English; otherwise English."
}

// maxConsoleInPrompt is how many browser console errors go to the model.
const maxConsoleInPrompt = 10

// consolePromptSection lists the browser console errors of the test (uncaught page errors first):
// a JavaScript error is often why "the element never appeared". "" when there are none.
func consolePromptSection(entries []db.ConsoleEntry) string {
	var lines []string
	for _, pass := range []string{"pageerror", "error"} {
		for _, e := range entries {
			if e.Level != pass || len(lines) >= maxConsoleInPrompt {
				continue
			}
			text := strings.Join(strings.Fields(e.Text), " ")
			if len(text) > 300 {
				text = text[:300] + "..."
			}
			line := fmt.Sprintf("- [%s] %s", e.Level, text)
			if e.Location != "" {
				line += " (" + e.Location + ")"
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nBrowser console errors during the test (an uncaught JavaScript error often explains a UI that\n" +
		"did not update, which is a LOGIC_BUG rather than a changed locator):\n" + strings.Join(lines, "\n")
}

func buildPrompt(t *db.Test, lastStep string, netErrors []db.NetConn, lang string) string {
	trace := t.ErrorTrace
	if len(trace) > maxTraceChars {
		trace = trace[:maxTraceChars] + "\n...[truncated]"
	}
	network := "(no network capture for this test)"
	if t.NetworkTotal > 0 {
		network = fmt.Sprintf("%d requests captured, %d did not end OK.", t.NetworkTotal, t.NetworkErrors)
		for _, c := range netErrors {
			outcome := fmt.Sprintf("HTTP %d %s", c.Status, c.StatusText)
			if c.Failed || c.Status == 0 {
				outcome = "NO RESPONSE: " + c.ErrorText
			}
			if c.DurationMs != nil {
				outcome += fmt.Sprintf(" after %d ms", *c.DurationMs)
			}
			network += fmt.Sprintf("\n- %s %s -> %s", c.Method, c.URL, outcome)
			if body := strings.Join(strings.Fields(c.ResponseBody), " "); body != "" {
				if len(body) > maxNetBodyChars {
					body = body[:maxNetBodyChars] + "..."
				}
				network += "\n  response: " + body
			}
		}
	}
	return fmt.Sprintf(`You are a senior QA automation engineer triaging a failed automated UI/API test.
Classify the root cause into exactly one category:
- LOCATOR_CHANGED: element not found, stale element, selector/XPath/test-id no longer matches the DOM.
- BACKEND_TIMEOUT: slow or unresponsive backend/API, HTTP 5xx, request/wait timeouts caused by the server.
- LOGIC_BUG: the application behaved incorrectly (wrong value, failed assertion on business data).
- INFRA_ERROR: browser/driver crash, network/DNS, CI agent, environment or test-data setup problems.

Reply with JSON only: {"category": "...", "summary": "...", "suggestion": "..."}.
"summary": one or two sentences explaining what happened. "suggestion": a concrete next step to fix it.
%s

Test name: %s
Category/tags: %s
Description: %s
Last step: %s
Error message: %s
Stack trace:
%s

Backend calls made by the browser during the test (failed ones listed; HTTP 5xx, timeouts or
missing responses usually point to BACKEND_TIMEOUT or INFRA_ERROR rather than a UI problem):
%s`, languageInstruction(lang), t.Name, t.Category, t.Description, lastStep, t.ErrorMessage, trace, network)
}

func (a *Analyzer) classify(ctx context.Context, prompt string, cands []locator.Suggestion) (*Result, error) {
	props := map[string]any{
		"category":   map[string]any{"type": "string", "enum": Categories},
		"summary":    map[string]any{"type": "string"},
		"suggestion": map[string]any{"type": "string"},
	}
	if len(cands) > 0 {
		var sels []string
		for _, c := range cands {
			sels = append(sels, c.Python)
		}
		props["recommended_locator"] = map[string]any{"type": "string", "enum": sels}
		props["locator_reason"] = map[string]any{"type": "string"}
	}
	text, err := a.generate(ctx, UsageTriage, prompt, map[string]any{
		"type":       "object",
		"properties": props,
		"required":   []string{"category", "summary", "suggestion"},
	})
	if err != nil {
		return nil, err
	}
	var r Result
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		return nil, fmt.Errorf("decode triage JSON: %w", err)
	}
	r.Category = normalizeCategory(r.Category)
	return &r, nil
}

// generate asks the configured provider for a JSON answer matching schema (standard JSON Schema)
// and returns its text, retrying rate limits (429) and server errors with backoff. Every call to
// the provider (retries included) is counted in the AI usage under kind.
func (a *Analyzer) generate(ctx context.Context, kind, prompt string, schema map[string]any) (string, error) {
	c := a.Config()
	// segunda capa: nada sale al proveedor sin pasar por la redacción (datos antiguos incluidos)
	prompt = a.redactor().Text(prompt)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		text, u, err := call(ctx, a.client, c, prompt, schema)
		a.recordUsage(kind, c, u, err)
		if err == nil {
			return text, nil
		}
		lastErr = err
		var se *httpStatusError
		if !errors.As(err, &se) || !se.retryable() || attempt == maxAttempts {
			break
		}
		select {
		case <-time.After(time.Duration(attempt*attempt) * 5 * time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "", lastErr
}

// Kinds of AI calls in the usage report (Settings → AI usage).
const (
	UsageTriage     = "triage"      // diagnóstico de un test
	UsageRunSummary = "run_summary" // resumen de la ejecución
	UsageEscalation = "escalation"  // escalamiento con IA
	UsageTest       = "test"        // Probar conexión
)

// recordUsage counts one call to the provider (a failed one too: a provider may charge it, and
// errors are shown). Without a store (tests, the static report) nothing is recorded.
func (a *Analyzer) recordUsage(kind string, c Config, u Usage, err error) {
	if a.store == nil || c.Provider == "" {
		return
	}
	if e := a.store.AddAIUsage(db.AIUsageCall{At: time.Now(), Provider: c.Provider, Model: c.Model, Kind: kind,
		Failed: err != nil, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, TokensKnown: u.Known}); e != nil {
		slog.Warn("ai: usage not recorded", "err", e)
	}
}

func normalizeCategory(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	for _, known := range Categories {
		if c == known {
			return c
		}
	}
	return "LOGIC_BUG"
}
