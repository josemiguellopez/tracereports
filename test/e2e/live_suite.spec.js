// En vivo a escala: una ejecución con la forma de la suite que motivó el rediseño (79 corridas de 12
// procesos sin `worker`, ~230 tests), servida con page.route (fixtures/live_suite.js): los tiles que
// filtran, "Solo con problemas" activo por ser grande, carriles reutilizados y colapsados, el zoom del
// minimapa, agrupar, buscar sin perder el foco cuando la vista se refresca y el celular. No crea datos.
const { test, expect } = require("@playwright/test");
const { liveSuite, liveSuiteEndpoints, liveSuiteTest } = require("./fixtures/live_suite.js");

let served = 0;
test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
	served = 0;
	await page.route("**/api/v1/live", (r) => r.fulfill({ json: liveSuite(Date.now()).list }));
	await page.route("**/api/v1/live/execution?**", (r) => { served++; return r.fulfill({ json: liveSuite(Date.now()).execution }); });
	await page.route("**/api/v1/live/endpoints?**", (r) => r.fulfill({ json: liveSuiteEndpoints() }));
	await page.route(/\/api\/v1\/tests\/\d+\/network$/, (r) => r.fulfill({ json: liveSuiteTest(1).network }));
	await page.route(/\/api\/v1\/tests\/\d+$/, (r) => r.fulfill({ json: liveSuiteTest(Number(r.request().url().split("/").pop())).test }));
	await page.goto("/#view=live&exec=x%3Asuite-demo");
	await expect(page.locator("#view-live .live-tiles")).toBeVisible();
	await expect(page.locator("#view-live")).not.toHaveClass(/live-enter/, { timeout: 5000 }); // terminó la entrada animada
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("una ejecución grande abre con lo que pide acción: tiles, Solo con problemas y carriles reutilizados", async ({ page }) => {
	const view = page.locator("#view-live");
	await expect(view.locator(".xp-btn")).toContainText("rayen_salud_box_clinico · c3aec76");
	await expect(view.locator(".xp-btn .xp-fresh")).toHaveText("en curso");
	await expect(view.locator(".xp-btn")).toContainText("empezó hace 34 min");
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
	// por la parte baja: arriba están las marcas de anomalías (que abren su test)
	await page.mouse.move(mini.x + mini.width * 0.6, mini.y + mini.height * 0.8);
	await page.mouse.down();
	await page.mouse.move(mini.x + mini.width * 0.75, mini.y + mini.height * 0.8, { steps: 4 });
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

test("salud y diagnóstico: tasa de fallo frente a la anterior, errores agrupados, lentos, backend y actividad", async ({ page }) => {
	const view = page.locator("#view-live");
	const health = view.locator(".live-health");
	await expect(health).toContainText("Tasa de fallo");
	await expect(health).toContainText(/1\.3%\s*3 de 223 terminados/);
	await expect(health.locator(".lh-delta.worse")).toContainText("frente a la anterior");
	await expect(health).toContainText("Duración p50 · p95");
	await expect(health).toContainText(/\d+\.\d tests\/min/);
	// errores agrupados: los 3 fallos tienen la misma firma; un clic filtra por ella
	const err = view.locator(".lc-errors .lc-row").first();
	await expect(err.locator(".lc-count")).toHaveText("3");
	await expect(err).toContainText("TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]");
	await err.click();
	await expect(err).toHaveAttribute("aria-pressed", "true");
	await expect(view.locator("tr.lt-row")).toHaveCount(3);
	await expect(view.locator(".live-chip.facet")).toHaveCount(1);
	await view.locator(".live-chip.facet").click();
	await expect(view.locator("tr.lt-row")).toHaveCount(6);
	// lentos frente a su historial, el backend (errores primero, enlace a la red de un test) y la actividad
	await expect(view.locator(".lc-slow .lt-vs").first()).toHaveText("×8.3");
	const ep = view.locator(".lc-backend tbody tr").first();
	await expect(ep).toHaveClass(/bad/);
	await expect(ep).toContainText("/api/atenciones/:id");
	await expect(ep.locator("a")).toHaveAttribute("href", /view=tests&test=1001&tab=network/);
	await expect(view.locator(".lc-activity li")).toHaveCount(12);
	// las anomalías del minimapa: un ✕ por fallo y un ! por el sin señal; abren el test
	await expect(view.locator(".lg-anom.fail")).toHaveCount(3);
	await expect(view.locator(".lg-anom.nosig")).toHaveText("!");
	await view.locator(".lg-anom.nosig").click();
	await expect(view.locator(".live-panel h4")).toHaveText("test_001_periodontograma_paso_1");
});

test("facetas: archivo y estado a la vez, ver más, y cada filtro como chip que se quita", async ({ page }) => {
	const view = page.locator("#view-live");
	const facets = view.locator(".live-facets");
	await expect(facets.locator('[data-live-facet="state"]')).toHaveCount(6);
	await expect(facets.locator('[data-live-facet="file"]')).toHaveCount(8);
	await facets.locator('[data-live-more="file"]').click();
	expect(await facets.locator('[data-live-facet="file"]').count()).toBeGreaterThan(70);
	const file = facets.locator('[data-live-facet="file"][data-v="Clinico · test_agenda_062.py"]');
	await file.click();
	await expect(file).toHaveAttribute("aria-checked", "true");
	await expect(view.locator("tr.lt-row")).toHaveCount(3, { timeout: 5000 }); // todos los del archivo, no solo los con problemas
	await facets.locator('[data-live-facet="state"][data-v="PASS"]').click();
	await expect(view.locator("tr.lt-row")).toHaveCount(2);
	await expect(view.locator(".live-chip.facet")).toHaveCount(2);
	await view.locator("[data-live-clear]").click();
	await expect(view.locator(".live-chip.facet")).toHaveCount(0);
	await expect(view.locator("tr.lt-row")).toHaveCount(6);
});

test("ordenar por duración da una lista plana; tercer clic vuelve a agrupar", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator(".live-chip[data-live-problems]").click(); // todos
	const sort = view.locator('.lt-sort[data-live-sort="elapsed"]');
	await sort.click();
	await expect(view.locator(".lt-group")).toHaveCount(0);
	await expect(view.locator('th[aria-sort="descending"]')).toContainText("Dura.");
	const durs = await view.locator("tr.lt-row td.lt-num:nth-child(3)").allTextContents();
	expect(durs.length).toBeGreaterThan(200);
	await expect(view.locator("tr.lt-row").first().locator(".lt-name")).toContainText("periodontograma"); // 34 min, el más largo
	await view.locator('.lt-sort[data-live-sort="elapsed"]').click();
	await expect(view.locator('th[aria-sort="ascending"]')).toContainText("Dura.");
	await view.locator('.lt-sort[data-live-sort="elapsed"]').click();
	await expect(view.locator(".lt-group").first()).toBeVisible();
});

test("pausar congela la vista (no pide datos) y reanudar la pone al día", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator("[data-live-pause]").click();
	await expect(view.locator(".live-paused")).toContainText("En pausa");
	const n = served;
	await page.waitForTimeout(11_000); // más que el refresco de 10 s
	expect(served).toBe(n);
	await view.locator("[data-live-pause]").click();
	await expect(view.locator(".live-paused")).toHaveCount(0);
	await expect.poll(() => served).toBeGreaterThan(n);
});

