package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func usageStore(t *testing.T) *db.Store {
	t.Helper()
	s, err := db.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func usageNow(t *testing.T, s *db.Store) *db.AIUsageReport {
	t.Helper()
	u, err := s.AIUsage(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Cada proveedor informa sus tokens a su manera: se cuentan los de entrada y salida que informa.
func TestUsageReportsTheTokensOfEachProvider(t *testing.T) {
	cases := []struct {
		provider, reply string
		in, out         int64
		known           bool
	}{
		{"gemini", `{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]}}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":30,"thoughtsTokenCount":10}}`, 120, 40, true},
		{"openai", `{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":200,"completion_tokens":50}}`, 200, 50, true},
		{"openai_compatible", `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`, 0, 0, false},
		{"ollama", `{"message":{"content":"{\"ok\":true}"},"prompt_eval_count":80,"eval_count":20}`, 80, 20, true},
		{"anthropic", `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"{\"ok\":true}"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":25,"cache_creation_input_tokens":7,"cache_read_input_tokens":3}}`, 110, 25, true},
	}
	for _, c := range cases {
		t.Run(c.provider, func(t *testing.T) {
			store := usageStore(t)
			srv := mockProvider(t, 200, c.reply, nil, nil)
			a := New(store)
			if err := a.SetConfig(Config{Provider: c.provider, APIKey: "fake-key-123456", BaseURL: srv.URL, Model: "m-1"}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.generate(context.Background(), UsageTriage, "hola", okSchema); err != nil {
				t.Fatal(err)
			}
			u := usageNow(t, store)
			untracked := int64(0)
			if !c.known {
				untracked = 1
			}
			want := [5]int64{1, 0, c.in, c.out, untracked}
			got := func(t db.AIUsageTotals) [5]int64 {
				return [5]int64{t.Calls, t.Errors, t.InputTokens, t.OutputTokens, t.Untracked}
			}
			if got(u.Today) != want || got(u.Last7) != want || got(u.Last30) != want {
				t.Fatalf("totals: %+v, want %+v", u.Today, want)
			}
			if len(u.ByModel) != 1 || u.ByModel[0].Provider != c.provider || u.ByModel[0].Model != "m-1" {
				t.Fatalf("by model: %+v", u.ByModel)
			}
		})
	}
}

// Los reintentos cuentan como llamadas, los errores se cuentan, y una respuesta HTTP 200 que
// el modelo rechaza informa igual sus tokens.
func TestUsageCountsRetriesErrorsAndRefusals(t *testing.T) {
	store := usageStore(t)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch n.Add(1) {
		case 1:
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"message":"overloaded"}}`))
		case 2:
			w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`))
		default:
			w.Write([]byte(`{"choices":[{"message":{"content":"","refusal":"no"}}],"usage":{"prompt_tokens":7,"completion_tokens":1}}`))
		}
	}))
	defer srv.Close()
	a := New(store)
	a.SetConfig(Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: srv.URL, Model: "gpt-x"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := a.generate(ctx, UsageRunSummary, "hola", okSchema); err != nil { // 503 y reintento
		t.Fatal(err)
	}
	if _, err := a.generate(ctx, UsageEscalation, "hola", okSchema); err == nil { // rechazo
		t.Fatal("refusal must fail")
	}
	u := usageNow(t, store)
	if u.Today.Calls != 3 || u.Today.Errors != 2 || u.Today.InputTokens != 17 || u.Today.OutputTokens != 3 || u.Today.Untracked != 1 {
		t.Fatalf("totals: %+v", u.Today)
	}
	kinds := map[string]db.AIUsageTotals{}
	if u.Today.ErrorsBy["server"] != 1 || u.Today.ErrorsBy["other"] != 1 {
		t.Fatalf("errors by reason: %+v", u.Today.ErrorsBy)
	}
	for _, k := range u.ByKind {
		kinds[k.Kind] = k.AIUsageTotals
	}
	if kinds[UsageRunSummary].Calls != 2 || kinds[UsageRunSummary].Errors != 1 || kinds[UsageEscalation].Calls != 1 {
		t.Fatalf("by kind: %+v", u.ByKind)
	}
}

// Probar conexión también se cuenta (tipo "test"); sin store no se registra nada.
func TestUsageOfTestConnection(t *testing.T) {
	store := usageStore(t)
	srv := mockProvider(t, 200, `{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`, nil, nil)
	a := New(store)
	c := Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: srv.URL, Model: "gpt-x"}
	if _, err := a.Test(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	u := usageNow(t, store)
	if len(u.ByKind) != 1 || u.ByKind[0].Kind != UsageTest || u.ByKind[0].Calls != 1 {
		t.Fatalf("test connection: %+v", u.ByKind)
	}
	if _, err := New(nil).Test(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

// El tiempo de respuesta del proveedor se acumula: la respuesta media refleja cuánto tardó él.
func TestUsageRecordsTheProviderResponseTime(t *testing.T) {
	store := usageStore(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer slow.Close()
	a := New(store)
	c := Config{Provider: "openai", APIKey: "fake-key-123456", BaseURL: slow.URL, Model: "gpt-x"}
	for i := 0; i < 3; i++ {
		if _, err := a.Test(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	u := usageNow(t, store)
	if u.Today.Calls != 3 || u.Today.AvgMs < 150 || u.Today.AvgMs > 1000 || u.Today.DurationMs < 450 {
		t.Fatalf("response time: %+v", u.Today)
	}
	if u.ByModel[0].AvgMs < 150 || u.Daily[0].AvgMs < 150 {
		t.Fatalf("per row: %+v %+v", u.ByModel, u.Daily)
	}
}
