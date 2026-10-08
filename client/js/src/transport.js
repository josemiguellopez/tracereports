// Transporte del cliente: la evidencia sale en segundo plano, sin frenar los tests.
//
// - Cola acotada (cantidad y bytes) que se envía en orden, de a un evento.
// - Reintentos con backoff ante caídas de red, timeouts, 5xx, 408 y 429. Cada envío lleva una
//   Idempotency-Key: si el servidor ya lo aplicó, el reintento no lo duplica.
// - Circuito: si el servidor no responde, las llamadas que necesitan respuesta (crear ejecución o
//   test) fallan al instante durante un rato en vez de esperar su timeout en cada test.
// - Cola llena: los eventos nuevos se descartan (se conservan los ya encolados) y se cuentan.
//
// A diferencia del cliente Python no hay spool en disco: lo que no se pudo enviar al cerrar se
// informa en `delivery.lost`.

import { randomUUID } from "node:crypto";

const RETRYABLE = new Set([408, 425, 429, 500, 502, 503, 504]);
export const limits = { circuitMs: 30_000, maxBackoffMs: 30_000 };

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export class Sender {
  /**
   * @param {string} baseUrl
   * @param {() => Record<string,string>} headers
   */
  constructor(baseUrl, headers, { maxItems = 5000, maxBytes = 64 << 20 } = {}) {
    this.baseUrl = baseUrl;
    this.headers = headers;
    this.maxItems = maxItems;
    this.maxBytes = maxBytes;
    this.queue = [];
    this.bytes = 0;
    this.inflight = null;
    this.downUntil = 0;
    this.running = false;
    this.waiters = [];
    this.pause = null; // backoff en curso: {timer, wake}, para que abandon() lo corte
    this.stats = { sent: 0, retried: 0, rejected: 0, dropped: 0, lost: 0 };
  }

  serverDown() {
    return Date.now() < this.downUntil;
  }

  get pending() {
    return this.queue.length + (this.inflight ? 1 : 0);
  }

  /** Un intento. Devuelve {kind: "ok"|"retry"|"reject", data}. */
  async post(item) {
    try {
      const res = await fetch(this.baseUrl + item.path, {
        method: item.method,
        body: item.body,
        headers: { ...this.headers(), "Content-Type": item.contentType, Accept: "application/json", "Idempotency-Key": item.key },
        signal: AbortSignal.timeout(item.timeout),
      });
      const text = await res.text();
      if (res.ok) {
        this.downUntil = 0;
        return { kind: "ok", data: text ? JSON.parse(text) : {} };
      }
      if (RETRYABLE.has(res.status)) return { kind: "retry" };
      const hint = res.status === 401 ? " (configura el token: new TraceReports({ token }), la variable TRACEREPORTS_TOKEN o TRACEREPORTS_TOKEN en el .env del proyecto)" : "";
      console.warn(`tracereports: ${item.method} ${item.path} -> HTTP ${res.status} ${text.slice(0, 300)}${hint}`);
      return { kind: "reject" };
    } catch (err) {
      if (!this.serverDown()) console.warn(`tracereports: ${item.method} ${item.path} falló: ${err.cause?.code || err.message}`);
      this.downUntil = Date.now() + limits.circuitMs;
      return { kind: "retry" };
    }
  }

  /** Llamada cuya respuesta se necesita (ids): reintentos cortos, misma Idempotency-Key. */
  async sendNow(method, path, body, contentType, timeout, retries = 2, { ignoreCircuit = false } = {}) {
    if (this.serverDown() && !ignoreCircuit) return null;
    const item = { method, path, body, contentType, timeout, key: randomUUID() };
    for (let attempt = 0; attempt <= retries; attempt++) {
      const { kind, data } = await this.post(item);
      if (kind === "ok") {
        this.stats.sent++;
        return data;
      }
      if (kind === "reject" || (this.serverDown() && !ignoreCircuit) || attempt === retries) break;
      this.stats.retried++;
      await sleep([300, 1000, 2000][Math.min(attempt, 2)]);
    }
    return null;
  }

  /** Encola un evento de evidencia. false si la cola está llena (se cuenta en stats.dropped). */
  enqueue(method, path, body, contentType, timeout) {
    const size = body?.length ?? 0;
    if (this.pending + 1 > this.maxItems || this.bytes + size > this.maxBytes) {
      if (this.stats.dropped++ === 0) {
        console.warn(`tracereports: la cola de envío está llena (${this.maxItems} eventos); se descartan los eventos nuevos`);
      }
      return false;
    }
    this.queue.push({ method, path, body, contentType, timeout, key: randomUUID(), attempts: 0, size });
    this.bytes += size;
    if (!this.running) this.run();
    return true;
  }

  async run() {
    this.running = true;
    while (this.queue.length) {
      const item = this.queue.shift();
      this.inflight = item;
      let kind;
      try {
        ({ kind } = await this.post(item));
      } catch {
        kind = "retry";
      }
      this.inflight = null;
      if (item.abandoned) continue; // flush venció y se contó como perdido
      if (kind === "retry") {
        item.attempts++;
        this.stats.retried++;
        this.queue.unshift(item);
        await this.backoff(Math.min(limits.maxBackoffMs, 500 * 2 ** Math.min(item.attempts, 6)));
        continue;
      }
      this.bytes -= item.size;
      this.stats[kind === "ok" ? "sent" : "rejected"]++;
      this.notify();
    }
    this.running = false;
    this.notify();
  }

  notify() {
    if (this.pending) return;
    for (const w of this.waiters.splice(0)) w();
  }

  /** Espera de un reintento que abandon() puede cortar (si no, mantendría vivo el proceso). */
  backoff(ms) {
    return new Promise((wake) => {
      const timer = setTimeout(() => { this.pause = null; wake(); }, ms);
      this.pause = { timer, wake };
    });
  }

  /**
   * Espera a que la cola se vacíe (máx. timeoutMs). Devuelve lo que quedó pendiente. Lo que gana
   * cancela lo otro: si se vacía, el temporizador del timeout se borra (no retiene el proceso);
   * si vence, el waiter sale de la lista.
   */
  async flush(timeoutMs) {
    if (!this.pending) return 0;
    let timer, waiter;
    await new Promise((resolve) => {
      waiter = resolve;
      this.waiters.push(waiter);
      timer = setTimeout(resolve, timeoutMs);
    });
    clearTimeout(timer);
    const i = this.waiters.indexOf(waiter);
    if (i >= 0) this.waiters.splice(i, 1);
    return this.pending;
  }

  /** Descarta lo pendiente (al cerrar, tras el flush) y lo cuenta como perdido. */
  abandon() {
    const items = [...this.queue, ...(this.inflight ? [this.inflight] : [])];
    for (const it of items) it.abandoned = true;
    this.queue.length = 0;
    this.inflight = null;
    this.bytes = 0;
    this.stats.lost += items.length;
    // nada queda esperando: el backoff en curso termina ya y los flush pendientes vuelven
    if (this.pause) {
      clearTimeout(this.pause.timer);
      this.pause.wake();
      this.pause = null;
    }
    this.notify();
    if (items.length) {
      console.warn(`tracereports: ${items.length} eventos de evidencia no se pudieron enviar y se perdieron`);
    }
    return items.length;
  }
}
