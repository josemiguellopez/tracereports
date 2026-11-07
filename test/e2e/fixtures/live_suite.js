// Una ejecución grande para En vivo, con la forma de la suite que la motivó: un lanzador que abre una
// corrida por archivo (86) en 12 procesos, sin `worker`, ~270 tests, prefijo común en los nombres y un
// "último paso" largo con URL. Datos inventados y deterministas (sin Math.random): el e2e y las capturas
// ven siempre lo mismo. Se sirve con page.route en vez de /api/v1/live y /api/v1/live/execution.
"use strict";

const MIN = 60_000;

/** {list, execution} para una ejecución en curso que empezó hace 34 min. */
function liveSuite(now = Date.now(), { runs: nRuns = 86, procs = 12 } = {}) {
	const start = now - 34 * MIN;
	const areas = ["ficha_odontologica", "agenda", "receta", "despachar_receta", "agregar_docs", "periodontograma", "box_atencion", "derivacion", "vacunatorio", "farmacia"];
	const runs = [];
	const free = Array(procs).fill(start);
	let id = 1000, k = 0;
	const special = {
		5: "no_signal", 9: "FAIL", 17: "turtle", 23: "FAIL", 40: "turtle", 61: "FAIL",
	};
	for (let r = 0; r < nRuns; r++) {
		// el proceso que se libera primero toma la siguiente corrida
		const p = free.indexOf(Math.min(...free));
		const runStart = free[p] + 2000;
		if (runStart > now - 5000) break; // ya no caben más corridas en estos 34 min (o todos los procesos siguen ocupados)
		const name = `Clinico · test_${areas[r % areas.length]}_${String(r + 1).padStart(3, "0")}.py`;
		const n = 2 + (r % 3) + (r % 7 === 0 ? 1 : 0); // 2 a 5 tests
		const tests = [];
		let t0 = runStart;
		let running = false;
		for (let i = 0; i < n; i++) {
			k++;
			const dur = (40 + ((r * 7 + i * 13) % 90)) * 1000;
			const p50 = dur;
			let state = (k % 8 === 0) ? "WARNING" : "PASS";
			let end = t0 + dur;
			if (i === 0 && special[r]) state = special[r];
			const stillGoing = end > now || state === "no_signal" || state === "turtle";
			if (stillGoing) {
				running = true;
				if (state === "PASS" || state === "WARNING") state = "running";
			}
			const t = {
				id: ++id, run_id: r + 1, name: `test_${String(i + 1).padStart(3, "0")}_${areas[(r + i) % areas.length]}_paso_${i + 1}`, worker: "", status: stillGoing ? "RUNNING" : state,
				state, started_at: t0, attempts: 1, last_signal: stillGoing ? now - (state === "no_signal" ? 4 * MIN : 3000) : end, p50, p90: p50 * 1.4, hist: 6,
				last_step: state === "FAIL" ? "TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]"
					: `Evidencia de ${areas[(r + i) % areas.length].replace(/_/g, " ")} del paciente - url: https://preclinico.demo/atencion/${r + 1}/${i + 1}?token=abc`,
			};
			if (stillGoing) {
				t.elapsed = now - t0;
				t.idle = now - t.last_signal;
				t.factor = state === "no_signal" ? 8.3 : state === "turtle" ? 2.4 : t.elapsed / p50;
				tests.push(t);
				break; // el resto de la corrida todavía no empieza
			}
			t.ended_at = end;
			t.elapsed = dur;
			t.factor = state === "FAIL" ? 1.1 : 1;
			tests.push(t);
			t0 = end + 1000;
		}
		const runEnd = running ? null : tests[tests.length - 1].ended_at;
		free[p] = running ? Number.MAX_SAFE_INTEGER - (procs - p) : runEnd;
		runs.push({ id: r + 1, name, environment: "preproduccion", project: "rayen_salud_box_clinico", branch: "main", commit: "c3aec76d", execution_id: "suite-demo",
			status: running ? "RUNNING" : "PASS", started_at: runStart, ...(runEnd ? { ended_at: runEnd } : {}), planned: n, last_activity: now, tests });
	}
	const all = runs.flatMap((r) => r.tests);
	const counts = {};
	for (const t of all) counts[t.state] = (counts[t.state] || 0) + 1;
	const done = all.filter((t) => t.ended_at).length;
	const planned = 270;
	const exec = {
		key: "x:suite-demo", title: "rayen_salud_box_clinico · c3aec76", running: true, started_at: start, project: "rayen_salud_box_clinico",
		environment: "preproduccion", branch: "main", commit: "c3aec76d", planned, total: all.length, done, eta_ms: 6 * MIN, workers: procs, counts,
	};
	return {
		list: { now, no_signal_ms: 180000, executions: [exec, { ...exec, key: "x:anterior", title: "rayen_salud_box_clinico · 9f1e2aa", running: false, counts: { PASS: 250, FAIL: 2 }, done: 252, total: 252 }] },
		execution: { now, execution: { ...exec, runs } },
	};
}

