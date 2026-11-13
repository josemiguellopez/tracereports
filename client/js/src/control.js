// Cancelación cooperativa en Playwright Test (TRACEREPORTS_REMOTE_CONTROL=1). El reporter vive en el
// proceso principal y los tests en workers: el reporter pregunta al servidor (GET /runs/{id}/control)
// y deja el estado en un archivo (TRACEREPORTS_CONTROL_FILE, fijado antes de que arranquen los
// workers); el fixture de cada test lo lee y, si alguien canceló desde En vivo, salta el test con el
// motivo. No se detiene ningún proceso.

import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export const CONTROL_FILE_ENV = "TRACEREPORTS_CONTROL_FILE";

/**
 * Una ruta nueva para el archivo de control de esta ejecución, en una carpeta propia recién creada
 * (mkdtemp: nombre impredecible, solo este usuario), no un nombre adivinable en la carpeta temporal.
 */
export function newControlFile() {
  return path.join(fs.mkdtempSync(path.join(os.tmpdir(), "tracereports-control-")), "control.json");
}

/** Guarda que la ejecución se canceló (lo llama el reporter). */
export function writeCancel(file, reason) {
  try {
    fs.writeFileSync(file, JSON.stringify({ cancel: true, reason: String(reason || "") }), { mode: 0o600 });
  } catch { /* el control nunca rompe la ejecución */ }
}

/** El motivo si la ejecución se canceló ("" sin motivo), o null si sigue (lo llama cada test). */
export function cancelReason(file = process.env[CONTROL_FILE_ENV]) {
  if (!file) return null;
  try {
    const c = JSON.parse(fs.readFileSync(file, "utf8"));
    return c.cancel ? String(c.reason || "") : null;
  } catch {
    return null; // todavía no hay archivo: nadie canceló
  }
}

/** El texto del Skip de un test cancelado. */
export const skipMessage = (reason) => "Cancelado desde TraceReports" + (reason ? `: ${reason}` : "");
