package ai

import (
	"fmt"
	"strings"
)

// textos del resumen sin IA, en los dos idiomas de la interfaz
var tpl = map[string]map[string]string{
	"es": {
		"run_title":      "%s: %d de %d tests fallaron",
		"run_ok":         "%s: todos los tests pasaron",
		"test_title":     "Falló \"%s\"",
		"headline_run":   "%d de %d tests de la ejecución \"%s\" fallaron en %s.",
		"headline_test":  "El test \"%s\" falló en %s.",
		"what_backend":   "El sistema no pudo completar la operación porque un servicio del backend respondió con error o no respondió.",
		"what_locator":   "La pantalla cambió y la automatización ya no encuentra un elemento: es mantenimiento del test, no un error del producto.",
		"what_logic":     "La aplicación respondió, pero con un resultado distinto del esperado.",
		"what_infra":     "Falló el entorno de pruebas (navegador, red o datos de prueba), no necesariamente la aplicación.",
		"what_unknown":   "El test no pudo completar su flujo.",
		"impact_biz":     "Mientras no se resuelva, los usuarios podrían no completar \"%s\".",
		"impact_qa":      "%d de %d tests de la suite fallaron; los resultados de esta ejecución no son confiables para aprobar la versión.",
		"impact_maint":   "No afecta a los usuarios: la automatización necesita un ajuste para seguir validando este flujo.",
		"ev_run":         "Ejecución \"%s\" (%s): %d pasaron, %d fallaron, %d omitidos.",
		"ev_error":       "Error: %s",
		"ev_net":         "Backend: %s %s → %s (%d ms).",
		"ev_shot":        "Captura del momento del fallo: %s.",
		"ev_flaky":       "Historial: el test es inestable, falló %s de sus últimas ejecuciones.",
		"cause_unknown":  "Causa por confirmar con la evidencia del reporte.",
		"step_report":    "Revisar el reporte completo: pasos, capturas y llamadas al backend.",
		"step_backend":   "Revisar los logs y la disponibilidad del servicio %s.",
		"step_locator":   "Actualizar el selector del test%s.",
		"step_bug":       "Abrir un bug con esta evidencia para el equipo de desarrollo.",
		"step_rerun":     "Volver a ejecutar la suite cuando el entorno esté estable.",
		"owner_backend":  "Equipo de backend",
		"owner_qa":       "QA automatización",
		"owner_dev":      "Equipo de desarrollo",
		"owner_infra":    "Infraestructura / DevOps",
		"what_incidents": "%d tests fallaron por %d problemas distintos. El más grande afecta a %d tests: %s.",
		"what_incident1": "Los %d tests fallaron con la misma evidencia: %s.",
		"what_distinct":  "%d tests fallaron, cada uno por un problema distinto. El primero: %s.",
		"ev_incident":    "%s (%d tests).",
		"ev_incident1":   "%s (1 test).",
		"step_incident":  "Empezar por: %s.",
		"ok_headline":    "Los %d tests de la ejecución \"%s\" pasaron en %s.",
		"ok_headline_1":  "El único test de la ejecución \"%s\" pasó en %s.",
		"ok_what":        "La ejecución terminó sin fallos: todos los flujos probados funcionaron como se esperaba.",
		"ok_what_warn":   "La ejecución terminó sin fallos, pero %d tests terminaron con advertencias que conviene revisar.",
		"ok_impact_biz":  "Sin impacto para los usuarios: los flujos probados de \"%s\" funcionan.",
		"ok_impact_qa":   "%d de %d tests pasaron (%d omitidos): los resultados permiten aprobar la versión en este ambiente.",
		"ok_cause":       "No hay fallos que analizar.",
		"ok_step":        "No se requiere acción: la versión puede avanzar con esta evidencia.",
		"ok_step_warn":   "Revisar las advertencias antes de aprobar.",
		"ok_step_skip":   "Confirmar que los %d tests omitidos se omitieron a propósito.",
		"owner_none":     "Sin responsable: no hay acciones pendientes",
	},
	"en": {
		"run_title":      "%s: %d of %d tests failed",
		"run_ok":         "%s: all tests passed",
		"test_title":     "\"%s\" failed",
		"headline_run":   "%d of %d tests of the run \"%s\" failed in %s.",
		"headline_test":  "The test \"%s\" failed in %s.",
		"what_backend":   "The system could not complete the operation because a backend service returned an error or did not answer.",
		"what_locator":   "The screen changed and the automation no longer finds an element: it is test maintenance, not a product bug.",
		"what_logic":     "The application answered, but with a different result than expected.",
		"what_infra":     "The test environment failed (browser, network or test data), not necessarily the application.",
		"what_unknown":   "The test could not complete its flow.",
		"impact_biz":     "Until it is fixed, users might not be able to complete \"%s\".",
		"impact_qa":      "%d of %d tests of the suite failed; this run's results cannot be used to approve the release.",
		"impact_maint":   "Users are not affected: the automation needs an update to keep checking this flow.",
		"ev_run":         "Run \"%s\" (%s): %d passed, %d failed, %d skipped.",
		"ev_error":       "Error: %s",
		"ev_net":         "Backend: %s %s → %s (%d ms).",
		"ev_shot":        "Screenshot at the moment of failure: %s.",
		"ev_flaky":       "History: the test is flaky, it failed %s of its recent runs.",
		"cause_unknown":  "Cause to be confirmed with the report's evidence.",
		"step_report":    "Review the full report: steps, screenshots and backend calls.",
		"step_backend":   "Check the logs and availability of the %s service.",
		"step_locator":   "Update the test selector%s.",
		"step_bug":       "Open a bug with this evidence for the development team.",
		"step_rerun":     "Re-run the suite once the environment is stable.",
		"owner_backend":  "Backend team",
		"owner_qa":       "QA automation",
		"owner_dev":      "Development team",
		"owner_infra":    "Infrastructure / DevOps",
		"what_incidents": "%d tests failed for %d different problems. The largest one affects %d tests: %s.",
		"what_incident1": "The %d tests failed with the same evidence: %s.",
		"what_distinct":  "%d tests failed, each one for a different problem. The first one: %s.",
		"ev_incident":    "%s (%d tests).",
		"ev_incident1":   "%s (1 test).",
		"step_incident":  "Start with: %s.",
		"ok_headline":    "All %d tests of the run \"%s\" passed in %s.",
		"ok_headline_1":  "The only test of the run \"%s\" passed in %s.",
		"ok_what":        "The run finished without failures: every tested flow worked as expected.",
		"ok_what_warn":   "The run finished without failures, but %d tests ended with warnings worth reviewing.",
		"ok_impact_biz":  "No impact for users: the tested flows of \"%s\" work.",
		"ok_impact_qa":   "%d of %d tests passed (%d skipped): the results support approving the release in this environment.",
		"ok_cause":       "There are no failures to analyze.",
		"ok_step":        "No action needed: the release can move forward with this evidence.",
		"ok_step_warn":   "Review the warnings before approving.",
		"ok_step_skip":   "Confirm that the %d skipped tests were skipped on purpose.",
		"owner_none":     "No owner: nothing pending",
	},
}

