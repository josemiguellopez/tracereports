// Reporter de Playwright Test para TraceReports.
//
//   // playwright.config.js
//   reporter: [["list"], ["tracereports/reporter", { runName: "Regresión web", environment: "staging" }]],
//
// - Una ejecución por corrida (todos los workers reportan aquí: el reporter vive en el proceso
//   principal). Con --shard en varias máquinas, TRACEREPORTS_RUN_ID une los shards a una ejecución.
// - Identidad estable de cada test: archivo + títulos (+ proyecto de Playwright), aparte del nombre.
// - Reintentos (`retries`): los intentos quedan en el mismo test ("pasó tras reintento").
// - Pasos de `test.step`, error y stack, capturas adjuntas (`screenshot: "only-on-failure"` o
//   testInfo.attach con imagen) y, con los fixtures de `tracereports/playwright`, la red y el DOM.
// - Al terminar espera la cola y cierra la ejecución; imprime el link. TRACEREPORTS_STRICT=1 hace fallar
//   la corrida si parte de la evidencia no llegó al servidor.
// - Sin servidor (no responde o el token es incorrecto) la evidencia se graba en
//   ./tracereports-offline/<sesión>: `tracereports report <carpeta>` arma el reporte HTML.
//
// Opciones (o variables): url (TRACEREPORTS_URL), token (TRACEREPORTS_TOKEN), runName (TRACEREPORTS_RUN_NAME),
// environment (TRACEREPORTS_ENV), project (TRACEREPORTS_PROJECT), strict (TRACEREPORTS_STRICT),
// offlineDir (TRACEREPORTS_OFFLINE_DIR), offlineBase (TRACEREPORTS_OFFLINE_BASE),
// offline: "auto" | "always" | "both" | "off" (TRACEREPORTS_OFFLINE).

import fs from "node:fs";
import path from "node:path";
import { env } from "./env.js";
import { TraceReports } from "./index.js";

