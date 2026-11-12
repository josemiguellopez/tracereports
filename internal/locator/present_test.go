package locator

import "testing"

func TestPresentReadsTheCommonSelectors(t *testing.T) {
	nodes := []Node{
		{I: 0, P: -1, Tag: "body"},
		{I: 1, P: 0, Tag: "div", Attrs: map[string]string{"class": "list-item textoverflow", "id": "fila-1"}},
		{I: 2, P: 1, Tag: "span", Text: "Consultorio Pudahuel Oriente."},
		{I: 3, P: 1, Tag: "span", Text: "Lote: LOTE_001"},
		{I: 4, P: 0, Tag: "button", Attrs: map[string]string{"data-testid": "pay", "class": "btn btn-primary"}, Text: "Pagar ahora"},
	}
	cases := []struct {
		sel         string
		found, sure bool
	}{
		{"//div[contains(@class,'textoverflow') and contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), 'consultorio pudahuel oriente. lote: lote_001')]", true, true},
		{"//div[contains(@class,'textoverflow') and contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), 'lote: lote_999')]", false, true},
		{"//button[normalize-space(.)='Pagar ahora']", true, true},
		{"//button[@data-testid='pay']", true, true},
		{"//*[@id='fila-1']", true, true},
		{"//a", false, true},
		{"#fila-1", true, true},
		{"button.btn-primary[data-testid=pay]", true, true},
		{".btn-prim", false, true},
		{"[data-testid='nope']", false, true},
		// lo que no se lee con seguridad no es una respuesta
		{"//div[2]/span", false, false},
		{"div > span", false, false},
		{"//div[@id='a' or @id='fila-1']", false, false},
		{"//div[starts-with(@id,'fila')]", false, false},
	}
	for _, c := range cases {
		if found, sure := Present(c.sel, nodes); found != c.found || sure != c.sure {
			t.Errorf("Present(%q) = %v, %v; want %v, %v", c.sel, found, sure, c.found, c.sure)
		}
	}
}
