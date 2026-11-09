// Página de ingreso de TraceReports. Tres partes:
//  1) el lienzo: reglas y una grilla donde cuatro workers (gw0…gw3) arman una traza en vivo, con una caja
//     de selección sobre el último fallo (decorativo, canvas 2D, ~30 fps, quieto con "reducir movimiento");
//  2) Trace Buddy encima de la tarjeta: sigue lo que escribes, se tapa los ojos con la clave oculta, espía
//     si el puntero va al ojo y se destapa (y la mira) al mostrarla; celebra al entrar, niega si falla;
//  3) el formulario: POST /api/v1/auth/login, recordarme, intentos bloqueados, vuelta a donde estabas.
const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
const $ = (s) => document.querySelector(s);

// ---------- textos ----------
const EN = {
	kicker: "6 AGENTS RUNNING", title: "What failed, why and where.", lede: "Steps, network, DOM and screenshots of every test, in one place.",
	h: "Sign in", sub: "Use the account configured on this server.", user: "User", pass: "Password", show: "Show password", hide: "Hide password",
	caps: "Caps Lock is on", remember: "Remember me", go: "Sign in", policy: "Session in a secure cookie.",
	days: (n) => `(${n} days)`, idle: (m) => `Session in a secure cookie · it expires after ${m} min without use.`,
	wrong: "Wrong user or password.", empty: "Enter your user and password.", locked: (t) => `Too many attempts. Try again in ${t}.`,
	net: "Could not reach the server. Try again.", expired: "Your session expired. Sign in again to continue.", bye: "You signed out.",
	open: "This server has no login: opening the reports…", welcome: "Welcome",
};
const ES = {
	days: (n) => `(${n} días)`, idle: (m) => `Sesión en cookie segura · vence tras ${m} min sin uso.`,
	wrong: "Usuario o clave incorrectos.", empty: "Escribe tu usuario y tu clave.", locked: (t) => `Demasiados intentos. Prueba de nuevo en ${t}.`,
	net: "No se pudo conectar con el servidor. Prueba de nuevo.", expired: "Tu sesión venció. Ingresa de nuevo para seguir.", bye: "Cerraste la sesión.",
	open: "Este servidor no pide login: abriendo los reportes…", show: "Mostrar clave", hide: "Ocultar clave", welcome: "Bienvenido",
};
let lang = "es";
try { const l = localStorage.getItem("tracereports-lang"); lang = l === "en" || (!l && !/^es/i.test(navigator.language)) ? "en" : "es"; } catch { /* sin storage */ }
const T = lang === "en" ? EN : ES;
if (lang === "en") {
	document.documentElement.lang = "en";
	document.title = "Sign in · TraceReports";
	for (const n of document.querySelectorAll("[data-t]")) if (typeof EN[n.dataset.t] === "string") n.textContent = EN[n.dataset.t];
}

// ---------- adónde volver ----------
const params = new URLSearchParams(location.search);
function safeNext() {
	const n = params.get("next") || "/";
	// solo una ruta de este mismo sitio (nada de //otro.sitio ni de volver al login)
	if (!n.startsWith("/") || n.startsWith("//") || n.startsWith("/\\") || n.startsWith("/login")) return "/";
	// el servidor no ve el #run=… del enlace, pero el navegador lo conserva en esta página: vuelve con él
	return !n.includes("#") && location.hash ? n + location.hash : n;
}
function leave() { location.replace(safeNext()); }

// ---------- 1) el lienzo: agentes (los workers) que ejecutan tests y dejan su traza ----------
const LANES = [
	{ w: "gw0", color: "#29dfff" }, { w: "gw1", color: "#c9fa6b" }, { w: "gw2", color: "#b69cff" },
	{ w: "gw3", color: "#ff9f6b" }, { w: "gw4", color: "#5ee0c0" }, { w: "gw5", color: "#ffd166" },
];
const TESTS = ["abrir_ficha", "agenda_del_dia", "guardar_atencion", "imprimir_receta", "registrar_llegada", "despachar_receta",
	"crear_solicitud", "periodontograma", "derivar_paciente", "modificar_orden", "agregar_alergia", "cargar_preparacion"];
// el guion, determinista: duración (s) y estado de cada test de cada worker
const PLAN = LANES.map((_, li) => Array.from({ length: 14 }, (_, i) => {
	const k = (li * 7 + i * 5) % 17;
	const state = (li === 2 && i === 3) || (li === 4 && i === 6) || (li === 0 && i === 9) ? "fail" : k % 6 === 0 ? "warn" : "pass";
	return { name: `test_${String(i + 1).padStart(3, "0")}_${TESTS[(li * 3 + i) % TESTS.length]}`, dur: 2.2 + (k % 5) * 0.9, state };
}));
const COLORS = { pass: "#3ecf8e", warn: "#f5b83d", fail: "#ff5d5d" };

