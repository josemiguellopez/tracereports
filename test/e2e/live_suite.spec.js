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
	// los tests de esta ejecución son simulados: su historial (para "fallo nuevo") viene vacío salvo que el test lo pida
	await page.route(/\/api\/v1\/tests\/\d+\/history/, (r) => r.fulfill({ json: [] }));
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
	const chip = view.locator(".seg-o[data-live-problems]");
	await expect(chip).toHaveAttribute("aria-checked", "true");
	await expect(chip).toHaveText(/Con problemas\s*6/);
	await expect(view.locator(".live-status")).toContainText(/Mostrando 6 de \d+ tests\s*·\s*Solo con problemas\s*·\s*Agrupado por archivo/);
	await expect(view.locator("tr.lt-row")).toHaveCount(6);
	await expect(view.locator("tr.lt-row").first()).toHaveClass(/nosig/); // lo más severo primero
	await expect(view.locator(".live-collapsed")).toContainText("tests sin problemas ocultos");
	// sin worker: 12 carriles de proceso, los 6 sin problemas colapsados en una línea
	await expect(view.locator(".lg-lane")).toHaveCount(6);
	await expect(view.locator(".lg-worker b").first()).toHaveText(/^Proceso \d+$/); // fija y sin siglas (antes "P1")
	await expect(view.locator(".lg-legend")).toContainText("Proceso N = carril en paralelo");
	await expect(view.locator(".lg-worker small").first()).toHaveText(/^\S+\.py$/); // el archivo actual, sin "Clinico · test_"
	await view.locator(".lg-more").click();
	await expect(view.locator(".lg-lane")).toHaveCount(12);
	// el último paso en una línea, sin la URL (tampoco en el tooltip: puede llevar datos)
	const step = view.locator("tr.lt-row.turtle .lt-steptext").first();
	await expect(step).not.toContainText("http");
	await expect(step).toHaveAttribute("data-tip", /^Evidencia de .* del paciente$/);
	// Todos: los archivos sin problemas quedan en una sola línea que se puede abrir
	await view.locator(".seg-o[data-live-all]").click();
	await expect(chip).toHaveAttribute("aria-checked", "false");
	await expect(view.locator(".seg-o[data-live-all]")).toHaveAttribute("aria-checked", "true");
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
	await expect(health).toContainText(/1,3%\s*3 de 223 terminados/);
	await expect(health.locator(".lh-delta.worse")).toContainText("frente a la anterior");
	await expect(health).toContainText("Duración típica · lenta");
	await expect(health).toContainText("mediana (p50)");
	await expect(health).toContainText(/\d+,\d tests\/min/);
	// errores agrupados: los 3 fallos tienen la misma firma; un clic filtra por ella
	const err = view.locator(".lc-errors .lc-row").first();
	await expect(err.locator(".lc-count")).toHaveText("3");
	await expect(err).toContainText("TimeoutException en fa-plus-circle"); // el selector resumido; el XPath completo, al desplegar
	const filter = view.locator(".lc-errors .lc-filter").first(); // el clic en la fila la despliega; la lupa filtra
	await filter.click();
	await expect(view.locator(".lc-errors .lc-filter").first()).toHaveAttribute("aria-pressed", "true");
	await expect(view.locator("tr.lt-row")).toHaveCount(3);
	await expect(view.locator(".live-chip.facet")).toHaveCount(1);
	await view.locator(".live-chip.facet").click();
	await expect(view.locator("tr.lt-row")).toHaveCount(6);
	// lentos frente a su historial, el backend (errores primero, enlace a la red de un test) y la actividad
	await expect(view.locator(".lc-slow .lt-vs").first()).toHaveText("×8,3"); // decimales con coma, como se escribe en Chile
	const ep = view.locator(".lc-backend tbody tr").first();
	await expect(ep).toHaveClass(/bad/);
	await expect(ep).toContainText("/api/atenciones/:id");
	await expect(ep.locator("a")).toHaveAttribute("href", /view=tests&test=1001&tab=network/);
	await expect(view.locator(".lc-activity"), "la actividad reciente es la cinta EN VIVO: no se repite abajo").toHaveCount(0);
	await expect(view.locator(".tp-i").first()).toBeVisible();
	// las anomalías del minimapa: el ícono de fallo o de sin señal (no solo color); abren el test
	await expect(view.locator(".lg-anom.fail")).toHaveCount(3);
	await expect(view.locator(".lg-anom.nosig use")).toHaveAttribute("href", "#i-st-nosig");
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
	await view.locator(".seg-o[data-live-all]").click(); // todos
	const sort = view.locator('.lt-sort[data-live-sort="elapsed"]');
	await sort.click();
	await expect(view.locator(".lt-group")).toHaveCount(0);
	await expect(view.locator('th[aria-sort="descending"]')).toContainText("Dura.");
	const durs = await view.locator("tr.lt-row td.lt-num:nth-child(3)").allTextContents();
	expect(durs.length, "de a 100 filas: el resto con Mostrar más").toBe(100);
	await expect(view.locator("tr.lt-row").first().locator(".lt-name")).toContainText("periodontograma"); // 34 min, el más largo
	await view.locator('.lt-sort[data-live-sort="elapsed"]').click();
	await expect(view.locator('th[aria-sort="ascending"]')).toContainText("Dura.");
	await view.locator('.lt-sort[data-live-sort="elapsed"]').click();
	await expect(view.locator(".lt-group").first()).toBeVisible();
});

