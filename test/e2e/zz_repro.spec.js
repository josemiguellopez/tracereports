// Reproducir en local: el comando de este test y el de su archivo completo, sin git checkout dentro
// del comando (el commit de la ejecución va como nota). Si el error dice que el test dependía de uno
// anterior (dato vacío, paso omitido), el archivo completo va primero: corrido solo, ese test falla
// por otra razón. Un caso real: test_002 necesita el pedido que crea test_001.
// Va al final (zz_): crea una ejecución, y los specs anteriores cuentan con los IDs del seed.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

const FILE = "tests/shop/checkout/test_pay_with_saved_card.py";
const KEY = (name) => `${FILE}::TestCheckout::${name}`;
let run, root, dependent;

test.beforeAll(async ({ request }) => {
	run = (await (await request.post("/api/v1/runs", { data: { name: `repro-${Date.now()}`, environment: "preproduccion",
		framework: "pytest", commit: "fd051753d33f9a1b2c3d" } })).json()).run_id;
	const add = async (name, error) => {
		const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name, key: KEY(name) } })).json()).test_id;
		await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL", error_message: error } });
		return id;
	};
	root = await add("test_001_create_order_otro_nodo", "AssertionError: no guarda prescripción");
	dependent = await add("test_002_pay_with_saved_card", "Exception: Error número de pedido no puede ser vacio");
	await request.patch(`/api/v1/runs/${run}/finish`, { data: {} });
});

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

async function reproOf(page, testId) {
	await page.goto(`/#run=${run}&view=tests&test=${testId}`);
	const repro = page.locator("#test-detail details.repro");
	await repro.locator("summary").click();
	return repro;
}

test("vista del test: este test y su archivo, el commit solo como nota", async ({ page }) => {
	const repro = await reproOf(page, root);
	const rows = repro.locator(".repro-row");
	await expect(rows).toHaveCount(2);
	await expect(rows.nth(0)).toHaveAttribute("data-scope", "test");
	await expect(rows.nth(0).locator("code")).toHaveText(`pytest '${KEY("test_001_create_order_otro_nodo")}'`);
	await expect(rows.nth(0).locator(".repro-lbl")).toContainText("Este test");
	await expect(rows.nth(1)).toHaveAttribute("data-scope", "file");
	await expect(rows.nth(1).locator("code")).toHaveText(`pytest '${FILE}'`);
	await expect(rows.nth(1).locator(".repro-lbl")).toContainText("Todo el archivo");
	await expect(repro.locator("code", { hasText: "git" })).toHaveCount(0);
	await expect(repro.locator(".repro-note")).toHaveText("La ejecución usó el commit fd051753d33f; si tu copia está en otra versión: git checkout fd051753d33f");
});

test("un test que dependía del anterior: primero el archivo completo", async ({ page }) => {
	const rows = (await reproOf(page, dependent)).locator(".repro-row");
	await expect(rows.nth(0)).toHaveAttribute("data-scope", "file");
	await expect(rows.nth(0).locator("code")).toHaveText(`pytest '${FILE}'`);
	await expect(rows.nth(1)).toHaveAttribute("data-scope", "test");
	await expect(page.locator("#test-detail .repro-hint")).toContainText("Corre el archivo completo cuando el test depende de otros anteriores");
});

test("Escalar para Desarrollo: el mismo orden, como un bloque con comentarios y sin checkout encadenado", async ({ page }) => {
	await page.goto(`/#run=${run}&view=escalate`);
	const view = page.locator("#view-escalate");
	await view.locator("#esc-scope").selectOption(String(dependent));
	await view.locator('input[name="esc-aud"][value="dev"]').check({ force: true });
	const noAI = view.locator('[data-esc-ai="0"]');
	if (await noAI.count()) await noAI.click();
	const dev = view.locator("#esc-card .esc-dev");
	await expect(async () => {
		await view.locator("[data-esc-gen]").click();
		await expect(dev).toBeVisible({ timeout: 3000 });
	}).toPass({ timeout: 20_000 });
	const block = dev.locator("pre", { hasText: "Todo el archivo" });
	const text = await block.textContent();
	expect(text).toBe(
		`# Todo el archivo (si el test depende de otros anteriores del mismo archivo) · pytest\npytest '${FILE}'\n\n` +
		`# Este test · pytest\npytest '${KEY("test_002_pay_with_saved_card")}'\n\n` +
		"# La ejecución usó el commit fd051753d33f; si tu copia está en otra versión: git checkout fd051753d33f");
	expect(text).not.toContain("&&");
});
