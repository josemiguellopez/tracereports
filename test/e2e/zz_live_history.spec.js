// En vivo sin nada corriendo ni reciente: la pantalla vacía lista las ejecuciones anteriores (del historial
// real del servidor) para abrir una sin saber su execution_id: se buscan en el servidor, por día, de a
// páginas. Crea runs: va al final (zz_).
const { test, expect } = require("@playwright/test");

async function execution(request, exe, file, results) {
	const { run_id: run } = await (await request.post("/api/v1/runs", { data: { name: file, environment: "qa", project: "acme_shop_web", execution_id: exe, planned: results.length } })).json();
	for (const [i, status] of results.entries()) {
		const { test_id: id } = await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: `test_00${i + 1}_${status.toLowerCase()}` } })).json();
		await request.post(`/api/v1/tests/${id}/logs`, { data: { status, message: `paso ${status}` } });
		await request.patch(`/api/v1/tests/${id}/finish`, { data: { status } });
	}
	await request.patch(`/api/v1/runs/${run}/finish`, {});
}

test("En vivo vacío: las anteriores del servidor, se buscan por archivo y por día, se cargan de a páginas y se abren aquí", async ({ page, request }) => {
	const stamp = Date.now();
	const exe = `e2e-historial-${stamp}`;
	await execution(request, exe, `shop/test_historial_checkout_${stamp}.py`, ["FAIL", "PASS"]);
	// más de una página (30): el resto de la suite e2e ya dejó muchas; estas aseguran que haya
	for (let i = 0; i < 32; i++) await execution(request, `e2e-relleno-${stamp}-${i}`, "shop/test_relleno.py", ["PASS"]);

	// nada en las últimas 2 horas (como al otro día): En vivo vacío; el historial es el real
	await page.route("**/api/v1/live", (r) => r.fulfill({ json: { now: Date.now(), no_signal_ms: 180000, executions: [] } }));
	await page.goto("/#view=live");
	const view = page.locator("#view-live");
	await expect(view.locator(".live-hist")).toContainText("Ninguna corre ahora ni terminó en las últimas 2 horas");
	await expect(view.locator(".lhist-day").first()).toHaveText("Hoy");
	const rows = view.locator("[data-live-histsel]");
	await expect(rows).toHaveCount(30); // una página
	await expect(view.locator(".lhist-count")).toContainText(/Mostrando 30 de \d/);

	// maestro-detalle: la página no se mueve; el scroll es de la lista y al llegar al final trae más (sin repetir)
	const pageTop = await page.evaluate(() => scrollY);
	await view.locator("[data-live-histpane]").evaluate((p) => { p.scrollTop = p.scrollHeight; });
	await expect.poll(() => rows.count()).toBeGreaterThan(30);
	expect(await page.evaluate(() => scrollY)).toBe(pageTop);
	const keys = await rows.evaluateAll((els) => els.map((e) => e.dataset.liveHistsel));
	expect(new Set(keys).size).toBe(keys.length);

	// buscar por un archivo (en el servidor: también lo que no estaba cargado)
	await view.locator("[data-live-hq]").fill(`historial_checkout_${stamp}`);
	await expect(rows).toHaveCount(1);
	const row = view.locator(`[data-live-histsel="x:${exe}"]`);
	await expect(row).toContainText(exe);
	await expect(row.locator(".xp-b.fail")).toContainText("1");
	await expect(row.locator(".lhist-pass")).toContainText("1");
	await expect(view.locator(".lhist-count")).toContainText("Mostrando 1 de 1");
	// la elegida a la derecha: sus números, lo que falló (con su error) y el enlace al reporte
	const preview = view.locator(".lhist-preview");
	await expect(preview).toContainText(exe);
	await expect(preview).toContainText("acme_shop_web · qa");
	await expect(preview.locator(".lhp-fails")).toContainText("test_001_fail");
	await expect(preview.locator(".lhp-fails")).toContainText("paso FAIL");
	await expect(preview.locator('a[href*="view=dashboard"]')).toBeVisible();
	await view.locator("[data-live-hq]").fill("no-existe-zzz");
	await expect(view.locator(".live-hist")).toContainText("Ninguna ejecución coincide.");

	// por día: el chip Ayer (ayer no hubo ninguna de estas) y "hoy" escrito
	await view.locator("[data-live-hq]").fill("");
	await view.locator('[data-live-histrange="yesterday"]').click();
	await expect(view.locator('[data-live-histrange="yesterday"]')).toHaveAttribute("aria-pressed", "true");
	await expect(view.locator(`[data-live-histsel="x:${exe}"]`)).toHaveCount(0);
	await view.locator('[data-live-histrange=""]').click();
	await view.locator("[data-live-hq]").fill("hoy");
	await expect(view.locator(".lhist-day").first()).toHaveText("Hoy");
	await expect(view.locator(".lhist-day")).toHaveCount(1);

	// con el teclado: un clic elige (no abre), ↓ pasa a la siguiente con su vista previa
	await rows.nth(0).click();
	await expect(page).not.toHaveURL(/exec=/);
	const second = await rows.nth(1).getAttribute("data-live-histsel");
	await page.keyboard.press("ArrowDown");
	await expect(rows.nth(1)).toHaveAttribute("aria-selected", "true");
	await expect(rows.nth(1)).toBeFocused();
	await expect(preview.locator(".lhp-acts [data-live-hist]")).toHaveAttribute("data-live-hist", second);

	// Enter abre la elegida: se ve aquí, con su resumen, y el selector la ofrece
	await view.locator("[data-live-hq]").fill(`historial_checkout_${stamp}`);
	await row.click();
	await page.keyboard.press("Enter");
	await expect(page).toHaveURL(new RegExp(`exec=x%3A${exe}`));
	await expect(view.locator(".live-summary")).toContainText("Ejecución terminada");
	await expect(view.locator(".xp-btn")).toContainText(exe);

	// con un test abierto en el panel, cambiar de ejecución lo cierra (era el panel de otra ejecución)
	await view.locator(".la-row", { hasText: "test_001_fail" }).click();
	await expect(view.locator(".live-panel")).toBeVisible();
	await view.locator(".xp-btn").click();
	await view.locator("[data-live-xq]").fill(`e2e-relleno-${stamp}-3`);
	const other = view.locator(`.xp-opt[data-live-exec="x:e2e-relleno-${stamp}-3"]`);
	await expect(other).toBeVisible(); // buscada en el servidor
	await other.click();
	await expect(page).toHaveURL(new RegExp(`exec=x%3Ae2e-relleno-${stamp}-3`));
	await expect(view.locator(".live-panel")).toHaveCount(0);
});