test("la traza del test: pasos y llamadas en una línea de tiempo, el 504 y el fallo marcados, sin la query", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator("tr.lt-row.fail").first().locator("td.lt-step").click();
	const panel = view.locator(".live-panel");
	await expect(panel.locator('[data-live-ptab="trace"]')).toHaveAttribute("aria-selected", "true");
	const rows = panel.locator(".ltr");
	await expect(rows).toHaveCount(12); // 4 pasos y 8 llamadas (sin el script)
	await expect(panel.locator(".ltr.req.fail")).toHaveCount(3); // dos 504 y un ERR_ABORTED
	await expect(panel.locator(".ltr.req.fail").first()).toContainText("504 GET preclinico.demo/api/atenciones/actual");
	await expect(panel.locator(".lt-trace-fail")).toHaveCount(1);
	await expect(panel).not.toContainText("token=abc");
	await panel.locator("[data-live-trace-all]").click();
	await expect(rows).toHaveCount(13);
	await panel.locator('[data-live-ptab="steps"]').click();
	await expect(panel.locator(".live-steps li").first()).toContainText("TimeoutException");
	await panel.locator('[data-live-ptab="shot"]').click();
	await expect(panel).toContainText("Todavía sin capturas.");
});

test("gráficos: resultados en el tiempo, tasa de fallo acumulada y duraciones, con tooltip y tabla de datos", async ({ page }) => {
	const view = page.locator("#view-live");
	const charts = view.locator(".live-chart");
	await expect(charts).toHaveCount(3);
	await expect(charts.nth(0)).toContainText("Resultados en el tiempo");
	await expect(charts.nth(0).locator(".lch-legend")).toContainText("Pass");
	await expect(charts.nth(0).locator(".lch-legend")).toContainText("Fallo");
	// una columna: el valor manda en el tooltip (la columna se puede enfocar con el teclado)
	const col = charts.nth(0).locator(".lch-col").filter({ has: page.locator("i.fail") }).first();
	await expect(col).toHaveAttribute("tabindex", "0");
	await col.hover();
	await expect(page.locator("#cf-tooltip")).toBeVisible();
	await expect(page.locator("#cf-tooltip b")).toHaveText(/\d+ tests/);
	await expect(page.locator("#cf-tooltip")).toContainText("Fallo 1");
	// la tasa acumulada con la anterior como referencia
	await expect(charts.nth(1)).toContainText("Tasa de fallo acumulada");
	await expect(charts.nth(1).locator(".lch-now")).toHaveText(/%$/);
	await expect(charts.nth(1).locator("line.ref")).toHaveCount(1);
	await expect(charts.nth(1)).toContainText("anterior:");
	// el histograma marca dónde caen p50 y p95; "Ver datos" muestra la tabla y sobrevive al refresco
	await expect(charts.nth(2).locator(".lch-mark")).toHaveCount(2);
	await charts.nth(2).locator("[data-live-charttable]").click();
	const table = charts.nth(2).locator(".lch-table");
	await expect(table).toBeVisible();
	await expect(table.locator("tbody tr")).toHaveCount(7);
	const n = served;
	await expect.poll(() => served, { timeout: 15_000 }).toBeGreaterThan(n);
	await expect(view.locator(".live-chart").nth(2).locator(".lch-table")).toBeVisible();
});

