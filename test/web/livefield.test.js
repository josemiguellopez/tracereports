// Tests del campo de la ejecución (web/livefield.js): que todo lo dibujado sea dato (cada test un arco
// en su carril y en su momento), que la ingesta nunca se llame "activa" sin eventos reales de la
// ejecución, y que el punto bajo el puntero sea el test correcto (es lo que abre el clic).
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");
const { liveSuite } = require("../e2e/fixtures/live_suite.js");

const W = load(["i18n.js", "i18n.en.js", "live.js", "livefield.js"]);
const LIVE = W.TraceReportsLive, F = W.TraceReportsLiveField;
const en = load(["i18n.js", "i18n.en.js", "live.js", "livefield.js"], { lang: "en" }).TraceReportsI18n;
const MIN = 60000;
const plain = (v) => JSON.parse(JSON.stringify(v));

const exec = {
	key: "x:f", running: true, started_at: 0, counts: {},
	runs: [
		{ id: 1, name: "a.py", started_at: 0, tests: [
			{ id: 11, name: "t_ok", worker: "gw0", state: "PASS", started_at: 0, ended_at: 10 * MIN, elapsed: 10 * MIN, factor: 1 },
			{ id: 12, name: "t_lento", worker: "gw0", state: "turtle", started_at: 10 * MIN, elapsed: 10 * MIN, factor: 2.5 },
		] },
		{ id: 2, name: "b.py", started_at: 0, tests: [{ id: 21, name: "t_fail", worker: "gw1", state: "FAIL", started_at: 5 * MIN, ended_at: 15 * MIN, elapsed: 10 * MIN }] },
	],
};

test("cada test es un arco en su carril y en su momento: el ángulo es el tiempo (arriba el comienzo, horario)", () => {
	const L = F.fieldLayout(exec, 20 * MIN, LIVE);
	assert.equal(L.rings.length, 2, "un anillo por worker");
	assert.deepEqual(plain(L.items.map((x) => x.id)), [11, 21, 12], "en el orden en que empezaron");
	const by = Object.fromEntries(L.items.map((x) => [x.id, x]));
	assert.equal(by[11].ring, 0); assert.equal(by[21].ring, 1);
	const seam = Math.PI * F.SEAM; // la costura: medio a cada lado del comienzo
	assert.ok(Math.abs(by[11].a0 - seam) < 1e-9, "el primer test empieza justo después de la costura de arriba");
	assert.ok(by[11].a1 < by[21].a1 && by[21].a1 < by[12].a1, "lo que termina después, más lejos en el reloj");
	assert.ok(Math.abs(by[11].a1 - Math.PI) < 0.01, "a la mitad del tiempo, a media vuelta (abajo)");
	assert.ok(by[12].running && Math.abs(by[12].a1 - L.nowAngle) < 1e-9, "uno en curso llega hasta la aguja de ahora");
	assert.equal(F.fieldLayout({ ...exec, running: false }, 20 * MIN, LIVE).nowAngle, null, "terminada: sin aguja");
	assert.equal(F.fieldLayout({ ...exec, runs: [] }, 1, LIVE).items.length, 0);
});

test("ingesta real: activa solo con eventos de esta ejecución en el último minuto; nunca sobre una terminada", () => {
	const now = 10 * MIN;
	const ev = [{ at: now - 5000, kind: "test" }, { at: now - 20000, kind: "step" }, { at: now - 30000, kind: "step" }, { at: now - 5 * MIN, kind: "fail" }];
	const s = F.ingestStats(ev, now, { running: true, connected: true });
	assert.deepEqual(plain([s.state, s.perMin, s.lastAgo]), ["active", 3, 5000]);
	assert.deepEqual(plain(s.byKind), { test: 1, step: 2 }, "por canal, en el último minuto");
	assert.equal(F.ingestStats([{ at: now - 2 * MIN, kind: "step" }], now, { running: true, connected: true }).state, "quiet", "más de un minuto sin nada");
	assert.equal(F.ingestStats([], now, { running: true, connected: true }).state, "quiet");
	assert.equal(F.ingestStats(ev, now, { running: true, connected: false }).state, "offline", "sin conexión al stream");
	assert.equal(F.ingestStats(ev, now, { running: false, connected: true }).state, "done", "terminada: nunca activa");
	assert.equal(F.channelOf("test", { data: { action: "created" } }), "test");
	assert.equal(F.channelOf("test", { data: { status: "FAIL" } }), "fail");
	assert.equal(F.channelOf("log", { data: { status: "INFO" } }), "step");
	assert.equal(F.channelOf("network", {}), "net");
	assert.equal(F.channelOf("summary", {}), "");
});

