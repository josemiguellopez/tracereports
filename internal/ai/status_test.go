package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// summary de una página Statuspage (como status.claude.com) con un incidente abierto y otro resuelto
const statusSummary = `{"page":{"name":"Fake"},"status":{"indicator":"minor","description":"Partially Degraded Service"},
"components":[{"name":"claude.ai","status":"operational"},{"name":"Claude API (api.anthropic.com)","status":"degraded_performance"},{"name":"Claude Code","status":"operational"}],
"incidents":[{"name":"Elevated errors on Claude API","status":"investigating","impact":"minor","shortlink":"https://stspg.io/x","updated_at":"2026-10-08T12:00:00Z"},
{"name":"Old one","status":"resolved","impact":"major"}]}`

func statusPageServer(t *testing.T, body string, code int, hits *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withStatusPage(t *testing.T, provider, api string) {
	prev, had := statusPages[provider]
	p := prev
	p.api = api
	statusPages[provider] = p
	t.Cleanup(func() {
		if had {
			statusPages[provider] = prev
		} else {
			delete(statusPages, provider)
		}
	})
}

func TestProviderStatusFromItsStatusPage(t *testing.T) {
	clearEnv(t)
	var hits atomic.Int32
	page := statusPageServer(t, statusSummary, 200, &hits)
	withStatusPage(t, "anthropic", page.URL+"/api/v2/summary.json")
	a := New(nil)
	a.SetConfig(Config{Provider: "anthropic", APIKey: "fake-key-123456"})
	s := a.ProviderStatus(context.Background(), false)
	if s.Source != "statuspage" || s.Indicator != "minor" || s.Description != "Partially Degraded Service" || s.Name == "" || s.Name == "anthropic" {
		t.Fatalf("status: %+v", s)
	}
	if len(s.Components) != 1 || s.Components[0].Name != "Claude API (api.anthropic.com)" || s.Components[0].Status != "degraded_performance" {
		t.Fatalf("only the API component: %+v", s.Components)
	}
	if len(s.Incidents) != 1 || s.Incidents[0].Name != "Elevated errors on Claude API" || s.Incidents[0].URL == "" {
		t.Fatalf("open incidents only: %+v", s.Incidents)
	}
	// en caché un minuto; force consulta de nuevo
	a.ProviderStatus(context.Background(), false)
	if hits.Load() != 1 {
		t.Fatalf("cached: %d hits", hits.Load())
	}
	a.ProviderStatus(context.Background(), true)
	if hits.Load() != 2 {
		t.Fatalf("forced: %d hits", hits.Load())
	}
}

// Si la página no responde o responde otra cosa, se dice (estado desconocido), sin romper nada.
func TestProviderStatusWhenThePageFails(t *testing.T) {
	clearEnv(t)
	for _, c := range []struct {
		body string
		code int
	}{{"", 503}, {"<html>", 200}, {`{"status":{}}`, 200}} {
		page := statusPageServer(t, c.body, c.code, nil)
		withStatusPage(t, "openai", page.URL)
		a := New(nil)
		a.SetConfig(Config{Provider: "openai", APIKey: "fake-key-123456"})
		s := a.ProviderStatus(context.Background(), true)
		if s.Indicator != "unknown" || s.Error == "" || s.PageURL == "" {
			t.Fatalf("%d %q: %+v", c.code, c.body, s)
		}
	}
}

// Gemini no publica un estado consultable: solo el enlace. Ollama: si responde en su dirección.
// Sin IA: nada que consultar.
func TestProviderStatusWithoutAStatusPage(t *testing.T) {
	clearEnv(t)
	a := New(nil)
	a.SetConfig(Config{Provider: "gemini", APIKey: "fake-key-123456"})
	if s := a.ProviderStatus(context.Background(), true); s.Source != "none" || s.Indicator != "unknown" || s.PageURL == "" || s.Error != "" {
		t.Fatalf("gemini: %+v", s)
	}
	ollama := statusPageServer(t, `{"version":"0.12.3"}`, 200, nil)
	a.SetConfig(Config{Provider: "ollama", BaseURL: ollama.URL, Model: "llama3.1"})
	if s := a.ProviderStatus(context.Background(), true); s.Source != "local" || s.Indicator != "none" || s.Description != "0.12.3" {
		t.Fatalf("ollama up: %+v", s)
	}
	ollama.Close()
	if s := a.ProviderStatus(context.Background(), true); s.Indicator != "major" || s.Error == "" {
		t.Fatalf("ollama down: %+v", s)
	}
	off := New(nil)
	if s := off.ProviderStatus(context.Background(), true); s.Provider != "" || s.Source != "none" {
		t.Fatalf("no AI: %+v", s)
	}
}

// TRACEREPORTS_AI_STATUS_URL apunta a otra página (por ejemplo, la de un proveedor compatible).
func TestProviderStatusOverride(t *testing.T) {
	clearEnv(t)
	page := statusPageServer(t, `{"status":{"indicator":"none","description":"All Systems Operational"},"components":[{"name":"API","status":"operational"}]}`, 200, nil)
	t.Setenv("TRACEREPORTS_AI_STATUS_URL", page.URL+"/api/v2/summary.json")
	a := New(nil)
	a.SetConfig(Config{Provider: "openai_compatible", BaseURL: "http://llm.local/v1", Model: "m"})
	s := a.ProviderStatus(context.Background(), true)
	if s.Source != "statuspage" || s.Indicator != "none" || len(s.Components) != 1 || s.PageURL != page.URL {
		t.Fatalf("override: %+v", s)
	}
}
