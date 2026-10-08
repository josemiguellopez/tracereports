// Grabación sin servidor: si la ejecución no se puede crear (servidor caído, token incorrecto o
// modo offline), la evidencia se graba en una carpeta en vez de perderse.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { TraceReports } from "../src/index.js";
import { MARKER } from "../src/offline.js";
import { limits } from "../src/transport.js";
// los tests corren dentro del repositorio: que el cliente no lea el .env real del proyecto
process.env.TRACEREPORTS_ENV_FILE = "off";
for (const v of ["TRACEREPORTS_RUN_ID", "TRACEREPORTS_DISABLED", "TRACEREPORTS_TOKEN", "TRACEREPORTS_OFFLINE_DIR", "TRACEREPORTS_OFFLINE"]) delete process.env[v];
limits.circuitMs = 300;

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), "tr-offline-"));
const PNG = Buffer.from("89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de0000000c49444154789c63f80f00000101000518d84e0000000049454e44ae426082", "hex");

function events(dir) {
  return fs.readdirSync(dir).filter((n) => n.startsWith("events-")).sort()
    .flatMap((n) => fs.readFileSync(path.join(dir, n), "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l)));
}

/** Servidor que rechaza todo con 401 (token equivocado) y cuenta las llamadas. */
async function unauthorized() {
  let calls = 0;
  const server = http.createServer((req, res) => {
    calls++;
    req.resume();
    res.writeHead(401, { "Content-Type": "application/json" }).end('{"error":"invalid token"}');
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  return { url: `http://127.0.0.1:${server.address().port}`, calls: () => calls, close: () => new Promise((r) => server.close(r)) };
}

async function runSuite(cr) {
  await cr.startRun("Checkout", { environment: "qa", project: "shop", branch: "dev", commit: "abc" });
  const t = await cr.startTest("login", { key: "login.spec.js > login" });
  t.info("abrir login");
  t.screenshot(PNG, "pantalla");
  t.network([{ method: "POST", url: "https://api/login", status: 500, request_headers: { Authorization: "Bearer SECRET" } }]);
  t.finish("FAIL", { errorMessage: "boom" });
  await cr.finishRun();
}

test("token equivocado: se graba en vez de perder todo", async () => {
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  const srv = await unauthorized();
  try {
    const dir = path.join(tmp(), "rec");
    const cr = new TraceReports({ baseUrl: srv.url, token: "wrong", offlineDir: dir, timeoutMs: 300 });
    await runSuite(cr);
    assert.ok(cr.recording);
    assert.equal(cr.offlineDir, dir);
    assert.ok(cr.runId < 0);
    const evs = events(dir);
    const run = evs[0].local_id, tst = evs[1].local_id;
    assert.deepEqual(evs.map((e) => `${e.method} ${e.path}`), [
      "POST /api/v1/runs",
      `POST /api/v1/runs/${run}/tests`,
      `POST /api/v1/tests/${tst}/logs`,
      `POST /api/v1/tests/${tst}/screenshot`,
      `POST /api/v1/tests/${tst}/network`,
      `PATCH /api/v1/tests/${tst}/finish`,
      `PATCH /api/v1/runs/${run}/finish`,
    ]);
    assert.ok(run < 0 && tst < 0 && run !== tst);
    assert.equal(evs[0].body.name, "Checkout");
    assert.ok(evs.every((e) => e.ts > 0));
    const shot = evs[3];
    assert.ok(shot.content_type.startsWith("multipart/form-data") && !("body" in shot));
    assert.ok(fs.readFileSync(path.join(dir, shot.body_file)).includes(PNG));
    const marker = JSON.parse(fs.readFileSync(path.join(dir, MARKER), "utf8"));
    assert.equal(marker.format, "tracereports-offline");
    assert.equal(marker.version, 1);
    assert.equal(cr.delivery.recorded, 7);
    assert.equal(cr.deliveryProblems(), 0);
  } finally {
    await srv.close();
  }
});

test("servidor caído: una carpeta nueva por sesión dentro de tracereports-offline", async () => {
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  const cwd = process.cwd();
  process.chdir(tmp());
  try {
    const cr = new TraceReports({ baseUrl: "http://127.0.0.1:9", timeoutMs: 300 });
    await runSuite(cr);
    assert.ok(cr.recording);
    assert.equal(path.dirname(cr.offlineDir), "tracereports-offline");
    assert.equal(events(cr.offlineDir).length, 7);
  } finally {
    process.chdir(cwd);
  }
});

test("offline: off no graba nada; un cliente apagado tampoco", async () => {
  const cwd = process.cwd();
  const dir = tmp();
  process.chdir(dir);
  try {
    const off = new TraceReports({ baseUrl: "http://127.0.0.1:9", timeoutMs: 200, offline: "off" });
    await runSuite(off);
    const disabled = new TraceReports({ baseUrl: "http://127.0.0.1:9", enabled: false });
    await runSuite(disabled);
    assert.ok(!off.recording && !disabled.recording);
    assert.ok(!fs.existsSync(path.join(dir, "tracereports-offline")));
  } finally {
    process.chdir(cwd);
  }
});

test("offline: always nunca contacta a un servidor", async () => {
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  const srv = await unauthorized();
  try {
    const dir = tmp();
    await runSuite(new TraceReports({ baseUrl: srv.url, offline: "always", offlineDir: dir }));
    assert.equal(srv.calls(), 0);
    assert.equal(events(dir).length, 7);
  } finally {
    await srv.close();
  }
});

test("joinRun con id negativo: otro proceso graba en la misma carpeta", async () => {
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  const dir = tmp();
  const owner = new TraceReports({ offline: "always", offlineDir: dir });
  const runId = await owner.startRun("shards");
  const shard = new TraceReports({ baseUrl: "http://127.0.0.1:9" });
  shard.joinRun(runId, { offlineDir: dir });
  assert.ok(shard.recording);
  assert.equal(shard.offlineDir, dir);
  const t = await shard.startTest("a");
  t.finish("PASS");
  await owner.finishRun();
  assert.equal(fs.readdirSync(dir).filter((n) => n.startsWith("events-")).length, 2);
  assert.ok(events(dir).some((e) => e.path === `/api/v1/runs/${runId}/tests`));
});

const binary = process.env.TRACEREPORTS_BIN;
test("con el binario: finishRun arma el reporte HTML sin servidor", { skip: !binary && "needs $TRACEREPORTS_BIN" }, async () => {
  delete process.env.TRACEREPORTS_OFFLINE_REPORT;
  const srv = await unauthorized();
  try {
    const dir = path.join(tmp(), "rec");
    const cr = new TraceReports({ baseUrl: srv.url, token: "wrong", offlineDir: dir, timeoutMs: 300 });
    await runSuite(cr);
    assert.ok(cr.offlineReport && fs.existsSync(cr.offlineReport), cr.offlineReport);
    assert.equal(cr.reportUrl, cr.offlineReport);
    const data = fs.readFileSync(path.join(path.dirname(cr.offlineReport), "data.js"), "utf8");
    assert.ok(data.includes("Checkout") && data.includes("abrir login"));
    assert.ok(!data.includes("SECRET"), "masking runs without a server too");
    const out = path.join(tmp(), "manual");
    assert.equal(spawnSync(binary, ["report", "-o", out, dir]).status, 0);
    assert.ok(fs.existsSync(path.join(out, "index.html")));
  } finally {
    await srv.close();
  }
});

// Dos grabadores que comparten la carpeta (aquí en el mismo proceso: mismo pid) no repiten ids:
// cada uno reserva su bloque en ids/. Ids exactos en JS y fuera del rango de grabaciones viejas.
test("dos grabadores en la misma carpeta no repiten ids locales", async () => {
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  const dir = path.join(tmp(), "rec");
  const a = new TraceReports({ offline: "always", offlineDir: dir });
  const b = new TraceReports({ offline: "always", offlineDir: dir });
  const ra = await a.startRun("suite A");
  const rb = await b.startRun("suite B");
  const ta = await a.startTest("test A");
  const tb = await b.startTest("test B");
  b.sender.nextId = 100_000 - 1; // el bloque de B se agota: reserva otro
  const tb2 = await b.startTest("test B2");
  ta.finish("PASS");
  tb.finish("FAIL", { errorMessage: "only B failed" });
  tb2.finish("PASS");
  await a.finishRun();
  await b.finishRun();
  const ids = [ra, rb, ta.id, tb.id, tb2.id];
  assert.equal(new Set(ids).size, ids.length, `ids repetidos: ${ids}`);
  for (const id of ids) assert.ok(id < -1e11 && Number.isSafeInteger(id), `id ${id}`);
  assert.equal(fs.readdirSync(path.join(dir, "ids")).length, 3);
  assert.deepEqual(events(dir).filter((e) => e.local_id).map((e) => e.local_id).sort(), [...ids].sort());
  if (!binary) return;
  const out = path.join(tmp(), "report");
  const res = spawnSync(binary, ["report", dir, "-o", out], { encoding: "utf8" });
  assert.equal(res.status, 0, res.stderr);
  const runs = fs.readdirSync(out).filter((x) => x.startsWith("run-")).map((x) => {
    const js = fs.readFileSync(path.join(out, x, "data.js"), "utf8");
    const d = JSON.parse(js.slice("window.TRACEREPORTS_STATIC = ".length).trim().replace(/;$/, ""));
    return [d.run.name, d.run.tests.map((t) => t.name).sort()];
  });
  assert.deepEqual(Object.fromEntries(runs), { "suite A": ["test A"], "suite B": ["test B", "test B2"] });
});
