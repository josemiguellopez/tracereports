// Una ejecución que empieza mientras la pestaña todavía no tiene la conexión en vivo (cargando, o
// reconectando) también se avisa: su evento "created" no llegó, pero al conectar se revisa qué empezó.
// Va al final (zz_): crea ejecuciones nuevas.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

const created = [];
async function newRun(request, name) {
	const id = (await (await request.post("/api/v1/runs", { data: { name, environment: "qa" } })).json()).run_id;
	created.push(id);
	return id;
}

test.afterAll(async ({ request }) => {
	for (const id of created) await request.patch(`/api/v1/runs/${id}/finish`, { data: {} });
});

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("una ejecución que empezó antes de conectar en vivo también se avisa", async ({ page, request }) => {
	const base = await newRun(request, "Catchup base E2E");
	await request.patch(`/api/v1/runs/${base}/finish`, { data: {} });
	// la conexión en vivo queda retenida hasta que la ejecución nueva ya empezó
	let release;
	const held = new Promise((r) => (release = r));
	await page.route("**/api/v1/stream", async (route) => { await held; await route.continue(); });

	await page.goto(`/#run=${base}&view=metrics`);
	await expect(page.locator("#view-metrics")).toBeVisible();
	const live = await newRun(request, "Catchup en vivo E2E");
	release();

	const toast = page.locator("#run-toast");
	await expect(toast).toBeVisible();
	await expect(toast).toContainText("Catchup en vivo E2E");
	await toast.locator("[data-toast-open]").click();
	await expect(page.locator("#report-name")).toHaveText("Catchup en vivo E2E");
	await expect(page).toHaveURL(new RegExp(`run=${live}&view=tests`));
});

test("las ejecuciones que ya estaban al abrir la página no se avisan", async ({ page, request }) => {
	const base = await newRun(request, "Catchup ya estaba E2E");
	await page.goto(`/#run=${base}&view=tests`);
	await expect(page.locator("#report-name")).toHaveText("Catchup ya estaba E2E");
	await expect(page.locator("#live-pill")).toBeVisible();
	await page.waitForTimeout(500); // ya conectó y revisó: nada que avisar
	await expect(page.locator("#run-toast")).toHaveCount(0);
});