test("pausar congela la vista (no pide datos) y reanudar la pone al día", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator(".live-title [data-live-pause]").click();
	await expect(view.locator(".live-paused")).toContainText("En pausa");
	const n = served;
	await page.waitForTimeout(11_000); // más que el refresco de 10 s
	expect(served).toBe(n);
	await view.locator(".live-title [data-live-pause]").click();
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

// ---------- ronda 2 (run 1084): respuesta inmediata, controles, nombres, línea de tiempo, tooltips ----------

test("un clic en Mostrar o Agrupar por responde en el mismo cuadro y la línea de estado dice qué se ve", async ({ page }) => {
	const view = page.locator("#view-live");
	const all = view.locator(".seg-o[data-live-all]");
	// en el mismo cuadro del clic (antes de recalcular la lista): el control cambia y aparece la barra de trabajo
	const same = await all.evaluate((b) => {
		b.click();
		return { checked: b.getAttribute("aria-checked"), busy: document.querySelector("#view-live").classList.contains("live-busy") };
	});
	expect(same).toEqual({ checked: "true", busy: true });
	await expect(view).not.toHaveClass(/live-busy/);
	const tm = await page.waitForFunction(() => window.__trLiveTiming).then((h) => h.jsonValue());
	expect(tm.feedback, "clic → primer pintado").toBeLessThan(100);
	expect(tm.list, "clic → lista redibujada").toBeLessThan(800); // en la máquina de desarrollo, ~100 ms con 234 tests
	await expect(view.locator(".live-status")).toContainText(/Mostrando\s*\d+\s*de 234 tests\s*·\s*Todos/);
	// Agrupar por worker: el indicador se desliza bajo la opción elegida
	const thumb = view.locator('.seg[data-seg="group"] .seg-thumb');
	const x0 = await thumb.evaluate((n) => n.offsetLeft);
	await view.locator('.seg-o[data-live-groupby="worker"]').click();
	await expect(view.locator('.seg-o[data-live-groupby="worker"]')).toHaveAttribute("aria-checked", "true");
	await expect(view.locator(".live-status")).toContainText("Agrupado por worker");
	const w = await view.locator('.seg-o[data-live-groupby="worker"]').evaluate((n) => n.offsetLeft);
	await expect.poll(() => thumb.evaluate((n) => n.offsetLeft)).toBe(w);
	expect(w).toBeGreaterThan(x0);
	await expect(view.locator('.seg[data-seg="group"]')).toHaveClass(/has-on/);
});

test("tooltips: con el mouse (tras un instante) y con el teclado dicen qué hace cada control", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator(".seg-o[data-live-problems]").hover();
	const tip = page.locator("#cf-tooltip");
	await expect(tip).toBeVisible();
	await expect(tip).toContainText("Con problemas");
	await expect(tip).toContainText("Solo lo que pide acción");
	await page.mouse.move(0, 0);
	await view.locator('[data-live-zoom="in"]').focus();
	await page.keyboard.press("Shift+Tab");
	await page.keyboard.press("Tab");
	await expect(tip).toContainText("Acercar");
	await expect(tip.locator("kbd")).toHaveText("Ctrl + rueda");
	// los íconos de estado tienen su explicación: la tortuga
	await view.locator('.live-tile[data-live-state="turtle"]').hover();
	await expect(tip).toContainText("Sigue corriendo y ya tarda más que su p90");
	await expect(view.locator('.live-tile[data-live-state="turtle"] use')).toHaveAttribute("href", "#i-st-turtle");
});

test("nombres largos: elipsis en el medio, copiar el nombre, columna más ancha y 2 líneas que se recuerdan", async ({ page }) => {
	const view = page.locator("#view-live");
	const row = view.locator("tr.lt-row").first();
	const name = await row.locator(".lt-copy").getAttribute("data-live-copy");
	await expect(row.locator(".mid-b")).toHaveText(/paso_1$/); // el final se ve siempre
	await row.hover();
	await row.locator(".lt-copy").click();
	await expect(row.locator(".lt-copy")).toHaveClass(/done/);
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(name);
	await expect(view.locator(".live-panel")).toHaveCount(0); // copiar no abre el detalle
	// la columna se ensancha con el teclado (o arrastrando su borde)
	const before = await view.locator("th.th-name").evaluate((n) => n.getBoundingClientRect().width);
	await view.locator("[data-live-resize]").focus();
	await page.keyboard.press("ArrowRight");
	await page.keyboard.press("ArrowRight");
	await expect.poll(() => view.locator("th.th-name").evaluate((n) => n.getBoundingClientRect().width)).toBeGreaterThan(before + 30);
	// nombres en 2 líneas: se recuerda al volver
	await view.locator("[data-live-wrap]").click();
	await expect(view.locator(".live-runs")).toHaveClass(/wrap-names/);
	await page.reload();
	await expect(page.locator("#view-live .live-runs")).toHaveClass(/wrap-names/);
	await page.evaluate(() => localStorage.removeItem("tr-live-prefs"));
});

test("mapa de la ejecución y cinta de eventos: un cuadro por test que abre su detalle; la cinta avanza y se detiene", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator('[data-live-fieldview="grid"]').click(); // el campo es la vista por defecto
	await expect(view.locator(".hm-c[data-live-test]")).toHaveCount(234);
	await expect(view.locator(".hm-c.pending")).toHaveCount(36);
	await expect(view.locator(".hm-c.fail:not(.dim)")).toHaveCount(3); // Con problemas: lo demás atenuado
	await view.locator(".hm-c.nosig").click();
	await expect(view.locator(".live-panel h4")).toHaveText("test_001_periodontograma_paso_1");
	await page.keyboard.press("Escape");
	const x = () => view.locator("[data-live-tape-track]").evaluate((n) => new DOMMatrix(getComputedStyle(n).transform).m41);
	const a = await x();
	await expect.poll(x, { timeout: 5000 }).toBeLessThan(a - 10);
	await view.locator(".live-tape").hover();
	const b = await x();
	await page.waitForTimeout(500);
	expect(Math.abs((await x()) - b)).toBeLessThan(2);
});

