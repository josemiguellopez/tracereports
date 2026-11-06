// Inspector: la página del momento del fallo (y la de cada paso) reconstruida desde el snapshot
// estructural, para inspeccionarla, probar XPath/CSS y copiar locators sin volver a reproducir el
// fallo. Va al final (zz_): crea una ejecución nueva.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });
test.use({ permissions: ["clipboard-read", "clipboard-write"] });

const style = (bg, color = "rgb(30, 30, 30)") => ({ backgroundColor: bg, color, fontSize: "14px", borderTopWidth: "0px" });
/** Una página de la tienda: cabecera, ficha del producto y (si `pay`) el botón Pagar. */
function page(title, { pay = true, extra = [] } = {}) {
	const nodes = [
		{ i: 0, p: -1, tag: "body", attrs: {}, text: "", x: 0, y: 0, w: 1000, h: 600, visible: true, depth: 0, style: style("rgb(246, 244, 239)") },
		{ i: 1, p: 0, tag: "header", attrs: { class: "top" }, text: "Demo Shop", x: 0, y: 0, w: 1000, h: 60, visible: true, depth: 1, style: style("rgb(31, 95, 69)", "rgb(255, 255, 255)") },
		{ i: 2, p: 0, tag: "div", attrs: { id: "ficha", class: "card" }, text: "", x: 40, y: 100, w: 600, h: 300, visible: true, depth: 1, style: style("rgb(255, 255, 255)") },
		{ i: 3, p: 2, tag: "h1", attrs: {}, text: title, x: 60, y: 120, w: 400, h: 40, visible: true, depth: 2, style: style("rgba(0, 0, 0, 0)") },
	];
	if (pay) nodes.push({ i: 4, p: 2, tag: "button", attrs: { "data-testid": "pay", class: "btn" }, text: "Pagar ahora", x: 60, y: 300, w: 180, h: 44, visible: true, depth: 2, style: style("rgb(31, 111, 235)", "rgb(255, 255, 255)") });
	for (const e of extra) nodes.push({ ...e, i: nodes.length });
	return { url: "https://shop.test/p/1", title, viewport: { w: 1000, h: 600 }, page: { w: 1000, h: 600 }, nodes, elements: [] };
}

async function failedTest(request) {
	const run = (await (await request.post("/api/v1/runs", { data: { name: "Inspector E2E", environment: "qa" } })).json()).run_id;
	const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "compra con tarjeta" } })).json()).test_id;
	await request.post(`/api/v1/tests/${id}/logs`, { data: { status: "INFO", message: "abre la ficha" } });
	for (const [label, title] of [["Iniciar sesión", "Login"], ["Abrir la ficha", "Zapatilla Trail"]]) {
		const r = await request.post(`/api/v1/tests/${id}/dom`, { data: { ...page(title, { pay: false }), kind: "step", label } });
		expect(r.status()).toBeLessThan(300);
	}
	// el texto de la página es dato del cliente: se dibuja como texto, nunca como HTML ni se traduce
	const fail = page("Zapatilla Trail", { extra: [
		{ p: 2, tag: "span", attrs: { class: "evil" }, text: "<img src=x onerror=window.__pwned=1>", x: 60, y: 200, w: 400, h: 24, visible: true, depth: 2, style: style("rgba(0, 0, 0, 0)") },
		{ p: 2, tag: "span", attrs: { class: "save" }, text: "Guardar", x: 300, y: 300, w: 80, h: 24, visible: true, depth: 2, style: style("rgba(0, 0, 0, 0)") },
	] });
	expect((await request.post(`/api/v1/tests/${id}/dom`, { data: fail })).status()).toBeLessThan(300);
	await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL",
		error_message: 'TimeoutError: Locator.click: Timeout 5000ms exceeded.\n  - waiting for locator("#btn-pagar")' } });
	return { run, id };
}

