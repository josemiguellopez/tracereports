// Capturas con los datos sensibles tapados (mask.js) y política TRACEREPORTS_SCREENSHOTS.
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { MASK_COLOR, MASKED_SHOT, maskSelector, playwrightScreenshot, screenshotAllowed, seleniumScreenshot } from "../src/mask.js";
import { TraceReports } from "../src/index.js";
import TraceReportsReporter from "../src/reporter.js";
import { limits } from "../src/transport.js";
import { fakeServer } from "./fake-server.js";

process.env.TRACEREPORTS_ENV_FILE = "off";
process.env.TRACEREPORTS_BIN_DOWNLOAD ??= "0";
limits.maxBackoffMs = 100;
const VARS = ["TRACEREPORTS_SCREENSHOT_MASK", "TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS", "TRACEREPORTS_SCREENSHOTS", "TRACEREPORTS_RUN_ID", "TRACEREPORTS_DISABLED", "TRACEREPORTS_TOKEN"];
const clean = () => { for (const v of VARS) delete process.env[v]; };
clean();

test("selectores: los de siempre más TRACEREPORTS_SCREENSHOT_MASK; los de siempre se pueden quitar", () => {
  clean();
  assert.equal(maskSelector(), "input[type=password], [data-tracereports-mask]");
  process.env.TRACEREPORTS_SCREENSHOT_MASK = "input[name=rut], .tarjeta,";
  assert.equal(maskSelector(), "input[type=password], [data-tracereports-mask], input[name=rut], .tarjeta");
  process.env.TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS = "off";
  assert.equal(maskSelector(), "input[name=rut], .tarjeta");
  delete process.env.TRACEREPORTS_SCREENSHOT_MASK;
  assert.equal(maskSelector(), "");
  clean();
});

test("política TRACEREPORTS_SCREENSHOTS", () => {
  for (const [policy, status, allowed] of [["", "INFO", true], ["all", "INFO", true], ["basura", "INFO", true],
    ["failures", "INFO", false], ["failures", "FAIL", true], ["failures", "fail", true], ["off", "INFO", false], ["off", "FAIL", false]]) {
    process.env.TRACEREPORTS_SCREENSHOTS = policy;
    assert.equal(screenshotAllowed(status), allowed, `${policy}/${status}`);
  }
  clean();
});

test("Playwright: pasa mask con el locator de los selectores y el color", async () => {
  clean();
  process.env.TRACEREPORTS_SCREENSHOT_MASK = ".rut";
  let got;
  const page = { locator: (s) => ({ selector: s }), screenshot: async (o) => { got = o; return Buffer.from("png"); } };
  await playwrightScreenshot(page, { timeout: 5000 });
  assert.deepEqual(got.mask.map((m) => m.selector), ["input[type=password], [data-tracereports-mask], .rut"]);
  assert.equal(got.maskColor, MASK_COLOR);
  assert.equal(got.timeout, 5000);
  process.env.TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS = "off";
  delete process.env.TRACEREPORTS_SCREENSHOT_MASK;
  await playwrightScreenshot(page);
  assert.equal(got.mask, undefined, "sin nada que tapar no se pasa mask");
  clean();
});

/** Un driver de selenium-webdriver que registra el orden: tapar → capturar → destapar. */
function fakeDriver({ shotError, coverError } = {}) {
  const calls = [];
  return {
    calls,
    async executeScript(script) {
      const kind = script.includes("createElement") ? "cover" : "uncover";
      calls.push(kind);
      if (kind === "cover" && coverError) throw coverError;
      return 1;
    },
    async takeScreenshot() {
      calls.push("shot");
      if (shotError) throw shotError;
      return "cG5n";
    },
  };
}

test("Selenium: tapa, captura y destapa, también si la captura falla", async () => {
  clean();
  let d = fakeDriver();
  assert.equal(await seleniumScreenshot(d), "cG5n");
  assert.deepEqual(d.calls, ["cover", "shot", "uncover"]);
  d = fakeDriver({ shotError: new Error("browser gone") });
  assert.equal(await seleniumScreenshot(d), null);
  assert.deepEqual(d.calls, ["cover", "shot", "uncover"]);
});

