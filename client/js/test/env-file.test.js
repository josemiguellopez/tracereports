import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import http from "node:http";
import { env, envFileInUse, parseEnvFile, resetEnvFile } from "../src/env.js";
import { TraceReports } from "../src/index.js";

// El cliente lee TRACEREPORTS_* del .env del proyecto cuando no están en el entorno.

const KEYS = ["TRACEREPORTS_ENV_FILE", "TRACEREPORTS_TOKEN", "TRACEREPORTS_URL"];
let saved, cwd, base, root, sub;

beforeEach(() => {
  saved = Object.fromEntries(KEYS.map((k) => [k, process.env[k]]));
  for (const k of KEYS) delete process.env[k];
  cwd = process.cwd();
  base = mkdtempSync(path.join(tmpdir(), "tr-env-")); // base/repo/tests/e2e
  root = path.join(base, "repo");
  mkdirSync(path.join(root, ".git"), { recursive: true });
  sub = path.join(root, "tests", "e2e");
  mkdirSync(sub, { recursive: true });
  process.chdir(sub);
  resetEnvFile();
});

afterEach(() => {
  process.chdir(cwd);
  rmSync(base, { recursive: true, force: true });
  for (const k of KEYS) {
    if (saved[k] === undefined) delete process.env[k];
    else process.env[k] = saved[k];
  }
  resetEnvFile();
});

test(".env: mismas reglas que el servidor", () => {
  const f = path.join(root, "x.env");
  writeFileSync(f, "# c\n\nexport TRACEREPORTS_TOKEN=abc\nTRACEREPORTS_URL = 'http://x:1'  \n" +
    'TRACEREPORTS_RUN_NAME="con # dentro"\nTRACEREPORTS_ENV=qa # fin\nMALA LINEA=1\nsin_igual\n');
  assert.deepEqual(parseEnvFile(f), {
    TRACEREPORTS_TOKEN: "abc", TRACEREPORTS_URL: "http://x:1",
    TRACEREPORTS_RUN_NAME: "con # dentro", TRACEREPORTS_ENV: "qa",
  });
});

test(".env: se busca hacia arriba hasta la raíz del repositorio y solo usa claves del cliente", () => {
  writeFileSync(path.join(root, ".env"), "TRACEREPORTS_TOKEN=desde-env\nAI_API_KEY=no\n");
  assert.equal(env("TOKEN"), "desde-env");
  assert.equal(envFileInUse(), path.join(root, ".env"));
  assert.equal(env("API_KEY"), undefined);
});

test(".env: el entorno gana", () => {
  writeFileSync(path.join(root, ".env"), "TRACEREPORTS_TOKEN=del-archivo\n");
  assert.equal(env("TOKEN"), "del-archivo");
  process.env.TRACEREPORTS_TOKEN = "secret-de-ci";
  assert.equal(env("TOKEN"), "secret-de-ci");
});

test(".env: no sale del repositorio; archivo explícito y off", () => {
  // un .env por encima de la raíz del repositorio (p. ej. en la carpeta del usuario) no se usa
  writeFileSync(path.join(base, ".env"), "TRACEREPORTS_TOKEN=ajeno\n");
  assert.equal(env("TOKEN"), undefined);
  assert.equal(envFileInUse(), null);
  writeFileSync(path.join(base, "otro.env"), "TRACEREPORTS_TOKEN=otro\n");
  process.env.TRACEREPORTS_ENV_FILE = path.join(base, "otro.env");
  resetEnvFile();
  assert.equal(env("TOKEN"), "otro");
  process.env.TRACEREPORTS_ENV_FILE = "off";
  resetEnvFile();
  assert.equal(env("TOKEN"), undefined);
});

test(".env: el cliente envía el token del archivo", async () => {
  const seen = [];
  const srv = http.createServer((req, res) => {
    req.resume();
    req.on("end", () => {
      seen.push(req.headers.authorization);
      res.writeHead(201, { "Content-Type": "application/json" }).end(JSON.stringify({ run_id: 1 }));
    });
  });
  await new Promise((r) => srv.listen(0, "127.0.0.1", r));
  try {
    writeFileSync(path.join(root, ".env"),
      `TRACEREPORTS_URL=http://127.0.0.1:${srv.address().port}\nTRACEREPORTS_TOKEN=tok-del-env\n`);
    const cr = new TraceReports();
    assert.equal(await cr.startRun("r"), 1);
    assert.deepEqual(seen, ["Bearer tok-del-env"]);
  } finally {
    srv.close();
  }
});

// Definida en el entorno, aunque vacía, manda sobre el .env (como en el servidor): vacía es "sin
// configurar" y el valor del archivo no se recupera. Ausente, sí se lee del archivo.
test(".env: una variable vacía en el entorno no se reemplaza con la del archivo", async () => {
  writeFileSync(path.join(root, ".env"), "TRACEREPORTS_TOKEN=fake-file-token\nTRACEREPORTS_URL=http://127.0.0.1:9\n");
  assert.equal(env("TOKEN"), "fake-file-token"); // ausente: del archivo
  process.env.TRACEREPORTS_TOKEN = "fake-env-token";
  assert.equal(env("TOKEN"), "fake-env-token"); // con valor: del entorno
  process.env.TRACEREPORTS_TOKEN = "";
  assert.equal(env("TOKEN"), undefined); // vacía: sin configurar, no el archivo
  assert.equal(env("TOKEN", "por-defecto"), "por-defecto");
  process.env.TRACEREPORTS_ENV_FILE = "off";
  resetEnvFile();
  assert.equal(env("TOKEN"), undefined);
  delete process.env.TRACEREPORTS_TOKEN;
  assert.equal(env("TOKEN"), undefined); // archivo apagado y ausente
  assert.equal(env("URL"), undefined);

  // el cliente tampoco envía el token del archivo
  delete process.env.TRACEREPORTS_ENV_FILE;
  resetEnvFile();
  const seen = [];
  const srv = http.createServer((req, res) => {
    req.resume();
    req.on("end", () => {
      seen.push(req.headers.authorization);
      res.writeHead(201, { "Content-Type": "application/json" }).end(JSON.stringify({ run_id: 1 }));
    });
  });
  await new Promise((r) => srv.listen(0, "127.0.0.1", r));
  try {
    process.env.TRACEREPORTS_URL = `http://127.0.0.1:${srv.address().port}`;
    process.env.TRACEREPORTS_TOKEN = "";
    const cr = new TraceReports();
    assert.equal(await cr.startRun("r"), 1);
    assert.deepEqual(seen, [undefined]);
  } finally {
    srv.close();
  }
});
