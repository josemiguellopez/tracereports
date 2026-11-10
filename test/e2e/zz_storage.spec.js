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

test("Limpiar ahora: mientras limpia dice qué pasa, todo queda bloqueado y un segundo clic no lo repite; un error dice la causa", async ({ page }) => {
	// una vista previa con algo que borrar y un servidor que tarda en limpiar (base grande)
	await page.route("**/api/v1/maintenance/storage/preview?**", (r) => r.fulfill({ json: { days: 30, runs: 120, tests: 900, files: 0, files_bytes: 0, db_bytes_approx: 3 << 30, freed_bytes_approx: 3 << 30 } }));
	let calls = 0, release;
	const gate = new Promise((r) => { release = r; });
	await page.route("**/api/v1/ui/maintenance/retention/run", async (r) => {
		calls++;
		await gate;
		return r.fulfill({ status: 500, json: { error: "the server's data disk is full: free space or lower the retention (ref 9f8e7d6c)", ref: "9f8e7d6c", kind: "disk_full" } });
	});
	await page.goto("/#view=maintenance");
	const card = page.locator(".stor-card");
	await card.locator("[data-stor-clean]").click();
	const confirm = card.locator(".maint-confirm");
	await expect(confirm).toContainText("Se borrarán 120 ejecución(es)");
	const go = confirm.locator("[data-stor-clean-go]");
	await go.click();
	await go.click({ force: true }); // el clic impaciente
	await go.dispatchEvent("click");
	await expect(go).toBeDisabled();
	await expect(go).toContainText("Limpiando…");
	await expect(go.locator(".maint-spin")).toHaveCount(1);
	await expect(confirm.locator(".maint-progress[role=status]")).toContainText("se borran de a tandas");
	await expect(confirm).toHaveAttribute("aria-busy", "true");
	await expect(confirm.locator("[data-stor-clean-days]")).toBeDisabled();
	await expect(confirm.locator("[data-stor-clean-cancel]")).toBeDisabled();
	await expect(card.locator("[data-stor-clean]")).toBeDisabled();
	expect(calls).toBe(1);
	release();
	// el error dice la causa y la referencia; la vista previa sigue a la vista y se puede reintentar
	await expect(confirm.locator(".set-msg.err")).toContainText("El disco de datos del servidor está lleno");
	await expect(confirm.locator(".set-msg.err")).toContainText("9f8e7d6c");
	await expect(confirm).toContainText("Se borrarán 120 ejecución(es)");
	await expect(go).toBeEnabled();
	expect(calls).toBe(1);
	// el 500 simulado lo anota el navegador en la consola: es lo que se prueba, no un error de la página
	test.info().errors_ = test.info().errors_.filter((e) => !/status of 500/.test(e));
});