// templateSuccess fills the summary of a run without failures: low severity (green), no impact
// and no actions, instead of the generic "could not complete its flow" of an unknown cause.
func templateSuccess(e *Escalation, t map[string]string, env string) {
	f := e.Facts
	warnings := f.Total - f.Passed - f.Failed - f.Skipped
	if warnings < 0 {
		warnings = 0
	}
	if n := f.Total - f.Skipped; n == 1 {
		e.Headline = fmt.Sprintf(t["ok_headline_1"], f.RunName, env)
	} else {
		e.Headline = fmt.Sprintf(t["ok_headline"], n, f.RunName, env)
	}
	e.Severity, e.Owner = "low", t["owner_none"]
	e.WhatHappened = t["ok_what"]
	if warnings > 0 {
		e.WhatHappened = fmt.Sprintf(t["ok_what_warn"], warnings)
	}
	if e.Audience == "business" {
		e.Impact = fmt.Sprintf(t["ok_impact_biz"], f.RunName)
	} else {
		e.Impact = fmt.Sprintf(t["ok_impact_qa"], f.Passed, f.Total, f.Skipped)
	}
	e.Evidence = []string{fmt.Sprintf(t["ev_run"], f.RunName, env, f.Passed, f.Failed, f.Skipped)}
	e.RootCause = t["ok_cause"]
	e.NextSteps = []string{t["ok_step"]}
	if warnings > 0 {
		e.NextSteps = []string{t["ok_step_warn"]}
	}
	if f.Skipped > 0 {
		e.NextSteps = append(e.NextSteps, fmt.Sprintf(t["ok_step_skip"], f.Skipped))
	}
	e.Source = "template"
	e.Title = strings.TrimSpace(e.Title)
}

