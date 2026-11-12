// «En vivo» con un servidor que no contesta /api/v1/live: un solo pedido a la vez (el reloj de 10 s y los
// avisos no apilan pedidos colgados), un tiempo límite que dice qué pasa en vez de «Cargando…» para siempre,
// y el reintento después, con espera. No crea datos.
const { test, expect } = require("@playwright/test");

test("En vivo: con /live colgado hay un solo pedido a la vez, con tiempo límite y reintento", async ({ page }) => {
	const errors = [];
	page.on("pageerror", (e) => errors.push(e.message));
	let calls = 0, answer = false;
	await page.route("**/api/v1/live", async (r) => {
		calls++;
		if (answer) return r.fulfill({ json: { now: Date.now(), no_signal_ms: 120000, executions: [] } });
		// colgado: no contesta (el navegador lo corta con su tiempo límite)
	});
	await page.route("**/api/v1/live/history?**", (r) => r.fulfill({ json: { now: Date.now(), total: 0, next: 0, executions: [] } }));
	await page.clock.install();
	await page.goto("/#view=live");
	await expect.poll(() => calls).toBe(1);
	// 15 s colgado: el reloj de 10 s y los avisos no lanzan otro pedido
	await page.clock.runFor(15_000);
	expect(calls).toBe(1);
	// a los 20 s se corta y lo dice
	await page.clock.runFor(6_000);
	await expect(page.locator("#view-live")).toContainText("El servidor no respondió en 20 s");
	// el servidor vuelve: el próximo intento (con espera) carga la vista
	answer = true;
	await page.clock.runFor(12_000);
	await expect.poll(() => calls).toBeGreaterThanOrEqual(2);
	await expect(page.locator("#view-live")).not.toContainText("El servidor no respondió");
	expect(errors).toEqual([]);
});
