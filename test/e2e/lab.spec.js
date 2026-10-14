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

test("Lab: guardar un check y verificar el incidente (sigue fallando → resuelto)", async ({ page, request }) => {
	await setBroken(request, true);
	const detail = await openNetwork(page);
	await detail.locator(".net-card-error [data-lab]").click();
	const lab = page.locator(".cf-drawer .lab");
	await lab.locator('[data-lab-var="password"]').fill(PASSWORD);
	await lab.locator("[data-lab-send]").click();
	await expect(lab.locator(".lab-verdict")).toHaveAttribute("data-verdict", "reproduced");

	await lab.locator(".lab-save summary").click();
	await expect(lab.locator("[data-check-status]")).toHaveValue("200");
	await lab.locator("[data-check-name]").fill("los pedidos cargan");
	await lab.locator("[data-check-contains]").fill("orders");
	await lab.locator("[data-check-save]").click();
	await expect(lab.locator("[data-check-msg]")).toContainText("Check guardado");
	await page.keyboard.press("Escape");

	await page.goto(`/#run=${runId()}&view=ai`);
	const card = page.locator(".incident-card", { hasText: "/api/orders" });
	await card.locator("[data-lab-verify]").click();
	const drawer = page.locator(".cf-drawer .lab-verify");
	await expect(page.locator("#cf-drawer-title")).toHaveText("Verificar el arreglo");
	await expect(drawer.locator("[data-check]")).toContainText("los pedidos cargan");
	await expect(drawer.locator("[data-check]")).toContainText("status 200 · contiene «orders»");

	await drawer.locator("[data-verify-run]").click();
	await expect(drawer.locator(".lab-result")).toContainText("Falta completar: password");
	await drawer.locator('[data-lab-var="password"]').fill(PASSWORD);
	await drawer.locator("[data-verify-run]").click();
	await expect(drawer.locator("[data-verified]")).toHaveAttribute("data-verified", "fail");
	await expect(drawer.locator(".lab-result")).toContainText("status 500 (se esperaba 200)");
	await expect(card.locator(".lab-verified.bad")).toContainText("Verificado: sigue fallando");

	await setBroken(request, false); // el dev arregla el backend
	await drawer.locator("[data-verify-run]").click();
	await expect(drawer.locator("[data-verified]")).toHaveAttribute("data-verified", "ok");
	await expect(drawer.locator(".lab-result")).toContainText("Resuelto y verificado");
	expect(await page.locator(".cf-drawer").innerText()).not.toContain(PASSWORD);
	await page.keyboard.press("Escape");
	await expect(card.locator(".lab-verified.ok")).toContainText("Verificado: resuelto");

	// el estado queda guardado: también después de recargar
	await page.reload();
	await expect(page.locator(".incident-card", { hasText: "/api/orders" }).locator(".lab-verified.ok")).toBeVisible();
});

// La cápsula y el comando copiados desde el reporte funcionan de verdad en la terminal.
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const cli = (args, password) => {
	try {
		return { code: 0, out: execFileSync("go", ["run", "./cmd", ...args], { cwd: path.join(__dirname, "..", ".."), encoding: "utf8",
			env: { ...process.env, TRACEREPORTS_LAB_PASSWORD: password }, timeout: 120_000, stdio: ["ignore", "pipe", "pipe"] }) };
	} catch (e) { return { code: e.status, out: `${e.stdout}${e.stderr}` }; }
};
const shellWords = (cmd) => [...cmd.matchAll(/'((?:[^']|'\'')*)'|(\S+)/g)].map((m) => (m[1] !== undefined ? m[1].replace(/'\''/g, "'") : m[2]));

test("Lab: la cápsula y el comando copiado reproducen el fallo en la terminal", async ({ page, request }) => {
	test.setTimeout(180_000);
	await setBroken(request, true);
	const detail = await openNetwork(page);
	await detail.locator(".net-card-error [data-lab]").click();
	const lab = page.locator(".cf-drawer .lab");
	const href = await lab.locator(".lab-capsule a[download]").getAttribute("href");
	const capsule = await (await request.get(href)).json();
	expect(capsule.format).toBe("tracereports-capsule");
	expect(JSON.stringify(capsule)).not.toContain(PASSWORD);

	await lab.locator("[data-lab-cmd]").click();
	const cmd = await page.evaluate(() => navigator.clipboard.readText());
	expect(cmd).toMatch(/^tracereports repro 'http:\/\/localhost:\d+\/api\/v1\/network\/\d+\/capsule'$/);
	const [, verb, url] = shellWords(cmd);
	const res = cli([verb, url], PASSWORD);
	expect(res.code, res.out).toBe(0);
	expect(res.out).toContain("REPRODUCED");
	expect(res.out).not.toContain(PASSWORD);

	// verify: con el check guardado en el test anterior, sale con 1 mientras sigue roto y 0 al arreglarlo
	await page.keyboard.press("Escape");
	await page.goto(`/#run=${runId()}&view=ai`);
	await page.locator(".incident-card", { hasText: "/api/orders" }).locator("[data-lab-verify]").click();
	await page.locator("[data-verify-cmd]").click();
	const vcmd = await page.evaluate(() => navigator.clipboard.readText());
	const [, vverb, vurl] = shellWords(vcmd);
	expect(vverb).toBe("verify");
	let v = cli([vverb, vurl], PASSWORD);
	expect(v.code, v.out).toBe(1);
	expect(v.out).toContain("Still failing");
	await setBroken(request, false);
	v = cli([vverb, vurl], PASSWORD);
	expect(v.code, v.out).toBe(0);
	expect(v.out).toContain("Fixed and verified");
});

test("Lab: exportar a Postman y .http descarga archivos válidos sin valores", async ({ page }) => {
	const detail = await openNetwork(page);
	await detail.locator(".net-card-error [data-lab]").click();
	const lab = page.locator(".cf-drawer .lab");
	await lab.locator('[data-lab-var="password"]').fill(PASSWORD);

	const read = async (id) => {
		const [dl] = await Promise.all([page.waitForEvent("download"), lab.locator(`[data-lab-export="${id}"]`).click()]);
		const fs = require("node:fs");
		return { name: dl.suggestedFilename(), text: fs.readFileSync(await dl.path(), "utf8") };
	};
	const pm = await read("postman");
	expect(pm.name).toBe("get-api-orders-status-open.postman_collection.json");
	const col = JSON.parse(pm.text);
	expect(col.item.map((i) => i.request.method)).toEqual(["POST", "GET"]);
	expect(col.item[0].event[0].script.exec[0]).toContain('pm.collectionVariables.set("token"');
	expect(pm.text).not.toContain(PASSWORD);

	const http = await read("http");
	expect(http.name).toBe("get-api-orders-status-open.http");
	expect(http.text).toContain("# @name login");
	expect(http.text).toContain("authorization: Bearer {{login.response.body.$.accessToken}}");
	expect(http.text).not.toContain(PASSWORD);

	const bru = await read("bruno");
	expect(bru.name).toBe("get-api-orders-status-open.bru");
	expect(bru.text).toMatch(/^meta \{/);
});
