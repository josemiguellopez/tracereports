package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var okSchema = map[string]any{
	"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"},
}

// mockProvider records the request and answers with body.
func mockProvider(t *testing.T, status int, body string, got *map[string]any, hdr *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if got != nil {
			*got = map[string]any{"_path": r.URL.Path}
			_ = json.Unmarshal(raw, got)
			(*got)["_path"] = r.URL.Path
		}
		if hdr != nil {
			*hdr = r.Header.Clone()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProvidersRoundTrip(t *testing.T) {
	cases := []struct {
		name, provider, key, reply string
		check                      func(t *testing.T, req map[string]any, h http.Header)
	}{
		{"gemini", "gemini", "g-key", `{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]}}]}`,
			func(t *testing.T, req map[string]any, h http.Header) {
				if h.Get("x-goog-api-key") != "g-key" || !strings.HasSuffix(req["_path"].(string), ":generateContent") {
					t.Errorf("gemini request: %v %v", req["_path"], h)
				}
				schema := req["generationConfig"].(map[string]any)["responseSchema"].(map[string]any)
				if schema["type"] != "OBJECT" || schema["additionalProperties"] != nil {
					t.Errorf("gemini schema must be UPPERCASE without additionalProperties: %v", schema)
				}
			}},
		{"openai", "openai", "o-key", `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`,
			func(t *testing.T, req map[string]any, h http.Header) {
				if h.Get("Authorization") != "Bearer o-key" || req["_path"] != "/chat/completions" {
					t.Errorf("openai request: %v %v", req["_path"], h.Get("Authorization"))
				}
				rf := req["response_format"].(map[string]any)
				schema := rf["json_schema"].(map[string]any)["schema"].(map[string]any)
				if rf["type"] != "json_schema" || schema["additionalProperties"] != false {
					t.Errorf("openai response_format: %v", rf)
				}
			}},
		{"ollama", "ollama", "", `{"message":{"content":"{\"ok\":true}"}}`,
			func(t *testing.T, req map[string]any, h http.Header) {
				if req["_path"] != "/api/chat" || req["stream"] != false || req["format"] == nil {
					t.Errorf("ollama request: %v", req)
				}
			}},
		{"anthropic", "anthropic", "a-key",
			`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"{\"ok\":true}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
			func(t *testing.T, req map[string]any, h http.Header) {
				if h.Get("X-Api-Key") != "a-key" || req["_path"] != "/v1/messages" {
					t.Errorf("anthropic request: %v %v", req["_path"], h)
				}
				format := req["output_config"].(map[string]any)["format"].(map[string]any)
				if format["type"] != "json_schema" || format["schema"].(map[string]any)["additionalProperties"] != false {
					t.Errorf("anthropic output_config: %v", format)
				}
				if req["fallbacks"] != "default" || !strings.Contains(h.Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
					t.Errorf("claude-opus-5-5 should opt into the default refusal fallback: %v %v", req["fallbacks"], h.Get("Anthropic-Beta"))
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req map[string]any
			var h http.Header
			srv := mockProvider(t, 200, tc.reply, &req, &h)
			a := New(nil)
			if err := a.SetConfig(Config{Provider: tc.provider, APIKey: tc.key, BaseURL: srv.URL}); err != nil {
				t.Fatal(err)
			}
			text, err := a.generate(context.Background(), "hola", okSchema)
			if err != nil {
				t.Fatal(err)
			}
			if text != `{"ok":true}` {
				t.Fatalf("text = %q", text)
			}
			tc.check(t, req, h)
		})
	}
}

func TestOpenAICompatibleFallsBackToJSONObject(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["response_format"].(map[string]any)["type"] == "json_schema" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"response_format json_schema not supported"}}`))
			return
		}
		msgs := req["messages"].([]any)
		if !strings.Contains(msgs[0].(map[string]any)["content"].(string), "JSON Schema") {
			t.Error("fallback prompt should carry the schema")
		}
		w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"ok\\\":true}\\n```\"}}]}"))
	}))
	defer srv.Close()
	a := New(nil)
	if err := a.SetConfig(Config{Provider: "openai_compatible", Model: "llama-3.3-70b", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	text, err := a.generate(context.Background(), "hola", okSchema)
	if err != nil || text != `{"ok":true}` || calls != 2 {
		t.Fatalf("text=%q err=%v calls=%d", text, err, calls)
	}
}

func TestConfigFromEnvAndValidate(t *testing.T) {
	for _, k := range []string{"AI_PROVIDER", "AI_MODEL", "AI_API_KEY", "AI_BASE_URL", "GEMINI_API_KEY", "GEMINI_MODEL", "GEMINI_BASE_URL",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OLLAMA_HOST"} {
		t.Setenv(k, "")
	}
	if c := ConfigFromEnv(); c.Provider != "" {
		t.Fatalf("no env -> AI off, got %+v", c)
	}
	t.Setenv("ANTHROPIC_API_KEY", "k")
	if c := ConfigFromEnv(); c.Provider != "anthropic" || c.Model != "claude-opus-5-5" || c.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("anthropic inferred: %+v", c)
	}
	t.Setenv("AI_PROVIDER", "ollama")
	t.Setenv("OLLAMA_HOST", "gpu-box:11434")
	if c := ConfigFromEnv(); c.Provider != "ollama" || c.BaseURL != "http://gpu-box:11434" || c.Validate() != nil {
		t.Fatalf("ollama: %+v %v", c, c.Validate())
	}
	if err := (Config{Provider: "openai"}).Validate(); err == nil {
		t.Fatal("openai without key must fail")
	}
	if err := (Config{Provider: "openai_compatible", Model: "x"}).Validate(); err == nil {
		t.Fatal("openai_compatible without base URL must fail")
	}
	if err := (Config{Provider: "nope"}).Validate(); err == nil {
		t.Fatal("unknown provider must fail")
	}
}

