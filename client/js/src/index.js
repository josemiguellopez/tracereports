// Cliente de TraceReports para JavaScript/TypeScript (Node 18+, sin dependencias).
//
//   import { TraceReports } from "tracereports";
//
//   const cr = new TraceReports();                       // $TRACEREPORTS_URL o http://localhost:8080
//   await cr.startRun("Regresión web", { environment: "staging" });
//   const t = await cr.startTest("Login", { key: "login.spec.js > admin entra", category: "smoke" });
//   t.info("Abrir el login");
//   t.screenshot(await page.screenshot(), "Formulario");   // Playwright (Buffer)
//   t.screenshot(await driver.takeScreenshot(), "Form");   // Selenium (base64)
//   t.finish("PASS");
//   await cr.finishRun();                                  // espera la cola y cierra la ejecución
//
// Nunca rompe ni frena la suite: la evidencia sale en segundo plano con reintentos e
// Idempotency-Key (sin duplicados); `cr.delivery` dice qué no llegó. Si la ejecución no se puede
// crear (sin servidor, caído o con el token equivocado) la evidencia se graba en una carpeta
// ($TRACEREPORTS_OFFLINE_DIR, default ./tracereports-offline/<sesión>) en vez de perderse:
// `tracereports report <carpeta>` arma el reporte HTML y `tracereports push <carpeta>` la sube.

import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { detectBranch, detectCommit } from "./context.js";
import { env } from "./env.js";
import { Recorder, newSessionDir } from "./offline.js";
import { Sender } from "./transport.js";

export const VERSION = "0.1.0";
const STATUSES = new Set(["INFO", "PASS", "FAIL", "WARNING", "SKIP"]);
const truthy = (v) => ["1", "true", "yes"].includes(String(v || "").toLowerCase());

export class TraceReports {
  /**
   * @param {object} [opts]
   * @param {string} [opts.baseUrl]  default $TRACEREPORTS_URL o http://localhost:8080
   * @param {string} [opts.token]    token de la API (TRACEREPORTS_TOKEN del servidor)
   * @param {boolean} [opts.enabled] false (o $TRACEREPORTS_DISABLED=1) apaga todo
   * @param {number} [opts.timeoutMs]       por request JSON (default 3000)
   * @param {number} [opts.uploadTimeoutMs] por captura o lote de red (default 15000)
   * @param {number} [opts.flushTimeoutMs]  cuánto espera finishRun la cola (default $TRACEREPORTS_FLUSH_TIMEOUT s o 30 s)
   * @param {number} [opts.maxQueueItems]   default 5000
   * @param {number} [opts.maxQueueMB]      default 64
   * @param {string} [opts.offlineDir] dónde grabar sin servidor (default $TRACEREPORTS_OFFLINE_DIR; sin
   *   ella, una carpeta nueva por sesión dentro de ./tracereports-offline)
   * @param {"auto"|"always"|"off"} [opts.offline] auto (default, $TRACEREPORTS_OFFLINE): grabar solo si
   *   la ejecución no se puede crear; always: grabar sin intentar un servidor; off: nunca
   */
  constructor(opts = {}) {
    this.baseUrl = (opts.baseUrl || env("URL") || "http://localhost:8080").replace(/\/+$/, "");
    this.token = opts.token ?? env("TOKEN", "");
    this.enabled = (opts.enabled ?? true) && !truthy(env("DISABLED"));
    this.timeoutMs = opts.timeoutMs ?? 3000;
    this.uploadTimeoutMs = opts.uploadTimeoutMs ?? 15000;
    this.flushTimeoutMs = opts.flushTimeoutMs ?? (Number(env("FLUSH_TIMEOUT")) * 1000 || 30_000);
    this.sender = new Sender(this.baseUrl, () => this.headers(), {
      maxItems: opts.maxQueueItems ?? 5000,
      maxBytes: (opts.maxQueueMB ?? 64) << 20,
    });
    this.runId = null;
    this.runCreated = false;
    this.runNotClosed = false;
    this.unregisteredTests = 0;

    let mode = String(opts.offline ?? env("OFFLINE", "auto")).trim().toLowerCase();
    if (["1", "true", "yes", "on"].includes(mode)) mode = "always";
    if (["0", "false", "no"].includes(mode)) mode = "off";
    this.offlineMode = mode;
    const explicit = opts.offlineDir || env("OFFLINE_DIR") || "";
    this.offlineBase = explicit || "tracereports-offline";
    this.offlineExplicit = Boolean(explicit);
    this.offlineDir = null; // carpeta donde se graba (null: se envía al servidor)
    this.offlineReport = null; // index.html generado al cerrar, si el binario está
    if (this.enabled && mode === "always") this.goOffline();
  }

