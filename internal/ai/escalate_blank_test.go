package ai

import (
	"image"
	"image/color"
	"image/png"
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

