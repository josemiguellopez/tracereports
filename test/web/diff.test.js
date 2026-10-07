// Tests de la comparación de una llamada con la última vez que el test pasó (diffCalls, jsonDiff).
const test = require("node:test");
const assert = require("node:assert/strict");
const { load } = require("./load.js");

const { diffCalls, jsonDiff } = load(["tracereports_features.js"]).TraceReportsFeatures;
const plain = (v) => JSON.parse(JSON.stringify(v));

test("jsonDiff: agregados, quitados y cambiados, con su ruta", () => {
	const before = { order: { id: 1, total: 100, items: [{ sku: "A" }, { sku: "B" }] }, coupon: "X" };
	const after = { order: { id: 1, total: 120, items: [{ sku: "A" }], currency: "CLP" } };
	assert.deepEqual(plain(jsonDiff(before, after)), [
		{ path: "order.total", kind: "changed", before: 100, after: 120 },
		{ path: "order.items[1]", kind: "removed", before: { sku: "B" } },
		{ path: "order.currency", kind: "added", after: "CLP" },
		{ path: "coupon", kind: "removed", before: "X" },
	]);
	assert.deepEqual(plain(jsonDiff({ a: 1 }, { a: 1 })), []);
	assert.deepEqual(plain(jsonDiff([1], { 0: 1 })), [{ path: "(raíz)", kind: "changed", before: [1], after: { 0: 1 } }]);
	assert.deepEqual(plain(jsonDiff(null, 3)), [{ path: "(raíz)", kind: "changed", before: null, after: 3 }]);
});

test("jsonDiff: se corta en 60 diferencias", () => {
	const a = {}, b = {};
	for (let i = 0; i < 200; i++) { a["k" + i] = i; b["k" + i] = -i; }
	assert.equal(jsonDiff(a, b).length, 60);
});

test("diffCalls: status, headers sin los volátiles y bodies", () => {
	const then = { status: 200, duration_ms: 80, response_headers: { "Content-Type": "application/json", Date: "ayer", "X-Version": "1.4" },
		response_body: '{"ok":true,"price":10}', post_data: '{"qty":1}' };
	const now = { status: 500, duration_ms: 3000, response_headers: { "content-type": "application/json", date: "hoy", "x-version": "1.5" },
		response_body: '{"ok":false,"error":"db"}', post_data: '{"qty":1}' };
	const d = plain(diffCalls(now, then));
	assert.deepEqual(d.status, { before: "200", after: "500", changed: true });
	assert.deepEqual(d.durationMs, { before: 80, after: 3000 });
	assert.deepEqual(d.headers, [{ name: "x-version", before: "1.4", after: "1.5" }]); // date es volátil
	assert.deepEqual(d.response.json.map((x) => `${x.kind} ${x.path}`), ["changed ok", "removed price", "added error"]);
	assert.equal(d.request, null); // el request no cambió
});

test("diffCalls: sin respuesta y bodies que no son JSON", () => {
	const d = plain(diffCalls({ failed: true, error_text: "net::ERR_TIMED_OUT", response_body: "" }, { status: 200, response_body: "<html>ok</html>" }));
	assert.deepEqual(d.status, { before: "200", after: "sin respuesta (net::ERR_TIMED_OUT)", changed: true });
	assert.deepEqual(d.response, { text: { before: "<html>ok</html>", after: "" } });
	assert.equal(plain(diffCalls({ status: 200 }, { status: 200 })).status.changed, false);
});
