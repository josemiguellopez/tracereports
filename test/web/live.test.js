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
		{ id: 1, name: "test_agenda.py", started_at: 0, tests: [
			{ id: 11, run_id: 1, name: "test_ok", worker: "gw0", state: "PASS", started_at: 0, ended_at: 60000, elapsed: 60000, factor: 1, p50: 60000 },
			{ id: 12, run_id: 1, name: "test_bad", worker: "gw0", state: "FAIL", started_at: 60000, ended_at: 90000, elapsed: 30000 },
		] },
		{ id: 2, name: "test_ficha.py", started_at: 1000, tests: [
			{ id: 21, run_id: 2, name: "test_periodontograma", worker: "gw1", state: "no_signal", started_at: 1000, elapsed: 599000, idle: 252000, factor: 6.1, p50: 98000, last_step: "Evidencia <final>" },
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
	assert.deepEqual(plain(L.ganttLayout(noWorker, 10).lanes.map((l) => l.worker)), ["P1 · suite.py"]);
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
	assert.ok(/class="lt-state nosig"><svg data-i="i-warning"><\/svg>Sin señal/.test(html), "sin señal con ícono y texto");
	assert.ok(html.includes("2 de 10 tests · 20%") && html.includes("ETA 2 min"));
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
	const same = new Set(["Test", "vs p50", "ETA {t}", "{n} workers", "zoom {a}–{b}", "Worker / test", "Worker"]);
	assert.ok(used.length > 25);
	assert.deepEqual([...new Set(used)].filter((s) => !same.has(s) && en.t(s) === s), []);
});

test("una corrida recién abierta, sin tests (null o []), no rompe la vista", () => {
	const empty = { ...exec, runs: [...exec.runs, { id: 3, name: "test_receta.py", started_at: 2000, tests: null }, { id: 4, name: "x.py", started_at: 3000, tests: [] }] };
	const html = L.render({ list: [empty], exec: empty, now: 600000 }, h);
	assert.ok(html.includes("test_receta.py") && html.includes("Todavía sin tests."));
	assert.equal(L.sortRuns(empty.runs).length, 4);
	assert.ok(L.ganttLayout(empty, 600000).lanes.length >= 1);
});

test("sin worker los carriles se reutilizan: una corrida toma el primer proceso libre", () => {
	const run = (id, name, a, b) => ({ id, name: `Clinico · ${name}`, status: b ? "PASS" : "RUNNING", started_at: a, ...(b ? { ended_at: b } : {}),
		tests: [{ id: id * 10, run_id: id, name: `t${id}`, state: b ? "PASS" : "running", started_at: a, ...(b ? { ended_at: b } : {}) }] });
	// 1 y 2 a la vez; 3 empieza cuando 1 terminó (va a su carril); 4 empieza con 2 y 3 ocupados
	const e = { started_at: 0, running: true, runs: [run(1, "a.py", 0, 10), run(2, "b.py", 1, 50), run(3, "c.py", 12, null), run(4, "d.py", 20, 30)] };
	const lanes = L.lanesOf(e, 100);
	assert.deepEqual(plain(lanes.map((l) => l.label)), ["P1 · c.py", "P2 · b.py", "P3 · d.py"], "sin el prefijo común, con el archivo actual");
	assert.deepEqual(plain(lanes[0].tests.map((t) => t.id)), [10, 30]);
	assert.ok(lanes[0].title.includes("Clinico · a.py") && lanes[0].title.includes("Clinico · c.py"), "el tooltip dice todos los archivos");
	// la suite grande: 79 corridas en 12 procesos → 12 carriles
	const big = liveSuite(1_794_000_000_000).execution.execution;
	assert.equal(big.runs.length, 79);
	assert.equal(L.lanesOf(big, 1_794_000_000_000).length, 12);
});

test("shortNames: sin el prefijo común y con el final si es largo; stepText: sin URLs", () => {
	const m = L.shortNames(["Clinico · test_agenda.py", "Clinico · test_receta.py"]);
	assert.equal(m.get("Clinico · test_agenda.py"), "agenda.py", "lo distintivo: sin \"Clinico · test_\"");
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
	assert.deepEqual(plain(L.filterTests(tests, { q: "agenda" }, true).map((t) => t.id)), [11, 12], "la búsqueda mira todo (archivo)");
	assert.deepEqual(plain(L.filterTests(tests, { q: "EVIDENCIA" }, false).map((t) => t.id)), [21], "y el último paso");
});

