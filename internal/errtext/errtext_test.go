package errtext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// El error tal como lo guardó el test del run del reporte: el framework lo envuelve con el nombre del
// archivo y una lista serializada, y el recorte por largo dejaba solo "…espontanea.py: [".
const wrapped = `Errores acumulados para el file_name:test_agregar_item_inmunizaciones_atencion_espontanea.py: ["Error Linea 3739 esperando elemento después de 6 intentos: //div[contains(@class,'textoverflow') and contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), 'consultorio pudahuel oriente. lote: lote_001_vac_01_1794482716')]", "Error Linea 3801 otra cosa"]`

func TestUsefulDropsTheFileWrapperAndTheList(t *testing.T) {
	got := Useful(wrapped)
	if !strings.HasPrefix(got, "Error Linea 3739 esperando elemento después de 6 intentos: //div[contains(@class,'textoverflow')") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "file_name") || strings.Contains(got, "3801") || strings.HasSuffix(got, `"`) {
		t.Fatalf("the wrapper, the other items and the quotes go: %q", got)
	}
	// así lo recibían el título del incidente (110) y la evidencia
	for _, n := range []int{110, 160, 300} {
		s := Short(wrapped, n)
		if !strings.Contains(s, "esperando elemento") || !strings.Contains(s, "//div[") || strings.HasSuffix(s, "[…") || !utf8.ValidString(s) {
			t.Errorf("Short(%d) = %q", n, s)
		}
	}
}

func TestUsefulOtherShapes(t *testing.T) {
	cases := map[string]string{
		`["Timeout esperando el botón", "otro"]`:                             "Timeout esperando el botón",
		`['Element not found: #pay']`:                                        "Element not found: #pay",
		"Errores en tests/test_x.py: [\n  \"Error Linea 12 esperando //a\"\n]": "Error Linea 12 esperando //a",
		`Errores en test_x.py: ["Error Linea 12 esperando //div[@id='a'`:      "Error Linea 12 esperando //div[@id='a",
		"AssertionError: ['a', 'b'] != ['c']":                                "AssertionError: ['a', 'b'] != ['c']",
		"TimeoutError: waiting for locator(\"#x\")\n  at foo.js:1":            `TimeoutError: waiting for locator("#x")`,
		"Errores:\n[\nNoSuchElementException: no such element":               "NoSuchElementException: no such element",
	}
	for in, want := range cases {
		if got := Useful(in); got != want {
			t.Errorf("Useful(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCutOnAWordAndNeverInsideALetter(t *testing.T) {
	if got := Cut("esperando elemento después de seis intentos", 30); got != "esperando elemento después de…" {
		t.Errorf("on a word: %q", got)
	}
	if got := Cut("ááááááááá", 4); got != "áááá…" || !utf8.ValidString(got) {
		t.Errorf("letters, not bytes: %q", got)
	}
	if got := Cut("Error Linea 37 esperando: [", 26); got != "Error Linea 37 esperando…" {
		t.Errorf("no dangling [: %q", got)
	}
	if got := Cut("corto", 30); got != "corto" {
		t.Errorf("short text stays: %q", got)
	}
	if got := Cut("//div[contains(@class,'textoverflow')]", 20); got != "//div[contains(@clas…" {
		t.Errorf("without spaces, a hard cut: %q", got)
	}
}
