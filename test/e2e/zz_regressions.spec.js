// Regresiones del feedback de un cliente: un test que el runner cierra en PASS con un paso en WARNING
// se ve como Warning (no queda escondido en verde), y el switch de auto-scroll no queda bajo Trace
// Buddy, con su panel cerrado o abierto. Va último (zz_): crea una ejecución, y la lista muestra
// las 10 últimas; las demás pruebas cuentan con que la del seed siga ahí. Primero el auto-scroll, con un
// test en curso; al final se cierra todo y se revisan los estados.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

let run, live;

async function api(request, method, url, data) {
	const res = await request.fetch(`/api/v1${url}`, { method, data });
	expect(res.ok(), `${method} ${url}: ${res.status()}`).toBeTruthy();
	return res.json();
}

/** Un test con sus pasos; status null lo deja corriendo. */
async function addTest(request, name, steps, status) {
	const { test_id: id } = await api(request, "POST", `/runs/${run}/tests`, { name, suite: "regresion", key: `regresion ${name}` });
	for (const [st, message] of steps) await api(request, "POST", `/tests/${id}/logs`, { status: st, message });
	if (status !== null) await api(request, "PATCH", `/tests/${id}/finish`, { status });
	return id;
}

test.beforeAll(async ({ request }) => {
	({ run_id: run } = await api(request, "POST", "/runs", { name: "Regresión warning y auto-scroll", environment: "qa", project: "e2e-regresion", branch: "dev" }));
	live = await addTest(request, "en curso", [["INFO", "paso 1"]], null);
});

test.afterAll(async ({ request }) => {
	await request.patch(`/api/v1/runs/${run}/finish`, { data: {} });
});

const overlap = (a, b) => a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;

/** El switch se ve, no se cruza con Trace Buddy (ni con su panel) y es lo que recibe el clic en su centro. */
async function expectToggleFree(page) {
	const toggle = page.locator("#autoscroll-toggle");
	await expect(toggle).toBeVisible();
	const box = await toggle.boundingBox();
	for (const sel of [".buddy-launch", "#buddy-panel"]) {
		const other = page.locator(sel);
		if (await other.isVisible()) expect(overlap(box, await other.boundingBox()), `el switch se cruza con ${sel}`).toBe(false);
	}
	const onTop = await page.evaluate(({ x, y }) => !!document.elementFromPoint(x, y)?.closest("#autoscroll-toggle"),
		{ x: box.x + box.width / 2, y: box.y + box.height / 2 });
	expect(onTop, "otro elemento tapa el switch").toBe(true);
}

for (const [label, viewport] of [["escritorio", { width: 1440, height: 900 }], ["teléfono", { width: 390, height: 844 }]]) {
	test(`auto-scroll: Trace Buddy no lo tapa, cerrado ni abierto (${label})`, async ({ page }) => {
		await page.setViewportSize(viewport);
		await page.goto(`/#run=${run}&view=tests&test=${live}`);
		await expect(page.locator("#buddy-fab")).toBeVisible();
		await page.waitForTimeout(700); // la entrada animada de Trace Buddy
		await expectToggleFree(page);
		await page.locator("#buddy-fab").click();
		await expect(page.locator("#buddy-panel")).toBeVisible();
		await page.waitForTimeout(400); // la apertura del panel
		await expectToggleFree(page);
		// y sigue funcionando
		await page.locator("#autoscroll-toggle").click();
		await expect(page.locator("#autoscroll-toggle")).toHaveAttribute("aria-checked", "false");
		await page.locator("#autoscroll-toggle").click();
	});
}

test("2 pasan y 1 pasa con un paso en WARNING: ese test y la ejecución quedan en Warning", async ({ page, request }) => {
	await api(request, "PATCH", `/tests/${live}/finish`, { status: "PASS" });
	await addTest(request, "busca cliente", [["PASS", "abre el buscador"], ["WARNING", "el combo tardó 4 s en cargar"], ["PASS", "elige al cliente"]], "PASS");
	await addTest(request, "cierra sesión", [["PASS", "sale"]], "PASS");
	await api(request, "PATCH", `/runs/${run}/finish`, {});

	const detail = await api(request, "GET", `/runs/${run}`);
	expect(detail.tests.map((t) => [t.name, t.status])).toEqual(expect.arrayContaining([
		["en curso", "PASS"], ["busca cliente", "WARNING"], ["cierra sesión", "PASS"]]));
	expect(detail.status).toBe("WARNING");

	await page.goto(`/#run=${run}&view=tests`);
	const items = page.locator("#test-collection .collection-item");
	await expect(items).toHaveCount(3);
	await expect(items.filter({ hasText: "busca cliente" }).locator(".label")).toHaveText(/warning/i);
	for (const name of ["en curso", "cierra sesión"]) await expect(items.filter({ hasText: name }).locator(".label")).toHaveText(/pass/i);
	// el filtro Warning muestra solo ese test
	await page.locator('#status-filters button[data-status="WARNING"]').click();
	await expect(items.filter({ visible: true })).toHaveCount(1);
});