test("la partícula bajo el puntero es su test (lo que abre el clic), también con el tubo girado; lejos, nada", () => {
	const L = F.fieldLayout(exec, 20 * MIN, LIVE);
	const g = F.geometry(800, 420);
	for (const spin of [0, 1.3]) {
		for (const it of L.items) {
			const p = F.headOf(g, L, it, spin);
			assert.equal(F.hitTest(L, g, p.x + 2, p.y - 1, spin)?.id, it.id, `la partícula de ${it.name} (giro ${spin})`);
		}
	}
	assert.equal(F.hitTest(L, g, g.cx, g.cy), null, "el centro no es un test");
	assert.equal(F.hitTest(L, g, 2, 2), null);
	// adelante se ve más grande: la profundidad va de -1 (atrás, arriba) a 1 (adelante, abajo)
	assert.ok(F.project(g, 0, 0).depth < 0 && F.project(g, Math.PI, 0).depth > 0);
	assert.ok(F.project(g, 0, 0).y < F.project(g, Math.PI, 0).y, "el comienzo (arriba del reloj) queda arriba en pantalla");
});

test("los rayos: el largo es cuántos corrían; el color, cómo terminaron los que terminaron ahí (no un test largo)", () => {
	const L = F.fieldLayout(exec, 20 * MIN, LIVE);
	assert.equal(L.density.length, F.SPIKES);
	assert.equal(Math.max(...L.density.map((d) => d.n)), 3, "dos a la vez, y tres en el tramo donde uno termina y el siguiente empieza");
	const worst = L.density.map((d) => d.worst).filter(Boolean);
	assert.deepEqual(plain([...new Set(worst)].sort()), ["FAIL", "PASS"], "el lento sigue corriendo: no tiñe ningún tramo");
});

test("el campo de la suite grande: un arco por test y los carriles inferidos; el HUD con valores reales", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const L = F.fieldLayout(e, d.execution.now, LIVE);
	assert.equal(L.items.length, LIVE.allTests(e).length);
	assert.equal(L.rings.length, 12, "12 procesos");
	assert.ok(L.items.every((x) => x.a0 >= 0 && x.a1 <= Math.PI * 2 && x.a1 >= x.a0));
	const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
	const h = { t: (s, v) => (v ? s.replace(/\{(\w+)\}/g, (m, k) => (k in v ? v[k] : m)) : s), esc, icon: (n) => `<svg data-i="${n}"></svg>`, canAct: () => true, clock: () => "10:00" };
	const html = (ingest) => LIVE.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false }, ingest }, h);
	const quiet = html(null);
	assert.ok(quiet.includes('class="card live-map is-field"') && quiet.includes('data-live-field data-mode="field">'), "el campo es la vista por defecto");
	assert.ok(/class="lf-hud tl warn"[\s\S]*?SIN EVENTOS/.test(quiet), "sin eventos del stream no dice activa");
	const active = html({ state: "active", perMin: 42, lastAgo: 2000, byKind: { test: 5 } });
	assert.ok(/class="lf-hud tl ok"[\s\S]*?INGESTA ACTIVA[\s\S]*?42 eventos\/min · último hace 2 s/.test(active));
	assert.ok(/class="lf-lane pass" data-live-all/.test(active) && active.includes("5/min"), "el canal de tests con su ritmo real");
	assert.ok(/class="lf-lane fail" data-live-state="FAIL"/.test(active) && /class="lf-lane work" data-live-groupby="worker"/.test(active), "cada canal filtra o agrupa");
	assert.ok(/class="lf-hud bl bad" data-live-facet="error" data-v="TimeoutException/.test(active), "la anomalía dominante filtra por su firma");
	assert.ok(!active.includes("lf-sigs") && !active.includes("lf-hud tr"), "sin la lista de señales ni el recuadro de progreso: los repetía la cinta y la cabecera");
	assert.ok(!/class="hm-c /.test(active), "la cuadrícula, solo si se pide");
	const grid = LIVE.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: { problems: false, fieldView: "grid" } }, h);
	assert.ok(/class="hm-c [a-z]+" data-live-test/.test(grid) && !grid.includes("data-live-field>"));
	const done = LIVE.render({ list: d.list.executions, exec: { ...e, running: false }, now: d.execution.now, ui: {}, ingest: { state: "done", perMin: 0, lastAgo: null, byKind: {} } }, h);
	assert.ok(done.includes("EJECUCIÓN TERMINADA"));
	for (const s of ["INGESTA ACTIVA", "SIN EVENTOS", "SIN CONEXIÓN", "EJECUCIÓN TERMINADA"]) assert.notEqual(en.t(s), s, `${s} en inglés`);
});

