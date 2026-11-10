// La evidencia que llegó tarde se ve en TraceReports: el cliente informa al cerrar el run que no pudo
// crearlo a tiempo (y por qué); el reporte lo avisa y Mantenimiento > Conexiones lo lista junto a los
// pedidos lentos del servidor. Crea runs: va al final (zz_).
const { test, expect } = require("@playwright/test");

test("un run creado tarde: el reporte lo avisa con la causa y Mantenimiento > Conexiones lo lista", async ({ page, request }) => {
	const name = `E2E llegó tarde ${Date.now()}`;
	const res = await request.post("/api/v1/runs", { data: { name, environment: "qa", execution_id: "e2e-tarde" } });
	const { run_id: id } = await res.json();
	const { test_id: tid } = await (await request.post(`/api/v1/runs/${id}/tests`, { data: { name: "t" } })).json();
	await request.patch(`/api/v1/tests/${tid}/finish`, { data: { status: "PASS" } });
	const lateAt = Date.UTC(2026, 10, 10, 13, 0, 34);
	const fin = await request.patch(`/api/v1/runs/${id}/finish`, { data: { interrupted: false, delivery: {
		late: true, late_at: lateAt, reason: "the server did not answer within 30 s", detail: "connected to 10.0.0.25:8080 in 3 ms, no answer in 10 s",
		retried: 2, client: "python 0.5.1", host: "ntbk-e2e" } } });
	expect(fin.ok()).toBeTruthy();

	await page.goto(`/#run=${id}&view=tests`);
	const banner = page.locator("#run-banner");
	await expect(banner).toBeVisible();
	await expect(banner).toContainText("Este run llegó al final: no hubo conexión al crearlo");
	await expect(banner).toContainText("the server did not answer within 30 s");
	await expect(banner).toContainText("connected to 10.0.0.25:8080 in 3 ms, no answer in 10 s");
	await banner.locator("[data-banner-connections]").click();

	const card = page.locator("#maint-connections");
	await expect(card).toBeVisible();
	await expect(card).toContainText("Conexiones de los clientes");
	const row = card.locator("tr", { hasText: name });
	await expect(row).toContainText("Creado tarde");
	await expect(row).toContainText("2 reintentos");
	await expect(row).toContainText("ntbk-e2e · python 0.5.1");
	await expect(row.locator("a")).toHaveAttribute("href", `#run=${id}&view=tests`);
	await expect(card.locator('[data-conn-sec="runs"]')).toHaveAttribute("open", ""); // el aviso lleva a la sección abierta
	await expect(card).toContainText("Pedidos lentos del servidor");
	await card.locator('[data-conn-sec="slow"] summary').click();
	await expect(card.locator('[data-conn-sec="slow"]')).toContainText("el pedido no llegó al servidor");
	await expect(card.locator(".stor-head")).toBeInViewport(); // se llega a la tarjeta
	await card.locator("[data-conn-reload]").click();
	await expect(card.locator("tr", { hasText: name })).toBeVisible();
});

test("un run que llegó bien no muestra el aviso de entrega", async ({ page, request }) => {
	const { run_id: id } = await (await request.post("/api/v1/runs", { data: { name: "E2E llegó bien" } })).json();
	await request.patch(`/api/v1/runs/${id}/finish`, { data: { delivery: { client: "python 0.5.1" } } });
	await page.goto(`/#run=${id}&view=tests`);
	await expect(page.locator("#report-name")).toHaveText("E2E llegó bien");
	await expect(page.locator("#run-banner")).not.toContainText("llegó al final");
});

test("Mantenimiento > Conexiones: los bloqueos del servidor con su duración, qué esperaba y el volcado para descargar", async ({ page }) => {
	const t0 = Date.UTC(2026, 10, 10, 18, 47, 35);
	await page.route("**/api/v1/maintenance/connections", (r) => r.fulfill({ json: {
		days: 7, runs: [], slow: [], slow_after_ms: 2000, started_at: t0 - 600000, auth: [], stall_after_ms: 10000,
		stalls: [
			{ start: t0 + 120000, max_stuck: 4, paths: ["POST /api/v1/tests/9/logs"], db_wait_ms: 0, dump: "stall-20261110-184940.txt" },
			{ start: t0, end: t0 + 41000, max_stuck: 16, paths: ["POST /api/v1/runs", "GET /api/v1/auth/session"], db_wait_ms: 612000, dump: "stall-20261110-184745.txt" },
		] } }));
	await page.goto("/#view=maintenance");
	const card = page.locator("#maint-connections");
	await expect(card).toContainText("Bloqueos del servidor (varios pedidos sin respuesta más de 10 s a la vez)");
	await card.locator('[data-conn-jump="stalls"]').click(); // el resumen lleva a la sección y la abre
	await expect(card.locator('[data-conn-sec="stalls"]')).toHaveAttribute("open", "");
	const rows = card.locator("tr", { hasText: "stall-2026" });
	await expect(rows).toHaveCount(2);
	await expect(rows.first()).toContainText("en curso");
	const done = rows.nth(1);
	await expect(done).toContainText("41,0 s");
	await expect(done).toContainText("16");
	await expect(done).toContainText("POST /api/v1/runs");
	await expect(done).toContainText("612,0 s");
	await expect(done.locator("a[download]")).toHaveAttribute("href", "/api/v1/maintenance/stalls/stall-20261110-184745.txt");
	await expect(card).toContainText("El volcado dice qué estaba haciendo cada tarea del servidor");
});

