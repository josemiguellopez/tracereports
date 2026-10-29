// Tests del Request Lab de FixLab (web/lab.js): el cURL que copia el dev nunca lleva valores (solo
// $VARIABLES que se expanden en su shell), los headers editados se leen bien y cada veredicto tiene
// su texto en los dos idiomas.
const test = require("node:test");
const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const { load, read } = require("./load.js");

const files = ["i18n.js", "i18n.en.js", "tracereports_features.js", "lab.js"];
const L = load(files).TraceReportsLab;
const en = load(files, { lang: "en" });
// los objetos del contexto aislado tienen otro prototipo: se comparan como datos planos
const plain = (v) => JSON.parse(JSON.stringify(v));

test("parseHeaders y headersText van y vuelven", () => {
	const h = L.parseHeaders("content-type: application/json\nauthorization: {{authorization}}\nsin dos puntos\n x-a : b:c ");
	assert.deepEqual(JSON.parse(JSON.stringify(h)), { "content-type": "application/json", authorization: "{{authorization}}", "x-a": "b:c" });
	assert.equal(L.headersText({ a: "1", b: "2" }), "a: 1\nb: 2");
});

test("envName arma nombres de variable de shell", () => {
	assert.equal(L.envName("_token"), "TOKEN");
	assert.equal(L.envName("x-api-key"), "X_API_KEY");
	assert.equal(L.envName("user[password]"), "USER_PASSWORD");
	assert.equal(L.envName("--"), "VALUE");
});