// templateEscalation fills e from its facts without AI.
func templateEscalation(e *Escalation) {
	t := tpl[e.Lang]
	if t == nil {
		t = tpl["es"]
	}
	f := e.Facts
	env := f.Env
	if env == "" {
		env = "—"
	}
	cat := f.Category
	if e.TestID == 0 {
		for _, ft := range f.FailedTests { // la causa dominante de la ejecución
			if ft.Category != "" {
				cat = ft.Category
				break
			}
		}
	}
	if len(f.Network) > 0 && cat == "" {
		cat = "BACKEND_TIMEOUT"
	}

	if e.TestID != 0 {
		e.Title = fmt.Sprintf(t["test_title"], clip(f.TestName, 60))
		e.Headline = fmt.Sprintf(t["headline_test"], f.TestName, env)
	} else if f.Failed > 0 {
		e.Title = fmt.Sprintf(t["run_title"], clip(f.RunName, 40), f.Failed, f.Total)
		e.Headline = fmt.Sprintf(t["headline_run"], f.Failed, f.Total, f.RunName, env)
	} else {
		e.Title = fmt.Sprintf(t["run_ok"], clip(f.RunName, 50))
		e.Headline = e.Title
	}
	if e.TestID == 0 && f.RunHeadline != "" {
		e.Headline = f.RunHeadline
	}
	if e.TestID == 0 && f.Failed == 0 && f.Total > 0 {
		templateSuccess(e, t, env)
		return
	}

	switch cat {
	case "BACKEND_TIMEOUT":
		e.WhatHappened, e.Severity, e.Owner = t["what_backend"], "high", t["owner_backend"]
	case "LOCATOR_CHANGED":
		e.WhatHappened, e.Severity, e.Owner = t["what_locator"], "low", t["owner_qa"]
	case "LOGIC_BUG":
		e.WhatHappened, e.Severity, e.Owner = t["what_logic"], "high", t["owner_dev"]
	case "INFRA_ERROR":
		e.WhatHappened, e.Severity, e.Owner = t["what_infra"], "medium", t["owner_infra"]
	default:
		e.WhatHappened, e.Severity, e.Owner = t["what_unknown"], "medium", t["owner_qa"]
	}
	if e.TestID == 0 && f.Total > 0 && f.Failed*2 >= f.Total && cat != "LOCATOR_CHANGED" {
		e.Severity = "critical"
	}
	if e.Audience == "dev" && f.AISummary != "" {
		e.WhatHappened = f.AISummary
	}
	// ejecución completa sin causa clara: los incidentes agrupados dicen más que "no pudo completar su flujo"
	incidents := e.TestID == 0 && cat == "" && len(f.Incidents) > 0
	if incidents {
		top := f.Incidents[0]
		switch {
		case len(f.Incidents) == 1:
			e.WhatHappened = fmt.Sprintf(t["what_incident1"], f.Failed, clip(top.Title, 140))
		case len(top.TestIDs) == 1:
			e.WhatHappened = fmt.Sprintf(t["what_distinct"], f.Failed, clip(top.Title, 140))
		default:
			e.WhatHappened = fmt.Sprintf(t["what_incidents"], f.Failed, len(f.Incidents), len(top.TestIDs), clip(top.Title, 140))
		}
		if top.Kind == "backend" {
			e.Owner = t["owner_backend"]
		}
	}

	flow := f.TestName
	if flow == "" {
		flow = f.RunName
	}
	switch {
	case cat == "LOCATOR_CHANGED":
		e.Impact = t["impact_maint"]
	case e.Audience == "business":
		e.Impact = fmt.Sprintf(t["impact_biz"], flow)
	default:
		e.Impact = fmt.Sprintf(t["impact_qa"], f.Failed, f.Total)
	}

	e.Evidence = []string{fmt.Sprintf(t["ev_run"], f.RunName, env, f.Passed, f.Failed, f.Skipped)}
	if e.Audience != "business" && f.Error != "" {
		e.Evidence = append(e.Evidence, fmt.Sprintf(t["ev_error"], firstLine(f.Error)))
	}
	if e.Audience != "business" {
		for i, n := range f.Network {
			if i == 2 {
				break
			}
			e.Evidence = append(e.Evidence, fmt.Sprintf(t["ev_net"], n.Method, n.Path, n.Outcome, n.DurationMs))
		}
	}
	if f.Screenshot != "" && f.ShotCaption != "" {
		e.Evidence = append(e.Evidence, fmt.Sprintf(t["ev_shot"], f.ShotCaption))
	}
	if f.Flaky != "" {
		e.Evidence = append(e.Evidence, fmt.Sprintf(t["ev_flaky"], f.Flaky))
	}
	if incidents && e.Audience != "business" {
		for i, in := range f.Incidents {
			if i == 3 {
				break
			}
			if len(in.TestIDs) == 1 {
				e.Evidence = append(e.Evidence, fmt.Sprintf(t["ev_incident1"], clip(in.Title, 140)))
			} else {
				e.Evidence = append(e.Evidence, fmt.Sprintf(t["ev_incident"], clip(in.Title, 140), len(in.TestIDs)))
			}
		}
	}

	e.RootCause = t["cause_unknown"]
	if f.AISummary != "" {
		e.RootCause = f.AISummary
	} else if len(f.Incidents) > 0 && f.Incidents[0].Cause != "" {
		e.RootCause = f.Incidents[0].Cause
	}

	e.NextSteps = nil
	switch cat {
	case "BACKEND_TIMEOUT":
		svc := "backend"
		if len(f.Network) > 0 {
			svc = f.Network[0].Path
		}
		e.NextSteps = append(e.NextSteps, fmt.Sprintf(t["step_backend"], svc), t["step_rerun"])
	case "LOCATOR_CHANGED":
		hint := ""
		if f.LocatorPick != "" {
			hint = ": " + f.LocatorPick
		}
		e.NextSteps = append(e.NextSteps, fmt.Sprintf(t["step_locator"], hint))
	case "LOGIC_BUG":
		e.NextSteps = append(e.NextSteps, t["step_bug"])
	case "INFRA_ERROR":
		e.NextSteps = append(e.NextSteps, t["step_rerun"])
	}
	if incidents {
		e.NextSteps = append(e.NextSteps, fmt.Sprintf(t["step_incident"], clip(f.Incidents[0].Title, 140)))
	}
	if f.AISuggest != "" && e.Audience != "business" {
		e.NextSteps = append([]string{f.AISuggest}, e.NextSteps...)
	}
	e.NextSteps = append(e.NextSteps, t["step_report"])
	e.Source = "template"
	e.Title = strings.TrimSpace(e.Title)
}
