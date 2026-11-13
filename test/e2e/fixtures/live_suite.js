// Una ejecución grande para En vivo, con la forma de la suite que la motivó: un lanzador que abre una
// corrida por archivo (86) en 12 procesos, sin `worker`, ~270 tests, prefijo común en los nombres y un
// "último paso" largo con URL. Datos inventados y deterministas (sin Math.random): el e2e y las capturas
// ven siempre lo mismo. Se sirve con page.route en vez de /api/v1/live y /api/v1/live/execution.
"use strict";

const MIN = 60_000;

/** {list, execution} para una ejecución en curso que empezó hace 34 min. */
function liveSuite(now = Date.now(), { runs: nRuns = 86, procs = 12 } = {}) {
	const start = now - 34 * MIN;
	const areas = ["product_page", "cart", "search", "checkout_card", "upload_invoice", "wishlist", "order_history", "returns", "coupons", "shipping"];
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
		const name = `Shop · test_${areas[r % areas.length]}_${String(r + 1).padStart(3, "0")}.py`;
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
					: `Evidencia de ${areas[(r + i) % areas.length].replace(/_/g, " ")} del pedido - url: https://staging.shop.test/orders/${r + 1}/${i + 1}?token=abc`,
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
		runs.push({ id: r + 1, name, environment: "preproduccion", project: "acme_shop_web", branch: "main", commit: "c3aec76d", execution_id: "suite-demo",
			status: running ? "RUNNING" : "PASS", started_at: runStart, ...(runEnd ? { ended_at: runEnd } : {}), planned: n, last_activity: now, tests });
	}
	const all = runs.flatMap((r) => r.tests);
	const counts = {};
	for (const t of all) counts[t.state] = (counts[t.state] || 0) + 1;
	const done = all.filter((t) => t.ended_at).length;
	const planned = 270;
	const exec = {
		key: "x:suite-demo", title: "acme_shop_web · c3aec76", running: true, started_at: start, project: "acme_shop_web",
		environment: "preproduccion", branch: "main", commit: "c3aec76d", planned, total: all.length, done, eta_ms: 6 * MIN, workers: procs, counts,
	};
	return {
		list: { now, no_signal_ms: 180000, executions: [exec, { ...exec, key: "x:anterior", title: "acme_shop_web · 9f1e2aa", running: false, counts: { PASS: 250, FAIL: 2 }, done: 252, total: 252 }] },
		execution: { now, execution: { ...exec, runs } },
	};
}

/** El backend de la ejecución (/api/v1/live/endpoints): un endpoint con 504 y los demás sanos. */
function liveSuiteEndpoints() {
	return { endpoints: [
		{ method: "GET", host: "staging.shop.test", path: "/api/orders/:id", count: 212, errors: 3, avg_ms: 2100, p95_ms: 30000, max_ms: 30000, test_id: 1001 },
		{ method: "POST", host: "staging.shop.test", path: "/api/cart", count: 64, errors: 0, avg_ms: 380, p95_ms: 1200, max_ms: 1900, test_id: 1002 },
		{ method: "GET", host: "staging.shop.test", path: "/api/customers/:id/profile", count: 230, errors: 0, avg_ms: 240, p95_ms: 610, max_ms: 900, test_id: 1003 },
		{ method: "GET", host: "staging.shop.test", path: "/account/orders", count: 79, errors: 0, avg_ms: 520, p95_ms: 800, max_ms: 1100, test_id: 1004 },
	] };
}

/** El detalle de un test (/api/v1/tests/{id} y su /network) para el panel y su traza. */
function liveSuiteTest(id, now = Date.now()) {
	const t0 = now - 110_000;
	const logs = [
		{ id: 1, status: "INFO", message: "Página de ingreso de Acme Shop", timestamp: t0 + 1000, screenshot: "" },
		{ id: 2, status: "INFO", message: "Evidencia de ingreso al menú de pedidos", timestamp: t0 + 9000, screenshot: "" },
		{ id: 3, status: "INFO", message: "Evidencia de carga del pedido actual", timestamp: t0 + 20000, screenshot: "" },
		{ id: 4, status: "FAIL", message: "TimeoutException en (//i[contains(@class,'fa-plus-circle')])[1]", timestamp: t0 + 107000, screenshot: "" },
	];
	const call = (i, method, path, at, ms, status, type = "fetch") => ({ id: i, test_id: id, method, url: `https://staging.shop.test${path}?token=abc`, status, failed: status === 0,
		error_text: status === 0 ? "net::ERR_ABORTED" : "", started_at: t0 + at, duration_ms: ms, resource_type: type });
	const network = [
		call(1, "GET", "/login", 500, 300, 200, "document"), call(2, "POST", "/api/session", 2000, 420, 200),
		call(3, "GET", "/static/app.js", 900, 40, 200, "script"), call(4, "GET", "/account/orders", 8000, 600, 200, "document"),
		call(5, "GET", "/api/stores", 8800, 210, 200), call(6, "GET", "/api/customers/123/profile", 15000, 380, 200),
		call(7, "GET", "/api/orders/current", 20500, 30000, 504), call(8, "GET", "/api/orders/current", 52000, 30000, 504),
		call(9, "POST", "/api/telemetria", 90000, 0, 0, "xhr"),
	];
	return { test: { id, run_id: 1, name: "test_001_agenda_paso_1", status: "FAIL", started_at: t0, ended_at: t0 + 108000, logs }, network };
}

