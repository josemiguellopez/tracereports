// Crear un ticket desde "Escalar" (contra un GitHub falso): lleva el resumen, el error, la llamada
// fallida y el link al reporte; el mismo fallo no abre un segundo ticket.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);
const fakeRequests = async (request) => (await request.get(`${process.env.E2E_FAKE_GITHUB}/__requests`)).json();

test("ticket en GitHub desde Escalar, sin duplicados", async ({ page, request }) => {
	const errors = [];
	page.on("pageerror", (e) => errors.push(e.message));
	page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
	const before = (await fakeRequests(request)).length;

	const { run, failId } = seed();
	await page.goto(`/#run=${run}&view=escalate`);
	await page.locator("#esc-scope").selectOption(String(failId));
	await page.locator('input[name="esc-aud"][value="dev"]').check({ force: true });
	await page.locator("[data-esc-gen]").click();
	const button = page.locator('[data-esc-ticket="github"]').first();
	await expect(button).toBeVisible();

	await button.click();
	const msg = page.locator(".esc-preview .set-msg");
	await expect(msg).toContainText("Ticket creado:");
	await expect(msg.locator("a")).toHaveAttribute("href", /github\.example\/acme\/shop\/issues\/\d+/);

	const sent = (await fakeRequests(request)).slice(before);
	expect(sent).toHaveLength(1);
	expect(sent[0].method).toBe("POST");
	expect(sent[0].url).toBe("/repos/acme/shop/issues");
	expect(sent[0].auth).toBe("Bearer e2e-token");
	const issue = JSON.parse(sent[0].body);
	expect(issue.title).toContain("test_login_admin");
	expect(issue.body).toContain("TimeoutError");
	expect(issue.body).toContain("api.example.com/auth/login");
	expect(issue.body).toContain(`${process.env.E2E_BASE_URL}/#run=${run}`);
	expect(issue.body).not.toContain("E2E-PASSWORD");

	// otra vez: muestra el ticket existente y no llama a GitHub
	await page.locator('.esc-share [data-esc-ticket="github"]').click();
	await expect(msg).toContainText("ya tiene un ticket");
	await expect(msg.locator("[data-force]")).toBeVisible();
	expect((await fakeRequests(request)).slice(before)).toHaveLength(1);

	// "Crear otro igual" sí crea uno nuevo
	await msg.locator("[data-force]").click();
	await expect(msg).toContainText("Ticket creado:");
	expect((await fakeRequests(request)).slice(before)).toHaveLength(2);

	const tickets = await (await request.get(`/api/v1/runs/${run}/tickets`)).json();
	expect(tickets.length).toBeGreaterThanOrEqual(2);
	expect(errors).toEqual([]);
});