function stage() {
	const cv = $("#stage"), ctx = cv.getContext("2d");
	const top = $(".ruler-top"), left = $(".ruler-left");
	// la traza ya va avanzada al abrir (OFFSET s) y da la vuelta cada CYCLE s
	const OFFSET = 11, CYCLE = 44, SPEED = 58; // SPEED: px por segundo de la traza
	let W = 0, H = 0, dpr = 1, t0 = performance.now() - OFFSET * 1000, last = 0, running = true, px = 0, py = 0, limit = 0;
	function size() {
		dpr = Math.min(2, window.devicePixelRatio || 1);
		W = innerWidth; H = innerHeight;
		cv.width = W * dpr; cv.height = H * dpr;
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		// la traza no pasa por debajo de la tarjeta: se desvanece antes (en angosto, la tarjeta va encima)
		const card = document.querySelector(".card").getBoundingClientRect();
		limit = W >= 960 ? Math.max(320, card.left - 36) : W;
		drawRulers();
	}
	// las reglas, con sus marcas y números (como en un editor de diseño)
	function drawRulers() {
		const mk = (el, horizontal) => {
			const len = horizontal ? W : H, c = document.createElement("canvas");
			const r = 22;
			c.width = (horizontal ? len : r) * dpr; c.height = (horizontal ? r : len) * dpr;
			c.style.cssText = `position:absolute;inset:0;width:${horizontal ? len : r}px;height:${horizontal ? r : len}px`;
			const g = c.getContext("2d");
			g.scale(dpr, dpr);
			g.strokeStyle = "#2a313b"; g.fillStyle = "#5b6472"; g.font = "500 9px 'JetBrains Mono', monospace";
			for (let p = 0; p < len; p += 8) {
				const major = p % 80 === 0, mid = p % 40 === 0, h = major ? 9 : mid ? 6 : 3;
				g.beginPath();
				if (horizontal) { g.moveTo(p + 0.5, r); g.lineTo(p + 0.5, r - h); } else { g.moveTo(r, p + 0.5); g.lineTo(r - h, p + 0.5); }
				g.stroke();
				if (major && p > 0) {
					if (horizontal) g.fillText(String(p), p + 3, 10);
					else { g.save(); g.translate(10, p + 3); g.rotate(-Math.PI / 2); g.fillText(String(p), -18, 0); g.restore(); }
				}
			}
			el.replaceChildren(c);
		};
		mk(top, true); mk(left, false);
	}
	function lanesY() {
		const narrow = W < 960, y0 = narrow ? 60 : Math.max(96, H * 0.13), gap = narrow ? 34 : Math.min(48, (H * 0.5) / LANES.length);
		return LANES.map((_, i) => y0 + i * gap);
	}
	// dónde va cada worker a los t segundos: los tests terminados y el que corre
	function laneState(li, t) {
		const x0 = 96, out = [];
		let x = x0, time = 0.6 * li;
		for (const p of PLAN[li]) {
			const w = p.dur * SPEED;
			if (t < time) break;
			const done = t >= time + p.dur;
			out.push({ ...p, x, w: done ? w : (t - time) * SPEED, done });
			if (!done) break;
			x += w + 4; time += p.dur + 0.15;
		}
		return out;
	}
	function frame(now) {
		if (!running) return;
		requestAnimationFrame(frame);
		if (now - last < 33) return;
		last = now;
		draw(reduced ? 26 : OFFSET + (((now - t0) / 1000 - OFFSET) % (CYCLE - OFFSET)));
	}
	function draw(t) {
		ctx.clearRect(0, 0, W, H);
		const ox = px * 0.6, oy = py * 0.6; // depth 2 se mueve más que la grilla
		const ys = lanesY();
		const fade = reduced ? 1 : Math.min(1, (CYCLE - t) / 1.2); // al final del ciclo se desvanece y vuelve
		ctx.globalAlpha = Math.max(0, fade);
		let selected = null;
		LANES.forEach((lane, li) => {
			const y = ys[li] + oy;
			// la etiqueta del carril y su línea guía
			ctx.font = "600 11px 'JetBrains Mono', monospace";
			ctx.fillStyle = "#6d7787"; ctx.fillText(lane.w, 40 + ox, y + 4);
			ctx.strokeStyle = "rgba(42,49,59,.6)"; ctx.setLineDash([2, 4]);
			ctx.beginPath(); ctx.moveTo(84 + ox, y + 0.5); ctx.lineTo(W - 20, y + 0.5); ctx.stroke(); ctx.setLineDash([]);
			const bars = laneState(li, t);
			bars.forEach((b) => {
				const x = b.x + ox, w = Math.max(2, b.w - 1);
				ctx.fillStyle = COLORS[b.state] + (b.done ? "" : "cc");
				round(ctx, x, y - 9, w, 18, 3); ctx.fill();
				if (!b.done) { // el que corre: rayado que avanza
					ctx.save(); round(ctx, x, y - 9, w, 18, 3); ctx.clip();
					ctx.strokeStyle = "rgba(255,255,255,.18)"; ctx.lineWidth = 5;
					for (let s = -20 + ((t * 30) % 12); s < w + 20; s += 12) { ctx.beginPath(); ctx.moveTo(x + s, y + 10); ctx.lineTo(x + s + 12, y - 10); ctx.stroke(); }
					ctx.restore(); ctx.lineWidth = 1;
				}
				if (w > 120) {
					ctx.fillStyle = b.state === "warn" ? "#2a1d00" : "#06120c";
					ctx.font = "500 10px 'JetBrains Mono', monospace";
					ctx.fillText((b.state === "fail" ? "✕ " : b.state === "warn" ? "▲ " : "") + b.name.slice(0, Math.floor((w - 14) / 6.2)), x + 7, y + 3.5);
				}
				if (b.state === "fail" && b.done) selected = { x, y, w, b };
			});
			// el cursor del worker, en la punta de lo que está dibujando
			const cur = bars[bars.length - 1];
			if (cur) cursor(ctx, cur.x + ox + cur.w, y + 10, lane);
		});
		// la caja de selección sobre el último fallo, con sus manijas y la medida
		if (selected) {
			const { x, y, w, b } = selected, pad = 4;
			ctx.strokeStyle = "#29dfff"; ctx.lineWidth = 1;
			ctx.strokeRect(x - pad + 0.5, y - 9 - pad + 0.5, w + pad * 2, 18 + pad * 2);
			ctx.fillStyle = "#08090c";
			for (const [hx, hy] of [[x - pad, y - 9 - pad], [x + w + pad, y - 9 - pad], [x - pad, y + 9 + pad], [x + w + pad, y + 9 + pad]]) {
				ctx.fillRect(hx - 3, hy - 3, 6, 6); ctx.strokeRect(hx - 2.5, hy - 2.5, 6, 6);
			}
			const label = `${b.name} · ${b.dur.toFixed(1)} s · FAIL`;
			ctx.font = "600 10px 'JetBrains Mono', monospace";
			const lw = ctx.measureText(label).width + 12;
			ctx.fillStyle = "#29dfff"; round(ctx, x - pad, y + 9 + pad + 6, lw, 18, 4); ctx.fill();
			ctx.fillStyle = "#04161b"; ctx.fillText(label, x - pad + 6, y + 9 + pad + 18.5);
		}
		ctx.globalAlpha = 1;
		if (limit < W) { // el borde: se desvanece en 90 px y nada después
			ctx.save();
			ctx.globalCompositeOperation = "destination-out";
			const g = ctx.createLinearGradient(limit - 90, 0, limit, 0);
			g.addColorStop(0, "rgba(0,0,0,0)"); g.addColorStop(1, "rgba(0,0,0,1)");
			ctx.fillStyle = g; ctx.fillRect(limit - 90, 0, 90, H);
			ctx.fillStyle = "#000"; ctx.fillRect(limit, 0, W - limit, H);
			ctx.restore();
		}
		// depth 0: la grilla de puntos, en todo el lienzo (al final: el desvanecido no la borra)
		ctx.fillStyle = "rgba(120,135,155,.13)";
		const gx = (px * 0.2) % 24, gy = (py * 0.2) % 24;
		for (let x = 22 + gx; x < W; x += 24) for (let y = 22 + gy; y < H; y += 24) ctx.fillRect(x, y, 1, 1);
	}
	function cursor(g, x, y, lane) {
		g.save();
		g.translate(x, y);
		g.fillStyle = lane.color; g.strokeStyle = "#08090c"; g.lineWidth = 1.2;
		g.beginPath(); g.moveTo(0, 0); g.lineTo(0, 13); g.lineTo(3.6, 9.8); g.lineTo(6.3, 15.2); g.lineTo(8.4, 14.2); g.lineTo(5.8, 9); g.lineTo(10.4, 9); g.closePath();
		g.fill(); g.stroke();
		g.font = "600 10px 'JetBrains Mono', monospace";
		const w = g.measureText(lane.w).width + 10;
		round(g, 11, 12, w, 16, 4); g.fill();
		g.fillStyle = "#08090c"; g.fillText(lane.w, 16, 23.5);
		g.restore();
	}
	function round(g, x, y, w, h, r) {
		r = Math.min(r, w / 2, h / 2);
		g.beginPath(); g.moveTo(x + r, y); g.arcTo(x + w, y, x + w, y + h, r); g.arcTo(x + w, y + h, x, y + h, r);
		g.arcTo(x, y + h, x, y, r); g.arcTo(x, y, x + w, y, r); g.closePath();
	}
	size();
	addEventListener("resize", () => { size(); if (reduced) draw(26); });
	document.addEventListener("visibilitychange", () => { running = !document.hidden && !reduced; if (running) requestAnimationFrame(frame); });
	// parallax: el puntero mueve cada capa según su profundidad (CSS lee --px/--py; el lienzo, px/py)
	if (!reduced && matchMedia("(pointer: fine)").matches) {
		addEventListener("pointermove", (e) => {
			px = (e.clientX / W - 0.5) * 24; py = (e.clientY / H - 0.5) * 18;
			document.documentElement.style.setProperty("--px", `${px}px`);
			document.documentElement.style.setProperty("--py", `${py}px`);
		}, { passive: true });
	}
	if (reduced) { running = false; draw(26); } else requestAnimationFrame(frame);
	return {
		restart() { t0 = performance.now() - OFFSET * 1000; },
		// los carriles y sus barras tal como se verán al arrancar (para que el boceto calce con la animación)
		layout() {
			const ys = lanesY(), end = limit - 40;
			return { limit: end, lanes: LANES.map((lane, li) => ({ w: lane.w, y: ys[li],
				bars: laneState(li, OFFSET).filter((b) => b.x < end).map((b) => {
					const w = Math.max(4, Math.min(b.w - 1, end - b.x));
					const text = w > 120 ? (b.state === "fail" ? "✕ " : b.state === "warn" ? "▲ " : "") + b.name.slice(0, Math.floor((w - 14) / 6.2)) : "";
					return { x: b.x, w, state: b.state, text };
				}) })) };
		},
	};
}

