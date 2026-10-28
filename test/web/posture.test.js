const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;
const src = read("app.js");

test("Ajustes tiene la sección de postura de seguridad con el endpoint autenticado", () => {
	for (const token of [
		'{ id: "security", icon: "i-check", title: "Postura de seguridad", ...postureSummary(S.settings.posture) }',
		'api("/api/v1/security/posture")',
		'cur.id === "security" ? postureCard()',
	]) {
		assert.ok(src.includes(token), `falta: ${token}`);
	}
});

test("cada chequeo muestra su nivel, lo encontrado y qué hacer, escapado", () => {
	// nada del servidor entra al HTML sin esc(): un detalle con HTML no se interpreta
	for (const token of ["${esc(c.title)}", "${esc(c.detail)}", "→ ${esc(c.fix)}", 'data-check="${esc(c.id)}"']) {
		assert.ok(src.includes(token), `falta: ${token}`);
	}
	assert.ok(src.includes('const POSTURE_TONE = { ok: "ok", warn: "warn", fail: "bad" };'));
});

test("el índice resume la postura: críticos, por revisar o todo en orden", () => {
	const fn = src.slice(src.indexOf("function postureSummary(p)"), src.indexOf("async function loadPosture()"));
	const postureSummary = new Function("tr", `${fn}; return postureSummary;`)((s, v) => s.replace(/\{(\w+)\}/g, (_, k) => v[k]));
	assert.deepEqual(postureSummary(null), { sub: "…" });
	assert.deepEqual(postureSummary({ error: "403" }), { sub: "Sin acceso", tone: "muted" });
	assert.deepEqual(postureSummary({ fail: 2, warn: 1 }), { sub: "2 críticos", tone: "warn" });
	assert.deepEqual(postureSummary({ fail: 0, warn: 3 }), { sub: "3 por revisar", tone: "warn" });
	assert.deepEqual(postureSummary({ fail: 0, warn: 0 }), { sub: "Todo en orden", tone: "ok" });
});

test("los textos de la postura tienen traducción", () => {
	for (const phrase of ["Postura de seguridad", "Sin acceso", "{n} críticos", "{n} por revisar", "Todo en orden", "Revisar", "Crítico",
		"Revisando la configuración…", "No se pudo leer la postura de seguridad",
		"{ok} OK · {warn} por revisar · {fail} críticos"]) {
		assert.notEqual(en.t(phrase), phrase, `sin traducción: ${phrase}`);
	}
});
