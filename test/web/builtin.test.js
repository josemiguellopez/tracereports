const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;

test("el diagnóstico del motor local se marca como local, nunca como IA", () => {
	const src = read("app.js");
	// el badge sale del proveedor guardado con el diagnóstico (test y ejecución), con la regla
	for (const token of [
		"function isLocal(x) { return x?.provider === \"builtin\"; }",
		"isLocal(tr) ? localBadge(tr.rule)",
		"isLocal(sum) ? localBadge(\"\")",
		"incidentCause(inc, \"inc-cause\", isLocal(sum))",
		"incidentCause(inc, \"inc-cause-p\", isLocal(sum))",
		"S.config.ai_provider === \"builtin\" ? \"Diagnosticando con el motor local…\"",
	]) {
		assert.ok(src.includes(token), `falta: ${token}`);
	}
	// el motor local no pide modelo, URL ni API key en Ajustes
	assert.match(src, /p && p\.id !== "builtin" \? `<fieldset class="ai-fields"/);
});

test("los textos del motor local tienen traducción", () => {
	for (const phrase of [
		"Motor local", "Conclusión", "Motor local (sin LLM)",
		"Reglas determinísticas dentro del binario: sin red, sin API key y sin costo. Los datos no salen del servidor.", "Diagnosticando con el motor local…",
		"Motor local de reglas: sin LLM y sin red. Los datos no salen del servidor.",
		"El motor local no encontró una regla concluyente.",
		"Conclusión de una regla del motor local a partir de la evidencia observada. Confírmala antes de tratarla como la causa.",
		"Diagnóstico del motor local de reglas: cada conclusión sale de una regla determinística sobre la evidencia observada, sin IA y sin red.",
		"El motor local reconoce fallos de backend, red, credenciales, selectores, aserciones, tests inestables y ambientes caídos con reglas fijas. No necesita modelo, URL ni API key.",
	]) {
		assert.notEqual(en.t(phrase), phrase, `sin traducción: ${phrase}`);
	}
});
