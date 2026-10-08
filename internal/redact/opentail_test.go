package redact

import (
	"math/rand"
	"strings"
	"testing"
)

// openTail encuentra lo mismo que la expresión regular original sobre todo el texto (comparado
// en miles de textos aleatorios con comillas, barras, claves y espacios), sin recorrerlo entero.
func TestOpenTailMatchesTheRegexp(t *testing.T) {
	pieces := []string{`"`, `\`, `\"`, `\\`, `:`, ` `, `token`, `password`, `"token":"`, `"a": "`, `x`, `ñ`, `{`, `}`, `,`, `"tok\"en":"`, `\u0074`, `"\u0074oken":"`, `"` + strings.Repeat("k", 300) + `_token":"`, strings.Repeat("y", 200)}
	r := rand.New(rand.NewSource(42))
	for n := 0; n < 20000; n++ {
		var b strings.Builder
		for k := r.Intn(14); k >= 0; k-- {
			b.WriteString(pieces[r.Intn(len(pieces))])
		}
		s := b.String()
		want := jsonOpenTailRe.FindStringSubmatchIndex(s)
		got := openTail(s)
		if (want == nil) != (got == nil) || (want != nil && (want[0] != got[0] || want[2] != got[2] || want[3] != got[3] || want[4] != got[4] || want[5] != got[5])) {
			t.Fatalf("%q: regexp %v, openTail %v", s, want, got)
		}
	}
}

// Un prompt grande se enmascara sin recorrerlo una vez más entero para el valor cortado.
func BenchmarkTextLargePrompt(b *testing.B) {
	prompt := strings.Repeat(`{"user":"ana","note":"texto normal ñ","password":"hunter2","items":[1,2,3]} `, 4000)
	p := Default()
	for i := 0; i < b.N; i++ {
		p.Text(prompt)
	}
}
