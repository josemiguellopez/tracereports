package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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
	for _, good := range []string{`<svg xmlns="http://www.w3.org/2000/svg"`, `viewBox="0 0 24 24"`, `<g fill="rgb(106, 27, 154)">`, `<path d="M7 2c-2.8 0-5 2.5-5 5.5z" fill="none"></path>`} {
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

// A loading skeleton as content-loader libraries draw it, exactly as the client captures it (paint
// copied on every element, the fill resolved to url("#grad")): one full-size <rect> clipped to the
// bars and painted with an animated gradient. Without its <defs> the rect inherited the black of the
// <svg> and the Inspector showed a black block where the page showed grey bars.
const skeletonSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 95" preserveAspectRatio="none" style="width:467px;height:111px" fill="rgb(0, 0, 0)" stroke="none" width="467" height="111">` +
	`<rect x="0" y="0" width="400" height="95" clip-path="url(#clipA)" style="fill: url(#gradA)" fill="url(&quot;#gradA&quot;)" stroke="none"/>` +
	`<defs fill="rgb(0, 0, 0)"><clipPath id="clipA" fill="rgb(0, 0, 0)"><rect x="0" y="10" rx="3" width="300" height="12" fill="rgb(0, 0, 0)"/>` +
	`<rect x="320" y="10" width="80" height="12"/><rect x="0" y="40" width="300" height="8"/></clipPath>` +
	`<linearGradient id="gradA" fill="rgb(0, 0, 0)"><stop offset="0%" stop-color="#f3f3f3" fill="rgb(0, 0, 0)"><animate attributeName="offset" values="-2; 1" dur="2s" repeatCount="indefinite" fill="rgb(0, 0, 0)"/></stop>` +
	`<stop offset="100%" stop-color="#ecebeb"/></linearGradient></defs></svg>`

func TestCleanSVGKeepsInternalReferences(t *testing.T) {
	got := cleanSVG(skeletonSVG)
	for _, good := range []string{`<clipPath id="clipA"`, `<linearGradient id="gradA"`, `<stop offset="0%" stop-color="#f3f3f3"`,
		`clip-path="url(#clipA)"`, `<rect x="0" y="0" width="400" height="95" clip-path="url(#clipA)" fill="url(#gradA)"`,
		`<rect x="320" y="10" width="80" height="12">`} {
		if !strings.Contains(got, good) {
			t.Fatalf("missing %q: %s", good, got)
		}
	}
	for _, bad := range []string{"animate", "style", "&quot;", `"url("`} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q must not survive: %s", bad, got)
		}
	}
	// las referencias que quedan apuntan solo a elementos del mismo <svg>
	for _, m := range regexp.MustCompile(`url\(([^)]*)\)`).FindAllStringSubmatch(got, -1) {
		if m[1] != "#clipA" && m[1] != "#gradA" {
			t.Fatalf("unexpected reference %q: %s", m[0], got)
		}
	}
}

// The absolute form some libraries build (url("https://host/page#id")) points to the same <svg>: it
// stays as url(#id), nothing is loaded. A reference to something outside, or to an id that is not in
// this <svg>, is dropped, and a fill or stroke without its paint becomes "none", never black.
func TestCleanSVGReferencesOutsideAreDropped(t *testing.T) {
	in := `<svg viewBox="0 0 10 10"><defs><linearGradient id="g"><stop offset="0" stop-color="red"/></linearGradient>` +
		`<script>alert(1)</script><image href="https://evil.example/x.png"/></defs>` +
		`<rect width="5" height="5" fill="url(&quot;https://app.example/page?x=1#g&quot;)"/>` +
		`<rect width="6" height="6" fill="url(https://evil.example/p.svg)" stroke="url(#missing)" clip-path="url(data:image/svg+xml,x)" mask="url(#nope)"/>` +
		`<circle r="2" fill="javascript:alert(1)"/>` +
		`<use href="javascript:alert(1)"/><use href="https://evil.example/s.svg#g"/><use href="#missing"/></svg>`
	got := cleanSVG(in)
	if !strings.Contains(got, `<rect width="5" height="5" fill="url(#g)">`) {
		t.Fatalf("absolute url to an internal id becomes url(#id): %s", got)
	}
	if !strings.Contains(got, `<rect width="6" height="6" fill="none" stroke="none">`) {
		t.Fatalf("lost paint becomes none, lost clip/mask is dropped: %s", got)
	}
	if !strings.Contains(got, `<circle r="2" fill="none">`) {
		t.Fatalf("a fill that is not a color becomes none: %s", got)
	}
	for _, bad := range []string{"script", "image", "evil", "javascript", "data:", "missing", "nope", "<use"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q must not survive: %s", bad, got)
		}
	}
}

// <use> draws an element of the same <svg>, and only one that has no <use> inside: no copies of
// copies (a few bytes could otherwise ask the viewer for millions of shapes) and no loops.
func TestCleanSVGUseIsOneLevel(t *testing.T) {
	got := cleanSVG(`<svg xmlns:xlink="http://www.w3.org/1999/xlink"><defs><symbol id="a"><path d="M0 0h1"/></symbol>` +
		`<g id="b"><use href="#a"/><use href="#a"/></g><g id="c"><use href="#c"/></g></defs>` +
		`<use xlink:href="#a"/><use href="#b"/><use href="#c"/></svg>`)
	if strings.Count(got, `<use href="#a"></use>`) != 3 {
		t.Fatalf("use of a plain symbol (href or xlink:href) is kept: %s", got)
	}
	if strings.Contains(got, `href="#b"`) || strings.Contains(got, `href="#c"`) {
		t.Fatalf("use of something that has a use inside is dropped: %s", got)
	}
	many := `<svg><defs><path id="p" d="M0 0"/></defs>` + strings.Repeat(`<use href="#p"/>`, maxSVGUses+1) + `</svg>`
	if cleanSVG(many) != "" {
		t.Fatal("more than maxSVGUses <use> is not an icon")
	}
}
