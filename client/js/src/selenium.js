// Selenium WebDriver (selenium-webdriver) con TraceReports: red, consola, captura y DOM. Sirve con
// cualquier runner (node:test, Mocha, Jest…): no importa selenium-webdriver, usa el `driver` que le
// pases.
//
//   import { Builder } from "selenium-webdriver";
//   import chrome from "selenium-webdriver/chrome.js";
//   import { TraceReports } from "tracereports";
//   import { seleniumOptions, withDriver, screenshot } from "tracereports/selenium";
//
//   const options = seleniumOptions(new chrome.Options());   // activa los logs de red y de consola
//   const driver = await new Builder().forBrowser("chrome").setChromeOptions(options).build();
//
//   const cr = new TraceReports();
//   await cr.startRun("Regresión web", { environment: "chrome" });
//   await cr.test("Login correcto", withDriver(driver, { key: "login.test.js > ok" }), async (t) => {
//     await driver.get(URL);
//     t.screenshot(await screenshot(driver), "Formulario de login");
//   });
//   await cr.finishRun();
//
// `withDriver` envía la red (pestaña "Red", Lab) y la consola de cada test, y si falla la captura del
// momento y el snapshot del DOM (con él el reporte recomienda selectores cuando se rompe un locator).
// La red y la consola salen del log de rendimiento de Chrome y Edge (Firefox no lo tiene): hay que
// crear el driver con `seleniumOptions(options)`. El body de una llamada hecha antes de que la
// página navegara puede no estar ya disponible; el resto de la llamada sí.

import { DOM_SCRIPT } from "./dom.js";
import { seleniumScreenshot } from "./mask.js";

const LOG_PREFS = { performance: "ALL", browser: "ALL" };
const BODY_TYPES = new Set(["xhr", "fetch"]);
const TEXT_MIME = ["json", "text", "xml", "javascript", "html", "x-www-form-urlencoded", "graphql"];

/** Activa en las opciones de Chrome o Edge los logs de rendimiento (red) y del navegador (consola). */
export function seleniumOptions(options) {
  options.set("goog:loggingPrefs", { ...LOG_PREFS });
  if (/edge/i.test(options?.constructor?.name || "") || options.get?.("browserName") === "MicrosoftEdge") {
    options.set("ms:loggingPrefs", { ...LOG_PREFS });
  }
  return options;
}

/**
 * Captura de pantalla como base64 (lo que acepta `t.screenshot`) con los datos sensibles tapados
 * (ver mask.js), o null.
 */
export async function screenshot(driver) {
  return seleniumScreenshot(driver);
}

export { seleniumScreenshot };

/** Snapshot de los elementos de la página, o null si el navegador ya no responde. */
export async function captureDom(driver) {
  try {
    return await driver.executeScript(DOM_SCRIPT);
  } catch {
    return null;
  }
}

function* events(entries) {
  for (const entry of entries || []) {
    let msg;
    try {
      msg = JSON.parse(entry.message).message;
    } catch {
      continue;
    }
    if (msg?.method?.startsWith("Network.")) yield [msg.method, msg.params || {}];
  }
}

/**
 * Convierte las entradas del log "performance" (`driver.manage().logs().get("performance")`) en
 * conexiones (el formato de `t.network`). Cada una lleva `_requestId` para pedir su body después.
 */
export function connectionsFromLog(entries) {
  const byId = new Map();
  const order = [];
  const extraReq = new Map();
  const extraRes = new Map();
  const ms = (from, to) => (from == null || to == null ? undefined : Math.round((to - from) * 1000));
  const setResponse = (c, r) => {
    c.status = Number(r.status || 0);
    c.status_text = r.statusText || "";
    c.mime_type = r.mimeType || "";
    c.response_headers = { ...(r.headers || {}) };
  };
  for (const [method, p] of events(entries)) {
    const id = p.requestId;
    if (!id) continue;
    if (method === "Network.requestWillBeSent") {
      const prev = byId.get(id);
      if (prev && p.redirectResponse) {
        // la redirección cierra el salto anterior con su propia respuesta
        setResponse(prev, p.redirectResponse);
        prev.duration_ms = ms(prev._ts, p.timestamp);
        prev._requestId = null;
      }
      const req = p.request || {};
      const c = {
        _requestId: id, _ts: p.timestamp, method: req.method || "", url: req.url || "",
        resource_type: String(p.type || "").toLowerCase(), request_headers: { ...(req.headers || {}) },
        post_data: req.postData || "", _hasPostData: Boolean(req.hasPostData || req.postData),
        started_at: p.wallTime ? Math.round(p.wallTime * 1000) : 0, status: 0,
      };
      byId.set(id, c);
      order.push(c);
    } else if (method === "Network.requestWillBeSentExtraInfo") {
      extraReq.set(id, { ...(extraReq.get(id) || {}), ...(p.headers || {}) });
    } else if (method === "Network.responseReceived" && byId.has(id)) {
      const c = byId.get(id);
      setResponse(c, p.response || {});
      if (p.type) c.resource_type = String(p.type).toLowerCase();
    } else if (method === "Network.responseReceivedExtraInfo") {
      extraRes.set(id, { ...(extraRes.get(id) || {}), ...(p.headers || {}) });
    } else if (method === "Network.loadingFinished" && byId.has(id)) {
      const c = byId.get(id);
      c.duration_ms = ms(c._ts, p.timestamp);
    } else if (method === "Network.loadingFailed" && byId.has(id)) {
      const c = byId.get(id);
      c.failed = true;
      c.error_text = p.errorText || (p.canceled ? "canceled" : "failed");
      c.duration_ms = ms(c._ts, p.timestamp);
    }
  }
  const out = [];
  for (const c of order) {
    if (/^(data|blob):/.test(c.url)) continue;
    // los headers reales que salieron, con la cookie de sesión
    if (extraReq.has(c._requestId)) c.request_headers = { ...c.request_headers, ...extraReq.get(c._requestId) };
    if (extraRes.has(c._requestId) && c.response_headers) c.response_headers = { ...c.response_headers, ...extraRes.get(c._requestId) };
    delete c._ts;
    out.push(c);
  }
  return out;
}

