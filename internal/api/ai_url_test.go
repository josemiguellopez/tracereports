package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

// Quien puede cambiar Ajustes no puede llevarse la key guardada apuntando la URL de la API a su
// propio servidor: cambiar la URL exige escribir la key de nuevo.
func TestSavedKeyIsNotSentToANewURL(t *testing.T) {
	clearAIEnv(t)
	var hits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer evil.Close()
	srv, _ := newTestServer(t)
	srv.Secrets = masterBox(t, testMasterKey)
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","api_key":"`+fakeAIKey+`"}}`); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	// guardar con otra URL y sin key: rechazado, la configuración no cambia
	rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","base_url":"`+evil.URL+`"}}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "type the API key again") {
		t.Fatalf("redirigir con la key guardada: %d %s", rec.Code, rec.Body)
	}
	if c := srv.AI.Config(); c.BaseURL == evil.URL || c.APIKey != fakeAIKey {
		t.Fatalf("la configuración cambió: %+v", c.BaseURL)
	}
	// "Probar conexión" tampoco la manda
	rec = call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"openai","base_url":"`+evil.URL+`","model":"m"}`)
	if !strings.Contains(rec.Body.String(), "type the API key again") || hits.Load() != 0 {
		t.Fatalf("probar conexión: %s (llamadas: %d)", rec.Body, hits.Load())
	}
	// misma URL (o solo otro modelo): sigue usando la guardada
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","model":"gpt-5"}}`); rec.Code != 200 || srv.AI.Config().APIKey != fakeAIKey {
		t.Fatalf("mismo host: %d %s", rec.Code, rec.Body)
	}
	// escribiendo la key, la URL nueva se acepta
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","base_url":"`+evil.URL+`","api_key":"otra-key-escrita"}}`); rec.Code != 200 {
		t.Fatalf("key escrita: %d %s", rec.Code, rec.Body)
	}
}

// Lo mismo con la key del entorno: solo va a la URL del entorno.
func TestEnvKeyIsNotSentToANewURL(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	t.Setenv("GEMINI_API_KEY", fakeAIKey) // después: newTestServer la vacía
	rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"gemini","base_url":"https://collector.example"}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("key del entorno a otra URL: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"gemini","base_url":"https://generativelanguage.googleapis.com"}}`); rec.Code != 200 {
		t.Fatalf("la URL por defecto del proveedor: %d %s", rec.Code, rec.Body)
	}
}

// Una URL guardada por una versión anterior que no coincide con la del entorno no se lleva la key
// del entorno al arrancar.
func TestStartupDoesNotPairEnvKeyWithOtherSavedURL(t *testing.T) {
	clearAIEnv(t)
	t.Setenv("OPENAI_API_KEY", fakeAIKey)
	srv, _ := newTestServer(t)
	srv.Store.SaveSettings(map[string]string{setProvider: "openai", setBaseURL: "https://collector.example"})
	srv2 := &Server{Store: srv.Store, AI: ai.New(srv.Store)}
	srv2.LoadSettings()
	// queda la configuración del entorno (su key con su URL), nunca la key con la URL guardada
	if c := srv2.AI.Config(); c.APIKey == fakeAIKey && strings.Contains(c.BaseURL, "collector.example") {
		t.Fatalf("la key del entorno quedó apuntando a la URL guardada: %s", c.BaseURL)
	}
}

func TestKeyHost(t *testing.T) {
	for raw, want := range map[string]string{
		"":                        "api.openai.com",
		"https://API.OpenAI.com/": "api.openai.com",
		"localhost:11434":         "localhost:11434",
		"http://10.0.0.5:8080/v1": "10.0.0.5:8080",
	} {
		if got := keyHost(raw, "openai"); got != want {
			t.Errorf("%q → %q, quería %q", raw, got, want)
		}
	}
}
