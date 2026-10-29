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

test("Trace Buddy re-analiza si el servidor ya escribe en ese idioma; si no, lo fija con un clic o dice qué poner en el .env", () => {
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

test("el chat solo aparece con un modelo de lenguaje, y la respuesta del modelo se muestra escapada", () => {
	const fn = section.slice(section.indexOf("const buddyCanChat"), section.indexOf("const buddyHidden"));
	const can = (cfg) => new Function("S", `${fn}; return buddyCanChat();`)({ config: cfg });
	assert.equal(can({ ai_enabled: true, ai_provider: "ollama" }), true);
	assert.equal(can({ ai_enabled: true, ai_provider: "builtin" }), false, "el motor local no conversa");
	assert.equal(can({ ai_enabled: false, ai_provider: "" }), false);
	assert.ok(section.includes('${buddyCanChat() ? `<form class="buddy-chat" id="buddy-chat">'));
	// lo que escribe el modelo nunca entra al HTML sin esc(), ni lo que escribe la persona
	assert.ok(section.includes('${esc(r.answer).replace(/\\n/g, "<br>")}'));
	assert.ok(section.includes('buddySay(esc(text), "me");'));
	// una pregunta sin respuesta no queda en lo que viaja al modelo
	assert.ok(section.includes("buddy.chat.pop();"));
});

test("los pensamientos no molestan: solo con el panel cerrado, la pestaña visible y sin silenciar", () => {
	const fn = section.slice(section.indexOf("function buddyThink()"), section.indexOf("function buddyThoughtHide()"));
	for (const cond of ["root.hidden", "buddy.open", "document.hidden", "buddyIsQuiet()", "buddy.chatting", "buddy.probing"]) {
		assert.ok(fn.includes(cond), `falta la condición ${cond}`);
	}
	assert.ok(fn.includes("buddySpeak(text, 7000)") && fn.includes("const text = buddyThoughtText();"), "se va solo a los 7 s");
	const speak = section.slice(section.indexOf("function buddySpeak("), section.indexOf("const BUDDY_REACTIONS"));
	assert.ok(speak.includes("buddy.open") && speak.includes("setTimeout(buddyThoughtHide, ms)"), "nunca sobre el panel abierto");
	assert.ok(/75000 \+ Math\.random\(\) \* 45000/.test(fn), "uno cada 75 a 120 s");
});

test("piensa en lo que se está viendo: un test fallido, varios en rojo o todo verde", () => {
	const fn = section.slice(section.indexOf("function buddyThoughtText()"), section.indexOf("function buddyThink()"));
	const tr = (s, v) => s.replace(/\{(\w+)\}/g, (_, k) => v[k]);
	const think = (S, n = 3) => new Function("S", "tr", "buddyCanChat", "buddy", `${fn}; return buddyThoughtText();`)(S, tr, () => false, { thoughts: n });
	const fail = { view: "tests", test: { status: "FAIL" }, run: { tests: [{ status: "FAIL" }, { status: "FAIL" }, { status: "PASS" }] } };
	const seen = new Set(Array.from({ length: 60 }, () => think(fail)));
	assert.ok([...seen].some((x) => x.includes("Este test falló")), [...seen].join(" | "));
	assert.ok([...seen].some((x) => x.includes("Hay 2 tests en rojo")));
	const green = { view: "tests", test: { status: "PASS" }, run: { tests: [{ status: "PASS" }] } };
	assert.ok(Array.from({ length: 60 }, () => think(green)).some((x) => x.includes("Todo verde")));
	assert.equal(think(fail, 0), "¡Hola! Estoy aquí si me necesitas.", "el primero es un saludo");
});