async function cdp(driver, cmd, params) {
  try {
    return (await driver.sendAndGetDevToolsCommand(cmd, params)) || {};
  } catch {
    return {}; // otro navegador, o el recurso ya no existe (la página navegó)
  }
}

function wantsBody(c) {
  if (c.failed || !c._requestId) return false;
  if (!BODY_TYPES.has(c.resource_type) && c.status < 400) return false;
  const mime = String(c.mime_type || "").toLowerCase();
  return !mime || TEXT_MIME.some((t) => mime.includes(t));
}

async function readLog(driver, kind) {
  try {
    return await driver.manage().logs().get(kind);
  } catch {
    return [];
  }
}

/**
 * Las llamadas de red desde la última lectura (el log se vacía al leerlo: cada test lee las
 * suyas), con los bodies de las APIs y de las respuestas con error. [] si el driver no tiene el
 * log de rendimiento (ver `seleniumOptions`).
 */
export async function captureNetwork(driver) {
  const conns = connectionsFromLog(await readLog(driver, "performance"));
  for (const c of conns) {
    if (c._hasPostData && !c.post_data && c._requestId) {
      c.post_data = (await cdp(driver, "Network.getRequestPostData", { requestId: c._requestId })).postData || "";
    }
    if (wantsBody(c)) {
      const res = await cdp(driver, "Network.getResponseBody", { requestId: c._requestId });
      if (typeof res.body === "string") {
        const body = res.base64Encoded ? Buffer.from(res.body, "base64").toString("utf8") : res.body;
        c.body_size = Buffer.byteLength(body);
        c.response_body = body;
      }
    }
    delete c._requestId;
    delete c._hasPostData;
  }
  return conns;
}

/** Errores y warnings de la consola desde la última lectura (el formato de `t.console`). */
export async function captureConsole(driver) {
  const out = [];
  for (const e of await readLog(driver, "browser")) {
    const name = String(e?.level?.name ?? e?.level ?? "").toUpperCase();
    let level = name === "SEVERE" ? "error" : name === "WARNING" ? "warning" : "";
    if (!level) continue;
    if (level === "error" && /\bUncaught\b/.test(e.message || "")) level = "pageerror";
    out.push({ level, text: String(e.message || ""), location: "", timestamp: Number(e.timestamp || 0) });
  }
  return out;
}

/** Vacía los logs del driver: lo de antes del test (otro test con el mismo driver) no se envía. */
export async function discardLogs(driver) {
  await readLog(driver, "performance");
  await readLog(driver, "browser");
}

/** Envía al test la red y la consola desde la última lectura. Devuelve cuántas llamadas envió. */
export async function reportSelenium(t, driver) {
  const conns = await captureNetwork(driver);
  if (conns.length) t.network(conns);
  const consoleEntries = await captureConsole(driver);
  if (consoleEntries.length) t.console(consoleEntries);
  return conns.length;
}

/**
 * Opciones para `cr.test(...)` con un driver de Selenium: al empezar descarta los logs anteriores,
 * al terminar envía la red y la consola del test, y si falla la captura y el DOM.
 */
export function withDriver(driver, opts = {}) {
  return {
    ...opts,
    async onStart(t) {
      await discardLogs(driver);
      if (opts.onStart) await opts.onStart(t);
    },
    async onFailure(t, err) {
      t.screenshot(await screenshot(driver), "Captura al fallar", "FAIL");
      t.dom(await captureDom(driver));
      if (opts.onFailure) await opts.onFailure(t, err);
    },
    async onEnd(t, err) {
      await reportSelenium(t, driver);
      if (opts.onEnd) await opts.onEnd(t, err);
    },
  };
}
