// Tests de los avisos de ejecuciones nuevas (web/runnotify.js): se avisa por ejecución y no por
// corrida (un lanzador que abre un run por archivo, o muchos workers en paralelo, avisaba decenas de
// veces), "Ahora no" silencia esa ejecución, "Silenciar 15 min" todas, y todo vence solo.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const RN = load(["runnotify.js"]).TraceReportsRunNotify;
const MIN = 60000, NOW = 1_794_000_000_000;
const plain = (v) => JSON.parse(JSON.stringify(v));
const run = (id, exe, name = `shop/test_${id}.py`) => ({ id, execution_id: exe, name });

test("muchas corridas de una ejecución son UN aviso, con su cuenta; otra ejecución, otro", () => {
	const runs = [run(10, "suite-1"), run(11, "suite-1"), run(12, "suite-1"), run(13, "suite-2"), run(14, "")];
	const { list, loud } = RN.group(runs, RN.empty(), NOW);
	assert.deepEqual(plain(list.map((g) => [g.key, g.runs.length, g.latest.id])), [["x:suite-1", 3, 12], ["x:suite-2", 1, 13], ["r:14", 1, 14]]);
	assert.equal(loud.length, 3, "las tres se avisan");
	assert.equal(list[0].title, "suite-1", "el nombre de la ejecución");
	assert.equal(list[2].title, "shop/test_14.py", "sin execution_id: el run solo");
});

test("'Ahora no' silencia esa ejecución: sus corridas nuevas ya no abren el aviso, la insignia las cuenta", () => {
	let st = RN.mute(RN.empty(), "x:suite-1", "suite-1", NOW);
	const later = [run(10, "suite-1"), run(20, "suite-1"), run(21, "suite-1")];
	const { list, loud } = RN.group(later, st, NOW + 5 * MIN);
	assert.equal(list.length, 1, "sigue en la lista (insignia)");
	assert.equal(loud.length, 0, "pero no vuelve a avisar");
	assert.equal(RN.group([...later, run(30, "suite-2")], st, NOW).loud.map((g) => g.key).join(), "x:suite-2", "otra ejecución sí avisa");
	st = RN.unmute(st, "x:suite-1");
	assert.equal(RN.group(later, st, NOW).loud.length, 1, "reactivada desde la campana");
});

test("'Silenciar 15 min' calla todas por 15 min, con los minutos que quedan; reactivar lo deshace", () => {
	const st = RN.snooze(RN.empty(), NOW);
	assert.equal(RN.SNOOZE_MS, 15 * MIN);
	assert.equal(RN.snoozeLeft(st, NOW), 15);
	assert.equal(RN.snoozeLeft(st, NOW + 14 * MIN + 1), 1, "redondea hacia arriba: nunca dice 0 estando silenciado");
	assert.equal(RN.snoozeLeft(st, NOW + 15 * MIN), 0, "vence solo");
	assert.equal(RN.group([run(1, "a")], st, NOW + MIN).loud.length, 0);
	assert.equal(RN.group([run(1, "a")], st, NOW + 16 * MIN).loud.length, 1, "pasados los 15 min vuelve a avisar");
	assert.equal(RN.snoozeLeft(RN.resume(st), NOW), 0);
	assert.equal(RN.snoozeLeft(RN.snooze(RN.empty(), NOW, 60 * MIN), NOW), 60, "1 h desde la campana");
});

test("se guarda en el navegador y lo vencido se descarta al leer (nada queda apagado para siempre)", () => {
	const store = {};
	const storage = { getItem: (k) => store[k] ?? null, setItem: (k, v) => { store[k] = v; } };
	let st = RN.snooze(RN.empty(), NOW);
	st = RN.mute(st, "x:vieja", "vieja", NOW - RN.MUTE_TTL_MS - 1);
	st = RN.mute(st, "x:nueva", "nueva", NOW);
	RN.save(st, storage);
	const back = RN.load(NOW + MIN, storage);
	assert.ok(back.until > NOW, "el silencio sigue");
	assert.deepEqual(Object.keys(back.muted), ["x:nueva"], "la silenciada hace más de 12 h vuelve a avisar");
	assert.equal(RN.load(NOW + 20 * MIN, storage).until, 0, "pasados los 15 min ya no está silenciado");
	assert.deepEqual(plain(RN.load(NOW, { getItem: () => "{roto" })), plain(RN.empty()), "un valor roto no rompe la página");
	assert.deepEqual(plain(RN.load(NOW, { getItem() { throw new Error("bloqueado"); } })), plain(RN.empty()), "sin almacenamiento, tampoco");
	let many = RN.empty();
	for (let i = 0; i < 80; i++) many = RN.mute(many, `x:${i}`, String(i), NOW + i);
	assert.equal(Object.keys(RN.prune(many, NOW + 100).muted).length, 50, "como mucho 50 (las más recientes)");
});

test("la página carga runnotify.js antes de app.js, la campana está en la barra y todo texto tiene inglés", () => {
	const html = read("index.html");
	assert.ok(html.indexOf('src="runnotify.js"') > 0 && html.indexOf('src="runnotify.js"') < html.indexOf('src="app.js"'));
	assert.ok(html.includes('id="notify-bell"') && html.includes('id="notify-panel"') && html.includes('id="i-bell-off"'));
	const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;
	const app = read("app.js");
	const block = app.slice(app.indexOf("function notifyNewRun"), app.indexOf("function toggleNotifyPanel"));
	const texts = [...block.matchAll(/\btr\("([^"]+)"/g)].map((m) => m[1]);
	assert.ok(texts.length > 10);
	for (const s of new Set(texts)) if (s !== "{n} min") assert.notEqual(en.t(s), s, `"${s}" sin inglés`);
});
