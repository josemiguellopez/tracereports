import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import TraceReportsReporter from "../src/reporter.js";
import { limits } from "../src/transport.js";
import { fakeServer } from "./fake-server.js";
// los tests corren dentro del repositorio: que el cliente no lea el .env real del proyecto
process.env.TRACEREPORTS_ENV_FILE = "off";

limits.maxBackoffMs = 100;

// Objetos con la forma de los que Playwright Test le pasa a un reporter.
const root = path.resolve("/proj");
function fakeTest(id, title, { retries = 0, file = "tests/login.spec.js" } = {}) {
  return {
    id, title, retries, tags: ["@smoke"], annotations: [],
    location: { file: path.join(root, file) },
    titlePath: () => ["", "chromium", file, "Login", title],
    parent: { project: () => ({ name: "chromium" }) },
  };
}

test("reporter: una ejecución, identidad, reintentos, pasos, capturas, red y cierre", async () => {
  const srv = await fakeServer();
  try {
    const rep = new TraceReportsReporter({ url: srv.url, runName: "Regresión", project: "shop" });
    rep.onBegin({ rootDir: root, projects: [{ name: "chromium" }] }, { allTests: () => [1, 2, 3] });

    // flaky: falla el intento 1 y pasa el 2
    const flaky = fakeTest("t1", "pasa al reintentar", { retries: 1 });
    rep.onTestBegin(flaky, { retry: 0, workerIndex: 0 });
    rep.onStepEnd(flaky, {}, { category: "test.step", title: "Abrir el login" });
    rep.onTestEnd(flaky, { status: "failed", retry: 0, errors: [{ message: "\u001b[31mTimeout 5000ms\u001b[39m" }], attachments: [] });
    rep.onTestBegin(flaky, { retry: 1, workerIndex: 1 });
    rep.onTestEnd(flaky, { status: "passed", retry: 1, attachments: [] });

    // falla con captura, red y DOM adjuntos por los fixtures
    const broken = fakeTest("t2", "dashboard visible");
    broken.annotations.push({ type: "tracereports-expect", description: JSON.stringify({ status: 401, url: "*/auth/*", method: "" }) });
    rep.onTestBegin(broken, { retry: 0, workerIndex: 0 });
    rep.onTestEnd(broken, {
      status: "failed", retry: 0,
      errors: [{ message: "expect(locator).toBeVisible() failed", stack: "Error: expect(locator).toBeVisible() failed\n  at login.spec.js:12" }],
      attachments: [
        { name: "screenshot", contentType: "image/png", body: Buffer.from("PNGBYTES") },
        { name: "tracereports-network", contentType: "application/json", body: Buffer.from(JSON.stringify([
          { method: "POST", url: "https://app/web/auth/validate", status: 401 }, { method: "GET", url: "https://app/api/x", status: 500 }])) },
        { name: "tracereports-dom", contentType: "application/json", body: Buffer.from(JSON.stringify({ url: "u", elements: [] })) },
      ],
    });

    const skipped = fakeTest("t3", "solo en prod");
    rep.onTestBegin(skipped, { retry: 0, workerIndex: 0 });
    rep.onTestEnd(skipped, { status: "skipped", retry: 0, attachments: [], annotations: [{ type: "skip", description: "no aplica" }] });

    assert.equal(await rep.onEnd({ status: "failed" }), undefined);

    const runs = srv.json("/api/v1/runs");
    assert.equal(runs.length, 1);
    assert.deepEqual([runs[0].name, runs[0].project, runs[0].framework, runs[0].environment], ["Regresión", "shop", "playwright", "chromium"]);
    const tests = srv.json("/api/v1/runs/7/tests");
    assert.equal(tests.length, 3, "a retry does not create another test");
    assert.equal(tests[0].key, "tests/login.spec.js > Login > pasa al reintentar [chromium]");
    assert.equal(tests[0].name, "Login › pasa al reintentar");
    assert.match(tests[0].category, /smoke/);

    const finishes = srv.json("/api/v1/tests/11/finish"); // el servidor falso da id 11 a todos
    assert.deepEqual(finishes.map((f) => f.status), ["PASS", "FAIL", "SKIP"]);
    assert.equal(finishes[0].attempts, 2, "passed after retry");
    assert.equal(finishes[1].error_message, "expect(locator).toBeVisible() failed");
    const logs = srv.json("/api/v1/tests/11/logs").map((l) => `${l.status} ${l.message}`);
    assert.ok(logs.includes("INFO Abrir el login") && logs.includes("FAIL Timeout 5000ms"), logs.join(" | "));
    assert.ok(logs.some((l) => l.startsWith("WARNING Reintento 2")), "retry step");
    assert.ok(srv.requests.some((r) => r.path.endsWith("/screenshot") && r.body.includes("PNGBYTES")));
    assert.deepEqual(srv.json("/api/v1/tests/11/network")[0].connections.map((c) => c.expected), [true, false]);
    assert.equal(srv.paths().filter((p) => p.endsWith("/dom")).length, 1);
    assert.equal(srv.paths().at(-1), "PATCH /api/v1/runs/7/finish");
  } finally {
    await srv.close();
  }
});

