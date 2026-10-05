package api

import (
	"net/http"
	"testing"
)

func host(h string) reqOpt { return func(r *http.Request) { r.Host = h } }

// Protección contra DNS rebinding: sin credenciales solo se atiende a Host locales o permitidos.
func TestHostGuard(t *testing.T) {
	srv, testID := newTestServer(t)
	srv.Hosts = NewHostPolicy("reportes.lan, 192.168.1.10:8080", "https://tracereports.example.com/base")
	get := func(path string, opts ...reqOpt) int { return call(t, srv, "GET", path, "", opts...).Code }
	post := func(opts ...reqOpt) int { return call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`, opts...).Code }

	t.Run("sin auth", func(t *testing.T) {
		srv.Auth = Auth{}
		for _, h := range []string{"localhost:8080", "LOCALHOST", "127.0.0.1:8080", "[::1]:8080", "app.localhost",
			"reportes.lan", "192.168.1.10", "tracereports.example.com"} {
			if c := get("/api/v1/runs", host(h)); c != 200 {
				t.Errorf("host %q must be served: %d", h, c)
			}
		}
		for _, path := range []string{"/api/v1/runs", "/api/v1/tests/" + itoa(testID), "/", "/screenshots/x.png", "/api/v1/stream"} {
			if c := get(path, host("evil.example")); c != 403 {
				t.Errorf("rebinding host on %s: %d (want 403)", path, c)
			}
		}
		if c := post(host("evil.example")); c != 403 {
			t.Errorf("write from a rebinding host: %d", c)
		}
		if c := get("/api/v1/runs", host("evil.example"), fromAddr("127.0.0.1:1")); c != 403 {
			t.Errorf("the loopback peer does not excuse the Host: %d", c)
		}
	})

	t.Run("solo token", func(t *testing.T) {
		srv.Auth = Auth{Token: "tok"}
		if c := get("/api/v1/runs", host("evil.example")); c != 403 {
			t.Errorf("read without credentials from a rebinding host: %d", c)
		}
		if c := post(host("ci-runner.internal"), header("Authorization", "Bearer tok")); c != 201 {
			t.Errorf("a valid token is accepted from any host (CI): %d", c)
		}
		if c := get("/api/v1/runs", host("ci-runner.internal"), header("Authorization", "Bearer tok")); c != 200 {
			t.Errorf("token read from any host: %d", c)
		}
		if c := post(host("evil.example"), header("Authorization", "Bearer bad")); c == 201 {
			t.Errorf("invalid token from a rebinding host: %d", c)
		}
	})

	t.Run("con login de UI", func(t *testing.T) {
		srv.Auth = Auth{Token: "tok", UIUser: "qa", UIPass: "pw"}
		if c := get("/", host("reportes.miempresa.com")); c != 401 {
			t.Errorf("without login the browser must get the login prompt, not 403: %d", c)
		}
		if c := get("/api/v1/runs", host("reportes.miempresa.com"), basicAuth("qa", "pw")); c != 200 {
			t.Errorf("logged-in request from the deployment domain: %d", c)
		}
	})

	t.Run("comodín", func(t *testing.T) {
		srv.Auth = Auth{}
		srv.Hosts = NewHostPolicy("*", "")
		if c := get("/api/v1/runs", host("cualquiera.example")); c != 200 {
			t.Errorf("* accepts any host: %d", c)
		}
	})
}
