// Tests de En vivo (web/live.js): el Gantt por worker (o carriles reutilizados sin worker), el minimapa
// y el zoom, el orden (lo que pide acción primero), "Solo con problemas", los grupos, el panel lateral, el
// progreso con tests planificados, que lo del cliente entre escapado y que todo texto tenga inglés.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");
const { liveSuite } = require("../e2e/fixtures/live_suite.js");

const files = ["i18n.js", "i18n.en.js", "live.js"];
const L = load(files).TraceReportsLive;
const en = load(files, { lang: "en" }).TraceReportsI18n;
const plain = (v) => JSON.parse(JSON.stringify(v));

const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const h = { t: (s, v) => (v ? s.replace(/\{(\w+)\}/g, (m, k) => (k in v ? v[k] : m)) : s), esc, icon: (n) => `<svg data-i="${n}"></svg>`, canAct: () => true, clock: () => "10:00" };

const exec = {
	key: "x:b1", title: "build <b>1</b>", running: true, started_at: 0, planned: 10, total: 4, done: 2, eta_ms: 120000, workers: 2,
	counts: { PASS: 1, FAIL: 1, running: 1, no_signal: 1 },
	runs: [
		{ id: 1, name: "test_cart.py", started_at: 0, tests: [
			{ id: 11, run_id: 1, name: "test_ok", worker: "gw0", state: "PASS", started_at: 0, ended_at: 60000, elapsed: 60000, factor: 1, p50: 60000 },
			{ id: 12, run_id: 1, name: "test_bad", worker: "gw0", state: "FAIL", started_at: 60000, ended_at: 90000, elapsed: 30000 },
		] },
		{ id: 2, name: "test_checkout.py", started_at: 1000, tests: [
			{ id: 21, run_id: 2, name: "test_wishlist", worker: "gw1", state: "no_signal", started_at: 1000, elapsed: 599000, idle: 252000, factor: 6.1, p50: 98000, last_step: "Evidencia <final>" },
			{ id: 22, run_id: 2, name: "test_<script>", worker: "gw10", state: "running", started_at: 500000, elapsed: 100000, idle: 2000 },
		] },
	],
};

test("fmtDur", () => {
	assert.equal(L.fmtDur(75), "0 s");
	assert.equal(L.fmtDur(65000), "1 min 5 s");
	assert.equal(L.fmtDur(120000), "2 min");
	assert.equal(L.fmtDur(3725000), "1 h 2 min");
});

test("ganttLayout: un carril por worker, barras en % del tramo, los en curso hasta ahora", () => {
	const g = L.ganttLayout(exec, 600000);
	assert.deepEqual(plain(g.lanes.map((l) => l.worker)), ["gw0", "gw1", "gw10"], "orden natural de workers");
	const [ok] = g.lanes[0].bars;
	assert.equal(Math.round(ok.left), 0);
	assert.equal(Math.round(ok.width), 10);
	const stuck = g.lanes[1].bars[0];
	assert.equal(Math.round(stuck.left + stuck.width), 100, "sigue corriendo: llega hasta ahora");
	// sin worker (otro cliente): un carril por proceso, con el archivo que corre
	const noWorker = { ...exec, runs: [{ id: 3, name: "suite.py", tests: [{ id: 1, run_id: 3, name: "a", state: "PASS", started_at: 0, ended_at: 5 }] }] };
	assert.deepEqual(plain(L.ganttLayout(noWorker, 10).lanes.map((l) => [l.worker, l.sub])), [["P1", "suite.py"]]);
});

test("sortRuns: la corrida con algo sin señal o tortuga primero", () => {
	assert.deepEqual(plain(L.sortRuns(exec.runs).map((r) => r.id)), [2, 1]);
});

test("progress: % y ETA con planificados; sin ellos, terminados de iniciados", () => {
	assert.deepEqual(plain(L.progress(exec)), { pct: 20, known: true, eta: 120000 });
	assert.deepEqual(plain(L.progress({ ...exec, planned: 0 })), { pct: 50, known: false, eta: 0 });
});

test("ticks: horas redondas dentro del tramo", () => {
	const k = L.ticks(0, 30 * 60000);
	assert.ok(k.length >= 3 && k.length <= 7);
	assert.ok(k.every((x) => x.left >= 0 && x.left <= 100));
});

test("render: lo del cliente entra escapado; lo que pide acción con ícono y texto", () => {
	const html = L.render({ list: [exec], exec, now: 600000, no_signal_ms: 180000 }, h);
	assert.ok(!html.includes("<script>") && html.includes("test_&lt;script&gt;"));
	assert.ok(!html.includes("build <b>") && html.includes("build &lt;b&gt;1&lt;/b&gt;"));
	assert.ok(html.includes("Evidencia &lt;final&gt;"));
	assert.ok(/class="lt-state nosig" data-tip="El test dejó de enviar[^"]*"><svg data-i="i-st-nosig"><\/svg>Sin señal/.test(html), "sin señal con ícono, texto y qué significa");
	assert.ok(html.includes('<b data-count="pct" data-n="20">20</b>') && html.includes("2 de 10 tests") && html.includes("ETA 2 min"));
	assert.ok(html.includes("data-live-cancel"), "un admin puede pedir la cancelación");
	assert.ok(!L.render({ list: [exec], exec, now: 1 }, { ...h, canAct: () => false }).includes("data-live-cancel"), "un viewer no");
	assert.ok(L.render({ list: [exec], exec: { ...exec, cancel_requested: true }, now: 1 }, h).includes("Cancelación pedida"));
	assert.ok(L.render({ list: [], exec: null }, h).includes("No hay ejecuciones en curso"));
});

test("todo t(\"…\") de live.js tiene traducción", () => {
	const src = read("live.js");
	const used = [...src.matchAll(/\bt\("((?:[^"\\]|\\.)+)"/g)].map((m) => m[1]);
	used.push(...Object.values(L.STATES).map((s) => s.label).filter((s) => !["Pass", "Skip", "Warning"].includes(s)));
	// iguales en inglés
	const same = new Set(["Error / test", "{n}/min", "TESTS", "WORKERS", "warning", "{n} tests", "Test", "vs p50", "ETA {t}", "{n} workers", "zoom {a}–{b}", "Worker / test", "Worker", "{n} tests/min", "Endpoint", "{n} endpoints", "Error", "{n} tests", "Total", "p50 {a} · p95 {b}", "Tests", "{a} → {b}", "stack trace"]);
	assert.ok(used.length > 25);
	assert.deepEqual([...new Set(used)].filter((s) => !same.has(s) && en.t(s) === s), []);
});

test("una corrida recién abierta, sin tests (null o []), no rompe la vista", () => {
	const empty = { ...exec, runs: [...exec.runs, { id: 3, name: "test_search.py", started_at: 2000, tests: null }, { id: 4, name: "x.py", started_at: 3000, tests: [] }] };
	const html = L.render({ list: [empty], exec: empty, now: 600000 }, h);
	assert.ok(html.includes("test_search.py") && html.includes("Todavía sin tests."));
	assert.equal(L.sortRuns(empty.runs).length, 4);
	assert.ok(L.ganttLayout(empty, 600000).lanes.length >= 1);
});

test("sin worker los carriles se reutilizan: una corrida toma el primer proceso libre", () => {
	const run = (id, name, a, b) => ({ id, name: `Shop · ${name}`, status: b ? "PASS" : "RUNNING", started_at: a, ...(b ? { ended_at: b } : {}),
		tests: [{ id: id * 10, run_id: id, name: `t${id}`, state: b ? "PASS" : "running", started_at: a, ...(b ? { ended_at: b } : {}) }] });
	// 1 y 2 a la vez; 3 empieza cuando 1 terminó (va a su carril); 4 empieza con 2 y 3 ocupados
	const e = { started_at: 0, running: true, runs: [run(1, "a.py", 0, 10), run(2, "b.py", 1, 50), run(3, "c.py", 12, null), run(4, "d.py", 20, 30)] };
	const lanes = L.lanesOf(e, 100);
	assert.deepEqual(plain(lanes.map((l) => l.label)), ["P1", "P2", "P3"], "la etiqueta corta y fija: el proceso");
	assert.deepEqual(plain(lanes.map((l) => l.sub)), ["c.py", "b.py", "d.py"], "al lado, el archivo actual sin el prefijo común");
	assert.deepEqual(plain(lanes[0].tests.map((t) => t.id)), [10, 30]);
	assert.ok(lanes[0].title.includes("Shop · a.py") && lanes[0].title.includes("Shop · c.py"), "el tooltip dice todos los archivos");
	// la suite grande: 79 corridas en 12 procesos → 12 carriles
	const big = liveSuite(1_794_000_000_000).execution.execution;
	assert.equal(big.runs.length, 79);
	assert.equal(L.lanesOf(big, 1_794_000_000_000).length, 12);
});

