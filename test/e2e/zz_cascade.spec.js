// Fallos en cascada: el primer test de un archivo falla y los siguientes, que dependen de lo que
// debía dejar, fallan por eso. El diagnóstico de la ejecución los muestra como un solo incidente
// con la causa del primero. Datos sintéticos. Va al final (zz_): crea ejecuciones.
const { test, expect } = require("@playwright/test");

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

test("Diagnóstico: el primer fallo de un archivo y los siguientes en cascada son un incidente", async ({ page, request }) => {
	const token = `cascada-${Date.now()}`;
	const run = (await (await request.post("/api/v1/runs", { data: { name: `${token} pedidos`, environment: "qa", project: token } })).json()).run_id;
	const file = "tests/farmacia/test_pedidos.py";
	const blank = { url: "https://app/", viewport: { w: 1280, h: 800 }, elements: [{ tag: "div", visible: false }, { tag: "script", visible: false }] };
	const add = async (name, finish, dom, f = file) => {
		const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name, key: `${f}::TestPedido::${name}` } })).json()).test_id;
		if (dom) await request.post(`/api/v1/tests/${id}/dom`, { data: dom });
		await request.patch(`/api/v1/tests/${id}/finish`, { data: finish });
		return id;
	};
	const trace = (line) => `Traceback (most recent call last):\n  File "C:\\work\\${file}", line ${line}, in test\nException: x`;
	await add("test_001_crear_pedido", { status: "FAIL", error_message: "TimeoutError: la página no terminó de cargar", error_trace: trace(60) }, blank);
	for (const [i, line] of [135, 158, 181, 213].entries()) {
		await add(`test_00${i + 2}_paso`, { status: "FAIL", error_message: "Exception: Error número de pedido no puede ser vacio", error_trace: trace(line) }, blank);
	}
	await add("test_otro_archivo", { status: "PASS" }, null, "tests/test_otro.py");
	await request.patch(`/api/v1/runs/${run}/finish`, { data: {} });

	await page.goto(`/#run=${run}&view=ai`);
	const cards = page.locator(".incident-card");
	await expect(cards).toHaveCount(1);
	const card = cards.first();
	await expect(card.locator(".inc-kind")).toHaveText("En cascada");
	await expect(card.locator(".inc-count")).toHaveText("5 tests");
	await expect(card.locator("h6")).toHaveText("TimeoutError: la página no terminó de cargar");
	await expect(card.locator(".inc-evidence")).toContainText("test_001_crear_pedido falló primero; 4 test(s) siguientes");
	await expect(card.locator(".inc-evidence")).toContainText("Página en blanco al fallar");
	await expect(page.locator(".ai-headline")).toContainText("5 tests fallaron en cascada");
});
