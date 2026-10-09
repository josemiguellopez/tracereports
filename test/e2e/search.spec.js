const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);

async function createSearchRun(request, name, project, status = "PASS") {
	const run = await (await request.post("/api/v1/runs", { data: { name, project, environment: "qa", branch: "search-e2e" } })).json();
	const test = await (await request.post(`/api/v1/runs/${run.run_id}/tests`, { data: { name: `${name} test`, category: "search, exact-tag", key: `${name}-key` } })).json();
	await request.patch(`/api/v1/tests/${test.test_id}/finish`, { data: { status } });
	await request.patch(`/api/v1/runs/${run.run_id}/finish`, { data: {} });
	return run.run_id;
}

test("Buscar restaura filtros directos y después de recargar", async ({ page, request }) => {
	const token = `search-${Date.now()}`;
	const run = await createSearchRun(request, `${token} fail`, token, "FAIL");
	await page.goto(`/#view=search&q=${encodeURIComponent(token)}&project=${encodeURIComponent(token)}&status=FAIL`);
	await expect(page.locator("#run-search-input")).toBeVisible();
	await expect(page.locator("#run-search-results")).toContainText(`${token} fail`);
	await expect(page.locator("#search-chips")).toContainText("Proyecto");
	await page.reload();
	await expect(page.locator("#run-search-results")).toContainText(`${token} fail`);
	await page.locator(`[data-search-run="${run}"]`).click();
	await expect(page.locator("#run-search-preview")).toContainText(`${token} fail`);
	await page.locator('[data-search-remove="status"]').click();
	await expect(page).toHaveURL(new RegExp(`view=search.*project=${token}`));
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
	await expect(page.locator("#run-select option")).not.toHaveCount(0);
	await page.locator("#recents-search").click();
	await expect(page).toHaveURL(/view=search/);
});
