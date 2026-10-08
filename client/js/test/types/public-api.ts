// Chequeo de tipos de la API pública (npm run typecheck): lo que acepta el runtime debe poder
// escribirse con los tipos publicados en src/index.d.ts, sin casts. No se ejecuta.
import type { Connection, TraceTest } from "../../src/index.js";

// una llamada capturada normal
const plain: Connection = { method: "GET", url: "https://example.test/a", status: 200, body_size: 32 };

// una respuesta que ya llegó recortada: network() conserva la marca y el tamaño original
const cut: Connection = {
  method: "GET", url: "https://example.test/b", status: 200,
  response_body: '{"items":[', body_size: 900000, body_truncated: true,
};

// los tipos siguen siendo estrictos: un campo que no existe se rechaza
// @ts-expect-error unknown fields are not part of Connection
const unknown: Connection = { method: "GET", url: "https://example.test/c", not_a_field: true };

export function attach(t: TraceTest): number {
  return t.network([plain, cut, unknown]).stored;
}
