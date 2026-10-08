package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

const (
	runTriageWait = 3 * time.Minute // max wait for the per-test analyses of the run
	// maxIncidentsForAI caps the incidents described to the model (prompt size and cost). Every
	// incident is still kept and shown; the rest go without AI cause/action.
	maxIncidentsForAI = 8
)

var categoryLabel = map[string]string{
	"LOCATOR_CHANGED": "Locator cambiado",
	"BACKEND_TIMEOUT": "Backend lento o caído",
	"LOGIC_BUG":       "Bug de lógica",
	"INFRA_ERROR":     "Error de infraestructura",
}

// AnalyzeRunAsync builds the diagnosis of a finished run in the background and then calls
// onDone (used to send notifications). Failures are grouped into incidents by probable
// cause; with an API key, Gemini also writes a headline and the cause/action of each one.
// A run already being diagnosed is not diagnosed twice at the same time: the request is
// remembered and the diagnosis runs once more when the current one ends (without onDone).
func (a *Analyzer) AnalyzeRunAsync(runID int64, onDone func()) {
	a.jobsMu.Lock()
	if a.runsBusy[runID] {
		a.runsAgain[runID] = true
		a.jobsMu.Unlock()
		return
	}
	a.runsBusy[runID] = true
	a.jobsMu.Unlock()
	if err := a.store.SetRunTriagePending(runID); err != nil {
		slog.Error("ai: mark run pending", "run_id", runID, "err", err)
		a.jobsMu.Lock()
		delete(a.runsBusy, runID)
		a.jobsMu.Unlock()
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() {
			a.jobsMu.Lock()
			again := a.runsAgain[runID]
			delete(a.runsBusy, runID)
			delete(a.runsAgain, runID)
			a.jobsMu.Unlock()
			if again {
				a.AnalyzeRunAsync(runID, nil)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), runTriageWait+2*time.Minute)
		defer cancel()
		a.waitTestTriage(ctx, runID)
		a.runSem <- struct{}{}
		defer func() { <-a.runSem }()
		rt, err := a.analyzeRun(ctx, runID)
		if err != nil {
			slog.Warn("ai: run analysis failed", "run_id", runID, "err", err)
			rt = &db.RunTriage{State: "ERROR", Error: a.redactor().Text(err.Error()), Incidents: []db.Incident{}}
		}
		if err := a.store.SaveRunTriage(runID, rt); err != nil {
			slog.Error("ai: save run triage", "run_id", runID, "err", err)
		}
		a.changed("summary", runID, 0)
		if onDone != nil {
			onDone()
		}
	}()
}

// waitTestTriage waits until the per-test analyses of the run finish (they feed the run one).
func (a *Analyzer) waitTestTriage(ctx context.Context, runID int64) {
	deadline := time.Now().Add(runTriageWait)
	for time.Now().Before(deadline) {
		n, err := a.store.PendingTestTriage(runID)
		if err != nil || n == 0 {
			return
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (a *Analyzer) analyzeRun(ctx context.Context, runID int64) (*db.RunTriage, error) {
	run, err := a.store.GetRunDetail(runID)
	if err != nil {
		return nil, err
	}
	incidents, err := a.groupFailures(run)
	if err != nil {
		return nil, err
	}
	rt := &db.RunTriage{State: "DONE", Incidents: incidents, Headline: defaultHeadline(run, incidents)}
	if rt.PendingTests, err = a.store.PendingTestTriage(runID); err != nil {
		return nil, err
	}
	if len(incidents) == 0 || !a.Enabled() {
		return rt, nil
	}
	forAI := incidents
	if len(forAI) > maxIncidentsForAI {
		forAI = forAI[:maxIncidentsForAI]
	}
	res, err := a.summarizeRun(ctx, run, forAI, len(incidents))
	if err != nil {
		// sin texto de IA, el resumen agrupado sigue siendo útil
		slog.Warn("ai: run summary failed, keeping grouped incidents", "run_id", runID, "err", err)
		return rt, nil
	}
	rt.AI, rt.AIIncidents = true, len(forAI)
	if res.Headline != "" {
		rt.Headline = res.Headline
	}
	rt.Summary = res.Summary
	for _, in := range res.Incidents {
		for i := range rt.Incidents {
			if rt.Incidents[i].Key == in.Key {
				rt.Incidents[i].Action = in.Action
				if in.InsufficientEvidence {
					rt.Incidents[i].Insufficient = true
				} else {
					rt.Incidents[i].Cause = in.Cause
				}
			}
		}
	}
	return rt, nil
}

// groupFailures clusters every failed test of a run into incidents (see grouping.go), biggest
// first. Nothing is dropped: every failed test belongs to exactly one incident.
func (a *Analyzer) groupFailures(run *db.RunDetail) ([]db.Incident, error) {
	byKey := map[string]*db.Incident{}
	var order []string
	for i := range run.Tests {
		t := &run.Tests[i]
		if t.Status != "FAIL" {
			continue
		}
		in, err := a.incidentFor(t)
		if err != nil {
			return nil, err
		}
		cur := byKey[in.Key]
		if cur == nil {
			in.TestIDs, in.TestNames = []int64{}, []string{}
			cur = &in
			byKey[in.Key] = cur
			order = append(order, in.Key)
		} else if len(cur.TestIDs) < 3 { // evidencia de los primeros tests, no de todos
			cur.Evidence = append(cur.Evidence, in.Evidence...)
		}
		cur.TestIDs = append(cur.TestIDs, t.ID)
		cur.TestNames = append(cur.TestNames, t.Name)
	}
	out := make([]db.Incident, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].TestIDs) > len(out[j].TestIDs) })
	return out, nil
}

