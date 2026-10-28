package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

func probeBody(t *testing.T, srv *Server, path string, opts ...reqOpt) map[string]any {
	t.Helper()
	rec := call(t, srv, "POST", path, `{}`, opts...)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
	}
	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// Trace Buddy pregunta si la IA activa responde: sin IA, motor local o un LLM (con lo que tardó).
func TestAIProbeStates(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)

	if res := probeBody(t, srv, "/api/v1/ui/ai/probe"); res["state"] != "local" || res["ok"] != true || res["provider"] != ai.ProviderBuiltin {
		t.Fatalf("motor local: %v", res)
	}
	if err := srv.AI.SetConfig(ai.Config{}); err != nil { // AI_PROVIDER=off
		t.Fatal(err)
	}
	if res := probeBody(t, srv, "/api/v1/ui/ai/probe"); res["state"] != "off" || res["ok"] != false {
		t.Fatalf("apagada: %v", res)
	}

	var calls atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"message":{"content":"{\"ok\":true}"}}`))
	}))
	defer mock.Close()
	if err := srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: mock.URL, Model: "llama3.1"}); err != nil {
		t.Fatal(err)
	}
	res := probeBody(t, srv, "/api/v1/ui/ai/probe")
	if res["state"] != "llm" || res["ok"] != true || res["model"] != "llama3.1" || res["ms"] == nil {
		t.Fatalf("ollama: %v", res)
	}
	// abrir y cerrar el asistente no paga una llamada cada vez; "probar de nuevo" sí
	probeBody(t, srv, "/api/v1/ui/ai/probe")
	if n := calls.Load(); n != 1 {
		t.Fatalf("la respuesta reciente se reutiliza: %d llamadas", n)
	}
	probeBody(t, srv, "/api/v1/ui/ai/probe?force=1")
	if n := calls.Load(); n != 2 {
		t.Fatalf("force=1 vuelve a probar: %d llamadas", n)
	}
	// otra configuración no reutiliza la respuesta anterior
	if err := srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: mock.URL, Model: "qwen2.5"}); err != nil {
		t.Fatal(err)
	}
	if res := probeBody(t, srv, "/api/v1/ui/ai/probe"); res["model"] != "qwen2.5" || calls.Load() != 3 {
		t.Fatalf("modelo nuevo: %v (%d llamadas)", res, calls.Load())
	}
}

// Un error del proveedor llega en palabras simples (hint) y sin la API key.
func TestAIProbeErrorHidesKey(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	const key = "sk-probe-secreta-1234567890abcdef"
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"message":"model not found for key ` + key + `"}}`))
	}))
	defer mock.Close()
	if err := srv.AI.SetConfig(ai.Config{Provider: "openai", APIKey: key, BaseURL: mock.URL, Model: "gpt-x"}); err != nil {
		t.Fatal(err)
	}
	rec := call(t, srv, "POST", "/api/v1/ui/ai/probe", `{}`)
	if strings.Contains(rec.Body.String(), key) {
		t.Fatalf("la key no sale del servidor: %s", rec.Body)
	}
	var res map[string]any
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res["ok"] != false || res["hint"] != "model_unavailable" || res["error"] == "" {
		t.Fatalf("error: %v", res)
	}
}

// Solo administración: el login de solo lectura y otro equipo sin login no gastan la key del equipo.
func TestAIProbeAdminOnly(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Auth = Auth{UIUser: "admin", UIPass: "clave-admin", ViewerUser: "equipo", ViewerPass: "clave-lectura"}
	if rec := call(t, srv, "POST", "/api/v1/ui/ai/probe", `{}`, basicAuth("equipo", "clave-lectura")); rec.Code != http.StatusForbidden {
		t.Fatalf("solo lectura: %d", rec.Code)
	}
	if rec := call(t, srv, "POST", "/api/v1/ui/ai/probe", `{}`, basicAuth("admin", "clave-admin")); rec.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body)
	}
	srv.Auth = Auth{}
	if rec := call(t, srv, "POST", "/api/v1/ui/ai/probe", `{}`, fromAddr("10.1.1.1:1")); rec.Code != http.StatusForbidden {
		t.Fatalf("sin login desde otro equipo: %d", rec.Code)
	}
	// un formulario de otro sitio no la dispara
	if rec := call(t, srv, "POST", "/api/v1/ui/ai/probe", `{}`, header("Origin", "https://evil.example")); rec.Code != http.StatusForbidden {
		t.Fatalf("otro origen: %d", rec.Code)
	}
}
