const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { read } = require("./load.js");

const app = read("app.js");
const three = fs.readFileSync(path.join(__dirname, "..", "..", "web", "buddy", "buddy3d.js"), "utf8");

test("cada tema de la interfaz tiene ropa para Trace Buddy en 3D", () => {
	const themes = [...app.slice(app.indexOf("const THEMES = ["), app.indexOf("];", app.indexOf("const THEMES = ["))).matchAll(/id: "([\w-]+)"/g)].map((m) => m[1]);
	assert.ok(themes.length >= 6, `temas: ${themes}`);
	for (const id of themes) assert.ok(three.includes(`"${id}": {`), `sin ropa para el tema ${id}`);
});

test("el 3D se carga aparte y solo cuando Trace Buddy se ve; sin WebGL queda la imagen", () => {
	assert.ok(app.includes('import("./buddy/buddy3d.js")'), "carga diferida del módulo");
	assert.ok(three.includes('import * as THREE from "./three.module.min.js";'), "three.js local, sin CDN");
	assert.ok(three.includes("return null; // sin WebGL"));
	// no entra al ZIP exportado: el reporte offline no lo usa
	const exp = fs.readFileSync(path.join(__dirname, "..", "..", "internal", "api", "export.go"), "utf8");
	assert.ok(!exp.includes("buddy"), "web/buddy no se copia al ZIP");
});
