// En vivo para monitorear (feedback 09-11, run 888 con 20 workers): llegar rápido a lo que falla. Una
// ejecución como la del run 888 (fixtures/live_suite.js → liveWorkers) servida con page.route: "Requiere
// atención" arriba, los estados en la URL (&f=fail), el atajo "/", una sola tabla con columnas alineadas
// y Worker, el último paso en una línea (XPath corto, stack trace a pedido), Expandir/Colapsar todo y
// el Gantt legible (gw3, texto solo si cabe, clic → su fila). No crea datos.
const { test, expect } = require("@playwright/test");
const { liveWorkers, liveSuiteEndpoints, liveSuiteTest } = require("./fixtures/live_suite.js");

let mutate = null;
test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
	mutate = null;
	await page.route("**/api/v1/live", (r) => r.fulfill({ json: liveWorkers(Date.now()).list }));
	await page.route("**/api/v1/live/execution?**", (r) => {
		const d = liveWorkers(Date.now()).execution;
		if (mutate) mutate(d.execution);
		return r.fulfill({ json: d });
	});
	await page.route("**/api/v1/live/endpoints?**", (r) => r.fulfill({ json: liveSuiteEndpoints() }));
	// los tests de esta ejecución son simulados: su historial (para "fallo nuevo") viene vacío salvo que el test lo pida
	await page.route(/\/api\/v1\/tests\/\d+\/history/, (r) => r.fulfill({ json: [] }));
	await page.route(/\/api\/v1\/tests\/\d+\/network$/, (r) => r.fulfill({ json: liveSuiteTest(1).network }));
	await page.route(/\/api\/v1\/tests\/\d+$/, (r) => r.fulfill({ json: liveSuiteTest(Number(r.request().url().split("/").pop())).test }));
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

const open = async (page, extra = "") => {
	await page.goto(`/#view=live&exec=x%3Asuite-ntbk${extra}`);
	await expect(page.locator("#view-live .live-tiles")).toBeVisible();
	await expect(page.locator("#view-live")).not.toHaveClass(/live-enter/, { timeout: 5000 });
	return page.locator("#view-live");
};

test("Requiere atención arriba: sin señal, fallo y tortuga con archivo y worker; un clic abre el detalle", async ({ page }) => {
	const view = await open(page);
	const card = view.locator(".live-attn");
	await expect(card).toBeVisible();
	await expect(card.locator(".la-n")).toHaveText("3");
	const rows = card.locator(".la-row");
	await expect(rows).toHaveCount(3);
	await expect(rows.nth(0)).toHaveClass(/nosig/);
	await expect(rows.nth(1)).toContainText("test_002_box_despachar_receta_otro_nodo");
	await expect(rows.nth(1)).toContainText("Clinico · test_despachar_receta_otro_nodo_chrome");
	await expect(rows.nth(1).locator(".la-wk")).toHaveText(/^gw\d+$/);
	await expect(rows.nth(2)).toContainText("Tortuga");
	// está antes que todo lo demás: sin hacer scroll, a 1366 px
	await page.setViewportSize({ width: 1366, height: 768 });
	const box = await card.boundingBox();
	expect(box.y + box.height).toBeLessThan(768);
	await rows.nth(1).click();
	await expect(view.locator(".live-panel")).toBeVisible();
	await expect(rows.nth(1)).toHaveClass(/sel/);
});