  /** true si la evidencia se graba localmente (sin servidor). */
  get recording() {
    return this.offlineDir != null;
  }

  goOffline(reason = "") {
    const dir = this.offlineExplicit ? this.offlineBase : newSessionDir(this.offlineBase);
    try {
      this.sender = new Recorder(dir);
    } catch (err) {
      console.warn(`tracereports: no se pudo grabar en ${dir}: ${err.message}`);
      return;
    }
    this.offlineDir = dir;
    if (reason) {
      console.warn(`tracereports: ${reason}; la evidencia se graba en ${dir} (reporte: \`tracereports report ${dir}\`; ` +
        `subirla después: \`tracereports push ${dir}\`)`);
    }
  }

  /** Con el binario instalado ($TRACEREPORTS_BIN o en el PATH), arma el reporte HTML de la grabación. */
  buildOfflineReport({ zip } = {}) {
    const bin = env("BIN") || findInPath("tracereports");
    if (!bin || ["0", "false", "no"].includes(String(env("OFFLINE_REPORT", "1")).toLowerCase())) {
      console.warn(`tracereports: evidencia grabada en ${this.offlineDir}. Reporte sin servidor: ` +
        `\`tracereports report ${this.offlineDir} -o reporte\`; subirla: \`tracereports push ${this.offlineDir}\``);
      return null;
    }
    const out = path.join(this.offlineDir, "report");
    const args = ["report", "-o", out, ...(zip ? ["--zip", zip] : []), this.offlineDir];
    const res = spawnSync(bin, args, { timeout: 300_000, encoding: "utf8" });
    if (res.status !== 0) {
      console.warn(`tracereports: no se pudo armar el reporte (${(res.stderr || res.error?.message || "").trim()}); ` +
        `usa \`tracereports report ${this.offlineDir}\``);
      return null;
    }
    this.offlineReport = path.join(out, "index.html");
    return this.offlineReport;
  }

  headers() {
    const h = { "User-Agent": `tracereports-js/${VERSION}` };
    if (this.token) h.Authorization = `Bearer ${this.token}`;
    return h;
  }

  /** sent, retried, rejected (4xx), dropped (cola llena), lost, pending y unregisteredTests. */
  /**
   * sent, retried, rejected (4xx), dropped (cola llena), lost, pending, unregisteredTests y
   * runNotClosed (1 si el servidor no confirmó el cierre de la ejecución: quedó abierta).
   */
  get delivery() {
    return { ...this.sender.stats, pending: this.sender.pending, unregisteredTests: this.unregisteredTests,
      runNotClosed: this.runNotClosed ? 1 : 0 };
  }

  /** Eventos que no llegaron al servidor (incluye un cierre de ejecución no confirmado). */
  deliveryProblems() {
    const d = this.delivery;
    return d.rejected + d.dropped + d.lost + d.pending + d.unregisteredTests + d.runNotClosed;
  }

  get reportUrl() {
    if (this.recording) return this.offlineReport || "";
    return this.runId ? `${this.baseUrl}/#run=${this.runId}&view=dashboard` : "";
  }

  async request(method, apiPath, payload) {
    if (!this.enabled) return null;
    return this.sender.sendNow(method, apiPath, JSON.stringify(payload), "application/json", this.timeoutMs);
  }

  emit(method, apiPath, body, contentType = "application/json", timeout = this.timeoutMs) {
    if (!this.enabled) return false;
    return this.sender.enqueue(method, apiPath, body, contentType, timeout);
  }

  // ─── ejecuciones ──────────────────────────────────────────────────────

