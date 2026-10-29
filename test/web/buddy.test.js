const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;
const src = read("app.js");
const section = src.slice(src.indexOf("// ---------- Trace Buddy"), src.indexOf("// ---------- polling (live updates)"));

test("cada texto de Trace Buddy tiene traducción", () => {
	const texts = new Set();
	for (const m of section.matchAll(/tr\("([^"]+)"/g)) texts.add(m[1]);
	for (const m of section.matchAll(/^\s+\w+: "([^"]+)",$/gm)) texts.add(m[1]); // BUDDY_HINTS
	for (const m of section.matchAll(/\["\w+", "([^"]+)"\]/g)) texts.add(m[1]); // BUDDY_ASKS
	for (const t of ["Tu asistente de QA", "Ocultar", "Abrir Trace Buddy", "Cerrar", "Preguntas", "Mostrar",
		"El asistente de QA del botón de abajo a la derecha. Se guarda en este navegador."]) texts.add(t);
	assert.ok(texts.size > 30, `pocos textos encontrados: ${texts.size}`);
	for (const t of texts) assert.ok(Object.hasOwn(en.EN.exact, t), `sin traducción: ${t}`);
});

// textLang y buddyLangNote de verdad, con lo mínimo alrededor
function langParts(uiLang, aiLang, editable = false) {
	const fn = section.slice(section.indexOf("const LANG_WORDS"), section.indexOf("async function buddyReanalyze"));
	const tr = (s, v) => s.replace(/\{(\w+)\}/g, (_, k) => v[k]);
	return new Function("I18N", "S", "tr", "LANG_NAME", `${fn}; return { textLang, buddyLangNote };`)(
		{ lang: uiLang }, { config: { ai_language: aiLang, settings_editable: editable } }, tr, { es: "español", en: "inglés" });
}

test("distingue un diagnóstico en español de uno en inglés (textos reales de llama3.1)", () => {
	const { textLang } = langParts("es", "es");
	assert.equal(textLang("The test failed due to a 500 Internal Server Error response from the backend API, which indicates a backend timeout."), "en");
	assert.equal(textLang("El backend respondió con un error justo antes del fallo: POST localhost:8180/api/orders → HTTP 500, 5,1 s antes del fallo."), "es");
	assert.equal(textLang("El test no encontró el elemento #add-to-cart-btn debido a que el selector ya no coincide con la página"), "es");
	assert.equal(textLang("HTTP 500"), "", "con muy pocas palabras no adivina");
});

test("Tty re-analiza si el servidor ya escribe en ese idioma; si no, lo fija con un clic o dice qué poner en el .env", () => {
	const en = { id: 7, triage: { summary: "The test failed before the order was confirmed and the backend returned an error.", suggestion: "Check the logs of the service." } };
	const yaEnEspanol = langParts("es", "es").buddyLangNote(en);
	assert.ok(yaEnEspanol.includes('data-buddy-reanalyze="7"') && yaEnEspanol.includes("Re-analizarlo en español"), yaEnEspanol);
	const editable = langParts("es", "", true).buddyLangNote(en);
	assert.ok(editable.includes('data-buddy-lang="es"') && editable.includes('data-test="7"') && !editable.includes("data-buddy-reanalyze"), editable);
	const bloqueado = langParts("es", "", false).buddyLangNote(en);
	assert.ok(bloqueado.includes("TRACEREPORTS_AI_LANGUAGE=es") && !bloqueado.includes("<button"), bloqueado);
	const es = { id: 8, triage: { summary: "El backend respondió con un error justo antes del fallo de la prueba.", suggestion: "Revisa los logs del servicio." } };
	assert.equal(langParts("es", "es").buddyLangNote(es), "", "mismo idioma: nada que avisar");
});