// ---------- 2) Trace Buddy ----------
const buddy = { ctl: null };
async function mountBuddy() {
	const el = $("#buddy");
	try {
		const m = await import("/buddy/buddy3d.js");
		const holder = document.createElement("span");
		holder.style.cssText = "position:absolute;inset:0";
		el.appendChild(holder);
		const ctl = m.mountBuddy3D(holder, { theme: "trace-dark", reducedMotion: reduced, onEvent: () => {} });
		if (!ctl) return;
		buddy.ctl = ctl;
		el.classList.add("has-3d");
		watch(); // si ya había un campo con el foco, la pose correcta
	} catch (err) { console.warn("Trace Buddy:", err); }
}
// la pose según dónde está el foco y si la clave se ve
function watch() {
	const c = buddy.ctl;
	if (!c) return;
	const a = document.activeElement, shown = $("#pass").type === "text";
	const pose = a === $("#pass") && !shown ? "cover" : a === $("#eye") && !shown ? "peek" : "none";
	c.setPrivacy(pose);
	$("#buddy").dataset.pose = pose;
	if (a === $("#user")) follow($("#user"));
	if (a === $("#pass") && shown) follow($("#pass"));
}
// mira el texto que se escribe: de izquierda a derecha a medida que crece
function follow(input) {
	if (!buddy.ctl) return;
	const n = input.value.length, x = -0.95 + Math.min(1, n / 26) * 0.8;
	buddy.ctl.lookAt(x, 0.55 + (input === $("#pass") ? 0.25 : 0));
}

// ---------- 3) el formulario ----------
function setNotice(text, kind = "") {
	const n = $("#notice");
	n.hidden = !text; n.textContent = text || ""; n.className = `notice ${kind}`;
}
let lockTimer = 0;
function lockFor(secs) {
	const go = $("#go"), err = $("#error");
	clearInterval(lockTimer);
	let left = secs;
	const tick = () => {
		if (left <= 0) { clearInterval(lockTimer); go.disabled = false; err.textContent = ""; return; }
		const m = Math.floor(left / 60), s = left % 60;
		err.textContent = T.locked(m ? `${m} min ${String(s).padStart(2, "0")} s` : `${s} s`);
		left--;
	};
	go.disabled = true;
	tick();
	lockTimer = setInterval(tick, 1000);
}
function bad(msg, fields) {
	$("#error").textContent = msg;
	for (const f of fields) f.closest(".field").classList.add("bad");
	if (!reduced) { const card = $(".card"); card.classList.remove("shake"); void card.offsetWidth; card.classList.add("shake"); }
	buddy.ctl?.react("shake");
}

function form() {
	const user = $("#user"), pass = $("#pass"), eye = $("#eye"), go = $("#go");
	for (const f of [user, pass]) f.addEventListener("input", () => { f.closest(".field").classList.remove("bad"); $("#error").textContent = ""; });
	user.addEventListener("input", () => follow(user));
	pass.addEventListener("input", () => { if (pass.type === "text") follow(pass); });
	for (const el of [user, pass, eye]) { el.addEventListener("focus", watch); el.addEventListener("blur", () => setTimeout(watch, 0)); }
	let greeted = "";
	user.addEventListener("blur", () => {
		const name = user.value.trim();
		if (!name || name === greeted || !build.done) return;
		greeted = name;
		buddy.ctl?.react("nod");
		say(lang === "en" ? `Hi, <b>${escHTML(name.slice(0, 40))}</b>. Now the password… I'm not looking.` : `Hola, <b>${escHTML(name.slice(0, 40))}</b>. Ahora la clave… yo no miro.`, 3800);
	});
	eye.addEventListener("pointerenter", () => { if (pass.type === "password" && document.activeElement === pass && buddy.ctl) { buddy.ctl.setPrivacy("peek"); $("#buddy").dataset.pose = "peek"; } });
	eye.addEventListener("pointerleave", () => setTimeout(watch, 0));
	eye.addEventListener("click", () => {
		const show = pass.type === "password";
		pass.type = show ? "text" : "password";
		eye.setAttribute("aria-pressed", String(show));
		$("#eye-label").textContent = show ? T.hide : T.show;
		pass.focus({ preventScroll: true });
		const end = pass.value.length;
		pass.setSelectionRange(end, end);
		watch();
		if (show) { buddy.ctl?.react("startle"); say(pick(PEEK), 5200, "devil"); }
		else say(pick(HIDE), 2600);
	});
	// Bloq Mayús, mientras se escribe la clave
	const caps = (e) => { $("#caps").hidden = !(e.getModifierState && e.getModifierState("CapsLock")); };
	pass.addEventListener("keydown", caps); pass.addEventListener("keyup", caps);
	pass.addEventListener("blur", () => { $("#caps").hidden = true; });

	$("#form").addEventListener("submit", async (e) => {
		e.preventDefault();
		if (go.disabled) return;
		if (!user.value.trim() || !pass.value) { bad(T.empty, [!user.value.trim() ? user : pass]); (!user.value.trim() ? user : pass).focus(); return; }
		go.setAttribute("aria-busy", "true"); go.disabled = true; $("#error").textContent = "";
		let res;
		try {
			res = await fetch("/api/v1/auth/login", {
				method: "POST", credentials: "same-origin",
				headers: { "Content-Type": "application/json", Accept: "application/json", "X-TraceReports-UI": "1" },
				body: JSON.stringify({ user: user.value.trim(), password: pass.value, remember: $("#remember").checked }),
			});
		} catch {
			go.removeAttribute("aria-busy"); go.disabled = false;
			bad(T.net, []);
			return;
		}
		go.removeAttribute("aria-busy");
		if (res.ok) {
			go.classList.add("ok");
			$(".go-text").textContent = `${T.welcome} ✓`;
			buddy.ctl?.setPrivacy("none");
			buddy.ctl?.react("jump");
			setTimeout(leave, reduced ? 0 : 750);
			return;
		}
		if (res.status === 429) {
			lockFor(Number(res.headers.get("Retry-After")) || 60);
			buddy.ctl?.react("shake");
			return;
		}
		go.disabled = false;
		pass.value = "";
		bad(res.status === 401 ? T.wrong : T.net, res.status === 401 ? [user, pass] : []);
		pass.focus();
	});
}

// ---------- reloj del encabezado ----------
function clock() {
	const el = $("#clock");
	const tick = () => { el.textContent = new Date().toLocaleTimeString(lang === "en" ? "en-GB" : "es-CL", { hour12: false }); };
	tick();
	setInterval(tick, 1000);
}

