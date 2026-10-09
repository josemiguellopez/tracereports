import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { TraceReports } from "../src/index.js";
import { fakeServer } from "./fake-server.js";

process.env.TRACEREPORTS_ENV_FILE = "off";
for (const key of ["RUN_ID", "DISABLED", "OFFLINE", "OFFLINE_DIR", "OFFLINE_KEEP"]) delete process.env[`TRACEREPORTS_${key}`];
const marker = dir => JSON.parse(fs.readFileSync(path.join(dir, "tracereports-offline.json")));
const events = dir => fs.readdirSync(dir).filter(n => /^events-.*\.jsonl$/.test(n)).flatMap(n => fs.readFileSync(path.join(dir, n), "utf8").trim().split("\n").filter(Boolean).map(JSON.parse));
const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), "tr-mirror-"));
const client = (url, dir) => new TraceReports({ baseUrl: url, offline: "both", offlineDir: dir, timeoutMs: 100, flushTimeoutMs: 100 });
async function evidence(cr, name = "login") {
  const t = await cr.startTest(name);
  t.info("step"); t.network([{ url: "https://example.test", status: 200, request_headers: { Authorization: "Bearer MIRROR_SECRET" } }]);
  t.finish("PASS");
  return t;
}

test("both sends once, records negative ids, retains KEEP and builds a masked report", async () => {
  const srv = await fakeServer(), dir = tmp();
  process.env.TRACEREPORTS_OFFLINE_KEEP = "1";
  process.env.TRACEREPORTS_OFFLINE_REPORT = "1";
  try {
    const cr = client(srv.url, dir);
    await cr.startRun("mirror"); await evidence(cr); await cr.finishRun();
    assert.equal(srv.requests.length, 6);
    const ev = events(dir); assert.equal(ev.length, 6);
    assert.ok(ev[0].local_id < 0 && ev[1].local_id < 0);
    assert.equal(ev[1].path, `/api/v1/runs/${ev[0].local_id}/tests`);
    assert.equal(ev[2].path, `/api/v1/tests/${ev[1].local_id}/logs`);
    assert.equal(marker(dir).mirror.complete, true);
    assert.equal(marker(dir).mirror.runs[0].server, cr.runId);
    assert.equal(Number(fs.readFileSync(path.join(dir, "ids", `server-${cr.runId}`))), ev[0].local_id);
    assert.equal(cr.reportUrl, `${srv.url}/#run=7&view=dashboard`);
    assert.ok(fs.existsSync(cr.offlineReport));
    const data = fs.readFileSync(path.join(dir, "report", "data.js"), "utf8");
    assert.match(data, /login/); assert.doesNotMatch(data, /MIRROR_SECRET/);
  } finally { delete process.env.TRACEREPORTS_OFFLINE_KEEP; await srv.close(); }
});

test("complete delivery and HTML remove raw data only", async () => {
  const srv = await fakeServer(), dir = tmp();
  process.env.TRACEREPORTS_OFFLINE_REPORT = "1";
  try {
    const cr = client(srv.url, dir); await cr.startRun("clean"); await evidence(cr); await cr.finishRun();
    assert.deepEqual(fs.readdirSync(dir).sort(), ["report", "tracereports-offline.json"]);
    assert.equal(marker(dir).raw_removed, true);
    await cr.finishRun();
    assert.equal(marker(dir).mirror.complete, true);
  } finally { await srv.close(); }
});

test("outage preserves tests started after failure and one local finish", async () => {
  const srv = await fakeServer(), dir = tmp();
  process.env.TRACEREPORTS_OFFLINE_REPORT = "1";
  const cr = client(srv.url, dir); await cr.startRun("outage"); await evidence(cr, "before"); await cr.flush(2000);
  await srv.close();
  const t = await evidence(cr, "after"); assert.ok(t.id < 0);
  await cr.finishRun();
  assert.equal(events(dir).length, 10);
  assert.equal(events(dir).filter(e => e.path.endsWith("/finish") && e.path.includes("/runs/")).length, 1);
  assert.equal(marker(dir).mirror.complete, false);
  assert.ok(fs.existsSync(cr.offlineReport));
  assert.match(fs.readFileSync(path.join(dir, "report", "data.js"), "utf8"), /after/);
});

test("initial failure uses record-only fallback with no mirror marker", async () => {
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  const dir = tmp(), cr = client("http://127.0.0.1:1", dir);
  await cr.startRun("fallback"); await evidence(cr); await cr.finishRun();
  assert.ok(cr.runId < 0); assert.equal(events(dir).length, 6); assert.equal(marker(dir).mirror, undefined);
});

test("missing binary keeps raw data and a complete delivery marker", async () => {
  const srv = await fakeServer(), dir = tmp(), bin = process.env.TRACEREPORTS_BIN;
  process.env.TRACEREPORTS_OFFLINE_REPORT = "1";
  process.env.TRACEREPORTS_BIN = path.join(dir, "not-installed");
  try {
    const cr = client(srv.url, dir); await cr.startRun("no binary"); await evidence(cr); await cr.finishRun();
    assert.equal(cr.offlineReport, null); assert.equal(events(dir).length, 6); assert.equal(marker(dir).mirror.complete, true);
  } finally { if (bin) process.env.TRACEREPORTS_BIN = bin; else delete process.env.TRACEREPORTS_BIN; await srv.close(); }
});

