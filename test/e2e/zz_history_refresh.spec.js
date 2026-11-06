// Historial de un test (cuadros verde/rojo/amarillo): un test abierto antes de que corriera otra
// ejecución guardaba su historial viejo; al volver a él desde un cuadro, la ejecución nueva desaparecía
// ("clic en el rojo y se pierde el amarillo; en el verde se mantiene"). Va al final (zz_): crea ejecuciones.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

const token = `hist-${Date.now()}`;
async function runWith(request, status) {
	const run = (await (await request.post("/api/v1/runs", { data: { name: `${token} ${status}`, environment: "qa", project: token } })).json()).run_id;
	const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "test_historial", key: `${token}::test_historial` } })).json()).test_id;
	if (status === "WARNING") {
		await request.post(`/api/v1/tests/${id}/logs`, { data: { status: "WARNING", message: "tardó más de lo normal" } });
		await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "PASS" } });
	} else {
		await request.patch(`/api/v1/tests/${id}/finish`, { data: { status, error_message: status === "FAIL" ? "AssertionError: 1 != 2" : "" } });
	}
	await request.patch(`/api/v1/runs/${run}/finish`, { data: {} });
	return { run, id };
}

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("el historial de un test ya abierto incluye las ejecuciones que terminaron después", async ({ page, request }) => {
	await runWith(request, "PASS");
	const red = await runWith(request, "FAIL");
	const cells = page.locator("#test-detail .hist-cell");

	// se mira el rojo cuando todavía es el último
	const streamUp = page.waitForResponse((r) => r.url().includes("/api/v1/stream"));
	await page.goto(`/#run=${red.run}&view=tests&test=${red.id}`);
	await expect(cells).toHaveCount(2);
	await streamUp; // conectada en vivo: el fin de la ejecución nueva le llega a esta pestaña

	// mientras, termina otra ejecución (amarillo) y se abre
	const yellow = await runWith(request, "WARNING");
	await page.goto(`/#run=${yellow.run}&view=tests&test=${yellow.id}`);
	await expect(page.locator("#report-name")).toHaveText(`${token} WARNING`);
	await expect(cells).toHaveCount(3);

	// clic en el rojo: el amarillo sigue ahí (antes desaparecía)
	await page.locator("#test-detail .hist-cell.fail").click();
	await expect(page.locator("#test-detail .hist-cell.fail")).toHaveClass(/current/);
	await expect(cells).toHaveCount(3);
	await expect(page.locator("#test-detail .hist-cell.warning")).toHaveCount(1);

	// y en el verde también
	await page.locator("#test-detail .hist-cell.pass").click();
	await expect(page.locator("#test-detail .hist-cell.pass")).toHaveClass(/current/);
	await expect(cells).toHaveCount(3);
});
