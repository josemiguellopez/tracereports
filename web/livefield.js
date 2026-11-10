// El campo de la ejecución (En vivo): la ejecución como un toroide de partículas que se lee como un
// reloj. El ángulo alrededor del anillo es el tiempo (arriba el comienzo, en sentido horario, con las
// pausas comprimidas como en la línea de tiempo) y cada worker o proceso ocupa una vuelta alrededor del
// tubo. Cada test es una partícula grande y brillante donde terminó (o donde va, sobre la aguja de
// "ahora"), del color de su estado y más grande cuanto más lento fue; se puede tocar. El tubo gira
// despacio sobre sí mismo (el tiempo no cambia de lugar) y se detiene con el mouse encima.
//
// Lo demás también dice algo: los rayos alrededor son cuántos tests corrían en cada momento (del color
// del peor estado de ese momento) y el polvo tenue es solo el volumen del toroide (no se toca). Las
// señales que llegan por /api/v1/stream lanzan un pulso desde su canal hasta su test. El canvas vive
// fuera del HTML de la vista (que se redibuja con cada evento): app.js lo vuelve a colocar.
(() => {
	"use strict";

	const TAU = Math.PI * 2;
	/** Lo que el anillo deja libre arriba: la costura entre el comienzo y "ahora". */
	const SEAM = 0.05;
	/** La inclinación de la vista: sin(elevación). */
	const TILT = 0.5;
	const COS_E = Math.sqrt(1 - TILT * TILT);
	const FPS = 30;
	/** Rayos alrededor del anillo: uno por tramo de tiempo. */
	const SPIKES = 144;
	/** Giro del tubo (rad/s) y cuántas partículas de polvo. */
	const SPIN = 0.12, DUST = 750;

	/**
	 * Lo que se dibuja, sin pantalla (se prueba en node): los carriles, un ítem por test con su ángulo de
	 * inicio y fin (radianes, 0 = arriba, horario) y la densidad por tramo (cuántos corrían y el peor
	 * estado), que dibujan los rayos. exec: la de /api/v1/live/execution; LIVE: window.TraceReportsLive.
	 */
	function fieldLayout(exec, now, LIVE) {
		const tests = LIVE.allTests(exec);
		if (!tests.length) return { rings: [], items: [], gaps: [], nowAngle: null, density: [] };
		const sc = LIVE.timeScale(exec, now);
		const ang = (t) => TAU * (SEAM / 2 + Math.min(1, Math.max(0, sc.x(t))) * (1 - SEAM));
		const lanes = LIVE.lanesOf(exec, now);
		const ringOf = new Map();
		lanes.forEach((l, k) => { for (const t of l.tests) ringOf.set(t.id, k); });
		const items = tests.map((t) => {
			const a0 = ang(t.started_at), a1 = Math.max(a0 + 0.004, ang(t.ended_at || now));
			return { id: t.id, run_id: t.run_id, name: t.name, file: t.file, worker: t.worker || "", state: t.state, ring: ringOf.get(t.id) ?? 0,
				a0, a1, factor: t.factor || 0, elapsed: t.elapsed || 0, started_at: t.started_at, ended_at: t.ended_at || 0, running: !t.ended_at };
		}).sort((p, q) => p.started_at - q.started_at || p.id - q.id);
		const rank = (s) => (LIVE.STATES[s] ? LIVE.STATES[s].rank : 5);
		// por tramo: cuántos corrían (el largo del rayo) y cómo terminaron los que terminaron ahí (su color:
		// rojo solo donde algo falló; un test largo que sigue corriendo no tiñe todos los tramos)
		const density = Array.from({ length: SPIKES }, () => ({ n: 0, worst: null }));
		for (const it of items) {
			const i0 = Math.floor((it.a0 / TAU) * SPIKES), i1 = Math.min(SPIKES - 1, Math.floor((it.a1 / TAU) * SPIKES));
			for (let i = i0; i <= i1; i++) density[i].n++;
			if (!it.running) { const d = density[i1]; if (!d.worst || rank(it.state) < rank(d.worst)) d.worst = it.state; }
		}
		return {
			rings: lanes.map((l) => ({ key: l.key, label: l.label, proc: l.proc || 0 })),
			items,
			gaps: sc.gaps.map((g) => ({ a0: ang(g.a), a1: ang(g.b), dur: g.dur })),
			nowAngle: exec.running ? ang(now) : null,
			density,
		};
	}

	/**
	 * La ingesta real de esta ejecución, de los eventos que llegaron por /api/v1/stream a esta pestaña:
	 * cuántos por minuto, hace cuánto el último, el estado y la serie del último minuto en tramos de 5 s
	 * (las barras del HUD). Una ejecución terminada nunca se llama "activa", ni una que dejó de enviar.
	 */
	function ingestStats(events, now, { running, connected, quietMs = 60e3 } = {}) {
		const last = events.length ? Math.max(...events.map((e) => e.at)) : 0;
		const recent = events.filter((e) => e.at >= now - 60e3 && e.at <= now);
		const byKind = {};
		for (const e of recent) byKind[e.kind] = (byKind[e.kind] || 0) + 1;
		const series = Array(12).fill(0);
		for (const e of recent) series[Math.min(11, Math.floor((e.at - (now - 60e3)) / 5000))]++;
		let state;
		if (!running) state = "done";
		else if (!connected) state = "offline";
		else if (!last || now - last > quietMs) state = "quiet";
		else state = "active";
		return { state, perMin: recent.length, lastAgo: last ? Math.max(0, now - last) : null, byKind, series };
	}

	/** El canal de un evento del stream: tests (empezó/terminó), fallos, pasos o red. */
	function channelOf(type, e) {
		const st = String(e?.data?.status || "").toUpperCase();
		if ((type === "test" || type === "log") && (st === "FAIL" || st === "INTERRUPTED")) return "fail";
		if (type === "test") return "test";
		if (type === "log") return "step";
		if (type === "network") return "net";
		return "";
	}

	/** La vista por defecto: sin giro, la inclinación de siempre, sin zoom ni desplazamiento. */
	const VIEW0 = { yaw: 0, pitch: TILT, zoom: 1, panX: 0, panY: 0 };
	/** Límites de la vista: de casi de canto a casi desde arriba; de la mitad a cuatro veces. */
	function clampView(v) {
		return { yaw: ((v.yaw % TAU) + TAU) % TAU, pitch: Math.min(0.97, Math.max(0.12, v.pitch)), zoom: Math.min(4, Math.max(0.5, v.zoom)),
			panX: v.panX || 0, panY: v.panY || 0 };
	}

	/**
	 * El toroide para un ancho/alto y una vista (girado, inclinado, con zoom y desplazado, como lo dejó
	 * la persona): centro, radio mayor R y del tubo r, y la orientación que usa project().
	 */
	function geometry(w, h, view = VIEW0) {
		const vw = clampView({ ...VIEW0, ...view });
		const sinE = vw.pitch, cosE = Math.sqrt(1 - sinE * sinE);
		// a la vista por defecto, que entre con sus rayos: R + r + rayos ≤ 47 % del ancho y del alto lo suyo
		const k = 0.4; // r = k·R
		// un poco más chico que el escenario: las esquinas quedan para el HUD y ninguna partícula queda tapada
		const byW = (w * 0.4) / (1 + k + 0.12);
		const byH = (h * 0.36) / ((1 + k + 0.12) * TILT + k * COS_E);
		const R = Math.max(30, Math.min(byW, byH)) * vw.zoom;
		return { cx: w / 2 + vw.panX, cy: h / 2 + h * 0.02 + vw.panY, R, r: R * k, sinE, cosE, yaw: vw.yaw };
	}

	/** La vuelta alrededor del tubo de un carril (más el giro del momento). */
	const vOf = (ring, n, spin = 0) => TAU * ((ring + 0.5) / Math.max(1, n)) + spin;

	/**
	 * Un punto del toroide en pantalla: u = ángulo del reloj (0 arriba, horario), v = vuelta alrededor
	 * del tubo. depth va de -1 (atrás) a 1 (adelante): lo de adelante se ve más grande y más vivo.
	 */
	function project(g, u, v, out = 0) {
		const sinE = g.sinE ?? TILT, cosE = g.cosE ?? COS_E, a = u + (g.yaw || 0);
		const rr = g.R + g.r * Math.cos(v) + out, z = g.r * Math.sin(v);
		const back = rr * Math.cos(a); // positivo: hacia el fondo (arriba en pantalla)
		return { x: g.cx + rr * Math.sin(a), y: g.cy - back * sinE - z * cosE, depth: (-back * cosE + z * sinE) / (g.R + g.r) };
	}

	/** La partícula (punto) de un test, con el giro del momento. */
	const headOf = (g, layout, it, spin) => project(g, it.a1, vOf(it.ring, layout.rings.length, spin));
	/** Su tamaño: más grande adelante y cuanto más lento fue frente a lo normal. */
	const sizeOf = (g, it, depth) => Math.max(2.2, Math.min(8, g.R * 0.022 * (1 + Math.min(1.6, Math.max(0, (it.factor || 1) - 1)) * 0.6) * (0.8 + 0.3 * depth)));

	/** El test bajo el puntero: la partícula más cercana (a menos de ~14 px); a igual distancia, la de adelante. */
	/**
	 * La partícula bajo el puntero (la más cercana a menos de HIT_PX). Con un filtro (``match``), las
	 * que no coinciden se siguen viendo tenues pero no se pueden tocar: así el clic va al test que se
	 * buscaba aunque haya otro apagado encima.
	 */
	function hitTest(layout, g, x, y, spin = 0, match = null) {
		let best = null, bd = HIT_PX;
		for (const it of layout.items) {
			if (match && !match.has(it.id)) continue;
			const p = headOf(g, layout, it, spin), d = Math.hypot(p.x - x, p.y - y) - p.depth * 0.5;
			if (d < bd) { best = it; bd = d; }
		}
		return best;
	}

	const HIT_PX = 18; // radio para acertar una partícula con el mouse (antes 14)

	// ---------- en pantalla ----------

	const STATE_VAR = { pass: "--color-success", fail: "--color-danger", nosig: "--color-danger", interrupted: "--color-danger", warning: "--color-warning",
		turtle: "--color-warning", running: "--color-info", skip: "--color-text-muted" };
	const CLS = { PASS: "pass", FAIL: "fail", no_signal: "nosig", INTERRUPTED: "interrupted", WARNING: "warning", turtle: "turtle", running: "running", SKIP: "skip" };
	// "ahora" no es un estado: va en el color del texto (blanco en oscuro, negro en claro), el único que
	// no significa nada en el campo; antes iba en rojo y se confundía con los fallos
	const NOW_VAR = "--color-text";
	const CHANNEL_VAR = { test: "--color-success", step: "--color-info", net: "--color-primary", fail: "--color-danger" };

	// el polvo del toroide: siempre el mismo (semilla fija), para que no "salte" entre redibujos
	const DUST_P = (() => {
		let s = 91;
		const rnd = () => { s = (s * 1664525 + 1013904223) >>> 0; return s / 4294967296; };
		return Array.from({ length: DUST }, () => ({ u: rnd() * TAU, v: rnd() * TAU, size: 0.4 + rnd() * 1.1, phase: rnd() * TAU, hue: rnd() }));
	})();

	/**
	 * La tarjeta de un test bajo el mouse: su estado (color e ícono) arriba con la duración, el nombre,
	 * cuánto tardó frente a lo normal (destacado si es lento), las horas, dónde corrió y qué hace el clic.
	 * card: {cls, icon, label, dur, slow, factor, times, where, hint}.
	 */
	function cardInto(el, c, name) {
		const mk = (tag, cls, text) => { const n = document.createElement(tag); if (cls) n.className = cls; if (text !== undefined) n.textContent = text; return n; };
		el.className = `lf-tip lf-card st-${c.cls || "skip"}`;
		const head = mk("div", "lfc-head"), chip = mk("span", "lfc-state");
		if (c.icon) {
			const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg"), use = document.createElementNS("http://www.w3.org/2000/svg", "use");
			svg.setAttribute("aria-hidden", "true"); use.setAttribute("href", `#${c.icon}`); svg.append(use); chip.append(svg);
		}
		chip.append(mk("span", "", c.label));
		head.append(chip, mk("span", "lfc-dur", c.dur || ""));
		el.append(head, mk("b", "lfc-name", name));
		if (c.factor) el.append(mk("span", `lfc-factor${c.slow ? " slow" : ""}`, c.factor));
		if (c.times) el.append(mk("span", "lfc-meta", c.times));
		if (c.where) el.append(mk("span", "lfc-meta lfc-where", c.where));
		if (c.hint) el.append(mk("span", "lfc-hint", c.hint));
	}

	/**
	 * create({onPick}) → {attach(el), update(data), signal(channel, testId), setMotion(on)}. El canvas
	 * se crea una vez; attach lo pone dentro del lugar que dejó el HTML de la vista.
	 */
	function create(opts = {}) {
		const wrap = document.createElement("div");
		wrap.className = "lf-wrap";
		const canvas = document.createElement("canvas");
		canvas.className = "lf-canvas";
		canvas.tabIndex = 0;
		canvas.setAttribute("role", "img");
		const tip = document.createElement("div");
		tip.className = "lf-tip"; tip.hidden = true; tip.setAttribute("role", "status");
		const t_ = opts.t || ((x) => x);
		// la barra de controles (vive con el canvas: no se redibuja con la vista)
		const bar = document.createElement("div");
		bar.className = "lf-ctrl";
		const btn = (k, icon, label, tip2, keys) => `<button type="button" class="lf-cb" data-lf="${k}" aria-label="${label}" data-tip-title="${label}" data-tip="${tip2}"${keys ? ` data-tip-keys="${keys}"` : ""}><svg aria-hidden="true"><use href="#${icon}"/></svg><span>${label}</span></button>`;
		const esc2 = (x) => String(x).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
		bar.innerHTML = [
			btn("pause", "i-pause", esc2(t_("Pausar vista")), esc2(t_("Congela el campo y los datos para analizar algo puntual. La ingesta sigue en el servidor: al reanudar, se pone al día.")), esc2(t_("Espacio"))),
			btn("out", "i-zoom-out", esc2(t_("Alejar")), esc2(t_("Aleja el campo.")), "−"),
			btn("in", "i-zoom-in", esc2(t_("Acercar")), esc2(t_("Acerca el campo (también con la rueda en pantalla completa, o Ctrl + rueda).")), "+"),
			btn("reset", "i-orbit", esc2(t_("Restablecer")), esc2(t_("Vuelve a la vista de siempre: sin giro, sin zoom, el comienzo arriba.")), "0"),
			btn("full", "i-fit", esc2(t_("Pantalla completa")), esc2(t_("El campo a pantalla completa, para explorarlo. Esc para volver.")), "F"),
		].join("");
		const head = document.createElement("div");
		head.className = "lf-fullhead"; head.hidden = true;
		const help = document.createElement("div");
		help.className = "lf-help";
		help.textContent = t_("Arrastra para girar · Shift + arrastrar para mover · rueda para acercar · Ctrl + clic en un punto: abrir el test");
		wrap.append(canvas, tip, bar, head, help);
		const ctx = canvas.getContext("2d");
		const reduce = matchMedia("(prefers-reduced-motion: reduce)");
		let layout = { rings: [], items: [], gaps: [], nowAngle: null, density: [] }, g = geometry(1, 1);
		let lastTheme = null;
		let view = { ...VIEW0 }, expanded = false, drag = null, dragged = false, slotEl = null, paused = false, tween = 0;
		let hoverTimer = 0;
		// el clic se nota al instante (un aro que se abre en el punto), antes de que llegue su detalle: sin
		// eso, con el servidor lento, se volvía a hacer clic y el segundo clic contaba como doble clic
		const ACK_MS = 450;
		let ack = null, openable = false;
		let w = 0, h = 0, dpr = 1, hover = null, focusIdx = -1, sel = null, match = null, motion = true, visible = true, inside = false;
		let t = 0, spin = 0, lastFrame = 0, lastSpin = 0, raf = 0;
		const pulses = [], prevStates = new Map(), labels = { aria: "", tip: (it) => it.name };
		const colors = {};
		const readColors = () => {
			const cs = getComputedStyle(wrap.isConnected ? wrap : document.documentElement);
			for (const v of new Set([...Object.values(STATE_VAR), ...Object.values(CHANNEL_VAR), "--color-surface", "--color-border", "--color-text", "--color-text-muted", "--color-primary", "--color-info"])) colors[v] = cs.getPropertyValue(v).trim() || "#888";
		};
		const colorOf = (state) => colors[STATE_VAR[CLS[state] || "running"]] || colors["--color-info"];
		const live = () => motion && !reduce.matches;

		function resize() {
			const r = wrap.getBoundingClientRect();
			if (!r.width) return;
			w = r.width; h = r.height; dpr = Math.min(window.devicePixelRatio || 1, 2);
			canvas.width = Math.round(w * dpr); canvas.height = Math.round(h * dpr);
			canvas.style.width = w + "px"; canvas.style.height = h + "px";
			g = geometry(w, h, view);
			draw();
		}
		const ro = typeof ResizeObserver === "function" ? new ResizeObserver(resize) : null;
		ro?.observe(wrap);
		const io = typeof IntersectionObserver === "function" ? new IntersectionObserver((en) => { visible = en[0].isIntersecting; if (visible) kick(); }) : null;
		io?.observe(wrap);

		/** Una estela sobre la superficie del toroide: de a0 a a1 a la vuelta v. */
		function trailPath(a0, a1, v) {
			const steps = Math.max(2, Math.ceil((a1 - a0) / 0.04));
			ctx.beginPath();
			for (let i = 0; i <= steps; i++) { const p = project(g, a0 + ((a1 - a0) * i) / steps, v); i ? ctx.lineTo(p.x, p.y) : ctx.moveTo(p.x, p.y); }
		}

		function draw() {
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			ctx.clearRect(0, 0, w, h);
			if (!layout.items.length || !w) return;
			const n = layout.rings.length, on = live();
			const primary = colors["--color-primary"], info = colors["--color-info"];
			// halo y órbitas: el "campo" (decorativo, tenue)
			const halo = ctx.createRadialGradient(g.cx, g.cy, g.R * 0.2, g.cx, g.cy, g.R * 1.9);
			halo.addColorStop(0, "transparent"); halo.addColorStop(0.5, primary); halo.addColorStop(1, "transparent");
			ctx.globalAlpha = 0.07; ctx.fillStyle = halo; ctx.fillRect(0, 0, w, h);
			ctx.globalAlpha = 0.35; ctx.strokeStyle = colors["--color-border"]; ctx.lineWidth = 1; ctx.setLineDash([3, 6]);
			for (const f of [0.55, 1.62, 1.95]) { ctx.beginPath(); ctx.ellipse(g.cx, g.cy, g.R * f, Math.max(1, g.R * f * g.sinE), 0, 0, TAU); ctx.stroke(); }
			ctx.setLineDash([]);
			// rayos: cuántos tests corrían en cada momento (del color del peor estado de ese momento)
			const maxN = Math.max(1, ...layout.density.map((d) => d.n));
			for (let i = 0; i < layout.density.length; i++) {
				const d = layout.density[i];
				if (!d.n) continue;
				const u = ((i + 0.5) / SPIKES) * TAU, len = 4 + (g.R * 0.16 * d.n) / maxN;
				const wob = on ? 1 + 0.12 * Math.sin(t * 2.4 + i * 0.7) : 1;
				const p0 = project(g, u, 0, 3), p1 = project(g, u, 0, 3 + len * wob);
				const bad = d.worst && d.worst !== "PASS";
				ctx.globalAlpha = (bad ? 0.45 : 0.22) + 0.3 * (0.5 + 0.5 * p0.depth);
				ctx.strokeStyle = d.worst ? colorOf(d.worst) : primary; ctx.lineWidth = bad ? 1.6 : 1;
				ctx.beginPath(); ctx.moveTo(p0.x, p0.y); ctx.lineTo(p1.x, p1.y); ctx.stroke();
			}
			// pausas comprimidas y la costura: cortes radiales punteados
			ctx.setLineDash([2, 4]); ctx.strokeStyle = colors["--color-text-muted"]; ctx.lineWidth = 1; ctx.globalAlpha = 0.6;
			for (const gp of layout.gaps) for (const a of [gp.a0, gp.a1]) { const p0 = project(g, a, Math.PI), p1 = project(g, a, 0, g.R * 0.2); ctx.beginPath(); ctx.moveTo(p0.x, p0.y); ctx.lineTo(p1.x, p1.y); ctx.stroke(); }
			ctx.setLineDash([]);
			// el polvo (volumen del toroide) y los tests, de atrás hacia adelante
			const drawn = [];
			for (const p of DUST_P) {
				const q = project(g, p.u + t * 0.01, p.v + spin * 1.3);
				drawn.push({ k: 0, q, p });
			}
			for (const it of layout.items) drawn.push({ k: 1, q: headOf(g, layout, it, spin), it });
			drawn.sort((a, b) => a.q.depth - b.q.depth);
			// estelas de los tests (lo que duraron), finas, sobre la superficie
			for (const it of layout.items) {
				const off = match && !match.has(it.id), v = vOf(it.ring, n, spin);
				ctx.globalAlpha = off ? 0.05 : 0.22;
				ctx.strokeStyle = colorOf(it.state); ctx.lineWidth = 1.4; ctx.lineCap = "round";
				trailPath(it.a0, it.a1, v); ctx.stroke();
			}
			for (const d of drawn) {
				const front = 0.5 + 0.5 * d.q.depth;
				if (!d.k) {
					const tw = on ? 0.75 + 0.25 * Math.sin(t * 1.7 + d.p.phase) : 1;
					ctx.globalAlpha = (0.08 + 0.3 * front) * tw;
					ctx.fillStyle = d.p.hue < 0.75 ? primary : info;
					ctx.beginPath(); ctx.arc(d.q.x, d.q.y, d.p.size * (0.7 + 0.5 * front), 0, TAU); ctx.fill();
					continue;
				}
				const it = d.it, col = colorOf(it.state), off = match && !match.has(it.id), size = sizeOf(g, it, d.q.depth);
				const failing = it.state === "FAIL" || it.state === "no_signal" || it.state === "INTERRUPTED";
				const blink = it.state === "no_signal" && on ? 0.45 + 0.55 * Math.abs(Math.sin(t * 1.3)) : 1;
				if (!off) {
					ctx.globalAlpha = (failing ? (on ? 0.25 + 0.18 * Math.sin(t * 3 + it.id) : 0.32) : 0.16) * (0.5 + 0.5 * front) * blink;
					ctx.fillStyle = col; ctx.beginPath(); ctx.arc(d.q.x, d.q.y, size * (failing ? 2.6 : 2), 0, TAU); ctx.fill();
				}
				ctx.globalAlpha = (off ? 0.16 : 0.62 + 0.38 * front) * blink;
				ctx.fillStyle = col; ctx.beginPath(); ctx.arc(d.q.x, d.q.y, size, 0, TAU); ctx.fill();
				if (!off && front > 0.55) { // un brillo en el centro: se ve como una partícula, no un círculo plano
					ctx.globalAlpha = 0.5 * front * blink; ctx.fillStyle = "#fff";
					ctx.beginPath(); ctx.arc(d.q.x - size * 0.25, d.q.y - size * 0.25, size * 0.35, 0, TAU); ctx.fill();
				}
			}
			ctx.globalAlpha = 1;
			// "ahora": la aguja desde dentro del tubo hasta los rayos, con su punto que respira
			if (layout.nowAngle !== null) {
				const p0 = project(g, layout.nowAngle, Math.PI, -4), p1 = project(g, layout.nowAngle, 0, g.R * 0.2);
				ctx.strokeStyle = colors[NOW_VAR]; ctx.lineWidth = 1.5;
				ctx.setLineDash([5, 4]);
				ctx.beginPath(); ctx.moveTo(p0.x, p0.y); ctx.lineTo(p1.x, p1.y); ctx.stroke();
				ctx.setLineDash([]);
				ctx.fillStyle = colors[NOW_VAR];
				ctx.globalAlpha = on ? 0.3 + 0.35 * (0.5 + 0.5 * Math.sin(t * 4)) : 0.5;
				ctx.beginPath(); ctx.arc(p1.x, p1.y, 7, 0, TAU); ctx.fill();
				ctx.globalAlpha = 1; ctx.beginPath(); ctx.arc(p1.x, p1.y, 3, 0, TAU); ctx.fill();
			}
			// el elegido, el del mouse y el del teclado: su estela marcada y un aro en su partícula
			for (const [it, wdt] of [[sel, 1.5], [hover, 2], [focusIdx >= 0 ? layout.items[focusIdx] : null, 2]]) {
				if (!it) continue;
				const v = vOf(it.ring, n, spin), head = headOf(g, layout, it, spin);
				ctx.strokeStyle = colorOf(it.state); ctx.lineWidth = 3; ctx.globalAlpha = 0.95; ctx.lineCap = "round";
				trailPath(it.a0, it.a1, v); ctx.stroke();
				ctx.globalAlpha = 1; ctx.strokeStyle = colors["--color-text"]; ctx.lineWidth = wdt;
				ctx.beginPath(); ctx.arc(head.x, head.y, sizeOf(g, it, head.depth) + 5, 0, TAU); ctx.stroke();
			}
			// el clic recién hecho: un aro que se abre y se desvanece en el punto elegido
			if (ack) {
				const k = (performance.now() - ack.t0) / ACK_MS, it = layout.items.find((x) => x.id === ack.id);
				if (k >= 1 || !it) ack = null;
				else {
					const head = headOf(g, layout, it, spin), r0 = sizeOf(g, it, head.depth) + 5;
					ctx.strokeStyle = colorOf(it.state); ctx.lineWidth = 2.5; ctx.globalAlpha = 1 - k;
					ctx.beginPath(); ctx.arc(head.x, head.y, r0 + (reduce.matches ? 4 : 14 * k), 0, TAU); ctx.stroke();
					ctx.globalAlpha = 1;
				}
			}
			// pulsos: una señal real que llegó, desde su canal (a la izquierda) hasta su test
			for (let i = pulses.length - 1; i >= 0; i--) {
				const p = pulses[i], k = Math.min(1, (performance.now() - p.t0) / 900);
				if (k >= 1) { pulses.splice(i, 1); continue; }
				const it = layout.items.find((x) => x.id === p.id), to = it ? headOf(g, layout, it, spin) : { x: g.cx, y: g.cy };
				// desde el tramo libre del borde izquierdo, entre el recuadro de arriba (ingesta) y el de abajo (anomalía):
				// salir de detrás de un recuadro se veía como algo tapado
				const from = { x: -12, y: h * (0.38 + 0.07 * Math.max(0, ["test", "fail", "net", "step"].indexOf(p.channel))) };
				ctx.fillStyle = colors[CHANNEL_VAR[p.channel]] || primary;
				for (let j = 0; j < 7; j++) {
					const kk = Math.max(0, (1 - Math.pow(1 - k, 3)) - j * 0.03);
					ctx.globalAlpha = (1 - k) * (1 - j / 7) * 0.85;
					ctx.beginPath(); ctx.arc(from.x + (to.x - from.x) * kk, from.y + (to.y - from.y) * kk - Math.sin(Math.PI * kk) * 30, 3.2 - j * 0.35, 0, TAU); ctx.fill();
				}
			}
			ctx.globalAlpha = 1;
			canvas.dataset.spin = spin.toFixed(5);
			canvas.dataset.view = `${view.yaw.toFixed(3)},${view.pitch.toFixed(3)},${view.zoom.toFixed(3)}`; // para leerlo (y probarlo) desde afuera
		}

		function loop(ts) {
			raf = 0;
			const moving = live() && visible && !document.hidden && wrap.isConnected;
			if (ts - lastFrame >= 1000 / FPS) {
				// el tubo gira solo si nadie lo está mirando de cerca: con el mouse o el foco encima, quieto
				if (moving && !inside && focusIdx < 0) spin = (spin + Math.min(0.1, (ts - (lastSpin || ts)) / 1000) * SPIN) % TAU;
				lastSpin = ts;
				t = ts / 1000; lastFrame = ts;
				draw();
			}
			if (moving || pulses.length || ack) raf = requestAnimationFrame(loop);
		}
		function kick() { if (!raf) raf = requestAnimationFrame(loop); }

		function showTip(it, x, y) {
			if (!it) { tip.hidden = true; return; }
			tip.innerHTML = "";
			const c = labels.card?.(it);
			if (c) cardInto(tip, c, it.name);
			else {
				tip.className = "lf-tip";
				const b = document.createElement("b"); b.textContent = it.name;
				const s = document.createElement("span"); s.textContent = labels.tip(it);
				tip.append(b, s);
			}
			tip.hidden = false;
			const tw = tip.offsetWidth, th = tip.offsetHeight;
			tip.style.left = Math.max(4, Math.min(w - tw - 4, x + 14)) + "px";
			tip.style.top = Math.max(4, Math.min(h - th - 4, y + 14)) + "px";
		}

		// ---------- la vista se manipula: girar, inclinar, mover, acercar, expandir ----------
		function setView(v, animate = false) {
			const to = clampView({ ...view, ...v });
			cancelAnimationFrame(tween);
			if (!animate || reduce.matches) { view = to; g = geometry(w, h, view); draw(); return; }
			const from = { ...view }, t0 = performance.now();
			// el giro, por el camino corto
			let dy = to.yaw - from.yaw;
			if (dy > Math.PI) dy -= TAU; else if (dy < -Math.PI) dy += TAU;
			const step = (now) => {
				const k = Math.min(1, (now - t0) / 320), e = 1 - Math.pow(1 - k, 3);
				view = clampView({ yaw: from.yaw + dy * e, pitch: from.pitch + (to.pitch - from.pitch) * e, zoom: from.zoom + (to.zoom - from.zoom) * e,
					panX: from.panX + (to.panX - from.panX) * e, panY: from.panY + (to.panY - from.panY) * e });
				g = geometry(w, h, view); draw();
				if (k < 1) tween = requestAnimationFrame(step);
			};
			tween = requestAnimationFrame(step);
		}
		/** Acercar o alejar dejando quieto lo que está bajo (x, y). */
		function zoomAt(f, x = w / 2, y = h / 2) {
			const z = Math.min(4, Math.max(0.5, view.zoom * f)), ratio = z / view.zoom;
			setView({ zoom: z, panX: view.panX + (x - g.cx) * (1 - ratio), panY: view.panY + (y - g.cy) * (1 - ratio) });
		}
		function reset() { setView({ ...VIEW0 }, true); }
		function setExpanded(on) {
			expanded = !!on;
			wrap.classList.toggle("lf-full", expanded);
			document.body.classList.toggle("lf-full-open", expanded);
			head.hidden = !expanded;
			const b = bar.querySelector('[data-lf="full"]');
			b.querySelector("span").textContent = expanded ? t_("Salir") : t_("Pantalla completa");
			b.setAttribute("aria-label", b.querySelector("span").textContent);
			if (expanded) document.body.appendChild(wrap);
			else if (slotEl && slotEl.isConnected) slotEl.appendChild(wrap);
			else wrap.remove();
			canvas.focus({ preventScroll: true });
			requestAnimationFrame(resize);
		}
		function setPaused(on, n = 0) {
			paused = !!on;
			const b = bar.querySelector('[data-lf="pause"]');
			b.classList.toggle("on", paused);
			b.setAttribute("aria-pressed", String(paused));
			b.querySelector("use").setAttribute("href", paused ? "#i-play" : "#i-pause");
			const label = paused ? (n ? t_("Reanudar (+{n})").replace("{n}", n) : t_("Reanudar")) : t_("Pausar vista");
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
			else if (k === "reset") reset();
			else if (k === "full") setExpanded(!expanded);
		});

		canvas.addEventListener("pointerdown", (e) => {
			if (e.button !== 0) return;
			drag = { x: e.clientX, y: e.clientY, v: { ...view }, pan: e.shiftKey, id: e.pointerId };
			dragged = false;
		});
		canvas.addEventListener("pointerenter", () => { inside = true; });
		canvas.addEventListener("pointermove", (e) => {
			inside = true;
			const r = canvas.getBoundingClientRect(), x = e.clientX - r.left, y = e.clientY - r.top;
			if (drag && (e.buttons & 1)) {
				const dx = e.clientX - drag.x, dy = e.clientY - drag.y;
				if (!dragged && Math.hypot(dx, dy) < 4) return;
				if (!dragged) { dragged = true; canvas.setPointerCapture?.(drag.id); wrap.classList.add("lf-dragging"); tip.hidden = true; hover = null; }
				// arrastrar gira (a los lados) e inclina (arriba/abajo); con Shift, mueve
				if (drag.pan) setView({ panX: drag.v.panX + dx, panY: drag.v.panY + dy });
				else setView({ yaw: drag.v.yaw + dx * 0.008, pitch: drag.v.pitch - dy * 0.004 });
				return;
			}
			const it = hitTest(layout, g, x, y, spin, match);
			if (it !== hover) {
				hover = it; canvas.style.cursor = it ? "pointer" : "grab"; draw();
				// quedarse un momento sobre un test pide su detalle: al hacer clic ya está
				clearTimeout(hoverTimer);
				if (it) hoverTimer = setTimeout(() => opts.onHover?.(it.id), 150);
			}
			showTip(it, x, y);
		});
		const endDrag = () => { drag = null; wrap.classList.remove("lf-dragging"); };
		canvas.addEventListener("pointerup", endDrag);
		canvas.addEventListener("pointercancel", endDrag);
		canvas.addEventListener("pointerleave", () => { inside = false; hover = null; tip.hidden = true; draw(); kick(); });
		canvas.addEventListener("click", (e) => {
			if (dragged) { dragged = false; return; } // fue un arrastre, no un clic
			const r = canvas.getBoundingClientRect(), it = hitTest(layout, g, e.clientX - r.left, e.clientY - r.top, spin, match);
			if (!it) return;
			// Ctrl/⌘ + clic abre el test, como un enlace
			if (e.ctrlKey || e.metaKey) { if (expanded) setExpanded(false); opts.onOpen?.(it.id, it.run_id); return; }
			// el doble clic abre solo el test que ya estaba elegido antes de empezar: dos clics seguidos en un
			// punto nuevo (por ejemplo, porque el detalle tardaba) lo eligen, no sacan de En vivo
			if (e.detail <= 1) openable = !!sel && sel.id === it.id;
			sel = it; ack = { id: it.id, t0: performance.now() };
			canvas.dataset.selected = String(it.id);
			draw(); kick();
			if (e.detail <= 1) opts.onPick?.(it.id, it.run_id);
		});
		canvas.addEventListener("dblclick", (e) => {
			const r = canvas.getBoundingClientRect(), it = hitTest(layout, g, e.clientX - r.left, e.clientY - r.top, spin, match);
			if (!it) { reset(); return; }
			if (openable && sel && sel.id === it.id) { if (expanded) setExpanded(false); opts.onOpen?.(it.id, it.run_id); }
		});
		// la rueda acerca en pantalla completa (o con Ctrl/⌘); en la vista normal sigue desplazando la página
		canvas.addEventListener("wheel", (e) => {
			if (!expanded && !e.ctrlKey && !e.metaKey) return;
			e.preventDefault();
			const r = canvas.getBoundingClientRect();
			zoomAt(e.deltaY < 0 ? 1.12 : 1 / 1.12, e.clientX - r.left, e.clientY - r.top);
		}, { passive: false });
		canvas.addEventListener("keydown", (e) => {
			if (e.key === " ") { e.preventDefault(); opts.onPause?.(); return; }
			if (e.key === "+" || e.key === "=") { e.preventDefault(); zoomAt(1.25); return; }
			if (e.key === "-" || e.key === "_") { e.preventDefault(); zoomAt(1 / 1.25); return; }
			if (e.key === "0") { e.preventDefault(); reset(); return; }
			if (e.key === "f" || e.key === "F") { e.preventDefault(); setExpanded(!expanded); return; }
			if (e.key === "Escape" && expanded) { e.preventDefault(); e.stopPropagation(); setExpanded(false); return; }
			if (e.shiftKey && e.key.startsWith("Arrow")) { // Shift + flechas: girar e inclinar
				e.preventDefault();
				const d = { ArrowLeft: [-0.2, 0], ArrowRight: [0.2, 0], ArrowUp: [0, 0.06], ArrowDown: [0, -0.06] }[e.key];
				setView({ yaw: view.yaw + d[0], pitch: view.pitch + d[1] });
				return;
			}
			// las flechas recorren solo lo que coincide con el filtro (lo apagado no se elige)
			const ok = layout.items.map((x, i) => (!match || match.has(x.id) ? i : -1)).filter((i) => i >= 0);
			if (!ok.length) return;
			const at = ok.indexOf(focusIdx);
			if (e.key === "ArrowRight" || e.key === "ArrowDown") focusIdx = ok[at < 0 ? 0 : Math.min(ok.length - 1, at + 1)];
			else if (e.key === "ArrowLeft" || e.key === "ArrowUp") focusIdx = ok[at < 0 ? 0 : Math.max(0, at - 1)];
			else if (e.key === "Home") focusIdx = ok[0];
			else if (e.key === "End") focusIdx = ok[ok.length - 1];
			else if (e.key === "Enter" && focusIdx >= 0 && ok.includes(focusIdx)) {
				const it = layout.items[focusIdx];
				if (e.ctrlKey || e.metaKey) { if (expanded) setExpanded(false); opts.onOpen?.(it.id, it.run_id); } else opts.onPick?.(it.id, it.run_id);
				return;
			}
			else return;
			e.preventDefault();
			const it = layout.items[focusIdx], p = headOf(g, layout, it, spin);
			showTip(it, p.x, p.y);
			canvas.setAttribute("aria-label", `${labels.aria} ${it.name}: ${labels.tip(it)}`);
			draw();
		});
		canvas.addEventListener("blur", () => { focusIdx = -1; tip.hidden = true; draw(); kick(); });
		// Esc fuera del canvas también sale de la pantalla completa
		document.addEventListener("keydown", (e) => { if (e.key === "Escape" && expanded) setExpanded(false); }, true);

		return {
			el: wrap,
			/** Pone el campo en el lugar que dejó el HTML de la vista (o lo saca si ya no está). Expandido, se queda arriba de todo. */
			attach(slot) {
				slotEl = slot || null;
				let moved = false;
				if (!expanded) {
					if (!slot) { if (wrap.isConnected) wrap.remove(); return; }
					if (wrap.parentElement !== slot) { slot.appendChild(wrap); moved = true; }
				}
				// solo si se movió (o el tema cambió): redimensionar el canvas lo borra y lo vuelve a crear
				const theme = document.body.dataset.theme || "";
				if (moved || theme !== lastTheme || !w) { lastTheme = theme; readColors(); resize(); }
				kick();
			},
			/**
			 * data: {exec, now, live (TraceReportsLive), match (Set de ids o null), selected, aria, tip(it),
			 * head (texto de la cabecera expandida), paused, pausedNew (eventos llegados en pausa)}
			 */
			update(data) {
				layout = fieldLayout(data.exec, data.now, data.live);
				match = data.match || null;
				sel = layout.items.find((x) => x.id === data.selected) || null;
				canvas.dataset.selected = sel ? String(sel.id) : "";
				if (data.tip) labels.tip = data.tip;
				if (data.card) labels.card = data.card;
				labels.aria = data.aria || "";
				canvas.setAttribute("aria-label", labels.aria);
				if (data.head !== undefined) head.textContent = data.head;
				setPaused(!!data.paused, data.pausedNew || 0);
				// un test que cambió de estado (o apareció) lanza su pulso, aunque el stream no lo haya traído
				for (const it of layout.items) {
					const prev = prevStates.get(it.id);
					if (prevStates.size && prev !== it.state && pulses.length < 24) pulses.push({ id: it.id, channel: it.state === "FAIL" || it.state === "no_signal" ? "fail" : "test", t0: performance.now() });
					prevStates.set(it.id, it.state);
				}
				if (hover) hover = layout.items.find((x) => x.id === hover.id) || null;
				kick(); draw();
			},
			/** Los eventos que llegaron en pausa: el botón lo dice sin redibujar la vista. */
			setPausedCount(n) { if (paused) setPaused(true, n); },
			/** Una señal del stream: pulso desde su canal hasta el test (o al centro). */
			signal(channel, testId) {
				if (!channel || pulses.length >= 24 || !live()) return;
				pulses.push({ id: testId || 0, channel, t0: performance.now() });
				kick();
			},
			setMotion(on) { motion = !!on; kick(); draw(); },
			setView, reset, zoomAt, setExpanded,
			get view() { return { ...view }; },
			get expanded() { return expanded; },
			get layout() { return layout; },
			get geometry() { return g; },
			get spin() { return spin; },
		};
	}

	window.TraceReportsLiveField = { HIT_PX, NOW_VAR, cardInto, fieldLayout, ingestStats, channelOf, geometry, clampView, project, vOf, headOf, hitTest, create, SEAM, TILT, SPIKES, VIEW0 };
})();
