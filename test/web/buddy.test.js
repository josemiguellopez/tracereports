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
