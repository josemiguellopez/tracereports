// Una grabación con un evento de más de 16 MiB (una conexión con un request body grande, que el
// cliente envía sola) la lee el CLI: report la convierte en reporte.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { TraceReports } from "../src/index.js";

process.env.TRACEREPORTS_ENV_FILE = "off";
const binary = (process.env.TRACEREPORTS_BIN || process.env.TRACEREPORTS_TEST_BIN);

test("un evento de más de 16 MiB grabado sin servidor se puede abrir con report", { skip: !binary && "needs $TRACEREPORTS_BIN" }, async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tr-offline-large-"));
  const prev = process.env.TRACEREPORTS_OFFLINE_REPORT;
  process.env.TRACEREPORTS_OFFLINE_REPORT = "0";
  try {
    const rec = path.join(dir, "rec");
    const cr = new TraceReports({ offline: "always", offlineDir: rec });
    await cr.startRun("offline-large");
    const t = await cr.startTest("large POST");
    t.info("antes del upload");
    t.network([{ method: "POST", url: "https://app/api/upload", status: 500, post_data: "ñ\"x".repeat(4 << 20), response_body: "{}" }]);
    t.info("después del upload");
    t.finish("FAIL");
    await cr.finishRun();
    const file = fs.readdirSync(rec).find((x) => x.startsWith("events-"));
    const lines = fs.readFileSync(path.join(rec, file), "utf8").split("\n").filter(Boolean);
    assert.ok(Math.max(...lines.map((l) => Buffer.byteLength(l))) > 16 << 20, "the fixture writes a line over 16 MiB");
    const out = path.join(dir, "report");
    const res = spawnSync(binary, ["report", rec, "-o", out], { encoding: "utf8", timeout: 120000 });
    assert.equal(res.status, 0, res.stderr);
    const data = fs.readFileSync(path.join(out, "data.js"), "utf8");
    assert.ok(data.includes("https://app/api/upload") && data.indexOf("antes del upload") < data.indexOf("después del upload"));
  } finally {
    if (prev === undefined) delete process.env.TRACEREPORTS_OFFLINE_REPORT; else process.env.TRACEREPORTS_OFFLINE_REPORT = prev;
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