test("Selenium: si no puede tapar, no captura", async () => {
  clean();
  const warn = console.warn;
  console.warn = () => {};
  try {
    const d = fakeDriver({ coverError: new Error("invalid selector") });
    assert.equal(await seleniumScreenshot(d), null);
    assert.ok(!d.calls.includes("shot"));
  } finally {
    console.warn = warn;
  }
  process.env.TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS = "off";
  const d = fakeDriver();
  assert.equal(await seleniumScreenshot(d), "cG5n");
  assert.deepEqual(d.calls, ["shot"], "sin selectores, solo captura");
  clean();
});

test("t.screenshot respeta TRACEREPORTS_SCREENSHOTS", async () => {
  for (const [policy, sent] of [["all", 2], ["failures", 1], ["off", 0]]) {
    clean();
    process.env.TRACEREPORTS_SCREENSHOTS = policy;
    const srv = await fakeServer();
    try {
      const cr = new TraceReports({ baseUrl: srv.url });
      await cr.startRun("capturas");
      const t = await cr.startTest("t");
      t.screenshot(Buffer.from("PNG"), "paso", "INFO");
      t.screenshot(Buffer.from("PNG"), "fallo", "FAIL");
      t.finish("FAIL");
      await cr.finishRun();
      assert.equal(srv.paths().filter((p) => p.endsWith("/screenshot")).length, sent, policy);
    } finally {
      await srv.close();
    }
  }
  clean();
});

const root = path.resolve("/proj");
function fakeTest(id, title) {
  return {
    id, title, retries: 0, tags: [], annotations: [], location: { file: path.join(root, "a.spec.js") },
    titlePath: () => ["", "chromium", "a.spec.js", title], parent: { project: () => ({ name: "chromium" }) },
  };
}

/** Lo que el reporter sube de un test con las capturas y artefactos de Playwright. */
async function reported(env, status, attachments) {
  clean();
  Object.assign(process.env, env);
  const srv = await fakeServer();
  try {
    const rep = new TraceReportsReporter({ url: srv.url, runName: "r" });
    rep.onBegin({ rootDir: root, projects: [{ name: "chromium" }] }, { allTests: () => [1] });
    const t = fakeTest("t1", "pago");
    rep.onTestBegin(t, { retry: 0, workerIndex: 0 });
    rep.onTestEnd(t, { status, retry: 0, errors: status === "failed" ? [{ message: "boom" }] : [], attachments });
    await rep.onEnd({ status });
    const shots = srv.requests.filter((r) => r.path.endsWith("/screenshot")).map((r) => r.body.toString());
    const artifacts = srv.requests.filter((r) => r.path.endsWith("/artifact")).length;
    return { shots, artifacts };
  } finally {
    await srv.close();
    clean();
  }
}

const PW = [
  { name: "screenshot", contentType: "image/png", body: Buffer.from("AUTO-SIN-MASCARA") },
  { name: "trace", contentType: "application/zip", body: Buffer.from("PK\u0003\u0004trace") },
  { name: "video", contentType: "video/webm", body: Buffer.from("\u001aEß£webm") },
];

test("reporter: con la captura tapada de los fixtures no sube la automática de Playwright", async () => {
  const withMasked = [...PW, { name: MASKED_SHOT, contentType: "image/png", body: Buffer.from("TAPADA") }];
  const r = await reported({}, "failed", withMasked);
  assert.equal(r.shots.length, 1);
  assert.match(r.shots[0], /TAPADA/);
  assert.match(r.shots[0], /Captura al fallar/);
  // solo el reporter (sin fixtures): la automática sigue subiendo, como antes
  const only = await reported({}, "failed", PW);
  assert.equal(only.shots.length, 1);
  assert.match(only.shots[0], /AUTO-SIN-MASCARA/);
});

test("reporter: TRACEREPORTS_SCREENSHOTS también rige capturas, trace y video", async () => {
  const off = await reported({ TRACEREPORTS_SCREENSHOTS: "off" }, "failed", PW);
  assert.deepEqual(off, { shots: [], artifacts: 0 });
  const failuresPass = await reported({ TRACEREPORTS_SCREENSHOTS: "failures" }, "passed", PW);
  assert.deepEqual(failuresPass, { shots: [], artifacts: 0 });
  const failuresFail = await reported({ TRACEREPORTS_SCREENSHOTS: "failures" }, "failed", PW);
  assert.equal(failuresFail.shots.length, 1);
  assert.equal(failuresFail.artifacts, 2);
});