test("Mantenimiento > Conexiones: sin bloqueos lo dice", async ({ page }) => {
	await page.goto("/#view=maintenance");
	await expect(page.locator("#maint-connections")).toContainText("Ningún bloqueo desde que el servidor arrancó");
});

test("Mantenimiento > Conexiones: resumen arriba, secciones plegadas, runs agrupados por ejecución y de a 10", async ({ page }) => {
	const t0 = Date.UTC(2026, 10, 10, 18, 0, 0);
	const late = (id, exec, host) => ({ id, name: `test_${id}.py`, execution_id: exec, started_at: t0 + id * 1000,
		delivery: { late: true, late_at: t0 + id * 1000, reason: "no answer in 30 s", retried: 1, host, client: "python 0.5.2" } });
	const runs = [...Array.from({ length: 37 }, (_, i) => late(100 + i, "suite-16w", `qa-${i % 2}`)),
		...Array.from({ length: 14 }, (_, i) => late(1 + i, `otra-${i}`, "qa-0"))]; // más viejas: la de 37 va primero
	const slow = Array.from({ length: 50 }, (_, i) => ({ at: t0 + i * 1000, method: "POST", path: `/api/v1/tests/${i}/network`, status: 201, ms: 2500 + i }));
	await page.route("**/api/v1/maintenance/connections", (r) => r.fulfill({ json: {
		days: 7, runs, slow, slow_after_ms: 2000, started_at: t0 - 600000, auth: [], stall_after_ms: 10000, stalls: [] } }));
	await page.goto("/#view=maintenance");
	const card = page.locator("#maint-connections");
	// el resumen: cuánto hay de cada cosa, sin listas abiertas
	await expect(card.locator(".conn-status")).toContainText("15 ejecuciones con problemas de entrega");
	await expect(card.locator(".conn-status")).toContainText("50 pedidos lentos");
	await expect(card.locator(".conn-status")).toContainText("0 bloqueos");
	await expect(card.locator("details[open]")).toHaveCount(0);
	await expect(card.locator("tbody tr:visible")).toHaveCount(0);
	// abrir runs: una fila por ejecución, de a 10; la de 37 runs dice cuántos y en qué equipos
	await card.locator('[data-conn-sec="runs"] summary').click();
	const runsSec = card.locator('[data-conn-sec="runs"]');
	await expect(runsSec.locator("tr.conn-group")).toHaveCount(10);
	const big = runsSec.locator("tr.conn-group", { hasText: "suite-16w" });
	await expect(big).toContainText("37");
	await expect(big).toContainText("37 creados tarde");
	await expect(big).toContainText("qa-0 · qa-1");
	await big.locator("[data-conn-exec]").click();
	await expect(runsSec.locator("tr.conn-subrow")).toHaveCount(37);
	await expect(runsSec.locator("[data-conn-exec]").first()).toHaveAttribute("aria-expanded", "true");
	await runsSec.locator("[data-conn-exec]").first().click();
	await expect(runsSec.locator("tr.conn-subrow")).toHaveCount(0);
	await runsSec.locator("[data-conn-more]").click();
	await expect(runsSec.locator("tr.conn-group")).toHaveCount(15);
	await expect(runsSec.locator("[data-conn-more]")).toHaveCount(0);
	// pedidos lentos: de a 10, "Mostrar 20 más" con el total
	await card.locator('[data-conn-sec="slow"] summary').click();
	const slowSec = card.locator('[data-conn-sec="slow"]');
	await expect(slowSec.locator("tbody tr")).toHaveCount(10);
	await expect(slowSec.locator("[data-conn-more]")).toContainText("Mostrar 20 más");
	await expect(slowSec.locator("[data-conn-more]")).toContainText("(50 en total)");
	// lo abierto sigue abierto al actualizar
	await card.locator("[data-conn-reload]").click();
	await expect(card.locator('[data-conn-sec="slow"]')).toHaveAttribute("open", "");
	await expect(card.locator('[data-conn-sec="runs"]')).toHaveAttribute("open", "");
});

test("Mantenimiento: un error del servidor dice la causa y la referencia para buscarla en el log", async ({ page }) => {
	const name = `E2E error al borrar ${Date.now()}`;
	await page.request.post("/api/v1/runs", { data: { name } }).then(async (r) => {
		const { run_id: id } = await r.json();
		await page.request.patch(`/api/v1/runs/${id}/finish`, { data: { interrupted: true } }); // incompleta: sale en "Con problemas"
	});
	await page.route("**/api/v1/ui/runs/delete", (r) => r.fulfill({ status: 500, json: { error: "the server's data disk is full: free space or lower the retention (ref 1a2b3c4d)", ref: "1a2b3c4d", kind: "disk_full" } }));
	await page.goto("/#view=maintenance");
	const row = page.locator(".maint-table tr", { hasText: name });
	await row.locator("[data-maint-pick]").check();
	await page.locator('[data-maint-act="delete"]').click();
	await page.locator("[data-maint-delete]").click();
	const view = page.locator("#view-maintenance");
	await expect(view).toContainText("El disco de datos del servidor está lleno: libera espacio o baja la retención.");
	await expect(view).toContainText("referencia 1a2b3c4d");
});
