// Tests de la vista Agentes (web/liveagents.js), el reactor: que todo lo dibujado sea dato — un segmento por worker
// con sus tests en celdas, en orden, y su estado más grave; las anotaciones, lo que importa (sin señal, el más
// lento, el último fallo) — que bajo el puntero esté lo correcto (también girado) y que escale: con miles de tests,
// los fallos se ven siempre. Con un filtro, lo apagado no responde (como en el Campo).
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");
const { liveWorkers } = require("../e2e/fixtures/live_suite.js");

const W = load(["i18n.js", "i18n.en.js", "live.js", "livefield.js", "liveagents.js"]);
const LIVE = W.TraceReportsLive, A = W.TraceReportsLiveAgents;
const MIN = 60000;
const plain = (v) => JSON.parse(JSON.stringify(v));
const T = (s, v) => (v ? s.replace(/\{(\w+)\}/g, (m, k) => (k in v ? v[k] : m)) : s);

const exec = {
	key: "x:a", running: true, started_at: 0, planned: 6, counts: {},
	runs: [{ id: 1, name: "shop/test_a.py", started_at: 0, tests: [
		{ id: 13, name: "t_corriendo", worker: "gw0", state: "running", started_at: 2 * MIN },
		{ id: 11, name: "t_ok", worker: "gw0", state: "PASS", started_at: 0, ended_at: MIN },
		{ id: 12, name: "t_fail", worker: "gw0", state: "FAIL", started_at: MIN, ended_at: 2 * MIN },
		{ id: 21, name: "t_lento", worker: "gw1", state: "turtle", started_at: 10, factor: 2.6 },
		{ id: 22, name: "t_sin_senal", worker: "gw1", state: "no_signal", started_at: MIN + 10, idle: 4 * MIN },
		{ id: 31, name: "t_listo", worker: "gw2", state: "PASS", started_at: 20, ended_at: MIN },
	] }],
};

test("agentes: un brazo por worker con sus tests en orden y su estado más grave; lo terminado al centro", () => {
	const m = A.agentsModel(exec, 3 * MIN, LIVE);
	assert.deepEqual(plain(m.agents.map((a) => [a.key, a.state, a.current?.id ?? null, a.tests.map((x) => x.id), a.done, a.fails])), [
		["gw0", "running", 13, [11, 12, 13], 2, 1],
		["gw1", "no_signal", 22, [21, 22], 0, 0], // dos abiertos: manda el sin señal
		["gw2", "idle", null, [31], 1, 0],
	]);
	assert.deepEqual(plain(m.tally), { pass: 2, fail: 1, other: 0 });
	assert.equal(m.total, 6);
	assert.deepEqual(plain(m.fails.map((x) => x.id)), [12]);
});

test("agentes: las anotaciones señalan al sin señal, al más lento y el último fallo", () => {
	const two = { ...exec, runs: [{ ...exec.runs[0], tests: [...exec.runs[0].tests,
		{ id: 41, name: "t_lento_mas", worker: "gw3", state: "turtle", started_at: 30, factor: 3.4 }] }] };
	const notes = A.notesOf(A.agentsModel(two, 3 * MIN, LIVE), 3 * MIN, T);
	assert.deepEqual(plain(notes.map((n) => [n.kind, n.id])), [["nosig", 22], ["turtle", 41], ["fail", 12]]);
	assert.match(notes[0].text, /^gw1 · SIN SEÑAL 4 min · t_sin_senal$/);
	assert.match(notes[1].text, /^gw3 · LENTO ×3,4 · t_lento_mas$/);
	assert.match(notes[2].text, /^ÚLTIMO FALLO · t_fail$/);
});

const CW = 870, CH = 640;
const big = () => A.agentsModel(liveWorkers(1e12).execution.execution, 1e12, LIVE);
const cellsOf = (lay) => lay.segs.flatMap((s) => s.cells);

test("agentes: el reactor — celdas en orden desde adentro, dentro del lienzo; el puntero acierta y con un filtro lo apagado no responde", () => {
	const m = big();
	assert.equal(m.agents.length, 20);
	const R = A.fitRadius(m, CW, CH), lay = A.reactor(m, CW, CH, R);
	for (const s of lay.segs) {
		assert.ok(s.lp.x >= 0 && s.lp.x <= CW && s.lp.y >= 0 && s.lp.y <= CH, `${s.key}: su nombre dentro del lienzo`);
		const d = s.cells.map((c) => Math.hypot(c.u, c.v));
		assert.ok(d.every((x, i) => !i || x >= d[i - 1] - 1e-9), `${s.key}: del primero (adentro) al último (afuera)`);
		assert.ok(d.every((x) => x > lay.Ra && x < lay.R), `${s.key}: dentro de su segmento`);
	}
	const fail = cellsOf(lay).find((c) => c.t.state === "FAIL"), pass = cellsOf(lay).find((c) => c.t.state === "PASS");
	assert.equal(A.hitTest(lay, fail.x + 1, fail.y).node.t.id, fail.t.id, "la celda bajo el puntero");
	const seg = lay.segs[3];
	assert.equal(A.hitTest(lay, seg.lp.x, seg.lp.y).arm.key, seg.key, "el nombre de un worker filtra por él");
	assert.equal(A.hitTest(lay, lay.cx, lay.cy), null, "el núcleo no abre nada");
	// con el filtro "Fallo": el que pasó no responde; el que falló, sí
	const onlyFail = new Set([fail.t.id]);
	assert.equal(A.hitTest(lay, pass.x, pass.y, onlyFail), null);
	assert.equal(A.hitTest(lay, fail.x, fail.y, onlyFail).node.t.id, fail.t.id);
	const failSeg = lay.segs.find((s) => s.cells.includes(fail));
	const other = lay.segs.find((s) => s.tests.every((x) => x.id !== fail.t.id));
	assert.equal(A.hitTest(lay, other.lp.x, other.lp.y, onlyFail), null, "un worker sin nada que coincida tampoco responde");
	assert.equal(A.hitTest(lay, failSeg.lp.x, failSeg.lp.y, onlyFail)?.arm.key, failSeg.key);
});

