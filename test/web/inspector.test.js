// Tests del Inspector (web/inspector.js): los locators que copia el QA (XPath sin mayúsculas ni
// tildes, relativo a un contenedor estable, CSS estable, código para Selenium y Playwright) y que todo
// texto suyo tenga su versión en inglés. La evaluación de XPath/CSS sobre la página va en el e2e.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const files = ["i18n.js", "i18n.en.js", "inspector.js"];
const I = load(files).TraceReportsInspector;
const en = load(files, { lang: "en" }).TraceReportsI18n;
const plain = (v) => JSON.parse(JSON.stringify(v));

// una ficha con dos botones "Guardar" en tarjetas distintas
const nodes = [
	{ i: 0, p: -1, tag: "body", attrs: {}, text: "" },
	{ i: 1, p: 0, tag: "div", attrs: { id: "ficha-cliente", class: "card row" }, text: "" },
	{ i: 2, p: 1, tag: "button", attrs: { class: "btn primary" }, text: "Guardar Envío Exprés" },
	{ i: 3, p: 0, tag: "div", attrs: { id: "x8f3a9c21d", class: "agenda" }, text: "" },
	{ i: 4, p: 3, tag: "button", attrs: { class: "btn" }, text: "Guardar Envío Exprés" },
	{ i: 5, p: 0, tag: "input", attrs: { name: "rut", placeholder: "RUT del cliente" }, text: "" },
	{ i: 6, p: 0, tag: "a", attrs: { "data-testid": "volver" }, text: "Volver" },
	{ i: 7, p: 0, tag: "span", attrs: { class: "price" }, text: "89.990" },
	{ i: 8, p: 0, tag: "span", attrs: {}, text: "Dijo \"no\" y l'otro" },
];
const FROM = "ABCDEFGHIJKLMNOPQRSTUVWXYZÁÉÍÓÚÜÀÈÌÒÙáéíóúüàèìòùÑ";

test("fold: minúsculas, sin tildes y espacios normalizados; la ñ se mantiene", () => {
	assert.equal(I.fold("  Envío   EXPRÉS "), "envio expres");
	assert.equal(I.fold("Ñuñoa"), "ñuñoa");
});

test("lit: comillas simples, dobles o concat()", () => {
	assert.equal(I.lit("hola"), "'hola'");
	assert.equal(I.lit("l'otro"), `"l'otro"`);
	assert.equal(I.lit(`Dijo "no" y l'otro`), `concat('Dijo "no" y l', "'", 'otro')`);
});

