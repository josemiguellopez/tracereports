package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai/builtin"
	"github.com/josemiguellopez/tracereports/internal/atrest"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/locator"
)

// El caso del reporte (run 1784, test 5148): el test guardó la inmunización y buscó la fila en la lista,
// pero la lista seguía cargando (cinco SVG de react-content-loader). El error dice "esperando elemento"
// y un XPath, y eso se clasificaba como LOCATOR_CHANGED ("cambió la interfaz"). Las señales de carga del
// DOM del fallo lo convierten en TIMING: el test no esperó.

const loadingXPath = `//div[contains(@class,'textoverflow') and contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), 'consultorio pudahuel oriente. lote: lote_001')]`

var loadingErr = `Errores acumulados para el file_name:test_agregar_item_inmunizaciones.py: ["Error Linea 3739 esperando elemento después de 6 intentos: ` + loadingXPath + `"]`

// skeletonPage: the panel of the failure, as the server stores it (an old snapshot: the skeleton SVGs
// already cut down to a black rectangle, no animation left).
func skeletonPage(anim bool) locator.Snapshot {
	nodes := []locator.Node{
		{I: 0, P: -1, Tag: "body", Visible: true, W: 1382, H: 928},
		{I: 1, P: 0, Tag: "div", Attrs: map[string]string{"class": "card"}, Visible: true, X: 8, Y: 98, W: 545, H: 788},
		{I: 2, P: 1, Tag: "h5", Text: "Inmunizaciones", Visible: true, X: 81, Y: 111, W: 411, H: 24},
		{I: 3, P: 1, Tag: "div", Attrs: map[string]string{"class": "mt-1 col-sm-12"}, Visible: true, X: 66, Y: 153, W: 497, H: 731},
		{I: 4, P: 0, Tag: "button", Attrs: map[string]string{"id": "plus-button"}, Visible: true, X: 492, Y: 107, W: 40, H: 33,
			SVG: `<svg viewBox="0 0 16 16"><path d="M8 0v16M0 8h16"/></svg>`},
	}
	for k := 0; k < 5; k++ {
		nodes = append(nodes, locator.Node{I: 5 + k, P: 3, Tag: "svg", Attrs: map[string]string{"role": "img"}, Visible: true, X: 81, Y: 153 + 111*k, W: 467, H: 111,
			SVG: `<svg viewBox="0 0 400 95" fill="rgb(0, 0, 0)"><rect x="0" y="0" width="400" height="95"></rect></svg>`, Anim: anim})
	}
	return locator.Snapshot{URL: "https://app/ficha", Viewport: locator.Size{W: 1382, H: 928}, Nodes: nodes}
}