test("agentes: girado o visto desde arriba, el puntero sigue acertando la celda (la perspectiva se recalcula)", () => {
	const m = big(), R = A.fitRadius(m, CW, CH);
	const id = cellsOf(A.reactor(m, CW, CH, R)).find((c) => c.t.state === "FAIL").t.id;
	const seen = new Set();
	for (const cam of [A.CAM, { tilt: 0, rot: 0 }, { tilt: 1.1, rot: 2.4 }]) {
		const lay = A.reactor(m, CW, CH, R, cam), c = lay.at.get(id);
		seen.add(`${Math.round(c.x)},${Math.round(c.y)}`);
		assert.equal(A.hitTest(lay, c.x, c.y).node.t.id, id);
	}
	assert.equal(seen.size, 3, "cada cámara la pone en otro lugar");
	// de frente (abajo) se ve más grande que atrás (arriba)
	const lay = A.reactor(m, CW, CH, R), front = lay.segs[Math.round(lay.segs.length / 2)], back = lay.segs[0];
	assert.ok(front.cells[0].r > back.cells[0].r);
});

test("agentes: con miles de tests, cada segmento muestra los últimos que le caben, cuenta los anteriores y los fallos se ven siempre", () => {
	const runs = [{ id: 1, name: "a.py", started_at: 0, tests: Array.from({ length: 3000 }, (_, i) => ({
		id: i + 1, name: `t${i}`, worker: `gw${i % 3}`, state: i === 3 ? "FAIL" : "PASS", started_at: i * 1000, ended_at: i * 1000 + 500 })) }];
	const m = A.agentsModel({ key: "x:big", running: false, started_at: 0, runs }, 1e7, LIVE);
	const lay = A.reactor(m, CW, CH, A.fitRadius(m, CW, CH));
	for (const s of lay.segs) {
		assert.equal(s.cells.length, s.cap, `${s.key}: lleno, nunca más de lo que cabe`);
		assert.equal(s.hidden, 1000 - s.cap);
		assert.ok(s.cap >= A.ROWS * 2 && lay.rows === A.ROWS, "lleno: celdas al mínimo, todas las filas");
	}
	assert.ok(lay.at.has(4), "el fallo del comienzo sigue a la vista");
	const fewTests = A.reactor(A.agentsModel({ key: "x:few", running: false, started_at: 0, runs: [{ ...runs[0], tests: runs[0].tests.slice(0, 6) }] }, 1e7, LIVE), CW, CH, A.fitRadius(m, CW, CH));
	assert.ok(fewTests.p > lay.p * 1.3 && fewTests.rows < A.ROWS, "con pocos tests, celdas grandes (fáciles de apuntar)");
	assert.ok(lay.at.has(2998) && !lay.at.has(7), "los últimos sí; los anteriores que pasaron, no");
});

test("agentes: los nombres y el disco no quedan bajo las tarjetas de las esquinas ni bajo la barra (el reactor se achica)", () => {
	const m = big(), few = { ...m, agents: m.agents.slice(0, 12) };
	const avoid = [[4, 4, 180, 110], [4, 526, 290, 594], [690, 526, 866, 594], [330, 586, 540, 636]]; // tl, bl, br, la barra
	const free = A.fitRadius(few, CW, CH), R = A.fitRadius(few, CW, CH, avoid);
	assert.ok(R < free, "con cosas encima, más chico");
	const lay = A.reactor(few, CW, CH, R, A.CAM, avoid);
	const under = lay.segs.filter((s) => {
		const [x0, y0, x1, y1] = A.labelBox(s, s.lp, s.side, s.two);
		return avoid.some(([c0, d0, c1, d1]) => x0 < c1 && x1 > c0 && y0 < d1 && y1 > d0);
	}).map((s) => s.key);
	assert.deepEqual(plain(under), []);
	assert.ok(R > 230, `sigue grande: el nombre que choca pierde su segunda línea, no achica todo (${R})`);
	assert.ok(lay.segs.some((s) => s.two) && lay.segs.some((s) => !s.two));
});

