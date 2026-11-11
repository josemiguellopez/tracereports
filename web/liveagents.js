/*
 * En vivo, vista "Agentes": la ejecución por dentro de un chip. Cada worker (o proceso, si el cliente no lo
 * informa) es un agente en órbita alrededor del núcleo: toma tests de la cola, trabaja y deja su estela; al
 * terminar uno, un bit de su color vuelve al núcleo y llena su anillo. El fondo son las pistas del circuito
 * con bits viajando, una lluvia tenue de 0 y 1 y el barrido del núcleo: se ve trabajar por dentro.
 *
 * Responde de un vistazo: qué worker está trabado (palpita en rojo, quieto), si el trabajo se reparte (uno
 * con estela larga, otros apagados), cuánto falta (la cola) y quién falla (los puntos rojos de su estela).
 * Todo lo que se dibuja es dato; el movimiento solo acompaña (sin él, con "reducir movimiento", se lee igual).
 *
 * Mismo contrato que livefield.js: create(opts) → {attach(el), update(data), setMotion(on)}.
 * opts: {t, onPick(testId), onOpen(testId, runId), onWorker(key), onHover(testId)}.
 */
(function () {
	"use strict";

	const TAU = Math.PI * 2;
	const FPS = 30;
	const TRAIL = 28; // puntos de la estela por agente (los últimos terminados)
	const CLS = { PASS: "pass", FAIL: "fail", INTERRUPTED: "fail", no_signal: "nosig", WARNING: "warning", turtle: "turtle", running: "running", SKIP: "skip" };
	const VAR = { pass: "--color-success", fail: "--color-danger", nosig: "--color-danger", warning: "--color-warning", turtle: "--color-warning",
		running: "--color-info", skip: "--color-text-muted", idle: "--color-text-muted" };
	const isFail = (s) => s === "FAIL" || s === "INTERRUPTED";

	/**
	 * Lo que se dibuja, a partir de los datos de En vivo: un agente por carril (worker o proceso) con su test
	 * en curso, su estado (corriendo, tortuga, sin señal o desocupado) y su estela; la cola y los resultados.
	 */
	function agentsModel(exec, now, LIVE) {
		const lanes = LIVE.lanesOf(exec, now);
		const tests = LIVE.allTests(exec);
		const agents = lanes.map((l) => {
			const ts = [...l.tests].sort((a, b) => a.started_at - b.started_at || a.id - b.id);
			// con más de un test abierto en el mismo worker, manda el más grave: sin señal, después tortuga
			const open = ts.filter((x) => !x.ended_at);
			const current = open.find((x) => x.state === "no_signal") || open.find((x) => x.state === "turtle") || open[open.length - 1] || null;
			const done = ts.filter((x) => x.ended_at);
			const state = !current ? "idle" : current.state === "no_signal" || current.state === "turtle" ? current.state : "running";
			return { key: l.key, label: l.label, sub: l.sub || "", current, state, done: done.length, fails: done.filter((x) => isFail(x.state)).length, trail: done.slice(-TRAIL) };
		});
		const tally = { pass: 0, fail: 0, other: 0 };
		for (const x of tests) if (x.ended_at) tally[x.state === "PASS" ? "pass" : isFail(x.state) ? "fail" : "other"]++;
		const planned = exec.planned || 0, started = tests.length;
		return { agents, tally, started, done: tally.pass + tally.fail + tally.other, total: Math.max(planned, started),
			queue: planned ? Math.max(0, planned - started) : null, running: !!exec.running };
	}

	/** Dónde va cada cosa: el núcleo al centro, los agentes en órbita (dos anillos si son muchos) y sus estelas. */
	function agentsLayout(model, w, h) {
		const cx = w / 2, cy = h / 2, coreR = Math.max(34, Math.min(w, h) * 0.13);
		const n = model.agents.length, rings = n > 24 ? 2 : 1;
		const r = n > 40 ? 9 : n > 20 ? 11 : 14;
		const trailLen = Math.max(24, Math.min(w, h) * (rings > 1 ? 0.1 : 0.15));
		const agents = model.agents.map((a, i) => {
			const ring = rings > 1 ? i % 2 : 0;
			const k = rings > 1 ? Math.floor(i / 2) : i, per = rings > 1 ? Math.ceil(n / 2) : n;
			const ang = (k / Math.max(1, per)) * TAU - Math.PI / 2 + (ring ? Math.PI / Math.max(1, per) : 0);
			// la órbita deja libres las esquinas (ahí van la ingesta, la anomalía y los carriles)
			const rx = w * (ring ? 0.21 : 0.31), ry = h * (ring ? 0.19 : 0.28);
			const x = cx + Math.cos(ang) * rx, y = cy + Math.sin(ang) * ry;
			const dx = Math.cos(ang), dy = Math.sin(ang);
			const step = Math.min(7, trailLen / TRAIL);
			// la estela sale hacia afuera, con una leve curva; el más nuevo, junto al agente
			const dots = [...a.trail].reverse().map((t, j) => {
				const d = r + 8 + j * step, bend = j * 0.012;
				return { t, x: x + (dx * Math.cos(bend) - dy * Math.sin(bend)) * d, y: y + (dy * Math.cos(bend) + dx * Math.sin(bend)) * d };
			});
			// la pista del núcleo al agente: sale del borde del núcleo y dobla en ángulo recto (como en un circuito)
			const sx = cx + dx * coreR, sy = cy + dy * coreR;
			const path = Math.abs(dx) > Math.abs(dy) ? [[sx, sy], [x, sy], [x, y]] : [[sx, sy], [sx, y], [x, y]];
			return { ...a, i, x, y, r, ang, dots, path };
		});
		return { cx, cy, coreR, agents };
	}

	/** El punto a la fracción s (0..1) de un camino de segmentos. */
	function pointAt(path, s) {
		let len = 0;
		const segs = [];
		for (let i = 1; i < path.length; i++) { const l = Math.hypot(path[i][0] - path[i - 1][0], path[i][1] - path[i - 1][1]); segs.push(l); len += l; }
		let d = Math.max(0, Math.min(1, s)) * len;
		for (let i = 0; i < segs.length; i++) {
			if (d <= segs[i] || i === segs.length - 1) {
				const k = segs[i] ? d / segs[i] : 0;
				return [path[i][0] + (path[i + 1][0] - path[i][0]) * k, path[i][1] + (path[i + 1][1] - path[i][1]) * k];
			}
			d -= segs[i];
		}
		return path[path.length - 1];
	}

	/** Qué hay bajo el puntero: un agente (para filtrar por él) o un test de su estela (para abrirlo). */
	function hitTest(lay, x, y) {
		if (!lay) return null;
		for (const a of lay.agents) if (Math.hypot(a.x - x, a.y - y) <= a.r + 7) return { kind: "agent", agent: a };
		let best = null, bd = 8;
		for (const a of lay.agents) for (const d of a.dots) { const dd = Math.hypot(d.x - x, d.y - y); if (dd < bd) { bd = dd; best = { kind: "test", test: d.t, agent: a }; } }
		return best;
	}

	/** Un generador de números pseudoaleatorios con semilla: el circuito del fondo es siempre el mismo. */
	function rng(seed) {
		return () => { seed |= 0; seed = (seed + 0x6d2b79f5) | 0; let t = Math.imul(seed ^ (seed >>> 15), 1 | seed); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
	}
	/** Las pistas del circuito del fondo: tramos en ángulo recto sobre una grilla, con sus vías en los extremos. */
	function circuit(w, h) {
		const rand = rng(Math.round(w) * 31 + Math.round(h)), G = 24, out = [];
		const n = Math.round((w * h) / 9000);
		for (let i = 0; i < n; i++) {
			let x = Math.round((rand() * w) / G) * G, y = Math.round((rand() * h) / G) * G;
			const pts = [[x, y]];
			let horiz = rand() < 0.5;
			for (let s = 0, steps = 2 + Math.floor(rand() * 4); s < steps; s++) {
				const len = (1 + Math.floor(rand() * 4)) * G * (rand() < 0.5 ? -1 : 1);
				if (horiz) x += len; else y += len;
				pts.push([x, y]);
				horiz = !horiz;
			}
			out.push({ pts, speed: 0.05 + rand() * 0.12, phase: rand() });
		}
		return out;
	}

	function create(opts = {}) {
		const t_ = opts.t || ((x) => x);
		const wrap = document.createElement("div");
		wrap.className = "la-wrap";
		const canvas = document.createElement("canvas");
		canvas.className = "la-canvas";
		canvas.tabIndex = 0;
		canvas.setAttribute("role", "img");
		const tip = document.createElement("div");
		tip.className = "lf-tip"; tip.hidden = true; tip.setAttribute("role", "status");
		const topLayer = typeof tip.showPopover === "function";
		if (topLayer) tip.popover = "manual";
		wrap.append(canvas, tip);
		const ctx = canvas.getContext("2d");
		const reduce = matchMedia("(prefers-reduced-motion: reduce)");
		const colors = {};
		let model = null, lay = null, bg = [], w = 0, h = 0, dpr = 1, raf = 0, lastFrame = 0, time = 0;
		let motion = true, visible = true, hover = null, focusIdx = -1, match = null, selected = null, labels = { aria: "", card: null };
		const packets = []; // {agent, t0, dir: "out" | "in", color}
		let prevStarted = null, prevEnded = null;
		const live = () => motion && !reduce.matches;

		const readColors = () => {
			const cs = getComputedStyle(wrap.isConnected ? wrap : document.documentElement);
			for (const v of new Set([...Object.values(VAR), "--color-text", "--color-primary"])) colors[v] = cs.getPropertyValue(v).trim() || "#888";
		};
		const colorOf = (cls) => colors[VAR[cls] || VAR.running] || "#4aa3ff";
		const stateCls = (s) => CLS[s] || "running";

		function resize() {
			const r = wrap.getBoundingClientRect();
			if (!r.width) return;
			w = r.width; h = r.height; dpr = Math.min(window.devicePixelRatio || 1, 2);
			canvas.width = Math.round(w * dpr); canvas.height = Math.round(h * dpr);
			canvas.style.width = w + "px"; canvas.style.height = h + "px";
			bg = circuit(w, h);
			lay = model ? agentsLayout(model, w, h) : null;
			draw();
		}
		const ro = typeof ResizeObserver === "function" ? new ResizeObserver(resize) : null;
		ro?.observe(wrap);
		const io = typeof IntersectionObserver === "function" ? new IntersectionObserver((en) => { visible = en[0].isIntersecting; if (visible) kick(); }) : null;
		io?.observe(wrap);

		/** Dónde está un agente ahora: flota un poco alrededor de su lugar (quieto sin movimiento o sin señal). */
		const posOf = (a) => (!live() || a.state === "no_signal" ? [a.x, a.y] : [a.x + Math.sin(time * 0.7 + a.i * 1.7) * 3, a.y + Math.cos(time * 0.55 + a.i * 2.3) * 3]);

		function drawBackground() {
			// el interior del chip: oscuro, con un brillo al centro
			const grd = ctx.createRadialGradient(w / 2, h / 2, 10, w / 2, h / 2, Math.max(w, h) * 0.75);
			grd.addColorStop(0, "#0d1b33"); grd.addColorStop(1, "#04070e");
			ctx.fillStyle = grd; ctx.fillRect(0, 0, w, h);
			// lluvia de bits, muy tenue
			ctx.font = "10px ui-monospace, monospace";
			ctx.fillStyle = "rgba(90, 200, 255, 0.07)";
			for (let x = 8, c = 0; x < w; x += 22, c++) {
				const speed = 14 + ((c * 37) % 23), off = (c * 97) % h;
				for (let k = 0; k < 6; k++) {
					const y = ((off + time * speed + k * 14) % (h + 80)) - 40;
					ctx.fillText(((c * 7 + k * 3 + Math.floor(time * 2)) % 3) ? "1" : "0", x, y);
				}
			}
			// pistas del circuito con sus vías y un bit que corre por cada una
			ctx.lineWidth = 1;
			ctx.strokeStyle = "rgba(80, 160, 255, 0.13)";
			for (const tr of bg) {
				ctx.beginPath();
				tr.pts.forEach(([x, y], i) => (i ? ctx.lineTo(x, y) : ctx.moveTo(x, y)));
				ctx.stroke();
			}
			ctx.fillStyle = "rgba(80, 160, 255, 0.28)";
			for (const tr of bg) for (const p of [tr.pts[0], tr.pts[tr.pts.length - 1]]) { ctx.beginPath(); ctx.arc(p[0], p[1], 1.8, 0, TAU); ctx.fill(); }
			if (live()) {
				ctx.fillStyle = "rgba(120, 220, 255, 0.75)";
				for (const tr of bg) {
					const [x, y] = pointAt(tr.pts, (tr.phase + time * tr.speed) % 1);
					ctx.fillRect(x - 1.5, y - 1.5, 3, 3);
				}
			}
			// el barrido del núcleo, como un radar
			if (live() && lay && typeof ctx.createConicGradient === "function") {
				const cg = ctx.createConicGradient((time * 0.6) % TAU, lay.cx, lay.cy);
				cg.addColorStop(0, "rgba(90, 200, 255, 0.10)"); cg.addColorStop(0.08, "rgba(90, 200, 255, 0)"); cg.addColorStop(1, "rgba(90, 200, 255, 0)");
				ctx.fillStyle = cg; ctx.fillRect(0, 0, w, h);
			}
		}

		function drawCore() {
			const { cx, cy, coreR } = lay, m = model;
			// el chip: patas en los cuatro lados
			ctx.strokeStyle = "rgba(120, 200, 255, 0.35)"; ctx.lineWidth = 2;
			for (let s = 0; s < 4; s++) for (let k = -3; k <= 3; k++) {
				const o = (k / 3.6) * coreR, a = coreR * 0.92, b = coreR * 1.12;
				const [x0, y0, x1, y1] = s === 0 ? [cx + o, cy - a, cx + o, cy - b] : s === 1 ? [cx + a, cy + o, cx + b, cy + o] : s === 2 ? [cx + o, cy + a, cx + o, cy + b] : [cx - a, cy + o, cx - b, cy + o];
				ctx.beginPath(); ctx.moveTo(x0, y0); ctx.lineTo(x1, y1); ctx.stroke();
			}
			const s = coreR * 0.92;
			ctx.fillStyle = "rgba(9, 18, 36, 0.92)"; ctx.strokeStyle = "rgba(120, 200, 255, 0.55)"; ctx.lineWidth = 1.5;
			ctx.beginPath(); ctx.roundRect ? ctx.roundRect(cx - s, cy - s, s * 2, s * 2, 10) : ctx.rect(cx - s, cy - s, s * 2, s * 2); ctx.fill(); ctx.stroke();
			// el anillo: lo terminado, verde lo que pasó y rojo lo que falló
			const R = coreR * 0.72, total = Math.max(1, m.total);
			ctx.lineWidth = 5; ctx.strokeStyle = "rgba(255, 255, 255, 0.08)";
			ctx.beginPath(); ctx.arc(cx, cy, R, 0, TAU); ctx.stroke();
			let a0 = -Math.PI / 2;
			for (const [n, cls] of [[m.tally.pass, "pass"], [m.tally.fail, "fail"], [m.tally.other, "warning"]]) {
				if (!n) continue;
				const a1 = a0 + (n / total) * TAU;
				ctx.strokeStyle = colorOf(cls); ctx.beginPath(); ctx.arc(cx, cy, R, a0, a1); ctx.stroke();
				a0 = a1;
			}
			// al centro: la cola (lo que falta empezar) o, sin plan, lo terminado
			ctx.textAlign = "center"; ctx.fillStyle = "#e8f4ff";
			ctx.font = `700 ${Math.round(coreR * 0.42)}px ui-monospace, monospace`;
			ctx.fillText(String(m.queue ?? m.done), cx, cy + coreR * 0.1);
			ctx.font = `600 ${Math.max(9, Math.round(coreR * 0.14))}px ui-monospace, monospace`;
			ctx.fillStyle = "rgba(160, 210, 255, 0.85)";
			ctx.fillText(m.queue !== null ? t_("EN COLA") : t_("TERMINADOS"), cx, cy - coreR * 0.32);
			ctx.fillText(`${m.done}/${m.total}`, cx, cy + coreR * 0.42);
		}

		function drawAgents() {
			const now = performance.now();
			for (const a of lay.agents) {
				const [x, y] = posOf(a);
				const dim = match && !(a.current && match.has(a.current.id)) && !a.dots.some((d) => match.has(d.t.id));
				ctx.globalAlpha = dim ? 0.25 : 1;
				// la pista del núcleo: más viva si está trabajando; un paquete la recorre hacia el agente
				const busy = a.state !== "idle";
				ctx.strokeStyle = busy ? "rgba(90, 200, 255, 0.35)" : "rgba(90, 200, 255, 0.10)"; ctx.lineWidth = 1;
				ctx.beginPath(); a.path.forEach(([px, py], i) => (i ? ctx.lineTo(px, py) : ctx.moveTo(px, py))); ctx.stroke();
				if (busy && live() && a.state !== "no_signal") {
					const [px, py] = pointAt(a.path, (time * 0.35 + a.i * 0.17) % 1);
					ctx.fillStyle = "rgba(140, 230, 255, 0.95)"; ctx.fillRect(px - 2, py - 2, 4, 4);
				}
				// la estela: lo que ya terminó (el más nuevo, junto al agente)
				for (const d of a.dots) {
					const cls = stateCls(d.t.state), sel = d.t.id === selected;
					ctx.fillStyle = colorOf(cls);
					ctx.globalAlpha = (dim ? 0.25 : 1) * (match && !match.has(d.t.id) ? 0.3 : 0.9);
					ctx.beginPath(); ctx.arc(d.x, d.y, sel ? 4.5 : isFail(d.t.state) ? 3.2 : 2.4, 0, TAU); ctx.fill();
					if (sel) { ctx.strokeStyle = "#fff"; ctx.lineWidth = 1.5; ctx.stroke(); }
				}
				ctx.globalAlpha = dim ? 0.25 : 1;
				// el agente: un chip con su luz del estado
				const cls = a.state === "idle" ? "idle" : stateCls(a.state);
				const col = colorOf(cls);
				if (a.state === "no_signal" || a.state === "turtle") { // palpita: sin señal en rojo, lento en ámbar
					const k = live() ? ((now / (a.state === "no_signal" ? 900 : 1500)) % 1) : 0.5;
					ctx.strokeStyle = col; ctx.globalAlpha = (dim ? 0.25 : 1) * (1 - k); ctx.lineWidth = 2;
					ctx.beginPath(); ctx.arc(x, y, a.r + 4 + k * 16, 0, TAU); ctx.stroke();
					ctx.globalAlpha = dim ? 0.25 : 1;
				}
				ctx.fillStyle = "rgba(8, 16, 32, 0.95)"; ctx.strokeStyle = a.state === "idle" ? "rgba(140, 160, 190, 0.45)" : col; ctx.lineWidth = hover?.agent?.key === a.key || focusIdx === a.i ? 3 : 1.6;
				ctx.beginPath(); ctx.roundRect ? ctx.roundRect(x - a.r, y - a.r, a.r * 2, a.r * 2, 4) : ctx.rect(x - a.r, y - a.r, a.r * 2, a.r * 2); ctx.fill(); ctx.stroke();
				ctx.fillStyle = col; ctx.globalAlpha = (dim ? 0.25 : 1) * (a.state === "idle" ? 0.35 : 1);
				if (a.state !== "idle") { ctx.shadowColor = col; ctx.shadowBlur = 12; } // la luz brilla mientras trabaja
				ctx.beginPath(); ctx.arc(x, y, a.r * 0.35, 0, TAU); ctx.fill();
				ctx.shadowBlur = 0;
				if (a.state === "running" && live()) { // trabajando: un arco gira alrededor
					const sp = (now / 600 + a.i) % TAU;
					ctx.strokeStyle = col; ctx.lineWidth = 2; ctx.beginPath(); ctx.arc(x, y, a.r + 4, sp, sp + 1.6); ctx.stroke();
				}
				ctx.globalAlpha = dim ? 0.25 : 1;
				// su nombre y, si hay lugar, lo que está corriendo
				ctx.textAlign = "center"; ctx.fillStyle = "#cfe6ff";
				ctx.font = `700 ${a.r > 11 ? 11 : 10}px ui-monospace, monospace`;
				ctx.fillText(a.label, x, y - a.r - 6);
				// lo que está corriendo: con pocos agentes, siempre; con muchos, el que está bajo el puntero o el foco
				if (a.current && (lay.agents.length <= 10 || hover?.agent?.key === a.key || focusIdx === a.i)) {
					ctx.font = "10px ui-sans-serif, system-ui, sans-serif"; ctx.fillStyle = "rgba(170, 200, 230, 0.8)";
					const name = a.current.name.length > 22 ? a.current.name.slice(0, 21) + "…" : a.current.name;
					ctx.fillText(name, x, y + a.r + 13);
				}
				ctx.globalAlpha = 1;
			}
		}

		/** Los paquetes que van del núcleo a un agente (empezó un test) y los bits que vuelven (terminó). */
		function drawPackets() {
			const now = performance.now();
			for (let i = packets.length - 1; i >= 0; i--) {
				const p = packets[i], a = lay.agents.find((x) => x.key === p.agent);
				const k = (now - p.t0) / 900;
				if (!a || k >= 1) { packets.splice(i, 1); continue; }
				const [x, y] = pointAt(p.dir === "out" ? a.path : [...a.path].reverse(), k);
				ctx.fillStyle = p.color; ctx.shadowColor = p.color; ctx.shadowBlur = 10;
				ctx.beginPath(); ctx.arc(x, y, 3.2, 0, TAU); ctx.fill();
				ctx.shadowBlur = 0;
			}
		}

		function draw() {
			if (!w || !ctx) return;
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			ctx.clearRect(0, 0, w, h);
			drawBackground();
			if (!lay) return;
			drawAgents();
			drawPackets();
			drawCore();
		}

		function loop(ts) {
			raf = 0;
			const moving = live() && visible && !document.hidden && wrap.isConnected;
			if (ts - lastFrame >= 1000 / FPS) { time = ts / 1000; lastFrame = ts; draw(); }
			if (moving || packets.length) raf = requestAnimationFrame(loop);
		}
		function kick() { if (!raf) raf = requestAnimationFrame(loop); }

		// ---------- la tarjeta del puntero ----------
		function hideTip() {
			tip.hidden = true;
			if (topLayer && tip.matches(":popover-open")) tip.hidePopover();
		}
		function showTip(hit, x, y) {
			if (!hit) return hideTip();
			tip.innerHTML = "";
			const F = window.TraceReportsLiveField;
			if (hit.kind === "test" && labels.card && F) F.cardInto(tip, labels.card(hit.test), hit.test.name);
			else if (hit.kind === "agent" && F) {
				const a = hit.agent, cur = a.current;
				F.cardInto(tip, { cls: a.state === "idle" ? "skip" : stateCls(a.state), icon: a.state === "idle" ? "i-pause" : a.state === "no_signal" ? "i-st-nosig" : "i-st-running",
					label: t_(a.state === "idle" ? "Desocupado" : a.state === "no_signal" ? "Sin señal" : a.state === "turtle" ? "Tortuga" : "Corriendo"),
					dur: "", times: cur ? cur.name : t_("Esperando un test"),
					where: `${t_("{n} terminados", { n: a.done })}${a.fails ? ` · ${t_("{n} fallaron", { n: a.fails })}` : ""}${a.sub ? ` · ${a.sub}` : ""}`,
					hint: t_("Clic: ver en la tabla solo los tests de este worker") }, a.label);
			} else return hideTip();
			tip.hidden = false;
			const r = canvas.getBoundingClientRect();
			if (topLayer && !tip.matches(":popover-open")) tip.showPopover();
			const tw = tip.offsetWidth, th = tip.offsetHeight, vx = (topLayer ? r.left : 0) + x, vy = (topLayer ? r.top : 0) + y;
			const W = topLayer ? innerWidth : w, H = topLayer ? innerHeight : h;
			tip.style.left = Math.max(4, Math.min(W - tw - 4, vx + 14 + tw <= W - 4 ? vx + 14 : vx - 14 - tw)) + "px";
			tip.style.top = Math.max(4, Math.min(H - th - 4, vy + 14 + th <= H - 4 ? vy + 14 : vy - 14 - th)) + "px";
		}

		const local = (e) => { const r = canvas.getBoundingClientRect(); return [e.clientX - r.left, e.clientY - r.top]; };
		canvas.addEventListener("pointermove", (e) => {
			const [x, y] = local(e);
			const hit = hitTest(lay, x, y);
			hover = hit;
			canvas.style.cursor = hit ? "pointer" : "default";
			showTip(hit, x, y);
			if (hit?.kind === "test") opts.onHover?.(hit.test.id);
			if (!live()) draw();
		});
		canvas.addEventListener("pointerleave", () => { hover = null; hideTip(); if (!live()) draw(); });
		canvas.addEventListener("click", (e) => {
			const hit = hitTest(lay, ...local(e));
			if (!hit) return;
			if (hit.kind === "agent") return opts.onWorker?.(hit.agent.key);
			if (e.ctrlKey || e.metaKey) return opts.onOpen?.(hit.test.id, hit.test.run_id);
			opts.onPick?.(hit.test.id);
		});
		// con el teclado: flechas entre agentes (con su tarjeta), Enter filtra por el elegido
		canvas.addEventListener("keydown", (e) => {
			if (!lay?.agents.length) return;
			const n = lay.agents.length;
			if (e.key === "ArrowRight" || e.key === "ArrowDown") focusIdx = (focusIdx + 1) % n;
			else if (e.key === "ArrowLeft" || e.key === "ArrowUp") focusIdx = (focusIdx - 1 + n) % n;
			else if (e.key === "Enter" && focusIdx >= 0) { e.preventDefault(); return opts.onWorker?.(lay.agents[focusIdx].key); }
			else return;
			e.preventDefault();
			const a = lay.agents[focusIdx];
			showTip({ kind: "agent", agent: a }, a.x, a.y);
			canvas.setAttribute("aria-label", `${labels.aria} ${a.label}: ${a.current ? a.current.name : t_("Desocupado")}`);
			if (!live()) draw();
		});
		canvas.addEventListener("blur", () => { focusIdx = -1; hideTip(); });

		let lastTheme = null;
		return {
			attach(slot) {
				if (!slot) { if (wrap.isConnected) wrap.remove(); hideTip(); return; }
				let moved = false;
				if (wrap.parentElement !== slot) { slot.appendChild(wrap); moved = true; }
				const theme = document.body.dataset.theme || "";
				if (moved || theme !== lastTheme || !w) { lastTheme = theme; readColors(); resize(); }
				kick();
			},
			/** data: {exec, now, live (TraceReportsLive), match (Set de ids o null), selected, aria, card(it)} */
			update(data) {
				model = agentsModel(data.exec, data.now, data.live);
				lay = w ? agentsLayout(model, w, h) : null;
				match = data.match || null;
				selected = data.selected || null;
				if (data.card) labels.card = data.card;
				labels.aria = data.aria || "";
				canvas.setAttribute("aria-label", labels.aria);
				canvas.dataset.agents = String(model.agents.length);
				// lo que empezó sale del núcleo hacia su agente; lo que terminó vuelve en su color
				const started = new Map(), ended = new Map();
				for (const a of model.agents) {
					if (a.current) started.set(a.current.id, a.key);
					for (const t of a.trail) ended.set(t.id, { key: a.key, state: t.state });
				}
				if (prevStarted && live()) {
					for (const [id, key] of started) if (!prevStarted.has(id) && packets.length < 60) packets.push({ agent: key, t0: performance.now(), dir: "out", color: "#8be9ff" });
					for (const [id, v] of ended) if (!prevEnded.has(id) && packets.length < 60) packets.push({ agent: v.key, t0: performance.now(), dir: "in", color: colorOf(stateCls(v.state)) });
				}
				prevStarted = started; prevEnded = new Set(ended.keys());
				if (hover?.kind === "agent") hover = { kind: "agent", agent: lay?.agents.find((a) => a.key === hover.agent.key) };
				kick(); draw();
			},
			setMotion(on) { motion = !!on; kick(); draw(); },
			get layout() { return lay; },
			get model() { return model; },
		};
	}

	window.TraceReportsLiveAgents = { agentsModel, agentsLayout, pointAt, hitTest, circuit, create, TRAIL };
})();
