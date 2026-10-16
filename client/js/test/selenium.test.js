// Selenium: la red y la consola salen del log de rendimiento de Chrome (sin navegador aquí).
import { test } from "node:test";
import assert from "node:assert/strict";
import { TraceReports } from "../src/index.js";
import { limits } from "../src/transport.js";
import { captureConsole, captureNetwork, connectionsFromLog, seleniumOptions, withDriver } from "../src/selenium.js";
import { fakeServer } from "./fake-server.js";

process.env.TRACEREPORTS_ENV_FILE = "off";
process.env.TRACEREPORTS_BIN_DOWNLOAD ??= "0";
limits.maxBackoffMs = 100;
for (const v of ["TRACEREPORTS_RUN_ID", "TRACEREPORTS_DISABLED", "TRACEREPORTS_TOKEN"]) delete process.env[v];

/** Una entrada del log "performance", como la entrega selenium-webdriver. */
const ev = (method, params) => ({ level: { name: "INFO" }, timestamp: 0, type: "performance", message: JSON.stringify({ message: { method, params }, webview: "x" }) });

function checkoutLog() {
  return [
    ev("Network.requestWillBeSent", { requestId: "1", type: "Document", wallTime: 1790000000, timestamp: 10,
      request: { url: "https://shop.test/cart", method: "GET", headers: {} } }),
    ev("Network.responseReceived", { requestId: "1", type: "Document", response: { status: 200, statusText: "OK", mimeType: "text/html", headers: {} } }),
    ev("Network.loadingFinished", { requestId: "1", timestamp: 10.2 }),
    ev("Network.requestWillBeSent", { requestId: "2", type: "Fetch", wallTime: 1790000001, timestamp: 11,
      request: { url: "https://shop.test/api/orders", method: "POST", headers: { "Content-Type": "application/json" }, hasPostData: true } }),
    ev("Network.requestWillBeSentExtraInfo", { requestId: "2", headers: { Authorization: "Bearer abc", Cookie: "sid=1" } }),
    ev("Network.responseReceived", { requestId: "2", type: "Fetch",
      response: { status: 500, statusText: "Internal Server Error", mimeType: "application/json", headers: { "content-type": "application/json" } } }),
    ev("Network.loadingFinished", { requestId: "2", timestamp: 11.35 }),
    ev("Network.requestWillBeSent", { requestId: "3", type: "Document", wallTime: 1790000002, timestamp: 12, request: { url: "https://shop.test/old", method: "GET", headers: {} } }),
    ev("Network.requestWillBeSent", { requestId: "3", type: "Document", wallTime: 1790000002.1, timestamp: 12.1,
      request: { url: "https://shop.test/new", method: "GET", headers: {} }, redirectResponse: { status: 302, statusText: "Found", headers: { location: "/new" } } }),
    ev("Network.responseReceived", { requestId: "3", type: "Document", response: { status: 200, mimeType: "text/html", headers: {} } }),
    ev("Network.requestWillBeSent", { requestId: "4", type: "XHR", wallTime: 1790000003, timestamp: 13, request: { url: "https://down.test/api", method: "GET", headers: {} } }),
    ev("Network.loadingFailed", { requestId: "4", timestamp: 13.5, errorText: "net::ERR_CONNECTION_REFUSED" }),
    ev("Network.requestWillBeSent", { requestId: "5", type: "Image", wallTime: 1790000004, timestamp: 14, request: { url: "data:image/png;base64,AA", method: "GET", headers: {} } }),
    ev("Page.frameNavigated", { frame: {} }),
  ];
}

/** Un driver de selenium-webdriver mínimo: el log se vacía al leerlo, como el real. */
function fakeDriver({ perf = [], browser = [] } = {}) {
  const logs = { performance: perf, browser };
  const calls = [];
  return {
    logs, calls,
    manage: () => ({ logs: () => ({ get: async (kind) => { const out = logs[kind] || []; logs[kind] = []; return out; } }) }),
    sendAndGetDevToolsCommand: async (cmd, params) => {
      calls.push(cmd);
      if (cmd === "Network.getRequestPostData" && params.requestId === "2") return { postData: '{"items":[1]}' };
      if (cmd === "Network.getResponseBody" && params.requestId === "2") {
        return { body: Buffer.from('{"error":"payments: connection pool exhausted"}').toString("base64"), base64Encoded: true };
      }
      throw new Error("No resource with given identifier found");
    },
    takeScreenshot: async () => Buffer.from("SHOT").toString("base64"),
    executeScript: async () => ({ url: "https://shop.test/cart", elements: [{ tag: "button", id: "pay" }] }),
  };
}

