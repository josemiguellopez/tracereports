// En vivo: lo que corre ahora, por ejecución. Un lanzador que crea una corrida por archivo con el mismo
// execution_id, tests planificados, un test que deja de enviar ("sin señal": 4 s en el e2e, ver
// playwright.config.js), el Gantt por worker, la tabla con lo que pide acción primero, la actualización
// en vivo y la cancelación cooperativa. Va al final (zz_): crea ejecuciones.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

const exec = `e2e-${Date.now()}`;
const created = [];
async function newRun(request, name, planned) {
	const id = (await (await request.post("/api/v1/runs", { data: { name, environment: "qa", project: "box", execution_id: exec, planned } })).json()).run_id;
	created.push(id);
	return id;
}
async function newTest(request, run, name, worker) {
	return (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name, key: `${exec}::${name}`, worker } })).json()).test_id;
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

let ficha, agenda, stuck, alive;

test("En vivo: progreso, carriles por worker, sin señal primero y el último paso", async ({ page, request }) => {
	ficha = await newRun(request, "test_ficha.py", 3);
	agenda = await newRun(request, "test_agenda.py", 2);
	const ok = await newTest(request, ficha, "test_abrir_ficha", "gw0");
	await request.post(`/api/v1/tests/${ok}/logs`, { data: { status: "PASS", message: "ficha abierta" } });
	await request.patch(`/api/v1/tests/${ok}/finish`, { data: { status: "PASS" } });
	stuck = await newTest(request, ficha, "test_periodontograma", "gw1");
	await request.post(`/api/v1/tests/${stuck}/logs`, { data: { status: "INFO", message: "Evidencia final del periodontograma" } });
	alive = await newTest(request, agenda, "test_agenda_del_dia", "gw0");

	await page.goto(`/#view=live&exec=${encodeURIComponent("x:" + exec)}`);
	const view = page.locator("#view-live");
	await expect(view).toBeVisible();
	await expect(page.locator('.side-nav a[data-view="live"]')).toHaveClass(/active/);
	const chip = view.locator(".xp-btn");
	await expect(chip).toContainText(exec);
	await expect(chip.locator(".xp-fresh")).toHaveText("en curso");
	await expect(view.locator(".live-head h3")).toHaveText(exec);
	await expect(view.locator(".live-badge.on")).toContainText("EN CURSO");
	await expect(view.locator(".live-pct")).toContainText("1 de 5 tests · 20%");
	await expect(view.locator(".lg-worker b")).toHaveText(["gw0", "gw1"]);
	await expect(view.locator(".lg-lane").first().locator(".lg-bar")).toHaveCount(2);

	// el test que dejó de enviar pasa a "sin señal" sin recargar (sin eventos: el reloj de la vista)
	const stuckRow = view.locator(`tr[data-live-test="${stuck}"]`);
	await expect(stuckRow).toHaveClass(/nosig/, { timeout: 20_000 });
	await expect(stuckRow.locator(".lt-state")).toHaveText("Sin señal");
	await expect(stuckRow.locator(".lt-step")).toContainText("Evidencia final del periodontograma");
	await expect(view.locator(".live-count.nosig")).toBeVisible();
	await expect(chip.locator(".xp-b.nosig")).toBeVisible(); // la agenda tampoco envió nada
	// la corrida con un problema va primero y abierta (aquí las dos: la agenda tampoco envió nada)
	await expect(view.locator(".live-run").first()).toHaveClass(/open/);
	await expect(view.locator(".live-run").first().locator("tr.nosig")).toHaveCount(1);

	// un resultado llega por SSE y la vista se pone al día
	await request.post(`/api/v1/tests/${alive}/logs`, { data: { status: "PASS", message: "agenda filtrada" } });
	await request.patch(`/api/v1/tests/${alive}/finish`, { data: { status: "PASS" } });
	await expect(view.locator(`tr[data-live-test="${alive}"] .lt-state`)).toHaveText("Pass");
	await expect(view.locator(".live-pct")).toContainText("2 de 5 tests · 40%");

	// una fila abre el panel lateral (sin salir de la vista): estado, últimos pasos y abrir el test
	await stuckRow.locator("td.lt-step").click();
	const panel = view.locator(".live-panel");
	await expect(panel.locator("h4")).toHaveText("test_periodontograma");
	await expect(panel.locator(".live-facts")).toContainText("Sin señal");
	await expect(panel.locator(".live-facts")).toContainText("gw1");
	await expect(panel.locator(".ltr.step")).toHaveCount(1); // la traza: su único paso
	await panel.locator('[data-live-ptab="steps"]').click();
	await expect(panel.locator(".live-steps li")).toHaveText(["Evidencia final del periodontograma"].map((m) => new RegExp(m)));
	await expect(view.locator(`tr[data-live-test="${stuck}"]`)).toHaveClass(/sel/);
	await expect(page).toHaveURL(/view=live/);
	// sigue al día: un paso nuevo aparece arriba sin cerrar el panel
	await request.post(`/api/v1/tests/${stuck}/logs`, { data: { status: "INFO", message: "reintenta guardar" } });
	await expect(panel.locator(".live-steps li").first()).toContainText("reintenta guardar", { timeout: 15_000 });
	await page.keyboard.press("Escape");
	await expect(panel).toHaveCount(0);
	// una barra del Gantt también lo abre, y desde el panel se va al test
	await view.locator(`.lg-bar[data-live-test="${stuck}"]`).click();
	await panel.getByRole("link", { name: "Abrir el test" }).click();
	await expect(page).toHaveURL(new RegExp(`run=${ficha}&view=tests&test=${stuck}`));
	await expect(page.locator("#test-detail .detail-name")).toHaveText("test_periodontograma");
});

