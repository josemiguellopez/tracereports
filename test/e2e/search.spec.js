const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);

test("Buscar restaura filtros desde la URL y permite abrir una vista previa", async ({ page }) => {
	await page.goto("/#view=search&project=e2e&status=FAIL");
	await expect(page.locator("#run-search-input")).toBeVisible();
	await expect(page.locator("#run-search-results")).toContainText("E2E actual");
	await expect(page.locator("#search-chips")).toContainText("Proyecto");
	await page.locator('[data-search-run="' + seed().run + '"]').click();
	await expect(page.locator("#run-search-preview")).toContainText("E2E actual");
	await page.locator('[data-search-remove="status"]').click();
	await expect(page.locator("#run-search-results")).toContainText("E2E anterior");
	await expect(page).toHaveURL(/view=search.*project=e2e/);
});

test("Ctrl+K abre la paleta desde otra vista y encuentra ejecuciones", async ({ page }) => {
	await page.goto("/");
	await page.keyboard.press("Control+k");
	await expect(page.locator("#command-palette")).toBeVisible();
	await page.locator("#command-input").fill("E2E actual");
	await expect(page.locator("#command-results")).toContainText("E2E actual");
	await page.keyboard.press("Escape");
	await expect(page.locator("#command-palette")).toBeHidden();
});

test("la cabecera conserva Recientes y el enlace a Buscar", async ({ page }) => {
	await page.goto("/");
	await expect(page.locator("#run-select option")).toHaveCount(2);
	await page.locator("#recents-search").click();
	await expect(page).toHaveURL(/view=search/);
});
