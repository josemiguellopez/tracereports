// Los stubs generados son código válido y devuelven exactamente los datos capturados, aunque la
// URL, el tipo o el cuerpo traigan apóstrofos, comillas, backslashes, saltos de línea, Unicode o
// palabras como true/false/null dentro de un texto. Se ejecutan: JS y Cypress en un contexto
// aislado; Python con el intérprete del sistema.
const test = require("node:test");
const assert = require("node:assert/strict");
const vm = require("node:vm");
const { spawnSync } = require("node:child_process");
const { load } = require("./load.js");

const { MockGenerator: G } = load(["tracereports_features.js"]).TraceReportsFeatures;

const data = {
	message: "true false null",
	"true": "None",
	quote: `it's "quoted" \\ back\\slash`,
	lines: "a\nb\r\nc d",
	unicode: "ñandú 😀 \u0000 \u001f",
	nested: [true, false, null, 0, -1.5, 1e21, { "": "", "__proto__": { x: 1 } }],
	empty: { list: [], obj: {} },
};
const body = JSON.stringify(data);
const expected = JSON.parse(body);
const conns = [
	{ method: "GET", url: "https://fake.test/api/o'reilly/\"x\"", status: 200, mime_type: "application/json", response_body: body },
	{ method: "POST", url: "relative/it's\\back\nslash\"q", status: 201, mime_type: "application/x'type", response_body: body },
	{ method: "GET", url: "https://fake.test/plain", status: 500, mime_type: "text/plain", response_body: `it's "plain" \\ text\nline 2 ñ` },
];
const want = (c) => (c.response_body === body ? expected : c.response_body);
const parsed = (c) => G.parse(c);

/** Ejecuta un stub de Playwright (JS) con un page/route falso y devuelve lo que respondió. */
function runPlaywright(code, method) {
	let handler, glob;
	const page = { route: (g, h) => { glob = g; handler = h; } };
	vm.runInNewContext(`(async () => {\n${code}\n})()`, { page });
	let out;
	const route = { request: () => ({ method: () => method }), fallback: () => { throw new Error("fallback"); }, fulfill: (o) => { out = o; } };
	return Promise.resolve(handler(route)).then(() => ({ glob, ...out }));
}

for (const c of conns) {
	test(`pw-js: código válido y los mismos datos (${c.url.slice(0, 30)})`, async () => {
		const code = G.generate("pw-js", c);
		assert.doesNotThrow(() => new vm.Script(`(async () => {\n${code}\n})()`), code);
		const out = await runPlaywright(code, c.method);
		assert.equal(out.glob, parsed(c).glob);
		assert.equal(out.status, c.status);
		assert.equal(out.contentType, c.mime_type);
		assert.deepEqual(typeof want(c) === "string" ? out.body : JSON.parse(out.body), want(c));
	});

	test(`cypress: código válido y los mismos datos (${c.url.slice(0, 30)})`, () => {
		const code = G.generate("cypress", c);
		let call, alias;
		const cy = { intercept: (...args) => { call = args; return { as: (a) => { alias = a; } }; } };
		vm.runInNewContext(code, { cy });
		assert.equal(call[0], c.method);
		assert.equal(call[1], parsed(c).glob);
		assert.equal(call[2].statusCode, c.status);
		assert.equal(call[2].headers["content-type"], c.mime_type);
		// del contexto aislado a datos planos: un campo "__proto__" propio sobrevive al ida y vuelta
		assert.deepEqual(JSON.parse(JSON.stringify(call[2].body)), want(c));
		assert.match(alias, /^\w+$/);
	});
}

const python = process.env.PYTHON || process.env.AUDIT_PYTHON || "python";
const hasPython = spawnSync(python, ["--version"]).status === 0;

test("pw-py: el stub corre en Python y responde los mismos datos", { skip: !hasPython && `needs ${python}` }, () => {
	for (const c of conns) {
		const code = G.generate("pw-py", c);
		const fn = code.match(/^def (\w+)\(route\):/m)[1];
		const harness = `import json, sys
class Page:
    def route(self, glob, handler):
        self.glob, self.handler = glob, handler
page = Page()
${code}
class Request:
    method = ${JSON.stringify(c.method)}
class Route:
    request = Request()
    def fallback(self):
        raise AssertionError("unexpected fallback")
    def fulfill(self, **kw):
        self.kw = kw
route = Route()
page.handler(route)
assert page.handler is ${fn}
sys.stdout.write(json.dumps({"glob": page.glob, "status": route.kw["status"], "type": route.kw["content_type"], "body": route.kw["body"]}))
`;
		const res = spawnSync(python, ["-c", harness], { encoding: "utf8", env: { ...process.env, PYTHONIOENCODING: "utf-8" } });
		assert.equal(res.status, 0, `${res.stderr}\n${code}`);
		const out = JSON.parse(res.stdout);
		assert.equal(out.glob, parsed(c).glob);
		assert.equal(out.status, c.status);
		assert.equal(out.type, c.mime_type);
		assert.deepEqual(typeof want(c) === "string" ? out.body : JSON.parse(out.body), want(c));
	}
});

test("wiremock: JSON válido con los mismos datos", () => {
	for (const c of conns) {
		const w = JSON.parse(G.generate("wiremock", c));
		assert.equal(w.request.method, c.method);
		assert.deepEqual(typeof want(c) === "string" ? w.response.body : w.response.jsonBody, want(c));
	}
});
