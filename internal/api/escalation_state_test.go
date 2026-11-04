package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

// El resumen sin IA de una ejecución sin fallos dice su estado real: en curso, incompleta, todo
// omitido o con advertencias no se describen como terminadas con éxito. Los contadores son los
// reales y una ejecución completa y en verde sigue siendo un éxito.
func TestEscalationTemplateFollowsTheRunState(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	s := srv.Store
	newRun := func(name string, statuses ...string) int64 {
		run, _ := s.CreateRun(name, "qa")
		for i, st := range statuses {
			id, _ := s.CreateTest(run, name+" "+string(rune('a'+i)), "", "")
			if st != "RUNNING" {
				s.FinishTest(id, st, "", "")
			}
		}
		return run
	}
	open := newRun("open", "PASS", "RUNNING")
	openDone := newRun("open-done", "PASS")
	incomplete := newRun("incomplete", "PASS")
	s.FinishRunWith(incomplete, true)
	warning := newRun("warning", "PASS", "WARNING")
	s.FinishRun(warning)
	skipped := newRun("skipped", "SKIP", "SKIP")
	s.FinishRun(skipped)
	green := newRun("green", "PASS", "SKIP")
	s.FinishRun(green)
	clean := newRun("clean", "PASS")
	s.FinishRun(clean)
	for _, c := range []struct {
		run                    int64
		lang, title, what, sev string
		evidence               string
	}{
		{open, "en", "open: run in progress", "has not finished", "low", "1 passed, 0 failed, 0 skipped"},
		{openDone, "en", "open-done: run in progress", "has not finished", "low", "1 passed, 0 failed, 0 skipped"},
		{incomplete, "en", "incomplete: incomplete run", "interrupted", "medium", "1 passed, 0 failed, 0 skipped"},
		{skipped, "en", "skipped: every test was skipped", "nothing was tested", "medium", "0 passed, 0 failed, 2 skipped"},
		// sin fallos, pero con una advertencia o un test omitido: el veredicto de QA lo dice
		{warning, "en", "warning: passed with observations", "No test failed", "low", "ended with a warning"},
		{green, "en", "green: passed with observations", "No test failed", "low", "1 of 2 tests was skipped"},
		{clean, "en", "clean: all tests passed", "every tested flow worked", "low", "1 passed, 0 failed, 0 skipped"},
		{open, "es", "open: en curso", "todavía no terminó", "low", "1 pasaron, 0 fallaron, 0 omitidos"},
	} {
		body, _ := json.Marshal(map[string]any{"run_id": c.run, "audience": "qa", "lang": c.lang, "no_ai": true})
		rec := call(t, srv, "POST", "/api/v1/ui/escalate", string(body))
		var e ai.Escalation
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &e) != nil {
			t.Fatalf("escalate: %d %s", rec.Code, rec.Body)
		}
		all := e.Title + "|" + e.Headline + "|" + e.WhatHappened + "|" + e.Impact + "|" + strings.Join(e.Evidence, "|")
		if e.Title != c.title || !strings.Contains(e.WhatHappened, c.what) || e.Severity != c.sev || !strings.Contains(all, c.evidence) {
			t.Errorf("%s/%s: %s (severity %s)", c.title, c.lang, all, e.Severity)
		}
		if c.run != clean && (strings.Contains(all, "support approving") || strings.Contains(all, "|All ") || strings.Contains(all, "The only test") || strings.Contains(all, "all tests passed")) {
			t.Errorf("%s: described as a success: %s", c.title, all)
		}
	}
}
