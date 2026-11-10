// Mantenimiento > Conexiones de los clientes y el aviso de entrega del run: todo texto tiene inglés.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load, read } = require("./load.js");

test("los textos de Conexiones y del aviso de entrega del run están en inglés", () => {
	const en = load(["i18n.js", "i18n.en.js"], { lang: "en" }).TraceReportsI18n;
	const app = read("app.js");
	const blocks = [app.slice(app.indexOf("async function loadConnections"), app.indexOf("async function loadMaintenance")),
		app.slice(app.indexOf("const dv = r?.delivery"), app.indexOf('const show = S.view === "tests" && sum'))];
	const texts = new Set(blocks.flatMap((b) => [...b.matchAll(/\btr\("([^"]+)"/g)].map((m) => m[1])));
	assert.ok(texts.size > 25);
	for (const s of texts) if (s !== "Run") assert.notEqual(en.t(s), s, `"${s}" sin inglés`);
});
