/*
 * En vivo, vista "Agentes": la ejecución como un reactor, visto en perspectiva (un disco inclinado que se puede
 * girar). Al centro, el núcleo: brilla con la ejecución y su anillo dice cuánto pasó y cuánto falló. Alrededor, un
 * segmento por worker (o proceso, si el cliente no lo informa); adentro de cada segmento, sus tests como celdas,
 * del borde interior (los primeros) al exterior (los últimos). Cada test que termina suelta una partícula que cae
 * al núcleo; un fallo deja su celda marcada y una onda.
 *
 * Escala: cada segmento muestra lo que le cabe (los últimos tests) y cuenta los anteriores junto a su nombre; los
 * fallos no se esconden nunca. Con zoom, las celdas se ven grandes para apuntar a una (como en el Campo).
 *
 * Las anotaciones salen de su celda con una línea guía y se escriben solas: el worker sin señal, el más lento y el
 * último fallo; un clic abre su detalle.
 *
 * Controles como el Campo: pausa, acercar y alejar (botones, + / −, Ctrl + rueda hacia donde apunta el mouse),
 * arrastrar (sin zoom gira el reactor; con zoom, o con Mayús, mueve la vista), 0 para restablecer, pantalla
 * completa (F, Esc para volver). Con un filtro, lo que no coincide queda apagado y no responde al mouse, al clic ni
 * al teclado. Todo lo que se dibuja es dato; con "reducir movimiento", el mismo reactor, quieto.
 *
 * Mismo contrato que livefield.js: create(opts) → {attach(el), update(data), setMotion(on)}.
 * opts: {t, onPick(testId), onOpen(testId, runId), onWorker(key), onHover(testId), onPause()}.
 */
