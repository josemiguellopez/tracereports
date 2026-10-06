// Tests del generador de mocks/stubs y de "Copiar como cURL" (web/tracereports_features.js): un
// cambio en un formato no debe romper los demás ni dejar pasar un dato sensible a lo que el dev
// copia o descarga.
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");

const { MockGenerator: G, curlOf, maskValue, esc, sparkline } = load(["tracereports_features.js"]).TraceReportsFeatures;
// Los objetos del contexto aislado tienen otro prototipo: se comparan como datos planos.
const plain = (v) => JSON.parse(JSON.stringify(v));

// Una llamada fallida con secretos en todos los lugares posibles (y otros que llegan ya
// enmascarados por el servidor como <masked>).
const SECRETS = ["hunter2", "SEKRET-BEARER", "sid=COOKIE1", "APIKEY123", "QTOKEN9", "12.345.678-9", "SESSION77"];
const leaky = () => ({
	method: "POST",
	url: "https://api.example.com/auth/login?lang=es&token=QTOKEN9",
	status: 500,
	status_text: "Internal Server Error",
	mime_type: "application/json; charset=utf-8",
	request_headers: {
		":authority": "api.example.com", "Content-Type": "application/json",
		Authorization: "Bearer SEKRET-BEARER", Cookie: "sid=COOKIE1", "X-Api-Key": "APIKEY123", "X-Masked": "<masked>",
	},
	post_data: JSON.stringify({ username: "admin", password: "hunter2", rut: "12.345.678-9", note: "<masked>" }),
	response_body: JSON.stringify({ error: "db pool exhausted", session: "SESSION77", user: { rut: "12.345.678-9" } }),
});

const assertNoLeak = (out, where) => {
	for (const s of SECRETS) assert.ok(!out.includes(s), `${where} filtra "${s}"`);
	assert.ok(!out.includes("<masked>"), `${where} deja el marcador <masked>`);
};

test.describe("FORMATS", () => {
	test("los formatos del panel, en orden", () => {
		assert.deepEqual(plain(G.FORMATS.map((f) => f.id)), ["pw-py", "pw-js", "cypress", "wiremock"]);
	});

	test("cada formato tiene label/ext/lang y genera contenido", () => {
		for (const f of G.FORMATS) {
			assert.ok(f.label && f.ext && f.lang, `${f.id}: falta label/ext/lang`);
			assert.ok(G.generate(f.id, leaky()).trim().length > 0, `${f.id}: generate() vacío (¿falta su case?)`);
		}
	});

	test("formato desconocido devuelve vacío", () => {
		assert.equal(G.generate("nope", leaky()), "");
	});

	for (const id of ["pw-py", "pw-js", "cypress", "wiremock"]) {
		test(`${id}: no filtra ningún dato sensible`, () => assertNoLeak(G.generate(id, leaky()), id));
	}
});

test.describe("maskValue", () => {
	test("enmascara por nombre de campo, también anidado y en arrays", () => {
		const out = maskValue({ password: "x", Token: 123, a: { api_key: "k", ok: "v" }, list: [{ secret: "s" }], rut: null });
		assert.deepEqual(plain(out), { password: "***", Token: "***", a: { api_key: "***", ok: "v" }, list: [{ secret: "***" }], rut: null });
	});

	test("enmascara RUT y marcadores dentro de textos", () => {
		assert.equal(maskValue("cliente 12.345.678-9 y 9876543-k <masked> <rut>"), "cliente *** y *** *** ***");
	});

	test("no toca números ni booleanos de campos normales", () => {
		assert.deepEqual(plain(maskValue({ count: 3, ok: true })), { count: 3, ok: true });
	});
});

test.describe("parse", () => {
	test("normaliza URL, glob, content-type y status", () => {
		const m = G.parse({ method: "GET", url: "https://h.com/api/items?page=2", status: 404, mime_type: "text/plain; charset=utf-8", response_body: "x" });
		assert.equal(m.path, "/api/items");
		assert.equal(m.glob, "**/api/items*");
		assert.equal(m.contentType, "text/plain");
		assert.equal(m.status, 404);
		assert.equal(m.json, null);
	});

	test("valores por defecto: GET, status 500, JSON", () => {
		const m = G.parse({ url: "https://h.com/x", response_body: "{}" });
		assert.equal(m.method, "GET");
		assert.equal(m.status, 500);
		assert.equal(m.contentType, "application/json");
		assert.equal(m.glob, "**/x");
	});

	test("URL relativa no rompe", () => {
		assert.equal(G.parse({ url: "/relative/path", response_body: "" }).path, "/relative/path");
	});
});

