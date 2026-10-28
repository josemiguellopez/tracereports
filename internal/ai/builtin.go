package ai

import (
	"fmt"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/ai/builtin"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
)

// Diagnóstico con el motor local de reglas (internal/ai/builtin). Aquí se reúne la evidencia que el
// resto del paquete ya correlaciona (la llamada causal, el frame del proyecto, el paso que falló, la
// consola, el historial y cuánto falló la ejecución) y se entrega al motor. Nada de esto usa la red.

// historyForBuiltin is how many past executions of the test the engine reads (flakiness).
const historyForBuiltin = 20

// UsesLLM reports whether the diagnoses are written by a language model (a provider other than
// the local rules engine).
func (a *Analyzer) UsesLLM() bool {
	return a.Enabled() && a.Provider() != ProviderBuiltin
}

// builtinHint: with an LLM configured, its prompts carry the conclusion of the local engine first
// (the LLM confirms or corrects it and writes it up). TRACEREPORTS_AI_BUILTIN_HINT=0 removes it.
func builtinHint() bool {
	switch strings.ToLower(strings.TrimSpace(env.Get("AI_BUILTIN_HINT"))) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// builtinLang is the language of the engine's texts: the configured one, or the test names'.
func (a *Analyzer) builtinLang(names ...string) string {
	if l := a.language(); l == "es" || l == "en" {
		return l
	}
	return builtin.DetectLang(names...)
}

// builtinInput gathers the evidence of a failed test (t comes from GetTest: steps and console).
func (a *Analyzer) builtinInput(t *db.Test, lang string) (builtin.Input, error) {
	conns, err := a.store.NetworkTimeline(t.ID)
	if err != nil {
		return builtin.Input{}, err
	}
	in := builtin.Input{Lang: lang, Error: t.ErrorMessage, Trace: t.ErrorTrace, Location: innermostFrame(t.ErrorTrace)}
	for _, l := range t.Logs {
		if l.Status == "FAIL" {
			in.FailedStep = l.Message
			break
		}
	}
	failAt := failureTime(t)
	if c := causalConn(conns, failAt); c != nil {
		host, path := db.NormalizeEndpoint(c.URL)
		before := int64(-1)
		if failAt > 0 && c.StartedAt > 0 {
			before = max(0, failAt-c.StartedAt) // con la holgura de causalSlack puede empezar "después"
		}
		in.Causal = &builtin.Call{Method: c.Method, Endpoint: host + path, Host: host, Status: c.Status,
			Failed: c.Failed, Error: c.ErrorText, BeforeMs: before}
	}
	for _, e := range t.Console {
		if e.Level == "pageerror" {
			in.PageErrors = append(in.PageErrors, e.Text)
		}
	}
	if t.Key != "" {
		hist, err := a.store.TestHistory(t.Key, t.RunID, historyForBuiltin)
		if err != nil {
			return builtin.Input{}, err
		}
		statuses, retried := make([]string, 0, len(hist)), 0
		for _, h := range hist {
			statuses = append(statuses, h.Status)
			if h.Status != "FAIL" && h.Attempts > 1 {
				retried++
			}
		}
		in.Stability, in.Flips, _ = db.Classify(statuses, retried)
	}
	run, err := a.store.GetRun(t.RunID)
	if err != nil {
		return builtin.Input{}, err
	}
	in.RunFailed, in.RunTotal = run.Failed, run.Total
	return in, nil
}

// diagnoseBuiltin is the local engine's diagnosis of a failed test, with its texts redacted.
func (a *Analyzer) diagnoseBuiltin(t *db.Test) (builtin.Diagnosis, error) {
	in, err := a.builtinInput(t, a.builtinLang(t.Name, t.Description))
	if err != nil {
		return builtin.Diagnosis{}, err
	}
	return a.redactDiagnosis(builtin.Diagnose(in)), nil
}

func (a *Analyzer) redactDiagnosis(d builtin.Diagnosis) builtin.Diagnosis {
	r := a.redactor()
	d.Summary, d.Suggestion = r.Text(d.Summary), r.Text(d.Suggestion)
	return d
}

// builtinIncidents diagnoses the first test of each incident with the totals of the finished run
// (a mostly failed run points at the environment).
func (a *Analyzer) builtinIncidents(run *db.RunDetail, incidents []db.Incident) ([]builtin.Diagnosis, string, error) {
	names := make([]string, 0, len(run.Tests))
	for _, t := range run.Tests {
		names = append(names, t.Name)
	}
	lang := a.builtinLang(names...)
	out := make([]builtin.Diagnosis, len(incidents))
	for i, inc := range incidents {
		t, err := a.store.GetTest(inc.TestIDs[0])
		if err != nil {
			return nil, "", err
		}
		in, err := a.builtinInput(t, lang)
		if err != nil {
			return nil, "", err
		}
		in.RunFailed, in.RunTotal = run.Failed, run.Total
		out[i] = a.redactDiagnosis(builtin.Diagnose(in))
	}
	return out, lang, nil
}

// builtinRun fills the causes, actions, headline and summary of a run diagnosis from the local
// engine (provider builtin: nothing goes to an LLM).
func (a *Analyzer) builtinRun(run *db.RunDetail, rt *db.RunTriage) error {
	diags, lang, err := a.builtinIncidents(run, rt.Incidents)
	if err != nil {
		return err
	}
	incs := make([]builtin.Incident, len(diags))
	for i, d := range diags {
		in := &rt.Incidents[i]
		in.Cause, in.Action, in.Rule, in.Insufficient = d.Summary, d.Suggestion, d.Rule, !d.Conclusive
		if in.Category == "" {
			in.Category = d.Category
		}
		incs[i] = builtin.Incident{Title: in.Title, Tests: len(in.TestIDs), Diagnosis: d}
	}
	headline, summary := builtin.Summarize(lang, run.Total, run.Failed, incs)
	if headline != "" {
		rt.Headline = headline
	}
	rt.Summary, rt.Provider = a.redactor().Text(summary), ProviderBuiltin
	return nil
}

// builtinPromptSection gives the LLM the conclusion of the local engine (already redacted): the
// model confirms or corrects it with the evidence, it does not start from nothing.
func builtinPromptSection(d builtin.Diagnosis) string {
	if d.Rule == "" {
		return ""
	}
	return fmt.Sprintf("\n\nDeterministic pre-diagnosis by TraceReports' local rules engine (rule %s, category %s). It was built\n"+
		"only from the evidence above; confirm it or correct it with that evidence:\n%s %s", d.Rule, d.Category, d.Summary, d.Suggestion)
}
