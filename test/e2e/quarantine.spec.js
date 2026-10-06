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