test("shortNames: sin el prefijo común y con el final si es largo; stepText: sin URLs", () => {
	const m = L.shortNames(["Shop · test_cart.py", "Shop · test_search.py"]);
	assert.equal(m.get("Shop · test_cart.py"), "cart.py", "lo distintivo: sin \"Shop · test_\"");
	assert.equal(L.shortNames(["solo.py"]).get("solo.py"), "solo.py");
	assert.equal(L.shortNames(["x · " + "a".repeat(60), "x · b"], 20).get("x · " + "a".repeat(60)), "…" + "a".repeat(19));
	assert.equal(L.stepText("Evidencia de carga - url: https://pre.demo/a?token=1"), "Evidencia de carga");
	assert.equal(L.stepText("Abrió https://pre.demo/x y siguió"), "Abrió … y siguió");
});

test("minimap: alto = tests en paralelo frente al máximo; cada columna apilada, lo severo primero", () => {
	const mm = L.minimap(exec, 600000, 10);
	assert.equal(mm.length, 10);
	assert.equal(Math.max(...mm.map((c) => c.h)), 1);
	const first = mm[0];
	assert.ok(Math.abs(first.parts.reduce((s, x) => s + x.f, 0) - 1) < 1e-9);
	assert.equal(first.parts[0].cls, "nosig", "el sin señal va abajo, a la vista");
});

test("zoomStep: + achica a la mitad, − duplica, Ajustar vuelve a todo", () => {
	assert.deepEqual(plain(L.zoomStep(null, "in")), { a: 0.25, b: 0.75 });
	assert.deepEqual(plain(L.zoomStep({ a: 0.25, b: 0.75 }, "in")), { a: 0.375, b: 0.625 });
	assert.equal(L.zoomStep({ a: 0.25, b: 0.75 }, "out"), null, "ya cubre todo");
	assert.deepEqual(plain(L.zoomStep({ a: 0.9, b: 1 }, "out")), { a: 0.8, b: 1 }, "no se sale del tramo");
	assert.equal(L.zoomStep({ a: 0.1, b: 0.2 }, "fit"), null);
	// el Gantt con zoom: solo lo que cae en la ventana, en % de ella
	const g = L.ganttLayout(exec, 600000, { a: 0.5, b: 1 });
	assert.ok(g.lanes.every((l) => l.bars.every((b) => b.left >= 0 && b.left + b.width <= 100.5)));
	assert.equal(g.lanes[0].bars.length, 0, "gw0 terminó antes de la mitad");
});

test("Solo con problemas: activo con más de 20 corridas o carriles, salvo que la persona elija", () => {
	assert.equal(L.problemsOn(exec, {}, 3), false);
	const many = { runs: Array.from({ length: 21 }, (_, i) => ({ id: i })) };
	assert.equal(L.problemsOn(many, {}, 12), true);
	assert.equal(L.problemsOn(many, { problems: false }, 12), false);
	assert.equal(L.problemsOn(exec, {}, 21), true);
	const tests = L.allTests(exec);
	assert.deepEqual(plain(L.filterTests(tests, {}, true).map((t) => t.id)), [12, 21], "fallo y sin señal");
	assert.deepEqual(plain(L.filterTests(tests, { states: ["PASS"] }, true).map((t) => t.id)), [11], "un tile manda sobre el filtro");
	assert.deepEqual(plain(L.filterTests(tests, { q: "cart" }, true).map((t) => t.id)), [11, 12], "la búsqueda mira todo (archivo)");
	assert.deepEqual(plain(L.filterTests(tests, { q: "EVIDENCIA" }, false).map((t) => t.id)), [21], "y el último paso");
});

test("groupTests: por archivo, worker o estado, lo más severo primero", () => {
	const tests = L.allTests(exec);
	assert.deepEqual(plain(L.groupTests(tests, "file").map((g) => g.key)), ["test_checkout.py", "test_cart.py"]);
	const w = L.groupTests(tests, "worker");
	assert.deepEqual(plain(w.map((g) => g.key)), ["gw1", "gw0", "gw10"]);
	assert.deepEqual(plain(w[1].tests.map((t) => t.state)), ["FAIL", "PASS"], "dentro, también por severidad");
	assert.deepEqual(plain(L.groupTests(tests, "state").map((g) => g.key)), ["no_signal", "FAIL", "running", "PASS"]);
});

