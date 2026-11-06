package ai

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/atrest"
)

// shotPNG writes a 400x300 PNG: blank (white) or with a form drawn on it.
func shotPNG(t *testing.T, dir, name string, blank bool) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if !blank && y > 100 && y < 140 && x > 50 && x < 350 {
				c = color.RGBA{30, 90, 200, 255}
			}
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, img)
	f.Close()
	return "/screenshots/" + name
}

// escalationFacts: a failed test (category LOCATOR_CHANGED, the local engine's guess) whose steps
// carry the given screenshots, oldest first.
func escalationFacts(t *testing.T, shots ...bool) (*Facts, int64, int64) {
	t.Helper()
	store, runID := groupingStore(t)
	dir := t.TempDir()
	id, _ := store.CreateTest(runID, "test_001_crear_receta", "", "")
	for i, blank := range shots {
		store.AddLog(id, "INFO", "paso "+string(rune('a'+i)), 0, shotPNG(t, dir, "s"+string(rune('a'+i))+".png", blank))
	}
	store.AddLog(id, "FAIL", "Captura al fallar", 0, shotPNG(t, dir, "fail.png", true))
	store.FinishTest(id, "FAIL", `NoSuchElementException: no such element: {"selector":"[name='useRecipe']"}`, "")
	store.SetTriagePending(id)
	store.SaveTriage(id, "LOCATOR_CHANGED", "El selector ya no coincide", "Actualiza el selector", "", "")
	store.FinishRun(runID)
	f, err := BuildFacts(store, atrest.Dir{Path: dir}, runID, id)
	if err != nil {
		t.Fatal(err)
	}
	return f, runID, id
}

// Caso del cliente (run 2, test_001_crear_receta_otro_nodo): todas las capturas en blanco. La
// tarjeta no debe mostrar un recuadro blanco gigante, y el texto no debe decir "cambió la interfaz".
func TestEscalationWithOnlyBlankScreenshots(t *testing.T) {
	clearEnv(t)
	f, runID, id := escalationFacts(t, true, true)
	if f.Screenshot == "" || !f.ShotBlank {
		t.Fatalf("the screenshot stays as evidence, marked blank: %q blank=%v", f.Screenshot, f.ShotBlank)
	}
	e := newEscalation(f, runID, id, "business", "es")
	templateEscalation(e)
	if strings.Contains(e.WhatHappened, "La pantalla cambió") || e.Severity == "low" {
		t.Fatalf("a blank page is not a UI change: %q (severity %s)", e.WhatHappened, e.Severity)
	}
	if !strings.Contains(e.WhatHappened, "no llegó a cargar") || !strings.Contains(e.RootCause, "no respondieron a tiempo") {
		t.Fatalf("what/cause: %q / %q", e.WhatHappened, e.RootCause)
	}
	joined := strings.Join(e.Evidence, " | ") + " | " + strings.Join(e.NextSteps, " | ")
	if !strings.Contains(joined, "capturas del test están en blanco") || strings.Contains(strings.ToLower(joined), "selector") {
		t.Fatalf("evidence and next steps: %s", joined)
	}
	en := newEscalation(f, runID, id, "qa", "en")
	templateEscalation(en)
	if !strings.Contains(en.WhatHappened, "never loaded") {
		t.Fatalf("english: %q", en.WhatHappened)
	}
	if p := f.promptBlock("test"); !strings.Contains(p, "Every screenshot of the test is blank") {
		t.Fatalf("the AI is told the page never rendered: %s", p)
	}
}

// El motor local ya dijo infraestructura (regla blank_page): el texto igual explica la página en blanco.
func TestEscalationBlankPageWithInfraCategory(t *testing.T) {
	clearEnv(t)
	f, runID, id := escalationFacts(t, true)
	f.Category = "INFRA_ERROR"
	e := newEscalation(f, runID, id, "business", "es")
	templateEscalation(e)
	if !strings.Contains(e.WhatHappened, "no llegó a cargar") {
		t.Fatalf("infra + blank page: %q", e.WhatHappened)
	}
}

// Con una captura con contenido antes del fallo, esa es la evidencia y no hay aviso de página en blanco.
func TestEscalationPrefersTheLastScreenshotWithContent(t *testing.T) {
	clearEnv(t)
	f, runID, id := escalationFacts(t, false, true)
	if f.ShotBlank || !strings.HasSuffix(f.Screenshot, "/sa.png") {
		t.Fatalf("screenshot with content: %q blank=%v", f.Screenshot, f.ShotBlank)
	}
	e := newEscalation(f, runID, id, "qa", "es")
	templateEscalation(e)
	if !strings.Contains(e.WhatHappened, "La pantalla cambió") {
		t.Fatalf("a real locator failure keeps its text: %q", e.WhatHappened)
	}
}

// Caso del cliente con llama3.1:8b: la evidencia vino como ["title"] y el resumen ejecutivo repetía el
// título. Esos campos se rellenan con lo que la plantilla arma desde la evidencia; el resto del texto
// del modelo queda.
func TestAIEscalationFieldsTheModelDidNotFill(t *testing.T) {
	clearEnv(t)
	reply := `{"title":"Ejecución de pruebas fallida en ambiente de pruebas","severity":"urgent",` +
		`"headline":"Ejecución de pruebas fallida en ambiente de pruebas","what_happened":"La ejecución falló en todos los tests.",` +
		`"impact":"","evidence":["title"," "],"root_cause":"Probablemente el ambiente esté caído.","next_steps":["next_steps"],"owner":"owner"}`
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer provider.Close()
	f, runID, id := escalationFacts(t, true)
	store, _ := groupingStore(t)
	a := New(store)
	if err := a.SetConfig(Config{Provider: "openai_compatible", BaseURL: provider.URL, Model: "fake-small"}); err != nil {
		t.Fatal(err)
	}
	e := a.Escalate(context.Background(), f, runID, id, "qa", "es")
	if e.Source != "ai" || e.AIError != "" {
		t.Fatalf("the AI answered: source=%s err=%s", e.Source, e.AIError)
	}
	if e.WhatHappened != "La ejecución falló en todos los tests." || e.RootCause != "Probablemente el ambiente esté caído." {
		t.Fatalf("what the model wrote stays: %q / %q", e.WhatHappened, e.RootCause)
	}
	if strings.EqualFold(e.Headline, e.Title) || e.Headline == "" {
		t.Fatalf("the executive summary does not repeat the title: %q", e.Headline)
	}
	for _, x := range append(append([]string{}, e.Evidence...), e.NextSteps...) {
		if schemaWords[strings.ToLower(strings.TrimSpace(x))] {
			t.Fatalf("a field name is not evidence nor a step: %q", x)
		}
	}
	if len(e.Evidence) == 0 || len(e.NextSteps) == 0 || e.Impact == "" || e.Owner == "owner" || e.Severity == "urgent" {
		t.Fatalf("filled from the evidence: %+v", e)
	}
}
