package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/locator"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

func sheetHash(css string) string {
	sum := sha256.Sum256([]byte(css))
	return hex.EncodeToString(sum[:])
}

// El HTML de la página llega de un cliente cualquiera: se rehace sin nada que ejecute, cargue algo o
// navegue, sin lo que alguien escribió y con los datos personales enmascarados. Lo que dibuja la página
// (estructura, clases, estilos en línea, SVG con sus referencias internas, shadow DOM abierto) queda.
func TestCleanHTMLDocDropsWhatRunsOrLoads(t *testing.T) {
	in := `<!DOCTYPE html><html lang="es"><head><title>x</title><script>alert(1)</script><link rel="stylesheet" href="https://app/x.css">` +
		`<meta http-equiv="refresh" content="0;url=https://evil.example"><base href="https://evil.example/"><style>body{color:red}</style></head>` +
		`<body class="app" onload="alert(1)"><div data-tr-i="1" id="main" style="color: red; background: url(https://evil.example/t.png)">` +
		`<a href="javascript:alert(1)" data-tr-i="2">uno</a><a href="/agenda" onclick="x()">dos</a>` +
		`<img src="https://app/logo.png" srcset="https://app/l2.png 2x" onerror="alert(1)"><img src="data:image/png;base64,iVBORw0KGgo=">` +
		`<iframe src="https://evil.example"></iframe><object data="x.swf"></object><embed src="x"><noscript><img src=x></noscript>` +
		`<form action="https://evil.example"><input name="rut" value="13.905.701-5"><input type="hidden" name="csrf" value="tok-abc">` +
		`<input type="password" value="clave"><input type="submit" value="Guardar"><input type="checkbox" value="si" checked>` +
		`<textarea>lo que escribió</textarea><button formaction="https://evil.example">Enviar</button>` +
		`<select><option>6 Meses</option><option selected>12 Meses</option></select></form>` +
		`<p>Paciente RUN 13.905.701-5, fono +56 9 1234 5678, ana@correo.cl</p><!-- comentario con token -->` +
		`<svg viewBox="0 0 10 10"><script>alert(1)</script><foreignObject><div>x</div></foreignObject><a href="javascript:alert(1)"><rect width="1" height="1"/></a>` +
		`<defs><linearGradient id="g"><stop offset="0"/><animate attributeName="x"/></linearGradient></defs><rect fill="url(#g)" width="5" height="5"/>` +
		`<use href="#g"/><use xlink:href="https://evil.example/s.svg#x"/><image href="https://evil.example/p.png"/></svg>` +
		`<math><mtext><img src=x onerror=alert(1)></mtext></math>` +
		`<my-widget><template shadowrootmode="open"><style>:host{display:block}</style><b>sombra</b></template></my-widget>` +
		`<template><img src=x onerror=alert(1)></template></div></body></html>`
	got := cleanHTMLDoc(redact.Default(), in)
	for _, bad := range []string{"<script", "alert", "<link", "<meta", "<base", "<iframe", "<object", "<embed", "<noscript", "onload", "onclick", "onerror",
		"javascript:", "evil.example", "https://app/logo.png", "srcset", "action=", "formaction", "tok-abc", "clave", "lo que escribió", "13.905.701-5",
		"1234 5678", "ana@correo.cl", "comentario", "foreignObject", "<animate", "<math", "<title", "body{color:red}"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q must not survive", bad)
		}
	}
	for _, good := range []string{`<html lang="es"><head></head><body class="app">`, `data-tr-i="1"`, `id="main"`, `style="color: red; background: none"`,
		`<a href="/agenda">dos</a>`, `src="data:image/png;base64,iVBORw0KGgo="`, `<input name="rut" value="•••"/>`, `<input type="submit" value="Guardar"/>`,
		`<input type="checkbox" value="si" checked=""/>`, `<textarea>•••</textarea>`, `<option selected="">12 Meses</option>`, `<button>Enviar</button>`,
		`<linearGradient id="g">`, `fill="url(#g)"`, `<use href="#g">`, `<template shadowrootmode="open"><style>:host{display:block}</style><b>sombra</b></template>`, "&lt;rut&gt;", `viewBox="0 0 10 10"`} {
		if !strings.Contains(got, good) {
			t.Errorf("missing %q in %s", good, got)
		}
	}
	if strings.Count(got, "<template") != 1 {
		t.Errorf("a template that is not an open shadow root is dropped: %s", got)
	}
}

