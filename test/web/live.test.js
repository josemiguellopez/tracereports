// Tests de En vivo (web/live.js): el Gantt por worker, el orden (lo que pide acción primero), el
// progreso con tests planificados, que lo del cliente entre escapado y que todo texto tenga inglés.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

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
	// sin worker (otro cliente): un carril por corrida
	const noWorker = { ...exec, runs: [{ id: 3, name: "suite.py", tests: [{ id: 1, run_id: 3, name: "a", state: "PASS", started_at: 0, ended_at: 5 }] }] };
	assert.deepEqual(plain(L.ganttLayout(noWorker, 10).lanes.map((l) => l.worker)), ["suite.py"]);
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
	assert.ok(html.includes("20% · 2 de 10 tests") && html.includes("ETA 2 min"));
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
	const same = new Set(["Test", "vs p50", "ETA {t}", "{n} workers"]);
	assert.ok(used.length > 25);
	assert.deepEqual([...new Set(used)].filter((s) => !same.has(s) && en.t(s) === s), []);
});