test("disk failure disables local writes once while server delivery continues", async () => {
  const srv = await fakeServer(), dir = tmp(), warnings = [], warn = console.warn;
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  console.warn = msg => warnings.push(msg);
  try {
    const cr = client(srv.url, dir); await cr.startRun("disk");
    fs.renameSync(cr.sender.recorder.file, cr.sender.recorder.file + ".saved");
    fs.mkdirSync(cr.sender.recorder.file); // append to a directory fails on every platform
    await evidence(cr); await cr.finishRun();
    assert.equal(srv.requests.length, 6);
    assert.equal(warnings.filter(w => w.includes("se detuvo la copia local")).length, 1);
    assert.equal(marker(dir).mirror.complete, false);
  } finally { console.warn = warn; await srv.close(); }
});

async function worker(url, dir, fail = false) {
  const source = `import { TraceReports } from ${JSON.stringify(new URL("../src/index.js", import.meta.url).href)};
    const cr = new TraceReports({baseUrl:${JSON.stringify(url)},offline:'both',offlineDir:${JSON.stringify(dir)},flushTimeoutMs:100});
    await cr.startRun('joined'); const t=await cr.startTest('worker');t.info('worker step');t.finish('PASS');
    ${fail ? "process.exit(0);" : "await cr.finishRun();"}`;
  const child = spawn(process.execPath, ["--input-type=module", "-e", source], { env: { ...process.env, TRACEREPORTS_RUN_ID: "7" }, stdio: ["ignore", "pipe", "pipe"] });
  let err = ""; child.stderr.on("data", b => err += b);
  await new Promise((resolve, reject) => { child.on("error", reject); child.on("exit", code => code === 0 ? resolve() : reject(new Error(err))); });
}

for (const crash of [false, true]) test(`shared process ${crash ? "crash retains raw" : "merges and cleans"}`, async () => {
  const srv = await fakeServer({ distinctIds: true }), dir = tmp();
  process.env.TRACEREPORTS_OFFLINE_REPORT = "1";
  try {
    const cr = client(srv.url, dir); await cr.startRun("shared"); await evidence(cr, "owner");
    await worker(srv.url, dir, crash); await cr.finishRun();
    assert.equal(marker(dir).mirror.complete, !crash);
    assert.equal(Boolean(marker(dir).raw_removed), !crash);
    assert.match(fs.readFileSync(path.join(dir, "report", "data.js"), "utf8"), /worker step/);
  } finally { await srv.close(); }
});

test("missing shared id warns and never claims complete", async () => {
  const srv = await fakeServer(), dir = tmp();
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  try {
    const cr = client(srv.url, dir); cr.joinRun(7); await evidence(cr); await cr.finishRun();
    assert.equal(marker(dir).mirror.complete, false); assert.equal(srv.requests.length, 4);
    assert.equal(events(dir).length, 4);
    assert.ok(events(dir).every(e => /^\/api\/v1\/(runs|tests)\/-\d+\//.test(e.path)));
  } finally { await srv.close(); }
});

// Un bloqueo que quedó de un proceso muerto (más de 30 s) se retira; uno reciente es de otro
// proceso y se informa con claridad.
test("un bloqueo huérfano no impide la copia local", async () => {
  const { Mirror } = await import("../src/mirror.js");
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "tr-lock-"));
  const old = path.join(base, "old"), fresh = path.join(base, "fresh");
  for (const d of [old, fresh]) fs.mkdirSync(path.join(d, ".mirror-lock"), { recursive: true });
  const past = new Date(Date.now() - 120_000);
  fs.utimesSync(path.join(old, ".mirror-lock"), past, past);
  const m = new Mirror(null, old, "http://server.test", 7, { name: "run" });
  assert.ok(m.runs.get(7) < 0);
  assert.ok(!fs.existsSync(path.join(old, ".mirror-lock")));
  assert.throws(() => new Mirror(null, fresh, "http://server.test", 7, { name: "run" }), /lo tiene otro proceso/);
  fs.rmSync(base, { recursive: true, force: true });
});

// Con TRACEREPORTS_OFFLINE_BASE cada corrida crea su carpeta dentro de la base: la segunda no choca
// con la copia de la primera (con una carpeta fija, la segunda quedaba sin copia).
test("corridas sucesivas guardan cada una su copia dentro de la base", async () => {
  const srv = await fakeServer(), base = path.join(tmp(), "output", "tracereports");
  process.env.TRACEREPORTS_OFFLINE_BASE = base;
  try {
    const dirs = [];
    for (let i = 0; i < 2; i++) {
      const cr = new TraceReports({ baseUrl: srv.url, offline: "both", timeoutMs: 100, flushTimeoutMs: 100 });
      await cr.startRun(`run ${i}`);
      await evidence(cr);
      await cr.finishRun();
      assert.ok(cr.recording, "each run keeps its local copy");
      dirs.push(cr.offlineDir);
      if (cr.offlineReport) assert.ok(fs.existsSync(cr.offlineReport) && cr.offlineReport.startsWith(cr.offlineDir));
    }
    assert.notEqual(dirs[0], dirs[1]);
    for (const d of dirs) assert.equal(path.dirname(d), base);
    // la carpeta exacta sigue mandando sobre la base
    const exact = path.join(tmp(), "exact");
    const cr = new TraceReports({ baseUrl: srv.url, offline: "both", offlineDir: exact, timeoutMs: 100, flushTimeoutMs: 100 });
    await cr.startRun("exact");
    assert.equal(cr.offlineDir, exact);
    await cr.finishRun();
  } finally {
    delete process.env.TRACEREPORTS_OFFLINE_BASE;
    await srv.close();
  }
});
