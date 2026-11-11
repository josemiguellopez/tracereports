// Escalar desde En vivo: con varios fallos, el botón escala el fallo elegido (no se queda con el último test
// que se escaló antes). Crea runs: va al final (zz_).
const { test, expect } = require("@playwright/test");

test("En vivo: «Escalar» sigue al fallo elegido, desde el resumen y desde su panel; sin elegir, la ejecución completa", async ({ page, request }) => {
	const exe = `e2e-escalar-${Date.now()}`;
	const { run_id: run } = await (await request.post("/api/v1/runs", { data: { name: "shop/test_checkout.py", environment: "qa", execution_id: exe, planned: 3 } })).json();
	const ids = {};
	for (const name of ["test_001_pagar", "test_002_pagar", "test_003_pagar"]) {
		const { test_id: id } = await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name } })).json();
		await request.post(`/api/v1/tests/${id}/logs`, { data: { status: "FAIL", message: `TimeoutException en ${name}` } });
		await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL", error_message: `TimeoutException en ${name}` } });
		ids[name] = id;
	}
	await request.patch(`/api/v1/runs/${run}/finish`, {});
	const live = `/#view=live&exec=${encodeURIComponent(`x:${exe}`)}`;
	const btn = page.locator(".live-summary [data-live-escalate]");
	const scope = page.locator("#esc-scope");

	// sin elegir: todos los fallos
	await page.goto(live);
	await expect(btn).toHaveText(/Escalar los fallos/);
	await btn.click();
	await expect(scope).toHaveValue("0");
	// en Escalar se elige test_001 (lo que quedaba pegado)
	await scope.selectOption(String(ids.test_001_pagar));

	// vuelve a En vivo y elige test_003: el resumen y el panel escalan ese, no test_001
	await page.goto(live);
	await page.locator(".la-row", { hasText: "test_003_pagar" }).click();
	await expect(btn).toHaveText(/Escalar este fallo/);
	await expect(btn).toHaveAttribute("data-tip-title", "test_003_pagar");
	await page.locator(".live-panel [data-live-escalate]").click();
	await expect(page).toHaveURL(new RegExp(`run=${run}&view=escalate`));
	await expect(scope).toHaveValue(String(ids.test_003_pagar));

	// otro: test_002, desde el botón del resumen
	await page.goto(live);
	await page.locator(".la-row", { hasText: "test_002_pagar" }).click();
	await btn.evaluate((a) => a.click()); // el panel abierto lo tapa a la vista; el clic es el mismo
	await expect(scope).toHaveValue(String(ids.test_002_pagar));

	// al cerrar el panel, el elegido SIGUE elegido (antes volvía a "los fallos" y Escalar abría otro test)
	await page.goto(live);
	await page.locator(".la-row", { hasText: "test_003_pagar" }).click();
	await page.locator("[data-live-close]").click();
	await expect(page.locator(".live-panel")).toHaveCount(0);
	await expect(btn).toHaveText(/Escalar este fallo/);
	await expect(btn).toHaveAttribute("data-tip-title", "test_003_pagar");
	await btn.click();
	await expect(scope).toHaveValue(String(ids.test_003_pagar));
	// la × lo suelta: vuelve a ser la ejecución completa
	await page.goto(live);
	await page.locator(".la-row", { hasText: "test_003_pagar" }).click();
	await page.locator("[data-live-close]").click();
	await page.locator(".live-summary [data-live-escunpick]").click();
	await expect(btn).toHaveText(/Escalar los fallos/);
	await btn.click();
	await expect(scope).toHaveValue("0");
});

test("Escalar con la suite corriendo: la evidencia que sigue llegando no borra ni cancela el resumen (antes parpadeaba y había que pedirlo dos veces)", async ({ page, request }) => {
	const exe = `e2e-escalar-vivo-${Date.now()}`;
	const { run_id: run } = await (await request.post("/api/v1/runs", { data: { name: "shop/test_checkout.py", environment: "qa", execution_id: exe, planned: 5 } })).json();
	const { test_id: failed } = await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "test_001_pagar" } })).json();
	await request.post(`/api/v1/tests/${failed}/logs`, { data: { status: "FAIL", message: "TimeoutException en el botón Pagar" } });
	await request.patch(`/api/v1/tests/${failed}/finish`, { data: { status: "FAIL", error_message: "TimeoutException en el botón Pagar" } });
	const { test_id: other } = await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "test_002_sigue" } })).json(); // el run sigue abierto
	const log = (id, message) => request.post(`/api/v1/tests/${id}/logs`, { data: { status: "INFO", message } });

	await page.goto(`/#run=${run}&view=escalate`);
	await page.locator("#esc-scope").selectOption(String(failed));
	await page.locator('[data-esc-ai="0"]').click();
	// el armado tarda: mientras, otro test del mismo run manda pasos (antes, cada uno cancelaba el armado)
	let release;
	const gate = new Promise((r) => { release = r; });
	await page.route("**/api/v1/ui/escalate", async (route) => { await gate; await route.continue(); });
	await page.locator("[data-esc-gen]").click();
	await expect(page.locator(".esc-loading")).toBeVisible();
	for (let i = 0; i < 3; i++) await log(other, `paso ${i}`);
	await log(failed, "un paso más del que falló"); // y uno del elegido, mientras se arma
	await page.waitForTimeout(800);
	release();
	const card = page.locator(".esc-frame");
	await expect(card).toBeVisible();
	await expect(card).toContainText("test_001_pagar");
	await expect(page.locator(".esc-stale")).toContainText("Llegó evidencia nueva"); // el del elegido sí cuenta, sin borrar nada
	// ya armado: lo que llega de otros tests no lo toca; nada parpadea
	await page.locator(".esc-stale [data-esc-gen]").click();
	await expect(page.locator(".esc-stale")).toHaveCount(0);
	for (let i = 3; i < 6; i++) await log(other, `paso ${i}`);
	await page.waitForTimeout(800);
	await expect(card).toBeVisible();
	await expect(page.locator(".esc-stale")).toHaveCount(0);
	await request.patch(`/api/v1/tests/${other}/finish`, { data: { status: "PASS" } });
	await request.patch(`/api/v1/runs/${run}/finish`, {});
});
