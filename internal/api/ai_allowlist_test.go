package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

// Con TRACEREPORTS_AI_ALLOWED_PROVIDERS, Ajustes solo ofrece los permitidos, no guarda otro y
// "Probar conexión" no lo llama.
func TestSettingsRespectTheAIAllowlist(t *testing.T) {
	clearAIEnv(t)
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "builtin,ollama")
	var hits int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		t.Errorf("petición al proveedor no permitido: %s %s", r.Method, r.URL)
	}))
	defer fake.Close()
	srv, _ := newTestServer(t)

	v, _ := settingsView(t, srv)
	var ids []string
	for _, p := range v["providers"].([]any) {
		ids = append(ids, p.(map[string]any)["id"].(string))
	}
	if strings.Join(ids, ",") != "builtin,ollama" {
		t.Fatalf("Ajustes ofrece: %v", ids)
	}

	body := `{"ai":{"provider":"openai_compatible","model":"m","base_url":"` + fake.URL + `"}}`
	rec := call(t, srv, "PUT", "/api/v1/settings", body)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "TRACEREPORTS_AI_ALLOWED_PROVIDERS") {
		t.Fatalf("guardar un proveedor no permitido: %d %s", rec.Code, rec.Body)
	}
	if saved, _ := srv.Store.Settings(); saved[setProvider] != "" {
		t.Fatalf("no se guarda: %v", saved[setProvider])
	}
	if srv.AI.Provider() != ai.ProviderBuiltin {
		t.Fatalf("sigue el motor local: %q", srv.AI.Provider())
	}
	rec = call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"openai_compatible","model":"m","base_url":"`+fake.URL+`"}`)
	if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "no está permitido") {
		t.Fatalf("probar conexión: %s", rec.Body)
	}
	// uno permitido sí se guarda
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"ollama","base_url":"http://127.0.0.1:1"}}`); rec.Code != 200 {
		t.Fatalf("ollama: %d %s", rec.Code, rec.Body)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("%d peticiones al proveedor no permitido", n)
	}
}

// Un proveedor guardado antes de activar la allowlist no se carga al arrancar: queda el del entorno.
func TestSavedProviderNotAllowedIsNotLoaded(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Store.SaveSettings(map[string]string{setProvider: "openai_compatible", setModel: "m", setBaseURL: "http://example.invalid/v1"})
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "builtin")
	if err := srv.LoadSettings(); err != nil {
		t.Fatal(err)
	}
	if srv.AI.Provider() != ai.ProviderBuiltin {
		t.Fatalf("cargó %q", srv.AI.Provider())
	}
}
