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

// ---------- arranque ----------
async function start() {
	if (params.get("expired")) setNotice(T.expired, "warn");
	else if (params.get("bye")) setNotice(T.bye);
	try {
		const s = await (await fetch("/api/v1/auth/session", { headers: { Accept: "application/json" }, credentials: "same-origin" })).json();
		if (s.logged_in) return leave(); // ya hay sesión: de vuelta
		if (s.login_required === false) { setNotice(T.open); return setTimeout(leave, 600); }
		if (s.remember_days) $("#remember-days").textContent = T.days(Math.round(s.remember_days));
		if (s.idle_min) $("#policy").textContent = T.idle(Math.round(s.idle_min));
	} catch { /* sin servidor: el formulario avisará al enviar */ }
	stage();
	form();
	clock();
	$("#user").focus({ preventScroll: true });
	(window.requestIdleCallback || ((f) => setTimeout(f, 300)))(mountBuddy);
}
start();