test("Cancelar ejecución: el pedido queda y el cliente lo ve en /control", async ({ page, request }) => {
	await page.goto(`/#view=live&exec=${encodeURIComponent("x:" + exec)}`);
	const view = page.locator("#view-live");
	await view.locator(".live-cancel summary").click();
	await expect(view.locator(".live-cancel form")).toContainText("TRACEREPORTS_REMOTE_CONTROL=1");
	await view.locator(".live-cancel input[name=reason]").fill("se cayó el ambiente de QA");
	await view.locator(".live-cancel button[type=submit]").click();
	await expect(view.locator(".live-cancelled")).toContainText("Cancelación pedida");
	const control = await (await request.get(`/api/v1/runs/${ficha}/control`)).json();
	expect(control).toMatchObject({ cancel: true, reason: "se cayó el ambiente de QA" });
	await expect(view.locator(".live-cancelled")).toContainText("al terminar el test en curso");
	// un proceso que estaba en cola abre su corrida después: también se detiene
	const queued = await newRun(request, "test_receta.py", 1);
	expect(await (await request.get(`/api/v1/runs/${queued}/control`)).json()).toMatchObject({ cancel: true, reason: "se cayó el ambiente de QA" });
});

test("En vivo sin nada corriendo explica qué va a mostrar; en el celular la tabla se lee", async ({ page }) => {
	await page.route("**/api/v1/live", (route) => route.fulfill({ json: { now: Date.now(), no_signal_ms: 4000, executions: [] } }));
	await page.goto("/#view=live");
	await expect(page.locator("#view-live .live-empty")).toContainText("No hay ejecuciones en curso");
	await page.unroute("**/api/v1/live");
	await page.setViewportSize({ width: 390, height: 800 });
	await page.goto(`/#view=live&exec=${encodeURIComponent("x:" + exec)}`);
	await expect(page.locator("#view-live .live-table").first()).toBeVisible();
	// la barra del menú se desplaza hasta la vista activa (En vivo queda a la derecha)
	const nav = await page.locator('.side-nav a[data-view="live"]').boundingBox();
	expect(nav.x >= 0 && nav.x + nav.width <= 390, `En vivo visible en la barra: ${JSON.stringify(nav)}`).toBeTruthy();
	const scroll = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
	expect(scroll, "sin scroll horizontal en el celular").toBeLessThanOrEqual(1);
});
