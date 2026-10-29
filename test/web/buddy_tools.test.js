const test = require("node:test");
const assert = require("node:assert/strict");
const { read } = require("./load.js");

const src = read("app.js");
const section = src.slice(src.indexOf("// ---------- Trace Buddy"), src.indexOf("// ---------- polling (live updates)"));

function tools() {
	const fn = section.slice(section.indexOf("const BUDDY_TOOL_WORDS"), section.indexOf("/** El cURL de la llamada"));
	return new Function(`${fn}; return buddyToolsFor;`)();
}

test("reconoce cuándo la pregunta pide el cURL o un mock (en español e inglés)", () => {
	const forQ = tools();
	assert.deepEqual(forQ("cual fue el endpoint que fallo y como puedo usarlo en postman para replicar el error", 7), ["curl"]);
	assert.deepEqual(forQ("dime el mock", 7), ["mock"]);
	assert.deepEqual(forQ("give me a WireMock stub and the curl", 7), ["curl", "mock"]);
	assert.deepEqual(forQ("¿por qué falló?", 7), [], "una pregunta normal no pide herramientas");
	assert.deepEqual(forQ("dame el curl", 0), [], "sin un test abierto no hay llamada que mostrar");
});

test("las tarjetas muestran el cURL y el mock escapados, armados del reporte", () => {
	const curl = section.slice(section.indexOf("function buddyCurlCard"), section.indexOf("/** Un mock del backend"));
	assert.ok(curl.includes("${esc(curl)}"), "el cURL entra escapado");
	const mock = section.slice(section.indexOf("async function buddyMockCard"), section.indexOf("function buddyGrow"));
	assert.ok(mock.includes("${esc(CF.MockGenerator.generate(first.id, conn))}"), "el mock sale del mismo generador del botón, escapado");
	// cambiar de formato escribe texto, nunca HTML
	assert.ok(section.includes('card.querySelector(".buddy-code").textContent = CF.MockGenerator.generate('));
	assert.ok(section.includes('copyText(copyBtn.closest(".buddy-tool").querySelector(".buddy-code").textContent, copyBtn)'));
});