test("render: tiles que filtran, Solo con problemas y los archivos sin problemas en una línea", () => {
	const d = liveSuite(1_794_000_000_000);
	const big = d.execution.execution;
	const html = L.render({ list: d.list.executions, exec: big, now: d.execution.now, ui: {} }, h);
	assert.ok(/data-live-state="FAIL" aria-pressed="false"/.test(html) && html.includes('<b data-count="FAIL" data-n="3">3</b>'), "tile Fallo con su número");
	assert.ok(/class="seg-o on" data-live-problems aria-checked="true"[^>]*>.*?Con problemas<\/span><b class="seg-n bad">6<\/b>/.test(html), "activo por ser grande");
	assert.ok(/<p class="live-status"[\s\S]*?Mostrando <b data-count="shown" data-n="6">6<\/b> de \d+ tests[\s\S]*?Solo con problemas[\s\S]*?Agrupado por archivo/.test(html), "la línea de estado dice qué se ve");
	assert.equal((html.match(/class="lt-row /g) || []).length, 6, "solo los 6 con problemas");
	assert.ok(html.includes("tests sin problemas ocultos"));
	assert.ok(html.includes("carriles sin problemas"), "los carriles sin problemas, colapsados");
	assert.ok(!html.includes("token=abc") && html.includes('data-tip="Evidencia'), "el último paso sin la URL, ni en la fila ni en el tooltip");
	// apagado: los problemas abiertos y el resto en una línea
	const off = L.render({ list: d.list.executions, exec: big, now: d.execution.now, ui: { problems: false } }, h);
	assert.ok(/\d+ archivos sin problemas \(\d+ tests\)/.test(off));
	// un tile elegido: solo esos
	const fails = L.render({ list: d.list.executions, exec: big, now: d.execution.now, ui: { states: ["FAIL"] } }, h);
	assert.equal((fails.match(/class="lt-row /g) || []).length, 3);
	assert.ok(fails.includes('data-live-facet="state" data-v="FAIL"'), "el filtro activo como chip que se quita");
});

test("panel lateral: estado, últimos 5 pasos (lo más nuevo arriba), la captura y abrir el test; todo escapado", () => {
	const logs = Array.from({ length: 7 }, (_, i) => ({ status: i === 6 ? "FAIL" : "INFO", message: `paso <${i}>`, timestamp: i, screenshot: i === 4 ? '/screenshots/a"b.png' : "" }));
	const html = L.render({ list: [exec], exec, now: 600000, ui: {}, panel: { id: 21, tab: "steps", test: { id: 21, run_id: 2, name: "x", logs } } }, h);
	const panel = html.slice(html.indexOf("data-live-panel"));
	assert.equal((panel.match(/<li /g) || []).length, 5);
	assert.ok(panel.indexOf("paso &lt;6&gt;") < panel.indexOf("paso &lt;2&gt;"), "lo más nuevo arriba");
	assert.ok(!panel.includes("paso &lt;1&gt;"));
	const shotTab = L.render({ list: [exec], exec, now: 600000, ui: {}, panel: { id: 21, tab: "shot", test: { id: 21, run_id: 2, name: "x", logs } } }, h);
	assert.ok(shotTab.includes('src="/screenshots/a&quot;b.png"'));
	assert.ok(panel.includes('href="#run=2&view=tests&test=21"') && panel.includes("Abrir el test"));
	assert.ok(panel.includes("Sin señal") && panel.includes("×6,1 p50"));
	const loading = L.render({ list: [exec], exec, now: 1, ui: {}, panel: { id: 21 } }, h);
	assert.ok(/<div class="lp-skel" role="status" aria-label="Cargando…">(<i><\/i>){6}<\/div>/.test(loading), "mientras carga: un esqueleto del alto de la traza, no un texto que achica el panel");
	assert.ok(html.includes('class="lt-row nosig sel"'), "la fila del panel queda marcada");
});

test("eje con segundos en tramos cortos; los pasos INFO con ícono de información, no de en curso", () => {
	const short = { ...exec, runs: [{ id: 1, name: "a.py", tests: [{ id: 1, run_id: 1, name: "t", state: "PASS", started_at: 0, ended_at: 40000, elapsed: 40000 }] }], running: false };
	const axis = (html) => html.match(/<div class="lg-axis"[^>]*>([\s\S]*?)<\/div>/)[1];
	const mark = { ...h, clock: (ms, secs) => (secs ? "S" : "M") };
	const a1 = axis(L.render({ list: [short], exec: short, now: 40000, ui: {} }, mark));
	assert.ok(a1.includes("S") && !a1.includes("M"), "menos de 10 min: con segundos");
	const a2 = axis(L.render({ list: [exec], exec: { ...exec, running: true }, now: 3_600_000, ui: {} }, mark));
	assert.ok(a2.includes("M") && !a2.includes("S"), "una hora: sin segundos en el eje");
	const html = L.render({ list: [exec], exec, now: 600000, ui: {}, panel: { id: 21, tab: "steps", test: { id: 21, logs: [{ status: "INFO", message: "abre", timestamp: 1 }] } } }, h);
	assert.ok(/<li class="info"><span class="lt-state info"><svg data-i="i-info">/.test(html));
});

test("salud: tasa de fallo, p50/p95, ritmo y la diferencia con la ejecución anterior del mismo proyecto", () => {
	const e = { key: "x:2", title: "build 2", project: "p", running: false, started_at: 0, done: 4, total: 4, counts: { PASS: 2, FAIL: 1, INTERRUPTED: 1 }, runs: [{ id: 1, name: "a.py", tests: [
		{ id: 1, state: "PASS", started_at: 0, ended_at: 60000, elapsed: 60000 },
		{ id: 2, state: "FAIL", started_at: 0, ended_at: 120000, elapsed: 120000 },
		{ id: 3, state: "PASS", started_at: 0, ended_at: 30000, elapsed: 30000 },
		{ id: 4, state: "INTERRUPTED", started_at: 0, ended_at: 240000, elapsed: 240000 },
	] }] };
	const list = [e, { key: "x:1", project: "p", running: false, started_at: -1, done: 10, counts: { FAIL: 1 } }, { key: "x:0", project: "otro", running: false, started_at: -2, done: 4, counts: { FAIL: 4 } }];
	const s = L.health(e, list, 300000);
	assert.equal(s.failRate, 0.5);
	assert.equal(s.p50, 60000);
	assert.equal(s.p95, 240000);
	assert.equal(s.perMin, 1, "4 tests en 4 minutos");
	assert.ok(Math.abs(s.prev.delta - 0.4) < 1e-9, "50% frente al 10% de la anterior del mismo proyecto");
	const html = L.render({ list, exec: e, now: 300000, ui: {} }, h);
	assert.ok(html.includes("Tasa de fallo") && html.includes("▲ +40,0 pts frente a la anterior"));
});

test("errorSignature agrupa el mismo error aunque cambien números y tiempos; conserva el selector", () => {
	const a = L.errorSignature("playwright._impl._errors.TimeoutError: Page.click: Timeout 3000ms exceeded. Call log: - waiting for locator(\"#b\")");
	const b = L.errorSignature("playwright._impl._errors.TimeoutError: Page.click: Timeout 5000ms exceeded.\n  - waiting for locator(\"#b\")");
	assert.equal(a, "TimeoutError: Page.click: Timeout N ms exceeded.");
	assert.equal(a, b);
	assert.equal(L.errorSignature("TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]"), "TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]");
	assert.equal(L.errorSignature("Pedido 123456 sin stock"), L.errorSignature("Pedido 987 sin stock"));
	const tests = [
		{ id: 1, file: "a.py", state: "FAIL", last_step: a, ended_at: 10 }, { id: 2, file: "b.py", state: "FAIL", last_step: b, ended_at: 20 },
		{ id: 3, file: "a.py", state: "INTERRUPTED", last_step: "AssertionError: 1 != 2", ended_at: 5 }, { id: 4, file: "c.py", state: "PASS", last_step: "ok" },
	];
	const g = L.errorGroups(tests);
	assert.deepEqual(plain(g.map((x) => [x.count, x.files.length])), [[2, 2], [1, 1]]);
	assert.equal(g[0].last, 20);
	// la faceta Error filtra por firma
	assert.deepEqual(plain(L.filterTests(tests, { errors: [a] }, false).map((x) => x.id)), [1, 2]);
	assert.deepEqual(plain(L.facets(tests).error.map((x) => x[1])), [2, 1]);
});

test("facetas: cada valor con su cantidad; varias a la vez (Y entre facetas, O dentro de una)", () => {
	const tests = L.allTests(exec);
	const f = L.facets(tests);
	assert.deepEqual(plain(f.state.map((x) => x[0])), ["no_signal", "FAIL", "running", "PASS"]);
	assert.deepEqual(plain(f.file), [["test_cart.py", 2], ["test_checkout.py", 2]]);
	assert.deepEqual(plain(L.filterTests(tests, { files: ["test_cart.py"], states: ["PASS", "FAIL"] }, true).map((x) => x.id)), [11, 12]);
	assert.deepEqual(plain(L.filterTests(tests, { workers: ["gw0", "gw1"] }, false).map((x) => x.id)), [11, 12, 21]);
	assert.deepEqual(plain(L.filterTests(tests, { files: ["test_checkout.py"] }, true).map((x) => x.id)), [21, 22], "una faceta muestra todo lo suyo, no solo los problemas");
	const html = L.render({ list: [exec], exec, now: 600000, ui: { files: ["test_checkout.py"] } }, h);
	assert.ok(/class="lf-item on" role="checkbox" aria-checked="true" data-live-facet="file" data-v="test_checkout.py"/.test(html));
	assert.ok(html.includes('aria-label="Quitar filtro: test_checkout.py"'));
});

test("slowest, events y anomalies: lo lento, lo reciente y dónde cayó cada fallo en el minimapa", () => {
	const tests = L.allTests(exec);
	assert.deepEqual(plain(L.slowest(tests).map((x) => x.id)), [21], "×6.1; los ×1 no");
	const ev = L.events(tests);
	assert.deepEqual(plain(ev.map((x) => x.kind)), ["start", "FAIL", "PASS", "no_signal"]);
	const an = L.anomalies({ ...exec, running: true }, 600000);
	assert.deepEqual(plain(an.map((x) => [x.id, x.state])), [[12, "FAIL"], [21, "no_signal"]]);
	assert.equal(Math.round(an[0].left), 15, "el fallo a los 90 s de 600 s");
	const html = L.render({ list: [exec], exec, now: 600000, ui: {} }, h);
	assert.ok(/class="lg-anom fail" data-live-test="12"[^>]*><svg data-i="i-st-fail"><\/svg><\/button>/.test(html), "con forma y tooltip, no solo color");
	assert.ok(html.includes("Errores agrupados") && html.includes("Más lentos que su historial") && html.includes("Actividad reciente"));
});

test("sortTests y la tabla ordenada: por duración (lo mayor arriba), lista plana y volver a agrupar", () => {
	const tests = L.allTests(exec);
	assert.deepEqual(plain(L.sortTests(tests, { col: "elapsed", dir: -1 }).map((x) => x.id)), [21, 22, 11, 12]);
	assert.deepEqual(plain(L.sortTests(tests, { col: "name", dir: 1 }).map((x) => x.name)), ["test_<script>", "test_bad", "test_ok", "test_wishlist"]);
	assert.equal(L.sortTests(tests, null), tests);
	const html = L.render({ list: [exec], exec, now: 600000, ui: { sort: { col: "elapsed", dir: -1 } } }, h);
	assert.ok(!html.includes("lt-group"), "sin grupos");
	assert.ok(/aria-sort="descending"><button type="button" class="lt-sort on" data-live-sort="elapsed" data-tip-title="Dura\." data-tip="[^"]+">Dura\. ▼/.test(html));
	assert.ok(/data-live-sort="" data-tip="[^"]+">Volver a agrupar/.test(html));
});

test("endpoints: la tarjeta del backend con errores primero, sin la query; endpointOf", () => {
	assert.equal(L.endpointOf("https://app.test/api/x?token=1#a"), "app.test/api/x");
	assert.equal(L.endpointOf("https://app.test"), "app.test/");
	const eps = [{ method: "GET", host: "app.test", path: "/api/customers/:id", count: 4, errors: 1, p95_ms: 30000, test_id: 21 }];
	const html = L.render({ list: [exec], exec, now: 600000, ui: {}, endpoints: eps }, h);
	assert.ok(html.includes('href="#run=2&view=tests&test=21&tab=network"') && html.includes("1 · 25%"));
	assert.ok(L.render({ list: [exec], exec, now: 600000, ui: {}, endpoints: [] }, h).includes("Sin llamadas al backend registradas"));
});

test("traza del test: pasos y llamadas en una línea de tiempo, el fallo marcado, sin estáticos salvo que se pidan", () => {
	const logs = [{ status: "INFO", message: "abre", timestamp: 1000 }, { status: "FAIL", message: "no apareció", timestamp: 9000 }];
	const network = [
		{ id: 1, method: "GET", url: "https://app.test/api/orders?token=x", status: 504, started_at: 2000, duration_ms: 6000, resource_type: "fetch" },
		{ id: 2, method: "GET", url: "https://app.test/app.js", status: 200, started_at: 1500, duration_ms: 10, resource_type: "script" },
		{ id: 3, method: "POST", url: "https://app.test/api/log", status: 0, failed: true, error_text: "net::ERR_ABORTED", started_at: 3000, duration_ms: 0, resource_type: "xhr" },
	];
	const tr = L.traceLayout({ start: 1000, end: 11000, logs, network });
	assert.deepEqual(plain(tr.rows.map((r) => [r.kind, r.label])), [["step", "abre"], ["req", "GET app.test/api/orders"], ["req", "POST app.test/api/log"], ["step", "no apareció"]]);
	assert.equal(tr.hidden, 1);
	assert.equal(Math.round(tr.failAt), 80);
	const req = tr.rows[1];
	assert.ok(req.failed && req.code === "504" && Math.round(req.left) === 10 && Math.round(req.width) === 60);
	assert.equal(tr.rows[2].code, "net::ERR_ABORTED");
	assert.equal(L.traceLayout({ start: 1000, end: 11000, logs, network, all: true }).rows.length, 5);
	const html = L.render({ list: [exec], exec, now: 600000, ui: {}, panel: { id: 21, test: { id: 21, logs }, net: network } }, h);
	assert.ok(html.includes('class="ltr req fail"') && html.includes("lt-trace-fail") && !html.includes("token=x"));
	assert.ok(html.includes("incluir 1 recursos estáticos"));
});

test("gráficos: resultados por tramo, tasa de fallo acumulada e histograma de duraciones (con p50/p95)", () => {
	const mk = (id, state, start, end) => ({ id, run_id: 1, name: `t${id}`, state, started_at: start, ended_at: end, elapsed: end - start });
	const e = { key: "x:g", title: "g", running: false, started_at: 0, done: 5, total: 5, counts: { PASS: 3, FAIL: 1, WARNING: 1 }, runs: [{ id: 1, name: "a.py", tests: [
		mk(1, "PASS", 0, 5_000), mk(2, "FAIL", 0, 70_000), mk(3, "WARNING", 60_000, 130_000), mk(4, "PASS", 100_000, 200_000), mk(5, "PASS", 150_000, 420_000)] }] };
	const b = L.timeBuckets(e, 420_000);
	assert.equal(b.step, 60_000, "7 min: columnas de 1 min");
	const r = L.resultsOverTime(e, 420_000);
	assert.deepEqual(plain(r.cols.slice(0, 3).map((c) => [c.PASS, c.FAIL, c.WARNING])), [[1, 0, 0], [0, 1, 0], [0, 0, 1]]);
	assert.equal(r.cols.reduce((s, c) => s + c.total, 0), 5);
	const fr = L.failRateOverTime(e, 420_000);
	assert.deepEqual(plain(fr.points.slice(0, 3).map((p) => p.rate)), [0, 0.5, 1 / 3]);
	const d = L.durationHistogram(L.allTests(e));
	assert.deepEqual(plain(d.bins.map((x) => x.n)), [1, 0, 0, 3, 1, 0, 0]);
	assert.equal(d.bins[d.p50Bin].label, "1-2 min");
	assert.equal(d.bins[d.p95Bin].label, "2-5 min");
	const html = L.render({ list: [e], exec: e, now: 420_000, ui: {} }, h);
	assert.ok(html.includes("Resultados en el tiempo") && html.includes("Tasa de fallo acumulada") && html.includes("Duración de los tests"));
	assert.ok(/<span class="lch-mark">p50<\/span>/.test(html) && html.includes('<span class="lch-mark">p95</span>') && html.includes('class="lch-stack"'));
	assert.ok(!html.includes("lch-table"), "la tabla de datos, solo a pedido");
	const withTable = L.render({ list: [e], exec: e, now: 420_000, ui: { tables: { dur: true } } }, h);
	assert.ok(/<table class="lch-table">[\s\S]*<td>1-2 min<\/td><td>3<\/td>/.test(withTable));
	const noneDone = { ...exec, runs: [{ id: 1, name: "a.py", tests: [{ id: 1, run_id: 1, name: "x", state: "running", started_at: 0, elapsed: 5 }] }] };
	assert.ok(!L.render({ list: [noneDone], exec: noneDone, now: 10, ui: {} }, h).includes("live-charts"), "sin nada terminado no hay gráficos vacíos");
});

test("selector de ejecución: hora, antigüedad (en curso, reciente, de hoy, anterior) y buscador con muchas", () => {
	const now = 100 * 3600e3;
	const x = (i, over) => ({ key: `x:${i}`, title: `build ${i}`, project: i % 2 ? "acme" : "otro", running: false, started_at: now - (i + 1) * 3600e3, ended_at: now - i * 3600e3 - 60e3, done: 10, total: 10, counts: { FAIL: i === 1 ? 2 : 0 }, ...over });
	assert.equal(L.freshness(x(0, { running: true }), now), "live");
	assert.equal(L.freshness(x(0), now), "recent");
	assert.equal(L.freshness(x(5), now), "today");
	assert.equal(L.freshness(x(30), now), "old");
	assert.equal(L.ago(90e3), "2 min");
	assert.equal(L.ago(3 * 3600e3), "3 h");
	const few = [x(0, { running: true }), x(1, { ended_at: now - 10 * 60e3 })];
	const closed = L.render({ list: few, exec: null, key: "x:1", now, ui: {} }, h);
	assert.ok(/class="xp-btn recent"/.test(closed) && closed.includes("terminó hace") && closed.includes('aria-expanded="false"'));
	assert.ok(/<span class="xp-b fail">[^]*?2<\/span>/.test(closed), "sus fallos a la vista");
	const open = L.render({ list: few, exec: null, key: "x:1", now, ui: { pickerOpen: true } }, h);
	assert.equal((open.match(/role="option"/g) || []).length, 2);
	assert.ok(!open.includes("data-live-xq"), "con pocas, sin buscador");
	const many = Array.from({ length: 40 }, (_, i) => x(i, i === 0 ? { running: true } : {}));
	const big = L.render({ list: many, exec: null, key: "x:0", now, ui: { pickerOpen: true } }, h);
	assert.ok(big.includes("data-live-xq") && big.includes('<li class="xp-group" role="presentation">En curso</li>') && big.includes(">Anteriores</li>"));
	const found = L.render({ list: many, exec: null, key: "x:0", now, ui: { pickerOpen: true, pickerQ: "build 3" } }, h);
	assert.deepEqual([...found.matchAll(/data-live-exec="(x:\d+)"/g)].map((m) => m[1]), ["x:3", "x:30", "x:31", "x:32", "x:33", "x:34", "x:35", "x:36", "x:37", "x:38", "x:39"]);
	assert.ok(L.render({ list: many, exec: null, key: "x:0", now, ui: { pickerOpen: true, pickerQ: "zzz" } }, h).includes("Ninguna ejecución coincide."));
});

// ---------- monitorear rápido lo que falla, legible a 20 workers ----------
const { liveWorkers } = require("../e2e/fixtures/live_suite.js");

test("último paso legible: XPaths acortados, sin URLs y el stack trace aparte", () => {
	assert.equal(L.shortXPaths("en (//i[contains(@class,'fa-plus-circle')])[1] tras 10 s"), "en fa-plus-circle tras 10 s");
	assert.equal(L.shortXPaths("no se encontró '//div[contains(@class, 'modal-dialog')]' , sigue"), "no se encontró 'modal-dialog' , sigue");
	assert.equal(L.shortXPaths("clic en //table/tbody/tr/td"), "clic en td", "sin atributos: la última etiqueta");
	assert.equal(L.shortXPaths("//span[normalize-space()='Pendiente']"), '"Pendiente"', "un XPath de texto: el texto que busca");
	assert.equal(L.shortXPaths("ver https://a.cl/x//y"), "ver https://a.cl/x//y", "una URL no es un XPath");
	const p = L.stepParts("No se encontró '//div[contains(@class, 'modal-dialog')]', detalle del error: Message: Stacktrace: Symbols not available. (No symbol) [0x7ff67e619ca5]");
	assert.equal(p.text, "No se encontró 'modal-dialog', detalle del error");
	assert.ok(p.trace.startsWith("Message: Stacktrace:") && p.trace.includes("0x7ff67e619ca5"));
	assert.deepEqual(plain(L.stepParts("Evidencia del pedido - url: https://x.cl/a?token=1")), { text: "Evidencia del pedido", trace: "", full: "Evidencia del pedido" });
	const java = L.stepParts("NoSuchElementException: boton\n\tat org.openqa.Driver.find(Driver.java:10)");
	assert.equal(java.text, "NoSuchElementException: boton");
	assert.ok(java.trace.includes("Driver.java"));
	const huge = L.stepParts(liveWorkers(1e12).execution.execution.runs[10].tests[0].last_step);
	assert.equal(huge.text, "Cantidad de pedidos pendientes que comience con el nombre de : el xpath fue: btn-secondary en Acme Admin", "un XPath de 300 caracteres: su clase");
});

test("filtros en la URL: &f=fail,warn ida y vuelta; lo desconocido se ignora", () => {
	assert.deepEqual(plain(L.statesFromHash("fail,warn,zzz,FAIL")), ["FAIL", "WARNING"]);
	assert.deepEqual(plain(L.statesFromHash("")), []);
	assert.equal(L.statesToHash(["FAIL", "no_signal", "turtle"]), "fail,nosig,turtle");
	for (const k of Object.keys(L.STATES)) assert.deepEqual(plain(L.statesFromHash(L.statesToHash([k]))), [k], k);
});

test("Requiere atención: fallo, sin señal y tortuga, lo más grave primero y estable; vacío lo dice", () => {
	const d = liveWorkers(1_794_000_000_000), e = d.execution.execution;
	const list = L.attention(L.allTests(e));
	assert.deepEqual(plain(list.map((x) => x.state)), ["no_signal", "FAIL", "turtle"]);
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: {} }, h);
	const card = html.match(/<section class="card live-attn"[\s\S]*?<\/section>/)[0];
	assert.ok(card.includes('<b class="la-n" data-bump="attn">3</b>'));
	assert.equal((card.match(/class="la-row /g) || []).length, 3);
	assert.ok(card.includes("test_002_web_pay_with_saved_card") && card.includes("Shop · test_pay_with_saved_card_chrome") && /la-wk" data-no-i18n>gw\d+/.test(card));
	assert.ok(/data-live-test="\d+"/.test(card), "un clic abre el detalle");
	assert.ok(html.indexOf("live-attn") < html.indexOf("live-gantt"), "arriba, antes de la línea de tiempo");
	const calm = { ...exec, runs: [{ id: 1, name: "a.py", tests: [{ id: 1, run_id: 1, name: "ok", state: "PASS", started_at: 0, ended_at: 5, elapsed: 5 }] }], counts: { PASS: 1 } };
	assert.ok(L.render({ list: [calm], exec: calm, now: 10, ui: {} }, h).includes("Nada requiere atención"));
});

test("tabla: una sola grilla con Worker, último paso en una línea, stack trace a pedido y Expandir/Colapsar todo", () => {
	const d = liveWorkers(1_794_000_000_000), e = d.execution.execution;
	const fail = L.allTests(e).find((x) => x.state === "FAIL");
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false } }, h);
	assert.equal((html.match(/<table class="live-table">/g) || []).length, 1, "todos los archivos en una tabla");
	assert.ok(html.includes('<col class="c-wk">') && /<th><span data-tip="[^"]+">Worker<\/span><\/th><th><span data-tip="[^"]+">Último paso<\/span><\/th>/.test(html));
	const row = html.match(new RegExp(`<tr class="lt-row fail[^>]*data-live-test="${fail.id}"[\\s\\S]*?</tr>`))[0];
	assert.ok(/class="lt-wk" data-no-i18n>gw\d+</.test(row));
	assert.ok(row.includes(">No se encontró el elemento 'modal-dialog', detalle del error</span>"), row);
	assert.ok(row.includes(`data-tip="No se encontró el elemento '//div[contains(@class, 'modal-dialog')]', detalle del error"`), "el tooltip, con el XPath completo y sin el volcado");
	assert.ok(!row.includes("0x7ff") && row.includes(`data-live-stack="${fail.id}" aria-expanded="false"`), "el volcado no va en la fila");
	assert.ok(!html.includes("lt-stackrow"));
	const open = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, stacks: { [fail.id]: true } } }, h);
	assert.ok(/<tr class="lt-stackrow"><td colspan="6"><pre data-no-i18n>Message: Stacktrace:[^<]*0x7ff67e619ca5/.test(open));
	assert.ok(html.includes('data-live-openall="1"') && html.includes('data-live-openall="0"'));
	const closedAll = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, openAll: false } }, h);
	assert.equal((closedAll.match(/class="lt-row /g) || []).length, 0, "Colapsar todo: solo los encabezados");
	const openAll = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, openAll: true } }, h);
	assert.equal((openAll.match(/class="lt-row /g) || []).length, L.allTests(e).length, "Expandir todo: también los sin problemas");
	assert.ok(!openAll.includes("archivos sin problemas ("));
});

test("orden estable: con el mismo estado no salta cuando cambia la duración", () => {
	const mk = (id, started_at, factor) => ({ id, run_id: 1, name: `t${id}`, file: "a.py", state: "running", started_at, factor });
	const a = L.groupTests([mk(1, 0, 1), mk(2, 10, 3)], "file")[0].tests.map((x) => x.id);
	const b = L.groupTests([mk(1, 0, 4), mk(2, 10, 0.5)], "file")[0].tests.map((x) => x.id);
	assert.deepEqual(plain(a), [1, 2]);
	assert.deepEqual(plain(b), [1, 2]);
});

test("chips: Todos, en 0 visibles pero deshabilitados; el Gantt atenúa lo que no coincide y etiqueta gw3", () => {
	const d = liveWorkers(1_794_000_000_000), e = d.execution.execution;
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false } }, h);
	assert.ok(/class="seg-o on" data-live-all aria-checked="true"[^>]*>.*?Todos<\/span><b class="seg-n">\d+<\/b>/.test(html));
	assert.ok(/data-live-state="SKIP"/.test(html) === false && /data-live-state="INTERRUPTED"/.test(html) === false, "Skip e Interrumpido solo si hay");
	const zero = { ...e, counts: { ...e.counts, turtle: 0 } };
	assert.ok(/data-live-state="turtle" aria-pressed="false" disabled/.test(L.render({ list: d.list.executions, exec: zero, now: d.execution.now, ui: {} }, h)));
	assert.ok(/<span class="lg-worker"[^>]*><b>(<i class="lg-live" aria-hidden="true"><\/i>)?<span data-no-i18n>gw0<\/span><\/b><small data-no-i18n>[^<]+<\/small><\/span>/.test(html), "gw0 y el archivo actual");
	assert.ok(!/<b>Shop · t/.test(html));
	assert.ok(!html.includes(" dim\""), "sin filtro no se atenúa nada");
	const f = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { states: ["FAIL"] } }, h);
	const bars = [...f.matchAll(/class="lg-bar ([^"]+)"/g)].map((m) => m[1]);
	assert.ok(bars.some((c) => c.startsWith("fail") && !c.includes("dim")) && bars.filter((c) => !c.startsWith("fail")).every((c) => c.includes("dim")));
	assert.ok(/class="lg-bar fail[^"]*"[^>]*data-tip-title="test_002_web_pay_with_saved_card" data-tip="[^"]*10:00 → 10:00/.test(f), "tooltip con nombre, archivo, estado, duración e inicio → fin");
	assert.ok(/class="lg-bar fail[^"]*"[^>]*><svg data-i="i-st-fail"><\/svg>/.test(f), "la forma del estado (su ícono) además del color");
});
// ---------- ronda 2 (run 1084): cómo se ven los datos ----------

