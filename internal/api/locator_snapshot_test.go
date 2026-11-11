package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

const domSnap = `{"url":"https://app/main","viewport":{"w":1382,"h":928},"elements":[
	{"tag":"button","id":"guardar","text":"Guardar","x":10,"y":10,"w":90,"h":30,"visible":true}]}`

func locatorOf(t *testing.T, srv *Server) LocatorReport {
	t.Helper()
	rec := do(t, srv, http.MethodGet, "/api/v1/tests/1/locator", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("locator: %d %s", rec.Code, rec.Body.String())
	}
	var rep LocatorReport
	json.Unmarshal(rec.Body.Bytes(), &rep)
	return rep
}

// Caso real (run 379, test 927): el snapshot llegó pero no hubo reemplazos; la UI decía "no hay
// snapshot". Ahora el reporte dice has_dom, y el selector se toma entero del trace.
func TestLocatorReportSaysWhetherThereIsASnapshot(t *testing.T) {
	srv, testID := newTestServer(t)
	const xp = `//div[contains(translate(normalize-space(.),'ABC','abc'),'segundo diagnostico texto libre') and contains(@class,'mr-auto')]`
	cut := `Errores acumulados: ["Warning Linea: 2299 al verificar si el elemento '` + xp[:70] // error_message cortado
	trace := `Errores acumulados: ["Warning Linea: 2299 al verificar si el elemento '` + xp + `' "]`
	srv.Store.FinishTest(testID, "FAIL", cut, trace)

	if rep := locatorOf(t, srv); rep.HasDOM || rep.DOMElements != 0 {
		t.Fatalf("without snapshot: %+v", rep)
	}
	if rec := do(t, srv, http.MethodPost, "/api/v1/tests/1/dom", domSnap); rec.Code != http.StatusCreated {
		t.Fatalf("POST dom: %d", rec.Code)
	}
	rep := locatorOf(t, srv)
	if !rep.HasDOM || rep.DOMElements != 1 || rep.PageURL != "https://app/main" {
		t.Fatalf("with snapshot: %+v", rep)
	}
	if rep.FailedSelector != xp || rep.FailedSelectorTruncated {
		t.Fatalf("the whole selector comes from the trace: %q truncated=%v", rep.FailedSelector, rep.FailedSelectorTruncated)
	}
}

// Si el selector solo está cortado (no aparece entero en ningún lado) no se buscan reemplazos.
func TestTruncatedSelectorGetsNoSuggestions(t *testing.T) {
	srv, testID := newTestServer(t)
	srv.Store.FinishTest(testID, "FAIL", `no se encontró el elemento '//button[@id='guar`, "")
	do(t, srv, http.MethodPost, "/api/v1/tests/1/dom", domSnap)
	rep := locatorOf(t, srv)
	if !rep.FailedSelectorTruncated || len(rep.Suggestions) != 0 || !rep.HasDOM {
		t.Fatalf("truncated selector: %+v", rep)
	}
}
