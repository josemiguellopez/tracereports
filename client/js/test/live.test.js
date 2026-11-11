// En vivo desde el cliente JS: execution_id (TRACEREPORTS_EXECUTION_ID o el build de CI), tests
// planificados del reporter y la cancelación cooperativa (TRACEREPORTS_REMOTE_CONTROL=1): el reporter
// pregunta al servidor y deja el estado en un archivo que lee el fixture de cada test.
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { TraceReports } from "../src/index.js";
import TraceReportsReporter from "../src/reporter.js";
import { defaultExecutionId } from "../src/context.js";
import { CONTROL_FILE_ENV, cancelReason, newControlFile, skipMessage, writeCancel } from "../src/control.js";

process.env.TRACEREPORTS_ENV_FILE = "off";
process.env.TRACEREPORTS_BIN_DOWNLOAD ??= "0";

async function liveServer({ old = false } = {}) {
  const requests = [];
  const state = { cancel: false, reason: "" };
  const server = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      const body = Buffer.concat(chunks).toString();
      requests.push({ method: req.method, path: req.url, body: body ? JSON.parse(body) : null });
      const send = (code, out) => res.writeHead(code, { "Content-Type": "application/json" }).end(JSON.stringify(out));
      if (old && (req.url.endsWith("/control") || req.url.endsWith("/plan"))) return send(404, { error: "not found" });
      if (req.url === "/api/v1/runs") return send(201, { run_id: 7 });
      if (req.url.endsWith("/control")) return send(200, state);
      return send(200, {});
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  return { url: `http://127.0.0.1:${server.address().port}`, requests, state, close: () => new Promise((r) => server.close(r)) };
}

test("defaultExecutionId: la variable propia o el build de CI", () => {
  const from = (vars) => (k) => vars[k];
  assert.equal(defaultExecutionId(from({})), "");
  assert.equal(defaultExecutionId(from({ BUILD_BUILDID: "4411" })), "azure-4411");
  assert.equal(defaultExecutionId(from({ GITHUB_RUN_ID: "99", GITHUB_RUN_ATTEMPT: "2" })), "github-99-2");
  assert.equal(defaultExecutionId(from({ TRACEREPORTS_EXECUTION_ID: "nocturna", BUILD_BUILDID: "1" })), "nocturna");
});

test("startRun envía execution_id y planned; setPlanned los actualiza", async () => {
  const srv = await liveServer();
  try {
    process.env.TRACEREPORTS_EXECUTION_ID = "lanzador-1";
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("suite", { planned: 12, branch: "", commit: "" });
    const created = srv.requests.find((r) => r.path === "/api/v1/runs").body;
    assert.equal(created.execution_id, "lanzador-1");
    assert.equal(created.planned, 12);
    await cr.setPlanned(14);
    assert.deepEqual(srv.requests.at(-1), { method: "POST", path: "/api/v1/runs/7/plan", body: { planned: 14 } });
  } finally {
    delete process.env.TRACEREPORTS_EXECUTION_ID;
    await srv.close();
  }
});

test("cancelRequested: solo con TRACEREPORTS_REMOTE_CONTROL, cada tanto, y queda cancelada", async () => {
  const srv = await liveServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("r", { branch: "", commit: "" });
    assert.deepEqual(await cr.cancelRequested(), { cancel: false, reason: "" });
    assert.ok(!srv.requests.some((r) => r.method === "GET"), "sin la variable no pregunta");
    process.env.TRACEREPORTS_REMOTE_CONTROL = "1";
    assert.deepEqual(await cr.cancelRequested(), { cancel: false, reason: "" });
    assert.deepEqual(await cr.cancelRequested(), { cancel: false, reason: "" }, "dentro de los 5 s no vuelve a preguntar");
    assert.equal(srv.requests.filter((r) => r.method === "GET").length, 1);
    Object.assign(srv.state, { cancel: true, reason: "QA caído" });
    assert.deepEqual(await cr.cancelRequested({ everyMs: 0 }), { cancel: true, reason: "QA caído" });
    srv.state.cancel = false;
    assert.deepEqual(await cr.cancelRequested({ everyMs: 0 }), { cancel: true, reason: "QA caído" }, "una vez cancelada, queda");
  } finally {
    delete process.env.TRACEREPORTS_REMOTE_CONTROL;
    await srv.close();
  }
});

test("un servidor anterior nunca cancela ni pierde evidencia", async () => {
  const srv = await liveServer({ old: true });
  try {
    process.env.TRACEREPORTS_REMOTE_CONTROL = "1";
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("r", { branch: "", commit: "" });
    await cr.setPlanned(3);
    assert.equal((await cr.cancelRequested({ everyMs: 0 })).cancel, false);
    assert.equal((await cr.cancelRequested({ everyMs: 0 })).cancel, false);
    assert.equal(srv.requests.filter((r) => r.path.endsWith("/control")).length, 1, "tras un 404 no vuelve a preguntar");
    assert.equal(cr.deliveryProblems(), 0);
  } finally {
    delete process.env.TRACEREPORTS_REMOTE_CONTROL;
    await srv.close();
  }
});

test("archivo de control: sin archivo nadie canceló; con cancelación, el motivo", () => {
  const file = newControlFile();
  try {
    assert.equal(cancelReason(file), null);
    assert.equal(cancelReason(""), null);
    writeCancel(file, "QA caído");
    assert.equal(cancelReason(file), "QA caído");
    // en una carpeta propia recién creada, no un nombre adivinable en la carpeta temporal
    assert.equal(path.dirname(path.dirname(file)), os.tmpdir());
    assert.match(path.basename(path.dirname(file)), /^tracereports-control-.{6}$/);
    const other = newControlFile();
    assert.notEqual(other, file);
    fs.rmSync(path.dirname(other), { recursive: true, force: true });
    if (process.platform !== "win32") assert.equal(fs.statSync(file).mode & 0o777, 0o600);
    assert.equal(skipMessage("QA caído"), "Cancelado desde TraceReports: QA caído");
    assert.equal(skipMessage(""), "Cancelado desde TraceReports");
  } finally {
    fs.rmSync(path.dirname(file), { recursive: true, force: true });
  }
});

test("reporter: tests planificados y la cancelación llega al archivo de los workers", async () => {
  const srv = await liveServer();
  process.env.TRACEREPORTS_REMOTE_CONTROL = "1";
  try {
    const rep = new TraceReportsReporter({ url: srv.url, runName: "r", project: "p", controlEveryMs: 20 });
    const file = process.env[CONTROL_FILE_ENV];
    assert.ok(file && path.isAbsolute(file), "la ruta queda en el entorno antes de que arranquen los workers");
    rep.onBegin({ rootDir: process.cwd(), projects: [{ name: "chromium" }] }, { allTests: () => [1, 2, 3, 4] });
    await rep.started;
    assert.equal(srv.requests.find((r) => r.path === "/api/v1/runs").body.planned, 4);
    Object.assign(srv.state, { cancel: true, reason: "se cayó QA" });
    for (let i = 0; i < 100 && cancelReason(file) === null; i++) await new Promise((r) => setTimeout(r, 20));
    assert.equal(cancelReason(file), "se cayó QA");
    await rep.onEnd({ status: "passed" });
    assert.ok(!fs.existsSync(file), "el archivo se borra al terminar");
  } finally {
    delete process.env.TRACEREPORTS_REMOTE_CONTROL;
    delete process.env[CONTROL_FILE_ENV];
    await srv.close();
  }
});
