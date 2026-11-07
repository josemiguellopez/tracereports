// En vivo a escala: una ejecución con la forma de la suite que motivó el rediseño (79 corridas de 12
// procesos sin `worker`, ~230 tests), servida con page.route (fixtures/live_suite.js): los tiles que
// filtran, "Solo con problemas" activo por ser grande, carriles reutilizados y colapsados, el zoom del
// minimapa, agrupar, buscar sin perder el foco cuando la vista se refresca y el celular. No crea datos.
const { test, expect } = require("@playwright/test");
const { liveSuite } = require("./fixtures/live_suite.js");

let served = 0;
test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
	served = 0;
	await page.route("**/api/v1/live", (r) => r.fulfill({ json: liveSuite(Date.now()).list }));
	await page.route("**/api/v1/live/execution?**", (r) => { served++; return r.fulfill({ json: liveSuite(Date.now()).execution }); });
	await page.goto("/#view=live&exec=x%3Asuite-demo");
	await expect(page.locator("#view-live .live-tiles")).toBeVisible();
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("una ejecución grande abre con lo que pide acción: tiles, Solo con problemas y carriles reutilizados", async ({ page }) => {
	const view = page.locator("#view-live");
	await expect(view.locator("[data-live-exec-select] option:checked")).toContainText("rayen_salud_box_clinico · c3aec76");
	await expect(view.locator(".live-meta")).toContainText("79 corridas · 12 workers");
	await expect(view.locator('.live-tile[data-live-state="FAIL"] b')).toHaveText("3");
	await expect(view.locator('.live-tile[data-live-state="no_signal"]')).toContainText("Sin señal");
	await expect(view.locator(".live-pct")).toContainText("de 270 tests");
	// más de 20 corridas: Solo con problemas viene activo
	const chip = view.locator(".live-chip[data-live-problems]");
	await expect(chip).toHaveAttribute("aria-pressed", "true");
	await expect(chip).toHaveText("Solo con problemas (6)");
	await expect(view.locator("tr.lt-row")).toHaveCount(6);
	await expect(view.locator("tr.lt-row").first()).toHaveClass(/nosig/); // lo más severo primero
	await expect(view.locator(".live-collapsed")).toContainText("tests sin problemas ocultos");
	// sin worker: 12 carriles de proceso, los 6 sin problemas colapsados en una línea
	await expect(view.locator(".lg-lane")).toHaveCount(6);
	await expect(view.locator(".lg-worker").first()).toHaveText(/^P\d+ · \S+\.py$/); // sin "Clinico · test_"
	await view.locator(".lg-more").click();
	await expect(view.locator(".lg-lane")).toHaveCount(12);
	// el último paso en una línea, sin la URL; completo en el tooltip
	const step = view.locator("tr.lt-row.turtle .lt-step span").first();
	await expect(step).not.toContainText("http");
	await expect(step).toHaveAttribute("title", /url: https:\/\/preclinico\.demo/);
	// apagarlo: los archivos sin problemas quedan en una sola línea que se puede abrir
	await chip.click();
	await expect(chip).toHaveAttribute("aria-pressed", "false");
	await expect(view.locator(".live-collapsed")).toContainText(/\d+ archivos sin problemas \(\d+ tests\) · \d+ con warning/);
	const before = await view.locator(".live-run").count();
	await view.locator(".live-collapsed").click();
	expect(await view.locator(".live-run").count()).toBeGreaterThan(before + 70); // cada archivo, cerrado
	await expect(view.locator(".live-collapsed")).toHaveText("Ocultar los archivos sin problemas");
});

