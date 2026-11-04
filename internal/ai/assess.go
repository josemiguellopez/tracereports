package ai

import (
	"fmt"
	"sort"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// El veredicto de QA de una ejecución sin fallos: que todo pase no basta para firmarla. Con criterio
// de QA Senior se revisa lo que entregaron sus tests (red, consola, pasos, reintentos, historial,
// duraciones) y se dice si pasó limpio, con observaciones o con riesgos. Lo calculan estas reglas,
// no la IA: la IA solo lo explica, así que no puede inventar ni callar una señal.

// Verdicts of a run without failures.
const (
	VerdictClean        = "clean"        // nada que objetar
	VerdictObservations = "observations" // señales que no rompen el flujo, pero conviene saber
	VerdictRisks        = "risks"        // señales que un QA Senior no deja pasar aunque el test esté verde
)

// Signal levels.
const (
	SignalRisk = "risk"
	SignalNote = "note"
)

// Signal kinds.
const (
	KindRetried     = "retried"      // pasó solo al reintentar
	KindFlaky       = "flaky"        // su historial alterna entre pasar y fallar
	KindServerError = "server_error" // el backend respondió 5xx y el test no lo detectó
	KindPageError   = "page_error"   // excepción de JavaScript no capturada en la página
	KindClientError = "client_error" // llamadas con 4xx
	KindAborted     = "aborted"      // llamadas sin respuesta (canceladas, abortadas)
	KindConsole     = "console"      // console.error
	KindWarning     = "warning"      // el test terminó con advertencia
	KindWarnSteps   = "warn_steps"   // pasos con advertencia en un test que pasó
	KindSkipped     = "skipped"      // tests omitidos
	KindSlow        = "slow"         // mucho más lento que su historial
)

// Signal is one thing worth knowing about a run that passed: its kind, how many, an example and
// (slow, flaky, skipped) a second value. Text is built per language from them (signalText).
type Signal struct {
	Level   string `json:"level"` // risk | note
	Kind    string `json:"kind"`
	Test    string `json:"test,omitempty"`
	TestID  int64  `json:"test_id,omitempty"`
	N       int    `json:"n,omitempty"`
	Example string `json:"example,omitempty"`
	Extra   string `json:"extra,omitempty"`
	Text    string `json:"text"` // en el idioma de la escalación (Localize)
}

// Assessment is the QA verdict of a run without failures, with its signals (risks first).
type Assessment struct {
	Verdict string   `json:"verdict"`
	Signals []Signal `json:"signals"`
}

// Count is how many signals have that level.
func (a *Assessment) Count(level string) int {
	n := 0
	for _, s := range a.Signals {
		if s.Level == level {
			n++
		}
	}
	return n
}

// maxSignals bounds what the card and the prompt carry.
const maxSignals = 12

// slowFactor: a test this many times slower than the median of its history is flagged (and at
// least slowMinMs slower, so milliseconds of noise are not).
const (
	slowFactor = 2
	slowMinMs  = 5000
)

// assessGreen reviews every test of a run without failures with the criteria of a senior QA.
func assessGreen(store *db.Store, run *db.RunDetail) (*Assessment, error) {
	a := &Assessment{Signals: []Signal{}}
	add := func(level, kind string, t db.Test, n int, example, extra string) {
		a.Signals = append(a.Signals, Signal{Level: level, Kind: kind, Test: t.Name, TestID: t.ID, N: n, Example: example, Extra: extra})
	}
	skipped := 0
	for _, t := range run.Tests {
		switch t.Status {
		case "SKIP":
			skipped++
			continue
		case "WARNING":
			add(SignalNote, KindWarning, t, 0, "", "")
		}
		if t.Attempts > 1 {
			add(SignalRisk, KindRetried, t, t.Attempts, "", "")
		}
		// inestable por historial: solo si de verdad falló antes, con historial suficiente (un reintento
		// de hoy ya es su propia señal)
		if fi := t.FlakyInfo; t.Flaky && fi != nil && fi.Fails > 0 && !fi.LowData {
			add(SignalRisk, KindFlaky, t, fi.Fails, "", fmt.Sprint(fi.Runs))
		}
		full, err := store.GetTest(t.ID)
		if err != nil {
			return nil, err
		}
		warnSteps := 0
		for _, l := range full.Logs {
			if l.Status == "WARNING" {
				warnSteps++
			}
		}
		if t.Status != "WARNING" && warnSteps > 0 {
			add(SignalNote, KindWarnSteps, t, warnSteps, "", "")
		}
		if t.NetworkErrors > 0 {
			conns, err := store.ListNetworkErrors(t.ID, 20)
			if err != nil {
				return nil, err
			}
			byKind := map[string][]string{}
			for _, c := range conns {
				call := fmt.Sprintf("%s %s", c.Method, clip(c.URL, 100))
				switch {
				case c.Failed || c.Status == 0:
					byKind[KindAborted] = append(byKind[KindAborted], fmt.Sprintf("%s (%s)", call, c.ErrorText))
				case c.Status >= 500:
					byKind[KindServerError] = append(byKind[KindServerError], fmt.Sprintf("%s -> HTTP %d", call, c.Status))
				default:
					byKind[KindClientError] = append(byKind[KindClientError], fmt.Sprintf("%s -> HTTP %d", call, c.Status))
				}
			}
			if calls := byKind[KindServerError]; len(calls) > 0 {
				add(SignalRisk, KindServerError, t, len(calls), examples(calls), "")
			}
			if calls := byKind[KindClientError]; len(calls) > 0 {
				add(SignalNote, KindClientError, t, len(calls), examples(calls), "")
			}
			if calls := byKind[KindAborted]; len(calls) > 0 {
				add(SignalNote, KindAborted, t, len(calls), examples(calls), "")
			}
		}
		pageErrors, consoleErrors := 0, 0
		var firstPage, firstConsole string
		for _, c := range full.Console {
			switch c.Level {
			case "pageerror":
				if pageErrors == 0 {
					firstPage = clip(c.Text, 140)
				}
				pageErrors++
			case "error":
				if consoleErrors == 0 {
					firstConsole = clip(c.Text, 140)
				}
				consoleErrors++
			}
		}
		if pageErrors > 0 {
			add(SignalRisk, KindPageError, t, pageErrors, firstPage, "")
		}
		if consoleErrors > 0 {
			add(SignalNote, KindConsole, t, consoleErrors, firstConsole, "")
		}
		if t.EndedAt != nil && t.Key != "" {
			if med, ok := medianDuration(store, t.Key, run.ID); ok {
				if d := *t.EndedAt - t.StartedAt; d > med*slowFactor && d-med >= slowMinMs {
					add(SignalNote, KindSlow, t, 0, fmtMs(d), fmtMs(med))
				}
			}
		}
	}
	if skipped > 0 {
		a.Signals = append(a.Signals, Signal{Level: SignalNote, Kind: KindSkipped, N: skipped, Extra: fmt.Sprint(len(run.Tests))})
	}
	sort.SliceStable(a.Signals, func(i, j int) bool { return a.Signals[i].Level == SignalRisk && a.Signals[j].Level != SignalRisk })
	if len(a.Signals) > maxSignals {
		a.Signals = a.Signals[:maxSignals]
	}
	switch {
	case a.Count(SignalRisk) > 0:
		a.Verdict = VerdictRisks
	case len(a.Signals) > 0:
		a.Verdict = VerdictObservations
	default:
		a.Verdict = VerdictClean
	}
	return a, nil
}

// signalTexts: each kind as a sentence, with N, Example and Extra in that order where they appear.
var signalTexts = map[string]map[string]string{
	"es": {
		KindRetried:     "pasó solo al reintentar (%[1]d intentos): test inestable o defecto intermitente",
		KindFlaky:       "inestable en su historial: no pasó en %[1]d de sus últimas %[3]s ejecuciones",
		KindServerError: "el backend respondió con error de servidor %[1]d veces y el test no lo detectó (¿falta una aserción?): %[2]s",
		KindPageError:   "%[1]d excepciones de JavaScript no capturadas en la página (p. ej. %[2]s)",
		KindClientError: "%[1]d llamadas respondieron con error 4xx sin hacerlo fallar: %[2]s",
		KindAborted:     "%[1]d llamadas sin respuesta (abortadas o canceladas, habitual al navegar): %[2]s",
		KindConsole:     "%[1]d errores en la consola del navegador (p. ej. %[2]s)",
		KindWarning:     "terminó con advertencia: corrió, pero no pasó limpio",
		KindWarnSteps:   "%[1]d pasos con advertencia, aunque el test pasó",
		KindSkipped:     "%[1]d de %[3]s tests se omitieron: lo que cubren no se validó",
		KindSlow:        "tardó %[2]s, cuando lo habitual es %[3]s",
	},
	"en": {
		KindRetried:     "passed only after retrying (%[1]d attempts): an unstable test or an intermittent defect",
		KindFlaky:       "unstable in its history: did not pass in %[1]d of its last %[3]s runs",
		KindServerError: "the backend answered with a server error %[1]d times and the test did not catch it (a missing assertion?): %[2]s",
		KindPageError:   "%[1]d uncaught JavaScript exceptions in the page (e.g. %[2]s)",
		KindClientError: "%[1]d calls answered with a 4xx error without making it fail: %[2]s",
		KindAborted:     "%[1]d calls got no response (aborted or cancelled, common when the page navigates): %[2]s",
		KindConsole:     "%[1]d browser console errors (e.g. %[2]s)",
		KindWarning:     "ended with a warning: it ran, but did not pass cleanly",
		KindWarnSteps:   "%[1]d steps logged a warning, although the test passed",
		KindSkipped:     "%[1]d of %[3]s tests were skipped: what they cover was not validated",
		KindSlow:        "took %[2]s, against a usual %[3]s",
	},
}

// signalTextsOne: the kinds that count something, when it is just one.
var signalTextsOne = map[string]map[string]string{
	"es": {
		KindServerError: "el backend respondió con error de servidor 1 vez y el test no lo detectó (¿falta una aserción?): %[2]s",
		KindPageError:   "1 excepción de JavaScript no capturada en la página (%[2]s)",
		KindClientError: "1 llamada respondió con error 4xx sin hacerlo fallar: %[2]s",
		KindAborted:     "1 llamada sin respuesta (abortada o cancelada, habitual al navegar): %[2]s",
		KindConsole:     "1 error en la consola del navegador (%[2]s)",
		KindWarnSteps:   "1 paso con advertencia, aunque el test pasó",
		KindSkipped:     "1 de %[3]s tests se omitió: lo que cubre no se validó",
		KindFlaky:       "inestable en su historial: no pasó en 1 de sus últimas %[3]s ejecuciones",
	},
	"en": {
		KindServerError: "the backend answered with a server error once and the test did not catch it (a missing assertion?): %[2]s",
		KindPageError:   "1 uncaught JavaScript exception in the page (%[2]s)",
		KindClientError: "1 call answered with a 4xx error without making it fail: %[2]s",
		KindAborted:     "1 call got no response (aborted or cancelled, common when the page navigates): %[2]s",
		KindConsole:     "1 browser console error (%[2]s)",
		KindWarnSteps:   "1 step logged a warning, although the test passed",
		KindSkipped:     "1 of %[3]s tests was skipped: what it covers was not validated",
		KindFlaky:       "unstable in its history: did not pass in 1 of its last %[3]s runs",
	},
}

// signalSteps: what a senior QA would do about each kind of signal.
var signalSteps = map[string]map[string]string{
	"es": {
		KindRetried:     "Estabilizar los tests que pasaron al reintentar antes de confiar en su verde (esperas explícitas, datos aislados).",
		KindFlaky:       "Investigar los tests inestables: su verde de hoy no garantiza el de mañana.",
		KindServerError: "Agregar aserciones sobre las respuestas del backend y revisar los errores de servidor con el equipo de backend.",
		KindPageError:   "Revisar las excepciones de JavaScript con el equipo de frontend.",
		KindClientError: "Confirmar si las respuestas 4xx son esperadas o esconden un problema.",
		KindAborted:     "Confirmar que las llamadas sin respuesta no eran recursos necesarios para el flujo.",
		KindConsole:     "Revisar los errores de la consola del navegador.",
		KindWarning:     "Revisar las advertencias antes de aprobar.",
		KindWarnSteps:   "Revisar las advertencias antes de aprobar.",
		KindSkipped:     "Confirmar que los tests omitidos se omitieron a propósito.",
		KindSlow:        "Revisar por qué los tests lentos tardaron más que de costumbre.",
	},
	"en": {
		KindRetried:     "Stabilize the tests that passed on retry before trusting their green (explicit waits, isolated data).",
		KindFlaky:       "Look into the unstable tests: today's green does not guarantee tomorrow's.",
		KindServerError: "Add assertions on the backend responses and review the server errors with the backend team.",
		KindPageError:   "Review the JavaScript exceptions with the frontend team.",
		KindClientError: "Confirm whether the 4xx responses are expected or hide a problem.",
		KindAborted:     "Confirm that the calls without a response were not resources the flow needs.",
		KindConsole:     "Review the browser console errors.",
		KindWarning:     "Review the warnings before approving.",
		KindWarnSteps:   "Review the warnings before approving.",
		KindSkipped:     "Confirm that the skipped tests were skipped on purpose.",
		KindSlow:        "Find out why the slow tests took longer than usual.",
	},
}

// verdictLabels: the verdict as the title says it, per language.
var verdictLabels = map[string]map[string]string{
	"es": {VerdictClean: "pasó limpio", VerdictObservations: "pasó con observaciones", VerdictRisks: "pasó con riesgos"},
	"en": {VerdictClean: "passed clean", VerdictObservations: "passed with observations", VerdictRisks: "passed with risks"},
}

func verdictLabel(verdict, lang string) string {
	if l := verdictLabels[lang]; l != nil {
		return l[verdict]
	}
	return verdictLabels["es"][verdict]
}

// Localize fills the text of each signal in lang ("es" by default).
func (a *Assessment) Localize(lang string) {
	for i := range a.Signals {
		a.Signals[i].Text = signalText(a.Signals[i], lang)
	}
}

func signalText(s Signal, lang string) string {
	if signalTexts[lang] == nil {
		lang = "es"
	}
	format := signalTexts[lang][s.Kind]
	if one := signalTextsOne[lang][s.Kind]; s.N == 1 && one != "" {
		format = one
	}
	text := fmt.Sprintf(format, s.N, s.Example, s.Extra)
	if i := strings.Index(text, "%!(EXTRA"); i >= 0 { // los textos sin todos los valores
		text = text[:i]
	}
	if s.Test != "" {
		text = s.Test + ": " + text
	}
	return text
}

// steps is what to do about the signals, without repeating one (risks first).
func (a *Assessment) steps(lang string) []string {
	steps := signalSteps[lang]
	if steps == nil {
		steps = signalSteps["es"]
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range a.Signals {
		if st := steps[s.Kind]; st != "" && !seen[st] {
			seen[st] = true
			out = append(out, st)
		}
	}
	return out
}

// medianDuration is the median duration of the test in its earlier finished runs (at least 3).
func medianDuration(store *db.Store, key string, runID int64) (int64, bool) {
	hist, err := store.TestHistory(key, runID, 11)
	if err != nil {
		return 0, false
	}
	var ds []int64
	for _, h := range hist {
		if h.RunID != runID && h.DurationMs != nil && h.Status != "SKIP" {
			ds = append(ds, *h.DurationMs)
		}
	}
	if len(ds) < 3 {
		return 0, false
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[len(ds)/2], true
}

// examples is the first calls of a list, for a signal.
func examples(calls []string) string {
	if len(calls) > 2 {
		return strings.Join(calls[:2], "; ") + fmt.Sprintf(" (+%d)", len(calls)-2)
	}
	return strings.Join(calls, "; ")
}

func fmtMs(ms int64) string {
	if ms < 60000 {
		return fmt.Sprintf("%.1f s", float64(ms)/1000)
	}
	return fmt.Sprintf("%d min %d s", ms/60000, ms%60000/1000)
}
