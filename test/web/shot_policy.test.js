const test = require("node:test");
const assert = require("node:assert/strict");
const { read } = require("./load.js");

const src = read("app.js");

// shotPolicyText de verdad: con qué política y en qué test explica que faltan las capturas
function note(policy, test, isStatic = false) {
	const fn = src.slice(src.indexOf("function shotPolicyText"), src.indexOf("function shotPolicyNote"));
	return new Function("S", "STATIC", "tr", `${fn}; return shotPolicyText;`)({ config: { screenshots: policy } }, isStatic, (s) => s)(test);
}
const pass = { status: "PASS", logs: [{ screenshot: "" }, { screenshot: "" }] };
const fail = { status: "FAIL", logs: [{ screenshot: "" }] };
const withShot = { status: "PASS", logs: [{ screenshot: "/api/v1/artifacts/1" }] };

test("con failures, un test que pasó explica por qué no tiene capturas", () => {
	assert.match(note("failures", pass), /TRACEREPORTS_SCREENSHOTS=failures/);
	assert.equal(note("failures", fail), "", "un fallo sí guarda las suyas");
	assert.equal(note("failures", withShot), "", "si hay capturas no hay nada que explicar");
});

test("con off lo explica en cualquier test; con all o en un reporte exportado, nunca", () => {
	assert.match(note("off", fail), /TRACEREPORTS_SCREENSHOTS=off/);
	assert.equal(note("all", pass), "");
	assert.equal(note(undefined, pass), "", "un servidor anterior no informa la política");
	assert.equal(note("failures", pass, true), "");
});

test("Tty solo ofrece la pregunta de las capturas cuando el servidor descarta alguna", () => {
	assert.ok(src.includes('const buddyAsks = () => BUDDY_ASKS.filter(([id]) => id !== "shots" || (S.config.screenshots && S.config.screenshots !== "all"));'));
	assert.ok(src.includes("${buddyAsks().map("));
});
