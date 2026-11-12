// Contenido del escalamiento con un error real envuelto por el framework (el caso del reporte del
// run con «…espontanea.py: ["): la evidencia muestra la parte útil, QA ve dónde falló y la ficha de la
// ejecución completa con un solo fallo nombra el test. Va al final (zz_): crea una ejecución.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

const WRAPPED = `Errores acumulados para el file_name:test_agregar_item_inmunizaciones.py: ["Error Linea 3739 esperando elemento después de 6 intentos: //div[contains(@class,'textoverflow') and contains(normalize-space(.), 'lote: lote_001_vac_01')]"]`;
let run, failed;

test.beforeAll(async ({ request }) => {
	run = (await (await request.post("/api/v1/runs", { data: { name: `esccont-${Date.now()} inmunizaciones`, environment: "preproduccion" } })).json()).run_id;
	const ok = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "test_001_abrir_ficha" } })).json()).test_id;
	await request.patch(`/api/v1/tests/${ok}/finish`, { data: { status: "PASS" } });
	failed = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "test_002_agregar_inmunizacion" } })).json()).test_id;
	await request.post(`/api/v1/tests/${failed}/logs`, { data: { status: "INFO", message: "Guardar la inmunización y buscarla en la lista" } });
	await request.post(`/api/v1/tests/${failed}/logs`, { data: { status: "FAIL", message: WRAPPED } });
	await request.patch(`/api/v1/tests/${failed}/finish`, { data: { status: "FAIL", error_message: WRAPPED } });
	await request.patch(`/api/v1/runs/${run}/finish`, { data: {} });
});

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

/** El resumen sin IA (plantilla: inmediato y determinístico) de la ejecución completa (scope 0) o de un test. */
async function generate(page, scope, { audience = "qa", lang = "es" } = {}) {
	await page.goto(`/#run=${run}&view=escalate`);
	const view = page.locator("#view-escalate");
	await view.locator("#esc-scope").selectOption(String(scope));
	await view.locator(`input[name="esc-aud"][value="${audience}"]`).check({ force: true });
	await view.locator(`[data-esc-lang="${lang}"]`).click();
	const noAI = view.locator('[data-esc-ai="0"]');
	if (await noAI.count()) await noAI.click();
	const card = view.locator("#esc-card");
	await expect(async () => {
		await view.locator("[data-esc-gen]").click();
		await expect(card).toBeVisible({ timeout: 3000 });
	}).toPass({ timeout: 20_000 });
	return card;
}

test("la evidencia muestra la parte útil del error, no el envoltorio cortado en «[»", async ({ page }) => {
	const card = await generate(page, failed);
	const evidence = card.locator(".esc-sec", { hasText: "Evidencia" });
	await expect(evidence).toContainText("Error Linea 3739 esperando elemento");
	await expect(evidence).not.toContainText("file_name");
	for (const li of await evidence.locator("li").allTextContents()) expect(li.trim().endsWith("[")).toBe(false);
});

for (const lang of ["es", "en"]) {
	test(`QA ve dónde falló: el test, el paso, el error y el selector (${lang})`, async ({ page }) => {
		const card = await generate(page, 0, { lang });
		const where = card.locator(".esc-sec", { has: page.locator(".esc-where") });
		await expect(where.locator("h3")).toContainText(lang === "es" ? "Dónde falló" : "Where it failed");
		await expect(where).toContainText("test_002_agregar_inmunizacion");
		await expect(where).toContainText("Guardar la inmunización y buscarla en la lista");
		await expect(where).toContainText("Error Linea 3739 esperando elemento");
		await expect(where.locator("dd.esc-mono").last()).toContainText("//div[contains(@class,'textoverflow')");
		await expect(where.locator("dd.esc-mono").last()).not.toContainText('"]');
		// también en el texto para pegar (Teams, Slack, Markdown, texto)
		await page.evaluate(() => { window.__copied = ""; navigator.clipboard.writeText = (t) => { window.__copied = t; return Promise.resolve(); }; });
		await page.locator('[data-esc-share="plain"]').click();
		await expect.poll(() => page.evaluate(() => window.__copied)).toContain(lang === "es" ? "Dónde falló:" : "Where it failed:");
		expect(await page.evaluate(() => window.__copied)).toContain("test_002_agregar_inmunizacion");
	});
}

test("Negocio no recibe la sección técnica de dónde falló", async ({ page }) => {
	const card = await generate(page, 0, { audience: "business" });
	await expect(card.locator(".esc-where")).toHaveCount(0);
});

test("la ejecución completa con un solo fallo nombra el test en la ficha, sin celdas vacías", async ({ page }) => {
	const card = await generate(page, 0);
	const facts = card.locator(".esc-facts");
	await expect(facts.locator("tr", { hasText: "Test" }).locator("td").filter({ hasText: "test_002_agregar_inmunizacion" })).toHaveCount(1);
	const cells = await facts.locator("th, td").evaluateAll((els) => els.map((e) => e.textContent.trim()));
	expect(cells.filter((c) => c === ""), "la ficha no deja celdas vacías").toEqual([]);
	// una cantidad impar de datos: el último ocupa la fila entera
	const odd = await facts.evaluate((t) => [...t.rows].filter((r) => r.cells.length === 2).map((r) => r.cells[1].colSpan));
	expect(odd.every((c) => c === 3)).toBe(true);
});