// ---------- 4) la obra: un equipo de agentes construye la página ----------
// Como Swarm (un sitio que se construye solo): la página es una lista de tareas con dependencias (las
// reglas, las guías, rectángulos que se arrastran, textos que se escriben letra por letra); varios agentes,
// cada uno con su cursor, toman en paralelo las que ya pueden hacerse. Al terminar arrancan los agentes del
// fondo y Trace Buddy cuenta cuántos la armaron. Cualquier tecla, clic o pegado la termina al instante
// (nada que se mueva bloquea el ingreso); con "reducir movimiento", o si ya se vio en esta pestaña, no se repite.
const M = window.Motion; // Motion: Framer Motion sin React (/vendor/motion)
const AGENTS = [
	{ id: "grid", color: "#ff5fa2" }, { id: "layout", color: "#29dfff" }, { id: "copy", color: "#ffd166" },
	{ id: "marca", color: "#c9fa6b" }, { id: "forms", color: "#b69cff" }, { id: "seguridad", color: "#3ecf8e" }, { id: "qa", color: "#ff9f6b" },
];
const JOBS = {
	es: { sketch: "bosqueja", rulers: "pone las reglas", guide: "traza una guía", rect: "dibuja", paint: "pinta", palette: "abre la paleta", render: "renderiza", type: "escribe", reveal: "coloca", check: "revisa", trace: "arranca la traza" },
	en: { sketch: "sketches", rulers: "sets the rulers", guide: "draws a guide", rect: "draws", paint: "paints", palette: "opens the palette", render: "renders", type: "types", reveal: "places", check: "checks", trace: "starts the trace" },
}[lang];
const LINES = {
	es: {
		start: "<span class=\"who\">Trace Buddy</span>Espera un momento… tengo al equipo de agentes <b>armando tu login</b>.",
		copy: "<span class=\"who\">Trace Buddy</span>El de textos escribe rápido, pero no le pidas poesía.",
		forms: "<span class=\"who\">Trace Buddy</span>El de formularios es nuevo, pero promete.",
		security: "<span class=\"who\">Trace Buddy</span>Seguridad está sellando tu sesión: <b>cookie HttpOnly</b>, nada en claro.",
		qa: "<span class=\"who\">Trace Buddy</span>QA revisa que todo funcione con el teclado. Ojalá no encuentre bugs…",
		done: (n, s) => `<span class="who">Trace Buddy</span>Listo: <b>${n} agentes</b> armaron este login en ${s} s. Y tranquilo: <b>prometo no mirar tu clave</b>.`,
		skipped: "<span class=\"who\">Trace Buddy</span>¡Apurado! El equipo dejó todo listo. Prometo no mirar tu clave.",
		again: "<span class=\"who\">Trace Buddy</span>Hola de nuevo. El equipo ya dejó todo listo.",
		expired: "<span class=\"who\">Trace Buddy</span>Te fuiste un rato y la sesión se cerró sola. Por seguridad, ya sabes.",
	},
	en: {
		start: "<span class=\"who\">Trace Buddy</span>Hang on… my team of agents is <b>building your sign-in</b>.",
		copy: "<span class=\"who\">Trace Buddy</span>The copy agent types fast. Don't ask for poetry.",
		forms: "<span class=\"who\">Trace Buddy</span>The forms agent is new, but promising.",
		security: "<span class=\"who\">Trace Buddy</span>Security is sealing your session: <b>HttpOnly cookie</b>, nothing in clear.",
		qa: "<span class=\"who\">Trace Buddy</span>QA is checking it all works with the keyboard. Fingers crossed…",
		done: (n, s) => `<span class="who">Trace Buddy</span>Done: <b>${n} agents</b> built this sign-in in ${s} s. And relax: <b>I won't look at your password</b>.`,
		skipped: "<span class=\"who\">Trace Buddy</span>In a hurry! The team left everything ready. I won't look at your password.",
		again: "<span class=\"who\">Trace Buddy</span>Welcome back. The team left everything ready.",
		expired: "<span class=\"who\">Trace Buddy</span>You stepped away and the session closed itself. Security, you know.",
	},
}[lang];
const BUILT_KEY = "tracereports-login-built";

// las travesuras de Buddy con la clave: al mostrarla, un susto y después, pícaro
const PEEK = {
	es: ["¡Ey! ¿Me acabas de mostrar la clave? …Ya la vi. Es broma. Creo.", "¡Uy! Eso no se le muestra a un robot. Ahora sé tu secreto.",
		"¿En serio? ¿A mí? Tranquilo, mis labios están sellados… casi siempre.", "¡Ajá! Clave a la vista. Un villano de película ya estaría adentro."],
	en: ["Hey! Did you just show me the password? …I saw it. Kidding. I think.", "Whoa! You don't show that to a robot. Now I know your secret.",
		"Really? To me? Relax, my lips are sealed… mostly.", "Aha! Password in plain sight. A movie villain would be in by now."],
}[lang];
const HIDE = { es: ["Ya, ya… no miro.", "Vale, me tapo. Pero ya era tarde.", "Ojos cerrados. Palabra de robot."], en: ["OK, OK… not looking.", "Fine, covering up. A bit late though.", "Eyes shut. Robot's honor."] }[lang];
const pick = (list) => list[Math.floor(Math.random() * list.length)];
const escHTML = (v) => String(v).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

// el globo: encabezado con su estado, "escribiendo…" un instante y el texto (más humano que aparecer de golpe)
let sayTimer = 0, sayType = 0, sayGen = 0; // sayGen: una salida vieja no esconde un globo nuevo
function say(html, ms = 0, kind = "") {
	const el = $("#say");
	clearTimeout(sayTimer); clearTimeout(sayType);
	sayGen++;
	html = html.replace('<span class="who">Trace Buddy</span>', "");
	const devil = kind === "devil";
	const head = `<span class="say-h"><i></i>Trace Buddy<small>${lang === "en" ? "now" : "ahora"}</small></span>`;
	const first = el.hidden;
	el.hidden = false;
	el.className = `say${devil ? " devil" : ""}`;
	const text = () => { el.innerHTML = `${head}<span class="say-t">${html}</span>`; if (M && !reduced) M.animate(el.querySelector(".say-t"), { opacity: [0, 1], y: [4, 0] }, { duration: 0.22 }); };
	if (reduced || !M) text();
	else {
		el.innerHTML = `${head}<span class="say-t typing" aria-hidden="true"><i></i><i></i><i></i></span>`;
		sayType = setTimeout(text, first ? 420 : 260); // el texto es fijo de este archivo, o escapado (el usuario)
		M.animate(el, first ? { opacity: [0, 1], scale: [0.85, 1], y: [10, 0] } : { opacity: 1, y: 0, scale: [0.96, 1] }, { type: "spring", stiffness: 420, damping: 22 });
	}
	if (ms) sayTimer = setTimeout(() => hideSay(), ms + 420);
}
function hideSay() {
	const el = $("#say");
	if (el.hidden) return;
	clearTimeout(sayType);
	if (reduced || !M) { el.hidden = true; return; }
	const gen = sayGen;
	M.animate(el, { opacity: [1, 0], scale: [1, 0.9], y: [0, 6] }, { duration: 0.18 }).then(() => { if (gen !== sayGen) return; el.hidden = true; el.style.opacity = ""; el.style.transform = ""; });
}

