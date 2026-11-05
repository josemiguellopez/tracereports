package locator

import "testing"

// Caso real (run 379, test 927): el helper del equipo escribe el XPath entre comillas simples, y
// adentro el XPath usa las mismas comillas. Antes se leía hasta el final del texto.
func TestBareXPathBetweenQuotesEndsAtItsQuote(t *testing.T) {
	const xp = `//div[contains(translate(normalize-space(.),'ABCDEFGHIJKLMNOPQRSTUVWXYZ','abcdefghijklmnopqrstuvwxyz'),'segundo diagnostico texto libre') and contains(@class,'mr-auto')]`
	trace := `Errores acumulados: ["Warning Linea: 2299 al verificar si el elemento '` + xp +
		`' ", "Warning Linea: 2299 al verificar si el elemento '//div[contains(@class,'otro')]' "]`
	if got := ExtractSelector(trace); got != xp {
		t.Fatalf("got %q", got)
	}
	// sin comillas alrededor sigue terminando en el primer espacio fuera de [] () y comillas
	if got := ExtractSelector(`no se encontró el elemento //div[@id='a b'] tras 3 intentos`); got != `//div[@id='a b']` {
		t.Fatalf("unquoted: %q", got)
	}
}

func TestTruncated(t *testing.T) {
	for sel, want := range map[string]bool{
		`//div[contains(normalize-space(.),'segundo diagnostico texto l`: true,
		`//div[contains(@class,'mr-auto')]`:                               false,
		`//button[@id="pay"`:                                               true,
		`#login .submit`:                                                   false,
		`text=Ingresar`:                                                    false,
		``:                                                                 false,
	} {
		if got := Truncated(sel); got != want {
			t.Errorf("Truncated(%q) = %v, want %v", sel, got, want)
		}
	}
}