test("línea de tiempo sin vacío: empieza en el primer test y comprime las pausas largas (≈) con su duración", () => {
	const MIN = 60000;
	const mk = (id, a, b) => ({ id, run_id: 1, name: `t${id}`, state: "PASS", started_at: a, ended_at: b, elapsed: b - a });
	// el lanzador tardó 10 min en abrir la corrida y hubo 30 min sin nada entre dos tandas
	const e = { key: "x:g", running: false, started_at: 0, counts: {}, runs: [{ id: 1, name: "a.py", started_at: 10 * MIN, tests: [mk(1, 10 * MIN, 15 * MIN), mk(2, 15 * MIN, 20 * MIN), mk(3, 50 * MIN, 55 * MIN)] }] };
	const sc = L.timeScale(e, 60 * MIN);
	assert.equal(sc.from, 10 * MIN, "el eje empieza en el primer test, no en el inicio de la ejecución");
	assert.equal(sc.gaps.length, 1);
	assert.deepEqual([sc.gaps[0].a, sc.gaps[0].b, sc.gaps[0].dur], [20 * MIN, 50 * MIN, 30 * MIN]);
	assert.ok(sc.gaps[0].x1 - sc.gaps[0].x0 < 0.05, "la pausa de 30 min ocupa un ancho chico");
	assert.ok(Math.abs(sc.x(55 * MIN) - 1) < 1e-9 && sc.x(10 * MIN) === 0);
	for (const t of [11, 19, 21, 49, 51].map((m) => m * MIN)) assert.ok(Math.abs(sc.t(sc.x(t)) - t) < 1, "x y t son inversas");
	const g = L.ganttLayout(e, 60 * MIN);
	assert.equal(g.gaps.length, 1);
	assert.equal(g.nowLeft, null, "terminada: sin línea de ahora");
	const bars = g.lanes[0].bars;
	assert.ok(bars[2].left - (bars[1].left + bars[1].width) < 5, "la tanda de después queda al lado, no al final de un vacío");
	assert.ok(L.scaleTicks(g).every((k) => !(k.at > 20 * MIN && k.at < 50 * MIN)), "sin marcas dentro del corte");
	const html = L.render({ list: [e], exec: e, now: 60 * MIN, ui: { problems: false } }, h);
	assert.ok(/class="lg-gap"[^>]*><b>≈<\/b><small>30 min<\/small>/.test(html), "el corte dice cuánto duró");
	assert.ok(html.includes("1 pausas comprimidas (≈)") && html.includes("= pausa comprimida"));
	// en curso: la línea de "ahora" al final
	const run = { ...e, running: true, runs: [{ ...e.runs[0], tests: [...e.runs[0].tests.slice(0, 2), { ...mk(3, 50 * MIN, 0), ended_at: undefined, state: "running" }] }] };
	assert.ok(Math.abs(L.ganttLayout(run, 60 * MIN).nowLeft - 100) < 0.01);
	assert.ok(L.render({ list: [run], exec: run, now: 60 * MIN, ui: {} }, h).includes('class="lg-now"'));
	// sin pausas: lineal, como antes
	assert.equal(L.timeScale(exec, 600000).gaps.length, 0);
});