test("robustXPath: translate + contains, sin mayúsculas ni tildes", () => {
	assert.equal(I.robustXPath(nodes[2]), `//button[contains(translate(normalize-space(.),'${FROM}','abcdefghijklmnopqrstuvwxyzaeiouuaeiouaeiouuaeiouñ'),'guardar envio expres')]`);
	// un input se identifica por su placeholder, no por lo que alguien escribió
	assert.match(I.robustXPath(nodes[5]), /^\/\/input\[contains\(translate\(@placeholder,/);
	assert.match(I.robustXPath(nodes[5]), /'rut del cliente'\)\]$/);
	assert.equal(I.robustXPath({ tag: "div", attrs: {}, text: "" }), null);
});

test("anchorsOf: contenedores estables; ids autogenerados y clases genéricas no", () => {
	const a = I.anchorsOf(nodes[2], nodes);
	assert.ok(a.includes(`//*[@id='ficha-cliente']`));
	assert.ok(!a.some((x) => x.includes("'row'") || x.includes(" row ")), "row es genérica");
	assert.ok(!I.anchorsOf(nodes[4], nodes).some((x) => x.includes("x8f3a9c21d")), "id autogenerado");
});

test("relativeXPath: el contenedor que lo deja único; por posición como último recurso", () => {
	const base = I.robustXPath(nodes[2]);
	const count = (x) => (x === `//*[@id='ficha-cliente']` + base ? 1 : 2);
	assert.equal(I.relativeXPath(nodes[2], nodes, count), `//*[@id='ficha-cliente']` + base);
	// en la agenda no hay ancla que sirva (id autogenerado, la clase agenda también da 2)
	assert.equal(I.relativeXPath(nodes[4], nodes, () => 2, () => [2, 4]), `(${base})[2]`);
});

test("stableCSS y best: data-testid, id, name; si no, el XPath único", () => {
	assert.equal(I.stableCSS(nodes[6], nodes), `[data-testid="volver"]`);
	assert.equal(I.stableCSS(nodes[5], nodes), `input[name="rut"]`);
	assert.equal(I.stableCSS(nodes[2], nodes), "div.card > button.btn.primary");
	assert.deepEqual({ ...I.best(nodes[6], nodes, () => 1) }, { kind: "css", expr: `[data-testid="volver"]` });
	assert.deepEqual({ ...I.best(nodes[7], nodes, () => 1) }, { kind: "xpath", expr: I.robustXPath(nodes[7]) });
});

test("constName: id o data-testid primero, nunca empieza con dígito", () => {
	assert.equal(I.constName(nodes[6]), "VOLVER");
	assert.equal(I.constName(nodes[1]), "FICHA_CLIENTE");
	assert.equal(I.constName(nodes[7]), "ELEMENTO_89_990");
	assert.equal(I.constName(nodes[2]), "GUARDAR_ENVIO_EXPRES");
});

test("snippets: Selenium y Playwright listos para pegar, espera según el elemento", () => {
	const s = Object.fromEntries(I.snippets(nodes[6], nodes, () => 1).map((x) => [x.id, x.code]));
	assert.equal(s["sel-py"], `driver.find_element(By.CSS_SELECTOR, "[data-testid=\\"volver\\"]")`);
	assert.equal(s["sel-java"], `driver.findElement(By.cssSelector("[data-testid=\\"volver\\"]"))`);
	assert.equal(s["pw-py"], `page.get_by_test_id("volver")`);
	assert.equal(s["pw-js"], `page.getByTestId('volver')`);
	assert.equal(s.po, `VOLVER = (By.CSS_SELECTOR, "[data-testid=\\"volver\\"]")`);
	assert.match(s.wait, /element_to_be_clickable/);
	const b = Object.fromEntries(I.snippets(nodes[2], nodes, (x) => (x.startsWith("//*[@id") ? 1 : 2)).map((x) => [x.id, x.code]));
	assert.equal(b["pw-py"], `page.get_by_role("button", name="Guardar Envío Exprés")`);
	const price = Object.fromEntries(I.snippets(nodes[7], nodes, () => 1).map((x) => [x.id, x.code]));
	assert.match(price.wait, /visibility_of_element_located/, "un texto no es clickeable");
});

test("similarTo: el elemento más parecido a un selector que ya no existe", () => {
	const vis = nodes.map((n) => ({ ...n, visible: true, w: 10, h: 10 }));
	assert.equal(I.similarTo("#btn-volver-old", vis).i, 6);
	assert.equal(I.similarTo("//zzz[@q='qqq']", vis), null);
});

test("plainSelector quita el prefijo de Playwright", () => {
	assert.equal(I.plainSelector("xpath=//a"), "//a");
	assert.equal(I.plainSelector("css=#x"), "#x");
});

test("fromElements: un snapshot básico (clientes anteriores) se dibuja igual", () => {
	const s = I.fromElements({ viewport: { w: 800, h: 600 }, elements: [{ tag: "button", id: "pay", text: "Pagar", x: 1, y: 2, w: 3, h: 4, visible: true }] });
	assert.equal(s.basic, true);
	assert.equal(s.nodes.length, 2);
	assert.equal(s.nodes[1].attrs.id, "pay");
	assert.equal(s.nodes[1].p, 0);
});

test("todo t(\"…\") de inspector.js y del Inspector en app.js tiene traducción", () => {
	const used = [...read("inspector.js").matchAll(/\bt\("((?:[^"\\]|\\.)+)"/g)].map((m) => m[1]);
	used.push("La página del momento del fallo reconstruida: inspecciona elementos, prueba XPath o CSS y copia locators robustos (clic derecho) sin volver a reproducir el fallo.",
		"Cargando la página del test…", "Constante para Page Object (Python)", "Espera explícita (Selenium Python)");
	assert.ok(used.length > 30);
	const missing = [...new Set(used)].filter((s) => en.t(s) === s);
	assert.deepEqual(missing, []);
});

test("diffSnapshots: lo que ya no está (solo la rama de arriba), lo nuevo y lo que cambió", () => {
	const base = [
		{ i: 0, p: -1, tag: "body", attrs: {}, text: "", visible: true },
		{ i: 1, p: 0, tag: "div", attrs: { id: "ficha" }, text: "", visible: true },
		{ i: 2, p: 1, tag: "button", attrs: { id: "btn-guardar", class: "btn" }, text: "Guardar", visible: true },
		{ i: 3, p: 1, tag: "div", attrs: { class: "acciones" }, text: "", visible: true },
		{ i: 4, p: 3, tag: "i", attrs: { class: "fa fa-plus-circle" }, text: "", visible: true },
		{ i: 5, p: 1, tag: "span", attrs: {}, text: "Pendiente", visible: true },
	];
	const cur = [
		{ i: 0, p: -1, tag: "body", attrs: {}, text: "", visible: true },
		{ i: 1, p: 0, tag: "div", attrs: { id: "ficha" }, text: "", visible: true },
		{ i: 2, p: 1, tag: "button", attrs: { id: "btn-guardar", class: "btn disabled" }, text: "Guardar", visible: true }, // clase cambió
		{ i: 3, p: 1, tag: "span", attrs: {}, text: "Guardada", visible: true }, // mismo lugar, otro texto
		{ i: 4, p: 1, tag: "div", attrs: { class: "skeleton" }, text: "", visible: true }, // nuevo
		{ i: 5, p: 4, tag: "div", attrs: { class: "row" }, text: "", visible: true }, // dentro del nuevo: no se repite
	];
	const d = I.diffSnapshots(base, cur);
	assert.deepEqual(plain(d.added), [4]);
	assert.deepEqual(plain(d.removed.map((r) => [r.attrs.class, r.inside])), [["acciones", 1]], "la rama que ya no está, con el ícono adentro");
	const changed = Object.fromEntries(d.changed.map((c) => [c.i, c.fields.map((f) => f.what)]));
	assert.deepEqual(plain(changed), { 2: ["class"], 3: ["text"] });
	assert.equal(I.nodeName(cur[2]), "button#btn-guardar.btn.disabled «Guardar»");
});

test("nodeKeys: un id estable o test-id identifica al elemento aunque se mueva", () => {
	const a = [{ i: 0, p: -1, tag: "body", attrs: {} }, { i: 1, p: 0, tag: "div", attrs: {} }, { i: 2, p: 1, tag: "button", attrs: { "data-testid": "pay" } }];
	const b = [{ i: 0, p: -1, tag: "body", attrs: {} }, { i: 1, p: 0, tag: "button", attrs: { "data-testid": "pay" } }];
	assert.equal(I.nodeKeys(a)[2], I.nodeKeys(b)[1]);
	assert.equal(I.diffSnapshots(a, b).changed.length, 0);
});

// Cómo pinta el Inspector: un modal de Bootstrap al final del body quedaba detrás de la página
// (zIndex = 10 + profundidad) y su backdrop negro con opacity 0.5 salía negro sólido.
const node = (i, p, extra = {}) => ({ i, p, tag: "div", x: 0, y: 0, w: 100, h: 100, style: {}, ...extra });
test("paintPlan: el modal y su backdrop encima de la página, por z-index y orden del documento", () => {
	const nodes = [
		node(0, -1, { tag: "body" }),
		node(1, 0, { style: { position: "relative" } }), // la página, muy profunda
		node(2, 1), node(3, 2), node(4, 3, { style: { position: "relative", zIndex: "5" } }), node(5, 4),
		node(6, 0, { style: { position: "fixed", zIndex: "1030" } }), // la barra
		node(7, 0, { style: { position: "fixed", zIndex: "1050" } }), // el modal
		node(8, 7), node(9, 8, { style: { position: "relative", zIndex: "1" } }), // su contenido
		node(10, 0, { style: { position: "fixed", zIndex: "1040", opacity: "0.5" } }), // el backdrop, al final
	];
	const { rank, opacity } = I.paintPlan(nodes);
	const above = (a, b) => rank[a] > rank[b];
	assert.ok(above(10, 5) && above(10, 6), "el backdrop sobre la página y la barra");
	assert.ok(above(7, 10) && above(8, 10) && above(9, 10), "el modal y todo su contenido sobre el backdrop");
	assert.ok(above(5, 3) && above(4, 3), "dentro de la página, lo que va después encima");
	assert.ok(above(6, 5), "la barra (1030) sobre la página aunque la página tenga un z-index 5 adentro");
	assert.equal(opacity[10], 0.5);
	assert.equal(opacity[5], 1);
});

test("paintPlan: un snapshot viejo (sin zIndex) queda en el orden del documento; la opacidad se hereda", () => {
	const nodes = [node(0, -1), node(1, 0, { depth: 24 }), node(2, 0, { depth: 1, style: { opacity: "0.5" } }), node(3, 2, { style: { opacity: "0.5" } })];
	const { rank, opacity } = I.paintPlan(nodes);
	assert.ok(rank[2] > rank[1], "lo que va después en el documento queda encima, no lo más profundo");
	assert.equal(opacity[3], 0.25);
});

test("paintPlan y clipInset: un panel con scroll recorta a sus hijos; un fixed no se recorta", () => {
	const nodes = [
		node(0, -1, { w: 1000, h: 1000 }),
		node(1, 0, { x: 100, y: 100, w: 300, h: 200, style: { overflowY: "auto", overflowX: "hidden" } }),
		node(2, 1, { x: 100, y: 250, w: 300, h: 100 }), // la mitad afuera
		node(3, 1, { x: 100, y: 400, w: 300, h: 50 }), // entero afuera
		node(4, 1, { x: 0, y: 0, w: 50, h: 50, style: { position: "fixed" } }),
		node(5, 0, { x: 0, y: 900, w: 10, h: 10 }), // fuera de todo panel
	];
	const { clip } = I.paintPlan(nodes);
	assert.equal(I.clipInset(nodes[2], clip[2]), "inset(0px 0px 50px 0px)");
	assert.equal(I.clipInset(nodes[3], clip[3]), "hidden");
	assert.equal(I.clipInset(nodes[4], clip[4]), "");
	assert.equal(I.clipInset(nodes[5], clip[5]), "");
	assert.equal(clip[1], null, "el panel mismo no se recorta a sí mismo");
});

test("íconos: el de fuente va a la izquierda del texto (::before) o a la derecha (::after); solo, centrado; la ligadura reemplaza la palabra", () => {
	const btn = { tag: "button", text: "Agregar", w: 140, h: 36, tx: 36 };
	assert.deepEqual(plain(I.iconBox(btn, { pos: "before", w: 16, h: 20 })), { x: 16, y: 8 }, "pegado al comienzo del texto");
	assert.deepEqual(plain(I.iconBox({ ...btn, tx: 2 }, { pos: "before", w: 16, h: 20 })), { x: 0, y: 8 }, "nunca fuera de su caja");
	assert.deepEqual(plain(I.iconBox(btn, { pos: "after", w: 10, h: 20 })), { x: 128, y: 8 });
	assert.deepEqual(plain(I.iconBox({ tag: "i", text: "", w: 20, h: 20 }, { pos: "before", w: 16, h: 20 })), { x: 2, y: 0 }, "un <i class=fa> solo: centrado");
	const lig = { tag: "span", text: "home", w: 24, h: 24, icons: [{ pos: "text", g: "g0", w: 24, h: 30 }] };
	assert.equal(I.shownText(lig), "", "la palabra de la ligadura no se escribe: se dibuja su ícono");
	assert.equal(I.shownText(btn), "Agregar");
	assert.deepEqual(plain(I.iconBox(lig, lig.icons[0])), { x: 0, y: 0 });
});

test("expandStyles: cada nodo recupera su estilo de la tabla; un snapshot anterior queda igual", () => {
	const snap = { styles: [{ color: "rgb(1, 2, 3)" }, { color: "rgb(4, 5, 6)" }],
		nodes: [{ i: 0, s: 1 }, { i: 1, s: 0 }, { i: 2 }, { i: 3, s: 9 }, { i: 4, style: { color: "red" }, s: 0 }] };
	I.expandStyles(snap);
	assert.deepEqual(plain(snap.nodes.map((n) => n.style?.color ?? null)), ["rgb(4, 5, 6)", "rgb(1, 2, 3)", null, null, "red"]);
	const old = { nodes: [{ i: 0, style: { color: "x" } }] };
	assert.equal(I.expandStyles(old).nodes[0].style.color, "x");
});

test("fidelity: la parte de los píxeles que se parecen, con tolerancia", () => {
	const img = (...px) => ({ data: Uint8ClampedArray.from(px.flatMap((c) => [...c, 255])) });
	const white = [255, 255, 255], grey = [236, 235, 235], black = [0, 0, 0];
	assert.equal(I.fidelity(img(white, grey), img(white, grey)), 1);
	assert.equal(I.fidelity(img(white, grey, grey, grey), img(white, [240, 238, 230], black, black)), 0.5, "un gris parecido cuenta, el negro no");
	assert.equal(I.fidelity({ data: new Uint8ClampedArray(0) }, { data: new Uint8ClampedArray(0) }), 0);
});

test("nearestShot: la captura de un paso es la más cercana a su página, del mismo tipo, y nunca una lejana", () => {
	// el caso del test 5657: la página del warning a T, su captura 3 s después y la última del test 38 s después
	const T = 1794508980691;
	const shots = [{ at: T - 20_000, src: "abrir.png", status: "INFO" }, { at: T + 2_998, src: "warning.png", status: "WARNING" },
		{ at: T + 2_500, src: "info-cerca.png", status: "INFO" }, { at: T + 41_511, src: "ultima.png", status: "INFO" }];
	assert.equal(I.nearestShot(shots, T, "warning"), "warning.png", "la del WARNING, aunque otra esté un poco más cerca");
	assert.equal(I.nearestShot(shots, T, "step"), "info-cerca.png", "un paso: la más cercana");
	assert.equal(I.nearestShot(shots, T + 60_000, "warning"), "", "ninguna a menos de 10 s: no hay con qué comparar");
	assert.equal(I.nearestShot([], T, "warning"), "");
	assert.equal(I.nearestShot(shots, 0, "warning"), "", "una página sin hora no adivina");
});

test("failedRaster: una página rasterizada en blanco contra una captura con contenido no es una cifra", () => {
	const img = (n, ...parts) => ({ data: Uint8ClampedArray.from(parts.flatMap(([c, k]) => Array.from({ length: Math.round(n * k) }, () => [...c, 255])).flat()) });
	const white = [255, 255, 255], dim = [120, 120, 120], panel = [60, 70, 90], blue = [31, 111, 235];
	// el caso del run con un modal: la captura con la página oscurecida detrás y el modal blanco (~9 %)
	const shot = img(100, [dim, 0.6], [panel, 0.31], [white, 0.09]);
	assert.equal(I.failedRaster(img(100, [white, 1]), shot), true, "rasterizada en blanco: dibujo fallido (era el 7 %)");
	assert.equal(I.failedRaster(img(100, [dim, 0.6], [panel, 0.3], [white, 0.1]), shot), false, "la página real como la captura");
	assert.equal(I.failedRaster(img(100, [white, 1]), img(100, [white, 0.95], [blue, 0.05])), false, "una página casi en blanco también en la captura sí cuenta");
	assert.ok(Math.abs(I.dominantShare(shot) - 0.6) < 1e-9);
});

test("htmlSrcdoc: la página con su CSS, una CSP sin red y el CSS del Inspector (íconos, scroll, fijos, animaciones)", () => {
	const PNG = "data:image/png;base64,iVBORw0KGgo=";
	const snap = { scroll: { x: 0, y: 200 }, glyphs: { g0: PNG, g1: "javascript:alert(1)" },
		nodes: [{ i: 0, p: -1, tag: "body" }, { i: 1, p: 0, tag: "i", icons: [{ g: "g0", pos: "before", w: 16, h: 16 }, { g: "g1", pos: "after", w: 9, h: 9 }] },
			{ i: 2, p: 0, tag: "span", icons: [{ g: "g0", pos: "text", w: 24, h: 24 }] },
			{ i: 3, p: 0, tag: "nav", style: { position: "fixed" } }, { i: 4, p: 3, tag: "div", style: { position: "fixed" } }],
		html: { doc: '<html><head></head><body data-tr-i="0"><div data-tr-i="5" data-tr-scroll="0,120"></div></body></html>' } };
	const css = I.htmlExtraCSS(snap, { querySelectorAll: () => [{ getAttribute: (k) => ({ "data-tr-scroll": "0,120", "data-tr-i": "5" })[k] }] });
	assert.match(css, /animation:none!important/);
	assert.match(css, /\[data-tr-i="1"\]::before\{color:transparent!important;background:url\("data:image\/png/);
	assert.doesNotMatch(css, /::before\{[^}]*(content|width|height|display):/, "the icon keeps its own box: the row does not change height");
	assert.doesNotMatch(css, /javascript/);
	assert.match(css, /\[data-tr-i="2"\]\{color:transparent!important;background:url\("data:image\/png/);
	assert.match(css, /\[data-tr-i="5"\]>\*\{translate:0px -120px\}/);
	assert.match(css, /\[data-tr-i="3"\]\{translate:0px 200px\}/);
	assert.doesNotMatch(css, /data-tr-i="4"\]\{translate/, "inside a fixed box, the box already moves");
	const doc = I.htmlSrcdoc(snap, [".a{color:red}</style><script>x</script>", null], null);
	assert.match(doc, /^<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'/);
	assert.doesNotMatch(doc, /<\/style><script>/, "a sheet never closes its <style>");
	assert.match(doc, /<\/head><body data-tr-i="0">/);
});