func TestLanguageInstruction(t *testing.T) {
	if !strings.Contains(languageInstruction("en"), "English") || !strings.Contains(languageInstruction("es"), "Spanish") ||
		!strings.Contains(languageInstruction(""), "same language") {
		t.Fatal("language instruction mismatch")
	}
}

func TestHintsAndRetryPolicy(t *testing.T) {
	c := Config{Provider: "gemini", Model: "gemini-3.8-flash", APIKey: "k"}
	quota := &httpStatusError{provider: "gemini", code: 429, body: "Quota exceeded for metric: ...free_tier_requests, limit: 20. Please retry in 23h56m16s."}
	perMinute := &httpStatusError{provider: "gemini", code: 429, body: "Resource has been exhausted (e.g. check quota)."}
	gone := &httpStatusError{provider: "gemini", code: 404, body: "This model models/gemini-2.5-flash is no longer available to new users."}
	busy := &httpStatusError{provider: "gemini", code: 503, body: "This model is currently experiencing high demand."}
	if quota.retryable() || !perMinute.retryable() || gone.retryable() || !busy.retryable() {
		t.Fatal("retry policy: daily quota and 404 must not be retried; per-minute 429 and 503 must")
	}
	for _, tc := range []struct {
		err        error
		code, sugg string
	}{
		{quota, "quota_daily", "gemini-flash-lite-latest"}, {perMinute, "rate_limit", ""},
		{gone, "model_unavailable", "gemini-flash-lite-latest"}, {busy, "overloaded", "gemini-flash-lite-latest"},
	} {
		if code, sugg := Hint(tc.err, c); code != tc.code || sugg != tc.sugg {
			t.Errorf("%v -> %s %s", tc.err, code, sugg)
		}
	}
	// si ya usa el modelo por defecto, no se lo sugiere a sí mismo
	if _, sugg := Hint(busy, Config{Provider: "gemini", APIKey: "k"}); sugg != "" {
		t.Errorf("should not suggest the same default model: %q", sugg)
	}
	if !strings.Contains(HintText(quota, c), "cuota diaria") {
		t.Error("hint text for the saved triage error")
	}
}

func TestNoCreditsIsNotRetried(t *testing.T) {
	oa := &httpStatusError{provider: "openai", code: 429, body: "You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/."}
	an := &httpStatusError{provider: "anthropic", code: 400, body: "Your credit balance is too low to access the Anthropic API."}
	if oa.retryable() {
		t.Fatal("no credits must not be retried")
	}
	for _, e := range []error{oa, an} {
		if code, sugg := Hint(e, Config{Provider: "openai", Model: "gpt-5", APIKey: "k"}); code != "no_credits" || sugg != "" {
			t.Errorf("%v -> %s %q", e, code, sugg)
		}
	}
}
