// Almacenamiento en Mantenimiento: lo que ocupa la evidencia, el disco, la retención elegida en la
// interfaz y "Limpiar ahora" con vista previa. Va al final (zz_): cambia un ajuste del servidor y lo
// deja como estaba.
const { test, expect } = require("@playwright/test");

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({ request }, info) => {
	await request.put("/api/v1/ui/maintenance/retention", { data: { days: 0 } });
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("Mantenimiento: almacenamiento, retención y limpiar ahora con vista previa", async ({ page, request }) => {
	const st = await (await request.get("/api/v1/maintenance/storage")).json();
	expect(st.total_bytes).toBeGreaterThan(0);

	await page.goto("/#view=maintenance");
	const card = page.locator(".stor-card");
	await expect(card).toContainText("Almacenamiento");
	await expect(card).toContainText("Evidencia guardada");
	await expect(card.locator(".stor-stats dd").nth(1)).toHaveText(String(st.runs));
	if (st.disk) await expect(card.locator(".stor-meter")).toBeVisible();
	await expect(card).toContainText("Sin retención: la evidencia se guarda para siempre.");

	// la retención se guarda desde la interfaz
	await card.locator("[data-stor-retention]").selectOption("90");
	await expect(page.locator(".set-msg.ok")).toContainText("se conservarán los últimos 90 días");
	await expect(card).toContainText("se borran las ejecuciones terminadas de más de 90 días");
	expect((await (await request.get("/api/v1/maintenance/storage")).json()).retention.days).toBe(90);

	// limpiar ahora: la vista previa dice lo mismo que la API antes de borrar nada (se cancela)
	await card.locator("[data-stor-clean]").click();
	const confirm = card.locator(".maint-confirm");
	await expect(confirm.locator("[data-stor-clean-days]")).toHaveValue("90");
	await confirm.locator("[data-stor-clean-days]").selectOption("30");
	const pv = await (await request.get("/api/v1/maintenance/storage/preview?days=30")).json();
	if (pv.runs) {
		await expect(confirm).toContainText(`Se borrarán ${pv.runs} ejecución(es) terminadas de más de 30 días (${pv.tests} tests)`);
		await expect(confirm.locator("[data-stor-clean-go]")).toBeEnabled();
	} else {
		await expect(confirm).toContainText("No hay ejecuciones terminadas de más de 30 días");
		await expect(confirm.locator("[data-stor-clean-go]")).toBeDisabled();
	}
	await confirm.locator("[data-stor-clean-cancel]").click();
	await expect(card.locator(".maint-confirm")).toHaveCount(0);
});
