// Playwright Test: fixtures que capturan la evidencia sin cambiar los tests.
//
//   // en tus specs, en vez de "@playwright/test":
//   import { test, expect } from "tracereports/playwright";
//
//   test("login inválido", async ({ page, tracereports }) => {
//     tracereports.expectResponse(401, { url: "*/auth/validate*" });   // negativo esperado
//     tracereports.info("Abrir el login");
//     ...
//   });
//
// Con la `page` del test:
//   - captura las llamadas HTTP que hace el navegador (pestaña "Red"; es el tráfico visto desde el
//     navegador, no los logs del backend);
//   - si el test falla: snapshot del DOM (recomendador de locators) y una captura si el proyecto
//     no tiene `screenshot: "only-on-failure"`.
// Todo viaja como attachments del test; el reporter `tracereports/reporter` los sube.

import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { domSnapshotInPage } from "./dom.js";

// @playwright/test se carga desde el proyecto que corre los tests: así hay una sola copia aunque
// este paquete esté instalado como enlace (npm install ../tracereports/client/js).
async function loadPlaywrightTest() {
  try {
    const req = createRequire(path.join(process.cwd(), "package.json"));
    return await import(pathToFileURL(req.resolve("@playwright/test")).href);
  } catch {
    return import("@playwright/test");
  }
}
const pw = await loadPlaywrightTest();
const base = pw.test ?? pw.default?.test;
const expect = pw.expect ?? pw.default?.expect;

const BODY_TYPES = new Set(["xhr", "fetch"]);
const MAX_BODY = 256 * 1024;

/**
 * Engancha la captura de red a una página (antes de navegar). Devuelve un colector con
 * `settle(ms)` (espera los bodies en lectura) y `drain()` (conexiones nuevas desde el último drain).
 */
export function captureNetwork(page, { apiPatterns = ["/api/"] } = {}) {
  const conns = [];
  const byRequest = new Map();
  const pending = new Set();
  let reported = 0;

  page.on("request", (req) => {
    const c = {
      method: req.method(), url: req.url(), resource_type: req.resourceType(),
      request_headers: req.headers(), post_data: req.postData() || "", started_at: Date.now(), status: 0,
    };
    conns.push(c);
    byRequest.set(req, c);
  });
  page.on("response", (res) => {
    const c = byRequest.get(res.request());
    if (!c) return;
    c.status = res.status();
    c.status_text = res.statusText();
    c.response_headers = res.headers();
    c.mime_type = res.headers()["content-type"] || "";
    const isApi = BODY_TYPES.has(c.resource_type) || apiPatterns.some((p) => c.url.includes(p));
    if (isApi || c.status >= 400) {
      const p = res.text()
        .then((body) => { c.body_size = body.length; c.response_body = body.slice(0, MAX_BODY); })
        .catch(() => {})
        .finally(() => pending.delete(p));
      pending.add(p);
    }
  });
  page.on("requestfinished", (req) => {
    const c = byRequest.get(req);
    if (!c) return;
    const t = req.timing();
    c.duration_ms = t && t.responseEnd > 0 ? Math.round(t.responseEnd) : Date.now() - c.started_at;
  });
  page.on("requestfailed", (req) => {
    const c = byRequest.get(req);
    if (!c) return;
    c.failed = true;
    c.error_text = req.failure()?.errorText || "";
    c.duration_ms = Date.now() - c.started_at;
  });

  return {
    connections: conns,
    async settle(ms = 2000) {
      await Promise.race([Promise.allSettled([...pending]), new Promise((r) => setTimeout(r, ms))]);
    },
    drain() {
      const out = conns.slice(reported);
      reported = conns.length;
      return out;
    },
  };
}

/** Snapshot de los elementos de la página, o null si ya no está disponible. */
export async function captureDom(page) {
  try {
    return await page.evaluate(domSnapshotInPage);
  } catch {
    return null;
  }
}

const note = (testInfo, type, data) => testInfo.annotations.push({ type, description: JSON.stringify(data) });

/**
 * Agrega los fixtures de TraceReports a un `test` de Playwright (el tuyo, ya extendido o no):
 *   import { test as base } from "@playwright/test";
 *   export const test = withTraceReports(base);
 */
export function withTraceReports(baseTest) {
  return baseTest.extend(fixtures);
}

const fixtures = {
  page: async ({ page }, use, testInfo) => {
    const net = captureNetwork(page);
    await use(page);
    await net.settle(2000).catch(() => {});
    const conns = net.drain();
    if (conns.length) {
      await testInfo.attach("tracereports-network", { body: JSON.stringify(conns), contentType: "application/json" });
    }
    if (testInfo.status !== testInfo.expectedStatus) {
      const snap = await captureDom(page);
      if (snap) await testInfo.attach("tracereports-dom", { body: JSON.stringify(snap), contentType: "application/json" });
      const mode = testInfo.project.use?.screenshot;
      const auto = mode === "on" || mode === "only-on-failure" || mode?.mode === "on" || mode?.mode === "only-on-failure";
      if (!auto) {
        try {
          await testInfo.attach("Captura al fallar", { body: await page.screenshot({ timeout: 5000 }), contentType: "image/png" });
        } catch { /* la página puede estar cerrada */ }
      }
    }
  },

  /** Pasos propios y respuestas esperadas (el reporter los manda a TraceReports). */
  tracereports: async ({}, use, testInfo) => {
    const log = (status) => (message) => note(testInfo, "tracereports-log", { status, message: String(message), timestamp: Date.now() });
    await use({
      info: log("INFO"), pass: log("PASS"), fail: log("FAIL"), warn: log("WARNING"),
      expectResponse: (status, { url = "", method = "" } = {}) => note(testInfo, "tracereports-expect", { status, url, method }),
    });
  },
};

export const test = withTraceReports(base);

export { expect };
