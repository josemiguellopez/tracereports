// El indicador body_truncated y el tamaño original (bytes UTF-8, como el servidor) se conservan
// desde la captura hasta lo guardado.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { TraceReports } from "../src/index.js";
import { fakeServer } from "./fake-server.js";

process.env.TRACEREPORTS_ENV_FILE = "off";
for (const v of ["TRACEREPORTS_RUN_ID", "TRACEREPORTS_DISABLED", "TRACEREPORTS_TOKEN"]) delete process.env[v];

async function sent(conns, opts) {
  const srv = await fakeServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("s");
    const t = await cr.startTest("t");
    t.network(conns, opts);
    t.finish("PASS");
    await cr.finishRun();
    return srv.json("/api/v1/tests/11/network").flatMap((b) => b.connections);
  } finally {
    await srv.close();
  }
}

test("un body ya recortado por la captura sigue marcado, con su tamaño original", async () => {
  const body = "x".repeat(256 << 10);
  const [c] = await sent([{ method: "GET", url: "https://app/api", status: 200, response_body: body, body_size: body.length + 1000, body_truncated: true }]);
  assert.equal(c.body_truncated, true);
  assert.equal(c.body_size, body.length + 1000);
  assert.equal(c.response_body.length, body.length);
});

test("un indicador explícito se respeta aunque el body quepa", async () => {
  const [c] = await sent([{ method: "GET", url: "https://app/api", status: 200, response_body: "{\"a\":", body_truncated: true }]);
  assert.equal(c.body_truncated, true);
});

test("un body completo que cabe no se marca; su tamaño son bytes UTF-8", async () => {
  const body = "ñ😀".repeat(1000); // 2000 caracteres, 3000 unidades UTF-16, 6000 bytes
  const [c] = await sent([{ method: "GET", url: "https://app/api", status: 200, response_body: body }]);
  assert.equal(c.body_truncated, false);
  assert.equal(c.body_size, Buffer.byteLength(body));
  assert.equal(c.response_body, body);
});

test("el SDK recorta y lo marca, sin partir un emoji", async () => {
  const body = "😀".repeat(200000); // 400.000 unidades UTF-16
  const [c] = await sent([{ method: "GET", url: "https://app/api", status: 200, response_body: body }]);
  assert.equal(c.body_truncated, true);
  assert.equal(c.body_size, Buffer.byteLength(body));
  assert.ok(c.response_body.length <= 256 << 10 && !/[\uD800-\uDBFF]$/.test(c.response_body));
});

// ---- recorrido completo: captura de Playwright real -> reporter -> servidor real -> lo guardado
const here = path.dirname(fileURLToPath(import.meta.url));
const example = path.resolve(here, "../../../examples/playwright-js");
const cli = path.join(example, "node_modules/@playwright/test/cli.js");
const reporter = path.resolve(here, "../src/reporter.js");
const binary = process.env.TRACEREPORTS_BIN;

const freePort = () => new Promise((resolve) => {
  const s = net.createServer().listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => resolve(p)); });
});

async function realServer() {
  const port = await freePort();
  const data = fs.mkdtempSync(path.join(os.tmpdir(), "tr-body-flag-"));
  const env = { ...process.env, PORT: String(port), DATA_DIR: data, TRACEREPORTS_ENV_FILE: path.join(data, "no.env"), TRACEREPORTS_TOKEN: "",
    TRACEREPORTS_INGEST_TOKEN: "", TRACEREPORTS_UI_USER: "", TRACEREPORTS_UI_PASSWORD: "", AI_PROVIDER: "", TEAMS_WEBHOOK_URL: "", SLACK_WEBHOOK_URL: "" };
  const proc = spawn(binary, [], { env, stdio: "ignore" });
  const url = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 100; i++) {
    try { if ((await fetch(url + "/api/v1/runs")).ok) break; } catch { /* arrancando */ }
    await new Promise((r) => setTimeout(r, 100));
  }
  return { url, stop: () => { proc.kill(); try { fs.rmSync(data, { recursive: true, force: true }); } catch { /* en uso */ } } };
}

const BIG = "ñ".repeat(150000) + "x".repeat(150000); // 300.000 caracteres, 450.000 bytes

test("Playwright -> SDK -> servidor: el recorte de la captura queda guardado como recortado",
  { skip: (!fs.existsSync(cli) && "sin node_modules en examples/playwright-js") || (!binary && "needs $TRACEREPORTS_BIN") }, async (t) => {
    const srv = await realServer();
    const dir = fs.mkdtempSync(path.join(example, ".tracereports-body-"));
    try {
      const fixtures = pathToFileURL(path.resolve(here, "../src/playwright.js")).href;
      fs.writeFileSync(path.join(dir, "body.spec.mjs"), `
import { test } from ${JSON.stringify(fixtures)};
process.env.TRACEREPORTS_ENV_FILE = "off";
test("body grande", async ({ page }) => {
  await page.route("http://app.test/**", (route) => route.request().url().includes("/api/")
    ? route.fulfill({ status: 200, contentType: "application/json; charset=utf-8", body: ${JSON.stringify(JSON.stringify(BIG))} })
    : route.fulfill({ status: 200, contentType: "text/html", body: "<script>fetch('/api/big')</script>" }));
  await page.goto("http://app.test/");
  await page.waitForResponse("**/api/big");
});`);
      fs.writeFileSync(path.join(dir, "playwright.config.mjs"),
        `export default { testDir: ".", workers: 1, reporter: [[${JSON.stringify(reporter)}, { runName: "body" }]] };`);
      const out = await new Promise((resolve) => {
        const p = spawn(process.execPath, [cli, "test", "-c", path.join(dir, "playwright.config.mjs")],
          { cwd: dir, env: { ...process.env, TRACEREPORTS_URL: srv.url, TRACEREPORTS_RUN_ID: "" } });
        let o = "";
        p.stdout.on("data", (d) => (o += d));
        p.stderr.on("data", (d) => (o += d));
        p.on("close", () => resolve(o));
      });
      if (/Executable doesn.t exist|browserType.launch/.test(out)) return t.skip("sin Chromium para esta versión de Playwright");
      const conns = await (await fetch(srv.url + "/api/v1/tests/1/network")).json();
      const list = Array.isArray(conns) ? conns : conns.connections;
      const c = list.find((x) => x.url.endsWith("/api/big"));
      assert.ok(c, out);
      const full = JSON.stringify(BIG);
      assert.equal(c.body_truncated, true, "the capture cut it");
      assert.equal(c.body_size, Buffer.byteLength(full), "original size, in bytes");
      assert.ok(c.response_body.length < full.length);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
      srv.stop();
    }
  });
