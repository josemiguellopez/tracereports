package api

import (
	"encoding/json"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func similarOf(t *testing.T, srv *Server, testID int64) []SimilarIncident {
	t.Helper()
	rec := call(t, srv, "GET", "/api/v1/tests/"+itoa(testID)+"/similar", "")
	if rec.Code != 200 {
		t.Fatalf("similar: %d %s", rec.Code, rec.Body)
	}
	var out []SimilarIncident
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

// failedRun crea una ejecución del proyecto con un test que falla por la llamada dada.
func failedRun(t *testing.T, srv *Server, name, url string, status int, msg string) int64 {
	t.Helper()
	runID, _ := srv.Store.CreateRunWithMeta("nightly", "qa", db.RunMeta{Project: "shop"})
	id, _ := srv.Store.CreateTest(runID, name, "", "")
	if url != "" {
		srv.Store.AddNetwork(id, []db.NetConn{{Method: "POST", URL: url, Status: status}})
	}
	srv.Store.AddLog(id, "FAIL", msg, 0, "")
	srv.Store.FinishTest(id, "FAIL", msg, "")
	srv.Store.FinishRun(runID)
	return id
}

// Clasificar un fallo lo indexa; un fallo nuevo con la misma causa lo encuentra; uno distinto no.
func TestSimilarIncidentsFromVerdicts(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	old := failedRun(t, srv, "pays an order", "https://shop.test/api/orders/123", 500, "AssertionError: order not confirmed")
	other := failedRun(t, srv, "cart total", "", 0, "AssertionError: expected 4 but got 3")
	if rec := call(t, srv, "POST", "/api/v1/ui/tests/"+itoa(old)+"/verdict", `{"verdict":"environment","comment":"pool de conexiones de auth agotado"}`); rec.Code != 201 {
		t.Fatalf("veredicto: %d %s", rec.Code, rec.Body)
	}
	call(t, srv, "POST", "/api/v1/ui/tests/"+itoa(other)+"/verdict", `{"verdict":"product_bug","comment":"redondeo"}`)

	fresh := failedRun(t, srv, "pays with a card", "https://shop.test/api/orders/987", 500, "AssertionError: order not confirmed")
	got := similarOf(t, srv, fresh)
	if len(got) != 1 || got[0].TestID != old || got[0].Score < 90 || got[0].Verdict != "environment" ||
		got[0].Comment != "pool de conexiones de auth agotado" || got[0].TestName != "pays an order" {
		t.Fatalf("%+v", got)
	}
	// un fallo distinto (otra llamada, otro estado) no se parece lo suficiente
	if got := similarOf(t, srv, failedRun(t, srv, "login", "https://auth.test/api/session", 401, "TimeoutError: waiting for #welcome")); len(got) != 0 {
		t.Fatalf("distinto: %+v", got)
	}
	// un test que no falló no tiene parecidos
	pass, _ := srv.Store.CreateTest(1, "ok", "", "")
	srv.Store.FinishTest(pass, "PASS", "", "")
	if got := similarOf(t, srv, pass); len(got) != 0 {
		t.Fatalf("PASS: %+v", got)
	}

	// cambiar el veredicto actualiza el índice (una fila por test, el último)
	call(t, srv, "POST", "/api/v1/ui/tests/"+itoa(old)+"/verdict", `{"verdict":"product_bug","comment":"bug del checkout"}`)
	if got := similarOf(t, srv, fresh); len(got) != 1 || got[0].Verdict != "product_bug" || got[0].Comment != "bug del checkout" {
		t.Fatalf("veredicto cambiado: %+v", got)
	}

	// la retención borra la evidencia y con ella su entrada del índice
	srv.Store.PurgeRunsBefore(db.NowMs() + 60_000)
	if terms, err := srv.Store.IndexedTerms(old); err != nil || terms != nil {
		t.Fatalf("tras purgar: %v %v", terms, err)
	}
}