test("línea de tiempo: Proceso N con su leyenda, la línea de ahora y Ctrl + rueda acerca", async ({ page }) => {
	const view = page.locator("#view-live");
	await expect(view.locator(".lg-worker b").first()).toHaveText(/^Proceso \d+$/);
	await expect(view.locator(".lg-legend")).toContainText("×1,7 = 1,7 veces lo normal");
	await expect(view.locator(".lg-now")).toHaveCount(1);
	// Ctrl + rueda (o el pellizco del trackpad, que llega igual): Playwright no pasa el Ctrl a la rueda, así que el evento va directo
	const prevented = await view.locator("[data-live-lanes] .lg-track").first().evaluate((n) => {
		const r = n.getBoundingClientRect();
		const ev = new WheelEvent("wheel", { deltaY: -200, ctrlKey: true, clientX: r.left + r.width / 2, clientY: r.top + 4, bubbles: true, cancelable: true });
		n.dispatchEvent(ev);
		return ev.defaultPrevented;
	});
	expect(prevented, "no hace zoom de la página").toBe(true);
	await expect(view.locator(".lg-head")).toContainText("zoom");
	await view.locator('[data-live-zoom="fit"]').click();
	await expect(view.locator(".lg-head")).not.toContainText("zoom");
});

test("línea de tiempo sin vacío: una pausa larga entre tandas se comprime con ≈ y dice cuánto duró", async ({ page }) => {
	// dos tandas con 40 min sin nada entre ellas (el lanzador esperando un ambiente, por ejemplo)
	const MIN = 60_000;
	const gapped = (now) => {
		const t0 = now - 60 * MIN;
		const mk = (id, run, a, b, state = "PASS") => ({ id, run_id: run, name: `test_${String(id).padStart(3, "0")}_tanda`, state, status: b ? state : "RUNNING", started_at: t0 + a * MIN,
			...(b ? { ended_at: t0 + b * MIN, elapsed: (b - a) * MIN } : { elapsed: now - (t0 + a * MIN), idle: 2000, last_signal: now - 2000 }), p50: 3 * MIN, factor: 1, attempts: 1 });
		const runs = [
			{ id: 1, name: "tanda_1.py", status: "PASS", started_at: t0, ended_at: t0 + 10 * MIN, tests: [mk(1, 1, 0, 4), mk(2, 1, 4, 10)] },
			{ id: 2, name: "tanda_2.py", status: "RUNNING", started_at: t0 + 50 * MIN, tests: [mk(3, 2, 50, 54), mk(4, 2, 54, 0, "running")] },
		];
		const exec = { key: "x:suite-demo", title: "dos tandas", running: true, started_at: t0, planned: 4, total: 4, done: 3, workers: 1, counts: { PASS: 3, running: 1 } };
		return { list: { now, no_signal_ms: 180000, executions: [exec] }, execution: { now, execution: { ...exec, runs } } };
	};
	await page.unroute("**/api/v1/live");
	await page.unroute("**/api/v1/live/execution?**");
	await page.route("**/api/v1/live", (r) => r.fulfill({ json: gapped(Date.now()).list }));
	await page.route("**/api/v1/live/execution?**", (r) => r.fulfill({ json: gapped(Date.now()).execution }));
	await page.reload();
	const view = page.locator("#view-live");
	await expect(view.locator(".lg-gap").first()).toBeVisible();
	await expect(view.locator(".lg-gap small").first()).toHaveText(/min/);
	await expect(view.locator(".lg-head")).toContainText("pausas comprimidas (≈)");
	const w = await view.locator(".lg-gap").first().evaluate((n) => n.getBoundingClientRect().width / n.parentElement.getBoundingClientRect().width);
	expect(w, "la pausa ocupa un ancho chico").toBeLessThan(0.06);
});

// ---------- el campo de la ejecución (consola de observabilidad) ----------

test("campo: cada punto es un test; el mouse lo muestra y el clic (o Enter) abre su detalle", async ({ page }) => {
	const view = page.locator("#view-live");
	await expect(view.locator(".live-map.is-field .lf-canvas")).toBeVisible();
	// dónde cae el punto de un fallo: la misma cuenta que hace el campo, con los datos que sirve la vista
	await page.evaluate(() => document.querySelector("#view-live .live-map").scrollIntoView({ block: "center" }));
	// con el mouse encima el tubo deja de girar: se lee su giro y se calcula dónde quedó la partícula de un fallo
	const box = await view.locator(".lf-canvas").boundingBox();
	await page.mouse.move(box.x + 6, box.y + 6);
	const at = await page.evaluate(async () => {
		const F = window.TraceReportsLiveField, LIVE = window.TraceReportsLive;
		const { execution, now } = await (await fetch("/api/v1/live/execution?key=x%3Asuite-demo")).json();
		const c = document.querySelector(".lf-canvas"), r = c.getBoundingClientRect();
		const L = F.fieldLayout(execution, now, LIVE), g = F.geometry(r.width, r.height);
		const it = L.items.find((x) => x.state === "FAIL"), p = F.headOf(g, L, it, Number(c.dataset.spin || 0));
		return { x: r.left + p.x, y: r.top + p.y, name: it.name };
	});
	await page.mouse.move(at.x, at.y);
	const tip = view.locator(".lf-tip");
	await expect(tip).toBeVisible();
	await expect(tip).toContainText(at.name);
	await expect(tip).toContainText("Fallo");
	await page.mouse.click(at.x, at.y);
	await expect(view.locator(".live-panel h4")).toHaveText(at.name);
	await page.keyboard.press("Escape");
	// con el teclado: las flechas recorren los tests en el orden en que empezaron, Enter abre
	await view.locator(".lf-canvas").focus();
	await page.keyboard.press("ArrowRight");
	await expect(tip).toBeVisible();
	const first = (await tip.locator("b").innerText()).trim();
	await page.keyboard.press("Enter");
	await expect(view.locator(".live-panel h4")).toHaveText(first);
});

