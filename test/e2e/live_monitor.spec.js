// En vivo para monitorear (una suite con 20 workers): llegar rápido a lo que falla. Una
// ejecución con 20 workers (fixtures/live_suite.js → liveWorkers) servida con page.route: "Requiere
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
	// el historial (ejecuciones anteriores) también simulado: el del servidor e2e trae las de otros tests
	await page.route("**/api/v1/live/history?**", (r) => r.fulfill({ json: { now: Date.now(), total: 0, next: 0, executions: [] } }));
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
	await page.goto(`/#view=live&exec=x%3Asuite-laptop${extra}`);
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
	await expect(rows.nth(1)).toContainText("test_002_web_pay_with_saved_card");
	await expect(rows.nth(1)).toContainText("Shop · test_pay_with_saved_card_chrome");
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
	await page.keyboard.type("size_chart");
	await expect(page).toHaveURL(/lq=size_chart/);
	await expect(view.locator("tr.lt-row")).not.toHaveCount(0);
	for (const name of await view.locator("tr.lt-row .lt-name").allTextContents()) expect(name).toContain("size_chart");
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
	const long = view.locator("tr.lt-row", { hasText: "test_001_web_cancel_pending_orders" });
	await expect(long.locator(".lt-steptext")).toContainText("el xpath fue: btn-secondary en Acme Admin");
	expect((await long.boundingBox()).height).toBeLessThan(40);
	await expect(long.locator(".lt-steptext")).toHaveAttribute("data-tip", /normalize-space/);
	// el fallo: sin el volcado en la fila; el badge lo abre debajo y lo vuelve a cerrar
	const fail = view.locator("tr.lt-row.fail");
	await expect(fail.locator(".lt-steptext")).toHaveText("No se encontró el elemento 'modal-dialog', detalle del error");
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
	await expect(view.locator(".lg-worker small").first()).not.toHaveText(/^Shop · t/);
	// ninguna barra muestra un nombre recortado: o cabe entero o no lleva texto
	const cut = await view.locator(".lg-bar:not(.notext)").evaluateAll((bs) => bs.filter((b) => b.scrollWidth > b.clientWidth + 1).length);
	expect(cut).toBe(0);
	expect(await view.locator(".lg-bar.notext").count()).toBeGreaterThan(0);
	// el Gantt se angosta sin que cambie la ventana (una barra de scroll, un panel): se vuelve a medir y ningún
	// nombre queda cortado. Y el brillo de lo que corre no cuenta como desborde (con transform, según el
	// instante, una barra en curso parecía no caber: en CI fallaba al azar y la barra perdía su nombre)
	// (con la vista en pausa: ningún refresco la vuelve a medir por casualidad)
	await view.locator("[data-live-pause]").first().click();
	await view.locator(".live-gantt").evaluate((g) => { g.style.width = "62%"; });
	await page.waitForTimeout(400);
	const cutNarrow = await view.locator(".lg-bar:not(.notext)").evaluateAll((bs) => bs.filter((b) => b.scrollWidth > b.clientWidth + 1).length);
	expect(cutNarrow).toBe(0);
	await view.locator(".live-gantt").evaluate((g) => { g.style.width = ""; });
	await view.locator("[data-live-pause]").first().click();
	// el tooltip de una barra: nombre, archivo, estado, duración, inicio → fin
	const bar = view.locator(".lg-bar.fail");
	await bar.hover();
	await expect(page.locator("#cf-tooltip")).toBeVisible();
	await expect(page.locator("#cf-tooltip")).toContainText("test_002_web_pay_with_saved_card");
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
	await expect(page).toHaveURL(/exec=x%3Asuite-laptop/);
	await expect(view.locator(".xp-btn")).toContainText("suite-laptop-20261109-081806");
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
		if (key !== "x:suite-laptop") return r.fulfill({ status: 404, json: { error: "execution not found" } });
		return r.fulfill({ json: liveWorkers(Date.now()).execution });
	});
	await page.goto("/#view=live&exec=x%3Ano-existe");
	await expect(page.locator("#view-live .live-attn")).toBeVisible();
	await expect(page).toHaveURL(/exec=x%3Asuite-laptop/);
	// el 404 de la clave que no existe lo anota el navegador; es el esperado, ningún otro error
	const info = test.info();
	info.errors_ = info.errors_.filter((e) => !/status of 404/.test(e));
});
test("mapa Agentes: el reactor señala lo importante; el nombre de un worker filtra la tabla; con «Fallo» lo que pasó no responde; zoom para apuntar al que falló, girarlo y pantalla completa", async ({ page }) => {
	await open(page);
	const view = page.locator("#view-live");
	await view.locator('[data-live-fieldview="agents"]').click();
	const canvas = view.locator(".la-canvas");
	await expect(canvas).toHaveAttribute("data-agents", "20");
	// al abrir, los agentes bocetan el modelo; después se enciende (y aparecen las anotaciones)
	await expect(canvas).toHaveAttribute("data-sketch", "on");
	await expect(view.locator(".la-note")).toHaveCount(0);
	await expect(canvas).toHaveAttribute("data-sketch", "off", { timeout: 6000 });
	await expect(canvas).toHaveAttribute("aria-label", /Agentes de la ejecución: 20 workers/);
	await canvas.scrollIntoViewIfNeeded();
	// las anotaciones: el sin señal, el más lento y el último fallo
	await expect(view.locator(".la-note.nosig")).toContainText("SIN SEÑAL", { timeout: 6000 });
	await expect(view.locator(".la-note.turtle")).toContainText("LENTO");
	await expect(view.locator(".la-note.fail")).toContainText("ÚLTIMO FALLO");
	// dónde quedó cada cosa, con el zoom de ahora (lo que dibujó el lienzo)
	const plan = () => page.evaluate(() => {
		const c = document.querySelector(".la-canvas"), r = c.getBoundingClientRect(), lay = c.layout();
		const { k, x: vx, y: vy } = c.view();
		const stuck = lay.segs.find((a) => a.state === "no_signal");
		const cells = lay.segs.flatMap((a) => a.cells);
		const pass = cells.find((nd) => nd.t.state === "PASS" && nd.t.ended_at), fail = cells.find((nd) => nd.t.state === "FAIL");
		const at = (p) => ({ x: r.left + vx + p.x * k, y: r.top + vy + p.y * k });
		return { stuck: { key: stuck.key, ...at(stuck.lp) }, pass: { name: pass.t.name, id: pass.t.id, ...at(pass) }, fail: { name: fail.t.name, id: fail.t.id, r: fail.r, ...at(fail) } };
	});
	const p = await plan();
	// las anotaciones no tapan celdas: se puede apuntar a cualquier test
	const covered = await page.evaluate(() => {
		const c = document.querySelector(".la-canvas"), r = c.getBoundingClientRect();
		const boxes = [...document.querySelectorAll(".la-note .la-box")].map((b) => b.getBoundingClientRect());
		return c.layout().segs.flatMap((s) => s.cells).filter((cell) => boxes.some((b) => {
			const x = r.left + cell.x, y = r.top + cell.y;
			return x > b.left && x < b.right && y > b.top && y < b.bottom;
		})).map((cell) => cell.t.name);
	});
	expect(covered).toEqual([]);
	const tip = view.locator(".la-wrap .lf-tip");
	// el nombre del worker trabado: su tarjeta y, con un clic, la tabla con solo sus tests (otro clic: todos)
	await page.mouse.move(p.stuck.x, p.stuck.y);
	await expect(tip).toContainText("Sin señal");
	await page.mouse.click(p.stuck.x, p.stuck.y);
	await expect(view.locator(`[aria-label="Quitar filtro: ${p.stuck.key}"]`)).toBeVisible();
	await page.mouse.click(p.stuck.x, p.stuck.y);
	await expect(view.locator(`[aria-label="Quitar filtro: ${p.stuck.key}"]`)).toHaveCount(0);
	// un nodo: su tarjeta y, con un clic, su detalle al costado
	await page.mouse.move(5, 5);
	await page.mouse.move(p.pass.x, p.pass.y);
	await expect(tip).toContainText(p.pass.name);
	await page.mouse.click(p.pass.x, p.pass.y);
	await expect(view.locator(".live-panel h4")).toHaveText(p.pass.name);
	await page.keyboard.press("Escape");
	// con el filtro "Fallo": el que pasó queda apagado y no responde; el fallo sí
	await view.locator('.live-tile[data-live-state="FAIL"]').click();
	await canvas.scrollIntoViewIfNeeded();
	const q = await plan();
	await page.mouse.move(5, 5);
	await page.mouse.move(q.pass.x, q.pass.y);
	await page.waitForTimeout(300);
	await expect(tip).toBeHidden();
	await page.mouse.click(q.pass.x, q.pass.y);
	await expect(view.locator(".live-panel")).toHaveCount(0);
	await page.mouse.move(q.fail.x, q.fail.y);
	await expect(tip).toContainText(q.fail.name);
	await expect(view.locator(".la-note.nosig")).toHaveCount(0); // las anotaciones también siguen al filtro
	await view.locator('.live-tile[data-live-state="FAIL"]').click();
	// zoom: botones y teclas, como en el campo; 0 vuelve
	await canvas.focus();
	await page.keyboard.press("+");
	await expect(canvas).toHaveAttribute("data-zoom", "1.25");
	await view.locator('.la-wrap [data-lf="in"]').click();
	await expect(canvas).toHaveAttribute("data-zoom", "1.56");
	await canvas.focus();
	await page.keyboard.press("0");
	await expect(canvas).toHaveAttribute("data-zoom", "1.00");
	// apuntar al que falló: Ctrl + rueda sobre él lo agranda (queda bajo el mouse) y un clic abre su detalle
	const f0 = (await plan()).fail;
	await page.mouse.move(f0.x, f0.y);
	await page.keyboard.down("Control");
	for (let i = 0; i < 6; i++) await page.mouse.wheel(0, -100);
	await page.keyboard.up("Control");
	await expect.poll(async () => Number(await canvas.getAttribute("data-zoom"))).toBeGreaterThan(1.5);
	const z = Number(await canvas.getAttribute("data-zoom"));
	const big = await page.evaluate((id) => { const c = document.querySelector(".la-canvas"); return c.layout().at.get(id).r; }, f0.id);
	expect(big * z).toBeGreaterThan(f0.r * 1.5); // en pantalla, bastante más grande
	const f1 = (await plan()).fail; // donde quedó con el zoom (debajo del mouse, salvo en un borde)
	await page.mouse.move(5, 5);
	await page.mouse.move(f1.x + 2, f1.y);
	await expect(tip).toContainText(f0.name);
	await page.mouse.click(f1.x + 2, f1.y);
	await expect(view.locator(".live-panel h4")).toHaveText(f0.name);
	await page.keyboard.press("Escape");
	await canvas.focus();
	await page.keyboard.press("0");
	// sin zoom, arrastrar gira el reactor (la celda cambia de lugar) y 0 lo devuelve
	await expect(canvas).toHaveAttribute("data-cam", "0.00 0.72");
	const box = await canvas.boundingBox();
	await page.mouse.move(box.x + 60, box.y + box.height / 2);
	await page.mouse.down();
	await page.mouse.move(box.x + 160, box.y + box.height / 2 - 30, { steps: 5 });
	await page.mouse.up();
	await expect(canvas).not.toHaveAttribute("data-cam", "0.00 0.72");
	expect((await plan()).fail.x).not.toBeCloseTo(f0.x, 0);
	await canvas.focus();
	await page.keyboard.press("0");
	await expect(canvas).toHaveAttribute("data-cam", "0.00 0.72");
	// B repite el boceto; un clic lo salta sin abrir nada
	await page.keyboard.press("b");
	await expect(canvas).toHaveAttribute("data-sketch", "on");
	const c0 = await canvas.boundingBox();
	await page.mouse.click(c0.x + c0.width / 2, c0.y + c0.height / 2);
	await expect(canvas).toHaveAttribute("data-sketch", "off");
	await expect(view.locator(".live-panel")).toHaveCount(0);
	await expect(view.locator(".la-note.fail .la-box")).toBeVisible(); // las anotaciones vuelven
	await canvas.focus();
	// las flechas recorren los workers, con su tarjeta
	await page.keyboard.press("ArrowRight");
	await expect(tip).toContainText("gw0");
	// pantalla completa (F) y Esc para volver
	await page.keyboard.press("f");
	await expect(page.locator("body > .la-wrap.lf-full")).toBeVisible();
	await page.keyboard.press("Escape");
	await expect(page.locator("body > .la-wrap.lf-full")).toHaveCount(0);
	await expect(view.locator(".la-canvas")).toBeVisible();
	// de vuelta al campo
	await view.locator('[data-live-fieldview="field"]').click();
	await expect(view.locator(".la-canvas")).toHaveCount(0);
});