test.beforeEach(async ({ page: p }, info) => {
	info.errors_ = [];
	p.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	p.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("el Inspector reconstruye el fallo, marca el selector roto y sugiere el parecido", async ({ page: p, request }) => {
	const { run, id } = await failedTest(request);
	const t = await (await request.get(`/api/v1/tests/${id}`)).json();
	expect(t).toMatchObject({ has_dom: true, dom_steps: 2 });

	await p.goto(`/#run=${run}&view=tests&test=${id}`);
	const tab = p.locator('#test-detail [data-tab="inspector"]');
	await expect(tab).toContainText("3"); // 2 pasos + el fallo
	await tab.click();
	const insp = p.locator("#inspector-panel .insp");
	await insp.locator('[data-insp="instant"]').click();
	await expect(insp.locator(".insp-status")).toHaveText(/\d+ elementos reconstruidos/);

	// recorrido: los dos pasos y el fallo, el último marcado
	const steps = insp.locator(".insp-steps li");
	await expect(steps).toHaveCount(3);
	await expect(steps.nth(2)).toHaveClass(/failed/);
	await expect(steps.nth(2)).toContainText("#btn-pagar");

	// el selector roto no existe: fantasma, y el más parecido es el botón Pagar
	await expect(insp.locator(".insp-ghost")).toContainText("#btn-pagar");
	await expect(insp.locator(".insp-failinfo")).toContainText("El elemento más parecido");
	await expect(insp.locator(".insp-n.similar")).toHaveText("Pagar ahora");
	await expect(insp.locator(".insp-failinfo .insp-cand code")).toHaveText('[data-testid="pay"]');

	// los textos de la página: literales y sin traducir
	await expect(insp.locator(".insp-n", { hasText: "<img src=x" })).toHaveCount(1);
	expect(await p.evaluate(() => window.__pwned)).toBeUndefined();
	await expect(insp.locator(".insp-n", { hasText: /^Guardar$/ })).toHaveCount(1);

	// probador
	const probe = insp.locator(".insp-probe");
	await probe.fill("//button[@id='btn-pagar']");
	await expect(insp.locator(".insp-probeout")).toContainText("Ninguna coincidencia");
	await probe.fill("//button[contains(translate(normalize-space(.),'PAGR','pagr'),'pagar')]");
	await expect(insp.locator(".insp-probeout")).toContainText("1 coincidencia: único");
	await expect(insp.locator(".insp-n.match")).toHaveText("Pagar ahora");
	await probe.fill("#ficha span");
	await expect(insp.locator(".insp-probeout")).toContainText("2 coincidencias");
	await probe.fill("//*[");
	await expect(insp.locator(".insp-probeout")).toContainText("Expresión no válida");

	// pasos: cada uno muestra su página
	await steps.nth(0).click();
	await expect(insp.locator(".insp-n", { hasText: /^Login$/ })).toHaveCount(1);
	await expect(insp.locator(".insp-ghost")).toHaveCount(0);
	await steps.nth(2).click();
	await expect(insp.locator(".insp-ghost")).toHaveCount(1);
});

test("clic derecho: menú QA / Automation copia locators y código, y arma el reemplazo", async ({ page: p, request }) => {
	const { run, id } = await failedTest(request);
	await p.goto(`/#run=${run}&view=tests&test=${id}`);
	await p.locator('#test-detail [data-tab="inspector"]').click();
	const insp = p.locator("#inspector-panel .insp");
	await insp.locator('[data-insp="instant"]').click();
	await expect(insp.locator(".insp-status")).toHaveText(/elementos reconstruidos/);

	const title = insp.locator(".insp-n", { hasText: /^Zapatilla Trail$/ });
	await title.click({ button: "right" });
	const menu = p.locator(".insp-ctx");
	await expect(menu).toBeVisible();
	await expect(menu.locator("header")).toContainText("QA / Automation");
	const robust = menu.locator("button.it", { hasText: "XPath robusto" });
	await expect(robust.locator("code")).toHaveText(/^\/\/h1\[contains\(translate\(normalize-space\(\.\),.*'zapatilla trail'\)\]$/);
	await expect(robust.locator(".u")).toHaveText("único ✓");
	await robust.click();
	await expect(menu).toBeHidden();
	await expect(p.locator(".insp-toast")).toContainText("copiado");
	expect(await p.evaluate(() => navigator.clipboard.readText())).toContain("'zapatilla trail'");

	// el menú se maneja con teclado y se cierra con Escape
	await title.click({ button: "right" });
	await expect(menu.locator("button.it").first()).toBeFocused();
	await p.keyboard.press("ArrowDown");
	await expect(menu.locator("button.it").nth(1)).toBeFocused();
	await p.keyboard.press("Escape");
	await expect(menu).toBeHidden();

	// código para Selenium y Playwright
	const pay = insp.locator(".insp-n", { hasText: "Pagar ahora" });
	await pay.click({ button: "right" });
	await expect(menu.locator("button.it", { hasText: "Selenium · Python" }).locator("code")).toHaveText('driver.find_element(By.CSS_SELECTOR, "[data-testid=\\"pay\\"]")');
	await expect(menu.locator("button.it", { hasText: "Playwright · JS/TS" }).locator("code")).toHaveText("page.getByTestId('pay')");
	await expect(menu.locator("button.it", { hasText: "Espera explícita" }).locator("code")).toContainText("element_to_be_clickable");

	// probar desde el menú
	await menu.locator("button.it", { hasText: "Probar en el probador" }).click();
	await expect(insp.locator(".insp-probe")).toHaveValue('[data-testid="pay"]');
	await expect(insp.locator(".insp-probeout")).toContainText("1 coincidencia: único");

	// reemplazo del selector que falló: antes, después y el código del test
	await pay.click({ button: "right" });
	await menu.locator("button.it", { hasText: "Usar como reemplazo" }).click();
	const rep = insp.locator(".insp-replace");
	await expect(rep).toContainText("#btn-pagar");
	await expect(rep).toContainText('[data-testid="pay"]');
	expect(await p.evaluate(() => navigator.clipboard.readText())).toBe('[data-testid="pay"]');
	await rep.locator("[data-copy]", { hasText: "Playwright · Python" }).click();
	expect(await p.evaluate(() => navigator.clipboard.readText())).toBe('page.get_by_test_id("pay")');
});

test("un selector sin nada parecido solo avisa: no marca un elemento cualquiera", async ({ page: p, request }) => {
	const run = (await (await request.post("/api/v1/runs", { data: { name: "Inspector sin parecido E2E", environment: "qa" } })).json()).run_id;
	const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "selector perdido" } })).json()).test_id;
	await request.post(`/api/v1/tests/${id}/dom`, { data: page("Zapatilla Trail") });
	await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL", error_message: 'waiting for locator("#zzqx-kwyj")' } });
	await p.goto(`/#run=${run}&view=tests&test=${id}`);
	await p.locator('#test-detail [data-tab="inspector"]').click();
	const insp = p.locator("#inspector-panel .insp");
	await insp.locator('[data-insp="instant"]').click();
	await expect(insp.locator(".insp-failinfo")).toContainText("no hay un elemento parecido");
	await expect(insp.locator(".insp-bubble")).toContainText("#zzqx-kwyj no existe en la página");
	await expect(insp.locator(".insp-ghost")).toHaveCount(0);
	await expect(insp.locator(".insp-n.similar")).toHaveCount(0);
});