test("campo: los canales filtran o agrupan, la anomalía filtra por su firma, la ingesta sale de datos reales; y la cuadrícula", async ({ page }) => {
	const view = page.locator("#view-live");
	// recién abierta, la ingesta se completa con los tests que terminaron o empezaron en el último minuto
	// (en esta ejecución, hace segundos): activa y con barras, no un gráfico vacío
	await expect(view.locator(".lf-hud.tl")).toContainText("INGESTA ACTIVA");
	await expect.poll(() => view.locator('.lf-bars i:not([style*="--h:0%"])').count()).toBeGreaterThan(0);
	await view.locator('.lf-lane[data-live-state="FAIL"]').click();
	await expect(view.locator("tr.lt-row")).toHaveCount(3);
	await view.locator("[data-live-clear], .live-chip.facet").first().click();
	await view.locator(".lf-hud.bl").click();
	await expect(view.locator(".live-chip.facet")).toContainText("TimeoutException");
	await expect(view.locator("tr.lt-row")).toHaveCount(3);
	await view.locator(".live-chip.facet").click();
	await view.locator('.lf-lane[data-live-groupby="worker"]').click();
	await expect(view.locator(".live-status")).toContainText("Agrupado por worker");
	await expect(view.locator(".lf-sigs, .lf-hud.tr"), "sin la lista de señales ni PROGRESO: los repetían la cinta y la cabecera").toHaveCount(0);
	// la cuadrícula: lo mismo, navegable con Tab
	await view.locator('[data-live-fieldview="grid"]').click();
	await expect(view.locator(".hm-c[data-live-test]")).toHaveCount(234);
	await expect(view.locator(".lf-canvas")).toHaveCount(0);
	await view.locator('[data-live-fieldview="field"]').click();
	await expect(view.locator(".lf-canvas")).toHaveCount(1);
});

test("campo manipulable: arrastrar gira e inclina, zoom con botones, pantalla completa con Esc, doble clic en el elegido abre el test", async ({ page }) => {
	const view = page.locator("#view-live");
	const canvas = view.locator(".lf-canvas");
	const v = async () => (await canvas.getAttribute("data-view")).split(",").map(Number);
	await page.evaluate(() => document.querySelector("#view-live .live-map").scrollIntoView({ block: "center" }));
	const [yaw0, pitch0, zoom0] = await v();
	const box = await canvas.boundingBox();
	await page.mouse.move(box.x + box.width * 0.7, box.y + box.height * 0.6);
	await page.mouse.down();
	await page.mouse.move(box.x + box.width * 0.4, box.y + box.height * 0.3, { steps: 10 });
	await page.mouse.up();
	const [yaw1, pitch1] = await v();
	expect(yaw1).not.toBeCloseTo(yaw0, 2);
	expect(pitch1).toBeGreaterThan(pitch0); // arrastrar hacia arriba lo muestra más desde arriba
	await expect(view.locator(".live-panel")).toHaveCount(0); // un arrastre no es un clic
	await view.locator('[data-lf="in"]').click();
	await expect.poll(async () => (await v())[2]).toBeGreaterThan(zoom0);
	await view.locator('[data-lf="reset"]').click();
	await expect.poll(async () => (await v()).join(",")).toBe("0,0.5,1"); // Restablecer: sin giro, la inclinación de siempre, sin zoom
	// pantalla completa: el mismo campo arriba de todo; Esc vuelve
	await page.locator('[data-lf="full"]').click();
	await expect(page.locator(".lf-wrap.lf-full")).toBeVisible();
	await expect(page.locator(".lf-fullhead")).toContainText("rayen_salud_box_clinico");
	await page.keyboard.press("Escape");
	await expect(page.locator(".lf-wrap.lf-full")).toHaveCount(0);
	await expect(view.locator(".live-map .lf-canvas")).toBeVisible();
	await page.evaluate(() => document.querySelector("#view-live .live-map").scrollIntoView({ block: "center" }));
	// doble clic en la partícula de un fallo ya elegido: abre el test (antes, un clic lo elige)
	const box2 = await canvas.boundingBox();
	await page.mouse.move(box2.x + 4, box2.y + 4);
	const at = await page.evaluate(async () => {
		const F = window.TraceReportsLiveField, LIVE = window.TraceReportsLive;
		const { execution, now } = await (await fetch("/api/v1/live/execution?key=x%3Asuite-demo")).json();
		const c = document.querySelector(".lf-canvas"), r = c.getBoundingClientRect();
		const L = F.fieldLayout(execution, now, LIVE), g = F.geometry(r.width, r.height);
		const it = L.items.find((x) => x.state === "FAIL"), p = F.headOf(g, L, it, Number(c.dataset.spin || 0));
		return { x: r.left + p.x, y: r.top + p.y, id: it.id };
	});
	await page.mouse.click(at.x, at.y);
	await expect(view.locator(".live-panel")).toBeVisible();
	await page.mouse.dblclick(at.x, at.y);
	await expect(page).toHaveURL(new RegExp(`view=tests&test=${at.id}`));
	// la ejecución de este archivo es simulada (page.route): sus corridas no existen en el servidor de
	// prueba, así que abrir el test responde 404. Lo que se prueba es que el doble clic lleve a él.
	await page.waitForTimeout(300);
	test.info().errors_ = test.info().errors_.filter((e) => !/404|\/api\/v1\/runs\/\d+/.test(e));
});

