package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAfterIsRead(t *testing.T) {
	h := func(k, v string) http.Header { x := http.Header{}; x.Set(k, v); return x }
	gemini := []byte(`{"error":{"code":429,"message":"Resource exhausted","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure"},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"29s"}]}}`)
	cases := []struct {
		h    http.Header
		body []byte
		want time.Duration
	}{
		{h("Retry-After", "7"), nil, 7 * time.Second},
		{h("Retry-After", "1.5"), nil, 1500 * time.Millisecond},
		{h("retry-after-ms", "250"), nil, 250 * time.Millisecond},
		{http.Header{}, gemini, 29 * time.Second},
		{http.Header{}, []byte(`{"error":{"message":"x"}}`), 0},
		{h("Retry-After", "soon"), nil, 0},
	}
	for _, c := range cases {
		if got := retryAfter(c.h, c.body); got != c.want {
			t.Errorf("%v %s: %v, want %v", c.h, c.body, got, c.want)
		}
	}
	date := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if got := retryAfter(h("Retry-After", date), nil); got < time.Second || got > 4*time.Second {
		t.Errorf("date: %v", got)
	}
}

// Gemini pide esperar 1 s con RetryInfo: se espera eso (no los 5 s fijos) y queda anotado que lo
// pidió el proveedor; cada llamada queda con su estado y motivo.
func TestRetryWaitsWhatTheProviderAsks(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if n.Add(1) == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"code":429,"message":"Resource exhausted","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"1s"}]}}`))
			return
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()
	a := New(nil)
	a.SetConfig(Config{Provider: "gemini", APIKey: "fake-key-123456", BaseURL: srv.URL, Model: "gemini-flash-lite-latest"})
	ctx, timing := withCallTiming(context.Background())
	start := time.Now()
	if _, err := a.generate(ctx, UsageEscalation, "hola", okSchema); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < time.Second || d > 4*time.Second {
		t.Fatalf("waited %v (asked 1 s, default would be 5 s)", d)
	}
	at := timing.attempts
	if len(at) != 2 || at[0].OK || at[0].Status != 429 || at[0].Reason != "rate_limit" || !at[0].WaitAsked || at[0].WaitMs < 990 || !at[1].OK {
		t.Fatalf("attempts: %+v", at)
	}
}

// Si el proveedor pide esperar más que el máximo, o la espera no cabe antes del tiempo límite,
// no se espera: falla ya.
func TestRetryDoesNotWaitTooLong(t *testing.T) {
	for name, c := range map[string]struct {
		delay   string
		timeout time.Duration
	}{
		"asks too much":      {"600s", time.Minute},
		"beyond the timeout": {"20s", 3 * time.Second},
	} {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"code":429,"message":"Resource exhausted","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"` + c.delay + `"}]}}`))
		}))
		a := New(nil)
		a.SetConfig(Config{Provider: "gemini", APIKey: "fake-key-123456", BaseURL: srv.URL, Model: "m"})
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		start := time.Now()
		_, err := a.generate(ctx, UsageTriage, "hola", okSchema)
		cancel()
		srv.Close()
		if err == nil || n.Load() != 1 || time.Since(start) > 2*time.Second {
			t.Fatalf("%s: err=%v calls=%d took=%v", name, err, n.Load(), time.Since(start))
		}
	}
}

func TestFailureReason(t *testing.T) {
	cases := map[string]error{
		"rate_limit": &httpStatusError{code: 429},
		"server":     &httpStatusError{code: 503},
		"auth":       &httpStatusError{code: 401},
		"other":      &httpStatusError{code: 400},
		"timeout":    context.DeadlineExceeded,
	}
	for want, err := range cases {
		if got := FailureReason(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
	if FailureReason(nil) != "" {
		t.Error("no error, no reason")
	}
	// un error de Anthropic (su SDK ya reintentó) no se reintenta de nuevo, pero se clasifica igual
	se := &httpStatusError{provider: "anthropic", code: 429, noRetry: true}
	if se.retryable() || FailureReason(se) != "rate_limit" {
		t.Error("anthropic 429")
	}
	// el error sin la key conserva la espera pedida y el no-reintento
	clean := scrubError(&httpStatusError{provider: "gemini", code: 429, body: "key fake-key-123456", retryAfter: time.Second, noRetry: true}, "fake-key-123456")
	if s, ok := clean.(*httpStatusError); !ok || s.retryAfter != time.Second || !s.noRetry {
		t.Fatalf("scrubbed: %#v", clean)
	}
}
