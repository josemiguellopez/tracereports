package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/locator"
)

// a real 1x1 PNG, as the client's canvas would send it
const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// Los íconos de la página (glifos de fuentes de íconos, imágenes chicas y <svg> en línea) viajan para
// que el Inspector se parezca a la aplicación; el servidor deja solo PNG de verdad y un SVG rehecho con
// formas y colores, sin nada que se ejecute o cargue algo.
func TestDOMIconsAreKeptAndCleaned(t *testing.T) {
	srv, testID := newTestServer(t)
	icons := `,{"i":3,"p":0,"tag":"i","attrs":{"class":"fa-solid fa-plus-circle"},"x":0,"y":0,"w":16,"h":16,"visible":true,"depth":1,` +
		`"icons":[{"g":"g0","pos":"before","w":16,"h":20},{"g":"g1","pos":"before","w":16,"h":20},{"g":"g9","pos":"before","w":16,"h":20},{"g":"g0","pos":"evil","w":16,"h":20},{"g":"g0","pos":"after","w":9999,"h":20}]},` +
		`{"i":4,"p":0,"tag":"svg","x":0,"y":0,"w":20,"h":20,"visible":true,"depth":1,` +
		`"svg":"<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 24 24\" onload=\"alert(1)\" width=\"20\" height=\"20\"><script>alert(1)</script><foreignObject><div>x</div></foreignObject>` +
		`<g fill=\"rgb(106, 27, 154)\"><path d=\"M7 2c-2.8 0-5 2.5-5 5.5z\" style=\"fill:red\" fill=\"url(https://evil.example/a)\"/><use href=\"https://evil.example/s.svg#x\"/></g></svg>"}`
	body := strings.Replace(structSnap("fail", "", icons), `"nodes":`, `"glyphs":{"g0":"`+tinyPNG+`","g1":"data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=","g2":"data:image/png;base64,PHNjcmlwdD4=","bad id":"`+tinyPNG+`"},"nodes":`, 1)
	postDOM(t, srv, testID, body)
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "")
	var snap locator.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Glyphs) != 1 || snap.Glyphs["g0"] != tinyPNG {
		t.Fatalf("only the real PNG stays (not an svg, not text posing as png, not a bad id): %v", snap.Glyphs)
	}
	ic := snap.Nodes[3].Icons
	if len(ic) != 1 || ic[0].G != "g0" || ic[0].Pos != "before" {
		t.Fatalf("icons pointing to a dropped glyph, an unknown position or a huge size are dropped: %+v", ic)
	}
	svg := snap.Nodes[4].SVG
	for _, bad := range []string{"script", "onload", "foreignObject", "style", "url(", "use", "evil"} {
		if strings.Contains(svg, bad) {
			t.Fatalf("%q must not survive: %s", bad, svg)
		}
	}
	for _, good := range []string{`<svg xmlns="http://www.w3.org/2000/svg"`, `viewBox="0 0 24 24"`, `<g fill="rgb(106, 27, 154)">`, `<path d="M7 2c-2.8 0-5 2.5-5 5.5z"></path>`} {
		if !strings.Contains(svg, good) {
			t.Fatalf("missing %q: %s", good, svg)
		}
	}
}

func TestCleanSVGRejectsWhatIsNotAnIcon(t *testing.T) {
	for name, in := range map[string]string{
		"not svg at the root": `<div><path d="M0 0"/></div>`,
		"two roots":           `<svg><path d="M0 0"/></svg><svg><path d="M0 0"/></svg>`,
		"no shapes":           `<svg><script>alert(1)</script></svg>`,
		"broken xml":          `<svg><path d="M0 0"></svg>`,
		"too big":             `<svg><path d="` + strings.Repeat("M0 0 ", 3000) + `"/></svg>`,
		"entity tricks":       `<!DOCTYPE svg [<!ENTITY x "y">]><svg><path d="&x;"/></svg>`,
	} {
		if got := cleanSVG(in); got != "" {
			t.Errorf("%s: want empty, got %s", name, got)
		}
	}
	if got := cleanSVG(`<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" stroke="rgb(0, 0, 0)" fill="none"/></svg>`); !strings.Contains(got, `<circle cx="12" cy="12" r="9" stroke="rgb(0, 0, 0)" fill="none"></circle>`) {
		t.Fatalf("a plain icon is kept: %s", got)
	}
}

func TestGlyphBudget(t *testing.T) {
	in := map[string]string{}
	for i := 0; i < 400; i++ {
		in[fmt.Sprintf("g%d", i)] = tinyPNG
	}
	if got := cleanGlyphs(in); len(got) != maxGlyphs {
		t.Fatalf("at most %d glyphs per snapshot, got %d", maxGlyphs, len(got))
	}
	if got := cleanGlyphs(map[string]string{"g0": tinyPNG + strings.Repeat("A", maxGlyphBytes)}); got != nil {
		t.Fatal("a glyph over the size limit is dropped")
	}
}