test("selector de ejecución: hora y antigüedad; con muchas, buscador agrupado y teclado", async ({ page }) => {
	// 30 ejecuciones: la grande en curso y otras de hoy y anteriores
	const now = Date.now();
	await page.route("**/api/v1/live", (r) => {
		const base = liveSuite(Date.now()).list;
		const old = Array.from({ length: 29 }, (_, i) => ({ ...base.executions[1], key: `x:old-${i}`, title: `rayen_${i % 2 ? "box" : "agenda"} · build ${100 + i}`,
			started_at: now - (i + 2) * 3600e3, ended_at: now - (i + 1) * 3600e3, running: false }));
		return r.fulfill({ json: { ...base, executions: [base.executions[0], ...old] } });
	});
	await page.goto("/#view=live&exec=x%3Asuite-demo");
	const view = page.locator("#view-live");
	const btn = view.locator(".xp-btn");
	await expect(btn).toContainText("30 ejecuciones");
	await btn.click();
	const q = view.locator("[data-live-xq]");
	await expect(q).toBeFocused();
	await expect(view.locator(".xp-group")).toHaveText(["En curso", "De hoy", "Anteriores"]);
	await expect(view.locator(".xp-opt")).toHaveCount(30);
	await expect(view.locator(".xp-opt.old").first()).toContainText("terminó hace");
	await q.fill("agenda · build 112");
	await expect(view.locator(".xp-opt")).toHaveCount(1);
	await expect(view.locator(".xp-found")).toHaveText("1 de 30");
	await q.fill("rayen_box"); // el proyecto (rayen_salud_box_clinico) también dice "box"
	await expect(view.locator(".xp-opt")).toHaveCount(14);
	// con el teclado: flecha abajo entra en la lista, Enter elige
	await page.keyboard.press("ArrowDown");
	await expect(view.locator(".xp-opt").first()).toBeFocused();
	await page.keyboard.press("ArrowDown");
	await page.keyboard.press("Enter");
	await expect(view.locator(".xp-pop")).toHaveCount(0);
	await expect(page).toHaveURL(/exec=x%3Aold-3/);
	// Escape cierra sin elegir y un clic fuera también
	await view.locator(".xp-btn").click();
	await page.keyboard.press("Escape");
	await expect(view.locator(".xp-pop")).toHaveCount(0);
	await expect(view.locator(".xp-btn")).toBeFocused();
	await view.locator(".xp-btn").click();
	await page.mouse.click(5, 700);
	await expect(view.locator(".xp-pop")).toHaveCount(0);
});

test.describe("animaciones", () => {
	test("al abrir se anima la entrada; un número que cambia late y cuenta", async ({ page }) => {
		const view = page.locator("#view-live");
		await page.goto("/#view=tests");
		await page.goto("/#view=live&exec=x%3Asuite-demo");
		await expect(view).toHaveClass(/live-enter/);
		await expect(view).not.toHaveClass(/live-enter/, { timeout: 3000 });
		// otra ejecución con más fallos: el tile cambia de número y late
		await page.route("**/api/v1/live/execution?**", (r) => {
			const ex = liveSuite(Date.now()).execution;
			ex.execution.runs[1].tests[0] = { ...ex.execution.runs[1].tests[0], state: "FAIL", status: "FAIL" };
			ex.execution.counts = { ...ex.execution.counts, FAIL: 4 };
			return r.fulfill({ json: ex });
		});
		await expect(view.locator('.live-tile[data-live-state="FAIL"]')).toHaveClass(/bump/, { timeout: 15_000 });
		await expect(view.locator('[data-count="FAIL"]')).toHaveText("4");
	});

	test("con reducir movimiento no se anima nada", async ({ page }) => {
		await page.emulateMedia({ reducedMotion: "reduce" });
		await page.goto("/#view=tests");
		await page.goto("/#view=live&exec=x%3Asuite-demo");
		const view = page.locator("#view-live");
		await expect(view.locator(".live-tiles")).toBeVisible();
		await expect(view).not.toHaveClass(/live-enter/);
		const anim = await view.locator(".lch-stack").first().evaluate((n) => getComputedStyle(n).animationName);
		expect(anim).toBe("none");
	});
});
