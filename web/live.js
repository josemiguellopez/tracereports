// En vivo: lo que corre ahora, por ejecución, en tres niveles (como CI Visibility): (1) el estado de la
// ejecución en tiles que también filtran, (2) dónde está el problema en la línea de tiempo (minimapa con
// zoom y carriles), (3) qué hacer en la tabla, agrupada y ordenada por severidad. "Tortuga": más que su
// p90 o el doble de su p50; "sin señal": el test dejó de enviar. Los estados los calcula el servidor
// (/api/v1/live); aquí solo se ordenan, filtran y dibujan. Todo dato del cliente entra escapado.
//
// Escala (la suite Rayen: 270 tests en 86 corridas de 12 procesos): sin `worker` los carriles se
// reutilizan (una corrida ocupa el primer carril libre cuando empieza), y con más de 20 corridas o
// carriles "Solo con problemas" viene activo: se ve de entrada lo que falla, se cuelga o va lento.
(() => {
	"use strict";

	// estado → clase, ícono, texto (nunca solo color) y severidad (0 = lo primero que hay que mirar)
	const STATES = {
		no_signal: { cls: "nosig", icon: "i-warning", label: "Sin señal", rank: 0 },
		FAIL: { cls: "fail", icon: "i-fail", label: "Fallo", rank: 1 },
		INTERRUPTED: { cls: "interrupted", icon: "i-interrupted", label: "Interrumpido", rank: 2 },
		turtle: { cls: "turtle", icon: "i-running", label: "Tortuga", rank: 3 },
		WARNING: { cls: "warning", icon: "i-warning", label: "Warning", rank: 4 },
		running: { cls: "running", icon: "i-running", label: "En curso", rank: 5 },
		PASS: { cls: "pass", icon: "i-pass", label: "Pass", rank: 6 },
		SKIP: { cls: "skip", icon: "i-skip", label: "Skip", rank: 7 },
	};
	const stateOf = (s) => STATES[s] || { cls: "running", icon: "i-running", label: s, rank: 5 };
	/** Lo que pide acción: con "Solo con problemas" se ve solo esto. */
	const PROBLEM = new Set(["no_signal", "FAIL", "INTERRUPTED", "turtle"]);
	const isProblem = (s) => PROBLEM.has(s);
	/** Los tiles, en el orden de la propuesta; Skip e Interrumpido solo si hay. */
	const TILES = ["PASS", "FAIL", "WARNING", "running", "turtle", "no_signal", "INTERRUPTED", "SKIP"];
	const OPTIONAL_TILES = new Set(["INTERRUPTED", "SKIP"]);
	/** Desde cuántas corridas o carriles "Solo con problemas" viene activo. */
	const AUTO_PROBLEMS = 20;
	const MINIMAP_BUCKETS = 80;
	/** Desde cuántos archivos los que no tienen problemas se juntan en una línea. */
	const COLLAPSE_FROM = 5;

	/** 75 ms → "0 s", 65000 → "1 min 5 s", 3725000 → "1 h 2 min". */
	function fmtDur(ms) {
		const s = Math.max(0, Math.round((ms || 0) / 1000));
		if (s < 60) return `${s} s`;
		const m = Math.floor(s / 60);
		if (m < 60) return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
		return m % 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${Math.floor(m / 60)} h`;
	}

	/** Las corridas con algo que pide acción primero; dentro, sus tests ya vienen ordenados. */
	function sortRuns(runs) {
		const worst = (r) => Math.min(9, ...(r.tests || []).map((t) => stateOf(t.state).rank));
		return [...runs].map((r) => ({ ...r, worst: worst(r) })).sort((a, b) => a.worst - b.worst || b.started_at - a.started_at);
	}

	/** Todos los tests de la ejecución, cada uno con su archivo (el nombre de su corrida). */
	function allTests(exec) {
		return (exec.runs || []).flatMap((r) => (r.tests || []).map((t) => ({ ...t, run_id: t.run_id || r.id, file: r.name })));
	}

	/**
	 * Nombres cortos y distinguibles: sin el prefijo común ("Clinico · test_…" en todos) y, si siguen
	 * largos, con el final (lo distintivo) y "…" adelante. Devuelve un Map nombre → corto.
	 */
	function shortNames(names, max = 34) {
		const uniq = [...new Set(names.filter(Boolean))];
		let pre = "";
		if (uniq.length > 1) {
			pre = uniq.reduce((p, n) => { let i = 0; while (i < p.length && i < n.length && p[i] === n[i]) i++; return p.slice(0, i); });
			const cut = Math.max(pre.lastIndexOf(" "), pre.lastIndexOf("·"), pre.lastIndexOf("/"), pre.lastIndexOf("_"), pre.lastIndexOf("::"));
			pre = cut >= 0 ? pre.slice(0, cut + 1) : ""; // nunca a mitad de palabra
			if (uniq.some((n) => n.length <= pre.length)) pre = "";
		}
		const out = new Map();
		for (const n of uniq) {
			let s = n.slice(pre.length).trim() || n;
			if (s.length > max) s = "…" + s.slice(s.length - max + 1);
			out.set(n, s);
		}
		return out;
	}

	/** El último paso en una línea: sin URLs (largas y con datos) y sin espacios de más. */
	function stepText(s) {
		return String(s || "").replace(/\s*[-–—]?\s*url:\s*\S+/gi, "").replace(/\bhttps?:\/\/\S+/gi, "…").replace(/\s+/g, " ").trim();
	}

	/** Tramo total de la ejecución: [inicio, ahora o su fin]. */
	function span(exec, now) {
		const tests = allTests(exec);
		const from = Math.min(exec.started_at || now, ...tests.map((t) => t.started_at));
		const to = Math.max(from + 1000, exec.running ? now : Math.max(from, ...tests.map((t) => t.ended_at || t.started_at), exec.ended_at || 0));
		return { from, to };
	}

	/**
	 * Carriles: uno por worker si el cliente lo informa. Si no (una corrida por archivo, el caso Rayen),
	 * se reutilizan como procesos: cada corrida, en orden de inicio, ocupa el primer carril que ya
	 * terminó la anterior. 86 corridas en 12 procesos → 12 carriles.
	 */
	function lanesOf(exec, now) {
		const tests = allTests(exec);
		if (tests.some((t) => t.worker)) {
			const byWorker = new Map();
			for (const t of tests) {
				const k = t.worker || t.file;
				if (!byWorker.has(k)) byWorker.set(k, []);
				byWorker.get(k).push(t);
			}
			const natural = (a, b) => a.localeCompare(b, undefined, { numeric: true });
			return [...byWorker.entries()].sort((a, b) => natural(a[0], b[0])).map(([k, ts]) => ({ key: k, label: k, title: k, tests: ts }));
		}
		const runs = [...(exec.runs || [])].sort((a, b) => a.started_at - b.started_at || a.id - b.id);
		const lanes = [];
		for (const r of runs) {
			const ts = (r.tests || []).map((t) => ({ ...t, run_id: t.run_id || r.id, file: r.name }));
			const end = r.ended_at || (r.status && r.status !== "RUNNING" ? Math.max(r.started_at, ...ts.map((t) => t.ended_at || t.started_at)) : now);
			let lane = lanes.find((l) => l.free <= r.started_at);
			if (!lane) { lane = { key: `P${lanes.length + 1}`, free: 0, runs: [], tests: [] }; lanes.push(lane); }
			lane.free = end;
			lane.runs.push(r.name);
			lane.tests.push(...ts);
		}
		const short = shortNames(runs.map((r) => r.name));
		return lanes.map((l) => {
			const current = l.runs[l.runs.length - 1];
			return { key: l.key, label: `${l.key} · ${short.get(current) || current}`, title: l.runs.join("\n"), tests: l.tests };
		});
	}

	/**
	 * El Gantt: carriles con sus barras en % de la ventana visible. zoom = {a, b} en fracciones del
	 * tramo total (null = todo). Cada carril dice si tiene algo que pide acción.
	 */
	function ganttLayout(exec, now, zoom = null) {
		const { from, to } = span(exec, now);
		const total = to - from;
		const a = zoom ? from + total * zoom.a : from, b = zoom ? from + total * zoom.b : to;
		const view = Math.max(1, b - a);
		const names = shortNames(allTests(exec).map((t) => t.name), 40);
		const lanes = lanesOf(exec, now).map((l) => {
			const bars = [];
			for (const t of l.tests) {
				const end = t.ended_at || now;
				if (end < a || t.started_at > b) continue; // fuera de la ventana del zoom
				const left = ((Math.max(t.started_at, a) - a) / view) * 100, right = ((Math.min(end, b) - a) / view) * 100;
				bars.push({ id: t.id, run_id: t.run_id, name: t.name, short: names.get(t.name) || t.name, file: t.file, state: t.state,
					left, width: Math.max(0.4, right - left), elapsed: t.elapsed, factor: t.factor });
			}
			return { key: l.key, worker: l.label, title: l.title, bars, problem: l.tests.some((t) => isProblem(t.state)) };
		});
		return { from, to, a, b, lanes };
	}

	/** Marcas de tiempo del eje: 4-6 horas redondas dentro del tramo. */
	function ticks(from, to) {
		const span = to - from, steps = [10e3, 30e3, 60e3, 2 * 60e3, 5 * 60e3, 10 * 60e3, 15 * 60e3, 30 * 60e3, 3600e3, 2 * 3600e3];
		const step = steps.find((s) => span / s <= 6) || 4 * 3600e3;
		const out = [];
		for (let t = Math.ceil(from / step) * step; t <= to; t += step) out.push({ at: t, left: ((t - from) / span) * 100 });
		return out;
	}

	/**
	 * Minimapa: la ejecución entera en columnas; el alto es cuántos tests corrían en ese momento (frente
	 * al máximo) y cada columna se apila por estado (lo severo abajo, a la vista).
	 */
	function minimap(exec, now, buckets = MINIMAP_BUCKETS) {
		const { from, to } = span(exec, now);
		const step = (to - from) / buckets;
		const tests = allTests(exec);
		const cols = [];
		for (let i = 0; i < buckets; i++) {
			const a = from + i * step, b = a + step;
			const on = tests.filter((t) => t.started_at < b && (t.ended_at || now) >= a);
			const by = new Map();
			for (const t of on) { const st = stateOf(t.state); by.set(st.cls, { cls: st.cls, rank: st.rank, n: (by.get(st.cls)?.n || 0) + 1 }); }
			const parts = [...by.values()].sort((x, y) => x.rank - y.rank).map((x) => ({ cls: x.cls, f: x.n / on.length }));
			cols.push({ left: (i / buckets) * 100, n: on.length, parts });
		}
		const max = Math.max(1, ...cols.map((c) => c.n));
		return cols.map((c) => ({ ...c, h: c.n / max }));
	}

	/** Zoom: + achica la ventana a la mitad, − la duplica (alrededor de su centro); fit = todo. */
	function zoomStep(zoom, dir) {
		if (dir === "fit") return null;
		const z = zoom || { a: 0, b: 1 };
		const c = (z.a + z.b) / 2, w = Math.min(1, Math.max(0.02, (z.b - z.a) * (dir === "in" ? 0.5 : 2)));
		if (w >= 1) return null;
		const a = Math.min(1 - w, Math.max(0, c - w / 2));
		return { a, b: a + w };
	}

	/** Si "Solo con problemas" va activo: lo que eligió la persona, o activo en ejecuciones grandes. */
	function problemsOn(exec, ui, lanes) {
		if (typeof ui.problems === "boolean") return ui.problems;
		return (exec.runs || []).length > AUTO_PROBLEMS || lanes > AUTO_PROBLEMS;
	}

	/**
	 * Los tests de la tabla: los estados elegidos en los tiles; si no hay, con "Solo con problemas" solo
	 * los que piden acción. Una búsqueda mira todos (test, archivo, worker o paso).
	 */
	function filterTests(tests, ui, problems) {
		const q = (ui.q || "").trim().toLowerCase();
		const states = ui.states || [], files = ui.files || [], workers = ui.workers || [], errors = ui.errors || [];
		const faceted = states.length || files.length || workers.length || errors.length;
		return tests.filter((t) => {
			if (q && !`${t.name} ${t.file} ${t.worker || ""} ${t.last_step || ""}`.toLowerCase().includes(q)) return false;
			if (files.length && !files.includes(t.file)) return false;
			if (workers.length && !workers.includes(t.worker)) return false;
			if (errors.length && !(isFailed(t.state) && errors.includes(errorSignature(t.last_step)))) return false;
			if (states.length) return states.includes(t.state);
			if (problems && !q && !faceted) return isProblem(t.state);
			return true;
		});
	}

	/** Grupos de la tabla (archivo, worker o estado), lo más severo primero. */
	function groupTests(tests, by) {
		const key = (t) => (by === "worker" ? t.worker || "—" : by === "state" ? t.state : t.file);
		const groups = new Map();
		for (const t of tests) {
			const k = key(t);
			if (!groups.has(k)) groups.set(k, []);
			groups.get(k).push(t);
		}
		return [...groups.entries()].map(([k, ts]) => {
			const sorted = [...ts].sort((a, b) => stateOf(a.state).rank - stateOf(b.state).rank || (b.factor || 0) - (a.factor || 0));
			const counts = {};
			for (const t of ts) counts[t.state] = (counts[t.state] || 0) + 1;
			return { key: k, tests: sorted, worst: stateOf(sorted[0].state).rank, problem: ts.some((t) => isProblem(t.state)), counts,
				worker: by === "file" ? [...new Set(ts.map((t) => t.worker).filter(Boolean))].join(", ") : "" };
		}).sort((a, b) => a.worst - b.worst || String(a.key).localeCompare(String(b.key), undefined, { numeric: true }));
	}

	/** El progreso: con tests planificados, % y ETA; sin ellos, cuántos terminaron. */
	function progress(exec) {
		if (exec.planned > 0) {
			const pct = Math.min(100, Math.round((exec.done / exec.planned) * 100));
			return { pct, known: true, eta: exec.running && exec.eta_ms ? exec.eta_ms : 0 };
		}
		return { pct: exec.total ? Math.round((exec.done / exec.total) * 100) : 0, known: false, eta: 0 };
	}

	/** La barra de progreso con la mezcla de estados, en % de los planificados (o de los iniciados). */
	function mix(exec) {
		const c = exec.counts || {};
		const base = Math.max(1, exec.planned || 0, exec.total || 0);
		const part = (n) => Math.min(100, ((n || 0) / base) * 100);
		return [
			{ cls: "pass", w: part(c.PASS) }, { cls: "fail", w: part((c.FAIL || 0) + (c.INTERRUPTED || 0)) },
			{ cls: "warning", w: part(c.WARNING) }, { cls: "skip", w: part(c.SKIP) },
			{ cls: "running", w: part((c.running || 0) + (c.turtle || 0) + (c.no_signal || 0)) },
		].filter((s) => s.w > 0);
	}


	// ---------- observabilidad: salud, errores, lentos, actividad, facetas, orden y traza ----------

	/** Percentil p (0-100) de una lista ya ordenada; 0 si está vacía. */
	function percentile(sorted, p) {
		if (!sorted.length) return 0;
		return sorted[Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1))];
	}

	const isFailed = (s) => s === "FAIL" || s === "INTERRUPTED";

	/**
	 * La salud de la ejecución (como el resumen de CI Visibility): tasa de fallo de lo terminado,
	 * duración p50/p95, ritmo (tests por minuto: los últimos 5 min si sigue corriendo) y la tasa de
	 * fallo de la ejecución anterior del mismo proyecto, para ver si empeoró.
	 */
	function health(exec, list, now) {
		const tests = allTests(exec);
		const done = tests.filter((t) => t.ended_at);
		const failed = done.filter((t) => isFailed(t.state)).length;
		const durs = done.map((t) => t.elapsed || 0).sort((a, b) => a - b);
		const rate = (x) => { const d = x.done || 0; return d ? ((x.counts?.FAIL || 0) + (x.counts?.INTERRUPTED || 0)) / d : 0; };
		let perMin = 0;
		if (exec.running) {
			const win = Math.min(5 * 60e3, Math.max(60e3, now - (exec.started_at ?? now)));
			perMin = done.filter((t) => t.ended_at >= now - win).length / (win / 60e3);
		} else if (done.length) {
			const last = Math.max(...done.map((t) => t.ended_at));
			perMin = done.length / Math.max(1 / 60, (last - (exec.started_at ?? last)) / 60e3);
		}
		const prev = (list || []).find((x) => x.key !== exec.key && !x.running && x.done > 0 && x.project === exec.project && x.started_at < exec.started_at);
		const failRate = done.length ? failed / done.length : 0;
		return { done: done.length, failed, failRate, p50: percentile(durs, 50), p95: percentile(durs, 95), perMin,
			prev: prev ? { title: prev.title, failRate: rate(prev), delta: failRate - rate(prev) } : null };
	}

	/**
	 * La firma de un error, para agruparlos (como Error Tracking): la primera línea, sin el "Call log"
	 * de Playwright ni la ruta del módulo, con los números variables como N. El selector se mantiene:
	 * es lo que distingue un fallo de otro.
	 */
	function errorSignature(text) {
		let s = String(text || "").split(/\r?\n/)[0];
		s = s.replace(/\s*Call log:.*$/i, "").replace(/^(?:[\w]+\.)+(\w+(?:Error|Exception))\b/, "$1");
		s = s.replace(/\d+(?:\.\d+)?\s*ms\b/g, "N ms").replace(/\b\d{2,}\b/g, "N").replace(/\s+/g, " ").trim();
		return (s.length > 160 ? s.slice(0, 159) + "…" : s) || "—";
	}

	/** Los fallos de la ejecución agrupados por firma: cuántos, en qué archivos y cuándo. */
	function errorGroups(tests) {
		const by = new Map();
		for (const t of tests) {
			if (!isFailed(t.state)) continue;
			const sig = errorSignature(t.last_step);
			const g = by.get(sig) || { sig, count: 0, files: new Set(), ids: [], first: Infinity, last: 0 };
			g.count++; g.files.add(t.file); g.ids.push(t.id);
			const at = t.ended_at || t.started_at;
			g.first = Math.min(g.first, at); g.last = Math.max(g.last, at);
			by.set(sig, g);
		}
		return [...by.values()].map((g) => ({ ...g, files: [...g.files] })).sort((a, b) => b.count - a.count || b.last - a.last);
	}

	/** Los más lentos frente a su historial (×1.5 de su mediana o más), los que siguen corriendo incluidos. */
	function slowest(tests, n = 5) {
		return tests.filter((t) => t.p50 && (t.factor || 0) >= 1.5).sort((a, b) => b.factor - a.factor).slice(0, n);
	}

	/** Lo que pasó hace poco: terminó (con su estado), dejó de enviar o empezó; lo más nuevo primero. */
	function events(tests, n = 12) {
		const out = [];
		for (const t of tests) {
			if (t.ended_at) out.push({ at: t.ended_at, kind: t.state, test: t });
			else if (t.state === "no_signal") out.push({ at: t.last_signal || t.started_at, kind: "no_signal", test: t });
			else out.push({ at: t.started_at, kind: "start", test: t });
		}
		return out.sort((a, b) => b.at - a.at).slice(0, n);
	}

	/** Las anomalías sobre el minimapa: cada fallo y cada test que dejó de enviar, en % del tramo. */
	function anomalies(exec, now) {
		const { from, to } = span(exec, now);
		return allTests(exec).filter((t) => isFailed(t.state) || t.state === "no_signal").map((t) => {
			const at = t.state === "no_signal" ? (t.last_signal || t.started_at) : (t.ended_at || t.started_at);
			return { id: t.id, run_id: t.run_id, name: t.name, state: t.state, at, left: Math.min(100, Math.max(0, ((at - from) / (to - from)) * 100)) };
		});
	}

	/** Las facetas del explorador (como Datadog): cada valor con cuántos tests tiene, los más frecuentes primero. */
	function facets(tests) {
		const count = (key) => {
			const m = new Map();
			for (const t of tests) { const k = key(t); if (k) m.set(k, (m.get(k) || 0) + 1); }
			return [...m.entries()].sort((a, b) => b[1] - a[1] || String(a[0]).localeCompare(String(b[0]), undefined, { numeric: true }));
		};
		return {
			state: count((t) => t.state).sort((a, b) => stateOf(a[0]).rank - stateOf(b[0]).rank),
			file: count((t) => t.file),
			worker: count((t) => t.worker),
			error: count((t) => (isFailed(t.state) ? errorSignature(t.last_step) : "")),
		};
	}

	/** Orden de la tabla: por estado, nombre, duración o frente a su p50; dir 1 ascendente, -1 descendente. */
	function sortTests(tests, sort) {
		if (!sort?.col) return tests;
		const val = { state: (t) => stateOf(t.state).rank, name: (t) => t.name, elapsed: (t) => t.elapsed || 0, factor: (t) => t.factor || 0 }[sort.col];
		if (!val) return tests;
		return [...tests].sort((a, b) => {
			const x = val(a), y = val(b);
			return (typeof x === "string" ? x.localeCompare(y, undefined, { numeric: true }) : x - y) * (sort.dir || 1);
		});
	}

	/** host + ruta, sin la query (puede llevar tokens). */
	function endpointOf(url) {
		const m = /^[a-z]+:\/\/([^/?#]+)([^?#]*)/i.exec(String(url || ""));
		return m ? m[1] + (m[2] || "/") : String(url || "").split("?")[0];
	}

	/**
	 * La traza del test (como un waterfall de APM): sus pasos y sus llamadas al backend en una misma línea
	 * de tiempo, con el momento del fallo. Sin `all`, solo las llamadas de API y documentos (no scripts,
	 * estilos ni imágenes). Devuelve filas con su posición en % y cuántas quedaron fuera.
	 */
	function traceLayout({ start, end, logs = [], network = [], all = false, max = 120 }) {
		const shown = network.filter((c) => all || ["xhr", "fetch", "document", ""].includes(c.resource_type || ""));
		const items = [
			...logs.map((l) => ({ kind: "step", at: l.timestamp, status: l.status, label: l.message, shot: !!l.screenshot })),
			...shown.map((c) => ({ kind: "req", at: c.started_at, dur: c.duration_ms || 0, status: c.status, failed: !!c.failed || c.status >= 400 || c.status === 0,
				label: `${c.method} ${endpointOf(c.url)}`, code: c.failed && !c.status ? (c.error_text || "ERR") : String(c.status), id: c.id })),
		].filter((x) => x.at).sort((a, b) => a.at - b.at);
		if (!items.length) return { rows: [], hidden: network.length - shown.length, from: start || 0, to: end || 0, failAt: 0 };
		const from = Math.min(start || Infinity, ...items.map((x) => x.at));
		const to = Math.max(from + 1, end || 0, ...items.map((x) => x.at + (x.dur || 0)));
		const pct = (x) => ((x - from) / (to - from)) * 100;
		const fail = logs.find((l) => l.status === "FAIL");
		const rows = items.slice(0, max).map((x) => ({ ...x, left: pct(x.at), width: x.kind === "req" ? Math.max(0.4, pct(x.at + x.dur) - pct(x.at)) : 0, rel: x.at - from }));
		return { rows, hidden: network.length - shown.length, cut: Math.max(0, items.length - max), from, to, failAt: fail ? pct(fail.timestamp) : 0 };
	}


	// ---------- gráficos (columnas apiladas, línea, histograma) y el selector de ejecución ----------

	/** Tramo en columnas de tiempo: un paso redondo (1, 2, 5, 10, 15, 30, 60 min) con 30 columnas como mucho. */
	function timeBuckets(exec, now, max = 30) {
		const { from, to } = span(exec, now);
		const steps = [60e3, 2 * 60e3, 5 * 60e3, 10 * 60e3, 15 * 60e3, 30 * 60e3, 3600e3, 2 * 3600e3, 6 * 3600e3];
		const step = steps.find((s) => (to - from) / s <= max) || 12 * 3600e3;
		const start = Math.floor(from / step) * step;
		return { start, step, n: Math.max(1, Math.ceil((to - start) / step)) };
	}

	/** Resultados por columna de tiempo: los tests que terminaron en cada tramo, por estado. */
	function resultsOverTime(exec, now) {
		const { start, step, n } = timeBuckets(exec, now);
		const cols = Array.from({ length: n }, (_, i) => ({ at: start + i * step, PASS: 0, FAIL: 0, WARNING: 0, SKIP: 0, total: 0 }));
		for (const t of allTests(exec)) {
			if (!t.ended_at) continue;
			const c = cols[Math.min(n - 1, Math.max(0, Math.floor((t.ended_at - start) / step)))];
			const k = isFailed(t.state) ? "FAIL" : t.state === "WARNING" ? "WARNING" : t.state === "SKIP" ? "SKIP" : "PASS";
			c[k]++; c.total++;
		}
		return { step, cols, max: Math.max(1, ...cols.map((c) => c.total)) };
	}

	/** La tasa de fallo acumulada al final de cada tramo (solo desde que terminó el primer test). */
	function failRateOverTime(exec, now) {
		const { step, cols } = resultsOverTime(exec, now);
		let done = 0, failed = 0;
		return { step, points: cols.map((c) => { done += c.total; failed += c.FAIL; return { at: c.at + step, done, failed, rate: done ? failed / done : null }; }) };
	}

	/** Las duraciones de lo terminado en tramos legibles, con dónde caen el p50 y el p95. */
	const DUR_BINS = [[0, 10e3, "<10 s", "<10s"], [10e3, 30e3, "10-30 s", "10-30s"], [30e3, 60e3, "30 s-1 min", "30s-1m"], [60e3, 120e3, "1-2 min", "1-2m"],
		[120e3, 300e3, "2-5 min", "2-5m"], [300e3, 600e3, "5-10 min", "5-10m"], [600e3, Infinity, ">10 min", ">10m"]];
	function durationHistogram(tests) {
		const durs = tests.filter((t) => t.ended_at).map((t) => t.elapsed || 0).sort((a, b) => a - b);
		const bins = DUR_BINS.map(([lo, hi, label, short]) => ({ lo, hi, label, short, n: durs.filter((d) => d >= lo && d < hi).length }));
		const at = (v) => bins.findIndex((b) => v >= b.lo && v < b.hi);
		const p50 = percentile(durs, 50), p95 = percentile(durs, 95);
		return { bins, max: Math.max(1, ...bins.map((b) => b.n)), total: durs.length, p50, p95, p50Bin: durs.length ? at(p50) : -1, p95Bin: durs.length ? at(p95) : -1 };
	}

	/** "hace 3 min", "hace 2 h", "hace 3 d". */
	function ago(ms) {
		const m = Math.max(0, Math.round(ms / 60e3));
		if (m < 1) return "<1 min";
		if (m < 60) return `${m} min`;
		const hh = Math.round(m / 60);
		return hh < 48 ? `${hh} h` : `${Math.round(hh / 24)} d`;
	}

	/** Qué tan reciente es una ejecución: en curso, reciente (< 1 h), de hoy (< 24 h) o anterior. */
	function freshness(x, now) {
		if (x.running) return "live";
		const age = now - (x.ended_at || x.started_at || now);
		return age < 3600e3 ? "recent" : age < 24 * 3600e3 ? "today" : "old";
	}
	const FRESH = { live: "en curso", recent: "reciente", today: "de hoy", old: "anterior" };
	/** Desde cuántas ejecuciones el selector trae buscador. */
	const PICKER_SEARCH_FROM = 8;

	/** El selector de ejecución: un botón con la elegida y la lista con hora, antigüedad, progreso y fallos. */
	function execPickerHTML(data, ui, now, h) {
		const { t, esc, icon } = h;
		const cur = data.exec || data.list.find((x) => x.key === data.key) || data.list[0];
		const pct = (x) => Math.min(100, Math.round(((x.done || 0) / Math.max(1, x.planned || x.total || 0)) * 100));
		const meta = (x) => {
			const f = freshness(x, now);
			const when = `${h.clock(x.started_at)}${h.day && h.day(x.started_at) ? ` · ${h.day(x.started_at)}` : ""}`;
			return { f, when, rel: x.running ? t("empezó hace {t}", { t: ago(now - x.started_at) }) : t("terminó hace {t}", { t: ago(now - (x.ended_at || x.started_at)) }) };
		};
		const badges = (x) => `${x.counts?.FAIL ? `<span class="xp-b fail">${icon("i-fail")}${x.counts.FAIL}</span>` : ""}${x.counts?.no_signal ? `<span class="xp-b nosig">${icon("i-warning")}${x.counts.no_signal}</span>` : ""}`;
		const m = meta(cur);
		// con muchas ejecuciones: buscador y la lista agrupada por antigüedad (en curso, recientes, de hoy, anteriores)
		const searchable = data.list.length > PICKER_SEARCH_FROM;
		const q = searchable ? (ui.pickerQ || "").trim().toLowerCase() : "";
		const found = data.list.map((x, i) => ({ x, i })).filter(({ x }) => !q || [x.title, x.project, x.environment, x.branch, x.execution_id].filter(Boolean).join(" ").toLowerCase().includes(q));
		const GROUP = { live: "En curso", recent: "Recientes", today: "De hoy", old: "Anteriores" };
		let lastGroup = "";
		const options = found.map(({ x, i }) => {
			const g = freshness(x, now);
			const head = searchable && g !== lastGroup ? `<li class="xp-group" role="presentation">${esc(t(GROUP[g]))}</li>` : "";
			lastGroup = g;
			return head + optionHTML(x, i);
		}).join("") || `<li class="xp-none" role="presentation">${esc(t("Ninguna ejecución coincide."))}</li>`;
		function optionHTML(x, i) {
			const mx = meta(x), on = x.key === cur.key;
			return `<li role="option" id="xp-opt-${i}" class="xp-opt ${mx.f} ${on ? "on" : ""}" aria-selected="${on}" data-live-exec="${esc(x.key)}" tabindex="-1">
				<span class="live-dot ${x.running ? "on" : ""}" aria-hidden="true"></span>
				<span class="xp-main"><b data-no-i18n>${esc(x.title)}</b><small><span class="xp-fresh ${mx.f}">${esc(t(FRESH[mx.f]))}</span> · ${esc(mx.when)} · ${esc(mx.rel)}</small></span>
				<span class="xp-side">${badges(x)}<span class="xp-prog" aria-label="${esc(t("{d} de {n} tests", { d: x.done, n: x.planned || x.total }))}"><i style="width:${pct(x)}%"></i></span><small>${x.done}/${x.planned || x.total}</small></span></li>`;
		}
		return `<div class="live-xpick ${ui.pickerOpen ? "open" : ""}">
			<button type="button" class="xp-btn ${m.f}" data-live-xpick aria-haspopup="listbox" aria-expanded="${!!ui.pickerOpen}" aria-label="${esc(t("Ejecución: {e}", { e: cur.title }))}">
				<span class="live-dot ${cur.running ? "on" : ""}" aria-hidden="true"></span>
				<span class="xp-main"><b data-no-i18n>${esc(cur.title)}</b><small><span class="xp-fresh ${m.f}">${esc(t(FRESH[m.f]))}</span> · ${esc(m.when)} · ${esc(m.rel)}</small></span>
				${badges(cur)}${data.list.length > 1 ? `<span class="xp-count">${esc(t("{n} ejecuciones", { n: data.list.length }))}</span>` : ""}<span class="xp-chev" aria-hidden="true">${icon("i-chevron")}</span></button>
			${ui.pickerOpen ? `<div class="xp-pop">${searchable ? `<input type="search" class="xp-q" data-live-xq value="${esc(ui.pickerQ || "")}" placeholder="${esc(t("Buscar ejecución, proyecto o rama…"))}" aria-label="${esc(t("Buscar ejecución"))}">
				<small class="xp-found">${esc(t("{n} de {t}", { n: found.length, t: data.list.length }))}</small>` : ""}
				<ul class="xp-list" role="listbox" aria-label="${esc(t("Ejecuciones"))}">${options}</ul></div>` : ""}</div>`;
	}

	/**
	 * Los gráficos de la ejecución (como los widgets de Datadog): resultados por minuto (columnas apiladas
	 * por estado), la tasa de fallo acumulada (línea, con la de la ejecución anterior como referencia) y la
	 * distribución de duraciones (histograma con p50 y p95). Cada marca con su tooltip; "Ver datos" en tabla.
	 */
	function chartsHTML(e, data, ui, now, h) {
		const { t, esc, icon } = h;
		const tests = allTests(e);
		if (!tests.some((x) => x.ended_at)) return "";
		const tables = ui.tables || {};
		const secs = (ms) => ms < 10 * 60e3;
		const table = (key, head, rows) => tables[key] ? `<table class="lch-table"><thead><tr>${head.map((x) => `<th>${esc(x)}</th>`).join("")}</tr></thead><tbody>${rows.map((r) => `<tr>${r.map((c) => `<td>${esc(c)}</td>`).join("")}</tr>`).join("")}</tbody></table>` : "";
		const toggle = (key) => `<button type="button" class="lf-more" data-live-charttable="${key}" aria-pressed="${!!tables[key]}">${esc(tables[key] ? t("Ocultar datos") : t("Ver datos"))}</button>`;
		const legend = (items) => `<div class="lch-legend">${items.map(([cls, ic, label]) => `<span><i class="sw ${cls}"></i>${icon(ic)}${esc(t(label))}</span>`).join("")}</div>`;

		// (1) resultados por tramo: columnas apiladas por estado
		const r = resultsOverTime(e, now);
		const SEG = [["PASS", "pass", "i-pass", "Pass"], ["WARNING", "warning", "i-warning", "Warning"], ["FAIL", "fail", "i-fail", "Fallo"], ["SKIP", "skip", "i-skip", "Skip"]];
		const shown = SEG.filter(([k]) => r.cols.some((c) => c[k]));
		const stepLabel = t("por {s}", { s: fmtDur(r.step) });
		const colsHTML = r.cols.map((c) => {
			const tip = shown.map(([k, , , label]) => `${t(label)} ${c[k]}`).join(" · ");
			return `<div class="lch-col" tabindex="0" data-tip-title="${esc(t("{n} tests", { n: c.total }))}" data-tip="${esc(`${h.clock(c.at, secs(r.step))} · ${tip}`)}">
				<div class="lch-stack" style="height:${((c.total / r.max) * 100).toFixed(1)}%">${shown.map(([k, cls]) => c[k] ? `<i class="${cls}" style="flex-grow:${c[k]}"></i>` : "").join("")}</div></div>`;
		}).join("");
		// el eje X: una hora cada tantas columnas (4 como mucho), en la posición de su columna, sin cortarse
		const axis = (n, at, center = true) => { const every = Math.max(1, Math.ceil(n / 4));
			return Array.from({ length: n }, (_, i) => i).filter((i) => i % every === 0).map((i) => `<span style="left:${(((i + (center ? 0.5 : 0)) / (center ? n : Math.max(1, n - 1))) * 100).toFixed(2)}%">${esc(h.clock(at(i), false))}</span>`).join(""); };
		const c1 = `<section class="card live-chart"><header><h6>${esc(t("Resultados en el tiempo"))}</h6><span class="m-hint">${esc(stepLabel)}</span><span class="live-spacer"></span>${toggle("results")}</header>
			${legend(shown.map(([, cls, ic, label]) => [cls, ic, label]))}
			<div class="lch-plot"><div class="lch-y"><span>${r.max}</span><span>0</span></div><div class="lch-cols" role="img" aria-label="${esc(t("Resultados en el tiempo"))}">${colsHTML}</div></div>
			<div class="lch-x">${axis(r.cols.length, (i) => r.cols[i].at)}</div>
			${table("results", [t("Desde"), ...shown.map(([, , , l]) => t(l)), t("Total")], r.cols.map((c) => [h.clock(c.at, secs(r.step)), ...shown.map(([k]) => String(c[k])), String(c.total)]))}</section>`;

		// (2) tasa de fallo acumulada: línea con área tenue y la anterior como referencia
		const fr = failRateOverTime(e, now);
		const pts = fr.points.map((p, i) => ({ ...p, i })).filter((p) => p.rate !== null);
		const prev = health(e, data.list, now).prev;
		const top = Math.max(0.05, ...pts.map((p) => p.rate), prev ? prev.failRate : 0) * 1.15;
		const n = fr.points.length;
		const X = (i) => (n === 1 ? 50 : (i / (n - 1)) * 100), Y = (v) => 100 - (v / top) * 100;
		const line = pts.map((p, k) => `${k ? "L" : "M"}${X(p.i).toFixed(2)},${Y(p.rate).toFixed(2)}`).join(" ");
		const area = pts.length ? `${line} L${X(pts[pts.length - 1].i).toFixed(2)},100 L${X(pts[0].i).toFixed(2)},100 Z` : "";
		const last = pts[pts.length - 1];
		const hits = fr.points.map((p, i) => `<span class="lch-hit" tabindex="0" style="left:${X(i).toFixed(2)}%" data-tip-title="${esc(p.rate === null ? "—" : pctOf(p.rate))}"
			data-tip="${esc(`${h.clock(p.at, secs(fr.step))} · ${t("{f} de {n} terminados", { f: p.failed, n: p.done })}`)}"></span>`).join("");
		const c2 = `<section class="card live-chart"><header><h6>${esc(t("Tasa de fallo acumulada"))}</h6>${last ? `<span class="lch-now">${esc(pctOf(last.rate))}</span>` : ""}<span class="live-spacer"></span>${toggle("rate")}</header>
			${prev ? `<div class="lch-legend"><span><i class="sw ref"></i>${esc(t("anterior: {r}", { r: pctOf(prev.failRate) }))}</span></div>` : ""}
			<div class="lch-plot"><div class="lch-y"><span>${esc(pctOf(top))}</span><span>0%</span></div>
				<div class="lch-line" role="img" aria-label="${esc(t("Tasa de fallo acumulada"))}">
					<svg viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
						${prev ? `<line class="ref" x1="0" x2="100" y1="${Y(prev.failRate).toFixed(2)}" y2="${Y(prev.failRate).toFixed(2)}" vector-effect="non-scaling-stroke"></line>` : ""}
						${area ? `<path class="area" d="${area}"></path><path class="ln" d="${line}" vector-effect="non-scaling-stroke"></path>` : ""}</svg>
					${last ? `<span class="lch-dot" style="left:${X(last.i).toFixed(2)}%;top:${Y(last.rate).toFixed(2)}%"></span>` : ""}${hits}</div></div>
			<div class="lch-x">${axis(n, (i) => fr.points[i].at, false)}</div>
			${table("rate", [t("Hasta"), t("Terminados"), t("Fallos"), t("Tasa")], fr.points.map((p) => [h.clock(p.at, secs(fr.step)), String(p.done), String(p.failed), p.rate === null ? "—" : pctOf(p.rate)]))}</section>`;

		// (3) distribución de duraciones: histograma con p50 y p95 marcados
		const d = durationHistogram(tests);
		const bars = d.bins.map((b, i) => `<div class="lch-col" tabindex="0" data-tip-title="${esc(t("{n} tests", { n: b.n }))}" data-tip="${esc(`${b.label}${i === d.p50Bin ? " · p50" : ""}${i === d.p95Bin ? " · p95" : ""}`)}">
				${i === d.p50Bin || i === d.p95Bin ? `<span class="lch-mark">${[i === d.p50Bin ? "p50" : "", i === d.p95Bin ? "p95" : ""].filter(Boolean).join(" · ")}</span>` : ""}
				<div class="lch-stack" style="height:${((b.n / d.max) * 100).toFixed(1)}%">${b.n ? `<i class="dur" style="flex-grow:1"></i>` : ""}</div></div>`).join("");
		const c3 = `<section class="card live-chart"><header><h6>${esc(t("Duración de los tests"))}</h6><span class="m-hint">${esc(t("p50 {a} · p95 {b}", { a: fmtDur(d.p50), b: fmtDur(d.p95) }))}</span><span class="live-spacer"></span>${toggle("dur")}</header>
			<div class="lch-plot"><div class="lch-y"><span>${d.max}</span><span>0</span></div><div class="lch-cols hist" role="img" aria-label="${esc(t("Duración de los tests"))}">${bars}</div></div>
			<div class="lch-x">${d.bins.map((b, i) => `<span style="left:${(((i + 0.5) / d.bins.length) * 100).toFixed(2)}%">${esc(b.short)}</span>`).join("")}</div>
			${table("dur", [t("Duración"), t("Tests")], d.bins.map((b) => [b.label, String(b.n)]))}</section>`;
		return `<div class="live-charts">${c1}${c2}${c3}</div>`;
	}

	// ---------- dibujo ----------

	/**
	 * Dibuja la vista. data: {list, exec, now, no_signal_ms, ui, panel}; h: {t, esc, icon, canAct, clock(ms)}.
	 * ui: {problems, states[], group, q, zoom, allLanes, showOk, open{}, panel}. Devuelve el HTML (app.js
	 * lo pone en #view-live y maneja los clics).
	 */
	function render(data, h) {
		const { t, esc, icon } = h;
		const now = data.now || Date.now();
		const ui = { states: [], files: [], workers: [], errors: [], group: "file", q: "", open: {}, more: {}, ...(data.ui || {}) };
		if (!data.list.length) {
			return `<div class="card live-empty">${icon("i-running")}<h3>${esc(t("No hay ejecuciones en curso"))}</h3>
				<p class="m-hint">${esc(t("Cuando un cliente empiece a enviar resultados, aquí ves qué terminó, qué sigue corriendo, qué está lento y qué dejó de enviar, por worker y por test."))}</p></div>`;
		}
		const e = data.exec;
		const selector = execPickerHTML(data, ui, now, h);
		if (!e) return `<div class="card live-top">${selector}</div><p class="m-hint">${esc(t("Cargando…"))}</p>`;

		const tests = allTests(e);
		const g = ganttLayout(e, now, ui.zoom);
		const problems = problemsOn(e, ui, g.lanes.length);

		// (1) cabecera: ejecución, estado, meta, cancelar; tiles con la mezcla de estados
		const p = progress(e);
		const meta = [e.project, e.environment, e.branch, e.commit && e.commit.slice(0, 7)].filter(Boolean).map((x) => `<span data-no-i18n>${esc(x)}</span>`).join(" · ");
		const extra = [t("{n} corridas", { n: (e.runs || []).length }), t("{n} workers", { n: e.workers }), fmtDur((e.running ? now : e.ended_at) - e.started_at)].map(esc).join(" · ");
		const cancel = e.running && h.canAct()
			? (e.cancel_requested
				? `<span class="live-cancelled">${icon("i-pause")}${esc(t("Cancelación pedida: cada proceso se detendrá al terminar el test en curso y saltará los que faltan, también los que empiecen después."))}</span>`
				: `<details class="live-cancel"><summary class="cf-btn cf-btn-sm">${icon("i-pause")}${esc(t("Cancelar ejecución…"))}</summary>
					<form data-live-cancel><p class="m-hint">${esc(t("Se detendrá al terminar el test en curso: los clientes con TRACEREPORTS_REMOTE_CONTROL=1 ven el pedido entre un test y otro, marcan los que faltan como Skip y cierran la ejecución. Alcanza también a las corridas que la ejecución abra después. No se mata ningún proceso."))}</p>
					<input name="reason" maxlength="300" placeholder="${esc(t("Motivo (opcional)"))}" aria-label="${esc(t("Motivo"))}">
					<button class="cf-btn cf-btn-sm cf-btn-danger" type="submit">${esc(t("Pedir cancelación"))}</button></form></details>`)
			: "";
		const pctText = `${p.known ? `${esc(t("{d} de {n} tests", { d: e.done, n: e.planned }))} · ${p.pct}%` : esc(t("{d} terminados de {n} iniciados", { d: e.done, n: e.total }))}`;
		const tiles = TILES.filter((k) => !OPTIONAL_TILES.has(k) || e.counts[k]).map((k) => {
			const st = stateOf(k), on = ui.states.includes(k);
			const sub = k === "no_signal" && e.counts[k] ? ` ${esc(t("hace {t}", { t: fmtDur(Math.max(0, ...tests.filter((x) => x.state === k).map((x) => x.idle || 0))) }))}` : "";
			return `<button type="button" class="live-tile live-count ${st.cls} ${on ? "on" : ""} ${e.counts[k] ? "" : "zero"}" data-live-state="${k}" aria-pressed="${on}"
				data-tip="${esc(t("Clic: ver solo estos tests"))}"><b data-count="${k}" data-n="${e.counts[k] || 0}">${e.counts[k] || 0}</b><span>${icon(st.icon)}${esc(t(st.label))}${sub}</span></button>`;
		}).join("");
		const head = `<div class="card live-head">
			<div class="live-title">${selector}
				${e.running ? `<span class="live-badge on">${icon("i-running")}${esc(t("EN CURSO"))}</span>` : `<span class="live-badge">${esc(t("Terminada"))}</span>`}
				<h3 data-no-i18n>${esc(e.title)}</h3><span class="live-meta">${meta}${meta ? " · " : ""}${extra}</span>${cancel}</div>
			<div class="live-tiles">
				<div class="live-tile live-prog" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${p.pct}" aria-label="${esc(t("Progreso"))}">
					<span class="live-prog-l">${esc(t("Progreso"))}${p.eta ? ` · ${esc(t("ETA {t}", { t: fmtDur(p.eta) }))}` : ""}</span>
					<div class="live-bar">${mix(e).map((s) => `<i class="${s.cls}" style="width:${s.w.toFixed(2)}%"></i>`).join("")}</div>
					<span class="live-pct">${pctText}</span></div>${tiles}</div>${healthHTML(e, data.list, now, h)}</div>`;

		// (2) filtros: solo con problemas, agrupar, buscar
		const nProblems = tests.filter((x) => isProblem(x.state)).length;
		const groupBtn = (k, l) => `<button type="button" class="cf-btn cf-btn-sm ${ui.group === k ? "on" : ""}" data-live-groupby="${k}" aria-pressed="${ui.group === k}">${esc(t(l))}</button>`;
		const filters = `<div class="card live-filters">
			<button type="button" class="live-chip ${problems ? "on" : ""}" data-live-problems aria-pressed="${problems}">${esc(t("Solo con problemas ({n})", { n: nProblems }))}</button>
			${activeChips(ui, h)}
			<span class="live-spacer"></span>
			<button type="button" class="cf-btn cf-btn-sm live-pause ${ui.paused ? "on" : ""}" data-live-pause aria-pressed="${!!ui.paused}">${icon(ui.paused ? "i-running" : "i-pause")}${esc(ui.paused ? t("Reanudar") : t("Pausar"))}</button>
			<span class="m-hint">${esc(t("Agrupar:"))}</span>${groupBtn("file", "Archivo")}${groupBtn("worker", "Worker")}${groupBtn("state", "Estado")}
			<input type="search" class="live-q" data-live-q value="${esc(ui.q)}" placeholder="${esc(t("Buscar test, archivo o paso…"))}" aria-label="${esc(t("Buscar"))}">
			${ui.paused ? `<p class="live-paused" role="status">${icon("i-pause")}${esc(t("En pausa: la vista no se actualiza. Los datos siguen llegando al servidor."))}</p>` : ""}</div>`;

		// (3) línea de tiempo: minimapa con el recuadro del zoom, carriles (los sin problemas colapsados)
		let gantt = "";
		if (g.lanes.length) {
			const mm = minimap(e, now);
			const brush = ui.zoom ? `<div class="lg-brush" style="left:${ui.zoom.a * 100}%;width:${(ui.zoom.b - ui.zoom.a) * 100}%"></div>` : "";
			const shown = problems && !ui.allLanes ? g.lanes.filter((l) => l.problem) : g.lanes;
			const hidden = g.lanes.length - shown.length;
			const laneHTML = (l) => `<div class="lg-lane"><span class="lg-worker" data-no-i18n title="${esc(l.title)}">${esc(l.worker)}</span><div class="lg-track">${l.bars.map((b) => {
				const st = stateOf(b.state);
				const tip = `${b.name} · ${b.file} · ${t(st.label)} · ${fmtDur(b.elapsed)}${b.factor ? ` · ×${b.factor.toFixed(1)} p50` : ""}`;
				return `<button type="button" class="lg-bar ${st.cls}" data-live-test="${b.id}" data-live-run="${b.run_id}" style="left:${b.left.toFixed(3)}%;width:${b.width.toFixed(3)}%" data-tip="${esc(tip)}" aria-label="${esc(tip)}"><span data-no-i18n>${esc(b.short)}</span></button>`;
			}).join("")}</div></div>`;
			gantt = `<div class="card live-gantt">
				<div class="lg-head"><h6>${esc(t("Línea de tiempo"))}</h6><span class="m-hint">${ui.zoom ? esc(t("zoom {a}–{b}", { a: h.clock(g.a), b: h.clock(g.b) })) : esc(t("{n} carriles", { n: g.lanes.length }))}</span>
					<span class="live-spacer"></span>
					<button type="button" class="cf-btn cf-btn-sm" data-live-zoom="out" aria-label="${esc(t("Alejar"))}" ${ui.zoom ? "" : "disabled"}>−</button>
					<button type="button" class="cf-btn cf-btn-sm" data-live-zoom="in" aria-label="${esc(t("Acercar"))}">+</button>
					<button type="button" class="cf-btn cf-btn-sm" data-live-zoom="fit" ${ui.zoom ? "" : "disabled"}>${esc(t("Ajustar"))}</button></div>
				<div class="lg-mini" data-live-minimap data-tip="${esc(t("Arrastra para acercar un tramo; clic para centrar"))}">${mm.map((c) => c.n ? `<i style="left:${c.left.toFixed(2)}%;height:${Math.max(8, c.h * 100).toFixed(0)}%">${c.parts.map((x) => `<b class="${x.cls}" style="height:${(x.f * 100).toFixed(1)}%"></b>`).join("")}</i>` : "").join("")}${brush}
					${anomalies(e, now).map((a) => { const st = stateOf(a.state), tip = `${t(st.label)} · ${a.name} · ${h.clock(a.at, true)}`; return `<button type="button" class="lg-anom ${st.cls}" data-live-test="${a.id}" data-live-run="${a.run_id}" style="left:${a.left.toFixed(2)}%" data-tip="${esc(tip)}" aria-label="${esc(tip)}">${a.state === "no_signal" ? "!" : "✕"}</button>`; }).join("")}</div>
				<div class="lg-axis" aria-hidden="true">${ticks(g.a, g.b).map((k) => `<span style="left:${k.left}%">${esc(h.clock(k.at, g.b - g.a < 10 * 60e3))}</span>`).join("")}</div>
				<div class="lg-lanes">${shown.map(laneHTML).join("")}
				${hidden ? `<button type="button" class="lg-more" data-live-alllanes>${icon("i-chevron")}${esc(t("{n} carriles sin problemas", { n: hidden }))} · ${esc(t("mostrar"))}</button>` : ""}
				${problems && ui.allLanes && g.lanes.some((l) => !l.problem) ? `<button type="button" class="lg-more" data-live-alllanes>${esc(t("Ocultar los carriles sin problemas"))}</button>` : ""}
				${!shown.length && !hidden ? `<p class="m-hint">${esc(t("Nada en este tramo."))}</p>` : ""}</div></div>`;
		}

		// tabla: grupos por severidad; los sin problemas colapsados en una línea
		const visible = filterTests(tests, ui, problems);
		const faceted = ui.states.length || ui.files.length || ui.workers.length || ui.errors.length;
		const groups = ui.sort?.col ? [] : groupTests(visible, ui.group);
		// con muchos archivos, los sin problemas en una sola línea (con pocos, se ven todos)
		const plainGroups = ui.group === "file" && !problems && !faceted && !ui.q && !ui.showOk && groups.length > COLLAPSE_FROM ? groups.filter((x) => !x.problem) : [];
		const listed = groups.filter((x) => !plainGroups.includes(x));
		const row = (x) => {
			const st = stateOf(x.state);
			const vs = x.p50 ? `<span class="lt-vs ${x.factor >= 2 ? "hi" : ""}">×${x.factor.toFixed(1)}</span>` : `<span class="m-hint">—</span>`;
			const ago = x.state === "running" || x.state === "turtle" || x.state === "no_signal" ? ` <small>${esc(t("hace {t}", { t: fmtDur(x.idle) }))}</small>` : "";
			const step = stepText(x.last_step);
			return `<tr class="lt-row ${st.cls} ${data.panel?.id === x.id ? "sel" : ""}" data-live-test="${x.id}" data-live-run="${x.run_id}" tabindex="0">
				<td class="lt-name"><a href="#run=${x.run_id}&view=tests&test=${x.id}" data-no-i18n title="${esc(x.name)}">${esc(x.name)}</a>${x.worker && ui.group !== "worker" ? ` <span class="m-hint" data-no-i18n>${esc(x.worker)}</span>` : ""}</td>
				<td><span class="lt-state ${st.cls}">${icon(st.icon)}${esc(t(st.label))}</span></td>
				<td class="lt-num">${esc(fmtDur(x.elapsed))}</td><td class="lt-num">${vs}</td>
				<td class="lt-step"><span data-no-i18n title="${esc(x.last_step || "")}">${esc(step || "—")}</span>${ago}</td></tr>`;
		};
		const groupHTML = (gr) => {
			const open = gr.key in ui.open ? ui.open[gr.key] : gr.problem || listed.length <= 3 || !!ui.q || faceted > 0;
			const sum = Object.keys(STATES).filter((k) => gr.counts[k]).map((k) => `<span class="lt-state ${stateOf(k).cls}">${icon(stateOf(k).icon)}${gr.counts[k]}</span>`).join(" ");
			const name = ui.group === "state" ? t(stateOf(gr.key).label) : gr.key;
			return `<tbody class="live-run ${open ? "open" : ""}"><tr class="lt-group"><td colspan="5"><button type="button" class="live-run-h" data-live-group="${esc(gr.key)}" aria-expanded="${open}">
					<span class="live-chev" aria-hidden="true">${icon("i-chevron")}</span><span class="live-sev ${stateOf(gr.tests[0].state).cls}" aria-hidden="true"></span><b data-no-i18n>${esc(name)}</b>
					<span class="live-run-sum">${sum}${gr.worker ? ` · <span data-no-i18n>${esc(gr.worker)}</span>` : ""}</span></button></td></tr>
				${open ? gr.tests.map(row).join("") : ""}</tbody>`;
		};
		const emptyRuns = (e.runs || []).filter((r) => !(r.tests || []).length);
		const hiddenTests = tests.length - visible.length;
		let collapsed = "";
		if (plainGroups.length) {
			const n = plainGroups.reduce((s, x) => s + x.tests.length, 0), w = plainGroups.filter((x) => x.counts.WARNING).length;
			collapsed = `<button type="button" class="live-collapsed" data-live-showok>${icon("i-chevron")}${esc(t("{f} archivos sin problemas ({n} tests)", { f: plainGroups.length, n }))}${w ? ` · ${esc(t("{n} con warning", { n: w }))}` : ""} · ${esc(t("mostrar"))}</button>`;
		} else if (problems && !faceted && !ui.q && hiddenTests > 0) {
			collapsed = `<button type="button" class="live-collapsed" data-live-problems>${icon("i-chevron")}${esc(t("{n} tests sin problemas ocultos", { n: hiddenTests }))} · ${esc(t("mostrar todos"))}</button>`;
		} else if (ui.showOk && ui.group === "file" && !problems && !faceted && !ui.q && groups.length > COLLAPSE_FROM && groups.some((x) => !x.problem)) {
			collapsed = `<button type="button" class="live-collapsed" data-live-showok>${esc(t("Ocultar los archivos sin problemas"))}</button>`;
		}
		const sorted = ui.sort?.col ? sortTests(visible, ui.sort) : [];
		const nothing = !listed.length && !plainGroups.length && !sorted.length
			? `<p class="m-hint live-none">${esc(problems && !faceted && !ui.q ? t("Ningún test con problemas por ahora.") : t("Ningún test coincide con el filtro."))}</p>` : "";
		const th = (col, label, tip = "") => {
			const on = ui.sort?.col === col, dir = on ? (ui.sort.dir > 0 ? "ascending" : "descending") : "none";
			return `<th aria-sort="${dir}"${tip ? ` title="${esc(tip)}"` : ""}><button type="button" class="lt-sort ${on ? "on" : ""}" data-live-sort="${col}">${esc(t(label))}${on ? (ui.sort.dir > 0 ? " ▲" : " ▼") : ""}</button></th>`;
		};
		const thead = `<thead><tr>${th("name", "Test")}${th("state", "Estado")}${th("elapsed", "Dura.")}${th("factor", "vs p50", t("Duración frente a la mediana de sus ejecuciones anteriores"))}<th>${esc(t("Último paso"))}</th></tr></thead>`;
		const MAX_ROWS = 500;
		const flat = sorted.length ? `<table class="live-table">${thead}<tbody>${sorted.slice(0, MAX_ROWS).map(row).join("")}</tbody></table>
			${sorted.length > MAX_ROWS ? `<p class="m-hint">${esc(t("Mostrando {n} de {t}: filtra o busca para acotar.", { n: MAX_ROWS, t: sorted.length }))}</p>` : ""}
			<button type="button" class="live-collapsed" data-live-sort="">${esc(t("Volver a agrupar"))}</button>` : "";
		const table = `<div class="card live-runs"><h6>${esc(ui.group === "worker" ? t("Worker / test") : ui.group === "state" ? t("Estado / test") : t("Archivo / test"))} <span class="m-hint">${esc(t("{n} de {t} tests", { n: visible.length, t: tests.length }))}</span></h6>
			${flat}${listed.length ? `<table class="live-table">${thead}
				${listed.map(groupHTML).join("")}</table>` : ""}${collapsed}${nothing}
			${emptyRuns.length && ui.group === "file" && !problems ? emptyRuns.map((r) => `<div class="live-run"><div class="live-run-h"><b data-no-i18n>${esc(r.name)}</b> <span class="m-hint">${esc(t("Todavía sin tests."))}</span></div></div>`).join("") : ""}
			<p class="m-hint">${esc(t("Tortuga: más lento que su p90 (o el doble de su mediana) en sus ejecuciones anteriores. Sin señal: nada del test hace {n} min.", { n: Math.round((data.no_signal_ms || 180000) / 60000 * 10) / 10 }))}</p></div>`;

		const explore = `<div class="live-explore">${facetsHTML(tests, ui, h)}${table}</div>`;
		return `${head}${filters}${insightsHTML(e, tests, data, ui, now, h)}${chartsHTML(e, data, ui, now, h)}${gantt}${explore}${panelHTML(data.panel, tests, h, now)}`;
	}

	/** El estado de un paso: los de test (Pass, Fallo…) o informativo. */
	const stepState = (s) => STATES[s] || { cls: "info", icon: "i-info" };
	const pctOf = (x) => `${(x * 100).toFixed(x > 0 && x < 0.1 ? 1 : 0)}%`;

	/** La franja de salud bajo los tiles: tasa de fallo (frente a la anterior), p50/p95 y ritmo. */
	function healthHTML(e, list, now, h) {
		const { t, esc } = h;
		const s = health(e, list, now);
		if (!s.done) return "";
		let delta = "";
		if (s.prev) {
			const pts = s.prev.delta * 100;
			const cls = Math.abs(pts) < 0.05 ? "eq" : pts > 0 ? "worse" : "better";
			const txt = cls === "eq" ? t("igual que la anterior") : t("{d} pts frente a la anterior", { d: `${pts > 0 ? "▲ +" : "▼ "}${pts.toFixed(1)}` });
			delta = `<em class="lh-delta ${cls}" title="${esc(t("Ejecución anterior: {e} · {r} de fallo", { e: s.prev.title, r: pctOf(s.prev.failRate) }))}">${esc(txt)}</em>`;
		}
		const item = (label, value, extra = "", cls = "") => `<span class="lh ${cls}"><small>${esc(label)}</small><b data-bump="${esc(label)}">${value}</b>${extra}</span>`;
		return `<div class="live-health" aria-label="${esc(t("Salud de la ejecución"))}">
			${item(t("Tasa de fallo"), esc(pctOf(s.failRate)), `<em>${esc(t("{f} de {n} terminados", { f: s.failed, n: s.done }))}</em>${delta}`, s.failed ? "bad" : "")}
			${item(t("Duración p50 · p95"), `${esc(fmtDur(s.p50))} · ${esc(fmtDur(s.p95))}`)}
			${item(t("Ritmo"), esc(t("{n} tests/min", { n: s.perMin.toFixed(1) })), `<em>${esc(e.running ? t("últimos 5 min") : t("promedio"))}</em>`)}</div>`;
	}

	/** Los filtros activos como chips que se quitan con un clic, y "Quitar filtros". */
	function activeChips(ui, h) {
		const { t, esc, icon } = h;
		const chips = [
			...ui.states.map((v) => ["state", v, t(stateOf(v).label)]),
			...ui.files.map((v) => ["file", v, v]), ...ui.workers.map((v) => ["worker", v, v]),
			...ui.errors.map((v) => ["error", v, v.length > 48 ? v.slice(0, 47) + "…" : v]),
		];
		if (!chips.length) return "";
		return chips.map(([k, v, label]) => `<button type="button" class="live-chip on facet" data-live-facet="${k}" data-v="${esc(v)}" aria-label="${esc(t("Quitar filtro: {f}", { f: label }))}"><span data-no-i18n>${esc(label)}</span>${icon("i-close")}</button>`).join("")
			+ (chips.length > 1 ? `<button type="button" class="live-chip" data-live-clear>${esc(t("Quitar filtros"))}</button>` : "");
	}

	/**
	 * Las tarjetas de diagnóstico (como el overview de Datadog): errores agrupados por firma, los más
	 * lentos frente a su historial (y los reintentos), el backend de la ejecución y la actividad reciente.
	 */
	function insightsHTML(e, tests, data, ui, now, h) {
		const { t, esc, icon } = h;
		const card = (cls, title, sub, body) => `<section class="card live-card ${cls}"><h6>${esc(title)}${sub ? ` <span class="m-hint">${esc(sub)}</span>` : ""}</h6>${body}</section>`;
		const empty = (txt) => `<p class="m-hint live-card-empty">${esc(txt)}</p>`;

		const errs = errorGroups(tests);
		const errBody = errs.length ? `<ol class="lc-list">${errs.slice(0, 5).map((g) => `<li><button type="button" class="lc-row ${ui.errors.includes(g.sig) ? "on" : ""}" data-live-facet="error" data-v="${esc(g.sig)}" aria-pressed="${ui.errors.includes(g.sig)}">
				<b class="lc-count fail">${g.count}</b><span class="lc-main"><code data-no-i18n title="${esc(g.sig)}">${esc(g.sig)}</code>
				<small>${esc(g.files.length === 1 ? g.files[0] : t("{n} archivos", { n: g.files.length }))} · ${esc(t("último {h}", { h: h.clock(g.last, true) }))}</small></span></button></li>`).join("")}</ol>
				${errs.length > 5 ? `<p class="m-hint">${esc(t("y {n} errores más", { n: errs.length - 5 }))}</p>` : ""}`
			: empty(t("Sin fallos por ahora."));

		const slow = slowest(tests);
		const retried = tests.filter((x) => (x.attempts || 0) > 1);
		const slowBody = (slow.length ? `<ol class="lc-list">${slow.map((x) => `<li><button type="button" class="lc-row" data-live-test="${x.id}" data-live-run="${x.run_id}">
				<span class="lt-vs ${x.factor >= 2 ? "hi" : ""}">×${x.factor.toFixed(1)}</span><span class="lc-main"><span data-no-i18n>${esc(x.name)}</span>
				<small>${esc(fmtDur(x.elapsed))} · ${esc(t("mediana {m}", { m: fmtDur(x.p50) }))}${x.ended_at ? "" : ` · ${esc(t("sigue corriendo"))}`}</small></span></button></li>`).join("")}</ol>`
			: empty(t("Ningún test más lento que su historial."))) + (retried.length
			? `<p class="lc-retry">${icon("i-warning")}${esc(t("{n} con reintentos: {l}", { n: retried.length, l: retried.slice(0, 3).map((x) => x.name).join(", ") + (retried.length > 3 ? "…" : "") }))}</p>` : "");

		const eps = data.endpoints;
		const testRun = new Map(tests.map((x) => [x.id, x.run_id]));
		const epBody = !eps ? empty(t("Cargando…")) : eps.length ? `<table class="lc-ep"><colgroup><col class="c-ep"><col class="c-n"><col class="c-err"><col class="c-n"></colgroup><thead><tr><th>${esc(t("Endpoint"))}</th><th class="lt-num">${esc(t("Llamadas"))}</th><th class="lt-num">${esc(t("Errores"))}</th><th class="lt-num">p95</th></tr></thead><tbody>
				${eps.slice(0, 6).map((x) => `<tr class="${x.errors ? "bad" : ""}"><td><a href="#run=${testRun.get(x.test_id) || ""}&view=tests&test=${x.test_id}&tab=network" title="${esc(`${x.method} ${x.host}${x.path}`)}" data-no-i18n><b>${esc(x.method)}</b> ${esc(x.path)}</a></td>
				<td class="lt-num">${x.count}</td><td class="lt-num">${x.errors ? `<span class="lc-err">${x.errors} · ${esc(pctOf(x.errors / x.count))}</span>` : "0"}</td><td class="lt-num">${esc(fmtDur(x.p95_ms))}</td></tr>`).join("")}</tbody></table>`
			: empty(t("Sin llamadas al backend registradas (el cliente captura la red con Playwright)."));

		const ev = events(tests);
		const evLabel = { start: t("empezó"), no_signal: t("dejó de enviar"), FAIL: t("falló"), INTERRUPTED: t("se interrumpió"), WARNING: t("terminó con warning"), PASS: t("pasó"), SKIP: t("se saltó") };
		const evBody = ev.length ? `<ol class="lc-feed">${ev.map((x) => { const st = x.kind === "start" ? stateOf("running") : stateOf(x.kind);
			return `<li data-ev="${x.test.id}:${x.kind}"><button type="button" class="lc-row" data-live-test="${x.test.id}" data-live-run="${x.test.run_id}"><small class="lc-time">${esc(h.clock(x.at, true))}</small>
				<span class="lt-state ${st.cls}">${icon(st.icon)}</span><span class="lc-main"><span data-no-i18n>${esc(x.test.name)}</span> <small>${esc(evLabel[x.kind] || x.kind)}</small></span></button></li>`; }).join("")}</ol>`
			: empty(t("Todavía sin actividad."));

		return `<div class="live-insights">
			${card("lc-errors", t("Errores agrupados"), errs.length ? t("{n} firmas", { n: errs.length }) : "", errBody)}
			${card("lc-slow", t("Más lentos que su historial"), "", slowBody)}
			${card("lc-backend", t("Backend de la ejecución"), eps?.length ? t("{n} endpoints", { n: eps.length }) : "", epBody)}
			${card("lc-activity", t("Actividad reciente"), "", evBody)}</div>`;
	}

	/** Las facetas del explorador: estado, archivo, worker y error, con su cantidad; varias a la vez. */
	function facetsHTML(tests, ui, h) {
		const { t, esc, icon } = h;
		const f = facets(tests);
		const sel = { state: ui.states, file: ui.files, worker: ui.workers, error: ui.errors };
		const LIMIT = 8;
		const group = (k, title, values, label = (v) => v) => {
			if (!values.length || (k === "worker" && values.length < 2)) return "";
			const more = ui.more?.[k], list = more ? values : values.slice(0, LIMIT);
			return `<fieldset class="lf-group"><legend>${esc(title)}</legend>${list.map(([v, n]) => {
				const on = sel[k].includes(v);
				return `<button type="button" class="lf-item ${on ? "on" : ""}" role="checkbox" aria-checked="${on}" data-live-facet="${k}" data-v="${esc(v)}" title="${esc(v)}">
					<span class="lf-box" aria-hidden="true">${on ? icon("i-pass") : ""}</span><span class="lf-name" ${k === "state" ? "" : "data-no-i18n"}>${esc(label(v))}</span><span class="lf-n">${n}</span></button>`;
			}).join("")}${values.length > LIMIT ? `<button type="button" class="lf-more" data-live-more="${k}">${esc(more ? t("ver menos") : t("ver {n} más", { n: values.length - LIMIT }))}</button>` : ""}</fieldset>`;
		};
		const short = shortNames(f.file.map(([v]) => v), 30);
		return `<aside class="card live-facets" aria-label="${esc(t("Facetas"))}"><h6>${esc(t("Facetas"))}</h6>
			${group("state", t("Estado"), f.state, (v) => t(stateOf(v).label))}
			${group("error", t("Error"), f.error, (v) => (v.length > 60 ? v.slice(0, 59) + "…" : v))}
			${group("file", t("Archivo"), f.file, (v) => short.get(v) || v)}
			${group("worker", t("Worker"), f.worker)}</aside>`;
	}

	/**
	 * Panel lateral del test: estado y, en pestañas, la traza (pasos y llamadas al backend en una línea
	 * de tiempo, con el fallo marcado), los últimos pasos y la captura más reciente; y abrir el test.
	 */
	function panelHTML(panel, tests, h, now) {
		if (!panel) return "";
		const { t, esc, icon } = h;
		const live = tests.find((x) => x.id === panel.id);
		const full = panel.test;
		const name = live?.name || full?.name || `#${panel.id}`;
		const st = stateOf(live?.state || full?.status);
		const allLogs = full?.logs || [];
		const logs = allLogs.slice(-5).reverse();
		const shot = [...allLogs].reverse().find((l) => l.screenshot);
		const tab = panel.tab || "trace";
		const facts = live ? [
			[t("Estado"), `<span class="lt-state ${st.cls}">${icon(st.icon)}${esc(t(st.label))}</span>`],
			[t("Archivo"), `<span data-no-i18n>${esc(live.file)}</span>`],
			live.worker ? [t("Worker"), `<span data-no-i18n>${esc(live.worker)}</span>`] : null,
			[t("Dura."), esc(fmtDur(live.elapsed)) + (live.p50 ? ` · ×${live.factor.toFixed(1)} p50 (${esc(fmtDur(live.p50))})` : "")],
			live.idle && ["running", "turtle", "no_signal"].includes(live.state) ? [t("Última señal"), esc(t("hace {t}", { t: fmtDur(live.idle) }))] : null,
			(live.attempts || 0) > 1 ? [t("Intentos"), String(live.attempts)] : null,
		].filter(Boolean) : [];
		let body;
		if (panel.error) body = `<p class="m-hint">${esc(panel.error)}</p>`;
		else if (!full) body = `<p class="m-hint">${esc(t("Cargando…"))}</p>`;
		else if (tab === "steps") {
			body = logs.length ? `<ol class="live-steps">${logs.map((l) => `<li class="${stepState(l.status).cls}"><span class="lt-state ${stepState(l.status).cls}">${icon(stepState(l.status).icon)}</span><span data-no-i18n>${esc(l.message)}</span><small>${esc(h.clock(l.timestamp, true))}</small></li>`).join("")}</ol>`
				: `<p class="m-hint">${esc(t("Todavía sin pasos."))}</p>`;
		} else if (tab === "shot") {
			body = shot ? `<img class="live-shot" src="${esc(shot.screenshot)}" alt="${esc(shot.message || t("Captura"))}" loading="lazy"><p class="m-hint" data-no-i18n>${esc(shot.message || "")}</p>`
				: `<p class="m-hint">${esc(t("Todavía sin capturas."))}</p>`;
		} else {
			const tr = traceLayout({ start: full.started_at || live?.started_at, end: full.ended_at || live?.ended_at || now, logs: allLogs, network: panel.net || [], all: !!panel.all });
			const netState = panel.net === undefined ? `<p class="m-hint">${esc(t("Cargando la red…"))}</p>` : "";
			body = tr.rows.length ? `<div class="lt-trace" role="list" aria-label="${esc(t("Traza del test"))}">
				${tr.failAt ? `<span class="lt-trace-fail" style="left:calc(var(--trace-label) + (100% - var(--trace-label)) * ${(tr.failAt / 100).toFixed(4)})" aria-hidden="true"></span>` : ""}
				${tr.rows.map((r, i) => {
					const cls = r.kind === "req" ? (r.failed ? "fail" : "ok") : stepState(r.status).cls;
					const when = `+${fmtDur(r.rel)}`;
					const tip = r.kind === "req" ? `${r.label} · ${r.code} · ${fmtDur(r.dur)} · ${when}` : `${r.label} · ${when}`;
					return `<div class="ltr ${r.kind} ${cls}" role="listitem" title="${esc(tip)}" style="--i:${Math.min(i, 30)}"><span class="ltr-label" data-no-i18n>${r.kind === "req" ? `<b>${esc(r.code)}</b> ` : ""}${esc(r.label)}</span>
						<span class="ltr-track">${r.kind === "req" ? `<i style="left:${r.left.toFixed(2)}%;width:${r.width.toFixed(2)}%"></i>` : `<i class="pt" style="left:${r.left.toFixed(2)}%"></i>`}</span></div>`;
				}).join("")}</div>
				<p class="m-hint lt-trace-foot">${esc(t("{n} pasos y llamadas", { n: tr.rows.length }))}${tr.cut ? ` · ${esc(t("{n} más sin mostrar", { n: tr.cut }))}` : ""}${tr.hidden ? ` · <button type="button" class="lf-more" data-live-trace-all>${esc(panel.all ? t("solo API") : t("incluir {n} recursos estáticos", { n: tr.hidden }))}</button>` : ""}</p>`
				: netState || `<p class="m-hint">${esc(t("Todavía sin pasos."))}</p>`;
		}
		const tabBtn = (k, label) => `<button type="button" role="tab" class="live-ptab ${tab === k ? "on" : ""}" aria-selected="${tab === k}" data-live-ptab="${k}">${esc(t(label))}</button>`;
		return `<aside class="live-panel" role="dialog" aria-modal="false" aria-label="${esc(name)}" data-live-panel>
			<header><span class="lt-state ${st.cls}">${icon(st.icon)}</span><h4 data-no-i18n>${esc(name)}</h4>
				<button type="button" class="cf-btn cf-btn-sm" data-live-close aria-label="${esc(t("Cerrar"))}">${icon("i-close")}</button></header>
			<dl class="live-facts">${facts.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join("")}</dl>
			<div class="live-ptabs" role="tablist">${tabBtn("trace", "Traza")}${tabBtn("steps", "Últimos pasos")}${tabBtn("shot", "Captura")}</div>
			<div class="live-pbody" role="tabpanel">${body}</div>
			<a class="cf-btn cf-btn-primary" href="#run=${live?.run_id || full?.run_id}&view=tests&test=${panel.id}">${esc(t("Abrir el test"))}</a></aside>`;
	}

	window.TraceReportsLive = { STATES, PROBLEM, AUTO_PROBLEMS, fmtDur, sortRuns, allTests, shortNames, stepText, span, lanesOf, ganttLayout, ticks, minimap,
		zoomStep, problemsOn, filterTests, groupTests, progress, mix, percentile, health, errorSignature, errorGroups, slowest, events, anomalies,
		facets, sortTests, endpointOf, traceLayout, timeBuckets, resultsOverTime, failRateOverTime, durationHistogram, ago, freshness, render };
})();