test("pausar desde el campo: la vista queda quieta, cuenta lo que sigue llegando y la cinta abre su test", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator('[data-lf="pause"]').click();
	// la pausa la dice el mismo cuadro de ingesta (nada flota encima del campo ni queda tapado)
	await expect(view.locator(".lf-hud.tl.paused")).toContainText("VISTA EN PAUSA");
	await expect(view.locator(".lf-hud.tl")).toContainText("la ingesta sigue: 0 eventos nuevos");
	await expect(view.locator(".lf-pausebar")).toHaveCount(0);
	await expect(view.locator('[data-lf="pause"]')).toHaveAttribute("aria-pressed", "true");
	await expect(view.locator(".live-title [data-live-pause]")).toHaveAttribute("aria-pressed", "true"); // la misma pausa que la barra
	await view.locator(".lf-hud.tl .lf-resume").click();
	await expect(view.locator(".lf-hud.tl.paused")).toHaveCount(0);
	// la cinta: con el mouse encima se detiene y un clic abre el detalle de su test
	await page.evaluate(() => document.querySelector("#view-live .live-tape").scrollIntoView({ block: "center" }));
	const tape = await view.locator(".live-tape").boundingBox();
	const x = tape.x + tape.width * 0.45, y = tape.y + tape.height / 2;
	await page.mouse.move(x, y);
	const name = await page.evaluate(({ x, y }) => document.elementFromPoint(x, y)?.closest(".tp-i")?.querySelector("b")?.textContent, { x, y });
	expect(name).toBeTruthy();
	await page.mouse.click(x, y);
	await expect(view.locator(".live-panel h4")).toHaveText(name);
});

test("Mostrar, Agrupar por y Buscar van pegados a las facetas y la tabla que filtran; Pausar, en la cabecera (es de toda la vista)", async ({ page }) => {
	const view = page.locator("#view-live");
	const order = await view.evaluate((v) => [...v.children].map((c) => c.className.split(" ").find((k) => k.startsWith("live-")) || c.className));
	const bar = order.indexOf("live-tablebar"), explore = order.indexOf("live-explore");
	expect(bar).toBeGreaterThan(order.indexOf("live-gantt"));
	expect(explore).toBe(bar + 1);
	await expect(view.locator(".live-tablebar .seg[data-seg=show]")).toBeVisible();
	await expect(view.locator(".live-tablebar .live-status")).toContainText("Mostrando");
	await expect(view.locator(".live-head .live-title [data-live-pause]")).toBeVisible();
	await expect(view.locator(".live-filters [data-live-pause]")).toHaveCount(0);
});

// ---------- ronda 3 ----------

test("un estado elegido se nota en la línea de tiempo: la cuenta, solo sus carriles y lo demás apagado", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator('.live-tile[data-live-state="FAIL"]').click();
	await expect(view.locator(".lg-count")).toHaveText("3 de 234 tests coinciden");
	await expect(view.locator(".lg-lane")).toHaveCount(3);
	await expect(view.locator(".lg-bar.hit")).toHaveCount(3);
	await expect(view.locator(".lg-more").first()).toContainText("carriles sin coincidencias");
	await view.locator('.live-tile[data-live-state="FAIL"]').click();
	await view.locator('.live-tile[data-live-state="PASS"]').click();
	await expect(view.locator(".lg-count")).toHaveText(/^195 de 234/);
	// pasar por un carril resalta sus tests en la tabla
	await view.locator('.live-tile[data-live-state="PASS"]').click();
	await view.locator(".seg-o[data-live-all]").click();
	await view.locator(".lg-worker").first().hover();
	await expect.poll(() => view.locator("tr.lt-row.lane-hl").count()).toBeGreaterThan(0);
});

test("Requiere atención: buscador y fallos nuevos; errores con sus tests; J abre el siguiente; ? muestra los atajos", async ({ page }) => {
	// historial: el fallo de la agenda pasó la vez anterior (nuevo); los otros dos ya fallaban
	const agenda = await page.evaluate(async () => (await (await fetch("/api/v1/live/execution?key=x%3Asuite-demo")).json()).execution.runs.flatMap((r) => r.tests).find((x) => x.state === "FAIL" && x.name.includes("agenda")).id);
	await page.route(/\/api\/v1\/tests\/\d+\/history/, (r) => { const id = Number(r.request().url().match(/tests\/(\d+)/)[1]); return r.fulfill({ json: [{ test_id: id + 9000, run_id: 0, status: id === agenda ? "PASS" : "FAIL", started_at: 1 }] }); });
	await page.reload();
	const view = page.locator("#view-live");
	await expect(view.locator(".lt-reg.new").first()).toBeVisible();
	await expect(view.locator('[data-live-attnstate="new"]')).toContainText("1 nuevo");
	await view.locator('[data-live-attnstate="new"]').click();
	await expect(view.locator(".la-list li")).toHaveCount(1);
	await view.locator('[data-live-attnstate="new"]').click();
	await view.locator(".la-q").fill("agenda");
	await expect(view.locator(".la-list li")).toHaveCount(1);
	await expect(view.locator(".la-q")).toBeFocused(); // escribir no pierde el foco con los refrescos
	await view.locator(".la-q").fill("");
	// errores agrupados: se despliega con los tres tests y cada uno abre su detalle
	await view.locator("[data-live-errgroup]").first().click();
	await expect(view.locator(".lc-who li")).toHaveCount(3);
	await view.locator(".lc-whorow").first().click();
	await expect(view.locator(".live-panel .lp-strip")).toContainText("Su carril: Proceso");
	await page.keyboard.press("Escape");
	await page.evaluate(() => document.activeElement?.blur());
	await page.keyboard.press("j");
	await expect(view.locator(".live-panel h4")).toHaveText("test_001_periodontograma_paso_1"); // lo más grave primero
	await page.keyboard.press("j");
	await expect(view.locator(".live-panel h4")).not.toHaveText("test_001_periodontograma_paso_1");
	await page.keyboard.press("Escape");
	await page.keyboard.press("?");
	await expect(view.locator(".live-help")).toContainText("Atajos de teclado");
	await page.keyboard.press("Escape");
	await expect(view.locator(".live-help")).toHaveCount(0);
	// compacto: sin diagnóstico ni gráficos, y se recuerda
	await page.keyboard.press("c");
	await expect(view).toHaveClass(/live-compact/);
	await expect(view.locator(".live-insights")).toBeHidden();
	await page.reload();
	await expect(page.locator("#view-live")).toHaveClass(/live-compact/);
	await page.evaluate(() => localStorage.removeItem("tr-live-prefs"));
});

