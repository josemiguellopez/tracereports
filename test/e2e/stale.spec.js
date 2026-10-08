// Respuestas que llegan fuera de orden: si se elige A y enseguida B, y la respuesta de A llega
// después de la de B, la pantalla sigue mostrando B (encabezado, selector, enlace y tests). Lo mismo
// con el detalle de un test y con un error de una petición que ya no corresponde.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);

test.beforeEach(async ({ page }, info) => {
	const errors = [];
	info.errors_ = errors;
	page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
	// el 500 simulado lo anota el navegador como recurso fallido: no es un error de la página
	page.on("console", (m) => { if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

/**
 * Retiene la primera respuesta GET de `pathname` hasta release(); `status` la reemplaza por un
 * error. arrived se cumple cuando el navegador ya la pidió.
 */
async function hold(page, pathname, status) {
	let arrivedResolve, releaseResolve, done;
	const arrived = new Promise((r) => { arrivedResolve = r; });
	const gate = new Promise((r) => { releaseResolve = r; });
	const finished = new Promise((r) => { done = r; });
	let held = false;
	await page.route((url) => url.pathname === pathname, async (route) => {
		if (held || route.request().method() !== "GET") return route.continue();
		held = true;
		const response = status ? null : await route.fetch();
		arrivedResolve();
		await gate;
		if (status) await route.fulfill({ status, contentType: "application/json", body: '{"error":"stale"}' });
		else await route.fulfill({ response });
		done();
	});
	return { arrived, release: async () => { releaseResolve(); await finished; await page.waitForTimeout(150); } };
}

async function openRun(page, id, name) {
	await page.goto(`/#run=${id}&view=tests`);
	await expect(page.locator("#report-name")).toHaveText(name);
}

async function expectRun(page, id, name) {
	await expect(page.locator("#report-name")).toHaveText(name);
	await expect(page.locator("#run-select")).toHaveValue(String(id));
	expect(await page.evaluate(() => location.hash)).toContain(`run=${id}`);
}

test("una ejecución pedida antes no pisa a la elegida después", async ({ page }) => {
	const { prev, run } = seed();
	await openRun(page, run, "E2E actual");
	const a = await hold(page, `/api/v1/runs/${prev}`);
	await page.locator("#run-select").selectOption(String(prev)); // A: retenida
	await a.arrived;
	const b = page.waitForResponse((r) => new URL(r.url()).pathname === `/api/v1/runs/${run}`);
	await page.locator("#run-select").selectOption(String(run)); // B: llega primero
	await b;
	await expectRun(page, run, "E2E actual");
	await a.release(); // A llega tarde
	await expectRun(page, run, "E2E actual");
	await expect(page.locator("#test-collection .collection-item", { hasText: "test_login_admin" })).toBeVisible();
	await expect(page.locator("#test-detail")).toContainText("test_login_admin");
});

test("el detalle de un test pedido antes no pisa al elegido después", async ({ page }) => {
	const { run } = seed();
	await openRun(page, run, "E2E actual");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(3);
	const ids = await page.locator("#test-collection .collection-item").evaluateAll((els) =>
		Object.fromEntries(els.map((e) => [e.querySelector(".test-name").textContent, e.dataset.test])));
	const a = await hold(page, `/api/v1/tests/${ids.test_reporte_mensual}`);
	await page.locator("#test-collection .collection-item", { hasText: "test_reporte_mensual" }).locator(".test-name").click();
	await a.arrived;
	await page.locator("#test-collection .collection-item", { hasText: "test_buscar_empleado" }).locator(".test-name").click();
	await expect(page.locator("#test-detail")).toContainText("test_buscar_empleado");
	await a.release();
	await expect(page.locator("#test-detail")).toContainText("test_buscar_empleado");
	await expect(page.locator("#test-detail")).not.toContainText("test_reporte_mensual");
	expect(await page.evaluate(() => location.hash)).toContain(`test=${ids.test_buscar_empleado}`);
});

test("el error de una petición que ya no corresponde no se muestra ni rompe la vista", async ({ page }) => {
	const { prev, run } = seed();
	await openRun(page, run, "E2E actual");
	const a = await hold(page, `/api/v1/runs/${prev}`, 500);
	await page.locator("#run-select").selectOption(String(prev));
	await a.arrived;
	await page.locator("#run-select").selectOption(String(run));
	await expectRun(page, run, "E2E actual");
	await a.release(); // el 500 de A llega cuando ya se ve B
	await expectRun(page, run, "E2E actual");
});
