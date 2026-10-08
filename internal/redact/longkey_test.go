package redact

import (
	"strings"
	"testing"
)

// Una clave configurada como sensible se enmascara tenga el largo que tenga: valor escalar,
// contenedor, clave escapada, valor cortado al final, formulario y cabecera.
func TestLongConfiguredKeysAreMasked(t *testing.T) {
	key := strings.Repeat("long-field-", 9) + "token" // 104 caracteres
	huge := strings.Repeat("k", 5000) + "_secret"     // sufijo sensible, clave enorme
	t.Setenv("TRACEREPORTS_REDACT_KEYS", key)
	p := FromEnv()
	if !p.SensitiveKey(key) || !p.SensitiveKey(huge) {
		t.Fatal("fixture keys must be sensitive")
	}
	const secret = "FAKE-PRIVATE-VALUE-12345"
	escapedKey := strings.Replace(key, "t", `\u0074`, 1) // la misma clave con un escape JSON
	cases := map[string]string{
		"string":    `{"a":1,"` + key + `":"` + secret + `","b":2}`,
		"number":    `{"` + key + `":12345678901234}`,
		"container": `{"` + key + `":{"inner":"` + secret + `"},"b":2}`,
		"array":     `{"` + key + `":["` + secret + `"]}`,
		"escaped":   `{"` + escapedKey + `":"` + secret + `"}`,
		"huge key":  `{"` + huge + `":"` + secret + `"}`,
		"cut":       `{"a":1,"` + key + `":"` + secret,
		"cut huge":  `{"` + huge + `":"` + secret,
		"form":      `a=1&` + key + `=` + secret + `&b=2`,
		"header":    key + `: ` + secret,
	}
	for name, in := range cases {
		out := p.Text(in)
		if strings.Contains(out, secret) || strings.Contains(out, "12345678901234") {
			t.Errorf("%s: secret kept: %.200s", name, out)
		}
	}
	// lo no sensible sigue igual, también con claves largas
	plain := `{"` + strings.Repeat("ordinary-", 20) + `":"visible","b":true}`
	if got := p.Text(plain); got != plain {
		t.Errorf("non-sensitive long key changed: %.200s", got)
	}
}
