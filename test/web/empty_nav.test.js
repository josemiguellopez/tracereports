const test = require("node:test");
const assert = require("node:assert/strict");
const { read } = require("./load.js");

const src = read("app.js");

// renderView de verdad, con un DOM mínimo: las vistas, el estado vacío, el banner y el menú
function harness() {
	const el = (id) => ({ id, hidden: false });
	const views = ["view-tests", "view-categories", "view-dashboard", "view-metrics", "view-settings"].map(el);
	const empty = el("empty-state"), banner = el("run-banner");
	const links = ["tests", "categories", "dashboard", "metrics", "settings"].map((view) => {
		const a = { dataset: { view }, active: false };
		a.classList = { toggle: (_, on) => { a.active = on; } };
		return a;
	});
	const body = new Set();
	const document = { body: { classList: { toggle: (c, on) => (on ? body.add(c) : body.delete(c)), remove: (...cs) => cs.forEach((c) => body.delete(c)) } } };
	const $ = (sel) => ({ "#empty-state": empty, "#run-banner": banner })[sel];
	const $$ = (sel) => ({ ".view": views, ".side-nav a": links })[sel];
	const S = { view: "tests", run: null, runs: [] };
	const rendered = [];
	const r = (name) => () => rendered.push(name);
	const fn = src.slice(src.indexOf("const GLOBAL_VIEWS"), src.indexOf("// ---------- run explorer"));
	const renderView = new Function("S", "$", "$$", "document", "renderRunBanner",
		"renderRunSearch", "renderTests", "renderCategories", "renderExceptions", "renderDashboard",
		"renderAI", "renderEscalate", "renderMetrics", "renderSettings", "renderRelease",
		`${fn}; return renderView;`)(S, $, $$, document, r("banner"),
		r("search"), r("tests"), r("categories"), r("exceptions"), r("dashboard"),
		r("ai"), r("escalate"), r("metrics"), r("settings"), r("release"));
	const visible = () => [...views, empty].filter((v) => !v.hidden).map((v) => v.id);
	const active = () => links.filter((a) => a.active).map((a) => a.dataset.view);
	return { S, renderView, visible, active, body, banner, rendered };
}

test("sin ejecuciones, volver de Ajustes a Tests muestra el estado vacío, no Ajustes", () => {
	const h = harness();
	h.S.view = "settings";
	h.renderView();
	assert.deepEqual(h.visible(), ["view-settings"]);
	assert.ok(h.body.has("view-global"));
	h.S.view = "tests";
	h.renderView();
	assert.deepEqual(h.visible(), ["empty-state"]);
	assert.deepEqual(h.active(), ["tests"]);
	assert.ok(!h.body.has("view-global"));
});

test("sin ejecuciones, de Métricas a Categorías o Análisis no queda Métricas en pantalla", () => {
	const h = harness();
	for (const view of ["metrics", "categories", "metrics", "dashboard"]) {
		h.S.view = view;
		h.renderView();
		assert.deepEqual(h.visible(), view === "metrics" ? ["view-metrics"] : ["empty-state"], view);
		assert.deepEqual(h.active(), [view]);
	}
	assert.ok(!h.body.has("view-dashboard"));
});

test("con ejecuciones pero la elegida aún cargando, se oculta la vista anterior sin el estado vacío", () => {
	const h = harness();
	h.S.runs = [{ id: 1 }];
	h.S.view = "settings";
	h.renderView();
	h.S.view = "tests";
	h.renderView();
	assert.deepEqual(h.visible(), []);
	assert.ok(h.banner.hidden);
});

test("con la ejecución cargada, cada vista se dibuja como antes", () => {
	const h = harness();
	h.S.runs = [{ id: 1 }];
	h.S.run = { id: 1 };
	h.S.view = "categories";
	h.renderView();
	assert.deepEqual(h.visible(), ["view-categories"]);
	assert.deepEqual(h.rendered, ["banner", "categories"]);
});
