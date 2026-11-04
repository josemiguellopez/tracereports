// Ejecuciones cortadas: una que dejó de recibir datos avisa "sin actividad" y se puede cerrar a mano
// con un motivo; sus tests sin terminar quedan Interrumpido (no cuentan como fallo). Va al final
// (zz_): crea ejecuciones nuevas y watch.spec.js necesita que el seed siga entre las 10 recientes.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

async function runningRun(request, name) {
	const run = (await (await request.post("/api/v1/runs", { data: { name, environment: "qa" } })).json()).run_id;
	const login = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "login" } })).json()).test_id;
	await request.post(`/api/v1/tests/${login}/logs`, { data: { status: "PASS", message: "entra al dashboard" } });
	await request.patch(`/api/v1/tests/${login}/finish`, { data: { status: "PASS" } });
	const cut = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "busca cliente" } })).json()).test_id;
	await request.post(`/api/v1/tests/${cut}/logs`, { data: { status: "INFO", message: "filtra la grilla" } });
	return { run, cut };
}

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("una ejecución sin actividad avisa y se cierra a mano con motivo: sus tests quedan Interrumpido", async ({ page, request }) => {
	const { run, cut } = await runningRun(request, "Colgada E2E");
	await page.clock.install();
	await page.goto(`/#run=${run}&view=tests`);
	await expect(page.locator("#report-name")).toHaveText("Colgada E2E");
	const banner = page.locator("#run-banner");
	await expect(banner).toBeHidden(); // recién recibió datos: corre normal

	await page.clock.fastForward("06:00"); // 6 minutos sin que llegue nada
	await expect(banner).toBeVisible();
	await expect(banner).toContainText("Sin actividad hace 6 min");
	await banner.locator("[data-close-run-open]").click();
	const form = banner.locator("[data-close-run-form]");
	await form.locator('select[name="reason"]').selectOption("Se cayó la máquina o el CI");
	await form.locator('input[name="detail"]').fill("detuve la suite desde el IDE");
	await form.locator('button[type="submit"]').click();

	// la ejecución queda cerrada, incompleta y en Warning; el test cortado no es un fallo
	await expect(banner).toContainText("Cerrada manualmente");
	await expect(banner).toContainText("Se cayó la máquina o el CI: detuve la suite desde el IDE");
	await expect(banner).toContainText("1 test(s) quedaron como Interrumpido");
	const r = await (await request.get(`/api/v1/runs/${run}`)).json();
	expect(r).toMatchObject({ status: "WARNING", incomplete: true, interrupted: 1, failed: 0, passed: 1 });
	const t = await (await request.get(`/api/v1/tests/${cut}`)).json();
	expect(t.status).toBe("INTERRUPTED");
	expect(t.error_message).toContain("Motivo: Se cayó la máquina o el CI: detuve la suite desde el IDE");

	// en la lista: su etiqueta y su filtro; en el detalle, por qué
	const item = page.locator("#test-collection .collection-item", { hasText: "busca cliente" });
	await expect(item.locator(".label.interrupted")).toBeVisible();
	await banner.locator("[data-banner-interrupted]").click();
	await expect(page.locator('#status-filters [data-status="INTERRUPTED"]')).toHaveClass(/active/);
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(1);
	await item.locator(".test-name").click();
	await expect(page.locator("#test-detail .interrupted-block")).toContainText("no cuenta como fallo");
	// el formulario no vuelve a aparecer: ya no está en curso
	await expect(banner.locator("[data-close-run-open]")).toHaveCount(0);
});

test("cerrar una ejecución que sigue en curso desde la API del cliente (Ctrl+C) también deja sus tests Interrumpido", async ({ page, request }) => {
	const { run } = await runningRun(request, "Ctrl+C E2E");
	await request.patch(`/api/v1/runs/${run}/finish`, { data: { interrupted: true } });
	await page.goto(`/#run=${run}&view=tests`);
	const banner = page.locator("#run-banner");
	await expect(banner).toContainText("La ejecución se cortó antes de terminar");
	await expect(page.locator('#status-filters [data-status="INTERRUPTED"]')).toBeVisible();
	await expect(page.locator("#test-collection .collection-item", { hasText: "busca cliente" }).locator(".label.interrupted")).toBeVisible();
	await page.locator('[data-view="dashboard"]').first().click();
	await expect(page.locator("#env-table")).toContainText("Interrumpidos");
});
