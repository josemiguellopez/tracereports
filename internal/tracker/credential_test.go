package tracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// token ficticio con caracteres que cambian al codificarlo (URL, JSON, base64)
const fakeTrackerToken = "fake-trk+token/with=chars_123456"

// echoing answers code and repeats, in its body, the credential it received in every form a
// proxy or tracker could: raw, JSON-escaped, URL-escaped and the whole Authorization header.
func echoing(t *testing.T, code int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		j, _ := json.Marshal(fakeTrackerToken)
		w.WriteHeader(code)
		w.Write([]byte(`{"message":"bad credentials ` + fakeTrackerToken + ` ` + string(j) + ` ` + url.QueryEscape(fakeTrackerToken) +
			` ` + strings.ReplaceAll(fakeTrackerToken, "/", `\/`) + `","sent":"` + auth + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func trackerProviders(u string) map[string]Provider {
	client := &http.Client{Timeout: 2 * time.Second}
	return map[string]Provider{
		"github": &GitHub{API: u, Repo: "a/b", Token: fakeTrackerToken, HTTP: client},
		"jira":   &Jira{URL: u, Project: "P", Email: "qa@example.invalid", Token: fakeTrackerToken, IssueType: "Bug", HTTP: client},
		"azure":  &Azure{URL: u, Project: "P", Token: fakeTrackerToken, Type: "Bug", HTTP: client},
	}
}

func assertNoToken(t *testing.T, where, s string) {
	t.Helper()
	forms := []string{fakeTrackerToken, url.QueryEscape(fakeTrackerToken), strings.ReplaceAll(fakeTrackerToken, "/", `\/`),
		base64.StdEncoding.EncodeToString([]byte(":" + fakeTrackerToken)),
		base64.StdEncoding.EncodeToString([]byte("qa@example.invalid:" + fakeTrackerToken))}
	for _, f := range forms {
		if strings.Contains(s, f) {
			t.Fatalf("%s carries the credential (%q): %s", where, f, s)
		}
	}
}

// El error de un tracker que repite la credencial no la devuelve, en ninguna forma, y conserva
// su clasificación: 401 rechazo confirmado, 503 incierto.
func TestTrackerErrorsNeverCarryTheCredential(t *testing.T) {
	for _, c := range []struct {
		code    int
		created bool
	}{{401, false}, {503, true}} {
		srv := echoing(t, c.code)
		for name, p := range trackerProviders(srv.URL) {
			_, err := p.Create(context.Background(), &Issue{Title: "x"})
			if err == nil {
				t.Fatalf("%s/%d: must fail", name, c.code)
			}
			assertNoToken(t, name, err.Error())
			if Created(err) != c.created || !strings.Contains(err.Error(), "bad credentials") {
				t.Fatalf("%s/%d: classification %v / message %s", name, c.code, Created(err), err)
			}
		}
	}
}

// La cancelación y el timeout se siguen reconociendo.
func TestTrackerErrorsKeepCancellation(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	for name, p := range trackerProviders(slow.URL) {
		_, err := p.Create(ctx, &Issue{Title: "x"})
		if !errors.Is(err, context.DeadlineExceeded) || !Created(err) {
			t.Fatalf("%s: %v (created %v)", name, err, Created(err))
		}
	}
}
