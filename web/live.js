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
	// un set propio de íconos de estado (index.html, i-st-*): misma rejilla de 24, trazo de 2 y puntas
	// redondas, legibles a 14 px; siempre con texto o tooltip (tip: qué significa, en una frase)
	const STATES = {
		no_signal: { cls: "nosig", icon: "i-st-nosig", label: "Sin señal", rank: 0, tip: "El test dejó de enviar eventos: puede estar colgado o el proceso murió." },
		FAIL: { cls: "fail", icon: "i-st-fail", label: "Fallo", rank: 1, tip: "Terminó con error o con una validación que no se cumplió." },
		INTERRUPTED: { cls: "interrupted", icon: "i-st-interrupted", label: "Interrumpido", rank: 2, tip: "Se cortó antes de terminar: cancelación, timeout del runner o el proceso se detuvo." },
		turtle: { cls: "turtle", icon: "i-st-turtle", label: "Tortuga", rank: 3, tip: "Sigue corriendo y ya tarda más que su p90 (o el doble de lo normal) en sus ejecuciones anteriores." },
		WARNING: { cls: "warning", icon: "i-st-warning", label: "Warning", rank: 4, tip: "Pasó, pero algún paso dejó una advertencia." },
		running: { cls: "running", icon: "i-st-running", label: "En curso", rank: 5, tip: "Corriendo ahora, dentro de su tiempo normal." },
		PASS: { cls: "pass", icon: "i-st-pass", label: "Pass", rank: 6, tip: "Terminó bien." },
		SKIP: { cls: "skip", icon: "i-st-skip", label: "Skip", rank: 7, tip: "No se ejecutó: lo saltó el runner o una cancelación." },
	};
	const stateOf = (s) => STATES[s] || { cls: "running", icon: "i-st-running", label: s, rank: 5, tip: "" };
	/** Los estados que llevan su ícono dentro de la barra del Gantt (además del color y su patrón). */
	const GLYPH = new Set(["FAIL", "no_signal", "turtle", "WARNING", "INTERRUPTED"]);
	/** "Proceso 3" para los carriles inferidos (sin worker); se explica en el tooltip y en la leyenda. */
	const PROC = "Proceso";
	/** Lo que pide acción: con "Solo con problemas" se ve solo esto. */
	const PROBLEM = new Set(["no_signal", "FAIL", "INTERRUPTED", "turtle"]);
	const isProblem = (s) => PROBLEM.has(s);
	/** Los tiles, en el orden de la propuesta; Skip e Interrumpido solo si hay. */
	const TILES = ["PASS", "FAIL", "WARNING", "running", "turtle", "no_signal", "INTERRUPTED", "SKIP"];
	const OPTIONAL_TILES = new Set(["INTERRUPTED", "SKIP"]);
	/** Desde cuántas corridas o carriles "Solo con problemas" viene activo. */
	const AUTO_PROBLEMS = 20;
	const MINIMAP_BUCKETS = 80;
	/** Cuántas filas dibuja la tabla de una vez (y cuántas más con "Mostrar más"). */
	const ROWS_STEP = 100;
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

	/** Los números que se leen, con el formato del idioma de la interfaz (es-CL: 1,7; en: 1.7). */
	const numFmts = new Map();
	function numFmt(d) {
		const lang = window.TraceReportsI18n?.lang === "en" ? "en-US" : "es-CL", k = `${lang}|${d}`;
		if (!numFmts.has(k)) numFmts.set(k, new Intl.NumberFormat(lang, { minimumFractionDigits: d, maximumFractionDigits: d, useGrouping: false }));
		return numFmts.get(k);
	}
	const num1 = (x) => numFmt(1).format(x || 0);

	/** Una duración corta con su unidad: 180 → "180 ms", 2400 → "2 s" (fmtDur redondea a segundos). */
	function fmtMs(ms) { return ms < 1000 ? `${Math.round(ms || 0)} ms` : fmtDur(ms); }

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

	/**
	 * Un XPath en el texto de un paso, por lo que identifica: el último atributo (clase, id, name…) o,
	 * si no tiene, el texto que busca o la última etiqueta. `//div[contains(@class,'modal-dialog')]` →
	 * `modal-dialog`. Recorre corchetes, paréntesis y comillas para saber dónde termina.
	 */
	function shortXPaths(s) {
		const text = String(s || "");
		let out = "", i = 0;
		while (i < text.length) {
			const at = text.indexOf("//", i);
			// un XPath empieza el texto o va tras un espacio, comilla, paréntesis o "="; "https://a/b//c" no
			if (at < 0 || (at > 0 && !/[\s'"(\[=,]/.test(text[at - 1]))) {
				out += text.slice(i, at < 0 ? text.length : at + 2);
				if (at < 0) break;
				i = at + 2;
				continue;
			}
			const start = at > 0 && text[at - 1] === "(" ? at - 1 : at;
			let j = start, depth = 0, quote = "";
			for (; j < text.length; j++) {
				const c = text[j];
				if (quote) { if (c === quote) quote = ""; continue; }
				if (c === "'" || c === '"') { if (depth === 0) break; quote = c; continue; }
				if (c === "[" || c === "(") depth++;
				else if (c === "]" || c === ")") { if (depth === 0) break; depth--; }
				else if (depth === 0 && /[\s,]/.test(c)) break;
			}
			const expr = text.slice(start, j);
			const attrs = [...expr.matchAll(/@(?:class|id|name|data-[\w-]+|aria-label|placeholder|title)\s*(?:,|=)\s*(['"])(.*?)\1/g)].map((m) => m[2].trim()).filter(Boolean);
			const lits = [...expr.matchAll(/(['"])(.*?)\1/g)].map((m) => m[2].trim()).filter(Boolean);
			const tags = [...expr.matchAll(/\/([a-zA-Z][\w-]*)/g)].map((m) => m[1]);
			// una lista de clases ("text-white grey-btn btn btn-secondary"): la última, la más específica
			const attr = attrs.pop();
			let label = attr ? attr.split(/\s+/).pop() : lits.length ? `"${lits[0]}"` : tags.pop() || "xpath";
			if (label.length > 32) label = label.slice(0, 31) + "…";
			out += text.slice(i, start) + label;
			i = Math.max(j, start + 1);
		}
		return out;
	}

	// donde empieza el volcado técnico de un error (Selenium/Chrome, Python, Java): no va en la fila
	const STACK_AT = /(?:\b(?:Message|Mensaje):\s*)?(?:Stacktrace:|Backtrace:|Dumping unresolved backtrace|Traceback \(most recent call last\)|\(No symbol\)|\s0x[0-9a-f]{6,}|\n\s*at [\w$.<>]+\()/i;

	/**
	 * El último paso para la fila: una línea sin URLs, con los XPaths acortados y sin stack trace
	 * (que vuelve aparte, para abrirlo a pedido). {text, trace, full}: full conserva los XPaths, para el tooltip.
	 */
	function stepParts(s) {
		const raw = String(s || "");
		const at = raw.search(STACK_AT);
		const head = at >= 0 ? raw.slice(0, at) : raw, trace = at >= 0 ? raw.slice(at).trim() : "";
		const full = stepText(head).replace(/[\s:;,.\-–—]+$/, "").trim();
		return { text: shortXPaths(full), trace, full };
	}

	// los estados en la URL (&f=fail,warn): cortos y estables, para compartir el enlace ya filtrado
	const HASH_STATES = { fail: "FAIL", nosig: "no_signal", turtle: "turtle", warn: "WARNING", run: "running", pass: "PASS", skip: "SKIP", int: "INTERRUPTED" };
	function statesFromHash(v) {
		return [...new Set(String(v || "").split(",").map((s) => HASH_STATES[s.trim().toLowerCase()]).filter(Boolean))];
	}
	function statesToHash(states) {
		const keys = Object.keys(HASH_STATES);
		return (states || []).map((s) => keys.find((k) => HASH_STATES[k] === s)).filter(Boolean).join(",");
	}

	/** Lo que requiere atención (fallo, sin señal, interrumpido, tortuga), lo más grave primero y sin saltar. */
	function attention(tests) {
		return tests.filter((x) => isProblem(x.state))
			.sort((a, b) => stateOf(a.state).rank - stateOf(b.state).rank || a.started_at - b.started_at || a.id - b.id);
	}

	/**
	 * Tramo total de la ejecución: [primer test iniciado, ahora o su fin]. Empieza en el primer test y
	 * no en el inicio de la ejecución: el lanzador suele tardar en abrir la primera corrida y ese rato
	 * quedaba como un vacío a la izquierda de la línea de tiempo.
	 */
	function span(exec, now) {
		const tests = allTests(exec);
		const from = tests.length ? Math.min(...tests.map((t) => t.started_at)) : exec.started_at || now;
		const to = Math.max(from + 1000, exec.running ? now : Math.max(from, ...tests.map((t) => t.ended_at || t.started_at), exec.ended_at || 0));
		return { from, to };
	}

	/** Desde qué hueco sin ningún test corriendo se comprime: 2 min y un 8 % del tramo. */
	const GAP_MIN = 2 * 60e3, GAP_FRAC = 0.08, GAP_W = 0.025;

	/**
	 * La escala de la línea de tiempo: tiempo → fracción [0, 1] del ancho. Los huecos largos sin ningún
	 * test corriendo (el lanzador esperando, una pausa entre tandas) ocupan un ancho fijo y chico con un
	 * corte (≈) en vez de un vacío: {from, to, x(t), t(x), gaps: [{a, b, x0, x1, dur}]}.
	 */
	function timeScale(exec, now) {
		const { from, to } = span(exec, now);
		const total = to - from;
		const busy = allTests(exec).map((t) => [t.started_at, t.ended_at || now]).sort((p, q) => p[0] - q[0]);
		const gaps = [];
		let reach = from;
		for (const [s, e] of busy) {
			if (s - reach >= Math.max(GAP_MIN, total * GAP_FRAC)) gaps.push({ a: reach, b: s });
			reach = Math.max(reach, e);
		}
		if (!gaps.length) return { from, to, gaps: [], x: (t) => (t - from) / total, t: (x) => from + x * total };
		// cada hueco vale G "ms virtuales" (un GAP_W del ancho final, aunque haya varios); el resto, lo que dura
		const active = total - gaps.reduce((s, g) => s + (g.b - g.a), 0);
		const G = (active * GAP_W) / Math.max(0.5, 1 - gaps.length * GAP_W), V = active + gaps.length * G;
		const segs = [];
		let at = from, v = 0;
		for (const g of gaps) {
			segs.push({ t0: at, t1: g.a, v0: v, v1: v + (g.a - at) }); v += g.a - at;
			segs.push({ t0: g.a, t1: g.b, v0: v, v1: v + G, gap: true }); v += G;
			at = g.b;
		}
		segs.push({ t0: at, t1: to, v0: v, v1: V });
		const x = (t) => {
			if (t <= from) return 0;
			if (t >= to) return 1;
			const s = segs.find((q) => t <= q.t1) || segs[segs.length - 1];
			return (s.v0 + ((t - s.t0) / Math.max(1, s.t1 - s.t0)) * (s.v1 - s.v0)) / V;
		};
		const tOf = (xv) => {
			const vv = Math.min(1, Math.max(0, xv)) * V;
			const s = segs.find((q) => vv <= q.v1) || segs[segs.length - 1];
			return s.t0 + ((vv - s.v0) / Math.max(1e-9, s.v1 - s.v0)) * (s.t1 - s.t0);
		};
		return { from, to, x, t: tOf, gaps: gaps.map((g) => ({ ...g, x0: x(g.a), x1: x(g.b), dur: g.b - g.a })) };
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
			const shortW = shortNames(tests.map((t) => t.file), 80); // sin el prefijo común; si es largo, la elipsis la pone el CSS al final
			return [...byWorker.entries()].sort((a, b) => natural(a[0], b[0])).map(([k, ts]) => {
				const files = [...new Set([...ts].sort((a, b) => a.started_at - b.started_at).map((t) => t.file))];
				const current = files[files.length - 1];
				return { key: k, label: k, sub: shortW.get(current) || current || "", title: files.join("\n"), current, tests: ts };
			});
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
		const short = shortNames(runs.map((r) => r.name), 80);
		return lanes.map((l) => {
			const current = l.runs[l.runs.length - 1];
			return { key: l.key, label: l.key, proc: Number(l.key.slice(1)), sub: short.get(current) || current, title: l.runs.join("\n"), current, tests: l.tests };
		});
	}

	/**
	 * El Gantt: carriles con sus barras en % de la ventana visible, sobre la escala con los huecos
	 * comprimidos (timeScale). zoom = {a, b} en fracciones del ancho total (null = todo). Cada carril
	 * dice si tiene algo que pide acción y si sigue corriendo; nowLeft, dónde va "ahora" (o null).
	 */
	function ganttLayout(exec, now, zoom = null) {
		const sc = timeScale(exec, now);
		const za = zoom ? zoom.a : 0, zb = zoom ? zoom.b : 1, zw = Math.max(1e-6, zb - za);
		const pos = (t) => ((sc.x(t) - za) / zw) * 100;
		const a = sc.t(za), b = sc.t(zb);
		const names = shortNames(allTests(exec).map((t) => t.name), 40);
		const lanes = lanesOf(exec, now).map((l) => {
			const bars = [];
			for (const t of l.tests) {
				const end = t.ended_at || now;
				if (end < a || t.started_at > b) continue; // fuera de la ventana del zoom
				const left = Math.max(0, pos(t.started_at)), right = Math.min(100, pos(end));
				bars.push({ id: t.id, run_id: t.run_id, name: t.name, short: names.get(t.name) || t.name, file: t.file, state: t.state,
					left, width: Math.max(0.4, right - left), elapsed: t.elapsed, factor: t.factor, started_at: t.started_at, ended_at: t.ended_at });
			}
			const running = l.tests.some((t) => !t.ended_at);
			const nProblems = l.tests.filter((t) => isProblem(t.state)).length;
			return { key: l.key, worker: l.label, proc: l.proc || 0, sub: l.sub || "", current: l.current || "", title: l.title, bars, running,
				problem: nProblems > 0, nProblems, ids: l.tests.map((t) => t.id) };
		});
		const gaps = sc.gaps.filter((g) => g.x1 > za && g.x0 < zb).map((g) => ({ ...g, left: Math.max(0, ((g.x0 - za) / zw) * 100), width: ((Math.min(g.x1, zb) - Math.max(g.x0, za)) / zw) * 100 }));
		const nowLeft = exec.running && sc.x(now) >= za && sc.x(now) <= zb ? pos(now) : null;
		return { from: sc.from, to: sc.to, a, b, lanes, gaps, nowLeft, scale: sc, zoom: { a: za, b: zb } };
	}

	/** Marcas de tiempo del eje: 4-6 horas redondas dentro del tramo. */
	function ticks(from, to) {
		const span = to - from, steps = [10e3, 30e3, 60e3, 2 * 60e3, 5 * 60e3, 10 * 60e3, 15 * 60e3, 30 * 60e3, 3600e3, 2 * 3600e3];
		const step = steps.find((s) => span / s <= 6) || 4 * 3600e3;
		const out = [];
		for (let t = Math.ceil(from / step) * step; t <= to; t += step) out.push({ at: t, left: ((t - from) / span) * 100 });
		return out;
	}

	/** Las marcas del eje sobre la escala con huecos: las horas redondas fuera de los cortes, en % de la ventana. */
	function scaleTicks(g) {
		const sc = g.scale, zw = g.zoom.b - g.zoom.a;
		const busy = (g.b - g.a) - g.gaps.reduce((s, x) => s + (Math.min(x.b, g.b) - Math.max(x.a, g.a)), 0);
		// el paso sale del tiempo con actividad (sin los cortes), no del tramo entero
		const steps = [10e3, 30e3, 60e3, 2 * 60e3, 5 * 60e3, 10 * 60e3, 15 * 60e3, 30 * 60e3, 3600e3, 2 * 3600e3];
		const step = steps.find((s) => busy / s <= 7) || 4 * 3600e3;
		const res = [];
		for (let t = Math.ceil(g.a / step) * step; t <= g.b; t += step) {
			if (sc.gaps.some((x) => t > x.a && t < x.b)) continue;
			const left = ((sc.x(t) - g.zoom.a) / zw) * 100;
			if (res.length && left - res[res.length - 1].left < 6) continue; // sin encimarse junto a un corte
			res.push({ at: t, left });
		}
		return res;
	}

	/**
	 * Minimapa: la ejecución entera en columnas; el alto es cuántos tests corrían en ese momento (frente
	 * al máximo) y cada columna se apila por estado (lo severo abajo, a la vista). Usa la misma escala
	 * que los carriles: un hueco comprimido es una columna vacía angosta.
	 */
	function minimap(exec, now, buckets = MINIMAP_BUCKETS, only = null) {
		const sc = timeScale(exec, now);
		const tests = allTests(exec).filter((t) => !only || only.has(t.id));
		const cols = [];
		for (let i = 0; i < buckets; i++) {
			const a = sc.t(i / buckets), b = sc.t((i + 1) / buckets);
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

	/** La clave del grupo de los tests sin fallo, al agrupar por error. */
	const NO_ERROR = "__sin-error__";
	/** Grupos de la tabla (archivo, worker, estado o error), lo más severo primero. */
	function groupTests(tests, by) {
		// por error: la firma de su fallo ("1 causa, 177 afectados"); lo que no falló va junto, al final
		const key = (t) => (by === "worker" ? t.worker || "—" : by === "state" ? t.state : by === "error" ? (isFailed(t.state) ? errorSignature(t.last_step) : NO_ERROR) : t.file);
		const groups = new Map();
		for (const t of tests) {
			const k = key(t);
			if (!groups.has(k)) groups.set(k, []);
			groups.get(k).push(t);
		}
		return [...groups.entries()].map(([k, ts]) => {
			// por gravedad y, dentro, por orden de inicio: con cada evento el factor cambia y la fila no debe saltar
			const sorted = [...ts].sort((a, b) => stateOf(a.state).rank - stateOf(b.state).rank || a.started_at - b.started_at || a.id - b.id);
			const counts = {};
			for (const t of ts) counts[t.state] = (counts[t.state] || 0) + 1;
			return { key: k, tests: sorted, worst: stateOf(sorted[0].state).rank, problem: ts.some((t) => isProblem(t.state)), counts,
				worker: by === "file" ? [...new Set(ts.map((t) => t.worker).filter(Boolean))].join(", ") : "" };
		}).sort((a, b) => (a.key === NO_ERROR) - (b.key === NO_ERROR) || (by === "error" ? b.tests.length - a.tests.length : 0) || a.worst - b.worst || String(a.key).localeCompare(String(b.key), undefined, { numeric: true }));
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

	/**
	 * Las marcas del minimapa: las anomalías a menos de un 1,5 % de distancia van en una sola marca con
	 * su número (el peor estado primero); así no se montan unas sobre otras.
	 */
	function anomalyMarks(list, gap = 1.5) {
		const sorted = [...list].sort((a, b) => a.left - b.left), out = [];
		for (const a of sorted) {
			const last = out[out.length - 1];
			if (last && a.left - last.end < gap) { last.items.push(a); last.end = a.left; last.left = (last.start + a.left) / 2; }
			else out.push({ start: a.left, end: a.left, left: a.left, items: [a] });
		}
		for (const m of out) m.items.sort((x, y) => stateOf(x.state).rank - stateOf(y.state).rank);
		return out;
	}

	/** Las anomalías sobre el minimapa: cada fallo y cada test que dejó de enviar, en % del tramo. */
	function anomalies(exec, now) {
		const sc = timeScale(exec, now);
		return allTests(exec).filter((t) => isFailed(t.state) || t.state === "no_signal").map((t) => {
			const at = t.state === "no_signal" ? (t.last_signal || t.started_at) : (t.ended_at || t.started_at);
			return { id: t.id, run_id: t.run_id, name: t.name, state: t.state, at, left: Math.min(100, Math.max(0, sc.x(at) * 100)) };
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
			<button type="button" class="xp-btn ${m.f}" data-live-xpick data-tip="${esc(t("Cambiar de ejecución: las que corren ahora, las recientes y las anteriores."))}" aria-haspopup="listbox" aria-expanded="${!!ui.pickerOpen}" aria-label="${esc(t("Ejecución: {e}", { e: cur.title }))}">
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
		const toggle = (key) => `<button type="button" class="lf-more" data-live-charttable="${key}" aria-pressed="${!!tables[key]}" data-tip="${esc(t("Los números del gráfico en una tabla, para leerlos o copiarlos."))}">${esc(tables[key] ? t("Ocultar datos") : t("Ver datos"))}</button>`;
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

	// ---------- piezas de dibujo: nombres, sparklines, mapa de tests, cinta de eventos ----------

	/**
	 * Un nombre largo con la elipsis en el medio: conserva el comienzo (test_004_) y el final, que es lo
	 * que distingue un test de otro. El corte lo hace el CSS (.mid-a se achica, .mid-b no), así que se
	 * adapta al ancho real; copiar el texto da el nombre entero.
	 */
	function splitName(name) {
		const s = String(name || "");
		if (s.length <= 22) return [s, ""];
		let cut = s.length - Math.min(22, Math.ceil(s.length * 0.45));
		const sep = s.slice(cut - 6, cut + 6).search(/[_\s·./:-]/); // en un separador cercano, no a mitad de palabra
		if (sep >= 0) cut = cut - 6 + sep;
		return [s.slice(0, cut), s.slice(cut)];
	}
	function midName(name, esc) {
		const [a, b] = splitName(name);
		return `<span class="mid" data-no-i18n><span class="mid-a">${esc(a)}</span>${b ? `<span class="mid-b">${esc(b)}</span>` : ""}</span>`;
	}

	/** Una sparkline (60×18) de una serie: los nulos se saltan; el último punto, marcado. */
	function sparkline(values, cls = "") {
		const pts = values.map((v, i) => ({ v, i })).filter((p) => p.v !== null && p.v !== undefined && !Number.isNaN(p.v));
		if (pts.length < 2) return "";
		const max = Math.max(...pts.map((p) => p.v)), min = Math.min(0, ...pts.map((p) => p.v)), n = values.length - 1 || 1;
		const X = (i) => (i / n) * 60, Y = (v) => 16 - ((v - min) / Math.max(1e-9, max - min)) * 14;
		const d = pts.map((p, k) => `${k ? "L" : "M"}${X(p.i).toFixed(1)},${Y(p.v).toFixed(1)}`).join(" ");
		const last = pts[pts.length - 1];
		return `<svg class="spark ${cls}" viewBox="0 0 60 18" aria-hidden="true"><path class="sa" d="${d} L${X(last.i).toFixed(1)},18 L${X(pts[0].i).toFixed(1)},18 Z"></path><path class="sl" d="${d}"></path><circle cx="${X(last.i).toFixed(1)}" cy="${Y(last.v).toFixed(1)}" r="1.8"></circle></svg>`;
	}

	/**
	 * El mapa de la ejecución: una celda por test, en el orden en que empezaron, con el color de su
	 * estado, y las que faltan (planificadas) vacías. Toda la ejecución de un vistazo, como una grilla de
	 * mercado; cada celda abre su test.
	 */
	function heatCells(exec) {
		const tests = allTests(exec).sort((a, b) => a.started_at - b.started_at || a.id - b.id);
		const pending = Math.max(0, (exec.planned || 0) - tests.length);
		return { tests, pending: Math.min(pending, 2000) };
	}

	// ---------- el campo de la ejecución (web/livefield.js dibuja el anillo; aquí, lo que lo rodea) ----------

	/** Cómo está la ingesta: activa solo si llegan eventos de esta ejecución; nunca "activa" sobre datos viejos. */
	const INGEST = {
		active: { label: "INGESTA ACTIVA", cls: "ok" }, quiet: { label: "SIN EVENTOS", cls: "warn" },
		offline: { label: "SIN CONEXIÓN", cls: "bad" }, done: { label: "EJECUCIÓN TERMINADA", cls: "done" },
	};

	/**
	 * El campo: a la izquierda los cuatro canales (tests, fallos, red, workers), al centro el anillo con
	 * su HUD (ingesta real, progreso, la anomalía dominante y los carriles activos) y a la derecha las
	 * últimas señales y la presión de errores por firma. Todo es un botón: filtra, agrupa o abre el test.
	 */
	function fieldHTML(e, tests, data, ui, now, h, g) {
		const { t, esc, icon } = h;
		const ing = data.ingest || { state: e.running ? "quiet" : "done", perMin: 0, lastAgo: null, byKind: {} };
		const I = INGEST[ing.state] || INGEST.quiet;
		const done = tests.filter((x) => x.ended_at).length, fails = tests.filter((x) => isFailed(x.state)).length;
		const eps = data.endpoints || [], calls = eps.reduce((s, x) => s + (x.count || 0), 0), epErr = eps.reduce((s, x) => s + (x.errors || 0), 0);
		const active = g.lanes.filter((l) => l.running).length;
		const errs = errorGroups(tests), top = errs[0];
		const p = progress(e);
		const per = (k) => (ing.byKind?.[k] ? ` · ${t("{n}/min", { n: ing.byKind[k] })}` : "");
		const lane = (cls, idx, name, big, sub, attrs, tip) => `<button type="button" class="lf-lane ${cls}" ${attrs} data-tip-title="${esc(name)}" data-tip="${esc(tip)}">
			<span>${idx} / ${esc(name)}</span><b>${big}</b><small>${esc(sub)}</small><i class="lf-flow" aria-hidden="true"></i></button>`;
		const lanes = [
			lane("pass", "01", t("TESTS"), `<span data-count="f-done" data-n="${done}">${done}</span>`, `${t("{n} terminados", { n: done })}${per("test")}`, "data-live-all",
				t("Tests terminados de la ejecución. Clic: ver todos.")),
			lane("fail", "02", t("FALLOS"), `<span data-count="f-fail" data-n="${fails}">${fails}</span>`, `${t("{n} firmas", { n: errs.length })}${per("fail")}`, `data-live-state="FAIL"`,
				t("Tests en fallo o interrumpidos, agrupados por la firma de su error. Clic: ver solo esos.")),
			lane("net", "03", t("RED"), String(calls), eps.length ? t("{n} con error", { n: epErr }) : t("sin llamadas"), `data-live-scroll=".lc-backend"`,
				t("Llamadas al backend de todas las corridas (las captura el cliente con Playwright). Clic: ir al backend de la ejecución.")),
			lane("work", "04", t("WORKERS"), `${active}<small>/${g.lanes.length}</small>`, t("con un test corriendo"), `data-live-groupby="worker"`,
				t("Carriles en paralelo con un test corriendo ahora, del total. Clic: agrupar la tabla por worker.")),
		].join("");
		const ago = ing.lastAgo === null ? t("todavía nada") : t("último hace {t}", { t: fmtDur(ing.lastAgo) });
		// la ingesta del último minuto en tramos de 5 s (el más nuevo a la derecha)
		const series = ing.series || Array(12).fill(0), smax = Math.max(1, ...series);
		// barras finas sobre una línea de base (un tramo vacío no dibuja nada): antes, rayas y una o dos barras
		// juntas al final se leían como un ícono de pausa o una letra
		const bars = series.map((n) => `<i style="--h:${n ? Math.max(12, Math.round((n / smax) * 100)) : 0}%"></i>`).join("");
		const hud = `<div class="lf-hud tl ${ui.paused ? "paused" : I.cls}" tabindex="0" data-tip-title="${esc(t(I.label))}" data-tip="${esc(t("Eventos de esta ejecución que llegaron a esta pestaña por el stream en vivo en el último minuto, y hace cuánto llegó el último. Terminada o sin eventos, nunca dice activa."))}">
				<span>${esc(t("INGESTA"))}</span>${ui.paused
					? `<b class="lf-paused-b">${icon("i-pause")}${esc(t("VISTA EN PAUSA"))}</b><small>${esc(t("la ingesta sigue:"))} <b data-live-pausecount>${data.pausedNew || 0}</b> ${esc(t("eventos nuevos"))}</small>
					<button type="button" class="cf-btn cf-btn-sm lf-resume" data-live-pause aria-pressed="true" data-tip="${esc(t("Vuelve a actualizar la vista con lo que llegó mientras estaba en pausa."))}">${icon("i-play")}${esc(t("Reanudar"))}</button>`
					: `<b><i class="lf-dot" aria-hidden="true"></i>${esc(t(I.label))}</b><small>${esc(t("{n} eventos/min", { n: ing.perMin }))} · ${esc(ago)}</small>`}
				<span class="lf-bars" aria-hidden="true">${bars}</span><span class="lf-bars-x" aria-hidden="true"><small>${esc(t("hace 1 min"))}</small><small>${esc(t("ahora"))}</small></span></div>
			${top ? `<button type="button" class="lf-hud bl bad" data-live-facet="error" data-v="${esc(top.sig)}" data-tip-title="${esc(t("Anomalía dominante"))}" data-tip="${esc(`${top.sig}\n${t("Clic: ver solo los tests con este error.")}`)}">
				<span>${esc(t("ANOMALÍA"))}</span><b data-no-i18n>${esc(shortXPaths(top.sig))}</b><small>${esc(t("{n} tests · {f} archivos", { n: top.count, f: top.files.length }))}</small></button>`
				: `<div class="lf-hud bl ok"><span>${esc(t("ANOMALÍA"))}</span><b>${esc(t("Sin fallos"))}</b><small>${esc(t("nada que agrupar"))}</small></div>`}
			<div class="lf-hud br" tabindex="0" data-tip="${esc(t("Carriles con un test corriendo ahora, del total de carriles de la ejecución."))}">
				<span>${esc(t("CARRILES"))}</span><b>${active} / ${g.lanes.length}</b><small>${esc(t("activos"))}</small></div>
			<div class="lf-core" aria-hidden="true"><span>${esc(t("EJECUCIÓN"))}</span><b>${p.pct}<small>%</small></b><small data-no-i18n>${esc(e.commit ? e.commit.slice(0, 7) : String(e.title || "").slice(-12))}</small></div>`;
		const max = Math.max(1, ...errs.slice(0, 3).map((x) => x.count));
		const pressure = errs.length ? errs.slice(0, 3).map((x) => `<button type="button" class="lf-pr" data-live-facet="error" data-v="${esc(x.sig)}" data-tip-title="${esc(x.sig)}" data-tip="${esc(t("Clic: ver solo los tests con este error."))}">
				<b data-no-i18n>${esc(x.sig.replace(/^(\w+(?:Error|Exception)).*/, "$1").slice(0, 16))}</b><i style="--bar:${((x.count / max) * 100).toFixed(0)}%"></i><em>${x.count}</em></button>`).join("")
			: `<p class="m-hint">${esc(t("Sin fallos por ahora."))}</p>`;
		return `<div class="lf">
			<div class="lf-lanes" aria-label="${esc(t("Canales"))}">${lanes}</div>
			<div class="lf-stage">${hud}<div class="lf-slot" data-live-field></div><div class="lf-foot" aria-hidden="true"><span>${esc(t("INGESTA → CORRELACIÓN → TRIAGE"))}</span><span>${esc(t("↑ comienzo · sentido horario"))}</span></div></div>
			<div class="lf-side"><h6>${esc(t("Presión de errores"))}</h6><div class="lf-press">${pressure}</div></div></div>`;
	}

	// ---------- dibujo ----------

	/** Lo que hace cada forma de agrupar, para el control y la línea de estado. */
	const GROUPS = {
		file: { icon: "i-file", label: "Archivo", tip: "Una fila por archivo (corrida) con sus tests debajo." },
		worker: { icon: "i-cpu", label: "Worker", tip: "Una fila por proceso o worker en paralelo: muestra si uno solo concentra los problemas." },
		state: { icon: "i-layers", label: "Estado", tip: "Una fila por estado: todos los fallos juntos, luego los lentos, etc." },
		error: { icon: "i-bug", label: "Error", tip: "Una fila por causa: los tests que fallaron con el mismo error, juntos (1 causa, 177 afectados). Los que no fallaron van al final." },
	};

	/**
	 * Dibuja la vista. data: {list, exec, now, no_signal_ms, ui, panel}; h: {t, esc, icon, canAct, clock(ms)}.
	 * ui: {problems, states[], group, q, zoom, allLanes, hideIdle, showOk, open{}, nameW, wrap, panel}.
	 * Devuelve el HTML (app.js lo pone en #view-live y maneja los clics).
	 */
	function render(data, h) {
		const { t, esc, icon } = h;
		const now = data.now || Date.now();
		const ui = { states: [], files: [], workers: [], errors: [], group: "file", q: "", open: {}, more: {}, ...(data.ui || {}) };
		if (!data.list.length) {
			return `<div class="card live-empty">${icon("i-st-running")}<h3>${esc(t("No hay ejecuciones en curso"))}</h3>
				<p class="m-hint">${esc(t("Cuando un cliente empiece a enviar resultados, aquí ves qué terminó, qué sigue corriendo, qué está lento y qué dejó de enviar, por worker y por test."))}</p></div>`;
		}
		const e = data.exec;
		const selector = execPickerHTML(data, ui, now, h);
		if (!e) return `<div class="card live-top">${selector}</div><p class="m-hint">${esc(t("Cargando…"))}</p>`;

		const tests = allTests(e);
		const g = ganttLayout(e, now, ui.zoom);
		const laneOf = new Map();
		for (const l of g.lanes) for (const id of l.ids) laneOf.set(id, l.key);
		const problems = problemsOn(e, ui, g.lanes.length);
		const stTip = (st) => `${t(st.label)}: ${t(st.tip || "")}`;

		// (0) la cinta: lo último que pasó, como un ticker (app.js la desplaza; se detiene con el mouse encima)
		const tape = events(tests, 18);
		const evVerb = { start: t("empezó"), no_signal: t("dejó de enviar"), FAIL: t("falló"), INTERRUPTED: t("se interrumpió"), WARNING: t("terminó con warning"), PASS: t("pasó"), SKIP: t("se saltó") };
		const tapeItem = (x) => {
			const st = x.kind === "start" ? stateOf("running") : stateOf(x.kind);
			// cada evento de la cinta abre su test (como una fila); la copia duplicada del bucle no se enfoca
			return `<button type="button" tabindex="-1" class="tp-i ${st.cls}" data-live-test="${x.test.id}" data-live-run="${x.test.run_id}" data-tip-title="${esc(x.test.name)}" data-tip="${esc(t("Clic: el detalle al costado (traza, últimos pasos y captura)."))}"><small>${esc(h.clock(x.at, true))}</small>${icon(st.icon)}<b data-no-i18n>${esc(x.test.name)}</b>${esc(evVerb[x.kind] || x.kind)}</button>`;
		};
		const tapeHTML = tape.length ? `<div class="live-tape" data-live-tape data-tip="${esc(t("Lo último que pasó en la ejecución, en vivo. Pasa el mouse para detenerla; el detalle está en Actividad reciente."))}">
			<span class="tp-tag ${e.running ? "on" : ""}">${e.running ? `<i class="live-dot on" aria-hidden="true"></i>${esc(t("EN VIVO"))}` : esc(t("ÚLTIMOS"))}</span>
			<div class="tp-win"><div class="tp-track" data-live-tape-track>${tape.map(tapeItem).join("")}<span class="tp-dup" aria-hidden="true" inert>${tape.map(tapeItem).join("")}</span></div></div></div>` : "";

		// (1) cabecera: ejecución, estado, meta, reloj, cancelar; tiles con la mezcla de estados
		const p = progress(e);
		const meta = [e.project, e.environment, e.branch, e.commit && e.commit.slice(0, 7)].filter(Boolean).map((x) => `<span data-no-i18n>${esc(x)}</span>`).join(" · ");
		const extra = [t("{n} corridas", { n: (e.runs || []).length }), t("{n} workers", { n: e.workers }), fmtDur((e.running ? now : e.ended_at) - e.started_at)].map(esc).join(" · ");
		const cancel = e.running && h.canAct()
			? (e.cancel_requested
				? `<span class="live-cancelled">${icon("i-pause")}${esc(t("Cancelación pedida: cada proceso se detendrá al terminar el test en curso y saltará los que faltan, también los que empiecen después."))}</span>`
				: `<details class="live-cancel"><summary class="cf-btn cf-btn-sm" data-tip="${esc(t("Pide a los clientes que se detengan al terminar el test en curso. No mata ningún proceso."))}">${icon("i-pause")}${esc(t("Cancelar ejecución…"))}</summary>
					<form data-live-cancel><p class="m-hint">${esc(t("Se detendrá al terminar el test en curso: los clientes con TRACEREPORTS_REMOTE_CONTROL=1 ven el pedido entre un test y otro, marcan los que faltan como Skip y cierran la ejecución. Alcanza también a las corridas que la ejecución abra después. No se mata ningún proceso."))}</p>
					<input name="reason" maxlength="300" placeholder="${esc(t("Motivo (opcional)"))}" aria-label="${esc(t("Motivo"))}">
					<button class="cf-btn cf-btn-sm cf-btn-danger" type="submit" data-tip="${esc(t("Envía el pedido a los clientes de esta ejecución."))}">${esc(t("Pedir cancelación"))}</button></form></details>`)
			: "";
		const pctText = `${p.known ? `${esc(t("{d} de {n} tests", { d: e.done, n: e.planned }))}` : esc(t("{d} terminados de {n} iniciados", { d: e.done, n: e.total }))}`;
		const tiles = TILES.filter((k) => !OPTIONAL_TILES.has(k) || e.counts[k]).map((k) => {
			const st = stateOf(k), on = ui.states.includes(k);
			const sub = k === "no_signal" && e.counts[k] ? ` ${esc(t("hace {t}", { t: fmtDur(Math.max(0, ...tests.filter((x) => x.state === k).map((x) => x.idle || 0))) }))}` : "";
			// en 0: visible (se sabe que no hay) pero sin clic, salvo que esté elegido y haya que poder quitarlo
			return `<button type="button" class="live-tile live-count ${st.cls} ${on ? "on" : ""} ${e.counts[k] ? "" : "zero"}" data-live-state="${k}" aria-pressed="${on}"${!e.counts[k] && !on ? " disabled" : ""}
				data-tip-title="${esc(t(st.label))}" data-tip="${esc(`${t(st.tip)} ${on ? t("Clic: quitar este filtro.") : t("Clic: ver solo estos tests.")}`)}"><b data-count="${k}" data-n="${e.counts[k] || 0}">${e.counts[k] || 0}</b><span>${icon(st.icon)}${esc(t(st.label))}${sub}</span></button>`;
		}).join("");
		const head = `<div class="card live-head">${tapeHTML}
			<div class="live-title">${selector}
				${e.running ? `<span class="live-badge on" data-tip="${esc(t("La ejecución sigue recibiendo resultados; la vista se actualiza sola."))}">${icon("i-st-running")}${esc(t("EN CURSO"))}</span>` : `<span class="live-badge">${esc(t("Terminada"))}</span>`}
				<h3 data-no-i18n>${esc(e.title)}</h3><span class="live-meta">${meta}${meta ? " · " : ""}${extra}</span>
				<span class="live-clock" data-live-clock data-tip="${esc(t("La hora, para leer los tiempos de la vista."))}">${esc(h.clock(Date.now(), true))}</span>
				<button type="button" class="cf-btn cf-btn-sm live-pause ${ui.paused ? "on" : ""}" data-live-pause aria-pressed="${!!ui.paused}" data-tip="${esc(ui.paused ? t("Vuelve a actualizar la vista con lo que llegó mientras estaba en pausa.") : t("Congela toda la vista para leer con calma; los datos siguen llegando al servidor."))}" data-tip-keys="P">${icon(ui.paused ? "i-play" : "i-pause")}${esc(ui.paused ? t("Reanudar") : t("Pausar"))}</button>
				<button type="button" class="cf-btn cf-btn-sm ${ui.compact ? "on" : ""}" data-live-compact aria-pressed="${!!ui.compact}" data-tip="${esc(t("Menos espacio y sin las tarjetas de diagnóstico ni los gráficos: para pantallas chicas o una tele de monitoreo."))}" data-tip-keys="C">${icon("i-list")}${esc(t("Compacto"))}</button>
				<button type="button" class="cf-btn cf-btn-sm" data-live-help aria-label="${esc(t("Atajos de teclado"))}" data-tip="${esc(t("Los atajos de teclado de esta vista."))}" data-tip-keys="?">?</button>${cancel}
				${ui.paused ? `<p class="live-paused" role="status">${icon("i-pause")}${esc(t("En pausa: la vista no se actualiza. Los datos siguen llegando al servidor."))}</p>` : ""}</div>
			<div class="live-tiles">
				<div class="live-tile live-prog" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${p.pct}" aria-label="${esc(t("Progreso"))}">
					<span class="live-prog-l">${esc(t("Progreso"))}${p.eta ? ` · <span data-tip="${esc(t("Tiempo estimado para terminar, según el ritmo de los últimos minutos y los tests que faltan."))}">${esc(t("ETA {t}", { t: fmtDur(p.eta) }))}</span>` : ""}</span>
					<span class="live-hero"><b data-count="pct" data-n="${p.pct}">${p.pct}</b><small>%</small><span class="live-pct">${pctText}</span></span>
					<div class="live-bar ${e.running ? "on" : ""}">${mix(e).map((s) => `<i class="${s.cls}" style="width:${s.w.toFixed(2)}%"></i>`).join("")}</div></div>${tiles}</div>${healthHTML(e, data.list, now, h)}</div>`;

		// (2) controles: qué mostrar y cómo agrupar (controles segmentados), pausa, buscar; y la línea de estado
		const nProblems = tests.filter((x) => isProblem(x.state)).length;
		const all = !problems && !ui.states.length;
		const seg = (key, label, opts) => `<div class="seg" role="radiogroup" aria-label="${esc(label)}" data-seg="${key}"><span class="seg-thumb" aria-hidden="true"></span>${opts.join("")}</div>`;
		const showSeg = seg("show", t("Mostrar"), [
			`<button type="button" role="radio" class="seg-o ${all ? "on" : ""}" data-live-all aria-checked="${all}" data-tip-title="${esc(t("Todos"))}" data-tip="${esc(t("Muestra todos los tests de la ejecución, también los que pasaron."))}">${icon("i-list")}<span>${esc(t("Todos"))}</span><b class="seg-n">${tests.length}</b></button>`,
			`<button type="button" role="radio" class="seg-o ${problems && !ui.states.length ? "on" : ""}" data-live-problems aria-checked="${problems && !ui.states.length}" data-tip-title="${esc(t("Con problemas"))}" data-tip="${esc(t("Solo lo que pide acción: fallos, interrumpidos, sin señal y tortugas. Viene activo en ejecuciones grandes."))}">${icon("i-st-fail")}<span>${esc(t("Con problemas"))}</span><b class="seg-n ${nProblems ? "bad" : ""}">${nProblems}</b></button>`,
		]);
		const groupSeg = seg("group", t("Agrupar por"), Object.entries(GROUPS).map(([k, gr]) => `<button type="button" role="radio" class="seg-o ${ui.group === k ? "on" : ""}" data-live-groupby="${k}" aria-checked="${ui.group === k}" data-tip-title="${esc(t("Agrupar por {g}", { g: t(gr.label).toLowerCase() }))}" data-tip="${esc(t(gr.tip))}">${icon(gr.icon)}<span>${esc(t(gr.label))}</span></button>`));
		const filters = `<div class="card live-filters">
			<span class="lf-lbl">${esc(t("Mostrar"))}</span>${showSeg}
			<span class="lf-lbl">${esc(t("Agrupar por"))}</span>${groupSeg}
			${activeChips(ui, h)}
			<span class="live-spacer"></span>
			<span class="live-qw">${icon("i-search")}<input type="search" class="live-q" data-live-q value="${esc(ui.q)}" placeholder="${esc(t("Buscar test, archivo o paso…"))}" aria-label="${esc(t("Buscar"))}" aria-keyshortcuts="/"><kbd aria-hidden="true" data-tip="${esc(t("Atajo: / para buscar, Esc para limpiar"))}">/</kbd></span>
			<i class="live-busybar" aria-hidden="true"></i></div>`;

		// lo que muestra la tabla; con un filtro elegido (tile, faceta o búsqueda) el Gantt y el mapa atenúan el resto
		const visible = filterTests(tests, ui, problems);
		const faceted = ui.states.length || ui.files.length || ui.workers.length || ui.errors.length;
		const dimOn = !!(faceted || (ui.q || "").trim());
		const match = new Set(visible.map((x) => x.id));
		const mode = ui.states.length ? ui.states.map((k) => t(stateOf(k).label)).join(", ") : problems ? t("Solo con problemas") : t("Todos");
		const [before, after] = t("Mostrando {n} de {t} tests", { n: "\u0000", t: tests.length }).split("\u0000");
		const status = `<p class="live-status" role="status" aria-live="polite"><span class="ls-dot ${e.running && !ui.paused ? "on" : ""}" aria-hidden="true"></span>
			<span>${esc(before)}<b data-count="shown" data-n="${visible.length}">${visible.length}</b>${esc(after || "")}</span>
			<span class="ls-sep">·</span><span>${esc(mode)}</span>${ui.q ? `<span class="ls-sep">·</span><span>${esc(t("búsqueda «{q}»", { q: ui.q }))}</span>` : ""}
			<span class="ls-sep">·</span><span>${esc(t("Agrupado por {g}", { g: t(GROUPS[ui.group]?.label || "Archivo").toLowerCase() }))}</span></p>`;

		// (2b) el mapa de la ejecución: una celda por test (empezados, en orden) y las planificadas que faltan
		const hm = heatCells(e);
		const hmDim = dimOn || problems;
		const cell = (x) => {
			const st = stateOf(x.state), off = hmDim && !match.has(x.id);
			const tip = `${t(st.label)} · ${fmtDur(x.elapsed)}${x.p50 && x.factor ? ` · ×${num1(x.factor)} ${t("de lo normal")}` : ""}\n${x.file}`;
			return `<button type="button" class="hm-c ${st.cls}${off ? " dim" : ""}${data.panel?.id === x.id ? " sel" : ""}" data-live-test="${x.id}" data-live-run="${x.run_id}" data-tip-title="${esc(x.name)}" data-tip="${esc(tip)}" aria-label="${esc(`${x.name} · ${tip.replace(/\n/g, " · ")}`)}"></button>`;
		};
		const legend = `<span class="hm-legend">${["PASS", "WARNING", "FAIL", "turtle", "no_signal", "running"].filter((k) => e.counts[k]).map((k) => `<span class="${stateOf(k).cls}" data-tip="${esc(stTip(stateOf(k)))}"><i></i>${esc(t(stateOf(k).label))}</span>`).join("")}${hm.pending ? `<span class="pending" data-tip="${esc(t("Tests planificados que todavía no empiezan."))}"><i></i>${esc(t("Pendiente"))}</span>` : ""}</span>`;
		const grid = `<div class="hm-grid">${hm.tests.map(cell).join("")}${hm.pending ? `<span class="hm-c pending" aria-hidden="true"></span>`.repeat(hm.pending) : ""}</div>`;
		const view = ui.fieldView === "grid" ? "grid" : "field";
		const tabs = `<div class="seg seg-sm" role="radiogroup" aria-label="${esc(t("Vista del mapa"))}" data-seg="field"><span class="seg-thumb" aria-hidden="true"></span>
			<button type="button" role="radio" class="seg-o ${view === "field" ? "on" : ""}" data-live-fieldview="field" aria-checked="${view === "field"}" data-tip-title="${esc(t("Campo"))}" data-tip="${esc(t("La ejecución como un reloj: el ángulo es el tiempo y cada anillo un worker o proceso. Cada arco es un test; clic para abrirlo."))}">${icon("i-orbit")}<span>${esc(t("Campo"))}</span></button>
			<button type="button" role="radio" class="seg-o ${view === "grid" ? "on" : ""}" data-live-fieldview="grid" aria-checked="${view === "grid"}" data-tip-title="${esc(t("Cuadrícula"))}" data-tip="${esc(t("Un cuadro por test, en el orden en que empezaron. La misma información, más compacta y navegable con Tab."))}">${icon("i-grid")}<span>${esc(t("Cuadrícula"))}</span></button></div>`;
		const map = tests.length ? `<section class="card live-map ${view === "field" ? "is-field" : ""}" aria-label="${esc(t("Mapa de la ejecución"))}">
			<header class="lp-h"><h6>${esc(t("Mapa de la ejecución"))}</h6><span class="m-hint">${esc(view === "field" ? t("el tiempo en círculo: arriba el comienzo, la aguja punteada es ahora") : t("un cuadro por test, en el orden en que empezaron"))}</span><span class="live-spacer"></span>
				${legend}${tabs}</header>
			${view === "field" ? fieldHTML(e, tests, data, ui, now, h, g) : grid}</section>` : "";

		// (3) línea de tiempo: minimapa con el recuadro del zoom, carriles con las pausas comprimidas y "ahora"
		let gantt = "";
		if (g.lanes.length) {
			// con un filtro (estado, faceta o búsqueda): el minimapa cuenta solo lo que coincide
			const mm = minimap(e, now, MINIMAP_BUCKETS, dimOn ? match : null);
			const brush = ui.zoom ? `<div class="lg-brush" style="left:${ui.zoom.a * 100}%;width:${(ui.zoom.b - ui.zoom.a) * 100}%"></div>` : "";
			// con un filtro, los carriles sin ningún test que coincida se juntan en una línea; con "Con
			// problemas", los que no tienen problemas
			const hits = (l) => l.ids.filter((id) => match.has(id)).length;
			const keep = dimOn ? (l) => hits(l) > 0 : problems ? (l) => l.problem : () => true;
			let shown = ui.allLanes ? g.lanes : g.lanes.filter(keep);
			const idle = e.running ? shown.filter((l) => !l.running && !l.problem) : [];
			if (ui.hideIdle) shown = shown.filter((l) => !idle.includes(l));
			const hidden = g.lanes.length - shown.length - (ui.hideIdle ? idle.length : 0);
			const procTip = t("Carril inferido: el cliente no informa el worker, así que cada corrida ocupa el primer carril libre, como un proceso en paralelo. {n} carriles = {n} tests a la vez como máximo.", { n: g.lanes.length });
			// etiqueta del carril: el worker o proceso y, al lado, el archivo que corre ahora; todo en el tooltip
			const laneHTML = (l) => {
				const name = l.proc ? t("Proceso {n}", { n: l.proc }) : l.worker;
				const tip = (l.proc ? procTip + "\n" : "") + (l.current ? t("Archivo actual: {f}", { f: l.current }) + (l.title.includes("\n") ? "\n" + l.title : "") : l.title);
				// el nombre siempre del mismo color; lo que pide acción va en una marca con su cuenta y su explicación
				const flag = l.nProblems ? `<i class="lg-flag" data-tip="${esc(t("{n} tests con problema en este carril (fallo, sin señal, interrumpido o tortuga).", { n: l.nProblems }))}">${l.nProblems}</i>` : "";
				return `<div class="lg-lane ${l.problem ? "problem" : ""} ${l.running ? "busy" : "idle"}" data-lane="${esc(l.key)}"><span class="lg-worker" data-live-lane="${esc(l.key)}" data-tip-title="${esc(name)}" data-tip="${esc(tip + "\n" + t("Pasa el mouse para resaltar sus tests en la tabla."))}" tabindex="0"><b>${l.running ? `<i class="lg-live" aria-hidden="true"></i>` : ""}<span data-no-i18n>${esc(name)}</span>${flag}</b>${l.sub ? `<small data-no-i18n>${esc(l.sub)}</small>` : ""}</span><div class="lg-track">${l.bars.map((b) => {
					const st = stateOf(b.state);
					const tip = [b.file, `${t(st.label)} · ${fmtDur(b.elapsed)}${b.factor ? ` · ×${num1(b.factor)} ${t("de lo normal")}` : ""}`,
						t("{a} → {b}", { a: h.clock(b.started_at, true), b: b.ended_at ? h.clock(b.ended_at, true) : t("ahora") })].join("\n");
					const off = dimOn && !match.has(b.id), hit = dimOn && !off;
					return `<button type="button" class="lg-bar ${st.cls}${off ? " dim" : ""}${hit ? " hit" : ""}${data.panel?.id === b.id ? " sel" : ""}" data-live-test="${b.id}" data-live-run="${b.run_id}" style="left:${b.left.toFixed(3)}%;width:${b.width.toFixed(3)}%" data-tip-title="${esc(b.name)}" data-tip="${esc(tip)}" aria-label="${esc(`${b.name} · ${tip.replace(/\n/g, " · ")}`)}">${GLYPH.has(b.state) && b.width >= 1.6 ? icon(st.icon) : ""}${b.width >= 5 ? midName(b.short, esc) : ""}</button>`;
				}).join("")}</div></div>`;
			};
			const overlay = `<div class="lg-overlay" aria-hidden="true">${g.gaps.map((x) => `<span class="lg-gap" style="left:${x.left.toFixed(3)}%;width:${x.width.toFixed(3)}%"><b>≈</b><small>${esc(fmtDur(x.dur))}</small></span>`).join("")}${g.nowLeft !== null ? `<span class="lg-now" style="left:${g.nowLeft.toFixed(3)}%"><small>${esc(t("ahora"))}</small></span>` : ""}</div>`;
			const zoomBtn = (dir, ic, label, tip, keys, dis) => `<button type="button" class="cf-btn cf-btn-sm lg-zb" data-live-zoom="${dir}" aria-label="${esc(label)}" data-tip-title="${esc(label)}" data-tip="${esc(tip)}"${keys ? ` data-tip-keys="${esc(keys)}"` : ""} ${dis ? "disabled" : ""}>${icon(ic)}${dir === "fit" ? `<span>${esc(label)}</span>` : ""}</button>`;
			gantt = `<div class="card live-gantt">
				<div class="lg-head lp-h"><h6>${esc(t("Línea de tiempo"))}</h6>${dimOn ? `<span class="lg-count" role="status">${esc(t("{n} de {t} tests coinciden", { n: match.size, t: tests.length }))}</span>` : ""}<span class="m-hint">${ui.zoom ? esc(t("zoom {a}–{b}", { a: h.clock(g.a), b: h.clock(g.b) })) : esc(t("{n} carriles", { n: g.lanes.length }))}${g.gaps.length ? ` · ${esc(t("{n} pausas comprimidas (≈)", { n: g.gaps.length }))}` : ""}</span>
					<span class="live-spacer"></span>
					${idle.length || ui.hideIdle ? `<button type="button" class="cf-btn cf-btn-sm ${ui.hideIdle ? "on" : ""}" data-live-hideidle aria-pressed="${!!ui.hideIdle}" data-tip="${esc(t("Oculta los carriles que ya no tienen un test corriendo, para ver solo lo que avanza."))}">${icon("i-eye-off")}${esc(ui.hideIdle ? t("Mostrar inactivos") : t("Ocultar inactivos ({n})", { n: idle.length }))}</button>` : ""}
					${zoomBtn("out", "i-zoom-out", t("Alejar"), t("Muestra el doble de tiempo."), "Ctrl + rueda", !ui.zoom)}
					${zoomBtn("in", "i-zoom-in", t("Acercar"), t("Muestra la mitad del tiempo, alrededor del centro. También arrastrando sobre el minimapa."), "Ctrl + rueda", false)}
					${zoomBtn("fit", "i-fit", t("Ajustar a la ventana"), t("Vuelve a mostrar la ejecución entera."), "", !ui.zoom)}</div>
				<div class="lg-mini" data-live-minimap data-tip="${esc(t("Toda la ejecución: el alto es cuántos tests corrían a la vez. Arrastra para acercar un tramo; clic para centrar."))}">${mm.map((c) => c.n ? `<i style="left:${c.left.toFixed(2)}%;height:${Math.max(8, c.h * 100).toFixed(0)}%">${c.parts.map((x) => `<b class="${x.cls}" style="height:${(x.f * 100).toFixed(1)}%"></b>`).join("")}</i>` : "").join("")}${brush}
					${anomalyMarks(anomalies(e, now)).map((m) => {
						const a = m.items[0], st = stateOf(a.state);
						const tip = m.items.length === 1 ? `${t(st.label)} · ${a.name} · ${h.clock(a.at, true)}` : `${t("{n} fallos o tests sin señal en este tramo", { n: m.items.length })}\n${m.items.slice(0, 6).map((x) => `${h.clock(x.at, true)} · ${x.name}`).join("\n")}${m.items.length > 6 ? "\n…" : ""}`;
						return `<button type="button" class="lg-anom ${st.cls}${m.items.length > 1 ? " many" : ""}" data-live-test="${a.id}" data-live-run="${a.run_id}" style="left:${m.left.toFixed(2)}%" data-tip="${esc(tip)}" aria-label="${esc(tip.replace(/\n/g, " · "))}">${m.items.length > 1 ? `<b>${m.items.length}</b>` : icon(st.icon)}</button>`;
					}).join("")}</div>
				<div class="lg-axis" aria-hidden="true">${scaleTicks(g).map((k) => `<span style="left:${k.left.toFixed(2)}%">${esc(h.clock(k.at, g.b - g.a < 10 * 60e3))}</span>`).join("")}</div>
				<div class="lg-lanes" data-live-lanes>${overlay}${shown.map(laneHTML).join("")}
				${hidden > 0 ? `<button type="button" class="lg-more" data-live-alllanes data-tip="${esc(dimOn ? t("Muestra también los carriles sin ningún test que coincida con el filtro.") : t("Muestra también los carriles donde todo pasó."))}">${icon("i-chevron")}${esc(dimOn ? t("{n} carriles sin coincidencias", { n: hidden }) : t("{n} carriles sin problemas", { n: hidden }))} · ${esc(t("mostrar"))}</button>` : ""}
				${(problems || dimOn) && ui.allLanes && g.lanes.some((l) => !keep(l)) ? `<button type="button" class="lg-more" data-live-alllanes data-tip="${esc(dimOn ? t("Deja solo los carriles con tests que coinciden.") : t("Deja solo los carriles con algo que pide acción."))}">${esc(dimOn ? t("Ocultar los carriles sin coincidencias") : t("Ocultar los carriles sin problemas"))}</button>` : ""}
				${!shown.length && !hidden ? `<p class="m-hint">${esc(t("Nada en este tramo."))}</p>` : ""}</div>
				<div class="lg-legend">${["PASS", "WARNING", "FAIL", "turtle", "no_signal", "running"].map((k) => `<span class="${stateOf(k).cls}" data-tip="${esc(stTip(stateOf(k)))}"><i class="sw"></i>${icon(stateOf(k).icon)}${esc(t(stateOf(k).label))}</span>`).join("")}
					${g.lanes.some((l) => l.proc) ? `<span data-tip="${esc(procTip)}"><b>${esc(t("Proceso N"))}</b> ${esc(t("= carril en paralelo"))}</span>` : ""}
					${g.gaps.length ? `<span data-tip="${esc(t("Un rato sin ningún test corriendo: se dibuja angosto para no dejar un vacío; dice cuánto duró."))}"><b>≈</b> ${esc(t("= pausa comprimida"))}</span>` : ""}
					<span data-tip="${esc(t("La duración del test frente a su mediana (p50) en sus ejecuciones anteriores del mismo proyecto."))}"><b>×${num1(1.7)}</b> ${esc(t("= 1,7 veces lo normal"))}</span></div></div>`;
		}

		// tabla: grupos por severidad; los sin problemas colapsados en una línea
		const groups = ui.sort?.col ? [] : groupTests(visible, ui.group);
		// con muchos archivos, los sin problemas en una sola línea (con pocos, se ven todos); "Expandir todo" los abre
		const plainGroups = ui.group === "file" && !problems && !faceted && !ui.q && !ui.showOk && ui.openAll !== true && groups.length > COLLAPSE_FROM ? groups.filter((x) => !x.problem) : [];
		const listed = groups.filter((x) => !plainGroups.includes(x));
		// columnas fijas para todos los archivos (una sola tabla): el worker, columna propia si el cliente lo informa
		const wcol = ui.group !== "worker" && tests.some((x) => x.worker);
		const ncols = wcol ? 6 : 5;
		const nameW = ui.nameW ? ` style="width:${Math.round(ui.nameW)}px"` : "";
		const colgroup = `<colgroup><col class="c-name"${nameW}><col class="c-state"><col class="c-dur"><col class="c-vs">${wcol ? `<col class="c-wk">` : ""}<col class="c-step"></colgroup>`;
		const row = (x) => {
			const st = stateOf(x.state);
			const vs = x.p50 ? `<span class="lt-vs ${x.factor >= 2 ? "hi" : ""}" data-tip="${esc(t("Tardó {f} veces lo normal para este test (su mediana en ejecuciones anteriores: {m}).", { f: num1(x.factor), m: fmtDur(x.p50) }))}">×${num1(x.factor)}</span>` : `<span class="m-hint" data-tip="${esc(t("Sin historial: es la primera vez que corre en este proyecto."))}">—</span>`;
			const ago = x.state === "running" || x.state === "turtle" || x.state === "no_signal" ? `<small class="lt-ago" data-tip="${esc(t("Hace cuánto llegó el último evento del test."))}">${esc(t("hace {t}", { t: fmtDur(x.idle) }))}</small>` : "";
			// una línea: XPaths acortados, sin URLs; el stack trace, en un badge que lo abre debajo
			const sp = stepParts(x.last_step);
			const stackOpen = !!(sp.trace && ui.stacks?.[x.id]);
			const stack = sp.trace ? `<button type="button" class="lt-stack ${stackOpen ? "on" : ""}" data-live-stack="${x.id}" aria-expanded="${stackOpen}" data-tip="${esc(t("Ver el stack trace"))}">${esc(t("stack trace"))}</button>` : "";
			const full = sp.full.slice(0, 700); // el tooltip: el paso completo, con su XPath (el volcado va en el badge)
			return `<tr class="lt-row ${st.cls} ${data.panel?.id === x.id ? "sel" : ""}" data-live-test="${x.id}" data-live-run="${x.run_id}" data-lane="${esc(laneOf.get(x.id) || "")}" tabindex="0">
				<td class="lt-name"><div class="lt-namew"><a href="#run=${x.run_id}&view=tests&test=${x.id}" data-tip-title="${esc(x.name)}" data-tip="${esc(t("Abrir el test con sus pasos, capturas y red. Clic en la fila: el detalle al costado."))}">${midName(x.name, esc)}</a><button type="button" class="lt-copy" data-live-copy="${esc(x.name)}" aria-label="${esc(t("Copiar el nombre"))}" data-tip="${esc(t("Copiar el nombre completo"))}">${icon("i-copy")}</button></div>${x.worker && ui.group !== "worker" && !wcol ? ` <span class="m-hint" data-no-i18n>${esc(x.worker)}</span>` : ""}</td>
				<td><span class="lt-state ${st.cls}" data-tip="${esc(t(st.tip))}">${icon(st.icon)}${esc(t(st.label))}</span>${regBadge(regressionOf(data.regressions, x.id), h)}</td>
				<td class="lt-num">${esc(fmtDur(x.elapsed))}</td><td class="lt-num">${vs}</td>
				${wcol ? `<td class="lt-wk" data-no-i18n>${esc(x.worker || "—")}</td>` : ""}
				<td class="lt-step"><div class="lt-stepw"><span class="lt-steptext" data-no-i18n${full ? ` data-tip="${esc(full)}"` : ""}>${esc(sp.text || (sp.trace ? "" : "—"))}</span>${stack}${ago}</div></td></tr>
				${stackOpen ? `<tr class="lt-stackrow"><td colspan="${ncols}"><pre data-no-i18n>${esc(sp.trace)}</pre></td></tr>` : ""}`;
		};
		const rowLimit = Math.max(ROWS_STEP, ui.rowLimit || ROWS_STEP);
		const moreRows = (left) => `<button type="button" class="live-collapsed lt-more" data-live-rowsmore data-tip="${esc(t("Dibuja {n} filas más. Para encontrar algo puntual, busca o filtra: es más rápido.", { n: Math.min(ROWS_STEP, left) }))}">${icon("i-chevron")}${esc(t("Mostrar {n} más ({t} sin mostrar)", { n: Math.min(ROWS_STEP, left), t: left }))}</button>`;
		let budget = rowLimit, cut = 0;
		const groupHTML = (gr) => {
			const open = gr.key in ui.open ? ui.open[gr.key] : typeof ui.openAll === "boolean" ? ui.openAll : gr.problem || listed.length <= 3 || !!ui.q || faceted > 0;
			const sum = Object.keys(STATES).filter((k) => gr.counts[k]).map((k) => `<span class="lt-state ${stateOf(k).cls}" data-tip="${esc(`${gr.counts[k]} ${t(stateOf(k).label)}`)}">${icon(stateOf(k).icon)}${gr.counts[k]}</span>`).join(" ");
			const name = ui.group === "state" ? t(stateOf(gr.key).label) : ui.group === "error" ? (gr.key === NO_ERROR ? t("Sin fallo") : shortXPaths(gr.key)) : gr.key;
			return `<tbody class="live-run ${open ? "open" : ""}" data-group="${esc(gr.key)}"><tr class="lt-group"><td colspan="${ncols}"><button type="button" class="live-run-h" data-live-group="${esc(gr.key)}" aria-expanded="${open}" data-tip="${esc(open ? t("Clic: cerrar este grupo.") : t("Clic: ver sus tests."))}">
					<span class="live-chev" aria-hidden="true">${icon("i-chevron")}</span><span class="live-sev ${stateOf(gr.tests[0].state).cls}" aria-hidden="true"></span><b data-no-i18n>${esc(name)}</b>
					<span class="live-run-sum">${sum}${gr.worker ? ` · <span data-no-i18n>${esc(gr.worker)}</span>` : ""}</span></button></td></tr>
				${open ? (() => {
					const take = gr.tests.slice(0, Math.max(0, budget));
					budget -= take.length;
					cut += gr.tests.length - take.length;
					return take.map(row).join("") + (take.length < gr.tests.length && take.length ? `<tr class="lt-cut"><td colspan="${ncols}">${esc(t("y {n} más en este grupo", { n: gr.tests.length - take.length }))}</td></tr>` : "");
				})() : ""}</tbody>`;
		};
		const emptyRuns = (e.runs || []).filter((r) => !(r.tests || []).length);
		const hiddenTests = tests.length - visible.length;
		let collapsed = "";
		if (plainGroups.length) {
			const n = plainGroups.reduce((s, x) => s + x.tests.length, 0), w = plainGroups.filter((x) => x.counts.WARNING).length;
			collapsed = `<button type="button" class="live-collapsed" data-live-showok data-tip="${esc(t("Abre los archivos donde todo pasó; quedan cerrados, con su resumen."))}">${icon("i-chevron")}${esc(t("{f} archivos sin problemas ({n} tests)", { f: plainGroups.length, n }))}${w ? ` · ${esc(t("{n} con warning", { n: w }))}` : ""} · ${esc(t("mostrar"))}</button>`;
		} else if (problems && !faceted && !ui.q && hiddenTests > 0) {
			collapsed = `<button type="button" class="live-collapsed" data-live-problems data-tip="${esc(t("Apaga Con problemas y muestra todos los tests."))}">${icon("i-chevron")}${esc(t("{n} tests sin problemas ocultos", { n: hiddenTests }))} · ${esc(t("mostrar todos"))}</button>`;
		} else if (ui.showOk && ui.group === "file" && !problems && !faceted && !ui.q && groups.length > COLLAPSE_FROM && groups.some((x) => !x.problem)) {
			collapsed = `<button type="button" class="live-collapsed" data-live-showok data-tip="${esc(t("Vuelve a juntar en una línea los archivos donde todo pasó."))}">${esc(t("Ocultar los archivos sin problemas"))}</button>`;
		}
		const sorted = ui.sort?.col ? sortTests(visible, ui.sort) : [];
		const nothing = !listed.length && !plainGroups.length && !sorted.length
			? `<p class="m-hint live-none">${esc(problems && !faceted && !ui.q ? t("Ningún test con problemas por ahora.") : t("Ningún test coincide con el filtro."))}</p>` : "";
		const th = (col, label, tip, extraHTML = "") => {
			const on = ui.sort?.col === col, dir = on ? (ui.sort.dir > 0 ? "ascending" : "descending") : "none";
			return `<th aria-sort="${dir}"${col === "name" ? ` class="th-name"` : ""}><button type="button" class="lt-sort ${on ? "on" : ""}" data-live-sort="${col}" data-tip-title="${esc(t(label))}" data-tip="${esc(`${tip ? tip + " " : ""}${t("Clic: ordenar por esta columna (otro clic invierte; el tercero vuelve a agrupar).")}`)}">${esc(t(label))}${on ? (ui.sort.dir > 0 ? " ▲" : " ▼") : ""}</button>${extraHTML}</th>`;
		};
		const resize = `<span class="lt-resize" data-live-resize role="separator" aria-orientation="vertical" tabindex="0" aria-label="${esc(t("Ancho de la columna Test"))}" data-tip="${esc(t("Arrastra para cambiar el ancho de la columna (con el teclado: ← →). Doble clic: el ancho de antes."))}"></span>`;
		const thead = `<thead><tr>${th("name", "Test", "", resize)}${th("state", "Estado", t("Ordena por gravedad: sin señal, fallo, interrumpido, tortuga, warning…"))}${th("elapsed", "Dura.", t("Lo que lleva o lo que duró."))}${th("factor", "vs normal", t("La duración frente a la mediana (p50) de sus ejecuciones anteriores: ×1,7 = tardó 1,7 veces lo normal."))}${wcol ? `<th><span data-tip="${esc(t("El proceso o worker en paralelo que lo corrió."))}">${esc(t("Worker"))}</span></th>` : ""}<th><span data-tip="${esc(t("El último paso que informó el test; el completo, en el tooltip y en el detalle."))}">${esc(t("Último paso"))}</span></th></tr></thead>`;
		const flat = sorted.length ? `<table class="live-table">${colgroup}${thead}<tbody>${sorted.slice(0, rowLimit).map(row).join("")}</tbody></table>
			${sorted.length > rowLimit ? moreRows(sorted.length - rowLimit) : ""}
			<button type="button" class="live-collapsed" data-live-sort="" data-tip="${esc(t("Quita el orden por columna y vuelve a los grupos."))}">${esc(t("Volver a agrupar"))}</button>` : "";
		const canFold = !sorted.length && groups.length > 1;
		const table = `<div class="card live-runs ${ui.wrap ? "wrap-names" : ""}"><div class="lr-head lp-h"><h6>${esc(ui.group === "worker" ? t("Worker / test") : ui.group === "state" ? t("Estado / test") : ui.group === "error" ? t("Error / test") : t("Archivo / test"))} <span class="m-hint">${esc(t("{n} de {t} tests", { n: visible.length, t: tests.length }))}</span></h6>
			<span class="live-spacer"></span>
			<button type="button" class="cf-btn cf-btn-sm ${ui.wrap ? "on" : ""}" data-live-wrap aria-pressed="${!!ui.wrap}" data-tip="${esc(ui.wrap ? t("Vuelve a mostrar cada nombre en una línea, con la elipsis en el medio.") : t("Muestra los nombres largos completos, en hasta dos líneas."))}">${icon("i-wrap")}${esc(t("Nombres en 2 líneas"))}</button>
			${canFold ? `<button type="button" class="cf-btn cf-btn-sm" data-live-openall="1" data-tip="${esc(t("Abre todos los grupos, también los que no tienen problemas."))}">${icon("i-unfold")}${esc(t("Expandir todo"))}</button><button type="button" class="cf-btn cf-btn-sm" data-live-openall="0" data-tip="${esc(t("Cierra todos los grupos: queda una línea por grupo con su resumen."))}">${icon("i-fold")}${esc(t("Colapsar todo"))}</button>` : ""}</div>
			${flat}${listed.length ? `<table class="live-table">${colgroup}${thead}
				${listed.map(groupHTML).join("")}</table>${cut > 0 ? moreRows(cut) : ""}` : ""}${collapsed}${nothing}
			${emptyRuns.length && ui.group === "file" && !problems ? emptyRuns.map((r) => `<div class="live-run"><div class="live-run-h"><b data-no-i18n>${esc(r.name)}</b> <span class="m-hint">${esc(t("Todavía sin tests."))}</span></div></div>`).join("") : ""}
			<p class="m-hint lt-foot">${icon("i-st-turtle")}<span>${esc(t("Tortuga: más lento que su p90 (o el doble de su mediana) en sus ejecuciones anteriores. Sin señal: nada del test hace {n} min.", { n: Math.round((data.no_signal_ms || 180000) / 60000 * 10) / 10 }))}</span></p></div>`;

		const explore = `<div class="live-explore">${facetsHTML(tests, ui, h)}${table}</div>`;
		// Mostrar, Agrupar por y Buscar son de la tabla (y sus facetas): van pegados a ella, con su línea de estado
		const gFull = ui.zoom ? ganttLayout(e, now, null) : g;
		return `${head}${summaryHTML(e, tests, data, now, h)}${attentionHTML(tests, data, ui, h)}${map}${insightsHTML(e, tests, data, ui, now, h)}${chartsHTML(e, data, ui, now, h)}${gantt}<div class="live-tablebar">${filters}${status}</div>${explore}${panelHTML(data.panel, tests, h, now, gFull)}${ui.help ? helpHTML(h) : ""}`;
	}

	/**
	 * "Requiere atención": arriba de todo, los tests en fallo, sin señal, interrumpidos o tortuga, con
	 * archivo, worker, duración y ×p50; un clic abre el detalle. Si no hay, una línea que lo dice.
	 */
	/**
	 * ¿Este fallo es nuevo? Con el historial del test (app.js lo trae solo para los que fallaron): "new"
	 * si en su ejecución anterior pasó, "old" si ya fallaba; sin historial, nada.
	 */
	function regressionOf(regs, id) { return regs ? regs[id] || "" : ""; }
	/** La marca de un fallo nuevo o de uno que ya fallaba, con su explicación. */
	function regBadge(kind, h) {
		const { t, esc } = h;
		if (kind === "new") return `<span class="lt-reg new" data-tip="${esc(t("Fallo nuevo: en su ejecución anterior este test pasó. Es lo primero que conviene mirar."))}">${esc(t("Nuevo"))}</span>`;
		if (kind === "old") return `<span class="lt-reg old" data-tip="${esc(t("Ya fallaba en su ejecución anterior: no es una regresión de hoy."))}">${esc(t("Ya fallaba"))}</span>`;
		if (kind === "first") return `<span class="lt-reg first" data-tip="${esc(t("Es la primera vez que este test corre (o cambió de nombre): no hay una ejecución anterior con qué comparar."))}">${esc(t("Primera vez"))}</span>`;
		return "";
	}
	/** Mientras se revisa el historial de un fallo: una marca que lo dice (no un hueco). */
	function regPending(h) {
		const { t, esc } = h;
		return `<span class="lt-reg wait" data-tip="${esc(t("Revisando su historial para saber si es un fallo nuevo…"))}">${esc(t("revisando…"))}</span>`;
	}

	/**
	 * "Requiere atención": arriba de todo, los tests en fallo, sin señal, interrumpidos o tortuga, con
	 * archivo, worker, duración y ×p50, y si el fallo es nuevo. Tiene su propio buscador y filtro por
	 * estado (no tocan la tabla); un clic abre el detalle. Si no hay nada, una línea que lo dice.
	 */
	function attentionHTML(tests, data, ui, h) {
		const { t, esc, icon } = h;
		const all = attention(tests);
		if (!all.length) {
			return `<div class="card live-attn ok" role="status">${icon("i-pass")}<b>${esc(t("Nada requiere atención"))}</b><span class="m-hint">${esc(t("Sin fallos, tests sin señal ni tortugas."))}</span></div>`;
		}
		const regs = data.regressions || null;
		const aq = (ui.attnQ || "").trim().toLowerCase(), ast = ui.attnStates || [];
		const list = all.filter((x) => (!ast.length || ast.includes(x.state) || (ast.includes("new") && regressionOf(regs, x.id) === "new"))
			&& (!aq || `${x.name} ${x.file} ${x.worker || ""} ${x.last_step || ""}`.toLowerCase().includes(aq)));
		const MAX = 8, shown = ui.attnAll || aq || ast.length ? list : list.slice(0, MAX);
		const by = {};
		for (const x of all) by[x.state] = (by[x.state] || 0) + 1;
		const nNew = all.filter((x) => regressionOf(regs, x.id) === "new").length;
		// el resumen es también el filtro: cada estado (y "nuevos") se elige con un clic
		const chip = (k, n, cls, ic, label, tip) => `<button type="button" class="la-chip ${cls} ${ast.includes(k) ? "on" : ""}" data-live-attnstate="${k}" aria-pressed="${ast.includes(k)}" data-tip="${esc(tip)}">${ic ? icon(ic) : ""}${n} ${esc(label)}</button>`;
		const sum = Object.keys(STATES).filter((k) => by[k]).map((k) => chip(k, by[k], stateOf(k).cls, stateOf(k).icon, t(stateOf(k).label), t("Clic: ver solo estos en la lista (otro clic lo quita).")))
			.join("") + (nNew ? chip("new", nNew, "new", "", nNew === 1 ? t("nuevo") : t("nuevos"), t("Fallos nuevos: en su ejecución anterior pasaron. Clic: ver solo esos.")) : "");
		const rows = shown.map((x, i) => {
			const st = stateOf(x.state);
			return `<li style="--i:${i}" data-attn="${x.id}"><button type="button" class="la-row ${st.cls} ${data.panel?.id === x.id ? "sel" : ""}" data-live-test="${x.id}" data-live-run="${x.run_id}" data-tip="${esc(t("Clic: el detalle al costado (traza, últimos pasos y captura). Doble clic: abrir el test."))}" aria-label="${esc(`${t(st.label)} · ${x.name} · ${x.file}`)}">
				<span class="lt-state ${st.cls}">${icon(st.icon)}${esc(t(st.label))}${regBadge(regressionOf(regs, x.id), h)}</span>
				<span class="la-name" data-tip="${esc(x.name)}">${midName(x.name, esc)}</span>
				<span class="la-file" data-no-i18n data-tip="${esc(x.file)}">${esc(x.file)}</span>
				<span class="la-wk" data-no-i18n>${esc(x.worker || "")}</span>
				<span class="la-dur lt-num">${esc(fmtDur(x.elapsed))}${x.state === "no_signal" ? ` <small>${esc(t("sin señal hace {t}", { t: fmtDur(x.idle) }))}</small>` : ""}</span>
				<span class="la-vs">${x.p50 && x.factor ? `<span class="lt-vs ${x.factor >= 2 ? "hi" : ""}">×${num1(x.factor)}</span>` : ""}</span></button></li>`;
		}).join("");
		const more = !aq && !ast.length && list.length > MAX ? `<button type="button" class="live-collapsed" data-live-attnmore data-tip="${esc(ui.attnAll ? t("Vuelve a los 8 más graves.") : t("Muestra todo lo que requiere atención."))}">${icon("i-chevron")}${esc(ui.attnAll ? t("ver menos") : t("ver {n} más", { n: list.length - MAX }))}</button>` : "";
		const none = !list.length ? `<p class="m-hint la-none">${esc(t("Nada coincide con la búsqueda o el filtro."))}</p>` : "";
		return `<section class="card live-attn" aria-label="${esc(t("Requiere atención"))}"><div class="la-head"><h6>${icon("i-warning")}${esc(t("Requiere atención"))} <b class="la-n" data-bump="attn">${all.length}</b></h6><span class="la-sum">${sum}</span>
				<span class="live-spacer"></span>${ui.attnAll && !aq && !ast.length && list.length > MAX ? `<button type="button" class="cf-btn cf-btn-sm la-less" data-live-attnmore data-tip="${esc(t("Vuelve a los 8 más graves."))}">${icon("i-fold")}${esc(t("ver menos"))}</button>` : ""}<span class="la-qw">${icon("i-search")}<input type="search" class="la-q" data-live-aq value="${esc(ui.attnQ || "")}" placeholder="${esc(t("Buscar en lo que requiere atención…"))}" aria-label="${esc(t("Buscar en lo que requiere atención"))}"></span></div>
			${aq || ast.length ? `<p class="la-status" role="status">${esc(t("{n} de {t}", { n: list.length, t: all.length }))}</p>` : ""}
			${ui.attnAll && list.length > MAX ? `<div class="la-scroll" tabindex="0" aria-label="${esc(t("Todo lo que requiere atención ({n})", { n: list.length }))}"><ol class="la-list">${rows}</ol></div>` : `<ol class="la-list">${rows}</ol>`}${none}${more}</section>`;
	}

	/**
	 * El resumen de cierre: cuando la ejecución terminó, lo primero es cómo quedó. Duración, resultados,
	 * fallos nuevos (pasaron en su ejecución anterior), lentos, y los siguientes pasos: escalar o el reporte.
	 */
	function summaryHTML(e, tests, data, now, h) {
		if (e.running || !tests.length) return "";
		const { t, esc, icon } = h;
		const c = e.counts || {};
		const failed = tests.filter((x) => isFailed(x.state));
		const nNew = failed.filter((x) => regressionOf(data.regressions, x.id) === "new").length;
		const slow = tests.filter((x) => (x.factor || 0) >= 1.5).length;
		const run = (failed[0] || tests[0]).run_id;
		const ok = !failed.length;
		const end = e.ended_at || Math.max(...tests.map((x) => x.ended_at || x.started_at));
		const stat = (n, label, cls, tip) => `<span class="ls-stat ${cls}" data-tip="${esc(tip)}"><b>${n}</b><small>${esc(label)}</small></span>`;
		return `<section class="card live-summary ${ok ? "ok" : "bad"}" aria-label="${esc(t("Resumen de la ejecución"))}">
			<div class="ls-title">${icon(ok ? "i-st-pass" : "i-st-fail")}<div><h6>${esc(t("Ejecución terminada"))}</h6>
				<p>${esc(ok ? t("Todo pasó.") : nNew ? t("{n} fallos nuevos frente a la ejecución anterior.", { n: nNew }) : t("{n} fallos; ninguno nuevo frente a la ejecución anterior.", { n: failed.length }))}</p></div></div>
			<div class="ls-stats">
				${stat(fmtDur(end - e.started_at), t("duración"), "", t("Del comienzo al último test."))}
				${stat(c.PASS || 0, t("pasaron"), "pass", t("Tests que terminaron bien."))}
				${stat(failed.length, t("fallaron"), failed.length ? "fail" : "", t("En fallo o interrumpidos."))}
				${stat(nNew, t("nuevos"), nNew ? "fail" : "", t("Fallos que en su ejecución anterior pasaron: lo primero que conviene mirar."))}
				${stat(slow, t("lentos"), slow ? "warn" : "", t("Tardaron 1,5 veces lo normal o más."))}</div>
			<div class="ls-acts">${failed.length ? `<a class="cf-btn cf-btn-sm cf-btn-primary" href="#run=${run}&view=escalate" data-tip="${esc(t("Arma el reporte del fallo para el equipo de desarrollo, con evidencia y causa probable."))}">${icon("i-megaphone")}${esc(t("Escalar los fallos"))}</a>` : ""}
				<a class="cf-btn cf-btn-sm" href="#run=${run}&view=dashboard" data-tip="${esc(t("Abre el reporte de la ejecución: tests, pasos, capturas y red."))}">${icon("i-chart")}${esc(t("Ver el reporte"))}</a></div></section>`;
	}

	/** Los atajos de teclado de la vista (tecla ?). */
	function helpHTML(h) {
		const { t, esc, icon } = h;
		const keys = [["/", t("Buscar en la tabla")], ["Esc", t("Limpiar la búsqueda, cerrar el detalle o esta ayuda")], ["J / K", t("Siguiente / anterior que requiere atención (abre su detalle)")],
			["O", t("Abrir el test del detalle")], ["P", t("Pausar o reanudar la vista")], ["C", t("Modo compacto")], ["F", t("Campo a pantalla completa (con el foco en el campo)")],
			["← → / Enter", t("Recorrer los tests del campo / abrir su detalle")], ["?", t("Mostrar u ocultar esta ayuda")]];
		return `<div class="live-help" role="dialog" aria-modal="false" aria-label="${esc(t("Atajos de teclado"))}" data-live-help>
			<header><h6>${esc(t("Atajos de teclado"))}</h6><button type="button" class="cf-btn cf-btn-sm" data-live-helpclose aria-label="${esc(t("Cerrar"))}" data-tip="${esc(t("Cerrar"))}">${icon("i-close")}</button></header>
			<dl>${keys.map(([k, d]) => `<dt><kbd>${esc(k)}</kbd></dt><dd>${esc(d)}</dd>`).join("")}</dl></div>`;
	}

	/** El estado de un paso: los de test (Pass, Fallo…) o informativo. */
	const stepState = (s) => STATES[s] || { cls: "info", icon: "i-info" };
	const pctOf = (x) => `${numFmt(x > 0 && x < 0.1 ? 1 : 0).format(x * 100)}%`;

	/** La franja de salud bajo los tiles: tasa de fallo (frente a la anterior), p50/p95 y ritmo. */
	function healthHTML(e, list, now, h) {
		const { t, esc } = h;
		const s = health(e, list, now);
		if (!s.done) return "";
		let delta = "";
		if (s.prev) {
			const pts = s.prev.delta * 100;
			const cls = Math.abs(pts) < 0.05 ? "eq" : pts > 0 ? "worse" : "better";
			const txt = cls === "eq" ? t("igual que la anterior") : t("{d} pts frente a la anterior", { d: `${pts > 0 ? "▲ +" : "▼ "}${num1(pts)}` });
			delta = `<em class="lh-delta ${cls}" data-tip="${esc(t("Ejecución anterior: {e} · {r} de fallo", { e: s.prev.title, r: pctOf(s.prev.failRate) }))}">${esc(txt)}</em>`;
		}
		// cada número con su explicación en el tooltip y su sparkline (la tendencia de la ejecución)
		const item = (label, value, extra = "", cls = "", tip = "", spark = "") => `<span class="lh ${cls}" tabindex="0" data-tip-title="${esc(label)}" data-tip="${esc(tip)}"><small>${esc(label)}</small><span class="lh-v"><b data-bump="${esc(label)}">${value}</b>${spark}</span>${extra}</span>`;
		const rate = failRateOverTime(e, now).points.map((p) => p.rate);
		const pace = resultsOverTime(e, now).cols.map((c) => c.total);
		return `<div class="live-health" aria-label="${esc(t("Salud de la ejecución"))}">
			${item(t("Tasa de fallo"), esc(pctOf(s.failRate)), `<em>${esc(t("{f} de {n} terminados", { f: s.failed, n: s.done }))}</em>${delta}`, s.failed ? "bad" : "",
				t("Qué parte de los tests terminados falló o se interrumpió. La flecha la compara con la ejecución anterior del mismo proyecto."), sparkline(rate, "bad"))}
			${item(t("Duración típica · lenta"), `${esc(fmtDur(s.p50))} · ${esc(fmtDur(s.p95))}`, `<em>${esc(t("mediana (p50) · 95 % terminó antes (p95)"))}</em>`, "",
				t("Típica: la mitad de los tests duró menos que esto (mediana, p50). Lenta: el 95 % duró menos que esto (p95); lo que pasa de ahí es la cola lenta."))}
			${item(t("Ritmo"), esc(t("{n} tests/min", { n: num1(s.perMin) })), `<em>${esc(e.running ? t("últimos 5 min") : t("promedio"))}</em>`, "",
				t("Cuántos tests terminan por minuto. Si cae mientras la ejecución sigue, algo se está trabando."), sparkline(pace, "pace"))}</div>`;
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
		const errOpen = ui.errOpen || {}, errAll = ui.errAll || {}, regs = data.regressions;
		// los tests de una firma: los fallos nuevos primero (lo que hay que mirar hoy), después lo más reciente
		const errTests = (sig) => tests.filter((x) => isFailed(x.state) && errorSignature(x.last_step) === sig)
			.sort((p, q) => (regressionOf(regs, q.id) === "new") - (regressionOf(regs, p.id) === "new") || (q.ended_at || q.started_at) - (p.ended_at || p.started_at));
		// cada firma con cuántos de sus fallos son nuevos y cuántos ya fallaban: se ve aunque esté cerrada
		const groups = errs.map((g) => { const ts = errTests(g.sig), k = (v) => ts.filter((x) => regressionOf(regs, x.id) === v).length; return { ...g, ts, nNew: k("new"), nOld: k("old"), nFirst: k("first") }; })
			.sort((p, q) => (q.nNew > 0) - (p.nNew > 0) || q.count - p.count || q.last - p.last);
		const WHO = 10;
		const legend = `<p class="lc-legend"><span class="lt-reg new">${esc(t("Nuevo"))}</span> ${esc(t("pasó la vez anterior"))} · <span class="lt-reg old">${esc(t("Ya fallaba"))}</span> ${esc(t("también falló antes"))} · <span class="lt-reg first">${esc(t("Primera vez"))}</span> ${esc(t("sin ejecución anterior"))}</p>`;
		const errBody = groups.length ? `${legend}<ol class="lc-list">${groups.slice(0, 5).map((g) => {
			const open = !!errOpen[g.sig], all = !!errAll[g.sig], list = all ? g.ts : g.ts.slice(0, WHO);
			const who = open ? `<ol class="lc-who">${list.map((x) => {
				const kind = regressionOf(regs, x.id);
				return `<li class="${kind}"><button type="button" class="lc-whorow" data-live-test="${x.id}" data-live-run="${x.run_id}" data-tip="${esc(t("Clic: el detalle al costado. Doble clic: abrir el test."))}">
					<span class="lc-wn" data-tip="${esc(x.name)}">${midName(x.name, esc)}</span>
					<small class="lc-wf">${kind ? regBadge(kind, h) : regPending(h)}<span class="lc-time">${esc(h.clock(x.ended_at || x.started_at, true))}</span> · <span data-no-i18n>${esc(x.file)}${x.worker ? ` · ${esc(x.worker)}` : ""}</span></small></button></li>`;
			}).join("")}</ol>${g.ts.length > WHO ? `<button type="button" class="lf-more lc-whomore" data-live-errall="${esc(g.sig)}" data-tip="${esc(all ? t("Muestra solo los primeros.") : t("Muestra todos los tests con este error."))}">${esc(all ? t("ver menos") : t("ver los {n}", { n: g.ts.length }))}</button>` : ""}` : "";
			const tally = `${g.nNew ? `<span class="lc-tally new" data-tip="${esc(t("Fallos nuevos con este error: en su ejecución anterior pasaron."))}">${esc(g.nNew === 1 ? t("{n} nuevo", { n: 1 }) : t("{n} nuevos", { n: g.nNew }))}</span>` : ""}${g.nOld ? `<span class="lc-tally old" data-tip="${esc(t("Con este error y ya fallaban en su ejecución anterior."))}">${esc(g.nOld === 1 ? t("{n} ya fallaba", { n: 1 }) : t("{n} ya fallaban", { n: g.nOld }))}</span>` : ""}${g.nFirst ? `<span class="lc-tally first" data-tip="${esc(t("Con este error y sin una ejecución anterior con qué comparar."))}">${esc(t("{n} primera vez", { n: g.nFirst }))}</span>` : ""}`;
			return `<li class="${open ? "open" : ""} ${g.nNew ? "has-new" : ""}"><div class="lc-errrow ${ui.errors.includes(g.sig) ? "on" : ""}">
				<button type="button" class="lc-row" data-live-errgroup="${esc(g.sig)}" aria-expanded="${open}" data-tip-title="${esc(g.sig)}" data-tip="${esc(open ? t("Clic: ocultar los tests con este error.") : t("Clic: ver a qué tests pertenece este error."))}">
				<span class="live-chev" aria-hidden="true">${icon("i-chevron")}</span><b class="lc-count fail">${g.count}</b><span class="lc-main"><code data-no-i18n>${esc(shortXPaths(g.sig))}</code>
				<small>${esc(g.files.length === 1 ? g.files[0] : t("{n} archivos", { n: g.files.length }))} · ${esc(t("último {h}", { h: h.clock(g.last, true) }))}</small>${tally ? `<span class="lc-tallies">${tally}</span>` : ""}</span></button>
				<button type="button" class="lc-filter ${ui.errors.includes(g.sig) ? "on" : ""}" data-live-facet="error" data-v="${esc(g.sig)}" aria-pressed="${ui.errors.includes(g.sig)}" aria-label="${esc(t("Filtrar la tabla por este error"))}" data-tip="${esc(t("Filtrar la tabla, el mapa y la línea de tiempo por este error (otro clic lo quita)."))}">${icon("i-search")}</button></div>${who}</li>`;
		}).join("")}</ol>
				${groups.length > 5 ? `<p class="m-hint">${esc(t("y {n} errores más", { n: groups.length - 5 }))}</p>` : ""}`
			: empty(t("Sin fallos por ahora."));

		const slow = slowest(tests);
		const retried = tests.filter((x) => (x.attempts || 0) > 1);
		const slowBody = (slow.length ? `<ol class="lc-list">${slow.map((x) => `<li><button type="button" class="lc-row" data-live-test="${x.id}" data-live-run="${x.run_id}" data-tip="${esc(t("Clic: el detalle al costado (traza, últimos pasos y captura)."))}">
				<span class="lt-vs ${x.factor >= 2 ? "hi" : ""}">×${num1(x.factor)}</span><span class="lc-main"><span data-no-i18n>${esc(x.name)}</span>
				<small>${esc(fmtDur(x.elapsed))} · ${esc(t("mediana {m}", { m: fmtDur(x.p50) }))}${x.ended_at ? "" : ` · ${esc(t("sigue corriendo"))}`}</small></span></button></li>`).join("")}</ol>`
			: empty(t("Ningún test más lento que su historial."))) + (retried.length
			? `<p class="lc-retry">${icon("i-warning")}${esc(t("{n} con reintentos: {l}", { n: retried.length, l: retried.slice(0, 3).map((x) => x.name).join(", ") + (retried.length > 3 ? "…" : "") }))}</p>` : "");

		const eps = data.endpoints;
		const testRun = new Map(tests.map((x) => [x.id, x.run_id]));
		const epBody = !eps ? empty(t("Cargando…")) : eps.length ? `<table class="lc-ep"><colgroup><col class="c-ep"><col class="c-n"><col class="c-err"><col class="c-n"></colgroup><thead><tr><th>${esc(t("Endpoint"))}</th><th class="lt-num">${esc(t("Llamadas"))}</th><th class="lt-num">${esc(t("Errores"))}</th><th class="lt-num">p95</th></tr></thead><tbody>
				${eps.slice(0, 6).map((x) => `<tr class="${x.errors ? "bad" : ""}"><td><a href="#run=${testRun.get(x.test_id) || ""}&view=tests&test=${x.test_id}&tab=network" data-tip="${esc(`${x.method} ${x.host}${x.path}`)}" data-no-i18n><b>${esc(x.method)}</b> ${esc(x.path)}</a></td>
				<td class="lt-num">${x.count}</td><td class="lt-num">${x.errors ? `<span class="lc-err">${x.errors} · ${esc(pctOf(x.errors / x.count))}</span>` : "0"}</td><td class="lt-num">${esc(fmtMs(x.p95_ms))}</td></tr>`).join("")}</tbody></table>`
			: empty(t("Sin llamadas al backend registradas (el cliente captura la red con Playwright)."));


		return `<div class="live-insights">
			${card("lc-errors", t("Errores agrupados"), errs.length ? t("{n} firmas", { n: errs.length }) : "", errBody)}
			${card("lc-slow", t("Más lentos que su historial"), "", slowBody)}
			${card("lc-backend", t("Backend de la ejecución"), eps?.length ? t("{n} endpoints", { n: eps.length }) : "", epBody)}
</div>`; // la actividad reciente es la cinta EN VIVO de arriba: aquí repetía lo mismo
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
				return `<button type="button" class="lf-item ${on ? "on" : ""}" role="checkbox" aria-checked="${on}" data-live-facet="${k}" data-v="${esc(v)}" data-tip="${esc(v)}">
					<span class="lf-box" aria-hidden="true">${on ? icon("i-pass") : ""}</span><span class="lf-name" ${k === "state" ? "" : "data-no-i18n"}>${esc(label(v))}</span><span class="lf-n">${n}</span></button>`;
			}).join("")}${values.length > LIMIT ? `<button type="button" class="lf-more" data-live-more="${k}" data-tip="${esc(more ? t("Muestra solo los más frecuentes.") : t("Muestra todos los valores de esta faceta."))}">${esc(more ? t("ver menos") : t("ver {n} más", { n: values.length - LIMIT }))}</button>` : ""}</fieldset>`;
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
	/** La mini línea de tiempo del panel: el carril del test entero, con el test marcado. */
	function laneStripHTML(g, id, h) {
		const { t, esc } = h;
		const lane = g?.lanes.find((l) => l.ids.includes(id));
		if (!lane) return "";
		const name = lane.proc ? t("Proceso {n}", { n: lane.proc }) : lane.worker;
		const me = lane.bars.find((b) => b.id === id);
		const i = lane.bars.indexOf(me), prev = lane.bars[i - 1], next = lane.bars[i + 1];
		return `<div class="lp-strip"><small>${esc(t("Su carril: {l}", { l: name }))}${lane.bars.length > 1 ? ` · ${esc(t("{n} tests", { n: lane.bars.length }))}` : ""}</small>
			<div class="lp-track">${lane.bars.map((b) => `<button type="button" class="lp-bar ${stateOf(b.state).cls}${b.id === id ? " me" : ""}" data-live-test="${b.id}" data-live-run="${b.run_id}" style="left:${b.left.toFixed(3)}%;width:${b.width.toFixed(3)}%" data-tip-title="${esc(b.name)}" data-tip="${esc(`${t(stateOf(b.state).label)} · ${fmtDur(b.elapsed)}`)}" aria-label="${esc(b.name)}"></button>`).join("")}${g.nowLeft !== null ? `<span class="lg-now" style="left:${g.nowLeft.toFixed(3)}%"></span>` : ""}</div>
			<small class="lp-nb">${prev ? `${esc(t("antes:"))} <span data-no-i18n>${esc(prev.name)}</span>` : ""}${prev && next ? " · " : ""}${next ? `${esc(t("después:"))} <span data-no-i18n>${esc(next.name)}</span>` : ""}</small></div>`;
	}

	function panelHTML(panel, tests, h, now, g) {
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
			[t("Dura."), esc(fmtDur(live.elapsed)) + (live.p50 ? ` · ×${num1(live.factor)} p50 (${esc(fmtDur(live.p50))})` : "")],
			live.idle && ["running", "turtle", "no_signal"].includes(live.state) ? [t("Última señal"), esc(t("hace {t}", { t: fmtDur(live.idle) }))] : null,
			(live.attempts || 0) > 1 ? [t("Intentos"), String(live.attempts)] : null,
		].filter(Boolean) : [];
		let body;
		if (panel.error) body = `<p class="m-hint">${esc(panel.error)}</p>`;
		// sin el detalle todavía: un esqueleto del mismo alto que la traza (no un texto que achica el panel)
		else if (!full) body = `<div class="lp-skel" role="status" aria-label="${esc(t("Cargando…"))}">${"<i></i>".repeat(6)}</div>`;
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
					return `<div class="ltr ${r.kind} ${cls}" role="listitem" data-tip="${esc(tip)}" style="--i:${Math.min(i, 30)}"><span class="ltr-label" data-no-i18n>${r.kind === "req" ? `<b>${esc(r.code)}</b> ` : ""}${esc(r.label)}</span>
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
			${laneStripHTML(g, panel.id, h)}
			<div class="live-ptabs" role="tablist">${tabBtn("trace", "Traza")}${tabBtn("steps", "Últimos pasos")}${tabBtn("shot", "Captura")}</div>
			<div class="live-pbody${panel.stale ? " is-stale" : ""}" role="tabpanel"${panel.stale ? ' aria-busy="true"' : ""}>${body}</div>
			<a class="cf-btn cf-btn-primary" href="#run=${live?.run_id || full?.run_id}&view=tests&test=${panel.id}">${esc(t("Abrir el test"))}</a></aside>`;
	}

	window.TraceReportsLive = { num1, anomalyMarks, fmtMs, regressionOf, summaryHTML, laneStripHTML, STATES, PROBLEM, AUTO_PROBLEMS, GROUPS, INGEST, fieldHTML, fmtDur, timeScale, scaleTicks, splitName, sparkline, heatCells, sortRuns, allTests, shortNames, stepText, shortXPaths, stepParts, statesFromHash, statesToHash, attention, span, lanesOf, ganttLayout, ticks, minimap,
		zoomStep, problemsOn, filterTests, groupTests, progress, mix, percentile, health, errorSignature, errorGroups, slowest, events, anomalies,
		facets, sortTests, endpointOf, traceLayout, timeBuckets, resultsOverTime, failRateOverTime, durationHistogram, ago, freshness, render };
})();
