package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Ajustes → Uso de la IA: Probar conexión se cuenta y el endpoint devuelve los totales, el
// desglose y el límite por ejecución; sin uso, todo en cero y listas vacías (no null).
func TestAIUsageEndpoint(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	var out struct {
		Usage struct {
			Today struct {
				Calls, InputTokens, OutputTokens int64
			} `json:"today"`
			ByModel []map[string]any `json:"by_model"`
			ByKind  []map[string]any `json:"by_kind"`
			Daily   []map[string]any `json:"daily"`
		} `json:"usage"`
		MaxPerRun int `json:"max_per_run"`
	}
	rec := call(t, srv, "GET", "/api/v1/settings/ai/usage", "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Usage.ByModel == nil || out.Usage.Daily == nil {
		t.Fatalf("empty usage: %d %s", rec.Code, rec.Body)
	}
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`))
	}))
	defer llm.Close()
	if r := call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"openai","api_key":"fake-key-123456","base_url":"`+llm.URL+`","model":"gpt-x"}`); r.Code != 200 {
		t.Fatalf("test connection: %d %s", r.Code, r.Body)
	}
	rec = call(t, srv, "GET", "/api/v1/settings/ai/usage", "")
	out = struct {
		Usage struct {
			Today struct {
				Calls, InputTokens, OutputTokens int64
			} `json:"today"`
			ByModel []map[string]any `json:"by_model"`
			ByKind  []map[string]any `json:"by_kind"`
			Daily   []map[string]any `json:"daily"`
		} `json:"usage"`
		MaxPerRun int `json:"max_per_run"`
	}{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Usage.Today.Calls != 1 || len(out.Usage.ByKind) != 1 || out.Usage.ByKind[0]["kind"] != "test" || out.Usage.ByModel[0]["model"] != "gpt-x" {
		t.Fatalf("after a test connection: %s", rec.Body)
	}
	if out.MaxPerRun != srv.AI.MaxPerRun {
		t.Fatalf("max per run: %d", out.MaxPerRun)
	}
}
