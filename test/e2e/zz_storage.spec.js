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
	await card.locator("[data-days-pick=\"retention\"]").selectOption("90");
	await expect(page.locator(".set-msg.ok")).toContainText("se conservarán los últimos 90 días");
	await expect(card).toContainText("se borran las ejecuciones terminadas de más de 90 días");
	expect((await (await request.get("/api/v1/maintenance/storage")).json()).retention.days).toBe(90);

	// limpiar ahora: la vista previa dice lo mismo que la API antes de borrar nada (se cancela)
	await card.locator("[data-stor-clean]").click();
	const confirm = card.locator(".maint-confirm");
	await expect(confirm.locator("[data-days-pick=\"clean\"]")).toHaveValue("90");
	await confirm.locator("[data-days-pick=\"clean\"]").selectOption("30");
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
	await page.route("**/api/v1/maintenance/storage/preview?**", (r) => r.fulfill({ json: { days: 30, runs: 120, tests: 900, files: 0, files_bytes: 0, db_bytes_approx: 3 * 2 ** 30, freed_bytes_approx: 3 * 2 ** 30 } }));
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
	await expect(confirm.locator("[data-days-pick=\"clean\"]")).toBeDisabled();
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

test("Compactar ahora: aparte de borrar, con lo que pide el disco; mientras compacta se ve y un segundo clic no lo repite", async ({ page }) => {
	await page.route("**/api/v1/maintenance/storage", async (r) => {
		const res = await r.fetch();
		const json = await res.json();
		json.compact = { pending: true, running: false, can_run: true, quiet_minutes: 15, reclaimable_bytes: 4 * 2 ** 30, need_bytes: 10 * 2 ** 30, free_bytes: 50 * 2 ** 30, running_runs: 2 };
		return r.fulfill({ json });
	});
	let calls = 0, release;
	const gate = new Promise((r) => { release = r; });
	await page.route("**/api/v1/ui/maintenance/compact", async (r) => { calls++; await gate; return r.fulfill({ json: { at: Date.now(), took_ms: 185000, freed_bytes: 4 * 2 ** 30, trigger: "manual" } }); });
	await page.goto("/#view=maintenance");
	const card = page.locator(".stor-card");
	const row = card.locator(".stor-compact");
	await expect(row).toContainText("Hay ~4.0 GB de lo borrado por devolver al disco");
	await row.locator("[data-stor-compact]").click();
	const confirm = card.locator(".maint-confirm", { hasText: "Se reescribe la base" });
	await expect(confirm).toContainText("Necesita ~10.0 GB libres en el disco de los datos (hay 50.0 GB)");
	await expect(confirm).toContainText("Hay 2 ejecución(es) en curso");
	const go = confirm.locator("[data-stor-compact-go]");
	await go.click();
	await go.dispatchEvent("click"); // el clic impaciente
	await expect(go).toBeDisabled();
	await expect(go).toContainText("Compactando…");
	await expect(confirm.locator(".maint-progress[role=status]")).toContainText("puede tardar varios minutos");
	await expect(confirm.locator("[data-stor-compact-cancel]")).toBeDisabled();
	await expect(row.locator("[data-stor-compact]")).toBeDisabled();
	expect(calls).toBe(1);
	release();
	await expect(page.locator(".set-msg.ok")).toContainText("Listo: la base se compactó y devolvió 4.0 GB al disco (3 min 5 s)");
	await expect(card.locator(".maint-confirm", { hasText: "Se reescribe la base" })).toHaveCount(0);
});

test("Compactar ahora: sin espacio en el disco no deja empezar y dice por qué", async ({ page }) => {
	await page.route("**/api/v1/maintenance/storage", async (r) => {
		const res = await r.fetch();
		const json = await res.json();
		json.compact = { pending: true, running: false, can_run: true, quiet_minutes: 15, reclaimable_bytes: 4 * 2 ** 30, need_bytes: 10 * 2 ** 30, free_bytes: 2 * 2 ** 30, running_runs: 0 };
		return r.fulfill({ json });
	});
	await page.goto("/#view=maintenance");
	await page.locator(".stor-compact [data-stor-compact]").click();
	const confirm = page.locator(".maint-confirm", { hasText: "Se reescribe la base" });
	await expect(confirm.locator(".set-msg.err")).toContainText("No hay espacio suficiente para compactar");
	await expect(confirm.locator("[data-stor-compact-go]")).toBeDisabled();
});

