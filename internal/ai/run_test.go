package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestRunTriageGroupsByCause(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runID, _ := store.CreateRun("Regresión", "qa")
	fail := func(name, msg string, conns ...db.NetConn) {
		id, _ := store.CreateTest(runID, name, "", "")
		if len(conns) > 0 {
			store.AddNetwork(id, conns)
		}
		store.FinishTest(id, "FAIL", msg, "")
	}
	authDown := db.NetConn{Method: "POST", URL: "https://app/auth/validate", Failed: true, ErrorText: "net::ERR_CONNECTION_REFUSED"}
	fail("Login admin", "dashboard not visible", authDown)
	fail("Login operador", "dashboard not visible", authDown)
	fail("Exportar PDF", "AssertionError: botón deshabilitado")
	pass, _ := store.CreateTest(runID, "Buscar", "", "")
	store.FinishTest(pass, "PASS", "", "")
	store.FinishRun(runID)

	// Sin API key: incidentes agrupados + titular automático, sin IA.
	t.Setenv("GEMINI_API_KEY", "")
	a := New(store)
	done := make(chan struct{})
	a.AnalyzeRunAsync(runID, func() { close(done) })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run analysis did not finish")
	}
	rt, _ := store.GetRunTriage(runID)
	if rt == nil || rt.State != "DONE" || rt.AI || len(rt.Incidents) != 2 {
		t.Fatalf("run triage without AI: %+v", rt)
	}
	first := rt.Incidents[0]
	if first.Kind != "backend" || len(first.TestIDs) != 2 || !strings.Contains(first.Title, "POST app/auth/validate") || len(first.Evidence) == 0 {
		t.Errorf("biggest incident should be the auth backend: %+v", first)
	}
	if !strings.Contains(rt.Headline, "3 tests fallaron") {
		t.Errorf("headline: %q", rt.Headline)
	}

	// Con API key: Gemini completa titular, resumen y causa/acción de cada incidente.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		json.NewDecoder(r.Body).Decode(&req)
		prompt := req.Contents[0].Parts[0].Text
		if !strings.Contains(prompt, first.Key) {
			t.Errorf("prompt must list incident keys")
		}
		answer, _ := json.Marshal(map[string]any{
			"headline":  "Un solo problema real: el servicio de login está caído.",
			"summary":   "Dos fallos vienen del backend; uno es un bug de UI.",
			"incidents": []map[string]string{{"key": first.Key, "cause": "Auth caído", "action": "Revisar el servicio de auth"}},
		})
		resp, _ := json.Marshal(map[string]any{"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]string{{"text": string(answer)}}}}}})
		w.Write(resp)
	}))
	defer mock.Close()
	t.Setenv("GEMINI_API_KEY", "k")
	t.Setenv("GEMINI_BASE_URL", mock.URL)
	a = New(store)
	a.AnalyzeRunAsync(runID, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.Wait(ctx)
	rt, _ = store.GetRunTriage(runID)
	if !rt.AI || !strings.HasPrefix(rt.Headline, "Un solo problema real") || rt.Incidents[0].Action != "Revisar el servicio de auth" {
		t.Errorf("run triage with AI: %+v", rt)
	}
}
