package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "s3cret", UIUser: "qa", UIPass: "pw"}
	h := srv.Router()
	call := func(method, path string, hdr map[string]string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"name":"x"}`))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	basic := func(u, p string) map[string]string {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetBasicAuth(u, p)
		return map[string]string{"Authorization": r.Header.Get("Authorization")}
	}

	cases := []struct {
		name   string
		method string
		path   string
		hdr    map[string]string
		want   int
	}{
		{"write without token", http.MethodPost, "/api/v1/runs", nil, 401},
		{"write with wrong token", http.MethodPost, "/api/v1/runs", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"write with bearer token", http.MethodPost, "/api/v1/runs", map[string]string{"Authorization": "Bearer s3cret"}, 201},
		{"write with header token", http.MethodPost, "/api/v1/runs", map[string]string{"X-TraceReports-Token": "s3cret"}, 201},
		{"write with UI login only", http.MethodPost, "/api/v1/runs", basic("qa", "pw"), 401},
		{"read without login", http.MethodGet, "/api/v1/runs", nil, 401},
		{"read with UI login", http.MethodGet, "/api/v1/runs", basic("qa", "pw"), 200},
		{"read with wrong password", http.MethodGet, "/api/v1/runs", basic("qa", "x"), 401},
		{"read with token (CI)", http.MethodGet, "/api/v1/runs", map[string]string{"Authorization": "Bearer s3cret"}, 200},
	}
	for _, c := range cases {
		if got := call(c.method, c.path, c.hdr); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}

	// Sin configuración, todo abierto (compatibilidad con instalaciones existentes).
	srv.Auth = Auth{}
	h = srv.Router()
	if got := call(http.MethodPost, "/api/v1/runs", nil); got != 201 {
		t.Errorf("open server: got %d", got)
	}
}

func TestTokenHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/runs", nil)
	r.Header.Set("X-TraceReports-Token", "s3cret")
	if !(Auth{Token: "s3cret"}).tokenOK(r) {
		t.Fatal("the token is accepted in X-TraceReports-Token")
	}
}
