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

/** Una sección de la tarjeta por su título («5. Evidencia»), no por un texto que también puede estar en otra. */
const sec = (card, title) => card.locator(".esc-sec").filter({ has: card.page().locator("h3", { hasText: new RegExp("^\\s*\\d+\\.\\s*" + title + "\\s*$", "i") }) });

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
	const evidence = sec(card, "Evidencia");
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
		// el paso que falló repite el error: el anterior es el último que pasó, y se rotula así
		await expect(where.locator("dt", { hasText: lang === "es" ? "Último paso correcto" : "Last step that passed" })).toHaveCount(1);
		await expect(where.locator("dt", { hasText: lang === "es" ? "Paso que falló" : "Step that failed" })).toHaveCount(0);
		// «Ver reporte completo ↗»: la flecha no baja sola a otra línea
		await expect(card.locator(".esc-foot a")).toHaveCSS("white-space", "nowrap");
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

// El test buscó la fila mientras la lista seguía cargando (cinco SVG de skeleton en el panel): no cambió
// el selector, el test no esperó. El triage del test y el escalamiento lo dicen, y no piden cambiar el selector.
test("la lista seguía cargando: TIMING en el triage y en el escalamiento, sin culpar al selector", async ({ page, request }) => {
	// el motor local de reglas, aunque un spec anterior haya dejado otro proveedor configurado
	expect((await request.put("/api/v1/settings", { data: { ai: { provider: "builtin" } } })).ok()).toBeTruthy();
	try { await loadingCase(page, request); } finally { await request.delete("/api/v1/settings/ai"); }
});

async function loadingCase(page, request) {
	const loadRun = (await (await request.post("/api/v1/runs", { data: { name: `esccarga-${Date.now()} inmunizaciones`, environment: "preproduccion" } })).json()).run_id;
	const id = (await (await request.post(`/api/v1/runs/${loadRun}/tests`, { data: { name: "test_002_agregar_inmunizacion" } })).json()).test_id;
	const nodes = [
		{ i: 0, p: -1, tag: "body", x: 0, y: 0, w: 1382, h: 928, visible: true, depth: 0 },
		{ i: 1, p: 0, tag: "div", attrs: { class: "card" }, x: 8, y: 98, w: 545, h: 788, visible: true, depth: 1 },
		{ i: 2, p: 1, tag: "h5", text: "Inmunizaciones", x: 81, y: 111, w: 411, h: 24, visible: true, depth: 2 },
		{ i: 3, p: 1, tag: "div", attrs: { class: "mt-1 col-sm-12" }, x: 66, y: 153, w: 497, h: 731, visible: true, depth: 2 },
	];
	for (let k = 0; k < 5; k++) nodes.push({ i: 4 + k, p: 3, tag: "svg", attrs: { role: "img" }, x: 81, y: 153 + 111 * k, w: 467, h: 111, visible: true, depth: 3,
		svg: '<svg viewBox="0 0 400 95"><rect width="400" height="95" fill="url(#g)"/><defs><linearGradient id="g"><stop offset="0" stop-color="#f3f3f3"><animate attributeName="offset" values="-2; 1" dur="2s" repeatCount="indefinite"/></stop></linearGradient></defs></svg>' });
	expect((await request.post(`/api/v1/tests/${id}/dom`, { data: { url: "https://app/ficha", viewport: { w: 1382, h: 928 }, page: { w: 1382, h: 928 }, nodes, elements: [] } })).ok()).toBeTruthy();
	await request.post(`/api/v1/tests/${id}/logs`, { data: { status: "FAIL", message: WRAPPED } });
	await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL", error_message: WRAPPED } });
	await request.patch(`/api/v1/runs/${loadRun}/finish`, { data: {} });

	// el triage del test (motor local): espera, no locator
	await expect.poll(async () => (await (await request.get(`/api/v1/tests/${id}`)).json()).triage?.category, { timeout: 60_000 }).toBe("TIMING") // la cola del analizador puede venir cargada de otros specs;
	const tri = (await (await request.get(`/api/v1/tests/${id}`)).json()).triage;
	expect(tri.summary).toMatch(/5 (bloques de carga|loading blocks)/); // el idioma del motor sale de los nombres de los tests
	expect(tri.summary).not.toMatch(/cambió la interfaz|the UI changed/);

	await page.goto(`/#run=${loadRun}&view=escalate`);
	const view = page.locator("#view-escalate");
	await view.locator("#esc-scope").selectOption("0");
	await view.locator('input[name="esc-aud"][value="qa"]').check({ force: true });
	await view.locator('[data-esc-lang="es"]').click();
	const noAI = view.locator('[data-esc-ai="0"]');
	if (await noAI.count()) await noAI.click();
	const card = view.locator("#esc-card");
	await expect(async () => {
		await view.locator("[data-esc-gen]").click();
		await expect(card).toBeVisible({ timeout: 3000 });
	}).toPass({ timeout: 20_000 });
	await expect(card).toContainText("la página seguía cargando cuando el test lo buscó");
	await expect(sec(card, "Evidencia")).toContainText("5 bloques de carga (skeleton) visibles");
	await expect(sec(card, "Próximos pasos")).toContainText("Esperar a que termine la carga");
	await expect(card).not.toContainText("Actualizar el selector");
	await expect(card).not.toContainText("La pantalla cambió");
	await expect(card.locator(".esc-sev")).toHaveText("Baja");
}
