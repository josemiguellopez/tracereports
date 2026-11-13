package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func gz(t *testing.T, b []byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.String()
}

func gzipped(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }

// Un lote de red comprimido se guarda igual que uno sin comprimir, y el servidor avisa que acepta gzip.
func TestGzipEvidenceIsAccepted(t *testing.T) {
	srv, _ := newTestServer(t)
	payload, _ := json.Marshal(map[string]any{"connections": []map[string]any{
		{"method": "GET", "url": "https://app/api/ok", "status": 200, "response_body": strings.Repeat("{\"a\":1}", 4000)},
		{"method": "POST", "url": "https://app/api/save", "status": 500},
	}})
	rec := call(t, srv, http.MethodPost, "/api/v1/tests/1/network", gz(t, payload), gzipped)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"errors":1`) {
		t.Fatalf("gzip POST network: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get(acceptEncodingHeader) != "gzip" {
		t.Fatal("the server does not say it accepts gzip")
	}
	rec = call(t, srv, http.MethodGet, "/api/v1/tests/1/network", "")
	if !strings.Contains(rec.Body.String(), "https://app/api/save") {
		t.Fatalf("not stored: %.200s", rec.Body)
	}
}

func TestGzipBadBodies(t *testing.T) {
	srv, _ := newTestServer(t)
	if rec := call(t, srv, http.MethodPost, "/api/v1/tests/1/network", "esto no es gzip", gzipped); rec.Code != http.StatusBadRequest {
		t.Fatalf("broken gzip: %d", rec.Code)
	}
	br := func(r *http.Request) { r.Header.Set("Content-Encoding", "br") }
	if rec := call(t, srv, http.MethodPost, "/api/v1/tests/1/network", "{}", br); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unknown encoding: %d", rec.Code)
	}
	// un gzip chico que se infla más allá del límite del endpoint (4 MiB de DOM) no pasa
	bomb := gz(t, append([]byte(`{"html":"`), bytes.Repeat([]byte("a"), 6<<20)...))
	if len(bomb) > 1<<20 {
		t.Fatalf("the bomb is not small: %d", len(bomb))
	}
	if rec := call(t, srv, http.MethodPost, "/api/v1/tests/1/dom", bomb, gzipped); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("inflated past the limit: %d %.200s", rec.Code, rec.Body)
	}
}
