// La grabación offline lleva la evidencia sin redactar (eso lo hace el servidor): solo el usuario
// la lee (carpetas 0700, archivos 0600).
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { Recorder } from "../src/offline.js";

test("la grabación queda solo para el usuario", { skip: process.platform === "win32" && "sin permisos POSIX" }, () => {
  const dir = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "tr-perm-")), "grabacion");
  const r = new Recorder(dir);
  r.record("POST", "/api/v1/runs", Buffer.from('{"name":"x"}'), "application/json");
  r.record("POST", "/api/v1/tests/-1/screenshot", Buffer.from("\x89PNG secreto"), "image/png");
  let files = 0;
  const walk = (p) => {
    const st = fs.statSync(p);
    if (st.isDirectory()) {
      assert.equal(st.mode & 0o777, 0o700, p);
      for (const n of fs.readdirSync(p)) walk(path.join(p, n));
    } else {
      assert.equal(st.mode & 0o777, 0o600, p);
      files++;
    }
  };
  walk(dir);
  assert.ok(files >= 3, `solo ${files} archivos`);
});