test("P1, P2…: los carriles inferidos dicen Proceso N, con su explicación y una leyenda (también ×1.7)", () => {
	const d = liveSuite(1_794_000_000_000);
	const html = L.render({ list: d.list.executions, exec: d.execution.execution, now: d.execution.now, ui: { allLanes: true } }, h);
	assert.ok(/<span data-no-i18n>Proceso 1<\/span>/.test(html) && !/>P1</.test(html), "nunca la sigla sola");
	assert.ok(/data-tip-title="Proceso 1" data-tip="Carril inferido: el cliente no informa el worker/.test(html));
	assert.ok(html.includes("<b>Proceso N</b> = carril en paralelo") && html.includes("<b>×1,7</b> = 1,7 veces lo normal"));
	assert.ok(html.includes("Duración típica · lenta") && !html.includes("Duración p50 · p95"), "sin siglas sueltas en la salud");
	assert.ok(/data-live-sort="factor"[^>]*>vs normal/.test(html), "la columna dice qué compara");
	assert.ok(/class="lt-vs hi" data-tip="Tardó 8,3 veces lo normal para este test/.test(html), "×8,3 explicado en su tooltip (decimales con coma, como en Chile)");
});

test("nombres largos: elipsis en el medio (conserva el comienzo y el final), copiar y 2 líneas", () => {
	const long = "test_004_web_checkout_agregar_tarjeta_regalo_con_mensaje_paso_3";
	const [a, b] = L.splitName(long);
	assert.equal(a + b, long);
	assert.ok(a.startsWith("test_004_") && b.endsWith("paso_3") && b.length >= 10, "el final, que distingue un test de otro, se ve siempre");
	assert.ok(/^[_\s·./:-]/.test(b) || /[_\s·./:-]$/.test(a), "se corta en un separador");
	assert.deepEqual(plain(L.splitName("test_ok")), ["test_ok", ""], "un nombre corto, entero");
	const e = { ...exec, runs: [{ ...exec.runs[0], tests: [{ ...exec.runs[0].tests[1], name: long }] }] };
	const html = L.render({ list: [e], exec: e, now: 600000, ui: { problems: false } }, h);
	assert.ok(html.includes(`<span class="mid" data-no-i18n><span class="mid-a">${a}</span><span class="mid-b">${b}</span></span>`));
	assert.ok(html.includes(`data-live-copy="${long}"`) && html.includes(`data-tip-title="${long}"`), "copiar y el nombre completo en el tooltip");
	assert.ok(html.includes("data-live-resize") && html.includes("data-live-wrap"), "columna redimensionable y nombres en 2 líneas");
	assert.ok(L.render({ list: [e], exec: e, now: 600000, ui: { wrap: true, nameW: 420 } }, h).includes('<col class="c-name" style="width:420px">'));
	assert.ok(/class="live-card[^"]*"|class="card live-runs wrap-names"/.test(L.render({ list: [e], exec: e, now: 600000, ui: { wrap: true } }, h)));
});

test("Mostrar y Agrupar por: controles segmentados con su contador, y la línea de estado de lo que se ve", () => {
	const d = liveWorkers(1_794_000_000_000), e = d.execution.execution;
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, group: "worker" } }, h);
	assert.ok(/<div class="seg" role="radiogroup" aria-label="Mostrar" data-seg="show"><span class="seg-thumb"/.test(html));
	assert.ok(/<div class="seg" role="radiogroup" aria-label="Agrupar por" data-seg="group">/.test(html));
	assert.equal((html.match(/role="radio" class="seg-o on"/g) || []).length, 3, "una opción elegida en cada control (Mostrar, Agrupar por y la vista del mapa)");
	assert.ok(/class="seg-o on" data-live-groupby="worker" aria-checked="true" data-tip-title="Agrupar por worker"/.test(html));
	assert.ok(/<svg data-i="i-cpu"><\/svg><span>Worker<\/span>/.test(html), "con ícono");
	assert.ok(/Agrupado por worker<\/span><\/p>/.test(html));
	assert.ok(html.includes('<i class="live-busybar" aria-hidden="true"></i>'), "la barra de trabajo que aparece tras un clic");
	const st = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { states: ["FAIL"] } }, h);
	assert.equal((st.match(/role="radio" class="seg-o on"/g) || []).length, 2, "con un tile elegido, Mostrar no marca Todos ni Con problemas (quedan Agrupar por y la vista)");
	assert.ok(/<span>Fallo<\/span>\s*<span class="ls-sep">·<\/span><span>Agrupado por archivo/.test(st));
});