test("los tiles filtran; agrupar por estado; un grupo se cierra y sigue cerrado al refrescar", async ({ page }) => {
	const view = page.locator("#view-live");
	const fail = view.locator('.live-tile[data-live-state="FAIL"]');
	await fail.click();
	await expect(fail).toHaveAttribute("aria-pressed", "true");
	await expect(view.locator("tr.lt-row")).toHaveCount(3);
	await expect(view.locator("tr.lt-row .lt-state")).toHaveText(["Fallo", "Fallo", "Fallo"]);
	await view.locator('.live-tile[data-live-state="turtle"]').click();
	await expect(view.locator("tr.lt-row")).toHaveCount(5);
	await view.locator("[data-live-clear]").click();
	await expect(view.locator("tr.lt-row")).toHaveCount(6);

	await view.locator('[data-live-groupby="state"]').click();
	await expect(view.locator(".live-run-h b")).toHaveText(["Sin señal", "Fallo", "Tortuga"]);
	const fails = view.locator('[data-live-group="FAIL"]');
	await fails.click();
	await expect(fails).toHaveAttribute("aria-expanded", "false");
	await expect(view.locator("tr.lt-row")).toHaveCount(3);
	const n = served;
	await expect.poll(() => served, { timeout: 15_000 }).toBeGreaterThan(n); // la vista se refrescó
	await expect(view.locator('[data-live-group="FAIL"]')).toHaveAttribute("aria-expanded", "false");
});

test("buscar no pierde el foco ni lo escrito cuando la vista se refresca", async ({ page }) => {
	const view = page.locator("#view-live");
	const q = view.locator("[data-live-q]");
	await q.click();
	await q.pressSequentially("despachar");
	await expect(view.locator("tr.lt-row").first()).toBeVisible();
	const n = served;
	await expect.poll(() => served, { timeout: 15_000 }).toBeGreaterThan(n);
	await page.keyboard.type("_receta");
	await expect(view.locator("[data-live-q]")).toHaveValue("despachar_receta");
	await expect(view.locator("[data-live-q]")).toBeFocused();
	// mira todo (no solo los con problemas): el archivo o el nombre del test
	const rows = await view.locator("tbody.live-run").evaluateAll((gs) => gs.flatMap((g) => [...g.querySelectorAll("tr.lt-row")].map((r) => `${g.querySelector(".live-run-h b").textContent} ${r.querySelector(".lt-name a").textContent}`)));
	expect(rows.length).toBeGreaterThan(6);
	expect(rows.every((x) => x.includes("despachar_receta")), rows.join(", ")).toBeTruthy();
});

test("zoom: arrastrar en el minimapa acerca los carriles; − y Ajustar vuelven", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator(".lg-more").click();
	const bars = async () => view.locator(".lg-bar").count();
	const all = await bars();
	const mini = await view.locator("[data-live-minimap]").boundingBox();
	await page.mouse.move(mini.x + mini.width * 0.6, mini.y + mini.height / 2);
	await page.mouse.down();
	await page.mouse.move(mini.x + mini.width * 0.75, mini.y + mini.height / 2, { steps: 4 });
	await page.mouse.up();
	await expect(view.locator(".lg-head")).toContainText("zoom");
	await expect(view.locator(".lg-brush")).toHaveCount(1);
	expect(await bars()).toBeLessThan(all);
	await view.locator('[data-live-zoom="in"]').click();
	await view.locator('[data-live-zoom="out"]').click();
	await expect(view.locator(".lg-head")).toContainText("zoom");
	await view.locator('[data-live-zoom="fit"]').click();
	await expect(view.locator(".lg-head")).toContainText("12 carriles");
	await expect(view.locator(".lg-brush")).toHaveCount(0);
	expect(await bars()).toBe(all);
});

test("en el celular: tiles en 3 columnas, solo el minimapa, la tabla en tarjetas y sin scroll horizontal", async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 800 });
	const view = page.locator("#view-live");
	await expect(view.locator("[data-live-minimap]")).toBeVisible();
	await expect(view.locator(".lg-lanes")).toBeHidden();
	const tiles = await view.locator("button.live-tile").evaluateAll((els) => new Set(els.map((e) => Math.round(e.getBoundingClientRect().top))).size);
	expect(tiles).toBe(2); // 6 tiles en 2 filas de 3
	await expect(view.locator(".live-table thead")).toBeHidden();
	const scroll = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
	expect(scroll, "sin scroll horizontal en el celular").toBeLessThanOrEqual(1);
});