// ─── navegador real: la zona queda tapada en los píxeles ────────────────────

const here = path.dirname(fileURLToPath(import.meta.url));
function loadPlaywright() {
  for (const dir of ["../../../test/e2e", "../../../examples/playwright-js"]) {
    const pkg = path.resolve(here, dir, "package.json");
    if (!fs.existsSync(pkg)) continue;
    try {
      return createRequire(pkg)("@playwright/test");
    } catch { /* sin node_modules */ }
  }
  return null;
}
const playwright = loadPlaywright();

const PAGE = `<html><body style="margin:0;background:#fff">
<input type="password" value="SECRETO" style="position:absolute;left:10px;top:10px;width:200px;height:40px">
<div data-tracereports-mask style="position:absolute;left:10px;top:70px;width:200px;height:40px;background:#0f0">12.345.678-9</div>
<div class="rut" style="position:absolute;left:10px;top:130px;width:200px;height:40px;background:#00f">9.876.543-2</div>
<div style="position:absolute;left:300px;top:10px;width:200px;height:40px;background:#0f0">visible</div>
</body></html>`;
const POINTS = { password: [110, 30], attr: [110, 90], rut: [110, 150], visible: [400, 30] };
const MAGENTA = [255, 0, 255];

/** Lee píxeles del PNG en un canvas de la misma página (sin dependencias de imágenes). */
async function pixels(page, b64) {
  return page.evaluate(async ([data, points]) => {
    const img = new Image();
    img.src = "data:image/png;base64," + data;
    await img.decode();
    const c = document.createElement("canvas");
    c.width = img.width;
    c.height = img.height;
    const ctx = c.getContext("2d");
    ctx.drawImage(img, 0, 0);
    const out = {};
    for (const [k, [x, y]] of Object.entries(points)) out[k] = Array.from(ctx.getImageData(x, y, 1, 1).data.slice(0, 3));
    return out;
  }, [b64, POINTS]);
}

async function withPage(fn) {
  let browser;
  try {
    browser = await playwright.chromium.launch();
  } catch (err) {
    return `chromium no disponible: ${err.message}`;
  }
  try {
    const page = await browser.newPage({ viewport: { width: 600, height: 200 } });
    await page.setContent(PAGE);
    await fn(page);
  } finally {
    await browser.close();
  }
  return "";
}

test("Playwright real: la zona queda tapada", { skip: !playwright && "sin @playwright/test en test/e2e" }, async (t) => {
  clean();
  process.env.TRACEREPORTS_SCREENSHOT_MASK = ".rut";
  const skipped = await withPage(async (page) => {
    const png = await playwrightScreenshot(page);
    const px = await pixels(page, png.toString("base64"));
    assert.deepEqual([px.password, px.attr, px.rut], [MAGENTA, MAGENTA, MAGENTA], JSON.stringify(px));
    assert.deepEqual(px.visible, [0, 255, 0]);
  });
  if (skipped) t.skip(skipped);
  clean();
});

test("los scripts de Selenium tapan en un navegador real y dejan el DOM igual", { skip: !playwright && "sin @playwright/test en test/e2e" }, async (t) => {
  clean();
  process.env.TRACEREPORTS_SCREENSHOT_MASK = ".rut";
  const skipped = await withPage(async (page) => {
    // un driver de Selenium sobre la página de Playwright: los mismos scripts que corren en Selenium
    const driver = {
      executeScript: (script, ...args) => page.evaluate(([s, a]) => new Function(s).apply(null, a), [script, args]),
      // caret: "initial": Playwright no toca el input (oculta el cursor cambiando su estilo)
      takeScreenshot: async () => (await page.screenshot({ caret: "initial" })).toString("base64"),
    };
    const before = await page.content();
    const b64 = await seleniumScreenshot(driver);
    assert.equal(await page.content(), before, "el DOM queda igual");
    const px = await pixels(page, b64);
    assert.deepEqual([px.password, px.attr, px.rut], [MAGENTA, MAGENTA, MAGENTA], JSON.stringify(px));
    assert.deepEqual(px.visible, [0, 255, 0]);
  });
  if (skipped) t.skip(skipped);
  clean();
});
