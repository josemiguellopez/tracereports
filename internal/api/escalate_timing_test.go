package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

type escTiming struct {
	Source string `json:"source"`
	Timing *struct {
		TotalMs    int64 `json:"total_ms"`
		ProviderMs int64 `json:"provider_ms"`
		WaitMs     int64 `json:"wait_ms"`
		OwnMs      int64 `json:"own_ms"`
		Calls      int   `json:"calls"`
		Attempts   []struct {
			Ms        int64  `json:"ms"`
			OK        bool   `json:"ok"`
			Status    int    `json:"status"`
			Reason    string `json:"reason"`
			WaitMs    int64  `json:"wait_ms"`
			WaitAsked bool   `json:"wait_asked"`
		} `json:"attempts"`
	} `json:"timing"`
}

const escReply = `{"choices":[{"message":{"content":"{\"title\":\"t\",\"severity\":\"high\",\"headline\":\"h\",\"what_happened\":\"w\",\"impact\":\"i\",\"evidence\":[\"e\"],\"root_cause\":\"r\",\"next_steps\":[\"n\"],\"owner\":\"o\"}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`

func escalateWith(t *testing.T, srv *Server, runID, testID int64, body string) escTiming {
	t.Helper()
	if body == "" {
		raw, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "dev", "lang": "es", "regenerate": true})
		body = string(raw)
	}
	rec := call(t, srv, "POST", "/api/v1/ui/escalate", body)
	var out escTiming
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("escalate: %d %s", rec.Code, rec.Body)
	}
	return out
}

// Escalar con IA informa cuánto tardó, separando al proveedor (que aquí tarda 250 ms) de
// TraceReports; la plantilla (sin IA) no trae tiempos.
func TestEscalationReportsHowLongTheAITook(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(escReply))
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: llm.URL, Model: "gpt-x"})
	runID, testID := failingTestIn(t, srv, "shop")
	e := escalateWith(t, srv, runID, testID, "")
	tm := e.Timing
	if e.Source != "ai" || tm == nil || tm.Calls != 1 || tm.ProviderMs < 250 || tm.TotalMs < tm.ProviderMs || tm.WaitMs != 0 {
		t.Fatalf("timing: %+v %+v", e, tm)
	}
	if tm.OwnMs != tm.TotalMs-tm.ProviderMs-tm.WaitMs || tm.OwnMs > 2000 {
		t.Fatalf("TraceReports' own time: %+v", tm)
	}
	raw, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "dev", "lang": "es", "no_ai": true})
	if tpl := escalateWith(t, srv, runID, testID, string(raw)); tpl.Source != "template" || tpl.Timing != nil {
		t.Fatalf("template: %+v", tpl)
	}
}

// Con un reintento (el proveedor saturado responde 503), se cuentan las dos llamadas y la pausa.
func TestEscalationTimingCountsRetriesAndPauses(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	var n atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"message":"overloaded"}}`))
			return
		}
		w.Write([]byte(escReply))
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: llm.URL, Model: "gpt-x"})
	runID, testID := failingTestIn(t, srv, "shop")
	tm := escalateWith(t, srv, runID, testID, "").Timing
	if tm == nil || tm.Calls != 2 || tm.WaitMs < 4900 || tm.TotalMs < tm.WaitMs {
		t.Fatalf("retry timing: %+v", tm)
	}
	if at := tm.Attempts; len(at) != 2 || at[0].OK || at[0].Status != 503 || at[0].Reason != "server" || at[0].WaitMs < 4900 || at[0].WaitAsked || !at[1].OK {
		t.Fatalf("each call: %+v", tm.Attempts)
	}
}
