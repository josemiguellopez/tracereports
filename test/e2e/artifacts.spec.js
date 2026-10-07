// Trace y video de Playwright en el detalle del test: el video se reproduce, el trace se descarga.
const { test, expect } = require("@playwright/test");

const seed = () => JSON.parse(process.env.E2E_SEED);

test("trace y video adjuntos al test", async ({ page, request }) => {
	const errors = [];
	page.on("pageerror", (e) => errors.push(e.message));
	const { run, failId } = seed();
	const upload = (kind, name, buffer) => request.post(`/api/v1/tests/${failId}/artifact`, {
		multipart: { kind, name, file: { name: "artifact", mimeType: "application/octet-stream", buffer } },
	});
	expect((await upload("trace", "trace del intento 1", Buffer.from("PK\u0003\u0004trace"))).status()).toBe(201);
	expect((await upload("video", "video", Buffer.concat([Buffer.from([0x1a, 0x45, 0xdf, 0xa3]), Buffer.alloc(64)]))).status()).toBe(201);
	// lo que no es un trace o un video no entra
	expect((await upload("video", "x", Buffer.from("<!doctype html><script>alert(1)</script>"))).status()).toBe(415);

	await page.goto(`/#run=${run}&view=tests&test=${failId}`);
	const block = page.locator("#test-detail .artifacts");
	await expect(block).toBeVisible();
	await expect(block.locator("video")).toHaveCount(1);
	const download = block.locator("a[download]");
	await expect(download).toHaveAttribute("href", /^\/screenshots\/a\d+_\d+\.zip$/);
	// en http (no https) no se ofrece el Trace Viewer: no podría descargar el trace
	await expect(block.locator('a[href^="https://trace.playwright.dev"]')).toHaveCount(0);
	await expect(block).toContainText("npx playwright show-trace");
	const res = await request.get(await download.getAttribute("href"));
	expect(res.status()).toBe(200);
	expect((await res.body()).subarray(0, 4).toString("latin1")).toBe("PK\u0003\u0004");
	expect(errors).toEqual([]);
});