test("groupTests: por archivo, worker o estado, lo más severo primero", () => {
	const tests = L.allTests(exec);
	assert.deepEqual(plain(L.groupTests(tests, "file").map((g) => g.key)), ["test_ficha.py", "test_agenda.py"]);
	const w = L.groupTests(tests, "worker");
	assert.deepEqual(plain(w.map((g) => g.key)), ["gw1", "gw0", "gw10"]);
	assert.deepEqual(plain(w[1].tests.map((t) => t.state)), ["FAIL", "PASS"], "dentro, también por severidad");
	assert.deepEqual(plain(L.groupTests(tests, "state").map((g) => g.key)), ["no_signal", "FAIL", "running", "PASS"]);
});

test("render: tiles que filtran, Solo con problemas y los archivos sin problemas en una línea", () => {
	const d = liveSuite(1_794_000_000_000);
	const big = d.execution.execution;
	const html = L.render({ list: d.list.executions, exec: big, now: d.execution.now, ui: {} }, h);
	assert.ok(/data-live-state="FAIL" aria-pressed="false"/.test(html) && html.includes("<b>3</b>"), "tile Fallo con su número");
	assert.ok(/class="live-chip on" data-live-problems aria-pressed="true">Solo con problemas \(6\)/.test(html), "activo por ser grande");
	assert.equal((html.match(/class="lt-row /g) || []).length, 6, "solo los 6 con problemas");
	assert.ok(html.includes("tests sin problemas ocultos"));
	assert.ok(html.includes("carriles sin problemas"), "los carriles sin problemas, colapsados");
	assert.ok(!/>[^<]*token=abc/.test(html) && html.includes('title="Evidencia'), "el último paso visible sin la URL; completo en el tooltip");
	// apagado: los problemas abiertos y el resto en una línea
	const off = L.render({ list: d.list.executions, exec: big, now: d.execution.now, ui: { problems: false } }, h);
	assert.ok(/\d+ archivos sin problemas \(\d+ tests\)/.test(off));
	// un tile elegido: solo esos
	const fails = L.render({ list: d.list.executions, exec: big, now: d.execution.now, ui: { states: ["FAIL"] } }, h);
	assert.equal((fails.match(/class="lt-row /g) || []).length, 3);
	assert.ok(fails.includes("data-live-clear"));
});

test("panel lateral: estado, últimos 5 pasos (lo más nuevo arriba), la captura y abrir el test; todo escapado", () => {
	const logs = Array.from({ length: 7 }, (_, i) => ({ status: i === 6 ? "FAIL" : "INFO", message: `paso <${i}>`, timestamp: i, screenshot: i === 4 ? '/screenshots/a"b.png' : "" }));
	const html = L.render({ list: [exec], exec, now: 600000, ui: {}, panel: { id: 21, test: { id: 21, run_id: 2, name: "x", logs } } }, h);
	const panel = html.slice(html.indexOf("data-live-panel"));
	assert.equal((panel.match(/<li /g) || []).length, 5);
	assert.ok(panel.indexOf("paso &lt;6&gt;") < panel.indexOf("paso &lt;2&gt;"), "lo más nuevo arriba");
	assert.ok(!panel.includes("paso &lt;1&gt;"));
	assert.ok(panel.includes('src="/screenshots/a&quot;b.png"'));
	assert.ok(panel.includes('href="#run=2&view=tests&test=21"') && panel.includes("Abrir el test"));
	assert.ok(panel.includes("Sin señal") && panel.includes("×6.1 p50"));
	assert.ok(L.render({ list: [exec], exec, now: 1, ui: {}, panel: { id: 21 } }, h).includes("Cargando…"), "mientras carga");
	assert.ok(html.includes('class="lt-row nosig sel"'), "la fila del panel queda marcada");
});

test("eje con segundos en tramos cortos; los pasos INFO con ícono de información, no de en curso", () => {
	const short = { ...exec, runs: [{ id: 1, name: "a.py", tests: [{ id: 1, run_id: 1, name: "t", state: "PASS", started_at: 0, ended_at: 40000, elapsed: 40000 }] }], running: false };
	const calls = [];
	L.render({ list: [short], exec: short, now: 40000, ui: {} }, { ...h, clock: (ms, secs) => { calls.push(!!secs); return "x"; } });
	assert.ok(calls.length && calls.every(Boolean), "menos de 10 min: con segundos");
	const long = []; L.render({ list: [exec], exec: { ...exec, running: true }, now: 3_600_000, ui: {} }, { ...h, clock: (ms, secs) => { long.push(!!secs); return "x"; } });
	assert.ok(long.some((x) => !x), "una hora: sin segundos en el eje");
	const html = L.render({ list: [exec], exec, now: 600000, ui: {}, panel: { id: 21, test: { id: 21, logs: [{ status: "INFO", message: "abre", timestamp: 1 }] } } }, h);
	assert.ok(/<li class="info"><span class="lt-state info"><svg data-i="i-info">/.test(html));
});