test("tooltips en todo: cada botón de la vista dice qué hace", () => {
	for (const ui of [{ problems: false }, { problems: true }, { sort: { col: "elapsed", dir: -1 } }]) {
		const d = liveSuite(1_794_000_000_000);
		const html = L.render({ list: d.list.executions, exec: d.execution.execution, now: d.execution.now, ui }, h);
		const bare = [...html.matchAll(/<(button|summary)\b[^>]*>/g)].map((m) => m[0]).filter((b) => !/data-tip=/.test(b));
		assert.deepEqual(bare, [], JSON.stringify(ui));
	}
	// un ícono sin texto siempre lleva aria-label
	const d = liveSuite(1_794_000_000_000);
	const html = L.render({ list: d.list.executions, exec: d.execution.execution, now: d.execution.now, ui: { zoom: { a: 0, b: 0.5 } } }, h);
	for (const dir of ["in", "out"]) assert.ok(new RegExp(`data-live-zoom="${dir}" aria-label="[^"]+"`).test(html));
});

test("íconos propios: cada estado con el suyo (la tortuga es un ícono, no un emoji) y definidos en index.html", () => {
	const sprite = read("index.html");
	for (const st of Object.values(L.STATES)) {
		assert.ok(st.icon.startsWith("i-st-") && sprite.includes(`<symbol id="${st.icon}"`), st.icon);
		assert.ok(st.tip && en.t(st.tip) !== st.tip, `qué significa ${st.label}, también en inglés`);
	}
	assert.equal(new Set(Object.values(L.STATES).map((s) => s.icon)).size, Object.keys(L.STATES).length, "un ícono distinto por estado");
	for (const id of Object.values(L.GROUPS).map((g) => g.icon).concat(["i-zoom-in", "i-zoom-out", "i-fit", "i-wrap", "i-unfold", "i-fold"])) assert.ok(sprite.includes(`<symbol id="${id}"`), id);
	for (const g of Object.values(L.GROUPS)) assert.ok(en.t(g.tip) !== g.tip && (en.t(g.label) !== g.label || ["Worker", "Error"].includes(g.label)), g.label);
	const d = liveSuite(1_794_000_000_000);
	const html = L.render({ list: d.list.executions, exec: d.execution.execution, now: d.execution.now, ui: { allLanes: true } }, h);
	assert.ok(!/🐢|✕/.test(html), "sin emojis ni caracteres sueltos como ícono");
	assert.ok(/class="lg-bar turtle[^"]*"[^>]*><svg data-i="i-st-turtle"><\/svg>/.test(html));
});

