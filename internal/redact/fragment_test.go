package redact

import (
	"strings"
	"testing"
)

// Un texto recortado (por el cliente o el servidor) puede terminar dentro del valor de una clave
// sensible, sin su comilla de cierre: ese resto se enmascara igual.
func TestTextMasksASensitiveValueCutAtTheEnd(t *testing.T) {
	p := Default()
	const secret = "S3CR3T-sentinel-ñ-😀-value"
	full := `{"user":"ana","token":"` + secret + `","next":"/home"}`
	start := strings.Index(full, secret)
	for cut := 0; cut <= len(full); cut++ {
		in := full[:cut]
		got := p.Text(in)
		// la parte del secreto presente (6 bytes o más) nunca queda
		if cut > start {
			part := full[start:min(cut, start+len(secret))]
			if len(part) >= 6 && strings.Contains(got, part) {
				t.Fatalf("cut %d keeps %q: %q", cut, part, got)
			}
		}
		if cut <= start-len(`"token":"`) && got != in {
			t.Fatalf("cut %d, before the sensitive key, must not change: %q -> %q", cut, in, got)
		}
	}
	// escapes dentro del valor, clave escapada, barra invertida suelta al final, espacios
	for _, in := range []string{
		`{"token":"abc\"def\\ghiéjkl`,
		`{"token" : "abcdefghijklmnop`,
		`{"password":"abcdefghijkl\`,
		`{"a":1,"csrf_token":   "abcdefghijklmnop`,
		`[{"apiKey":"abcdefghijklmnop`,
	} {
		got := p.Text(in)
		if strings.Contains(got, "abcdefghij") || !strings.HasSuffix(got, `"`+Mask+`"`) {
			t.Errorf("Text(%q) = %q", in, got)
		}
	}
	// lo no sensible recortado queda igual
	for _, in := range []string{`{"user":"ana","note":"hello wor`, `{"tokens_left":5,"message":"cut her`, `plain text "token`} {
		if got := p.Text(in); got != in {
			t.Errorf("non-sensitive fragment changed: %q -> %q", in, got)
		}
	}
}
