const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;

test("el diagnóstico muestra los incidentes parecidos que el equipo ya clasificó", () => {
	const src = read("app.js");
	for (const token of [
		"ensureSimilar(t);",
		"fetch(`/api/v1/tests/${t.id}/similar`)",
		"${similarBlock(t)}",
		"<span class=\"similar-score\">${s.score}%</span>",
		"verdictName(s.verdict)",
		"if (t.status !== \"FAIL\" || STATIC || S.sim[t.id] !== undefined) return;",
	]) {
		assert.ok(src.includes(token), `falta: ${token}`);
	}
	// sin parecidos no se muestra nada (ni un título vacío)
	assert.match(src, /if \(!list\?\.length\) return "";/);
});

test("los textos de incidentes parecidos tienen traducción", () => {
	for (const phrase of ["Incidentes parecidos",
		"Fallos que el equipo ya clasificó y se parecen a este por su firma (la llamada que falló o el error). El porcentaje es el parecido, no una probabilidad."]) {
		assert.notEqual(en.t(phrase), phrase, `sin traducción: ${phrase}`);
	}
});