test("mapa de la ejecución: un cuadro por test (y los pendientes), atenuado según el filtro; cinta, reloj y sparklines", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const n = L.allTests(e).length;
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, fieldView: "grid" } }, h);
	assert.equal((html.match(/class="hm-c [^"]*" data-live-test=/g) || []).length, n);
	assert.equal((html.match(/class="hm-c pending"/g) || []).length, e.planned - n, "los planificados que faltan, vacíos");
	assert.ok(!/class="hm-c [a-z]+ dim"/.test(html), "con Todos no se atenúa nada");
	const pr = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { fieldView: "grid" } }, h);
	assert.equal((pr.match(/class="hm-c [a-z]+" data-live-test/g) || []).length, 6, "con problemas: solo esos a color");
	assert.ok(html.includes("data-live-tape-track") && /class="tp-tag on"><i class="live-dot on"[^>]*><\/i>EN VIVO/.test(html), "la cinta de eventos");
	assert.ok(html.includes("data-live-clock"));
	assert.ok(/<svg class="spark bad"/.test(html) && /<svg class="spark pace"/.test(html), "sparklines de tasa de fallo y ritmo");
	assert.equal(L.sparkline([1]), "", "un punto no es una tendencia");
});

// ---------- ronda 3: filtros que se ven, fallos nuevos, detalle de errores, cierre y atajos ----------

test("línea de tiempo con un filtro: lo que coincide resalta, los carriles sin coincidencias se juntan, el minimapa cuenta solo eso", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const all = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false } }, h);
	const f = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { states: ["FAIL"] } }, h);
	assert.ok(f.includes('<span class="lg-count" role="status">3 de 234 tests coinciden</span>'), "la cuenta al lado del título");
	assert.equal((f.match(/class="lg-lane /g) || []).length, 3, "solo los carriles con un fallo");
	assert.ok(f.includes("9 carriles sin coincidencias"));
	assert.equal((f.match(/class="lg-bar fail[^"]* hit"/g) || []).length, 3, "los que coinciden, resaltados");
	assert.ok(!all.includes("lg-count") && !/ hit"/.test(all), "sin filtro, nada resaltado");
	const mmAll = L.minimap(e, d.execution.now), only = new Set(L.allTests(e).filter((x) => x.state === "FAIL").map((x) => x.id));
	const mmF = L.minimap(e, d.execution.now, 80, only);
	assert.ok(mmF.reduce((s, c) => s + c.n, 0) < mmAll.reduce((s, c) => s + c.n, 0) / 10, "el minimapa filtrado cuenta solo los fallos");
	// el nombre del carril no se tiñe: lo que pide acción va en su marca con la cuenta
	assert.ok(/<span data-no-i18n>Proceso \d+<\/span><i class="lg-flag" data-tip="1 tests con problema en este carril/.test(all));
});

test("Requiere atención: buscador y filtro propios (también 'nuevos'), y la marca de fallo nuevo o que ya fallaba", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const fails = L.allTests(e).filter((x) => x.state === "FAIL");
	const regressions = { [fails[0].id]: "new", [fails[1].id]: "old" };
	const base = { list: d.list.executions, exec: e, now: d.execution.now, regressions };
	const html = L.render({ ...base, ui: {} }, h);
	assert.ok(/data-live-attnstate="new"[^>]*>1 nuevo<\/button>/.test(html), "el chip de nuevos con su cuenta (y el singular bien escrito)");
	assert.ok(html.includes('class="lt-reg new"') && html.includes('class="lt-reg old"'), "Nuevo y Ya fallaba");
	assert.ok(html.includes("data-live-aq"), "el buscador");
	const q = L.render({ ...base, ui: { attnQ: "cart" } }, h);
	assert.equal((q.match(/<li style="--i:\d+" data-attn=/g) || []).length, 1);
	assert.ok(q.includes('<p class="la-status" role="status">1 de 6</p>'));
	const onlyNew = L.render({ ...base, ui: { attnStates: ["new"] } }, h);
	assert.ok(onlyNew.includes(`data-attn="${fails[0].id}"`) && !onlyNew.includes(`data-attn="${fails[1].id}"`));
	assert.ok(L.render({ ...base, ui: { attnQ: "zzz" } }, h).includes("Nada coincide con la búsqueda o el filtro."));
});

test("errores agrupados: cada firma se despliega con sus tests; la lupa filtra aparte", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const sig = L.errorGroups(L.allTests(e))[0].sig;
	const closed = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: {} }, h);
	assert.ok(closed.includes(`data-live-errgroup="${sig.replace(/"/g, "&quot;")}" aria-expanded="false"`) && !closed.includes('class="lc-who"'));
	assert.ok(/class="lc-filter " data-live-facet="error"/.test(closed), "filtrar es un botón aparte");
	const open = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { errOpen: { [sig]: true } } }, h);
	assert.equal((open.match(/class="lc-whorow" data-live-test=/g) || []).length, 3, "los tres tests con ese error, cada uno abre su detalle");
});

test("cierre, carril en el panel y atajos: lo que se ve al terminar, dónde cayó el test, y la ayuda", () => {
	const d = liveSuite(1_794_000_000_000), e = { ...d.execution.execution, running: false, ended_at: d.execution.now };
	const fails = L.allTests(e).filter((x) => x.state === "FAIL");
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { help: true }, regressions: { [fails[0].id]: "new" }, panel: { id: fails[0].id } }, h);
	assert.ok(html.includes('class="card live-summary bad"') && html.includes("1 fallos nuevos frente a la ejecución anterior."));
	assert.ok(/href="#run=\d+&view=escalate"/.test(html), "escalar en un clic");
	assert.ok(!L.render({ list: d.list.executions, exec: d.execution.execution, now: d.execution.now, ui: {} }, h).includes("live-summary"), "en curso: sin resumen de cierre");
	assert.ok(/<div class="lp-strip"><small>Su carril: Proceso \d+/.test(html) && html.includes('lp-bar fail me"'), "el panel con el carril del test, marcado");
	assert.ok(html.includes("data-live-help") && html.includes("<kbd>J / K</kbd>"), "la ayuda de atajos");
	assert.ok(html.includes("data-live-compact"), "el modo compacto");
});

test("errores agrupados: cuántos son nuevos se ve con la firma cerrada; adentro, los nuevos primero y los demás a pedido", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const fails = L.allTests(e).filter((x) => x.state === "FAIL");
	const regressions = { [fails[2].id]: "new", [fails[0].id]: "old", [fails[1].id]: "old" };
	const sig = L.errorGroups(fails)[0].sig;
	const closed = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: {}, regressions }, h);
	assert.ok(/class="lc-tally new"[^>]*>1 nuevo<\/span><span class="lc-tally old"[^>]*>2 ya fallaban<\/span>/.test(closed), "la cuenta, cerrada");
	assert.ok(closed.includes('class=" has-new"') || /class="[^"]*has-new"/.test(closed));
	const open = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { errOpen: { [sig]: true } }, regressions }, h);
	const ids = [...open.matchAll(/class="lc-whorow" data-live-test="(\d+)"/g)].map((m) => Number(m[1]));
	assert.equal(ids[0], fails[2].id, "el fallo nuevo, primero");
});

