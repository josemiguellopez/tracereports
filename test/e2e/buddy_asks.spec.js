// Las preguntas sugeridas de Trace Buddy corren su acción y nunca le llegan al modelo como texto: con
// dos funciones buddyAsk, "¿Está segura mi instalación?" le mandaba "security" al modelo, que contestaba
// "¡Listo!" o "tu instalación parece segura" sin revisar nada (lo vio el cliente con llama3.1:8b).
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("las preguntas sugeridas corren su acción, no le preguntan al modelo", async ({ page }) => {
	// una IA configurada (el modelo real no corre en el e2e) que responde al chequeo de conexión
	await page.route("**/api/v1/config", async (route) => {
		const res = await route.fetch();
		await route.fulfill({ response: res, json: { ...(await res.json()), ai_enabled: true, ai_provider: "ollama", ai_model: "llama3.1:8b" } });
	});
	await page.route("**/api/v1/ui/ai/probe*", (route) => route.fulfill({ json: { ok: true, provider: "ollama", model: "llama3.1:8b", ms: 1200 } }));
	const chat = [];
	await page.route("**/api/v1/ui/ai/chat", async (route) => {
		chat.push(route.request().postDataJSON());
		await route.fulfill({ json: { answer: "¡Listo!", provider: "ollama", model: "llama3.1:8b" } });
	});
	await page.goto(`/#run=${seed().run}&view=tests`);
	await page.locator("#buddy-fab").click();
	const asks = page.locator("#buddy-asks");
	await expect(asks).toBeVisible();

	// ¿Está segura mi instalación? → la revisión de seguridad del servidor, con su enlace
	await asks.locator('[data-buddy-ask="security"]').click();
	const log = page.locator("#buddy-log");
	await expect(log.locator('a[data-set-tab="security"]').last()).toBeVisible();
	await expect(log).not.toContainText("¡Listo!");

	// ¿Funciona la IA? → el chequeo de conexión
	await asks.locator('[data-buddy-ask="probe"]').click();
	await expect(log).toContainText("llama3.1:8b");

	// ¿Cómo conecto mis tests? → la guía, sin el modelo
	await asks.locator('[data-buddy-ask="connect"]').click();
	await expect(log).toContainText("¿Cómo conecto mis tests?");

	expect(chat.map((c) => c.messages.at(-1).text), "ninguna sugerencia le llegó al modelo").toEqual([]);
});