test("días a elección: opciones cortas y largas, «Otro…» con vista previa en vivo y la retención con cualquier número de días", async ({ page, request }) => {
	const asked = [];
	await page.route("**/api/v1/maintenance/storage/preview?**", (r) => {
		const days = Number(new URL(r.request().url()).searchParams.get("days"));
		asked.push(days);
		return r.fulfill({ json: { days, runs: days, tests: days * 3, files: 0, files_bytes: 0, db_bytes_approx: 2 ** 20, freed_bytes_approx: 2 ** 20 } });
	});
	await page.goto("/#view=maintenance");
	const card = page.locator(".stor-card");
	const keep = card.locator('[data-days-pick="retention"]');
	// opciones de 1 día a 1 año, y "Otro…"
	for (const label of ["Conservar todo", "1 día", "2 días", "3 días", "7 días", "14 días", "30 días", "2 meses", "6 meses", "1 año", "Otro…"])
		await expect(keep.locator("option", { hasText: label })).toHaveCount(1);

	// limpiar: "Otro…" abre el campo con el foco; lo que se escribe actualiza la vista previa sin salir del campo
	await card.locator("[data-stor-clean]").click();
	const confirm = card.locator(".maint-confirm");
	await confirm.locator('[data-days-pick="clean"]').selectOption("custom");
	const days = confirm.locator('[data-days-input="clean"]');
	await expect(days).toBeFocused();
	await days.fill("45");
	await expect(confirm).toContainText("Se borrarán 45 ejecución(es) terminadas de más de 45 días (135 tests)");
	await expect(days).toBeFocused(); // la vista previa no le quita el foco a lo que se escribe
	await expect(confirm.locator("[data-clean-cutoff]")).toContainText("Se borra lo que empezó antes del");
	await expect(confirm.locator("[data-clean-cutoff]")).toContainText("la ejecución más antigua es del");
	// un valor fuera de rango no pide nada al servidor y lo dice
	await days.fill("0");
	await days.press("Enter");
	expect(await days.evaluate((el) => el.validationMessage)).toContain("entre 1 y 3650");
	expect(asked).not.toContain(0);
	await confirm.locator("[data-stor-clean-cancel]").click();

	// la retención con cualquier número de días: se guarda con Enter y se ve en el campo al volver
	await keep.selectOption("custom");
	const keepDays = card.locator('[data-days-input="retention"]');
	await keepDays.fill("5");
	await keepDays.press("Enter");
	await expect(page.locator(".set-msg.ok")).toContainText("se conservarán los últimos 5 días");
	expect((await (await request.get("/api/v1/maintenance/storage")).json()).retention.days).toBe(5);
	await page.reload();
	await expect(page.locator('.stor-card [data-days-pick="retention"]')).toHaveValue("custom");
	await expect(page.locator('.stor-card [data-days-input="retention"]')).toHaveValue("5");
});

test("una limpieza que se cortó a medias dice cuánto borró y el error", async ({ page }) => {
	await page.route("**/api/v1/maintenance/storage", async (r) => {
		const json = await (await r.fetch()).json();
		json.retention.last = { at: Date.now() - 60000, days: 1, runs: 875, files: 52545, freed_bytes: 5719252473, trigger: "auto", error: "database or disk is full (13)" };
		return r.fulfill({ json });
	});
	await page.goto("/#view=maintenance");
	const card = page.locator(".stor-card");
	await expect(card).toContainText("875 ejecución(es)");
	await expect(card.locator(".stor-cut")).toContainText("se cortó con un error: database or disk is full (13)");
});

// Las páginas del Inspector (lo que más crece con TRACEREPORTS_DOM_STEPS): cuánto ocupan en total y las
// ejecuciones más pesadas, cada una con su enlace.
test("Almacenamiento muestra lo que ocupan las páginas del Inspector por ejecución", async ({ page, request }) => {
	const run = (await (await request.post("/api/v1/runs", { data: { name: `zz-paginas-${Date.now()}`, environment: "qa" } })).json()).run_id;
	const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "test_con_paginas" } })).json()).test_id;
	const nodes = Array.from({ length: 200 }, (_, i) => ({ i, p: i - 1, tag: "div", text: `fila ${i}`, x: 0, y: i * 20, w: 400, h: 20, visible: true, depth: i }));
	for (const kind of ["step", "step", "fail"]) {
		expect((await request.post(`/api/v1/tests/${id}/dom`, { data: { kind, label: kind, url: "https://app/x", viewport: { w: 1000, h: 800 }, nodes, elements: [] } })).ok()).toBeTruthy();
	}
	await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL", error_message: "x" } });
	await request.patch(`/api/v1/runs/${run}/finish`, { data: {} });
	const size = await (await request.get(`/api/v1/runs/${run}/dom/size`)).json();
	expect(size.pages).toBe(3);

	await page.goto("/#view=maintenance");
	const dom = page.locator(".stor-card .stor-dom");
	await expect(dom.locator("summary")).toContainText("Páginas del Inspector:");
	await dom.locator("summary").click();
	await expect(dom).toContainText("Se guardan comprimidas");
	const row = dom.locator("tr", { hasText: `#${run} ` });
	if (await row.count()) await expect(row.locator("td").nth(1)).toHaveText("3"); // si entra entre las 10 más pesadas
});
