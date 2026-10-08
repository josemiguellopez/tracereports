package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

func clearAIEnv(t *testing.T) {
	for _, k := range []string{"AI_PROVIDER", "AI_MODEL", "AI_API_KEY", "AI_BASE_URL", "GEMINI_API_KEY", "GEMINI_MODEL",
		"GEMINI_BASE_URL", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OLLAMA_HOST"} {
		t.Setenv(k, "")
	}
}

type reqOpt func(*http.Request)

func fromAddr(a string) reqOpt  { return func(r *http.Request) { r.RemoteAddr = a } }
func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func basicAuth(u, p string) reqOpt {
	return func(r *http.Request) { r.SetBasicAuth(u, p) }
}

func call(t *testing.T, srv *Server, method, path, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000" // por defecto: mismo equipo
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func TestSettingsAccessRules(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	body := `{"language":"en"}`

	if rec := call(t, srv, "PUT", "/api/v1/settings", body); rec.Code != 200 {
		t.Fatalf("loopback without auth should edit: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", body, fromAddr("10.0.0.5:4000")); rec.Code != 403 {
		t.Fatalf("remote without auth must be read-only: %d", rec.Code)
	}
	// X-Forwarded-For no engaña: se mira el socket real
	if rec := call(t, srv, "PUT", "/api/v1/settings", body, fromAddr("10.0.0.5:4000"), header("X-Forwarded-For", "127.0.0.1")); rec.Code != 403 {
		t.Fatalf("spoofed X-Forwarded-For must not grant access: %d", rec.Code)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", body, header("Content-Type", "text/plain")); rec.Code != 415 {
		t.Fatalf("non-JSON write must be rejected (CSRF): %d", rec.Code)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", body, header("Origin", "https://evil.example")); rec.Code != 403 {
		t.Fatalf("cross-origin write must be rejected: %d", rec.Code)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", body, header("Origin", "http://example.com")); rec.Code != 200 {
		t.Fatalf("same-origin write should pass (httptest Host is example.com): %d", rec.Code)
	}

	// con token y login: la UI edita con su login aunque no tenga el token
	srv.Auth = Auth{Token: "tok", UIUser: "qa", UIPass: "pw"}
	if rec := call(t, srv, "PUT", "/api/v1/settings", body, fromAddr("10.0.0.5:4000"), basicAuth("qa", "pw")); rec.Code != 200 {
		t.Fatalf("UI login should edit settings: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`, basicAuth("qa", "pw")); rec.Code != 401 {
		t.Fatalf("the settings exemption must not open other writes: %d", rec.Code)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", body, fromAddr("10.0.0.5:4000"), header("Authorization", "Bearer tok")); rec.Code != 200 {
		t.Fatalf("API token should edit settings: %d", rec.Code)
	}
	// solo token (sin login de UI): desde otro equipo no se puede sin el token
	srv.Auth = Auth{Token: "tok"}
	if rec := call(t, srv, "PUT", "/api/v1/settings", body, fromAddr("10.0.0.5:4000")); rec.Code != 403 {
		t.Fatalf("remote without token must be read-only: %d", rec.Code)
	}
	srv.Auth = Auth{}
	srv.SettingsLocked = true
	rec := call(t, srv, "PUT", "/api/v1/settings", body)
	if rec.Code != 403 {
		t.Fatalf("locked settings must be read-only: %d", rec.Code)
	}
	var view map[string]any
	json.Unmarshal(call(t, srv, "GET", "/api/v1/settings", "").Body.Bytes(), &view)
	if view["editable"] != false || view["edit_reason"] != "locked" {
		t.Fatalf("GET should report locked: %v", view)
	}
}

func TestSettingsAIProviderLifecycle(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Secrets = masterBox(t, testMasterKey)         // guardar una key desde la UI exige la clave maestra
	t.Setenv("GEMINI_API_KEY", "env-gemini-key-1234") // después: newTestServer la vacía
	srv.AI = ai.New(srv.Store)
	if err := srv.LoadSettings(); err != nil {
		t.Fatal(err)
	}

	get := func() map[string]any {
		var v map[string]any
		json.Unmarshal(call(t, srv, "GET", "/api/v1/settings", "").Body.Bytes(), &v)
		return v
	}

	// cambiar a Ollama desde la UI
	rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"ollama","model":"qwen2.5:7b"}}`)
	if rec.Code != 200 || srv.AI.Provider() != "ollama" || srv.AI.Model() != "qwen2.5:7b" || !srv.AI.Enabled() {
		t.Fatalf("switch to ollama: %d %s cfg=%+v", rec.Code, rec.Body, srv.AI.Config())
	}
	// OpenAI sin key: error claro y no se aplica
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai"}}`); rec.Code != 400 || srv.AI.Provider() != "ollama" {
		t.Fatalf("openai without key must fail: %d %s", rec.Code, rec.Body)
	}
	// con key nueva: se guarda pero nunca se devuelve
	rec = call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","api_key":"sk-test-abcdef9876"}}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "sk-test-abcdef9876") {
		t.Fatalf("key must be accepted and never echoed: %d %s", rec.Code, rec.Body)
	}
	v := get()
	aiv := v["ai"].(map[string]any)
	if aiv["key_hint"] != "••••9876" || aiv["key_source"] != "ui" || v["ai_source"] != "ui" {
		t.Fatalf("view: %v", v)
	}
	// cambiar solo el modelo conserva la key guardada
	call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","model":"gpt-5"}}`)
	if c := srv.AI.Config(); c.APIKey != "sk-test-abcdef9876" || c.Model != "gpt-5" {
		t.Fatalf("model change lost the key: %+v", c)
	}
	// volver a Gemini sin escribir key: usa la del .env y olvida la de OpenAI
	call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"gemini"}}`)
	saved, _ := srv.Store.Settings()
	if c := srv.AI.Config(); c.APIKey != "env-gemini-key-1234" || saved["ai.api_key"] != "" {
		t.Fatalf("gemini should take the env key: %+v saved=%v", c, saved)
	}
	// persistencia: un servidor nuevo sobre la misma base aplica lo guardado
	call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"ollama"},"ai_language":"en"}`)
	srv2 := &Server{Store: srv.Store, AI: ai.New(srv.Store), Secrets: srv.Secrets}
	if err := srv2.LoadSettings(); err != nil || srv2.AI.Provider() != "ollama" {
		t.Fatalf("reload: %v %+v", err, srv2.AI.Config())
	}
	// apagar y volver al .env
	call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"off"}}`)
	if srv.AI.Enabled() {
		t.Fatal("AI should be off")
	}
	if rec := call(t, srv, "DELETE", "/api/v1/settings/ai", `{}`); rec.Code != 200 || srv.AI.Provider() != "gemini" {
		t.Fatalf("reset to env: %d %+v", rec.Code, srv.AI.Config())
	}
	if get()["ai_source"] != "env" || get()["ai_language"] != "en" {
		t.Fatalf("after reset: %v", get())
	}
}

func TestSettingsTestConnectionAndMetrics(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"message":{"content":"{\"ok\":true}"}}`))
	}))
	defer mock.Close()
	var res map[string]any
	rec := call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"ollama","base_url":"`+mock.URL+`"}`)
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res["ok"] != true {
		t.Fatalf("test connection: %s", rec.Body)
	}
	if srv.AI.Enabled() {
		t.Fatal("testing a provider must not apply it")
	}
	rec = call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"anthropic"}`)
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res["ok"] != false || !strings.Contains(res["error"].(string), "API key") {
		t.Fatalf("missing key should be reported: %s", rec.Body)
	}
	if rec := call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"ollama"}`, fromAddr("10.1.1.1:1")); rec.Code != 403 {
		t.Fatalf("testing a provider is a write (outbound request): %d", rec.Code)
	}

	rec = call(t, srv, "GET", "/api/v1/metrics?days=7", "")
	var m map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m["days"] != float64(7) {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body)
	}
	// período móvil: toca 8 fechas (el primer día parcial incluido), 7 si empieza a medianoche
	if n := len(m["daily"].([]any)); n != 7 && n != 8 {
		t.Fatalf("daily series should cover the period: %v", m["daily"])
	}
}

func TestCheckToken(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	get := func(opts ...reqOpt) map[string]any {
		var v map[string]any
		json.Unmarshal(call(t, srv, "GET", "/api/v1/auth/check", "", opts...).Body.Bytes(), &v)
		return v
	}
	if v := get(); v["token_required"] != false || v["token_valid"] != false {
		t.Fatalf("no token configured: %v", v)
	}
	srv.Auth = Auth{Token: "s3cret"}
	if v := get(header("Authorization", "Bearer s3cret")); v["token_required"] != true || v["token_valid"] != true {
		t.Fatalf("valid token: %v", v)
	}
	if v := get(header("X-TraceReports-Token", "nope")); v["token_valid"] != false || v["token_sent"] != true {
		t.Fatalf("wrong token: %v", v)
	}
	var st map[string]any
	json.Unmarshal(call(t, srv, "GET", "/api/v1/settings", "").Body.Bytes(), &st)
	if st["token_set"] != true || st["ui_login"] != false || strings.Contains(fmt.Sprint(st), "s3cret") {
		t.Fatalf("settings must report the token without revealing it: %v", st)
	}
}
