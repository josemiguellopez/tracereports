import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { TraceReports } from "../src/index.js";
import { fakeServer } from "./fake-server.js";
process.env.TRACEREPORTS_ENV_FILE = "off";
process.env.TRACEREPORTS_BIN_DOWNLOAD ??= "0"; // los tests nunca descargan el binario de GitHub

test("artifact: Buffer o ruta, y un no-op sin test activo", async () => {
  const srv = await fakeServer();
  try {
    const cr = new TraceReports({ baseUrl: srv.url });
    await cr.startRun("x");
    const t = await cr.startTest("t");
    const file = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "tr-a-")), "mi-trace.zip");
    fs.writeFileSync(file, "PK\u0003\u0004z");
    assert.equal(t.artifact("trace", file), true);
    assert.equal(t.artifact("video", Buffer.from([0x1a, 0x45, 0xdf, 0xa3]), "grabación"), true);
    assert.equal(t.artifact("trace", path.join(path.dirname(file), "falta.zip")), false);
    await cr.finishRun();
    const bodies = srv.requests.filter((r) => r.path === "/api/v1/tests/11/artifact").map((r) => r.body.toString("latin1"));
    assert.equal(bodies.length, 2);
    assert.ok(bodies[0].includes("mi-trace.zip"), "the file name is the default name");
    assert.ok(bodies[1].includes("grabación") || bodies[1].includes(Buffer.from("grabación").toString("latin1")));
    const off = new TraceReports({ enabled: false });
    assert.equal((await off.startTest("y")).artifact("trace", Buffer.from("PK")), false);
  } finally {
    await srv.close();
  }
});