/**
 * Una ejecución con pytest-xdist con 20 workers (gw0…gw19), 66 tests en
 * 22 archivos con el prefijo "Shop · test_…_chrome", 1 fallo, warnings, tortuga, sin señal y en curso;
 * pasos con XPaths largos y un volcado de Selenium ("Stacktrace: … (No symbol) [0x7ff…]"). Inventada.
 */
function liveWorkers(now = Date.now()) {
	const start = now - 7 * MIN;
	const areas = ["edit_guest_customer", "register_bulk_purchase_order", "add_fraud_rules", "pay_with_saved_card",
		"register_support_ticket", "load_guest_cart_draft", "edit_bulk_purchase_order", "add_docs_loyalty_programs",
		"product_page_text_templates", "view_new_size_chart", "cancel_pending_orders", "edit_wholesale_order",
		"edit_guest_cart_draft", "add_customer_agreement", "cancel_cart_drafts", "alert_guest_customer",
		"create_edit_purchase_requests", "add_gift_note", "add_edit_remove_products", "register_store_pickup", "create_delivery_schedule", "transfer_customer"];
	const XPATH = "//div[contains(@class, 'rt-tr') and (./div[contains(normalize-space(), 'Ana Demo') or contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), 'pendiente')])]//button[@class=\"text-white grey-btn btn btn-secondary\"]";
	const STACK = "Message: Stacktrace: Symbols not available. Dumping unresolved backtrace: (No symbol) [0x7ff67e619ca5] (No symbol) [0x7ff67e5194f0] (No symbol) [0x7ff67e5ad9f3] (No symbol) [0x7ff67e3c2763]";
	const runs = [];
	let id = 5000, k = 0;
	areas.forEach((area, r) => {
		const name = `Shop · test_${area}_chrome`;
		const tests = [];
		let t0 = start + r * 4000;
		for (let i = 0; i < 3; i++) {
			k++;
			const worker = `gw${(r + i * 7) % 20}`;
			const dur = (35 + ((r * 11 + i * 17) % 80)) * 1000;
			let state = k % 11 === 0 ? "WARNING" : "PASS";
			if (r === 3 && i === 1) state = "FAIL";
			if (r === 12 && i === 0) state = "turtle";
			if (r === 15 && i === 0) state = "no_signal";
			const end = t0 + dur;
			const going = state === "turtle" || state === "no_signal" || end > now || i === 2;
			if (going && (state === "PASS" || state === "WARNING")) state = "running";
			const step = state === "FAIL" ? `No se encontró el elemento '//div[contains(@class, 'modal-dialog')]', detalle del error: ${STACK}`
				: r === 10 && i === 0 ? `Cantidad de pedidos pendientes que comience con el nombre de : el xpath fue: ${XPATH} en Acme Admin - url: https://admin.shop.test/#/mantenedor-agenda`
					: `Evidencia de ${area.replace(/_/g, " ")} - url: https://staging.shop.test/main`;
			const t = { id: ++id, run_id: r + 1, name: `test_00${i + 1}_web_${area}`, worker, state, status: going ? "RUNNING" : state, started_at: t0, attempts: 1,
				p50: dur, p90: dur * 1.4, hist: 6, last_step: step, last_signal: going ? now - (state === "no_signal" ? 4 * MIN : 2000) : end };
			if (going) {
				t.elapsed = now - t0; t.idle = now - t.last_signal;
				t.factor = state === "no_signal" ? 5.2 : state === "turtle" ? 2.6 : t.elapsed / dur;
				tests.push(t);
				break;
			}
			Object.assign(t, { ended_at: end, elapsed: dur, factor: state === "FAIL" ? 1.1 : 1 });
			tests.push(t);
			t0 = end + 1000;
		}
		const running = tests.some((t) => !t.ended_at);
		runs.push({ id: r + 1, name, environment: "preproduccion", project: "acme_shop_web", execution_id: "suite-laptop", status: running ? "RUNNING" : "PASS",
			started_at: start + r * 4000, ...(running ? {} : { ended_at: tests[tests.length - 1].ended_at }), planned: 3, last_activity: now, tests });
	});
	const all = runs.flatMap((r) => r.tests);
	const counts = {};
	for (const t of all) counts[t.state] = (counts[t.state] || 0) + 1;
	const exec = { key: "x:suite-laptop", title: "suite-laptop-20261109-081806", running: true, started_at: start, project: "acme_shop_web",
		environment: "preproduccion", planned: 66, total: all.length, done: all.filter((t) => t.ended_at).length, eta_ms: 3 * MIN, workers: 20, counts };
	return { list: { now, no_signal_ms: 180000, executions: [exec] }, execution: { now, execution: { ...exec, runs } } };
}

module.exports = { liveSuite, liveSuiteEndpoints, liveSuiteTest, liveWorkers };