test("reporter: TRACEREPORTS_STRICT hace fallar la corrida si la evidencia no llegó", async () => {
  limits.circuitMs = 200;
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  const offlineDir = fs.mkdtempSync(path.join(os.tmpdir(), "tr-strict-"));
  const rep = new TraceReportsReporter({ url: "http://127.0.0.1:9", strict: true, offlineDir });
  rep.cr.timeoutMs = 200;
  rep.onBegin({ rootDir: root, projects: [] }, { allTests: () => [] });
  assert.deepEqual(await rep.onEnd({ status: "passed" }), { status: "failed" });
});

test("reporter: un cierre no confirmado se informa y TRACEREPORTS_STRICT falla la corrida", async () => {
  const srv = await fakeServer({ failPaths: ["/runs/7/finish"] });
  try {
    const rep = new TraceReportsReporter({ url: srv.url, strict: true });
    rep.onBegin({ rootDir: root, projects: [] }, { allTests: () => [] });
    assert.deepEqual(await rep.onEnd({ status: "passed" }), { status: "failed" });
    assert.equal(rep.cr.delivery.runNotClosed, 1);
  } finally {
    await srv.close();
  }
});

test("reporter: lee los adjuntos y anotaciones tracereports-* del test", async () => {
  const srv = await fakeServer();
  try {
    const rep = new TraceReportsReporter({ url: srv.url });
    rep.onBegin({ rootDir: root, projects: [] }, { allTests: () => [1] });
    const t = fakeTest("t1", "anotado");
    t.annotations.push({ type: "tracereports-expect", description: JSON.stringify({ status: 401, url: "/auth", method: "" }) });
    t.annotations.push({ type: "tracereports-log", description: JSON.stringify({ status: "INFO", message: "paso anotado", timestamp: 1 }) });
    rep.onTestBegin(t, { retry: 0, workerIndex: 0 });
    rep.onTestEnd(t, { status: "passed", retry: 0, attachments: [
      { name: "tracereports-network", contentType: "application/json", body: Buffer.from(JSON.stringify([{ method: "POST", url: "https://a/auth", status: 401 }])) },
      { name: "tracereports-dom", contentType: "application/json", body: Buffer.from(JSON.stringify({ url: "u", elements: [] })) },
    ] });
    await rep.onEnd({ status: "passed" });
    assert.equal(srv.json("/api/v1/tests/11/network")[0].connections[0].expected, true);
    assert.equal(srv.paths().filter((p) => p.endsWith("/dom")).length, 1);
    assert.ok(srv.json("/api/v1/tests/11/logs").some((l) => l.message === "paso anotado"));
  } finally {
    await srv.close();
  }
});
