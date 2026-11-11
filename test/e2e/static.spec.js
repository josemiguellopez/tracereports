// El reporte estático (tracereports report) abierto como lo abre el usuario: doble clic, file://,
// sin servidor ni internet. Debe dibujarse sin errores de JavaScript ni pedidos de red.
const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { test, expect } = require("@playwright/test");

const ROOT = path.join(__dirname, "..", "..");
const out = fs.mkdtempSync(path.join(os.tmpdir(), "tracereports-static-"));

/** Arma un reporte con el binario (go run) y devuelve la URL file:// de su index.html. */
function report(name, ...inputs) {
	const dir = path.join(out, name);
	execFileSync("go", ["run", "./cmd", "report", "-o", dir, ...inputs], { cwd: ROOT, stdio: "pipe", timeout: 180_000 });
	return pathToFileURL(path.join(dir, "index.html")).href;
}

/** Una grabación sin servidor mínima, como la que escriben los clientes. */
function recording() {
	const dir = path.join(out, "rec");
	fs.mkdirSync(dir, { recursive: true });
	fs.writeFileSync(path.join(dir, "tracereports-offline.json"), JSON.stringify({ format: "tracereports-offline", version: 1, id: "static-e2e" }));
	const ev = (seq, method, p, body, extra = {}) => JSON.stringify({ seq, ts: 1790000000000 + seq * 100, method, path: p, content_type: "application/json", body, ...extra });
	fs.writeFileSync(path.join(dir, "events-1.jsonl"), [
		ev(1, "POST", "/api/v1/runs", { name: "Grabación sin servidor" }, { local_id: -1 }),
		ev(2, "POST", "/api/v1/runs/-1/tests", { name: "test_offline_checkout" }, { local_id: -2 }),
		ev(3, "POST", "/api/v1/tests/-2/logs", { status: "INFO", message: "abrir checkout" }),
		ev(4, "POST", "/api/v1/tests/-2/network", { connections: [{ method: "POST", url: "https://api.example.com/pay", status: 500,
			request_headers: { Authorization: "Bearer STATIC-SECRET" }, response_body: "{\"error\":\"db\"}" }] }),
		ev(5, "PATCH", "/api/v1/tests/-2/finish", { status: "FAIL", error_message: "pago rechazado" }),
		ev(6, "PATCH", "/api/v1/runs/-1/finish", {}),
	].join("\n") + "\n");
	return dir;
}