test.describe("formatos de mock", () => {
	const conn = { method: "GET", url: "https://h.com/api/user", status: 503, response_body: '{"ok":false,"data":null,"n":1}' };

	test("pw-py: literales de Python y route.fulfill", () => {
		const out = G.generate("pw-py", conn);
		assert.match(out, /def stub_user\(route\):/);
		assert.match(out, /"ok": False/);
		assert.match(out, /"data": None/);
		assert.match(out, /status=503/);
		assert.match(out, /page\.route\("\*\*\/api\/user", stub_user\)/);
	});

	test("pw-js y cypress: método, glob y status", () => {
		assert.match(G.generate("pw-js", conn), /page\.route\('\*\*\/api\/user'[\s\S]*status: 503/);
		assert.match(G.generate("cypress", conn), /cy\.intercept\('GET', '\*\*\/api\/user'[\s\S]*statusCode: 503[\s\S]*\.as\('user'\)/);
	});

	test("wiremock: JSON válido con jsonBody y query enmascarada", () => {
		const w = JSON.parse(G.generate("wiremock", leaky()));
		assert.deepEqual(w.request, { method: "POST", urlPath: "/auth/login", queryParameters: { lang: { equalTo: "es" }, token: { equalTo: "***" } } });
		assert.equal(w.response.status, 500);
		assert.equal(w.response.jsonBody.session, "***");
		assert.equal(w.response.headers["Content-Type"], "application/json");
	});

	test("wiremock: body de texto va en body, no en jsonBody", () => {
		const w = JSON.parse(G.generate("wiremock", { url: "https://h.com/x", status: 500, response_body: "boom" }));
		assert.equal(w.response.body, "boom");
		assert.equal(w.response.jsonBody, undefined);
	});
});

test.describe("curlOf (Copiar como cURL)", () => {
	test("no filtra ningún dato sensible", () => assertNoLeak(curlOf(leaky()), "curlOf"));

	test("URL con el query sensible enmascarado", () => {
		assert.ok(curlOf(leaky()).startsWith("curl -X POST 'https://api.example.com/auth/login?lang=es&token=***' \\\n"));
	});

	test("headers sensibles o enmascarados pasan a variables de entorno, el resto entre comillas", () => {
		const out = curlOf(leaky());
		assert.ok(out.includes(`-H "Authorization: $AUTHORIZATION"`));
		assert.ok(out.includes(`-H "Cookie: $COOKIE"`));
		assert.ok(out.includes(`-H "X-Api-Key: $X_API_KEY"`));
		assert.ok(out.includes(`-H "X-Masked: $X_MASKED"`));
		assert.ok(out.includes(`-H 'Content-Type: application/json'`));
		assert.ok(!out.includes(":authority"));
	});

	test("body JSON enmascarado", () => {
		assert.ok(curlOf(leaky()).endsWith(`--data-raw '{"username":"admin","password":"***","rut":"***","note":"***"}'`));
	});

	test("escapa comillas simples para bash", () => {
		assert.ok(curlOf({ method: "POST", url: "http://h/x", post_data: `{"name":"O'Brien"}` }).endsWith(`--data-raw '{"name":"O'\\''Brien"}'`));
	});

	test("body de texto: enmascara marcadores", () => {
		assert.ok(curlOf({ method: "POST", url: "http://h/x", post_data: "a=1&pwd=<masked>" }).endsWith("--data-raw 'a=1&pwd=***'"));
	});

	test("llamada mínima: una sola línea", () => {
		assert.equal(curlOf({ url: "http://h/x" }), "curl -X GET 'http://h/x'");
	});

	test("URL relativa se deja como vino", () => {
		assert.equal(curlOf({ method: "GET", url: "/api/x?token=<masked>" }), "curl -X GET '/api/x?token=***'");
	});
});

test.describe("highlight y utilidades", () => {
	test("highlight escapa HTML del body (sin XSS) y marca lo enmascarado", () => {
		const out = G.highlight(G.generate("pw-js", { url: "https://h/x", status: 500, response_body: '{"html":"<img src=x onerror=alert(1)>","password":"p"}' }));
		assert.ok(!out.includes("<img"));
		assert.ok(out.includes("&lt;img"));
		assert.ok(out.includes('<mark class="cf-masked"'));
	});

	test("esc escapa los 5 caracteres de HTML", () => {
		assert.equal(esc(`<a href="x">'&'</a>`), "&lt;a href=&quot;x&quot;&gt;&#39;&amp;&#39;&lt;/a&gt;");
		assert.equal(esc(null), "");
	});

	test("sparkline: una barra por estado con su alto", () => {
		const out = sparkline(["PASS", "FAIL", "SKIP"]);
		assert.equal((out.match(/<i /g) || []).length, 3);
		assert.match(out, /class="fail" style="height:100%"/);
		assert.match(out, /class="skip" style="height:35%"/);
		assert.match(out, /class="pass" style="height:60%"/);
	});
});