test("al terminar la ejecución: el resumen de cierre con los fallos nuevos y escalar en un clic", async ({ page }) => {
	const done = (now) => { const d = liveSuite(now); d.execution.execution.running = false; d.execution.execution.ended_at = now; return d; };
	await page.unroute("**/api/v1/live/execution?**");
	await page.route("**/api/v1/live/execution?**", (r) => r.fulfill({ json: done(Date.now()).execution }));
	await page.route(/\/api\/v1\/tests\/\d+\/history/, (r) => { const id = Number(r.request().url().match(/tests\/(\d+)/)[1]); return r.fulfill({ json: [{ test_id: id + 9000, run_id: 0, status: "PASS", started_at: 1 }] }); });
	await page.reload();
	const s = page.locator("#view-live .live-summary");
	await expect(s).toContainText("Ejecución terminada");
	await expect(s).toContainText("3 fallos nuevos frente a la ejecución anterior.");
	await expect(s.locator('a[href*="view=escalate"]')).toContainText("Escalar los fallos");
});

test("sin parpadeo: al refrescarse, la vista no se rehace (los elementos que no cambian siguen siendo los mismos)", async ({ page }) => {
	const view = page.locator("#view-live");
	// marcas en elementos que no cambian entre un refresco y otro: si la vista se rehiciera, se perderían
	await view.evaluate((v) => { for (const el of v.querySelectorAll(".live-tile, .seg, .lf-lane, tr.lt-row")) el.__keep = 1; });
	const n0 = served;
	await expect.poll(() => served, { timeout: 15_000 }).toBeGreaterThan(n0); // la vista se refrescó con datos nuevos
	await page.waitForTimeout(400);
	const kept = await view.evaluate((v) => [...v.querySelectorAll(".live-tile, .seg, .lf-lane, tr.lt-row")].map((el) => !!el.__keep));
	expect(kept.length).toBeGreaterThan(10);
	expect(kept.every(Boolean), "ningún elemento se reemplazó").toBeTruthy();
	// el canvas del campo tampoco (su animación no se reinicia)
	await expect(view.locator(".lf-canvas")).toHaveCount(1);
});

test("Requiere atención: 'ver más' abre la lista con su propio scroll; 'ver menos' está arriba y al cerrar la tarjeta queda a la vista", async ({ page }) => {
	// 20 fallos más que la suite de siempre: la lista pasa de 8
	const many = (now) => { const d = liveSuite(now); let k = 0; for (const r of d.execution.execution.runs) for (const x of r.tests) if (x.state === "PASS" && k++ < 20) x.state = "FAIL"; return d; };
	await page.unroute("**/api/v1/live/execution?**");
	await page.route("**/api/v1/live/execution?**", (r) => r.fulfill({ json: many(Date.now()).execution }));
	await page.reload();
	const view = page.locator("#view-live"), card = view.locator(".live-attn");
	await card.locator(".live-collapsed[data-live-attnmore]").click();
	const scroll = card.locator(".la-scroll");
	await expect(scroll).toBeVisible();
	const box = await scroll.boundingBox();
	expect(box.height, "media pantalla como mucho").toBeLessThanOrEqual(900 * 0.55);
	expect(await scroll.evaluate((n) => n.scrollHeight > n.clientHeight), "la lista se recorre adentro").toBeTruthy();
	// "ver menos" a mano, arriba, sin bajar
	const less = card.locator(".la-less");
	await expect(less).toBeInViewport();
	await less.click();
	await expect(card.locator(".la-scroll")).toHaveCount(0);
	await expect(card.locator(".la-list li")).toHaveCount(8);
	await expect(card).toBeInViewport();
});

test("buscar no congela: la vista se redibuja una vez al dejar de escribir, no con cada letra", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator(".seg-o[data-live-all]").click();
	const before = await view.locator("tr.lt-row").count();
	// dos letras seguidas en el mismo instante: todavía no se filtra (antes, cada letra rehacía la tabla entera)
	const same = await page.evaluate(() => {
		const q = document.querySelector("#view-live [data-live-q]");
		for (const v of ["t", "te"]) { q.value = v; q.dispatchEvent(new Event("input", { bubbles: true })); }
		return document.querySelectorAll("#view-live tr.lt-row").length;
	});
	expect(same).toBe(before);
	await view.locator("[data-live-q]").fill("periodontograma_paso_1");
	await expect(view.locator("tr.lt-row")).not.toHaveCount(before);
	await expect(view.locator("tr.lt-row").first()).toContainText("periodontograma_paso_1");
	await expect(view.locator("[data-live-q]")).toBeFocused();
});

test("la tabla de a 100 filas con 'Mostrar 100 más'; agrupar por error junta los afectados de cada causa", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator(".seg-o[data-live-all]").click();
	await view.locator('.lt-sort[data-live-sort="name"]').click();
	await expect(view.locator("tr.lt-row")).toHaveCount(100);
	const more = view.locator("[data-live-rowsmore]");
	await expect(more).toContainText(/Mostrar 100 más \(\d+ sin mostrar\)/);
	await more.click();
	await expect(view.locator("tr.lt-row")).toHaveCount(200);
	await view.locator('.lt-sort[data-live-sort="name"]').click();
	await view.locator('.lt-sort[data-live-sort="name"]').click(); // el tercero vuelve a agrupar
	await view.locator('[data-live-groupby="error"]').click();
	await expect(view.locator(".live-status")).toContainText("Agrupado por error");
	const first = view.locator("tbody.live-run").first();
	await expect(first.locator(".live-run-h b")).toHaveText("TimeoutException en fa-plus-circle");
	await expect(first.locator("tr.lt-row")).toHaveCount(3);
	await expect(view.locator("tbody.live-run").last().locator(".live-run-h b")).toHaveText("Sin fallo");
});

