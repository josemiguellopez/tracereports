// Mientras alguien mira un reporte, otra persona lanza una ejecución: lo que se está viendo no
// cambia solo (ni el selector); un aviso ofrece verla en vivo. Va en su propio archivo, al final:
// crea ejecuciones nuevas y las demás pruebas cuentan con las del seed.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);
const created = [];

async function newRun(request, name) {
	const res = await request.post("/api/v1/runs", { data: { name, environment: "qa", project: "e2e", branch: "dev" } });
	expect(res.ok()).toBeTruthy();
	const { run_id: id } = await res.json();
	created.push(id);
	return id;
}

async function viewSeedRun(page) {
	await page.goto(`/#run=${seed().run}&view=tests`);
	await expect(page.locator("#report-name")).toHaveText("E2E actual");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(3);
}

test.afterAll(async ({ request }) => {
	for (const id of created) await request.patch(`/api/v1/runs/${id}/finish`, { data: {} });
});

test("otra ejecución empieza: no cambia la que se ve y avisa para verla en vivo", async ({ page, request }) => {
	const { run } = seed();
	await viewSeedRun(page);
	const live = await newRun(request, "E2E en vivo");

	const toast = page.locator("#run-toast");
	await expect(toast).toBeVisible();
	await expect(toast).toContainText("Empezó otra ejecución");
	await expect(toast).toContainText(`#${live} · E2E en vivo · qa · dev`);
	// lo que se estaba viendo sigue igual, también el selector
	await expect(page.locator("#run-select")).toHaveValue(String(run));
	await expect(page.locator("#report-name")).toHaveText("E2E actual");
	await expect(page.locator(`#run-select option[value="${live}"]`)).toHaveCount(1);
	// también se nota con la pestaña en segundo plano
	await expect(page).toHaveTitle(/^● /);
	await expect(page.locator("#new-run-badge")).toBeVisible();

	await toast.locator("[data-toast-open]").click();
	await expect(page.locator("#report-name")).toHaveText("E2E en vivo");
	await expect(page.locator("#run-select")).toHaveValue(String(live));
	await expect(page.locator("#live-pill")).toBeVisible();
	await expect(toast).toBeHidden();
	await expect(page.locator("#new-run-badge")).toBeHidden();
	await expect(page).not.toHaveTitle(/^● /);
	expect(page.url()).toContain(`run=${live}`);
});

test("'Ahora no' deja la insignia junto al selector, y la insignia lleva a la ejecución", async ({ page, request }) => {
	await viewSeedRun(page);
	const live = await newRun(request, "E2E más tarde");
	const toast = page.locator("#run-toast");
	await toast.locator("[data-toast-close]").click();
	await expect(toast).toBeHidden();
	const badge = page.locator("#new-run-badge");
	await expect(badge).toBeVisible();
	await expect(badge).toHaveText("1 nueva");
	await expect(page.locator("#report-name")).toHaveText("E2E actual");

	await badge.click();
	await expect(page.locator("#report-name")).toHaveText("E2E más tarde");
	await expect(page.locator("#run-select")).toHaveValue(String(live));
	await expect(badge).toBeHidden();
});

test("varias ejecuciones nuevas: el aviso las cuenta y vuelve a aparecer con cada una", async ({ page, request }) => {
	await viewSeedRun(page);
	const toast = page.locator("#run-toast");
	await newRun(request, "E2E shard 1");
	await expect(toast).toContainText("E2E shard 1");
	await toast.locator("[data-toast-close]").click();
	const second = await newRun(request, "E2E shard 2");
	await expect(toast).toBeVisible(); // otra nueva: avisa de nuevo aunque se haya cerrado
	await expect(toast).toContainText("2 ejecuciones nuevas en curso");
	await expect(toast).toContainText(`#${second} · E2E shard 2`);
	await expect(page.locator("#new-run-badge")).toHaveText("2 nuevas");
	// cambiar con el selector también cuenta como vista
	await page.locator("#run-select").selectOption(String(second));
	await expect(page.locator("#report-name")).toHaveText("E2E shard 2");
	await expect(page.locator("#new-run-badge")).toHaveText("1 nueva");
});
