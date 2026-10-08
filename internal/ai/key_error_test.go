package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// key ficticia con caracteres que cambian al codificarla (URL, base64)
const fakeKey = "sk-FAKE_live+key/with=chars9876"

// keyForms: cómo puede reaparecer la key en un error (tal cual, en una URL, en base64, en Basic).
func testKeyForms(k string) []string {
	return []string{k, url.QueryEscape(k), url.PathEscape(k), base64.StdEncoding.EncodeToString([]byte(k)),
		base64.StdEncoding.EncodeToString([]byte(":" + k)), base64.StdEncoding.EncodeToString([]byte("user:" + k))}
}

func assertNoKey(t *testing.T, where, s string) {
	t.Helper()
	for _, f := range testKeyForms(fakeKey) {
		if strings.Contains(s, f) {
			t.Errorf("%s contains the key (%q): %s", where, f, s)
		}
	}
}

// providerEchoingKey answers like a provider that repeats the credential in its error: the key in
// several forms, the Authorization header it received, and the status asked for.
func providerEchoingKey(t *testing.T, status int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s (auth %q, url %s, b64 %s)"}}`,
			fakeKey, r.Header.Get("Authorization"), url.QueryEscape(fakeKey), base64.StdEncoding.EncodeToString([]byte(":"+fakeKey)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *logBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func captureSlog(t *testing.T) *logBuffer {
	buf := &logBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

func TestProviderErrorsNeverCarryTheAPIKey(t *testing.T) {
	clearEnv(t)
	logs := captureSlog(t)
	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "Pago", "AssertionError: x", "", 0)
	store.FinishRun(runID)

	for _, provider := range []string{"openai", "openai_compatible", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			a := New(store)
			if err := a.SetConfig(Config{Provider: provider, APIKey: fakeKey, BaseURL: providerEchoingKey(t, 401).URL, Model: "m"}); err != nil {
				t.Fatal(err)
			}
			// diagnóstico de un test: ai_triage.error y log
			a.ReanalyzeAsync(id)
			waitAll(t, a)
			got, _ := store.GetTest(id)
			if got.Triage == nil || got.Triage.State != "ERROR" {
				t.Fatalf("triage: %+v", got.Triage)
			}
			assertNoKey(t, "ai_triage.error", got.Triage.Error)
			if !strings.Contains(got.Triage.Error, "HTTP 401") {
				t.Errorf("the useful part is kept: %s", got.Triage.Error)
			}
			// Probar conexión
			_, err := a.Test(context.Background(), a.Config())
			if err == nil {
				t.Fatal("test connection must fail")
			}
			assertNoKey(t, "test connection error", err.Error())
			// escalamiento con IA: ai_error
			facts, _ := BuildFacts(store, t.TempDir(), runID, id)
			e := a.Escalate(context.Background(), facts, runID, id, "dev", "es")
			assertNoKey(t, "escalation ai_error", e.AIError)
			// resumen de la ejecución
			done := make(chan struct{})
			a.AnalyzeRunAsync(runID, func() { close(done) })
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("run summary did not finish")
			}
			waitAll(t, a)
			rt, _ := store.GetRunTriage(runID)
			if rt != nil {
				assertNoKey(t, "run_triage.error", rt.Error)
			}
		})
	}
	assertNoKey(t, "logs", logs.String())
}

// Un error de transporte puede traer la URL con la key (proxy con ?api-key=…): tampoco sale.
func TestTransportErrorsNeverCarryTheAPIKey(t *testing.T) {
	clearEnv(t)
	a := New(nil)
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("proxy refused %s with %s", r.URL.String(), r.Header.Get("Authorization"))
	})}
	c := Config{Provider: "openai_compatible", APIKey: fakeKey, BaseURL: "https://proxy.test/v1?api-key=" + url.QueryEscape(fakeKey), Model: "m"}
	_, err := a.Test(context.Background(), c)
	if err == nil {
		t.Fatal("must fail")
	}
	assertNoKey(t, "transport error", err.Error())
	if !strings.Contains(err.Error(), "proxy refused") {
		t.Errorf("useful context kept: %s", err)
	}
}

// La key que se protege es la de esa solicitud, aunque la configuración cambie mientras tanto.
func TestTheKeyOfTheRequestIsScrubbedEvenIfConfigChanges(t *testing.T) {
	clearEnv(t)
	a := New(nil)
	release := make(chan struct{})
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-release
		return &http.Response{StatusCode: 401, Header: http.Header{}, Request: r,
			Body: ioNop(`{"error":{"message":"bad key ` + fakeKey + `"}}`)}, nil
	})}
	c := Config{Provider: "openai", APIKey: fakeKey, BaseURL: "https://api.test/v1", Model: "m"}
	errc := make(chan error, 1)
	go func() { _, err := a.Test(context.Background(), c); errc <- err }()
	a.SetConfig(Config{Provider: "openai", APIKey: "sk-other-key-0000", BaseURL: "https://api.test/v1", Model: "m"})
	close(release)
	err := <-errc
	assertNoKey(t, "error after a config change", err.Error())
	// la clasificación HTTP se conserva
	var se *httpStatusError
	if !errors.As(err, &se) || se.code != 401 {
		t.Errorf("HTTP classification lost: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func ioNop(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

func TestScrubKeyInsideBase64WithAnyPrefix(t *testing.T) {
	for _, user := range []string{"", "u", "us", "usr", "any-user", "x"} {
		enc := base64.StdEncoding.EncodeToString([]byte(user + ":" + fakeKey))
		out := scrubKey("Authorization: Basic "+enc, fakeKey)
		if strings.Contains(out, enc) {
			t.Errorf("user %q: %s", user, out)
		}
	}
	if scrubKey("nothing secret here", fakeKey) != "nothing secret here" {
		t.Error("text without the key is unchanged")
	}
}

// Un HTTP 200 cuyo contenido repite la key: tampoco queda en el diagnóstico guardado ni en el
// error de "no es JSON" (que recorta el texto: se quita antes del recorte).
func TestSuccessfulResponsesNeverCarryTheAPIKey(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "Pago", "AssertionError: x", "", 0)
	content := ""
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer mock.Close()
	a := New(store)
	if err := a.SetConfig(Config{Provider: "openai", APIKey: fakeKey, BaseURL: mock.URL, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range testKeyForms(fakeKey) {
		content = "not json " + f
		_, err := a.Test(context.Background(), a.Config())
		if err == nil || !strings.Contains(err.Error(), "JSON") {
			t.Fatalf("parse error kept: %v", err)
		}
		assertNoKey(t, "parse error", err.Error())
	}
	raw, _ := json.Marshal(map[string]any{"category": "PRODUCT_BUG", "confidence": 0.9,
		"summary": "token " + fakeKey, "evidence": []string{fakeKey}, "suggested_fix": "rotate " + fakeKey})
	content = string(raw)
	a.ReanalyzeAsync(id)
	waitAll(t, a)
	got, _ := store.GetTest(id)
	if got.Triage == nil || got.Triage.State != "DONE" {
		t.Fatalf("triage: %+v", got.Triage)
	}
	b, _ := json.Marshal(got.Triage)
	assertNoKey(t, "stored diagnosis", string(b))
}
