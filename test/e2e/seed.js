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

module.exports = async () => {
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

	process.env.E2E_SEED = JSON.stringify({ prev, run, failId });
	fs.writeFileSync(path.join(process.env.E2E_DATA_DIR, "seed.json"), process.env.E2E_SEED);
};
