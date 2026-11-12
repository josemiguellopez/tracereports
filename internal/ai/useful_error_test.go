package ai

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/josemiguellopez/tracereports/internal/ai/builtin"
	"github.com/josemiguellopez/tracereports/internal/atrest"
)

// El error del reporte de escalamiento: el framework lo envuelve con el nombre del archivo y una lista
// serializada. El título del incidente, la evidencia del escalamiento y el triage del test mostraban
// "Errores acumulados para el file_name:…espontanea.py: [" (y una letra partida: «37�»).
const wrappedErr = `Errores acumulados para el file_name:test_agregar_item_inmunizaciones_atencion_espontanea.py: ["Error Linea 3739 esperando elemento después de 6 intentos: //div[contains(@class,'textoverflow') and contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), 'consultorio pudahuel oriente. lote: lote_001_vac_01_1794482716')]"]`

func usefulText(t *testing.T, where, s string) {
	t.Helper()
	if !strings.Contains(s, "esperando elemento") || !strings.Contains(s, "//div[") || strings.Contains(s, "file_name") || !utf8.ValidString(s) || strings.HasSuffix(s, "[") {
		t.Errorf("%s: %q", where, s)
	}
}

func TestWrappedErrorReadsWellInIncidentEscalationAndTriage(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	store.CreateTest(runID, "test_001_ok", "", "")
	id := failAt(t, store, runID, "test_002_box_agregar_inmunizaciones", "Captura al fallar", "", 0)
	store.FinishTest(id, "FAIL", wrappedErr, "")
	got := incidents(t, store, runID)
	if len(got) != 1 {
		t.Fatalf("incidents: %+v", got)
	}
	usefulText(t, "incident title", got[0].Title)
	usefulText(t, "incident evidence", got[0].Evidence[0].Text)

	f, err := BuildFacts(store, atrest.Dir{}, runID, id)
	if err != nil {
		t.Fatal(err)
	}
	e := newEscalation(f, runID, id, "qa", "es")
	templateEscalation(e)
	found := false
	for _, ev := range e.Evidence {
		if strings.Contains(ev, "esperando elemento") {
			usefulText(t, "escalation evidence", ev)
			found = true
		}
		if strings.HasSuffix(strings.TrimSpace(ev), "[") {
			t.Errorf("evidence ending in '[': %q", ev)
		}
	}
	if !found {
		t.Fatalf("the escalation evidence carries the useful error: %q", e.Evidence)
	}

	d := builtin.Diagnose(builtin.Input{Lang: "es", Error: wrappedErr})
	usefulText(t, "triage evidence", strings.Join(d.Evidence, " "))
}

// El modelo (llama3.1:8b en el reporte) copió en la evidencia el título viejo del incidente: el
// envoltorio cortado en "[". Esa línea se limpia (o se va, si no tenía nada más).
func TestModelEvidenceWithAWrappedErrorIsCleaned(t *testing.T) {
	got := usefulEvidence([]string{"Captura al fallar", "Errores acumulados para el file_name:test_x.py: [",
		`Errores acumulados para el file_name:test_x.py: ["Error Linea 12 esperando elemento //div[@id='a']"]`})
	if len(got) != 2 || got[0] != "Captura al fallar" || got[1] != "Error Linea 12 esperando elemento //div[@id='a']" {
		t.Fatalf("%q", got)
	}
}
