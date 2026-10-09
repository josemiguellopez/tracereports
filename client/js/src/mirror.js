// Copia de cada llamada lógica; los reintentos siguen siendo responsabilidad del Sender.
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { Recorder, MARKER } from "./offline.js";

// Un proceso tiene el bloqueo unos milisegundos: uno más viejo quedó de un proceso que murió.
const STALE_LOCK_MS = 30_000;

export class Mirror {
  constructor(sender, directory, server, runId, payload) {
    this.sender = sender;
    this.directory = directory;
    this.runs = new Map();
    this.tests = new Map();
    this.failed = false;
    this.closed = false;
    this.disabled = false;
    this.active = 0;
    fs.mkdirSync(directory, { recursive: true });
    this.locked(() => {
      const marker = path.join(directory, MARKER);
      if (fs.existsSync(marker) && JSON.parse(fs.readFileSync(marker)).raw_removed) throw new Error("la carpeta solo contiene el reporte; usa otra carpeta");
      this.recorder = new Recorder(directory);
      this.status = `status-${process.pid}-${this.recorder.tag}.json`;
      this.write(this.status, { complete: false, reason: "active" });
      const m = JSON.parse(fs.readFileSync(marker));
      if (m.mirror && m.mirror.server !== server) throw new Error("la carpeta pertenece a otro servidor");
      m.mirror ||= { server, runs: [], complete: false };
      m.mirror.complete = false;
      this.write(MARKER, m);
      let local;
      const mapping = path.join(directory, "ids", `server-${runId}`);
      if (payload) {
        local = this.recorder.record("POST", "/api/v1/runs", JSON.stringify(payload), "application/json").run_id;
        fs.writeFileSync(mapping, String(local), { flag: "wx" });
        m.mirror.runs.push({ local, server: runId });
        this.write(MARKER, m);
      } else {
        try {
          local = Number(fs.readFileSync(mapping, "utf8"));
          if (!Number.isSafeInteger(local) || local >= 0) throw new Error("id local inválido");
        } catch (err) {
          this.failed = true;
          console.warn(`tracereports: falta el id local de la ejecución #${runId}: ${err.message}`);
        }
      }
      if (local) this.runs.set(runId, local);
    });
  }

  locked(fn) {
    const lock = path.join(this.directory, ".mirror-lock");
    for (let n = 0; ; n++) {
      try { fs.mkdirSync(lock); break; } catch (err) {
        if (err.code !== "EEXIST") throw err;
        if (n >= 50) {
          let stale = false;
          try { stale = Date.now() - fs.statSync(lock).mtimeMs > STALE_LOCK_MS; } catch { /* ya no existe */ }
          if (!stale) throw new Error(`${lock} lo tiene otro proceso (bórralo si ninguna ejecución usa esta carpeta)`);
          try { fs.rmdirSync(lock); } catch { /* otro proceso lo retiró antes */ }
          n = 0; // huérfano: se retira y se vuelve a intentar
        }
        Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 10);
      }
    }
    try { return fn(); } finally { fs.rmdirSync(lock); }
  }

  write(name, value) {
    const dest = path.join(this.directory, name), tmp = `${dest}.${randomUUID()}.tmp`;
    fs.writeFileSync(tmp, JSON.stringify(value));
    fs.renameSync(tmp, dest);
  }

  diskError(err) {
    this.failed = true;
    if (!this.disabled) console.warn(`tracereports: se detuvo la copia local en ${this.directory}: ${err.message}`);
    this.disabled = true;
  }

  record(method, apiPath, body, contentType) {
    if (this.disabled || this.closed) return null;
    try {
      const localPath = apiPath.replace(/^\/api\/v1\/(runs|tests)\/(-?\d+)/, (match, kind, id) => {
        const ids = kind === "runs" ? this.runs : this.tests;
        let local = ids.get(Number(id));
        if (Number(id) > 0 && !local) {
          this.failed = true;
          // Referencia local sin creación: replay la omite, nunca modifica un run remoto ajeno.
          local = this.recorder.localId();
          ids.set(Number(id), local);
        }
        return `/api/v1/${kind}/${local ?? id}`;
      });
      return this.recorder.record(method, localPath, body, contentType);
    } catch (err) { this.diskError(err); return null; }
  }

  async sendNow(method, apiPath, body, contentType, ...args) {
    this.active++;
    const local = this.record(method, apiPath, body, contentType);
    try {
      const remote = /\/(runs|tests)\/-\d+/.test(apiPath) ? null : await this.sender.sendNow(method, apiPath, body, contentType, ...args);
      if (remote == null) this.failed = true;
      if (local?.test_id && remote?.test_id) this.tests.set(remote.test_id, local.test_id);
      // Si el servidor ya no crea tests, la evidencia posterior sigue usando el id local.
      return remote ?? (local?.test_id ? local : null);
    } finally { this.active--; }
  }

  enqueue(method, apiPath, body, contentType, ...args) {
    this.record(method, apiPath, body, contentType);
    if (/\/(runs|tests)\/-\d+/.test(apiPath)) { this.failed = true; return false; }
    const ok = this.sender.enqueue(method, apiPath, body, contentType, ...args);
    if (!ok) this.failed = true;
    return ok;
  }

  get stats() { return { ...this.sender.stats, recorded: this.recorder.stats.recorded }; }
  get pending() { return this.sender.pending; }
  serverDown() { return this.sender.serverDown(); }
  flush(...args) { return this.sender.flush(...args); }
  abandon() { return this.sender.abandon(); }

  close(complete) {
    if (this.closed) return;
    this.closed = true;
    try {
      const ok = complete && !this.failed && this.active === 0;
      this.locked(() => this.write(this.status, { complete: ok, reason: ok ? "" : "delivery or recording incomplete" }));
    } catch (err) { this.diskError(err); }
  }

  snapshot() {
    const files = fs.readdirSync(this.directory).filter(n => /^status-.*\.json$/.test(n)).sort();
    const events = fs.readdirSync(this.directory).filter(n => /^events-.*\.jsonl$/.test(n));
    if (!files.length || events.some(n => !files.includes(n.replace(/^events-/, "status-").replace(/\.jsonl$/, ".json")))) return null;
    const entries = files.map(n => [n, fs.readFileSync(path.join(this.directory, n), "utf8")]);
    return entries.every(([, raw]) => JSON.parse(raw).complete === true) ? JSON.stringify(entries) : null;
  }

  prepare() {
    try { return this.locked(() => this.snapshot()); } catch (err) { this.diskError(err); return null; }
  }

  finalize(report, keep, before) {
    try {
      this.locked(() => {
        const snapshot = this.snapshot();
        const marker = JSON.parse(fs.readFileSync(path.join(this.directory, MARKER)));
        marker.mirror.complete = !this.failed && snapshot !== null;
        const remove = marker.mirror.complete && before === snapshot && report && fs.existsSync(report) && !keep;
        // Se marca antes de borrar: una interrupción no deja una grabación parcial subible.
        if (remove) marker.raw_removed = true;
        this.write(MARKER, marker);
        if (remove) {
          for (const n of fs.readdirSync(this.directory)) {
            if (/^(events-.*\.jsonl|status-.*\.json)$/.test(n) || ["bodies", "ids"].includes(n)) fs.rmSync(path.join(this.directory, n), { recursive: true, force: true });
          }
        }
      });
    } catch (err) { this.diskError(err); }
  }
}
