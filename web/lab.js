/* TraceReports — FixLab: Request Lab.
 *
 * Reenvía una llamada capturada para ver si el fallo sigue ocurriendo. El servidor arma el plan
 * (GET /api/v1/network/{id}/lab): qué valores enmascarados hacen falta y cuáles consigue solo
 * (sesión nueva desde el login grabado, token CSRF de la página). Esta pantalla pide los que
 * escribe la persona, deja editar la petición, la envía (POST /api/v1/ui/lab/send) y muestra el
 * original junto a la respuesta de ahora con un veredicto. Ver docs/rfc/fixlab.md.
 */
(() => {
	"use strict";

	const CF = window.TraceReportsFeatures;
	const esc = CF.esc;
	const tr = (s, vars) => (window.TraceReportsI18n ? window.TraceReportsI18n.t(s, vars) : s);
	const icon = (id) => `<svg aria-hidden="true"><use href="#${id}"/></svg>`;
	const VAR_RE = /\{\{([A-Za-z0-9_.\-[\]]+)\}\}/g;

	// ─── lógica pura (con tests en test/web/lab.test.js) ─────────────────────
	/** "k: v" por línea → objeto (las líneas sin ":" se ignoran). */
	function parseHeaders(text) {
		const out = {};
		for (const line of String(text || "").split(/\r?\n/)) {
			const i = line.indexOf(":");
			if (i <= 0) continue;
			const k = line.slice(0, i).trim();
			if (k) out[k] = line.slice(i + 1).trim();
		}
		return out;
	}

	const headersText = (h) => Object.entries(h || {}).map(([k, v]) => `${k}: ${v}`).join("\n");

	/** Nombre de variable de entorno para un {{nombre}}: _token → TOKEN, x-api-key → X_API_KEY. */
	const envName = (n) => String(n).replace(/[^A-Za-z0-9]+/g, "_").replace(/^_+|_+$/g, "").toUpperCase() || "VALUE";

	const shq = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`;

	/** Texto para la shell donde cada {{variable}} se expande desde el entorno ($VARIABLE). */
	function shellWithVars(s) {
		const parts = [];
		let last = 0;
		for (const m of String(s).matchAll(VAR_RE)) {
			if (m.index > last) parts.push(shq(s.slice(last, m.index)));
			parts.push(`"$${envName(m[1])}"`);
			last = m.index + m[0].length;
		}
		if (last < s.length || !parts.length) parts.push(shq(s.slice(last)));
		return parts.join("");
	}

	/** cURL de una petición del Lab: las variables quedan como $VARIABLES, nunca con su valor. */
	function labCurl(req) {
		const method = String(req.method || "GET").toUpperCase();
		const lines = [`curl -X ${/^[A-Z]+$/.test(method) ? method : shq(method)} ${shellWithVars(req.url || "")}`];
		for (const [k, v] of Object.entries(req.headers || {})) {
			if (k.startsWith(":") || /^(host|content-length|connection)$/i.test(k)) continue;
			lines.push(`-H ${shellWithVars(`${k}: ${v}`)}`);
		}
		if (req.body) lines.push(`--data-raw ${shellWithVars(req.body)}`);
		const vars = [...new Set([...JSON.stringify(req).matchAll(VAR_RE)].map((m) => envName(m[1])))];
		const head = vars.length ? `# ${tr("Define antes:")} ${vars.map((v) => `${v}=…`).join(" ")}\n` : "";
		return head + lines.join(" \\\n  ");
	}

	const VERDICTS = {
		reproduced: { cls: "bad", title: "Se reproduce", text: "Falla igual que en la ejecución: el problema sigue ahí." },
		fixed: { cls: "good", title: "Ya no falla", text: "El backend responde bien ahora. Si nadie lo arregló, el fallo fue transitorio o del entorno." },
		session: { cls: "warn", title: "Falta sesión", text: "El backend pidió login (401/403 o redirección al login). Revisa la contraseña o escribe el header a mano." },
		different: { cls: "warn", title: "Otro error", text: "Responde con un error distinto al de la ejecución." },
		network: { cls: "warn", title: "Sin conexión", text: "No se pudo conectar con el backend desde el servidor de TraceReports." },
		ok: { cls: "good", title: "Responde bien", text: "La llamada original tampoco había fallado." },
	};

	/** Título, texto y estilo del veredicto; avisa si la respuesta original parecía simulada. */
	function verdictInfo(verdict, original = {}) {
		const v = VERDICTS[verdict] || VERDICTS.different;
		let text = tr(v.text);
		if (original.simulated && (verdict === "fixed" || verdict === "ok")) {
			text += " " + tr("La respuesta original parece simulada por el propio test: el fallo no venía del backend.");
		}
		return { cls: v.cls, title: tr(v.title), text };
	}

	const statusOf = (o) => (o.failed || !o.status ? `${tr("sin respuesta")}${o.error ? ` (${o.error})` : ""}` : String(o.status));

	// ─── pantalla ────────────────────────────────────────────────────────────
	const REASONS = {
		remote: "Por seguridad, sin login solo se puede enviar desde el equipo del servidor. Copia el cURL y pruébalo desde tu terminal.",
		login: "Inicia sesión en la interfaz para enviar desde aquí, o copia el cURL.",
		ingest_token: "El token de ingesta no puede enviar peticiones desde el Lab. Copia el cURL.",
		disabled: "El Lab está desactivado en este servidor (TRACEREPORTS_LAB=off). Copia el cURL.",
	};
	const KIND_NOTE = { session: "automático: sesión nueva", csrf: "automático: se lee de la página" };
	const ROLE = { csrf: "Página", login: "Login", target: "Llamada" };
	const isSecretName = (n) => /pass|pwd|clave|contrase|secret|token|key|auth|cookie/i.test(n);

	function pretty(body) {
		if (!body) return "";
		try { return JSON.stringify(JSON.parse(body), null, 2); } catch { return body; }
	}

	function block(title, status, body, cls = "") {
		const text = pretty(body);
		return `<div class="lab-col ${cls}"><div class="lab-col-head">${esc(title)} <span class="lab-status">${esc(status)}</span></div>
			${text ? `<pre class="cf-code lab-body" data-no-i18n>${esc(text.length > 20000 ? text.slice(0, 20000) + "\n…" : text)}</pre>` : `<p class="lab-empty">${tr("Sin body")}</p>`}</div>`;
	}

	async function open(connId, { subtitle = "", onChange = null } = {}) {
		const wrap = document.createElement("div");
		wrap.className = "lab";
		wrap.innerHTML = `<div class="placeholder">${tr("Preparando el Lab…")}</div>`;
		CF.Drawer.open({ title: tr("Lab: reenviar la llamada"), subtitle, body: wrap });
		let data;
		try {
			const res = await fetch(`/api/v1/network/${connId}/lab`, { headers: { Accept: "application/json" } });
			data = await res.json();
			if (!res.ok) throw new Error(data.error || res.status);
		} catch (err) {
			wrap.innerHTML = `<div class="placeholder">${tr("No se pudo preparar el Lab: {e}", { e: esc(err.message) })}</div>`;
			return;
		}
		render(wrap, data, onChange);
	}

	function render(wrap, data, onChange) {
		const p = data.plan;
		const t = p.target;
		const o = p.original;
		let origin = "";
		try { origin = new URL(t.url).origin; } catch { /* URL relativa */ }
		const secrets = p.vars.filter((v) => v.kind === "secret");
		const autos = p.vars.filter((v) => v.kind !== "secret");
		const where = (v) => v.where.map((w) => w.replace(/^login /, tr("login") + " · ")).join(", ");
		const required = (v) => v.kind === "secret" && !v.optional;
		const input = (v) => `<label class="lab-var"><span><code data-no-i18n>${esc(v.name)}</code>${required(v) ? "" : ` <small>${tr("opcional")}</small>`}</span>
			<input data-lab-var="${esc(v.name)}" ${required(v) ? "data-lab-required" : ""} type="${isSecretName(v.name) ? "password" : "text"}" autocomplete="off" spellcheck="false" placeholder="${esc(tr("Escribe el valor"))}">
			<small class="lab-where" data-no-i18n>${esc(where(v))}</small></label>`;
		const flow = [];
		if (p.csrf) {
			flow.push(`<li>${tr("Abre la página para obtener una sesión nueva")}${p.csrf.fields.length ? ` ${tr("y el token CSRF")} (<code data-no-i18n>${esc(p.csrf.fields.join(", "))}</code>)` : ""}: <code data-no-i18n>${esc(p.csrf.page_url)}</code></li>`);
		}
		if (p.login) {
			flow.push(`<li>${tr("Repite el login grabado")} <code data-no-i18n>${esc(p.login.request.method)} ${esc(p.login.request.url)}</code> → ${p.login.token_path ? tr("token de acceso (Bearer)") : tr("cookies de sesión")}</li>`);
		}
		flow.push(`<li>${tr("Envía la llamada")}</li>`);
		const hosts = data.any_host ? "" : `<p class="lab-hint">${tr("Servidor compartido: el Lab solo puede llamar a los hosts que usó el test")}${data.lab_hosts?.length ? ` ${tr("y a")} <code data-no-i18n>${esc(data.lab_hosts.join(", "))}</code>` : ""} (TRACEREPORTS_LAB_HOSTS).</p>`;

		wrap.innerHTML = `
			<div class="cf-mock-meta"><span class="nbadge cf-method">${esc(t.method)}</span><code data-no-i18n>${esc(t.url)}</code>
				<span class="cf-status-pill" title="${esc(tr("Lo que obtuvo en la ejecución"))}">${esc(statusOf(o))}</span></div>
			${o.simulated ? `<p class="lab-note lab-warn">${icon("i-warning")}<span>${tr("La respuesta original no trae los headers date ni server: probablemente la fabricó el propio test (mock o route.fulfill).")}</span></p>` : ""}
			<section class="lab-sec"><h3>${tr("Cómo se reenvía")}</h3><ol class="lab-flow">${flow.join("")}</ol></section>
			<section class="lab-sec"><h3>${tr("Valores")}</h3>
				${secrets.map(input).join("")}
				${autos.map((v) => `<div class="lab-auto" data-lab-auto="${esc(v.name)}"><code data-no-i18n>${esc(v.name)}</code><span class="lab-chip">${tr(KIND_NOTE[v.kind] || v.kind)}</span>
					<button class="cf-btn cf-btn-sm" data-lab-manual="${esc(v.name)}">${tr("Escribir a mano")}</button></div>`).join("")}
				${p.vars.length ? `<p class="lab-hint">${tr("Los valores se usan solo para este envío: no se guardan.")}</p>` : `<p class="lab-hint">${tr("Esta llamada no tiene datos enmascarados: se reenvía tal cual.")}</p>`}
			</section>
			<section class="lab-sec"><h3>${tr("Entorno")}</h3>
				<label class="lab-var"><span>${tr("Enviar a")}</span><input data-lab-base type="url" spellcheck="false" placeholder="${esc(origin)}"></label>
				<p class="lab-hint">${tr("Vacío: el mismo host de la ejecución. Para probar tu arreglo en local, por ejemplo http://localhost:3000.")}</p>${hosts}
			</section>
			<details class="lab-sec lab-edit"><summary>${tr("Editar la petición")}</summary>
				<div class="lab-edit-row"><input data-lab-method value="${esc(t.method)}" aria-label="${esc(tr("Método"))}" spellcheck="false"><input data-lab-url value="${esc(t.url)}" aria-label="URL" spellcheck="false"></div>
				<label class="lab-var"><span>${tr("Headers")}</span><textarea data-lab-headers rows="5" spellcheck="false">${esc(headersText(t.headers))}</textarea></label>
				<label class="lab-var"><span>${tr("Body")}</span><textarea data-lab-body rows="5" spellcheck="false">${esc(t.body || "")}</textarea></label>
				<p class="lab-hint">${tr("Las {{variables}} se completan con los valores de arriba al enviar.")}</p>
				<button class="cf-btn cf-btn-sm" data-lab-reset>${tr("Restaurar la original")}</button>
			</details>
			<div class="cf-actions">
				<button class="cf-btn cf-btn-primary" data-lab-send ${data.can_send ? "" : "disabled"}>${icon("i-play")}${tr("Enviar")}</button>
				<button class="cf-btn" data-lab-curl>${icon("i-copy")}${tr("Copiar cURL")}</button>
			</div>
			${data.can_send ? "" : `<p class="lab-note">${tr(REASONS[data.reason] || REASONS.remote)}</p>`}
			<div class="lab-result" aria-live="polite"></div>`;

		let edited = false;
		let lastSent = null; // lo que se envió la última vez: lo que guarda "Guardar como check"
		const $ = (sel) => wrap.querySelector(sel);
		const current = () => ({
			method: $("[data-lab-method]").value.trim() || "GET", url: $("[data-lab-url]").value.trim(),
			headers: parseHeaders($("[data-lab-headers]").value), body: $("[data-lab-body]").value,
		});
		const values = () => Object.fromEntries([...wrap.querySelectorAll("[data-lab-var]")].map((i) => [i.dataset.labVar, i.value]).filter(([, v]) => v !== ""));

		wrap.addEventListener("input", (e) => {
			if (e.target.closest(".lab-edit")) edited = true;
			e.target.classList.remove("lab-missing");
		});
		wrap.addEventListener("click", async (e) => {
			const save = e.target.closest("[data-check-save]");
			if (save && lastSent) {
				const box = save.closest(".lab-save");
				const msg = box.querySelector("[data-check-msg]");
				save.disabled = true;
				try {
					const status = Number(box.querySelector("[data-check-status]").value) || 0;
					const contains = box.querySelector("[data-check-contains]").value.split(/\r?\n/).map((x) => x.trim()).filter(Boolean);
					await post("/api/v1/ui/lab/checks", { conn_id: p.conn_id, name: box.querySelector("[data-check-name]").value.trim(), ...lastSent, expect: { status, contains } });
					msg.className = "lab-note lab-ok";
					msg.textContent = tr("Check guardado: se corre al pulsar Verificar en el incidente.");
					onChange?.();
				} catch (err) {
					msg.className = "lab-note lab-warn";
					msg.textContent = err.message;
				} finally {
					save.disabled = false;
				}
				return;
			}
			const manual = e.target.closest("[data-lab-manual]");
			if (manual) {
				const v = p.vars.find((x) => x.name === manual.dataset.labManual);
				const row = manual.closest("[data-lab-auto]");
				row.outerHTML = input(v);
				wrap.querySelector(`[data-lab-var="${CSS.escape(v.name)}"]`)?.focus();
				return;
			}
			if (e.target.closest("[data-lab-reset]")) {
				$("[data-lab-method]").value = t.method; $("[data-lab-url]").value = t.url;
				$("[data-lab-headers]").value = headersText(t.headers); $("[data-lab-body]").value = t.body || "";
				edited = false;
				return;
			}
			if (e.target.closest("[data-lab-curl]")) {
				const req = current();
				const base = $("[data-lab-base]").value.trim();
				if (base && origin) req.url = req.url.replace(origin, base.replace(/\/+$/, ""));
				CF.copyText(labCurl(req), e.target.closest("button"));
				return;
			}
			const send = e.target.closest("[data-lab-send]");
			if (!send || send.disabled) return;
			const out = $(".lab-result");
			// lo obligatorio se revisa aquí (el servidor lo vuelve a revisar): sin ida y vuelta
			const empty = [...wrap.querySelectorAll("[data-lab-required]")].filter((i) => !i.value);
			if (empty.length) {
				empty.forEach((i) => i.classList.add("lab-missing"));
				empty[0].focus();
				out.innerHTML = `<p class="lab-note lab-warn">${icon("i-warning")}<span>${tr("Falta completar: {v}", { v: esc(empty.map((i) => i.dataset.labVar).join(", ")) })}</span></p>`;
				return;
			}
			send.disabled = true;
			out.innerHTML = `<div class="placeholder">${tr("Enviando…")}</div>`;
			try {
				const res = await fetch("/api/v1/ui/lab/send", {
					method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" },
					body: JSON.stringify({ conn_id: p.conn_id, values: values(), base_url: $("[data-lab-base]").value.trim(), request: edited ? current() : null }),
				});
				const r = await res.json().catch(() => ({}));
				if (!res.ok) {
					for (const n of r.missing || []) wrap.querySelector(`[data-lab-var="${CSS.escape(n)}"]`)?.classList.add("lab-missing");
					out.innerHTML = `<p class="lab-note lab-warn">${icon("i-warning")}<span>${r.missing ? tr("Falta completar: {v}", { v: esc(r.missing.join(", ")) }) : esc(r.error || res.status)}</span></p>`;
					return;
				}
				out.innerHTML = result(r, o) + saveForm(t, r);
				lastSent = { request: edited ? current() : null, base_url: $("[data-lab-base]").value.trim() };
			} catch (err) {
				out.innerHTML = `<p class="lab-note lab-warn">${esc(err.message)}</p>`;
			} finally {
				send.disabled = false;
			}
		});
	}

	function result(r, o) {
		const v = verdictInfo(r.verdict, o);
		const steps = r.steps.map((s) => `<li class="${s.error || s.status >= 400 ? "lab-step-err" : ""}">
			<span class="lab-role">${tr(ROLE[s.role] || s.role)}</span>
			<code data-no-i18n>${esc(s.method)} ${esc(s.url)}</code>
			<span class="lab-status">${esc(s.error ? s.error : s.status)}${s.location ? ` → <span data-no-i18n>${esc(s.location)}</span>` : ""}</span>
			<span class="lab-ms">${s.duration_ms} ms</span>
			${s.note ? `<small class="lab-step-note" data-no-i18n>${esc(s.note)}</small>` : ""}</li>`).join("");
		const now = r.response;
		return `<div class="lab-verdict lab-${v.cls}" data-verdict="${esc(r.verdict)}"><b>${esc(v.title)}</b><p>${esc(v.text)}</p></div>
			<ol class="lab-steps">${steps}</ol>
			<div class="lab-compare">
				${block(tr("En la ejecución"), statusOf(o), o.body, "lab-before")}
				${block(tr("Ahora"), now.error ? `${tr("sin respuesta")} (${now.error})` : String(now.status), now.body + (now.truncated ? "\n…" : ""), "lab-after")}
			</div>`;
	}

	async function post(path, body) {
		const res = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(body) });
		const data = await res.json().catch(() => ({}));
		if (!res.ok) {
			const err = new Error(data.missing ? tr("Falta completar: {v}", { v: data.missing.join(", ") }) : data.error || String(res.status));
			err.missing = data.missing;
			throw err;
		}
		return data;
	}

	function pathOf(url) {
		try { const u = new URL(url); return u.pathname + u.search; } catch { return url; }
	}

	/** Formulario "Guardar como check", con lo que respondió ahora como punto de partida. */
	function saveForm(t, r) {
		const status = r.response.status && r.response.status < 400 ? r.response.status : 200;
		return `<details class="lab-save"><summary class="cf-btn cf-btn-sm">${icon("i-check")}${tr("Guardar como check")}</summary>
			<p class="lab-hint">${tr("Un check es esta llamada más la respuesta que cuenta como buena. Verificar el incidente corre todos sus checks.")}</p>
			<label class="lab-var"><span>${tr("Nombre")}</span><input data-check-name value="${esc(`${t.method} ${pathOf(t.url)}`)}" spellcheck="false"></label>
			<label class="lab-var"><span>${tr("Status esperado")}</span><input data-check-status type="number" min="100" max="599" value="${status}"></label>
			<label class="lab-var"><span>${tr("La respuesta debe contener (uno por línea, opcional)")}</span><textarea data-check-contains rows="2" spellcheck="false"></textarea></label>
			<button class="cf-btn cf-btn-primary cf-btn-sm" data-check-save>${tr("Guardar check")}</button>
			<p data-check-msg aria-live="polite"></p></details>`;
	}

	/** Lo que espera un check, en palabras: "status 200 · contiene «orders»". */
	function expectText(x) {
		const e = (typeof x === "string" ? JSON.parse(x) : x) || {};
		const parts = [e.status ? tr("status {s}", { s: e.status }) : tr("status menor a 400")];
		for (const c of e.contains || []) parts.push(tr("contiene «{t}»", { t: c }));
		return parts.join(" · ");
	}

	const failureText = (f) => ({
		status: tr("status {got} (se esperaba {want})", { got: f.got, want: f.want }),
		contains: tr("no contiene «{t}»", { t: f.want }),
		network: tr("sin respuesta: {e}", { e: f.got || "" }),
	}[f.kind] || f.kind);

	/**
	 * Verificar un incidente: corre sus checks y deja constancia de si quedó resuelto.
	 * opts: {runId, incidentKey, title, checks: [{id, name, expect, needs, last_ok}], canSend, onDone}
	 */
	function verify(opts) {
		const wrap = document.createElement("div");
		wrap.className = "lab lab-verify";
		let checks = opts.checks.slice();
		const draw = () => {
			const needs = [...new Set(checks.flatMap((c) => c.needs || []))];
			wrap.innerHTML = `<p class="lab-hint">${tr("Corre los checks guardados de este incidente contra el backend y guarda el resultado con el antes y el después.")}</p>
				<ul class="lab-checks">${checks.map((c) => `<li data-check="${c.id}">
					<span class="lab-check-state ${c.last_ok == null ? "" : c.last_ok ? "ok" : "bad"}">${c.last_ok == null ? "·" : c.last_ok ? "✓" : "✗"}</span>
					<div><b data-no-i18n>${esc(c.name)}</b><small>${esc(expectText(c.expect))}</small></div>
					<button class="cf-icon-btn" data-check-del="${c.id}" aria-label="${esc(tr("Borrar check"))}" data-tip="${esc(tr("Borrar check"))}">${icon("i-close")}</button></li>`).join("")}</ul>
				${needs.map((n) => `<label class="lab-var"><span><code data-no-i18n>${esc(n)}</code></span><input data-lab-var="${esc(n)}" data-lab-required type="${isSecretName(n) ? "password" : "text"}" autocomplete="off" spellcheck="false" placeholder="${esc(tr("Escribe el valor"))}"></label>`).join("")}
				<label class="lab-var"><span>${tr("Enviar a")}</span><input data-lab-base type="url" spellcheck="false" placeholder="${esc(tr("el mismo host de la ejecución"))}"></label>
				<div class="cf-actions"><button class="cf-btn cf-btn-primary" data-verify-run ${opts.canSend && checks.length ? "" : "disabled"}>${icon("i-check")}${tr("Verificar")}</button></div>
				${opts.canSend ? "" : `<p class="lab-note">${tr(REASONS.remote)}</p>`}
				<div class="lab-result" aria-live="polite"></div>`;
		};
		draw();
		wrap.addEventListener("input", (e) => e.target.classList.remove("lab-missing"));
		wrap.addEventListener("click", async (e) => {
			const del = e.target.closest("[data-check-del]");
			if (del) {
				const id = Number(del.dataset.checkDel);
				const res = await fetch(`/api/v1/ui/lab/checks/${id}`, { method: "DELETE", headers: { "Content-Type": "application/json" }, body: "{}" });
				if (res.ok || res.status === 404) { checks = checks.filter((c) => c.id !== id); draw(); opts.onDone?.(); }
				return;
			}
			const run = e.target.closest("[data-verify-run]");
			if (!run || run.disabled) return;
			const out = wrap.querySelector(".lab-result");
			const empty = [...wrap.querySelectorAll("[data-lab-required]")].filter((i) => !i.value);
			if (empty.length) {
				empty.forEach((i) => i.classList.add("lab-missing"));
				empty[0].focus();
				out.innerHTML = `<p class="lab-note lab-warn">${icon("i-warning")}<span>${tr("Falta completar: {v}", { v: esc(empty.map((i) => i.dataset.labVar).join(", ")) })}</span></p>`;
				return;
			}
			run.disabled = true;
			out.innerHTML = `<div class="placeholder">${tr("Verificando…")}</div>`;
			try {
				const values = Object.fromEntries([...wrap.querySelectorAll("[data-lab-var]")].map((i) => [i.dataset.labVar, i.value]));
				const v = await post("/api/v1/ui/lab/verify", { run_id: opts.runId, incident_key: opts.incidentKey, check_ids: checks.map((c) => c.id), values, base_url: wrap.querySelector("[data-lab-base]").value.trim() });
				out.innerHTML = verification(v);
				const byId = Object.fromEntries((typeof v.results === "string" ? JSON.parse(v.results) : v.results || []).map((x) => [x.check_id, x.ok]));
				checks = checks.map((c) => (c.id in byId ? { ...c, last_ok: byId[c.id] } : c));
				wrap.querySelectorAll("[data-check]").forEach((li) => {
					const ok = byId[li.dataset.check];
					if (ok == null) return;
					const st = li.querySelector(".lab-check-state");
					st.className = `lab-check-state ${ok ? "ok" : "bad"}`;
					st.textContent = ok ? "✓" : "✗";
				});
				opts.onDone?.();
			} catch (err) {
				out.innerHTML = `<p class="lab-note lab-warn">${icon("i-warning")}<span>${esc(err.message)}</span></p>`;
			} finally {
				run.disabled = false;
			}
		});
		CF.Drawer.open({ title: tr("Verificar el arreglo"), subtitle: opts.title || "", body: wrap });
	}

	/** Resultado de una verificación: resuelto o no, y cada check con su antes y su ahora. */
	function verification(v) {
		const results = typeof v.results === "string" ? JSON.parse(v.results) : v.results || [];
		const bad = results.filter((x) => !x.ok).length;
		return `<div class="lab-verdict ${v.ok ? "lab-good" : "lab-bad"}" data-verified="${v.ok ? "ok" : "fail"}"><b>${v.ok ? tr("Resuelto y verificado") : tr("Sigue fallando")}</b>
			<p>${v.ok ? (results.length === 1 ? tr("El check pasa. Queda registrado en el incidente con el antes y el después.") : tr("Los {n} checks pasan. Queda registrado en el incidente con el antes y el después.", { n: results.length })) : tr("{b} de {n} checks no pasan.", { b: bad, n: results.length })}</p></div>
			<ol class="lab-steps">${results.map((x) => `<li class="${x.ok ? "" : "lab-step-err"}"><span class="lab-role">${x.ok ? "✓" : "✗"}</span>
				<b data-no-i18n>${esc(x.name)}</b><span class="lab-status">${esc(x.before)} → ${esc(x.status || tr("sin respuesta"))}</span>
				${(x.failures || []).map((f) => `<small class="lab-step-note">${esc(failureText(f))}</small>`).join("")}</li>`).join("")}</ol>`;
	}

	window.TraceReportsLab = { open, verify, verification, labCurl, parseHeaders, headersText, verdictInfo, envName, expectText, failureText };
})();
