// flush() y abandon() del transporte: no dejan temporizadores ni waiters vivos, así el proceso de
// tests termina apenas se vacía la cola (sin esperar el timeout de flush, 30 s por defecto).
// Todo con fetch simulado: nada sale a la red.
import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { Sender, limits } from "../src/transport.js";

const realFetch = globalThis.fetch;
test.afterEach(() => { globalThis.fetch = realFetch; });

const ok = () => new Response("{}", { status: 200 });
const delay = (ms) => new Promise((r) => setTimeout(r, ms));
/** fetch que responde después de ms (respetando el abort del Sender). */
const slowFetch = (ms, res = ok) => (_url, { signal }) => new Promise((resolve, reject) => {
  const t = setTimeout(() => resolve(res()), ms);
  signal?.addEventListener("abort", () => { clearTimeout(t); reject(signal.reason); });
});
const timers = () => process.getActiveResourcesInfo().filter((r) => r === "Timeout").length;
const sender = () => new Sender("http://fake.invalid", () => ({}));

test("cola vacía: flush responde al instante sin temporizadores ni waiters", { timeout: 8000 }, async () => {
  const s = sender();
  const before = timers();
  assert.equal(await s.flush(30_000), 0);
  assert.equal(s.waiters.length, 0);
  assert.equal(timers(), before);
});

test("el envío termina antes del timeout: flush vuelve sin dejar el temporizador", { timeout: 8000 }, async () => {
  globalThis.fetch = slowFetch(20);
  const s = sender();
  const before = timers();
  for (let i = 0; i < 3; i++) s.enqueue("POST", "/x", "{}", "application/json", 5000);
  const t0 = Date.now();
  assert.equal(await s.flush(30_000), 0);
  assert.ok(Date.now() - t0 < 2000, "returns when drained");
  assert.equal(s.waiters.length, 0, "no waiter left");
  await delay(10);
  assert.equal(timers(), before, "the flush timeout was cancelled");
});

test("el timeout vence con eventos pendientes: devuelve lo pendiente y retira su waiter", { timeout: 8000 }, async () => {
  globalThis.fetch = slowFetch(10_000);
  const s = sender();
  s.enqueue("POST", "/x", "{}", "application/json", 300); // el envío aborta a los 300 ms y se reintenta
  const pending = await s.flush(80);
  assert.ok(pending >= 1);
  assert.equal(s.waiters.length, 0, "the expired waiter is removed");
  assert.equal(s.abandon(), 1);
  assert.equal(s.stats.lost, 1);
  await delay(400); // el envío simulado aborta a los 300 ms: no deja nada vivo para el test siguiente
});

test("varios flush a la vez: todos vuelven al vaciarse, sin waiters ni temporizadores", { timeout: 8000 }, async () => {
  globalThis.fetch = slowFetch(30);
  const s = sender();
  const before = timers();
  for (let i = 0; i < 4; i++) s.enqueue("POST", "/x", "{}", "application/json", 5000);
  const got = await Promise.all([s.flush(30_000), s.flush(30_000), s.flush(10_000)]);
  assert.deepEqual(got, [0, 0, 0]);
  assert.equal(s.waiters.length, 0);
  await delay(10);
  assert.equal(timers(), before);
});

test("abandon durante el backoff: cancela la espera y libera los flush pendientes", { timeout: 8000 }, async () => {
  const old = limits.maxBackoffMs;
  limits.maxBackoffMs = 20_000;
  try {
    globalThis.fetch = async () => new Response("down", { status: 503 }); // reintento con backoff
    const s = sender();
    const before = timers();
    s.enqueue("POST", "/x", "{}", "application/json", 5000);
    await delay(50); // primer intento hecho: ahora espera su backoff
    const waiting = s.flush(30_000);
    assert.equal(s.abandon(), 1);
    assert.equal(await waiting, 0, "a pending flush is released by abandon");
    await delay(20);
    assert.equal(s.waiters.length, 0);
    assert.equal(timers(), before, "no backoff timer keeps the process alive");
  } finally {
    limits.maxBackoffMs = old;
  }
});

test("un proceso Node termina solo apenas se vacía la cola, sin esperar el timeout", { timeout: 25000 }, () => {
  const transport = pathToFileURL(fileURLToPath(new URL("../src/transport.js", import.meta.url))).href;
  const script = `
    import { Sender } from ${JSON.stringify(transport)};
    globalThis.fetch = () => new Promise((r) => setTimeout(() => r(new Response("{}", { status: 200 })), 20));
    const s = new Sender("http://fake.invalid", () => ({}));
    for (let i = 0; i < 3; i++) s.enqueue("POST", "/x", "{}", "application/json", 5000);
    const t0 = Date.now();
    const left = await s.flush(3000);
    process.stdout.write(JSON.stringify({ left, flushMs: Date.now() - t0 }));
  `;
  const t0 = Date.now();
  const r = spawnSync(process.execPath, ["--input-type=module", "-e", script], { encoding: "utf8", timeout: 20_000 });
  const total = Date.now() - t0;
  assert.equal(r.status, 0, r.stderr);
  const out = JSON.parse(r.stdout);
  assert.equal(out.left, 0);
  // sin el arreglo el proceso vive hasta el timeout de flush (3 s); con él, sale enseguida
  assert.ok(total < 2500, `the process took ${total} ms to exit (flush took ${out.flushMs} ms)`);
});
