// El snapshot del DOM no lleva lo tapado: ni el texto del elemento enmascarado ni el de un contenedor
// (una ficha con id) que lo tiene adentro. Con un navegador real (el Playwright del e2e del repo).
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { DOM_SCRIPT, domSnapshotInPage } from "../src/dom.js";
import { maskSelector } from "../src/mask.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const e2e = path.join(here, "..", "..", "..", "test", "e2e", "package.json");
const PAGE = `<section id="ficha"><h2>Pedido</h2><p>Cliente: <span data-tracereports-mask>Juana Pérez Soto</span></p>
  <p>RUT <b class="rut-cliente">9.876.543-2</b></p><label for="pw">Clave</label><input id="pw" type="password" value="secreta">
  <button id="guardar">Guardar</button></section>`;

test("el snapshot del DOM no lleva lo tapado (Playwright y Selenium)", { skip: !fs.existsSync(e2e) && "sin el Playwright del e2e" }, async (t) => {
  const { chromium } = createRequire(e2e)("@playwright/test");
  let browser;
  try { browser = await chromium.launch(); } catch { return t.skip("sin Chromium"); }
  try {
    const page = await browser.newPage();
    await page.setContent(PAGE);
    process.env.TRACEREPORTS_SCREENSHOT_MASK = ".rut-cliente";
    const mask = maskSelector();
    delete process.env.TRACEREPORTS_SCREENSHOT_MASK;
    for (const snap of [
      await page.evaluate(domSnapshotInPage, mask),
      // Selenium: el mismo script como texto, con la máscara en arguments[0]
      await page.evaluate(([script, m]) => new Function(script).call(null, m), [DOM_SCRIPT, mask]),
    ]) {
      const raw = JSON.stringify(snap);
      assert.ok(!raw.includes("Juana") && !raw.includes("9.876.543-2") && !raw.includes("secreta"), raw);
      const ficha = snap.elements.find((e) => e.id === "ficha");
      assert.match(ficha.text, /Pedido Cliente: ••• RUT ••• Clave ••• Guardar/);
      assert.equal(snap.elements.find((e) => e.id === "guardar").text, "Guardar");
    }
  } finally {
    await browser.close();
  }
});
