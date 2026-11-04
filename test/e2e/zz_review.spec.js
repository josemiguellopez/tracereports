// Lo que pidió el cliente al revisar: Recientes con el estado y los resultados de cada ejecución,
// Release sin tarjetas que se salen con nombres largos, y una comparación que dice qué compara
// contra qué y deja buscar otra base. Va al final (zz_): crea ejecuciones nuevas.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

const token = `review-${Date.now()}`;
const longName = "test_deshabilitar_segmento_horario_agenda_chrome_con_un_nombre_todavia_mas_largo";

async function run(request, name, statuses, finish = true) {
	const id = (await (await request.post("/api/v1/runs", { data: { name, environment: "preproduccion", project: token, branch: "tracereports" } })).json()).run_id;
	for (const [i, st] of statuses.entries()) {
		const t = (await (await request.post(`/api/v1/runs/${id}/tests`, { data: { name: `test_${i}`, category: longName } })).json()).test_id;
		if (st !== "RUNNING") await request.patch(`/api/v1/tests/${t}/finish`, { data: { status: st } });
	}
	if (finish) await request.patch(`/api/v1/runs/${id}/finish`, { data: {} });
	return id;
}

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

let green, red, live;

test("Recientes: estado, #id y resultados de cada ejecución, con filtro y pestañas", async ({ page, request }) => {
	green = await run(request, `${token} verde`, ["PASS", "PASS"]);
	red = await run(request, `${token} roja`, ["PASS", "FAIL", "FAIL"]);
	live = await run(request, `${token} en vivo`, ["PASS", "RUNNING"], false);
	await page.goto(`/#run=${red}&view=dashboard`);
	await expect(page.locator("#report-name")).toHaveText(`${token} roja`);
	await expect(page.locator("#recents-live")).toContainText("en curso");
	await page.locator("#recents-control summary").click();
	await expect(page.locator("#recents-filter")).toBeFocused();
	const row = (id) => page.locator(`#recents-list [data-recent-run="${id}"]`);
	await expect(row(red)).toHaveClass(/current/);
	await expect(row(red).locator(".run-state")).toHaveText("Fail");
	await expect(row(red)).toContainText(`#${red}`);
	await expect(row(red)).toContainText("1/3 pasan");
	await expect(row(green).locator(".run-state")).toHaveText("Pass");
	await expect(row(live).locator(".run-state")).toHaveText("En curso");

	await page.locator('[data-recents-tab="FAIL"]').click();
	await expect(row(red)).toBeVisible();
	await expect(row(green)).toHaveCount(0);
	await page.locator('[data-recents-tab=""]').click();
	await page.locator("#recents-filter").fill(`#${green}`);
	await expect(page.locator("#recents-list [data-recent-run]")).toHaveCount(1);
	await page.keyboard.press("Enter"); // abre la primera que coincide
	await expect(page.locator("#report-name")).toHaveText(`${token} verde`);
	await expect(page.locator("#recents-control")).not.toHaveAttribute("open", "");
});

test("Release: un nombre largo de funcionalidad no se sale de su tarjeta ni ensancha la página", async ({ page }) => {
	await page.setViewportSize({ width: 1100, height: 800 });
	await page.goto(`/#run=${green}&view=release`);
	const card = page.locator(".rel-feature").first();
	await expect(card).toContainText(longName);
	const [box, name] = [await card.boundingBox(), await card.locator(".rel-feature-name").boundingBox()];
	expect(name.x + name.width).toBeLessThanOrEqual(box.x + box.width + 1);
	expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("Análisis: la comparación muestra las dos ejecuciones y se puede elegir otra base buscándola", async ({ page }) => {
	await page.goto(`/#run=${red}&view=dashboard`);
	const cmp = page.locator("#compare-card");
	await expect(cmp.locator(".cmp-run.this")).toContainText(`#${red}`);
	await expect(cmp.locator(".cmp-run.base")).toContainText(`#${green}`);
	await expect(cmp.locator(".cmp-run.base .run-state")).toHaveText("Pass");
	await expect(cmp.locator(".cmp-chip.new_failures")).toHaveText("1 fallo nuevo"); // test_1: pasaba y ahora falla
	await expect(cmp.locator(".cmp-only.new_tests")).toContainText("test_2"); // no existía en la verde
	await expect(cmp.locator(".cmp-summary")).toContainText("De los 2 test(s) que corrieron en las dos: 1 empezó a fallar, 1 sin cambios.");

	await cmp.locator("[data-cmp-pick]").click();
	await expect(cmp.locator("[data-cmp-q]")).toBeFocused();
	await cmp.locator("[data-cmp-q]").fill(`#${green}`);
	await expect(cmp.locator(".cmp-picker-list [data-cmp-choose]")).toHaveCount(1);
	await cmp.locator("[data-cmp-q]").fill("no-existe-nada");
	await expect(cmp.locator(".cmp-picker-list")).toContainText("Ninguna ejecución coincide");
	// el atajo a la última que pasó entera
	await cmp.locator(".cmp-quick [data-cmp-choose]").nth(1).click();
	await expect(cmp.locator(".cmp-picker")).toHaveCount(0);
	await expect(cmp.locator(".cmp-run.base")).toContainText(`#${green}`);
	await expect(cmp.locator(".cmp-reason")).toContainText("Elegida a mano");
});

test("Análisis: comparar con otra suite dice que no hay nada que comparar (no 'sin cambios')", async ({ page, request }) => {
	// otra suite del mismo proyecto y ambiente: ningún test en común con la roja
	const other = (await (await request.post("/api/v1/runs", { data: { name: `${token} otra suite`, environment: "preproduccion", project: token, branch: "tracereports" } })).json()).run_id;
	const t = (await (await request.post(`/api/v1/runs/${other}/tests`, { data: { name: "test_de_otra_suite" } })).json()).test_id;
	await request.patch(`/api/v1/tests/${t}/finish`, { data: { status: "PASS" } });
	await request.patch(`/api/v1/runs/${other}/finish`, { data: {} });
	await page.goto(`/#run=${red}&view=dashboard`);
	const cmp = page.locator("#compare-card");
	await cmp.locator("[data-cmp-pick]").click();
	// en el selector, la de otra suite dice que no comparte tests y va al final
	const rows = cmp.locator(".cmp-picker-list [data-cmp-choose]");
	await expect(rows.first()).toContainText("en común");
	await expect(cmp.locator(`.cmp-picker-list [data-cmp-choose="${green}"]`)).toContainText("2 test(s) en común");
	await cmp.locator("[data-cmp-q]").fill(`#${live}`);
	await expect(rows).toHaveCount(0); // la que sigue en curso no es candidata
	await cmp.locator("[data-cmp-q]").fill("");
	const otherSuite = (await request.get(`/api/v1/runs/${red}/baselines`).then((r) => r.json())).find((b) => b.shared === 0);
	await expect(cmp.locator(`.cmp-picker-list [data-cmp-choose="${otherSuite.id}"]`)).toContainText("sin tests en común");
	await cmp.locator(`.cmp-picker-list [data-cmp-choose="${otherSuite.id}"]`).click();
	await expect(cmp.locator(".cmp-summary.none")).toContainText("No hay nada que comparar.");
	await expect(cmp).not.toContainText("Sin cambios");
	await expect(cmp.locator(".cmp-chips")).toHaveCount(0);
	await expect(cmp.locator(".cmp-table")).toHaveCount(0);
});