test("En vivo: el título dice la ejecución, Trace Buddy se achica para no tapar, la cinta no roba el Tab y la hora va en 24 h", async ({ page }) => {
	await expect(page.locator("#report-name")).toHaveText("rayen_salud_box_clinico · c3aec76");
	await expect(page.locator("body")).toHaveClass(/view-live/);
	const buddy = page.locator("#buddy");
	if (await buddy.count()) expect(await buddy.evaluate((n) => getComputedStyle(n).transform)).not.toBe("none");
	const tape = page.locator("#view-live .tp-i");
	await expect(tape.first()).toHaveAttribute("tabindex", "-1");
	const times = await page.locator("#view-live .tp-i small").allTextContents();
	expect(times.length).toBeGreaterThan(0);
	for (const x of times) expect(x, "sin a. m./p. m.").not.toMatch(/[ap]\.?\s?m\.?/i);
	// al salir a otra vista, el título vuelve a ser el del reporte y Buddy vuelve a su tamaño
	await page.goto("/#view=summary");
	await expect(page.locator("body")).not.toHaveClass(/view-live/);
});

test("mapa: la tarjeta lleva el color de su estado; con un filtro, lo apagado no responde al mouse, al clic ni al teclado", async ({ page }) => {
	const view = page.locator("#view-live");
	await expect(view.locator(".live-map.is-field .lf-canvas")).toBeVisible();
	await page.evaluate(() => document.querySelector("#view-live .live-map").scrollIntoView({ block: "center" }));
	const box = await view.locator(".lf-canvas").boundingBox();
	await page.mouse.move(box.x + 6, box.y + 6); // con el mouse encima deja de girar
	// onlySame: con el filtro puesto, solo cuentan como vecinos los que coinciden (lo apagado no se toca)
	const where = (state, onlySame = false) => page.evaluate(async ([state, onlySame]) => {
		const F = window.TraceReportsLiveField, LIVE = window.TraceReportsLive;
		const { execution, now } = await (await fetch("/api/v1/live/execution?key=x%3Asuite-demo")).json();
		const c = document.querySelector(".lf-canvas"), r = c.getBoundingClientRect();
		const L = F.fieldLayout(execution, now, LIVE), g = F.geometry(r.width, r.height);
		// uno sin vecinos a menos de 20 px: el mouse cae solo sobre él
		const heads = L.items.map((it) => ({ it, p: F.headOf(g, L, it, Number(c.dataset.spin || 0)) }));
		const near = onlySame ? heads.filter((o) => o.it.state === state) : heads;
		const inView = (p) => r.left + p.x > 10 && r.left + p.x < innerWidth - 10 && r.top + p.y > 10 && r.top + p.y < innerHeight - 10;
		const lone = heads.find(({ it, p }) => it.state === state && inView(p) && near.every((o) => o.it === it || Math.hypot(o.p.x - p.x, o.p.y - p.y) > 20));
		return lone && { x: r.left + lone.p.x, y: r.top + lone.p.y, name: lone.it.name };
	}, [state, onlySame]);
	let fail = await where("FAIL"), pass = await where("PASS");
	expect(fail && pass).toBeTruthy();
	const tip = view.locator(".lf-tip");
	await page.mouse.move(fail.x, fail.y);
	await expect(tip).toHaveClass(/lf-card st-fail/);
	await expect(tip.locator(".lfc-state")).toHaveText("Fallo");
	await expect(tip.locator(".lfc-name")).toHaveText(fail.name);
	await expect(tip.locator(".lfc-hint")).toContainText("Ctrl + clic");
	// la tarjeta va en la capa superior: las tarjetas fijas del mapa (ingesta, anomalía, carriles) no la tapan
	expect(await tip.evaluate((n) => n.matches(":popover-open") && getComputedStyle(n).position)).toBe("fixed");
	const accent = await tip.evaluate((n) => [getComputedStyle(n).borderLeftColor, getComputedStyle(document.body).getPropertyValue("--color-danger")]);
	expect(accent[0]).not.toBe("rgba(0, 0, 0, 0)");
	await page.mouse.move(pass.x, pass.y);
	await expect(tip).toHaveClass(/st-pass/);
	// la anomalía: el error en dos líneas (no una cortada), con cortes suaves en / . _ : ]
	const anom = view.locator(".lf-hud.bl.bad > b");
	expect(await anom.evaluate((b) => [getComputedStyle(b).webkitLineClamp, getComputedStyle(b).whiteSpace])).toEqual(["2", "normal"]);
	// filtrado por Fallo: el que pasó se ve tenue pero no se puede tocar
	await view.locator('.live-tile[data-live-state="FAIL"]').click();
	await page.evaluate(() => document.querySelector("#view-live .live-map").scrollIntoView({ block: "center" }));
	const box2 = await view.locator(".lf-canvas").boundingBox();
	await page.mouse.move(box2.x + 6, box2.y + 6); // de vuelta en el mapa: deja de girar y se recalcula dónde quedó cada uno
	await page.waitForTimeout(300);
	fail = await where("FAIL", true); pass = await where("PASS");
	await page.mouse.move(pass.x, pass.y);
	await expect(tip).toBeHidden();
	await page.mouse.click(pass.x, pass.y);
	await expect(view.locator(".live-panel")).toHaveCount(0);
	await page.mouse.move(fail.x, fail.y);
	await expect(tip).toHaveClass(/st-fail/);
	// el teclado recorre solo lo que coincide
	await view.locator(".lf-canvas").focus();
	for (let i = 0; i < 3; i++) {
		await page.keyboard.press("ArrowRight");
		await expect(tip.locator(".lfc-state")).toHaveText("Fallo");
	}
});

