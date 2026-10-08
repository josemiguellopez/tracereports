package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// scriptedProvider answers each request with the next (status, body, headers) and counts them.
// A status 0 holds the request until the client gives up.
type reply struct {
	status int
	body   string
	header map[string]string
}

func scriptedProvider(t *testing.T, replies ...reply) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		rp := replies[min(i, len(replies)-1)]
		if rp.status == 0 {
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second): // el cliente ya se rindió: no bloquear el cierre
			}
			return
		}
		for k, v := range rp.header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rp.status)
		w.Write([]byte(rp.body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

const (
	openAIOK    = `{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`
	anthropicOK = `{"id":"msg_fake","type":"message","role":"assistant","model":"fake-anthropic","content":[{"type":"text","text":"{\"ok\":true}"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`
	overloaded  = `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`
)

// checkCounts compares the usage of today and the timing with what the provider received: one
// call per HTTP attempt, the tokens of the answer, and the provider time not counted twice.
func checkCounts(t *testing.T, a *Analyzer, timing *callTiming, requests int32, calls, errs, untracked, in, out int64) {
	t.Helper()
	u := usageNow(t, a.store).Today
	if int32(u.Calls) != requests || u.Calls != calls || u.Errors != errs || u.Untracked != untracked || u.InputTokens != in || u.OutputTokens != out {
		t.Errorf("requests=%d usage=%+v (want calls=%d errors=%d untracked=%d tokens=%d/%d)", requests, u, calls, errs, untracked, in, out)
	}
	if timing.calls != int(calls) || len(timing.attempts) != int(calls) {
		t.Errorf("timing: calls=%d attempts=%+v", timing.calls, timing.attempts)
	}
	var sum time.Duration
	for _, at := range timing.attempts {
		sum += time.Duration(at.Ms) * time.Millisecond
	}
	if d := timing.provider - sum; d < 0 || d > time.Duration(len(timing.attempts))*time.Millisecond {
		t.Errorf("provider time %v is the sum of its attempts %v", timing.provider, sum)
	}
}

func analyzerFor(t *testing.T, c Config) *Analyzer {
	t.Helper()
	a := New(usageStore(t))
	if err := a.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	return a
}

// API compatible sin json_schema: la solicitud rechazada (400) y la repetida son dos llamadas.
func TestSchemaFallbackCountsBothRequests(t *testing.T) {
	srv, n := scriptedProvider(t, reply{status: 400, body: `{"error":{"message":"json_schema is unsupported"}}`}, reply{status: 200, body: openAIOK})
	a := analyzerFor(t, Config{Provider: "openai_compatible", BaseURL: srv.URL, APIKey: "fake-key-123456", Model: "fake"})
	ctx, timing := withCallTiming(context.Background())
	if _, err := a.generate(ctx, UsageEscalation, "hello", okSchema); err != nil {
		t.Fatal(err)
	}
	checkCounts(t, a, timing, n.Load(), 2, 1, 1, 10, 2)
	if at := timing.attempts; len(at) == 2 && (at[0].OK || at[0].Status != 400 || !at[1].OK) {
		t.Errorf("attempts in order: %+v", at)
	}
	// las dos rechazadas: error terminal, dos llamadas con error
	srv, n = scriptedProvider(t, reply{status: 400, body: `{"error":{"message":"bad request"}}`})
	a = analyzerFor(t, Config{Provider: "openai_compatible", BaseURL: srv.URL, Model: "fake"})
	ctx, timing = withCallTiming(context.Background())
	if _, err := a.generate(ctx, UsageEscalation, "hello", okSchema); err == nil {
		t.Fatal("a rejected request is an error")
	}
	checkCounts(t, a, timing, n.Load(), 2, 2, 2, 0, 0)
}

// Anthropic: cada reintento del SDK es una llamada, y su pausa (la que pidió el proveedor) queda
// como espera entre intentos, no como tiempo de respuesta.
func TestAnthropicSDKRetriesAreCounted(t *testing.T) {
	retry := reply{status: 503, body: overloaded, header: map[string]string{"Retry-After-Ms": "120"}}
	srv, n := scriptedProvider(t, retry, reply{status: 200, body: anthropicOK})
	a := analyzerFor(t, Config{Provider: "anthropic", BaseURL: srv.URL, APIKey: "fake-key-123456", Model: "fake-anthropic"})
	ctx, timing := withCallTiming(context.Background())
	if _, err := a.generate(ctx, UsageEscalation, "hello", okSchema); err != nil {
		t.Fatal(err)
	}
	checkCounts(t, a, timing, n.Load(), 2, 1, 1, 10, 2)
	if at := timing.attempts; len(at) != 2 || at[0].OK || at[0].Status != 503 || at[0].Reason != "server" || !at[0].WaitAsked || at[0].WaitMs < 100 || !at[1].OK {
		t.Errorf("attempts: %+v", timing.attempts)
	}
	if timing.wait < 100*time.Millisecond {
		t.Errorf("the SDK pause is a wait: %v", timing.wait)
	}

	// error terminal: el SDK reintenta 2 veces y TraceReports no reintenta encima
	srv, n = scriptedProvider(t, retry)
	a = analyzerFor(t, Config{Provider: "anthropic", BaseURL: srv.URL, APIKey: "fake-key-123456", Model: "fake-anthropic"})
	ctx, timing = withCallTiming(context.Background())
	if _, err := a.generate(ctx, UsageEscalation, "hello", okSchema); err == nil {
		t.Fatal("overloaded every time is an error")
	}
	checkCounts(t, a, timing, n.Load(), 3, 3, 3, 0, 0)
}

// Cancelada durante la pausa del SDK: el intento que se hizo cuenta, la cancelación no es otra
// llamada. Cancelada con la solicitud en curso: esa solicitud es la llamada (con error).
func TestCancelledAttemptsAreCountedOnce(t *testing.T) {
	srv, n := scriptedProvider(t, reply{status: 503, body: overloaded, header: map[string]string{"Retry-After": "30"}})
	a := analyzerFor(t, Config{Provider: "anthropic", BaseURL: srv.URL, APIKey: "fake-key-123456", Model: "fake-anthropic"})
	base, cancel := context.WithCancel(context.Background())
	ctx, timing := withCallTiming(base)
	go func() {
		for n.Load() == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	if _, err := a.generate(ctx, UsageEscalation, "hello", okSchema); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	checkCounts(t, a, timing, n.Load(), 1, 1, 1, 0, 0)
	if at := timing.attempts; len(at) != 1 || !at[0].WaitAsked || timing.wait < 50*time.Millisecond {
		t.Errorf("the pause until the cancellation is a wait: %+v %v", at, timing.wait)
	}

	srv, n = scriptedProvider(t, reply{status: 0})
	a = analyzerFor(t, Config{Provider: "openai_compatible", BaseURL: srv.URL, Model: "fake"})
	base, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond) // más corto que la espera del proveedor
	defer cancel()
	ctx, timing = withCallTiming(base)
	if _, err := a.generate(ctx, UsageEscalation, "hello", okSchema); err == nil {
		t.Fatal("a request past the deadline is an error")
	}
	checkCounts(t, a, timing, n.Load(), 1, 1, 1, 0, 0)
}