test("las marcas de los fallos se entienden: leyenda, 'Primera vez' sin historial, 'revisando…' mientras tanto (nunca un hueco)", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const fails = L.allTests(e).filter((x) => x.state === "FAIL");
	const sig = L.errorGroups(fails)[0].sig;
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { errOpen: { [sig]: true } }, regressions: { [fails[0].id]: "first", [fails[1].id]: "new" } }, h);
	assert.ok(html.includes('<p class="lc-legend">'), "qué quiere decir cada marca");
	assert.ok(/class="lc-tally first"[^>]*>1 primera vez</.test(html));
	const who = html.slice(html.indexOf('<ol class="lc-who">'), html.indexOf("</ol>", html.indexOf('<ol class="lc-who">')));
	assert.equal((who.match(/class="lt-reg (new|old|first|wait)"/g) || []).length, 3, "cada test con una marca");
	assert.ok(who.includes('class="lt-reg wait"') && who.includes('class="lt-reg first"'));
	// en pausa, el cuadro de ingesta lo dice y trae Reanudar (no hay aviso flotante encima del campo)
	const p = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { paused: true }, pausedNew: 7 }, h);
	assert.ok(/class="lf-hud tl paused"[\s\S]*?VISTA EN PAUSA[\s\S]*?<b data-live-pausecount>7<\/b>[\s\S]*?class="cf-btn cf-btn-sm lf-resume" data-live-pause/.test(p));
	assert.ok(!p.includes("lf-pausebar"));
});

test("Requiere atención abierta: su propio scroll y 'ver menos' también arriba (no hay que bajar al final para cerrarla)", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	let k = 0;
	for (const r of e.runs) for (const x of r.tests) if (x.state === "PASS" && k++ < 20) x.state = "FAIL";
	const closed = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: {} }, h);
	assert.ok(!closed.includes("la-scroll") && !closed.includes("la-less") && /data-live-attnmore[^>]*>.*ver \d+ más/.test(closed));
	const open = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { attnAll: true } }, h);
	assert.ok(open.includes('<div class="la-scroll" tabindex="0"'), "la lista con scroll propio");
	assert.ok(/class="cf-btn cf-btn-sm la-less" data-live-attnmore/.test(open), "ver menos en el encabezado");
	assert.equal((open.match(/data-live-attnmore/g) || []).length, 2, "arriba y abajo");
});

test("agrupar por error: una fila por causa con sus afectados (el selector resumido) y lo que no falló al final", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const g = L.groupTests(L.allTests(e), "error");
	const sig = L.errorSignature(L.allTests(e).find((x) => x.state === "FAIL").last_step);
	assert.equal(g[0].key, sig, "la causa primero (los fallos son lo más grave)");
	assert.equal(g[0].tests.length, 3, "1 causa, 3 afectados");
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, group: "error" } }, h);
	assert.ok(/data-live-groupby="error" aria-checked="true"/.test(html) && html.includes("Error / test"));
	assert.ok(html.includes("<b data-no-i18n>TimeoutException en fa-plus-circle</b>"), "la firma con el selector resumido, legible");
	assert.ok(html.includes("<b data-no-i18n>Sin fallo</b>"), "lo que no falló, junto");
});

test("la tabla dibuja de a 100 filas: el resto detrás de 'Mostrar 100 más' (la página ya no se arrastra con 1.000 filas)", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution, total = L.allTests(e).length;
	assert.ok(total > 200, "la suite de prueba es grande");
	const rows = (ui) => (L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, sort: { col: "name", dir: 1 }, ...ui } }, h).match(/<tr class="lt-row/g) || []).length;
	assert.equal(rows({}), 100, "de entrada, 100");
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, sort: { col: "name", dir: 1 } } }, h);
	assert.ok(html.includes(`Mostrar 100 más (${total - 100} sin mostrar)`) && html.includes("data-live-rowsmore"));
	assert.equal(rows({ rowLimit: 200 }), 200);
	assert.equal(rows({ rowLimit: 1000 }), total, "con todas a la vista, sin botón");
	assert.ok(!L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, sort: { col: "name", dir: 1 }, rowLimit: 1000 } }, h).includes("data-live-rowsmore"));
	// agrupada: el mismo presupuesto repartido entre los grupos abiertos, con el aviso en el grupo cortado
	const grouped = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, openAll: true, group: "state" } }, h);
	assert.equal((grouped.match(/<tr class="lt-row/g) || []).length, 100);
	assert.ok(/y \d+ más en este grupo/.test(grouped) && grouped.includes("data-live-rowsmore"));
	for (const s of ["Mostrar {n} más ({t} sin mostrar)", "y {n} más en este grupo", "Dibuja {n} filas más. Para encontrar algo puntual, busca o filtra: es más rápido."]) assert.notEqual(en.t(s), s, `${s} en inglés`);
});

test("minimapa: las anomalías muy juntas van en una marca con su número (no montadas); la peor primero", () => {
	const m = L.anomalyMarks([{ left: 10, state: "no_signal" }, { left: 10.8, state: "FAIL" }, { left: 11.6, state: "FAIL" }, { left: 40, state: "FAIL" }]);
	assert.equal(m.length, 2, "tres a menos de 1,5 % entre sí: una marca");
	assert.equal(m[0].items.length, 3);
	assert.ok(m[0].left > 10 && m[0].left < 11.6, "al centro del grupo");
	assert.equal(m[0].items[0].state, "no_signal", "lo más grave manda el color");
	assert.equal(m[1].items.length, 1);
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: {} }, h);
	const marks = [...html.matchAll(/class="lg-anom [^"]*" [^>]*style="left:([\d.]+)%/g)].map((x) => Number(x[1])).sort((a, b) => a - b);
	assert.ok(marks.every((x, i) => i === 0 || x - marks[i - 1] >= 1.5), "ninguna marca encima de otra");
});

test("números como se escriben en cada idioma (es-CL con coma) y el p95 corto en ms, no '0 s'", () => {
	assert.equal(L.fmtMs(180), "180 ms");
	assert.equal(L.fmtMs(0), "0 ms");
	assert.ok(!L.fmtMs(2400).includes("ms"));
	assert.equal(L.num1(6.13), "6,1");
	const E = load(files, { lang: "en" }).TraceReportsLive;
	assert.equal(E.num1(6.13), "6.1");
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const html = L.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: {}, endpoints: [{ method: "GET", path: "/api/x", n: 3, errors: 0, p95_ms: 180 }] }, h);
	assert.ok(!/\d\.\d tests\/min/.test(html), "el ritmo con coma");
});

test("cambiar de test con el panel abierto: el encabezado ya es del nuevo y lo anterior queda atenuado hasta que llega (nunca vacío)", () => {
	const old = { id: 11, run_id: 1, name: "t_ok", logs: [{ status: "INFO", message: "paso del anterior", timestamp: 1 }] };
	const html = L.render({ list: [exec], exec, now: 600000, ui: {}, panel: { id: 21, tab: "steps", test: old, stale: true } }, h);
	const panel = html.slice(html.indexOf("data-live-panel"));
	assert.ok(/<h4 data-no-i18n>test_wishlist<\/h4>/.test(panel), "el nombre del test elegido, al instante (de los datos en vivo)");
	assert.ok(panel.includes('<div class="live-pbody is-stale" role="tabpanel" aria-busy="true">'), "el cuerpo anterior, atenuado y marcado como cargando");
	assert.ok(panel.includes("paso del anterior") && !panel.includes("lp-skel"), "no se vacía ni pasa por el esqueleto");
	assert.ok(panel.includes('href="#run=2&view=tests&test=21"'), "Abrir el test ya lleva al nuevo");
	const fresh = L.render({ list: [exec], exec, now: 600000, ui: {}, panel: { id: 21, tab: "steps", test: { id: 21, run_id: 2, name: "t_fail", logs: [] }, stale: false } }, h);
	assert.ok(fresh.includes('<div class="live-pbody" role="tabpanel">'), "llegó: sin atenuar");
});
