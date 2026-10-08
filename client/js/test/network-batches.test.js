import { test } from "node:test";
import assert from "node:assert/strict";
import { TraceReports } from "../src/index.js";
import { limits } from "../src/transport.js";
import { fakeServer } from "./fake-server.js";
process.env.TRACEREPORTS_ENV_FILE = "off";

limits.maxBackoffMs = 100;
limits.circuitMs = 300;
for (const v of ["TRACEREPORTS_RUN_ID", "TRACEREPORTS_DISABLED", "TRACEREPORTS_TOKEN"]) delete process.env[v];

const SERVER_LIMIT = 48 << 20; // maxNetworkBody del servidor

async function sendNetwork(conns, opts) {
  const srv = await fakeServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url, flushTimeoutMs: 60000 });
    await cr.startRun("Suite");
    const t = await cr.startTest("Red");
    const res = t.network(conns, opts);
    t.finish("PASS");
    await cr.finishRun();
    const reqs = srv.requests.filter((r) => r.path.endsWith("/network"));
    return { res, sizes: reqs.map((r) => r.body.length), batches: reqs.map((r) => JSON.parse(r.body.toString("utf8")).connections), problems: cr.deliveryProblems() };
  } finally {
    await srv.close();
  }
}

test("los lotes predeterminados entran en el límite del servidor, en orden y sin duplicados", async () => {
  // 200 conexiones con bodies de 256 KB: ~52 MB en un solo lote antes del arreglo
  const conns = Array.from({ length: 200 }, (_, i) => ({ method: "GET", url: `https://app/api/${i}`, status: 200, response_body: "x".repeat(256 << 10) }));
  const { res, sizes, batches, problems } = await sendNetwork(conns);
  assert.ok(sizes.every((n) => n <= SERVER_LIMIT), `every request fits: ${sizes}`);
  assert.deepEqual(batches.flat().map((c) => c.url), conns.map((c) => c.url), "all, once, in order");
  assert.deepEqual(res, { stored: 200, errors: 0 });
  assert.equal(problems, 0);
});

test("se mide el JSON real: Unicode, escapes y headers", async () => {
  // cada carácter de control pesa 6 bytes escapado, cada emoji 4 bytes UTF-8 (2 unidades UTF-16)
  const body = "\u0001\"\\\n😀ñ".repeat(40000);
  const conns = Array.from({ length: 60 }, (_, i) => ({
    method: "POST", url: `https://app/api/${i}`, status: 500, response_body: body, post_data: "é".repeat(30000),
    request_headers: { "x-trace": "ü".repeat(4000) }, response_headers: { "content-type": "application/json" },
  }));
  const { sizes, batches } = await sendNetwork(conns);
  assert.ok(sizes.length > 1 && sizes.every((n) => n <= SERVER_LIMIT), `${sizes}`);
  const got = batches.flat();
  assert.deepEqual(got.map((c) => c.url), conns.map((c) => c.url));
  assert.equal(got[3].response_body, body.slice(0, 256 << 10), "values are preserved, not mangled by the split");
  assert.equal(got[3].post_data, conns[3].post_data);
  assert.equal(got[3].request_headers["x-trace"], conns[3].request_headers["x-trace"]);
});

test("una conexión que sola pasa el límite se envía igual, sin bodies y marcada como recortada", async () => {
  const huge = { method: "POST", url: "https://app/upload", status: 413, post_data: "p".repeat(60 << 20), request_headers: { big: "h".repeat(1 << 20) } };
  const small = { method: "GET", url: "https://app/ok", status: 200, response_body: "ok" };
  const { sizes, batches, res } = await sendNetwork([small, huge, small], { maxBodyKB: 256 });
  assert.ok(sizes.every((n) => n <= SERVER_LIMIT), `${sizes}`);
  const got = batches.flat();
  assert.deepEqual(got.map((c) => c.url), ["https://app/ok", "https://app/upload", "https://app/ok"]);
  assert.equal(got[1].post_data, "");
  assert.equal(got[1].body_truncated, true);
  assert.equal(got[1].status, 413, "the connection itself is kept");
  assert.equal(got[2].response_body, "ok");
  assert.deepEqual(res, { stored: 3, errors: 1 });
});
