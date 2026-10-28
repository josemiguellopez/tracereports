package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// failingProvider es un proveedor falso que hace fallar el test si recibe cualquier petición.
func failingProvider(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		t.Errorf("no debe llegar ninguna petición al proveedor no permitido: %s %s", r.Method, r.URL)
		http.Error(w, "nope", 500)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestAllowlistIgnoresAKeyOfAProviderNotAllowed(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "fake-gemini-key")
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "builtin,ollama")
	if c := ConfigFromEnv(); c.Provider != ProviderBuiltin || c.APIKey != "" {
		t.Fatalf("la key cloud se ignora y diagnostica el motor local: %+v", c)
	}
	// sin builtin en la lista: diagnóstico apagado, nunca el proveedor cloud
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "ollama")
	if c := ConfigFromEnv(); c.Provider != "" {
		t.Fatalf("apagado: %+v", c)
	}
	// AI_PROVIDER explícito tampoco lo salta
	t.Setenv("AI_PROVIDER", "gemini")
	if c := ConfigFromEnv(); c.Provider != "" {
		t.Fatalf("AI_PROVIDER=gemini con allowlist ollama: %+v", c)
	}
	// con la allowlist que lo incluye, se usa
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "ollama, Gemini")
	if c := ConfigFromEnv(); c.Provider != "gemini" {
		t.Fatalf("permitido: %+v", c)
	}
}

// Sin allowlist todo sigue como antes (compatibilidad).
func TestEmptyAllowlistKeepsTheCurrentBehavior(t *testing.T) {
	clearEnv(t)
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "")
	t.Setenv("GEMINI_API_KEY", "fake-gemini-key")
	if c := ConfigFromEnv(); c.Provider != "gemini" || c.APIKey != "fake-gemini-key" {
		t.Fatalf("%+v", c)
	}
	if len(AllowedProviders()) != len(Providers) || CheckAllowlist() != nil {
		t.Fatal("sin allowlist se ofrecen todos")
	}
}

func TestValidateRejectsAProviderNotAllowed(t *testing.T) {
	clearEnv(t)
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "builtin,ollama")
	err := Config{Provider: "anthropic", APIKey: "k"}.Validate()
	if err == nil || !strings.Contains(err.Error(), "no está permitido") || !strings.Contains(err.Error(), "builtin,ollama") {
		t.Fatalf("%v", err)
	}
	for _, ok := range []Config{{}, {Provider: ProviderBuiltin}, {Provider: "ollama"}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	var ids []string
	for _, p := range AllowedProviders() {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "builtin,ollama" {
		t.Fatalf("Ajustes ofrece: %v", ids)
	}
}

func TestCheckAllowlistRejectsUnknownIDs(t *testing.T) {
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "builtin,olama")
	if err := CheckAllowlist(); err == nil || !strings.Contains(err.Error(), "olama") {
		t.Fatalf("%v", err)
	}
}

// Con la allowlist local y una key cloud en el entorno, ni el análisis ni "Probar conexión" ni
// SetConfig llegan al proveedor.
func TestNoRequestReachesAProviderNotAllowed(t *testing.T) {
	clearEnv(t)
	fake, hits := failingProvider(t)
	t.Setenv("GEMINI_API_KEY", "fake-gemini-key")
	t.Setenv("GEMINI_BASE_URL", fake.URL)
	t.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "builtin,ollama")
	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "pago", "AssertionError: expected 4 but got 3", "", 0)
	store.FinishRun(runID)

	a := New(store)
	if a.Provider() != ProviderBuiltin {
		t.Fatalf("proveedor: %q", a.Provider())
	}
	a.AnalyzeAsync(id)
	waitAll(t, a)
	a.AnalyzeRunAsync(runID, nil)
	waitAll(t, a)
	if got, _ := store.GetTest(id); got.Triage == nil || got.Triage.Provider != ProviderBuiltin {
		t.Fatalf("diagnóstico: %+v", got.Triage)
	}
	cfg := Config{Provider: "gemini", APIKey: "fake-gemini-key", BaseURL: fake.URL}
	if err := a.SetConfig(cfg); err == nil {
		t.Fatal("SetConfig de un proveedor no permitido")
	}
	if _, err := a.Test(context.Background(), cfg); err == nil {
		t.Fatal("Probar conexión de un proveedor no permitido")
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Fatalf("%d peticiones llegaron al proveedor", n)
	}
}