func TestCleanCSS(t *testing.T) {
	in := `@import url("https://evil.example/x.css"); @import 'y.css';
.a{background:url(https://evil.example/t.png) no-repeat}
.b{background-image:url("data:image/png;base64,iVBORw0KGgo=")}
@font-face{font-family:x;src:url(data:font/woff2;base64,d09GMgAB) format("woff2"),url(/fonts/x.woff)}
.c{width:expression(alert(1));-moz-binding:url(x.xml#b);behavior:url(x.htc)}
.d::after{content:"</style><script>alert(1)</script>"}
.e{background:url(javascript:alert(1))}
.f{fill:url(#grad);mask:url('#m1')}`
	got := cleanCSS(in)
	for _, bad := range []string{"@import", "evil.example", "/fonts/x.woff", "expression(", "-moz-binding", "behavior:", "</style", "<script", "javascript:"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q must not survive: %s", bad, got)
		}
	}
	for _, good := range []string{`url("data:image/png;base64,iVBORw0KGgo=")`, `url("data:font/woff2;base64,d09GMgAB") format("woff2")`, `background:none no-repeat`, `fill:url(#grad)`, `mask:url(#m1)`} {
		if !strings.Contains(got, good) {
			t.Errorf("missing %q: %s", good, got)
		}
	}
}

// Un snapshot en modo HTML: el HTML se guarda limpio, cada hoja una sola vez por su hash (una que no es
// la de su hash no se guarda) y se sirve aparte; los nodos siguen ahí para los locators.
func TestHTMLSnapshotStoresSheetsOnce(t *testing.T) {
	srv, testID := newTestServer(t)
	sheet := `.card{box-shadow:0 1px 3px rgba(0,0,0,.2);background:url(https://evil.example/x.png)}`
	h := sheetHash(sheet)
	fake := strings.Repeat("a", 64)
	html := `<html><head></head><body><div class="card" data-tr-i="1">Hola</div><script>alert(1)</script></body></html>`
	mk := func(sheets map[string]string) string {
		doc, _ := json.Marshal(map[string]any{"doc": html, "css": []string{h, fake, "../../etc"}, "sheets": sheets, "missing": []string{"https://cdn.example/x.css"}, "dpr": 2})
		return strings.Replace(structSnap("fail", "", ""), `"nodes":`, `"html":`+string(doc)+`,"nodes":`, 1)
	}
	onlyImport := `@import url("https://evil.example/x.css");`
	out := postDOM(t, srv, testID, mk(map[string]string{h: sheet, fake: "body{}", sheetHash(onlyImport): onlyImport}))
	if out["mode"] != "html" {
		t.Fatalf("mode: %+v", out)
	}
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "")
	var snap locator.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Mode != "html" || snap.HTML == nil || len(snap.Nodes) == 0 || snap.HTML.Sheets != nil {
		t.Fatalf("stored: %+v", snap.HTML)
	}
	if strings.Contains(snap.HTML.Doc, "script") || !strings.Contains(snap.HTML.Doc, `<div class="card" data-tr-i="1">Hola</div>`) {
		t.Fatalf("doc: %s", snap.HTML.Doc)
	}
	if len(snap.HTML.CSS) != 2 || snap.HTML.CSS[0] != h || snap.HTML.DPR != 2 || len(snap.HTML.Missing) != 1 {
		t.Fatalf("refs: %+v", snap.HTML)
	}
	css := do(t, srv, http.MethodGet, "/api/v1/dom/css/"+h, "")
	if css.Code != 200 || !strings.Contains(css.Body.String(), "box-shadow") || strings.Contains(css.Body.String(), "evil") || css.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("sheet: %d %s", css.Code, css.Body.String())
	}
	if c := do(t, srv, http.MethodGet, "/api/v1/dom/css/"+fake, "").Code; c != http.StatusNoContent {
		t.Fatalf("a sheet that is not its hash is not stored: %d", c)
	}
	// un <style> vacío de la página: la hoja está (vacía), no es una que falta
	if e := do(t, srv, http.MethodGet, "/api/v1/dom/css/"+sheetHash(onlyImport), ""); e.Code != http.StatusOK || strings.Contains(e.Body.String(), "evil") {
		t.Fatalf("a sheet left empty by the cleaning is stored, not lost: %d %q", e.Code, e.Body.String())
	}
	if e := do(t, srv, http.MethodGet, "/api/v1/dom/css/"+sheetHash(""), ""); e.Code != http.StatusOK || e.Body.Len() != 0 {
		t.Fatalf("the empty sheet is there: %d %q", e.Code, e.Body.String())
	}
	if c := do(t, srv, http.MethodGet, "/api/v1/dom/css/xyz", "").Code; c != http.StatusBadRequest {
		t.Fatalf("bad hash: %d", c)
	}
	// otra página que usa la misma hoja: no hace falta mandarla de nuevo
	postDOM(t, srv, testID, strings.Replace(mk(nil), `"kind":"fail"`, `"kind":"step"`, 1))
	if c := do(t, srv, http.MethodGet, "/api/v1/dom/css/"+h, "").Code; c != 200 {
		t.Fatalf("the sheet is still there: %d", c)
	}
}