test("cambiar de test con el panel abierto no lo vacía: lo anterior atenuado hasta que llega, sin repetir la entrada; volver a uno ya visto es instantáneo", async ({ page }) => {
	const view = page.locator("#view-live");
	const rows = view.locator("tr.lt-row");
	await rows.nth(0).click();
	const panel = view.locator(".live-panel");
	await expect(panel.locator(".lt-trace, .live-steps, .live-shot, .m-hint").first()).toBeVisible();
	const firstName = (await panel.locator("h4").innerText()).trim();
	// el siguiente tarda en llegar: se ve el estado intermedio
	let release;
	const gate = new Promise((r) => { release = r; });
	await page.route(/\/api\/v1\/tests\/\d+$/, async (route) => {
		await gate;
		return route.fallback();
	});
	await rows.nth(1).dispatchEvent("click"); // el panel tapa el lado derecho de la fila
	const secondName = (await rows.nth(1).locator(".lt-name").innerText()).trim();
	await expect(panel.locator("h4")).toHaveText(secondName); // el encabezado ya es del elegido
	await expect(panel.locator(".live-pbody")).toHaveClass(/is-stale/);
	await expect(panel.locator(".live-pbody")).toHaveAttribute("aria-busy", "true");
	await expect(panel.locator(".lp-skel")).toHaveCount(0); // nunca vacío ni esqueleto al cambiar
	await expect(panel.locator(".lt-trace.lt-trace-in")).toHaveCount(0); // la traza no vuelve a entrar fila por fila
	release();
	await expect(panel.locator(".live-pbody")).not.toHaveClass(/is-stale/);
	await page.unroute(/\/api\/v1\/tests\/\d+$/);
	// volver al primero: ya estaba, se muestra al instante (sin esperar ni atenuar)
	let asked = 0;
	await page.route(/\/api\/v1\/tests\/\d+$/, async (route) => { asked++; await new Promise((r) => setTimeout(r, 1500)); return route.fallback(); });
	await rows.nth(0).dispatchEvent("click");
	await expect(panel.locator("h4")).toHaveText(firstName);
	await expect(panel.locator(".live-pbody")).not.toHaveClass(/is-stale/, { timeout: 500 });
	expect(asked).toBeLessThanOrEqual(1); // se actualiza por detrás, sin bloquear
});

test("un refresco lento no deja la vista en 'Cargando…': lo que se ve sigue ahí y un clic abre el detalle", async ({ page }) => {
	const view = page.locator("#view-live");
	await view.locator(".live-title [data-live-pause]").click();
	await expect(view.locator(".live-paused")).toContainText("En pausa");
	// el servidor tarda en responder la ejecución (como durante una suite pesada)
	let release;
	const gate = new Promise((r) => { release = r; });
	let asking = false;
	await page.route("**/api/v1/live/execution?**", async (route) => { asking = true; await gate; return route.fallback(); });
	const n = served;
	await view.locator(".live-title [data-live-pause]").click(); // reanudar pide la ejecución de nuevo
	await expect.poll(() => asking).toBe(true);
	await view.locator("tr.lt-row").nth(0).click(); // un clic mientras llega: redibuja la vista
	await expect(view.locator(".live-panel h4")).toBeVisible();
	await expect(view.locator(".live-tiles")).toBeVisible();
	await expect(view.locator(".live-map .lf-canvas")).toHaveCount(1);
	await expect(view.locator(":scope > .m-hint", { hasText: "Cargando…" })).toHaveCount(0);
	expect(served).toBe(n); // seguía esperando la respuesta
	release();
	await expect.poll(() => served).toBeGreaterThan(n);
	await expect(view.locator(".live-tiles")).toBeVisible();
});

test("mapa: el clic se nota al instante; dos clics rápidos en un punto nuevo lo eligen sin salir; Ctrl + clic abre", async ({ page }) => {
	const view = page.locator("#view-live");
	const canvas = view.locator(".lf-canvas");
	await page.evaluate(() => document.querySelector("#view-live .live-map").scrollIntoView({ block: "center" }));
	const box = await canvas.boundingBox();
	await page.mouse.move(box.x + 4, box.y + 4); // con el mouse encima el campo deja de girar
	const where = (state) => page.evaluate(async (state) => {
		const F = window.TraceReportsLiveField, LIVE = window.TraceReportsLive;
		const { execution, now } = await (await fetch("/api/v1/live/execution?key=x%3Asuite-demo")).json();
		const c = document.querySelector(".lf-canvas"), r = c.getBoundingClientRect();
		const L = F.fieldLayout(execution, now, LIVE), g = F.geometry(r.width, r.height);
		const it = L.items.filter((x) => x.state === state).map((x) => ({ x, p: F.headOf(g, L, x, Number(c.dataset.spin || 0)) }))
			.find(({ p }) => p.x > 30 && p.x < r.width - 30 && p.y > 30 && p.y < r.height - 30);
		return { x: r.left + it.p.x, y: r.top + it.p.y, id: it.x.id };
	}, state);
	// el detalle tarda (servidor lento): el punto queda marcado igual, al instante
	let release;
	const gate = new Promise((r) => { release = r; });
	await page.route(/\/api\/v1\/tests\/\d+$/, async (route) => { await gate; return route.fallback(); });
	const fail = await where("FAIL");
	const url = page.url();
	await page.mouse.dblclick(fail.x, fail.y); // el "doble clic" de quien vuelve a hacer clic porque no pasaba nada
	await expect(canvas).toHaveAttribute("data-selected", String(fail.id));
	await page.waitForTimeout(400);
	expect(page.url()).toBe(url); // no salió de En vivo
	await expect(view.locator(".live-panel")).toBeVisible();
	release();
	await page.unroute(/\/api\/v1\/tests\/\d+$/);
	await expect(view.locator(".live-panel .lp-open .m-hint")).toContainText("Ctrl + clic");
	// Ctrl + clic en otro punto: abre ese test
	const pass = await where("PASS");
	await page.keyboard.down("Control");
	await page.mouse.click(pass.x, pass.y);
	await page.keyboard.up("Control");
	await expect(page).toHaveURL(new RegExp(`view=tests&test=${pass.id}`));
	await page.waitForTimeout(300);
	test.info().errors_ = test.info().errors_.filter((e) => !/404|\/api\/v1\/runs\/\d+/.test(e));
});
