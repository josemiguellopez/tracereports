// Tests de la vista Agentes (web/liveagents.js): que todo lo dibujado sea dato — un agente por worker con su
// estado más grave, su test en curso y su estela; la cola y los resultados al centro — y que bajo el puntero
// esté lo correcto (un agente filtra la tabla; un punto de su estela abre el test).
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");
const { liveWorkers } = require("../e2e/fixtures/live_suite.js");

const W = load(["i18n.js", "i18n.en.js", "live.js", "livefield.js", "liveagents.js"]);
const LIVE = W.TraceReportsLive, A = W.TraceReportsLiveAgents;
const MIN = 60000;
const plain = (v) => JSON.parse(JSON.stringify(v));

const exec = {
	key: "x:a", running: true, started_at: 0, planned: 6, counts: {},
	runs: [{ id: 1, name: "shop/test_a.py", started_at: 0, tests: [
		{ id: 11, name: "t_ok", worker: "gw0", state: "PASS", started_at: 0, ended_at: MIN },
		{ id: 12, name: "t_fail", worker: "gw0", state: "FAIL", started_at: MIN, ended_at: 2 * MIN },
		{ id: 13, name: "t_corriendo", worker: "gw0", state: "running", started_at: 2 * MIN },
		{ id: 21, name: "t_trabado", worker: "gw1", state: "running", started_at: 0 },
		{ id: 22, name: "t_sin_senal", worker: "gw1", state: "no_signal", started_at: MIN },
		{ id: 31, name: "t_listo", worker: "gw2", state: "PASS", started_at: 0, ended_at: MIN },
	] }],
};

test("agentes: uno por worker con su estado más grave, su test en curso y su estela; la cola y los resultados", () => {
	const m = A.agentsModel(exec, 3 * MIN, LIVE);
	assert.deepEqual(plain(m.agents.map((a) => [a.key, a.state, a.current?.id ?? null, a.done, a.fails])), [
		["gw0", "running", 13, 2, 1],
		["gw1", "no_signal", 22, 0, 0], // dos abiertos: manda el sin señal, no el último
		["gw2", "idle", null, 1, 0],
	]);
	assert.deepEqual(plain(m.agents[0].trail.map((x) => x.id)), [11, 12], "la estela: lo terminado, en orden");
	assert.deepEqual(plain(m.tally), { pass: 2, fail: 1, other: 0 });
	assert.equal(m.queue, 0, "6 planificados, 6 empezados: la cola está vacía");
	assert.equal(m.done, 3);
	assert.equal(m.total, 6);
	assert.equal(A.agentsModel({ ...exec, planned: 0 }, 3 * MIN, LIVE).queue, null, "sin plan no hay cola que mostrar");
});

test("agentes: la suite de 20 workers — todos en órbita, sin salir del lienzo, y el puntero acierta", () => {
	const { execution } = liveWorkers(1e12).execution;
	const m = A.agentsModel(execution, 1e12, LIVE);
	assert.equal(m.agents.length, 20);
	assert.ok(m.agents.some((a) => a.state === "no_signal") && m.agents.some((a) => a.state === "turtle") && m.agents.some((a) => a.state === "idle"));
	const lay = A.agentsLayout(m, 870, 600);
	for (const a of lay.agents) {
		assert.ok(a.x - a.r >= 0 && a.x + a.r <= 870 && a.y - a.r >= 0 && a.y + a.r <= 600, `${a.key} dentro del lienzo`);
		assert.ok(Math.hypot(a.x - lay.cx, a.y - lay.cy) > lay.coreR * 1.5, `${a.key} fuera del núcleo`);
		// su pista sale del núcleo, va en ángulo recto y llega al agente
		assert.deepEqual(plain(a.path[a.path.length - 1]), [a.x, a.y]);
		assert.ok(a.path.slice(1).every((p, i) => p[0] === a.path[i][0] || p[1] === a.path[i][1]), "tramos rectos");
	}
	const gw = lay.agents[5];
	assert.equal(A.hitTest(lay, gw.x + 2, gw.y - 2).agent.key, gw.key, "el agente bajo el puntero");
	const withTrail = lay.agents.find((a) => a.dots.length);
	const dot = withTrail.dots[0];
	const hit = A.hitTest(lay, dot.x, dot.y);
	assert.equal(hit.kind, "test");
	assert.equal(hit.test.id, dot.t.id, "un punto de la estela es su test");
	assert.equal(A.hitTest(lay, lay.cx, lay.cy), null, "el núcleo no abre nada");
});

test("agentes: muchos workers van en dos anillos y la estela no pasa de su largo", () => {
	const runs = [{ id: 1, name: "a.py", started_at: 0, tests: Array.from({ length: 300 }, (_, i) => ({
		id: i + 1, name: `t${i}`, worker: `gw${i % 30}`, state: "PASS", started_at: i * 1000, ended_at: i * 1000 + 500 })) }];
	const m = A.agentsModel({ key: "x:big", running: false, started_at: 0, runs }, 1e6, LIVE);
	assert.equal(m.agents.length, 30);
	assert.ok(m.agents.every((a) => a.trail.length === Math.min(10, A.TRAIL)));
	const lay = A.agentsLayout(m, 1000, 600);
	const dist = lay.agents.map((a) => Math.hypot((a.x - lay.cx) / 1000, (a.y - lay.cy) / 600));
	assert.ok(Math.max(...dist) - Math.min(...dist) > 0.05, "dos anillos: unos más adentro");
	assert.ok(lay.agents.every((a) => a.r <= 11), "con muchos, más chicos");
});

test("agentes: el punto a lo largo de una pista (los paquetes la recorren)", () => {
	const path = [[0, 0], [10, 0], [10, 10]];
	assert.deepEqual(plain(A.pointAt(path, 0)), [0, 0]);
	assert.deepEqual(plain(A.pointAt(path, 0.5)), [10, 0]);
	assert.deepEqual(plain(A.pointAt(path, 0.75)), [10, 5]);
	assert.deepEqual(plain(A.pointAt(path, 1)), [10, 10]);
	assert.ok(A.circuit(800, 500).length > 20, "el fondo tiene sus pistas");
	assert.deepEqual(plain(A.circuit(800, 500)), plain(A.circuit(800, 500)), "y siempre las mismas");
});

test("mapa: la tercera vista, Agentes, con su ayuda; sin el núcleo del campo (lo dibuja el lienzo)", () => {
	const { execution, now } = liveWorkers(1e12).execution;
	const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
	const h = { t: (s, v) => (v ? s.replace(/\{(\w+)\}/g, (m, k) => (k in v ? v[k] : m)) : s), esc, icon: (n) => `<svg data-i="${n}"></svg>`, canAct: () => true, clock: () => "10:00" };
	const render = (ui) => LIVE.render({ list: [execution], exec: execution, now, ui: { problems: false, ...ui } }, h);
	const field = render({});
	assert.ok(field.includes('data-live-fieldview="agents"') && field.includes("Agentes") && field.includes('class="lf-core"'));
	const agents = render({ fieldView: "agents" });
	assert.ok(agents.includes('class="card live-map is-field is-agents"'));
	assert.ok(agents.includes('data-live-field data-mode="agents">'));
	assert.ok(/data-live-fieldview="agents" aria-checked="true"/.test(agents));
	assert.ok(agents.includes("cada worker es un agente") && !agents.includes('class="lf-core"'));
});