/** El backend de la ejecución (/api/v1/live/endpoints): un endpoint con 504 y los demás sanos. */
function liveSuiteEndpoints() {
	return { endpoints: [
		{ method: "GET", host: "preclinico.demo", path: "/api/atenciones/:id", count: 212, errors: 3, avg_ms: 2100, p95_ms: 30000, max_ms: 30000, test_id: 1001 },
		{ method: "POST", host: "preclinico.demo", path: "/api/recetas", count: 64, errors: 0, avg_ms: 380, p95_ms: 1200, max_ms: 1900, test_id: 1002 },
		{ method: "GET", host: "preclinico.demo", path: "/api/pacientes/:id/ficha", count: 230, errors: 0, avg_ms: 240, p95_ms: 610, max_ms: 900, test_id: 1003 },
		{ method: "GET", host: "preclinico.demo", path: "/box/pacientes-citados", count: 79, errors: 0, avg_ms: 520, p95_ms: 800, max_ms: 1100, test_id: 1004 },
	] };
}

/** El detalle de un test (/api/v1/tests/{id} y su /network) para el panel y su traza. */
function liveSuiteTest(id, now = Date.now()) {
	const t0 = now - 110_000;
	const logs = [
		{ id: 1, status: "INFO", message: "Página de ingreso de Rayen Clínico", timestamp: t0 + 1000, screenshot: "" },
		{ id: 2, status: "INFO", message: "Evidencia de ingreso a menú de paciente citado", timestamp: t0 + 9000, screenshot: "" },
		{ id: 3, status: "INFO", message: "Evidencia de carga de atención actual del paciente", timestamp: t0 + 20000, screenshot: "" },
		{ id: 4, status: "FAIL", message: "TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]", timestamp: t0 + 107000, screenshot: "" },
	];
	const call = (i, method, path, at, ms, status, type = "fetch") => ({ id: i, test_id: id, method, url: `https://preclinico.demo${path}?token=abc`, status, failed: status === 0,
		error_text: status === 0 ? "net::ERR_ABORTED" : "", started_at: t0 + at, duration_ms: ms, resource_type: type });
	const network = [
		call(1, "GET", "/login", 500, 300, 200, "document"), call(2, "POST", "/api/sesion", 2000, 420, 200),
		call(3, "GET", "/static/app.js", 900, 40, 200, "script"), call(4, "GET", "/box/pacientes-citados", 8000, 600, 200, "document"),
		call(5, "GET", "/api/ubicaciones", 8800, 210, 200), call(6, "GET", "/api/pacientes/123/ficha", 15000, 380, 200),
		call(7, "GET", "/api/atenciones/actual", 20500, 30000, 504), call(8, "GET", "/api/atenciones/actual", 52000, 30000, 504),
		call(9, "POST", "/api/telemetria", 90000, 0, 0, "xhr"),
	];
	return { test: { id, run_id: 1, name: "test_001_agenda_paso_1", status: "FAIL", started_at: t0, ended_at: t0 + 108000, logs }, network };
}

module.exports = { liveSuite, liveSuiteEndpoints, liveSuiteTest };
