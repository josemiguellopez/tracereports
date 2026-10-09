// Integración con Playwright Test real: el reporter debe respetar la semántica del runner
// (test.fail(), skip, timeout, reintentos). Usa el Playwright instalado en examples/playwright-js
// y tests que no abren navegador. Se omite si ese ejemplo no tiene node_modules.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { fakeServer } from "./fake-server.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const example = path.resolve(here, "../../../examples/playwright-js");
const cli = path.join(example, "node_modules/@playwright/test/cli.js");
const reporter = path.resolve(here, "../src/reporter.js");

const SPEC = `
import { test, expect } from "@playwright/test";
// los tests corren dentro del repositorio: que el cliente no lea el .env real del proyecto
process.env.TRACEREPORTS_ENV_FILE = "off";
process.env.TRACEREPORTS_BIN_DOWNLOAD ??= "0"; // los tests nunca descargan el binario de GitHub
test("normal pass", () => { expect(1).toBe(1); });
test("normal fail", () => { expect(1).toBe(2); });
test("expected failure", () => { test.fail(); expect(1).toBe(2); });
test("unexpected pass", () => { test.fail(); expect(1).toBe(1); });
test("skip", () => { test.skip(true, "no aplica"); });
test("timeout", async () => { test.setTimeout(300); await new Promise((r) => setTimeout(r, 3000)); });
test("flaky", ({}, testInfo) => { expect(testInfo.retry).toBe(1); });
test("step under expected failure", async () => {
  test.fail();
  await test.step("paso que falla", async () => { expect(1).toBe(2); });
});
`;

function run(cwd, env) {
  return new Promise((resolve) => {
    const p = spawn(process.execPath, [cli, "test", "-c", path.join(cwd, "playwright.config.mjs")], { cwd, env: { ...process.env, ...env } });
    let out = "";
    p.stdout.on("data", (d) => (out += d));
    p.stderr.on("data", (d) => (out += d));
    p.on("close", (code) => resolve({ code, out }));
  });
}

test("Playwright real: fallo esperado, pase inesperado, skip, timeout y reintentos", { skip: !fs.existsSync(cli) && "sin node_modules en examples/playwright-js" }, async () => {
  const srv = await fakeServer({ distinctIds: true });
  const dir = fs.mkdtempSync(path.join(example, ".tracereports-int-"));
  try {
    fs.writeFileSync(path.join(dir, "outcomes.spec.mjs"), SPEC);
    fs.writeFileSync(path.join(dir, "playwright.config.mjs"),
      `export default { testDir: ".", retries: 1, workers: 1, reporter: [[${JSON.stringify(reporter)}, { runName: "int" }]] };`);
    const { out } = await run(dir, { TRACEREPORTS_URL: srv.url, TRACEREPORTS_RUN_ID: "" });

    const created = srv.json("/api/v1/runs/7/tests").map((t, i) => ({ name: t.name, id: 100 + i }));
    const byName = {};
    for (const { name, id } of created) {
      const fin = srv.json(`/api/v1/tests/${id}/finish`)[0];
      const logs = srv.json(`/api/v1/tests/${id}/logs`).map((l) => `${l.status} ${l.message}`);
      byName[name] = { status: fin?.status, attempts: fin?.attempts ?? 1, message: fin?.error_message, logs };
    }
    const outcome = (n) => [byName[n]?.status, byName[n]?.attempts];
    assert.deepEqual(outcome("normal pass"), ["PASS", 1], out);
    assert.deepEqual(outcome("normal fail"), ["FAIL", 2]);
    assert.deepEqual(outcome("expected failure"), ["PASS", 1], "Playwright considers it fulfilled");
    assert.ok(byName["expected failure"].logs.some((l) => l.startsWith("INFO Falló como se esperaba")));
    assert.ok(!byName["expected failure"].logs.some((l) => l.startsWith("FAIL")), "an expected error is not shown as a failure");
    assert.deepEqual(outcome("unexpected pass"), ["FAIL", 2], "expected to fail, but passed");
    assert.match(byName["unexpected pass"].message, /Se esperaba que fallara/);
    assert.deepEqual(outcome("skip"), ["SKIP", 1]);
    assert.deepEqual(outcome("timeout"), ["FAIL", 2]);
    assert.deepEqual(outcome("flaky"), ["PASS", 2], "passed after retry");
    assert.equal(byName["step under expected failure"].status, "PASS");
    assert.ok(byName["step under expected failure"].logs.some((l) => l.startsWith("WARNING paso que falla") && l.includes("error esperado")),
      byName["step under expected failure"].logs.join(" | "));
    assert.equal(srv.paths().at(-1), "PATCH /api/v1/runs/7/finish");
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
    await srv.close();
  }
});

const BROWSER_SPEC = (fixtures) => `
import { test, expect } from ${JSON.stringify(fixtures)};
process.env.TRACEREPORTS_ENV_FILE = "off";
test("consola del navegador", async ({ page }) => {
  await page.setContent("<script>console.log('ruido'); console.warn('API deprecada'); console.error('Fallo al cargar /api/cart: 500');" +
    "setTimeout(() => { throw new TypeError('reading total of undefined'); }, 0);</script>");
  await page.waitForTimeout(300);
  expect(1).toBe(2);
});
`;

test("Playwright real con navegador: el fixture captura la consola y el reporter la sube", { skip: !fs.existsSync(cli) && "sin node_modules en examples/playwright-js" }, async (t) => {
  const srv = await fakeServer({ distinctIds: true });
  const dir = fs.mkdtempSync(path.join(example, ".tracereports-int-"));
  try {
    const fixtures = pathToFileURL(path.resolve(here, "../src/playwright.js")).href;
    fs.writeFileSync(path.join(dir, "console.spec.mjs"), BROWSER_SPEC(fixtures));
    fs.writeFileSync(path.join(dir, "playwright.config.mjs"),
      `export default { testDir: ".", workers: 1, reporter: [[${JSON.stringify(reporter)}, { runName: "consola" }]] };`);
    const { out } = await run(dir, { TRACEREPORTS_URL: srv.url, TRACEREPORTS_RUN_ID: "" });
    if (/Executable doesn.t exist|browserType.launch/.test(out)) return t.skip("sin Chromium para esta versión de Playwright");
    const sent = srv.json("/api/v1/tests/100/console");
    assert.equal(sent.length, 1, out);
    const levels = sent[0].entries.map((e) => e.level);
    assert.deepEqual(levels, ["warning", "error", "pageerror"], out);
    assert.ok(sent[0].entries[2].text.includes("reading total of undefined"));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
    await srv.close();
  }
});