// Un HTML que no se puede usar deja el snapshot en el modo estructura.
func TestHTMLSnapshotFallsBackToStructure(t *testing.T) {
	srv, testID := newTestServer(t)
	doc, _ := json.Marshal(map[string]any{"doc": strings.Repeat("<div>", maxHTMLElements+10)})
	out := postDOM(t, srv, testID, strings.Replace(structSnap("fail", "", ""), `"nodes":`, `"html":`+string(doc)+`,"nodes":`, 1))
	if out["mode"] != "" {
		t.Fatalf("mode: %+v", out)
	}
	body := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "").Body.String()
	if strings.Contains(body, `"html"`) || !strings.Contains(body, `"nodes"`) {
		t.Fatalf("stored: %.300s", body)
	}
}

func TestHTMLShapeLimits(t *testing.T) {
	if htmlShapeOK(strings.Repeat("<div>", maxHTMLDepth+1)) {
		t.Error("nested without end is not used")
	}
	if !htmlShapeOK(strings.Repeat("<div><img><br></div>", 5000)) {
		t.Error("a wide page is fine")
	}
}

// El ZIP exportado trae cada hoja del modo HTML una sola vez (css/<hash>.js, se carga desde file://).
func TestExportCarriesHTMLSheetsOnce(t *testing.T) {
	srv, testID := newTestServer(t)
	sheet := `.card{color:rgb(1, 2, 3)}`
	h := sheetHash(sheet)
	doc, _ := json.Marshal(map[string]any{"doc": `<html><head></head><body><div class="card">x</div></body></html>`, "css": []string{h}, "sheets": map[string]string{h: sheet}})
	body := strings.Replace(structSnap("fail", "", ""), `"nodes":`, `"html":`+string(doc)+`,"nodes":`, 1)
	postDOM(t, srv, testID, body)
	postDOM(t, srv, testID, strings.Replace(body, `"kind":"fail"`, `"kind":"step"`, 1))
	runID, err := srv.Store.TestRunID(testID)
	if err != nil {
		t.Fatal(err)
	}
	files := exportFiles(t, srv, runID)
	n := 0
	for name := range files {
		if strings.HasPrefix(name, "css/") {
			n++
		}
	}
	js := files["css/"+h+".js"]
	if n != 1 || !strings.Contains(js, `window.TRACEREPORTS_CSS`) || !strings.Contains(js, `rgb(1, 2, 3)`) {
		t.Fatalf("css files: %d %q", n, js)
	}
}
