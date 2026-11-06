// Dos funciones con el mismo nombre en el mismo archivo no dan error: gana la última y la otra queda
// muerta sin aviso. Así las preguntas sugeridas de Trace Buddy ("¿Está segura mi instalación?") le
// llegaban al modelo como texto ("security") en vez de correr la revisión: buddyAsk(id, label) quedó
// tapada por buddyAsk(text).
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { read } = require("./load.js");

const WEB = path.join(__dirname, "..", "..", "web");

test("ningún archivo de web/ declara dos veces la misma función en su nivel principal", () => {
	const files = fs.readdirSync(WEB).filter((f) => f.endsWith(".js"));
	assert.ok(files.includes("app.js"));
	const dups = [];
	for (const f of files) {
		// el nivel principal de cada módulo: dentro de su IIFE, con una tabulación
		const names = [...read(f).matchAll(/^\t(?:async )?function\*? (\w+)\s*\(/gm)].map((m) => m[1]);
		for (const n of new Set(names)) if (names.filter((x) => x === n).length > 1) dups.push(`${f}: ${n}`);
	}
	assert.deepEqual(dups, []);
});

test("las preguntas sugeridas de Trace Buddy corren su acción, no le preguntan al modelo", () => {
	const src = read("app.js");
	assert.match(src, /function buddyAsk\(id, label\) \{[^}]*security: buddySecurity/);
	assert.ok(!/function buddyAsk\(text\)/.test(src), "la pregunta libre tiene su propio nombre");
	assert.match(src, /buddyAskText\(cmpQuestion\(/, "\"Explícamelo\" de la comparación sigue preguntándole al modelo");
});