(function () {
	"use strict";

	const TAU = Math.PI * 2;
	const FPS = 30;
	const INTRO_MS = 1400; // al abrir la vista, el reactor se enciende: núcleo, segmentos, celdas
	const ROWS = 10; // a lo sumo, filas de celdas en cada segmento (del borde interior al exterior)
	const COIL = 9, COIL_H = 7; // el ancho y el alto de la bobina, el borde interior de cada segmento
	const NOTES = 4; // anotaciones a la vez, como mucho
	const ZOOM_MIN = 1, ZOOM_MAX = 6;
	const CAM = { tilt: 0.72, rot: 0 }; // la cámara de siempre: el disco inclinado ~41°, sin giro
	const TILT_MAX = 1.15;
	const DUST = 150; // el polvo que gira entre el núcleo y los segmentos
	const SKETCH_MS = 3600; // el boceto: los agentes dibujan el modelo antes de que se encienda
	// cómo trabaja cada agente del boceto (por turno): rápido (con temblor y estela), el que se queda pensando (reloj
	// de arena), el de CAD (cruz, coordenadas, va de vértice en vértice y acota) y el que corrige (borra y rehace)
	const PERSONAS = ["fast", "think", "cad", "fix"];
	const SKETCH_CURSORS = 8; // a lo sumo, agentes dibujando a la vez (cada uno, sus segmentos)
	const CLS = { PASS: "pass", FAIL: "fail", INTERRUPTED: "fail", no_signal: "nosig", WARNING: "warning", turtle: "turtle", running: "running", SKIP: "skip" };
	const VAR = { pass: "--color-success", fail: "--color-danger", nosig: "--color-danger", warning: "--color-warning", turtle: "--color-warning",
		running: "--color-info", skip: "--color-text-muted", idle: "--color-text-muted" };
	const isFail = (s) => s === "FAIL" || s === "INTERRUPTED";
	const fmtMin = (ms) => (ms >= 3600e3 ? `${Math.floor(ms / 3600e3)} h ${Math.round((ms % 3600e3) / 60e3)} min` : ms >= 60e3 ? `${Math.round(ms / 60e3)} min` : `${Math.round(ms / 1000)} s`);
	const short = (s, n) => (s.length > n ? s.slice(0, n - 1) + "…" : s);
	const clamp01 = (x) => Math.max(0, Math.min(1, x));
	const easeOut = (x) => 1 - Math.pow(1 - clamp01(x), 3);

	/** Lo que se dibuja: por worker, sus tests en orden, su estado (el más grave de lo abierto) y su test en curso. */
	function agentsModel(exec, now, LIVE) {
		const lanes = LIVE.lanesOf(exec, now);
		const tests = LIVE.allTests(exec);
		const agents = lanes.map((l) => {
			const ts = [...l.tests].sort((a, b) => a.started_at - b.started_at || a.id - b.id);
			const open = ts.filter((x) => !x.ended_at);
			// con más de un test abierto en el mismo worker, manda el más grave: sin señal, después tortuga
			const current = open.find((x) => x.state === "no_signal") || open.find((x) => x.state === "turtle") || open[open.length - 1] || null;
			const state = !current ? "idle" : current.state === "no_signal" || current.state === "turtle" ? current.state : "running";
			const done = ts.filter((x) => x.ended_at);
			return { key: l.key, label: l.label, tests: ts, current, state, done: done.length, fails: done.filter((x) => isFail(x.state)).length };
		});
		const tally = { pass: 0, fail: 0, other: 0 };
		for (const x of tests) if (x.ended_at) tally[x.state === "PASS" ? "pass" : isFail(x.state) ? "fail" : "other"]++;
		const fails = tests.filter((x) => isFail(x.state)).sort((a, b) => (b.ended_at || b.started_at) - (a.ended_at || a.started_at));
		return { agents, tally, total: Math.max(exec.planned || 0, tests.length), fails };
	}

	/**
	 * La perspectiva: un punto del disco (u, v), a la altura z sobre él, visto con la cámara (inclinación y giro);
	 * s, cuánto se agranda por cercanía.
	 */
	function projector(cam, cx, cy, R) {
		const ct = Math.cos(cam.tilt), st = Math.sin(cam.tilt), cr = Math.cos(cam.rot), sr = Math.sin(cam.rot), F = R * 4;
		return (u, v, z = 0) => {
			const x1 = u * cr - v * sr, y1 = u * sr + v * cr, s = F / (F - (y1 * st + z * ct));
			return { x: cx + x1 * s, y: cy + (y1 * ct - z * st) * s, s };
		};
	}
	const angOf = (i, n) => -Math.PI / 2 + (i / Math.max(1, n)) * TAU; // desde las 12, en sentido horario

	/**
	 * La caja del nombre de un worker, del lado en que cae; con two, también la segunda línea (su test en curso: con
	 * pocos workers, si cabe sin tapar nada).
	 */
	function labelBox(a, lp, side, two) {
		const tw = Math.max((a.label.length + 5) * 7.4, two ? 26 * 6.2 : 0) + 4;
		const x0 = Math.abs(side) < 30 ? lp.x - tw / 2 : side > 0 ? lp.x : lp.x - tw;
		return [x0, lp.y - 9, x0 + tw, lp.y + (two ? 21 : 8)];
	}
	const freeIn = (w, h, avoid) => ([x0, y0, x1, y1]) => x0 >= 0 && x1 <= w && y0 >= 0 && y1 <= h && avoid.every(([c0, d0, c1, d1]) => x0 >= c1 || x1 <= c0 || y0 >= d1 || y1 <= d0);

	/**
	 * El radio del reactor que cabe en el lugar: el disco y los nombres (sin la segunda línea, si hace falta)
	 * dentro del lienzo y fuera de lo que está encima (avoid: las tarjetas de las esquinas, la barra de controles, como [x0, y0, x1, y1]). Se calcula con la
	 * cámara de siempre, para que no cambie de tamaño al girarlo.
	 */
	function fitRadius(model, w, h, avoid = []) {
		const n = model.agents.length, cx = w / 2, cy = h / 2;
		const free = freeIn(w, h, avoid);
		const ok = (R) => {
			const P = projector(CAM, cx, cy, R);
			for (let j = 0; j < 36; j++) {
				const g = (j / 36) * TAU, q = P(Math.cos(g) * R, Math.sin(g) * R);
				if (!free([q.x - 2, q.y - 2, q.x + 2, q.y + 2])) return false;
			}
			return model.agents.every((a, i) => {
				const m = angOf(i + 0.5, n), q = P(Math.cos(m) * (R + 16), Math.sin(m) * (R + 16));
				return free(labelBox(a, q, q.x - cx, false)); // el nombre, al menos (la segunda línea, si cabe)
			});
		};
		let R = Math.min(w * 0.42, h * 0.7);
		while (R > 110 && !ok(R)) R -= 4;
		return Math.max(110, R);
	}

	/** Lo que cabe en un segmento: los últimos tests, y los fallos siempre (hasta la mitad del lugar). */
	function windowOf(tests, cap) {
		if (tests.length <= cap) return tests;
		const fails = tests.filter((x) => isFail(x.state)).slice(-Math.floor(cap / 2));
		const rest = tests.filter((x) => !isFail(x.state));
		const keep = new Set([...fails, ...rest.slice(-(cap - fails.length))]);
		return tests.filter((x) => keep.has(x));
	}

	/**
	 * Dónde va cada cosa (en coordenadas del lienzo, sin zoom): el núcleo al centro, un segmento por worker y sus
	 * tests en celdas, fila por fila desde adentro (en zigzag, para que el orden se siga), proyectados con la cámara.
	 */
	function reactor(model, w, h, R, cam = CAM, avoid = []) {
		const n = model.agents.length, cx = w / 2, cy = h / 2, P = projector(cam, cx, cy, R), free = freeIn(w, h, avoid);
		const Ra = R * 0.42, Rc = R * 0.26, span = TAU / Math.max(1, n), gap = Math.min(0.07, span * 0.16);
		// el paso de las celdas: con pocos tests, grandes (fáciles de ver y de apuntar); se achican a medida que el
		// worker con más tests llena su segmento, hasta ROWS filas (de ahí en más, se ven los últimos)
		const band = R - Ra - COIL, arc = ((Ra + R) / 2) * (span - gap), most = Math.max(1, ...model.agents.map((a) => a.tests.length));
		const pMin = band / ROWS, fits = (q) => Math.max(2, Math.floor(arc / q)) * Math.floor(band / q) >= most;
		let p = Math.max(pMin, Math.min(band / 3, 40, arc / 2)); // nunca más de lo que cabe a lo ancho
		while (p > pMin && !fits(p)) p *= 0.95;
		p = Math.max(pMin, p);
		const rows = Math.floor(band / p + 1e-9);
		const at = new Map();
		const segs = model.agents.map((a, i) => {
			const a0 = angOf(i, n) + gap / 2, a1 = angOf(i + 1, n) - gap / 2, mid = (a0 + a1) / 2;
			const cols = Math.max(2, Math.floor(arc / p));
			const cap = cols * rows, shown = windowOf(a.tests, cap);
			const cells = shown.map((t, k) => {
				const row = Math.floor(k / cols), c0 = k % cols, col = row % 2 ? cols - 1 - c0 : c0;
				const r = Ra + COIL + (row + 0.5) * p, g = a0 + ((col + 0.5) / cols) * (a1 - a0), u = Math.cos(g) * r, v = Math.sin(g) * r, q = P(u, v);
				return { t, k, u, v, x: q.x, y: q.y, r: p * (isFail(t.state) ? 0.4 : 0.3) * q.s };
			});
			for (const c of cells) at.set(c.t.id, c);
			// la bobina: el borde interior del segmento, del color del estado del worker; con grosor (su tapa, a
			// COIL_H sobre el disco, y la pared de afuera)
			const coil = [], wall = [], rc = Ra + COIL * 0.75;
			for (let j = 0; j <= 12; j++) { const g = a0 + ((a1 - a0) * j) / 12; coil.push(P(Math.cos(g) * Ra, Math.sin(g) * Ra, COIL_H)); wall.push(P(Math.cos(g) * rc, Math.sin(g) * rc, COIL_H)); }
			for (let j = 12; j >= 0; j--) { const g = a0 + ((a1 - a0) * j) / 12; coil.push(P(Math.cos(g) * rc, Math.sin(g) * rc, COIL_H)); wall.push(P(Math.cos(g) * rc, Math.sin(g) * rc, 0)); }
			const poly = [];
			for (let j = 0; j <= 12; j++) { const g = a0 + ((a1 - a0) * j) / 12; poly.push(P(Math.cos(g) * Ra, Math.sin(g) * Ra)); }
			for (let j = 12; j >= 0; j--) { const g = a0 + ((a1 - a0) * j) / 12; poly.push(P(Math.cos(g) * R, Math.sin(g) * R)); }
			const lp = P(Math.cos(mid) * (R + 16), Math.sin(mid) * (R + 16)); // dónde va su nombre
			const two = n <= 12 && free(labelBox(a, lp, lp.x - cx, true)); // y debajo, lo que corre (si cabe)
			return { ...a, i, a0, a1, mid, cols, cap, cells, coil, wall, poly, lp, two, side: lp.x - cx, depth: P(Math.cos(mid) * R, Math.sin(mid) * R).s, hidden: a.tests.length - shown.length };
		});
		return { cx, cy, R, Ra, Rc, p, rows, cam, P, segs, at };
	}

	/** Hasta dónde llega un trazo (una lista de puntos) cuando va por la fracción q de su largo; y su punta. */
	function partial(pts, q) {
		if (!pts.length) return { pts: [], tip: null };
		const len = [0];
		for (let i = 1; i < pts.length; i++) len.push(len[i - 1] + Math.hypot(pts[i].x - pts[i - 1].x, pts[i].y - pts[i - 1].y));
		const goal = len[len.length - 1] * clamp01(q), out = [pts[0]];
		for (let i = 1; i < pts.length; i++) {
			if (len[i] <= goal) { out.push(pts[i]); continue; }
			const f = (goal - len[i - 1]) / Math.max(1e-9, len[i] - len[i - 1]);
			out.push({ x: pts[i - 1].x + (pts[i].x - pts[i - 1].x) * f, y: pts[i - 1].y + (pts[i].y - pts[i - 1].y) * f });
			break;
		}
		return { pts: out, tip: out[out.length - 1] };
	}

	const hash = (a, b = 0) => { const v = Math.sin(a * 127.1 + b * 311.7) * 43758.5453; return v - Math.floor(v); };

	/**
	 * El reloj de un agente según cómo trabaja: t (0 a 1) de su tiempo de trabajo → p, cuánto de lo suyo lleva
	 * dibujado; y si en ese momento piensa, corrige o se engancha a un vértice.
	 */
	function agentClock(persona, t) {
		t = clamp01(t);
		if (persona === "fast") return { p: easeOut(Math.min(1, t * 1.15)) };
		if (persona === "think") { // dibuja, se detiene a pensar dos veces y sigue
			const stops = [[0.22, 0.4], [0.58, 0.74]], idle = stops.reduce((a, [x, y]) => a + y - x, 0);
			let w = t, thinking = false;
			for (const [x, y] of stops) { if (t >= y) w -= y - x; else if (t > x) { w -= t - x; thinking = true; } }
			return { p: clamp01(w / (1 - idle)), thinking };
		}
		if (persona === "cad") { // de vértice en vértice: un tramo preciso y una pausa enganchado al punto
			const steps = 7, k = Math.min(steps - 1, Math.floor(t * steps)), f = t * steps - k;
			return { p: t >= 1 ? 1 : clamp01((k + easeOut(Math.min(1, f / 0.65))) / steps), snap: t < 1 && f >= 0.65 };
		}
		// el que corrige: avanza, se arrepiente (borra un poco) y vuelve a dibujar
		const p = t < 0.42 ? (t / 0.42) * 0.6 : t < 0.56 ? 0.6 - ((t - 0.42) / 0.14) * 0.16 : 0.44 + ((t - 0.56) / 0.44) * 0.56;
		return { p: clamp01(p), erasing: t >= 0.42 && t < 0.56 };
	}

	/**
	 * El boceto del modelo (e, de 0 a 1), como si varios agentes manejaran cada uno su mouse: entran a destiempo,
	 * cada uno con el nombre de un worker, y dibujan el contorno de sus segmentos a su manera (ver PERSONAS); entre
	 * un segmento y otro levantan el lápiz y viajan en curva; al apoyarlo, un clic. Mientras, las líneas de
	 * construcción y el núcleo se trazan solos. Desde la mitad el reactor real se enciende encima, el boceto se
	 * desvanece y los agentes se van. Devuelve qué dibujar: cuánto de cada segmento (q, y de quién es), el núcleo,
	 * las guías, los agentes (dónde, su nombre, qué hacen, su opacidad) y la opacidad del lápiz.
	 */
	function sketchPlan(lay, e) {
		const n = lay.segs.length, C = Math.max(1, Math.min(SKETCH_CURSORS, n));
		const segs = lay.segs.map((sg, i) => ({ i, q: 0, j: i % C, persona: PERSONAS[(i % C) % PERSONAS.length] }));
		const away = clamp01((e - 0.84) / 0.16); // al final, los agentes se van hacia afuera
		const cursors = [];
		for (let j = 0; j < Math.min(C, n); j++) {
			const persona = PERSONAS[j % PERSONAS.length], mine = segs.filter((x) => x.j === j), m = mine.length;
			const start = 0.01 + hash(j) * 0.07, enter = start + 0.07;
			const end = { fast: 0.42, think: 0.63, cad: 0.6, fix: 0.58 }[persona] - hash(j, 1) * 0.04;
			const clock = agentClock(persona, (e - enter) / (end - enter));
			// lo suyo, en unidades de segmento: cada uno (menos el primero) empieza con un viaje con el lápiz levantado
			const P = clock.p * m, k = Math.min(m - 1, Math.floor(P)), f = P - k, travel = k > 0 && f < 0.2;
			mine.forEach((x, idx) => { x.q = idx < k ? 1 : idx > k ? 0 : travel ? 0 : clamp01(k > 0 ? (f - 0.2) / 0.8 : f); });
			if (clock.p >= 1) mine[m - 1].q = 1;
			const cur = mine[k], sg = lay.segs[cur.i];
			const g = sg.mid, out = { x: lay.cx + Math.cos(g) * lay.R * 2.2, y: lay.cy + Math.sin(g) * lay.R * 1.6 };
			let x, y;
			if (travel) { // en curva, hacia afuera, del final del anterior al comienzo de este
				const a = lay.segs[mine[k - 1].i].poly, from = a[a.length - 1], to = sg.poly[0], u = easeOut(f / 0.2);
				const ctl = { x: lay.cx + ((from.x + to.x) / 2 - lay.cx) * 1.35, y: lay.cy + ((from.y + to.y) / 2 - lay.cy) * 1.35 };
				x = (1 - u) * (1 - u) * from.x + 2 * (1 - u) * u * ctl.x + u * u * to.x;
				y = (1 - u) * (1 - u) * from.y + 2 * (1 - u) * u * ctl.y + u * u * to.y;
			} else ({ x, y } = partial(sg.poly, cur.q).tip || sg.poly[0]);
			if (e < enter) { const u = easeOut(clamp01((e - start) / 0.07)); x = out.x + (sg.poly[0].x - out.x) * u; y = out.y + (sg.poly[0].y - out.y) * u; } // entra
			const done = clock.p >= 1 && e > end;
			if (done) { x += Math.sin(e * 22 + j) * 9; y += Math.cos(e * 17 + j) * 6; } // terminó: da vueltas esperando a los demás
			// el pulso de la mano (el de CAD es preciso)
			const amp = { fast: 1.6, think: 0.6, cad: 0, fix: 1.1 }[persona];
			x += Math.sin(e * (70 + j * 9)) * amp; y += Math.cos(e * (61 + j * 7)) * amp;
			if (away > 0) { const u = away * away; x += (out.x - x) * u; y += (out.y - y) * u; } // se va
			// el clic al apoyar el lápiz: 0 → 1 en el primer tramo de cada segmento
			const down = !travel && e >= enter && !done && cur.q < 0.12 ? cur.q / 0.12 : -1;
			cursors.push({ j, persona, x, y, label: sg.label, seg: cur.i, alpha: e < start ? 0 : 1 - away, done,
				thinking: !!clock.thinking && !done, erasing: !!clock.erasing, snap: !!clock.snap && !travel, travel, down });
		}
		return { segs, cursors, core: clamp01((e - 0.04) / 0.32), guides: clamp01(e / 0.2), pencil: 1 - clamp01((e - 0.72) / 0.28), reactor: clamp01((e - 0.5) / 0.4) };
	}

	/** Lo que el asistente señala, por importancia: los sin señal, el más lento y el último fallo. */
	function notesOf(model, now, t) {
		const out = [];
		for (const a of model.agents.filter((x) => x.state === "no_signal")) {
			const idle = a.current.idle ?? now - (a.current.last_signal || a.current.started_at);
			out.push({ id: a.current.id, kind: "nosig", text: `${a.label} · ${t("SIN SEÑAL")} ${fmtMin(idle)} · ${short(a.current.name, 22)}` });
		}
		const slow = model.agents.filter((x) => x.state === "turtle").sort((a, b) => (b.current.factor || 0) - (a.current.factor || 0))[0];
		if (slow) out.push({ id: slow.current.id, kind: "turtle", text: `${slow.label} · ${t("LENTO")}${slow.current.factor ? ` ×${slow.current.factor.toFixed(1).replace(".", ",")}` : ""} · ${short(slow.current.name, 22)}` });
		const f = model.fails[0];
		if (f) out.push({ id: f.id, kind: "fail", text: `${t("ÚLTIMO FALLO")} · ${short(f.name, 22)}` });
		return out.slice(0, NOTES);
	}

	/**
	 * Qué hay bajo el puntero (x, y en coordenadas del lienzo sin zoom; k, el zoom): una celda (abre el test) o el
	 * nombre de un worker (filtra por él). Con un filtro (match), lo apagado no responde.
	 */
	function hitTest(lay, x, y, match = null, k = 1) {
		if (!lay) return null;
		let best = null, bd = Infinity;
		for (const s of lay.segs) for (const c of s.cells) {
			if (match && !match.has(c.t.id)) continue;
			const d = Math.hypot(c.x - x, c.y - y);
			if (d < Math.max(c.r + 1.5, 6 / k) && d < bd) { bd = d; best = { kind: "test", node: c, arm: s }; }
		}
		if (best) return best;
		for (const s of lay.segs) {
			if (match && s.tests.every((t) => !match.has(t.id))) continue;
			const [x0, y0, x1, y1] = labelBox(s, s.lp, s.side, s.two);
			if (x >= x0 && x <= x1 && y >= y0 && y <= y1) return { kind: "agent", arm: s };
		}
		return null;
	}

	/** rgba() de un color CSS (#rgb, #rrggbb o rgb()) con otra opacidad. */
	const rgbCache = new Map();
	function alpha(css, a) {
		let rgb = rgbCache.get(css);
		if (!rgb) {
			const h = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(css);
			if (h) { const x = h[1].length === 3 ? h[1].replace(/./g, "$&$&") : h[1]; rgb = [0, 2, 4].map((i) => parseInt(x.slice(i, i + 2), 16)); }
			else { const m = /rgba?\(([^)]+)\)/.exec(css); rgb = m ? m[1].split(/[ ,/]+/).slice(0, 3).map(Number) : [80, 140, 220]; }
			rgbCache.set(css, rgb);
		}
		return `rgba(${rgb[0]},${rgb[1]},${rgb[2]},${a})`;
	}

	function create(opts = {}) {
		const t_ = opts.t || ((x) => x);
		const esc = (x) => String(x).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
		const wrap = document.createElement("div");
		wrap.className = "la-wrap lf-wrap"; // lf-wrap: la barra de controles y la pantalla completa del Campo
		const canvas = document.createElement("canvas");
		canvas.className = "la-canvas";
		canvas.tabIndex = 0;
		canvas.setAttribute("role", "img");
		const notesEl = document.createElement("div");
		notesEl.className = "la-notes";
		const tip = document.createElement("div");
		tip.className = "lf-tip"; tip.hidden = true; tip.setAttribute("role", "status");
		const topLayer = typeof tip.showPopover === "function";
		if (topLayer) tip.popover = "manual";
		// la barra de controles, la misma del Campo
		const bar = document.createElement("div");
		bar.className = "lf-ctrl";
		const btn = (k, icon, label, tip2, keys) => `<button type="button" class="lf-cb" data-lf="${k}" aria-label="${esc(label)}" data-tip-title="${esc(label)}" data-tip="${esc(tip2)}"${keys ? ` data-tip-keys="${keys}"` : ""}><svg aria-hidden="true"><use href="#${icon}"/></svg><span>${esc(label)}</span></button>`;
		bar.innerHTML = [
			btn("pause", "i-pause", t_("Pausar vista"), t_("Congela la vista y los datos para analizar algo puntual. La ingesta sigue en el servidor: al reanudar, se pone al día."), t_("Espacio")),
			btn("out", "i-zoom-out", t_("Alejar"), t_("Aleja el reactor."), "−"),
			btn("in", "i-zoom-in", t_("Acercar"), t_("Acerca el reactor (también con Ctrl + rueda, hacia donde apunta el mouse, o con la rueda en pantalla completa). Con zoom, arrastra para moverte."), "+"),
			btn("reset", "i-orbit", t_("Restablecer"), t_("Vuelve a la vista de siempre: sin zoom ni giro."), "0"),
			btn("sketch", "i-spark", t_("Boceto"), t_("Los agentes vuelven a bocetar el modelo, worker por worker, antes de encenderlo. Un clic lo salta."), "B"),
			btn("full", "i-fit", t_("Pantalla completa"), t_("El reactor a pantalla completa, para explorarlo. Esc para volver."), "F"),
		].join("");
		const help = document.createElement("div");
		help.className = "lf-help";
		help.textContent = t_("Arrastra: girar (con zoom, mover) · Ctrl + rueda: zoom · clic: el detalle");
		wrap.append(canvas, notesEl, tip, bar, help);
		const ctx = canvas.getContext("2d");
		const reduce = matchMedia("(prefers-reduced-motion: reduce)");
		const colors = {};
		let model = null, lay = null, R = 0, w = 0, h = 0, dpr = 1, raf = 0, lastFrame = 0, now = 0, nowMs = Date.now();
		let motion = true, paused = false, visible = true, hover = null, focusIdx = -1, match = null, selected = null, labels = { aria: "", card: null };
		let intro = null, sketch = null, lastExec = "", expanded = false, slotEl = null, drag = null, dragged = false, flashT = -1e9;
		let view = { k: 1, x: 0, y: 0 }; // zoom y desplazamiento (pantalla = lienzo · k + x)
		let cam = { ...CAM };
		let seenEnded = null; // los tests ya terminados, para soltar una partícula por cada uno nuevo
		const particles = [], shocks = new Map(); // test id → cuándo falló (su onda)
		const dust = Array.from({ length: DUST }, (_, i) => { const f = ((i * 7919) % 1000) / 1000, g = ((i * 104729) % 1000) / 1000; return { f: 0.29 + 0.12 * Math.pow(f, 1.6), a: g * TAU, sp: 0.18 + 0.22 * (1 - f), z: 0.6 + ((i * 31) % 10) / 10 }; });
		const noteEls = new Map(); // test id → elemento de la anotación (se escribe una sola vez)
		const guides = new Map(); // test id → su línea guía, en pixeles de pantalla
		const live = () => motion && !reduce.matches;
		canvas.layout = () => lay; // para las pruebas: dónde quedó cada cosa, y con qué zoom
		canvas.view = () => ({ ...view });

		const readColors = () => {
			const cs = getComputedStyle(wrap.isConnected ? wrap : document.documentElement);
			for (const v of new Set([...Object.values(VAR), "--color-text", "--color-text-muted", "--color-primary", "--color-surface"])) colors[v] = cs.getPropertyValue(v).trim() || "#888";
		};
		const colorOf = (s) => colors[VAR[CLS[s] || s] || VAR.running] || "#4aa3ff";
		const glow = (a) => alpha(colors["--color-info"] || "#14b8c4", a); // la luz del reactor
		const toScreen = (p) => ({ x: p.x * view.k + view.x, y: p.y * view.k + view.y });
		const toPlan = (x, y) => [(x - view.x) / view.k, (y - view.y) / view.k];
		/** Lo que está encima del reactor (las tarjetas de las esquinas, la barra de controles), en sus coordenadas. */
		const obstacles = () => {
			const r = wrap.getBoundingClientRect(), stage = wrap.closest(".lf-stage");
			return [...(stage && !expanded ? stage.querySelectorAll(".lf-hud") : []), bar].map((el) => el.getBoundingClientRect())
				.filter((b) => b.width && b.height).map((b) => [b.left - r.left - 6, b.top - r.top - 4, b.right - r.left + 6, b.bottom - r.top + 4]);
		};
		let avoid = [];
		const relayout = (fit) => {
			if (!model || !w) { lay = null; return; }
			if (fit || !R) { avoid = obstacles(); R = fitRadius(model, w, h, avoid); }
			lay = reactor(model, w, h, R, cam, avoid);
		};

		function resize() {
			const r = wrap.getBoundingClientRect();
			if (!r.width) return;
			w = r.width; h = r.height; dpr = Math.min(window.devicePixelRatio || 1, 2);
			canvas.width = Math.round(w * dpr); canvas.height = Math.round(h * dpr);
			canvas.style.width = w + "px"; canvas.style.height = h + "px";
			relayout(true);
			clampView();
			placeNotes(true);
			draw();
		}
		const ro = typeof ResizeObserver === "function" ? new ResizeObserver(resize) : null;
		ro?.observe(wrap);
		const io = typeof IntersectionObserver === "function" ? new IntersectionObserver((en) => { visible = en[0].isIntersecting; if (visible) kick(); }) : null;
		io?.observe(wrap);

		// ---------- zoom, desplazamiento y giro ----------
		function clampView() {
			view.k = Math.max(ZOOM_MIN, Math.min(ZOOM_MAX, view.k));
			// el reactor no se va de la vista: a lo sumo, su borde llega al borde
			view.x = Math.min(0, Math.max(w - w * view.k, view.x));
			view.y = Math.min(0, Math.max(h - h * view.k, view.y));
		}
		function zoomAt(f, x = w / 2, y = h / 2) {
			const [px, py] = toPlan(x, y);
			view.k *= f;
			clampView();
			view.x = x - px * view.k; view.y = y - py * view.k;
			clampView();
			canvas.dataset.zoom = view.k.toFixed(2);
			placeNotes(true); draw();
		}
		function setCam(rot, tilt) {
			cam = { rot, tilt: Math.max(0, Math.min(TILT_MAX, tilt)) };
			canvas.dataset.cam = `${cam.rot.toFixed(2)} ${cam.tilt.toFixed(2)}`;
			relayout(false); placeNotes(true); draw();
		}
		function resetView() { view = { k: 1, x: 0, y: 0 }; canvas.dataset.zoom = "1.00"; setCam(CAM.rot, CAM.tilt); }
		function setExpanded(on) {
			expanded = !!on;
			wrap.classList.toggle("lf-full", expanded);
			document.body.classList.toggle("lf-full-open", expanded);
			const b = bar.querySelector('[data-lf="full"]');
			b.querySelector("span").textContent = expanded ? t_("Salir") : t_("Pantalla completa");
			b.setAttribute("aria-label", b.querySelector("span").textContent);
			if (expanded) document.body.appendChild(wrap);
			else if (slotEl && slotEl.isConnected) slotEl.appendChild(wrap);
			else wrap.remove();
			canvas.focus({ preventScroll: true });
			requestAnimationFrame(resize);
		}
		function setPaused(on) {
			paused = !!on;
			const b = bar.querySelector('[data-lf="pause"]');
			b.classList.toggle("on", paused);
			b.setAttribute("aria-pressed", String(paused));
			b.querySelector("use").setAttribute("href", paused ? "#i-play" : "#i-pause");
			const label = paused ? t_("Reanudar") : t_("Pausar vista");
			b.querySelector("span").textContent = label;
			b.setAttribute("aria-label", label);
			wrap.classList.toggle("lf-paused", paused);
		}
		bar.addEventListener("click", (e) => {
			const b = e.target.closest("[data-lf]");
			if (!b) return;
			const k = b.dataset.lf;
			if (k === "pause") opts.onPause?.();
			else if (k === "in") zoomAt(1.25);
			else if (k === "out") zoomAt(1 / 1.25);
			else if (k === "reset") resetView();
			else if (k === "sketch") startSketch();
			else if (k === "full") setExpanded(!expanded);
		});

		/** Cuánto del reactor ya se encendió (al abrir: el núcleo, después los segmentos, después las celdas). */
		const built = () => (intro ? clamp01((now - intro.t0) / INTRO_MS) : 1);
		/** Al abrir la vista (o con "Boceto"): los agentes bocetan el modelo y, desde la mitad, el reactor se enciende. */
		function startSketch() {
			if (!live() || !lay) { sketch = null; intro = null; canvas.dataset.sketch = "off"; placeNotes(true); draw(); return; }
			now = performance.now();
			sketch = { t0: now };
			intro = { t0: now + SKETCH_MS * 0.5 - INTRO_MS * 0.3 };
			canvas.dataset.sketch = "on";
			placeNotes(true);
			kick();
		}
		function skipSketch() {
			if (!sketch) return;
			sketch = null; intro = null; canvas.dataset.sketch = "off";
			placeNotes(true); draw();
		}

		/** Un anillo del disco (proyectado), de a0 a a1. */
		function ring(r, a0 = 0, a1 = TAU) {
			const steps = Math.max(8, Math.ceil(((a1 - a0) / TAU) * 72));
			ctx.beginPath();
			for (let j = 0; j <= steps; j++) {
				const g = a0 + ((a1 - a0) * j) / steps, q = lay.P(Math.cos(g) * r, Math.sin(g) * r);
				j ? ctx.lineTo(q.x, q.y) : ctx.moveTo(q.x, q.y);
			}
		}

		/** El disco de acreción: bandas de luz y el polvo que gira entre el núcleo y los segmentos (más rápido adentro). */
		function drawDisk(p, ts) {
			const { R, Ra, Rc } = lay, r0 = Rc * 1.12, r1 = Ra * 0.95, bands = 7;
			ctx.lineWidth = (r1 - r0) / bands;
			for (let j = 0; j < bands; j++) {
				ctx.strokeStyle = glow(0.16 * Math.pow(1 - j / bands, 1.6) * p);
				ring(r0 + (r1 - r0) * ((j + 0.5) / bands)); ctx.stroke();
			}
			for (const d of dust) {
				const r = d.f * R * (0.4 + 0.6 * p), g = d.a + ts * d.sp, q = lay.P(Math.cos(g) * r, Math.sin(g) * r);
				ctx.fillStyle = glow((0.25 + 0.45 * (1 - (d.f - 0.29) / 0.12)) * p);
				const s = 1.3 * d.z * q.s;
				ctx.fillRect(q.x - s / 2, q.y - s / 2, s, s);
			}
		}

		/** El núcleo: su luz, el anillo de lo terminado (verde lo que pasó, rojo lo que falló, desde las 12) y dos destellos que lo recorren. */
		function drawCore(p, ts, anim) {
			const { cx, cy, Rc } = lay, beat = anim ? 1 + 0.035 * Math.sin(ts * 2.4) : 1;
			const done = model.tally.pass + model.tally.fail, hurt = done ? Math.min(1, (model.tally.fail / done) * 4) : 0;
			const R = Rc * 1.3 * easeOut(p / 0.5) * beat;
			if (R > 0) {
				const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, R);
				g.addColorStop(0, glow(0.32));
				g.addColorStop(0.45, glow(0.2));
				g.addColorStop(0.7, hurt ? alpha(colorOf("fail"), 0.1 * hurt) : glow(0.14));
				g.addColorStop(1, glow(0));
				ctx.fillStyle = g; ctx.beginPath(); ctx.arc(cx, cy, R, 0, TAU); ctx.fill();
			}
			// el halo de frente: la luz del disco que se ve por encima del núcleo
			ctx.strokeStyle = glow(0.3 * p); ctx.lineWidth = 1.2;
			ctx.beginPath(); ctx.arc(cx, cy, Rc * 1.18 * easeOut(p / 0.5), 0, TAU); ctx.stroke();
			// lo terminado
			const rr = Rc * 1.05, total = Math.max(1, model.total), e = easeOut(p / 0.6);
			ctx.lineWidth = 4; ctx.strokeStyle = glow(0.18); ring(rr); ctx.stroke();
			let a0 = -Math.PI / 2;
			for (const [n, cls] of [[model.tally.pass, "pass"], [model.tally.fail, "fail"], [model.tally.other, "warning"]]) {
				if (!n) continue;
				const a1 = a0 + (n / total) * TAU * e;
				ctx.strokeStyle = colorOf(cls); ring(rr, a0, a1); ctx.stroke(); a0 = a1;
			}
			if (anim) {
				ctx.lineWidth = 2;
				for (const [head, dir] of [[ts * 1.3, 1], [-ts * 0.9 + Math.PI, -1]]) for (let j = 0; j < 10; j++) {
					const g = head - dir * j * 0.045;
					ctx.strokeStyle = glow(0.7 * (1 - j / 10)); ring(Rc * 1.2, g, g + 0.05); ctx.stroke();
				}
			}
			// un fallo nuevo: una onda roja desde el núcleo
			const q = (now - flashT) / 900;
			if (q >= 0 && q < 1) { ctx.strokeStyle = alpha(colorOf("fail"), 0.6 * (1 - q)); ctx.lineWidth = 2; ring(Rc * (1.1 + q * 1.4)); ctx.stroke(); }
		}

		function drawSeg(s, segP, cellP, ts, anim) {
			const hot = hover?.arm?.key === s.key || focusIdx === s.i;
			const off = match && s.tests.every((x) => !match.has(x.id)); // nada que coincida: apagado, no responde
			const base = off ? 0.22 : 1;
			// la carcasa del segmento: su color dice el estado del worker
			const col = s.state === "idle" ? colors["--color-text-muted"] : s.state === "running" ? colors["--color-info"] : colorOf(s.state);
			const pulse = s.state === "no_signal" && anim ? 0.5 + 0.5 * Math.sin(ts * 6) : 0.5;
			ctx.globalAlpha = base * segP;
			ctx.beginPath();
			s.poly.forEach((q, j) => (j ? ctx.lineTo(q.x, q.y) : ctx.moveTo(q.x, q.y)));
			ctx.closePath();
			ctx.fillStyle = alpha(col, hot ? 0.09 : 0.035);
			ctx.fill();
			ctx.strokeStyle = alpha(col, hot ? 0.9 : 0.22);
			ctx.lineWidth = (hot ? 2 : 1) / Math.sqrt(view.k);
			ctx.stroke();
			// la bobina: su pared (más oscura) y su tapa, que brilla con el estado del worker (sin señal, parpadea)
			ctx.beginPath();
			s.wall.forEach((q, j) => (j ? ctx.lineTo(q.x, q.y) : ctx.moveTo(q.x, q.y)));
			ctx.closePath();
			ctx.fillStyle = alpha(col, s.state === "idle" ? 0.22 : 0.5);
			ctx.fill();
			ctx.beginPath();
			s.coil.forEach((q, j) => (j ? ctx.lineTo(q.x, q.y) : ctx.moveTo(q.x, q.y)));
			ctx.closePath();
			ctx.fillStyle = alpha(col, s.state === "idle" ? 0.35 : s.state === "no_signal" ? 0.45 + 0.5 * pulse : 0.85);
			ctx.shadowColor = s.state === "idle" ? "transparent" : alpha(col, 0.8); ctx.shadowBlur = 8 * view.k;
			ctx.fill();
			ctx.shadowBlur = 0; ctx.shadowColor = "transparent";
			// las celdas: el resultado de cada test; el que corre, respira
			const show = Math.ceil(s.cells.length * cellP);
			for (const c of s.cells.slice(0, show)) {
				const open = !c.t.ended_at, cc = colorOf(c.t.state), dim = off || (match && !match.has(c.t.id));
				ctx.globalAlpha = dim ? 0.15 : 1;
				ctx.fillStyle = open ? colors["--color-surface"] || "#fff" : cc; ctx.strokeStyle = cc; ctx.lineWidth = 1.4;
				ctx.beginPath(); ctx.arc(c.x, c.y, c.r, 0, TAU); ctx.fill();
				if (!open) {
					// una esfera: el borde en sombra y un brillo arriba a la izquierda
					ctx.strokeStyle = "rgba(0,0,0,0.16)"; ctx.lineWidth = Math.max(0.6, c.r * 0.16); ctx.stroke();
					ctx.fillStyle = "rgba(255,255,255,0.5)";
					ctx.beginPath(); ctx.arc(c.x - c.r * 0.32, c.y - c.r * 0.36, c.r * 0.36, 0, TAU); ctx.fill();
					ctx.strokeStyle = cc;
				}
				if (open) {
					ctx.stroke();
					if (anim && !dim) {
						const q = (now / (c.t.state === "no_signal" ? 900 : 1400)) % 1;
						ctx.globalAlpha = 1 - q;
						ctx.beginPath(); ctx.arc(c.x, c.y, c.r + 2 + q * 8, 0, TAU); ctx.stroke();
					}
				}
				const sh = shocks.get(c.t.id);
				if (sh !== undefined) {
					const q = (now - sh) / 1100;
					if (q >= 1) shocks.delete(c.t.id);
					else if (!dim) { ctx.globalAlpha = 1 - q; ctx.strokeStyle = cc; ctx.lineWidth = 2; ctx.beginPath(); ctx.arc(c.x, c.y, c.r + q * 22, 0, TAU); ctx.stroke(); }
				}
				if (c.t.id === selected || hover?.node?.t.id === c.t.id) {
					ctx.globalAlpha = 1; ctx.strokeStyle = colors["--color-text"] || "#111"; ctx.lineWidth = 2 / Math.sqrt(view.k);
					ctx.beginPath(); ctx.arc(c.x, c.y, c.r + 3, 0, TAU); ctx.stroke();
				}
			}
			// su nombre, afuera (y cuántos anteriores no se ven); con pocos workers o de cerca, lo que corre
			ctx.globalAlpha = base;
			if (segP >= 1) {
				const fs = 1 / Math.sqrt(view.k);
				ctx.textAlign = Math.abs(s.side) < 30 ? "center" : s.side > 0 ? "left" : "right";
				ctx.textBaseline = "middle";
				ctx.font = `700 ${(hot ? 12 : 11) * fs}px ui-monospace, SFMono-Regular, Menlo, monospace`;
				ctx.fillStyle = s.state === "idle" ? colors["--color-text-muted"] || "#888" : s.state === "running" ? colors["--color-text"] || "#111" : colorOf(s.state);
				ctx.fillText(`${s.label}${s.hidden ? ` +${s.hidden}` : ""}`, s.lp.x, s.lp.y);
				if (s.current && (s.two || hot || view.k >= 1.8)) {
					ctx.font = `${10.5 * fs}px ui-sans-serif, system-ui, sans-serif`; ctx.fillStyle = colors["--color-text-muted"] || "#888";
					ctx.fillText(short(s.current.name, 26), s.lp.x, s.lp.y + 13 * fs);
				}
			}
			ctx.globalAlpha = 1;
		}

		/** El boceto: líneas de construcción, el núcleo y los contornos (a lápiz, o precisos y acotados los del agente de CAD), y los agentes. */
		const trails = new Map(); // agente → sus últimas posiciones (la estela del rápido)
		function drawSketch(e) {
			const sp = sketchPlan(lay, e), pen = colors["--color-text-muted"] || "#888";
			const pal = ["--color-info", "--color-primary", "--color-success", "--color-warning", "--color-danger"].map((v) => colors[v] || "#4aa3ff");
			const colOf = (j) => pal[j % pal.length];
			const wob = (i, j, a) => { const v = Math.sin(i * 12.9898 + j * 78.233) * 43758.5453; return (v - Math.floor(v) - 0.5) * a; };
			const stroke = (pts, seed, a = 2.2, passes = 2) => {
				for (let off = 0; off < passes; off++) { // pasadas, como un lápiz
					ctx.beginPath();
					pts.forEach((q, i) => { const x = q.x + wob(i + off * 7, seed, a), y = q.y + wob(seed, i + off * 5, a); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); });
					ctx.stroke();
				}
			};
			ctx.save();
			ctx.globalAlpha = sp.pencil; ctx.strokeStyle = alpha(pen, 0.5); ctx.lineWidth = 0.9; ctx.lineCap = "round"; ctx.lineJoin = "round";
			// líneas de construcción: del centro a cada worker, punteadas
			ctx.setLineDash([3, 5]);
			for (const sg of lay.segs) {
				const a = lay.P(0, 0), b = lay.P(Math.cos(sg.mid) * lay.R * 1.06, Math.sin(sg.mid) * lay.R * 1.06);
				ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(a.x + (b.x - a.x) * sp.guides, a.y + (b.y - a.y) * sp.guides); ctx.stroke();
			}
			ctx.setLineDash([]);
			// el núcleo y el borde del disco
			const circle = (r) => Array.from({ length: 49 }, (_, j) => { const g = -Math.PI / 2 + (j / 48) * TAU; return lay.P(Math.cos(g) * r, Math.sin(g) * r); });
			stroke(partial(circle(lay.Rc * 1.05), sp.core).pts, 3);
			stroke(partial(circle(lay.Ra), sp.core).pts, 5);
			// los contornos, cada uno al estilo de su agente
			for (const x of sp.segs) {
				if (x.q <= 0) continue;
				const sg = lay.segs[x.i], pts = partial(sg.poly, x.q).pts;
				if (x.persona === "cad") {
					// CAD: recto y del color de su capa; los vértices ya pasados, marcados; acotado al terminar
					ctx.strokeStyle = alpha(colOf(x.j), 0.85); ctx.lineWidth = 1;
					stroke(pts, 0, 0, 1);
					ctx.fillStyle = alpha(colOf(x.j), 0.85);
					for (const v of [0, 12, 13, 25]) if (pts.length > v) ctx.fillRect(sg.poly[v].x - 2, sg.poly[v].y - 2, 4, 4);
					if (x.q >= 1) {
						// la cota: un arco afuera del segmento, con sus líneas de extensión y los grados
						const r1 = lay.R + 10, r2 = lay.R + 16, deg = Math.round(((sg.a1 - sg.a0) * 180) / Math.PI);
						ctx.beginPath();
						for (let k = 0; k <= 16; k++) { const g = sg.a0 + ((sg.a1 - sg.a0) * k) / 16, q = lay.P(Math.cos(g) * r1, Math.sin(g) * r1); k ? ctx.lineTo(q.x, q.y) : ctx.moveTo(q.x, q.y); }
						for (const g of [sg.a0, sg.a1]) { const q0 = lay.P(Math.cos(g) * lay.R, Math.sin(g) * lay.R), q1 = lay.P(Math.cos(g) * r2, Math.sin(g) * r2); ctx.moveTo(q0.x, q0.y); ctx.lineTo(q1.x, q1.y); }
						ctx.stroke();
						const t = lay.P(Math.cos(sg.mid) * (r2 + 6), Math.sin(sg.mid) * (r2 + 6));
						ctx.font = "600 9px ui-monospace, SFMono-Regular, Menlo, monospace"; ctx.textAlign = "center"; ctx.textBaseline = "middle";
						ctx.fillText(`${deg}°`, t.x, t.y);
					}
				} else {
					ctx.strokeStyle = alpha(pen, x.persona === "fix" ? 0.9 : 0.75); ctx.lineWidth = x.persona === "fix" ? 1.3 : 1;
					stroke(pts, 11 + x.i, x.persona === "fast" ? 3.2 : 2, x.persona === "fix" ? 3 : 2);
				}
			}
			ctx.restore();
			// los agentes
			for (const c of sp.cursors) {
				if (c.alpha <= 0) { trails.delete(c.j); continue; }
				const col = colOf(c.j);
				ctx.save();
				ctx.globalAlpha = c.alpha;
				// la estela del rápido
				if (c.persona === "fast") {
					const tr = trails.get(c.j) || [];
					tr.push({ x: c.x, y: c.y }); if (tr.length > 7) tr.shift(); trails.set(c.j, tr);
					tr.forEach((q, k) => { ctx.globalAlpha = c.alpha * (k / tr.length) * 0.35; ctx.fillStyle = col; ctx.beginPath(); ctx.arc(q.x, q.y, 1.2 + k * 0.35, 0, TAU); ctx.fill(); });
					ctx.globalAlpha = c.alpha;
				}
				// el clic al apoyar el lápiz
				if (c.down >= 0) { ctx.strokeStyle = col; ctx.lineWidth = 1.4; ctx.globalAlpha = c.alpha * (1 - c.down); ctx.beginPath(); ctx.arc(c.x, c.y, 3 + c.down * 12, 0, TAU); ctx.stroke(); ctx.globalAlpha = c.alpha; }
				ctx.translate(c.x, c.y);
				let tag = c.label;
				if (c.persona === "cad") {
					// la cruz de CAD con su recuadro de selección, las coordenadas y, si se engancha, el punto final
					ctx.strokeStyle = col; ctx.lineWidth = 1;
					ctx.beginPath(); ctx.moveTo(-34, 0); ctx.lineTo(-4, 0); ctx.moveTo(4, 0); ctx.lineTo(34, 0); ctx.moveTo(0, -34); ctx.lineTo(0, -4); ctx.moveTo(0, 4); ctx.lineTo(0, 34); ctx.stroke();
					ctx.strokeRect(-3.5, -3.5, 7, 7);
					if (c.snap) { ctx.strokeStyle = colors["--color-warning"] || "#d9a400"; ctx.lineWidth = 1.6; ctx.strokeRect(-6, -6, 12, 12); }
					ctx.font = "600 9px ui-monospace, SFMono-Regular, Menlo, monospace"; ctx.textAlign = "left"; ctx.textBaseline = "middle";
					const xy = `X ${(c.x - lay.cx).toFixed(1)}  Y ${(lay.cy - c.y).toFixed(1)}${c.snap ? `  ${t_("PUNTO FINAL")}` : ""}`;
					const xw = ctx.measureText(xy).width + 8;
					ctx.fillStyle = alpha(colors["--color-surface"] || "#fff", 0.9); ctx.fillRect(10, -26, xw, 14);
					ctx.strokeStyle = alpha(col, 0.6); ctx.strokeRect(10, -26, xw, 14);
					ctx.fillStyle = col; ctx.fillText(xy, 14, -19);
				} else {
					// la flecha del mouse
					ctx.fillStyle = col; ctx.strokeStyle = "#fff"; ctx.lineWidth = 1.2;
					ctx.beginPath(); ctx.moveTo(0, 0); ctx.lineTo(0, 15); ctx.lineTo(4, 11.5); ctx.lineTo(7, 18); ctx.lineTo(9.5, 17); ctx.lineTo(6.5, 10.5); ctx.lineTo(11.5, 10.5); ctx.closePath();
					ctx.fill(); ctx.stroke();
					if (c.thinking) {
						// el reloj de arena, que se da vuelta
						const flip = (e * SKETCH_MS) / 700, rot = (Math.floor(flip) + easeOut((flip % 1) / 0.25)) * Math.PI;
						ctx.save(); ctx.translate(15, 4); ctx.rotate(rot);
						ctx.fillStyle = colors["--color-surface"] || "#fff"; ctx.strokeStyle = col; ctx.lineWidth = 1.3;
						ctx.beginPath(); ctx.moveTo(-5, -7); ctx.lineTo(5, -7); ctx.lineTo(0, 0); ctx.lineTo(5, 7); ctx.lineTo(-5, 7); ctx.lineTo(0, 0); ctx.closePath(); ctx.fill(); ctx.stroke();
						const sand = 1 - (flip % 1);
						ctx.fillStyle = col;
						ctx.beginPath(); ctx.moveTo(-3.6 * sand, -5.6 * sand); ctx.lineTo(3.6 * sand, -5.6 * sand); ctx.lineTo(0, 0); ctx.closePath(); ctx.fill();
						ctx.beginPath(); ctx.moveTo(0, 7 - 5.6 * (1 - sand)); ctx.lineTo(3.6 * (1 - sand), 7); ctx.lineTo(-3.6 * (1 - sand), 7); ctx.closePath(); ctx.fill();
						ctx.restore();
						tag = `${c.label} · ${t_("pensando…")}`;
					}
					if (c.erasing) tag = `${c.label} · ${t_("corrigiendo…")}`;
				}
				// su nombre (y qué está haciendo)
				ctx.fillStyle = col;
				ctx.font = "700 10px ui-monospace, SFMono-Regular, Menlo, monospace";
				const tw = ctx.measureText(tag).width + 10, ty = c.persona === "cad" ? 8 : 16;
				ctx.beginPath(); ctx.roundRect ? ctx.roundRect(10, ty, tw, 16, 4) : ctx.rect(10, ty, tw, 16); ctx.fill();
				ctx.fillStyle = "#fff"; ctx.textAlign = "left"; ctx.textBaseline = "middle"; ctx.fillText(tag, 15, ty + 8.5);
				ctx.restore();
			}
		}

		/** Cada test que termina: una partícula que cae en espiral al núcleo y se apaga. */
		function drawParticles() {
			for (let i = particles.length - 1; i >= 0; i--) {
				const pt = particles[i], e = (now - pt.t0) / 1500;
				if (e >= 1) { particles.splice(i, 1); continue; }
				if (e < 0) continue;
				const r0 = Math.hypot(pt.u, pt.v), a0 = Math.atan2(pt.v, pt.u), r = r0 + (lay.Rc - r0) * e * e, g = a0 + e * 1.4;
				const q = lay.P(Math.cos(g) * r, Math.sin(g) * r);
				ctx.globalAlpha = 1 - e * e; ctx.fillStyle = colorOf(pt.cls);
				ctx.beginPath(); ctx.arc(q.x, q.y, 2.4 * (1 - e * 0.5) * q.s, 0, TAU); ctx.fill();
			}
			ctx.globalAlpha = 1;
		}

		function draw() {
			if (!w || !ctx) return;
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			ctx.clearRect(0, 0, w, h);
			if (!lay) return;
			ctx.setTransform(dpr * view.k, 0, 0, dpr * view.k, dpr * view.x, dpr * view.y);
			const p = built(), anim = live() && !paused, ts = live() ? now / 1000 : 0;
			const segP = clamp01((p - 0.25) / 0.4), cellP = clamp01((p - 0.5) / 0.5);
			if (p > 0) {
				drawDisk(easeOut(p / 0.5), ts);
				drawCore(p, ts, anim);
				for (const s of [...lay.segs].sort((a, b) => a.depth - b.depth)) drawSeg(s, segP, cellP, ts, anim); // los de atrás primero
				if (anim) drawParticles();
			}
			if (sketch) drawSketch(clamp01((now - sketch.t0) / SKETCH_MS));
			drawGuides();
		}

		// ---------- las anotaciones: una línea guía desde su celda y el texto que se escribe solo ----------
		function placeNotes(instant = false) {
			if (!lay || !model) return;
			const notes = built() < 1 || sketch ? [] : notesOf(model, nowMs, t_).filter((n) => !match || match.has(n.id));
			const keep = new Set(), boxes = []; // lo ya ubicado, para que las etiquetas no se pisen
			const c0 = toScreen({ x: lay.cx - 64, y: lay.cy - 40 }), c1 = toScreen({ x: lay.cx + 64, y: lay.cy + 40 }), core = [c0.x, c0.y, c1.x, c1.y];
			const namesOnScreen = notes.length ? lay.segs.map((sg) => { const [a0, b0, a1, b1] = labelBox(sg, sg.lp, sg.side, sg.two); const p0 = toScreen({ x: a0, y: b0 }), p1 = toScreen({ x: a1, y: b1 }); return [p0.x, p0.y, p1.x, p1.y]; }) : [];
			const cellsOnScreen = notes.length ? lay.segs.flatMap((sg) => sg.cells.map((c) => ({ ...toScreen(c), r: c.r * view.k + 3 }))).filter((q) => q.x > -50 && q.x < w + 50 && q.y > -50 && q.y < h + 50) : [];
			for (const n of notes) {
				const nd = lay.at.get(n.id);
				if (!nd) continue;
				keep.add(n.id);
				let el = noteEls.get(n.id);
				if (!el) {
					el = document.createElement("button");
					el.type = "button";
					el.innerHTML = `<span class="la-box"><b></b></span>`;
					el.t0 = performance.now(); // la línea guía se traza y después se escribe el texto
					el.addEventListener("click", () => opts.onPick?.(n.id));
					notesEl.appendChild(el);
					noteEls.set(n.id, el);
				}
				el.className = `la-note ${n.kind}`;
				// dónde va la etiqueta: de las posiciones posibles (a cada lado, más cerca o más lejos), la que no se sale
				// del lienzo, no pisa otra anotación y tapa menos celdas (para que se pueda seguir apuntando a ellas);
				// a igualdad, hacia afuera del reactor
				const s = toScreen(nd), bw = n.text.length * 6.9 + 26, bh = 24, outR = nd.x >= lay.cx, outD = nd.y >= lay.cy;
				const rectOf = (right, dy) => { const x0 = s.x + (right ? 46 : -46 - bw); return [x0, s.y + dy - bh / 2, x0 + bw, s.y + dy + bh / 2]; };
				let best = null;
				for (const right of [outR, !outR]) for (const dy of outD ? [24, 52, -24, 80, -52, 108] : [-24, -52, 24, -80, 52, -108]) {
					const [x0, y0, x1, y1] = rectOf(right, dy);
					let cost = (x0 < 2 || x1 > w - 2 || y0 < 2 || y1 > h - 2 ? 1000 : 0) + (right !== outR ? 1 : 0) + Math.abs(dy) / 100;
					if (boxes.some(([c0, d0, c1, d1]) => x0 < c1 && x1 > c0 && y0 < d1 && y1 > d0)) cost += 500;
					for (const q of cellsOnScreen) if (q.x > x0 - q.r && q.x < x1 + q.r && q.y > y0 - q.r && q.y < y1 + q.r) cost += 3;
					for (const [c0, d0, c1, d1] of namesOnScreen) if (x0 < c1 && x1 > c0 && y0 < d1 && y1 > d0) cost += 4;
					if (x0 < core[2] && x1 > core[0] && y0 < core[3] && y1 > core[1]) cost += 20; // ni el avance, al centro
					if (!best || cost < best.cost) best = { cost, right, dy };
				}
				const { right, dy } = best, dx = right ? 30 : -30;
				boxes.push(rectOf(right, dy));
				el.style.left = s.x + dx + (right ? 16 : -16) + "px"; el.style.top = s.y + dy + "px";
				el.classList.toggle("left", !right);
				guides.set(n.id, { x: s.x, y: s.y, dx, dy, right, kind: n.kind, t0: el.t0 });
				const b = el.querySelector("b");
				el.setAttribute("aria-label", n.text);
				if (b.dataset.full !== n.text) {
					const fresh = b.dataset.full === undefined; // se escribe sola al aparecer; después (el factor, los minutos) solo se actualiza
					b.dataset.full = n.text;
					if (!live() || instant || !fresh) b.textContent = n.text;
					else typewrite(b, n.text);
				}
			}
			for (const [id, el] of noteEls) if (!keep.has(id)) { el.remove(); noteEls.delete(id); }
			for (const id of guides.keys()) if (!keep.has(id)) guides.delete(id);
		}
		/** Las líneas guía de las anotaciones (en pixeles de pantalla): se trazan en 250 ms. */
		function drawGuides() {
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			for (const g of guides.values()) {
				const k = live() ? Math.min(1, (performance.now() - g.t0) / 250) : 1;
				const col = g.kind === "turtle" ? colorOf("turtle") : colorOf("fail");
				ctx.strokeStyle = col; ctx.fillStyle = col; ctx.lineWidth = 1.2; ctx.globalAlpha = 1;
				const ex = g.x + g.dx, ey = g.y + g.dy, fx = ex + (g.right ? 16 : -16);
				ctx.beginPath(); ctx.moveTo(g.x, g.y);
				if (k < 0.6) { const q = k / 0.6; ctx.lineTo(g.x + g.dx * q, g.y + g.dy * q); }
				else { ctx.lineTo(ex, ey); ctx.lineTo(ex + (fx - ex) * ((k - 0.6) / 0.4), ey); }
				ctx.stroke();
				ctx.beginPath(); ctx.arc(g.x, g.y, 2.4, 0, TAU); ctx.fill();
			}
		}
		/** El texto se escribe solo, letra por letra (como un asistente que va anotando). */
		function typewrite(el, text) {
			el.textContent = "";
			let i = 0;
			const tick = () => { if (el.dataset.full !== text) return; el.textContent = text.slice(0, ++i); if (i < text.length) setTimeout(tick, 18); };
			setTimeout(tick, 260); // después de que se traza la línea guía
		}

		function loop(ts) {
			raf = 0;
			const moving = live() && !paused && visible && !document.hidden && wrap.isConnected;
			if (ts - lastFrame >= 1000 / FPS) {
				now = performance.now(); lastFrame = ts;
				if (sketch && now - sketch.t0 >= SKETCH_MS) { sketch = null; canvas.dataset.sketch = "off"; placeNotes(); } // las anotaciones, cuando terminó
				if (intro && now - intro.t0 >= INTRO_MS) { intro = null; placeNotes(); }
				draw();
			}
			if (moving || intro || sketch) raf = requestAnimationFrame(loop);
		}
		function kick() { if (!raf) raf = requestAnimationFrame(loop); }

		// ---------- la tarjeta del puntero ----------
		function hideTip() {
			tip.hidden = true;
			if (topLayer && tip.matches(":popover-open")) tip.hidePopover();
		}
		function placeTip(x, y) {
			tip.hidden = false;
			const r = canvas.getBoundingClientRect();
			if (topLayer && !tip.matches(":popover-open")) tip.showPopover();
			const tw = tip.offsetWidth, th = tip.offsetHeight, vx = (topLayer ? r.left : 0) + x, vy = (topLayer ? r.top : 0) + y;
			const W = topLayer ? innerWidth : w, H = topLayer ? innerHeight : h;
			tip.style.left = Math.max(4, Math.min(W - tw - 4, vx + 14 + tw <= W - 4 ? vx + 14 : vx - 14 - tw)) + "px";
			tip.style.top = Math.max(4, Math.min(H - th - 4, vy + 14 + th <= H - 4 ? vy + 14 : vy - 14 - th)) + "px";
		}
		function showTip(hit) {
			const F = window.TraceReportsLiveField;
			if (!F || !hit) return hideTip();
			tip.innerHTML = "";
			if (hit.kind === "test") {
				if (!labels.card) return hideTip();
				F.cardInto(tip, labels.card(hit.node.t), hit.node.t.name);
				const s = toScreen(hit.node);
				return placeTip(s.x, s.y);
			}
			const a = hit.arm, cur = a.current;
			F.cardInto(tip, { cls: a.state === "idle" ? "skip" : CLS[a.state] || "running", icon: a.state === "idle" ? "i-pause" : a.state === "no_signal" ? "i-st-nosig" : "i-st-running",
				label: t_(a.state === "idle" ? "Desocupado" : a.state === "no_signal" ? "Sin señal" : a.state === "turtle" ? "Tortuga" : "Corriendo"),
				dur: "", times: cur ? cur.name : t_("Esperando un test"),
				where: `${t_("{n} terminados", { n: a.done })}${a.fails ? ` · ${t_("{n} fallaron", { n: a.fails })}` : ""}${a.hidden ? ` · ${t_("{n} anteriores sin mostrar; los fallos se ven siempre", { n: a.hidden })}` : ""}`,
				hint: t_("Clic: ver en la tabla solo los tests de este worker") }, a.label);
			const s = toScreen(a.lp);
			placeTip(s.x, s.y);
		}

		const local = (e) => { const r = canvas.getBoundingClientRect(); return [e.clientX - r.left, e.clientY - r.top]; };
		const hitAt = (e) => { const [x, y] = local(e); return hitTest(lay, ...toPlan(x, y), match, view.k); };
		const idleCursor = () => (view.k > 1 ? "grab" : "move");
		canvas.addEventListener("pointerdown", (e) => {
			if (sketch) { skipSketch(); dragged = true; return; } // un clic durante el boceto lo salta (y no abre nada)
			if (e.button !== 0) return;
			// sin zoom, arrastrar gira el reactor; con zoom (o con Mayús), mueve la vista
			drag = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y, rot: cam.rot, tilt: cam.tilt, id: e.pointerId, pan: view.k > 1 || e.shiftKey };
			dragged = false;
		});
		canvas.addEventListener("pointermove", (e) => {
			if (drag) {
				const dx = e.clientX - drag.x, dy = e.clientY - drag.y;
				if (!dragged && Math.hypot(dx, dy) > 4) { dragged = true; canvas.setPointerCapture?.(drag.id); hideTip(); hover = null; }
				if (dragged) {
					if (drag.pan) { view.x = drag.vx + dx; view.y = drag.vy + dy; clampView(); placeNotes(true); draw(); }
					else setCam(drag.rot + dx * 0.008, drag.tilt - dy * 0.006);
					canvas.style.cursor = "grabbing";
					return;
				}
			}
			const hit = hitAt(e);
			const key = hit ? `${hit.kind}:${hit.node?.t.id ?? hit.arm.key}` : "";
			if (key === (hover ? `${hover.kind}:${hover.node?.t.id ?? hover.arm.key}` : "")) return;
			hover = hit;
			canvas.style.cursor = hit ? "pointer" : idleCursor();
			showTip(hit);
			if (hit?.kind === "test") opts.onHover?.(hit.node.t.id);
			draw();
		});
		const endDrag = () => { drag = null; canvas.style.cursor = idleCursor(); };
		canvas.addEventListener("pointerup", endDrag);
		canvas.addEventListener("pointercancel", endDrag);
		canvas.addEventListener("pointerleave", () => { if (!drag) { hover = null; hideTip(); draw(); } });
		canvas.addEventListener("click", (e) => {
			if (dragged) { dragged = false; return; } // soltar después de arrastrar no es un clic
			const hit = hitAt(e);
			if (!hit) return;
			if (hit.kind === "agent") return opts.onWorker?.(hit.arm.key);
			if (e.ctrlKey || e.metaKey) { if (expanded) setExpanded(false); return opts.onOpen?.(hit.node.t.id, hit.node.t.run_id); }
			opts.onPick?.(hit.node.t.id);
		});
		// la rueda acerca en pantalla completa (o con Ctrl/⌘); en la vista normal sigue desplazando la página
		canvas.addEventListener("wheel", (e) => {
			if (!expanded && !e.ctrlKey && !e.metaKey) return;
			e.preventDefault();
			const [x, y] = local(e);
			zoomAt(e.deltaY < 0 ? 1.12 : 1 / 1.12, x, y);
		}, { passive: false });
		// con el teclado: flechas entre los workers (resaltado, con su tarjeta); Enter, sus tests en la tabla;
		// + / − / 0 el zoom, F pantalla completa, Espacio pausa (como en el Campo)
		canvas.addEventListener("keydown", (e) => {
			if (e.key === "b" || e.key === "B") { e.preventDefault(); if (sketch) skipSketch(); else startSketch(); return; }
			if (sketch && e.key !== "Tab") skipSketch(); // cualquier otra tecla salta el boceto (y hace lo suyo)
			if (e.key === " ") { e.preventDefault(); opts.onPause?.(); return; }
			if (e.key === "+" || e.key === "=") { e.preventDefault(); zoomAt(1.25); return; }
			if (e.key === "-" || e.key === "_") { e.preventDefault(); zoomAt(1 / 1.25); return; }
			if (e.key === "0") { e.preventDefault(); resetView(); return; }
			if (e.key === "f" || e.key === "F") { e.preventDefault(); setExpanded(!expanded); return; }
			if (e.key === "Escape" && expanded) { e.preventDefault(); e.stopPropagation(); setExpanded(false); return; }
			const segs = (lay?.segs || []).filter((a) => !match || a.tests.some((t) => match.has(t.id))); // lo apagado no se recorre
			const n = segs.length;
			if (!n) return;
			const cur = segs.findIndex((a) => a.i === focusIdx);
			if (e.key === "ArrowRight" || e.key === "ArrowDown") focusIdx = segs[(cur + 1) % n].i;
			else if (e.key === "ArrowLeft" || e.key === "ArrowUp") focusIdx = segs[(cur - 1 + n) % n].i;
			else if (e.key === "Enter" && focusIdx >= 0) { e.preventDefault(); return opts.onWorker?.(lay.segs[focusIdx].key); }
			else return;
			e.preventDefault();
			const a = lay.segs[focusIdx];
			showTip({ kind: "agent", arm: a });
			canvas.setAttribute("aria-label", `${labels.aria} ${a.label}: ${a.current ? a.current.name : t_("Desocupado")}`);
			draw();
		});
		canvas.addEventListener("blur", () => { focusIdx = -1; hideTip(); draw(); });
		document.addEventListener("keydown", (e) => { if (e.key === "Escape" && expanded) setExpanded(false); }, true);

		let lastTheme = null;
		return {
			attach(slot) {
				slotEl = slot;
				if (expanded) return; // en pantalla completa vive en el body hasta que se sale
				if (!slot) { if (wrap.isConnected) wrap.remove(); hideTip(); return; }
				let moved = false;
				if (wrap.parentElement !== slot) { slot.appendChild(wrap); moved = true; }
				const theme = document.body.dataset.theme || "";
				if (moved || theme !== lastTheme || !w) { lastTheme = theme; readColors(); resize(); }
				if (moved && model) startSketch(); // al aparecer: el boceto y después se enciende
				kick();
			},
			/** data: {exec, now, live (TraceReportsLive), match (Set de ids o null), selected, aria, card(it), paused} */
			update(data) {
				const first = data.exec.key !== lastExec;
				lastExec = data.exec.key;
				nowMs = data.now;
				const before = model?.agents.length;
				model = agentsModel(data.exec, data.now, data.live);
				if (first) { view = { k: 1, x: 0, y: 0 }; cam = { ...CAM }; canvas.dataset.zoom = "1.00"; particles.length = 0; shocks.clear(); seenEnded = null; }
				// el tamaño se recalcula solo si cambia lo que ocupa (otra ejecución, otros workers): entre un refresco y
				// el siguiente, lo que está bajo el mouse sigue ahí
				relayout(first || before !== model.agents.length);
				// lo que terminó desde la última vez: una partícula al núcleo; si falló, su onda
				const ended = new Set();
				for (const a of model.agents) for (const x of a.tests) if (x.ended_at) ended.add(x.id);
				if (seenEnded && lay && live()) {
					const t0 = performance.now();
					for (const a of model.agents) for (const x of a.tests) {
						if (!x.ended_at || seenEnded.has(x.id)) continue;
						const c = lay.at.get(x.id);
						if (c && particles.length < 80) particles.push({ u: c.u, v: c.v, t0, cls: isFail(x.state) ? "fail" : "pass" });
						if (isFail(x.state)) { shocks.set(x.id, t0); flashT = t0; }
					}
				}
				seenEnded = ended;
				match = data.match || null;
				selected = data.selected || null;
				if (hover && ((hover.node && match && !match.has(hover.node.t.id)) || !lay)) { hover = null; hideTip(); } // lo que se apagó deja de responder
				if (data.card) labels.card = data.card;
				labels.aria = data.aria || "";
				canvas.setAttribute("aria-label", labels.aria);
				canvas.dataset.agents = String(model.agents.length);
				setPaused(!!data.paused);
				if (first) canvas.dataset.cam = `${cam.rot.toFixed(2)} ${cam.tilt.toFixed(2)}`;
				if (first && w) startSketch();
				else placeNotes(first);
				kick(); draw();
			},
			setMotion(on) { motion = !!on; if (!live()) { intro = null; sketch = null; canvas.dataset.sketch = "off"; placeNotes(true); } kick(); draw(); },
			zoomAt, resetView, setExpanded,
			get view() { return { ...view }; },
			get layout() { return lay; },
			get model() { return model; },
		};
	}

	window.TraceReportsLiveAgents = { agentsModel, reactor, fitRadius, projector, labelBox, sketchPlan, agentClock, partial, PERSONAS, notesOf, hitTest, create, CAM, ROWS, SKETCH_MS };
})();