  /**
   * Crea la ejecución. Con $TRACEREPORTS_RUN_ID se une a una ya creada (shards de CI) y no la cierra.
   * `project` (default $TRACEREPORTS_PROJECT o la carpeta actual), `branch` y `commit` (default: CI o
   * git) definen el contexto: historial y comparaciones solo usan ejecuciones del mismo proyecto,
   * ambiente y rama.
   */
  async startRun(name, { environment = "", project, branch, commit, framework = "" } = {}) {
    if (env("RUN_ID")) return this.joinRun(env("RUN_ID"));
    const payload = {
      name,
      environment,
      project: project ?? env("PROJECT") ?? path.basename(process.cwd()),
      branch: branch ?? detectBranch(),
      commit: commit ?? detectCommit(),
      framework,
    };
    const res = await this.request("POST", "/api/v1/runs", payload);
    let created = res;
    if (!created && this.enabled && this.offlineMode === "auto" && !this.recording) {
      // sin servidor, caído o con el token equivocado: se graba en vez de perderlo todo
      this.goOffline(`no se pudo crear la ejecución en ${this.baseUrl} (servidor caído o token incorrecto)`);
      if (this.recording) created = await this.request("POST", "/api/v1/runs", payload);
    }
    this.runId = created?.run_id ?? null;
    this.runCreated = Boolean(this.runId);
    if (this.enabled && !this.runId) {
      console.warn(`tracereports: no se pudo crear la ejecución en ${this.baseUrl}; los tests siguen sin reporte.`);
    }
    return this.runId;
  }

  /**
   * Reporta en una ejecución creada en otro proceso; quien la creó la cierra. Un runId negativo es
   * una ejecución que se graba sin servidor: pasa la misma `offlineDir`.
   */
  joinRun(runId, { offlineDir } = {}) {
    if (runId && Number(runId) < 0 && this.enabled && !this.recording) {
      if (offlineDir || env("OFFLINE_DIR")) {
        this.offlineBase = offlineDir || env("OFFLINE_DIR");
        this.offlineExplicit = true;
      }
      this.goOffline();
    }
    this.runId = runId ? Number(runId) : null;
    this.runCreated = false;
    return this.runId;
  }

  /** Espera la cola (flushTimeoutMs) y cierra la ejecución. interrupted: incompleta, nunca verde. */
  async finishRun({ interrupted = false } = {}) {
    await this.flush();
    this.sender.abandon();
    if (!this.runId || !this.runCreated || !this.enabled) return null; // unido a otra: la cierra su dueño
    // el cierre importa más que un paso: más reintentos, siempre con la misma Idempotency-Key
    const res = await this.sender.sendNow("PATCH", `/api/v1/runs/${this.runId}/finish`,
      JSON.stringify({ interrupted }), "application/json", this.timeoutMs, 4, { ignoreCircuit: true });
    this.runNotClosed = res == null;
    if (this.recording) {
      this.buildOfflineReport();
      return res;
    }
    if (this.runNotClosed) {
      console.warn(`tracereports: el servidor no confirmó el cierre de la ejecución #${this.runId}: quedó abierta ` +
        `(ciérrala con PATCH ${this.baseUrl}/api/v1/runs/${this.runId}/finish)`);
    }
    return res;
  }

  /** Espera a que la evidencia encolada se envíe. Devuelve lo que quedó pendiente. */
  flush(timeoutMs = this.flushTimeoutMs) {
    return this.sender.flush(timeoutMs);
  }

  // ─── tests ────────────────────────────────────────────────────────────

  /**
   * Inicia un test. `key` es su identidad estable dentro del proyecto (archivo + título): el
   * historial y la detección de flaky la siguen. Sin key, el servidor usa el nombre.
   * Devuelve siempre un TraceTest (no-op si el servidor no respondió).
   */
  async startTest(name, { category = "", description = "", key = "", suite = "", params = "", worker = "" } = {}) {
    if (!this.runId) return new TraceTest(this, null);
    const res = await this.request("POST", `/api/v1/runs/${this.runId}/tests`,
      { name, category, description, key, suite, params, worker: String(worker ?? "") });
    if (this.enabled && !res?.test_id) this.unregisteredTests++;
    return new TraceTest(this, res?.test_id ?? null);
  }

