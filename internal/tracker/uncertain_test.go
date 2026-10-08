package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Un Create fallido se clasifica: confirmado (no se creó nada) o incierto (pudo crearse).
func TestCreateFailuresAreClassified(t *testing.T) {
	answer := func(code int, body string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if body == "lost" {
				c, _, _ := w.(http.Hijacker).Hijack()
				c.Close()
				return
			}
			w.WriteHeader(code)
			w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	client := &http.Client{Timeout: 2 * time.Second}
	providers := func(u string) map[string]Provider {
		return map[string]Provider{
			"github": &GitHub{API: u, Repo: "a/b", Token: "fake", HTTP: client},
			"jira":   &Jira{URL: u, Project: "P", Token: "fake", IssueType: "Bug", HTTP: client},
			"azure":  &Azure{URL: u, Project: "P", Token: "fake", Type: "Bug", HTTP: client},
		}
	}
	cases := []struct {
		name    string
		code    int
		body    string
		created bool
	}{
		{"validation", 422, `{"message":"bad"}`, false},
		{"auth", 401, `{"message":"bad credentials"}`, false},
		{"rate limit", 429, `{}`, false},
		{"5xx", 502, `bad gateway`, true},
		{"lost answer", 0, "lost", true},
		{"unreadable 2xx", 201, `<html>`, true},
		{"2xx without id", 201, `{}`, true},
		{"empty 2xx", 201, ``, true},
	}
	for _, c := range cases {
		srv := answer(c.code, c.body)
		for name, p := range providers(srv.URL) {
			_, err := p.Create(context.Background(), &Issue{Title: "x"})
			if err == nil || Created(err) != c.created {
				t.Errorf("%s/%s: err %v, created %v (want %v)", name, c.name, err, Created(err), c.created)
			}
		}
	}
	// conexión rechazada: nunca salió
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	for name, p := range providers(u) {
		if _, err := p.Create(context.Background(), &Issue{Title: "x"}); err == nil || Created(err) {
			t.Errorf("%s refused connection: %v", name, err)
		}
	}
}

// Un 4xx del destino de una redirección no prueba nada sobre la primera solicitud (que ya llegó):
// incierto. El mismo 4xx sin redirección es un rechazo confirmado.
func TestRejectionAfterARedirectIsUncertain(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}))
	defer target.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusSeeOther)
	}))
	defer redirecting.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	for _, c := range []struct {
		url     string
		created bool
	}{{redirecting.URL, true}, {target.URL, false}} {
		for name, p := range map[string]Provider{
			"github": &GitHub{API: c.url, Repo: "a/b", Token: "fake", HTTP: client},
			"jira":   &Jira{URL: c.url, Project: "P", Token: "fake", IssueType: "Bug", HTTP: client},
			"azure":  &Azure{URL: c.url, Project: "P", Token: "fake", Type: "Bug", HTTP: client},
		} {
			_, err := p.Create(context.Background(), &Issue{Title: "x"})
			if err == nil || Created(err) != c.created {
				t.Errorf("%s via %s: err %v, created %v (want %v)", name, c.url, err, Created(err), c.created)
			}
		}
	}
}
