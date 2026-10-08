// Escalar con IA muestra cuánto tardó (proveedor vs TraceReports) debajo de la tarjeta, sin que
// viaje en lo que se comparte; Uso de la IA muestra el estado del proveedor (página de estado
// falsa, ver playwright.config.js) y lo observado hoy. Proveedor de IA falso que tarda 400 ms.
const http = require("node:http");
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);
const ESC = { title: "Checkout falla", severity: "high", headline: "El pago no se confirma", what_happened: "w", impact: "i",
	evidence: ["e"], root_cause: "r", next_steps: ["n"], owner: "o" };

function slowLLM(ms) {
	return new Promise((resolve) => {
		const srv = http.createServer((req, res) => {
			req.resume();
			req.on("end", () => setTimeout(() => {
				res.setHeader("Content-Type", "application/json");
				res.end(JSON.stringify({ choices: [{ message: { content: JSON.stringify(ESC) } }], usage: { prompt_tokens: 900, completion_tokens: 80 } }));
			}, ms));
		}).listen(0, "127.0.0.1", () => resolve(srv));
	});
}

test("cuánto tardó la IA al escalar, y el estado del proveedor", async ({ page, request }) => {
	const errors = [];
	page.on("pageerror", (e) => errors.push(e.message));
	page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
	const llm = await slowLLM(400);
	// un proveedor compatible con OpenAI no necesita key
	const put = await request.put("/api/v1/settings", { data: { ai: { provider: "openai_compatible", model: "fake-llm", base_url: `http://127.0.0.1:${llm.address().port}` } } });
	expect(put.ok()).toBeTruthy();
	try {
		const { run, failId } = seed();
		await page.goto(`/#run=${run}&view=escalate`);
		await page.locator("#esc-scope").selectOption(String(failId));
		await page.locator('input[name="esc-aud"][value="dev"]').check({ force: true });
		await page.locator('[data-esc-ai="1"]').click();
		await page.locator("[data-esc-gen]").click();
		const timing = page.locator(".esc-timing");
		await expect(timing).toContainText("Tardó");
		await expect(timing).toContainText("esperando al proveedor de IA (1 llamada)");
		await expect(timing).toContainText("de TraceReports");
		await expect(timing).toContainText("Casi todo el tiempo fue del proveedor de IA.");
		// fuera de la tarjeta que se comparte
		await expect(page.locator("#esc-card .esc-timing")).toHaveCount(0);
		await expect(page.locator("#esc-card")).toContainText("Resumen generado con IA (fake-llm)");

		await page.goto("/#view=settings");
		await page.locator('[data-set-tab="usage"]').click();
		const status = page.locator(".prov-status");
		await expect(status).toContainText("Degradación parcial");
		await expect(status).toContainText("Elevated latency");
		await expect(status).toContainText("Observado por TraceReports hoy");
		await expect(status.locator('a[href="https://status.example/i/1"]')).toHaveCount(1);
		await expect(page.locator("#set-panel table").first()).toContainText("openai_compatible · fake-llm");
	} finally {
		await request.delete("/api/v1/settings/ai"); // vuelve a la IA del entorno (desactivada)
		llm.close();
	}
	expect(errors).toEqual([]);
});