test("el enlace con &f=fail abre filtrado (solo el fallo, arriba y abierto); los chips cambian la URL", async ({ page }) => {
	const view = await open(page, "&f=fail");
	await expect(view.locator('.live-tile[data-live-state="FAIL"]')).toHaveAttribute("aria-pressed", "true");
	await expect(view.locator("tr.lt-row")).toHaveCount(1);
	await expect(view.locator("tr.lt-row")).toHaveClass(/fail/);
	await expect(view.locator("tr.lt-row")).toBeVisible();
	// el Gantt atenúa lo que no coincide
	await view.locator(".lg-more").click();
	await expect(view.locator(".lg-bar.fail:not(.dim)")).toHaveCount(1);
	expect(await view.locator(".lg-bar:not(.fail):not(.dim)").count()).toBe(0);
	// otro tile: se combina y queda en el enlace
	await view.locator('.live-tile[data-live-state="WARNING"]').click();
	await expect(page).toHaveURL(/[#&]f=fail%2Cwarn(&|$)/);
	await view.locator(".seg-o[data-live-all]").click();
	await expect(view.locator(".seg-o[data-live-all]")).toHaveAttribute("aria-checked", "true");
	await expect(page).not.toHaveURL(/[#&]f=/);
	// un chip en 0 se ve, pero no se puede elegir
	mutate = (e) => { e.counts = { ...e.counts, turtle: 0 }; };
	await expect(view.locator('.live-tile[data-live-state="turtle"]')).toBeDisabled({ timeout: 15_000 });
	await expect(view.locator('.live-tile[data-live-state="turtle"]')).toBeVisible();
});

test("buscar: / enfoca desde cualquier parte, Esc limpia y luego sale sin cerrar el panel", async ({ page }) => {
	const view = await open(page);
	await view.locator(".la-row").first().click();
	await expect(view.locator(".live-panel")).toBeVisible();
	await view.locator(".live-head h3").click(); // fuera de cualquier campo
	await page.keyboard.press("/");
	const q = view.locator("[data-live-q]");
	await expect(q).toBeFocused();
	await page.keyboard.type("odontograma");
	await expect(page).toHaveURL(/lq=odontograma/);
	await expect(view.locator("tr.lt-row")).not.toHaveCount(0);
	for (const name of await view.locator("tr.lt-row .lt-name").allTextContents()) expect(name).toContain("odontograma");
	await page.keyboard.press("Escape");
	await expect(q).toHaveValue("");
	await expect(q).toBeFocused();
	await expect(view.locator(".live-panel")).toBeVisible();
	await page.keyboard.press("Escape");
	await expect(q).not.toBeFocused();
});

test("una sola tabla: columnas alineadas entre archivos, Worker, último paso en una línea y stack trace a pedido", async ({ page }) => {
	await page.setViewportSize({ width: 1366, height: 900 });
	const view = await open(page);
	await view.locator(".seg-o[data-live-all]").click();
	await view.locator('[data-live-openall="1"]').click();
	await expect(view.locator("table.live-table")).toHaveCount(1);
	await expect(view.locator("table.live-table thead th")).toHaveText(["Test", "Estado", "Dura.", "vs normal", "Worker", "Último paso"]);
	// la columna Estado empieza en el mismo x en todos los archivos
	const xs = await view.locator("tr.lt-row td:nth-child(2)").evaluateAll((tds) => [...new Set(tds.map((td) => Math.round(td.getBoundingClientRect().left)))]);
	expect(xs).toHaveLength(1);
	await expect(view.locator("tr.lt-row td.lt-wk").first()).toHaveText(/^gw\d+$/);
	// el XPath de 300 caracteres: su clase, en una sola línea; completo en el tooltip
	const long = view.locator("tr.lt-row", { hasText: "test_001_box_cancelar_atencion_pacientes" });
	await expect(long.locator(".lt-steptext")).toContainText("el xpath fue: btn-secondary en Rayen Admin");
	expect((await long.boundingBox()).height).toBeLessThan(40);
	await expect(long.locator(".lt-steptext")).toHaveAttribute("data-tip", /normalize-space/);
	// el fallo: sin el volcado en la fila; el badge lo abre debajo y lo vuelve a cerrar
	const fail = view.locator("tr.lt-row.fail");
	await expect(fail.locator(".lt-steptext")).toHaveText("Upss, no se encontró el elemento 'modal-dialog' , el detalle del error es");
	expect((await fail.boundingBox()).height).toBeLessThan(40);
	await fail.locator("[data-live-stack]").click();
	await expect(view.locator(".lt-stackrow pre")).toContainText("(No symbol) [0x7ff67e619ca5]");
	await expect(view.locator(".live-panel")).toHaveCount(0); // el badge no abre el panel
	await view.locator("tr.lt-row.fail [data-live-stack]").click();
	await expect(view.locator(".lt-stackrow")).toHaveCount(0);
	// Colapsar todo: los archivos con problemas, cerrados; los demás vuelven a su línea
	await view.locator('[data-live-openall="0"]').click();
	await expect(view.locator("tr.lt-row")).toHaveCount(0);
	await expect(view.locator("tbody.live-run")).toHaveCount(3);
	await expect(view.locator(".live-collapsed")).toContainText("19 archivos sin problemas");
});

test("Gantt legible: gw3 con el archivo actual, nombre solo si cabe, tooltip completo y clic → su fila", async ({ page }) => {
	await page.setViewportSize({ width: 1366, height: 900 });
	const view = await open(page);
	await view.locator(".lg-more").click();
	await expect(view.locator(".lg-lane")).toHaveCount(20);
	for (const label of await view.locator(".lg-worker b").allTextContents()) expect(label).toMatch(/^gw\d+$/);
	await expect(view.locator(".lg-worker small").first()).not.toHaveText(/^Clinico · t/);
	// ninguna barra muestra un nombre recortado: o cabe entero o no lleva texto
	const cut = await view.locator(".lg-bar:not(.notext)").evaluateAll((bs) => bs.filter((b) => b.scrollWidth > b.clientWidth + 1).length);
	expect(cut).toBe(0);
	expect(await view.locator(".lg-bar.notext").count()).toBeGreaterThan(0);
	// el tooltip de una barra: nombre, archivo, estado, duración, inicio → fin
	const bar = view.locator(".lg-bar.fail");
	await bar.hover();
	await expect(page.locator("#cf-tooltip")).toBeVisible();
	await expect(page.locator("#cf-tooltip")).toContainText("test_002_box_despachar_receta_otro_nodo");
	await expect(page.locator("#cf-tooltip")).toContainText("Fallo");
	await expect(page.locator("#cf-tooltip")).toContainText("→");
	// clic: abre el detalle y lleva a su fila, resaltada
	await bar.click();
	await expect(view.locator(".live-panel")).toBeVisible();
	const row = view.locator("tr.lt-row.fail");
	await expect(row).toHaveClass(/sel/);
	await expect(row).toBeInViewport();
	await expect(view.locator(".lg-bar.fail")).toHaveClass(/sel/);
});

test("si un test cambia de estado y de lugar, la fila se desliza (no salta)", async ({ page }) => {
	await page.addInitScript(() => {
		const animate = Element.prototype.animate;
		window.__flips = 0;
		Element.prototype.animate = function (k, o) {
			if (Array.isArray(k) && String(k[0]?.transform || "").startsWith("translateY")) window.__flips++;
			return animate.call(this, k, o);
		};
	});
	const view = await open(page);
	await expect(view.locator(".la-row")).toHaveCount(3);
	// un test en curso falla: entra a Requiere atención y los de abajo bajan
	mutate = (e) => {
		const t = e.runs[0].tests[e.runs[0].tests.length - 1];
		Object.assign(t, { state: "FAIL", status: "FAIL" });
		e.counts = { ...e.counts, FAIL: (e.counts.FAIL || 0) + 1, running: e.counts.running - 1 };
	};
	await expect(view.locator(".la-row")).toHaveCount(4, { timeout: 15_000 });
	await expect.poll(() => page.evaluate(() => window.__flips)).toBeGreaterThan(0);
	await expect(view.locator(".la-list li.la-new")).toHaveCount(1);
});

test("una ejecución anterior (fuera de la lista) se abre igual con la vista nueva; Observabilidad desde un run", async ({ page }) => {
	// la lista de En vivo ya no la trae (terminó hace días); el servidor la arma por su run (r:<id>)
	const asked = [];
	await page.route("**/api/v1/live", (r) => r.fulfill({ json: { now: Date.now(), no_signal_ms: 180000, executions: [] } }));
	await page.route("**/api/v1/live/execution?**", (r) => {
		const key = new URL(r.request().url()).searchParams.get("key");
		asked.push(key);
		const d = liveWorkers(Date.now() - 3 * 86400e3).execution;
		Object.assign(d.execution, { running: false, ended_at: d.now, past: true });
		return r.fulfill({ json: { ...d, now: Date.now() } });
	});
	// desde el detalle de un run real: el enlace de la barra superior
	await page.goto("/");
	const link = page.locator("#live-of-run");
	await expect(link).toBeVisible();
	await expect(link).toHaveText("Observabilidad"); // no "En vivo": no se confunde con el aviso de la ejecución en curso
	const href = await link.getAttribute("href");
	expect(href).toMatch(/^#view=live&exec=r%3A\d+$/);
	await link.click();
	const view = page.locator("#view-live");
	await expect(view.locator(".live-attn")).toBeVisible();
	await expect(link).toBeHidden(); // en En vivo no hace falta
	expect(asked[0]).toMatch(/^r:\d+$/);
	// la URL queda con la clave real; el selector la muestra como anterior, con su fecha
	await expect(page).toHaveURL(/exec=x%3Asuite-ntbk/);
	await expect(view.locator(".xp-btn")).toContainText("suite-ntbk-20261109-081806");
	await expect(view.locator(".xp-btn .xp-fresh")).toHaveText("anterior");
	// todo lo nuevo funciona igual sobre la anterior: Requiere atención, &f=, stack trace
	await expect(view.locator(".la-row")).toHaveCount(3);
	await view.locator('.live-tile[data-live-state="FAIL"]').click();
	await expect(page).toHaveURL(/f=fail/);
	await expect(view.locator("tr.lt-row")).toHaveCount(1);
	await view.locator("tr.lt-row [data-live-stack]").click();
	await expect(view.locator(".lt-stackrow pre")).toContainText("Stacktrace");
});

test("una clave que no existe vuelve a la ejecución más nueva, sin error", async ({ page }) => {
	await page.route("**/api/v1/live/execution?**", (r) => {
		const key = new URL(r.request().url()).searchParams.get("key");
		if (key !== "x:suite-ntbk") return r.fulfill({ status: 404, json: { error: "execution not found" } });
		return r.fulfill({ json: liveWorkers(Date.now()).execution });
	});
	await page.goto("/#view=live&exec=x%3Ano-existe");
	await expect(page.locator("#view-live .live-attn")).toBeVisible();
	await expect(page).toHaveURL(/exec=x%3Asuite-ntbk/);
	// el 404 de la clave que no existe lo anota el navegador; es el esperado, ningún otro error
	const info = test.info();
	info.errors_ = info.errors_.filter((e) => !/status of 404/.test(e));
});