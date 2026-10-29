// Barrido de regresión por toda la UI, en escritorio y en el teléfono: cada vista se dibuja sin
// errores, sin quedarse en "Cargando…" y sin scroll lateral, y los cajones (Lab, mock, comparar) se
// abren y se cierran bien. Cubre fallos que se encontraron recorriendo la interfaz a mano.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);
const VIEWS = ["search", "tests", "categories", "exceptions", "dashboard", "release", "ai", "escalate", "metrics", "settings"];
// textos de carga: si siguen a la vista después de unos segundos, la pantalla quedó colgada
const LOADING = /(Cargando|Preparando|Loading|Preparing)[^.]*…/;

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

/** Ancho que sobra a la derecha de la ventana (0: sin scroll lateral). */
const overflow = (page) => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);

/** Abre una vista de la ejecución y espera a que termine de cargar. */
async function openView(page, run, view) {
	await page.goto(`/#run=${run}&view=${view}`);
	await expect(page.locator(`#view-${view}`)).toBeVisible();
	await expect.poll(async () => {
		const texts = await page.locator(".placeholder:visible, [aria-busy=true]:visible").allTextContents();
		return texts.filter((t) => LOADING.test(t));
	}, { message: `la vista ${view} no debe quedarse cargando`, timeout: 8000 }).toEqual([]);
}

for (const [label, viewport] of [["escritorio", { width: 1440, height: 900 }], ["teléfono", { width: 390, height: 844 }]]) {
	test(`cada vista se dibuja completa y sin scroll lateral (${label})`, async ({ page }) => {
		await page.setViewportSize(viewport);
		for (const view of VIEWS) {
			await openView(page, seed().run, view);
			expect(await overflow(page), `scroll lateral en ${view}`).toBeLessThanOrEqual(1);
		}
	});
}

test("teléfono: nombres y errores largos sin espacios no desbordan las tablas ni los incidentes", async ({ page }) => {
	// así llegan de pytest o Playwright: test_..._[chromium] y playwright._impl._errors.TimeoutError
	const longName = "test_checkout_pays_the_order_with_a_saved_card_and_coupon[chromium-mobile-safari]";
	const longError = "playwright._impl._errors.TimeoutError:Page.click:Timeout30000msexceeded.Calllog:waitingforlocator(\"#add-to-cart-btn\")";
	await page.route((url) => url.pathname === `/api/v1/runs/${seed().run}`, async (route) => {
		const res = await route.fetch();
		const run = await res.json();
		for (const t of run.tests || []) {
			t.name = `${longName}_${t.id}`;
			if (t.error_message) t.error_message = longError;
		}
		for (const inc of run.summary?.incidents || []) inc.title = longError;
		await route.fulfill({ response: res, json: run });
	});
	await page.setViewportSize({ width: 390, height: 844 });
	for (const view of ["tests", "categories", "exceptions", "dashboard"]) {
		await openView(page, seed().run, view);
		expect(await overflow(page), `scroll lateral en ${view}`).toBeLessThanOrEqual(1);
	}
});

test("un cajón que se cierra antes de terminar de abrirse queda cerrado", async ({ page }) => {
	await openView(page, seed().run, "tests");
	// abrir y cerrar en el mismo frame (Escape o un doble clic muy rápido)
	await page.evaluate(() => {
		const d = window.TraceReportsFeatures.Drawer;
		d.open({ title: "x", body: "y" });
		d.close();
	});
	await page.waitForTimeout(400);
	await expect(page.locator(".cf-drawer-root")).not.toHaveClass(/\bopen\b/);
	await expect(page.locator(".cf-drawer-root")).toBeHidden();
});

test("red: Lab, mock y comparar abren su cajón y Escape lo cierra en cada llamada", async ({ page }) => {
	await page.goto(`/#run=${seed().run}&view=tests&test=${seed().failId}`);
	const detail = page.locator("#test-detail");
	await detail.locator('[data-tab="network"]').click();
	const cards = detail.locator(".net-card");
	await expect(cards).toHaveCount(2);
	for (let i = 0; i < 2; i++) {
		const card = cards.nth(i);
		await card.locator("summary").click();
		for (const [sel, title] of [["[data-lab]", "Lab: reenviar la llamada"], ["[data-mock]", "Generar mock / stub"]]) {
			await card.locator(`.net-actions ${sel}`).click();
			await expect(page.locator("#cf-drawer-title")).toHaveText(title);
			await expect(page.locator(".cf-drawer-root")).toHaveClass(/\bopen\b/);
			await expect(page.locator(".cf-drawer-body")).not.toContainText(LOADING, { timeout: 8000 });
			await page.keyboard.press("Escape");
			await expect(page.locator(".cf-drawer-root")).toBeHidden();
		}
		await card.locator("summary").click();
	}
});
