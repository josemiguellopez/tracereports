package repro

import (
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

// Caracteres válidos en un token HTTP (método, nombre de header) que la shell interpreta:
// | & ; ` $ ! ' * ~ ^ #. Se comparan como texto; nada se ejecuta.
var shellyTokens = []string{"GET|id", "GET&echo", "GET`id`", "GET$HOME", "GET!x", "GET'x", "GET*", "~GET", "GET#x", "GET^x"}

func TestCurlMethodIsAlwaysALiteralArgument(t *testing.T) {
	for _, m := range shellyTokens {
		got := Curl(&db.NetConn{Method: m, URL: "https://example.test/"}, redact.Default())
		want := "curl -X " + shq(m) + " 'https://example.test/'"
		if !strings.HasPrefix(got, want) {
			t.Errorf("method %q: %s", m, got)
		}
	}
	// los normales siguen igual
	for _, m := range []string{"GET", "POST", "PATCH", "DELETE", "PROPFIND", "M-SEARCH"} {
		if got := Curl(&db.NetConn{Method: m, URL: "https://x/"}, nil); !strings.HasPrefix(got, "curl -X "+m+" 'https://x/'") {
			t.Errorf("normal method %q changed: %s", m, got)
		}
	}
}

func TestCurlHeaderNameIsLiteralAndOnlyTheGeneratedVariableExpands(t *testing.T) {
	names := []string{"X-Auth-`id`", "X-Auth-$UID", "X-Auth!x", "X-A|b", "X-A&b", "X-'q", "X-A^b", "X-A~b", "X-A#b", "X-A*b"}
	for _, n := range names {
		got := Curl(&db.NetConn{Method: "GET", URL: "https://x/", RequestHeaders: map[string]string{n: "<masked>"}}, redact.Default())
		line := got[strings.Index(got, "-H "):]
		// -H '<nombre literal>: '"$VARIABLE" — la única expansión es la variable generada
		want := "-H " + shq(n+": ") + `"$` + envVar(n) + `"`
		if !strings.HasPrefix(line, want) {
			t.Errorf("header %q:\n got %s\nwant %s", n, line, want)
		}
		if strings.Contains(got, `-H "`+n) {
			t.Errorf("header %q inside double quotes: %s", n, got)
		}
		if v := envVar(n); !validShellVar(v) {
			t.Errorf("generated variable %q is not a plain shell variable", v)
		}
	}
	// un nombre normal mantiene la forma de siempre
	got := Curl(&db.NetConn{Method: "GET", URL: "https://x/", RequestHeaders: map[string]string{"Authorization": "<masked>", "X-Api-Key": "k"}}, redact.Default())
	if !strings.Contains(got, `-H "Authorization: $AUTHORIZATION"`) || !strings.Contains(got, `-H "X-Api-Key: $X_API_KEY"`) {
		t.Errorf("normal headers changed: %s", got)
	}
}

func validShellVar(v string) bool {
	if v == "" || (v[0] >= '0' && v[0] <= '9') {
		return false
	}
	for _, r := range v {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
