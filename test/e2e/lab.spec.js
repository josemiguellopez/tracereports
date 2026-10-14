// FixLab: el Request Lab reenvía la llamada que falló contra un backend falso (login JWT). Con la
// contraseña arma una sesión nueva desde el login grabado, muestra "Se reproduce" mientras el
// backend sigue roto y "Ya no falla" cuando se arregla; nunca muestra la contraseña ni el token.
const { test, expect } = require("@playwright/test");
const { LAB } = require("./seed.js");

// el backend falso y la ejecución grabada contra él: playwright.config.js y seed.js
const PASSWORD = LAB.password;
const base = process.env.E2E_FAKE_LAB;
const runId = () => JSON.parse(process.env.E2E_SEED).labRun;
const setBroken = async (request, on) => { await request.get(`${base}/__broken/${on ? "on" : "off"}`); };

test.beforeEach(async ({ page }, info) => {
	const errors = [];
	info.errors_ = errors;
	page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") errors.push(`console.error: ${m.text()}`); });
	page.on("response", (r) => { if (r.status() >= 500) errors.push(`${r.status()} ${r.url()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

async function openNetwork(page) {
	await page.goto(`/#run=${runId()}&view=tests`);
	await page.locator("#test-collection .collection-item", { hasText: "ver pedidos abiertos" }).locator(".test-name").click();
	const detail = page.locator("#test-detail");
	await detail.locator('[data-tab="network"]').click();
	return detail;
}

test("Lab: reproduce el fallo, pide la contraseña y confirma el arreglo", async ({ page, request }) => {
	await setBroken(request, true);
	const detail = await openNetwork(page);
	await detail.locator(".net-card-error [data-lab]").click();

	const lab = page.locator(".cf-drawer .lab");
	await expect(page.locator("#cf-drawer-title")).toHaveText("Lab: reenviar la llamada");
	await expect(lab.locator(".lab-flow")).toContainText("Repite el login grabado");
	await expect(lab.locator(".lab-flow")).toContainText("token de acceso (Bearer)");
	await expect(lab.locator('[data-lab-auto="authorization"]')).toContainText("automático: sesión nueva");
	await expect(lab.locator('[data-lab-var="password"]')).toHaveAttribute("type", "password");

	// sin contraseña: no se envía nada y se marca el campo
	await lab.locator("[data-lab-send]").click();
	await expect(lab.locator(".lab-result")).toContainText("Falta completar: password");
	await expect(lab.locator('[data-lab-var="password"]')).toHaveClass(/lab-missing/);

	await lab.locator('[data-lab-var="password"]').fill(PASSWORD);
	await lab.locator("[data-lab-send]").click();
	await expect(lab.locator(".lab-verdict")).toHaveAttribute("data-verdict", "reproduced");
	await expect(lab.locator(".lab-verdict")).toContainText("Se reproduce");
	await expect(lab.locator(".lab-steps li")).toHaveCount(2);
	await expect(lab.locator(".lab-steps li").first()).toContainText("token: accessToken");
	await expect(lab.locator(".lab-after")).toContainText("500");
	await expect(lab.locator(".lab-after")).toContainText("connection pool exhausted");
	const shown = await page.locator(".cf-drawer").innerText();
	expect(shown).not.toContain(PASSWORD);
	expect(shown).not.toContain("fresh-e2e-signature");

	await setBroken(request, false); // el dev arregla el backend
	await lab.locator("[data-lab-send]").click();
	await expect(lab.locator(".lab-verdict")).toHaveAttribute("data-verdict", "fixed");
	await expect(lab.locator(".lab-verdict")).toContainText("Ya no falla");
	await expect(lab.locator(".lab-after")).toContainText("200");
	await expect(lab.locator(".lab-before")).toContainText("500");
});

test("Lab: editar la petición y copiar el cURL sin valores", async ({ page, request }) => {
	await setBroken(request, false);
	const detail = await openNetwork(page);
	await detail.locator(".net-card-error [data-lab]").click();
	const lab = page.locator(".cf-drawer .lab");
	await lab.locator('[data-lab-var="password"]').fill(PASSWORD);

	await lab.locator(".lab-edit summary").click();
	await lab.locator("[data-lab-url]").fill(`${base}/api/nope`);
	await lab.locator("[data-lab-send]").click();
	await expect(lab.locator(".lab-verdict")).toHaveAttribute("data-verdict", "different");
	await expect(lab.locator(".lab-after")).toContainText("404");

	await lab.locator("[data-lab-reset]").click();
	await expect(lab.locator("[data-lab-url]")).toHaveValue(`${base}/api/orders?status=open`);
	await lab.locator("[data-lab-curl]").click();
	const curl = await page.evaluate(() => navigator.clipboard.readText());
	expect(curl).toContain("Define antes: AUTHORIZATION=…");
	expect(curl).toContain(`-H 'authorization: '"$AUTHORIZATION"`);
	expect(curl).not.toContain(PASSWORD);
});

test("Lab: se abre desde el incidente de backend", async ({ page }) => {
	await page.goto(`/#run=${runId()}&view=ai`);
	const card = page.locator(".incident-card", { hasText: "/api/orders" });
	await expect(card).toBeVisible();
	await card.locator("[data-lab-conn]").click();
	await expect(page.locator("#cf-drawer-title")).toHaveText("Lab: reenviar la llamada");
	await expect(page.locator(".cf-drawer .cf-mock-meta")).toContainText("/api/orders?status=open");
	await expect(page.locator(".cf-drawer .cf-status-pill")).toHaveText("500");
});
