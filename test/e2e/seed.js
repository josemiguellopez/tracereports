// Carga por la API REST (la misma que usan los clientes) dos ejecuciones del mismo proyecto: una
// anterior en verde y la actual con fallos, red, capturas y pasos, para que todas las vistas
// tengan algo que dibujar. Los IDs quedan en E2E_SEED para los tests.
const fs = require("node:fs");
const path = require("node:path");

// PNG de 1x1 (gris): basta para que existan capturas, el replay y su miniatura
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNoaGgAAAMEAYFL09IQAAAAAElFTkSuQmCC", "base64");

async function api(method, url, body) {
	const res = await fetch(`${process.env.E2E_BASE_URL}/api/v1${url}`, {
		method,
		headers: { "Content-Type": "application/json" },
		body: body === undefined ? undefined : JSON.stringify(body),
	});
	if (!res.ok) throw new Error(`${method} ${url} -> ${res.status} ${await res.text()}`);
	return res.json();
}

async function screenshot(testId, message, status = "INFO") {
	const form = new FormData();
	form.append("file", new Blob([PNG], { type: "image/png" }), "shot.png");
	form.append("message", message);
	form.append("status", status);
	const res = await fetch(`${process.env.E2E_BASE_URL}/api/v1/tests/${testId}/screenshot`, { method: "POST", body: form });
	if (!res.ok) throw new Error(`screenshot -> ${res.status} ${await res.text()}`);
}

async function addTest(runId, t) {
	const { test_id: id } = await api("POST", `/runs/${runId}/tests`, { name: t.name, category: t.category, suite: "e2e", key: t.name });
	const base = Date.now() - 60_000;
	for (const [i, [status, message]] of (t.steps || []).entries()) {
		await api("POST", `/tests/${id}/logs`, { status, message, timestamp: base + i * 1000 });
	}
	for (const s of t.shots || []) await screenshot(id, s);
	if (t.network) await api("POST", `/tests/${id}/network`, { connections: t.network });
	if (t.console) await api("POST", `/tests/${id}/console`, { entries: t.console });
	await api("PATCH", `/tests/${id}/finish`, { status: t.status, error_message: t.error || "" });
	return id;
}

const RUN = { project: "e2e", environment: "qa", branch: "dev", framework: "pytest" };

// FixLab (lab.spec.js): lo que grabó un test contra el backend falso de playwright.config.js
const LAB = { password: "E2E-lab-pass!", token: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJsYWIifQ.fresh-e2e-signature" };

async function seedLab() {
	const base = process.env.E2E_FAKE_LAB;
	const { run_id: run } = await api("POST", "/runs", { name: "Lab E2E", project: "lab-e2e", environment: "qa", framework: "playwright" });
	const hdr = { "content-type": "application/json", date: new Date().toUTCString(), server: "lab-e2e" };
	const old = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJsYWIifQ.old-signature";
	// antes del paso FAIL (addTest lo fecha hace 60 s): así la llamada es la causa del incidente
	const now = Date.now() - 62_000;
	await addTest(run, {
		name: "ver pedidos abiertos", category: "pedidos", status: "FAIL", error: "Error: expect(locator).toHaveCount(2) failed",
		steps: [["FAIL", "La lista de pedidos no cargó"]],
		network: [
			{ method: "POST", url: `${base}/api/login`, status: 200, resource_type: "fetch", started_at: now - 3000, duration_ms: 40,
				request_headers: { "content-type": "application/json" }, post_data: JSON.stringify({ email: "qa@shop.test", password: LAB.password }),
				response_headers: hdr, response_body: JSON.stringify({ accessToken: old }) },
			{ method: "GET", url: `${base}/api/orders?status=open`, status: 500, resource_type: "fetch", started_at: now - 1000, duration_ms: 30,
				request_headers: { authorization: `Bearer ${old}` }, response_headers: hdr,
				response_body: JSON.stringify({ error: "orders db: connection pool exhausted" }) },
		],
	});
	await api("PATCH", `/runs/${run}/finish`, {});
	return run;
}

module.exports = async () => {
	// primero: la ejecución más antigua es la que sale primero del selector (últimas 10)
	const labRun = await seedLab();
	// ejecución anterior: todo en verde (para comparación, historial y métricas)
	const { run_id: prev } = await api("POST", "/runs", { ...RUN, name: "E2E anterior", commit: "aaa111" });
	for (const name of ["test_login_admin", "test_buscar_empleado", "test_reporte_mensual"]) {
		await addTest(prev, { name, category: "smoke", status: "PASS", steps: [["PASS", `${name} ok`]],
			// la misma llamada del login, cuando funcionaba: base para "comparar con la última vez que pasó"
			network: name !== "test_login_admin" ? undefined : [{
				method: "POST", url: "https://api.example.com/auth/login?lang=es", status: 200, status_text: "OK",
				mime_type: "application/json", resource_type: "fetch", started_at: Date.now() - 120_000, duration_ms: 95,
				request_headers: { "Content-Type": "application/json" }, post_data: JSON.stringify({ username: "admin", password: "x" }),
				response_headers: { "content-type": "application/json", "x-api-version": "1.4" },
				response_body: JSON.stringify({ ok: true, user: "admin" }),
			}] });
	}
	await api("PATCH", `/runs/${prev}/finish`, {});

	// ejecución actual
	const { run_id: run } = await api("POST", "/runs", { ...RUN, name: "E2E actual", commit: "bbb222" });
	const failId = await addTest(run, {
		name: "test_login_admin", category: "login", status: "FAIL",
		error: "TimeoutError: Timeout 30000ms exceeded waiting for \"Dashboard\" to be visible",
		steps: [["INFO", "Abrir la página de login"], ["PASS", "Escribir usuario y clave"], ["FAIL", "El Dashboard no apareció"]],
		shots: ["Formulario de login", "Pantalla al fallar"],
		console: [
			{ level: "warning", text: "API deprecada: usa /v2/auth", location: "https://app.example.com/main.js:10:5" },
			{ level: "pageerror", text: "TypeError: Cannot read properties of undefined (reading 'dashboard')" },
		],
		network: [
			{
				method: "POST", url: "https://api.example.com/auth/login?lang=es", status: 500, status_text: "Internal Server Error",
				mime_type: "application/json", resource_type: "fetch", started_at: Date.now() - 50_000, duration_ms: 812,
				request_headers: { "Content-Type": "application/json", Authorization: "Bearer E2E-SECRET",
					traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" },
				post_data: JSON.stringify({ username: "admin", password: "E2E-PASSWORD" }),
				response_headers: { "content-type": "application/json", "x-request-id": "req-e2e-42" },
				response_body: JSON.stringify({ error: "db pool exhausted" }),
			},
			{
				method: "GET", url: "https://api.example.com/config", status: 200, status_text: "OK",
				mime_type: "application/json", resource_type: "fetch", started_at: Date.now() - 55_000, duration_ms: 40,
				request_headers: { Accept: "application/json" }, response_body: "{\"ok\":true}",
			},
		],
	});
	await addTest(run, { name: "test_buscar_empleado", category: "smoke", status: "PASS", steps: [["PASS", "Búsqueda ok"]], shots: ["Resultados"] });
	await addTest(run, { name: "test_reporte_mensual", category: "smoke", status: "SKIP", steps: [["SKIP", "Sin datos del mes"]] });
	await api("PATCH", `/runs/${run}/finish`, {});

	process.env.E2E_SEED = JSON.stringify({ prev, run, failId, labRun });
	fs.writeFileSync(path.join(process.env.E2E_DATA_DIR, "seed.json"), process.env.E2E_SEED);
};

module.exports.LAB = LAB;