// el humo de la obra: bocanadas donde cae una pieza y una bruma alrededor de la tarjeta mientras se construye
const smoke = { ps: [], haze: false, run: false, last: 0, ctx: null };
function smokeSize() {
	const c = $("#smoke"), d = Math.min(2, devicePixelRatio || 1);
	c.width = innerWidth * d; c.height = innerHeight * d;
	smoke.ctx = c.getContext("2d");
	smoke.ctx.setTransform(d, 0, 0, d, 0, 0);
}
function puff(x, y, n = 14, spread = 1) {
	if (reduced || !smoke.ctx) return;
	for (let i = 0; i < n; i++) {
		smoke.ps.push({ x: x + (Math.random() - 0.5) * 24 * spread, y: y + (Math.random() - 0.5) * 10, vx: (Math.random() - 0.5) * 46 * spread, vy: -12 - Math.random() * 34,
			r: 8 + Math.random() * 14, g: 24 + Math.random() * 30, a: 0.3 + Math.random() * 0.16, life: 0, max: 1.2 + Math.random() * 1.4 });
	}
	smokeLoop();
}
function smokeLoop() {
	if (smoke.run || !smoke.ctx) return;
	smoke.run = true;
	smoke.last = performance.now();
	const tick = (now) => {
		const dt = Math.min(0.05, (now - smoke.last) / 1000);
		smoke.last = now;
		const g = smoke.ctx;
		g.clearRect(0, 0, innerWidth, innerHeight);
		if (smoke.haze && Math.random() < 0.85) {
			const r = $("#card").getBoundingClientRect(), side = Math.random();
			const x = side < 0.5 ? r.left + Math.random() * r.width : side < 0.75 ? r.left : r.right;
			const y = side < 0.5 ? (side < 0.25 ? r.top : r.bottom) : r.top + Math.random() * r.height;
			smoke.ps.push({ x, y, vx: (Math.random() - 0.5) * 18, vy: -8 - Math.random() * 14, r: 12, g: 26, a: 0.13, life: 0, max: 2 });
		}
		smoke.ps = smoke.ps.filter((p) => (p.life += dt) < p.max);
		for (const p of smoke.ps) {
			p.x += p.vx * dt; p.y += p.vy * dt; p.vx *= 0.985; p.r += p.g * dt;
			const a = p.a * Math.pow(1 - p.life / p.max, 1.6);
			const grd = g.createRadialGradient(p.x, p.y, 0, p.x, p.y, p.r);
			grd.addColorStop(0, `rgba(214,222,232,${a})`); grd.addColorStop(0.6, `rgba(170,182,198,${a * 0.45})`); grd.addColorStop(1, "rgba(150,160,175,0)");
			g.fillStyle = grd; g.beginPath(); g.arc(p.x, p.y, p.r, 0, Math.PI * 2); g.fill();
		}
		if (smoke.ps.length || smoke.haze) requestAnimationFrame(tick);
		else { smoke.run = false; g.clearRect(0, 0, innerWidth, innerHeight); }
	};
	requestAnimationFrame(tick);
}

// ---- el plan: qué se construye, quién lo hace y después de qué ----
// kind: rulers | guide (axis x|y, at) | rect (se arrastra un rectángulo y aparece la pieza) | type (letra por
// letra) | reveal (aparece con resorte) | check (la barrida de QA) | trace (arrancan los agentes del fondo)
function plan() {
	const R = (sel) => () => $(sel).getBoundingClientRect();
	const P = ["palette"];
	return [
		{ id: "rulers", kind: "rulers", by: "grid" },
		{ id: "palette", kind: "palette", by: "grid", deps: ["rulers"] },
		{ id: "g-card-l", kind: "guide", axis: "x", at: () => R("#card")().left, by: "qa", deps: ["rulers"] },
		{ id: "g-card-t", kind: "guide", axis: "y", at: () => R("#card")().top, by: "layout", deps: ["rulers"] },
		{ id: "g-pitch", kind: "guide", axis: "x", at: () => R(".pitch h1")().left, by: "copy", deps: ["rulers"], wide: true },
		{ id: "card", kind: "rect", el: "#card", paint: "surface", by: "layout", deps: ["g-card-l", "g-card-t", ...P], say: "start" },
		{ id: "kicker", kind: "rect", el: ".kicker", paint: "line", by: "copy", deps: ["g-pitch", ...P], wide: true },
		{ id: "h1", kind: "type", el: ".pitch h1", by: "copy", deps: ["kicker"], wide: true, say: "copy" },
		{ id: "lede", kind: "type", el: ".lede", by: "copy", deps: ["h1"], wide: true },
		{ id: "brand", kind: "rect", el: ".brand .mark", show: ".brand", paint: "lime", by: "marca", deps: ["card"] },
		{ id: "title", kind: "type", el: "#login-title", by: "marca", deps: ["brand"] },
		{ id: "sub", kind: "type", el: "#sub", by: "marca", deps: ["title"] },
		{ id: "f-user", kind: "rect", el: "#user", show: ".field:nth-of-type(1)", paint: "ink", by: "forms", deps: ["card"], say: "forms" },
		{ id: "f-pass", kind: "rect", el: "#pass", show: ".field:nth-of-type(2)", paint: "ink", by: "seguridad", deps: ["card"] },
		{ id: "remember", kind: "rect", el: ".remember .switch", show: ".remember", paint: "pass", by: "seguridad", deps: ["f-pass"], say: "security" },
		{ id: "foot", kind: "rect", el: ".foot", paint: "line", by: "layout", deps: ["card"] },
		{ id: "go", kind: "rect", el: "#go", paint: "lime", by: "forms", deps: ["f-user"] },
		{ id: "go-t", kind: "type", el: ".go-text", by: "forms", deps: ["go"] },
		{ id: "qa", kind: "check", el: "#card", by: "qa", deps: ["go-t", "foot", "sub"], say: "qa" },
		// el boceto de la traza, repartido: tres agentes, dos carriles cada uno
		{ id: "lanes", kind: "lanes", lanes: [0, 1], by: "grid", deps: ["palette"] },
		{ id: "lanes-2", kind: "lanes", lanes: [2, 3], by: "qa", deps: ["palette"] },
		{ id: "lanes-3", kind: "lanes", lanes: [4, 5], by: "layout", deps: ["palette", "foot"] },
		{ id: "render", kind: "render", by: "grid", deps: ["qa", "lede", "kicker", "lanes", "lanes-2", "lanes-3"] },
		{ id: "trace", kind: "trace", by: "grid", deps: ["render"] },
	];
}
// la paleta del agente grid: los colores del sistema (los mismos tokens del CSS)
const PALETTE = [
	["ink", "#0a0c10"], ["surface", "#171b22"], ["line", "#2a313b"], ["lime", "#c9fa6b"], ["cyan", "#29dfff"], ["pass", "#3ecf8e"], ["warn", "#f5b83d"], ["fail", "#ff5d5d"],
];
// lo que se construye empieza invisible (el texto que se escribe, vacío)
const BUILT_SEL = [".ruler", ".kicker", ".pitch h1", ".lede", ".brand", "#login-title", "#sub", ".field", ".remember", ".foot", "#go"];
const texts = new Map();

const build = { skipped: false, done: false, wake: [], t0: 0, stage: null, agents: new Map(), guides: [], drafts: [], palette: null, log: [] };
window.__obra = build; // para mirar la obra desde las pruebas (qué tarea, cuándo empezó y cuánto tardó)
const wait = (ms) => new Promise((ok) => { if (build.skipped) return ok(); const t = setTimeout(ok, ms); build.wake.push(() => { clearTimeout(t); ok(); }); });
const anim = (el, kf, opt) => (build.skipped || !M ? Promise.resolve() : M.animate(el, kf, opt));