test.beforeEach(async ({ page }, info) => {
	const errors = [];
	info.errors_ = errors;
	page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") errors.push(`console.error: ${m.text()}`); });
	// sin servidor: ningún pedido debe salir a la red
	page.on("request", (r) => { if (!r.url().startsWith("file:") && !r.url().startsWith("data:")) errors.push(`network: ${r.url()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "the static report must not have errors").toEqual([]);
});

test("Allure: pasos, captura y fallos en el reporte estático", async ({ page }) => {
	await page.goto(report("allure", path.join(ROOT, "internal", "allure", "testdata", "allure-results")));
	await expect(page.locator("#report-name")).toHaveText("Nightly #42");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(4);
	await page.locator("#test-collection .collection-item", { hasText: "test_pay_with_card" }).click();
	const detail = page.locator("#test-detail");
	await expect(detail).toContainText("Pagar: 500 en /api/pay");
	await expect(detail).toContainText("AssertionError: expected 200, got 500");
	// la captura del paso viaja dentro del reporte y se ve
	const img = detail.locator('img[src*="screenshots/"]').first();
	await expect(img).toBeVisible();
	expect(await img.evaluate((i) => i.naturalWidth)).toBeGreaterThan(0);
	// con el diagnóstico del motor local, Errores agrupa por categoría: el ConnectionError es de infraestructura
	await page.locator('[data-view="exceptions"]').first().click();
	await page.locator("#exception-collection .collection-item", { hasText: "INFRA_ERROR" }).click();
	await expect(page.locator("#view-exceptions")).toContainText("ConnectionError");
});

test("JUnit: el reporte estático equivale a allure generate", async ({ page }) => {
	await page.goto(report("junit", "-name", "JUnit estático", path.join(ROOT, "internal", "junit", "testdata", "surefire.xml")));
	await expect(page.locator("#report-name")).toHaveText("JUnit estático");
	await expect(page.locator("#test-collection .collection-item")).toHaveCount(3);
	await page.locator("#test-collection .collection-item", { hasText: "loginBroken" }).click();
	await expect(page.locator("#test-detail")).toContainText("expected: <200> but was: <500>");
});

test("grabación sin servidor: red enmascarada y cURL en el reporte estático", async ({ page }) => {
	await page.goto(report("offline", recording()));
	await expect(page.locator("#report-name")).toHaveText("Grabación sin servidor");
	await page.locator("#test-collection .collection-item", { hasText: "test_offline_checkout" }).click();
	const detail = page.locator("#test-detail");
	await expect(detail).toContainText("pago rechazado");
	await detail.locator('[data-tab="network"]').click();
	await expect(detail.locator(".net-card-error")).toHaveCount(1);
	await expect(detail).not.toContainText("STATIC-SECRET");
});

test("grabación sin servidor: Escalar y Release también en el reporte estático", async ({ page }) => {
	await page.goto(report("offline-escalate", recording()));
	await expect(page.locator("#report-name")).toHaveText("Grabación sin servidor");
	// Escalar: el resumen ya viene armado (plantilla), sin servidor ni red
	await page.locator('[data-view="escalate"]').first().click();
	const card = page.locator("#esc-card");
	await expect(card).toBeVisible();
	await expect(page.locator("[data-esc-gen]")).toHaveCount(0); // nada que generar: ya está
	await expect(page.locator('[data-esc-share="teams"], [data-esc-ticket]')).toHaveCount(0); // enviar necesita el servidor
	await page.locator("#esc-scope").selectOption({ label: "test_offline_checkout" });
	await expect(card).toContainText("test_offline_checkout");
	await page.locator('input[name="esc-aud"][value="dev"]').check({ force: true });
	await page.locator('[data-esc-lang="en"]').click();
	await expect(card).toContainText("pago rechazado");
	await expect(card).not.toContainText("STATIC-SECRET");
	// Release: la decisión de esta ejecución (falló un test)
	await page.locator('[data-view="release"]').first().click();
	await expect(page.locator("#view-release")).toContainText(/No salir todavía|Do not ship yet|Se puede salir, con riesgos|Can ship, with risks/);
});

test.afterAll(() => fs.rmSync(out, { recursive: true, force: true }));

// El reporte exportado trae las páginas del Inspector (antes no traía ninguna y el Inspector no aparecía):
// se navega sin servidor, con el probador de XPath/CSS, y Comparar con el último PASS explica por qué no está.
test("Inspector en el reporte estático: sus páginas, el probador y el aviso de comparar", async ({ page }) => {
	const snap = JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", "ficha_modal.snapshot.json"), "utf8"));
	const dir = path.join(out, "rec-dom");
	fs.mkdirSync(dir, { recursive: true });
	fs.writeFileSync(path.join(dir, "tracereports-offline.json"), JSON.stringify({ format: "tracereports-offline", version: 1, id: "static-dom" }));
	const ev = (seq, method, p, body, extra = {}) => JSON.stringify({ seq, ts: 1790000000000 + seq * 100, method, path: p, content_type: "application/json", body, ...extra });
	fs.writeFileSync(path.join(dir, "events-1.jsonl"), [
		ev(1, "POST", "/api/v1/runs", { name: "Ficha sin servidor" }, { local_id: -1 }),
		ev(2, "POST", "/api/v1/runs/-1/tests", { name: "test_ficha_modal" }, { local_id: -2 }),
		ev(3, "POST", "/api/v1/tests/-2/dom", { ...snap, kind: "step", label: "Abrir la ficha" }),
		ev(4, "POST", "/api/v1/tests/-2/dom", { ...snap, kind: "fail" }),
		ev(5, "PATCH", "/api/v1/tests/-2/finish", { status: "FAIL", error_message: 'waiting for locator("#entendido-viejo")' }),
		ev(6, "PATCH", "/api/v1/runs/-1/finish", {}),
	].join("\n") + "\n");
	await page.setViewportSize({ width: 1800, height: 1100 });
	await page.goto(report("offline-dom", dir));
	await page.locator("#test-collection .collection-item", { hasText: "test_ficha_modal" }).click();
	await page.locator('#test-detail [data-tab="inspector"]').click();
	const insp = page.locator("#inspector-panel .insp");
	await insp.locator('[data-insp="instant"]').click();
	await expect(insp.locator(".insp-status")).toHaveText(/elementos reconstruidos/);
	await expect(insp.locator(".insp-steps li")).toHaveCount(2);
	await expect(insp.locator(".insp-n", { hasText: /^Entendido$/ })).toHaveCount(1);
	// el probador funciona sobre la página del ZIP
	await insp.locator(".insp-probe").fill("//button[@id='entendido']");
	await expect(insp.locator(".insp-probeout")).toContainText("1 coincidencia: único");
	// la página del paso también se abre desde el ZIP
	await insp.locator(".insp-steps li").first().click();
	await expect(insp.locator(".insp-n", { hasText: /^Historial de pedidos$/ })).toHaveCount(1);
	// comparar necesita el servidor: deshabilitado, con su explicación
	await expect(insp.getByRole("button", { name: /Comparar con el último PASS/ })).toBeDisabled();
	await expect(insp.locator(".insp-nocompare")).toContainText("necesita el servidor");
});
