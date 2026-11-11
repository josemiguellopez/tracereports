// Observaciones del cliente (05-11-2026): el historial de cuadros no dejaba volver hacia adelante,
// "Ver en vivo" desde Métricas no llevaba a la ejecución, Recientes no se actualizaba sin F5 y la
// tarjeta de locator decía "no hay snapshot" cuando sí lo había. Va al final (zz_): crea ejecuciones.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

const token = `nov5-${Date.now()}`;
const ctx = { environment: "qa", project: token, branch: "main" };

async function newRun(request, name) {
	return (await (await request.post("/api/v1/runs", { data: { name, ...ctx } })).json()).run_id;
}
async function newTest(request, run, name, finish) {
	const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name } })).json()).test_id;
	if (finish) await request.patch(`/api/v1/tests/${id}/finish`, { data: finish });
	return id;
}
const finishRun = (request, run) => request.patch(`/api/v1/runs/${run}/finish`, { data: {} });

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("Historial: desde un cuadro viejo se puede volver hacia adelante", async ({ page, request }) => {
	const tests = [];
	for (const st of ["PASS", "FAIL", "PASS"]) {
		const run = await newRun(request, `${token} historial`);
		tests.push([run, await newTest(request, run, "test_historial", { status: st, error_message: st === "FAIL" ? "AssertionError: 1 != 2" : "" })]);
		await finishRun(request, run);
	}
	const [oldest, , newest] = tests;
	await page.goto(`/#run=${newest[0]}&view=tests&test=${newest[1]}`);
	const cells = page.locator("#test-detail .hist-cell");
	await expect(cells).toHaveCount(3);
	await cells.first().click(); // la más antigua
	await expect(page).toHaveURL(new RegExp(`test=${oldest[1]}$`));
	await expect(cells, "las posteriores siguen en el historial").toHaveCount(3);
	await expect(cells.first()).toHaveClass(/current/);
	await cells.last().click(); // y se vuelve a la más nueva
	await expect(page).toHaveURL(new RegExp(`test=${newest[1]}$`));
	await expect(cells.last()).toHaveClass(/current/);
});

test("Ver en vivo desde Métricas abre la ejecución nueva", async ({ page, request }) => {
	const base = await newRun(request, `${token} base`);
	await newTest(request, base, "test_base", { status: "PASS" });
	await finishRun(request, base);
	await page.goto(`/#run=${base}&view=metrics`);
	await expect(page.locator("#view-metrics")).toBeVisible();
	const live = await newRun(request, `${token} nueva en vivo`);
	const toast = page.locator("#run-toast");
	await expect(toast).toBeVisible();
	await toast.locator("[data-toast-open]").click();
	await expect(page.locator("#view-metrics")).toBeHidden();
	await expect(page.locator("#view-tests")).toBeVisible();
	await expect(page.locator("#report-name")).toHaveText(`${token} nueva en vivo`);
	await expect(page).toHaveURL(new RegExp(`run=${live}&view=tests`));
	await finishRun(request, live);
});

test("Recientes se actualiza sola mientras está abierta", async ({ page, request }) => {
	const base = await newRun(request, `${token} recientes base`);
	await newTest(request, base, "test_a", { status: "PASS" });
	await finishRun(request, base);
	await page.goto(`/#run=${base}&view=tests`);
	await page.locator("#recents-control summary").click();
	const live = await newRun(request, `${token} recientes en vivo`);
	const row = page.locator(`#recents-list [data-recent-run="${live}"]`);
	await expect(row.locator(".run-state")).toHaveText("En curso");
	const t = await newTest(request, live, "test_b", { status: "PASS" });
	await expect(row, "el resultado del test llega sin F5").toContainText("1/1 pasan");
	await finishRun(request, live);
	await expect(row.locator(".run-state"), "y el cierre también").toHaveText("Pass");
	await expect(page.locator("#recents-control")).toHaveAttribute("open", "");
	expect(t).toBeGreaterThan(0);
});

test("Locator: con snapshot y sin reemplazos no dice que falta el snapshot", async ({ page, request }) => {
	const xp = "//div[contains(translate(normalize-space(.),'ABC','abc'),'segundo diagnostico texto libre') and contains(@class,'mr-auto')]";
	const run = await newRun(request, `${token} locator`);
	const id = await newTest(request, run, "test_complementar", {
		status: "FAIL",
		error_message: `Errores acumulados: ["Warning Linea: 2299 al verificar si el elemento '${xp.slice(0, 70)}`,
		error_trace: `Errores acumulados: ["Warning Linea: 2299 al verificar si el elemento '${xp}' "]`,
	});
	await request.post(`/api/v1/tests/${id}/dom`, { data: { url: "https://app/main", viewport: { w: 1382, h: 928 },
		elements: [{ tag: "button", id: "guardar", text: "Guardar", x: 10, y: 10, w: 90, h: 30, visible: true }] } });
	await finishRun(request, run);
	await page.goto(`/#run=${run}&view=tests&test=${id}`);
	const card = page.locator("#locator-card");
	await expect(card).toContainText("Hay snapshot de la página");
	await expect(card).toContainText("validación de contenido");
	await expect(card).not.toContainText("No hay snapshot");
	await expect(card.locator(".loc-title")).toHaveText("Selector de texto sin coincidencias");
	await expect(card.locator(".loc-failed"), "el selector entero, tomado del trace").toHaveText(xp);
});
