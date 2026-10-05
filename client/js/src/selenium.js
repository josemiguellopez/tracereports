// Selenium WebDriver (selenium-webdriver) con TraceReports. Sirve con cualquier runner
// (node:test, Mocha, Jest…): no importa selenium-webdriver, usa el `driver` que le pases.
//
//   import { TraceReports } from "tracereports";
//   import { withDriver, screenshot } from "tracereports/selenium";
//
//   const cr = new TraceReports();
//   await cr.startRun("Regresión web", { environment: "chrome" });
//   await cr.test("Login correcto", withDriver(driver, { key: "login.test.js > ok" }), async (t) => {
//     await driver.get(URL);
//     t.screenshot(await screenshot(driver), "Formulario de login");
//   });
//   await cr.finishRun();
//
// Si el test falla, `withDriver` adjunta la captura del momento y el snapshot del DOM (con él el
// reporte recomienda selectores cuando se rompe un locator). Selenium no expone la red del
// navegador: para la pestaña "Red" usa Playwright o envía las llamadas con `t.network([...])`.

import { DOM_SCRIPT } from "./dom.js";

/** Captura de pantalla como base64 (lo que acepta `t.screenshot`), o null. */
export async function screenshot(driver) {
  try {
    return await driver.takeScreenshot();
  } catch {
    return null;
  }
}

/** Snapshot de los elementos de la página, o null si el navegador ya no responde. */
export async function captureDom(driver) {
  try {
    return await driver.executeScript(DOM_SCRIPT);
  } catch {
    return null;
  }
}

/** Opciones para `cr.test(...)` que, al fallar, adjuntan captura y DOM desde `driver`. */
export function withDriver(driver, opts = {}) {
  return {
    ...opts,
    async onFailure(t, err) {
      t.screenshot(await screenshot(driver), "Captura al fallar", "FAIL");
      t.dom(await captureDom(driver));
      if (opts.onFailure) await opts.onFailure(t, err);
    },
  };
}