test("un selector que existe pero está oculto: no es el selector, se señala su contenedor", async ({ page: p, request }) => {
	const run = (await (await request.post("/api/v1/runs", { data: { name: "Inspector oculto E2E", environment: "qa" } })).json()).run_id;
	const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "imprimir oculto" } })).json()).test_id;
	await request.post(`/api/v1/tests/${id}/dom`, { data: page("Zapatilla Trail", { extra: [
		{ p: 2, tag: "button", attrs: { "aria-label": "Imprimir receta" }, text: "Imprimir", x: 0, y: 0, w: 0, h: 0, visible: false, depth: 2, style: style("rgba(0, 0, 0, 0)") },
	] }) });
	await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL", error_message: `waiting for locator("[aria-label='Imprimir receta']")` } });
	await p.goto(`/#run=${run}&view=tests&test=${id}`);
	await p.locator('#test-detail [data-tab="inspector"]').click();
	const insp = p.locator("#inspector-panel .insp");
	await insp.locator('[data-insp="instant"]').click();
	await expect(insp.locator(".insp-failinfo")).toContainText("existe pero no es visible");
	await expect(insp.locator(".insp-bubble")).toContainText("el fallo no fue el selector");
	await expect(insp.locator(".insp-n.failed-el")).toHaveCount(1);
	await expect(insp.locator(".insp-n.failed-el")).toHaveAttribute("data-i", "2"); // la ficha que lo contiene
	await expect(insp.locator(".insp-ghost")).toHaveCount(0);
});

test("sin snapshot no hay pestaña Inspector", async ({ page: p, request }) => {
	const run = (await (await request.post("/api/v1/runs", { data: { name: "Inspector vacío E2E", environment: "qa" } })).json()).run_id;
	const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "sin dom" } })).json()).test_id;
	await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL", error_message: "boom" } });
	await p.goto(`/#run=${run}&view=tests&test=${id}`);
	await expect(p.locator('#test-detail [data-tab="steps"]')).toBeVisible();
	await expect(p.locator('#test-detail [data-tab="inspector"]')).toHaveCount(0);
});
