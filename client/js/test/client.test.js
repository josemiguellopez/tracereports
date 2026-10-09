import { test } from "node:test";
import assert from "node:assert/strict";
import { TraceReports } from "../src/index.js";
import { limits } from "../src/transport.js";
import { withDriver } from "../src/selenium.js";
import { fakeServer } from "./fake-server.js";
// los tests corren dentro del repositorio: que el cliente no lea el .env real del proyecto
process.env.TRACEREPORTS_ENV_FILE = "off";
process.env.TRACEREPORTS_BIN_DOWNLOAD ??= "0"; // los tests nunca descargan el binario de GitHub

limits.maxBackoffMs = 100;
limits.circuitMs = 300;
for (const v of ["TRACEREPORTS_RUN_ID", "TRACEREPORTS_DISABLED", "TRACEREPORTS_TOKEN"]) delete process.env[v];

test("flujo completo: contexto, identidad, pasos, captura, red, DOM y cierre en orden", async () => {
  process.env.TRACEREPORTS_BRANCH = "feature/x";
  process.env.TRACEREPORTS_COMMIT = "abc123";
  const srv = await fakeServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url, token: "tok" });
    assert.equal(await cr.startRun("Suite", { environment: "qa", project: "shop", framework: "node" }), 7);
    const t = await cr.startTest("Login", { key: "login.test.js > ok", suite: "login.test.js", worker: 2 });
    t.info("Abrir login");
    t.screenshot(Buffer.from("PNGDATA"), "Formulario");
    t.screenshot(Buffer.from("SELENIUM").toString("base64"), "base64 de Selenium");
    t.expectResponse(401, { url: "*/auth/*" });
    const net = t.network([{ method: "POST", url: "https://app/api/auth/validate", status: 401 }, { method: "GET", url: "https://app/api/me", status: 500 }]);
    t.dom({ url: "https://app", elements: [] });
    t.finish("PASS", { attempts: 2 });
    await cr.finishRun();

    assert.deepEqual(net, { stored: 2, errors: 1 });
    const [run] = srv.json("/api/v1/runs");
    assert.deepEqual([run.project, run.branch, run.commit, run.framework], ["shop", "feature/x", "abc123", "node"]);
    const [tst] = srv.json("/api/v1/runs/7/tests");
    assert.deepEqual([tst.key, tst.suite, tst.worker], ["login.test.js > ok", "login.test.js", "2"]);
    const shots = srv.requests.filter((r) => r.path.endsWith("/screenshot")).map((r) => r.body.toString("latin1"));
    assert.ok(shots[0].includes("PNGDATA") && shots[1].includes("SELENIUM"), "Buffer and base64 screenshots");
    assert.deepEqual(srv.json("/api/v1/tests/11/network")[0].connections.map((c) => c.expected), [true, false]);
    assert.equal(srv.json("/api/v1/tests/11/finish")[0].attempts, 2);
    assert.equal(srv.paths().at(-1), "PATCH /api/v1/runs/7/finish", "the run closes after the queue is flushed");
    assert.ok(srv.requests.every((r) => r.auth === "Bearer tok" && r.key), "token and Idempotency-Key on every call");
    assert.equal(cr.deliveryProblems(), 0);
  } finally {
    delete process.env.TRACEREPORTS_BRANCH;
    delete process.env.TRACEREPORTS_COMMIT;
    await srv.close();
  }
});

test("los 503 se reintentan con la misma Idempotency-Key y sin perder el orden", async () => {
  const srv = await fakeServer({ failFirst: 2 });
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    cr.runId = 7;
    const t = await cr.startTest("x"); // se crea al tercer intento
    assert.ok(t.active);
    const created = srv.requests.filter((r) => r.path.endsWith("/tests"));
    assert.equal(created.length, 1);
    srv.failNext(3);
    for (let i = 0; i < 15; i++) t.info(`paso ${i}`);
    assert.equal(await cr.flush(10_000), 0);
    const logs = srv.json("/api/v1/tests/11/logs").map((l) => l.message);
    assert.deepEqual(logs, Array.from({ length: 15 }, (_, i) => `paso ${i}`));
    assert.ok(cr.delivery.retried >= 3);
  } finally {
    await srv.close();
  }
});

