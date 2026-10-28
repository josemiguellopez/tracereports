const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;
const src = read("app.js");

test("Ajustes tiene la sección de revisión de seguridad con el endpoint autenticado", () => {
	for (const token of [
		'{ id: "security", icon: "i-shield", title: "Revisión de seguridad", ...postureSummary(S.settings.posture) }',
		'api("/api/v1/security/posture")',
		'cur.id === "security" ? postureCard()',
	]) {
		assert.ok(src.includes(token), `falta: ${token}`);
	}
});

test("cada chequeo muestra su nivel, lo encontrado y qué hacer, escapado", () => {
	// nada del servidor entra al HTML sin esc(): un detalle con HTML no se interpreta
	for (const token of ["${esc(postureTitle(c))}", "${esc(postureText(c.detail))}", "→ ${esc(postureText(c.fix))}", 'data-check="${esc(c.id)}"']) {
		assert.ok(src.includes(token), `falta: ${token}`);
	}
	assert.ok(src.includes('const POSTURE_TONE = { ok: "ok", warn: "warn", fail: "bad" };'));
});

test("el índice resume la postura: críticos, por revisar o todo en orden", () => {
	const fn = src.slice(src.indexOf("const postureFails"), src.indexOf("async function loadPosture()"));
	const postureSummary = new Function("tr", `${fn}; return postureSummary;`)((s, v) => s.replace(/\{(\w+)\}/g, (_, k) => v[k]));
	assert.deepEqual(postureSummary(null), { sub: "…" });
	assert.deepEqual(postureSummary({ error: "403" }), { sub: "Sin acceso", tone: "muted" });
	assert.deepEqual(postureSummary({ fail: 2, warn: 1 }), { sub: "2 críticos", tone: "warn" });
	assert.deepEqual(postureSummary({ fail: 1, warn: 0 }), { sub: "1 crítico", tone: "warn" });
	assert.deepEqual(postureSummary({ fail: 0, warn: 3 }), { sub: "3 por revisar", tone: "warn" });
	assert.deepEqual(postureSummary({ fail: 0, warn: 0 }), { sub: "Todo en orden", tone: "ok" });
});

test("los textos de la postura tienen traducción", () => {
	for (const phrase of ["Revisión de seguridad", "Sin acceso", "{n} críticos", "1 crítico", "{n} por revisar", "Todo en orden", "Revisar", "Crítico",
		"Revisando la configuración…", "No se pudo leer la revisión de seguridad",
		"{ok} OK · {warn} por revisar · {fail} críticos"]) {
		assert.notEqual(en.t(phrase), phrase, `sin traducción: ${phrase}`);
	}
});

// postureCard de verdad, con lo mínimo alrededor: el idioma se elige con lang
function card(lang) {
	const fn = src.slice(src.indexOf("const postureFails"), src.indexOf("async function loadUsage()"));
	const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
	const i18n = load(["i18n.js", "i18n.en.js"], { lang }).TraceReportsI18n;
	const S = { settings: {} };
	const render = new Function("S", "tr", "esc", "icon", "window", `${fn}; return postureCard;`)(
		S, (s, v) => i18n.t(s, v), esc, (id) => `<svg data-icon="${id}"></svg>`, { TraceReportsI18n: i18n });
	return (posture) => { S.settings.posture = posture; return render(); };
}
const check = (id, level, title, detail, fix) => ({ id, level, title, detail, ...(fix ? { fix } : {}) });

test("nada del servidor entra al HTML sin escapar, aunque el chequeo sea desconocido", () => {
	const html = card("es")({ ok: 1, warn: 1, fail: 0, checks: [
		check("x.<img>", "warn", "<img src=x onerror=alert(1)>", "<script>alert(2)</script>", "<b>fix</b>"),
		check("auth.tokens", "ok", "API tokens", "only from <svg onload=alert(3)>"),
	] });
	assert.ok(!/<img|<script|<b>fix|<svg onload/i.test(html), html);
	assert.ok(html.includes("&lt;img src=x onerror=alert(1)&gt;"));
	assert.ok(html.includes("solo desde &lt;svg onload=alert(3)&gt;"), "el valor capturado también va escapado");
});

test("lo crítico va primero, con qué hacer; lo que está en orden va agrupado por tema", () => {
	const html = card("es")({ ok: 2, warn: 1, fail: 1, checks: [
		check("data.retention", "warn", "Retention", "evidence is kept forever", "set TRACEREPORTS_RETENTION_DAYS to what your data policy allows"),
		check("auth.tokens", "ok", "API tokens", "admin and ingest tokens set and different"),
		check("evidence.redaction", "fail", "Secret redaction", "TRACEREPORTS_REDACT=off: secrets are stored as received", "remove TRACEREPORTS_REDACT=off"),
		check("network.exposure", "ok", "Network exposure", "listening only on this machine"),
	] });
	assert.ok(html.indexOf('data-check="evidence.redaction"') < html.indexOf('data-check="data.retention"'), "el crítico antes que el aviso");
	assert.ok(html.includes("→ quita TRACEREPORTS_REDACT=off"));
	assert.ok(html.includes("1 crítico · 1 por revisar"));
	assert.ok(html.includes('stroke-dasharray="50 100"'), "el anillo muestra 2 de 4");
	const enOrden = html.slice(html.indexOf("En orden"));
	assert.ok(enOrden.indexOf("Acceso") < enOrden.indexOf("Red y salida de datos"));
	assert.ok(!enOrden.includes('data-check="data.retention"'), "lo que hay que arreglar no se repite abajo");
});

test("en español se traducen títulos y detalles con sus valores; en inglés se muestra lo del servidor", () => {
	const posture = { ok: 2, warn: 0, fail: 0, checks: [
		check("data.retention", "ok", "Retention", "runs older than 90 days are deleted (secure_delete, VACUUM)"),
		check("process.user", "ok", "Process user", "unprivileged user (uid 65532)"),
	] };
	const es = card("es")(posture), en = card("en")(posture);
	assert.ok(es.includes("Retención") && es.includes("las ejecuciones de más de 90 días se borran"));
	assert.ok(es.includes("usuario sin privilegios (uid 65532)"));
	assert.ok(en.includes("Retention") && en.includes("runs older than 90 days are deleted (secure_delete, VACUUM)"));
	assert.ok(en.includes("Process user") && en.includes("2 of 2 checks in order"));
	// un detalle que la web no conoce se muestra tal cual, sin perder información
	assert.ok(card("es")({ ok: 1, warn: 0, fail: 0, checks: [check("auth.tokens", "ok", "API tokens", "something new")] }).includes("something new"));
});

test("cada chequeo del servidor tiene ícono, título traducido y tema", () => {
	const go = require("node:fs").readFileSync(require("node:path").join(__dirname, "..", "..", "internal", "posture", "posture.go"), "utf8");
	const ids = [...go.matchAll(/Check\{ID: "([^"]+)"/g)].map((m) => m[1]);
	assert.ok(ids.length >= 12);
	for (const id of ids) {
		assert.ok(src.includes(`"${id}": ["i-`), `sin ícono ni título: ${id}`);
		assert.ok(new RegExp(`\\[".+", \\[[^\\]]*"${id.replace(".", "\\.")}"`).test(src), `sin tema: ${id}`);
	}
});
