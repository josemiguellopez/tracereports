// Grabación local cuando no hay servidor (no responde o rechaza el token): en vez de perder la
// evidencia, el cliente escribe en una carpeta las mismas llamadas a la API que habría hecho.
// Después: `tracereports report <carpeta>` arma el reporte HTML y `tracereports push <carpeta>`
// la sube al servidor. Mismo formato que el cliente Python (lo lee el binario: internal/offline).

import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

export const FORMAT_VERSION = 1;
export const MARKER = "tracereports-offline.json";

const pad = (n) => String(n).padStart(2, "0");

/** Carpeta nueva para una sesión dentro de `base` (no mezcla corridas distintas). */
export function newSessionDir(base) {
  const d = new Date();
  const stamp = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
  return path.join(base, `${stamp}-${randomUUID().slice(0, 6)}`);
}

/** Reemplaza al Sender: graba cada llamada y responde con ids locales (negativos). */
export class Recorder {
  constructor(directory) {
    this.directory = directory;
    this.stats = { sent: 0, retried: 0, rejected: 0, dropped: 0, lost: 0, recorded: 0 };
    this.seq = 0;
    this.nextId = 0;
    this.tag = randomUUID().slice(0, 8);
    fs.mkdirSync(path.join(directory, "bodies"), { recursive: true });
    try {
      fs.writeFileSync(path.join(directory, MARKER),
        JSON.stringify({ format: "tracereports-offline", version: FORMAT_VERSION, id: randomUUID().replace(/-/g, "") }), { flag: "wx" });
    } catch (err) {
      if (err.code !== "EEXIST") throw err; // otro proceso ya lo creó
    }
    this.file = path.join(directory, `events-${process.pid}-${this.tag}.jsonl`);
  }

  // ids locales negativos, únicos entre procesos que graban en la misma carpeta
  localId() {
    this.nextId++;
    return -((process.pid % 1_000_000) * 100_000 + this.nextId);
  }

  record(method, apiPath, body, contentType) {
    this.seq++;
    const event = { seq: this.seq, ts: Date.now(), method, path: apiPath, content_type: contentType };
    let parsed;
    if (contentType.startsWith("application/json")) {
      try {
        parsed = JSON.parse(body?.length ? body.toString() : "{}");
      } catch {
        parsed = undefined;
      }
    }
    if (parsed !== undefined) event.body = parsed;
    else {
      const name = `bodies/${process.pid}-${this.tag}-${this.seq}.bin`;
      fs.writeFileSync(path.join(this.directory, name), body ?? "");
      event.body_file = name;
    }
    let res = {};
    if (method === "POST" && apiPath === "/api/v1/runs") res = { run_id: this.localId() };
    else if (method === "POST" && apiPath.startsWith("/api/v1/runs/") && apiPath.endsWith("/tests")) res = { test_id: this.localId() };
    const id = res.run_id ?? res.test_id;
    if (id) event.local_id = id;
    fs.appendFileSync(this.file, JSON.stringify(event) + "\n"); // síncrono: si el proceso muere, lo escrito queda
    this.stats.recorded++;
    return res;
  }

  // ─── interfaz del Sender
  async sendNow(method, apiPath, body, contentType) {
    return this.record(method, apiPath, body, contentType);
  }

  enqueue(method, apiPath, body, contentType) {
    this.record(method, apiPath, body, contentType);
    return true;
  }

  get pending() {
    return 0;
  }

  serverDown() {
    return false;
  }

  async flush() {
    return 0;
  }

  abandon() {
    return 0;
  }
}
