package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

// key ficticia larga: con el recorte del mensaje (120 caracteres) una parte quedaría afuera del
// reemplazo si se recortara antes de quitarla
var longFakeKey = "sk-FAKE-long-" + strings.Repeat("Ab3/+x", 30) + "-end"

// keyLeaks reports any piece of the key (12 characters or more) left in s.
func keyLeaks(s, key string) string {
	for i := 0; i+12 <= len(key); i++ {
		if strings.Contains(s, key[i:i+12]) {
			return key[i : i+12]
		}
	}
	return ""
}

// healthProvider answers HTTP 200 with a valid OpenAI envelope whose content is not JSON and
// repeats the key it received (raw and in the forms the scrubber knows), after `prefix`.
func healthProvider(t *testing.T, prefix string, onCall func()) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onCall != nil {
			onCall()
		}
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		content := prefix + key + " | " + url.QueryEscape(key) + " | " + base64.StdEncoding.EncodeToString([]byte("user:"+key)) +
			" | " + strings.ReplaceAll(key, "/", `\/`)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func captureLogs(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}), nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() string { mu.Lock(); defer mu.Unlock(); return buf.String() }
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Probar conexión con un proveedor que responde HTTP 200 pero con texto (no JSON) que repite la
// key: ni la respuesta, ni la caché idempotente, ni los logs la llevan, entera o en parte.
func TestTestConnectionAfterHTTP200NeverCarriesTheKey(t *testing.T) {
	for _, prefix := range []string{"", "Unable to produce JSON for credential ", strings.Repeat("x", 100)} {
		for _, idem := range []string{"", "health-1"} {
			clearAIEnv(t)
			t.Setenv("OPENAI_API_KEY", longFakeKey) // la configurada: la solicitud no trae key
			logs := captureLogs(t)
			srv, _ := newTestServer(t)
			p := healthProvider(t, prefix, nil)
			body := `{"provider":"openai","base_url":"` + p.URL + `","model":"m"}`
			var opts []reqOpt
			if idem != "" {
				opts = append(opts, header("Idempotency-Key", idem))
			}
			rec := call(t, srv, "POST", "/api/v1/settings/ai/test", body, opts...)
			var out struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			json.Unmarshal(rec.Body.Bytes(), &out)
			if out.OK || !strings.Contains(out.Error, "JSON") {
				t.Fatalf("the parse problem is still reported: %s", rec.Body)
			}
			if leak := keyLeaks(rec.Body.String(), longFakeKey); leak != "" {
				t.Fatalf("prefix %d, response carries %q: %s", len(prefix), leak, rec.Body)
			}
			if idem != "" {
				_, cached, found, err := srv.Store.IdempotentResponse("POST /api/v1/settings/ai/test " + idem)
				if err != nil || !found {
					t.Fatalf("idempotent response stored: %v %v", found, err)
				}
				if leak := keyLeaks(string(cached), longFakeKey); leak != "" {
					t.Fatalf("idempotency cache carries %q", leak)
				}
			}
			if leak := keyLeaks(logs(), longFakeKey); leak != "" {
				t.Fatalf("logs carry %q", leak)
			}
		}
	}
}

// La key que se quita es la que usó la solicitud: una key enviada en el formulario (distinta de
// la guardada) y una configuración que cambia mientras el proveedor responde.
func TestTestConnectionScrubsTheKeyOfThatRequest(t *testing.T) {
	clearAIEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-FAKE-saved-key-000000000000")
	srv, _ := newTestServer(t)
	formKey := "sk-FAKE-form-key-" + strings.Repeat("Q", 40)
	p := healthProvider(t, "no JSON: ", func() { os.Setenv("OPENAI_API_KEY", "sk-FAKE-changed-meanwhile-1111") })
	rec := call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"openai","api_key":"`+formKey+`","base_url":"`+p.URL+`","model":"m"}`)
	if leak := keyLeaks(rec.Body.String(), formKey); leak != "" || !strings.Contains(rec.Body.String(), "[api key]") {
		t.Fatalf("form key %q in %s", leak, rec.Body)
	}
}
