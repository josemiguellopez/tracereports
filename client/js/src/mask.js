// Capturas sin datos sensibles: se tapan antes de capturar.
//
// - TRACEREPORTS_SCREENSHOT_MASK: selectores CSS a tapar (un grupo CSS: "input[name=rut], .tarjeta").
// - Siempre se tapan input[type=password] y los elementos con el atributo data-tracereports-mask;
//   TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off lo desactiva.
// - TRACEREPORTS_SCREENSHOTS: all (por defecto), failures (solo la captura de un paso FAIL) u off
//   (ninguna). El servidor aplica la misma política por su cuenta.
//
// Playwright usa su opción nativa `mask`. Selenium no la tiene: se ponen recuadros del mismo color
// sobre los elementos, se captura y se quitan, también si la captura falla, así el DOM queda igual.
// Sin dependencias: lo importan index.js, playwright.js y selenium.js.

import { env } from "./env.js";

/** Lo que se tapa siempre (salvo TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off). */
export const DEFAULT_MASK = "input[type=password], [data-tracereports-mask]";
/** Color de los recuadros: el mismo que usa Playwright por defecto. */
export const MASK_COLOR = "#FF00FF";

const OVERLAY_ATTR = "data-tracereports-mask-overlay";

/** Nombre del attachment de Playwright con la captura al fallar ya tapada (la toman los fixtures). */
export const MASKED_SHOT = "tracereports-screenshot";

/** El grupo de selectores CSS a tapar ("" = nada). */
export function maskSelector() {
  const parts = [];
  if (!["off", "0", "false", "no"].includes(String(env("SCREENSHOT_MASK_DEFAULTS") ?? "").trim().toLowerCase())) {
    parts.push(DEFAULT_MASK);
  }
  const extra = String(env("SCREENSHOT_MASK") ?? "").trim().replace(/^,+|,+$/g, "").trim();
  if (extra) parts.push(extra);
  return parts.join(", ");
}

/** TRACEREPORTS_SCREENSHOTS: all, failures u off (un valor desconocido cuenta como all). */
export function screenshotsPolicy() {
  const v = String(env("SCREENSHOTS") ?? "").trim().toLowerCase();
  return v === "failures" || v === "off" ? v : "all";
}

/** Si la política deja enviar una captura de un paso con este estado. */
export function screenshotAllowed(status) {
  const policy = screenshotsPolicy();
  if (policy === "off") return false;
  return policy === "all" || String(status).toUpperCase() === "FAIL";
}

/** `page.screenshot()` de Playwright con los elementos sensibles tapados (opción `mask`). */
export function playwrightScreenshot(page, options = {}) {
  const selector = maskSelector();
  const opts = { ...options };
  if (selector) {
    opts.mask ??= [page.locator(selector)];
    opts.maskColor ??= MASK_COLOR;
  }
  return page.screenshot(opts);
}

// Recuadros fijos sobre cada elemento visible del selector (la captura de Selenium es del viewport).
const COVER_JS = `
const [sel, color, attr] = arguments;
let n = 0;
for (const el of document.querySelectorAll(sel)) {
  const r = el.getBoundingClientRect();
  if (!r.width || !r.height) continue;
  const d = document.createElement("div");
  d.setAttribute(attr, "");
  d.style.cssText = "position:fixed;left:" + r.left + "px;top:" + r.top + "px;width:" + r.width + "px;height:" + r.height + "px;" +
    "background:" + color + ";z-index:2147483647;pointer-events:none;margin:0;padding:0;border:0";
  document.documentElement.appendChild(d);
  n++;
}
return n;
`;

const UNCOVER_JS = `for (const d of document.querySelectorAll("[" + arguments[0] + "]")) d.remove();`;

/**
 * Captura de Selenium (base64, lo que acepta `t.screenshot`) con los elementos sensibles tapados, o
 * null si el navegador ya no responde. Los recuadros se quitan siempre; si no se pueden poner (un
 * selector inválido) no se captura: mejor sin captura que sin máscara.
 */
export async function seleniumScreenshot(driver) {
  const selector = maskSelector();
  let covered = false;
  try {
    if (selector) {
      try {
        await driver.executeScript(COVER_JS, selector, MASK_COLOR, OVERLAY_ATTR);
        covered = true;
      } catch (err) {
        console.warn(`tracereports: could not cover ${JSON.stringify(selector)} before the screenshot: ${err?.message ?? err}`);
        return null;
      }
    }
    return await driver.takeScreenshot();
  } catch {
    return null;
  } finally {
    if (covered) {
      try {
        await driver.executeScript(UNCOVER_JS, OVERLAY_ATTR);
      } catch { /* el navegador ya no responde */ }
    }
  }
}