test("labCurl deja las variables como $VARIABLES y declara cuáles definir", () => {
	const curl = L.labCurl({
		method: "post", url: "https://shop.test/api/login?next={{next}}",
		headers: { "content-type": "application/json", authorization: "{{authorization}}", host: "x", ":authority": "y" },
		body: `{"email":"qa@shop.test","password":"{{password}}"}`,
	});
	assert.match(curl, /^# Define antes: NEXT=… AUTHORIZATION=… PASSWORD=…\n/);
	assert.match(curl, /curl -X POST 'https:\/\/shop\.test\/api\/login\?next='"\$NEXT"/);
	assert.match(curl, /-H 'authorization: '"\$AUTHORIZATION"/);
	assert.match(curl, /--data-raw '\{"email":"qa@shop\.test","password":"'"\$PASSWORD"'"\}'/);
	assert.doesNotMatch(curl, /host: x|:authority/);
});

test("labCurl: la shell recibe el valor de la variable y nada de lo capturado se ejecuta", { skip: process.platform === "win32" && "sin sh POSIX garantizado" }, () => {
	const curl = L.labCurl({ method: "GET", url: "https://x.test/$(touch /tmp/pwned)'`id`", headers: { "x-k": "{{k}}" }, body: "" });
	const args = execFileSync("sh", ["-c", `curl() { printf '%s\\n' "$@"; }; ${curl.split("\n").slice(1).join("\n")}`], { env: { K: "va lor'\"" } }).toString();
	assert.match(args, /^-X\nGET\nhttps:\/\/x\.test\/\$\(touch \/tmp\/pwned\)'`id`\n-H\nx-k: va lor'"\n$/);
});

test("un método raro va entre comillas", () => {
	assert.match(L.labCurl({ method: "GET;rm", url: "u", headers: {} }), /curl -X 'GET;RM' 'u'/);
});

test("verdictInfo: cada veredicto tiene título, texto y estilo", () => {
	for (const v of ["reproduced", "fixed", "session", "different", "network", "ok"]) {
		const i = L.verdictInfo(v);
		assert.ok(i.title && i.text && i.cls, v);
	}
	assert.equal(L.verdictInfo("reproduced").cls, "bad");
	assert.equal(L.verdictInfo("fixed").cls, "good");
	assert.equal(L.verdictInfo("desconocido").title, "Otro error");
	assert.match(L.verdictInfo("fixed", { simulated: true }).text, /simulada por el propio test/);
	assert.doesNotMatch(L.verdictInfo("reproduced", { simulated: true }).text, /simulada/);
});

test("verdictInfo en inglés", () => {
	const i = en.TraceReportsLab.verdictInfo("fixed", { simulated: true });
	assert.equal(i.title, "No longer fails");
	assert.match(i.text, /looks simulated by the test itself/);
});

test("todo tr(\"…\") de lab.js tiene traducción", () => {
	const t = en.TraceReportsI18n.t;
	const src = read("lab.js");
	const used = [...src.matchAll(/\btr\("((?:[^"\\]|\\.)+)"/g)].map((m) => m[1]);
	// los textos de las tablas (veredictos, motivos, pasos) también se pasan por tr()
	const tables = [...src.matchAll(/(?:title|text): "((?:[^"\\]|\\.)+)"/g), ...src.matchAll(/^\s+\w+: "((?:[^"\\]|\\.)+)",?$/gm)].map((m) => m[1]);
	assert.ok(used.length > 20);
	// "login", "Lab", "Headers"... se escriben igual en inglés: basta con que estén en el diccionario
	const known = (s) => t(s) !== s || Object.hasOwn(en.TraceReportsI18n.EN.exact, s);
	for (const s of [...used, ...tables]) {
		if (/^(bad|good|warn)$/.test(s)) continue;
		assert.ok(known(s), `sin traducción: "${s}"`);
	}
});

test("los botones del Lab en app.js tienen traducción", () => {
	const t = en.TraceReportsI18n.t;
	const src = read("app.js");
	const tips = [...src.matchAll(/data-lab data-tip="([^"$]+)"/g)].map((m) => m[1]);
	assert.ok(tips.length > 0);
	for (const s of [...tips, "Abrir en Lab", "Reenvía la llamada que causó este incidente para ver si sigue fallando o ya quedó resuelto"]) {
		assert.notEqual(t(s), s, `sin traducción: "${s}"`);
	}
});

test("expectText y failureText describen un check en palabras", () => {
	assert.equal(L.expectText({ status: 200, contains: ["orders", "A-1"] }), "status 200 · contiene «orders» · contiene «A-1»");
	assert.equal(L.expectText('{"status":0}'), "status menor a 400");
	assert.equal(L.failureText({ kind: "status", want: "200", got: "500" }), "status 500 (se esperaba 200)");
	assert.equal(L.failureText({ kind: "contains", want: "orders" }), "no contiene «orders»");
	assert.equal(L.failureText({ kind: "network", got: "timeout" }), "sin respuesta: timeout");
	assert.equal(en.TraceReportsLab.expectText({ status: 200, contains: ["x"] }), "status 200 · contains «x»");
});

test("verification resume el resultado de cada check", () => {
	const html = L.verification({ ok: false, results: JSON.stringify([
		{ name: "pedidos", ok: false, before: "500", status: 500, failures: [{ kind: "status", want: "200", got: "500" }] },
		{ name: "login", ok: true, before: "500", status: 302 },
	]) });
	assert.match(html, /data-verified="fail"/);
	assert.match(html, /Sigue fallando/);
	assert.match(html, /1 de 2 checks no pasan/);
	assert.match(html, /status 500 \(se esperaba 200\)/);
	assert.match(L.verification({ ok: true, results: [{ name: "a", ok: true, before: "500", status: 200 }] }), /Resuelto y verificado[\s\S]*El check pasa\./);
});

test("capsuleCommand deja la URL entre comillas para la shell", () => {
	assert.equal(L.capsuleCommand("verify", "/api/v1/runs/3/lab/capsule?incident=backend%3AGET&x=1", "http://localhost:8080"),
		"tracereports verify 'http://localhost:8080/api/v1/runs/3/lab/capsule?incident=backend%3AGET&x=1'");
	assert.equal(L.capsuleCommand("repro", "/a'b", "http://h"), "tracereports repro 'http://h/a'\\''b'");
});

// Un plan como el que entrega el servidor: login JWT y la llamada que falló.
const PLAN = {
	conn_id: 2,
	target: { method: "GET", url: "https://shop.test/api/orders?status=open", headers: { authorization: "{{authorization}}", cookie: "{{cookie}}", accept: "application/json" }, body: "" },
	login: { conn_id: 1, token_path: ["data", "accessToken"], request: { method: "POST", url: "https://shop.test/api/login", headers: { "content-type": "application/json" }, body: '{"email":"qa@shop.test","password":"{{password}}"}' } },
	vars: [{ name: "authorization", kind: "session" }, { name: "password", kind: "secret" }],
};

test("toPostman: colección válida, login que guarda el token y variables sin valor", () => {
	const col = JSON.parse(L.toPostman(PLAN, PLAN.target, "demo"));
	assert.equal(col.info.schema, "https://schema.getpostman.com/json/collection/v2.1.0/collection.json");
	assert.equal(col.item.length, 2);
	assert.equal(col.item[0].request.method, "POST");
	assert.deepEqual(plain(col.item[0].event[0].script.exec), ['pm.collectionVariables.set("token", pm.response.json()["data"]["accessToken"]);']);
	assert.equal(col.item[0].request.body.options.raw.language, "json");
	const h = Object.fromEntries(col.item[1].request.header.map((x) => [x.key, x.value]));
	assert.equal(h.authorization, "Bearer {{token}}");
	assert.equal(h.cookie, undefined, "las cookies las maneja Postman");
	assert.deepEqual(plain(col.variable).map((v) => v.key).sort(), ["password", "token"]);
	assert.ok(col.variable.every((v) => v.value === ""));
});

test("toHttp: el login con nombre y la llamada que usa su token (REST Client)", () => {
	const http = L.toHttp(PLAN, PLAN.target);
	assert.match(http, /^# .*\n@password =\n\n# @name login\nPOST https:\/\/shop\.test\/api\/login\ncontent-type: application\/json\n\n\{"email":"qa@shop\.test","password":"\{\{password\}\}"\}\n\n###\n\nGET https:\/\/shop\.test\/api\/orders\?status=open\n/);
	assert.match(http, /authorization: Bearer \{\{login\.response\.body\.\$\.data\.accessToken\}\}/);
	assert.doesNotMatch(http, /cookie:/);
});

test("toBruno: la llamada en formato .bru y el login documentado", () => {
	const bru = L.toBruno(PLAN, PLAN.target);
	assert.match(bru, /^meta \{\n  name: GET \/api\/orders\?status=open\n  type: http\n  seq: 1\n\}/);
	assert.match(bru, /get \{\n  url: https:\/\/shop\.test\/api\/orders\?status=open\n  body: none\n  auth: none\n\}/);
	assert.match(bru, /headers \{\n  authorization: Bearer \{\{token\}\}\n  accept: application\/json\n\}/);
	assert.match(bru, /docs \{\n  Antes, el login grabado:\n  POST https:\/\/shop\.test\/api\/login/);
	const noLogin = L.toBruno({ ...PLAN, login: null }, { method: "POST", url: "https://x/y", headers: { "content-type": "application/json" }, body: '{"a":"{{b}}"}' });
	assert.match(noLogin, /body:json \{\n  \{"a":"\{\{b\}\}"\}\n\}/);
	assert.doesNotMatch(noLogin, /docs \{/);
});

// Una llamada sin datos enmascarados llegaba con "vars": null y el Lab se quedaba en
// "Preparando el Lab…" para siempre: ahora se dibuja, y si el plan trae algo raro muestra el error.
test("open dibuja un plan sin vars y nunca se queda en «Preparando el Lab…»", async () => {
	const plan = (extra) => ({
		any_host: false, can_send: true, lab_hosts: null, reason: "",
		plan: { conn_id: 625, target: { method: "GET", url: "https://shop.test/", headers: {} }, vars: null, original: { status: 200 }, hosts: ["shop.test"], ...extra },
	});
	let answer;
	const wrap = { innerHTML: "", addEventListener() {}, querySelector: () => null, querySelectorAll: () => [] };
	const w = load(files, {
		globals: {
			fetch: async () => ({ ok: true, json: async () => answer }),
			document: { addEventListener() {}, createElement: () => wrap, documentElement: { classList: { remove() {} } } },
		},
	});
	w.TraceReportsFeatures.Drawer = { open() {} };

	answer = plan({});
	await w.TraceReportsLab.open(625);
	assert.match(wrap.innerHTML, /Esta llamada no tiene datos enmascarados/);
	assert.doesNotMatch(wrap.innerHTML, /Preparando el Lab/);

	answer = plan({ csrf: { page_url: "https://shop.test/login", fields: null } });
	await w.TraceReportsLab.open(625);
	assert.match(wrap.innerHTML, /Abre la página para obtener una sesión nueva/);

	answer = plan({ target: null });
	await w.TraceReportsLab.open(625);
	assert.match(wrap.innerHTML, /No se pudo preparar el Lab/);
});