  /**
   * Corre `fn` como un test: lo inicia, lo cierra según el resultado y, si falla, adjunta la
   * captura y el snapshot de `onFailure` (opcional). El error se vuelve a lanzar.
   */
  async test(name, opts, fn) {
    if (typeof opts === "function") [fn, opts] = [opts, {}];
    const t = await this.startTest(name, opts);
    try {
      const out = await fn(t);
      t.finish(opts.status);
      return out;
    } catch (err) {
      if (opts.onFailure) {
        try { await opts.onFailure(t, err); } catch { /* la evidencia nunca rompe el test */ }
      }
      t.fail(`${err?.name || "Error"}: ${err?.message ?? err}`);
      t.finish("FAIL", { error: err });
      throw err;
    }
  }
}

export class TraceTest {
  constructor(cr, id) {
    this.cr = cr;
    this.id = id;
    this.expected = [];
    this.attempts = 1;
  }

  get active() {
    return Boolean(this.id && this.cr.enabled);
  }

  /** Paso del test. `timestamp` (ms) permite registrar la hora real en que ocurrió. */
  log(status, message, timestamp = Date.now()) {
    status = String(status || "INFO").toUpperCase();
    if (!this.active || !STATUSES.has(status)) return;
    this.cr.emit("POST", `/api/v1/tests/${this.id}/logs`,
      JSON.stringify({ status, message: String(message), timestamp: Math.round(timestamp) }));
  }

  info(m) { this.log("INFO", m); }
  pass(m) { this.log("PASS", m); }
  fail(m) { this.log("FAIL", m); }
  warn(m) { this.log("WARNING", m); }
  skip(m) { this.log("SKIP", m); }

