// Los recorridos de los que depende el negocio: si una funcionalidad nueva rompe alguno, el
// producto deja de cumplir lo que promete (números correctos, links compartidos que abren lo
// que dicen, ningún secreto a la vista, cada audiencia ve lo suyo). Solo lee: no cambia los datos
// del seed que usan las demás pruebas.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);
const SECRETS = ["E2E-SECRET", "E2E-PASSWORD"];

// cualquier error de JavaScript o 5xx en estos recorridos es una falla
test.beforeEach(async ({ page }, info) => {
	const errors = [];
	info.errors_ = errors;
	page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") errors.push(`console.error: ${m.text()}`); });
	page.on("response", (r) => { if (r.status() >= 500) errors.push(`${r.status()} ${r.request().method()} ${r.url()}`); });
});
test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

async function openRun(page, view = "tests") {
	await page.goto(`/#run=${seed().run}&view=${view}`);
	await expect(page.locator("#report-name")).toHaveText("E2E actual");
}

const items = (page) => page.locator("#test-collection .collection-item");

async function expectNoSecrets(page, where) {
	const html = await page.content();
	for (const s of SECRETS) expect(html, `${where}: no debe mostrar ${s}`).not.toContain(s);
}

test.describe("links compartidos", () => {
	test("un link a un test abre ese test, aunque no sea la ejecución más reciente", async ({ page }) => {
		const { run, failId } = seed();
		await page.goto(`/#run=${run}&view=tests&test=${failId}`);
		await expect(page.locator("#test-detail")).toContainText("test_login_admin");
		await expect(page.locator("#test-collection .collection-item.active")).toContainText("test_login_admin");
	});

	test("un link a una vista abre esa vista y sobrevive a recargar", async ({ page }) => {
		await openRun(page, "dashboard");
		await expect(page.locator("#view-dashboard")).toBeVisible();
		await page.reload();
		await expect(page.locator("#view-dashboard")).toBeVisible();
		await expect(page.locator("#report-name")).toHaveText("E2E actual");
	});

	test("abrir otro test actualiza el link para compartirlo", async ({ page }) => {
		await openRun(page);
		await items(page).filter({ hasText: "test_buscar_empleado" }).locator(".test-name").click();
		await expect(page.locator("#test-detail")).toContainText("test_buscar_empleado");
		await expect(page).toHaveURL(/[#&]test=\d+/);
		const url = page.url();
		await page.goto("about:blank");
		await page.goto(url);
		await expect(page.locator("#test-detail")).toContainText("test_buscar_empleado");
	});
});

test.describe("los números del reporte", () => {
	test("filtros por estado: cada test aparece donde corresponde", async ({ page }) => {
		await openRun(page);
		await expect(items(page)).toHaveCount(3);
		await expect(page.locator("#tests-count")).toHaveText("3");
		const filter = (s) => page.locator(`#status-filters [data-status="${s}"]`).click();
		await filter("FAIL");
		await expect(items(page)).toHaveText([/test_login_admin/]);
		await expect(page.locator("#tests-count")).toHaveText("1 / 3");
		await filter("PASS");
		await expect(items(page)).toHaveText([/test_buscar_empleado/]);
		await filter("SKIP");
		await expect(items(page)).toHaveText([/test_reporte_mensual/]);
		await filter("WARNING");
		await expect(page.locator("#test-collection .collection-empty")).toBeVisible();
		await filter("");
		await expect(items(page)).toHaveCount(3);
	});

	test("búsqueda sin resultados lo dice, y al limpiarla vuelven todos", async ({ page }) => {
		await openRun(page);
		await page.locator("#search-tests").fill("no-existe-este-test");
		await expect(page.locator("#test-collection .collection-empty")).toBeVisible();
		await page.locator("#search-tests").fill("");
		await expect(items(page)).toHaveCount(3);
	});

	test("análisis: totales, porcentaje y comparación con la ejecución anterior", async ({ page }) => {
		await openRun(page, "dashboard");
		await expect(page.locator("#d-total")).toHaveText("3");
		await expect(page.locator("#d-pass")).toHaveText("1");
		await expect(page.locator("#d-fail")).toHaveText("1");
		await expect(page.locator("#d-skip")).toHaveText("1");
		const cmp = page.locator("#compare-card");
		await expect(cmp).toContainText(`Comparación con #${seed().prev}`);
		await expect(cmp.locator(".cmp-chip.new_failures")).toHaveText("1 fallo nuevo");
		await expect(cmp.locator(".cmp-group.new_failures")).toContainText("test_login_admin");
		await expect(cmp.locator(".cmp-chip.fixed")).toHaveText("0 arreglados");
		// el link del fallo nuevo lleva a su detalle
		await cmp.locator('.cmp-group.new_failures a:has-text("test_login_admin")').click();
		await expect(page.locator("#view-tests")).toBeVisible();
		await expect(page.locator("#test-detail")).toContainText("test_login_admin");
	});

	test("la ejecución anterior, toda en verde, se ve en verde", async ({ page }) => {
		await page.goto(`/#run=${seed().prev}&view=tests`);
		await expect(page.locator("#report-name")).toHaveText("E2E anterior");
		await page.locator('#status-filters [data-status="FAIL"]').click();
		await expect(page.locator("#test-collection .collection-empty")).toBeVisible();
		await page.locator('[data-view="release"]').first().click();
		await expect(page.locator("#view-release .rel-banner")).not.toContainText("No salir");
	});

	test("cambiar de ejecución con el selector cambia todo el reporte", async ({ page }) => {
		const { prev, run } = seed();
		await openRun(page);
		await page.locator("#run-select").selectOption(String(prev));
		await expect(page.locator("#report-name")).toHaveText("E2E anterior");
		await expect(page).toHaveURL(new RegExp(`run=${prev}`));
		await page.locator('[data-view="dashboard"]').first().click();
		await expect(page.locator("#d-fail")).toHaveText("0");
		await page.locator("#run-select").selectOption(String(run));
		await expect(page.locator("#d-fail")).toHaveText("1");
	});

	test("categorías y errores agrupan los fallos", async ({ page }) => {
		await openRun(page, "categories");
		const cats = page.locator("#category-collection .collection-item");
		await expect(cats.filter({ hasText: "login" })).toHaveCount(1);
		await expect(cats.filter({ hasText: "smoke" })).toHaveCount(1);
		await page.locator('[data-view="exceptions"]').first().click();
		const exc = page.locator("#exception-collection .collection-item");
		await expect(exc).toHaveCount(1);
		await exc.first().click();
		await expect(page.locator("#exception-detail")).toContainText("test_login_admin");
	});
});

test.describe("cada audiencia ve lo suyo", () => {
	async function escalate(page, audience, scope = "test_login_admin") {
		await page.locator('[data-view="escalate"]').first().click();
		const view = page.locator("#view-escalate");
		if (scope) await view.locator("#esc-scope").selectOption({ label: scope });
		await view.locator(`input[name="esc-aud"][value="${audience}"]`).check({ force: true });
		await view.locator("[data-esc-gen]").click();
		await expect(view.locator("#esc-card")).toBeVisible();
		return view.locator("#esc-card");
	}

	test("Negocio: sin endpoints, códigos HTTP ni cURL", async ({ page }) => {
		await openRun(page);
		const card = await escalate(page, "business");
		await expect(card.locator(".esc-aud")).toHaveText("Negocio");
		await expect(card.locator(".esc-calls, .esc-dev")).toHaveCount(0);
		await expect(card).not.toContainText("/auth/login");
		await expect(card).not.toContainText("curl");
		await expect(card).not.toContainText("HTTP 500");
		await page.locator('[data-esc-share="plain"]').click();
		const text = await page.evaluate(() => navigator.clipboard.readText());
		expect(text).not.toContain("/auth/login");
		expect(text).not.toContain("curl");
	});

	test("QA: las llamadas que fallaron, sin el detalle de desarrollo", async ({ page }) => {
		await openRun(page);
		const card = await escalate(page, "qa");
		await expect(card.locator(".esc-calls")).toContainText("/auth/login");
		await expect(card.locator(".esc-dev")).toHaveCount(0);
		await expect(card).not.toContainText("curl -X");
	});

	test("Desarrollo en inglés: el detalle técnico también se traduce", async ({ page }) => {
		await openRun(page);
		await page.locator('[data-view="escalate"]').first().click();
		await page.locator('[data-esc-lang="en"]').click();
		const card = await escalate(page, "dev");
		await expect(card).toHaveAttribute("lang", "en");
		await expect(card.locator(".esc-dev h3")).toHaveText("Technical detail");
		await expect(card).toContainText("Run it locally");
		await expect(card).toContainText("curl -X POST");
	});

	test("Escalar desde el análisis de IA llega con el test elegido", async ({ page }) => {
		await openRun(page, "ai");
		const btn = page.locator('#view-ai [data-escalate]').first();
		if (!(await btn.count())) test.skip(true, "sin fallos listados en la vista de IA");
		const id = await btn.getAttribute("data-escalate");
		await btn.click();
		await expect(page.locator("#view-escalate")).toBeVisible();
		await expect(page.locator("#esc-scope")).toHaveValue(id);
	});
});

test.describe("privacidad", () => {
	test("ningún secreto en ninguna pantalla ni en lo que se copia", async ({ page, request }) => {
		const { run, failId } = seed();
		await openRun(page);
		await page.locator("#test-collection .collection-item", { hasText: "test_login_admin" }).locator(".test-name").click();
		const detail = page.locator("#test-detail");
		for (const tab of ["steps", "timeline", "network", "console"]) {
			const t = detail.locator(`[data-tab="${tab}"]`);
			if (await t.count()) { await t.click(); await expectNoSecrets(page, `pestaña ${tab}`); }
		}
		await detail.locator('[data-tab="network"]').click();
		for (const card of await detail.locator(".net-card").all()) await card.locator("summary").click();
		await expectNoSecrets(page, "red expandida");
		await detail.locator(".net-card-error [data-mock]").first().click();
		for (const tab of await page.locator('.cf-mock [role="tab"]').all()) { await tab.click(); await expectNoSecrets(page, "mocks"); }
		await page.keyboard.press("Escape");

		for (const view of ["categories", "exceptions", "dashboard", "release", "ai", "escalate"]) {
			await page.locator(`[data-view="${view}"]`).first().click();
			await expect(page.locator(`#view-${view}`)).toBeVisible();
			await expectNoSecrets(page, `vista ${view}`);
		}
		await page.locator("#esc-scope").selectOption({ label: "test_login_admin" });
		await page.locator('input[name="esc-aud"][value="dev"]').check({ force: true });
		await page.locator("[data-esc-gen]").click();
		await expect(page.locator("#esc-card .esc-dev")).toBeVisible();
		await expectNoSecrets(page, "escalar para Desarrollo");
		for (const fmt of ["plain", "slack", "md"]) {
			await page.locator(`[data-esc-share="${fmt}"]`).click();
			const text = await page.evaluate(() => navigator.clipboard.readText());
			for (const s of SECRETS) expect(text, `copiar ${fmt}`).not.toContain(s);
		}

		// lo que entrega la API a la interfaz tampoco los trae
		for (const url of [`/api/v1/tests/${failId}/network`, `/api/v1/tests/${failId}`, `/api/v1/runs/${run}`]) {
			const body = await (await request.get(url)).text();
			for (const s of SECRETS) expect(body, url).not.toContain(s);
		}
	});
});

test.describe("idioma", () => {
	test("elegir inglés en Ajustes traduce la interfaz y se recuerda al volver", async ({ page }) => {
		await openRun(page, "settings");
		await page.locator('[data-set-lang="en"]').click();
		await expect(page.locator('[data-view="tests"]').first()).toContainText("Tests");
		await expect(page.locator('[data-view="settings"]').first()).toContainText("Settings");
		await page.reload();
		await expect(page.locator('[data-view="categories"]').first()).toContainText("Categories");
		await page.locator('[data-view="dashboard"]').first().click();
		await expect(page.locator("#compare-card .cmp-chip.new_failures")).toHaveText("1 new failure");
	});
});
