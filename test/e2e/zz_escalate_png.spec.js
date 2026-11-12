// El PNG de la tarjeta de escalamiento se ve igual que la tarjeta en pantalla, también con la pantalla
// al 125 % (habitual en notebooks con Windows): antes, la insignia de severidad recibía un ancho fijo con
// decimales y, al dibujar el PNG a otra escala, la última letra saltaba de línea («BAJ»). Va al final
// (zz_): crea una ejecución nueva.
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });
test.use({ deviceScaleFactor: 1.25, acceptDownloads: true, viewport: { width: 1280, height: 1000 } });

let run, testId;

test.beforeAll(async ({ request }) => {
	run = (await (await request.post("/api/v1/runs", { data: { name: `escpng-${Date.now()}`, environment: "preproduccion" } })).json()).run_id;
	testId = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name: "test_agregar_registro_desde_la_lista" } })).json()).test_id;
	await request.patch(`/api/v1/tests/${testId}/finish`, { data: { status: "FAIL", error_message: "TimeoutError: esperando elemento //div[@class='fila']" } });
	await request.patch(`/api/v1/runs/${run}/finish`, { data: {} });
});

/**
 * Cuánto difiere el PNG exportado de la tarjeta en pantalla en la ficha (la tabla de datos): las dos
 * imágenes al tamaño de la tarjeta (1×), píxel a píxel. Un salto de línea en una insignia corre la fila
 * entera y lo de abajo.
 */
async function pngVsScreen(page, card, pngBuffer) {
	const shot = await card.screenshot();
	return page.evaluate(async ([a, b]) => {
		const el = document.querySelector("#esc-card"), box = el.getBoundingClientRect(), facts = el.querySelector(".esc-facts").getBoundingClientRect();
		const W = Math.round(box.width), H = Math.round(box.height);
		const draw = async (b64) => {
			const im = new Image(); im.src = "data:image/png;base64," + b64; await im.decode();
			const c = document.createElement("canvas"); c.width = W; c.height = H;
			const g = c.getContext("2d"); g.fillStyle = "#fff"; g.fillRect(0, 0, W, H); g.drawImage(im, 0, 0, W, H);
			return g.getImageData(Math.round(facts.left - box.left), Math.round(facts.top - box.top), Math.round(facts.width), Math.round(facts.height)).data;
		};
		const [x, y] = [await draw(a), await draw(b)];
		let off = 0;
		for (let i = 0; i < x.length; i += 4) if (Math.max(Math.abs(x[i] - y[i]), Math.abs(x[i + 1] - y[i + 1]), Math.abs(x[i + 2] - y[i + 2])) > 90) off++;
		return off / (x.length / 4);
	}, [shot.toString("base64"), pngBuffer.toString("base64")]);
}

for (const lang of ["es", "en"]) {
	test(`PNG a 125 %: las insignias de severidad salen enteras (${lang})`, async ({ page }) => {
		const errors = [];
		page.on("pageerror", (e) => errors.push(e.message));
		await page.goto(`/#run=${run}&view=escalate`);
		const view = page.locator("#view-escalate");
		await view.locator("#esc-scope").selectOption(String(testId));
		await view.locator(`[data-esc-lang="${lang}"]`).click();
		const noAI = view.locator('[data-esc-ai="0"]');
		if (await noAI.count()) await noAI.click();
		const card = view.locator("#esc-card");
		await expect(async () => {
			await view.locator("[data-esc-gen]").click();
			await expect(card).toBeVisible({ timeout: 3000 });
		}).toPass({ timeout: 20_000 });
		await expect(card.locator(".esc-sev")).toHaveCSS("white-space", "nowrap");
		// lo que se dibuja en el PNG: la tarjeta con su estilo en línea (XMLSerializer → SVG → canvas)
		await page.evaluate(() => {
			const orig = XMLSerializer.prototype.serializeToString;
			XMLSerializer.prototype.serializeToString = function (n) { const s = orig.call(this, n); if (n?.classList?.contains("esc-card")) window.__escXHTML = s; return s; };
		});
		const labels = lang === "es" ? ["Baja", "Media", "Alta", "Crítica"] : ["Low", "Medium", "High", "Critical"];
		for (const label of labels) {
			await card.locator(".esc-sev").evaluate((el, l) => { el.textContent = l; }, label);
			const [dl] = await Promise.all([page.waitForEvent("download"), view.locator('[data-esc-share="png-dl"]').click()]);
			const fs = require("node:fs");
			const png = fs.readFileSync(await dl.path());
			const off = await pngVsScreen(page, card, png);
			expect(off, `«${label}»: la ficha del PNG es la de la pantalla`).toBeLessThan(0.02);
			// la causa, sin depender de la fuente del equipo: la insignia no lleva un ancho fijo; las columnas sí
			const styles = await page.evaluate(() => {
				const d = new DOMParser().parseFromString(window.__escXHTML, "application/xhtml+xml");
				return { sev: d.querySelector(".esc-sev").getAttribute("style"), th: d.querySelector(".esc-facts th").getAttribute("style") };
			});
			expect(styles.sev, `«${label}»: la insignia toma el ancho de su texto`).not.toMatch(/(^|;)\s*(width|inline-size):/);
			expect(styles.sev, "una sola línea aunque permita partir palabras (overflow-wrap)").toMatch(/(white-space|text-wrap-mode):\s*nowrap/);
			expect(styles.th, "las columnas conservan su ancho").toMatch(/(^|;)\s*width:/);
		}
		expect(errors).toEqual([]);
	});
}
