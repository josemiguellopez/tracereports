// Playwright Test + TraceReports. El reporter manda la ejecución al servidor ($TRACEREPORTS_URL o
// http://localhost:8080); los fixtures de "tracereports/playwright" agregan la red y el DOM.
import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  retries: 1, // un reintento: si pasa, TraceReports lo marca "pasó tras reintento"
  reporter: [
    ["list"],
    ["tracereports/reporter", { runName: "OrangeHRM - Playwright JS", environment: "demo pública · chrome" }],
  ],
  use: {
    // usa el Chrome instalado (sin descargar navegadores); quítalo para usar el Chromium de Playwright
    channel: process.env.BROWSER_CHANNEL ?? "chrome",
    headless: process.env.HEADLESS !== "0",
    screenshot: "only-on-failure",
  },
});