function agentOf(id, i) {
	let ag = build.agents.get(id);
	if (ag) return ag;
	const a = AGENTS.find((x) => x.id === id);
	const el = document.createElement("div");
	el.className = "agent";
	el.dataset.agent = id;
	el.innerHTML = `<svg viewBox="0 0 24 24"><path d="M3 2l7.2 19 2.5-7.6L20.5 11z" fill="${a.color}" stroke="#08090c" stroke-width="1.4" stroke-linejoin="round"/></svg>`
		+ `<b style="background:${a.color}">agente·${id}<em></em></b>`;
	$("#agents").appendChild(el);
	const from = [[-60, innerHeight * 0.3], [innerWidth + 40, -40], [innerWidth * 0.5, innerHeight + 40], [innerWidth + 40, innerHeight * 0.8], [-60, innerHeight + 20], [innerWidth * 0.3, -50], [innerWidth + 40, innerHeight * 0.4]][(i ?? build.agents.size) % 7];
	el.style.transform = `translate(${from[0]}px, ${from[1]}px)`;
	ag = { el, x: from[0], y: from[1], color: a.color };
	build.agents.set(id, ag);
	return ag;
}
function job(ag, text) { ag.el.querySelector("em").textContent = text; }
async function fly(ag, x, y, secs = 0.38) {
	if (build.skipped) return;
	const mid = [(ag.x + x) / 2 + (Math.random() - 0.5) * 120, Math.min(ag.y, y) - 40];
	const k = { x: [ag.x, mid[0], x], y: [ag.y, mid[1], y] };
	ag.x = x; ag.y = y;
	await anim(ag.el, k, { duration: secs, ease: [0.22, 1, 0.36, 1] });
}
const visible = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).display !== "none";