const ANSI = /\u001b\[[0-9;]*m/g;
const truthy = (v) => ["1", "true", "yes"].includes(String(v || "").toLowerCase());

export default class TraceReportsReporter {
  constructor(options = {}) {
    this.options = options;
    this.cr = new TraceReports({ baseUrl: options.url, token: options.token, offlineDir: options.offlineDir, offlineBase: options.offlineBase, offline: options.offline });
    this.tests = new Map(); // test.id -> { ct: Promise<TraceTest>, lastError }
    this.steps = new Map(); // test.id -> intento -> pasos (test.step)
    this.strict = options.strict ?? truthy(env("STRICT"));
    this.chain = Promise.resolve(); // los eventos de cada test se procesan en orden
  }

  printsToStdio() {
    return false;
  }

  onBegin(config, suite) {
    // la carpeta del proyecto es la del playwright.config (rootDir es la de los tests)
    this.rootDir = config.configFile ? path.dirname(config.configFile) : config.rootDir;
    const name = this.options.runName || env("RUN_NAME") || path.basename(this.rootDir);
    const projects = [...new Set(config.projects.map((p) => p.name).filter(Boolean))].join(", ");
    const environment = this.options.environment ?? env("ENV") ?? projects;
    this.started = this.cr.startRun(name, {
      environment,
      project: this.options.project ?? env("PROJECT") ?? path.basename(this.rootDir),
      framework: "playwright",
    });
    this.total = suite.allTests().length;
  }

  identity(test) {
    const file = path.relative(this.rootDir || process.cwd(), test.location.file).split(path.sep).join("/");
    const titles = test.titlePath().slice(3); // ["", proyecto, archivo, ...describe, título]
    const projectName = test.parent?.project()?.name || "";
    return {
      key: [file, ...titles].join(" > ") + (projectName ? ` [${projectName}]` : ""),
      suite: [file, ...titles.slice(0, -1)].join(" > "),
      params: projectName,
      name: titles.map((t) => t.replace(/\s+@[\w-]+/g, "").trim()).join(" › ") || test.title, // sin los @tags
      category: [...new Set([...(test.tags || []).map((t) => t.replace(/^@/, "")), path.basename(file).replace(/\.(spec|test)\.[cm]?[jt]sx?$/, "")])].join(", "),
    };
  }

  onTestBegin(test, result) {
    this.chain = this.chain.then(async () => {
      await this.started;
      const entry = this.tests.get(test.id);
      if (entry) { // reintento: mismo test en TraceReports
        const ct = await entry.ct;
        ct.attempts = result.retry + 1;
        ct.warn(`Reintento ${result.retry + 1}: el intento ${result.retry} falló — ${entry.lastError || "falló"}`);
        return;
      }
      const id = this.identity(test);
      const ct = this.cr.startTest(id.name, { key: id.key, suite: id.suite, params: id.params, category: id.category,
        worker: `w${result.workerIndex}` });
      this.tests.set(test.id, { ct });
    }).catch(() => {});
  }

  // Los pasos se guardan hasta el final del intento: recién ahí se sabe si un error era esperado
  // (test.fail()) y no debe mostrarse como fallo.
  onStepEnd(test, result, step) {
    if (step.category !== "test.step") return;
    const steps = (this.steps.get(test.id) ?? new Map());
    this.steps.set(test.id, steps);
    const attempt = result?.retry ?? 0;
    const list = steps.get(attempt) ?? [];
    steps.set(attempt, list);
    list.push({ title: step.title, error: step.error ? clean(step.error.message) : "", at: +step.startTime || Date.now() });
  }

  onTestEnd(test, result) {
    this.chain = this.chain.then(async () => {
      const entry = this.tests.get(test.id);
      const ct = await entry?.ct;
      if (!ct) return;
      for (const a of result.annotations ?? test.annotations ?? []) {
        if (a.type === "tracereports-expect") {
          const e = JSON.parse(a.description);
          ct.expectResponse(e.status, { url: e.url, method: e.method });
        }
      }
      // Resultado observado vs. esperado: con test.fail() fallar es lo esperado (Playwright lo da
      // por bueno) y pasar es un error ("Expected to fail, but passed").
      const expected = test.expectedStatus ?? "passed";
      const skipped = result.status === "skipped";
      const asExpected = result.status === expected;
      const unexpected = !asExpected && !skipped;
      const expectedFailure = asExpected && expected === "failed";

      const error = result.errors?.[0] || result.error;
      const detail = error ? clean(error.message).split("\n").filter(Boolean).slice(0, 3).join(" ") : "";
      const message = unexpected && expected === "failed" && result.status === "passed"
        ? "Se esperaba que fallara (test.fail()), pero pasó"
        : detail || (unexpected ? `Test ${result.status}` : "");

      // pasos propios (tracereports-log) y test.step, en el orden en que ocurrieron
      const logs = (result.annotations ?? test.annotations ?? []).filter((x) => x.type === "tracereports-log").map((a) => JSON.parse(a.description));
      const steps = (this.steps.get(test.id)?.get(result.retry) ?? []).map((s) => ({
        status: !s.error ? "INFO" : unexpected ? "FAIL" : "WARNING",
        message: s.error ? `${s.title}: ${s.error}${unexpected ? "" : " (error esperado: test.fail())"}` : s.title,
        timestamp: s.at,
      }));
      for (const l of [...steps, ...logs].sort((a, b) => a.timestamp - b.timestamp)) ct.log(l.status, l.message, l.timestamp);

      for (const att of result.attachments) {
        const body = att.body ?? (att.path && fs.existsSync(att.path) ? fs.readFileSync(att.path) : null);
        if (!body) continue;
        if (att.name === "tracereports-network") ct.network(JSON.parse(body.toString()));
        else if (att.name === "tracereports-dom") ct.dom(JSON.parse(body.toString()));
        else if (att.name === "tracereports-console") ct.console(JSON.parse(body.toString()));
        else if (att.contentType?.startsWith("image/")) ct.screenshot(body, att.name === "screenshot" ? "Captura al fallar" : att.name,
          unexpected ? "FAIL" : "INFO");
        // trace: "on" / "retain-on-failure" / "on-first-retry"; video: lo mismo en use.video
        else if (att.name === "trace" && att.contentType === "application/zip") ct.artifact("trace", body, `trace-${result.retry + 1}.zip`);
        else if (att.contentType?.startsWith("video/")) ct.artifact("video", body, att.name === "video" ? `video-${result.retry + 1}` : att.name);
      }
      const final = asExpected || skipped || result.retry >= test.retries;
      if (!final) {
        entry.lastError = message;
        ct.fail(message || `Intento ${result.retry + 1}: ${result.status}`);
        return;
      }
      let status = skipped ? "SKIP" : unexpected ? "FAIL" : "PASS";
      if (status === "FAIL") ct.fail(message);
      if (expectedFailure) ct.info(`Falló como se esperaba (test.fail())${detail ? `: ${detail}` : ""}`);
      if (skipped) {
        const reason = (result.annotations ?? test.annotations ?? []).find((a) => a.type === "skip" || a.type === "fixme")?.description;
        if (reason) ct.skip(reason);
      }
      ct.finish(status, {
        errorMessage: status === "FAIL" ? message : "",
        errorTrace: status === "FAIL" && error ? clean(error.stack || error.message || "") : "",
        attempts: result.retry + 1,
      });
    }).catch(() => {});
  }

  async onEnd(result) {
    await this.chain;
    await this.started;
    const interrupted = result.status === "interrupted" || result.status === "timedout";
    await this.cr.finishRun({ interrupted });
    const problems = this.cr.deliveryProblems() + (this.cr.recording && this.cr.runId < 0 && ["auto", "both"].includes(this.cr.offlineMode) ? 1 : 0);
    if (this.cr.recording && this.cr.runId > 0) {
      console.log(`TraceReports: ${this.cr.reportUrl}`);
      console.log(`TraceReports copia local: ${this.cr.offlineReport || `tracereports report ${this.cr.offlineDir}`}`);
    } else if (this.cr.recording) {
      console.log(`TraceReports (sin servidor): evidencia en ${this.cr.offlineDir}; reporte: ` +
        (this.cr.reportUrl || `\`tracereports report ${this.cr.offlineDir} -o reporte\``) +
        `; para subirla: \`tracereports push ${this.cr.offlineDir}\``);
    } else if (this.cr.runId) console.log(`TraceReports: ${this.cr.reportUrl}`);
    if (problems && (!this.cr.recording || this.cr.runId > 0)) {
      const d = this.cr.delivery;
      console.warn(`TraceReports: atención: ${problems - d.runNotClosed} eventos de evidencia no llegaron al servidor ` +
        `(enviados ${d.sent}, rechazados ${d.rejected}, descartados ${d.dropped}, perdidos ${d.lost})` +
        (d.runNotClosed ? "; el servidor no confirmó el cierre de la ejecución: quedó abierta" : "") +
        (this.strict ? " — TRACEREPORTS_STRICT: la corrida falla" : ""));
    }
    if (this.strict && (problems || (this.cr.enabled && !this.cr.runId)) && result.status === "passed") {
      return { status: "failed" };
    }
  }
}

function clean(s) {
  return String(s || "").replace(ANSI, "").trim();
}
