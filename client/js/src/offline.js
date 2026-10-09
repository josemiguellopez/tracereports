// Grabación local cuando no hay servidor (no responde o rechaza el token): en vez de perder la
// evidencia, el cliente escribe en una carpeta las mismas llamadas a la API que habría hecho.
// Después: `tracereports report <carpeta>` arma el reporte HTML y `tracereports push <carpeta>`
// la sube al servidor. Mismo formato que el cliente Python (lo lee el binario: internal/offline).

import { randomInt, randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

export const FORMAT_VERSION = 1;
export const MARKER = "tracereports-offline.json";

const pad = (n) => String(n).padStart(2, "0");

// Ids locales: cada grabador reserva bloques de ID_BLOCK ids creando <carpeta>/ids/<bloque> en
// exclusiva, así dos grabadores que comparten la carpeta (mismo proceso, otro proceso u otra
// sesión) nunca repiten un id. El bloque es aleatorio en [MIN_BLOCK, MAX_BLOCK): no choca con los
// ids de grabaciones anteriores (pid % 10^6) y -(bloque * ID_BLOCK + n) sigue siendo un entero
// exacto en JavaScript (< 2^53). Mismo esquema en los clientes Python, Java y Go.
const ID_BLOCK = 100_000;
const MIN_BLOCK = 1_000_000;
const MAX_BLOCK = 90_000_000_000;

/** Carpeta nueva para una sesión dentro de `base` (no mezcla corridas distintas). */
export function sessionSlug(name) {
  const ascii = String(name || "").normalize("NFKD").replace(/[\u0300-\u036f]/g, "").toLowerCase();
  return (ascii.replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40).replace(/-+$/g, "") || "run");
}

export function newSessionDir(base, name = "") {
  const d = new Date();
  const stamp = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
  return path.join(base, `${sessionSlug(name)}-${stamp}-${randomUUID().replace(/-/g, "").slice(0, 6)}`);
}

/** Reemplaza al Sender: graba cada llamada y responde con ids locales (negativos). */
export class Recorder {
  constructor(directory) {
    this.directory = directory;
    this.stats = { sent: 0, retried: 0, rejected: 0, dropped: 0, lost: 0, recorded: 0 };
    this.seq = 0;
    this.nextId = 0;
    this.block = 0;
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

  // ids locales negativos, únicos entre los grabadores que graban en la misma carpeta
  localId() {
    if (!this.block || this.nextId >= ID_BLOCK - 1) {
      this.block = this.reserveBlock();
      this.nextId = 0;
    }
    this.nextId++;
    return -(this.block * ID_BLOCK + this.nextId);
  }

  reserveBlock() {
    const ids = path.join(this.directory, "ids");
    fs.mkdirSync(ids, { recursive: true });
    for (let i = 0; i < 100; i++) {
      const block = randomInt(MIN_BLOCK, MAX_BLOCK);
      try {
        fs.writeFileSync(path.join(ids, String(block)), "", { flag: "wx" });
        return block;
      } catch (err) {
        if (err.code !== "EEXIST") throw err; // ya es de otro grabador: se prueba otro
      }
    }
    throw new Error(`tracereports: could not reserve local ids in ${ids}`);
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