// ---- cada tipo de tarea ----
async function doRulers(ag) {
	job(ag, JOBS.rulers);
	await fly(ag, 30, 30, 0.3);
	const rulers = [...document.querySelectorAll(".ruler")];
	rulers[0].style.transform = "scaleX(0)"; rulers[1].style.transform = "scaleY(0)"; // sin parpadeo al aparecer
	rulers.forEach((r) => r.classList.remove("unbuilt"));
	await Promise.all([anim(rulers[0], { scaleX: [0, 1] }, { duration: 0.3, ease: [0.22, 1, 0.36, 1] }), anim(rulers[1], { scaleY: [0, 1] }, { duration: 0.3, ease: [0.22, 1, 0.36, 1] })]);
}
async function doGuide(ag, t) {
	const at = t.at();
	if (!Number.isFinite(at)) return;
	job(ag, JOBS.guide);
	const g = document.createElement("div");
	g.className = `guide ${t.axis}`;
	g.style[t.axis === "x" ? "left" : "top"] = `${Math.round(at)}px`;
	document.body.appendChild(g);
	build.guides.push(g);
	// el agente la saca de la regla y la arrastra por toda la página
	if (t.axis === "x") { await fly(ag, at, 14, 0.32); g.dataset.label = `x ${Math.round(at)}`; await Promise.all([anim(g, { scaleY: [0, 1] }, { duration: 0.38 }), fly(ag, at, innerHeight * 0.55, 0.38)]); }
	else { await fly(ag, 14, at, 0.32); g.dataset.label = `y ${Math.round(at)}`; await Promise.all([anim(g, { scaleX: [0, 1] }, { duration: 0.38 }), fly(ag, innerWidth * 0.45, at, 0.38)]); }
}
async function doRect(ag, t) {
	const el = $(t.el), shown = t.show ? $(t.show) : el;
	if (!visible(el)) return;
	const r = el.getBoundingClientRect();
	job(ag, `${JOBS.rect} ${Math.round(r.width)}×${Math.round(r.height)}`);
	await fly(ag, r.left, r.top);
	// el rectángulo crece desde la esquina con el cursor, mostrando su medida, y "pega" contra las guías
	const d = document.createElement("div");
	d.className = "draft";
	Object.assign(d.style, { left: `${r.left}px`, top: `${r.top}px`, width: `${r.width}px`, height: `${r.height}px`, borderColor: ag.color });
	d.innerHTML = `<span style="background:${ag.color}">${Math.round(r.width)} × ${Math.round(r.height)}</span>`;
	document.body.appendChild(d);
	const secs = Math.min(0.42, 0.2 + (r.width + r.height) / 3600);
	await Promise.all([anim(d, { scaleX: [0, 1], scaleY: [0, 1] }, { duration: secs, ease: [0.22, 1, 0.36, 1] }), fly(ag, r.right - 4, r.bottom - 4, secs)]);
	build.drafts.push(d);
	if (t.paint) await paint(ag, d, r, t.paint);
	puff(r.left + r.width / 2, r.bottom, t.id === "card" ? 26 : 10, t.id === "card" ? 2.4 : 1);
	if (build.skipped) d.remove();
}
// la paleta: el agente grid la abre; cada color con su muestra y su hex
async function doPalette(ag) {
	job(ag, JOBS.palette);
	const pal = document.createElement("div");
	pal.className = "palette";
	pal.innerHTML = `<b>${lang === "en" ? "Palette" : "Paleta"}</b>` + PALETTE.map(([n, c]) => `<span class="sw" data-sw="${n}"><i style="background:${c}"></i><small>${n}<em>${c}</em></small></span>`).join("");
	document.body.appendChild(pal);
	build.palette = pal;
	const L = build.stage?.layout();
	if (L && innerWidth >= 960) { // en el espacio libre entre la traza y el titular
		pal.style.top = `${Math.round(L.lanes[L.lanes.length - 1].y + 46)}px`;
		pal.style.left = "calc(var(--ruler) + 76px)";
	}
	const r = pal.getBoundingClientRect();
	await fly(ag, r.left + 12, r.top + 10, 0.35);
	await anim(pal, { opacity: [0, 1], x: [-30, 0], scale: [0.92, 1] }, { type: "spring", stiffness: 420, damping: 26 });
	anim(pal.querySelectorAll(".sw"), { opacity: [0, 1], x: [-8, 0] }, { delay: M ? M.stagger(0.04) : 0, duration: 0.25 });
	await fly(ag, r.right - 16, r.bottom - 12, 0.3);
}
// pintar: la muestra late, una gota viaja de la paleta al rectángulo y el color lo cubre como una pincelada
async function paint(ag, d, r, name) {
	const color = (PALETTE.find(([n]) => n === name) || PALETTE[1])[1];
	job(ag, `${JOBS.paint} ${name}`);
	const sw = build.palette?.querySelector(`[data-sw="${name}"]`);
	const fill = document.createElement("i");
	fill.className = "fill";
	fill.style.background = color;
	d.prepend(fill);
	if (sw && !build.skipped) {
		sw.classList.add("on");
		const s = sw.querySelector("i").getBoundingClientRect();
		const drop = document.createElement("i");
		drop.className = "drop";
		drop.style.background = color;
		document.body.appendChild(drop);
		const tx = r.left + Math.min(40, r.width / 2), ty = r.top + r.height / 2;
		await anim(drop, { x: [s.left + 4, (s.left + tx) / 2, tx], y: [s.top + 4, Math.min(s.top, ty) - 70, ty], scale: [1, 1.3, 0.6] }, { duration: 0.3, ease: [0.3, 0, 0.3, 1] });
		drop.remove();
		sw.classList.remove("on");
	}
	await Promise.all([anim(fill, { scaleX: [0, 1] }, { duration: 0.24, ease: [0.65, 0, 0.35, 1] }), fly(ag, r.right - 6, r.top + r.height / 2, 0.24)]);
}
// escribe un texto corto en el boceto, letra por letra, con el cursor del agente (si está)
async function typeInto(el, text, ag, color) {
	const caret = document.createElement("i");
	caret.className = "type-caret";
	caret.style.background = color;
	el.textContent = "";
	el.appendChild(caret);
	const node = document.createTextNode("");
	el.insertBefore(node, caret);
	const step = Math.max(1, Math.ceil(text.length / 10));
	for (let i = step; i < text.length + step; i += step) {
		if (build.skipped) break;
		node.textContent = text.slice(0, Math.min(i, text.length));
		if (ag) { const cr = caret.getBoundingClientRect(); ag.el.style.transform = `translate(${cr.right + 2}px, ${cr.top + 4}px)`; ag.x = cr.right + 2; ag.y = cr.top + 4; }
		await wait(12);
	}
	node.textContent = text;
	caret.remove();
}
// el boceto de la traza (agente grid): carril por carril, su etiqueta, su línea y las barras pintadas con el
// color de su estado, donde después corre la animación de verdad
async function doLanes(ag, t) {
	const L = build.stage?.layout();
	if (!L || build.skipped) return;
	const color = (st) => (PALETTE.find(([n]) => n === st) || PALETTE[5])[1];
	for (const lane of L.lanes.filter((_, i) => !t.lanes || t.lanes.includes(i))) {
		if (build.skipped) return;
		job(ag, `${JOBS.sketch} ${lane.w}`);
		await fly(ag, 30, lane.y - 6, 0.2);
		const lab = document.createElement("div");
		lab.className = "draft lane-label";
		Object.assign(lab.style, { left: "40px", top: `${lane.y - 8}px` });
		const track = document.createElement("div");
		track.className = "draft lane-track";
		Object.assign(track.style, { left: "84px", top: `${lane.y}px`, width: `${L.limit - 84}px` });
		document.body.append(lab, track);
		build.drafts.push(lab, track);
		anim(track, { scaleX: [0, 1] }, { duration: 0.3, ease: [0.22, 1, 0.36, 1] });
		await typeInto(lab, lane.w, ag, ag.color); // el nombre del carril, letra por letra
		for (const b of lane.bars) {
			if (build.skipped) return;
			const st = b.state === "warn" ? "warn" : b.state;
			const sw = build.palette?.querySelector(`[data-sw="${st}"]`);
			sw?.classList.add("on");
			const d = document.createElement("div");
			d.className = "draft lane-bar";
			Object.assign(d.style, { left: `${b.x}px`, top: `${lane.y - 9}px`, width: `${b.w}px` });
			d.innerHTML = `<i class="fill" style="background:${color(st)}"></i><span class="lbl ${st}"></span>`;
			document.body.appendChild(d);
			build.drafts.push(d);
			const secs = Math.min(0.2, 0.08 + b.w / 2400);
			await Promise.all([anim(d.firstChild, { scaleX: [0, 1] }, { duration: secs, ease: [0.65, 0, 0.35, 1] }), fly(ag, b.x + b.w, lane.y + 4, secs)]);
			sw?.classList.remove("on");
			if (b.text) await typeInto(d.querySelector(".lbl"), b.text, ag, "#06120c"); // el nombre del test, escribiéndose
		}
		puff(Math.min(L.limit - 60, (lane.bars.at(-1)?.x ?? 120) + (lane.bars.at(-1)?.w ?? 0)), lane.y, 4, 0.6);
	}
}
// el render: una barrida recorre la página, el boceto se desvanece y aparece la página de verdad
async function doRender(ag) {
	job(ag, JOBS.render);
	await fly(ag, innerWidth / 2, 36, 0.35);
	const sweep = document.createElement("div");
	sweep.className = "render-sweep";
	document.body.appendChild(sweep);
	const pieces = [...document.querySelectorAll(".unbuilt")].filter((n) => !n.classList.contains("ruler"));
	const drafts = build.drafts.splice(0);
	frameUp();
	await Promise.all([
		anim(sweep, { y: [0, innerHeight], opacity: [0, 1, 1, 0] }, { duration: 0.7, ease: [0.4, 0, 0.2, 1] }),
		fly(ag, innerWidth / 2, innerHeight - 60, 0.7),
		anim(drafts, { opacity: [1, 0], filter: ["blur(0px)", "blur(8px)"], scale: [1, 1.02] }, { duration: 0.45, delay: M ? M.stagger(Math.min(0.035, 0.5 / Math.max(1, drafts.length))) : 0 }),
		...pieces.map((n, i) => wait(i * 40).then(() => reveal(n))),
	]);
	drafts.forEach((d) => d.remove());
	sweep.remove();
	if (build.palette) anim(build.palette, { opacity: [1, 0], x: [0, -24] }, { duration: 0.25 }).then(() => build.palette?.remove());
	const cr = $("#card").getBoundingClientRect();
	puff(cr.left + cr.width / 2, cr.bottom, 24, 2.6);
}
async function doType(ag, t) {
	const el = $(t.el);
	const full = texts.get(el) ?? el.textContent;
	el.classList.remove("unbuilt");
	el.style.opacity = "1";
	if (!visible(el)) { el.textContent = full; return; }
	job(ag, JOBS.type);
	el.textContent = "";
	const caret = document.createElement("i");
	caret.className = "type-caret";
	caret.style.background = ag.color;
	el.appendChild(caret);
	const r0 = el.getBoundingClientRect();
	await fly(ag, r0.left, r0.top + 6, 0.32);
	// unos 40 tics como máximo: un texto largo avanza varias letras por tic
	const step = Math.max(1, Math.ceil(full.length / 40)), per = 16;
	for (let i = step; i < full.length + step; i += step) {
		if (build.skipped) break;
		i = Math.min(i, full.length);
		el.firstChild?.nodeType === 3 ? (el.firstChild.textContent = full.slice(0, i)) : el.insertBefore(document.createTextNode(full.slice(0, i)), caret);
		{
			const cr = caret.getBoundingClientRect();
			ag.el.style.transform = `translate(${cr.right + 2}px, ${cr.top + cr.height * 0.4}px)`;
			ag.x = cr.right + 2; ag.y = cr.top + cr.height * 0.4;
		}
		await wait(per);
	}
	caret.remove();
	el.textContent = full;
}
function reveal(el) {
	if (!el) return Promise.resolve();
	if (build.skipped || !M) { el.classList.remove("unbuilt"); el.style.opacity = ""; return Promise.resolve(); }
	el.style.opacity = "0";
	el.classList.remove("unbuilt");
	return M.animate(el, { opacity: [0, 1], y: [14, 0], scale: [0.95, 1] }, { type: "spring", stiffness: 380, damping: 24 });
}
async function doReveal(ag, t) {
	const el = $(t.el);
	if (visible(el)) { const r = el.getBoundingClientRect(); job(ag, JOBS.reveal); await fly(ag, r.left + 8, r.top + r.height / 2); }
	reveal(el);
	if (visible(el)) { const r = el.getBoundingClientRect(); puff(r.left + r.width / 2, r.bottom, 8); }
	await wait(120);
}
async function doCheck(ag, t) {
	const card = $(t.el), cr = card.getBoundingClientRect();
	job(ag, JOBS.check);
	await fly(ag, cr.right - 20, cr.top + 10);
	const scan = document.createElement("div");
	scan.className = "scan";
	card.appendChild(scan);
	await Promise.all([anim(scan, { y: [0, cr.height], opacity: [1, 1, 0] }, { duration: 0.55, ease: "easeInOut" }), fly(ag, cr.right - 20, cr.bottom - 10, 0.55)]);
	scan.remove();
	job(ag, "a11y ✓ · teclado ✓");
	await wait(150);
}
async function doTrace(ag) {
	job(ag, JOBS.trace);
	await fly(ag, 90, $("#stage").getBoundingClientRect().top + 110, 0.5);
	build.stage?.restart();
	$(".stage").classList.remove("dark");
}
function frameUp() {
	const card = $("#card");
	card.classList.remove("building");
	card.classList.add("framed");
	anim(card, { scale: [0.97, 1] }, { type: "spring", stiffness: 260, damping: 20 });
}

