package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Un LLM local compatible con OpenAI (llama.cpp server, llamafile, LM Studio, vLLM, LocalAI): sin
// API key, en /v1/chat/completions, a veces con el JSON entre ``` y con "usage". El diagnóstico
// funciona igual que con OpenAI, y el motor local corre primero.
func TestLocalOpenAICompatibleServer(t *testing.T) {
	clearEnv(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("ruta: %s", r.URL.Path)
		}
		if h := r.Header.Get("Authorization"); h != "" && h != "Bearer " {
			t.Errorf("un servidor local sin key no recibe Authorization: %q", h)
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string
			Messages []struct{ Content string }
		}
		json.Unmarshal(raw, &req)
		if req.Model != "local" || len(req.Messages) == 0 || !strings.Contains(req.Messages[0].Content, "local rules engine") {
			t.Errorf("petición: %s", raw)
		}
		// como llama.cpp con algunos modelos: el JSON entre ```json ... ```
		content := "```json\n{\"category\":\"BACKEND_TIMEOUT\",\"summary\":\"The orders API failed.\",\"suggestion\":\"Check the orders service.\",\"headline\":\"h\",\"summary_run\":\"s\",\"incidents\":[]}\n```"
		resp, _ := json.Marshal(map[string]any{
			"object":  "chat.completion",
			"model":   "local",
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}},
			"usage":   map[string]any{"prompt_tokens": 512, "completion_tokens": 40, "total_tokens": 552},
		})
		w.Header().Set("Content-Type", "application/json")
		w.Write(resp)
	}))
	defer srv.Close()

	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "pays", "AssertionError: order not confirmed", "", 1_000)
	store.FinishRun(runID)
	a := New(store)
	if err := a.SetConfig(Config{Provider: "openai_compatible", Model: "local", BaseURL: srv.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	a.AnalyzeAsync(id)
	waitAll(t, a)
	got, _ := store.GetTest(id)
	if got.Triage == nil || got.Triage.State != "DONE" || got.Triage.Category != "BACKEND_TIMEOUT" || got.Triage.Provider != "openai_compatible" {
		t.Fatalf("%+v", got.Triage)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("el servidor local no recibió el pedido")
	}
}
