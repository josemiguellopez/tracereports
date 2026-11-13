package ai

import (
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/atrest"
	"github.com/josemiguellopez/tracereports/internal/db"
)

// El caso de la validación del cliente (run 1784, test 5148): el cliente guardó el error recortado a
// 301 caracteres (termina en "…'abcdefghijklmnopqrstuvwxyz'), 'co"), pero el paso FAIL lo trae entero. Antes
// del paso que falló hay una validación que pasó ("Texto esperado … coincide"), y la captura del fallo es
// otro paso FAIL.
const fullXPath = `//div[contains(@class,'textoverflow') and contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), 'consultorio pudahuel oriente. lote: lote_001_vac_01_1794482716')]`

func clientCase(t *testing.T, store *db.Store, runID int64) int64 {
	t.Helper()
	full := `Errores acumulados para el file_name:test_agregar_item_inmunizaciones_atencion_espontanea.py: ["Error Linea 3739 esperando elemento después de 6 intentos: ` + fullXPath + `"]`
	cut := string([]rune(full)[:301])
	id, _ := store.CreateTest(runID, "test_002_box_agregar_inmunizaciones_paciente_espontaneo_por_rut", "", "")
	store.AddLog(id, "PASS", "Texto esperado 'has guardado la inmunización con éxito!' coincide con 'Has guardado la inmunización con éxito!' dentro de Validación de mensaje de inmunización agregada exitosamente", 9_000, "")
	store.AddLog(id, "FAIL", "Error Linea 3739 esperando elemento después de 6 intentos: "+fullXPath, 10_000, "")
	store.AddLog(id, "FAIL", "Captura al fallar", 10_100, "/screenshots/x.png")
	store.FinishTest(id, "FAIL", cut, "")
	return id
}

func TestWhereFailedUsesTheWholeErrorAndNamesTheLastPassedStep(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	ok, _ := store.CreateTest(runID, "test_001_abrir_ficha", "", "")
	store.FinishTest(ok, "PASS", "", "")
	id := clientCase(t, store, runID)
	saveDOM(t, store, id, skeletonPage(false))
	store.FinishRun(runID)
	f, err := BuildFacts(store, atrest.Dir{}, runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	w := f.Where
	if !strings.HasSuffix(w.Selector, "lote_001_vac_01_1794482716')]") || !strings.HasPrefix(w.Selector, "//div[") {
		t.Fatalf("the whole selector: %q", w.Selector)
	}
	if !strings.Contains(w.Error, "1794482716')]") || strings.HasSuffix(w.Error, "'co") {
		t.Fatalf("the whole error: %q", w.Error)
	}
	if w.Step != "" || !strings.HasPrefix(w.LastOK, "Texto esperado 'has guardado") {
		t.Fatalf("a passed step is not «the step that failed»: step=%q last_ok=%q", w.Step, w.LastOK)
	}
	for _, lang := range []string{"es", "en"} {
		e := newEscalation(f, runID, 0, "qa", lang)
		templateEscalation(e)
		ev := strings.Join(e.Evidence, " | ")
		if !strings.Contains(ev, "1794482716')]") || strings.Contains(ev, "'co |") {
			t.Errorf("%s: evidence with the whole error: %s", lang, ev)
		}
		want := map[string]string{"es": "Falló 1 de 2 tests: la página seguía cargando", "en": "1 of 2 tests failed: the page was still loading"}[lang]
		if !strings.HasPrefix(e.Headline, want) {
			t.Errorf("%s: the summary says the cause: %q", lang, e.Headline)
		}
	}
	// el titular genérico del diagnóstico de la ejecución no se usa con un solo fallo
	f.RunHeadline = "1 incidente principal, 1 incidente secundario"
	e := newEscalation(f, runID, 0, "qa", "es")
	templateEscalation(e)
	if strings.Contains(e.Headline, "incidente principal") {
		t.Fatalf("headline: %q", e.Headline)
	}
}

// Un error que llegó cortado y que ningún paso completa se muestra con «…»: no es el texto entero.
func TestACutErrorEndsInAnEllipsis(t *testing.T) {
	got := shownError(`Error Linea 12 esperando elemento: //div[contains(@class,'textoverflow') and contains(., 'co`, 300)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("%q", got)
	}
	if got := shownError(`Error Linea 12 esperando elemento: //div[@id='a']`, 300); strings.HasSuffix(got, "…") {
		t.Fatalf("a whole error has no ellipsis: %q", got)
	}
}