const RUN = { rulers: doRulers, guide: doGuide, palette: doPalette, lanes: doLanes, rect: doRect, type: doType, reveal: doReveal, check: doCheck, render: doRender, trace: doTrace };
async function construct() {
	const card = $("#card");
	build.t0 = performance.now();
	card.classList.add("building");
	$(".stage").classList.add("dark");
	$("#ab-state").textContent = lang === "en" ? "· building…" : "· construyendo…";
	for (const sel of BUILT_SEL) for (const el of document.querySelectorAll(sel)) el.classList.add("unbuilt");
	for (const t of plan()) if (t.kind === "type") { const el = $(t.el); texts.set(el, el.textContent); }
	smokeSize();
	addEventListener("resize", smokeSize);
	// la persona manda: escribir, hacer clic o pegar termina la obra al instante
	const skip = () => finish(true);
	for (const ev of ["keydown", "pointerdown", "paste"]) addEventListener(ev, skip, { once: true, capture: true });
	smoke.haze = true;
	smokeLoop();
	// los agentes toman, en paralelo, las tareas cuyas dependencias ya están hechas (cada una la hace su dueño)
	const tasks = plan(), done = new Set(), taken = new Set();
	const ready = (by) => tasks.find((t) => t.by === by && !taken.has(t.id) && (t.deps || []).every((d) => done.has(d)));
	await Promise.all(AGENTS.map(async (a, i) => {
		await wait(i * 90);
		while (!build.skipped && done.size < tasks.length) {
			const t = ready(a.id);
			if (!t) {
				if (!tasks.some((x) => x.by === a.id && !taken.has(x.id))) break; // no le queda nada: se va
				const ag = build.agents.get(a.id);
				if (ag && !ag.parked) { ag.parked = true; job(ag, lang === "en" ? "waiting…" : "esperando…"); fly(ag, 34 + i * 4, 60 + i * 34, 0.35); }
				await wait(60);
				continue;
			}
			const parked = build.agents.get(a.id);
			if (parked) parked.parked = false;
			taken.add(t.id);
			if (t.wide && !visible($(".pitch h1"))) { if (t.kind === "type") $(t.el).textContent = texts.get($(t.el)); done.add(t.id); continue; }
			const ag = agentOf(a.id, i);
			if (t.say) say(LINES[t.say]);
			const t1 = performance.now();
			try { await RUN[t.kind](ag, t); } catch (err) { console.warn("obra:", t.id, err); }
			build.log.push([t.id, Math.round(t1 - build.t0), Math.round(performance.now() - t1)]);
			done.add(t.id);
		}
		const ag = build.agents.get(a.id);
		if (ag && !build.skipped) { job(ag, lang === "en" ? "done ✓" : "listo ✓"); await wait(200); anim(ag.el, { opacity: [1, 0], scale: [1, 0.7] }, { duration: 0.3 }).then(() => ag.el.remove()); }
	}));
	finish(false);
}
function finish(skipped) {
	if (build.done) return;
	build.done = true;
	build.skipped = true;
	build.wake.splice(0).forEach((w) => w());
	const card = $("#card");
	card.classList.remove("building");
	card.classList.add("framed");
	for (const [el, full] of texts) el.textContent = full;
	for (const el of document.querySelectorAll(".unbuilt")) el.classList.remove("unbuilt");
	for (const el of document.querySelectorAll(BUILT_SEL.join(","))) { el.style.opacity = ""; el.style.transform = ""; }
	document.querySelectorAll(".draft, .scan, .type-caret, .drop, .render-sweep, .palette").forEach((n) => n.remove());
	$(".stage").classList.remove("dark");
	build.stage?.restart();
	smoke.haze = false;
	$("#ab-state").textContent = lang === "en" ? "· ready" : "· listo";
	$("#ab-state").classList.add("done");
	try { sessionStorage.setItem(BUILT_KEY, "1"); } catch { /* sin storage */ }
	// las guías y los agentes se van (más rápido de lo que llegaron)
	for (const el of [...document.querySelectorAll(".agent"), ...build.guides]) {
		if (M && !reduced) M.animate(el, { opacity: [getComputedStyle(el).opacity, 0] }, { duration: 0.35 }).then(() => el.remove()); else el.remove();
	}
	if (skipped) { say(LINES.skipped, 6000); return; }
	const r = card.getBoundingClientRect();
	puff(r.left + r.width / 2, r.bottom, 30, 3);
	buddy.ctl?.react("jump");
	say(LINES.done(AGENTS.length, ((performance.now() - build.t0) / 1000).toFixed(1).replace(".", lang === "en" ? "." : ",")), 7000);
}
function startBuild() {
	let seen = false;
	try { seen = sessionStorage.getItem(BUILT_KEY) === "1"; } catch { /* sin storage */ }
	if (reduced || seen || params.get("expired") || params.get("bye") || !M) {
		build.done = true;
		$("#ab-state").textContent = lang === "en" ? "· ready" : "· listo";
		$("#ab-state").classList.add("done");
		say(params.get("expired") ? LINES.expired : seen ? LINES.again : LINES.skipped.replace(/¡Apurado! |In a hurry! /, ""), 5000);
		return;
	}
	construct();
}
// escribir esconde el globo (no tapa el formulario)
document.addEventListener("input", () => { if (build.done) hideSay(); });

// ---------- arranque ----------
async function start() {
	if (params.get("expired")) setNotice(T.expired, "warn");
	else if (params.get("bye")) setNotice(T.bye);
	startBuild(); // antes de esperar al servidor: la tarjeta no alcanza a verse armada y luego desarmarse
	try {
		const s = await (await fetch("/api/v1/auth/session", { headers: { Accept: "application/json" }, credentials: "same-origin" })).json();
		if (s.logged_in) return leave(); // ya hay sesión: de vuelta
		if (s.login_required === false) { setNotice(T.open); return setTimeout(leave, 600); }
		if (s.remember_days) $("#remember-days").textContent = T.days(Math.round(s.remember_days));
		if (s.idle_min) $("#policy").textContent = T.idle(Math.round(s.idle_min));
	} catch { /* sin servidor: el formulario avisará al enviar */ }
	build.stage = stage();
	form();
	clock();
	$("#user").focus({ preventScroll: true });
	(window.requestIdleCallback || ((f) => setTimeout(f, 300)))(mountBuddy);
}
start();
