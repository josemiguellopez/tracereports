// Importar un reporte JUnit XML por la API y verlo en la UI como cualquier otra ejecución.
const fs = require("node:fs");
const path = require("node:path");
const { test, expect } = require("@playwright/test");

const fixture = (name) => fs.readFileSync(path.join(__dirname, "..", "..", "internal", "junit", "testdata", name), "utf8");

test("un JUnit XML importado se ve con sus tests, errores y duraciones", async ({ page, request }) => {
	const errors = [];
	page.on("pageerror", (e) => errors.push(e.message));
	page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });

	const res = await request.post("/api/v1/import/junit?name=JUnit%20E2E&project=e2e-junit&branch=dev", {
		headers: { "Content-Type": "application/xml" },
		data: fixture("pytest.xml"),
	});
	expect(res.status()).toBe(201);
	const out = await res.json();
	expect(out).toMatchObject({ tests: 4, passed: 1, failed: 2, skipped: 1, status: "FAIL" });

	await page.goto(out.report);
	await expect(page.locator("#report-name")).toHaveText("JUnit E2E");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(4);

	// la duración es la del reporte (time="2.001"), no la de la subida
	const item = page.locator("#test-collection .collection-item", { hasText: "test_login_admin[chromium]" });
	await expect(item).toContainText("0h 0m 2s+1ms");
	await item.click();
	const detail = page.locator("#test-detail");
	await expect(detail).toContainText("AssertionError: assert 500 == 200");
	await expect(detail).toContainText("abriendo login");

	await page.locator('[data-view="exceptions"]').first().click();
	await expect(page.locator("#view-exceptions")).toContainText("AssertionError");

	expect(errors).toEqual([]);
});
