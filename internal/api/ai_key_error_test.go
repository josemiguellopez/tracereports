package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Probar conexión con un proveedor que repite la key en su error: la respuesta no la trae.
func TestTestConnectionResponseNeverCarriesTheKey(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	const key = "sk-FAKE-echoed-key-24680"
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided: ` + key + ` (` + r.Header.Get("Authorization") + `)"}}`))
	}))
	defer mock.Close()
	rec := call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"openai","api_key":"`+key+`","base_url":"`+mock.URL+`","model":"m"}`)
	if strings.Contains(rec.Body.String(), key) || !strings.Contains(rec.Body.String(), "401") {
		t.Fatalf("test connection response: %s", rec.Body)
	}
}
