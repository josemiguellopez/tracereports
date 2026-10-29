const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;
const src = read("app.js");

test("el motor local no se presenta como Ollama en el estado del proveedor", () => {
	// los dos tienen source "local": el texto depende del proveedor, no solo de la fuente
	assert.ok(src.includes('s.provider === "builtin" ? tr("Funciona dentro del servidor (reglas {v})", { v: s.description })'));
	assert.ok(src.indexOf('s.provider === "builtin"') < src.indexOf('tr("Ollama responde (versión {v})"'));
	assert.equal(en.t("Funciona dentro del servidor (reglas {v})", { v: "rules-v1" }), "Runs inside the server (rules rules-v1)");
});