test("el gráfico de ingesta se lee como gráfico: barras sobre una base, un tramo vacío no dibuja nada, y su escala de tiempo", () => {
	const d = liveSuite(1_794_000_000_000), e = d.execution.execution;
	const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
	const h = { t: (s, v) => (v ? s.replace(/\{(\w+)\}/g, (m, k) => (k in v ? v[k] : m)) : s), esc, icon: (n) => `<svg data-i="${n}"></svg>`, canAct: () => true, clock: () => "10:00" };
	const series = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 6];
	const html = LIVE.render({ list: d.list.executions, exec: e, now: d.execution.now, ui: {}, ingest: { state: "active", perMin: 9, lastAgo: 0, byKind: {}, series } }, h);
	const bars = [...html.matchAll(/<span class="lf-bars" aria-hidden="true">(.*?)<\/span>/g)][0][1];
	assert.equal((bars.match(/--h:0%/g) || []).length, 10, "los tramos sin eventos no dibujan una raya (se leían como letra o ícono)");
	assert.ok(bars.includes("--h:100%") && bars.includes("--h:50%"));
	assert.ok(html.includes('<span class="lf-bars-x" aria-hidden="true"><small>hace 1 min</small><small>ahora</small></span>'));
	assert.notEqual(en.t("hace 1 min"), "hace 1 min");
});

test("con un filtro, lo apagado no se puede tocar: el clic va al test que coincide aunque haya otro encima", () => {
	const L = F.fieldLayout(exec, 20 * MIN, LIVE);
	const g = F.geometry(800, 420);
	const fail = L.items.find((x) => x.id === 21), pass = L.items.find((x) => x.id === 11);
	const p = F.headOf(g, L, pass, 0);
	assert.equal(F.hitTest(L, g, p.x, p.y, 0)?.id, 11, "sin filtro: el de abajo del puntero");
	assert.equal(F.hitTest(L, g, p.x, p.y, 0, new Set([21])), null, "filtrado por fallo: el que pasó no responde");
	const q = F.headOf(g, L, fail, 0);
	assert.equal(F.hitTest(L, g, q.x + 3, q.y, 0, new Set([21]))?.id, 21, "el que coincide, sí");
	assert.equal(F.HIT_PX, 18, "un poco más de margen para acertar");
	assert.equal(F.hitTest(L, g, q.x + 16, q.y, 0, new Set([21]))?.id, 21, "a 16 px todavía se acierta");
});

test("'ahora' va en el color del texto, nunca en el rojo de los fallos", () => {
	assert.equal(F.NOW_VAR, "--color-text");
	const src = require("node:fs").readFileSync(require("node:path").join(__dirname, "..", "..", "web", "livefield.js"), "utf8");
	const nowBlock = src.slice(src.indexOf("if (layout.nowAngle !== null) {"), src.indexOf("// el elegido, el del mouse"));
	assert.ok(nowBlock.includes("colors[NOW_VAR]") && !nowBlock.includes("--color-danger"));
	const css = require("node:fs").readFileSync(require("node:path").join(__dirname, "..", "..", "web", "features.css"), "utf8");
	assert.ok(/\.lg-now \{ border-left: 2px dashed var\(--color-text\); \}/.test(css), "y en la línea de tiempo");
});

test("el subtítulo del mapa ya no habla de una aguja roja (el rojo es solo de los fallos)", () => {
	const src = require("node:fs").readFileSync(require("node:path").join(__dirname, "..", "..", "web", "live.js"), "utf8");
	assert.ok(!src.includes("aguja roja") && src.includes("la aguja punteada es ahora"));
	assert.equal(en.t("el tiempo en círculo: arriba el comienzo, la aguja punteada es ahora"), "time in a circle: the start on top, the dashed hand is now");
});