func saveDOM(t *testing.T, store *db.Store, id int64, snap locator.Snapshot) {
	t.Helper()
	raw, _ := json.Marshal(snap)
	if err := store.SaveDOM(id, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestDOMLoadingSignals(t *testing.T) {
	if got := domLoading(skeletonPage(false), "es"); len(got) != 1 || !strings.Contains(got[0], "5 bloques de carga (skeleton)") {
		t.Fatalf("skeleton of an old snapshot (rectangles only): %q", got)
	}
	if got := domLoading(skeletonPage(true), "en"); len(got) != 1 || !strings.Contains(got[0], "5 loading blocks") {
		t.Fatalf("en: %q", got)
	}
	one := skeletonPage(true)
	one.Nodes = one.Nodes[:6] // un solo SVG animado: no es un skeleton de varias filas, sí una animación
	if got := domLoading(one, "es"); len(got) != 1 || !strings.Contains(got[0], "1 animaciones de carga") {
		t.Fatalf("animated: %q", got)
	}
	spin := locator.Snapshot{Nodes: []locator.Node{{I: 0, P: -1, Tag: "body", Visible: true, W: 100, H: 100},
		{I: 1, P: 0, Tag: "div", Attrs: map[string]string{"class": "d-flex spinner-border text-primary"}, Visible: true, W: 32, H: 32},
		{I: 2, P: 0, Tag: "section", Attrs: map[string]string{"id": "agenda", "aria-busy": "true"}, Visible: true, W: 300, H: 300}}}
	got := strings.Join(domLoading(spin, "es"), " | ")
	if !strings.Contains(got, "indicador de carga visible (div.spinner-border)") || !strings.Contains(got, "aria-busy) en section#agenda") {
		t.Fatalf("spinner and aria-busy: %q", got)
	}
	// una página cargada: íconos con path, un spinner oculto, filas con texto
	done := locator.Snapshot{Nodes: []locator.Node{{I: 0, P: -1, Tag: "body", Visible: true, W: 100, H: 100},
		{I: 1, P: 0, Tag: "svg", Visible: true, W: 16, H: 16, SVG: `<svg><path d="M0 0"/></svg>`},
		{I: 2, P: 0, Tag: "svg", Visible: true, W: 16, H: 16, SVG: `<svg><path d="M0 0"/></svg>`},
		{I: 3, P: 0, Tag: "div", Attrs: map[string]string{"class": "spinner-border"}, Visible: false, W: 32, H: 32},
		{I: 4, P: 0, Tag: "div", Attrs: map[string]string{"class": "textoverflow"}, Text: "Consultorio", Visible: true, W: 300, H: 20}}}
	if got := domLoading(done, "es"); len(got) != 0 {
		t.Fatalf("a loaded page has no loading signals: %q", got)
	}
}

func TestPendingCallsAtTheFailure(t *testing.T) {
	d := func(ms int64) *int64 { return &ms }
	conns := []db.NetConn{
		{Method: "GET", URL: "https://app/api/inmunizaciones?paciente=1", Status: 200, StartedAt: 9_000, DurationMs: d(3_000)}, // terminó después del fallo
		{Method: "GET", URL: "https://app/api/menu", Status: 200, StartedAt: 1_000, DurationMs: d(200)},                       // terminó antes
		{Method: "POST", URL: "https://app/api/x", Status: 500, StartedAt: 9_500, DurationMs: d(2_000)},                       // un error: otra regla
	}
	got := pendingCalls(conns, 10_000, "es")
	if !strings.Contains(got, "1 llamadas al backend seguían en curso") || !strings.Contains(got, "GET app/api/inmunizaciones") {
		t.Fatalf("pending: %q", got)
	}
	if pendingCalls(conns[1:2], 10_000, "es") != "" {
		t.Fatal("a call that ended before the failure is not pending")
	}
}

func TestStillLoadingIsTimingNotLocatorChanged(t *testing.T) {
	in := builtin.Input{Lang: "es", Error: loadingErr}
	if d := builtin.Diagnose(in); d.Category != builtin.CategoryLocator {
		t.Fatalf("without signals it stays a locator failure: %+v", d)
	}
	in.Loading = []string{"5 bloques de carga (skeleton) visibles"}
	d := builtin.Diagnose(in)
	if d.Category != builtin.CategoryTiming || d.Rule != "still_loading" || !strings.Contains(d.Summary, "5 bloques de carga") ||
		!strings.Contains(d.Suggestion, "Espera a que termine la carga") || strings.Contains(d.Summary, "cambió la interfaz") {
		t.Fatalf("loading: %+v", d)
	}
	in = builtin.Input{Lang: "en", Error: loadingErr, SelectorBefore: "the same selector found the element the last time the test passed (run #7)"}
	if d := builtin.Diagnose(in); d.Category != builtin.CategoryTiming || !strings.Contains(d.Summary, "run #7") {
		t.Fatalf("selector worked before: %+v", d)
	}
	// una página en blanco sigue siendo de ambiente (no llegó a cargar nada)
	in = builtin.Input{Lang: "es", Error: loadingErr, Loading: []string{"x"}, BlankPage: "Página en blanco"}
	if d := builtin.Diagnose(in); d.Category != builtin.CategoryInfra {
		t.Fatalf("blank page: %+v", d)
	}
}

// La misma búsqueda en el último PASS: con DOM_STEPS el test guardó sus páginas, y el XPath encontraba
// la fila. El selector no cambió.
func TestSelectorFoundInTheLastPass(t *testing.T) {
	clearEnv(t)
	store, run1 := groupingStore(t)
	pass, _ := store.CreateTest(run1, "test_002_agregar_inmunizacion", "", "")
	row := locator.Snapshot{Nodes: []locator.Node{{I: 0, P: -1, Tag: "body", Visible: true},
		{I: 1, P: 0, Tag: "div", Attrs: map[string]string{"class": "textoverflow"}, Visible: true},
		{I: 2, P: 1, Tag: "span", Text: "Consultorio Pudahuel Oriente. Lote: LOTE_001", Visible: true}}}
	raw, _ := json.Marshal(row)
	if _, err := store.SaveDOMStep(pass, "lista", 1, string(raw)); err != nil {
		t.Fatal(err)
	}
	store.FinishTest(pass, "PASS", "", "")
	store.FinishRun(run1)
	run2, _ := store.CreateRun("Regresión", "qa")
	fail := failAt(t, store, run2, "test_002_agregar_inmunizacion", "Captura al fallar", "", 10_000)
	store.FinishTest(fail, "FAIL", loadingErr, "")
	got, _ := store.GetTest(fail)
	if s := selectorBefore(store, got, loadingXPath, "es"); !strings.Contains(s, "el mismo selector encontró el elemento") || !strings.Contains(s, "#") {
		t.Fatalf("found in the last PASS: %q", s)
	}
	if s := selectorBefore(store, got, "//div[@id='otra']", "es"); s != "" {
		t.Fatalf("a selector the last PASS did not have says nothing: %q", s)
	}
}

// El triage del test y el escalamiento QA del caso del reporte, con el motor local.
func TestLoadingFailureTriageAndEscalation(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	ok, _ := store.CreateTest(runID, "test_001_abrir_ficha", "", "")
	store.FinishTest(ok, "PASS", "", "")
	id := failAt(t, store, runID, "test_002_box_agregar_inmunizaciones", "Captura al fallar", "", 10_000)
	store.FinishTest(id, "FAIL", loadingErr, "")
	saveDOM(t, store, id, skeletonPage(false))
	// el diagnóstico guardado antes del arreglo: "cambió el selector"
	store.SetTriagePending(id)
	store.SaveTriage(id, "LOCATOR_CHANGED", "Probablemente el selector del elemento ha cambiado debido a cambios en la interfaz", "Actualizar el selector en el test", "", "")
	store.FinishRun(runID)

	a := New(store)
	tt, _ := store.GetTest(id)
	d, err := a.diagnoseBuiltin(tt)
	if err != nil || d.Category != builtin.CategoryTiming {
		t.Fatalf("triage of the test: %+v %v", d, err)
	}

	f, err := BuildFacts(store, atrest.Dir{}, runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.RunHeadline = "Test fallido: problema con selector de elemento en página" // el diagnóstico de la ejecución que escribió la IA
	for _, lang := range []string{"es", "en"} {
		e := newEscalation(f, runID, 0, "qa", lang)
		templateEscalation(e)
		all := strings.Join(append(append([]string{e.Headline, e.WhatHappened, e.RootCause}, e.Evidence...), e.NextSteps...), " | ")
		if blamesSelector(all) || strings.Contains(all, "Actualizar el selector") {
			t.Errorf("%s: blames the selector: %s", lang, all)
		}
		want := map[string][]string{"es": {"seguía cargando", "5 bloques de carga", "Esperar a que termine la carga"},
			"en": {"still loading", "5 loading blocks", "Wait for the load to finish"}}[lang]
		for _, w := range want {
			if !strings.Contains(all, w) {
				t.Errorf("%s: missing %q in %s", lang, w, all)
			}
		}
		if e.Severity != "low" {
			t.Errorf("%s: severity %s", lang, e.Severity)
		}
	}
}

// Un modelo chico (el del reporte era llama3.1:8b) repite "cambió el selector" aunque el prompt traiga
// las señales: en el triage gana la regla, y en el escalamiento la plantilla.
func TestTheModelDoesNotOverruleTheLoadingSignals(t *testing.T) {
	clearEnv(t)
	triage := `{"category":"LOCATOR_CHANGED","summary":"El selector cambió por un cambio en la interfaz.","suggestion":"Actualiza el selector."}`
	esc := `{"title":"Test fallido","severity":"low","headline":"Test fallido: problema con selector de elemento en página",` +
		`"what_happened":"El test falló debido a un problema con el selector de un elemento.","impact":"No bloquea la regresión",` +
		`"evidence":["Captura al fallar"],"root_cause":"Probablemente el selector del elemento ha cambiado debido a cambios en la interfaz",` +
		`"next_steps":["Revisar la interfaz y el selector del elemento","Actualizar el selector en el test si es necesario"],"owner":"QA Team"}`
	var prompts []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Messages []struct{ Content string } }
		json.NewDecoder(r.Body).Decode(&req)
		content := esc
		if len(req.Messages) > 0 {
			prompts = append(prompts, req.Messages[len(req.Messages)-1].Content)
			if strings.Contains(req.Messages[0].Content, "triaging a failed") {
				content = triage
			}
		}
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer provider.Close()

	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "test_002_box_agregar_inmunizaciones", "Captura al fallar", "", 10_000)
	store.FinishTest(id, "FAIL", loadingErr, "")
	saveDOM(t, store, id, skeletonPage(true))
	a := New(store)
	if err := a.SetConfig(Config{Provider: "openai_compatible", BaseURL: provider.URL, Model: "fake-small"}); err != nil {
		t.Fatal(err)
	}
	a.AnalyzeAsync(id)
	waitAll(t, a)
	got, _ := store.GetTest(id)
	if got.Triage == nil || got.Triage.Category != "TIMING" || strings.Contains(got.Triage.Summary, "cambió") {
		t.Fatalf("the rule wins over the model's LOCATOR_CHANGED: %+v", got.Triage)
	}
	if len(prompts) == 0 || !strings.Contains(prompts[0], "Loading signals at the moment of the failure") || !strings.Contains(prompts[0], "5 loading blocks") && !strings.Contains(prompts[0], "5 bloques") {
		t.Fatalf("the model is told about the loading: %v", prompts)
	}
	store.FinishRun(runID)

	f, err := BuildFacts(store, atrest.Dir{}, runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	e := a.Escalate(context.Background(), f, runID, 0, "qa", "es")
	if e.Source != "ai" {
		t.Fatalf("the AI answered: %+v", e)
	}
	all := strings.Join(append(append([]string{e.Headline, e.WhatHappened, e.RootCause}, e.Evidence...), e.NextSteps...), " | ")
	if blamesSelector(all) || !strings.Contains(all, "Esperar a que termine la carga") || !strings.Contains(all, "seguía cargando") {
		t.Fatalf("the template wins over the model blaming the selector: %s", all)
	}
	if !strings.Contains(prompts[len(prompts)-1], "Loading signals at the moment of the failure") {
		t.Fatalf("the escalation prompt carries the signals: %s", prompts[len(prompts)-1])
	}
}