func defaultHeadline(run *db.RunDetail, incidents []db.Incident) string {
	switch {
	case run.Failed == 0 && run.Total > 0:
		return fmt.Sprintf("Los %d tests terminaron sin fallos.", run.Total)
	case run.Failed == 0:
		return "La ejecución no tiene tests."
	case len(incidents) == 1 && run.Failed > 1:
		return fmt.Sprintf("%d tests fallaron con la misma evidencia: %s.", run.Failed, incidents[0].Title)
	case len(incidents) == 1:
		return fmt.Sprintf("1 test falló: %s.", incidents[0].Title)
	default:
		return fmt.Sprintf("%d tests fallaron, agrupados en %d incidentes.", run.Failed, len(incidents))
	}
}

type runSummary struct {
	Headline  string `json:"headline"`
	Summary   string `json:"summary"`
	Incidents []struct {
		Key                  string `json:"key"`
		Cause                string `json:"cause"`
		Action               string `json:"action"`
		InsufficientEvidence bool   `json:"insufficient_evidence"`
	} `json:"incidents"`
}

func (a *Analyzer) summarizeRun(ctx context.Context, run *db.RunDetail, incidents []db.Incident, total int) (*runSummary, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `You are the QA lead summarizing an automated test run for the team and for non-technical stakeholders.
The failed tests were already grouped into incidents by shared evidence (the same failing backend call
right before the failure, or the same error signature). The evidence lines are observed facts; what you
write about causes is a hypothesis and must be phrased as probable, never as confirmed. Write:
- "headline": one sentence with the conclusion (how many incidents, which is the main one).
- "summary": 2-3 sentences in plain language: impact, whether failures look like product bugs,
  backend/infrastructure problems or test maintenance (locators), and what to do first.
- for each incident (by its "key"): "cause" (one sentence, the probable cause, citing the evidence it
  relies on) and "action" (one concrete next step). If the evidence does not support any cause, set
  "insufficient_evidence" to true, leave "cause" empty and use "action" to say what evidence to collect.
  Do not give percentages or confidence scores.
%s
Reply with JSON only.

Run: %s (environment: %s)
Result: %d tests, %d passed, %d failed, %d skipped.
`, runLanguageInstruction(a.language()), run.Name, run.Environment, run.Total, run.Passed, run.Failed, run.Skipped)
	if total > len(incidents) {
		fmt.Fprintf(&b, "Only the %d largest of %d incidents are listed; mention that %d more were not analyzed.\n", len(incidents), total, total-len(incidents))
	}
	b.WriteString("\nIncidents:\n")
	byID := map[int64]db.Test{}
	for _, t := range run.Tests {
		byID[t.ID] = t
	}
	for _, in := range incidents {
		fmt.Fprintf(&b, "\n- key: %s\n  title: %s\n  tests (%d): %s\n", in.Key, in.Title, len(in.TestIDs), strings.Join(in.TestNames, "; "))
		for _, ev := range in.Evidence {
			if ev.Kind != "screenshot" {
				fmt.Fprintf(&b, "  evidence: %s\n", ev.Text)
			}
		}
		t := byID[in.TestIDs[0]]
		if msg := strings.TrimSpace(t.ErrorMessage); msg != "" {
			if len(msg) > 400 {
				msg = msg[:400] + "…"
			}
			fmt.Fprintf(&b, "  first error: %s\n", strings.ReplaceAll(msg, "\n", " | "))
		}
		if t.Triage != nil && t.Triage.State == "DONE" {
			fmt.Fprintf(&b, "  per-test AI diagnosis: [%s] %s\n", t.Triage.Category, t.Triage.Summary)
		}
	}

	keys := make([]string, len(incidents))
	for i, in := range incidents {
		keys[i] = in.Key
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"headline": map[string]any{"type": "string"},
			"summary":  map[string]any{"type": "string"},
			"incidents": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":                   map[string]any{"type": "string", "enum": keys},
					"cause":                 map[string]any{"type": "string"},
					"action":                map[string]any{"type": "string"},
					"insufficient_evidence": map[string]any{"type": "boolean"},
				},
				"required": []string{"key", "cause", "action", "insufficient_evidence"},
			}},
		},
		"required": []string{"headline", "summary", "incidents"},
	}
	text, err := a.generate(ctx, UsageRunSummary, b.String(), schema)
	if err != nil {
		return nil, err
	}
	var res runSummary
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		return nil, fmt.Errorf("decode run summary JSON: %w", err)
	}
	return &res, nil
}

// runLanguageInstruction is languageInstruction for the run summary (it reads the test names).
func runLanguageInstruction(lang string) string {
	if lang == "" {
		return "Answer in the same language as the test names when it is clearly not English; otherwise English."
	}
	return languageInstruction(lang)
}