test("con el servidor caído nada se rompe ni se bloquea, y se informa", async () => {
  // sin grabación local (offline: "off"); la grabación tiene sus propios tests en offline.test.js
  const cr = new TraceReports({ baseUrl: "http://127.0.0.1:9", timeoutMs: 300, flushTimeoutMs: 300, offline: "off" });
  const t0 = Date.now();
  assert.equal(await cr.startRun("x"), null);
  const t = await cr.startTest("y");
  t.info("no-op");
  t.finish("PASS");
  await cr.finishRun();
  assert.equal(t.active, false);
  assert.ok(Date.now() - t0 < 3000, "fails fast");
  assert.ok(cr.deliveryProblems() === 0 && !cr.runId);

  const cr2 = new TraceReports({ baseUrl: "http://127.0.0.1:9", timeoutMs: 200, flushTimeoutMs: 200, maxQueueItems: 3, offline: "off" });
  cr2.runId = 1;
  const t2 = new (await import("../src/index.js")).TraceTest(cr2, 5);
  for (let i = 0; i < 6; i++) t2.info(`paso ${i}`);
  await cr2.finishRun();
  const d = cr2.delivery;
  assert.ok(d.dropped >= 3 && d.lost >= 1, JSON.stringify(d));
});

test("cr.test + withDriver: al fallar adjunta captura y DOM y relanza el error", async () => {
  const srv = await fakeServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("Selenium");
    const driver = {
      takeScreenshot: async () => Buffer.from("SHOT").toString("base64"),
      executeScript: async () => ({ url: "https://app", elements: [{ tag: "button" }] }),
    };
    await assert.rejects(cr.test("Falla", withDriver(driver, { key: "k" }), async (t) => {
      t.info("antes");
      throw new Error("no encontré el botón");
    }), /no encontré el botón/);
    await cr.finishRun();
    assert.ok(srv.paths().includes("POST /api/v1/tests/11/screenshot"));
    assert.equal(srv.json("/api/v1/tests/11/dom")[0].elements[0].tag, "button");
    const fin = srv.json("/api/v1/tests/11/finish")[0];
    assert.equal(fin.status, "FAIL");
    assert.match(fin.error_message, /Error: no encontré el botón/);
  } finally {
    await srv.close();
  }
});

test("un cierre de ejecución rechazado o agotado se informa; uno recuperado no deja error", async () => {
  // evidencia OK, pero el cierre siempre responde 503
  let srv = await fakeServer({ failPaths: ["/runs/7/finish"] });
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("x");
    (await cr.startTest("t")).finish("PASS");
    await cr.finishRun();
    assert.equal(cr.delivery.runNotClosed, 1);
    assert.ok(cr.deliveryProblems() >= 1, "an unconfirmed close is a delivery problem");
  } finally {
    await srv.close();
  }
  // el cierre falla dos veces y después pasa: sin error falso, misma Idempotency-Key
  srv = await fakeServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("x");
    await cr.flush();
    srv.failNext(2);
    await cr.finishRun();
    assert.equal(cr.delivery.runNotClosed, 0);
    assert.equal(cr.deliveryProblems(), 0);
    assert.equal(srv.paths().at(-1), "PATCH /api/v1/runs/7/finish");
  } finally {
    await srv.close();
  }
  // un cliente unido a una ejecución ajena no la cierra
  srv = await fakeServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    cr.joinRun(55);
    await cr.finishRun();
    assert.ok(!srv.paths().some((p) => p.endsWith("/finish")) && cr.deliveryProblems() === 0);
  } finally {
    await srv.close();
  }
});

test("configuración: TRACEREPORTS_URL y TRACEREPORTS_TOKEN del entorno", async () => {
  const mod = await import("../src/index.js");
  process.env.TRACEREPORTS_URL = "http://new:2";
  process.env.TRACEREPORTS_TOKEN = "tok";
  try {
    const cr = new mod.TraceReports();
    assert.equal(cr.baseUrl, "http://new:2");
    assert.equal(cr.token, "tok");
  } finally {
    delete process.env.TRACEREPORTS_URL;
    delete process.env.TRACEREPORTS_TOKEN;
  }
});
