package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/live"
)

// El stream SSE atraviesa todo el router (incluido el middleware de compresión) y recibe
// los eventos que publican los handlers de escritura.
func TestStreamThroughRouter(t *testing.T) {
	srv, testID := newTestServer(t)
	srv.Live = live.NewHub()
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/stream?run=1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for srv.Live.Subscribers() == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	http.Post(ts.URL+"/api/v1/tests/1/logs", "application/json", strings.NewReader(`{"status":"INFO","message":"paso en vivo"}`))

	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e live.Event
		json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e)
		if e.Type != "log" || e.TestID != testID || !strings.Contains(line, "paso en vivo") {
			t.Fatalf("unexpected event: %s", line)
		}
		return
	}
	t.Fatal("no event received")
}

func TestLocatorEndpoint(t *testing.T) {
	srv, testID := newTestServer(t)
	srv.Store.AddLog(testID, "FAIL", "Captura al fallar", 0, "/screenshots/f.png")
	srv.Store.FinishTest(testID, "FAIL",
		`TimeoutError: Locator.click: Timeout 15000ms exceeded. waiting for locator("xpath=//button[@data-test='login-submit']")`, "")

	if rec := do(t, srv, http.MethodGet, "/api/v1/tests/1/locator", ""); rec.Code != http.StatusOK {
		t.Fatalf("without snapshot: %d", rec.Code)
	}
	snap := `{"url":"https://app/login","viewport":{"w":1366,"h":768},"elements":[
		{"tag":"button","type":"submit","text":"Login","x":500,"y":460,"w":300,"h":44,"visible":true},
		{"tag":"input","name":"username","placeholder":"Username","x":500,"y":300,"w":300,"h":40,"visible":true}]}`
	if rec := do(t, srv, http.MethodPost, "/api/v1/tests/1/dom", snap); rec.Code != http.StatusCreated {
		t.Fatalf("POST dom: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(t, srv, http.MethodGet, "/api/v1/tests/1/locator", "")
	var rep LocatorReport
	json.Unmarshal(rec.Body.Bytes(), &rep)
	if rep.FailedSelector != `xpath=//button[@data-test='login-submit']` || len(rep.Suggestions) == 0 ||
		rep.Suggestions[0].Python != `page.get_by_role("button", name="Login")` || rep.Screenshot != "/screenshots/f.png" ||
		rep.Viewport.W != 1366 || rep.StepLogID == 0 {
		t.Fatalf("locator report: %s", rec.Body.String())
	}

	// un fallo que no es de locator no tiene recomendación
	srv.Store.FinishTest(testID, "FAIL", "AssertionError: 3 != 4", "")
	if rec := do(t, srv, http.MethodGet, "/api/v1/tests/1/locator", ""); rec.Code != http.StatusNoContent {
		t.Errorf("non-locator failure: want 204, got %d", rec.Code)
	}
}

// El elemento no apareció porque el backend falló justo antes: el reporte no sugiere reemplazos,
// pero el Inspector recibe el selector y la llamada (sin la query) para decirlo en vez de "no se pudo
// extraer el selector". El panel de locator lo ignora.
func TestLocatorEndpointBackendCause(t *testing.T) {
	srv, testID := newTestServer(t)
	if rec := do(t, srv, http.MethodPost, "/api/v1/tests/1/network",
		`{"connections":[{"method":"GET","url":"https://app.test/api/orders/current?token=s3cr3t","status":504}]}`); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("POST network: %d %s", rec.Code, rec.Body.String())
	}
	srv.Store.AddLog(testID, "FAIL", "Captura al fallar", 0, "")
	srv.Store.FinishTest(testID, "FAIL",
		`TimeoutError: Page.click: Timeout 3000ms exceeded. Call log: - waiting for locator("xpath=(//i[contains(@class,'fa-plus-circle')])[1]")`, "")
	rec := do(t, srv, http.MethodGet, "/api/v1/tests/1/locator", "")
	var rep LocatorReport
	json.Unmarshal(rec.Body.Bytes(), &rep)
	if rec.Code != http.StatusOK || rep.FailedSelector != `xpath=(//i[contains(@class,'fa-plus-circle')])[1]` || len(rep.Suggestions) != 0 ||
		rep.BackendCause == nil || rep.BackendCause.Status != 504 || rep.BackendCause.Method != "GET" ||
		rep.BackendCause.Endpoint != "app.test/api/orders/current" || strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("backend cause: %d %s", rec.Code, rec.Body.String())
	}

	// un fallo que no es de elemento no encontrado sigue sin reporte, aunque el backend haya fallado
	srv.Store.FinishTest(testID, "FAIL", "AssertionError: 3 != 4", "")
	if rec := do(t, srv, http.MethodGet, "/api/v1/tests/1/locator", ""); rec.Code != http.StatusNoContent {
		t.Errorf("non-locator failure: want 204, got %d %s", rec.Code, rec.Body.String())
	}
}

// El evento de un run creado trae su nombre y su ejecución: la UI lo avisa (agrupado por ejecución)
// aunque la lista de runs todavía no lo traiga, como pasa con muchas corridas creadas a la vez.
func TestRunCreatedEventCarriesItsExecution(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Live = live.NewHub()
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for srv.Live.Subscribers() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	http.Post(ts.URL+"/api/v1/runs", "application/json", strings.NewReader(`{"name":"shop/test_a.py","environment":"qa","branch":"dev","execution_id":"suite-1"}`))
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e struct {
			Type string            `json:"type"`
			Data map[string]string `json:"data"`
		}
		json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e)
		if e.Type != "run" {
			continue
		}
		want := map[string]string{"action": "created", "name": "shop/test_a.py", "execution_id": "suite-1", "environment": "qa", "branch": "dev"}
		for k, v := range want {
			if e.Data[k] != v {
				t.Fatalf("%s = %q, want %q (%s)", k, e.Data[k], v, line)
			}
		}
		return
	}
	t.Fatal("no run event received")
}
