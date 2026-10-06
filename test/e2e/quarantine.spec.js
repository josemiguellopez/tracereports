// Cuarentena desde el detalle del test: el fallo se sigue viendo, la ejecución deja de estar en
// rojo, y al quitarla vuelve. Deja los datos como estaban para los demás tests.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);

test("poner y quitar la cuarentena de un test que falla", async ({ page, request }) => {
	const errors = [];
	page.on("pageerror", (e) => errors.push(e.message));
	page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
	const { run, failId } = seed();
	const runStatus = async () => (await (await request.get(`/api/v1/runs/${run}`)).json());

	await page.goto(`/#run=${run}&view=tests&test=${failId}`);
	const detail = page.locator("#test-detail");
	await expect(detail).toContainText("test_login_admin");
	await detail.locator("[data-quar-open]").click();
	const form = detail.locator("[data-quar-form]");
	await form.locator('input[name="reason"]').fill("timeout intermitente del login (SHOP-34)");
	await form.locator('input[name="owner"]').fill("Equipo auth");
	await form.locator('select[name="days"]').selectOption("7");
	await form.locator('button[type="submit"]').click();

	await expect(detail.locator(".quar-chip")).toHaveText("En cuarentena");
	await expect(page.locator("#test-collection .collection-item", { hasText: "test_login_admin" }).locator(".quar-chip")).toBeVisible();
	let r = await runStatus();
	expect(r.status).toBe("WARNING");
	expect(r.quarantined).toBe(1);
	expect(r.failed).toBe(1); // el fallo se sigue contando

	await detail.locator("[data-quar-remove]").click();
	await expect(detail.locator(".quar-chip")).toHaveCount(0);
	r = await runStatus();
	expect(r.status).toBe("FAIL");
	expect(r.quarantined).toBe(0);
	expect(errors).toEqual([]);
});

test("clasificar un fallo: veredicto, comentario y autor", async ({ page, request }) => {
	const errors = [];
	page.on("pageerror", (e) => errors.push(e.message));
	const { run, failId } = seed();
	await page.goto(`/#run=${run}&view=tests&test=${failId}`);
	const detail = page.locator("#test-detail");
	await detail.locator("[data-verdict-edit]").click();
	const form = detail.locator("[data-verdict-form]");
	await form.locator('input[value="product_bug"]').check({ force: true });
	await form.locator('textarea[name="comment"]').fill("el login devuelve 500 desde el deploy de las 9:00");
	await form.locator('input[name="author"]').fill("Ana QA");
	await form.locator('button[type="submit"]').click();

	const block = detail.locator(".verdict-block");
	await expect(block.locator(".verdict-chip")).toHaveText("Bug de producto");
	await expect(block).toContainText("el login devuelve 500");
	await expect(block).toContainText("Ana QA");
	await expect(page.locator("#test-collection .collection-item", { hasText: "test_login_admin" }).locator(".verdict-chip")).toHaveText("Bug de producto");
	const t = await (await request.get(`/api/v1/tests/${failId}`)).json();
	expect(t.verdict.verdict).toBe("product_bug");
	// el autor se recuerda para la próxima clasificación
	await detail.locator("[data-verdict-edit]").click();
	await expect(detail.locator('[data-verdict-form] input[name="author"]')).toHaveValue("Ana QA");
	await detail.locator("[data-verdict-cancel]").click();
	expect(errors).toEqual([]);
});
