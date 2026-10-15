// Playwright Test + TraceReports para grabar escenarios (ver test/scenarios/README.md).
import { defineConfig } from "@playwright/test";

export default defineConfig({
	testDir: "./tests",
	timeout: 30_000,
	retries: 1, // el escenario "flaky" pasa en el reintento
	workers: 1,
	reporter: [["list"], ["tracereports/reporter", {}]],
	use: { headless: true, screenshot: "only-on-failure", viewport: { width: 1280, height: 720 } },
});
