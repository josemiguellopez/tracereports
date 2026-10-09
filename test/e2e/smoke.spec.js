// Recorrido de humo por la UI: cada vista y cada pestaña debe dibujarse sin errores de JavaScript
// ni respuestas 5xx del servidor. Si una funcionalidad nueva rompe otra pantalla, falla aquí.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);

// Errores de la página: excepciones sin capturar, console.error y respuestas 5xx de la API.
test.beforeEach(async ({ page }, info) => {
	const errors = [];
	info.errors_ = errors;
	page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") errors.push(`console.error: ${m.text()}`); });
	page.on("response", (r) => { if (r.status() >= 500) errors.push(`${r.status()} ${r.request().method()} ${r.url()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

/** Abre la ejecución actual y espera a que la lista de tests esté dibujada. */
async function openRun(page) {
	await page.goto("/");
	const { run } = seed();
	// la ejecución más reciente se abre sola; si no, se elige en el selector
	const select = page.locator("#run-select");
	if ((await select.inputValue()) !== String(run)) await select.selectOption(String(run), { force: true });
	await expect(page.locator("#report-name")).toHaveText("E2E actual");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(3);
}

/** Abre el detalle de un test por nombre. */
async function openTest(page, name) {
	// en el nombre: el centro de la fila puede caer sobre un chip (latencia, veredicto) que abre otro panel
	await page.locator("#test-collection .collection-item", { hasText: name }).locator(".test-name").click();
	await expect(page.locator("#test-detail")).toContainText(name);
}

test("carga la ejecución con sus tests y filtros", async ({ page }) => {
	await openRun(page);
	await page.locator("#search-tests").fill("buscar");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(1);
	await page.locator("#search-tests").fill("");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(3);
});

test("detalle de un test fallido: pasos, timeline, replay y red", async ({ page }) => {
	await openRun(page);
	await openTest(page, "test_login_admin");
	const detail = page.locator("#test-detail");

	await detail.locator('[data-tab="steps"]').click();
	await expect(detail).toContainText("El Dashboard no apareció");

	await detail.locator('[data-tab="timeline"]').click();
	await expect(detail.locator('[data-tab="timeline"]')).toHaveClass(/active/);

	await detail.locator('[data-tab="replay"]').click();
	await expect(detail.locator('[data-tab="replay"]')).toHaveClass(/active/);
	// arranca en el primer paso (sin captura); en el último se ve la captura
	await expect(detail.locator(".tt-player")).toBeVisible();
	await detail.locator(".tt-player").focus();
	await page.keyboard.press("End");
	await expect(detail.locator("img.tt-frame")).toBeVisible();
	await expect(detail.locator("img.tt-frame")).toHaveJSProperty("complete", true);
	expect(await detail.locator("img.tt-frame").evaluate((img) => img.naturalWidth)).toBeGreaterThan(0);

	await detail.locator('[data-tab="network"]').click();
	await expect(detail.locator(".net-card")).toHaveCount(2);
	await expect(detail.locator(".net-card-error")).toHaveCount(1);
});

test("red: Copiar como cURL no filtra secretos y conserva los headers enmascarados", async ({ page }) => {
	await openRun(page);
	await openTest(page, "test_login_admin");
	const detail = page.locator("#test-detail");
	await detail.locator('[data-tab="network"]').click();
	const card = detail.locator(".net-card-error");
	await card.locator("summary").click();
	await card.locator("[data-curl]").click();
	const curl = await page.evaluate(() => navigator.clipboard.readText());
	expect(curl).toContain("curl -X POST 'https://api.example.com/auth/login?lang=es'");
	expect(curl).toContain('-H "Authorization: $AUTHORIZATION"');
	expect(curl).toContain('"password":"***"');
	expect(curl).not.toContain("E2E-SECRET");
	expect(curl).not.toContain("E2E-PASSWORD");
	expect(curl).not.toContain("<masked>");
});

test("métricas: vista previa del resumen semanal", async ({ page }) => {
	await openRun(page);
	await page.locator('[data-view="metrics"]').first().click();
	await page.locator("[data-weekly]").click();
	const weekly = page.locator(".weekly");
	await expect(page.locator("#cf-drawer-title")).toHaveText("Resumen semanal de pruebas");
	await expect(weekly.locator(".weekly-headline")).toContainText("%");
	await expect(weekly).toContainText("Lo que más falla");
	// sin Teams ni Slack en este servidor: explica cómo configurarlo en vez de ofrecer enviar
	await expect(weekly.locator("[data-weekly-send]")).toHaveCount(0);
	await expect(weekly).toContainText("TRACEREPORTS_WEEKLY_SUMMARY");
});

test("release: decisión, criterios y funcionalidades", async ({ page }) => {
	await openRun(page);
	await page.locator('[data-view="release"]').first().click();
	const view = page.locator("#view-release");
	// 1 de 2 tests pasa (el skip no cuenta): 50% < 95% por defecto, y hay un fallo nuevo
	await expect(view.locator(".rel-banner")).toContainText("No salir todavía");
	await expect(view.locator(".rel-check.block")).toContainText("Tasa de éxito 50% (mínimo 95%)");
	await expect(view.locator(".rel-check.warn").first()).toContainText("1 fallo nuevo frente a la ejecución anterior");
	await expect(view.locator(".rel-feature.fail")).toContainText("login");
	await expect(view.locator(".rel-feature.ok")).toContainText("smoke");
});

test("consola del navegador: pestaña con el error de JavaScript", async ({ page }) => {
	await openRun(page);
	await openTest(page, "test_login_admin");
	const detail = page.locator("#test-detail");
	const tab = detail.locator('[data-tab="console"]');
	await expect(tab).toContainText("1 error");
	await expect(tab).not.toContainText("1 errores");
	await tab.click();
	const rows = detail.locator(".console-table tbody tr");
	await expect(rows).toHaveCount(2);
	await expect(rows.nth(1)).toContainText("Error no manejado");
	await expect(rows.nth(1)).toContainText("reading 'dashboard'");
	await expect(rows.nth(0)).toContainText("main.js:10:5");
});

test("reproducir en local: el comando de pytest con el commit", async ({ page }) => {
	await openRun(page);
	await openTest(page, "test_login_admin");
	const repro = page.locator("#test-detail details.repro");
	await repro.locator("summary").click();
	await expect(repro.locator("code")).toHaveText("git checkout bbb222 && pytest 'test_login_admin'");
	await repro.locator(".copy-btn").click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("git checkout bbb222 && pytest 'test_login_admin'");
});

test("red: comparar con la última vez que el test pasó", async ({ page }) => {
	await openRun(page);
	await openTest(page, "test_login_admin");
	const detail = page.locator("#test-detail");
	await detail.locator('[data-tab="network"]').click();
	const card = detail.locator(".net-card-error");
	await card.locator("summary").click();
	await card.locator("[data-baseline]").click();
	const drawer = page.locator(".baseline-diff");
	await expect(drawer).toContainText("Comparado con la ejecución #1");
	await expect(drawer.locator("tr.diff-changed").first()).toContainText("500");
	await expect(drawer).toContainText("db pool exhausted");
	await expect(drawer.locator("code", { hasText: "x-api-version" })).toBeVisible(); // header que ya no viene
	await page.keyboard.press("Escape");
});

test("red: correlación con los logs del backend", async ({ page }) => {
	await openRun(page);
	await openTest(page, "test_login_admin");
	const detail = page.locator("#test-detail");
	await detail.locator('[data-tab="network"]').click();
	const card = detail.locator(".net-card-error");
	await card.locator("summary").click();
	const corr = card.locator(".bloque.corr");
	await expect(corr).toContainText("4bf92f3577b34da6a3ce929d0e0e4736");
	await expect(corr).toContainText("req-e2e-42");
	await expect(corr.locator('a:has-text("Ver traza")')).toHaveAttribute("href", "https://traces.example/4bf92f3577b34da6a3ce929d0e0e4736");
	await expect(corr.locator('a:has-text("Ver logs")')).toHaveAttribute("href", /^https:\/\/logs\.example\/search\?q=req-e2e-42&from=\d+$/);
	// la llamada sin ids no muestra el bloque
	await expect(detail.locator(".net-card:not(.net-card-error) .bloque.corr")).toHaveCount(0);
});

test("logo del sitio en la barra superior", async ({ page }) => {
	await openRun(page);
	await expect(page.locator(".logo-container use")).toHaveAttribute("href", "#i-logo");
	await expect(page.locator(".logo-word")).toHaveText("tracereports_");
});

test("escalar para Desarrollo: detalle técnico con cURL, stack, consola y cómo reproducirlo", async ({ page }) => {
	await openRun(page);
	await page.locator('[data-view="escalate"]').first().click();
	const view = page.locator("#view-escalate");
	await view.locator("#esc-scope").selectOption({ label: "test_login_admin" });
	await view.locator('input[name="esc-aud"][value="dev"]').check({ force: true });
	await view.locator("[data-esc-gen]").click();
	const dev = view.locator("#esc-card .esc-dev");
	await expect(dev).toContainText("Detalle técnico");
	await expect(dev).toContainText("Commit");
	await expect(dev).toContainText("git checkout bbb222 && pytest 'test_login_admin'");
	await expect(dev).toContainText("reading 'dashboard'");
	await expect(dev).toContainText("La última vez que el test pasó (ejecución #1) respondió HTTP 200");
	const call = dev.locator(".esc-call").first();
	await expect(call).toContainText("curl -X POST 'https://api.example.com/auth/login?lang=es'");
	await expect(call).toContainText('-H "Authorization: $AUTHORIZATION"');
	await expect(call).toContainText("db pool exhausted");
	await expect(dev).not.toContainText("E2E-SECRET");
	await expect(dev).not.toContainText("E2E-PASSWORD");
	// copiar el cURL de la llamada
	await call.locator(".esc-copy").first().click();
	const curl = await page.evaluate(() => navigator.clipboard.readText());
	expect(curl).toContain('-H "Authorization: $AUTHORIZATION"');
	expect(curl).not.toContain("<masked>");
	// Markdown: bloques de código listos para pegar en Jira, GitHub o Teams
	await view.locator('[data-esc-share="md"]').click();
	const md = (await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, "\n"); // el portapapeles de Windows usa CRLF
	expect(md).toContain("**Detalle técnico:**");
	expect(md).toContain("```bash\ncurl -X POST");
	expect(md).toContain("```bash\ngit checkout bbb222");
	expect(md).not.toContain("E2E-SECRET");
});

test("escalar la ejecución completa para Desarrollo: detalle técnico por test", async ({ page }) => {
	await openRun(page);
	await page.locator('[data-view="escalate"]').first().click();
	const view = page.locator("#view-escalate");
	await view.locator("#esc-scope").selectOption("0");
	await view.locator('input[name="esc-aud"][value="dev"]').check({ force: true });
	await view.locator("[data-esc-gen]").click();
	const dev = view.locator("#esc-card .esc-dev");
	await expect(dev.locator(".esc-dev-name")).toHaveText(["test_login_admin"]);
	await expect(dev.locator(".esc-call")).toContainText('-H "Authorization: $AUTHORIZATION"');
	await view.locator('[data-esc-share="plain"]').click();
	const text = await page.evaluate(() => navigator.clipboard.readText());
	expect(text).toContain("Test: test_login_admin");
	expect(text).toContain("    curl -X POST 'https://api.example.com/auth/login?lang=es'");
	expect(text).not.toContain("E2E-SECRET");
});

test("red: el panel de mocks muestra cada formato", async ({ page }) => {
	await openRun(page);
	await openTest(page, "test_login_admin");
	const detail = page.locator("#test-detail");
	await detail.locator('[data-tab="network"]').click();
	await detail.locator(".net-card-error [data-mock]").first().click();
	const drawer = page.locator(".cf-mock");
	await expect(drawer).toBeVisible();
	const tabs = drawer.locator('[role="tab"]');
	await expect(tabs).toHaveText(["Playwright (Python)", "Playwright (JS)", "Cypress", "WireMock"]);
	for (let i = 0; i < 4; i++) {
		await tabs.nth(i).click();
		await expect(tabs.nth(i)).toHaveAttribute("aria-selected", "true");
		await expect(drawer.locator(".cf-code")).toContainText("db pool exhausted");
	}
	await expect(drawer.locator(".cf-code")).not.toContainText("E2E-PASSWORD");
	await page.keyboard.press("Escape");
	await expect(drawer).toBeHidden();
});

for (const [view, expected] of [
	["categories", "login"],
	["exceptions", "TimeoutError"],
	["dashboard", null],
	["release", "No salir"],
	["ai", null],
	["escalate", null],
	["metrics", null],
	["settings", null],
]) {
	test(`vista ${view} se dibuja`, async ({ page }) => {
		await openRun(page);
		await page.locator(`nav [data-view="${view}"], [data-view="${view}"]`).first().click();
		const section = page.locator(`#view-${view}`);
		await expect(section).toBeVisible();
		await expect(section).not.toBeEmpty();
		if (expected) await expect(section).toContainText(expected);
		// volver a Tests sigue funcionando
		await page.locator('[data-view="tests"]').first().click();
		await expect(page.locator("#view-tests")).toBeVisible();
	});
}

test("temas: cada uno se aplica", async ({ page }) => {
	await openRun(page);
	await page.locator("#theme-btn").click();
	const options = page.locator("#theme-menu .theme-option");
	const count = await options.count();
	expect(count).toBeGreaterThanOrEqual(6);
	for (let i = 0; i < count; i++) {
		if (i > 0) await page.locator("#theme-btn").click();
		const id = await options.nth(i).getAttribute("data-theme-id");
		await options.nth(i).click();
		await expect(page.locator("body")).toHaveAttribute("data-theme", id);
	}
});

test("inglés: la navegación se traduce", async ({ page }) => {
	await page.addInitScript(() => { try { localStorage.setItem("tracereports-lang", "en"); } catch {} });
	await openRun(page);
	await expect(page.locator('[data-view="categories"]').first()).toContainText("Categories");
	await expect(page.locator('[data-view="settings"]').first()).toContainText("Settings");
});

test("exportar ZIP responde un archivo zip", async ({ page, request }) => {
	await openRun(page);
	const href = await page.locator("#export-btn").getAttribute("href");
	expect(href).toBe(`/api/v1/runs/${seed().run}/export`);
	const res = await request.get(href);
	expect(res.status()).toBe(200);
	const body = await res.body();
	expect(body.subarray(0, 2).toString()).toBe("PK");
});