test("connectionsFromLog: requests, the real headers, redirects and failures", () => {
  const conns = connectionsFromLog(checkoutLog());
  assert.deepEqual(conns.map((c) => [c.method, c.url, c.status]), [
    ["GET", "https://shop.test/cart", 200],
    ["POST", "https://shop.test/api/orders", 500],
    ["GET", "https://shop.test/old", 302],
    ["GET", "https://shop.test/new", 200],
    ["GET", "https://down.test/api", 0],
  ]);
  const order = conns[1];
  assert.equal(order.resource_type, "fetch");
  assert.equal(order.duration_ms, 350);
  assert.equal(order.started_at, 1790000001000);
  assert.equal(order.request_headers.Cookie, "sid=1");
  assert.equal(conns[2].response_headers.location, "/new");
  assert.equal(conns[4].failed, true);
  assert.equal(conns[4].error_text, "net::ERR_CONNECTION_REFUSED");
});

test("captureNetwork: bodies of the API and the errors, post data, and the log is consumed", async () => {
  const driver = fakeDriver({ perf: checkoutLog() });
  const conns = await captureNetwork(driver);
  const order = conns.find((c) => c.method === "POST");
  assert.match(order.response_body, /connection pool exhausted/);
  assert.equal(order.post_data, '{"items":[1]}');
  assert.deepEqual(driver.calls, ["Network.getRequestPostData", "Network.getResponseBody"]);
  assert.ok(conns.every((c) => !("_requestId" in c) && !("_hasPostData" in c)));
  assert.deepEqual(await captureNetwork(driver), []);
});

test("captureConsole: errors, uncaught errors and warnings", async () => {
  const driver = fakeDriver({ browser: [
    { level: { name: "SEVERE" }, message: "https://shop.test/api/orders - Failed to load resource: 500", timestamp: 5 },
    { level: { name: "SEVERE" }, message: "https://shop.test/cart 10:3 Uncaught TypeError: o.order is undefined", timestamp: 6 },
    { level: { name: "WARNING" }, message: "deprecated", timestamp: 7 },
    { level: { name: "INFO" }, message: "hello", timestamp: 8 },
  ] });
  assert.deepEqual((await captureConsole(driver)).map((e) => e.level), ["error", "pageerror", "warning"]);
});

test("seleniumOptions turns on the performance and browser logs", () => {
  class Options { constructor() { this.caps = {}; } set(k, v) { this.caps[k] = v; } get(k) { return this.caps[k]; } }
  assert.deepEqual(seleniumOptions(new Options()).caps, { "goog:loggingPrefs": { performance: "ALL", browser: "ALL" } });
  class EdgeOptions extends Options {}
  assert.ok("ms:loggingPrefs" in seleniumOptions(new EdgeOptions()).caps);
});

test("withDriver: network and console of the test (only its own), and on failure screenshot and DOM", async () => {
  const srv = await fakeServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("Selenium");
    const driver = fakeDriver({ perf: [ev("Network.requestWillBeSent", { requestId: "0", type: "Document", request: { url: "https://before.test/", method: "GET" } })] });
    await assert.rejects(cr.test("Checkout", withDriver(driver, { key: "k" }), async () => {
      driver.logs.performance = checkoutLog(); // lo que pasa durante el test
      driver.logs.browser = [{ level: { name: "SEVERE" }, message: "Uncaught TypeError: x", timestamp: 1 }];
      throw new Error("order not confirmed");
    }), /order not confirmed/);
    await cr.finishRun();
    const net = srv.json("/api/v1/tests/11/network").flatMap((b) => b.connections);
    assert.equal(net.length, 5);
    assert.ok(net.some((c) => c.status === 500 && /pool exhausted/.test(c.response_body)));
    assert.equal(srv.json("/api/v1/tests/11/console")[0].entries[0].level, "pageerror");
    assert.ok(srv.paths().includes("POST /api/v1/tests/11/screenshot"));
    assert.equal(srv.json("/api/v1/tests/11/dom")[0].elements[0].id, "pay");
    // la red se envía antes de cerrar el test
    const paths = srv.paths();
    assert.ok(paths.indexOf("POST /api/v1/tests/11/network") < paths.indexOf("PATCH /api/v1/tests/11/finish"));
  } finally {
    await srv.close();
  }
});
