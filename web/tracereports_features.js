/* TraceReports — componentes de nueva generación (vanilla JS, sin dependencias).
 *
 *   TraceReportsFeatures.copyText(text, button)        copiar al portapapeles con feedback visual
 *   TraceReportsFeatures.Drawer                        panel lateral accesible (focus trap, Esc)
 *   TraceReportsFeatures.TimeTravelPlayer              replay de pasos con capturas (1 FPS, 2x)
 *   TraceReportsFeatures.LiveStream                    cliente SSE con reconexión silenciosa
 *   TraceReportsFeatures.MockGenerator                 mocks/stubs: Playwright, Cypress, WireMock
 *   TraceReportsFeatures.curlOf(conn)                  "Copiar como cURL" con secretos enmascarados
 *   TraceReportsFeatures.sparkline(statuses)           barras mínimas de las últimas ejecuciones
 *
 * Cada módulo crea su propio DOM y lo actualiza en el lugar (no re-renderiza el árbol).
 */
(() => {
	"use strict";

	const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
	const icon = (id) => `<svg aria-hidden="true"><use href="#${id}"/></svg>`;
	const lower = (s) => String(s || "").toLowerCase();

	// ─── portapapeles ────────────────────────────────────────────────────────
	function legacyCopy(text) {
		const ta = document.createElement("textarea");
		ta.value = text;
		ta.setAttribute("readonly", "");
		ta.style.cssText = "position:fixed;opacity:0;pointer-events:none";
		document.body.appendChild(ta);
		ta.select();
		let ok = false;
		try { ok = document.execCommand("copy"); } catch { /* sin permiso */ }
		ta.remove();
		return ok;
	}

	/** Copia `text` y da feedback en el botón: texto "¡Copiado!", pulso y vibración corta. */
	function copyText(text, btn) {
		const done = (ok) => {
			if (!btn) return;
			const prev = btn.dataset.label || btn.innerHTML;
			btn.dataset.label = prev;
			btn.classList.remove("cf-copied", "cf-copy-failed");
			void btn.offsetWidth; // reinicia la animación
			btn.classList.add(ok ? "cf-copied" : "cf-copy-failed");
			btn.innerHTML = ok ? `${icon("i-check")}¡Copiado!` : "No se pudo copiar";
			if (ok && navigator.vibrate) navigator.vibrate(12);
			clearTimeout(btn._cfTimer);
			btn._cfTimer = setTimeout(() => { btn.innerHTML = prev; btn.classList.remove("cf-copied", "cf-copy-failed"); }, 1600);
		};
		if (navigator.clipboard?.writeText && window.isSecureContext) {
			navigator.clipboard.writeText(text).then(() => done(true), () => done(legacyCopy(text)));
		} else done(legacyCopy(text));
	}

	function download(filename, text, type = "text/plain") {
		const url = URL.createObjectURL(new Blob([text], { type: `${type};charset=utf-8` }));
		const a = Object.assign(document.createElement("a"), { href: url, download: filename });
		document.body.appendChild(a);
		a.click();
		a.remove();
		setTimeout(() => URL.revokeObjectURL(url), 1000);
	}

	// ─── drawer accesible ────────────────────────────────────────────────────
	const Drawer = (() => {
		let root, panel, lastFocus, onClose;
		const FOCUSABLE = 'button:not([disabled]), [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

		function ensure() {
			if (root) return;
			root = document.createElement("div");
			root.className = "cf-drawer-root";
			root.hidden = true;
			root.innerHTML = `<div class="cf-drawer-backdrop" data-cf-close></div>
				<aside class="cf-drawer" role="dialog" aria-modal="true" aria-labelledby="cf-drawer-title" tabindex="-1">
					<header class="cf-drawer-head">
						<div class="cf-drawer-titles"><h2 id="cf-drawer-title"></h2><p class="cf-drawer-sub"></p></div>
						<button class="cf-icon-btn" data-cf-close aria-label="Cerrar">${icon("i-close")}</button>
					</header>
					<div class="cf-drawer-body"></div>
				</aside>`;
			document.body.appendChild(root);
			panel = root.querySelector(".cf-drawer");
			root.addEventListener("click", (e) => { if (e.target.closest("[data-cf-close]")) close(); });
			// Escape a nivel de documento: cierra aunque el foco haya quedado fuera del panel
			document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !root.hidden) close(); });
			root.addEventListener("keydown", (e) => {
				if (e.key !== "Tab") return;
				const items = [...panel.querySelectorAll(FOCUSABLE)].filter((el) => el.offsetParent !== null);
				if (!items.length) return;
				const first = items[0], last = items[items.length - 1];
				if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
				else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
			});
		}

		/** open({title, subtitle, body: html|Node, onClose}) → elemento body del drawer. */
		function open({ title, subtitle = "", body = "", onClose: cb } = {}) {
			ensure();
			lastFocus = document.activeElement;
			onClose = cb;
			panel.querySelector("#cf-drawer-title").textContent = title;
			panel.querySelector(".cf-drawer-sub").textContent = subtitle;
			const slot = panel.querySelector(".cf-drawer-body");
			slot.innerHTML = "";
			if (typeof body === "string") slot.innerHTML = body; else slot.appendChild(body);
			root.hidden = false;
			document.documentElement.classList.add("cf-no-scroll");
			requestAnimationFrame(() => {
				root.classList.add("open");
				(panel.querySelector("[data-cf-autofocus]") || panel).focus();
			});
			return slot;
		}

		function close() {
			if (!root || root.hidden) return;
			root.classList.remove("open");
			document.documentElement.classList.remove("cf-no-scroll");
			setTimeout(() => { root.hidden = true; }, 180);
			onClose?.();
			lastFocus?.focus?.();
		}

		return { open, close, isOpen: () => !!root && !root.hidden };
	})();

	// ─── sparkline ───────────────────────────────────────────────────────────
	/** Barras mínimas (del más viejo al más nuevo). */
	function sparkline(statuses = []) {
		return `<span class="cf-spark" aria-hidden="true">${statuses.map((st) =>
			`<i class="${lower(st)}" style="height:${st === "FAIL" ? 100 : st === "SKIP" ? 35 : 60}%"></i>`).join("")}</span>`;
	}

	// ─── Time-Travel Step Replay ─────────────────────────────────────────────
	const STATUS_ICON = { PASS: "i-pass", FAIL: "i-fail", WARNING: "i-warning", SKIP: "i-skip", INFO: "i-info" };

	class TimeTravelPlayer {
		/**
		 * @param {HTMLElement} container
		 * @param {Array<{id,status,message,timestamp,screenshot}>} steps
		 * @param {{start?:number, index?:number, speed?:number, onChange?:(i:number, speed:number)=>void, onLightbox?:(src,caption)=>void}} opts
		 */
		constructor(container, steps, opts = {}) {
			this.el = container;
			this.opts = opts;
			this.speed = opts.speed || 1;
			this.timer = null;
			this.setSteps(steps, opts.index ?? 0, true);
		}

		setSteps(steps, index = this.i, initial = false) {
			this.steps = steps.slice();
			this.t0 = this.opts.start || (this.steps[0]?.timestamp ?? 0);
			const last = this.steps.length ? this.steps[this.steps.length - 1].timestamp : this.t0;
			this.span = Math.max(1, last - this.t0);
			// cuadro a mostrar en cada paso: su captura o la última anterior
			let lastShot = null;
			this.frames = this.steps.map((s) => { if (s.screenshot) lastShot = s.screenshot; return { src: lastShot, own: !!s.screenshot }; });
			this.frames.forEach((f) => { if (f.src && f.own) { const img = new Image(); img.src = f.src; } }); // precarga
			if (initial || !this.el.querySelector(".tt-player")) this.mount();
			else this.renderMarkers();
			this.go(Math.min(Math.max(0, index), this.steps.length - 1), false);
		}

		mount() {
			this.el.innerHTML = `<div class="tt-player" tabindex="0" role="region" aria-label="Replay de los pasos del test" aria-describedby="tt-help">
				<div class="tt-main">
					<div class="tt-stage">
						<img class="tt-frame" alt="">
						<div class="tt-empty">${icon("i-tests")}<span>Este paso no tiene captura</span></div>
						<span class="tt-carry" hidden>sin captura en este paso · se muestra la anterior</span>
						<button class="tt-zoom cf-icon-btn" title="Ver en tamaño completo" aria-label="Ver captura en tamaño completo">${icon("i-search")}</button>
						<div class="tt-hud"><span class="tt-hud-status"></span><span class="tt-caption"></span><span class="tt-time"></span></div>
					</div>
					<div class="tt-scrubber" role="slider" tabindex="0" aria-label="Paso del replay" aria-valuemin="1">
						<div class="tt-track"><div class="tt-progress"></div></div>
						<div class="tt-nodes"></div>
					</div>
					<div class="tt-controls">
						<button class="cf-icon-btn" data-tt="prev" aria-label="Paso anterior" title="Anterior (←)">${icon("i-prev")}</button>
						<button class="tt-play" data-tt="play" aria-label="Reproducir" title="Reproducir / pausar (Espacio)">${icon("i-play")}</button>
						<button class="cf-icon-btn" data-tt="next" aria-label="Paso siguiente" title="Siguiente (→)">${icon("i-next")}</button>
						<div class="tt-speed" role="group" aria-label="Velocidad">
							<button data-speed="1" aria-pressed="true">1x</button><button data-speed="2" aria-pressed="false">2x</button>
						</div>
						<span class="tt-counter" aria-live="polite"></span>
						<span class="tt-help" id="tt-help">Espacio: reproducir · ← →: pasos</span>
					</div>
				</div>
				<ol class="tt-steps" aria-label="Pasos"></ol>
			</div>`;
			const $ = (s) => this.el.querySelector(s);
			this.$ = { root: $(".tt-player"), img: $(".tt-frame"), empty: $(".tt-empty"), carry: $(".tt-carry"), hudStatus: $(".tt-hud-status"),
				caption: $(".tt-caption"), time: $(".tt-time"), scrub: $(".tt-scrubber"), progress: $(".tt-progress"), nodes: $(".tt-nodes"),
				play: $(".tt-play"), counter: $(".tt-counter"), list: $(".tt-steps"), speed: $(".tt-speed"), zoom: $(".tt-zoom") };
			this.renderMarkers();

			this.$.root.addEventListener("click", (e) => {
				const b = e.target.closest("[data-tt],[data-speed],[data-i]");
				if (!b) return;
				if (b.dataset.tt === "prev") this.go(this.i - 1);
				else if (b.dataset.tt === "next") this.go(this.i + 1);
				else if (b.dataset.tt === "play") this.toggle();
				else if (b.dataset.speed) this.setSpeed(Number(b.dataset.speed));
				else if (b.dataset.i) { this.pause(); this.go(Number(b.dataset.i)); }
			});
			this.$.zoom.addEventListener("click", () => {
				const f = this.frames[this.i];
				if (f?.src) this.opts.onLightbox?.(f.src, this.steps[this.i].message);
			});
			this.$.root.addEventListener("keydown", (e) => this.onKey(e));
			// scrubber: clic o arrastre → paso más cercano
			const seek = (e) => {
				const r = this.$.scrub.getBoundingClientRect();
				const ratio = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
				let best = 0, bestD = Infinity;
				this.positions.forEach((p, i) => { const d = Math.abs(p - ratio * 100); if (d < bestD) { bestD = d; best = i; } });
				this.go(best);
			};
			this.$.scrub.addEventListener("pointerdown", (e) => {
				if (e.target.closest(".tt-node")) return;
				this.pause();
				this.$.scrub.setPointerCapture(e.pointerId);
				seek(e);
				const move = (ev) => seek(ev);
				const up = () => { this.$.scrub.removeEventListener("pointermove", move); this.$.scrub.removeEventListener("pointerup", up); };
				this.$.scrub.addEventListener("pointermove", move);
				this.$.scrub.addEventListener("pointerup", up);
			});
		}

		renderMarkers() {
			const n = this.steps.length;
			const byTime = this.span > 1 && n > 1;
			this.positions = this.steps.map((s, i) => byTime ? ((s.timestamp - this.t0) / this.span) * 100 : n > 1 ? (i / (n - 1)) * 100 : 0);
			this.$.nodes.innerHTML = this.steps.map((s, i) => `<button class="tt-node ${lower(s.status)} ${s.screenshot ? "has-shot" : ""}"
				style="left:${this.positions[i].toFixed(2)}%" data-i="${i}" tabindex="-1"
				aria-label="Paso ${i + 1}: ${esc(s.status)} ${esc(s.message)}" title="${i + 1}. ${esc(s.message)}"></button>`).join("");
			this.$.list.innerHTML = this.steps.map((s, i) => `<li><button class="tt-step ${lower(s.status)}" data-i="${i}">
				<span class="tt-step-icon">${icon(STATUS_ICON[s.status] || "i-info")}</span>
				<span class="tt-step-time">+${((s.timestamp - this.t0) / 1000).toFixed(1)}s</span>
				<span class="tt-step-text">${esc(s.message)}</span>${s.screenshot ? `<span class="tt-step-shot" title="Tiene captura">${icon("i-camera")}</span>` : ""}
			</button></li>`).join("");
			this.$.scrub.setAttribute("aria-valuemax", String(n));
		}

		go(i, notify = true) {
			if (!this.steps.length) return;
			this.i = Math.min(Math.max(0, i), this.steps.length - 1);
			const s = this.steps[this.i], f = this.frames[this.i];
			if (f.src) {
				if (this.$.img.getAttribute("src") !== f.src) this.$.img.src = f.src;
				this.$.img.hidden = false;
			} else this.$.img.hidden = true;
			this.$.img.alt = s.message || "";
			this.$.img.classList.toggle("carried", !!f.src && !f.own);
			this.$.empty.hidden = !!f.src;
			this.$.carry.hidden = !(f.src && !f.own);
			this.$.zoom.hidden = !f.src;
			this.$.hudStatus.className = `tt-hud-status label ${lower(s.status)}`;
			this.$.hudStatus.textContent = lower(s.status);
			this.$.caption.textContent = s.message || "";
			this.$.time.textContent = `+${((s.timestamp - this.t0) / 1000).toFixed(2)}s`;
			this.$.progress.style.width = `${this.positions[this.i]}%`;
			this.$.counter.textContent = `${this.i + 1} / ${this.steps.length}`;
			this.$.scrub.setAttribute("aria-valuenow", String(this.i + 1));
			this.$.scrub.setAttribute("aria-valuetext", `Paso ${this.i + 1} de ${this.steps.length}: ${s.message || ""}`);
			this.el.querySelectorAll(".tt-node").forEach((n, k) => n.classList.toggle("current", k === this.i));
			this.el.querySelectorAll(".tt-step").forEach((b, k) => {
				const on = k === this.i;
				b.classList.toggle("current", on);
				b.setAttribute("aria-current", on ? "step" : "false");
				if (on) b.scrollIntoView({ block: "nearest" });
			});
			if (notify) this.opts.onChange?.(this.i, this.speed);
		}

		play() {
			if (this.i >= this.steps.length - 1) this.go(0);
			this.pause();
			this.timer = setInterval(() => {
				if (this.i >= this.steps.length - 1) { this.pause(); return; }
				this.go(this.i + 1);
			}, 1000 / this.speed); // 1 cuadro por segundo a 1x
			this.$.play.innerHTML = icon("i-pause");
			this.$.play.setAttribute("aria-label", "Pausar");
			this.$.root.classList.add("playing");
		}

		pause() {
			clearInterval(this.timer);
			this.timer = null;
			if (!this.$) return;
			this.$.play.innerHTML = icon("i-play");
			this.$.play.setAttribute("aria-label", "Reproducir");
			this.$.root.classList.remove("playing");
		}

		toggle() { this.timer ? this.pause() : this.play(); }

		setSpeed(x) {
			this.speed = x;
			this.$.speed.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(Number(b.dataset.speed) === x)));
			if (this.timer) this.play();
			this.opts.onChange?.(this.i, this.speed);
		}

		onKey(e) {
			if (e.target.closest("input, textarea")) return;
			const keys = { " ": () => this.toggle(), ArrowLeft: () => { this.pause(); this.go(this.i - 1); },
				ArrowRight: () => { this.pause(); this.go(this.i + 1); }, Home: () => this.go(0), End: () => this.go(this.steps.length - 1) };
			if (keys[e.key]) { e.preventDefault(); keys[e.key](); }
		}

		destroy() { this.pause(); }
	}

	// ─── Server-Sent Events ──────────────────────────────────────────────────
	class LiveStream {
		/**
		 * @param {string} url  endpoint SSE
		 * @param {{onEvent:(type,data)=>void, onStatus:(state:"live"|"reconnecting"|"off")=>void}} handlers
		 */
		constructor(url, { onEvent, onStatus }) {
			this.url = url;
			this.onEvent = onEvent;
			this.onStatus = onStatus;
			this.state = "off";
		}

		start() {
			if (!window.EventSource) { this.set("off"); return false; }
			this.es = new EventSource(this.url);
			this.es.onopen = () => this.set("live");
			// EventSource reintenta solo: solo se refleja el estado, sin alertas intrusivas
			this.es.onerror = () => this.set(this.es.readyState === EventSource.CLOSED ? "off" : "reconnecting");
			for (const type of ["run", "test", "log", "network", "triage", "summary"]) {
				this.es.addEventListener(type, (e) => {
					try { this.onEvent(type, JSON.parse(e.data)); } catch (err) { console.warn("SSE", err); }
				});
			}
			return true;
		}

		set(state) {
			if (state === this.state) return;
			this.state = state;
			this.onStatus?.(state);
		}

		stop() { this.es?.close(); this.set("off"); }
	}

	// textos que no pasan por el DOM traducido (código generado): i18n.js
	const tr = (es, vars) => (window.TraceReportsI18n ? window.TraceReportsI18n.t(es, vars) : es);

	// ─── Contract stub & mock generator ──────────────────────────────────────
	const SENSITIVE_KEY = /(pass(word)?|pwd|clave|token|secret|session|auth|cookie|rut|api[_-]?key)/i;
	const RUT = /\b\d{1,2}\.?\d{3}\.?\d{3}-[\dkK]\b/g;
	const MASK = "***";
	// nombre de header que puede ir entre comillas dobles junto a la variable: nada que la shell
	// interprete ahí. Un token HTTP válido no basta: admite | & ` $ ! ' * ~ ^ # (igual que repro.go)
	const PLAIN_HEADER = /^[A-Za-z0-9_.-]+$/;
	/** Variable de shell para un header enmascarado (AUTHORIZATION, X_API_KEY…): nunca empieza con dígito. */
	const envVar = (k) => { const v = k.toUpperCase().replace(/\W+/g, "_"); return !v || /^\d/.test(v) ? `H_${v}` : v; };

	/** Enmascara campos sensibles de un JSON (por nombre de campo y valores tipo RUT). */
	function maskValue(v, key = "") {
		if (key && SENSITIVE_KEY.test(key) && v !== null && typeof v !== "object") return MASK;
		if (Array.isArray(v)) return v.map((x) => maskValue(x));
		if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, maskValue(x, k)]));
		if (typeof v === "string") return v.replace(RUT, MASK).replace(/<masked>|<rut>/g, MASK);
		return v;
	}

	/**
	 * Comando cURL (bash) que repite el request capturado; también se importa en Postman.
	 * Los headers enmascarados pasan a variables de entorno ($AUTHORIZATION) en vez de perderse.
	 */
	function curlOf(conn) {
		const q = (v) => `'${String(v).replace(/'/g, `'\\''`)}'`;
		let url = String(conn.url || "");
		try {
			const u = new URL(url);
			u.searchParams.forEach((v, k) => { if (SENSITIVE_KEY.test(k)) u.searchParams.set(k, MASK); });
			url = u.href;
		} catch { /* URL relativa: se deja como vino */ }
		url = url.replace(/<masked>|%3Cmasked%3E/gi, MASK);
		const method = conn.method || "GET";
		// el método es un argumento literal: un token HTTP válido puede llevar | & ` $ ...
		const parts = [`curl -X ${shArg(method)} ${q(url)}`];
		for (const [k, v] of Object.entries(conn.request_headers || {})) {
			if (k.startsWith(":")) continue;
			if (v === "<masked>" || SENSITIVE_KEY.test(k)) {
				// solo se expande la variable generada; el nombre va literal (igual que repro.go)
				parts.push(PLAIN_HEADER.test(k) ? `-H "${k}: $${envVar(k)}"` : `-H ${q(`${k}: `)}"$${envVar(k)}"`);
			} else parts.push(`-H ${q(`${k}: ${v}`)}`);
		}
		if (conn.post_data) {
			let body = conn.post_data.replace(RUT, MASK).replace(/<masked>|<rut>/g, MASK);
			try { body = JSON.stringify(maskValue(JSON.parse(conn.post_data))); } catch { /* no es JSON */ }
			parts.push(`--data-raw ${q(body)}`);
		}
		return parts.join(" \\\n  ");
	}

	const MockGenerator = {
		FORMATS: [
			{ id: "pw-py", label: "Playwright (Python)", ext: "py", lang: "python" },
			{ id: "pw-js", label: "Playwright (JS)", ext: "js", lang: "javascript" },
			{ id: "cypress", label: "Cypress", ext: "cy.js", lang: "javascript" },
			{ id: "wiremock", label: "WireMock", ext: "json", lang: "json" },
		],

		/** Normaliza una conexión capturada a {method, url, path, glob, status, headers, body, json}. */
		parse(conn) {
			let u;
			try { u = new URL(conn.url); } catch { u = { pathname: conn.url, search: "", searchParams: new URLSearchParams() }; }
			const ctype = conn.mime_type || conn.response_headers?.["content-type"] || "application/json";
			let json = null;
			try { json = maskValue(JSON.parse(conn.response_body || "")); } catch { /* no es JSON */ }
			const body = json !== null ? json : (conn.response_body || "").replace(RUT, MASK).replace(/<masked>|<rut>/g, MASK);
			return {
				method: conn.method || "GET", url: conn.url, path: u.pathname, query: u.searchParams,
				glob: `**${u.pathname}${u.search ? "*" : ""}`, status: conn.status || 500,
				contentType: ctype.split(";")[0], json, body,
			};
		},

		generate(format, conn) {
			const m = this.parse(conn);
			const pretty = (v, ind = 2) => JSON.stringify(v, null, ind);
			const bodyJS = m.json !== null ? `JSON.stringify(${pretty(m.json).replace(/\n/g, "\n    ")})` : JSON.stringify(m.body);
			switch (format) {
			case "pw-py": {
				const fn = `stub_${(m.path.split("/").filter(Boolean).pop() || "endpoint").replace(/\W+/g, "_")}`;
				const body = m.json !== null ? `json.dumps(${pretty(m.json, 4).replace(/\n/g, "\n    ").replace(/\bnull\b/g, "None").replace(/\btrue\b/g, "True").replace(/\bfalse\b/g, "False")})` : JSON.stringify(m.body);
				return `import json

# ${tr("Stub generado por TraceReports a partir de {req} (status {st})", { req: `${m.method} ${m.path}`, st: m.status })}
def ${fn}(route):
    if route.request.method != "${m.method}":
        return route.fallback()
    route.fulfill(
        status=${m.status},
        content_type="${m.contentType}",
        body=${body},
    )

page.route("${m.glob}", ${fn})
`;
			}
			case "pw-js":
				return `// ${tr("Stub generado por TraceReports a partir de {req} (status {st})", { req: `${m.method} ${m.path}`, st: m.status })}
await page.route('${m.glob}', async (route) => {
  if (route.request().method() !== '${m.method}') return route.fallback();
  await route.fulfill({
    status: ${m.status},
    contentType: '${m.contentType}',
    body: ${bodyJS},
  });
});
`;
			case "cypress":
				return `// ${tr("Stub generado por TraceReports a partir de {req} (status {st})", { req: `${m.method} ${m.path}`, st: m.status })}
cy.intercept('${m.method}', '${m.glob}', {
  statusCode: ${m.status},
  headers: { 'content-type': '${m.contentType}' },
  body: ${m.json !== null ? pretty(m.json).replace(/\n/g, "\n  ") : JSON.stringify(m.body)},
}).as('${(m.path.split("/").filter(Boolean).pop() || "endpoint").replace(/\W+/g, "_")}');
`;
			case "wiremock": {
				const req = { method: m.method, urlPath: m.path };
				const qp = {};
				m.query.forEach((v, k) => { qp[k] = { equalTo: SENSITIVE_KEY.test(k) ? MASK : v }; });
				if (Object.keys(qp).length) req.queryParameters = qp;
				const res = { status: m.status, headers: { "Content-Type": m.contentType } };
				if (m.json !== null) res.jsonBody = m.json; else res.body = m.body;
				return pretty({ request: req, response: res }) + "\n";
			}
			default: return "";
			}
		},

		/** Resalta el código (tokenizador simple) y marca visualmente los valores enmascarados. */
		highlight(code) {
			const re = /(\/\/[^\n]*|#[^\n]*)|("(?:\.|[^"\\n])*"|'(?:\.|[^'\\n])*')(\s*:)?|\b(true|false|null|None|True|False)\b|(-?\b\d+(?:\.\d+)?\b)/g;
			let out = "", last = 0;
			for (const m of code.matchAll(re)) {
				out += esc(code.slice(last, m.index));
				if (m[1]) out += `<span class="cf-c">${esc(m[1])}</span>`;
				else if (m[2]) out += m[3] ? `<span class="cf-k">${esc(m[2])}</span>${esc(m[3])}` : `<span class="cf-s">${esc(m[2])}</span>`;
				else if (m[4]) out += `<span class="cf-b">${m[4]}</span>`;
				else out += `<span class="cf-n">${m[5]}</span>`;
				last = m.index + m[0].length;
			}
			out += esc(code.slice(last));
			return out.replace(/\*\*\*/g, '<mark class="cf-masked" title="Dato sensible enmascarado">***</mark>');
		},

		/** Abre el drawer con las pestañas de formato para la conexión dada. */
		open(conn, { subtitle = "" } = {}) {
			const formats = this.FORMATS;
			const wrap = document.createElement("div");
			wrap.className = "cf-mock";
			wrap.innerHTML = `<div class="cf-tabs" role="tablist" aria-label="Formato del mock">
					${formats.map((f, i) => `<button role="tab" id="cf-tab-${f.id}" aria-controls="cf-mock-panel" aria-selected="${i === 0}" tabindex="${i === 0 ? 0 : -1}" data-format="${f.id}" ${i === 0 ? "data-cf-autofocus" : ""}>${esc(f.label)}</button>`).join("")}
				</div>
				<div class="cf-mock-meta"><span class="nbadge cf-method">${esc(conn.method)}</span><code>${esc(this.parse(conn).path)}</code>
					<span class="cf-status-pill">${conn.status || "sin respuesta"}</span>
					<span class="cf-masked-note"><mark class="cf-masked">***</mark> datos sensibles enmascarados</span></div>
				<pre class="cf-code" id="cf-mock-panel" role="tabpanel" tabindex="0"></pre>
				<div class="cf-actions">
					<button class="cf-btn cf-btn-primary" data-cf-copy>${icon("i-copy")}Copiar</button>
					<button class="cf-btn" data-cf-download>${icon("i-download")}Descargar</button>
				</div>`;
			let current = formats[0];
			const pre = wrap.querySelector(".cf-code");
			const render = () => {
				pre.innerHTML = this.highlight(this.generate(current.id, conn));
				pre.setAttribute("aria-labelledby", `cf-tab-${current.id}`);
				wrap.querySelectorAll("[role=tab]").forEach((t) => {
					const on = t.dataset.format === current.id;
					t.setAttribute("aria-selected", String(on));
					t.tabIndex = on ? 0 : -1;
				});
			};
			wrap.addEventListener("click", (e) => {
				const tab = e.target.closest("[data-format]");
				if (tab) { current = formats.find((f) => f.id === tab.dataset.format); render(); return; }
				if (e.target.closest("[data-cf-copy]")) copyText(this.generate(current.id, conn), e.target.closest("button"));
				if (e.target.closest("[data-cf-download]")) {
					const name = (this.parse(conn).path.split("/").filter(Boolean).pop() || "endpoint").replace(/\W+/g, "_");
					download(`mock_${name}.${current.ext}`, this.generate(current.id, conn), current.ext === "json" ? "application/json" : "text/plain");
				}
			});
			wrap.querySelector("[role=tablist]").addEventListener("keydown", (e) => {
				if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
				const i = formats.indexOf(current) + (e.key === "ArrowRight" ? 1 : -1);
				current = formats[(i + formats.length) % formats.length];
				render();
				wrap.querySelector(`[data-format="${current.id}"]`).focus();
			});
			render();
			Drawer.open({ title: "Generar mock / stub", subtitle: subtitle || `Respuesta capturada de ${conn.method} ${this.parse(conn).path}`, body: wrap });
		},
	};

	// ─── tips: qué hace cada opción ──────────────────────────────────────────
	/**
	 * Tooltip único para todo elemento con data-tip="texto" (y opcional data-tip-title, data-tip-keys,
	 * data-tip-pos="right|top"). Aparece con hover (tras un instante) y al instante con el foco del
	 * teclado; se reubica para no salirse de la pantalla. Reemplaza al title nativo: lento, sin
	 * estilo y que no existe con teclado ni en pantallas táctiles.
	 */
	const Tips = (() => {
		let tip, target, timer;
		function el() {
			if (!tip) {
				tip = document.createElement("div");
				tip.className = "cf-tooltip"; tip.id = "cf-tooltip"; tip.setAttribute("role", "tooltip"); tip.hidden = true;
				document.body.appendChild(tip);
			}
			return tip;
		}
		function show(t) {
			const text = t.dataset.tip;
			if (!text) return;
			target = t;
			const box = el();
			const title = t.dataset.tipTitle, keys = t.dataset.tipKeys;
			box.innerHTML = `${title ? `<b>${esc(title)}</b>` : ""}<span>${esc(text)}</span>${keys ? `<kbd>${esc(keys)}</kbd>` : ""}`;
			box.hidden = false; box.classList.remove("on");
			const r = t.getBoundingClientRect(), b = box.getBoundingClientRect(), gap = 8, vw = innerWidth, vh = innerHeight;
			let pos = t.dataset.tipPos || "bottom";
			if (pos === "right" && getComputedStyle(t.parentElement).flexDirection.startsWith("row")) pos = "bottom"; // nav superior (Paper)
			if (pos === "right" && r.right + gap + b.width > vw) pos = "bottom";
			if (pos === "bottom" && r.bottom + gap + b.height > vh) pos = "top";
			if (pos === "top" && r.top - gap - b.height < 0) pos = "bottom";
			let x, y;
			if (pos === "right") { x = r.right + gap; y = r.top + r.height / 2 - b.height / 2; }
			else { x = r.left + r.width / 2 - b.width / 2; y = pos === "top" ? r.top - gap - b.height : r.bottom + gap; }
			x = Math.max(8, Math.min(x, vw - b.width - 8)); y = Math.max(8, Math.min(y, vh - b.height - 8));
			box.style.left = `${x}px`; box.style.top = `${y}px`; box.dataset.pos = pos;
			t.setAttribute("aria-describedby", "cf-tooltip");
			requestAnimationFrame(() => box.classList.add("on"));
		}
		function hide() {
			clearTimeout(timer);
			if (target) target.removeAttribute("aria-describedby");
			target = null;
			if (tip) { tip.classList.remove("on"); tip.hidden = true; }
		}
		function init() {
			document.addEventListener("pointerover", (e) => {
				const t = e.target.closest("[data-tip]");
				if (t === target) return;
				hide();
				if (t && e.pointerType !== "touch") timer = setTimeout(() => show(t), 380);
			});
			document.addEventListener("pointerdown", hide);
			document.addEventListener("focusin", (e) => {
				const t = e.target.closest("[data-tip]");
				hide();
				if (t && t.matches(":focus-visible")) show(t);
			});
			document.addEventListener("focusout", hide);
			document.addEventListener("keydown", (e) => { if (e.key === "Escape") hide(); });
			// al hacer scroll se cierra el visible (quedaría desubicado); el pendiente se posiciona al mostrarse
			addEventListener("scroll", () => { if (tip && !tip.hidden) hide(); }, true);
		}
		return { init, hide };
	})();

	// ─── comparar una llamada con la última vez que el test pasó ──────────────
	// headers que cambian en cada respuesta: compararlos solo agrega ruido
	const VOLATILE_HEADERS = new Set(["date", "age", "etag", "last-modified", "expires", "content-length", "x-request-id", "x-correlation-id",
		"request-id", "traceparent", "traceresponse", "server-timing", "cf-ray", "x-amzn-requestid", "x-amzn-trace-id", "x-response-time", "set-cookie"]);
	const MAX_DIFFS = 60;

	/** Diferencias entre dos valores JSON: [{path, kind: added|removed|changed, before, after}]. */
	function jsonDiff(before, after, path = "", out = []) {
		if (out.length >= MAX_DIFFS) return out;
		const isObj = (v) => v !== null && typeof v === "object";
		if (isObj(before) && isObj(after) && Array.isArray(before) === Array.isArray(after)) {
			const keys = Array.isArray(before)
				? [...Array(Math.max(before.length, after.length)).keys()]
				: [...new Set([...Object.keys(before), ...Object.keys(after)])];
			for (const k of keys) {
				const p = Array.isArray(before) ? `${path}[${k}]` : path ? `${path}.${k}` : String(k);
				if (!(k in before)) out.push({ path: p, kind: "added", after: after[k] });
				else if (!(k in after)) out.push({ path: p, kind: "removed", before: before[k] });
				else jsonDiff(before[k], after[k], p, out);
				if (out.length >= MAX_DIFFS) break;
			}
			return out;
		}
		if (JSON.stringify(before) !== JSON.stringify(after)) out.push({ path: path || "(raíz)", kind: "changed", before, after });
		return out;
	}

	const parseJSON = (s) => { try { return JSON.parse(s); } catch { return undefined; } };

	function bodyDiff(before = "", after = "") {
		if ((before || "") === (after || "")) return null;
		const a = parseJSON(before), b = parseJSON(after);
		if (a !== undefined && b !== undefined) return { json: jsonDiff(a, b) };
		return { text: { before: String(before || "").slice(0, 4000), after: String(after || "").slice(0, 4000) } };
	}

	/**
	 * Qué cambió en una llamada al backend entre la última vez que el test pasó (then) y ahora
	 * (now): status, headers de respuesta (sin los volátiles), body de la respuesta y del request.
	 */
	function diffCalls(now, then) {
		const lowerKeys = (h) => Object.fromEntries(Object.entries(h || {}).map(([k, v]) => [lower(k), v]));
		const hn = lowerKeys(now.response_headers), ht = lowerKeys(then.response_headers);
		const headers = [...new Set([...Object.keys(hn), ...Object.keys(ht)])].sort()
			.filter((k) => !VOLATILE_HEADERS.has(k) && hn[k] !== ht[k])
			.map((k) => ({ name: k, before: ht[k], after: hn[k] }));
		const statusOf = (c) => (c.failed || !c.status ? `sin respuesta${c.error_text ? ` (${c.error_text})` : ""}` : String(c.status));
		return {
			status: { before: statusOf(then), after: statusOf(now), changed: statusOf(then) !== statusOf(now) },
			durationMs: { before: then.duration_ms ?? null, after: now.duration_ms ?? null },
			headers,
			response: bodyDiff(then.response_body, now.response_body),
			request: bodyDiff(then.post_data, now.post_data),
		};
	}

	// ─── reproducir en local ─────────────────────────────────────────────────
	const shq = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`;
	const reEsc = (s) => String(s).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	// palabra sin caracteres que la shell interprete: va tal cual; si no, entre comillas (igual que repro.go)
	const shArg = (s) => (/^[A-Za-z0-9_./:=@%+,-]+$/.test(s) ? s : shq(s));
	/** El commit de una ejecución: un id de Git (4 a 64 hex). Cualquier otra cosa no entra en un comando. */
	const normalizeCommit = (c) => { const s = String(c ?? "").trim(); return /^[0-9a-fA-F]{4,64}$/.test(s) ? s.toLowerCase() : ""; };

	/**
	 * Comandos para correr un test en local según el framework de la ejecución y la identidad del
	 * test (la key que reportó el cliente). Devuelve [{label, cmd}] (vacío si no se puede deducir).
	 * Con commit, el primero hace checkout de esa versión.
	 */
	function reproCommands({ framework = "", key = "", name = "", commit = "" } = {}) {
		const fw = lower(framework);
		const out = [];
		if (!key || key.startsWith("name:")) return out;
		if (fw === "pytest") {
			out.push({ label: "pytest", cmd: `pytest ${shq(key)}` });
		} else if (fw === "playwright") {
			// "tests/login.spec.js > Login > admin entra [chromium]"
			const m = key.match(/^(.*?) > (.*?)(?: \[([^\]]+)\])?$/);
			if (m) {
				const title = m[2].split(" > ").pop();
				out.push({ label: "Playwright", cmd: `npx playwright test ${shq(m[1])} -g ${shq(`^${reEsc(title)}$`)}${m[3] ? ` --project=${shq(m[3])}` : ""}` });
			}
		} else if (["junit5", "junit", "maven", "gradle", "testng"].includes(fw)) {
			// "com.acme.LoginTest#adminEntra(String)" o "com.acme.LoginTest#loginOk"
			const m = key.match(/^([\w.$]+)#([\w$]+)/);
			if (m) {
				const cls = m[1].split(".").pop();
				out.push({ label: "Maven", cmd: `mvn test -Dtest=${shq(`${cls}#${m[2]}`)}` });
				out.push({ label: "Gradle", cmd: `./gradlew test --tests ${shq(`${m[1]}.${m[2]}`)}` });
			}
		} else if (fw === "go") {
			// "shop/checkout/TestPay/visa": paquete hasta el primer Test*
			const parts = key.split("/");
			const i = parts.findIndex((p) => /^Test/.test(p));
			if (i >= 0) {
				const pkg = parts.slice(0, i).join("/");
				const run = parts.slice(i).map((p) => `^${reEsc(p)}$`).join("/");
				out.push({ label: "Go", cmd: `go test ${shArg(`./${pkg}${pkg ? "/" : ""}...`)} -run ${shq(run)}` });
			}
		}
		// solo un commit de Git válido: también protege lo guardado por versiones anteriores
		const c = normalizeCommit(commit).slice(0, 12);
		if (c && out.length) {
			for (const o of out) o.cmd = `git checkout ${c} && ${o.cmd}`;
		}
		return out;
	}

	window.TraceReportsFeatures = { copyText, download, Drawer, sparkline, TimeTravelPlayer, LiveStream, MockGenerator, curlOf, reproCommands, diffCalls, jsonDiff, maskValue, esc, Tips };
})();
