const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;

test("la vista Buscar conserva filtros en el hash y usa cursores", () => {
	const src = read("app.js");
	for (const token of ["view === \"search\"", "next_cursor", "searchParams", "turn(\"run-search\")", "incomplete", "flaky"]) {
		assert.match(src, new RegExp(token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
	}
});

test("los textos visibles de Buscar tienen traducción", () => {
	for (const phrase of ["Buscar", "Con fallos", "Más reciente", "Cargar más", "Selecciona una ejecución para ver el resumen.", "Paleta de comandos"]) {
		assert.notEqual(en.t(phrase), phrase, `sin traducción: ${phrase}`);
	}
});
