// Ajustes → Uso de la IA: una prueba de conexión contra un proveedor falso (OpenAI) se cuenta, con
// los tokens que él informa, y la sección lo muestra (también en inglés).
const http = require("node:http");
const { test, expect } = require("@playwright/test");

function fakeOpenAI() {
	return new Promise((resolve) => {
		const srv = http.createServer((req, res) => {
			req.resume();
			req.on("end", () => {
				res.setHeader("Content-Type", "application/json");
				res.end(JSON.stringify({ choices: [{ message: { content: "{\"ok\":true}" } }], usage: { prompt_tokens: 1234, completion_tokens: 56 } }));
			});
		}).listen(0, "127.0.0.1", () => resolve(srv));
	});
}

test("uso de la IA: llamadas y tokens que informó el proveedor", async ({ page, request }) => {
	const errors = [];
	page.on("pageerror", (e) => errors.push(e.message));
	page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
	const before = (await (await request.get("/api/v1/settings/ai/usage")).json()).usage.today;

	const llm = await fakeOpenAI();
	try {
		const res = await request.post("/api/v1/settings/ai/test", {
			data: { provider: "openai", api_key: "fake-e2e-key-123456", base_url: `http://127.0.0.1:${llm.address().port}`, model: "gpt-e2e" },
		});
		expect((await res.json()).ok).toBe(true);
	} finally {
		llm.close();
	}

	await page.goto("/#view=settings");
	await page.locator('[data-set-tab="usage"]').click();
	const card = page.locator("#set-panel");
	await expect(card.locator("h5")).toHaveText("Uso de la IA");
	const today = card.locator(".kpi").first();
	await expect(today.locator(".kpi-value")).toHaveText(String(before.calls + 1));
	await expect(today).toContainText(`${(before.input_tokens + 1234).toLocaleString("es-CL")} de entrada`);
	await expect(card.locator("table").first()).toContainText("openai · gpt-e2e");
	await expect(card.locator("table").nth(1)).toContainText("Probar conexión");
	await expect(page.locator('[data-set-tab="usage"] .set-nav-sub')).toContainText("llamadas hoy");

	// en inglés
	await page.locator('[data-set-tab="look"]').click();
	await page.locator('[data-set-lang="en"]').click();
	await page.locator('[data-set-tab="usage"]').click();
	await expect(card.locator("h5")).toHaveText("AI usage");
	await expect(card.locator("table").nth(1)).toContainText("Test connection");
	await page.locator('[data-set-tab="look"]').click();
	await page.locator('[data-set-lang="es"]').click();
	expect(errors).toEqual([]);
});
