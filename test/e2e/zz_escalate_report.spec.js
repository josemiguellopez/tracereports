// Va al final (zz_): crea una ejecución, y los specs anteriores cuentan con los IDs del seed.
// Escalar como reporte formal: número de reporte, ficha, resumen ejecutivo, secciones numeradas,
// pie "Uso interno" y PDF. Una captura en blanco (el navegador nunca mostró la página) se muestra
// como aviso y no como un recuadro blanco que parece una imagen que no cargó (caso del cliente).
const zlib = require("node:zlib");
const { test, expect } = require("@playwright/test");

test.describe.configure({ mode: "serial" });

/** PNG de 160x100: en blanco, o con una franja azul (una página con contenido). */
function png(blank) {
	const w = 160, h = 100;
	const raw = Buffer.alloc((w * 3 + 1) * h);
	for (let y = 0; y < h; y++) {
		const row = y * (w * 3 + 1);
		for (let x = 0; x < w; x++) {
			const ink = !blank && y > 30 && y < 60 && x > 20 && x < 140;
			raw.set(ink ? [31, 58, 95] : [255, 255, 255], row + 1 + x * 3);
		}
	}
	const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
	const crc = (buf) => { let c = 0xffffffff; for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
	const chunk = (type, data) => {
		const len = Buffer.alloc(4); len.writeUInt32BE(data.length);
		const td = Buffer.concat([Buffer.from(type), data]);
		const c = Buffer.alloc(4); c.writeUInt32BE(crc(td));
		return Buffer.concat([len, td, c]);
	};
	const ihdr = Buffer.alloc(13);
	ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2;
	return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
}

const token = `escrep-${Date.now()}`;
let run, blankTest, pageTest;

async function shot(request, testId, blank, status) {
	const r = await request.post(`/api/v1/tests/${testId}/screenshot`, { multipart: {
		file: { name: "shot.png", mimeType: "image/png", buffer: png(blank) }, message: status === "FAIL" ? "Captura al fallar" : "Paso", status } });
	expect(r.ok()).toBeTruthy();
}

test.beforeAll(async ({ request }) => {
	run = (await (await request.post("/api/v1/runs", { data: { name: `${token} receta`, environment: "preproduccion", project: token } })).json()).run_id;
	const add = async (name, blank) => {
		const id = (await (await request.post(`/api/v1/runs/${run}/tests`, { data: { name, key: `tests/receta.py::${name}` } })).json()).test_id;
		await shot(request, id, true, "INFO");
		await shot(request, id, blank, "FAIL");
		await request.patch(`/api/v1/tests/${id}/finish`, { data: { status: "FAIL",
			error_message: `NoSuchElementException: no such element: {"selector":"[name='useRecipe']"}` } });
		return id;
	};
	blankTest = await add("test_crear_receta_pagina_en_blanco", true);
	pageTest = await add("test_crear_receta_con_pagina", false);
	await request.patch(`/api/v1/runs/${run}/finish`, { data: {} });
});

test.beforeEach(async ({ page }, info) => {
	info.errors_ = [];
	page.on("pageerror", (e) => info.errors_.push(`pageerror: ${e.message}`));
	page.on("console", (m) => { if (m.type() === "error") info.errors_.push(`console.error: ${m.text()}`); });
});

test.afterEach(async ({}, info) => {
	expect(info.errors_, "la página no debe tener errores").toEqual([]);
});

/** Genera el resumen sin IA (plantilla: inmediato y determinístico). */
async function generate(page, testId, { audience = "business", lang = "es" } = {}) {
	await page.goto(`/#run=${run}&view=escalate`);
	const view = page.locator("#view-escalate");
	await view.locator("#esc-scope").selectOption(String(testId));
	await view.locator(`input[name="esc-aud"][value="${audience}"]`).check({ force: true });
	await view.locator(`[data-esc-lang="${lang}"]`).click();
	const noAI = view.locator('[data-esc-ai="0"]');
	if (await noAI.count()) await noAI.click();
	// elegir audiencia, idioma y "Sin IA" redibuja los controles: un clic justo en ese momento se pierde
	const card = view.locator("#esc-card");
	await expect(async () => {
		await view.locator("[data-esc-gen]").click();
		await expect(card).toBeVisible({ timeout: 3000 });
	}).toPass({ timeout: 20_000 });
	return card;
}

test("captura en blanco: aviso en vez de un recuadro blanco, y el texto no dice que cambió la interfaz", async ({ page }) => {
	const card = await generate(page, blankTest);
	const notice = card.locator(".esc-shot-blank");
	await expect(notice).toContainText("La página estaba en blanco al fallar");
	await expect(notice.locator("a")).toHaveAttribute("href", /\/screenshots\//);
	await expect(card.locator(".esc-shot img")).toHaveCount(0);
	await expect(card).not.toContainText("La pantalla cambió");
	await expect(card).toContainText("La página no llegó a cargar");
	await expect(card).toContainText("Las capturas del test están en blanco");
	await expect(card.locator(".esc-sev")).not.toHaveText("Baja");
});

test("captura con contenido: se muestra la imagen, sin aviso", async ({ page }) => {
	const card = await generate(page, pageTest);
	await expect(card.locator(".esc-shot img")).toBeVisible();
	await expect(card.locator(".esc-shot-blank")).toHaveCount(0);
});

test("formato de reporte: número, ficha, resumen ejecutivo, secciones numeradas y pie", async ({ page }) => {
	const card = await generate(page, blankTest);
	const ref = `ESC-${String(run).padStart(4, "0")}-${blankTest}`;
	await expect(card).toHaveAttribute("data-ref", ref);
	await expect(card.locator(".esc-doc-ref")).toContainText(`N.º ${ref}`);
	await expect(card.locator(".esc-doc-kind")).toHaveText("Reporte de escalamiento");
	const facts = card.locator(".esc-facts");
	for (const k of ["Severidad", "Audiencia", "Ejecución", "Ambiente", "Resultado", "Fecha de ejecución", "Test", "Responsable sugerido"]) {
		await expect(facts.locator("th", { hasText: k }).first()).toBeVisible();
	}
	await expect(card.locator(".esc-aud")).toHaveText("Negocio");
	await expect(card.locator(".esc-summary h3")).toHaveText("Resumen ejecutivo");
	const titles = await card.locator(".esc-sec h3").allTextContents();
	expect(titles.slice(0, 3).map((x) => x.trim())).toEqual(["1. Qué pasó", "2. Impacto", "3. Causa probable"]);
	await expect(card.locator(".esc-foot")).toContainText("Generado con TraceReports");
	await expect(card.locator(".esc-foot")).toContainText("Uso interno");

	// en inglés, el mismo formato
	const en = await generate(page, blankTest, { lang: "en" });
	await expect(en.locator(".esc-doc-kind")).toHaveText("Escalation report");
	await expect(en.locator(".esc-summary h3")).toHaveText("Executive summary");
	await expect(en.locator(".esc-shot-blank")).toContainText("The page was blank at the failure");
	await expect(en.locator(".esc-foot")).toContainText("Internal use");
});

test("PDF: imprime solo el reporte, con el número como nombre, y deja la página como estaba", async ({ page }) => {
	await generate(page, blankTest);
	const title = await page.title();
	await page.evaluate(() => { window.print = () => { window.__printed = (window.__printed || 0) + 1; }; });
	await page.locator('[data-esc-share="pdf"]').click();
	expect(await page.evaluate(() => window.__printed)).toBe(1);
	expect(await page.title()).toMatch(new RegExp(`^ESC-${String(run).padStart(4, "0")}-${blankTest} · `));
	await expect(page.locator("#esc-print .esc-card")).toHaveCount(1);
	await expect(page.locator("#esc-print #esc-card")).toHaveCount(0); // la copia no duplica el id
	await page.emulateMedia({ media: "print" });
	const shown = await page.evaluate(() => [...document.body.children].filter((el) => getComputedStyle(el).display !== "none").map((el) => el.id));
	expect(shown).toEqual(["esc-print"]);
	await page.emulateMedia({ media: "screen" });
	await page.evaluate(() => window.dispatchEvent(new Event("afterprint")));
	await expect(page.locator("#esc-print")).toHaveCount(0);
	expect(await page.title()).toBe(title);
	expect(await page.evaluate(() => document.body.classList.contains("esc-printing"))).toBe(false);
});

test("texto y correo llevan el número de reporte; el correo no pega una captura en blanco", async ({ page, context }) => {
	await context.grantPermissions(["clipboard-read", "clipboard-write"]);
	await generate(page, blankTest);
	await page.locator('[data-esc-share="plain"]').click();
	const text = await page.evaluate(() => navigator.clipboard.readText());
	expect(text).toContain(`Reporte de escalamiento ESC-${String(run).padStart(4, "0")}-${blankTest}`);
	expect(text).toContain("Uso interno");
	// el HTML del correo, tomado al escribirse en el portapapeles (leerlo de vuelta depende del navegador)
	await page.evaluate(() => {
		const write = navigator.clipboard.write.bind(navigator.clipboard);
		navigator.clipboard.write = async (items) => {
			const html = items.find((i) => i.types.includes("text/html"));
			if (html) window.__html = await (await html.getType("text/html")).text();
			return write(items);
		};
	});
	await page.locator('[data-esc-share="email"]').click();
	await expect.poll(() => page.evaluate(() => window.__html || "")).toContain("Resumen ejecutivo");
	const html = await page.evaluate(() => window.__html);
	expect(html).toContain("La página estaba en blanco al fallar");
	expect(html).not.toContain("<img");
	expect(html).toContain("Resumen ejecutivo");
});
