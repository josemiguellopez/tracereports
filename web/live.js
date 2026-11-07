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
		const states = ui.states || [];
		return tests.filter((t) => {
			if (q && !`${t.name} ${t.file} ${t.worker || ""} ${t.last_step || ""}`.toLowerCase().includes(q)) return false;
			if (states.length) return states.includes(t.state);
			if (problems && !q) return isProblem(t.state);
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

	// ---------- dibujo ----------

	/**
	 * Dibuja la vista. data: {list, exec, now, no_signal_ms, ui, panel}; h: {t, esc, icon, canAct, clock(ms)}.
	 * ui: {problems, states[], group, q, zoom, allLanes, showOk, open{}, panel}. Devuelve el HTML (app.js
	 * lo pone en #view-live y maneja los clics).
	 */
	function render(data, h) {
		const { t, esc, icon } = h;
		const now = data.now || Date.now();
		const ui = { states: [], group: "file", q: "", open: {}, ...(data.ui || {}) };
		if (!data.list.length) {
			return `<div class="card live-empty">${icon("i-running")}<h3>${esc(t("No hay ejecuciones en curso"))}</h3>
				<p class="m-hint">${esc(t("Cuando un cliente empiece a enviar resultados, aquí ves qué terminó, qué sigue corriendo, qué está lento y qué dejó de enviar, por worker y por test."))}</p></div>`;
		}
		const e = data.exec;
		const label = (x) => `${x.title} · ${x.running ? t("en curso") : t("terminada")} · ${x.done}/${x.planned || x.total}${x.counts.no_signal ? ` · ${t("{n} sin señal", { n: x.counts.no_signal })}` : ""}${x.counts.FAIL ? ` · ${t("{n} fallos", { n: x.counts.FAIL })}` : ""}`;
		const selector = `<label class="live-exec-pick"><span class="live-dot ${data.exec?.running ? "on" : ""}" aria-hidden="true"></span>
			<select data-live-exec-select aria-label="${esc(t("Ejecución"))}">${data.list.map((x) => `<option value="${esc(x.key)}" ${x.key === data.exec?.key ? "selected" : ""}>${esc(label(x))}</option>`).join("")}</select>
			${data.list.length > 1 ? `<small>${esc(t("{n} ejecuciones", { n: data.list.length }))}</small>` : ""}</label>`;
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
				data-tip="${esc(t("Clic: ver solo estos tests"))}"><b>${e.counts[k] || 0}</b><span>${icon(st.icon)}${esc(t(st.label))}${sub}</span></button>`;
		}).join("");
		const head = `<div class="card live-head">
			<div class="live-title">${selector}
				${e.running ? `<span class="live-badge on">${icon("i-running")}${esc(t("EN CURSO"))}</span>` : `<span class="live-badge">${esc(t("Terminada"))}</span>`}
				<h3 data-no-i18n>${esc(e.title)}</h3><span class="live-meta">${meta}${meta ? " · " : ""}${extra}</span>${cancel}</div>
			<div class="live-tiles">
				<div class="live-tile live-prog" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${p.pct}" aria-label="${esc(t("Progreso"))}">
					<span class="live-prog-l">${esc(t("Progreso"))}${p.eta ? ` · ${esc(t("ETA {t}", { t: fmtDur(p.eta) }))}` : ""}</span>
					<div class="live-bar">${mix(e).map((s) => `<i class="${s.cls}" style="width:${s.w.toFixed(2)}%"></i>`).join("")}</div>
					<span class="live-pct">${pctText}</span></div>${tiles}</div></div>`;

		// (2) filtros: solo con problemas, agrupar, buscar
		const nProblems = tests.filter((x) => isProblem(x.state)).length;
		const groupBtn = (k, l) => `<button type="button" class="cf-btn cf-btn-sm ${ui.group === k ? "on" : ""}" data-live-groupby="${k}" aria-pressed="${ui.group === k}">${esc(t(l))}</button>`;
		const filters = `<div class="card live-filters">
			<button type="button" class="live-chip ${problems ? "on" : ""}" data-live-problems aria-pressed="${problems}">${esc(t("Solo con problemas ({n})", { n: nProblems }))}</button>
			${ui.states.length ? `<button type="button" class="live-chip" data-live-clear>${icon("i-close")}${esc(t("Quitar filtro de estado"))}</button>` : ""}
			<span class="live-spacer"></span>
			<span class="m-hint">${esc(t("Agrupar:"))}</span>${groupBtn("file", "Archivo")}${groupBtn("worker", "Worker")}${groupBtn("state", "Estado")}
			<input type="search" class="live-q" data-live-q value="${esc(ui.q)}" placeholder="${esc(t("Buscar test, archivo o paso…"))}" aria-label="${esc(t("Buscar"))}"></div>`;

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
				<div class="lg-mini" data-live-minimap data-tip="${esc(t("Arrastra para acercar un tramo; clic para centrar"))}">${mm.map((c) => c.n ? `<i style="left:${c.left.toFixed(2)}%;height:${Math.max(8, c.h * 100).toFixed(0)}%">${c.parts.map((x) => `<b class="${x.cls}" style="height:${(x.f * 100).toFixed(1)}%"></b>`).join("")}</i>` : "").join("")}${brush}</div>
				<div class="lg-axis" aria-hidden="true">${ticks(g.a, g.b).map((k) => `<span style="left:${k.left}%">${esc(h.clock(k.at, g.b - g.a < 10 * 60e3))}</span>`).join("")}</div>
				<div class="lg-lanes">${shown.map(laneHTML).join("")}
				${hidden ? `<button type="button" class="lg-more" data-live-alllanes>${icon("i-chevron")}${esc(t("{n} carriles sin problemas", { n: hidden }))} · ${esc(t("mostrar"))}</button>` : ""}
				${problems && ui.allLanes && g.lanes.some((l) => !l.problem) ? `<button type="button" class="lg-more" data-live-alllanes>${esc(t("Ocultar los carriles sin problemas"))}</button>` : ""}
				${!shown.length && !hidden ? `<p class="m-hint">${esc(t("Nada en este tramo."))}</p>` : ""}</div></div>`;
		}

		// tabla: grupos por severidad; los sin problemas colapsados en una línea
		const visible = filterTests(tests, ui, problems);
		const groups = groupTests(visible, ui.group);
		// con muchos archivos, los sin problemas en una sola línea (con pocos, se ven todos)
		const plainGroups = ui.group === "file" && !problems && !ui.states.length && !ui.q && !ui.showOk && groups.length > COLLAPSE_FROM ? groups.filter((x) => !x.problem) : [];
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
			const open = gr.key in ui.open ? ui.open[gr.key] : gr.problem || listed.length <= 3 || !!ui.q || ui.states.length > 0;
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
		} else if (problems && !ui.states.length && !ui.q && hiddenTests > 0) {
			collapsed = `<button type="button" class="live-collapsed" data-live-problems>${icon("i-chevron")}${esc(t("{n} tests sin problemas ocultos", { n: hiddenTests }))} · ${esc(t("mostrar todos"))}</button>`;
		} else if (ui.showOk && ui.group === "file" && !problems && !ui.states.length && !ui.q && groups.length > COLLAPSE_FROM && groups.some((x) => !x.problem)) {
			collapsed = `<button type="button" class="live-collapsed" data-live-showok>${esc(t("Ocultar los archivos sin problemas"))}</button>`;
		}
		const nothing = !listed.length && !plainGroups.length
			? `<p class="m-hint live-none">${esc(problems && !ui.states.length && !ui.q ? t("Ningún test con problemas por ahora.") : t("Ningún test coincide con el filtro."))}</p>` : "";
		const table = `<div class="card live-runs"><h6>${esc(ui.group === "worker" ? t("Worker / test") : ui.group === "state" ? t("Estado / test") : t("Archivo / test"))} <span class="m-hint">${esc(t("{n} de {t} tests", { n: visible.length, t: tests.length }))}</span></h6>
			${listed.length ? `<table class="live-table"><thead><tr><th>${esc(t("Test"))}</th><th>${esc(t("Estado"))}</th><th>${esc(t("Dura."))}</th><th title="${esc(t("Duración frente a la mediana de sus ejecuciones anteriores"))}">${esc(t("vs p50"))}</th><th>${esc(t("Último paso"))}</th></tr></thead>
				${listed.map(groupHTML).join("")}</table>` : ""}${collapsed}${nothing}
			${emptyRuns.length && ui.group === "file" && !problems ? emptyRuns.map((r) => `<div class="live-run"><div class="live-run-h"><b data-no-i18n>${esc(r.name)}</b> <span class="m-hint">${esc(t("Todavía sin tests."))}</span></div></div>`).join("") : ""}
			<p class="m-hint">${esc(t("Tortuga: más lento que su p90 (o el doble de su mediana) en sus ejecuciones anteriores. Sin señal: nada del test hace {n} min.", { n: Math.round((data.no_signal_ms || 180000) / 60000 * 10) / 10 }))}</p></div>`;

		return `${head}${filters}${gantt}${table}${panelHTML(data.panel, tests, h)}`;
	}

	/** El estado de un paso: los de test (Pass, Fallo…) o informativo. */
	const stepState = (s) => STATES[s] || { cls: "info", icon: "i-info" };

	/** Panel lateral del test: estado, últimos 5 pasos, la captura más reciente y abrir el test. */
	function panelHTML(panel, tests, h) {
		if (!panel) return "";
		const { t, esc, icon } = h;
		const live = tests.find((x) => x.id === panel.id);
		const full = panel.test;
		const name = live?.name || full?.name || `#${panel.id}`;
		const st = stateOf(live?.state || full?.status);
		const logs = (full?.logs || []).slice(-5).reverse();
		const shot = [...(full?.logs || [])].reverse().find((l) => l.screenshot);
		const facts = live ? [
			[t("Estado"), `<span class="lt-state ${st.cls}">${icon(st.icon)}${esc(t(st.label))}</span>`],
			[t("Archivo"), `<span data-no-i18n>${esc(live.file)}</span>`],
			live.worker ? [t("Worker"), `<span data-no-i18n>${esc(live.worker)}</span>`] : null,
			[t("Dura."), esc(fmtDur(live.elapsed)) + (live.p50 ? ` · ×${live.factor.toFixed(1)} p50 (${esc(fmtDur(live.p50))})` : "")],
			live.idle && ["running", "turtle", "no_signal"].includes(live.state) ? [t("Última señal"), esc(t("hace {t}", { t: fmtDur(live.idle) }))] : null,
		].filter(Boolean) : [];
		return `<aside class="live-panel" role="dialog" aria-modal="false" aria-label="${esc(name)}" data-live-panel>
			<header><span class="lt-state ${st.cls}">${icon(st.icon)}</span><h4 data-no-i18n>${esc(name)}</h4>
				<button type="button" class="cf-btn cf-btn-sm" data-live-close aria-label="${esc(t("Cerrar"))}">${icon("i-close")}</button></header>
			<dl class="live-facts">${facts.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join("")}</dl>
			<h6>${esc(t("Últimos pasos"))}</h6>
			${panel.error ? `<p class="m-hint">${esc(panel.error)}</p>` : !full ? `<p class="m-hint">${esc(t("Cargando…"))}</p>`
				: logs.length ? `<ol class="live-steps">${logs.map((l) => `<li class="${stepState(l.status).cls}"><span class="lt-state ${stepState(l.status).cls}">${icon(stepState(l.status).icon)}</span><span data-no-i18n>${esc(l.message)}</span><small>${esc(h.clock(l.timestamp, true))}</small></li>`).join("")}</ol>`
				: `<p class="m-hint">${esc(t("Todavía sin pasos."))}</p>`}
			${shot ? `<h6>${esc(t("Captura más reciente"))}</h6><img class="live-shot" src="${esc(shot.screenshot)}" alt="${esc(shot.message || t("Captura"))}" loading="lazy">` : ""}
			<a class="cf-btn cf-btn-primary" href="#run=${live?.run_id || full?.run_id}&view=tests&test=${panel.id}">${esc(t("Abrir el test"))}</a></aside>`;
	}

	window.TraceReportsLive = { STATES, PROBLEM, AUTO_PROBLEMS, fmtDur, sortRuns, allTests, shortNames, stepText, span, lanesOf, ganttLayout, ticks, minimap,
		zoomStep, problemsOn, filterTests, groupTests, progress, mix, render };
})();