  /**
   * Captura como paso. Acepta un Buffer/Uint8Array (Playwright `page.screenshot()`) o un string
   * base64 (Selenium `driver.takeScreenshot()`).
   */
  screenshot(image, message = "", status = "INFO") {
    if (!this.active || !image) return;
    const data = typeof image === "string" ? Buffer.from(image, "base64") : Buffer.from(image);
    const boundary = `----tracereports${Math.random().toString(16).slice(2)}`;
    const part = (name, value) => `--${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n${value}\r\n`;
    const body = Buffer.concat([
      Buffer.from(part("message", message) + part("status", String(status).toUpperCase())),
      Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="screenshot.png"\r\nContent-Type: image/png\r\n\r\n`),
      data,
      Buffer.from(`\r\n--${boundary}--\r\n`),
    ]);
    this.cr.emit("POST", `/api/v1/tests/${this.id}/screenshot`, body, `multipart/form-data; boundary=${boundary}`, this.cr.uploadTimeoutMs);
  }

  /**
   * Adjunta el trace de Playwright (`kind: "trace"`, el trace.zip) o el video del test
   * (`kind: "video"`, WebM o MP4): Buffer o ruta. Hasta 100 MB; el reporte muestra el video y abre el
   * trace en el Trace Viewer.
   */
  artifact(kind, data, name = "") {
    if (!this.active || !data) return false;
    let body = data;
    if (typeof data === "string") {
      try {
        body = fs.readFileSync(data);
        name ||= path.basename(data);
      } catch {
        return false;
      }
    }
    if (body.length > 100 << 20) {
      console.warn(`tracereports: ${name || kind} pesa más de 100 MB: no se adjunta`);
      return false;
    }
    const boundary = `----tracereports${Math.random().toString(16).slice(2)}`;
    const part = (n, v) => `--${boundary}\r\nContent-Disposition: form-data; name="${n}"\r\n\r\n${v}\r\n`;
    const multipart = Buffer.concat([
      Buffer.from(part("kind", kind) + part("name", String(name).replace(/[\r\n"]/g, " "))),
      Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="artifact"\r\nContent-Type: application/octet-stream\r\n\r\n`),
      Buffer.from(body),
      Buffer.from(`\r\n--${boundary}--\r\n`),
    ]);
    return this.cr.emit("POST", `/api/v1/tests/${this.id}/artifact`, multipart, `multipart/form-data; boundary=${boundary}`, Math.max(this.cr.uploadTimeoutMs, 60_000));
  }

  /**
   * Declara una respuesta negativa que el test verifica a propósito (p. ej. un 401 con
   * credenciales inválidas): no cuenta como error ni como causa del fallo. `url`: texto contenido
   * o glob con `*`.
   */
  expectResponse(status, { url = "", method = "" } = {}) {
    this.expected.push({ status: [].concat(status).map(Number), url, method: method.toUpperCase() });
  }

  isExpected(c) {
    return this.expected.some((r) => {
      if (!r.status.includes(Number(c.status || 0))) return false;
      if (r.method && (c.method || "").toUpperCase() !== r.method) return false;
      if (!r.url) return true;
      if (!r.url.includes("*")) return (c.url || "").includes(r.url);
      const re = new RegExp("^" + r.url.split("*").map((s) => s.replace(/[.+?^${}()|[\]\\]/g, "\\$&")).join(".*") + "$");
      return re.test(c.url || "");
    });
  }

  /** Llamadas de red del test (ver `tracereports/playwright`). Antes de finish. */
  network(connections, { maxBodyKB = 256, batchSize = 200 } = {}) {
    if (!this.active || !connections?.length) return { stored: 0, errors: 0 };
    const limit = maxBodyKB * 1024;
    const payload = connections.map((c) => {
      const body = String(c.response_body ?? "");
      return {
        method: c.method || "", url: c.url || "", status: Number(c.status || 0), status_text: c.status_text || "",
        mime_type: c.mime_type || "", resource_type: c.resource_type || "", failed: Boolean(c.failed),
        error_text: String(c.error_text || ""), started_at: Math.round(c.started_at || 0),
        duration_ms: c.duration_ms == null ? null : Math.round(c.duration_ms),
        request_headers: c.request_headers || {}, post_data: String(c.post_data || ""),
        response_headers: c.response_headers || {}, response_body: body.slice(0, limit),
        body_size: Math.max(Number(c.body_size || 0), body.length), body_truncated: body.length > limit,
        expected: Boolean(c.expected) || this.isExpected(c),
      };
    });
    for (let i = 0; i < payload.length; i += batchSize) {
      this.cr.emit("POST", `/api/v1/tests/${this.id}/network`, JSON.stringify({ connections: payload.slice(i, i + batchSize) }),
        "application/json", this.cr.uploadTimeoutMs);
    }
    return { stored: payload.length, errors: payload.filter((p) => !p.expected && (p.failed || p.status >= 400)).length };
  }

  /** Snapshot de la página al fallar (recomendador de locators). Antes de finish. */
  dom(snapshot) {
    if (!this.active || !snapshot) return;
    this.cr.emit("POST", `/api/v1/tests/${this.id}/dom`, JSON.stringify(snapshot), "application/json", this.cr.uploadTimeoutMs);
  }

  /**
   * Cierra el test. status: PASS | FAIL | WARNING | SKIP, o vacío para deducirlo de los pasos.
   * Un FAIL dispara el diagnóstico con IA. `attempts` > 1: reintentos (pasó tras reintento).
   */
  finish(status = "", { errorMessage = "", errorTrace = "", error, attempts } = {}) {
    if (!this.active) return;
    if (error) {
      errorMessage ||= `${error.name || "Error"}: ${error.message ?? error}`;
      errorTrace ||= String(error.stack || "");
      status ||= "FAIL";
    }
    const payload = { status: String(status || "").toUpperCase(), error_message: errorMessage, error_trace: errorTrace };
    const n = attempts ?? this.attempts;
    if (n > 1) payload.attempts = n;
    this.cr.emit("PATCH", `/api/v1/tests/${this.id}/finish`, JSON.stringify(payload));
  }
}

export { DOM_SCRIPT, domSnapshotInPage } from "./dom.js";

function findInPath(name) {
  const exts = process.platform === "win32" ? (process.env.PATHEXT || ".EXE").split(";") : [""];
  for (const dir of (process.env.PATH || "").split(path.delimiter)) {
    for (const ext of exts) {
      const file = path.join(dir, name + ext.toLowerCase());
      try {
        if (dir && fs.statSync(file).isFile()) return file;
      } catch {
        // no está aquí
      }
    }
  }
  return null;
}