test("agentes: el boceto — los agentes entran, dibujan cada segmento con el nombre de su worker, el reactor se enciende y se van", () => {
	const m = big(), lay = A.reactor(m, CW, CH, A.fitRadius(m, CW, CH));
	const at = (e) => plain(A.sketchPlan(lay, e));
	const start = at(0), mid = at(0.4), drawn = at(0.65), end = at(1);
	assert.equal(start.cursors.length, 8, "a lo sumo 8 agentes (20 workers: cada uno, varios segmentos)");
	assert.ok(start.cursors.every((c) => Math.hypot(c.x - lay.cx, c.y - lay.cy) > lay.R * 1.4), "entran desde afuera");
	assert.ok(start.segs.every((x) => x.q === 0) && start.reactor === 0);
	assert.ok(mid.segs.some((x) => x.q === 1) && mid.segs.some((x) => x.q < 1), "van segmento por segmento");
	for (const c of mid.cursors) assert.equal(c.label, lay.segs[c.seg].label, "cada agente lleva el nombre del worker que dibuja");
	assert.ok(drawn.segs.every((x) => x.q === 1), "a 2/3, todo el modelo bocetado");
	assert.ok(drawn.reactor > 0 && drawn.reactor < 1, "el reactor se enciende encima del boceto");
	assert.equal(end.pencil, 0);
	assert.ok(end.cursors.every((c) => c.alpha === 0), "al final, los agentes se fueron");
	// el trazo parcial llega hasta la fracción pedida de su largo
	const line = [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 10 }];
	assert.deepEqual(plain(A.partial(line, 0.75).tip), { x: 10, y: 5 });
});

test("agentes: el boceto — cada agente trabaja a su manera: uno se queda pensando, otro borra y rehace, el de CAD se engancha a los vértices, y no terminan juntos", () => {
	const m = big(), lay = A.reactor(m, CW, CH, A.fitRadius(m, CW, CH));
	const steps = Array.from({ length: 201 }, (_, k) => k / 200), plans = steps.map((e) => plain(A.sketchPlan(lay, e)));
	const of = (persona) => plans.flatMap((p, k) => p.cursors.filter((c) => c.persona === persona).map((c) => ({ ...c, e: steps[k] })));
	assert.ok(of("think").some((c) => c.thinking), "el pensador se detiene con el reloj de arena");
	assert.ok(of("cad").some((c) => c.snap), "el de CAD se engancha a un vértice");
	assert.ok(of("fix").some((c) => c.erasing), "el que corrige, borra");
	assert.ok(of("fast").some((c) => c.done) && of("think").filter((c) => c.e < 0.5).every((c) => !c.done), "el rápido termina antes que el pensador");
	// mientras piensa, no avanza; mientras corrige, su trazo retrocede
	const qOf = (k, i) => plans[k].segs[i].q;
	const think = of("think").find((c) => c.thinking), k1 = steps.indexOf(think.e);
	assert.equal(qOf(k1 + 1, think.seg), qOf(k1, think.seg));
	const drawnBy = (k, j) => plans[k].segs.filter((x) => x.j === j).reduce((a, x) => a + x.q, 0);
	const fixJ = A.PERSONAS.indexOf("fix");
	assert.ok(steps.some((_, k) => k < 200 && plans[k].cursors[fixJ].erasing && drawnBy(k + 1, fixJ) < drawnBy(k, fixJ)), "su trazo retrocede");
	// entre un segmento y otro levanta el lápiz (viaja), y al apoyarlo hace clic
	assert.ok(plans.some((p) => p.cursors.some((c) => c.travel)) && plans.some((p) => p.cursors.some((c) => c.down >= 0)));
	// cuándo termina cada uno: distintos
	const ends = [0, 1, 2, 3].map((j) => steps.find((e, k) => plans[k].segs.filter((x) => x.j === j).every((x) => x.q === 1)));
	assert.ok(Math.max(...ends) - Math.min(...ends) > 0.12, `terminan a destiempo (${ends})`);
});

test("mapa: la tercera vista, Agentes, con su ayuda y el núcleo del campo al centro", () => {
	const { execution, now } = liveWorkers(1e12).execution;
	const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
	const h = { t: T, esc, icon: (n) => `<svg data-i="${n}"></svg>`, canAct: () => true, clock: () => "10:00" };
	const render = (ui) => LIVE.render({ list: [execution], exec: execution, now, ui: { problems: false, ...ui } }, h);
	assert.ok(render({}).includes('data-live-fieldview="agents"'));
	const agents = render({ fieldView: "agents" });
	assert.ok(agents.includes('class="card live-map is-field is-agents"') && agents.includes('data-live-field data-mode="agents">'));
	assert.ok(/data-live-fieldview="agents" aria-checked="true"/.test(agents));
	assert.ok(agents.includes("un segmento con sus tests") && agents.includes('class="lf-core"'));
});
