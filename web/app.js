/* TraceReports frontend — vanilla JS, no build step. */
(() => {
	"use strict";

	const POLL_MS = 3000;
	const RUNS_POLL_MS = 10000;
	const STATUS_ICON = { PASS: "i-pass", FAIL: "i-fail", WARNING: "i-warning", SKIP: "i-skip", INFO: "i-info", RUNNING: "i-running", INTERRUPTED: "i-interrupted" };
	const AI_LABEL = {
		LOCATOR_CHANGED: "Locator cambiado",
		BACKEND_TIMEOUT: "Timeout de backend",
		LOGIC_BUG: "Bug de lógica",
		INFRA_ERROR: "Error de infraestructura",
		TIMING: "Espera insuficiente",
	};

	const S = {
		config: { ai_enabled: false, ai_model: "" },
		runs: [], runId: null, run: null,
		view: "tests", testId: null, test: null,
		status: "", search: "", stepFilter: "",
		detailTab: "steps", net: { key: null, list: null }, netFilter: "", netSearch: "", netApiOnly: false,
		hist: {}, histGen: 0, liveView: { list: [], exec: null, key: "", now: 0, no_signal_ms: 0, loaded: false, ui: liveUI(), panel: null }, insights: { key: null }, tlAll: false, epAll: false,
		loc: {}, locLang: "", locTool: "", replay: { testId: null, index: 0, speed: 1 }, player: null,
		liveState: "off", autoScroll: true, activity: {}, newRuns: [], newRunInfo: {}, notify: window.TraceReportsRunNotify?.load() || { until: 0, muted: {} },
		catSel: null, excSel: null,
		metrics: { days: 30, suite: "", env: "", tag: "", custom: null, data: null, loading: false, error: null, tests: {} },
		recents: { q: "", tab: "" },
		maint: { state: "", project: "", environment: "", branch: "", older: "", data: null, facets: null, loading: false, error: null,
			selected: new Set(), action: null, msg: null, busy: false,
			// almacenamiento: uso del disco, retención y "Limpiar ahora" (con vista previa)
			storage: null, storageError: null, clean: null },
		runSearch: { q: "", project: "", environment: "", branch: "", tag: "", status: "", from: "", to: "", incomplete: false, flaky: false, sort: "recent", items: [], cursor: "", total: 0, loading: false, error: null, selected: 0, compare: [], facets: {}, timer: 0, newCount: 0 },
		aiv: { rec: {}, recKey: null, busy: false, msg: null },
		esc: { test: 0, audience: "business", lang: window.TraceReportsI18n.lang, noAI: null, data: null, loading: false, msg: null, loadedKey: null, evidence: 0, stale: false },
		settings: { data: null, form: null, msg: null, busy: false, error: null, loading: false,
			usage: null, usageError: null, usageLoading: false, status: null, statusLoading: false,
			gen: {}, snips: {}, connTab: "ps", check: null, checkInput: null, checking: false,
			tab: (() => { try { return localStorage.getItem("tracereports-settings-tab") || "look"; } catch { return "look"; } })() },
		charts: {}, rendered: {},
	};

	const $ = (sel, root = document) => root.querySelector(sel);
	const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

	// ---------- utils ----------
	const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
	const pad = (n, w = 2) => String(n).padStart(w, "0");
	const fmtDateTime = (ms) => {
		if (!ms) return "—";
		const d = new Date(ms);
		return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
	};
	const fmtTime = (ms) => { const d = new Date(ms); return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`; };
	// duración legible: 0h 0m 9s+178ms
	const fmtDuration = (ms) => {
		if (ms == null || ms < 0) return "—";
		const h = Math.floor(ms / 3600000), m = Math.floor(ms / 60000) % 60, s = Math.floor(ms / 1000) % 60;
		return `${h}h ${m}m ${s}s+${ms % 1000}ms`;
	};
	const durationOf = (o) => (o.ended_at ?? Date.now()) - o.started_at;
	const lower = (s) => String(s || "").toLowerCase();
	const tagsOf = (t) => (t.category || "").split(",").map((x) => x.trim()).filter(Boolean);
	const icon = (id) => `<svg><use href="#${id}"/></svg>`;
	const statusLabel = (st, kind = "outline") => `<span class="label ${kind} ${lower(st)}">${esc(lower(st))}</span>`;

	// Exported ZIP: data.js defines window.TRACEREPORTS_STATIC and the report runs without a server.
	const STATIC = window.TRACEREPORTS_STATIC || null;

	// ---------- sesión (la página de ingreso, /login/) ----------
	// Toda llamada a la API dice que viene de la UI (así un 401 no abre el cuadro de login del
	// navegador) y, si nadie tocó la página en un minuto, que es automática: una pestaña olvidada no
	// mantiene viva la sesión. Un 401 por falta de login o sesión vencida lleva a /login/ y vuelve aquí.
	const SESSION = { lastInput: Date.now(), leaving: false, info: null };
	if (!STATIC) {
		for (const ev of ["pointerdown", "keydown", "wheel", "touchstart"]) addEventListener(ev, () => { SESSION.lastInput = Date.now(); }, { passive: true, capture: true });
		const nativeFetch = window.fetch.bind(window);
		window.fetch = async (input, init = {}) => {
			const url = typeof input === "string" ? input : input?.url || "";
			const path = url.startsWith(location.origin) ? url.slice(location.origin.length) : url;
			if (!path.startsWith("/api/")) return nativeFetch(input, init);
			const headers = new Headers(init.headers || (typeof input === "string" ? undefined : input.headers));
			headers.set("X-TraceReports-UI", "1");
			if (Date.now() - SESSION.lastInput > 60_000) headers.set("X-TraceReports-Passive", "1");
			const res = await nativeFetch(input, { ...init, headers });
			if (res.status === 401 && !path.startsWith("/api/v1/auth/")) toLogin(res);
			return res;
		};
	}
	async function toLogin(res) {
		if (SESSION.leaving) return;
		let msg = "";
		try { msg = (await res.clone().json()).error || ""; } catch { /* sin cuerpo */ }
		// un token que falta para escribir resultados no es falta de login
		if (msg !== "authentication required" && msg !== "session expired") return;
		SESSION.leaving = true;
		const next = location.pathname + location.search + location.hash;
		location.href = `/login/?next=${encodeURIComponent(next)}${msg === "session expired" ? "&expired=1" : ""}`;
	}

	function staticApi(path) {
		const p = path.split("?")[0];
		let m;
		if (p === "/api/v1/config") return STATIC.config;
		if (p === "/api/v1/runs") return [STATIC.run];
		if ((m = p.match(/^\/api\/v1\/runs\/(\d+)$/))) return STATIC.run;
		if ((m = p.match(/^\/api\/v1\/tests\/(\d+)$/)) && STATIC.tests[m[1]]) return STATIC.tests[m[1]];
		if ((m = p.match(/^\/api\/v1\/tests\/(\d+)\/history$/))) return STATIC.history?.[m[1]] || [];
		if (/^\/api\/v1\/runs\/\d+\/compare$/.test(p)) return STATIC.compare || {};
		if (/^\/api\/v1\/runs\/\d+\/endpoints$/.test(p)) return STATIC.endpoints || [];
		if ((m = p.match(/^\/api\/v1\/tests\/(\d+)\/drift$/))) return STATIC.drifts?.[m[1]] || [];
		if (/^\/api\/v1\/runs\/\d+\/release$/.test(p) && STATIC.release) return STATIC.release;
		throw new Error(`no disponible en el reporte exportado: ${path}`);
	}

	async function api(path, timeoutMs = 0) {
		if (STATIC) return staticApi(path);
		// con tiempo límite: un pedido que el servidor no contesta no queda colgado para siempre
		const ctl = timeoutMs ? new AbortController() : null, timer = ctl && setTimeout(() => ctl.abort(), timeoutMs);
		let res;
		try {
			res = await fetch(path, { headers: { Accept: "application/json" }, signal: ctl?.signal });
		} catch (err) {
			if (ctl?.signal.aborted) throw new Error(tr("El servidor no respondió en {s} s", { s: Math.round(timeoutMs / 1000) }));
			throw err;
		} finally { if (timer) clearTimeout(timer); }
		if (!res.ok) {
			// un error del servidor con su causa o su referencia se explica; si no, el código y la ruta
			const data = res.status >= 500 ? await res.json().catch(() => ({})) : {};
			throw new Error(data.ref || data.kind ? apiErrorText(data, res.status) : `${res.status} ${path}`);
		}
		return res.json();
	}

	// ---------- routing (hash) ----------
	function readHash() {
		const p = new URLSearchParams(location.hash.slice(1));
		if (p.get("view") === "live" && S.view !== "live") S.liveView.anim = null; // al volver a En vivo se anima de nuevo
		if (p.get("view")) S.view = p.get("view");
		if (p.get("run")) S.runId = Number(p.get("run"));
		if (p.get("test")) S.testId = Number(p.get("test"));
		if (p.get("exec")) S.liveView.key = p.get("exec");
		// En vivo ya filtrado desde un enlace: &f=fail,warn (estados) y &lq= (búsqueda)
		if (p.get("view") === "live" && window.TraceReportsLive) {
			S.liveView.ui.states = window.TraceReportsLive.statesFromHash(p.get("f"));
			S.liveView.ui.q = p.get("lq") || "";
		}
		if (p.get("view") === "search") {
			const x = S.runSearch;
			["q", "project", "environment", "branch", "tag", "status", "from", "to", "sort"].forEach((k) => { x[k] = p.get(k) || (k === "sort" ? "recent" : ""); });
			x.incomplete = p.get("incomplete") === "true"; x.flaky = p.get("flaky") === "true";
		}
	}
	function writeHash() {
		// saliendo de En vivo hacia un test (doble clic en el campo): un refresco de la vista que llega justo
		// en ese momento no debe volver a escribir su propio enlace encima del del test
		if (S.view === "live" && Date.now() - (S.leavingLive || 0) < 2000) return;
		const p = new URLSearchParams();
		if (S.runId) p.set("run", S.runId);
		p.set("view", S.view);
		if (S.testId && S.view === "tests") p.set("test", S.testId);
		if (S.view === "live" && S.liveView.key) p.set("exec", S.liveView.key);
		if (S.view === "live" && window.TraceReportsLive) {
			const f = window.TraceReportsLive.statesToHash(S.liveView.ui.states);
			if (f) p.set("f", f);
			if (S.liveView.ui.q) p.set("lq", S.liveView.ui.q);
		}
		if (S.view === "search") {
			const x = S.runSearch;
			["q", "project", "environment", "branch", "tag", "status", "from", "to"].forEach((k) => { if (x[k]) p.set(k, x[k]); });
			if (x.incomplete) p.set("incomplete", "true"); if (x.flaky) p.set("flaky", "true"); if (x.sort !== "recent") p.set("sort", x.sort);
		}
		history.replaceState(null, "", "#" + p.toString());
	}

	// ---------- data loading ----------
	// Cada carga toma un turno: la respuesta (o el error) de una petición que ya tiene otra más nueva
	// del mismo tipo se descarta, así una ejecución o un test elegido antes no pisa al elegido después.
	const turns = {};
	function turn(name) {
		const n = (turns[name] || 0) + 1;
		turns[name] = n;
		return () => turns[name] === n;
	}

	// ---------- recientes ----------
	// Las últimas ejecuciones con lo que importa para elegir: cómo terminaron, cuántos tests pasaron,
	// dónde corrieron y hace cuánto. Se filtran por texto (#id, nombre, rama, proyecto) y por estado.

	/** Estado de una ejecución en palabras para la lista: en curso, sin actividad o su resultado. */
	function runStateLabel(r) {
		if (r.status === "RUNNING") {
			const idle = r.idle_ms >= IDLE_WARN_MS;
			return `<span class="run-state ${idle ? "idle" : "running"}">${icon(idle ? "i-pause" : "i-running")}${idle ? tr("Sin actividad") : tr("En curso")}</span>`;
		}
		return `<span class="run-state ${lower(r.status)}">${icon(STATUS_ICON[r.status] || "i-info")}${esc(r.status.charAt(0) + lower(r.status.slice(1)))}</span>`;
	}

	/** Barra de resultados de los tests (pasan, warning, fallan, otros) y "p/t pasan". */
	function runBar(r) {
		const t = r.total || 0;
		if (!t) return `<span class="run-bar-text">${tr("sin tests")}</span>`;
		const seg = (n, cls) => (n ? `<i class="${cls}" style="flex:${n}"></i>` : "");
		const other = t - r.passed - r.failed - (r.warning || 0);
		return `<span class="run-bar" aria-hidden="true">${seg(r.passed, "pass")}${seg(r.warning || 0, "warning")}${seg(r.failed, "fail")}${seg(other, "other")}</span>
			<span class="run-bar-text">${tr("{p}/{t} pasan", { p: r.passed, t })}</span>`;
	}

	function renderRecents() {
		const list = $("#recents-list");
		if (!list) return;
		const x = S.recents;
		const runs = S.runs.filter((r) => (!x.tab || (x.tab === "FAIL" ? r.failed > 0 || r.status === "FAIL" : r.status === x.tab))
			&& runMatches(r, x.q, [r.name, r.branch, r.project, r.environment, r.status]));
		const live = S.runs.filter((r) => r.status === "RUNNING").length;
		const pill = $("#recents-live");
		pill.hidden = !live;
		pill.textContent = live ? tr("{n} en curso", { n: live }) : "";
		$$("[data-recents-tab]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.recentsTab === x.tab)));
		list.innerHTML = runs.length ? runs.map((r) => runRow(r, `data-recent-run="${r.id}"`, r.id === S.runId)).join("")
			: `<p class="recents-empty">${tr("Ninguna de las últimas {n} ejecuciones coincide.", { n: S.runs.length })}</p>`;
	}

	/** Si una ejecución coincide con lo escrito: "#12" busca ese número exacto; cualquier otro texto
	 * (también "12" sin #, que puede ser parte de una fecha), en los campos dados. */
	function runMatches(r, query, fields) {
		const q = lower((query || "").trim());
		if (!q) return true;
		if (/^#\d+$/.test(q)) return String(r.id) === q.slice(1);
		return String(r.id) === q || fields.some((v) => lower(v || "").includes(q));
	}

	/** Una ejecución como fila elegible (Recientes, el selector de la comparación): estado, nombre,
	 * #id y contexto, barra de resultados, hace cuánto y cuánto duró. */
	function runRow(r, attrs, current, withDate = false) {
		// en el selector de la comparación todas son del mismo proyecto y ambiente: basta la rama
		const ctx = (withDate ? [r.branch] : [r.project, r.environment, r.branch]).filter(Boolean).map(esc).join(" · ");
		const dur = r.ended_at ? fmtDuration(r.ended_at - r.started_at) : "";
		// candidata de la comparación: cuántos tests comparte con la que se ve (0 = otra suite)
		const shared = r.shared === undefined ? "" : r.shared
			? `<span class="cmp-shared">${tr("{n} test(s) en común", { n: r.shared })}</span>` : `<span class="cmp-shared none">${tr("sin tests en común")}</span>`;
		return `<button type="button" role="option" class="recent-row ${current ? "current" : ""} ${r.shared === 0 ? "nocommon" : ""}" aria-selected="${!!current}" ${attrs}>
			<span class="recent-state">${runStateLabel(r)}</span>
			<span class="recent-main"><b data-no-i18n>${esc(r.name)}</b><small><span data-no-i18n>#${r.id}${ctx ? ` · ${ctx}` : ""}</span>${shared ? ` · ${shared}` : ""}</small></span>
			<span class="recent-side">${runBar(r)}<small>${withDate ? `${esc(fmtDateTime(r.started_at))} · ` : ""}${esc(relativeRun(r.started_at))}${dur ? ` · ${dur}` : ""}</small></span>
		</button>`;
	}

	async function loadRuns() {
		const current = turn("runs");
		let runs;
		try {
			runs = await api("/api/v1/runs?limit=10");
		} catch (err) {
			if (current()) throw err;
			return;
		}
		if (!current()) return;
		S.runs = runs;
		const sel = $("#run-select");
		sel.innerHTML = S.runs.map((r) =>
			`<option value="${r.id}">#${r.id} · ${esc(r.name)}${r.status === "RUNNING" ? (r.idle_ms >= IDLE_WARN_MS ? ` (${tr("sin actividad hace {t}", { t: fmtIdle(r.idle_ms) })})` : " (en curso)") : r.incomplete ? " (incompleta)" : ""}${r.branch ? ` · ${esc(r.branch)}` : ""} — ${fmtDateTime(r.started_at)}</option>`).join("");
		renderRecents();
		// A shared link can point to an older run which is no longer in the compact list.
		if (!S.runId) S.runId = S.runs[0]?.id ?? null;
		// al reconstruir las opciones el select muestra la primera (la más nueva): vuelve a la que se ve
		if (S.runId) sel.value = String(S.runId);
		$("#empty-state").hidden = S.runs.length > 0;
		$$(".view").forEach((v) => { if (!S.runs.length) v.hidden = true; });
	}

	async function loadRun() {
		if (!S.runId) return;
		const id = S.runId, current = turn("run");
		let run;
		try {
			run = await api(`/api/v1/runs/${id}`);
		} catch (err) {
			if (current() && id === S.runId) throw err;
			return;
		}
		if (!current() || id !== S.runId) return; // ya se eligió otra ejecución
		// otra ejecución, o cambió lo que se escala (los pasos de otros tests de la suite que sigue no cuentan)
		if (S.run && (S.run.id !== run.id || escEvidence(S.run) !== escEvidence(run))) invalidateEscalation();
		if (S.run?.id !== run.id || run.status !== "RUNNING") S.closeRun = null;
		S.run = run;
		S.runLoadedAt = Date.now();
		if (S.testId && !S.run.tests.some((t) => t.id === S.testId)) S.testId = null;
		if (!S.testId && S.run.tests.length) S.testId = S.run.tests[0].id;
		renderHeader();
		renderView();
	}

	async function loadTest() {
		const current = turn("test");
		if (!S.testId) { S.test = null; renderTestDetail(); return; }
		const id = S.testId;
		let test;
		try {
			test = await api(`/api/v1/tests/${id}`);
		} catch (err) {
			if (current() && id === S.testId) throw err;
			return;
		}
		if (!current() || id !== S.testId) return; // ya se eligió otro test
		test.logs ||= []; // un test recién iniciado llega sin "logs" (omitempty)
		if (S.test?.id === id && JSON.stringify(S.test) !== JSON.stringify(test) && (!S.esc.test || S.esc.test === id)) {
			invalidateEscalation();
			if (S.view === "escalate") renderEscalate();
		}
		S.test = test;
		renderTestDetail();
		buddyTestLoaded(test);
	}

	/** Por qué un test no tiene capturas: la política del servidor (TRACEREPORTS_SCREENSHOTS) las descartó
	 * antes de guardarlas. Sin esto, un test que pasó parece haber perdido sus fotos. */
	function shotPolicyText(t) {
		const p = S.config?.screenshots;
		if (STATIC || !p || p === "all" || t.logs.some((l) => l.screenshot)) return "";
		if (p === "off") return tr("Este servidor no guarda capturas (TRACEREPORTS_SCREENSHOTS=off).");
		if (t.status === "FAIL") return "";
		return tr("Este test no falló, y este servidor solo guarda las capturas de los tests y pasos que fallan (TRACEREPORTS_SCREENSHOTS=failures).");
	}
	function shotPolicyNote(t) {
		const msg = shotPolicyText(t);
		return msg ? `<p class="shot-policy-note">${icon("i-camera")}<span>${msg}</span></p>` : "";
	}

	// ---------- header ----------
	function renderHeader() {
		const r = S.run;
		if (S.view !== "live") $("#report-name").textContent = r.name; // en En vivo manda la ejecución (renderLiveView)
		if ($("#run-select").value !== String(r.id)) $("#run-select").value = String(r.id);
		if (S.view !== "live") $("#report-env").textContent = r.environment || "";
		$("#suite-started").textContent = fmtDateTime(r.started_at);
		renderLive();
		document.title = `${S.newRuns.length ? "● " : ""}${r.name} · TraceReports`;
		$("#export-li").hidden = !!STATIC;
		$("#export-btn").href = `/api/v1/runs/${r.id}/export`;
		// esta ejecución (y las demás corridas de su ejecución) en En vivo, aunque haya terminado hace días
		const toLive = $("#live-of-run");
		if (toLive) { toLive.href = `#view=live&exec=${encodeURIComponent(`r:${r.id}`)}`; toLive.hidden = !!STATIC; } // en las vistas globales (En vivo, Buscar…) lo oculta el CSS
		if (STATIC) {
			const pill = $("#static-pill");
			pill.textContent = `Exportado ${fmtDateTime(STATIC.exported_at)}`;
			pill.hidden = false;
		}
	}

	// ---------- view switching ----------
	function setView(view) {
		// al entrar a En vivo la vista se anima de nuevo (y se redibuja aunque el HTML sea el mismo)
		if (view === "live" && S.view !== "live") { S.liveView.anim = null; delete S.rendered["view-live"]; }
		S.view = view;
		$$(".side-nav a").forEach((a) => a.classList.toggle("active", a.dataset.view === view));
		renderView();
		writeHash();
		if (view === "search" && !STATIC && !S.runSearch.items.length && !S.runSearch.loading) loadRunSearch();
		if (view === "live" && !STATIC) loadLiveView();
	}

	// vistas que no dependen de la ejecución elegida
	const GLOBAL_VIEWS = new Set(["search", "live", "metrics", "maintenance", "settings"]);

	function renderView() {
		const global = GLOBAL_VIEWS.has(S.view);
		$$(".side-nav a").forEach((a) => a.classList.toggle("active", a.dataset.view === S.view));
		// en el celular el menú es una barra que se desplaza: la vista activa queda a la vista
		const nav = $(".side-nav"), on = nav?.querySelector?.("a.active");
		if (on && nav.scrollWidth > nav.clientWidth && (on.offsetLeft < nav.scrollLeft || on.offsetLeft + on.offsetWidth > nav.scrollLeft + nav.clientWidth)) {
			nav.scrollLeft = on.offsetLeft - 8;
		}
		if (!S.run && !global) {
			// sin ejecución cargada no hay qué mostrar: se oculta la vista anterior (Ajustes, Métricas…)
			// y, si el servidor no tiene ejecuciones, vuelve el estado vacío
			$$(".view").forEach((v) => (v.hidden = true));
			$("#empty-state").hidden = S.runs.length > 0;
			document.body.classList.remove("view-global", "view-dashboard");
			$("#run-banner").hidden = true;
			return;
		}
		$$(".view").forEach((v) => (v.hidden = v.id !== `view-${S.view}`));
		$("#empty-state").hidden = global || S.runs.length > 0;
		document.body.classList.toggle("view-global", global);
		if (global) $("#run-banner").hidden = true; else renderRunBanner();
		document.body.classList.toggle("view-dashboard", S.view === "dashboard");
		document.body.classList.toggle("view-live", S.view === "live");
		// el título de arriba: en En vivo, la ejecución que se mira (no la última corrida, que cambia sola)
		if (S.view !== "live" && S.run) {
			const rn = $("#report-name"), re = $("#report-env");
			if (rn && S.run.name) rn.textContent = S.run.name;
			if (re) re.textContent = S.run.environment || "";
		}
		const views = { search: renderRunSearch, tests: renderTests, categories: renderCategories, exceptions: renderExceptions, dashboard: renderDashboard,
			ai: renderAI, escalate: renderEscalate, metrics: renderMetrics, maintenance: renderMaintenance, settings: renderSettings, release: renderRelease,
			live: renderLiveView };
		// la vista viene de la URL (#view=…): solo las propias, nunca un método heredado del objeto
		(Object.hasOwn(views, S.view) ? views[S.view] : renderTests)();
	}

	// ---------- run explorer ----------
	function searchParams(cursor = "") {
		const x = S.runSearch, p = new URLSearchParams();
		["q", "project", "environment", "branch", "tag", "status", "from", "to", "sort"].forEach((k) => { if (x[k]) p.set(k, x[k]); });
		if (x.incomplete) p.set("incomplete", "true"); if (x.flaky) p.set("flaky", "true"); if (cursor) p.set("cursor", cursor);
		p.set("limit", "30"); return p;
	}
	function relativeRun(ms) {
		const sec = Math.max(0, Math.round((Date.now() - ms) / 1000));
		if (sec < 60) return tr("Hace menos de un minuto");
		if (sec < 3600) return tr("Hace {n} min", { n: Math.floor(sec / 60) });
		if (sec < 86400) return tr("Hace {n} h", { n: Math.floor(sec / 3600) });
		return tr("Hace {n} días", { n: Math.floor(sec / 86400) });
	}
	function scheduleRunSearch(reset = true) {
		const x = S.runSearch; clearTimeout(x.timer); x.timer = setTimeout(() => loadRunSearch(reset), 250);
	}
	async function loadRunSearch(reset = true) {
		if (STATIC) return;
		const x = S.runSearch, current = turn("run-search"); x.loading = true; x.error = null;
		if (reset) { x.cursor = ""; x.items = []; x.selected = 0; }
		renderRunSearch();
		try {
			const data = await api(`/api/v1/runs/search?${searchParams(reset ? "" : x.cursor)}`);
			if (!current()) return;
			x.items = reset ? data.items : x.items.concat(data.items); x.cursor = data.next_cursor || ""; x.total = data.total_estimate || 0; x.loading = false;
			writeHash(); renderRunSearch(); loadRunFacets();
		} catch (err) { if (!current()) return; x.loading = false; x.error = err; renderRunSearch(); }
	}
	async function loadRunFacets() {
		const x = S.runSearch, current = turn("run-facets");
		try { const facets = await api(`/api/v1/runs/facets?${searchParams()}`); if (current()) { x.facets = facets; renderSearchFilters(); } } catch { /* search remains usable without suggestions */ }
	}
	function renderSearchFilters() {
		const x = S.runSearch;
		["project", "environment", "branch", "tag"].forEach((key) => {
			const el = $(`[data-search-filter="${key}"]`); if (!el) return;
			const initial = el.options[0]?.textContent || tr("Todos");
			el.innerHTML = `<option value="">${esc(initial)}</option>${(x.facets[key] || []).map((f) => `<option value="${esc(f.value)}">${esc(f.value)} (${f.count})</option>`).join("")}`;
			el.value = x[key] || "";
		});
		["status", "from", "to"].forEach((key) => { const el = $(`[data-search-filter="${key}"]`); if (el) el.value = x[key] || ""; });
		const sort = $("#search-sort"); if (sort) sort.value = x.sort;
		renderSavedRunSearches();
	}
	function savedRunSearches() { try { return JSON.parse(localStorage.getItem("tracereports-run-searches") || "[]"); } catch { return []; } }
	function renderSavedRunSearches() { const el = $("#search-saved"); if (!el) return; const items = savedRunSearches(); el.innerHTML = `<option value="">${tr("Búsquedas guardadas")}</option>${items.map((item, i) => `<option value="${i}">${esc(item.name)}</option>`).join("")}`; }
	function saveRunSearch() { const name = window.prompt(tr("Nombre de la búsqueda")); if (!name?.trim()) return; const x = S.runSearch, query = {}; ["q", "project", "environment", "branch", "tag", "status", "from", "to", "sort", "incomplete", "flaky"].forEach((key) => { query[key] = x[key]; }); try { const items = savedRunSearches().filter((item) => item.name !== name.trim()).slice(-9); items.push({ name: name.trim(), query }); localStorage.setItem("tracereports-run-searches", JSON.stringify(items)); renderSavedRunSearches(); } catch { /* optional convenience only */ } }
	function runSearchChips() {
		const x = S.runSearch, names = { q: "Texto", project: "Proyecto", environment: "Ambiente", branch: "Rama", tag: "Tag", status: "Estado", from: "Desde", to: "Hasta", incomplete: "Incompletas", flaky: "Flaky" };
		return Object.entries(names).filter(([key]) => x[key]).map(([key, name]) => `<button class="search-chip" data-search-remove="${key}">${tr(name)}: ${key === "incomplete" || key === "flaky" ? "✓" : esc(x[key])}<b aria-hidden="true">×</b><span class="sr-only">${tr("Quitar")}</span></button>`).join("");
	}
	function renderRunSearch() {
		const x = S.runSearch, root = $("#view-search"); if (!root) return;
		if (STATIC) { root.innerHTML = `<div class="card placeholder">${tr("La búsqueda de historial no está disponible en un reporte exportado.")}</div>`; return; }
		const input = $("#run-search-input"); if (input && input.value !== x.q) input.value = x.q;
		renderSearchFilters();
		$("#search-chips").innerHTML = runSearchChips();
		$("#search-result-count").textContent = x.loading ? tr("Buscando…") : x.error ? tr("No se pudo buscar") : tr("{n} resultados", { n: x.total });
		const newRuns = $("#search-new-runs"); newRuns.hidden = !x.newCount; newRuns.textContent = tr("{n} ejecuciones nuevas · Mostrar", { n: x.newCount });
		const list = $("#run-search-results");
		if (x.error) list.innerHTML = `<div class="placeholder">${tr("No se pudieron cargar las ejecuciones.")} <button data-search-retry>${tr("Reintentar")}</button></div>`;
		else if (x.loading && !x.items.length) list.innerHTML = `<div class="placeholder"><div class="shimmer"></div><div class="shimmer"></div><div class="shimmer"></div></div>`;
		else if (!x.items.length) list.innerHTML = `<div class="placeholder">${tr("Sin resultados: quita un filtro o prueba otra búsqueda.")}</div>`;
		else list.innerHTML = x.items.map((r) => `<button class="search-row" data-search-run="${r.id}" role="option" aria-selected="${x.selected === r.id}">
			<span class="search-state ${lower(r.status)}" style="--c:var(--${lower(r.status)})"></span><span class="search-main"><b>${esc(r.name)}</b><span>${[r.branch, r.environment, r.project].filter(Boolean).map(esc).join(" · ")}</span></span>
			<span class="search-meta" title="${esc(fmtDateTime(r.started_at))}">${esc(relativeRun(r.started_at))}</span><span class="search-count">${r.passed}/${r.total} OK${r.failed ? ` · <b class="fail">${plural(r.failed, tr("fallo"), tr("fallos"))}</b>` : ""}${r.flaky_count ? ` · <b class="flaky">${r.flaky_count} flaky</b>` : ""}</span><label title="${tr("Marcar para comparar")}" data-search-compare-label><input type="checkbox" data-search-compare="${r.id}" ${x.compare.includes(r.id) ? "checked" : ""}></label></button>`).join("");
		$("#search-more").hidden = !x.cursor || x.loading;
		if (!x.selected) renderSearchPreview();
	}
	function shortDuration(ms) { const s = Math.max(0, Math.round((ms || 0) / 1000)); const m = Math.floor(s / 60), h = Math.floor(m / 60); return `${h ? `${h} h ` : ""}${m % 60 ? `${m % 60} m ` : ""}${s % 60} s`; }
	function releasePreview(release) { const labels = { go: "Listo para salir", risk: "Se puede salir, con riesgos", no_go: "No salir todavía" }; const d = release?.decision; return d ? `<span class="label ${d === "go" ? "pass" : d === "risk" ? "warning" : "fail"}">${tr(labels[d] || d)}</span>` : tr("Sin decisión"); }
	function renderSearchPreview(detail, release, compare) {
		const x = S.runSearch, el = $("#run-search-preview"); if (!el) return;
		const r = detail || x.items.find((item) => item.id === x.selected);
		if (!r) { el.innerHTML = `<p>${tr("Selecciona una ejecución para ver el resumen.")}</p>`; return; }
		if (!detail) { el.innerHTML = `<div class="placeholder">${tr("Cargando vista previa…")}</div>`; return; }
		const failed = detail.tests?.filter((t) => t.status === "FAIL").slice(0, 4) || [];
		const incidents = detail.summary?.incidents?.slice(0, 2) || [];
		const delta = compare?.base_run ? `${plural(compare.new_failures?.length || 0, "fallo nuevo", "fallos nuevos")} · ${plural(compare.fixed?.length || 0, "arreglado", "arreglados")}` : tr("Sin ejecución anterior");
		el.innerHTML = `<h3>${esc(detail.name)}</h3><p>${statusLabel(detail.status)} · ${esc(fmtDateTime(detail.started_at))}</p><dl><dt>${tr("Tests")}</dt><dd>${detail.passed}/${detail.total} OK · ${plural(detail.failed, "fallo", "fallos")}</dd><dt>${tr("Duración")}</dt><dd>${esc(shortDuration(durationOf(detail)))}</dd><dt>${tr("Release")}</dt><dd>${releasePreview(release)}</dd><dt>${tr("Cambios")}</dt><dd>${esc(delta)}</dd></dl>${incidents.length ? `<p><b>${tr("Incidentes")}</b><br>${incidents.map((i) => esc(i.title || i.summary || i.category || "")).join("<br>")}</p>` : ""}${failed.length ? `<p><b>${tr("Tests fallidos")}</b><br>${failed.map((t) => `<button class="search-failed-test" data-search-test="${t.id}" data-search-test-run="${detail.id}">${esc(t.name)}</button>`).join("<br>")}</p>` : ""}<div class="search-preview-actions"><button data-search-open="${detail.id}">${tr("Ver reporte")}</button>${(() => { const other = x.compare.find((c) => c !== detail.id); return `<button data-search-compare-open="${detail.id}" data-search-compare-base="${other || 0}" data-tip="${esc(tr(other ? "Compara esta ejecución con la que marcaste en la lista." : "Compara con la ejecución anterior del mismo proyecto y ambiente. Para elegir otra, márcala en la lista."))}">${other ? tr("Comparar con #{n}", { n: other }) : tr("Comparar con la anterior")}</button>`; })()}</div>`;
	}
	async function selectSearchRun(id) {
		const x = S.runSearch; x.selected = id; renderRunSearch(); const current = turn("search-preview");
		try { const [detail, release, compare] = await Promise.all([api(`/api/v1/runs/${id}`), api(`/api/v1/runs/${id}/release`).catch(() => null), api(`/api/v1/runs/${id}/compare`).catch(() => null)]); if (!current() || x.selected !== id) return; x.preview = [detail, release, compare]; renderSearchPreview(detail, release, compare); } catch { if (current()) $("#run-search-preview").innerHTML = `<p>${tr("No se pudo cargar la vista previa.")}</p>`; }
	}
	/** Abre run en el Dashboard con su comparación: contra base, o la automática (0). */
	async function compareSearchRuns(base, run) {
		await switchRun(run);
		setView("dashboard");
		if (base) await chooseBase(base);
		$("#compare-card")?.scrollIntoView({ block: "start", behavior: "smooth" });
	}

	/**
	 * Como setHTML, pero sin reemplazar: compara el HTML nuevo con lo que hay y toca solo lo que cambió
	 * (los nodos iguales siguen siendo los mismos). En vivo se redibuja con cada evento; reemplazarla entera
	 * reiniciaba sus animaciones (parpadeo), perdía el mouse y obligaba al navegador a rehacer miles de
	 * elementos. Las listas se emparejan por su clave (data-live-test, data-attn, data-group, data-lane…).
	 */
	function morphHTML(el, html) {
		if (S.rendered[el.id] === html) return false;
		S.rendered[el.id] = html;
		if (!el.firstChild) { el.innerHTML = html; return true; }
		const tpl = document.createElement("template");
		tpl.innerHTML = html;
		morphChildren(el, tpl.content);
		return true;
	}
	// clases que pone el código después de dibujar (animaciones, medidas): un parche no las borra
	const MORPH_KEEP_CLASSES = ["has-on", "notext", "live-changed", "lt-new", "bump", "lane-hl", "lt-flash", "la-new", "lc-new", "lg-anom-new", "live-panel-in", "lt-trace-in", "live-enter"];
	const morphKey = (n) => n.nodeType === 1 ? (n.getAttribute("data-live-test") ?? n.getAttribute("data-attn") ?? n.getAttribute("data-group") ?? n.getAttribute("data-lane") ?? n.getAttribute("data-ev") ?? n.getAttribute("data-seg") ?? n.getAttribute("data-live-errgroup") ?? n.id ?? null) || null : null;
	const morphSame = (a, b) => a.nodeType === b.nodeType && a.nodeName === b.nodeName && morphKey(a) === morphKey(b);
	function morphChildren(a, b) {
		// el campo (canvas) vive dentro de su lugar y no está en el HTML: no se toca
		if (a.nodeType === 1 && a.hasAttribute("data-live-field")) return;
		const want = [...b.childNodes];
		let cur = a.firstChild;
		for (const nb of want) {
			if (cur && morphSame(cur, nb)) { morphNode(cur, nb); cur = cur.nextSibling; continue; }
			// una clave que está más adelante (la lista cambió de orden): se trae ese nodo, no se rehace
			const key = morphKey(nb);
			let found = null;
			if (key !== null) for (let x = cur; x; x = x.nextSibling) if (morphSame(x, nb)) { found = x; break; }
			if (found) { a.insertBefore(found, cur); morphNode(found, nb); continue; }
			a.insertBefore(nb, cur);
		}
		while (cur) { const nx = cur.nextSibling; a.removeChild(cur); cur = nx; }
	}
	function morphNode(a, b) {
		if (a.nodeType === 3 || a.nodeType === 8) { if (a.data !== b.data) a.data = b.data; return; }
		if (a.nodeType !== 1) return;
		for (const { name, value } of b.attributes) {
			if (name === "class") {
				const keep = MORPH_KEEP_CLASSES.filter((c) => a.classList.contains(c) && !b.classList.contains(c));
				const v = keep.length ? `${value} ${keep.join(" ")}` : value;
				if (a.getAttribute("class") !== v) a.setAttribute("class", v);
			} else if (name === "value" && (a.tagName === "INPUT" || a.tagName === "TEXTAREA")) {
				if (a !== document.activeElement && a.value !== value) a.value = value;
			} else if (a.getAttribute(name) !== value) a.setAttribute(name, value);
		}
		for (const { name } of [...a.attributes]) {
			if (b.hasAttribute(name)) continue;
			// lo que pone el código y el HTML no trae: la posición del indicador, de la cinta, un <details> abierto,
			// la descripción del tooltip
			if ((name === "style" && a.tagName !== "COL") || (name === "open" && a.tagName === "DETAILS") || name === "aria-describedby") continue;
			if (name === "class") { const keep = MORPH_KEEP_CLASSES.filter((c) => a.classList.contains(c)); keep.length ? a.setAttribute("class", keep.join(" ")) : a.removeAttribute("class"); continue; }
			a.removeAttribute(name);
		}
		if ((a.tagName === "INPUT" || a.tagName === "TEXTAREA") && !b.hasAttribute("value") && a !== document.activeElement && a.value) a.value = "";
		morphChildren(a, b);
	}

	// only touch the DOM when content actually changed (keeps scroll position & avoids flicker while polling)
	function setHTML(el, html) {
		if (S.rendered[el.id] === html) return false;
		S.rendered[el.id] = html;
		el.innerHTML = html;
		return true;
	}

	// ---------- tests view ----------
	function filteredTests() {
		const q = lower(S.search);
		return S.run.tests.filter((t) =>
			(!S.status || t.status === S.status) &&
			(!q || lower(t.name).includes(q) || lower(t.category).includes(q) || lower(t.description).includes(q)));
	}

	function testItem(t) {
		const ai = t.triage?.state === "DONE"
			? `<span class="ai-mini" data-tip="Causa probable según la IA: ${esc(AI_LABEL[t.triage.category] || t.triage.category)}">${icon("i-spark")}${esc(t.triage.category)}</span>` : "";
		return `<li class="collection-item ${t.id === S.testId ? "active" : ""}" data-test="${t.id}">
			<div class="test-head"><span class="test-name">${esc(t.name)}</span>${statusLabel(t.status)}</div>
			<div class="meta"><span>${fmtTime(t.started_at)}</span><span>${fmtDuration(durationOf(t))}</span>${retryChip(t, true)}${flakyChip(t, true)}${driftChip(t, true)}${quarantineChip(t, true)}${verdictChip(t)}${ai}${netMini(t)}</div>
		</li>`;
	}

	function renderTests() {
		const tests = filteredTests();
		$("#tests-count").textContent = tests.length === S.run.tests.length ? tests.length : `${tests.length} / ${S.run.tests.length}`;
		// el filtro de interrumpidos solo aparece cuando hay alguno (o está elegido)
		$("#status-filters [data-status=INTERRUPTED]").hidden = !S.run.interrupted && S.status !== "INTERRUPTED";
		$$("#status-filters button").forEach((b) => b.classList.toggle("active", b.dataset.status === S.status));
		setHTML($("#test-collection"), tests.length ? tests.map(testItem).join("") : `<li class="collection-empty">No hay tests que coincidan con el filtro.</li>`);
		renderTestDetail();
	}

	// Chips para filtrar los steps por estado (con conteo).
	const STEP_FILTERS = [["INFO", "info", "i-info", "Info"], ["PASS", "pass", "i-pass", "Pass"], ["FAIL", "fail", "i-fail", "Fail"],
		["WARNING", "warning", "i-warning", "Warning"], ["SKIP", "skip", "i-skip", "Skip"]];

	function renderTestDetail() {
		const el = $("#test-detail");
		const t = S.test;
		if (!t || t.id !== S.testId) {
			setHTML(el, `<div class="card placeholder">${S.run?.tests.length ? "Selecciona un test para ver sus detalles." : "Esta ejecución aún no tiene tests."}</div>`);
			return;
		}
		const tags = tagsOf(t).map((c) => `<span class="label tag">${esc(c)}</span>`).join("");
		const shots = t.logs.filter((l) => l.screenshot).length;
		const consoleList = t.console || [];
		const tab = S.detailTab === "network" && t.network_total > 0 ? "network"
			: S.detailTab === "console" && consoleList.length ? "console"
			: S.detailTab === "timeline" ? "timeline"
			: S.detailTab === "replay" && shots > 0 ? "replay"
			: S.detailTab === "inspector" && hasInspector(t) ? "inspector" : "steps";
		const head = `<div class="card detail-head">
			<div class="title-row"><h4 class="detail-name">${esc(t.name)}</h4>${statusLabel(t.status)}</div>
			<div class="test-info">
				<span class="label started" title="Comienzo del test">${fmtDateTime(t.started_at)}</span>
				${t.ended_at ? `<span class="label ended" title="Fin del test">${fmtDateTime(t.ended_at)}</span>` : `<span class="label running">en curso</span>`}
				<span class="label elapsed" title="Tiempo de ejecución">${fmtDuration(durationOf(t))}</span>
				${retryChip(t, false)}${flakyChip(runTest(t), false)}${driftChip(runTest(t), false)}${quarantineChip(t, false)}${ownerChip(t)}
				${t.worker ? `<span class="label" data-tip="Worker o shard que ejecutó el test (pytest-xdist)">${esc(t.worker)}</span>` : ""}
				${quarantineButton(t)}
			</div>
			${quarantineForm(t)}
			${t.description ? `<div class="test-desc">${esc(t.description)}</div>` : ""}
			${tags ? `<div class="test-attributes">${tags}</div>` : ""}
			${reproBlock(t)}
			${historyStrip(t)}
		</div>`;

		const error = t.status === "FAIL" && (t.error_message || t.error_trace) ? `<div class="card error-block">
			${t.error_message ? `<pre class="error-msg">${esc(t.error_message)}</pre>` : ""}
			${t.error_trace ? `<details><summary>Ver stack trace</summary><pre>${esc(t.error_trace)}</pre></details>` : ""}
			${t.network_errors ? `<div class="error-actions"><button class="cf-btn cf-btn-sm cf-btn-mock" data-mock-first data-tip="Crea un stub con la respuesta de la primera llamada que falló, para reproducir este fallo en local sin el backend real">${icon("i-bolt")}Generar mock del backend que falló</button></div>` : ""}
			${verdictBlock(t)}
		</div>` : t.status === "INTERRUPTED" && t.error_message ? `<div class="card error-block interrupted-block">
			<pre class="error-msg">${esc(t.error_message)}</pre>
			<p class="m-hint">${tr("No alcanzó a terminar: no cuenta como fallo ni como éxito, y la IA no lo analiza.")}</p>
		</div>` : "";

		// La captura va dentro de "Detalle", debajo del texto del step.
		const counts = {};
		for (const l of t.logs) counts[l.status] = (counts[l.status] || 0) + 1;
		const chips = STEP_FILTERS.filter(([st]) => counts[st]).map(([st, cls, ic, name]) =>
			`<button class="step-chip ${cls} ${S.stepFilter === st ? "active" : ""}" data-step-filter="${st}" data-tip="Mostrar solo los pasos ${esc(name)} (${counts[st]})">${icon(ic)}${name} ${counts[st]}</button>`).join("");
		const logs = S.stepFilter ? t.logs.filter((l) => l.status === S.stepFilter) : t.logs;
		const rows = logs.map((l) => stepRow(l)).join("");
		const steps = `${shotPolicyNote(t)}${t.logs.length > 1 ? `<div class="steps-toolbar"><span class="lbl">Filtrar:</span>
				<button class="step-chip all ${!S.stepFilter ? "active" : ""}" data-step-filter="" data-tip="Mostrar todos los pasos">Todos ${t.logs.length}</button>${chips}</div>` : ""}
			<table class="table-results">
				<thead><tr><th>Estado</th><th>Hora</th><th>Detalle</th></tr></thead>
				<tbody>${rows || `<tr><td colspan="3" class="no-steps">${S.stepFilter ? "Ningún step con ese estado." : "Sin pasos registrados."}</td></tr>`}</tbody>
			</table>`;

		const tabs = `<div class="detail-tabs">
			<button data-tab="steps" class="${tab === "steps" ? "active" : ""}" data-tip-title="Steps" data-tip="Cada acción y validación del test en orden, con sus capturas debajo. Filtra por estado con los chips.">${icon("i-tests")}Steps <span class="tab-count">${t.logs.length}</span></button>
			<button data-tab="timeline" class="${tab === "timeline" ? "active" : ""}" data-tip-title="Timeline" data-tip="Pasos, capturas y llamadas al backend en una sola línea de tiempo, con lo que duró cada llamada: muestra qué esperaba el test cuando se trabó.">${icon("i-timeline")}Timeline</button>
			${shots ? `<button data-tab="replay" class="${tab === "replay" ? "active" : ""}" data-tip-title="Replay" data-tip="Reproduce el test como un video, paso a paso, con la captura de cada momento. Útil para ver cómo llegó la pantalla al fallo." data-tip-keys="Espacio: play/pausa · ← →: pasos">${icon("i-play")}Replay <span class="tab-count">${shots}</span></button>` : ""}
			${hasInspector(t) ? `<button data-tab="inspector" class="${tab === "inspector" ? "active" : ""}" data-tip-title="Inspector" data-tip="${tr("La página del momento del fallo reconstruida: inspecciona elementos, prueba XPath o CSS y copia locators robustos (clic derecho) sin volver a reproducir el fallo.")}">${icon("i-target")}Inspector${t.dom_steps ? ` <span class="tab-count">${t.dom_steps + (t.has_dom ? 1 : 0)}</span>` : ""}</button>` : ""}
			${t.network_total > 0 ? `<button data-tab="network" class="${tab === "network" ? "active" : ""}" data-tip-title="Red" data-tip="Las llamadas al backend que hizo la página durante el test: status, tiempos, headers y body. Desde una llamada con error puedes generar un mock.">${icon("i-network")}Red <span class="tab-count">${t.network_total}</span>
				${t.network_errors ? `<span class="tab-err">${t.network_errors} con error</span>` : `<span class="tab-ok">OK</span>`}</button>` : ""}
			${consoleList.length ? `<button data-tab="console" class="${tab === "console" ? "active" : ""}" data-tip-title="${tr("Consola")}" data-tip="${tr("Errores y advertencias de la consola del navegador durante el test, y los errores de JavaScript que la página no manejó.")}">${icon("i-warning")}${tr("Consola")} <span class="tab-count">${consoleList.length}</span>
				${t.console_errors ? `<span class="tab-err">${tr(t.console_errors === 1 ? "1 error" : "{n} errores", { n: t.console_errors })}</span>` : ""}</button>` : ""}
		</div>`;
		const panel = tab === "network" ? `<div class="net-panel" id="net-panel"></div>`
			: tab === "timeline" ? `<div class="timeline" id="timeline-panel"></div>`
			: tab === "replay" ? `<div class="replay-panel" id="replay-panel"></div>`
			: tab === "inspector" ? `<div class="inspector-panel" id="inspector-panel"></div>`
			: tab === "console" ? consolePanel(consoleList) : steps;
		const body = `<div class="card content-card">${tabs}${panel}</div>`;

		if (setHTML(el, head + aiCard(t) + error + artifactsBlock(t) + body)) { S.rendered["net-list"] = null; S.rendered["timeline-panel"] = null; }
		if (tab === "network") ensureNetworkPanel(t);
		if (tab === "timeline") ensureTimeline(t);
		if (tab === "replay") mountReplay(t); else if (S.player) { S.player.destroy(); S.player = null; }
		if (tab === "inspector") mountInspector(t); else if (S.inspector) { S.inspector.api?.destroy(); S.inspector = null; }
		const bar = $("#test-detail .detail-tabs"), act = bar && $("button.active", bar);
		if (act) { // pestaña activa visible en pantallas angostas
			const r = act.getBoundingClientRect(), br = bar.getBoundingClientRect();
			if (r.left < br.left || r.right > br.right) bar.scrollLeft += r.left - br.left - 8;
		}
		ensureHistory(t);
		ensureLocator(t);
		ensureSimilar(t);
		updateAutoScrollToggle();
	}

	function aiCard(t) {
		if (t.status !== "FAIL") return "";
		const loc = locatorCard(t);
		const card = aiCardBody(t, loc);
		if (card) return card;
		return loc ? `<div class="card ai-card"><div class="ai-head"><span class="ai-title" tabindex="0" data-tip="El test falló porque un selector ya no encuentra el elemento. Estos reemplazos salen de la página real al momento del fallo, ordenados por robustez.">${icon("i-crosshair")}AI Locator Recommender</span></div>${loc}</div>` : "";
	}

	function aiCardBody(t, loc) {
		const title = `<span class="ai-title" tabindex="0" data-tip="La IA lee el error, los pasos y las llamadas de red del test y clasifica la causa probable: locator roto, backend, datos, entorno o bug de la aplicación.">${icon("i-spark")}AI Failure Triage</span>`;
		const tr = t.triage;
		if (!tr) {
			return S.config.ai_enabled || STATIC ? "" : `<div class="card ai-card muted"><div class="ai-head">${title}</div>
				<p style="margin-top:8px">${esc(window.TraceReportsI18n.t(S.config.demo
					? "Diagnóstico con IA desactivado en la demo. Para activarlo, agrega AI_PROVIDER y AI_API_KEY (o GEMINI_API_KEY) a tu entorno o a tu .env y vuelve a iniciar tracereports demo (sin -ai=false)."
					: "Diagnóstico con IA desactivado. Actívalo en Ajustes → IA o con AI_PROVIDER y su API key en el servidor."))}</p>${loc}</div>`;
		}
		if (tr.state === "PENDING" && STATIC) {
			return `<div class="card ai-card muted"><div class="ai-head">${title}</div>
				<p style="margin-top:8px">El análisis de IA aún no había terminado cuando se exportó este reporte.</p></div>`;
		}
		if (tr.state === "PENDING") {
			return `<div class="card ai-card"><div class="ai-head">${title}<span class="ai-model">${S.config.ai_provider === "builtin" ? "Diagnosticando con el motor local…" : `Analizando con ${esc(S.config.ai_model)}…`}</span></div>
				<div class="shimmer"></div><div class="shimmer short"></div>${loc}</div>`;
		}
		if (tr.state === "ERROR") {
			return `<div class="card ai-card muted"><div class="ai-head">${title}</div>
				<p style="margin-top:8px">No se pudo completar el análisis: ${esc(tr.error)}</p>${loc}</div>`;
		}
		if (tr.state === "SKIPPED") {
			return `<div class="card ai-card muted"><div class="ai-head">${title}</div>
				<p style="margin-top:8px">${esc(tr.error)}</p>${loc}</div>`;
		}
		return `<div class="card ai-card">
			<div class="ai-head">${title}<span class="ai-cat ${esc(tr.category)}" tabindex="0" data-tip-title="Categoría del fallo" data-tip="${esc(AI_LABEL[tr.category] || tr.category)}">${esc(tr.category)}</span>
				${isLocal(tr) ? localBadge(tr.rule) : `<span class="ai-model">${esc(S.config.ai_model)}</span>`}</div>
			<h6>Resumen</h6><p>${esc(tr.summary)}</p>
			<h6>Sugerencia</h6><p class="suggestion">${esc(tr.suggestion)}</p>
			${!STATIC && canAct() ? buddyLangNote(t) : "" /* en otro idioma que la interfaz: el mismo aviso que da Trace Buddy */}
			${similarBlock(t)}
			${loc}
		</div>`;
	}

	// ---------- network (pestaña "Red") ----------
	const METHOD_COLOR = { GET: "#2563eb", POST: "#16a34a", PUT: "#7c3aed", PATCH: "#0891b2", DELETE: "#dc2626" };
	const MAX_PREVIEW = 20000;  // como el visor original: no resaltar bodies gigantes token por token
	const MAX_CARDS = 1000;

	const netOutcome = (c) => (c.failed ? "FAILED" : !c.status ? "PENDING" : c.status >= 400 ? "ERROR" : "OK");
	const isNetError = (c) => !c.expected && (c.failed || c.status >= 400);
	const isApi = (c) => /\/api\//i.test(c.url) || c.resource_type === "xhr" || c.resource_type === "fetch";

	function netMini(t) {
		if (!t.network_errors) return "";
		return `<span class="net-mini" data-tip="${t.network_errors} de ${t.network_total} llamadas al backend fallaron o respondieron con error: revisa la pestaña Red">${icon("i-network")}${t.network_errors} error(es) de red</span>`;
	}

	/** Una duración corta: "< 1 s", "42 s", "3 min 5 s". */
	function fmtSecs(ms) {
		const t = Math.round((ms || 0) / 1000);
		if (t < 1) return "< 1 s";
		return t < 60 ? `${t} s` : `${Math.floor(t / 60)} min${t % 60 ? ` ${t % 60} s` : ""}`;
	}
	function fmtBytes(n) {
		if (n < 1024) return `${n} B`;
		if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`;
		if (n < 1073741824) return `${(n / 1048576).toFixed(1)} MB`;
		if (n < 1099511627776) return `${(n / 1073741824).toFixed(1)} GB`;
		return `${(n / 1099511627776).toFixed(1)} TB`;
	}

	function loadScript(src) {
		return new Promise((resolve, reject) => {
			const el = document.createElement("script");
			el.src = src;
			el.onload = resolve;
			el.onerror = () => reject(new Error(`no se pudo cargar ${src}`));
			document.head.appendChild(el);
		});
	}

	async function loadNetwork(testId) {
		if (!STATIC) return api(`/api/v1/tests/${testId}/network`);
		// Reporte exportado: cada test trae su red en network/test_<id>.js (funciona desde file://).
		if (!window.TRACEREPORTS_NET?.[testId]) await loadScript(`network/test_${testId}.js`);
		return window.TRACEREPORTS_NET[testId];
	}

	function ensureNetworkPanel(t) {
		const panel = $("#net-panel");
		if (!panel) return;
		const key = `${t.id}:${t.network_total}`;
		if (S.net.key !== key) {
			S.net = { key, list: null };
			panel.innerHTML = `<div class="placeholder">Cargando conexiones de red…</div>`;
			loadNetwork(t.id)
				.then((list) => { if (S.net.key === key) { S.net.list = list; renderNetPanel(); } })
				.catch((err) => { panel.innerHTML = `<div class="placeholder">No se pudo cargar la red: ${esc(err.message)}</div>`; });
			return;
		}
		if (S.net.list && !panel.querySelector(".net-toolbar")) renderNetPanel();
	}

	function renderNetPanel() {
		const panel = $("#net-panel");
		const list = S.net.list || [];
		const errors = list.filter(isNetError).length;
		const failed = list.filter((c) => c.failed).length;
		const ok = list.filter((c) => netOutcome(c) === "OK").length;
		const bytes = list.reduce((a, c) => a + (c.body_size || 0), 0);
		const slow = list.filter((c) => (c.duration_ms || 0) >= 1000).length;
		panel.innerHTML = `
			<div class="net-summary">
				<span><b>${list.length}</b> conexiones capturadas</span>
				<span class="ok"><b>${ok}</b> OK</span>
				<span class="${errors ? "err" : ""}"><b>${errors}</b> con error (status ≥ 400${failed ? ` o sin respuesta: ${failed}` : ""})</span>
				${slow ? `<span class="slow"><b>${slow}</b> lentas (≥ 1 s)</span>` : ""}
				<span><b>${fmtBytes(bytes)}</b> en bodies</span>
			</div>
			<div class="net-toolbar">
				<div class="net-seg">
					<button data-net-filter="" class="${S.netFilter === "" ? "active" : ""}" data-tip="Todas las llamadas capturadas">Todas</button>
					<button data-net-filter="OK" class="${S.netFilter === "OK" ? "active" : ""}" data-tip="Llamadas que respondieron bien (status menor a 400)">OK</button>
					<button data-net-filter="ERROR" class="${S.netFilter === "ERROR" ? "active" : ""}" data-tip="Status 4xx/5xx o sin respuesta (caída, timeout, CORS): lo primero que mirar cuando el fallo viene del backend">Con error</button>
				</div>
				<label class="net-check" data-tip="Oculta imágenes, CSS, fuentes y scripts: deja solo las llamadas a la API (XHR/fetch)"><input type="checkbox" id="net-api-only" ${S.netApiOnly ? "checked" : ""}> Solo API (XHR/fetch)</label>
				<input type="search" id="net-search" placeholder="Filtrar por URL, método o status..." value="${esc(S.netSearch)}" autocomplete="off">
			</div>
			<div id="net-list"></div>`;
		S.rendered["net-list"] = null;
		renderNetList();
	}

	function renderNetList() {
		const el = $("#net-list");
		if (!el) return;
		const q = lower(S.netSearch);
		const items = (S.net.list || [])
			.map((c, i) => [c, i])
			.filter(([c]) => (!S.netFilter || (S.netFilter === "ERROR" ? isNetError(c) : netOutcome(c) === "OK"))
				&& (!S.netApiOnly || isApi(c))
				&& (!q || lower(`${c.url} ${c.method} ${c.status || ""}`).includes(q)));
		$$(".net-seg button").forEach((b) => b.classList.toggle("active", b.dataset.netFilter === S.netFilter));
		const cards = items.slice(0, MAX_CARDS).map(([c, i]) => netCard(c, i)).join("");
		setHTML(el, items.length
			? cards + (items.length > MAX_CARDS ? `<div class="placeholder">Mostrando ${MAX_CARDS} de ${items.length}: usa el filtro para acotar.</div>` : "")
			: `<div class="placeholder">Ninguna conexión coincide con el filtro.</div>`);
	}

	function statusBadge(c) {
		if (c.expected) return `<span class="nbadge" style="background:#64748b" title="${esc(window.TraceReportsI18n.t("Respuesta negativa esperada por el test: no cuenta como error ni como causa del fallo"))}">${c.status || "—"} ✓</span>`;
		if (c.failed) return `<span class="nbadge" style="background:#dc2626" title="${esc(c.error_text)}">falló</span>`;
		if (!c.status) return `<span class="nbadge" style="background:#6b7280" title="El test terminó antes de que llegara la respuesta">sin terminar</span>`;
		const color = c.status >= 500 ? "#dc2626" : c.status >= 400 ? "#ea580c" : c.status >= 300 ? "#2563eb" : "#16a34a";
		return `<span class="nbadge" style="background:${color}" title="${esc(c.status_text)}">${c.status}</span>`;
	}

	function netCard(c, i) {
		const method = c.method || "?";
		const size = c.body_size ? `<span class="peso ${c.body_size >= 100000 ? "peso-alto" : ""}">${fmtBytes(c.body_size)}</span>` : "";
		const dur = c.duration_ms != null ? `<span class="peso ${c.duration_ms >= 1000 ? "peso-alto" : ""}" title="Duración">${c.duration_ms} ms</span>` : "";
		return `<details class="net-card ${isNetError(c) ? "net-card-error" : ""}" data-net-idx="${i}">
			<summary><span class="nbadge" style="background:${METHOD_COLOR[method] || "#6b7280"}">${esc(method)}</span>${statusBadge(c)}${dur}${size}
				<span class="net-url">${esc(c.url || "(sin url)")}</span>
				${isNetError(c) && c.status ? `<button class="cf-btn cf-btn-sm cf-btn-mock" data-mock data-tip="Genera un stub de esta respuesta (Playwright, Cypress o WireMock) para reproducir el error sin depender del backend">${icon("i-bolt")}Mock</button>` : ""}
				${isNetError(c) && !STATIC ? `<button class="cf-btn cf-btn-sm cf-btn-lab" data-lab data-tip="Reenvía esta llamada con una sesión nueva para ver si el fallo sigue ocurriendo">${icon("i-flask")}Lab</button>` : ""}</summary>
			<div class="net-detail"></div>
		</details>`;
	}

	// Resaltado JSON (port de _resaltar_json del visor de red original).
	const JSON_TOKEN = /"(?:\\u[a-fA-F0-9]{4}|\\[^u]|[^\\"])*"(?:\s*:)?|\b(?:true|false)\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g;
	function highlightJSON(text) {
		let out = "", last = 0;
		for (const m of text.matchAll(JSON_TOKEN)) {
			out += esc(text.slice(last, m.index));
			const tok = m[0];
			const cls = tok.startsWith('"') ? (tok.trimEnd().endsWith(":") ? "json-key" : "json-string")
				: tok === "true" || tok === "false" ? "json-bool" : tok === "null" ? "json-null" : "json-number";
			out += `<span class="${cls}">${esc(tok)}</span>`;
			last = m.index + tok.length;
		}
		return out + esc(text.slice(last));
	}

	// Dónde abrir el body completo guardado: endpoint del servidor, o archivo dentro del ZIP.
	function bodyLocation(c) {
		if (STATIC) return c.body_file ? { href: c.body_file, label: c.body_file, hint: "dentro de la carpeta del reporte" } : null;
		const path = `/api/v1/network/${c.id}/body`;
		return { href: path, label: location.origin + path, hint: "en el servidor de TraceReports" };
	}

	const copyBtn = (text, label = "Copiar ruta") => `<button class="copy-btn" data-copy="${esc(text)}">${label}</button>`;

	function cutNotice(full, loc) {
		const base = `Vista previa recortada a ${MAX_PREVIEW / 1000} KB${full ? ` (de ${fmtBytes(full)})` : ""} para que el reporte cargue rápido.`;
		if (!loc) return `<div class="truncado-aviso">${base} <button class="copy-btn" data-open-full="1">Abrir contenido completo</button></div>`;
		return `<div class="truncado-aviso">${base}<br>Contenido completo ${esc(loc.hint)}:
			<a class="ruta" href="${esc(loc.href)}" target="_blank" rel="noopener">${esc(loc.label)}</a> ${copyBtn(loc.label)}</div>`;
	}

	function block(title, content, loc = null) {
		if (!content || (typeof content === "object" && !Object.keys(content).length)) return "";
		if (typeof content === "object") {
			return `<div class="bloque"><div class="bloque-titulo">${esc(title)}</div><pre>${esc(Object.entries(content).map(([k, v]) => `${k}: ${v}`).join("\n"))}</pre></div>`;
		}
		let text = String(content), isJson = false;
		try { text = JSON.stringify(JSON.parse(text), null, 2); isJson = true; } catch { /* not JSON */ }
		const cut = text.length > MAX_PREVIEW;
		if (cut) text = text.slice(0, MAX_PREVIEW);
		return `<div class="bloque"><div class="bloque-titulo">${esc(title)}</div>
			${cut ? cutNotice(String(content).length, loc) : ""}
			<pre class="${isJson ? "json-pretty" : ""}">${isJson ? highlightJSON(text) : esc(text)}</pre></div>`;
	}

	function copyText(text, btn) {
		const done = (ok) => {
			const prev = btn.textContent;
			btn.textContent = ok ? "¡Copiado!" : "No se pudo copiar";
			setTimeout(() => (btn.textContent = prev), 1500);
		};
		if (navigator.clipboard?.writeText) {
			navigator.clipboard.writeText(text).then(() => done(true), () => done(legacyCopy(text)));
		} else done(legacyCopy(text));
	}

	// Fallback para file:// (reporte exportado), donde la Clipboard API suele no estar disponible.
	function legacyCopy(text) {
		const ta = document.createElement("textarea");
		ta.value = text;
		ta.style.cssText = "position:fixed;opacity:0";
		document.body.appendChild(ta);
		ta.select();
		let ok = false;
		try { ok = document.execCommand("copy"); } catch { /* ignore */ }
		ta.remove();
		return ok;
	}

	/** Ids que unen la llamada con los logs del backend (traceparent, X-Request-Id...) y sus links. */
	function correlationBlock(c) {
		if (!c.trace_id && !c.request_id) return "";
		const row = (label, id) => id ? `<div class="corr-row"><span class="corr-lbl">${label}</span><code data-no-i18n>${esc(id)}</code>${copyBtn(id, "Copiar")}</div>` : "";
		const link = (url, label, tip) => url ? `<a class="cf-btn cf-btn-sm" href="${esc(url)}" target="_blank" rel="noopener" data-tip="${tip}">${icon("i-search")}${label} ↗</a>` : "";
		const links = link(c.logs_url, "Ver logs", "Abre los logs del backend de esta llamada (TRACEREPORTS_LOGS_URL)")
			+ link(c.trace_url, "Ver traza", "Abre la traza distribuida de esta llamada (TRACEREPORTS_TRACE_URL)");
		return `<div class="bloque corr"><div class="bloque-titulo">Correlación con el backend</div>
			${row("Trace ID", c.trace_id)}${row("Request ID", c.request_id)}
			${links ? `<div class="corr-links">${links}</div>` : ""}</div>`;
	}

	function netDetail(c) {
		const isHtml = /html/i.test(c.mime_type || "") || /^\s*<(!doctype html|html)/i.test(c.response_body || "");
		const truncated = !c.body_truncated ? "" : `<div class="truncado-aviso">El servidor guardó ${fmtBytes((c.response_body || "").length)} de ${fmtBytes(c.body_size)} de este body.
			${c.evidence_file
				? `<br>Body completo en la máquina que ejecutó el test: <code class="ruta">${esc(c.evidence_file)}</code> ${copyBtn(c.evidence_file)}
					<br><small>Busca la conexión por su URL dentro de ese archivo.</small>`
				: `<br><small>Para conservarlo entero, usa <code>reportar_red(page, cr, guardar_en="carpeta")</code>: el reporte mostrará aquí la ruta del archivo.</small>`}</div>`;
		const general = {
			// un solo bloque de texto: las etiquetas se traducen aquí, no en el DOM
			URL: c.url, [tr("Método")]: c.method,
			[tr("Estado")]: c.failed ? `${tr("sin respuesta")} (${c.error_text})` : `${c.status || "—"} ${c.status_text || ""}`,
			[tr("Tipo")]: [c.resource_type, c.mime_type].filter(Boolean).join(" · ") || "—",
			[tr("Inicio")]: c.started_at ? fmtTime(c.started_at) : "—",
			[tr("Duración")]: c.duration_ms != null ? `${c.duration_ms} ms` : "—",
			...(c.evidence_file ? { [tr("Evidencia local")]: c.evidence_file } : {}),
		};
		const loc = bodyLocation(c);
		const compare = isNetError(c) && !STATIC
			? `<button class="cf-btn cf-btn-sm" data-baseline data-tip="${tr("Busca esta misma llamada en la última ejecución donde el test pasó y muestra qué cambió: status, headers y body.")}">${icon("i-timeline")}${tr("Comparar con la última vez que pasó")}</button>` : "";
		const lab = STATIC ? "" : `<button class="cf-btn cf-btn-sm cf-btn-lab" data-lab data-tip="${tr("Reenvía esta llamada con una sesión nueva para ver si el fallo sigue ocurriendo")}">${icon("i-flask")}${tr("Abrir en Lab")}</button>`;
		return `<div class="net-actions">${compare}${lab}<button class="cf-btn cf-btn-sm cf-btn-mock" data-mock>${icon("i-bolt")}Generar mock / stub</button><button class="net-curl" data-curl="1">Copiar como cURL</button></div>`
			+ correlationBlock(c)
			+ block("General", general)
			+ block("Request headers", c.request_headers)
			+ block("Request body" + (c.post_data_via_cdp ? " (recuperado via CDP)" : ""), c.post_data)
			+ block("Response headers", c.response_headers)
			+ (c.response_body && isHtml
				? `<div class="bloque"><div class="bloque-titulo">Response body: vista previa (como se vería en el navegador)</div>
					<iframe class="preview-html" sandbox="" srcdoc="${esc(c.response_body)}"></iframe></div>${truncated}${block("Response body (código fuente)", c.response_body, loc)}`
				: truncated + block("Response body", c.response_body, loc))
			+ (c.failed ? block("Error de carga", c.error_text) : "");
	}

	// ---------- funcionalidades: chips, locators, replay, mocks, en vivo ----------
	const CF = window.TraceReportsFeatures;
	const runTest = (t) => S.run?.tests.find((x) => x.id === t.id) || t;

	function stepRow(l, extraClass = "") {
		const rep = currentLocator();
		const flag = rep && rep.step_log_id === l.id
			? `<button class="step-loc-flag" data-loc-jump data-tip="En este paso el selector no encontró el elemento. Clic para ver los reemplazos sugeridos">${icon("i-crosshair")}Locator roto · ver sugerencias</button>` : "";
		return `<tr class="${lower(l.status)} ${extraClass}" data-log-id="${l.id}">
			<td class="status ${lower(l.status)}" title="${esc(lower(l.status))}">${icon(STATUS_ICON[l.status] || "i-info")}</td>
			<td class="timestamp">${fmtTime(l.timestamp)}</td>
			<td class="step-details"><span class="step-text">${esc(l.message)}</span>${flag}${l.screenshot
				? `<img class="report-img" loading="lazy" src="${esc(l.screenshot)}" alt="Captura" data-caption="${esc(l.message)}">` : ""}</td>
		</tr>`;
	}

	/**
	 * Estabilidad del test en su contexto (mismo proyecto, ambiente y rama):
	 *  - inestable: alterna entre pasar y fallar, o pasó solo tras reintentar;
	 *  - falla persistente: falla en sus últimas ejecuciones seguidas (regresión, no flaky).
	 * La tasa mostrada es la de fallos, no un "% de inestabilidad".
	 */
	function flakyChip(t, compact) {
		const f = t.flaky_info;
		if (!f) return "";
		const tr = window.TraceReportsI18n.t;
		if (f.kind === "persistent") {
			const label = compact ? tr("Falla persistente") : tr("Falla persistente · {n} seguidas", { n: f.streak });
			return `<span class="flaky-chip persistent ${compact ? "sm" : ""}" tabindex="0">⛔ ${label}
				<span class="cf-tip" role="tooltip"><b>${tr("Falla en sus últimas {n} ejecuciones seguidas", { n: f.streak })}</b>${CF.sparkline(f.recent)}
				<small>${tr("Es una regresión que se mantiene, no inestabilidad: revisar el cambio que la introdujo.")} ${tr("Tasa de fallo: {p}% ({f} de {r} ejecuciones).", { p: f.fail_rate, f: f.fails, r: f.runs })}</small></span></span>`;
		}
		const weak = f.low_data;
		const label = compact ? (weak ? tr("¿Inestable?") : tr("Inestable")) : weak ? tr("Posible inestabilidad") : tr("Inestable · falló {f} de {r}", { f: f.fails, r: f.runs });
		const facts = [tr("Tasa de fallo: {p}% ({f} de {r} ejecuciones).", { p: f.fail_rate, f: f.fails, r: f.runs }),
			f.flips ? tr("{n} cambios entre pasar y fallar.", { n: f.flips }) : "",
			f.retried ? tr("{n} veces pasó solo tras reintentar.", { n: f.retried }) : "",
			weak ? tr("Pocos datos (menos de 5 ejecuciones): conclusión débil.") : ""].filter(Boolean).join(" ");
		return `<span class="flaky-chip ${weak ? "weak" : ""} ${compact ? "sm" : ""}" tabindex="0">⚠️ ${label}
			<span class="cf-tip" role="tooltip"><b>${tr("Inestable en su contexto (mismo proyecto, ambiente y rama)")}</b>${CF.sparkline(f.recent)}
			<small>${facts} ${tr("Suele ser una espera, datos o entorno, no un bug nuevo.")}</small></span></span>`;
	}

	/** ↻ Pasó tras reintento: el runner lo ejecutó más de una vez (pytest-rerunfailures...). */
	function retryChip(t, compact) {
		if (!(t.attempts > 1)) return "";
		const tr = window.TraceReportsI18n.t;
		const label = t.status === "PASS" ? tr("Pasó tras reintento") : tr("{n} intentos", { n: t.attempts });
		return `<span class="retry-chip ${compact ? "sm" : ""}" data-tip="${esc(tr("El runner lo ejecutó {n} veces. La evidencia de los intentos fallidos sigue en los pasos.", { n: t.attempts }))}">↻ ${compact && t.status === "PASS" ? tr("reintento") : label}</span>`;
	}

	const CONSOLE_LEVEL = { pageerror: ["fail", "Error no manejado"], error: ["fail", "Error"], warning: ["warning", "Advertencia"],
		info: ["info", "Info"], log: ["info", "Log"], debug: ["info", "Debug"] };

	/** Consola del navegador del test, en orden de aparición. */
	function consolePanel(list) {
		return `<table class="table-results console-table"><thead><tr><th>${tr("Nivel")}</th><th>${tr("Hora")}</th><th>${tr("Mensaje")}</th></tr></thead><tbody>
			${list.map((e) => {
				const [cls, name] = CONSOLE_LEVEL[e.level] || ["info", e.level];
				return `<tr class="console-${esc(e.level)}"><td><span class="label ${cls}">${tr(name)}</span></td><td>${fmtTime(e.timestamp)}</td>
					<td><pre class="console-text" data-no-i18n>${esc(e.text)}</pre>${e.location ? `<small class="console-loc" data-no-i18n>${esc(e.location)}</small>` : ""}</td></tr>`;
			}).join("")}</tbody></table>`;
	}

	// ---------- release: ¿podemos salir a producción? ----------
	const RELEASE_TEXT = {
		go: ["ok", "Listo para salir", "Ningún criterio del equipo se incumple."],
		risk: ["warn", "Se puede salir, con riesgos", "Nada bloquea, pero hay puntos que alguien debería revisar antes."],
		no_go: ["fail", "No salir todavía", "Se incumple al menos un criterio que bloquea la salida."],
	};

	/** Frase de negocio para cada criterio (detail trae los números). */
	function releaseCheckText(c) {
		const [a, b] = String(c.detail || "").split("/");
		const tests = c.tests?.length ? ` ${tr("({t})", { t: c.tests.slice(0, 5).join(", ") + (c.tests.length > 5 ? "…" : "") })}` : "";
		switch (c.id) {
		case "complete": return c.ok ? tr("La ejecución terminó completa.")
			: c.detail === "open" || c.detail === "running" ? tr("La ejecución sigue en curso: la decisión se toma cuando termine.")
			: tr("La ejecución quedó incompleta: hay tests que no llegaron a terminar.");
		case "evidence": return c.ok ? tr("Hay tests ejecutados que respaldan la decisión.") : tr("Ningún test se ejecutó (sin tests o todos omitidos): no hay evidencia para salir.");
		case "critical": return (c.ok ? tr("Ninguna funcionalidad crítica falló ({f}).", { f: c.detail }) : tr("Falló una funcionalidad crítica")) + (c.ok ? "" : tests);
		case "pass_rate": return tr("Tasa de éxito {p}% (mínimo {m}%).", { p: Number(a).toLocaleString(), m: Number(b).toLocaleString() });
		case "new_failures": return tr(a === "1" ? "1 fallo nuevo frente a la ejecución anterior (se toleran {m})." : "{n} fallos nuevos frente a la ejecución anterior (se toleran {m}).", { n: a, m: b }) + tests;
		case "quarantined": return (c.ok ? tr("Sin fallos en cuarentena.")
			: tr(a === "1" ? "1 fallo conocido en cuarentena: no bloquea, pero sigue pendiente." : "{n} fallos conocidos en cuarentena: no bloquean, pero siguen pendientes.", { n: a })) + (c.ok ? "" : tests);
		case "flaky": return tr(a === "1" ? "1 test inestable (se toleran {m})." : "{n} tests inestables (se toleran {m}).", { n: a, m: b }) + (c.ok ? "" : tests);
		}
		return c.id;
	}

	async function renderRelease() {
		const el = $("#view-release");
		const r = S.run;
		const key = `${r.id}:${r.status}:${r.total}:${r.failed}:${r.quarantined || 0}`;
		if (S.release?.key !== key) {
			const rel = S.release = { key, data: null };
			try {
				rel.data = await api(`/api/v1/runs/${r.id}/release`);
			} catch (err) {
				rel.error = err.message;
			}
			if (S.release !== rel || S.view !== "release") return; // llegó tarde: hay otra pedida
		}
		const d = S.release.data;
		if (!d) {
			setHTML(el, `<div class="card m-empty">${icon("i-warning")}<p>${esc(S.release.error || tr("Cargando…"))}</p></div>`);
			return;
		}
		const [cls, title, sub] = RELEASE_TEXT[d.decision] || RELEASE_TEXT.risk;
		const checks = d.checks.map((c) => `<li class="rel-check ${c.ok ? "ok" : c.severity}">
			<span class="rel-dot" aria-hidden="true"></span><span>${esc(releaseCheckText(c))}</span>
			${c.ok ? "" : `<small>${c.severity === "block" ? tr("bloquea") : tr("riesgo")}</small>`}</li>`).join("");
		const features = d.features.map((f) => `<div class="rel-feature ${esc(f.status)}">
			<div class="rel-feature-name">${f.name ? `<span data-no-i18n>${esc(f.name)}</span>` : tr("Sin categoría")}${f.critical ? `<small class="rel-crit">${tr("crítica")}</small>` : ""}</div>
			<div class="rel-feature-state">${{ ok: "✓", fail: "✗", warn: "!", skip: "–" }[f.status] || ""} ${tr("{p} de {t} pasan", { p: f.passed, t: f.total })}${f.quarantined ? ` · ${tr("{q} en cuarentena", { q: f.quarantined })}` : ""}</div>
		</div>`).join("");
		setHTML(el, `<div class="page-head"><div><h4 class="page-title">${tr("¿Podemos salir a producción?")}</h4>
				<p class="page-sub">${tr("Decisión sobre esta ejecución según los criterios del equipo (TRACEREPORTS_RELEASE_GATE).")}</p></div></div>
			<div class="card rel-banner ${cls}"><div class="rel-title">${tr(title)}</div><p>${tr(sub)}</p>
				<div class="rel-rate">${tr("Tasa de éxito")}: <b>${Number(d.pass_rate).toLocaleString()}%</b></div></div>
			<div class="card rel-checks"><h5>${tr("Criterios")}</h5><ul>${checks}</ul></div>
			<div class="card rel-features-card"><h5>${tr("Funcionalidades")}</h5><div class="rel-features">${features}</div></div>`);
	}

	/** Panel con lo que cambió en una llamada fallida frente a la última vez que el test pasó. */
	async function openBaseline(c, btn) {
		btn.disabled = true;
		let b = null;
		try {
			const res = await fetch(`/api/v1/network/${c.id}/baseline`, { headers: { Accept: "application/json" } });
			if (res.status === 200) b = await res.json();
			else if (res.status !== 204) throw new Error(`${res.status}`);
		} catch (err) {
			btn.disabled = false;
			alert(err.message);
			return;
		}
		btn.disabled = false;
		const v = (x) => (x === undefined ? "—" : esc(typeof x === "string" ? x : JSON.stringify(x)));
		let html;
		if (!b) {
			html = `<p class="m-hint">${tr("No hay una ejecución anterior donde este test haya pasado con esta llamada, así que no hay con qué comparar.")}</p>`;
		} else {
			const d = CF.diffCalls(c, b.conn);
			const bodyHTML = (title, diff) => !diff ? "" : `<h6>${title}</h6>` + (diff.json
				? (diff.json.length ? `<table class="diff-table"><tbody>${diff.json.map((x) => `<tr class="diff-${x.kind}"><td><code>${esc(x.path)}</code></td>
					<td>${x.kind === "added" ? "—" : v(x.before)}</td><td>${x.kind === "removed" ? "—" : v(x.after)}</td></tr>`).join("")}</tbody></table>`
					: `<p class="m-hint">${tr("Mismo contenido (cambió solo el formato).")}</p>`)
				: `<div class="diff-text"><pre>${esc(diff.text.before) || "—"}</pre><pre>${esc(diff.text.after) || "—"}</pre></div>`);
			html = `<p class="m-hint">${tr("Comparado con la ejecución #{r}, donde el test pasó.", { r: b.run_id })}
					${b.same_context ? "" : ` ${tr("Es de otra rama o ambiente del proyecto: no había una en verde en este mismo contexto.")}`}
					<a href="#run=${b.run_id}&view=tests&test=${b.test_id}">${tr("Abrir esa ejecución")}</a></p>
				<table class="diff-table"><thead><tr><th></th><th>${tr("Cuando pasó")}</th><th>${tr("Ahora")}</th></tr></thead><tbody>
					<tr class="${d.status.changed ? "diff-changed" : ""}"><td>Status</td><td>${esc(d.status.before)}</td><td>${esc(d.status.after)}</td></tr>
					<tr><td>${tr("Duración")}</td><td>${d.durationMs.before ?? "—"} ms</td><td>${d.durationMs.after ?? "—"} ms</td></tr>
					<tr><td>URL</td><td data-no-i18n>${esc(b.conn.url)}</td><td data-no-i18n>${esc(c.url)}</td></tr>
				</tbody></table>
				${d.headers.length ? `<h6>${tr("Headers de respuesta que cambiaron")}</h6><table class="diff-table"><tbody>${d.headers.map((h) =>
					`<tr class="diff-${h.before === undefined ? "added" : h.after === undefined ? "removed" : "changed"}"><td><code>${esc(h.name)}</code></td><td>${v(h.before)}</td><td>${v(h.after)}</td></tr>`).join("")}</tbody></table>` : ""}
				${bodyHTML(tr("Body de la respuesta"), d.response)}${bodyHTML(tr("Body del request"), d.request)}
				${!d.status.changed && !d.headers.length && !d.response && !d.request ? `<p class="m-hint">${tr("La llamada es igual a cuando el test pasó: la causa del fallo probablemente está en otro lado.")}</p>` : ""}`;
		}
		CF.Drawer.open({ title: tr("Qué cambió desde que pasó"), subtitle: `${c.method} ${c.url}`, body: `<div class="baseline-diff">${html}</div>` });
	}

	/** Comandos para correr este test en local (según el framework y la identidad del test): este test
	 * y su archivo completo; el archivo primero si el error dice que dependía de un test anterior. El
	 * commit de la ejecución va como nota, no dentro del comando. */
	function reproBlock(t) {
		const lang = window.TraceReportsI18n?.lang === "en" ? "en" : "es";
		const cmds = CF.orderedRepro(CF.reproCommands({ framework: S.run?.framework, key: t.key }), t.status === "FAIL" && CF.dependsOnEarlier(t.error_message));
		if (!cmds.length) return "";
		const note = CF.reproCommitNote(S.run?.commit, lang);
		return `<details class="repro"><summary>${tr("Reproducir en local")}</summary>
			${cmds.map((c) => `<div class="repro-row" data-scope="${esc(c.scope)}"><span class="repro-lbl" data-no-i18n data-tip="${esc(CF.reproScope(c.scope, lang))}">${esc(CF.reproScope(c.scope, lang, true))} · ${esc(c.label)}</span>
				<code data-no-i18n>${esc(c.cmd)}</code><button class="copy-btn" data-copy="${esc(c.cmd)}">${tr("Copiar")}</button></div>`).join("")}
			${cmds.some((c) => c.scope !== "test") ? `<p class="repro-hint" data-no-i18n>${esc(lang === "en" ? "Run the whole file when the test depends on earlier ones (data they create, steps they leave done): alone it fails for another reason." : "Corre el archivo completo cuando el test depende de otros anteriores (datos que crean, pasos que dejan hechos): solo, falla por otra razón.")}</p>` : ""}
			${note ? `<p class="repro-note" data-no-i18n>${esc(note)}</p>` : ""}
		</details>`;
	}

	/** Trace y video de Playwright: el video se ve aquí; el trace se descarga o se abre en el Trace Viewer. */
	function artifactsBlock(t) {
		const list = t.artifacts || [];
		if (!list.length) return "";
		const abs = (u) => new URL(u, location.href).href;
		// el Trace Viewer oficial descarga el trace desde el navegador: necesita una URL https
		const viewer = location.protocol === "https:";
		const items = list.map((a) => a.kind === "video"
			? `<figure class="art-video"><video controls preload="metadata" src="${esc(a.url)}"></video><figcaption data-no-i18n>${esc(a.name)}</figcaption></figure>`
			: `<div class="art-trace"><span class="art-name" data-no-i18n>${esc(a.name)}</span>
				<a class="cf-btn cf-btn-sm" href="${esc(a.url)}" download>${icon("i-download")}${tr("Descargar trace")}</a>
				${viewer ? `<a class="cf-btn cf-btn-sm cf-btn-primary" target="_blank" rel="noopener" href="https://trace.playwright.dev/?trace=${encodeURIComponent(abs(a.url))}">${icon("i-play")}${tr("Abrir en Trace Viewer")} ↗</a>` : ""}
				<code class="art-cmd" data-no-i18n>npx playwright show-trace ${esc(a.url.split("/").pop())}</code></div>`).join("");
		return `<div class="card artifacts"><div class="bloque-titulo">${tr("Trace y video")}</div>${items}</div>`;
	}

	const VERDICTS = [["product_bug", "Bug de producto"], ["test_bug", "Test roto"], ["environment", "Ambiente"],
		["data", "Datos de prueba"], ["flaky", "Flaky"], ["other", "Otro"]];
	const verdictName = (v) => tr(VERDICTS.find(([id]) => id === v)?.[1] || v);

	function ownerChip(t) {
		return t.owner ? `<span class="label owner-chip" data-tip="${tr("Dueño del test según las reglas de TRACEREPORTS_OWNERS")}">${tr("Dueño: {o}", { o: t.owner })}</span>` : "";
	}

	function verdictChip(t) {
		return t.verdict ? `<span class="verdict-chip sm v-${esc(t.verdict.verdict)}">${esc(verdictName(t.verdict.verdict))}</span>` : "";
	}

	function readAuthor() {
		try { return localStorage.getItem("tracereports-author") || ""; } catch { return ""; }
	}

	/** Clasificación colaborativa del fallo: el veredicto de esta ejecución o el de la anterior. */
	function verdictBlock(t) {
		const v = t.verdict, prev = t.previous_verdict;
		const who = (x) => [x.author, fmtDateTime(x.created_at)].filter(Boolean).join(" · ");
		const editing = S.verdictEdit === t.id;
		let body = "";
		if (v && !editing) {
			body = `<div class="verdict-now"><span class="verdict-chip v-${esc(v.verdict)}">${esc(verdictName(v.verdict))}</span>
				${v.comment ? `<span class="verdict-comment" data-no-i18n>${esc(v.comment)}</span>` : ""}<small>${esc(who(v))}</small>
				${canAct() ? `<button class="cf-btn cf-btn-sm" data-verdict-edit="${t.id}">${tr("Cambiar")}</button>` : ""}</div>`;
		} else if (!editing) {
			body = prev ? `<div class="verdict-prev">${tr("En la ejecución #{r} lo clasificaron como", { r: prev.run_id })}
					<span class="verdict-chip v-${esc(prev.verdict)}">${esc(verdictName(prev.verdict))}</span>
					${prev.comment ? `<span class="verdict-comment" data-no-i18n>“${esc(prev.comment)}”</span>` : ""}<small>${esc(who(prev))}</small>
					${canAct() ? `<button class="cf-btn cf-btn-sm" data-verdict-same="${t.id}">${tr("Mismo veredicto")}</button>` : ""}</div>` : "";
			if (canAct()) body += `<button class="cf-btn cf-btn-sm" data-verdict-edit="${t.id}">${tr("Clasificar este fallo")}</button>`;
		} else {
			const cur = v?.verdict || prev?.verdict || "";
			body = `<form class="verdict-form" data-verdict-form="${t.id}">
				<div class="verdict-options" role="radiogroup" aria-label="${tr("Veredicto")}">${VERDICTS.map(([id, name]) =>
					`<label class="verdict-opt v-${id}"><input type="radio" name="verdict" value="${id}" ${cur === id ? "checked" : ""} required><span>${tr(name)}</span></label>`).join("")}</div>
				<textarea name="comment" rows="2" maxlength="2000" placeholder="${tr("Qué encontraste (opcional): causa, ticket, a quién se avisó…")}">${esc(v?.comment || "")}</textarea>
				<div class="verdict-row"><input name="author" maxlength="120" value="${esc(readAuthor())}" placeholder="${tr("Tu nombre (opcional)")}">
					<button type="submit" class="cf-btn cf-btn-sm cf-btn-primary">${tr("Guardar")}</button>
					<button type="button" class="cf-btn cf-btn-sm" data-verdict-cancel>${tr("Cancelar")}</button></div>
			</form>`;
		}
		if (!body) return "";
		return `<div class="verdict-block"><div class="bloque-titulo">${tr("Clasificación")}</div>${body}</div>`;
	}

	async function saveVerdict(testId, verdict, comment, author) {
		try {
			try { localStorage.setItem("tracereports-author", author || ""); } catch { /* sin almacenamiento */ }
			await apiSend("POST", `/api/v1/ui/tests/${testId}/verdict`, { verdict, comment, author });
			S.verdictEdit = null;
			await loadRun();
			await loadTest();
		} catch (err) {
			alert(err.message);
		}
	}

	/** Cuarentena del test: sus fallos no ponen la ejecución en rojo hasta que vence. */
	function quarantineChip(t, compact) {
		const q = t.quarantine;
		if (!q) return "";
		const tr = window.TraceReportsI18n.t;
		const until = fmtDateTime(q.until);
		const tip = q.active
			? tr("En cuarentena hasta {d}{o}. Motivo: {r}. Sus fallos se ven, pero no ponen la ejecución en rojo.", { d: until, o: q.owner ? ` · ${q.owner}` : "", r: q.reason })
			: tr("La cuarentena venció el {d}: sus fallos vuelven a contar.", { d: until });
		return `<span class="quar-chip ${q.active ? "" : "expired"} ${compact ? "sm" : ""}" data-tip="${esc(tip)}">${q.active ? tr("En cuarentena") : tr("Cuarentena vencida")}</span>`;
	}

	function quarantineButton(t) {
		if (!canAct()) return "";
		if (t.quarantine?.active) return `<button class="cf-btn cf-btn-sm" data-quar-remove="${t.id}">${tr("Quitar cuarentena")}</button>`;
		if (t.status !== "FAIL" && !runTest(t).flaky) return "";
		return `<button class="cf-btn cf-btn-sm" data-quar-open="${t.id}" data-tip="${tr("Para un test inestable conocido: sus fallos se siguen viendo, pero no ponen las ejecuciones en rojo hasta la fecha que elijas.")}">${tr("Poner en cuarentena")}</button>`;
	}

	function quarantineForm(t) {
		if (S.quar?.testId !== t.id) return "";
		const m = S.quar.msg;
		return `<form class="card quar-form" data-quar-form="${t.id}">
			<label class="field"><span class="field-label">${tr("Motivo")}</span>
				<input name="reason" required maxlength="500" placeholder="${tr("Ej.: timeout intermitente del proveedor de pagos (ticket SHOP-34)")}"></label>
			<div class="quar-row">
				<label class="field"><span class="field-label">${tr("Dueño")}</span><input name="owner" maxlength="200" placeholder="${tr("Equipo o persona")}"></label>
				<label class="field"><span class="field-label">${tr("Vence en")}</span><select name="days">
					${[7, 14, 30, 90].map((d) => `<option value="${d}" ${d === 14 ? "selected" : ""}>${tr("{n} días", { n: d })}</option>`).join("")}</select></label>
			</div>
			${m ? `<p class="set-msg err" role="status">${esc(m)}</p>` : ""}
			<div class="quar-actions"><button type="submit" class="cf-btn cf-btn-sm cf-btn-primary">${tr("Poner en cuarentena")}</button>
				<button type="button" class="cf-btn cf-btn-sm" data-quar-cancel>${tr("Cancelar")}</button></div>
		</form>`;
	}

	async function quarantineAction(method, path, body) {
		try {
			await apiSend(method, path, body);
			S.quar = null;
			await loadRun();
			await loadTest();
		} catch (err) {
			S.quar = { ...(S.quar || {}), msg: err.message };
			renderTestDetail();
		}
	}

	/** 🐢 Latencia +420ms: el p95 de su red empeoró frente a su historial. */
	function driftChip(t, compact) {
		const d = t.net_drift;
		if (!d) return "";
		return `<button class="drift-chip ${compact ? "sm" : ""}" data-drift="${t.id}"
			data-tip-title="La red de este test está más lenta" data-tip="p95 de sus llamadas: ${d.current_p95} ms ahora vs ${d.baseline_p95} ms (mediana de ${d.runs} ejecuciones anteriores). Clic para ver qué endpoint empeoró.">🐢 ${compact ? "" : "Latencia "}+${d.delta_ms}ms</button>`;
	}

	async function openDrift(testId) {
		const t = S.run.tests.find((x) => x.id === testId);
		const d = t?.net_drift;
		const slot = CF.Drawer.open({
			title: "Latencia de red vs. historial",
			subtitle: d ? `${t.name} · p95 ${d.current_p95} ms ahora vs ${d.baseline_p95} ms (mediana de ${d.runs} ejecuciones anteriores)` : "",
			body: `<div class="placeholder">Cargando…</div>`,
		});
		try {
			const eps = await api(`/api/v1/tests/${testId}/drift`);
			const max = Math.max(1, ...eps.map((e) => Math.max(e.current_p95, e.baseline_p95)));
			slot.innerHTML = `<table class="cf-table"><thead><tr><th>Endpoint</th><th>Llamadas</th><th>p95 histórico</th><th>p95 ahora</th><th>Δ</th></tr></thead><tbody>
				${eps.map((e) => `<tr><td><span class="nbadge" style="background:${METHOD_COLOR[e.method] || "#6b7280"}">${esc(e.method)}</span> <span class="mono">${esc((e.host || "") + e.path)}</span>
					<span class="cf-bar"><i style="width:${((e.current_p95 / max) * 100).toFixed(1)}%"></i></span></td>
					<td class="num">${e.count}</td><td class="num">${e.baseline_p95 ? `${e.baseline_p95} ms` : "—"}</td><td class="num">${e.current_p95} ms</td>
					<td class="num">${!e.baseline_p95 ? `<span class="cf-delta-new">nuevo</span>` : e.delta_ms > 0 ? `<span class="cf-delta-up">+${e.delta_ms} ms</span>` : `${e.delta_ms} ms`}</td></tr>`).join("")}
				</tbody></table>`;
		} catch (err) {
			slot.innerHTML = `<div class="placeholder">No se pudo cargar: ${esc(err.message)}</div>`;
		}
	}

	// ---- Incidentes parecidos (veredictos del equipo) ----
	S.sim = S.sim || {};

	function ensureSimilar(t) {
		if (t.status !== "FAIL" || STATIC || S.sim[t.id] !== undefined) return;
		S.sim[t.id] = null; // cargando
		fetch(`/api/v1/tests/${t.id}/similar`).then((r) => (r.ok ? r.json() : []))
			.then((list) => { S.sim[t.id] = list; if (S.test?.id === t.id) renderTestDetail(); })
			.catch(() => { S.sim[t.id] = []; });
	}

	/** Fallos ya clasificados por el equipo que se parecen a este (sin IA: TF-IDF sobre su firma). */
	function similarBlock(t) {
		const list = S.sim[t.id];
		if (!list?.length) return "";
		const tr = window.TraceReportsI18n.t;
		const items = list.map((s) => `<li class="similar-item"><span class="similar-score">${s.score}%</span>
			<a href="#run=${s.run_id}&view=tests&test=${s.test_id}" data-no-i18n>${esc(s.test_name || "#" + s.test_id)}</a>
			<span class="verdict-chip sm v-${esc(s.verdict)}">${esc(verdictName(s.verdict))}</span>
			${s.comment ? `<span class="verdict-comment" data-no-i18n>${esc(s.comment)}</span>` : ""}
			<small>${esc(fmtDateTime(s.created_at))}</small></li>`).join("");
		return `<h6 tabindex="0" data-tip="${esc(tr("Fallos que el equipo ya clasificó y se parecen a este por su firma (la llamada que falló o el error). El porcentaje es el parecido, no una probabilidad."))}">${tr("Incidentes parecidos")}</h6>
			<ul class="similar-list">${items}</ul>`;
	}

	// ---- AI Locator Recommender ----
	const locKey = (t) => `${t.id}:${t.status}:${t.triage?.state || ""}:${t.triage?.locator_pick || ""}`;
	const currentLocator = () => (S.test ? S.loc[locKey(S.test)] || null : null);

	function ensureLocator(t) {
		if (t.status !== "FAIL") return;
		const key = locKey(t);
		if (S.loc[key] !== undefined) return;
		S.loc[key] = null; // cargando
		const load = STATIC
			? Promise.resolve(STATIC.locators?.[t.id] || false)
			: fetch(`/api/v1/tests/${t.id}/locator`).then((r) => (r.status === 200 ? r.json() : false));
		// backend_cause: el backend falló primero; sin reemplazos que mostrar (solo lo usa el Inspector)
		load.then((rep) => { S.loc[key] = rep?.backend_cause ? false : rep; if (S.test?.id === t.id) renderTestDetail(); })
			.catch(() => { S.loc[key] = false; });
	}

	function orderedSuggestions(rep) {
		const list = rep.suggestions.map((s, i) => ({ ...s, i, pick: rep.ai_pick && (rep.ai_pick === s.python || rep.ai_pick === s.js) }));
		return list.sort((a, b) => (b.pick ? 1 : 0) - (a.pick ? 1 : 0));
	}

	const SELENIUM_ERROR = /selenium|webdriver|NoSuchElement|StaleElementReference|\bBy\.(?:id|xpath|css|cssSelector|name|className|linkText|ID|XPATH|CSS_SELECTOR|NAME)\b|\bBy\((?:css selector|xpath|id|name)/i;

	/**
	 * Herramienta (playwright | selenium) y lenguaje (python | js | java) de los snippets: lo que
	 * eligió la persona, o lo del test: Selenium si el error es de Selenium, y el lenguaje del framework.
	 */
	function locPrefs(t) {
		const fw = String(S.run?.framework || "").toLowerCase();
		const tool = S.locTool || (SELENIUM_ERROR.test(`${t?.error_message || ""}\n${t?.error_trace || ""}`) ? "selenium" : "playwright");
		let lang = S.locLang;
		if (!lang) {
			lang = /junit|maven|gradle|testng|java/.test(fw) ? "java" : /playwright|selenium|jest|mocha|node|vitest|js/.test(fw) && fw !== "playwright-python" ? "js" : "python";
		}
		if (lang === "java" && tool !== "selenium") lang = "python"; // los snippets de Java son de Selenium
		return { tool, lang };
	}

	/** El snippet de una sugerencia en la herramienta y el lenguaje elegidos (Playwright si falta). */
	function snippetOf(s, t) {
		const { tool, lang } = locPrefs(t);
		if (tool === "selenium") {
			const sel = { python: s.selenium_python, js: s.selenium_js, java: s.selenium_java }[lang];
			if (sel) return sel;
		}
		return lang === "js" ? s.js : s.python;
	}

	/** El servidor guardó el snapshot de la página de este test (campo explícito; en uno anterior a
	 * has_dom, el viewport que solo trae el snapshot). */
	const hasDom = (rep) => (rep.has_dom !== undefined ? !!rep.has_dom : !!(rep.viewport && rep.viewport.w));

	// un XPath que busca texto (contains(normalize-space(.)…), text()=…) suele validar contenido, no ubicar un control
	const TEXT_XPATH = /^\/\/.*(?:normalize-space\(|text\(\)|contains\(\.\s*,)/;

	/** Por qué no hay reemplazos: selector cortado, snapshot sin candidatos, o sin snapshot. */
	function locatorEmpty(rep) {
		if (rep.failed_selector_truncated) {
			return `<p class="loc-empty">${tr("El selector llegó cortado (el error se guarda con sus primeros caracteres y no traía el selector completo): no se buscan reemplazos para medio selector.")}</p>`;
		}
		if (hasDom(rep)) {
			const content = TEXT_XPATH.test(rep.failed_selector || "");
			return `<p class="loc-empty">${tr("Hay snapshot de la página, pero ningún elemento sirve de reemplazo para este selector.")} ${content
				? tr("El selector busca un texto: parece una validación de contenido o de flujo (el dato no estaba en la página), no un selector roto.")
				: tr("Puede que el elemento no sea interactivo (por ejemplo un div con solo texto) o que el fallo no sea del selector sino del contenido o del flujo.")}</p>`;
		}
		return `<p class="loc-empty">No hay snapshot de la página para proponer reemplazos. Los clientes lo envían al fallar (<code>capturar_dom</code> en Python, <code>Test.DOM</code> en Go).</p>`;
	}

	function locatorCard(t) {
		const rep = S.loc[locKey(t)];
		if (!rep) return "";
		const sugg = orderedSuggestions(rep);
		// sin red (un import de Allure o JUnit) el backend caído parece un selector roto: si la IA ya
		// dijo backend o infraestructura y no hay reemplazos, la tarjeta solo despistaría
		const cat = t.triage && t.triage.state === "DONE" ? t.triage.category : "";
		if (!sugg.length && (cat === "BACKEND_TIMEOUT" || cat === "INFRA_ERROR")) return "";
		const snippet = (s) => snippetOf(s, t);
		const { tool, lang } = locPrefs(t);
		const hasSelenium = sugg.some((s) => s.selenium_python);
		const items = sugg.map((s) => `<li class="loc-item ${s.pick ? "ai-pick" : ""}" data-loc-i="${s.i}">
				<code class="loc-code">${esc(snippet(s))}</code>
				<div class="loc-btns">
					<button class="cf-btn cf-btn-sm" data-loc-copy="${s.i}" data-tip="Copia la línea lista para pegar en tu Page Object">${icon("i-copy")}Copiar snippet</button>
					${s.box && rep.screenshot ? `<button class="cf-btn cf-btn-sm" data-loc-inspect="${s.i}" data-tip="Marca este elemento sobre la captura del momento del fallo">${icon("i-crosshair")}Ver en captura</button>` : ""}
				</div>
				<div class="loc-meta">${s.pick ? `<span class="loc-ai">${icon("i-spark")}Recomendado por IA</span>` : ""}
					<span class="loc-strength ${esc(s.robustness)}">Robustez ${esc(s.robustness)}</span><span>${esc(s.element)}</span>
					<span>${esc(s.pick && rep.ai_reason ? rep.ai_reason : s.reason)}</span></div>
			</li>`).join("");
		return `<details class="loc-card" id="locator-card" ${S.locOpen === false ? "" : "open"}>
			<summary><span class="loc-badge">${icon("i-crosshair")}Locator drift</span>
				<span class="loc-title">${!sugg.length && hasDom(rep) && TEXT_XPATH.test(rep.failed_selector || "") ? tr("Selector de texto sin coincidencias") : "Selector roto"}${sugg.length ? ` · ${sugg.length} reemplazo${sugg.length === 1 ? "" : "s"} sugerido${sugg.length === 1 ? "" : "s"}` : ""}</span>
				<svg class="chev" aria-hidden="true"><use href="#i-chevron"/></svg></summary>
			<div class="loc-body">
				<dl class="loc-diff"><dt>Fallido</dt><dd><code class="loc-failed">${esc(rep.failed_selector || "(no se pudo extraer del error)")}</code></dd>
					${rep.page_url ? `<dt>Página</dt><dd class="mono" style="font-size:12px;word-break:break-all">${esc(rep.page_url)}</dd>` : ""}</dl>
				${sugg.length ? `<div class="loc-toolbar"><span>Sugeridos, del más robusto al menos robusto</span>
					<div class="loc-switches">
					${hasSelenium ? `<div class="loc-lang" role="group" aria-label="Herramienta del snippet">
						<button data-loc-tool="playwright" aria-pressed="${tool === "playwright"}">Playwright</button><button data-loc-tool="selenium" aria-pressed="${tool === "selenium"}">Selenium</button></div>` : ""}
					<div class="loc-lang" role="group" aria-label="Lenguaje del snippet">
						<button data-loc-lang="python" aria-pressed="${lang === "python"}">Python</button><button data-loc-lang="js" aria-pressed="${lang === "js"}">JS / TS</button>${tool === "selenium" && hasSelenium ? `<button data-loc-lang="java" aria-pressed="${lang === "java"}">Java</button>` : ""}</div></div></div>
					<ol class="loc-list">${items}</ol>`
					: locatorEmpty(rep)}
			</div></details>`;
	}

	function inspectLocator(i) {
		const rep = currentLocator();
		const s = rep?.suggestions[i];
		if (!s?.box) return;
		const vw = rep.viewport?.w || 1, vh = rep.viewport?.h || 1;
		const pct = (b) => `left:${(b.x / vw) * 100}%;top:${(b.y / vh) * 100}%;width:${(b.w / vw) * 100}%;height:${(b.h / vh) * 100}%`;
		const others = rep.suggestions.filter((o, k) => k !== i && o.box).slice(0, 3);
		CF.Drawer.open({
			title: "Elemento sugerido en la captura",
			subtitle: `${s.element} · ${snippetOf(s, S.test)}`,
			body: `<div class="bbox-view"><img src="${esc(rep.screenshot)}" alt="Captura del momento del fallo">
					${others.map((o) => `<span class="bbox alt" style="${pct(o.box)}"><span class="bbox-tag">alternativa</span></span>`).join("")}
					<span class="bbox" style="${pct(s.box)}"><span class="bbox-tag">sugerido</span></span></div>
				<div class="bbox-legend"><span>Recuadro verde: elemento del selector sugerido.</span>${others.length ? "<span>Punteado: otras alternativas.</span>" : ""}
					<span>Posiciones tomadas del snapshot de la página al fallar.</span></div>`,
		});
	}

	// ---- Time-Travel Step Replay ----
	function mountReplay(t) {
		const el = $("#replay-panel");
		if (!el) return;
		if (S.player && S.player.el === el && S.player.testId === t.id) {
			if (S.player.steps.length !== t.logs.length) S.player.setSteps(t.logs, S.autoScroll && t.status === "RUNNING" ? t.logs.length - 1 : S.player.i);
			return;
		}
		S.player?.destroy();
		const keep = S.replay.testId === t.id;
		S.player = new CF.TimeTravelPlayer(el, t.logs, {
			start: t.started_at, index: keep ? S.replay.index : 0, speed: keep ? S.replay.speed : 1,
			onChange: (i, speed) => { S.replay = { testId: t.id, index: i, speed }; },
			onLightbox: (src, caption) => openLightbox(src, caption),
		});
		S.player.testId = t.id;
	}

	// ---- En vivo (web/live.js) ----
	/** Trae las ejecuciones y la elegida (o la más nueva en curso). Un turno por carga: la vieja no pisa a la nueva. */
	async function loadLiveView(force = false) {
		if (STATIC || !window.TraceReportsLive) return;
		const L = S.liveView;
		if (L.ui.paused && L.loaded && !force) return; // en pausa: la vista queda quieta para analizar
		// un pedido a la vez: si el servidor tarda, los avisos y el reloj no apilan pedidos colgados (se pide
		// otra vez al terminar el que está en curso); después de un error, se espera cada vez más
		if (L.inflight) { L.again = true; return; }
		if (!force && Date.now() < (L.retryAt || 0)) return;
		L.inflight = true;
		const current = turn("live");
		try {
			const list = await api("/api/v1/live", LIVE_TIMEOUT_MS);
			if (!current()) return;
			L.list = list.executions; L.now = list.now; L.no_signal_ms = list.no_signal_ms;
			// las anteriores (para el selector y la pantalla vacía): aparte y como mucho una vez por minuto
			// cada minuto, la primera página al día (si no se pidieron más: no se pierde lo cargado ni el scroll)
			if (!L.history?.loading && (!L.history?.at || (Date.now() - L.history.at > 60000 && (L.history.pages || 1) === 1))) loadLiveHistory();
			const fallback = (L.list.find((e) => e.running) || L.list[0])?.key || "";
			// una ejecución pedida que ya no está en la lista (un enlace, "Observabilidad" de un run anterior,
			// r:<run>) se pide igual: el servidor la arma por su clave; si no existe, la más nueva
			if (!L.key) L.key = fallback;
			// lo que se ve sigue en pantalla hasta que llega lo nuevo: vaciarlo antes de pedirlo dejaba la vista
			// entera en "Cargando…" mientras el servidor respondía (un clic en el mapa redibuja en ese rato)
			if (L.exec && L.exec.key !== L.key) L.exec = null;
			if (L.key) {
				let one = await api(`/api/v1/live/execution?key=${encodeURIComponent(L.key)}`, LIVE_TIMEOUT_MS).catch((err) => {
					if (L.list.some((e) => e.key === L.key) || !fallback) throw err;
					return null;
				});
				if (!current()) return;
				if (!one) { L.key = fallback; one = await api(`/api/v1/live/execution?key=${encodeURIComponent(fallback)}`, LIVE_TIMEOUT_MS); if (!current()) return; }
				L.exec = one.execution; L.now = one.now;
				if (L.key.startsWith("r:")) L.key = one.execution.key; // pedida por un run: desde ahora, su clave
				// la anterior también en el selector (con su hora: "Anteriores")
				if (!L.list.some((e) => e.key === L.key)) { const { runs, ...summary } = one.execution; L.list = [...L.list, summary]; }
			}
			L.loaded = true; L.fails = 0; L.retryAt = 0;
			if (L.panel) loadLivePanel(L.panel.id, true); // el test abierto en el panel sigue al día
			if (L.key && (L.epKey !== L.key || Date.now() - (L.epAt || 0) > 5000)) loadLiveEndpoints(L.key);
			loadLiveRegressions();
		} catch (err) {
			L.fails = (L.fails || 0) + 1;
			L.retryAt = Date.now() + Math.min(60000, 2000 * 2 ** (L.fails - 1)); // 2, 4, 8… hasta 60 s
			if (!current()) return;
			L.error = err.message;
		} finally {
			L.inflight = false;
			if (L.again) { L.again = false; scheduleLiveView(); }
		}
		if (S.view === "live") { renderLiveView(); writeHash(); }
	}
	/** Cuánto espera «En vivo» a que el servidor conteste antes de dar el pedido por perdido. */
	const LIVE_TIMEOUT_MS = 20000;

	/**
	 * Las ejecuciones anteriores: el selector las ofrece en "Anteriores" y, si nada corre ni terminó hace poco,
	 * la pantalla vacía las lista para abrir una sin saber su execution_id. Se buscan en el servidor (lo escrito
	 * en el buscador que está a la vista, o el día si es una fecha, y el chip de fecha), de a una página;
	 * append trae la siguiente ("Cargar más antiguas"). No frena la vista en vivo: una respuesta vieja no pisa
	 * a la nueva.
	 */
	async function loadLiveHistory(append = false) {
		const L = S.liveView, LV = window.TraceReportsLive;
		const H = (L.history ||= { items: undefined, total: undefined, next: 0, at: 0, loading: false });
		const typed = L.list?.length ? L.ui.pickerQ : L.ui.histQ;
		const qy = LV.historyQuery(typed, L.list?.length ? "" : L.ui.histRange, Date.now());
		const params = new URLSearchParams();
		if (qy.q) params.set("q", qy.q);
		if (qy.from) params.set("from", String(qy.from));
		if (qy.to) params.set("to", String(qy.to));
		if (append && H.next) params.set("before", String(H.next));
		const current = turn("live-history");
		H.loading = true;
		if (S.view === "live") renderLiveView();
		try {
			const r = await api(`/api/v1/live/history?${params}`);
			if (!current()) return;
			const seen = new Set(append ? (H.items || []).map((x) => x.key) : []);
			H.items = append ? [...(H.items || []), ...r.executions.filter((x) => !seen.has(x.key))] : r.executions;
			Object.assign(H, { total: r.total, next: r.next, at: Date.now(), pages: append ? (H.pages || 1) + 1 : 1, filtered: !!(qy.q || qy.from || qy.to) });
		} catch {
			if (!current()) return;
			H.items ||= []; H.at = Date.now(); // sin historial, la vista sigue igual
		}
		H.loading = false;
		if (S.view === "live") renderLiveView();
	}
	/** La vista previa de una anterior: lo que falló (con su error) y el run para "Ver el reporte". Con caché. */
	let histDetailTimer = 0;
	function loadHistDetailSoon(key) {
		const L = S.liveView;
		if (!key || L.histDetail?.has(key)) return;
		clearTimeout(histDetailTimer);
		histDetailTimer = setTimeout(async () => {
			try {
				const r = await api(`/api/v1/live/execution?key=${encodeURIComponent(key)}`);
				const LV = window.TraceReportsLive, runs = r.execution?.runs || [];
				const fails = runs.flatMap((run) => (run.tests || []).filter((x) => x.state === "FAIL" || x.state === "INTERRUPTED")
					.map((x) => ({ name: x.name, error: LV.errorSignature(x.last_step) })));
				const cache = (L.histDetail ||= new Map());
				cache.set(key, { fails, run: runs[0]?.id || 0 });
				if (cache.size > 60) cache.delete(cache.keys().next().value); // las 60 últimas vistas
			} catch { /* sin detalle: la vista previa queda con lo que ya tiene */ }
			if (S.view === "live") renderLiveView();
		}, 200); // al quedarse en una (no con cada flecha)
	}
	/** Después de dibujar el historial: su scroll y la fila enfocada siguen donde estaban; al llegar al final, más. */
	let histObserver = null;
	function afterHistRender(el, top, focusKey) {
		const L = S.liveView, pane = el.querySelector("[data-live-histpane]");
		if (!pane) { histObserver?.disconnect(); histObserver = null; return; }
		pane.scrollTop = top;
		if (focusKey) [...pane.querySelectorAll("[data-live-histsel]")].find((r) => r.dataset.liveHistsel === focusKey)?.focus({ preventScroll: true });
		const sel = L.ui.histSel || L.history?.items?.[0]?.key;
		if (sel && !L.histDetail?.has(sel)) loadHistDetailSoon(sel);
		const sentinel = pane.querySelector("[data-live-histsentinel]");
		histObserver?.disconnect();
		if (sentinel && "IntersectionObserver" in window) {
			histObserver = new IntersectionObserver((entries) => {
				if (entries.some((x) => x.isIntersecting) && L.history?.next && !L.history.loading) loadLiveHistory(true);
			}, { root: pane, rootMargin: "120px" });
			histObserver.observe(sentinel);
		}
	}
	let liveHistTimer = 0;
	/** Buscar en el servidor al dejar de escribir (250 ms), no con cada tecla. */
	const liveHistorySoon = () => { clearTimeout(liveHistTimer); liveHistTimer = setTimeout(() => loadLiveHistory(), 250); };

	/** Los filtros de En vivo: se mantienen entre refrescos (los dibuja live.js; ver su render). */
	S.ingest = [];
	S.ingestSince = Date.now(); // desde cuándo esta pestaña cuenta los eventos del stream
	function liveUI() {
		return { problems: null, states: [], files: [], workers: [], errors: [], more: {}, sort: null, paused: false,
			group: "file", q: "", zoom: null, allLanes: false, hideIdle: false, showOk: false, open: {}, openAll: null, stacks: {}, attnAll: false, pickerOpen: false, pickerQ: "", histQ: "", histRange: "", tables: {},
			// preferencias de lectura de esta persona: el ancho de la columna Test y los nombres en 2 líneas
			...livePrefs() };
	}
	function livePrefs() {
		try { const p = JSON.parse(localStorage.getItem("tr-live-prefs") || "{}"); return { nameW: Number(p.nameW) || 0, wrap: !!p.wrap, compact: !!p.compact }; } catch { return { nameW: 0, wrap: false, compact: false }; }
	}
	function saveLivePrefs(ui) {
		try { localStorage.setItem("tr-live-prefs", JSON.stringify({ nameW: ui.nameW || 0, wrap: !!ui.wrap, compact: !!ui.compact })); } catch { /* sin almacenamiento: queda para esta sesión */ }
	}

	/** El panel lateral: el test con sus pasos y su última captura, sin salir de la vista. */
	/**
	 * El detalle de los tests ya vistos (o pre-cargados al pasar el mouse por el mapa): elegir uno
	 * muestra lo que se tiene al instante y lo actualiza por detrás, en vez de vaciar el panel y
	 * volver a dibujarlo (era el "pestañeo" al cambiar de test). Como mucho 40, el último usado manda.
	 */
	const LIVE_PANEL_CACHE = 40;
	function livePanelCache() {
		const L = S.liveView;
		return (L.panelCache ||= new Map());
	}
	function rememberLivePanel(id, data) {
		const c = livePanelCache();
		c.delete(id);
		c.set(id, { ...(c.get(id) || {}), ...data });
		while (c.size > LIVE_PANEL_CACHE) c.delete(c.keys().next().value);
	}
	/** Pasar el mouse por un test del mapa pide su detalle (una vez): al hacer clic ya está. */
	function prefetchLivePanel(id) {
		const L = S.liveView;
		if (!id || livePanelCache().has(id) || L.prefetching?.has(id)) return;
		(L.prefetching ||= new Set()).add(id);
		Promise.all([api(`/api/v1/tests/${id}`), api(`/api/v1/tests/${id}/network`).catch(() => [])])
			.then(([test, net]) => rememberLivePanel(id, { test, net: Array.isArray(net) ? net : net?.connections || [], netAt: Date.now() }))
			.catch(() => {}).finally(() => L.prefetching?.delete(id));
	}

	async function loadLivePanel(id, refresh = false) {
		const L = S.liveView;
		if (!refresh) {
			const known = livePanelCache().get(id), prev = L.panel;
			// lo que ya se tiene de ese test; si no, con el panel abierto, lo anterior atenuado hasta que
			// llegue lo nuevo (el encabezado ya es del test elegido): nunca un panel vacío entre medio
			L.ui.escPick = id; // Escalar sigue con este test aunque se cierre el panel (hasta elegir otro o soltarlo)
			L.panel = { id, tab: prev?.tab || "trace",
				...(known ? { test: known.test, net: known.net, netAt: known.netAt } : prev?.test ? { test: prev.test, net: prev.net, stale: true } : {}) };
			renderLiveView();
		}
		const prev = L.panel;
		const running = !prev?.test || prev.test.status === "RUNNING";
		const netDue = !refresh || (running && Date.now() - (prev.netAt || 0) > 8000);
		try {
			const [test, net] = await Promise.all([api(`/api/v1/tests/${id}`), netDue ? api(`/api/v1/tests/${id}/network`).catch(() => []) : null]);
			const fresh = netDue ? { net: Array.isArray(net) ? net : net?.connections || [], netAt: Date.now() } : {};
			rememberLivePanel(id, { test, ...fresh });
			if (L.panel?.id !== id) return;
			L.panel = { ...L.panel, test, error: undefined, stale: false, ...fresh };
		} catch (err) {
			if (L.panel?.id !== id) return;
			L.panel = { ...L.panel, stale: false, error: refresh ? undefined : err.message };
		}
		if (S.view === "live") renderLiveView();
	}

	/**
	 * Los fallos nuevos: para cada test que falló, su historial dice cómo terminó en su ejecución anterior
	 * del mismo proyecto. "new" si pasó (una regresión de hoy), "old" si ya fallaba. Una sola vez por
	 * test y como mucho 12 pedidos por refresco.
	 */
	async function loadLiveRegressions() {
		const L = S.liveView;
		if (!L.exec || L.regsBusy) return;
		L.regs = L.regs || {};
		L.regsAsked = L.regsAsked || new Set();
		// de a 40, de a 8 pedidos a la vez, y seguido hasta terminar: con 128 fallos, de a 12 por refresco la
		// mayoría se quedaba sin marca
		const failed = window.TraceReportsLive.allTests(L.exec).filter((x) => (x.state === "FAIL" || x.state === "INTERRUPTED") && !L.regsAsked.has(x.id)).slice(0, 40);
		if (!failed.length) return;
		L.regsBusy = true;
		for (const x of failed) L.regsAsked.add(x.id);
		let changed = false;
		try {
			for (let i = 0; i < failed.length; i += 8) {
				const got = await Promise.all(failed.slice(i, i + 8).map((x) => api(`/api/v1/tests/${x.id}/history?limit=6`).then((hist) => [x, hist]).catch(() => [x, null])));
				for (const [x, hist] of got) {
					if (!Array.isArray(hist) || L.regs[x.id]) continue;
					const prev = hist.filter((e) => e.test_id !== x.id && e.run_id < x.run_id).sort((a, b) => b.started_at - a.started_at)[0];
					// sin una ejecución anterior de este test no hay con qué comparar: "primera vez", no un hueco
					const kind = !prev ? "first" : prev.status === "PASS" || prev.status === "WARNING" ? "new" : prev.status === "FAIL" || prev.status === "INTERRUPTED" ? "old" : "first";
					L.regs[x.id] = kind;
					changed = true;
				}
			}
		} finally { L.regsBusy = false; }
		if (changed && S.view === "live") renderLiveView();
		// quedan fallos sin revisar: sigue enseguida, sin esperar al próximo evento
		if (window.TraceReportsLive.allTests(L.exec).some((x) => (x.state === "FAIL" || x.state === "INTERRUPTED") && !L.regsAsked.has(x.id))) setTimeout(loadLiveRegressions, 150);
	}

	/** El backend de la ejecución (endpoints de todas sus corridas), para la tarjeta de En vivo. */
	async function loadLiveEndpoints(key) {
		const L = S.liveView;
		L.epKey = key; L.epAt = Date.now();
		try {
			const r = await api(`/api/v1/live/endpoints?key=${encodeURIComponent(key)}`);
			if (L.key !== key) return;
			L.endpoints = r.endpoints || [];
		} catch { if (L.key === key) L.endpoints = []; }
		if (S.view === "live") renderLiveView();
	}

	function bindLiveView() {
		const el = $("#view-live");
		if (!el) return;
		const L = S.liveView, ui = () => L.ui;
		// cada cambio de filtro queda en la URL (&f=, &lq=) para compartir el enlace tal cual se ve
		const redraw = () => { renderLiveView(); if (S.view === "live") writeHash(); };
		const pick = (key) => {
			// otra ejecución: sin el panel ni los filtros de la anterior (estaban dentro del comentario y no corrían).
			// Los fallos nuevos (L.regs) son por id de test, que es único: se conservan
			const filtered = L.history?.filtered;
			L.key = key; L.exec = null; L.endpoints = undefined;
			L.ui = { ...liveUI(), group: L.ui.group }; L.panel = null; L.anim = null;
			renderLiveView(); loadLiveView(true);
			if (filtered) loadLiveHistory(); // la búsqueda se borró con los filtros: el historial, sin filtrar
			el.querySelector("[data-live-xpick]")?.focus();
		};
		const openPicker = (open) => {
			ui().pickerOpen = open; if (!open) ui().pickerQ = "";
			redraw();
			if (!open && L.history?.filtered) loadLiveHistory(); // lo escrito se borró: la lista vuelve a ser la de siempre
			if (open) (el.querySelector("[data-live-xq]") || el.querySelector(".xp-opt.on") || el.querySelector(".xp-opt"))?.focus();
			else el.querySelector("[data-live-xpick]")?.focus();
		};
		// clic fuera del selector lo cierra
		document.addEventListener("pointerdown", (e) => { if (S.view === "live" && ui().pickerOpen && !e.target.closest(".live-xpick")) openPicker(false); });
		const FACET = { state: "states", file: "files", worker: "workers", error: "errors" };
		let qTimer = 0;
		/**
		 * Un clic en Mostrar o Agrupar por responde en el mismo cuadro: el control cambia y su indicador se
		 * desliza, y la tabla, el mapa y la línea de tiempo se atenúan con una barra de trabajo; recién en el
		 * cuadro siguiente se recalcula y redibuja (nunca con la UI congelada sin señal). Los tiempos quedan
		 * en window.__trLiveTiming: feedback (clic → primer pintado) y list (clic → lista redibujada).
		 */
		const instant = (btn, mutate) => {
			const t0 = performance.now();
			S.liveView.ui.rowLimit = 0; // otra vista de la tabla: vuelve a las primeras 100 filas
			const segEl = btn.closest(".seg");
			if (segEl) {
				for (const o of segEl.querySelectorAll(".seg-o")) { const on = o === btn; o.classList.toggle("on", on); o.setAttribute("aria-checked", String(on)); }
				placeThumb(segEl, true);
			}
			el.classList.add("live-busy");
			requestAnimationFrame(() => {
				const feedback = performance.now() - t0;
				setTimeout(() => {
					mutate();
					redraw();
					el.classList.remove("live-busy");
					requestAnimationFrame(() => { window.__trLiveTiming = { feedback, list: performance.now() - t0 }; });
				}, 0);
			});
		};
		el.addEventListener("click", (e) => {
			const d = (sel) => e.target.closest(sel);
			let b;
			if ((b = d("[data-live-copy]"))) {
				e.preventDefault(); e.stopPropagation();
				const name = b.dataset.liveCopy;
				const done = () => { b.classList.add("done"); b.dataset.tip = tr("Copiado"); setTimeout(() => { b.classList.remove("done"); b.dataset.tip = tr("Copiar el nombre completo"); }, 1400); };
				navigator.clipboard?.writeText(name).then(done, () => {});
				return;
			}
			if (d("[data-live-resize]")) return; // el borde de la columna no ordena
			if (d("[data-live-wrap]")) { ui().wrap = !ui().wrap; saveLivePrefs(ui()); return redraw(); }
			if (d("[data-live-hideidle]")) { ui().hideIdle = !ui().hideIdle; return redraw(); }
			if (d("[data-live-rowsmore]")) { ui().rowLimit = (ui().rowLimit || 100) + 100; return redraw(); }
			if ((b = d("[data-live-attnstate]"))) { const k = b.dataset.liveAttnstate, st = ui().attnStates || []; ui().attnStates = st.includes(k) ? st.filter((x) => x !== k) : [...st, k]; return redraw(); }
			if ((b = d("[data-live-errall]"))) { const k = b.dataset.liveErrall; ui().errAll = { ...(ui().errAll || {}), [k]: !(ui().errAll || {})[k] }; return redraw(); }
			if ((b = d("[data-live-errgroup]"))) { const k = b.dataset.liveErrgroup; ui().errOpen = { ...(ui().errOpen || {}), [k]: !(ui().errOpen || {})[k] }; return redraw(); }
			if (d("[data-live-compact]")) { ui().compact = !ui().compact; saveLivePrefs(ui()); return redraw(); }
			if (d("[data-live-help]")) { ui().help = !ui().help; return redraw(); }
			if (d("[data-live-helpclose]")) { ui().help = false; return redraw(); }
			if ((b = d("[data-live-fieldview]"))) { const v = b.dataset.liveFieldview; return instant(b, () => { ui().fieldView = v; }); }
			if ((b = d("[data-live-scroll]"))) { el.querySelector(b.dataset.liveScroll)?.scrollIntoView({ block: "center", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" }); return; }
			if ((b = d("[data-live-state]"))) {
				const k = b.dataset.liveState, st = ui().states;
				b.classList.toggle("on"); b.setAttribute("aria-pressed", String(b.classList.contains("on"))); // el tile responde ya
				return instant(b, () => { ui().states = st.includes(k) ? st.filter((x) => x !== k) : [...st, k]; });
			}
			if (d("[data-live-xpick]")) return openPicker(!ui().pickerOpen);
			if ((b = d(".xp-opt[data-live-exec]"))) { ui().pickerOpen = false; return pick(b.dataset.liveExec); }
			if ((b = d("[data-live-hist]"))) return pick(b.dataset.liveHist); // abrir la anterior elegida
			if ((b = d("[data-live-histsel]"))) return selectHist(b.dataset.liveHistsel); // elegirla: su vista previa
			if (d("[data-live-histmore]")) return loadLiveHistory(true); // la página siguiente, más antigua
			if ((b = d("[data-live-histrange]"))) { ui().histRange = b.dataset.liveHistrange; redraw(); return loadLiveHistory(); }
			if ((b = d("[data-live-charttable]"))) { ui().tables = { ...ui().tables, [b.dataset.liveCharttable]: !ui().tables[b.dataset.liveCharttable] }; return redraw(); }
			if (d("[data-live-clear]")) { Object.values(FACET).forEach((k) => { ui()[k] = []; }); return redraw(); }
			if ((b = d("[data-live-facet]"))) {
				const k = FACET[b.dataset.liveFacet], v = b.dataset.v, list = ui()[k];
				ui()[k] = list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
				ui().open = {};
				return redraw();
			}
			if ((b = d("[data-live-more]"))) { ui().more = { ...ui().more, [b.dataset.liveMore]: !ui().more[b.dataset.liveMore] }; return redraw(); }
			if ((b = d("[data-live-sort]"))) {
				const col = b.dataset.liveSort, cur = ui().sort;
				// primer clic: lo mayor arriba (duración, vs p50) o A-Z; segundo, al revés; tercero, vuelve a agrupar
				const first = col === "name" || col === "state" ? 1 : -1;
				ui().sort = !col ? null : cur?.col !== col ? { col, dir: first } : cur.dir === first ? { col, dir: -first } : null;
				return redraw();
			}
			if (d("[data-live-pause]")) {
				toggleLivePause();
				return redraw();
			}
			if ((b = d("[data-live-ptab]"))) { L.panel = { ...L.panel, tab: b.dataset.livePtab }; return redraw(); }
			if (d("[data-live-trace-all]")) { L.panel = { ...L.panel, all: !L.panel.all }; return redraw(); }
			if ((b = d("[data-live-problems]"))) {
				// en el control: elegir "Con problemas" (si ya está, no cambia); el enlace "mostrar todos" lo apaga
				const g = window.TraceReportsLive.ganttLayout(L.exec, L.now, null);
				const on = b.classList.contains("seg-o") ? true : !window.TraceReportsLive.problemsOn(L.exec, ui(), g.lanes.length);
				return instant(b, () => { ui().problems = on; ui().states = on ? [] : ui().states; ui().open = {}; });
			}
			if ((b = d("[data-live-groupby]"))) { const k = b.dataset.liveGroupby; return instant(b, () => { ui().group = k; ui().open = {}; }); }
			if ((b = d("[data-live-group]"))) {
				const k = b.dataset.liveGroup;
				ui().open = { ...ui().open, [k]: b.getAttribute("aria-expanded") !== "true" };
				return redraw();
			}
			if (d("[data-live-showok]")) { ui().showOk = !ui().showOk; return redraw(); }
			if (d("[data-live-alllanes]")) { ui().allLanes = !ui().allLanes; return redraw(); }
			if ((b = d("[data-live-zoom]"))) { const z = b.dataset.liveZoom; return instant(b, () => { ui().zoom = window.TraceReportsLive.zoomStep(ui().zoom, z); }); }
			if (d("[data-live-close]")) { L.panel = null; return redraw(); }
			if (d("[data-live-escunpick]")) { L.ui.escPick = null; return redraw(); } // Escalar vuelve a todos los fallos
			if ((b = d("[data-live-all]"))) return instant(b, () => { ui().states = []; ui().problems = false; ui().open = {}; });
			if ((b = d("[data-live-stack]"))) { const id = b.dataset.liveStack; ui().stacks = { ...ui().stacks, [id]: !ui().stacks?.[id] }; return redraw(); }
			if ((b = d("[data-live-openall]"))) { const on = b.dataset.liveOpenall === "1"; ui().openAll = on; ui().open = {}; ui().showOk = on; return redraw(); }
			if (d("[data-live-attnmore]")) {
				ui().attnAll = !ui().attnAll;
				redraw();
				// al cerrarla, la tarjeta queda a la vista (la página no se queda abajo, donde estaba el final de la lista)
				if (!ui().attnAll) el.querySelector(".live-attn")?.scrollIntoView({ block: "nearest", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
				return;
			}
			// una barra del Gantt: abre el detalle y lleva a su fila en la tabla (abriendo su grupo), resaltada
			const bar = d(".lg-bar[data-live-test]");
			if (bar) {
				const id = Number(bar.dataset.liveTest);
				const x = window.TraceReportsLive.allTests(L.exec).find((y) => y.id === id);
				if (x) { const k = ui().group === "worker" ? x.worker || "—" : ui().group === "state" ? x.state : x.file; ui().open = { ...ui().open, [k]: true }; }
				loadLivePanel(id);
				const tr = el.querySelector(`tr[data-live-test="${id}"]`);
				if (tr) {
					tr.scrollIntoView({ block: "center", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
					tr.classList.remove("lt-flash"); void tr.offsetWidth; tr.classList.add("lt-flash");
				}
				return;
			}
			// una fila abre el panel lateral (el nombre del test sigue llevando al test)
			const row = d("[data-live-test]");
			if (row && !d("a")) loadLivePanel(Number(row.dataset.liveTest));
		});
		// doble clic en una fila, un ítem de "Requiere atención" o un test de un error: abre el test
		el.addEventListener("dblclick", (e) => {
			const r = e.target.closest("[data-live-test][data-live-run]");
			if (!r || e.target.closest(".lf-canvas, a")) return;
			S.leavingLive = Date.now(); L.panel = null;
			location.hash = `#run=${r.dataset.liveRun}&view=tests&test=${r.dataset.liveTest}`;
		});
		// pasar el mouse (o el foco) por un carril de la línea de tiempo resalta sus tests en la tabla
		const laneHL = (key) => { for (const n of el.querySelectorAll(".lane-hl")) n.classList.remove("lane-hl"); if (key) for (const n of el.querySelectorAll(`tr[data-lane="${CSS.escape(key)}"], .lg-lane[data-lane="${CSS.escape(key)}"]`)) n.classList.add("lane-hl"); };
		el.addEventListener("pointerover", (e) => { const l = e.target.closest?.("[data-live-lane]"); if (l) laneHL(l.dataset.liveLane); });
		el.addEventListener("pointerout", (e) => { if (e.target.closest?.("[data-live-lane]") && !e.relatedTarget?.closest?.("[data-live-lane]")) laneHL(""); });
		el.addEventListener("focusin", (e) => { const l = e.target.closest?.("[data-live-lane]"); laneHL(l ? l.dataset.liveLane : ""); });
		// J/K: siguiente o anterior que requiere atención (abre su detalle); O abre el test; P pausa; C compacto; ? ayuda
		document.addEventListener("keydown", (e) => {
			if (S.view !== "live" || e.ctrlKey || e.metaKey || e.altKey || e.target.closest?.("input, textarea, select, [contenteditable], .lf-canvas")) return;
			const k = e.key;
			if (k === "?") { e.preventDefault(); ui().help = !ui().help; return redraw(); }
			if (k === "Escape" && ui().help) { ui().help = false; return redraw(); }
			if (k === "p" || k === "P") { e.preventDefault(); return toggleLivePause(); }
			if (k === "c" || k === "C") { e.preventDefault(); ui().compact = !ui().compact; saveLivePrefs(ui()); return redraw(); }
			if ((k === "o" || k === "O") && L.panel) {
				e.preventDefault();
				const x = window.TraceReportsLive.allTests(L.exec).find((y) => y.id === L.panel.id);
				if (x) { S.leavingLive = Date.now(); L.panel = null; location.hash = `#run=${x.run_id}&view=tests&test=${x.id}`; }
				return;
			}
			if (k === "j" || k === "J" || k === "k" || k === "K") {
				const list = window.TraceReportsLive.attention(window.TraceReportsLive.allTests(L.exec || { runs: [] }));
				if (!list.length) return;
				e.preventDefault();
				const i = list.findIndex((x) => x.id === L.panel?.id);
				const next = k.toLowerCase() === "j" ? (i + 1) % list.length : (i <= 0 ? list.length - 1 : i - 1);
				loadLivePanel(list[next].id);
				requestAnimationFrame(() => el.querySelector(`.la-list [data-attn="${list[next].id}"]`)?.scrollIntoView({ block: "nearest" }));
			}
		});
		// "/" lleva al buscador desde cualquier parte de la vista (salvo que se esté escribiendo en otro campo)
		document.addEventListener("keydown", (e) => {
			if (e.key !== "/" || S.view !== "live" || e.ctrlKey || e.metaKey || e.altKey || e.target.closest?.("input, textarea, select, [contenteditable]")) return;
			const q = el.querySelector("[data-live-q]") || el.querySelector("[data-live-hq]");
			if (q) { e.preventDefault(); q.focus(); q.select(); }
		});
		el.addEventListener("change", (e) => { const sel = e.target.closest("[data-live-exec-select]"); if (sel) pick(sel.value); });
		// el historial (pantalla vacía): doble clic abre; ↑/↓, Inicio/Fin recorren con su vista previa; Enter abre
		el.addEventListener("dblclick", (e) => { const r = e.target.closest("[data-live-histsel]"); if (r) pick(r.dataset.liveHistsel); });
		el.addEventListener("keydown", (e) => {
			const row = e.target.closest?.("[data-live-histsel]");
			if (!row) return;
			const rows = [...el.querySelectorAll("[data-live-histsel]")], i = rows.indexOf(row);
			const to = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: rows.length - 1 }[e.key];
			if (e.key === "Enter" || e.key === " ") { e.preventDefault(); return pick(row.dataset.liveHistsel); }
			if (to === undefined) return;
			e.preventDefault();
			const next = rows[Math.max(0, Math.min(rows.length - 1, to))];
			if (next) selectHist(next.dataset.liveHistsel, true);
		});
		/** Elige una anterior de la lista: se marca al instante y su detalle (lo que falló) llega por detrás. */
		function selectHist(key, focus = false) {
			ui().histSel = key;
			redraw();
			const r = [...el.querySelectorAll("[data-live-histsel]")].find((x) => x.dataset.liveHistsel === key);
			if (r) { if (focus) r.focus(); r.scrollIntoView({ block: "nearest" }); }
			loadHistDetailSoon(key);
		}
		el.addEventListener("input", (e) => {
			// buscar: se recalcula al dejar de escribir (140 ms), no con cada tecla; con 2.000 tests cada redibujo
			// de la vista entera trababa el campo
			if (e.target.matches("[data-live-q]")) { ui().q = e.target.value; ui().open = {}; ui().rowLimit = 0; clearTimeout(qTimer); qTimer = setTimeout(redraw, 140); }
			if (e.target.matches("[data-live-xq]")) { ui().pickerQ = e.target.value; redraw(); liveHistorySoon(); } // las de ahora al instante; las anteriores, del servidor
			if (e.target.matches("[data-live-aq]")) { ui().attnQ = e.target.value; clearTimeout(qTimer); qTimer = setTimeout(redraw, 140); }
			if (e.target.matches("[data-live-hq]")) { ui().histQ = e.target.value; liveHistorySoon(); }
		});
		// el selector con teclado: flechas entre opciones (desde el buscador también), Enter elige, Escape cierra
		el.addEventListener("keydown", (e) => {
			if (!ui().pickerOpen || !e.target.closest(".live-xpick")) return;
			const opts = [...el.querySelectorAll(".xp-opt")], i = opts.indexOf(e.target.closest(".xp-opt"));
			if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); return openPicker(false); }
			if (e.key === "ArrowDown" || e.key === "ArrowUp") {
				e.preventDefault();
				const next = e.key === "ArrowDown" ? Math.min(opts.length - 1, i + 1) : i - 1;
				(next < 0 ? el.querySelector("[data-live-xq]") || opts[0] : opts[next])?.focus();
			}
			if (e.key === "Enter" && i >= 0) { e.preventDefault(); ui().pickerOpen = false; pick(opts[i].dataset.liveExec); }
		}, true);
		// Escape cierra el panel aunque el foco haya quedado fuera (un refresco redibuja la vista)
		document.addEventListener("keydown", (e) => {
			if (e.key !== "Escape" || S.view !== "live" || !L.panel || e.target.closest?.(".live-cancel, .live-xpick, .modal, dialog")) return;
			const id = L.panel.id;
			L.panel = null; redraw();
			el.querySelector(`tr[data-live-test="${id}"]`)?.focus();
		});
		el.addEventListener("keydown", (e) => {
			// Esc en el buscador: primero lo limpia; vacío, sale de él (y no cierra el panel)
			if (e.key === "Escape" && e.target.matches("[data-live-q]")) {
				e.preventDefault(); e.stopPropagation();
				if (e.target.value || ui().q) { e.target.value = ""; ui().q = ""; ui().open = {}; redraw(); } else e.target.blur();
				return;
			}
			const row = e.target.closest("tr[data-live-test]");
			if (row && e.key === "Enter") loadLivePanel(Number(row.dataset.liveTest));
		});
		// minimapa: arrastrar marca el tramo a acercar; un clic centra la ventana actual ahí
		let drag = null;
		const frac = (mini, x) => { const r = mini.getBoundingClientRect(); return Math.min(1, Math.max(0, (x - r.left) / r.width)); };
		el.addEventListener("pointerdown", (e) => {
			const mini = e.target.closest("[data-live-minimap]");
			if (!mini || e.button !== 0 || e.target.closest("[data-live-test]")) return; // una anomalía abre su test
			e.preventDefault();
			drag = { mini, a: frac(mini, e.clientX), box: document.createElement("div") };
			drag.box.className = "lg-brush drawing";
			mini.appendChild(drag.box);
			mini.setPointerCapture?.(e.pointerId);
		});
		el.addEventListener("pointermove", (e) => {
			if (!drag) return;
			const b = frac(drag.mini, e.clientX), lo = Math.min(drag.a, b), hi = Math.max(drag.a, b);
			Object.assign(drag.box.style, { left: lo * 100 + "%", width: (hi - lo) * 100 + "%" });
		});
		el.addEventListener("pointerup", (e) => {
			if (!drag) return;
			const b = frac(drag.mini, e.clientX), lo = Math.min(drag.a, b), hi = Math.max(drag.a, b);
			drag.box.remove();
			drag = null;
			if (hi - lo >= 0.02) ui().zoom = { a: lo, b: hi };
			else if (ui().zoom) { const w = ui().zoom.b - ui().zoom.a, a = Math.min(1 - w, Math.max(0, lo - w / 2)); ui().zoom = { a, b: a + w }; }
			redraw();
		});
		// la columna Test se ensancha arrastrando su borde (o con ← → sobre él); doble clic, al ancho de antes
		const setNameW = (w) => {
			ui().nameW = Math.round(Math.min(900, Math.max(160, w)));
			for (const c of el.querySelectorAll("col.c-name")) c.style.width = ui().nameW + "px";
		};
		let resizing = null;
		el.addEventListener("pointerdown", (e) => {
			const hnd = e.target.closest("[data-live-resize]");
			if (!hnd || e.button !== 0) return;
			e.preventDefault(); e.stopPropagation();
			const th = hnd.closest("th");
			resizing = { x: e.clientX, w: th.getBoundingClientRect().width, hnd };
			hnd.classList.add("drag");
			hnd.setPointerCapture?.(e.pointerId);
		}, true);
		el.addEventListener("pointermove", (e) => { if (resizing) setNameW(resizing.w + e.clientX - resizing.x); });
		el.addEventListener("pointerup", () => {
			if (!resizing) return;
			resizing.hnd.classList.remove("drag"); resizing = null;
			saveLivePrefs(ui()); redraw();
		});
		el.addEventListener("dblclick", (e) => { if (e.target.closest("[data-live-resize]")) { ui().nameW = 0; saveLivePrefs(ui()); redraw(); } });
		el.addEventListener("keydown", (e) => {
			const hnd = e.target.closest?.("[data-live-resize]");
			if (!hnd || (e.key !== "ArrowLeft" && e.key !== "ArrowRight")) return;
			e.preventDefault();
			setNameW((ui().nameW || hnd.closest("th").getBoundingClientRect().width) + (e.key === "ArrowRight" ? 24 : -24));
			saveLivePrefs(ui());
		});
		// Ctrl + rueda sobre la línea de tiempo: acerca o aleja alrededor del puntero (la rueda sola sigue
		// desplazando la página)
		el.addEventListener("wheel", (e) => {
			const area = e.target.closest("[data-live-lanes], [data-live-minimap]");
			if (!area || !(e.ctrlKey || e.metaKey)) return;
			e.preventDefault();
			const track = area.matches("[data-live-minimap]") ? area : area.querySelector(".lg-track") || area;
			const r = track.getBoundingClientRect(), f = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
			const z = ui().zoom || { a: 0, b: 1 }, minimapArea = area.matches("[data-live-minimap]");
			const at = minimapArea ? f : z.a + f * (z.b - z.a); // en el minimapa la fracción ya es del total
			const w = Math.min(1, Math.max(0.02, (z.b - z.a) * (e.deltaY < 0 ? 0.7 : 1 / 0.7)));
			if (w >= 1) { ui().zoom = null; return redraw(); }
			const a = Math.min(1 - w, Math.max(0, at - (minimapArea ? w / 2 : f * w)));
			ui().zoom = { a, b: a + w };
			redraw();
		}, { passive: false });
		el.addEventListener("submit", async (e) => {
			const form = e.target.closest("[data-live-cancel]");
			if (!form) return;
			e.preventDefault();
			const btn = form.querySelector("button");
			btn.disabled = true;
			try {
				await apiSend("POST", "/api/v1/ui/live/cancel", { key: S.liveView.key, reason: form.reason.value });
				loadLiveView(true); // la cabecera pasa a "Cancelación pedida" (también en pausa)
			} catch (err) {
				btn.disabled = false;
				let msg = form.querySelector(".live-cancel-err");
				if (!msg) { msg = document.createElement("p"); msg.className = "live-cancel-err"; msg.setAttribute("role", "alert"); form.appendChild(msg); }
				msg.textContent = err.message;
			}
		});
	}

	let liveViewTimer = null;
	const scheduleLiveView = () => { if (S.view === "live" && !liveViewTimer) liveViewTimer = setTimeout(() => { liveViewTimer = null; loadLiveView(); }, 1500); };

	function renderLiveView() {
		const el = $("#view-live"), L = S.liveView;
		if (!el) return;
		if (!L.loaded) { setHTML(el, `<div class="card placeholder">${L.error ? esc(L.error) : tr("Cargando…")}</div>`); return; }
		const t = window.TraceReportsI18n.t;
		// el refresco (cada evento) redibuja todo: el buscador no pierde el foco ni el cursor, ni el
		// formulario de cancelar abierto lo que se estaba escribiendo
		// el buscador en uso no se recrea: se conserva el mismo <input> (valor, foco y cursor), así una
		// tecla que llega justo con un refresco no se pierde
		const act = document.activeElement, field = ["[data-live-q]", "[data-live-xq]", "[data-live-aq]", "[data-live-hq]"].find((f) => act?.matches?.(f)) || "";
		const typing = field ? act : null;
		const optFocus = act?.closest?.(".xp-opt")?.dataset.liveExec, listTop = el.querySelector(".xp-list")?.scrollTop || 0;
		const histTop = el.querySelector("[data-live-histpane]")?.scrollTop || 0, histFocus = act?.closest?.("[data-live-histsel]")?.dataset.liveHistsel;
		const reason = el.querySelector(".live-cancel[open] input[name=reason]");
		const keep = reason ? { value: reason.value, focus: document.activeElement === reason } : null;
		if (el.querySelector("[data-live-minimap] .lg-brush.drawing, [data-live-resize].drag")) return; // arrastrando: al soltar se dibuja
		const flip = S.liveView.anim && !S.liveView.anim.first ? flipSnapshot(el) : null;
		const thumbs = thumbSnapshot(el);
		const replaced = morphHTML(el, window.TraceReportsLive.render({ list: L.list, history: L.history?.items, historyDetail: L.histDetail?.get(L.ui.histSel || L.history?.items?.[0]?.key), historyMeta: L.history ? { total: L.history.total, next: L.history.next, loading: L.history.loading } : null, exec: L.exec, key: L.key, now: L.now, no_signal_ms: L.no_signal_ms, ui: L.ui, panel: L.panel, endpoints: L.endpoints, ingest: liveIngest(), pausedNew: livePausedNew(), regressions: L.regs || null }, {
			t, esc, icon, canAct,
			clock: (ms, secs) => liveFmt(secs ? "hms" : "hm").format(ms),
			// la fecha solo si no es de hoy
			day: (ms) => (new Date(ms).toDateString() === new Date(L.now || Date.now()).toDateString() ? "" : liveFmt("day").format(ms)),
		}));
		if (typing) {
			const fresh = el.querySelector(field);
			if (fresh && fresh !== typing) { fresh.replaceWith(typing); typing.focus(); }
		}
		const list = el.querySelector(".xp-list");
		if (list) list.scrollTop = listTop;
		afterHistRender(el, histTop, histFocus);
		if (optFocus) [...el.querySelectorAll(".xp-opt")].find((o) => o.dataset.liveExec === optFocus)?.focus();
		// solo si el DOM cambió: con el mismo HTML los nodos siguen ahí (y quizás animándose)
		el.classList.toggle("live-compact", !!L.ui.compact);
		syncLiveField(el);
		if (L.exec) {
			const name = $("#report-name");
			if (name && name.textContent !== L.exec.title) name.textContent = L.exec.title;
			const env = $("#report-env");
			if (env) env.textContent = [L.exec.environment, L.exec.running ? tr("en curso") : tr("terminada")].filter(Boolean).join(" · ");
		}
		if (replaced) {
			tape.dirty = true; // la cinta se vuelve a medir una vez, en el próximo cuadro
			const tr0 = el.querySelector("[data-live-tape-track]");
			if (tr0 && S.tapeX) tr0.style.transform = `translateX(${-S.tapeX}px)`; // la cinta sigue donde iba
			fitBars(el);
			watchGantt(el);
			for (const s of el.querySelectorAll(".seg")) placeThumb(s, false, thumbs.get(s.dataset.seg));
			animateLive(el);
			if (flip) flipPlay(el, flip);
		}
		if (keep) {
			const d = el.querySelector(".live-cancel");
			if (d) { d.open = true; const r = d.querySelector("input[name=reason]"); r.value = keep.value; if (keep.focus) r.focus(); }
		}
	}

	/**
	 * Animaciones de En vivo: la vista se redibuja con cada evento, así que solo se anima lo que cambió
	 * (un número, el estado de un test, un evento nuevo, una anomalía nueva) o lo que aparece (la vista
	 * la primera vez, el panel al abrirse, la traza al mostrarse). Con "reducir movimiento", nada.
	 */
	const STATE_CLS = ["pass", "fail", "warning", "skip", "interrupted", "running", "turtle", "nosig"];
	function animateLive(el) {
		const L = S.liveView;
		if (!L.exec) return;
		const A = L.anim || (L.anim = { first: true, states: new Map(), counts: {}, bumps: {}, evs: null, anoms: null, panelKey: "", traceKey: "" });
		const motion = !matchMedia("(prefers-reduced-motion: reduce)").matches;
		const flag = (n, cls) => { if (!motion) return; n.classList.remove(cls); void n.offsetWidth; n.classList.add(cls); };
		if (A.first) {
			A.first = false;
			if (motion) { el.classList.add("live-enter"); setTimeout(() => el.classList.remove("live-enter"), 1400); }
		}
		// cambio de estado: pulso del color nuevo; lo que no estaba (un test nuevo, o filas que entran al
		// cambiar el filtro), entra deslizándose; como mucho 40 por redibujo, para no marear
		let entered = 0;
		const firstSeen = !A.seen;
		A.seen = A.seen || new Set();
		for (const n of el.querySelectorAll("tr[data-live-test], .lg-bar[data-live-test], .hm-c[data-live-test]")) {
			const key = `${n.tagName}${n.className.split(" ")[0]}:${n.dataset.liveTest}`, st = STATE_CLS.find((c) => n.classList.contains(c)) || "";
			const prev = A.states.get(key);
			if (prev !== undefined && prev !== st) flag(n, "live-changed");
			else if (!firstSeen && motion && !A.seen.has(key) && (n.tagName === "TR" || n.classList.contains("hm-c")) && entered++ < 40) {
				n.classList.add("lt-new");
				n.style.animationDelay = `${Math.min(entered, 20) * 12}ms`;
				setTimeout(() => { n.classList.remove("lt-new"); n.style.animationDelay = ""; }, 600);
			}
			A.states.set(key, st);
			A.seen.add(key);
		}
		for (const n of el.querySelectorAll("[data-count]")) {
			const k = n.dataset.count, to = Number(n.dataset.n), from = A.counts[k]; // el valor, no el texto (que cuenta)
			if (from !== undefined && from !== to) { flag(n.closest(".live-tile") || n, "bump"); if (motion) countUp(n, from, to); }
			A.counts[k] = to;
		}
		for (const n of el.querySelectorAll("[data-bump]")) {
			const k = n.dataset.bump, v = n.textContent;
			if (A.bumps[k] !== undefined && A.bumps[k] !== v) flag(n, "bump");
			A.bumps[k] = v;
		}
		const evs = new Set([...el.querySelectorAll("li[data-ev]")].map((n) => n.dataset.ev));
		if (A.evs) for (const n of el.querySelectorAll("li[data-ev]")) if (!A.evs.has(n.dataset.ev)) flag(n, "lc-new");
		A.evs = evs;
		// algo nuevo que requiere atención entra deslizándose y destella
		const attn = new Set([...el.querySelectorAll(".la-list li[data-attn]")].map((n) => n.dataset.attn));
		if (A.attn) for (const n of el.querySelectorAll(".la-list li[data-attn]")) if (!A.attn.has(n.dataset.attn)) flag(n, "la-new");
		A.attn = attn;
		const anoms = new Set([...el.querySelectorAll(".lg-anom")].map((n) => n.dataset.liveTest));
		if (A.anoms) for (const n of el.querySelectorAll(".lg-anom")) if (!A.anoms.has(n.dataset.liveTest)) flag(n, "lg-anom-new");
		A.anoms = anoms;
		const panel = el.querySelector(".live-panel");
		const opened = panel && !A.panelKey; // el panel recién se abre (no: se cambió de test con el panel abierto)
		if (opened) flag(panel, "live-panel-in");
		A.panelKey = panel ? String(L.panel?.id) : "";
		// la traza entra fila por fila solo la primera vez que el panel se abre; al cambiar de test se
		// reemplaza de una vez (repetir el fundido con cada clic se veía como un parpadeo)
		const trace = el.querySelector(".lt-trace");
		if (trace && opened) flag(trace, "lt-trace-in");
		A.traceKey = trace ? String(L.panel?.id) : "";
	}
	// Reordenar sin saltos (la técnica FLIP, como el `layout` de Framer Motion): se anotan dónde estaban
	// las filas antes de redibujar y, si un test cambió de lugar, se lo desliza desde ahí con un resorte.
	const FLIP_SEL = "tr.lt-row[data-live-test], .la-list li[data-attn]";
	const FLIP_MAX = 400;
	function flipSnapshot(el) {
		if (matchMedia("(prefers-reduced-motion: reduce)").matches) return null;
		const nodes = el.querySelectorAll(FLIP_SEL);
		if (!nodes.length || nodes.length > FLIP_MAX) return null;
		const pos = new Map();
		for (const n of nodes) pos.set(n.dataset.liveTest ? "r" + n.dataset.liveTest : "a" + n.dataset.attn, n.getBoundingClientRect().top);
		return pos;
	}
	function flipPlay(el, pos) {
		for (const n of el.querySelectorAll(FLIP_SEL)) {
			const prev = pos.get(n.dataset.liveTest ? "r" + n.dataset.liveTest : "a" + n.dataset.attn);
			if (prev === undefined) continue;
			const dy = prev - n.getBoundingClientRect().top;
			if (Math.abs(dy) < 2 || Math.abs(dy) > 1200) continue;
			n.animate([{ transform: `translateY(${dy}px)` }, { transform: "none" }], { duration: 520, easing: "cubic-bezier(.22, 1.25, .36, 1)" });
		}
	}
	/** El nombre dentro de una barra del Gantt solo si cabe; si no, la barra queda limpia (el tooltip lo dice). */
	function fitBars(el) {
		// solo las barras con algo adentro (nombre o el ícono de su estado; las angostas se dibujan vacías): medir las
		// 1.200 de una ejecución larga costaba
		const bars = [...el.querySelectorAll(".lg-track .lg-bar")].filter((b) => b.firstElementChild);
		for (const b of bars) b.classList.remove("notext"); // medir con el texto puesto (el ancho pudo cambiar)
		const over = bars.map((b) => b.scrollWidth > b.clientWidth + 1); // todas las lecturas juntas: un solo cálculo de layout
		bars.forEach((b, i) => { if (over[i]) b.classList.add("notext"); });
	}
	// el ancho del Gantt también cambia sin que cambie la ventana (aparece la barra de scroll al cargar algo de
	// arriba, se abre un panel): se vuelve a medir entonces, si no los nombres quedaban cortados
	let ganttRO = null, ganttSeen = null, ganttW = 0, ganttRaf = 0;
	function watchGantt(el) {
		const g = el.querySelector(".live-gantt");
		if (g === ganttSeen || typeof ResizeObserver !== "function") return;
		ganttSeen = g; ganttW = g ? g.clientWidth : 0;
		ganttRO ||= new ResizeObserver((en) => {
			const w = en[en.length - 1].contentRect.width;
			if (Math.abs(w - ganttW) < 1) return;
			ganttW = w;
			cancelAnimationFrame(ganttRaf);
			ganttRaf = requestAnimationFrame(() => { const v = $("#view-live"); if (v && S.view === "live") fitBars(v); });
		});
		ganttRO.disconnect();
		if (g) ganttRO.observe(g);
	}
	let fitTimer = null;
	window.addEventListener("resize", () => {
		clearTimeout(fitTimer);
		fitTimer = setTimeout(() => { const el = $("#view-live"); if (el && S.view === "live") { fitBars(el); for (const s of el.querySelectorAll(".seg")) placeThumb(s, false); } }, 150);
	});

	/**
	 * Pausar la vista: lo que se ve queda quieto para analizar (también el campo), pero la ingesta sigue
	 * en el servidor; se cuenta lo que llega mientras tanto y al reanudar la vista se pone al día.
	 */
	function toggleLivePause() {
		const L = S.liveView;
		L.ui.paused = !L.ui.paused;
		L.ui.pausedAt = L.ui.paused ? Date.now() : 0;
		if (!L.ui.paused) loadLiveView(true);
		renderLiveView(); if (S.view === "live") writeHash();
	}
	/** Cuántos eventos de la ejecución abierta llegaron desde que se pausó la vista. */
	function livePausedNew() {
		const L = S.liveView;
		if (!L.ui?.paused || !L.exec) return 0;
		const runs = new Set((L.exec.runs || []).map((r) => r.id));
		return S.ingest.filter((x) => x.at >= L.ui.pausedAt && runs.has(x.run_id)).length;
	}

	/**
	 * Los formateadores de hora de En vivo, uno por idioma y formato. Crear uno en cada llamada (como hace
	 * toLocaleTimeString con opciones) costaba más que todo el resto de la vista: cada barra lleva su hora.
	 */
	const liveFmts = new Map();
	function liveFmt(kind) {
		const lang = I18N.lang === "en" ? "en-US" : "es-CL", k = `${lang}|${kind}`;
		if (!liveFmts.has(k)) liveFmts.set(k, new Intl.DateTimeFormat(lang, { hms: { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }, hm: { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }, day: { day: "2-digit", month: "short" } }[kind]));
		return liveFmts.get(k);
	}

	/** La ingesta de la ejecución abierta: solo los eventos de sus corridas, en el último minuto. */
	function liveIngest() {
		const L = S.liveView, F = window.TraceReportsLiveField;
		if (!F || !L.exec) return null;
		const runs = new Set((L.exec.runs || []).map((r) => r.id));
		// el texto cambia a lo sumo cada 5 s (el reloj de "hace X" no redibuja la vista a cada evento)
		const now = Math.floor(Date.now() / 5000) * 5000;
		// lo que llegó por el stream desde que la pestaña escucha; el rato de antes (la vista recién abierta) se
		// completa con cuándo empezó y terminó cada test: así el gráfico muestra el minuto entero desde el primer momento
		const since = S.ingestSince || now;
		const before = [];
		if (since > now - 60e3) {
			for (const t of window.TraceReportsLive.allTests(L.exec)) {
				if (t.started_at >= now - 60e3 && t.started_at < since) before.push({ at: t.started_at, kind: "test" });
				if (t.ended_at && t.ended_at >= now - 60e3 && t.ended_at < since) before.push({ at: t.ended_at, kind: t.state === "FAIL" ? "fail" : "test" });
			}
		}
		return F.ingestStats([...before, ...S.ingest.filter((x) => runs.has(x.run_id))], now, { running: !!L.exec.running, connected: S.liveState === "live" });
	}

	/**
	 * El campo de la ejecución: un solo canvas (web/livefield.js) que se vuelve a poner en su lugar
	 * después de cada redibujo, con los datos del momento; así su animación nunca se reinicia.
	 */
	function syncLiveField(el) {
		const L = S.liveView, F = window.TraceReportsLiveField, A = window.TraceReportsLiveAgents, LIVE = window.TraceReportsLive;
		const slot = el.querySelector("[data-live-field]");
		if (!F || !slot || !L.exec) { L.field?.attach(null); L.agents?.attach(null); return; }
		const t = window.TraceReportsI18n.t;
		const onOpen = (id, run) => { S.leavingLive = Date.now(); L.panel = null; location.hash = `#run=${run}&view=tests&test=${id}`; };
		// el mismo lugar, dos motores: el campo (el tiempo en círculo) o los agentes (el chip por dentro)
		const agentsMode = slot.dataset.mode === "agents" && A;
		if (agentsMode) {
			L.field?.attach(null);
			// clic en un agente: la tabla con los tests de ese worker (otro clic en el mismo, todos de nuevo)
			if (!L.agents) L.agents = A.create({ t, onPick: (id) => loadLivePanel(id), onOpen, onHover: prefetchLivePanel, onPause: toggleLivePause,
				onWorker: (key) => { L.ui.workers = L.ui.workers.length === 1 && L.ui.workers[0] === key ? [] : [key]; renderLiveView(); } });
			L.agents.attach(slot);
		} else {
			L.agents?.attach(null);
			if (!L.field) L.field = F.create({ t, onPick: (id) => loadLivePanel(id), onPause: toggleLivePause, onHover: prefetchLivePanel, onOpen });
			L.field.attach(slot);
		}
		const engine = agentsMode ? L.agents : L.field;
		const tests = LIVE.allTests(L.exec), shown = LIVE.filterTests(tests, L.ui, LIVE.problemsOn(L.exec, L.ui, LIVE.lanesOf(L.exec, L.now).length));
		const filtered = (L.ui.states.length || L.ui.files.length || L.ui.workers.length || L.ui.errors.length || (L.ui.q || "").trim()) ? new Set(shown.map((x) => x.id)) : null;
		const clock = (ms) => liveFmt("hms").format(ms);
		const ing = liveIngest();
		engine.update({
			exec: L.exec, now: L.now || Date.now(), live: LIVE, match: filtered, selected: L.panel?.id, paused: !!L.ui.paused, pausedNew: livePausedNew(),
			head: `${L.exec.title} · ${t(LIVE.INGEST[ing?.state || "quiet"].label)} · ${t("{n} eventos/min", { n: ing?.perMin || 0 })}`,
			aria: agentsMode ? t("Agentes de la ejecución: {l} workers y {n} tests. Flechas para recorrer los agentes, Enter para ver sus tests.", { n: tests.length, l: LIVE.lanesOf(L.exec, L.now).length })
				: t("Campo de la ejecución: {n} tests en {l} carriles. Flechas para recorrerlos, Enter para abrir.", { n: tests.length, l: LIVE.lanesOf(L.exec, L.now).length }),
			// la tarjeta del mouse: con el color y el ícono de su estado (livefield.js arma el DOM)
			card: (it) => {
				const st = LIVE.STATES[it.state] || { cls: "running", icon: "i-st-running", label: it.state };
				return { cls: st.cls, icon: st.icon, label: t(st.label), dur: LIVE.fmtDur(it.elapsed),
					factor: it.factor ? `×${LIVE.num1(it.factor)} ${t("de lo normal")}` : "", slow: it.factor >= 2,
					times: `${clock(it.started_at)}${it.ended_at ? ` → ${clock(it.ended_at)}` : ` · ${t("en curso")}`}`,
					where: `${it.file}${it.worker ? ` · ${it.worker}` : ""}`, hint: t("Clic: el detalle · Ctrl + clic: abrir el test") };
			},
			tip: (it) => `${t(LIVE.STATES[it.state]?.label || it.state)} · ${LIVE.fmtDur(it.elapsed)}${it.factor ? ` · ×${LIVE.num1(it.factor)} ${t("de lo normal")}` : ""} · ${clock(it.started_at)}${it.ended_at ? ` → ${clock(it.ended_at)}` : ""}
${it.file}${it.worker ? ` · ${it.worker}` : ""}
${t("Clic: el detalle · Ctrl + clic: abrir el test")}`,
		});
		engine.setMotion(!L.ui.paused);
	}

	/**
	 * El indicador de un control segmentado bajo la opción elegida. Con `from` (dónde estaba antes de
	 * redibujar) o `slide` (un clic en el control que sigue en pantalla) se desliza hasta ella.
	 */
	function placeThumb(seg, slide, from) {
		const th = seg.querySelector(".seg-thumb"), on = seg.querySelector(".seg-o.on");
		seg.classList.toggle("has-on", !!on);
		if (!th || !on) return;
		const prev = slide ? { left: th.offsetLeft, width: th.offsetWidth } : from;
		th.style.left = on.offsetLeft + "px"; th.style.width = on.offsetWidth + "px";
		if (!prev || !prev.width || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
		const dx = prev.left - on.offsetLeft, sx = prev.width / Math.max(1, on.offsetWidth);
		if (Math.abs(dx) < 1 && Math.abs(sx - 1) < 0.01) return;
		th.animate([{ transform: `translateX(${dx}px) scaleX(${sx})`, transformOrigin: "left" }, { transform: "none", transformOrigin: "left" }],
			{ duration: 220, easing: "cubic-bezier(.2, .8, .2, 1)" });
	}
	function thumbSnapshot(el) {
		const m = new Map();
		for (const s of el.querySelectorAll(".seg")) { const th = s.querySelector(".seg-thumb"); if (th && s.classList.contains("has-on")) m.set(s.dataset.seg, { left: th.offsetLeft, width: th.offsetWidth }); }
		return m;
	}

	// La cinta de eventos se desplaza sola (unos 40 px/s) y conserva su posición aunque la vista se
	// redibuje; se detiene con el mouse encima, con la pestaña oculta y con "reducir movimiento". El reloj
	// de la cabecera avanza cada segundo sin redibujar nada.
	const tape = { x: 0, last: 0, hover: false };
	function tapeTick(ts) {
		const el = S.view === "live" ? document.querySelector("#view-live [data-live-tape-track]") : null;
		const dt = tape.last ? Math.min(64, ts - tape.last) : 0;
		tape.last = ts;
		if (el && !tape.hover && !document.hidden && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
			if (tape.el !== el || tape.dirty) { tape.el = el; tape.half = el.scrollWidth / 2; tape.win = el.parentElement.clientWidth; tape.dirty = false; }
			const half = tape.half;
			if (half > tape.win * 0.5) {
				tape.x = (tape.x + dt * 0.04) % half; S.tapeX = tape.x;
				el.style.transform = `translateX(${-tape.x}px)`;
			} else el.style.transform = "none";
		}
		requestAnimationFrame(tapeTick);
	}
	requestAnimationFrame(tapeTick);
	document.addEventListener("pointerover", (e) => { tape.hover = !!e.target.closest?.("[data-live-tape]"); });
	setInterval(() => {
		const c = S.view === "live" ? document.querySelector("#view-live [data-live-clock]") : null;
		if (c) c.textContent = liveFmt("hms").format(Date.now());
	}, 1000);

	/** Un número que cuenta desde el valor anterior hasta el nuevo (600 ms, desacelerando). */
	function countUp(n, from, to) {
		const t0 = performance.now(), dur = 600;
		const step = (now) => {
			if (!n.isConnected) return;
			const p = Math.min(1, (now - t0) / dur), e = 1 - Math.pow(1 - p, 3);
			n.textContent = String(Math.round(from + (to - from) * e));
			if (p < 1) requestAnimationFrame(step);
		};
		requestAnimationFrame(step);
	}

	// ---- Inspector (web/inspector.js) ----
	// también en el reporte exportado: el ZIP trae las páginas en dom/ (ver staticInspector)
	const hasInspector = (t) => !!window.TraceReportsInspector && !!(t.has_dom || t.dom_steps);

	/** La captura del momento del fallo: la última del test (la misma regla del recomendador de locators, que sin un selector no responde). */
	/** Las capturas del test con su hora y su estado: el Inspector elige la de cada página por cercanía. */
	const stepShots = (t) => (t.logs || []).filter((l) => l.screenshot).map((l) => ({ at: l.timestamp, src: l.screenshot, status: l.status }));
	const lastShot = (t) => [...(t.logs || [])].reverse().find((l) => l.screenshot)?.screenshot || "";

	/** Monta el Inspector una sola vez por test: los refrescos del detalle no lo reinician. */
	async function mountInspector(t) {
		const el = $("#inspector-panel");
		if (!el || (S.inspector && S.inspector.el === el && S.inspector.testId === t.id)) return;
		// el detalle se redibujó (llegó el diagnóstico, un paso nuevo): se reubica el mismo Inspector
		if (S.inspector?.testId === t.id) { el.replaceWith(S.inspector.el); return; }
		S.inspector?.api?.destroy();
		const mine = (S.inspector = { el, testId: t.id, api: null });
		const tr = window.TraceReportsI18n.t;
		el.innerHTML = `<p class="m-hint">${esc(tr("Cargando la página del test…"))}</p>`;
		if (STATIC) { // el reporte exportado: las páginas vienen en el ZIP (dom/), sin servidor
			const opts = await staticInspector(t);
			if (S.inspector !== mine || !el.isConnected) return;
			mine.api = window.TraceReportsInspector.mount(el, { ...opts, t: tr });
			return;
		}
		const get = (path) => fetch(path, { headers: { Accept: "application/json" } }).then((r) => (r.status === 200 ? r.json() : null)).catch(() => null);
		const [snapshot, steps, loc] = await Promise.all([
			t.has_dom ? get(`/api/v1/tests/${t.id}/dom`) : null,
			t.dom_steps ? get(`/api/v1/tests/${t.id}/dom/steps`) : [],
			t.status === "FAIL" ? get(`/api/v1/tests/${t.id}/locator`) : null,
		]);
		if (S.inspector !== mine || !el.isConnected) return;
		mine.api = window.TraceReportsInspector.mount(el, {
			snapshot, steps: steps || [], t: tr,
			loadStep: (seq) => get(`/api/v1/tests/${t.id}/dom/steps/${seq}`),
			loadBaseline: (label) => get(`/api/v1/tests/${t.id}/dom/baseline?label=${encodeURIComponent(label || "")}`),
			// modo HTML: las hojas de estilo de la página, guardadas una vez por su hash
			loadCSS: (h) => fetch(`/api/v1/dom/css/${h}`).then((r) => (r.status === 200 ? r.text() : Promise.reject(new Error(String(r.status))))),
			failed: loc?.failed_selector ? { selector: loc.failed_selector } : null,
			backend: loc?.backend_cause || null,
			screenshot: loc?.screenshot || lastShot(t),
			shots: stepShots(t), // la captura de cada paso (la de un WARNING para su página, no la última del test)
			// la fidelidad medida queda en el servidor para compararla por run (structure frente a html);
			// sin permiso de escritura en la UI solo se muestra
			onFidelity: (fidelity, mode) => { apiSend("PUT", `/api/v1/ui/tests/${t.id}/dom/fidelity`, { fidelity, mode }).catch(() => {}); },
		});
	}

	/**
	 * Lo que el Inspector necesita en el reporte exportado: dom/index.js dice qué páginas trae cada test
	 * (las que no entraron por el tope de tamaño quedan en el servidor) y dom/test_<id>_<seq|fail>.js
	 * cada página; se cargan como <script>, así funciona desde file://.
	 */
	async function staticInspector(t) {
		const tr = window.TraceReportsI18n.t;
		if (!window.TRACEREPORTS_DOM_INDEX) await loadScript("dom/index.js").catch(() => {});
		const idx = window.TRACEREPORTS_DOM_INDEX?.[t.id] || { steps: [] };
		const page = async (key) => {
			if (!window.TRACEREPORTS_DOM?.[key]) await loadScript(`dom/test_${key.replace("/", "_")}.js`).catch(() => {});
			return window.TRACEREPORTS_DOM?.[key] || null;
		};
		const loc = STATIC.locators?.[t.id];
		const omitted = idx.omitted || (window.TRACEREPORTS_DOM_INDEX ? 0 : (t.has_dom ? 1 : 0) + (t.dom_steps || 0));
		return {
			snapshot: idx.fail?.included ? await page(`${t.id}/fail`) : null,
			steps: idx.steps.filter((s) => s.included),
			loadStep: (seq) => page(`${t.id}/${seq}`),
			loadCSS: async (h) => { // css/<16 del hash>.js del ZIP (rutas cortas en Windows), una vez por hoja
				if (!/^[0-9a-f]{64}$/.test(h)) throw new Error("hash");
				if (window.TRACEREPORTS_CSS?.[h] === undefined) await loadScript(`css/${h.slice(0, 16)}.js`).catch(() => {});
				if (typeof window.TRACEREPORTS_CSS?.[h] !== "string") throw new Error("missing");
				return window.TRACEREPORTS_CSS[h];
			},
			failed: loc?.failed_selector ? { selector: loc.failed_selector } : null,
			backend: loc?.backend_cause || null,
			screenshot: loc?.screenshot || lastShot(t),
			shots: stepShots(t), // la captura de cada paso (la de un WARNING para su página, no la última del test)
			compareUnavailable: tr("Comparar con el último PASS necesita el servidor: el reporte exportado no trae ejecuciones anteriores."),
			notice: omitted ? tr("{n} páginas de este test no entraron en el reporte exportado (tope de tamaño): siguen en el servidor.", { n: omitted }) : "",
		};
	}

	// ---- mocks ----
	async function openMockFor(conn) {
		CF.MockGenerator.open(conn, { subtitle: `${S.test?.name || ""} · ${conn.method} ${conn.status || ""}` });
	}

	async function openFirstFailedMock(t) {
		const key = `${t.id}:${t.network_total}`;
		if (S.net.key !== key || !S.net.list) { S.net = { key, list: await loadNetwork(t.id) }; }
		const conn = S.net.list.find((c) => isNetError(c) && c.status) || S.net.list.find(isNetError);
		if (conn) openMockFor(conn);
	}

	// ---- en vivo (SSE) ----
	let runTimer = null, testTimer = null;
	const scheduleRun = () => { clearTimeout(runTimer); runTimer = setTimeout(() => loadRun().catch(() => {}), 250); };
	const scheduleTest = () => { clearTimeout(testTimer); testTimer = setTimeout(() => loadTest().catch(() => {}), 250); };
	// la lista de ejecuciones (Recientes, el selector) se pone al día con lo que llega en vivo de
	// cualquiera de ellas: como mucho una vez cada RUNS_REFRESH_MS, también con tests llegando sin parar
	const RUNS_REFRESH_MS = 1500;
	let runsTimer = null;
	const scheduleRuns = () => { if (!runsTimer) runsTimer = setTimeout(() => { runsTimer = null; loadRuns().catch(() => {}); }, RUNS_REFRESH_MS); };

	function setLiveState(state) {
		const was = S.liveState;
		S.liveState = state;
		renderLive();
		// al conectar (o reconectar) se avisan las ejecuciones que empezaron mientras no había conexión:
		// su evento "created" no llegó a esta pestaña
		if (state === "live" && was !== "live") loadRuns().then(catchUpNewRuns).catch(() => {});
	}

	/** Avisa las ejecuciones en curso que esta pestaña todavía no conocía (ver notifyNewRun). */
	function catchUpNewRuns() {
		if (!S.seenRuns) { S.seenRuns = new Set(S.runs.map((r) => r.id)); return; }
		for (const r of S.runs) if (r.status === "RUNNING" && !S.seenRuns.has(r.id)) notifyNewRun(r.id);
		for (const r of S.runs) S.seenRuns.add(r.id);
	}

	const STALE_MS = 5 * 60 * 1000; // una ejecución abierta sin pasos nuevos en 5 min no está "en vivo"
	const lastActivity = (r) => Math.max(r.started_at || 0, S.activity[r.id] || 0,
		...r.tests.map((t) => Math.max(t.started_at || 0, t.ended_at || 0)));

	/**
	 * Indicador de la barra superior. Solo aparece si la ejecución que se está viendo sigue corriendo:
	 * "En vivo" (rojo, grabando) mientras llegan pasos, "Reconectando…" si se cortó el streaming y
	 * "Sin actividad" si la ejecución quedó abierta (p. ej. el proceso de tests murió sin cerrarla).
	 */
	function renderLive() {
		const pill = $("#live-pill");
		const r = S.run;
		if (STATIC || !r || r.status !== "RUNNING") { pill.hidden = true; return; }
		const idle = Date.now() - lastActivity(r);
		const state = S.liveState === "reconnecting" ? "reconnecting" : idle > STALE_MS ? "stale" : "live";
		const mins = Math.round(idle / 60000);
		const [text, tip] = {
			live: ["En vivo", "Ejecución en curso: los pasos, la red y el diagnóstico aparecen aquí apenas los envía el test, sin recargar."],
			reconnecting: ["Reconectando…", "Se cortó la conexión con el servidor. Se reconecta sola; mientras tanto el reporte puede no estar al día."],
			stale: ["Sin actividad", `La ejecución sigue abierta pero no recibe pasos hace ${mins} min. Probablemente el proceso de tests terminó sin cerrarla (end_run / FinishRun).`],
		}[state];
		pill.hidden = false;
		pill.dataset.state = state;
		$(".live-text", pill).textContent = text;
		pill.dataset.tip = tip;
		pill.dataset.tipTitle = text;
	}

	function onLive(type, e) {
		const sameRun = e.run_id === S.runId;
		if (sameRun && ["run", "test", "triage", "summary", "log", "network", "console", "artifact"].includes(type) && escTouchedBy(type, e)) {
			invalidateEscalation();
			if (S.view === "escalate") renderEscalate();
		}
		if (e.run_id) S.activity[e.run_id] = Date.now();
		if (["run", "test", "log"].includes(type)) scheduleLiveView(); // En vivo se pone al día (con calma: muchos eventos por segundo)
		// la ingesta real (para el campo de En vivo): qué llegó, de qué corrida y cuándo; y su pulso
		const F = window.TraceReportsLiveField, ch = F ? F.channelOf(type, e) : "";
		if (ch && e.run_id) {
			const now = Date.now();
			S.ingest.push({ at: now, kind: ch, run_id: e.run_id, test_id: e.test_id || 0 });
			if (S.ingest.length > 4000 || S.ingest[0].at < now - 120e3) S.ingest = S.ingest.filter((x) => x.at >= now - 120e3).slice(-3000);
			const L = S.liveView;
			if (S.view === "live" && L.field && L.exec?.runs?.some((r) => r.id === e.run_id)) {
				if (L.ui.paused) {
					const n = livePausedNew();
					L.field.setPausedCount(n);
					for (const c of document.querySelectorAll("#view-live [data-live-pausecount]")) c.textContent = String(n);
				} else L.field.signal(ch, e.test_id);
			}
		}
		// un resultado nuevo puede ser un cuadro más del historial de cualquier test (ensureHistory)
		if ((type === "test" && e.data?.action === "finished") || (type === "run" && ["finished", "deleted"].includes(e.data?.action))) S.histGen++;
		if (type === "run" && e.data?.action === "created") {
			// el evento trae nombre y ejecución: se avisa aunque la lista (los últimos 10, y la recarga
			// más nueva gana) todavía no lo traiga, como pasa con muchas corridas creadas a la vez
			S.newRunInfo[e.run_id] = { id: e.run_id, status: "RUNNING", ...e.data };
			loadRuns().catch(() => {}).then(() => notifyNewRun(e.run_id));
			if (S.view === "search") { S.runSearch.newCount++; renderRunSearch(); }
		}
		if (type === "log") {
			if (e.test_id === S.testId) appendLiveStep(e.data);
			return;
		}
		// estado y resultados de una ejecución de la lista (cerró, terminó un test): Recientes al día sin F5
		if ((type === "run" && e.data?.action !== "created") || (type === "test" && S.runs.some((r) => r.id === e.run_id))) scheduleRuns();
		if (sameRun) scheduleRun();
		if (e.test_id && e.test_id === S.testId) scheduleTest();
	}

	/** Cambia a otra ejecución (selector de la barra o aviso de ejecución nueva). Desde una vista que
	 * no es de una ejecución (Métricas, Buscar, Mantenimiento, Ajustes) pasa a sus tests: si no, la
	 * ejecución se cargaba detrás y en pantalla no cambiaba nada. ``stay`` la carga sin mover de vista. */
	async function switchRun(id, { stay = false } = {}) {
		S.runId = id;
		if (!stay && GLOBAL_VIEWS.has(S.view)) S.view = "tests";
		$("#run-select").value = String(id);
		S.testId = null; S.test = null; S.rendered = {};
		const RN = window.TraceReportsRunNotify, seen = runInfo(id);
		// ya la está viendo: sale de los avisos con todas las corridas de su ejecución
		S.newRuns = S.newRuns.filter((x) => x !== id && !(seen && RN && RN.execKey(runInfo(x)) === RN.execKey(seen)));
		renderNewRuns();
		writeHash();
		await loadRun(); await loadTest();
	}

	/**
	 * Empezó otra ejecución: no se cambia sola (quien mira un reporte no pierde lo que estaba
	 * viendo). Un aviso arriba ofrece verla en vivo; la insignia junto al selector y un punto en la
	 * pestaña quedan mientras no se vea. Se avisa por ejecución (runnotify.js): las corridas nuevas de
	 * una ejecución ya avisada solo suman a su cuenta. Si no se estaba viendo ninguna, se abre.
	 */
	/** Un run por su id: de la lista o, si todavía no está en ella, del evento que lo anunció. */
	function runInfo(id) {
		return S.runs.find((x) => x.id === id) || S.newRunInfo[id];
	}

	function notifyNewRun(id) {
		S.seenRuns?.add(id);
		const r = runInfo(id);
		if (!r || id === S.runId || S.newRuns.includes(id)) return;
		if (!S.run) { switchRun(id, { stay: true }).catch(() => {}); return; }
		// una corrida nueva de la ejecución que se está mirando (en el reporte o en En vivo) no es "otra"
		const exe = r.execution_id;
		if (exe && (exe === S.run.execution_id || S.liveView?.exec?.key === `x:${exe}` || S.liveView?.exec?.execution_id === exe)) return;
		S.newRuns.push(id);
		renderNewRuns();
	}

	/** Lo silenciado cambió: se guarda y se vuelve a dibujar el aviso, la insignia y la campana. */
	function setNotify(next) {
		const RN = window.TraceReportsRunNotify;
		S.notify = RN ? RN.prune(next, Date.now()) : next;
		RN?.save(S.notify);
		renderNewRuns();
	}

	/** Abre una ejecución nueva: En vivo de su ejecución (o su run, si no tiene execution_id). */
	function openNewExec(g) {
		const RN = window.TraceReportsRunNotify;
		S.newRuns = S.newRuns.filter((x) => RN.execKey(runInfo(x)) !== g.key);
		renderNewRuns();
		if (g.key.startsWith("x:")) location.hash = `#view=live&exec=${encodeURIComponent(g.key)}`;
		else switchRun(g.latest.id).catch(() => {});
	}

	function newRunGroups() {
		const RN = window.TraceReportsRunNotify;
		const runs = S.newRuns.map(runInfo).filter(Boolean);
		return RN ? RN.group(runs, S.notify, Date.now()) : { list: [], loud: [] };
	}

	function renderNewRuns() {
		const { list, loud } = newRunGroups();
		const latest = list[list.length - 1];
		const badge = $("#new-run-badge");
		badge.hidden = !latest;
		document.title = document.title.replace(/^● /, "");
		renderNotifyBell();
		let el = $("#run-toast");
		if (latest) {
			document.title = `● ${document.title}`;
			badge.dataset.key = latest.key;
			$(".nrb-text", badge).textContent = tr(list.length === 1 ? "1 nueva" : "{n} nuevas", { n: list.length });
			badge.dataset.tip = tr("Ejecuciones que empezaron mientras mirabas esta. Clic para ver la última en vivo.");
		}
		if (el?.dataset.undo) return; // mostrando "silenciado · Deshacer": se respeta hasta que venza
		const show = loud[loud.length - 1];
		if (!show) { if (el) el.hidden = true; return; }
		if (!el) {
			el = document.createElement("div");
			el.id = "run-toast";
			el.className = "run-toast";
			el.setAttribute("role", "status");
			el.setAttribute("aria-live", "polite");
			el.hidden = true;
			el.addEventListener("click", (ev) => {
				const RN = window.TraceReportsRunNotify, now = Date.now();
				const { loud: shown } = newRunGroups();
				if (ev.target.closest("[data-toast-open]")) {
					const g = shown.find((x) => x.key === el.dataset.key);
					el.hidden = true;
					if (g) openNewExec(g);
				} else if (ev.target.closest("[data-toast-close]")) {
					// "Ahora no": esa ejecución (y las otras del aviso) no vuelve a avisar con cada corrida
					let next = S.notify;
					for (const g of shown) next = RN.mute(next, g.key, g.title, now);
					el.hidden = true;
					setNotify(next);
				} else if (ev.target.closest("[data-toast-snooze]")) {
					showSnoozeUndo(el); // antes de redibujar: el aviso queda mostrando "Deshacer"
					setNotify(RN.snooze(S.notify, now));
				} else if (ev.target.closest("[data-toast-undo]")) {
					delete el.dataset.undo;
					clearTimeout(el._undoTimer);
					setNotify(RN.resume(S.notify));
				}
			});
			document.body.appendChild(el);
		}
		const wasHidden = el.hidden;
		el.dataset.key = show.key;
		const r = show.latest, n = show.runs.length;
		const where = [r.environment, r.branch].filter(Boolean).map(esc).join(" · ");
		const title = loud.length > 1 ? tr("{n} ejecuciones nuevas en curso", { n: loud.length })
			: n > 1 ? tr("Empezó una ejecución · {n} corridas", { n }) : tr("Empezó otra ejecución");
		el.innerHTML = `<span class="run-toast-tag"><span class="dot" aria-hidden="true"></span>${tr("En vivo")}</span>
			<div class="run-toast-body">
				<b class="run-toast-title">${esc(title)}</b>
				<span class="run-toast-sub" data-no-i18n>${esc(show.key.startsWith("x:") ? show.title : `#${r.id} · ${r.name}`)}${where ? ` · ${where}` : ""}</span>
			</div>
			<button class="run-toast-open" data-toast-open>${tr("Ver en vivo")} →</button>
			<button class="run-toast-later" data-toast-snooze data-tip="${esc(tr("No avisar ninguna ejecución nueva por 15 minutos. Se reactiva en la campana de arriba."))}">${tr("Silenciar 15 min")}</button>
			<button class="run-toast-later" data-toast-close data-tip="${esc(tr("No volver a avisar esta ejecución (sus corridas siguen sumando en la insignia)."))}">${tr("Ahora no")}</button>`;
		el.hidden = false;
		if (wasHidden) { el.classList.remove("run-toast-in"); void el.offsetWidth; el.classList.add("run-toast-in"); }
	}

	/** Tras "Silenciar 15 min": el aviso dice que quedó silenciado y ofrece deshacer unos segundos. */
	function showSnoozeUndo(el) {
		el.dataset.undo = "1";
		el.hidden = false;
		el.innerHTML = `<span class="run-toast-tag muted">${icon("i-bell-off")}</span>
			<div class="run-toast-body"><b class="run-toast-title">${esc(tr("Avisos silenciados por 15 min"))}</b>
				<span class="run-toast-sub">${esc(tr("Se reactivan solos, o antes desde la campana de arriba."))}</span></div>
			<button class="run-toast-later" data-toast-undo>${tr("Deshacer")}</button>`;
		clearTimeout(el._undoTimer);
		el._undoTimer = setTimeout(() => { delete el.dataset.undo; el.hidden = true; renderNewRuns(); }, 6000);
	}

	/**
	 * La campana de la barra: siempre dice si los avisos están activos o silenciados (con los minutos
	 * que quedan) y abre el panel para reactivarlos o silenciar. Sin servidor (reporte exportado), no.
	 */
	function renderNotifyBell() {
		const bell = $("#notify-bell");
		if (!bell) return;
		const RN = window.TraceReportsRunNotify;
		bell.hidden = !!STATIC || !RN;
		if (bell.hidden) return;
		const left = RN.snoozeLeft(S.notify, Date.now()), muted = Object.keys(S.notify.muted).length;
		bell.classList.toggle("off", !!left);
		bell.innerHTML = `${icon(left ? "i-bell-off" : "i-bell")}${left ? `<span class="nb-left">${esc(tr("{n} min", { n: left }))}</span>` : muted ? `<span class="nb-count" aria-hidden="true">${muted}</span>` : ""}`;
		const label = left ? tr("Avisos de ejecuciones nuevas silenciados: quedan {n} min", { n: left })
			: muted ? tr("Avisos de ejecuciones nuevas activos ({n} silenciadas)", { n: muted }) : tr("Avisos de ejecuciones nuevas activos");
		bell.setAttribute("aria-label", label);
		bell.dataset.tip = label;
		const panel = $("#notify-panel");
		if (panel && !panel.hidden) renderNotifyPanel();
	}

	function renderNotifyPanel() {
		const RN = window.TraceReportsRunNotify, panel = $("#notify-panel");
		const now = Date.now(), left = RN.snoozeLeft(S.notify, now);
		const muted = Object.entries(S.notify.muted);
		const until = left ? new Date(S.notify.until).toLocaleTimeString(document.documentElement.lang === "en" ? "en-US" : "es-CL", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }) : "";
		panel.innerHTML = `<h4>${esc(tr("Avisos de ejecuciones nuevas"))}</h4>
			<p class="np-state ${left ? "off" : "on"}">${icon(left ? "i-bell-off" : "i-bell")}<span>${left ? esc(tr("Silenciados hasta las {h} (quedan {n} min)", { h: until, n: left })) : esc(tr("Activos: se avisa cada ejecución nueva una vez, no cada corrida."))}</span></p>
			<div class="np-actions">${left ? `<button type="button" class="cf-btn cf-btn-primary cf-btn-sm" data-notify-resume>${esc(tr("Reactivar ahora"))}</button>` : ""}
				<button type="button" class="cf-btn cf-btn-sm" data-notify-snooze="15">${esc(tr("Silenciar 15 min"))}</button>
				<button type="button" class="cf-btn cf-btn-sm" data-notify-snooze="60">${esc(tr("Silenciar 1 h"))}</button></div>
			${muted.length ? `<h5>${esc(tr("Ejecuciones silenciadas"))}</h5><ul class="np-muted">${muted.map(([k, m]) => `<li><span data-no-i18n>${esc(m.title)}</span><button type="button" class="cf-btn cf-btn-sm" data-notify-unmute="${esc(k)}">${esc(tr("Reactivar"))}</button></li>`).join("")}</ul>
				<p class="m-hint">${esc(tr("Una ejecución silenciada con «Ahora no» vuelve a avisar sola a las 12 h."))}</p>` : ""}`;
	}

	function toggleNotifyPanel(open) {
		const panel = $("#notify-panel"), bell = $("#notify-bell");
		if (!panel || !bell) return;
		const want = open === undefined ? panel.hidden : open;
		if (want === !panel.hidden) return;
		panel.hidden = !want;
		bell.setAttribute("aria-expanded", String(want));
		if (want) {
			// el aviso flotante queda por encima de la barra: se cierra (el panel dice lo mismo y más)
			const toast = $("#run-toast");
			if (toast) { clearTimeout(toast._undoTimer); delete toast.dataset.undo; toast.hidden = true; }
			renderNotifyPanel();
			panel.querySelector("button")?.focus();
		}
	}

	/** Agrega un paso recibido en vivo sin re-renderizar el detalle (animación de entrada). */
	function appendLiveStep(l) {
		const t = S.test;
		if (!t || !l || t.logs.some((x) => x.id === l.id)) return;
		t.logs.push(l);
		S.rendered["test-detail"] = null; // el próximo render completo vuelve a partir de los datos
		const tbody = $("#test-detail .table-results tbody");
		if (tbody && !S.stepFilter) {
			tbody.querySelector(".no-steps")?.closest("tr")?.remove();
			tbody.insertAdjacentHTML("beforeend", stepRow(l, "row-enter"));
			const count = $('#test-detail [data-tab="steps"] .tab-count');
			if (count) count.textContent = t.logs.length;
			if (S.autoScroll) tbody.lastElementChild.scrollIntoView({ block: "nearest", behavior: "smooth" });
		}
		if (S.player?.testId === t.id) S.player.setSteps(t.logs, S.autoScroll ? t.logs.length - 1 : S.player.i);
		if ($("#timeline-panel")) { S.rendered["timeline-panel"] = null; renderTimeline(t); }
		if (l.screenshot && !$('#test-detail [data-tab="replay"]')) scheduleTest(); // aparece la pestaña Replay
	}

	function updateAutoScrollToggle() {
		const btn = $("#autoscroll-toggle");
		btn.hidden = STATIC || S.view !== "tests" || S.test?.status !== "RUNNING";
		btn.setAttribute("aria-checked", String(S.autoScroll));
	}

	// ---------- historial y flaky ----------
	const HIST_CELLS = 20;

	/** Hasta HIST_CELLS cuadros (de la más nueva a la más vieja) que incluyen el test que se ve: con
	 * posteriores, la ventana lo deja al centro para poder ir hacia atrás y hacia adelante. */
	function histWindow(hist, testId) {
		if (!hist || hist.length <= HIST_CELLS) return hist;
		const at = Math.max(0, hist.findIndex((h) => h.test_id === testId));
		const start = Math.min(Math.max(0, at - Math.floor(HIST_CELLS / 2)), hist.length - HIST_CELLS);
		return hist.slice(start, start + HIST_CELLS);
	}

	/**
	 * El historial se pide de nuevo cuando terminó algún test o ejecución desde la última vez
	 * (S.histGen): guardado para siempre, el de un test abierto antes de una ejecución nueva no la
	 * mostraba al volver a él desde un cuadro. Mientras llega el nuevo se sigue viendo el anterior.
	 */
	function ensureHistory(t) {
		const key = `${t.id}:${t.status}`, gen = S.histGen, e = S.hist[key];
		if (e && (e.gen === gen || e.loading === gen)) return;
		S.hist[key] = { gen: e?.gen, data: e?.data ?? null, loading: gen };
		// con las posteriores: abierto desde un cuadro viejo, el historial sigue mostrando lo que vino después
		api(`/api/v1/tests/${t.id}/history?newer=${HIST_CELLS}`)
			.then((h) => { S.hist[key] = { gen, data: h }; if (S.test?.id === t.id) renderTestDetail(); })
			.catch(() => { S.hist[key] = { gen, data: e?.data ?? [] }; });
	}

	function historyStrip(t) {
		const hist = histWindow(S.hist[`${t.id}:${t.status}`]?.data, t.id);
		if (!hist || hist.length < 2) return "";
		const counted = hist.filter((h) => h.status !== "SKIP");
		const ok = counted.filter((h) => h.status !== "FAIL").length;
		const cells = [...hist].reverse().map((h) => {
			const tip = `#${h.run_id} · ${h.status}${h.attempts > 1 ? ` (${h.attempts} intentos)` : ""} · ${fmtDateTime(h.started_at)} · ${fmtDuration(h.duration_ms)}`;
			const cls = `hist-cell ${lower(h.status)} ${h.test_id === t.id ? "current" : ""}`;
			return STATIC && h.run_id !== S.run.id
				? `<span class="${cls}" data-tip="${esc(tip)}"></span>`
				: `<a class="${cls}" href="#run=${h.run_id}&view=tests&test=${h.test_id}" data-tip="${esc(tip)}"></a>`;
		}).join("");
		return `<div class="history">
			<span class="history-lbl" tabindex="0" data-tip="Resultado de este mismo test en sus últimas ejecuciones del mismo proyecto, ambiente y rama, de la más antigua a la más nueva; el marcado es el que estás viendo. Clic en un cuadro para abrir esa ejecución.">Historial</span><span class="hist-cells">${cells}</span>
			<span class="history-rate">${counted.length ? Math.round((ok / counted.length) * 100) : 0}% OK en ${counted.length} ejecuciones</span>
			${t.key_approx ? `<span class="history-approx" data-tip="Este test se reportó sin identidad propia (key/nodeid): su historial se arma por nombre y puede mezclar tests homónimos de otros archivos.">≈ aproximado</span>` : ""}

		</div>`;
	}

	// ---------- timeline: pasos + red + capturas ----------
	function ensureTimeline(t) {
		if (!t.network_total) { renderTimeline(t); return; }
		const key = `${t.id}:${t.network_total}`;
		if (S.net.key === key && S.net.list) { renderTimeline(t); return; }
		if (S.net.key !== key) {
			S.net = { key, list: null };
			loadNetwork(t.id)
				.then((list) => { if (S.net.key === key) { S.net.list = list; renderTimeline(S.test); } })
				.catch(() => { S.net.list = []; renderTimeline(S.test); });
		}
		const el = $("#timeline-panel");
		if (el && !el.childElementCount) el.innerHTML = `<div class="placeholder">Cargando timeline…</div>`;
	}

	function renderTimeline(t) {
		const el = $("#timeline-panel");
		if (!el || !t) return;
		const all = S.net.list && S.net.key === `${t.id}:${t.network_total}` ? S.net.list : [];
		const relevant = (c) => isApi(c) || c.resource_type === "document" || isNetError(c);
		const conns = all.filter((c) => c.started_at && (S.tlAll || relevant(c)));
		const t0 = t.started_at;
		const ends = [t.ended_at || Date.now(), ...t.logs.map((l) => l.timestamp), ...conns.map((c) => c.started_at + (c.duration_ms || 0))];
		const span = Math.max(1, Math.max(...ends) - t0);
		const pct = (v) => `${Math.max(0, Math.min(100, (v / span) * 100)).toFixed(2)}%`;
		const rel = (at) => `+${((at - t0) / 1000).toFixed(2)}s`;
		const items = [
			...t.logs.map((l) => ({ at: l.timestamp, step: l })),
			...conns.map((c) => ({ at: c.started_at, conn: c })),
		].sort((a, b) => a.at - b.at || (a.step ? -1 : 1));
		const rows = items.map(({ at, step, conn }) => {
			if (step) {
				return `<div class="tl-row tl-step ${lower(step.status)}">
					<span class="tl-time">${rel(at)}</span>
					<span class="tl-main"><span class="tl-icon">${icon(STATUS_ICON[step.status] || "i-info")}</span>
						<span class="tl-text">${esc(step.message)}</span>
						${step.screenshot ? `<img class="report-img tl-img" loading="lazy" src="${esc(step.screenshot)}" alt="Captura" data-caption="${esc(step.message)}">` : ""}</span>
					<span class="tl-track"><i class="tl-mark" style="left:${pct(at - t0)}"></i></span>
					<span class="tl-dur"></span>
				</div>`;
			}
			const path = shortPath(conn.url);
			const outcome = netOutcome(conn);
			const cls = outcome === "ERROR" || outcome === "FAILED" ? "tl-err" : outcome === "PENDING" ? "tl-pending" : "";
			return `<div class="tl-row tl-net ${cls}" title="${esc(conn.method + " " + conn.url)}">
				<span class="tl-time">${rel(at)}</span>
				<span class="tl-main"><span class="nbadge" style="background:${METHOD_COLOR[conn.method] || "#6b7280"}">${esc(conn.method)}</span>${statusBadge(conn)}
					<span class="tl-url">${esc(path)}</span></span>
				<span class="tl-track"><i class="tl-bar ${lower(outcome)}" style="left:${pct(at - t0)};width:max(3px, ${pct(conn.duration_ms || 0)})"></i></span>
				<span class="tl-dur">${conn.duration_ms != null ? `${conn.duration_ms} ms` : ""}</span>
			</div>`;
		}).join("");
		const hidden = all.length - all.filter(relevant).length;
		setHTML(el, `<div class="tl-toolbar">
				<span>${t.logs.length} pasos · ${conns.length} llamadas de red · duración ${fmtDuration(span)}</span>
				${hidden > 0 ? `<label class="net-check"><input type="checkbox" id="tl-all" ${S.tlAll ? "checked" : ""}> Mostrar también recursos estáticos (${hidden})</label>` : ""}
			</div>
			<div class="tl-rows">${rows || `<div class="placeholder">Sin pasos ni llamadas registradas.</div>`}</div>`);
	}

	// URL → ruta corta para el timeline (sin host ni query)
	function shortPath(url) {
		try { const u = new URL(url); return u.pathname + (u.search ? "?…" : ""); } catch { return url; }
	}

	// ---------- diagnóstico de la ejecución ----------
	const INCIDENT_ICON = { backend: "i-plug", ai: "i-spark", error: "i-warning", cascade: "i-timeline" };
	// evidencia que escribe el servidor en español y se traduce con las reglas de i18n.en.js
	const TRANSLATED_EVIDENCE = new Set(["network", "cascade", "hung", "blank_page"]);

	/** Hechos observados de un incidente (llamada, error, captura), con enlace al test. */
	function incidentEvidence(inc) {
		// el error de un incidente "error" ya es su título: no se repite
		const ev = (inc.evidence || []).filter((e) => e.kind !== "screenshot" && !(e.kind === "error" && e.text === inc.title)).slice(0, 3);
		if (!ev.length && !inc.location) return "";
		const tr = window.TraceReportsI18n.t;
		return `<ul class="inc-evidence" aria-label="${esc(tr("Evidencia observada"))}">${ev.map((e) =>
			`<li class="ev-${esc(e.kind)}"><a data-goto="${e.test_id}" data-no-i18n>${esc(TRANSLATED_EVIDENCE.has(e.kind) ? tr(e.text) : e.text)}</a></li>`).join("")}${inc.location ? `<li class="mono" data-no-i18n>${esc(inc.location)}</li>` : ""}</ul>`;
	}

	/** Botón "Abrir en Lab" de un incidente de backend: reenvía su llamada causal (no en el ZIP). */
	function incidentLab(inc) {
		const ev = (inc.evidence || []).find((e) => e.kind === "network" && e.ref);
		if (STATIC) return "";
		const tr = window.TraceReportsI18n.t;
		const open = ev ? `<button class="cf-btn cf-btn-sm cf-btn-lab" data-lab-conn="${ev.ref}" data-lab-title="${esc(inc.title)}" data-tip="${esc(tr("Reenvía la llamada que causó este incidente para ver si sigue fallando o ya quedó resuelto"))}">${icon("i-flask")}${tr("Abrir en Lab")}</button>` : "";
		const checks = incidentChecks(inc);
		const verify = checks.length ? `<button class="cf-btn cf-btn-sm cf-btn-lab" data-lab-verify="${esc(inc.key)}" data-lab-title="${esc(inc.title)}" data-lab-tests="${esc(inc.test_ids.join(","))}" data-tip="${esc(tr("Corre los checks guardados de este incidente y registra si quedó resuelto"))}">${icon("i-check")}${tr("Verificar ({n})", { n: checks.length })}</button>` : "";
		return open + verify;
	}

	// ---- FixLab: checks guardados y verificaciones de los incidentes ----
	/** Checks de un incidente: los guardados con su clave o desde uno de sus tests. */
	function incidentChecks(inc) {
		return (S.labRun?.checks || []).filter((c) => c.incident_key === inc.key || inc.test_ids.includes(c.test_id));
	}

	/** Estado de la última verificación del incidente: ✅ resuelto o ❌ sigue fallando. */
	function incidentVerified(inc) {
		const v = STATIC ? null : S.labRun?.verifications?.[inc.key];
		if (!v) return "";
		const tr = window.TraceReportsI18n.t;
		const when = fmtDateTime(v.created_at);
		return v.ok
			? `<span class="lab-verified ok" tabindex="0" data-tip="${esc(tr("Todos los checks pasaron el {d}{a}.", { d: when, a: v.author ? ` (${v.author})` : "" }))}">${icon("i-pass")}${tr("Verificado: resuelto")}</span>`
			: `<span class="lab-verified bad" tabindex="0" data-tip="${esc(tr("En la última verificación ({d}) algún check no pasó.", { d: when }))}">${icon("i-fail")}${tr("Verificado: sigue fallando")}</span>`;
	}

	async function loadLabRun() {
		const r = S.run;
		if (STATIC || !r) return;
		const key = `${r.id}:${S.labRev || 0}`;
		if (S.labRun?.key === key) return;
		S.labRun = { ...(S.labRun?.runId === r.id ? S.labRun : { checks: [], verifications: {} }), key, runId: r.id };
		let data;
		try { data = await api(`/api/v1/runs/${r.id}/lab`); } catch { return; }
		if (S.labRun.key !== key) return; // llegó tarde: ya se pidió la de otra ejecución
		S.labRun = { key, runId: r.id, checks: data.checks || [], verifications: data.verifications || {} };
		if (S.view === "ai") renderAI();
		if (S.view === "dashboard") renderRunSummary();
	}

	/** Algo cambió en el Lab (un check nuevo, una verificación): se vuelve a pedir. */
	function labChanged() {
		S.labRev = (S.labRev || 0) + 1;
		loadLabRun();
	}

	/** El diagnóstico lo escribió el motor local de reglas (sin LLM): se marca como tal, nunca como IA. */
	function isLocal(x) { return x?.provider === "builtin"; }

	function localBadge(rule) {
		const tr = window.TraceReportsI18n.t;
		return `<span class="ai-model ai-local" tabindex="0" data-tip="${esc(tr("Motor local de reglas: sin LLM y sin red. Los datos no salen del servidor."))}">${tr("Motor local")}${rule ? ` · <span class="mono" data-no-i18n>${esc(rule)}</span>` : ""}</span>`;
	}

	/** Causa: hipótesis de la IA (nunca confirmada), o la IA dijo que la evidencia no alcanza. Con el
	 * motor local (local = true) es la conclusión de una regla determinística. */
	function incidentCause(inc, cls, local) {
		const tr = window.TraceReportsI18n.t;
		if (local) {
			if (inc.insufficient) return `<div class="${cls} muted">${tr("El motor local no encontró una regla concluyente.")}${inc.cause ? ` ${esc(inc.cause)}` : ""}</div>`;
			return inc.cause ? `<div class="${cls}"><span class="hyp" data-tip="${esc(tr("Conclusión de una regla del motor local a partir de la evidencia observada. Confírmala antes de tratarla como la causa."))}">${tr("Conclusión")}</span> ${esc(inc.cause)}</div>` : "";
		}
		if (inc.insufficient) return `<div class="${cls} muted">${tr("La IA no propone causa: la evidencia no alcanza.")}</div>`;
		return inc.cause ? `<div class="${cls}"><span class="hyp" data-tip="${esc(tr("Hipótesis de la IA a partir de la evidencia. Confírmala antes de tratarla como la causa."))}">${tr("Causa probable")}</span> ${esc(inc.cause)}</div>` : "";
	}

	/** Aviso de lo que el diagnóstico con IA no cubrió (límite de incidentes, análisis pendientes). */
	function summaryCoverage(sum) {
		const tr = window.TraceReportsI18n.t;
		const notes = [];
		if (sum.ai && sum.ai_incidents && sum.incidents.length > sum.ai_incidents) {
			notes.push(tr("La IA analizó los {a} incidentes más grandes de {n}; el resto se muestra con su evidencia, sin causa sugerida.", { a: sum.ai_incidents, n: sum.incidents.length }));
		}
		if (sum.pending_tests) {
			notes.push(tr("{n} diagnósticos de tests seguían en curso al escribir este resumen; se actualiza cuando terminan.", { n: sum.pending_tests }));
		}
		return notes.length ? `<p class="m-hint sum-coverage">${notes.map(esc).join(" ")}</p>` : "";
	}

	function summaryBody(sum, compact) {
		const tr = window.TraceReportsI18n.t;
		const incidents = sum.incidents.map((inc, n) => `<li class="incident ${inc.kind} ${compact && n >= 8 ? "inc-more" : ""}">
			<span class="inc-icon">${icon(INCIDENT_ICON[inc.kind] || "i-warning")}</span>
			<div class="inc-body">
				<div class="inc-title"><b data-no-i18n>${esc(inc.title)}</b><span class="inc-count">${inc.test_ids.length} test${inc.test_ids.length === 1 ? "" : "s"}</span>${compact ? "" : incidentVerified(inc)}</div>
				${incidentCause(inc, "inc-cause", isLocal(sum))}
				${inc.action ? `<div class="inc-action">→ ${esc(inc.action)}</div>` : ""}
				${compact ? "" : incidentEvidence(inc)}
				${compact ? "" : `<div class="inc-tests">${inc.test_ids.map((id, i) => `<a data-goto="${id}">${esc(inc.test_names[i])}</a>`).join("")}</div>`}
				${compact ? "" : incidentLab(inc)}
			</div></li>`).join("");
		return `${sum.summary ? `<p class="sum-text">${esc(sum.summary)}</p>` : ""}${summaryCoverage(sum)}${incidents ? `<ul class="incidents">${incidents}</ul>` : ""}`;
	}

	function renderRunSummary() {
		const el = $("#run-summary");
		const sum = S.run.summary;
		if (!sum) {
			el.hidden = S.run.status !== "RUNNING";
			setHTML(el, `<div class="sum-head">${icon("i-spark")}<span>La ejecución sigue en curso: el diagnóstico se genera al finalizar.</span></div>`);
			return;
		}
		el.hidden = false;
		loadLabRun();
		if (sum.state === "PENDING") {
			setHTML(el, `<div class="sum-head">${icon("i-spark")}<span>Analizando la ejecución…</span></div><div class="shimmer"></div><div class="shimmer short"></div>`);
			return;
		}
		if (sum.state === "ERROR") {
			setHTML(el, `<div class="sum-head">${icon("i-spark")}<span>No se pudo generar el diagnóstico: ${esc(sum.error)}</span></div>`);
			return;
		}
		setHTML(el, `<div class="sum-head ${S.run.failed ? "has-fail" : "all-ok"}">${icon(S.run.failed ? "i-spark" : "i-pass")}
				<h3>${esc(sum.headline)}</h3>${sum.ai ? `<span class="ai-model">${esc(S.config.ai_model)}</span>` : isLocal(sum) ? localBadge("") : ""}</div>
			${summaryBody(sum, false)}`);
	}

	// una ejecución en curso que lleva este tiempo sin recibir nada probablemente quedó colgada
	const IDLE_WARN_MS = 5 * 60 * 1000;

	/** Cuánto lleva sin recibir datos la ejecución en curso: lo que midió el servidor más lo que pasó
	 * desde que se cargó (una ejecución muerta no manda eventos que la recarguen). */
	const runIdleMs = (r) => (r?.status === "RUNNING" ? (r.idle_ms || 0) + (Date.now() - (S.runLoadedAt || Date.now())) : 0);

	const fmtIdle = (ms) => {
		const m = Math.max(1, Math.floor(ms / 60000));
		return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h${m % 60 ? ` ${m % 60} min` : ""}`;
	};

	/** Quién cerró la ejecución, en palabras. */
	function closedByText(r) {
		if (r.closed_by === "auto") return tr("Se cerró sola por inactividad");
		if (r.closed_by === "manual") return tr("Cerrada manualmente");
		return tr("Cerrada manualmente por {u}", { u: r.closed_by });
	}

	const CLOSE_REASONS = ["Cancelado manualmente", "Se cayó la máquina o el CI", "Otro"];

	function closeRunForm() {
		const m = S.closeRun?.msg;
		return `<form class="close-run-form" data-close-run-form>
			<label class="field"><span class="field-label">${tr("Motivo")}</span>
				<select name="reason">${CLOSE_REASONS.map((r) => `<option value="${r}">${tr(r)}</option>`).join("")}</select></label>
			<label class="field"><span class="field-label">${tr("Detalle (opcional)")}</span>
				<input name="detail" maxlength="400" placeholder="${tr("Ej.: detuve la suite desde el IDE")}"></label>
			<p>${tr("Los tests que siguen en curso quedarán como Interrumpido: no cuentan como fallo ni disparan la IA. Si después llega su resultado real, se acepta.")}</p>
			${m ? `<p class="set-msg err" role="status">${esc(m)}</p>` : ""}
			<div class="close-run-actions"><button type="submit" class="cf-btn cf-btn-sm cf-btn-primary">${icon("i-interrupted")}${tr("Cerrar ejecución")}</button>
				<button type="button" class="cf-btn cf-btn-sm" data-close-run-cancel>${tr("Cancelar")}</button></div>
		</form>`;
	}

	async function closeRunByHand(form) {
		const d = new FormData(form);
		const detail = String(d.get("detail") || "").trim();
		const reason = tr(String(d.get("reason"))) + (detail ? `: ${detail}` : "");
		form.querySelector("[type=submit]").disabled = true;
		try {
			await apiSend("POST", `/api/v1/ui/runs/${S.run.id}/close`, { reason });
			S.closeRun = null;
			await loadRun();
		} catch (err) {
			S.closeRun = { msg: err.message };
			renderRunBanner();
		}
	}

	function renderRunBanner() {
		const el = $("#run-banner");
		const r = S.run, sum = r?.summary;
		const idle = runIdleMs(r);
		el.classList.remove("idle", "cut");
		if (r?.status === "RUNNING" && (idle >= IDLE_WARN_MS || S.closeRun)) {
			el.hidden = false;
			el.classList.add("idle");
			setHTML(el, `<span class="banner-icon">${icon("i-pause")}</span>
				<span class="banner-text"><b>${tr("Sin actividad hace {t}", { t: fmtIdle(idle) })}</b>
					<small>${tr("Sigue en curso, pero no recibe datos: el proceso de los tests pudo detenerse sin avisar (Stop del IDE, terminal cerrada, CI cancelado).")}</small></span>
				${canAct() && !S.closeRun ? `<button class="cf-btn cf-btn-sm" data-close-run-open>${icon("i-interrupted")}${tr("Cerrar ejecución…")}</button>` : ""}
				${S.closeRun ? closeRunForm() : ""}`);
			return;
		}
		if (r && r.status !== "RUNNING" && r.interrupted > 0) {
			el.hidden = false;
			el.classList.add("cut");
			const who = r.closed_by ? closedByText(r) : tr("La ejecución se cortó antes de terminar");
			setHTML(el, `<span class="banner-icon">${icon("i-interrupted")}</span>
				<span class="banner-text"><b>${esc(who)}</b>${r.close_reason ? ` · <span data-no-i18n>${esc(r.close_reason)}</span>` : ""}
					<small>${tr("{n} test(s) quedaron como Interrumpido: no cuentan como fallo.", { n: r.interrupted })}</small></span>
				<a href="#" data-banner-interrupted>${tr("Ver interrumpidos →")}</a>`);
			return;
		}
		const dv = r?.delivery, lost = dv ? (dv.lost || 0) + (dv.dropped || 0) + (dv.rejected || 0) : 0;
		if (dv && (dv.late || lost)) {
			el.hidden = false;
			el.classList.add("cut");
			const at = dv.late_at ? fmtTime(dv.late_at) : "";
			const parts = [dv.lost && tr("{n} eventos perdidos", { n: dv.lost }), dv.dropped && tr("{n} descartados (cola llena)", { n: dv.dropped }),
				dv.rejected && tr("{n} rechazados por el servidor", { n: dv.rejected }), dv.spooled && tr("{n} guardados para reenviar", { n: dv.spooled })].filter(Boolean);
			setHTML(el, `<span class="banner-icon">${icon("i-plug")}</span>
				<span class="banner-text"><b>${dv.late ? tr("Este run llegó al final: no hubo conexión al crearlo") : tr("Parte de la evidencia no llegó al servidor")}</b>
					<small>${dv.late ? esc(tr("A las {h} el cliente no pudo crearlo ({why}); lo grabó en local y lo subió al terminar. No se ve en vivo, pero está completo.", { h: at, why: dv.reason || "—" })) : ""}
					${parts.length ? esc(parts.join(" · ")) : ""}${dv.detail ? ` <span class="dv-detail" data-no-i18n>${esc(dv.detail)}</span>` : ""}</small></span>
				<a href="#view=maintenance" data-banner-connections>${tr("Ver conexiones →")}</a>`);
			return;
		}
		const show = S.view === "tests" && sum?.state === "DONE" && r.failed > 0;
		el.hidden = !show;
		if (!show) return;
		setHTML(el, `<span class="banner-icon">${icon("i-spark")}</span><span class="banner-text"><b>${esc(sum.headline)}</b></span>
			<a href="#" data-view-link="ai">Ver diagnóstico →</a>`);
	}

	// ---------- comparación y endpoints ----------
	function ensureInsights() {
		const key = `${S.run.id}:${S.run.status}:${S.run.total}`;
		if (S.insights.key === key) { renderInsights(); return; }
		S.insights = { key, compare: null, endpoints: null, baselines: null, base: 0 };
		renderInsights();
		Promise.all([api(`/api/v1/runs/${S.run.id}/compare`), api(`/api/v1/runs/${S.run.id}/endpoints`),
			STATIC ? Promise.resolve([]) : api(`/api/v1/runs/${S.run.id}/baselines`).catch(() => [])])
			.then(([compare, endpoints, baselines]) => {
				if (S.insights.key !== key) return;
				Object.assign(S.insights, { endpoints, baselines });
				if (!S.insights.base) S.insights.compare = compare; // 0 = la automática; si se eligió otra, manda esa
				renderInsights();
			})
			.catch((err) => console.warn("insights", err));
	}

	/** Compara contra otra ejecución elegida (mismo proyecto y ambiente). 0 = la automática. */
	async function chooseBase(base) {
		const key = S.insights.key;
		S.insights.base = base;
		try {
			const compare = await api(`/api/v1/runs/${S.run.id}/compare${base ? `?base=${base}` : ""}`);
			if (S.insights.key === key && S.insights.base === base) { S.insights.compare = compare; renderInsights(); }
		} catch (err) { console.warn("compare", err); }
	}

	const BASE_REASON = {
		same_context: "La anterior del mismo proyecto, ambiente y rama.",
		other_branch: "No hay una anterior de esta rama: se compara con la última del mismo proyecto y ambiente en otra rama.",
		chosen: "Elegida a mano.",
	};

	/** "1 fallo nuevo" / "3 fallos nuevos" (las traducciones son por regex sobre el número). */
	const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

	/** Una de las dos ejecuciones de la comparación: cuál es, cómo terminó, cuándo y sus resultados. */
	function cmpRunCard(r, label, extra = "") {
		const own = r.id === S.run.id;
		return `<div class="cmp-run ${own ? "this" : "base"}"><small>${tr(label)}</small>
			<div class="cmp-run-id">${own ? `<b>#${r.id}</b>` : `<a href="#run=${r.id}&view=dashboard"><b>#${r.id}</b></a>`}${runStateLabel(r)}</div>
			<span class="cmp-run-when">${esc(fmtDateTime(r.started_at))}${r.branch ? ` · <span data-no-i18n>${esc(r.branch)}</span>` : ""}</span>
			<div class="cmp-run-bar">${runBar(r)}</div>${extra}</div>`;
	}

	/** Las ejecuciones con que se puede comparar, filtradas por #id, fecha, nombre o rama. */
	function cmpPickerRows() {
		const bases = S.insights.baselines || [];
		// primero las que comparten tests: una de otra suite no sirve para comparar
		const rows = bases.filter((b) => runMatches(b, S.insights.pickQ, [b.name, b.branch, b.status, fmtDateTime(b.started_at)]))
			.sort((x, y) => (y.shared > 0) - (x.shared > 0));
		return rows.length ? rows.map((b) => runRow(b, `data-cmp-choose="${b.id}"`, S.insights.base === b.id || (!S.insights.base && S.insights.compare?.base_run?.id === b.id), true)).join("")
			: `<p class="recents-empty">${tr("Ninguna ejecución coincide.")}</p>`;
	}

	function cmpPicker() {
		const bases = S.insights.baselines || [];
		const green = bases.find((b) => b.status === "PASS" && b.shared > 0);
		return `<div class="cmp-picker" role="dialog" aria-label="${tr("Elegir con qué comparar")}">
			<div class="cmp-picker-head">
				<label class="recents-filter"><svg><use href="#i-search"/></svg><input data-cmp-q type="search" autocomplete="off" value="${esc(S.insights.pickQ || "")}"
					placeholder="${tr("Buscar por #id, fecha (2026-11-03) o rama")}" aria-label="${tr("Buscar ejecución")}"></label>
				<div class="cmp-quick">
					<button type="button" class="cf-btn cf-btn-sm" data-cmp-choose="0" aria-pressed="${!S.insights.base}">${tr("La anterior (automática)")}</button>
					${green ? `<button type="button" class="cf-btn cf-btn-sm" data-cmp-choose="${green.id}" aria-pressed="${S.insights.base === green.id}">${icon("i-pass")}${tr("La última que pasó entera (#{n})", { n: green.id })}</button>` : ""}
					<button type="button" class="link-btn" data-cmp-pick-close>${tr("Cerrar")}</button>
				</div>
			</div>
			<div class="cmp-picker-list" role="listbox">${cmpPickerRows()}</div>
			<p class="m-hint">${tr("Las {n} ejecuciones terminadas más recientes del mismo proyecto y ambiente.", { n: bases.length })}</p>
		</div>`;
	}

	// cómo cambió cada test en común: [grupo de la comparación, clase, texto del cambio]
	const CMP_CHANGES = [["new_failures", "new_failures", "Empezó a fallar"], ["still_failing", "still", "Sigue fallando"],
		["fixed", "fixed", "Se arregló"], ["changed", "changed", "Cambió de estado"], ["unchanged", "same", "Igual"]];

	/** La comparación en una o dos frases, calculada (no la escribe una IA): qué se pudo comparar y qué
	 * cambió. tone: bad (fallos nuevos), good (solo arreglos), none (nada en común), same. */
	function cmpSummary(c, a, b) {
		if (!c.common) {
			return { tone: "none", icon: "i-info", html: `<b>${tr("No hay nada que comparar.")}</b> ${esc(tr("#{a} y #{b} no ejecutaron ningún test en común: #{b} es otra suite ({n}).", { a: a.id, b: b.id, n: b.name }))}
				<br>${tr("Elige con «Cambiar» una ejecución de la misma suite, por ejemplo con el mismo nombre.")}` };
		}
		const n = (k) => (c[k] || []).length;
		const parts = [[n("new_failures"), "{n} empezó a fallar", "{n} empezaron a fallar"], [n("fixed"), "{n} se arregló", "{n} se arreglaron"],
			[n("still_failing"), "{n} sigue fallando", "{n} siguen fallando"], [n("changed"), "{n} cambió de estado", "{n} cambiaron de estado"],
			[n("unchanged"), "{n} sin cambios", "{n} sin cambios"]]
			.filter(([k]) => k).map(([k, one, many]) => tr(k === 1 ? one : many, { n: k }));
		const extra = [n("new_tests") && tr("{n} solo corrió ahora", { n: n("new_tests") }), n("missing") && tr("{n} no corrió esta vez", { n: n("missing") })].filter(Boolean);
		let html = `${esc(tr("De los {n} test(s) que corrieron en las dos:", { n: c.common }))} <b>${esc(parts.join(", "))}</b>.`;
		if (extra.length) html += ` ${esc(tr("Además, {x}.", { x: extra.join(" y ") }))}`;
		if (c.base_later) html += `<br><small>${esc(tr("Ojo: #{b} es posterior a esta ejecución, así que «antes» es la más nueva.", { b: b.id }))}</small>`;
		const tone = n("new_failures") ? "bad" : n("fixed") ? "good" : "same";
		const lead = { bad: tr("Hay fallos nuevos: revísalos primero."), good: tr("Mejoró: hay tests arreglados y ningún fallo nuevo."), same: tr("No hay fallos nuevos.") }[tone];
		return { tone, icon: { bad: "i-fail", good: "i-pass", same: "i-check" }[tone], html: `<b>${esc(lead)}</b> ${html}` };
	}

	/** Cada test que corrió en las dos: su estado antes y ahora, y qué cambió. Los cambios primero. */
	function cmpTable(c, a, b) {
		const slow = new Map((c.slower || []).map((i) => [i.test_id, i]));
		const rows = CMP_CHANGES.flatMap(([key, cls, label]) => (c[key] || []).map((i) => {
			const s = slow.get(i.test_id);
			return `<tr class="cmp-row ${cls}" data-goto="${i.test_id}">
				<td class="cmp-name" data-no-i18n>${esc(i.name)}</td>
				<td>${statusLabel(i.base_status || "")}</td><td aria-hidden="true">→</td><td>${statusLabel(i.status)}</td>
				<td><span class="cmp-change-badge ${cls}">${tr(label)}</span>${s ? `<small class="cmp-slow">${tr("más lento")}: ${fmtDuration(s.base_duration_ms)} → ${fmtDuration(s.duration_ms)}</small>` : ""}</td>
			</tr>`;
		})).join("");
		return `<div class="cmp-table-wrap"><table class="cmp-table">
			<thead><tr><th>Test</th><th>${tr("Antes")} <span data-no-i18n>#${b.id}</span></th><th></th><th>${tr("Ahora")} <span data-no-i18n>#${a.id}</span></th><th>${tr("Cambio")}</th></tr></thead>
			<tbody>${rows}</tbody></table></div>`;
	}

	/** Tests que corrieron en una sola de las dos (plegado: no se pueden comparar). */
	function cmpOnly(items, cls, label, desc) {
		if (!items?.length) return "";
		return `<details class="cmp-only ${cls}"><summary>${esc(label)} <span>${items.length}</span></summary><p class="cmp-desc">${esc(desc)}</p>
			${items.map((i) => (cls === "missing" ? `<span class="cmp-only-item" data-no-i18n>${esc(i.name)}</span>` : `<a data-goto="${i.test_id}" data-no-i18n>${esc(i.name)}</a>`)).join("")}</details>`;
	}

	/** La pregunta para Trace Buddy: el resumen calculado y cada test, para que lo explique con sus palabras. */
	function cmpQuestion(c, a, b) {
		const line = (i) => `- ${i.name}: ${i.base_status || "—"} → ${i.status}`;
		const changed = CMP_CHANGES.slice(0, 4).flatMap(([key]) => (c[key] || []).map(line));
		const text = [`Explícame en palabras simples esta comparación de ejecuciones y qué debería revisar primero.`,
			`Ahora: #${a.id} (${a.status}, ${a.passed}/${a.total} pasan, ${fmtDateTime(a.started_at)}). Antes: #${b.id} (${b.status}, ${b.passed}/${b.total} pasan, ${fmtDateTime(b.started_at)}).`,
			`Tests en común: ${c.common}. Fallos nuevos: ${c.new_failures.length}. Arreglados: ${c.fixed.length}. Siguen fallando: ${c.still_failing.length}. Cambiaron de estado: ${(c.changed || []).length}. Sin cambios: ${c.unchanged.length}. Solo ahora: ${c.new_tests.length}. No corrieron esta vez: ${c.missing.length}.`,
			changed.length ? `Cambios (antes → ahora):\n${changed.join("\n")}` : ""].filter(Boolean).join("\n");
		return text.length > 1900 ? `${text.slice(0, 1900)}…` : text;
	}

	function renderInsights() {
		const { compare, endpoints } = S.insights;
		const head = `<div class="cmp-head"><h5>${tr("¿Qué cambió frente a otra ejecución?")}</h5>
			${compare?.base_run && compare.common && canAct() && buddyCanChat() ? `<button type="button" class="cf-btn cf-btn-sm cmp-ai" data-cmp-ai data-tip="${tr("Trace Buddy te explica esta comparación con sus palabras y qué revisar primero.")}">${icon("i-spark")}${tr("Explícamelo")}</button>` : ""}</div>`;
		let cmp = `${head}<div class="placeholder">Cargando…</div>`;
		const bases = S.insights.baselines || [];
		const change = bases.length ? `<button type="button" class="cf-btn cf-btn-sm cmp-change" data-cmp-pick aria-expanded="${!!S.insights.picking}">${icon("i-search")}${tr("Cambiar")}</button>` : "";
		if (compare) {
			const b = compare.base_run;
			const sum = b && cmpSummary(compare, S.run, b);
			cmp = !b
				? `${head}<div class="cmp-vs">${cmpRunCard(S.run, "Esta ejecución")}</div>
					${S.insights.picking ? cmpPicker() : change}
					<div class="placeholder">No hay una ejecución anterior del mismo proyecto y ambiente con estos tests: no hay con qué comparar.</div>`
				: `${head}
					<div class="cmp-vs">${cmpRunCard(S.run, "Esta ejecución")}<span class="cmp-vs-sep" aria-hidden="true">vs</span>${cmpRunCard(b, compare.base_later ? "Comparada con (posterior)" : "Comparada con", change)}</div>
					${compare.base_reason ? `<p class="m-hint cmp-reason">${esc(BASE_REASON[compare.base_reason] || "")}</p>` : ""}
					${S.insights.picking ? cmpPicker() : ""}
					<div class="cmp-summary ${sum.tone}" role="note">${icon(sum.icon)}<div>${sum.html}</div></div>
					${!compare.common ? "" : `<div class="cmp-chips">
						<span class="cmp-chip new_failures" data-tip="${esc(tr("Pasaban en #{b} y ahora fallan: lo primero que revisar.", { b: b.id }))}">${plural(compare.new_failures.length, "fallo nuevo", "fallos nuevos")}</span>
						<span class="cmp-chip fixed" data-tip="${esc(tr("Fallaban en #{b} y ahora pasan.", { b: b.id }))}">${plural(compare.fixed.length, "arreglado", "arreglados")}</span>
						<span class="cmp-chip still" data-tip="${esc(tr("Fallan en las dos ejecuciones: no es algo nuevo.", { b: b.id }))}">${plural(compare.still_failing.length, "sigue fallando", "siguen fallando")}</span>
						<span class="cmp-chip slower" data-tip="${esc(tr("Tardaron bastante más que en #{b}.", { b: b.id }))}">${plural(compare.slower.length, "más lento", "más lentos")}</span>
					</div>
					${cmpTable(compare, S.run, b)}`}
					${cmpOnly(compare.new_tests, "new_tests", tr("Solo en esta ejecución"), tr("No corrieron en #{b}: no hay con qué compararlos.", { b: b.id }))}
					${cmpOnly(compare.missing, "missing", tr("No corrieron esta vez"), tr("Corrieron en #{b}, pero no en esta ejecución.", { b: b.id }))}`;
		}
		setHTML($("#compare-card"), cmp);

		let eps = `<h5>Endpoints del backend</h5><div class="placeholder">Cargando…</div>`;
		if (endpoints) {
			eps = !endpoints.length
				? `<h5>Endpoints del backend</h5><div class="placeholder">Sin captura de red en esta ejecución.</div>`
				: `<h5>Endpoints del backend <small>con errores primero, luego los más lentos (p95)</small></h5>
					<table class="ep-table"><thead><tr><th>Endpoint</th><th>Llamadas</th><th>Errores</th><th>Prom.</th><th>p95</th><th>Máx.</th></tr></thead>
					<tbody>${endpoints.map((e, i) => `<tr class="link ${e.errors ? "ep-err" : ""} ${i >= 10 && !S.epAll ? "ep-more" : ""}" data-ep-test="${e.test_id}" data-ep-path="${esc(e.path.split("/:id")[0])}" title="${esc(e.host)}">
						<td><span class="nbadge" style="background:${METHOD_COLOR[e.method] || "#6b7280"}">${esc(e.method)}</span> <span class="mono">${esc(e.path)}</span></td>
						<td>${e.count}</td><td>${e.errors ? `<b>${e.errors}</b>` : "0"}</td>
						<td>${e.avg_ms} ms</td><td class="${e.p95_ms >= 1000 ? "slow" : ""}">${e.p95_ms} ms</td><td>${e.max_ms} ms</td></tr>`).join("")}</tbody></table>
					${endpoints.length > 10 ? `<button class="ep-toggle" data-ep-all="1">${S.epAll ? "Ver solo los 10 primeros" : `Ver los ${endpoints.length} endpoints`}</button>` : ""}`;
		}
		setHTML($("#endpoints-card"), eps);
	}

	// ---------- shared mini table ----------
	function testsTable(tests, withError = false) {
		return `<table class="mini-table">
			<thead><tr><th>Estado</th><th>Test</th><th>${withError ? "Error" : "Duración"}</th></tr></thead>
			<tbody>${tests.map((t) => `<tr class="link" data-goto="${t.id}">
				<td>${statusLabel(t.status)}</td><td>${esc(t.name)}</td>
				<td>${withError ? `<span class="mono" style="font-size:12px">${esc((t.error_message || "").split("\n")[0].slice(0, 160))}</span>` : fmtDuration(durationOf(t))}</td>
			</tr>`).join("")}</tbody></table>`;
	}

	function countChips(tests) {
		const c = (st) => tests.filter((t) => t.status === st).length;
		return `<span class="cat-counts">${[["PASS", "pass"], ["FAIL", "fail"], ["SKIP", "skip"], ["WARNING", "warning"]]
			.map(([st, cls]) => c(st) ? `<span class="label ${cls}" title="${st}">${c(st)}</span>` : "").join("")}</span>`;
	}

	// ---------- categories view ----------
	function renderCategories() {
		const groups = new Map();
		for (const t of S.run.tests) {
			const tags = tagsOf(t);
			for (const tag of tags.length ? tags : ["Sin categoría"]) {
				if (!groups.has(tag)) groups.set(tag, []);
				groups.get(tag).push(t);
			}
		}
		const names = [...groups.keys()].sort();
		if (!groups.has(S.catSel)) S.catSel = names[0] ?? null;
		setHTML($("#category-collection"), names.length ? names.map((n) => `<li class="collection-item ${n === S.catSel ? "active" : ""}" data-cat="${esc(n)}">
			<div class="test-head"><span class="test-name">${esc(n)}</span>${countChips(groups.get(n))}</div></li>`).join("") : `<li class="collection-empty">Sin categorías.</li>`);
		setHTML($("#category-detail"), S.catSel ? `<div class="card sub-card"><h4 class="sub-title">${esc(S.catSel)} ${countChips(groups.get(S.catSel))}</h4>
			${testsTable(groups.get(S.catSel))}</div>` : `<div class="card placeholder">No hay categorías en esta ejecución.</div>`);
	}

	// ---------- exceptions view ----------
	function exceptionKey(t) {
		if (t.triage?.state === "DONE") return t.triage.category;
		const first = (t.error_message || "").split("\n")[0].trim();
		return first ? first.slice(0, 90) : "Sin mensaje de error";
	}

	function renderExceptions() {
		const groups = new Map();
		for (const t of S.run.tests.filter((t) => t.status === "FAIL")) {
			const k = exceptionKey(t);
			if (!groups.has(k)) groups.set(k, []);
			groups.get(k).push(t);
		}
		const keys = [...groups.keys()].sort((a, b) => groups.get(b).length - groups.get(a).length);
		if (!groups.has(S.excSel)) S.excSel = keys[0] ?? null;
		setHTML($("#exception-collection"), keys.length ? keys.map((k) => `<li class="collection-item ${k === S.excSel ? "active" : ""}" data-exc="${esc(k)}">
			<div class="test-head"><span class="test-name ${AI_LABEL[k] ? "" : "mono"}" title="${esc(k)}">${AI_LABEL[k] ? esc(AI_LABEL[k]) : esc(k)}</span>
			<span class="label fail">${groups.get(k).length}</span></div>
			${AI_LABEL[k] ? `<span><span class="ai-cat ${esc(k)}">${esc(k)}</span></span>` : ""}</li>`).join("") : `<li class="collection-empty">Sin fallos en esta ejecución.</li>`);
		setHTML($("#exception-detail"), S.excSel ? `<div class="card sub-card"><h4 class="sub-title">${AI_LABEL[S.excSel] ? esc(AI_LABEL[S.excSel]) : esc(S.excSel)}</h4>
			${testsTable(groups.get(S.excSel), true)}</div>` : `<div class="card placeholder">No hay errores en esta ejecución.</div>`);
	}

	// ---------- dashboard ----------
	const cssVar = (n) => getComputedStyle(document.body).getPropertyValue(n).trim();

	function donut(id, labels, data, colors) {
		const canvas = document.getElementById(id);
		if (!canvas) return;
		if (!window.Chart) {
			canvas.parentElement.innerHTML = `<div class="chart-fallback">Chart.js no disponible (sin conexión a internet)</div>`;
			return;
		}
		// la leyenda solo muestra los estados presentes
		const keep = data.map((v, i) => i).filter((i) => data[i] > 0);
		const L = keep.map((i) => labels[i]), D = keep.map((i) => data[i]), Cs = keep.map((i) => colors[i]);
		const key = JSON.stringify([L, D, Cs]);
		const existing = S.charts[id];
		if (existing && existing.key === key) return;
		existing?.chart.destroy();
		const empty = !D.length;
		Chart.defaults.font.family = cssVar("--font-ui"); // la leyenda usa la tipografía del tema
		S.charts[id] = {
			key,
			chart: new Chart(canvas, {
				type: "doughnut",
				data: { labels: L, datasets: [{ data: empty ? [1] : D, backgroundColor: empty ? [cssVar("--border")] : Cs, borderWidth: 0 }] },
				options: {
					cutout: "55%", maintainAspectRatio: false, animation: { duration: 300 },
					layout: { padding: { right: 10 } },
					plugins: {
						legend: { display: !empty, position: "right", align: "start", labels: { color: cssVar("--text"), boxWidth: 10, boxHeight: 10, font: { size: 12 } } },
						tooltip: { enabled: !empty },
					},
				},
			}),
		};
	}

	function renderDashboard() {
		const r = S.run;
		const ss = r.step_stats;
		const stepTotal = Object.values(ss).reduce((a, b) => a + b, 0);
		$("#d-total").textContent = r.total;
		$("#d-steps").textContent = `${stepTotal} pasos`;
		$("#d-pass").textContent = r.passed;
		$("#d-fail").textContent = r.failed;
		$("#d-skip").textContent = r.skipped;
		$("#d-time").textContent = fmtDuration(durationOf(r));
		$("#d-range").textContent = `${fmtTime(r.started_at)} → ${r.ended_at ? fmtTime(r.ended_at) : "en curso"}`;
		const pct = r.total ? Math.round((r.passed / r.total) * 100) : 0;
		const netTotal = r.tests.reduce((a, t) => a + t.network_total, 0);
		const netErrors = r.tests.reduce((a, t) => a + t.network_errors, 0);
		$("#d-pct").textContent = `${pct}%`;
		$("#d-pct-bar").style.width = `${pct}%`;

		$("#test-legend").innerHTML = `<span><b>${r.passed}</b> Test(s) correcto(s)</span><span><b>${r.failed}</b> Test(s) fallado(s), <b>${r.total - r.passed - r.failed}</b> otro(s)</span>`;
		$("#step-legend").innerHTML = `<span><b>${ss.PASS || 0}</b> paso(s) correcto(s)</span><span><b>${ss.FAIL || 0}</b> paso(s) fallado(s), <b>${stepTotal - (ss.PASS || 0) - (ss.FAIL || 0)}</b> otro(s)</span>`;

		const C = ["--pass", "--fail", "--warning", "--skip", "--running", "--info", "--interrupted"].map(cssVar);
		donut("test-analysis", ["Pass", "Fail", "Warning", "Skip", window.TraceReportsI18n.t("En curso"), "Interrupted"],
			[r.passed, r.failed, r.warning, r.skipped, r.running, r.interrupted || 0], [...C.slice(0, 5), C[6]]);
		donut("step-analysis", ["Pass", "Fail", "Warning", "Skip", "Info"], ["PASS", "FAIL", "WARNING", "SKIP", "INFO"].map((k) => ss[k] || 0), [C[0], C[1], C[2], C[3], C[5]]);

		renderRunSummary();
		ensureInsights();

		$("#env-table").innerHTML = [
			["Ambiente", r.environment || "—"],
			...(r.project ? [["Proyecto", r.project]] : []),
			...(r.branch ? [["Rama", r.branch]] : []),
			...(r.commit ? [["Commit", r.commit.slice(0, 12)]] : []),
			["Estado", r.incomplete ? `${r.status} · incompleta (tests sin terminar o ejecución interrumpida)` : r.status],
			...(r.interrupted ? [["Interrumpidos", `${r.interrupted} · no cuentan como fallo`]] : []),
			...(r.closed_by ? [["Cerrada", closedByText(r) + (r.close_reason ? ` · ${r.close_reason}` : "")]] : []),
			["Comienzo", fmtDateTime(r.started_at)],
			["Fin", r.ended_at ? fmtDateTime(r.ended_at) : "en curso"],
			...(netTotal ? [["Conexiones de red", `${netTotal} capturadas · ${netErrors} con error`]] : []),
		].map(([k, v]) => `<tr><td>${esc(k)}</td><td>${esc(v)}</td></tr>`).join("");
	}

	// ---------- temas ----------
	// Cada tema es una interfaz distinta (themes.css); aquí solo se elige y se recuerda.
	const THEMES = [
		{ id: "trace", name: "Trace", desc: "Claro y corporativo. El predeterminado.",
			sw: { bg: "#f2f4f7", top: "#ffffff", nav: "#263238", card: "#ffffff", accent: "#26a69a" } },
		{ id: "trace-dark", name: "Trace Dark", desc: "La misma interfaz, en versión oscura.",
			sw: { bg: "#1b1f24", top: "#252a31", nav: "#15181c", card: "#2c323a", accent: "#26a69a" } },
		{ id: "midnight", name: "Midnight", desc: "Oscuro profundo, menú flotante y acento violeta.",
			sw: { bg: "#08080d", top: "#101017", nav: "#1d1b2e", card: "#16161f", accent: "#a78bfa" } },
		{ id: "paper", name: "Paper", desc: "Editorial: serifa y papel cálido.",
			sw: { bg: "#f7f4ee", top: "#f7f4ee", nav: "#e7e0d3", card: "#fffdf9", accent: "#b4532a" } },
		{ id: "pixel", name: "Pixel", desc: "Arte 8-bit de videojuego retro: ventanas de RPG y barra de vida.",
			sw: { bg: "#1a1c2c", top: "#b13e53", nav: "#1a1c2c", card: "#29366f", accent: "#ffcd75" } },
		{ id: "terminal", name: "Terminal", desc: "Consola de operaciones: monoespaciada, densa y neón.",
			sw: { bg: "#070b08", top: "#0c120e", nav: "#111a14", card: "#0f1712", accent: "#39ff88" } },
	];
	const THEME_KEY = "tracereports-theme";

	function initialTheme() {
		let saved = null;
		try { saved = localStorage.getItem(THEME_KEY); } catch { /* ignore */ }
		if (saved === "dark") saved = "trace-dark"; // valor de versiones anteriores
		if (THEMES.some((t) => t.id === saved)) return saved;
		return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "trace-dark" : "trace";
	}

	function applyTheme(id, persist = true) {
		document.body.dataset.theme = id;
		buddy.three?.setTheme(id); // Trace Buddy se cambia de ropa (applyTheme corre después de definir buddy)
		if (persist) { try { localStorage.setItem(THEME_KEY, id); } catch { /* ignore */ } }
		$$("#theme-menu .theme-option").forEach((o) => o.setAttribute("aria-checked", String(o.dataset.themeId === id)));
		// los gráficos leen los colores del tema al crearse: se vuelven a dibujar
		Object.values(S.charts).forEach((c) => c.chart.destroy());
		S.charts = {};
		if (S.run && S.view === "dashboard") renderDashboard();
		if (S.view === "metrics") renderMetricsCharts();
	}

	function renderThemeMenu() {
		const current = document.body.dataset.theme;
		$("#theme-menu").innerHTML = `<h6>Tema de la interfaz</h6>` + THEMES.map((t) => `
			<button class="theme-option" role="menuitemradio" aria-checked="${t.id === current}" data-theme-id="${t.id}">
				<span class="theme-swatch ${t.topnav ? "topnav" : ""}" style="--sw-bg:${t.sw.bg};--sw-top:${t.sw.top};--sw-nav:${t.sw.nav};--sw-card:${t.sw.card};--sw-accent:${t.sw.accent}">
					<i class="nav"></i><i class="c1"></i><i class="c2"></i><i class="dot"></i></span>
				<span><b>${esc(t.name)}</b><small>${esc(t.desc)}</small></span>
				<svg class="check"><use href="#i-check"/></svg>
			</button>`).join("");
	}

	function setThemeMenu(open) {
		const menu = $("#theme-menu");
		menu.hidden = !open;
		$("#theme-btn").setAttribute("aria-expanded", String(open));
		if (open) (menu.querySelector('[aria-checked="true"]') || menu.querySelector(".theme-option"))?.focus();
	}

	function bindThemePicker() {
		renderThemeMenu();
		const btn = $("#theme-btn"), menu = $("#theme-menu");
		btn.addEventListener("click", (e) => { e.stopPropagation(); setThemeMenu(menu.hidden); });
		menu.addEventListener("click", (e) => {
			const opt = e.target.closest(".theme-option");
			if (opt) { applyTheme(opt.dataset.themeId); setThemeMenu(false); btn.focus(); }
		});
		menu.addEventListener("keydown", (e) => {
			const opts = $$(".theme-option", menu);
			const i = opts.indexOf(document.activeElement);
			if (e.key === "ArrowDown" || e.key === "ArrowUp") {
				e.preventDefault();
				opts[(i + (e.key === "ArrowDown" ? 1 : -1) + opts.length) % opts.length].focus();
			} else if (e.key === "Escape") { setThemeMenu(false); btn.focus(); }
		});
		document.addEventListener("click", (e) => { if (!menu.hidden && !e.target.closest(".theme-picker")) setThemeMenu(false); });
	}

	// ---------- lightbox ----------
	function openLightbox(src, caption) {
		const lb = $("#lightbox");
		const img = $("img", lb);
		img.src = src;
		img.classList.remove("zoomed");
		lb.classList.remove("zoomed");
		$(".lightbox-caption", lb).textContent = caption || "";
		lb.hidden = false;
	}
	const closeLightbox = () => { $("#lightbox").hidden = true; };

	// ---------- command palette ----------
	let paletteFocus = null, paletteIndex = 0, paletteTimer = 0, paletteItems = [];
	function renderPalette() {
		const el = $("#command-results"), q = $("#command-input").value.trim();
		const views = [["tests", "Tests"], ["dashboard", "Análisis"], ["metrics", "Métricas"], ["release", "Release"], ["escalate", "Escalar"], ["settings", "Ajustes"], ["search", "Buscar"]]
			.filter(([, name]) => !q || lower(name).includes(lower(q))).map(([view, name]) => ({ type: "view", view, name }));
		const tests = (S.run?.tests || []).filter((t) => !q || lower(t.name).includes(lower(q)) || lower(t.key).includes(lower(q))).slice(0, 8).map((test) => ({ type: "test", test }));
		paletteItems = views.concat(tests, paletteItems.filter((x) => x.type === "run"));
		if (paletteIndex >= paletteItems.length) paletteIndex = 0;
		el.innerHTML = paletteItems.length ? paletteItems.map((item, i) => `<button class="command-option ${i === paletteIndex ? "active" : ""}" role="option" aria-selected="${i === paletteIndex}" data-command-index="${i}">${item.type === "run" ? `${icon(STATUS_ICON[item.run.status] || "i-list")}<span>${esc(item.run.name)}<br><small>${tr("Ejecuciones")} · #${item.run.id} · ${esc(item.run.branch || item.run.environment || "")}</small></span>` : item.type === "test" ? `${icon(STATUS_ICON[item.test.status] || "i-tests")}<span>${esc(item.test.name)}<br><small>${tr("Tests de esta ejecución")}</small></span>` : `${icon("i-list")}<span>${tr("Ir a")} ${tr(item.name)}</span>`}</button>`).join("") : `<p class="placeholder">${tr("Sin coincidencias")}</p>`;
	}
	function openPalette() { if (STATIC) return; const box = $("#command-palette"); paletteFocus = document.activeElement; box.hidden = false; $("#command-input").value = ""; paletteItems = []; paletteIndex = 0; renderPalette(); $("#command-input").focus(); }
	function closePalette() { const box = $("#command-palette"); if (box.hidden) return; box.hidden = true; paletteFocus?.focus?.(); }
	async function searchPalette() {
		const q = $("#command-input").value.trim(), current = turn("command-search");
		paletteItems = []; renderPalette(); if (!q) return;
		try { const data = await api(`/api/v1/runs/search?q=${encodeURIComponent(q)}&limit=8`); if (!current()) return; paletteItems = data.items.map((run) => ({ type: "run", run })); paletteIndex = 0; renderPalette(); } catch { /* navigation remains available */ }
	}
	function activatePalette(item) { if (!item) return; closePalette(); if (item.type === "run") switchRun(item.run.id).catch(() => {}); else if (item.type === "test") { S.testId = item.test.id; setView("tests"); loadTest().catch(() => {}); } else setView(item.view); }

	// ---------- events ----------
	function bindEvents() {
		bindMetricsEvents();
		bindMaintenance();
		bindLiveView();
		bindSettingsEvents();
		bindAIEscalateEvents();
		// cambio de idioma: lo que se dibuja en canvas (gráficos) no pasa por el DOM
		document.addEventListener("tracereports:lang", () => {
			Object.values(S.charts).forEach((c) => c.chart.destroy());
			S.charts = {};
			S.rendered = {};
			renderView();
		});
		$$(".side-nav a").forEach((a) => a.addEventListener("click", (e) => { e.preventDefault(); setView(a.dataset.view); }));

		$("#run-select").addEventListener("change", (e) => switchRun(Number(e.target.value)));
		$("#recents-list").addEventListener("click", (e) => { const row = e.target.closest("[data-recent-run]"); if (row) { $("#recents-control").removeAttribute("open"); switchRun(Number(row.dataset.recentRun)).then(renderRecents); } });
		$("#recents-control").addEventListener("toggle", (e) => { if (e.target.open) { renderRecents(); $("#recents-filter").focus(); } });
		$("#recents-filter").addEventListener("input", (e) => { S.recents.q = e.target.value; renderRecents(); });
		$("#recents-filter").addEventListener("keydown", (e) => {
			if (e.key === "Enter") $("#recents-list [data-recent-run]")?.click(); // la primera que coincide
			if (e.key === "ArrowDown") { e.preventDefault(); $("#recents-list [data-recent-run]")?.focus(); }
			if (e.key === "Escape") $("#recents-control").removeAttribute("open");
		});
		$("#recents-list").addEventListener("keydown", (e) => {
			const rows = $$("#recents-list [data-recent-run]"), i = rows.indexOf(document.activeElement);
			if (e.key === "ArrowDown" && i < rows.length - 1) { e.preventDefault(); rows[i + 1].focus(); }
			if (e.key === "ArrowUp") { e.preventDefault(); (i > 0 ? rows[i - 1] : $("#recents-filter")).focus(); }
			if (e.key === "Escape") $("#recents-control").removeAttribute("open");
		});
		$$("[data-recents-tab]").forEach((b) => b.addEventListener("click", () => { S.recents.tab = b.dataset.recentsTab; renderRecents(); }));
		$("[data-recents-all]").addEventListener("click", () => { $("#recents-control").removeAttribute("open"); setView("search"); });
		$("#recents-search").addEventListener("click", () => setView("search"));
		$("#run-search-input").addEventListener("input", (e) => { S.runSearch.q = e.target.value; scheduleRunSearch(); });
		$("#search-filters").addEventListener("change", (e) => { const key = e.target.dataset.searchFilter; if (!key) return; S.runSearch[key] = e.target.value; scheduleRunSearch(); });
		$("#search-sort").addEventListener("change", (e) => { S.runSearch.sort = e.target.value; scheduleRunSearch(); });
		$("#search-shortcuts").addEventListener("click", (e) => {
			const quick = e.target.closest("[data-search-quick]");
			if (quick) { const x = S.runSearch; Object.assign(x, { status: "", flaky: false, incomplete: false }); if (quick.dataset.searchQuick === "fail") x.status = "FAIL"; if (quick.dataset.searchQuick === "flaky") x.flaky = true; if (quick.dataset.searchQuick === "running") x.status = "RUNNING"; if (quick.dataset.searchQuick === "incomplete") x.incomplete = true; scheduleRunSearch(); return; }
			const range = e.target.closest("[data-search-range]"); if (range) { const days = Number(range.dataset.searchRange); const to = new Date(), from = new Date(to); from.setDate(from.getDate() - Math.max(0, days - 1)); S.runSearch.from = from.toLocaleDateString("sv"); S.runSearch.to = to.toLocaleDateString("sv"); scheduleRunSearch(); return; }
			if (e.target.closest("[data-search-save]")) { saveRunSearch(); return; }
			if (e.target.closest("[data-search-clear]")) { Object.assign(S.runSearch, { q: "", project: "", environment: "", branch: "", tag: "", status: "", from: "", to: "", incomplete: false, flaky: false, sort: "recent" }); scheduleRunSearch(); }
		});
		$("#search-saved").addEventListener("change", (e) => { const item = savedRunSearches()[Number(e.target.value)]; if (!item) return; Object.assign(S.runSearch, item.query, { items: [], cursor: "", selected: 0 }); scheduleRunSearch(); e.target.value = ""; });
		$("#run-banner").addEventListener("click", (e) => {
			if (e.target.closest("[data-close-run-open]")) { S.closeRun = {}; renderRunBanner(); $("[data-close-run-form] select")?.focus(); return; }
			if (e.target.closest("[data-close-run-cancel]")) { S.closeRun = null; renderRunBanner(); return; }
			if (e.target.closest("[data-banner-interrupted]")) { e.preventDefault(); S.status = "INTERRUPTED"; setView("tests"); }
			if (e.target.closest("[data-banner-connections]")) { e.preventDefault(); S.maint.focusConn = true; connUI().open.runs = true; setView("maintenance"); }
		});
		$("#run-banner").addEventListener("submit", (e) => {
			const f = e.target.closest("[data-close-run-form]");
			if (!f) return;
			e.preventDefault();
			closeRunByHand(f);
		});
		// una ejecución muerta no manda eventos: el "sin actividad hace…" se actualiza solo (sin pisar el formulario)
		setInterval(() => { if (S.run?.status === "RUNNING" && !S.closeRun && !GLOBAL_VIEWS.has(S.view)) renderRunBanner(); }, 30000);
		$("#search-chips").addEventListener("click", (e) => { const b = e.target.closest("[data-search-remove]"); if (!b) return; const key = b.dataset.searchRemove; S.runSearch[key] = key === "incomplete" || key === "flaky" ? false : ""; scheduleRunSearch(); });
		// la casilla está dentro de una etiqueta que corta el click: se escucha change
		$("#run-search-results").addEventListener("change", (e) => {
			const check = e.target.closest("[data-search-compare]");
			if (check) { const id = Number(check.dataset.searchCompare), x = S.runSearch; x.compare = check.checked ? [...x.compare.filter((n) => n !== id), id].slice(-2) : x.compare.filter((n) => n !== id); renderRunSearch(); if (x.preview && x.preview[0].id === x.selected) renderSearchPreview(...x.preview); /* su botón Comparar depende de la marca */ if (x.compare.length === 2) compareSearchRuns(x.compare[0], x.compare[1]).catch(() => {}); return; }
		});
		$("#run-search-results").addEventListener("click", (e) => {
			// la casilla de comparar (o su etiqueta) no abre la ejecución; sin onclick inline: la CSP no lo permite
			if (e.target.closest("[data-search-compare], [data-search-compare-label]")) return;
			const row = e.target.closest("[data-search-run]"); if (row) selectSearchRun(Number(row.dataset.searchRun));
		});
		$("#run-search-preview").addEventListener("click", (e) => { const test = e.target.closest("[data-search-test]"); if (test) { S.testId = Number(test.dataset.searchTest); switchRun(Number(test.dataset.searchTestRun)).then(() => { S.testId = Number(test.dataset.searchTest); setView("tests"); return loadTest(); }).catch(() => {}); return; } const open = e.target.closest("[data-search-open]"); if (open) { switchRun(Number(open.dataset.searchOpen)).then(() => setView("dashboard")).catch(() => {}); return; } const cmp = e.target.closest("[data-search-compare-open]"); if (cmp) compareSearchRuns(Number(cmp.dataset.searchCompareBase), Number(cmp.dataset.searchCompareOpen)).catch(() => {}); });
		$("#search-more").addEventListener("click", () => loadRunSearch(false));
		$("#search-new-runs").addEventListener("click", () => { S.runSearch.newCount = 0; loadRunSearch(); });
		$("#run-search-results").addEventListener("click", (e) => { if (e.target.closest("[data-search-retry]")) loadRunSearch(); });
		$("#new-run-badge").addEventListener("click", () => { const { list } = newRunGroups(); const g = list[list.length - 1]; if (g) openNewExec(g); });
		$("#notify-bell")?.addEventListener("click", (e) => { e.stopPropagation(); toggleNotifyPanel(); });
		$("#notify-panel")?.addEventListener("click", (e) => {
			e.stopPropagation();
			const RN = window.TraceReportsRunNotify, now = Date.now();
			const sn = e.target.closest("[data-notify-snooze]"), un = e.target.closest("[data-notify-unmute]");
			if (e.target.closest("[data-notify-resume]")) setNotify(RN.resume(S.notify));
			else if (sn) setNotify(RN.snooze(S.notify, now, Number(sn.dataset.notifySnooze) * 60000));
			else if (un) setNotify(RN.unmute(S.notify, un.dataset.notifyUnmute));
		});
		document.addEventListener("click", (e) => { if (!e.target.closest?.("#notify-panel, #notify-bell")) toggleNotifyPanel(false); });
		document.addEventListener("keydown", (e) => { if (e.key === "Escape" && $("#notify-panel") && !$("#notify-panel").hidden) { toggleNotifyPanel(false); $("#notify-bell")?.focus(); } });
		// la cuenta de la campana y el fin de lo silenciado, aunque no lleguen eventos
		setInterval(() => {
			const RN = window.TraceReportsRunNotify, before = S.notify.until;
			if (RN) S.notify = RN.prune(S.notify, Date.now());
			if (before && !S.notify.until) renderNewRuns(); else renderNotifyBell();
		}, 30000);
		renderNotifyBell();
		$("#compare-card").addEventListener("click", (e) => {
			if (e.target.closest("[data-cmp-pick]")) { S.insights.picking = !S.insights.picking; renderInsights(); $("[data-cmp-q]")?.focus(); return; }
			if (e.target.closest("[data-cmp-ai]")) {
				const c = S.insights.compare;
				if (c?.base_run) buddyAskText(cmpQuestion(c, S.run, c.base_run));
				return;
			}
			if (e.target.closest("[data-cmp-pick-close]")) { S.insights.picking = false; renderInsights(); return; }
			const pick = e.target.closest("[data-cmp-choose]");
			if (pick) { S.insights.picking = false; S.insights.pickQ = ""; chooseBase(Number(pick.dataset.cmpChoose)); }
		});
		$("#compare-card").addEventListener("input", (e) => {
			if (!e.target.matches("[data-cmp-q]")) return;
			S.insights.pickQ = e.target.value;
			$("#compare-card .cmp-picker-list").innerHTML = cmpPickerRows(); // solo la lista: el campo conserva el foco
		});
		$("#compare-card").addEventListener("keydown", (e) => {
			if (e.key === "Escape" && S.insights.picking) { S.insights.picking = false; renderInsights(); }
			if (e.key === "Enter" && e.target.matches("[data-cmp-q]")) $("#compare-card .cmp-picker-list [data-cmp-choose]")?.click();
		});

		$("#test-collection").addEventListener("click", async (e) => {
			const li = e.target.closest("[data-test]");
			if (!li) return;
			S.testId = Number(li.dataset.test);
			S.stepFilter = "";
			$$("#test-collection .collection-item").forEach((x) => x.classList.toggle("active", x === li));
			S.rendered["test-collection"] = null;
			writeHash();
			await loadTest();
		});

		$("#status-filters").addEventListener("click", (e) => {
			const b = e.target.closest("button");
			if (!b) return;
			S.status = b.dataset.status;
			renderTests();
		});
		$("#test-detail").addEventListener("submit", (e) => {
			const vf = e.target.closest("[data-verdict-form]");
			if (vf) {
				e.preventDefault();
				const d = new FormData(vf);
				saveVerdict(Number(vf.dataset.verdictForm), d.get("verdict"), d.get("comment"), d.get("author"));
				return;
			}
			const f = e.target.closest("[data-quar-form]");
			if (!f) return;
			e.preventDefault();
			const d = new FormData(f);
			quarantineAction("POST", "/api/v1/ui/quarantine", { test_id: Number(f.dataset.quarForm), reason: d.get("reason"), owner: d.get("owner"), days: Number(d.get("days")) });
		});
		$("#test-detail").addEventListener("click", (e) => {
			const ve = e.target.closest("[data-verdict-edit]");
			if (ve) { S.verdictEdit = Number(ve.dataset.verdictEdit); renderTestDetail(); return; }
			if (e.target.closest("[data-verdict-cancel]")) { S.verdictEdit = null; renderTestDetail(); return; }
			const same = e.target.closest("[data-verdict-same]");
			if (same) {
				const prev = S.test?.previous_verdict;
				if (prev) saveVerdict(Number(same.dataset.verdictSame), prev.verdict, prev.comment, readAuthor());
				return;
			}
			const open = e.target.closest("[data-quar-open]");
			if (open) { S.quar = { testId: Number(open.dataset.quarOpen) }; renderTestDetail(); $("[data-quar-form] input")?.focus(); return; }
			if (e.target.closest("[data-quar-cancel]")) { S.quar = null; renderTestDetail(); return; }
			const rm = e.target.closest("[data-quar-remove]");
			if (rm) { quarantineAction("DELETE", `/api/v1/ui/quarantine/${rm.dataset.quarRemove}`); return; }
			const b = e.target.closest("[data-step-filter]");
			if (!b) return;
			S.stepFilter = b.dataset.stepFilter;
			renderTestDetail();
		});

		// pestaña "Red"
		const detail = $("#test-detail");
		detail.addEventListener("click", (e) => {
			const tabBtn = e.target.closest("[data-tab]");
			if (tabBtn) { S.detailTab = tabBtn.dataset.tab; renderTestDetail(); return; }
			const f = e.target.closest("[data-net-filter]");
			if (f) { S.netFilter = f.dataset.netFilter; renderNetList(); return; }
			const base = e.target.closest("[data-baseline]");
			if (base) {
				const c = S.net.list?.[Number(base.closest("[data-net-idx]").dataset.netIdx)];
				if (c) openBaseline(c, base);
				return;
			}
			const curl = e.target.closest("[data-curl]");
			if (curl) {
				const c = S.net.list?.[Number(curl.closest("[data-net-idx]").dataset.netIdx)];
				if (c) copyText(CF.curlOf(c), curl);
				return;
			}
			const copy = e.target.closest("[data-copy]");
			if (copy) { copyText(copy.dataset.copy, copy); return; }
			const open = e.target.closest("[data-open-full]");
			if (open) {
				const c = S.net.list?.[Number(open.closest("[data-net-idx]").dataset.netIdx)];
				if (c) window.open(URL.createObjectURL(new Blob([c.response_body], { type: "text/plain;charset=utf-8" })), "_blank");
			}
		});
		detail.addEventListener("input", (e) => {
			if (e.target.id === "net-search") { S.netSearch = e.target.value; renderNetList(); }
		});
		detail.addEventListener("change", (e) => {
			if (e.target.id === "tl-all") { S.tlAll = e.target.checked; S.rendered["timeline-panel"] = null; renderTimeline(S.test); }
			if (e.target.id === "net-api-only") { S.netApiOnly = e.target.checked; renderNetList(); }
		});
		// el detalle de cada conexión se arma recién al abrirla (bodies de varios MB no inflan el DOM)
		detail.addEventListener("toggle", (e) => {
			const card = e.target;
			if (!card.matches?.("details.net-card") || !card.open) return;
			const box = card.querySelector(".net-detail");
			if (box.childElementCount) return;
			const c = S.net.list?.[Number(card.dataset.netIdx)];
			if (c) box.innerHTML = netDetail(c);
		}, true);
		$("#search-tests").addEventListener("input", (e) => { S.search = e.target.value; renderTests(); });

		$("#category-collection").addEventListener("click", (e) => {
			const li = e.target.closest("[data-cat]");
			if (li) { S.catSel = li.dataset.cat; renderCategories(); }
		});
		$("#exception-collection").addEventListener("click", (e) => {
			const li = e.target.closest("[data-exc]");
			if (li) { S.excSel = li.dataset.exc; renderExceptions(); }
		});
		// funcionalidades: locators, mocks, drift, auto-scroll
		document.addEventListener("click", (e) => {
			const drift = e.target.closest("[data-drift]");
			if (drift) { e.preventDefault(); openDrift(Number(drift.dataset.drift)); return; }
			const mock = e.target.closest("[data-mock]");
			if (mock) {
				e.preventDefault(); e.stopPropagation();
				const c = S.net.list?.[Number(mock.closest("[data-net-idx]")?.dataset.netIdx)];
				if (c) openMockFor(c);
				return;
			}
			if (e.target.closest("[data-mock-first]") && S.test) { openFirstFailedMock(S.test); return; }
			const lab = e.target.closest("[data-lab]");
			if (lab) {
				e.preventDefault(); e.stopPropagation();
				const c = S.net.list?.[Number(lab.closest("[data-net-idx]")?.dataset.netIdx)];
				if (c) window.TraceReportsLab?.open(c.id, { subtitle: `${S.test?.name || ""} · ${c.method} ${c.status || c.error_text || ""}`, onChange: labChanged });
				return;
			}
			const labConn = e.target.closest("[data-lab-conn]");
			if (labConn) { e.preventDefault(); window.TraceReportsLab?.open(Number(labConn.dataset.labConn), { subtitle: labConn.dataset.labTitle || "", onChange: labChanged }); return; }
			const labVerify = e.target.closest("[data-lab-verify]");
			if (labVerify) {
				e.preventDefault();
				// si la ejecución se está refrescando en vivo, el incidente puede faltar un instante en el
				// resumen: el botón trae lo necesario para abrir igual la verificación (nunca un clic mudo)
				const key = labVerify.dataset.labVerify;
				const inc = (S.run.summary?.incidents || []).find((i) => i.key === key)
					|| { key, title: labVerify.dataset.labTitle || "", test_ids: (labVerify.dataset.labTests || "").split(",").filter(Boolean).map(Number) };
				if (inc) window.TraceReportsLab?.verify({ runId: S.run.id, incidentKey: inc.key, title: inc.title, checks: incidentChecks(inc), canSend: !!S.config.ui_actions, onDone: labChanged });
				return;
			}
			const lang = e.target.closest("[data-loc-lang]");
			if (lang) { S.locLang = lang.dataset.locLang; renderTestDetail(); return; }
			const tool = e.target.closest("[data-loc-tool]");
			if (tool) {
				S.locTool = tool.dataset.locTool;
				if (S.locTool !== "selenium" && S.locLang === "java") S.locLang = "python"; // Java solo en Selenium
				renderTestDetail();
				return;
			}
			const copyBtn = e.target.closest("[data-loc-copy]");
			if (copyBtn) {
				const s = currentLocator()?.suggestions[Number(copyBtn.dataset.locCopy)];
				if (s) CF.copyText(snippetOf(s, S.test), copyBtn);
				return;
			}
			const insp = e.target.closest("[data-loc-inspect]");
			if (insp) { inspectLocator(Number(insp.dataset.locInspect)); return; }
			if (e.target.closest("[data-loc-jump]")) {
				const card = $("#locator-card");
				if (card) { card.open = true; card.scrollIntoView({ behavior: "smooth", block: "center" }); card.classList.remove("loc-flash"); void card.offsetWidth; card.classList.add("loc-flash"); }
				return;
			}
			if (e.target.closest("#autoscroll-toggle")) {
				S.autoScroll = !S.autoScroll;
				try { localStorage.setItem("tracereports-autoscroll", S.autoScroll ? "on" : "off"); } catch { /* ignore */ }
				updateAutoScrollToggle();
			}
		});
		document.addEventListener("toggle", (e) => { if (e.target.id === "locator-card") S.locOpen = e.target.open; }, true);

		// ranking de endpoints → pestaña Red del test, filtrada por el endpoint
		document.addEventListener("click", async (e) => {
			const row = e.target.closest("[data-ep-test]");
			if (row) {
				S.testId = Number(row.dataset.epTest);
				S.detailTab = "network"; S.netSearch = row.dataset.epPath; S.netFilter = "";
				setView("tests");
				await loadTest();
				return;
			}
			if (e.target.closest("[data-ep-all]")) { S.epAll = !S.epAll; renderInsights(); return; }
			const link = e.target.closest("[data-view-link]");
			if (link) {
				e.preventDefault();
				if (link.dataset.setTab) { S.settings.tab = link.dataset.setTab; try { localStorage.setItem("tracereports-settings-tab", link.dataset.setTab); } catch { /* */ } }
				setView(link.dataset.viewLink);
			}
		});
		// jump from category / exception tables to the test detail
		document.addEventListener("click", async (e) => {
			const row = e.target.closest("[data-goto]");
			if (!row) return;
			S.testId = Number(row.dataset.goto);
			S.status = ""; S.search = ""; S.stepFilter = "";
			$("#search-tests").value = "";
			setView("tests");
			await loadTest();
		});

		// lightbox
		$("#test-detail").addEventListener("click", (e) => {
			const img = e.target.closest(".report-img");
			if (img) openLightbox(img.src, img.dataset.caption);
		});
		$("#lightbox").addEventListener("click", (e) => {
			const lb = $("#lightbox");
			if (e.target.tagName === "IMG") {
				e.target.classList.toggle("zoomed");
				lb.classList.toggle("zoomed");
			} else closeLightbox();
		});
		document.addEventListener("keydown", (e) => {
			if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") { e.preventDefault(); openPalette(); return; }
			const palette = $("#command-palette");
			if (!palette.hidden) { if (e.key === "Escape") { e.preventDefault(); closePalette(); } else if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); paletteIndex = (paletteIndex + (e.key === "ArrowDown" ? 1 : -1) + paletteItems.length) % Math.max(1, paletteItems.length); renderPalette(); } else if (e.key === "Enter") { e.preventDefault(); activatePalette(paletteItems[paletteIndex]); } else if (e.key === "Tab") { e.preventDefault(); $("#command-input").focus(); } return; }
			if (e.key === "Escape" && !CF.Drawer.isOpen()) closeLightbox();
		});
		$("#command-input").addEventListener("input", () => { clearTimeout(paletteTimer); paletteTimer = setTimeout(searchPalette, 150); });
		$("#command-results").addEventListener("click", (e) => activatePalette(paletteItems[Number(e.target.closest("[data-command-index]")?.dataset.commandIndex)]));
		$("#command-palette").addEventListener("click", (e) => { if (e.target.id === "command-palette") closePalette(); });

		// links directos (#run=..&view=..&test=..) y atrás/adelante del navegador
		window.addEventListener("hashchange", async () => {
			const before = S.runId;
			CF.Drawer.close(); // el drawer es del test anterior
			readHash();
			S.rendered = {};
			if (S.runId !== before) { S.test = null; await loadRun(); } else { renderView(); renderLive(); }
			if (S.view === "search") await loadRunSearch();
			if (S.view === "tests") await loadTest();
			if (S.view === "live") loadLiveView();
		});

		bindThemePicker();
	}

	// ---------- IA: análisis de los fallos de la ejecución ----------
	const canAct = () => !STATIC && S.config.ui_actions;
	const actReason = () => tr({ login: "Inicia sesión para usar esta acción.", remote: "Por seguridad, sin login configurado esta acción solo se puede usar desde el equipo del servidor (ver Ajustes)." }[S.config.ui_actions_reason] || "");

	async function loadRecurrence() {
		const r = S.run;
		const key = `${r.id}:${r.summary?.updated_at || 0}`;
		if (S.aiv.recKey === key || STATIC) return;
		S.aiv.recKey = key;
		let rec;
		try { rec = await api(`/api/v1/runs/${r.id}/recurrence`); } catch { rec = {}; }
		if (S.aiv.recKey !== key) return; // llegó tarde: ya se pidió la de otra ejecución
		S.aiv.rec = rec;
		if (S.view === "ai") renderAI();
	}

	function recurrencePill(key) {
		const rc = S.aiv.rec?.[key];
		if (!rc || !rc.of) return "";
		return rc.seen
			? `<span class="rec-pill warn" tabindex="0" data-tip="${esc(tr("La misma causa apareció en {n} de las {of} ejecuciones anteriores de esta suite: no es un problema aislado.", { n: rc.seen, of: rc.of }))}">${icon("i-bolt")}${tr("Recurrente · {n}/{of}", { n: rc.seen, of: rc.of })}</span>`
			: `<span class="rec-pill new" tabindex="0" data-tip="${esc(tr("No apareció en las {of} ejecuciones anteriores de esta suite.", { of: rc.of }))}">${tr("Nuevo")}</span>`;
	}

	function triageBlock(t) {
		const tr0 = t.triage;
		if (!tr0) return `<p class="ai-none">${S.config.ai_enabled ? tr("Sin diagnóstico todavía.") : tr("IA desactivada: sin diagnóstico.")}</p>`;
		if (tr0.state === "PENDING") return `<div class="shimmer"></div><div class="shimmer short"></div>`;
		if (tr0.state === "ERROR") return `<p class="ai-err">${icon("i-fail")}<span>${esc(tr0.error || "")}</span></p>`;
		if (tr0.state === "SKIPPED") return `<p class="ai-none">${esc(tr0.error || "")}</p>`;
		return `<p class="ai-sum">${esc(tr0.summary)}</p><p class="ai-sug"><b>${tr("Sugerencia")}:</b> ${esc(tr0.suggestion)}</p>`;
	}

	function renderAI() {
		const el = $("#view-ai");
		const r = S.run;
		const failed = r.tests.filter((t) => t.status === "FAIL");
		const sum = r.summary;
		loadRecurrence();
		loadLabRun();
		const pendingTests = failed.filter((t) => !t.triage || t.triage.state === "ERROR" || t.triage.state === "SKIPPED").length;
		const counts = {};
		failed.forEach((t) => { const c = t.triage?.state === "DONE" ? t.triage.category : ""; counts[c] = (counts[c] || 0) + 1; });
		const maxC = Math.max(1, ...Object.values(counts));
		const aiOn = S.config.ai_enabled;
		const busy = S.aiv.busy;
		const actions = canAct() && aiOn && failed.length ? `<div class="ai-actions">
				${pendingTests ? `<button class="cf-btn cf-btn-primary" data-ai-rerun="missing" ${busy ? "disabled" : ""}>${icon("i-spark")}${tr("Analizar {n} pendientes", { n: pendingTests })}</button>` : ""}
				<button class="cf-btn" data-ai-rerun="all" ${busy ? "disabled" : ""} data-tip="${esc(tr("Vuelve a diagnosticar todos los fallos y el resumen de la ejecución. Usa cuota del proveedor de IA."))}">${icon("i-spark")}${tr("Volver a analizar todo")}</button>
			</div>` : "";
		const status = `<span class="ai-status ${aiOn ? "on" : "off"}"><span class="dot"></span>${aiOn
			? `${tr("IA activa")}: <span class="mono">${esc(S.config.ai_model)}</span>`
			: `${tr("IA desactivada")} · <a href="#" data-view-link="settings" data-set-tab="ai">${tr("Configurar")}</a>`}</span>`;

		let body;
		if (!failed.length) {
			body = `<div class="card m-empty">${icon("i-pass")}<h5>${r.status === "RUNNING" ? tr("La ejecución sigue en curso y todavía no hay fallos.") : tr("Sin fallos en esta ejecución.")}</h5>
				<p>${tr("Cuando un test falle, aquí verás su causa probable, si se repite en ejecuciones anteriores y qué hacer.")}</p></div>`;
		} else {
			const diag = !sum ? `<p class="ai-none">${tr("El diagnóstico de la ejecución se genera al terminarla.")}</p>`
				: sum.state === "PENDING" ? `<div class="shimmer"></div><div class="shimmer short"></div>`
				: sum.state === "ERROR" ? `<p class="ai-err">${icon("i-fail")}<span>${esc(sum.error || "")}</span></p>`
				: `<p class="ai-headline">${esc(sum.headline)}</p>${sum.summary ? `<p>${esc(sum.summary)}</p>` : ""}
					${isLocal(sum) ? `<p class="m-hint">${tr("Diagnóstico del motor local de reglas: cada conclusión sale de una regla determinística sobre la evidencia observada, sin IA y sin red.")}</p>`
						: !sum.ai ? `<p class="m-hint">${tr("Agrupado sin IA: cada incidente junta los fallos con la misma llamada de backend fallida justo antes del fallo, con la misma firma de error, o en cascada: el primer fallo de un archivo y los siguientes que apuntan a él.")}</p>` : ""}
					${summaryCoverage(sum)}`;
			const incidents = (sum?.incidents || []).map((inc) => `<article class="card incident-card">
				<header><span class="inc-kind ${esc(inc.kind)}">${esc({ backend: tr("Backend"), ai: tr("Causa IA"), error: tr("Error"), cascade: tr("En cascada") }[inc.kind] || inc.kind)}</span>
					<h6 data-no-i18n>${esc(AI_LABEL[inc.title] ? tr(AI_LABEL[inc.title]) : inc.title)}</h6>
					<span class="inc-count">${inc.test_ids.length === 1 ? tr("1 test") : tr("{n} tests", { n: inc.test_ids.length })}</span>${recurrencePill(inc.key)}${incidentVerified(inc)}</header>
				${incidentEvidence(inc)}
				${incidentCause(inc, "inc-cause-p", isLocal(sum))}
				${inc.action ? `<p><b>${tr("Qué hacer")}:</b> ${esc(inc.action)}</p>` : ""}
				<ul class="inc-tests">${inc.test_ids.map((id, i) => `<li><a href="#run=${r.id}&view=tests&test=${id}" data-no-i18n>${esc(inc.test_names[i] || "#" + id)}</a></li>`).join("")}</ul>
				${canAct() || incidentLab(inc) ? `<div class="inc-actions">${incidentLab(inc)}${canAct() ? `<button class="cf-btn cf-btn-sm" data-escalate="${inc.test_ids.length === 1 ? inc.test_ids[0] : 0}">${icon("i-megaphone")}${tr("Escalar")}</button>` : ""}</div>` : ""}
			</article>`).join("");
			body = `<div class="ai-top">
				<section class="card m-card ai-diag"><div class="m-card-head"><h5>${icon("i-spark")}${tr("Diagnóstico de la ejecución")}</h5></div>${diag}</section>
				<section class="card m-card"><div class="m-card-head"><h5>${tr("Causas de los fallos")}</h5><span class="m-hint">${tr("{n} fallos", { n: failed.length })}</span></div>
					<ul class="hbars">${Object.entries(counts).sort((a, b) => b[1] - a[1]).map(([c, n]) => `<li><span class="hb-label">${esc(c ? tr(AI_LABEL[c] || c) : tr("Sin diagnóstico de IA"))}</span>
						<span class="hb-track"><i style="width:${((n / maxC) * 100).toFixed(1)}%" class="${c ? "" : "muted"}"></i></span><span class="hb-value">${n}</span></li>`).join("")}</ul></section>
			</div>
			${incidents ? `<h5 class="ai-sec">${tr("Incidentes")} <span class="m-hint">${tr("fallos agrupados por la misma evidencia")}</span></h5><div class="incident-grid">${incidents}</div>` : ""}
			<h5 class="ai-sec">${tr("Fallos")} <span class="m-hint">${tr("diagnóstico de cada test")}</span></h5>
			<div class="ai-fails">${failed.map((t) => `<article class="card ai-fail">
				<header><a href="#run=${r.id}&view=tests&test=${t.id}" class="ai-fail-name" data-no-i18n>${esc(t.name)}</a>
					${t.triage?.state === "DONE" ? `<span class="ai-cat ${esc(t.triage.category)}">${esc(t.triage.category)}</span>` : ""}
					${retryChip(t, true)}${flakyChip(t, true)}</header>
				${t.error_message ? `<p class="ai-errmsg" data-no-i18n>${esc(t.error_message.split("\n")[0])}</p>` : ""}
				${triageBlock(t)}
				<div class="inc-actions">
					<a class="cf-btn cf-btn-sm" href="#run=${r.id}&view=tests&test=${t.id}">${icon("i-tests")}${tr("Ver test")}</a>
					${canAct() && aiOn ? `<button class="cf-btn cf-btn-sm" data-ai-retest="${t.id}" ${t.triage?.state === "PENDING" ? "disabled" : ""}>${icon("i-spark")}${tr("Re-analizar")}</button>` : ""}
					${canAct() ? `<button class="cf-btn cf-btn-sm" data-escalate="${t.id}">${icon("i-megaphone")}${tr("Escalar")}</button>` : ""}
				</div></article>`).join("")}</div>`;
		}
		setHTML(el, `<div class="page-head"><div><h4 class="page-title">${tr("Análisis con IA")}</h4>
				<p class="page-sub">${tr("Por qué falló esta ejecución, si los problemas se repiten y qué hacer con cada uno.")}</p></div>${status}</div>
			${actions}${S.aiv.msg ? `<p class="set-msg ${S.aiv.msg.ok ? "ok" : "err"}" role="status">${icon(S.aiv.msg.ok ? "i-pass" : "i-fail")}<span>${esc(S.aiv.msg.text)}</span></p>` : ""}
			${body}`);
	}

	async function aiRerun(kind, testId) {
		S.aiv.busy = true; S.aiv.msg = null; renderAI();
		try {
			const r = testId ? await apiSend("POST", `/api/v1/ui/tests/${testId}/analyze`, {})
				: await apiSend("POST", `/api/v1/ui/runs/${S.run.id}/analyze`, { all: kind === "all" });
			S.aiv.msg = { ok: true, text: tr("Analizando {n} fallo(s): los resultados aparecen aquí al terminar.", { n: r.queued }) };
			await loadRun();
		} catch (err) {
			S.aiv.msg = { ok: false, text: err.message };
		}
		S.aiv.busy = false;
		renderAI();
	}

	// ---------- escalar: resumen de un fallo para Negocio / QA / Desarrollo ----------
	const AUDIENCES = [
		{ id: "business", name: "Negocio", icon: "i-chart", short: "Impacto y riesgo, sin tecnicismos", desc: "Sin tecnicismos: qué no pueden hacer los usuarios, el riesgo y qué se está haciendo." },
		{ id: "qa", name: "QA", icon: "i-tests", short: "Dónde falló y cómo reproducirlo", desc: "Qué cubría el test, dónde falló, cómo reproducirlo y si bloquea la regresión." },
		{ id: "dev", name: "Desarrollo", icon: "i-bug", short: "cURL, stack trace y cómo reproducirlo", desc: "Técnico: cada llamada fallida con su cURL, la respuesta, el stack trace, la consola del navegador, el commit y el comando para correr el test en local." },
	];
	// textos de la tarjeta: van en el idioma del escalamiento, no en el de la interfaz
	const ESC_L = {
		es: { critical: "Severidad crítica", high: "Severidad alta", medium: "Severidad media", low: "Severidad baja",
			business: "Negocio", qa: "QA", dev: "Desarrollo", what: "Qué pasó", impact: "Impacto", cause: "Causa probable",
			evidence: "Evidencia", next: "Próximos pasos", owner: "Responsable sugerido", calls: "Llamadas al backend que fallaron",
			shot: "Captura del momento del fallo", run: "Ejecución", env: "Ambiente", result: "Resultado", failed: "{f} de {t} tests fallaron",
			report: "Ver reporte completo", by_ai: "Resumen generado con IA ({m})", by_tpl: "Resumen generado a partir de la evidencia del reporte",
			flaky: "Test inestable: falló {x} de sus últimas ejecuciones",
			tech: "Detalle técnico", branch: "Rama", test: "Test", attempts: "Intentos", stack: "Error y stack trace", repro: "Reproducir en local",
			response: "Respuesta", console: "Consola del navegador", artifacts: "Trace y video", logs: "Ver logs", trace: "Ver traza", copy: "Copiar",
			baseline: "La última vez que el test pasó (ejecución #{r}) respondió HTTP {s}", other_ctx: " · en otra rama o ambiente",
			more: "… {n} líneas más en el reporte", clipped: "Respuesta recortada: completa en el reporte",
			qa_verdict: "Veredicto de QA", qa_clean: "Pasó limpio", qa_observations: "Pasó con observaciones", qa_risks: "Pasó con riesgos",
			qa_clean_desc: "Sin señales en red, consola, pasos, reintentos ni historial.", qa_risk: "Riesgo", qa_note: "Observación",
			doc: "Reporte de escalamiento", ref: "N.º", generated: "Generado", severity: "Severidad", audience: "Audiencia",
			sev_critical: "Crítica", sev_high: "Alta", sev_medium: "Media", sev_low: "Baja", run_date: "Fecha de ejecución",
			summary: "Resumen ejecutivo", where: "Dónde falló", step: "Paso que falló", last_ok: "Último paso correcto", error: "Error", selector: "Selector no encontrado",
			foot: "Generado con TraceReports · {d} · Uso interno",
			blank_title: "La página estaba en blanco al fallar",
			blank_desc: "El navegador no llegó a mostrar contenido: todas las capturas del test están en blanco.", blank_view: "Ver la captura" },
		en: { critical: "Critical severity", high: "High severity", medium: "Medium severity", low: "Low severity",
			business: "Business", qa: "QA", dev: "Development", what: "What happened", impact: "Impact", cause: "Likely cause",
			evidence: "Evidence", next: "Next steps", owner: "Suggested owner", calls: "Backend calls that failed",
			shot: "Screenshot at the moment of failure", run: "Run", env: "Environment", result: "Result", failed: "{f} of {t} tests failed",
			report: "View full report", by_ai: "Summary written with AI ({m})", by_tpl: "Summary built from the report's evidence",
			flaky: "Flaky test: it failed {x} of its recent runs",
			tech: "Technical detail", branch: "Branch", test: "Test", attempts: "Attempts", stack: "Error and stack trace", repro: "Run it locally",
			response: "Response", console: "Browser console", artifacts: "Trace and video", logs: "View logs", trace: "View trace", copy: "Copy",
			baseline: "The last time the test passed (run #{r}) it answered HTTP {s}", other_ctx: " · on another branch or environment",
			more: "… {n} more lines in the report", clipped: "Response cut: the full one is in the report",
			qa_verdict: "QA verdict", qa_clean: "Passed clean", qa_observations: "Passed with observations", qa_risks: "Passed with risks",
			qa_clean_desc: "No signals in network, console, steps, retries or history.", qa_risk: "Risk", qa_note: "Observation",
			doc: "Escalation report", ref: "No.", generated: "Generated", severity: "Severity", audience: "Audience",
			sev_critical: "Critical", sev_high: "High", sev_medium: "Medium", sev_low: "Low", run_date: "Run date",
			summary: "Executive summary", where: "Where it failed", step: "Step that failed", last_ok: "Last step that passed", error: "Error", selector: "Selector not found",
			foot: "Generated with TraceReports · {d} · Internal use",
			blank_title: "The page was blank at the failure",
			blank_desc: "The browser never showed any content: every screenshot of the test is blank.", blank_view: "View the screenshot" },
	};

	/** Número de reporte estable: el mismo fallo (ejecución y test) siempre lleva el mismo. */
	const escRef = (e) => `ESC-${String(e.run_id).padStart(4, "0")}${e.test_id ? `-${e.test_id}` : ""}`;

	/** La captura del fallo, o el aviso de página en blanco (un recuadro blanco parece una imagen que no cargó). */
	function escShotHTML(e, L) {
		const f = e.facts;
		if (!f.screenshot) return "";
		if (f.shot_blank) {
			return `<div class="esc-shot-blank" role="note">${icon("i-warning")}<div><b>${esc(L.blank_title)}</b>
				<p>${esc(L.blank_desc)} <a href="${esc(f.screenshot)}" target="_blank" rel="noopener">${esc(L.blank_view)} ↗</a></p></div></div>`;
		}
		return `<figure class="esc-shot"><img src="${esc(f.screenshot)}" alt="${esc(L.shot)}"><figcaption>${esc(L.shot)}${f.shot_caption ? ` · ${esc(f.shot_caption)}` : ""}</figcaption></figure>`;
	}

	/** El veredicto de QA de una ejecución sin fallos (lo calcula el servidor): cómo pasó y sus señales. */
	function escVerdictHTML(e, L) {
		const a = e.facts.assessment;
		if (!a) return "";
		const signals = (a.signals || []).map((s) => `<li class="esc-signal ${esc(s.level)}"><span class="esc-signal-lvl">${esc(s.level === "risk" ? L.qa_risk : L.qa_note)}</span>${esc(s.text)}</li>`).join("");
		return `<section class="esc-verdict ${esc(a.verdict)}"><h3>${esc(L.qa_verdict)}</h3>
			<p class="esc-verdict-badge">${esc(L[`qa_${a.verdict}`] || a.verdict)}</p>
			${signals ? `<ul>${signals}</ul>` : `<p>${esc(L.qa_clean_desc)}</p>`}</section>`;
	}
	const escFmt = (s, v) => s.replace(/\{(\w+)\}/g, (m, k) => (k in v ? v[k] : m));
	/** Qué se escala (ejecución, test, público, idioma, con o sin IA): otro alcance es otro resumen. */
	const escKey = () => `${S.run?.id}:${S.esc.test}:${S.esc.audience}:${S.esc.lang}:${escNoAI() ? "tpl" : "ai"}`;
	/** Sin IA: elegido por la persona, o porque no hay IA configurada. */
	const escNoAI = () => !S.config.ai_enabled || !!S.esc.noAI;

	// Cambió la evidencia de lo que se escala. Con la ejecución terminada, el resumen en memoria y las respuestas
	// en vuelo son de la evidencia anterior: se retiran (no genera IA: el caché del servidor valida su huella;
	// regenerar sigue siendo explícito). Con la ejecución todavía corriendo la evidencia llega sin parar: lo que se
	// ve, o se está armando, NO se borra ni se cancela (eso hacía parpadear la vista y obligaba a pedirlo dos veces);
	// queda marcado "llegó evidencia nueva", con Regenerar.
	function invalidateEscalation() {
		S.esc.evidence++;
		if (S.run?.status === "RUNNING" && (S.esc.data || S.esc.loading)) { S.esc.stale = true; return; }
		S.esc.loadedKey = null; S.esc.data = null; S.esc.msg = null; S.esc.loading = false; S.esc.stale = false;
		turn("escalate"); turn("escalate-cache");
	}
	/** Lo de una ejecución que importa a lo que se escala: con un test elegido, ese test (y el análisis general). */
	const escEvidence = (run) => JSON.stringify(S.esc.test ? [run.tests?.find((t) => t.id === S.esc.test) ?? null, run.summary ?? null] : run);
	/** ¿Este evento en vivo cambia lo que se está escalando? Un test elegido: solo lo suyo (y el análisis de la ejecución). */
	const escTouchedBy = (type, e) => !S.esc.test || e.test_id === S.esc.test || type === "triage" || type === "summary";

	function escReportURL(e) {
		return STATIC ? `${location.href.split("#")[0]}${e.facts.report_path}` : `${location.origin}/${e.facts.report_path}`;
	}

	/** Reporte exportado: el resumen ya armado al exportar (con IA si estaba guardado; si no, la plantilla). */
	function staticEscalation() {
		const base = `${S.esc.test}:${S.esc.audience}:${S.esc.lang}`, all = STATIC.escalations || {};
		return (!escNoAI() && all[`${base}:ai`]) || all[`${base}:tpl`] || null;
	}

	async function loadCachedEscalation() {
		const key = escKey();
		if (S.esc.loadedKey === key) return;
		const current = turn("escalate-cache");
		S.esc.loadedKey = key; S.esc.data = null; S.esc.msg = null; S.esc.stale = false;
		if (escNoAI()) { if (S.view === "escalate") renderEscalate(); return; } // la plantilla no se guarda
		try {
			const res = await fetch(`/api/v1/runs/${S.run.id}/escalation?test=${S.esc.test}&audience=${S.esc.audience}&lang=${S.esc.lang}`);
			if (res.status === 200) {
				const data = await res.json();
				if (current() && escKey() === key) S.esc.data = data;
			}
		} catch { /* sin caché */ }
		if (S.view === "escalate") renderEscalate();
	}

	async function generateEscalation(regenerate) {
		const key = escKey(), current = turn("escalate"), evidence = S.esc.evidence;
		turn("escalate-cache"); // una lectura anterior no pisa la generación solicitada
		S.esc.loadedKey = key;
		S.esc.loading = true; S.esc.msg = null; renderEscalate();
		try {
			const data = await apiSend("POST", "/api/v1/ui/escalate", { run_id: S.run.id, test_id: S.esc.test, audience: S.esc.audience, lang: S.esc.lang, regenerate: !!regenerate, no_ai: escNoAI() });
			// otro test, público o idioma elegido mientras tanto: el resultado y su aviso son de otro escalado
			if (current() && escKey() === key) {
				S.esc.data = data;
				S.esc.stale = S.esc.evidence !== evidence; // llegó evidencia mientras se armaba
				if (data.ai_error) S.esc.msg = { ok: false, text: tr("La IA no respondió, así que se armó con la plantilla. Detalle: {e}", { e: data.ai_error }) };
				if (data.ai_skipped === "no_failures") S.esc.msg = { ok: true, text: tr("Esta ejecución todavía no terminó completa: el resumen se armó con los datos del reporte, sin IA, para que no invente un problema que no existe.") };
				if (data.ai_skipped === "invented_failure") S.esc.msg = { ok: true, text: tr("La IA describió un fallo que esta ejecución no tiene, así que se descartó su texto y el resumen se armó con los datos del reporte.") };
			}
		} catch (err) {
			if (current() && escKey() === key) S.esc.msg = { ok: false, text: err.message };
		}
		if (!current()) return; // hay otra generación más nueva en curso
		S.esc.loading = false;
		renderEscalate();
	}

	// el logo del sitio en línea: la tarjeta se exporta a PNG y ahí no llegan los <use> del sprite
	const LOGO_SVG = `<svg class="esc-logo" viewBox="0 0 32 32" shape-rendering="crispEdges" aria-hidden="true"><rect width="32" height="32" fill="#c9fa6b"/><path fill="#18200d" d="M6 6h20v5h-7v15h-6V11H6z"/><path fill="#6c9736" d="M22 22h5v5h-5z"/></svg>`;

	/** Primeras n líneas de un texto largo (la tarjeta también se exporta como imagen). */
	function escLines(s, n, L) {
		const lines = String(s || "").split("\n");
		return lines.length <= n ? lines.join("\n") : `${lines.slice(0, n).join("\n")}\n${escFmt(L.more, { n: lines.length - n })}`;
	}
	const escAbs = (u) => new URL(u, location.href).href;
	/** Contexto de la ejecución y del test: dónde y con qué corrió. */
	function escDevContext(d, L) {
		return [["Framework", d.framework], ["Commit", d.commit], [L.branch, d.branch], [L.test, d.test_key], ["Suite", d.suite],
			["Params", d.params], ["Worker", d.worker], [L.attempts, d.attempts > 1 ? d.attempts : ""]].filter(([, v]) => v);
	}
	function escBaseline(c, L) {
		const b = c.baseline;
		return b ? escFmt(L.baseline, { r: b.run_id, s: b.status }) + (b.duration_ms ? ` · ${b.duration_ms} ms` : "") + (b.same_context ? "" : L.other_ctx) : "";
	}

	/** Detalle técnico para Desarrollo: contexto, stack, cómo reproducirlo, cada llamada con su cURL, consola y trace. */
	function escDevHTML(d, L, named) {
		const copy = (text) => `<button class="esc-copy" data-esc-copy="${esc(text)}">${esc(L.copy)}</button>`;
		const block = (text, lines = 24) => `<div class="esc-code">${copy(text)}<pre>${esc(escLines(text, lines, L))}</pre></div>`;
		const ctx = escDevContext(d, L);
		const calls = (d.calls || []).map((c) => `<div class="esc-call">
				<div class="esc-call-head"><b>${esc(c.method)}</b> <span class="esc-path">${esc(c.url)}</span>
					<span class="esc-st">${esc(c.outcome)}</span>${c.duration_ms ? `<span class="esc-ms">${c.duration_ms} ms</span>` : ""}</div>
				${c.trace_id || c.request_id ? `<p class="esc-ids">${c.trace_id ? `Trace ID <code>${esc(c.trace_id)}</code>` : ""}${c.request_id ? ` Request ID <code>${esc(c.request_id)}</code>` : ""}
					${c.logs_url ? ` <a href="${esc(c.logs_url)}" target="_blank" rel="noopener">${esc(L.logs)} ↗</a>` : ""}${c.trace_url ? ` <a href="${esc(c.trace_url)}" target="_blank" rel="noopener">${esc(L.trace)} ↗</a>` : ""}</p>` : ""}
				${c.baseline ? `<p class="esc-base">${esc(escBaseline(c, L))}</p>` : ""}
				<h4>cURL</h4>${block(c.curl, 40)}
				${c.response_body ? `<h4>${esc(L.response)}</h4>${block(c.response_body, 30)}${c.body_clipped ? `<p class="esc-note">${esc(L.clipped)}</p>` : ""}` : ""}
			</div>`).join("");
		const consoleText = (d.console || []).map((m) => `[${m.level}] ${m.text}${m.location ? `  (${m.location})` : ""}`).join("\n");
		return `<div class="esc-dev-test">${named ? `<h4 class="esc-dev-name">${esc(d.test_name)}</h4>` : ""}
			${ctx.length ? `<dl class="esc-ctx">${ctx.map(([k, v]) => `<div><dt>${esc(k)}</dt><dd>${esc(v)}</dd></div>`).join("")}</dl>` : ""}
			${d.error || d.error_trace ? `<h4>${esc(L.stack)}</h4>${block([d.error, d.error_trace].filter(Boolean).join("\n\n"))}` : ""}
			${d.repro?.length ? `<h4>${esc(L.repro)}</h4>${block(CF.reproScript(d.repro, d.commit, L === ESC_L.en ? "en" : "es"))}` : ""}
			${calls ? `<h4>${esc(L.calls)}</h4>${calls}` : ""}
			${consoleText ? `<h4>${esc(L.console)}</h4>${block(consoleText, 12)}` : ""}
			${d.artifacts?.length ? `<h4>${esc(L.artifacts)}</h4><ul>${d.artifacts.map((a) => `<li><a href="${esc(a.url)}" target="_blank" rel="noopener">${esc(a.kind)}: ${esc(a.name)}</a></li>`).join("")}</ul>` : ""}
		</div>`;
	}

	/** El mismo detalle como texto: bloques de código para Markdown/Slack, sangría en texto simple. */
	function escDevText(d, L, style, B, named) {
		const fence = (text, lang = "") => (style === "plain" ? String(text).split("\n").map((l) => `    ${l}`).join("\n") : `\`\`\`${style === "md" ? lang : ""}\n${text}\n\`\`\``);
		const out = named ? ["", B(`${L.test}: ${d.test_name}`)] : [];
		const ctx = escDevContext(d, L);
		if (ctx.length) out.push(ctx.map(([k, v]) => `${k}: ${v}`).join(" · "));
		if (d.error || d.error_trace) out.push("", B(L.stack + ":"), fence([d.error, d.error_trace].filter(Boolean).join("\n\n")));
		if (d.repro?.length) out.push("", B(L.repro + ":"), fence(CF.reproScript(d.repro, d.commit, L === ESC_L.en ? "en" : "es"), "bash"));
		for (const c of d.calls || []) {
			out.push("", B(`${c.method} ${c.url} → ${c.outcome}${c.duration_ms ? ` (${c.duration_ms} ms)` : ""}`));
			const ids = [c.trace_id && `Trace ID ${c.trace_id}`, c.request_id && `Request ID ${c.request_id}`, c.logs_url && `${L.logs}: ${c.logs_url}`, c.trace_url && `${L.trace}: ${c.trace_url}`].filter(Boolean);
			if (ids.length) out.push(ids.join(" · "));
			if (c.baseline) out.push(escBaseline(c, L));
			out.push(fence(c.curl, "bash"));
			if (c.response_body) out.push(`${L.response}:`, fence(c.response_body, /json/i.test(c.mime_type || "") ? "json" : ""));
		}
		if (d.console?.length) out.push("", B(L.console + ":"), fence(d.console.map((m) => `[${m.level}] ${m.text}${m.location ? `  (${m.location})` : ""}`).join("\n")));
		if (d.artifacts?.length) { out.push("", B(L.artifacts + ":")); d.artifacts.forEach((a) => out.push(`• ${a.kind}: ${escAbs(a.url)}`)); }
		return out;
	}

	/**
	 * El resumen como documento formal: encabezado con número de reporte y fecha, ficha del caso,
	 * resumen ejecutivo y secciones numeradas. El color va solo en la severidad: el resto es sobrio
	 * para un correo a gerencia, un ticket o un PDF.
	 */
	/** «Dónde falló» (audiencia QA): el test, el paso, el error y el selector, de los datos del reporte. */
	function escWhere(e) {
		const w = e.facts?.where, L = ESC_L[e.lang] || ESC_L.es;
		if (e.audience !== "qa" || !w) return null;
		const rows = [[L.test, w.test, true], [L.step, w.step], [L.last_ok, w.last_ok], [L.error, w.error, true], [L.selector, w.selector, true]].filter(([, v]) => v);
		return rows.length > 1 ? rows : null;
	}
	function escCard(e) {
		const L = ESC_L[e.lang] || ESC_L.es, f = e.facts;
		const showCalls = e.audience !== "business" && f.network?.length && !f.dev?.some((d) => d.calls?.length);
		const generated = fmtDateTime(e.created_at || Date.now());
		const facts = [
			[L.severity, `<span class="esc-sev">${esc(L[`sev_${e.severity}`] || e.severity)}</span>`],
			[L.audience, `<span class="esc-aud">${esc(L[e.audience])}</span>`],
			[L.run, esc(f.run_name)], [L.env, esc(f.env || "—")],
			[L.result, esc(escFmt(L.failed, { f: f.failed, t: f.total }))], [L.run_date, esc(fmtDateTime(f.started_at))],
			...(f.test_name ? [[L.test, `<span class="esc-mono">${esc(f.test_name)}</span>`]] : []),
			...(e.owner ? [[L.owner, `<b>${esc(e.owner)}</b>`]] : []),
		];
		// secciones numeradas, en el orden en que se leen
		const where = escWhere(e);
		const sections = [
			[L.what, `<p>${esc(e.what_happened)}</p>`],
			...(where ? [[L.where, `<dl class="esc-where">${where.map(([k, v, mono]) => `<dt>${esc(k)}</dt><dd${mono ? ' class="esc-mono"' : ""}>${esc(v)}</dd>`).join("")}</dl>`]] : []),
			[L.impact, `<p>${esc(e.impact)}</p>`], [L.cause, `<p>${esc(e.root_cause)}</p>`],
			...(showCalls ? [[L.calls, `<table class="esc-calls">${f.network.map((n) => `<tr><td><b>${esc(n.method)}</b></td><td class="esc-path">${esc(n.path)}</td>
				<td class="esc-st">${esc(n.outcome)}</td><td class="esc-ms">${n.duration_ms ? `${n.duration_ms} ms` : ""}</td></tr>`).join("")}</table>`]] : []),
			...(e.evidence?.length || f.flaky ? [[L.evidence, `${e.evidence?.length ? `<ul>${e.evidence.map((x) => `<li>${esc(x)}</li>`).join("")}</ul>` : ""}
				${f.flaky ? `<p class="esc-flaky">${esc(escFmt(L.flaky, { x: f.flaky }))}</p>` : ""}`]] : []),
			...(e.next_steps?.length ? [[L.next, `<ol>${e.next_steps.map((x) => `<li>${esc(x)}</li>`).join("")}</ol>`]] : []),
		];
		return `<article class="esc-card esc-doc sev-${esc(e.severity)}" id="esc-card" lang="${esc(e.lang)}" data-no-i18n data-ref="${esc(escRef(e))}">
			<header class="esc-head">
				<div class="esc-doc-brand"><span class="esc-brand">${LOGO_SVG}<span>tracereports<span class="esc-cursor">_</span></span></span>
					<span class="esc-doc-kind">${esc(L.doc)}</span></div>
				<div class="esc-doc-ref"><b>${esc(L.ref)} ${esc(escRef(e))}</b><span>${esc(L.generated)}: ${esc(generated)}</span></div>
			</header>
			<h2 class="esc-title">${esc(e.title)}</h2>
			<table class="esc-facts">${facts.reduce((rows, x, i) => (i % 2 ? rows[rows.length - 1].push(x) : rows.push([x]), rows), [])
				.map((row) => `<tr>${row.map(([k, v]) => `<th scope="row">${esc(k)}</th><td${row.length === 1 ? ' colspan="3"' : ""}>${v}</td>`).join("")}</tr>`).join("")}</table>
			<section class="esc-summary"><h3>${esc(L.summary)}</h3><p class="esc-headline">${esc(e.headline)}</p></section>
			${escVerdictHTML(e, L)}
			${escShotHTML(e, L)}
			${sections.map(([title, body], i) => `<section class="esc-sec"><h3><span class="esc-num">${i + 1}.</span> ${esc(title)}</h3>${body}</section>`).join("")}
			${f.dev?.length ? `<section class="esc-dev"><h3>${esc(L.tech)}</h3>${f.dev.map((d) => escDevHTML(d, L, !f.test_name || !e.test_id)).join("")}</section>` : ""}
			<footer class="esc-foot"><div><span>${esc(escFmt(L.foot, { d: generated }))}</span>
				<span class="esc-by">${esc(e.source === "ai" ? escFmt(L.by_ai, { m: e.model || "" }) : L.by_tpl)}</span></div>
				<a href="${esc(escReportURL(e))}" target="_blank" rel="noopener">${esc(L.report)} ↗</a></footer>
		</article>`;
	}

	// motivo de una llamada fallida al proveedor de IA (ai.FailureReason)
	const AI_FAIL = { rate_limit: "límite de solicitudes", server: "falla o saturación del proveedor", auth: "credencial rechazada",
		timeout: "sin respuesta a tiempo", network: "sin conexión con el proveedor", other: "error del proveedor" };

	/** Duración con décimas (1,5 s): para ver de dónde viene una demora. */
	const fmtDur = (ms) => (ms < 1000 ? `${fmtNum(ms)} ms` : `${fmtNum(ms / 1000, 1)} s`);

	/** Cuánto tardó la IA en escribir el resumen, separando al proveedor de TraceReports. Va debajo
	 * de la tarjeta (no se comparte: ni en el texto, ni en el correo, ni en la imagen). */
	function escTimingHTML(e) {
		const t = e.timing;
		if (!t) return "";
		const parts = [t.calls === 1
			? tr("{p} esperando al proveedor de IA (1 llamada)", { p: fmtDur(t.provider_ms) })
			: tr("{p} esperando al proveedor de IA ({n} llamadas)", { p: fmtDur(t.provider_ms), n: t.calls })];
		if (t.wait_ms) parts.push(tr("{w} en pausas entre reintentos", { w: fmtDur(t.wait_ms) }));
		parts.push(tr("{o} de TraceReports", { o: fmtDur(t.own_ms) }));
		const service = t.provider_ms + t.wait_ms;
		const verdict = !t.total_ms ? ""
			: service >= t.total_ms * 0.8 ? tr("Casi todo el tiempo fue del proveedor de IA.")
			: t.own_ms > service && t.own_ms > 2000 ? tr("La mayor parte del tiempo fue de TraceReports: avísanos si se repite.")
			: "";
		// detalle de cada llamada cuando hubo más de una o alguna falló
		const att = t.attempts || [];
		const detail = att.length > 1 || att.some((x) => !x.ok) ? att.map((x, i) => {
			const what = x.ok ? tr("respondió en {t}", { t: fmtDur(x.ms) })
				: tr("{e} en {t}", { e: `${x.status ? `HTTP ${x.status} · ` : ""}${tr(AI_FAIL[x.reason] || AI_FAIL.other)}`, t: fmtDur(x.ms) });
			const wait = x.wait_ms ? ` → ${x.wait_asked ? tr("espera {w} (la pidió el proveedor)", { w: fmtDur(x.wait_ms) }) : tr("espera {w}", { w: fmtDur(x.wait_ms) })}` : "";
			return `${tr("Llamada {n}", { n: i + 1 })}: ${what}${wait}`;
		}).join(" · ") : "";
		return `<p class="esc-timing" role="note">${icon("i-timeline")}<span><b>${esc(tr("Tardó {t} en generarse", { t: fmtDur(t.total_ms) }))}</b>: ${esc(parts.join(" · "))}.${verdict ? ` ${esc(verdict)}` : ""}${detail ? `<br><small>${esc(detail)}.</small>` : ""}</span></p>`;
	}

	// ---- formatos para compartir ----
	function escText(e, style) {
		const L = ESC_L[e.lang] || ESC_L.es, f = e.facts;
		const B = (s) => (style === "slack" ? `*${s}*` : style === "md" ? `**${s}**` : s);
		const lines = [`${L.doc} ${escRef(e)}`, `${style === "plain" ? `[${(L[e.severity] || e.severity).toUpperCase()}]` : `${B(L[e.severity] || e.severity)} ·`} ${B(e.title)}`, e.headline, ""];
		const qa = f.assessment;
		if (qa) {
			lines.push(`${B(L.qa_verdict + ":")} ${L[`qa_${qa.verdict}`] || qa.verdict}`);
			(qa.signals || []).forEach((s) => lines.push(`• [${s.level === "risk" ? L.qa_risk : L.qa_note}] ${s.text}`));
			lines.push("");
		}
		if (e.what_happened) lines.push(`${B(L.what + ":")} ${e.what_happened}`);
		const where = escWhere(e);
		if (where) { lines.push("", B(L.where + ":")); where.forEach(([k, v]) => lines.push(`• ${k}: ${v}`)); lines.push(""); }
		[[L.impact, e.impact], [L.cause, e.root_cause]].forEach(([k, v]) => v && lines.push(`${B(k + ":")} ${v}`));
		if (e.audience !== "business" && f.network?.length && !f.dev?.some((d) => d.calls?.length)) {
			lines.push("", B(L.calls + ":"));
			f.network.forEach((n) => lines.push(`• ${n.method} ${n.path} → ${n.outcome}${n.duration_ms ? ` (${n.duration_ms} ms)` : ""}`));
		}
		if (e.evidence?.length) { lines.push("", B(L.evidence + ":")); e.evidence.forEach((x) => lines.push(`• ${x}`)); }
		if (e.next_steps?.length) { lines.push("", B(L.next + ":")); e.next_steps.forEach((x, i) => lines.push(`${i + 1}. ${x}`)); }
		if (e.owner) lines.push("", `${B(L.owner + ":")} ${e.owner}`);
		if (f.dev?.length) { lines.push("", B(L.tech + ":")); f.dev.forEach((d) => lines.push(...escDevText(d, L, style, B, !f.test_name || !e.test_id))); }
		lines.push("", `${L.run}: ${f.run_name} · ${f.env || "—"} · ${fmtDateTime(f.started_at)} · ${escFmt(L.failed, { f: f.failed, t: f.total })}`);
		lines.push(`${L.report}: ${escReportURL(e)}`);
		lines.push(escFmt(L.foot, { d: fmtDateTime(e.created_at || Date.now()) }));
		return lines.join("\n");
	}

	/** HTML con estilos en línea para pegar en un correo (Outlook, Gmail) con formato. */
	async function escEmailHTML(e) {
		const L = ESC_L[e.lang] || ESC_L.es, f = e.facts;
		const color = { critical: "#c62828", high: "#e65100", medium: "#b26a00", low: "#2e7d32" }[e.severity] || "#455a64";
		const h3 = (t) => `<h3 style="margin:18px 0 6px;font-size:14px;color:#263238">${esc(t)}</h3>`;
		const p = (t) => `<p style="margin:0;font-size:14px;line-height:1.5;color:#37474f">${esc(t)}</p>`;
		const shot = f.screenshot && !f.shot_blank ? await toDataURL(f.screenshot).catch(() => "") : "";
		const ink = "#1f3a5f", generated = fmtDateTime(e.created_at || Date.now());
		const row = (k, v) => `<td style="padding:6px 10px;border:1px solid #dfe5ea;background:#f3f6f9;color:#455a64;font-size:13px;font-weight:600;width:22%">${esc(k)}</td><td style="padding:6px 10px;border:1px solid #dfe5ea;color:#263238;font-size:13px">${v}</td>`;
		return `<div style="font-family:Segoe UI,Arial,sans-serif;max-width:680px;border:1px solid #dfe5ea;border-top:5px solid ${color};padding:20px 24px">
			<table style="width:100%;border-collapse:collapse;border-bottom:2px solid ${ink}"><tr>
				<td style="padding:0 0 10px;font-size:11px;font-weight:700;letter-spacing:.12em;text-transform:uppercase;color:${ink}">TraceReports · ${esc(L.doc)}</td>
				<td style="padding:0 0 10px;text-align:right;font-size:12px;color:#607d8b"><b style="color:${ink};font-family:Consolas,monospace">${esc(L.ref)} ${esc(escRef(e))}</b><br>${esc(L.generated)}: ${esc(generated)}</td></tr></table>
			<h2 style="margin:14px 0 10px;font-size:20px;color:${ink}">${esc(e.title)}</h2>
			<table style="width:100%;border-collapse:collapse">
				<tr>${row(L.severity, `<b style="color:#fff;background:${color};padding:1px 8px;border-radius:4px;font-size:12px">${esc(L[`sev_${e.severity}`] || e.severity)}</b>`)}${row(L.audience, esc(L[e.audience]))}</tr>
				<tr>${row(L.run, esc(f.run_name))}${row(L.env, esc(f.env || "—"))}</tr>
				<tr>${row(L.result, esc(escFmt(L.failed, { f: f.failed, t: f.total })))}${row(L.run_date, esc(fmtDateTime(f.started_at)))}</tr>
			</table>
			<div style="margin-top:14px;padding:10px 14px;background:#f7f9fb;border-left:4px solid ${color}"><div style="font-size:12px;font-weight:700;letter-spacing:.05em;text-transform:uppercase;color:${ink}">${esc(L.summary)}</div>${p(e.headline)}</div>
			${f.shot_blank ? `<p style="margin:14px 0 0;padding:10px 12px;border:1px dashed #b0bec5;font-size:13px;color:#455a64"><b>${esc(L.blank_title)}</b><br>${esc(L.blank_desc)}</p>` : ""}
			${f.assessment ? h3(`${L.qa_verdict}: ${L[`qa_${f.assessment.verdict}`] || f.assessment.verdict}`)
				+ (f.assessment.signals?.length ? `<ul style="margin:0;padding-left:20px;font-size:13px;color:#37474f">${f.assessment.signals.map((s) => `<li><b>${esc(s.level === "risk" ? L.qa_risk : L.qa_note)}:</b> ${esc(s.text)}</li>`).join("")}</ul>` : p(L.qa_clean_desc)) : ""}
			${shot ? `<img src="${shot}" alt="${esc(L.shot)}" style="margin-top:14px;max-width:100%;border:1px solid #e0e0e0;border-radius:6px">` : ""}
			${h3(L.what)}${p(e.what_happened)}${escWhere(e) ? h3(L.where) + `<ul style="margin:0;padding-left:20px;font-size:13px;color:#37474f">${escWhere(e).map(([k, v, mono]) => `<li><b>${esc(k)}:</b> ${mono ? `<code>${esc(v)}</code>` : esc(v)}</li>`).join("")}</ul>` : ""}${h3(L.impact)}${p(e.impact)}${h3(L.cause)}${p(e.root_cause)}
			${e.audience !== "business" && f.network?.length && !f.dev?.some((d) => d.calls?.length) ? h3(L.calls) + `<ul style="margin:0;padding-left:20px;font-size:13px;color:#37474f">${f.network.map((n) => `<li><code>${esc(n.method)} ${esc(n.path)}</code> → ${esc(n.outcome)}</li>`).join("")}</ul>` : ""}
			${e.evidence?.length ? h3(L.evidence) + `<ul style="margin:0;padding-left:20px;font-size:13px;color:#37474f">${e.evidence.map((x) => `<li>${esc(x)}</li>`).join("")}</ul>` : ""}
			${e.next_steps?.length ? h3(L.next) + `<ol style="margin:0;padding-left:20px;font-size:13px;color:#37474f">${e.next_steps.map((x) => `<li>${esc(x)}</li>`).join("")}</ol>` : ""}
			${e.owner ? `<p style="margin:16px 0 0;font-size:13px;color:#37474f">${esc(L.owner)}: <b>${esc(e.owner)}</b></p>` : ""}
			${f.dev?.length ? h3(L.tech) + f.dev.map((d) => escDevEmail(d, L, !f.test_name || !e.test_id)).join("") : ""}
			<p style="margin:16px 0 0"><a href="${esc(escReportURL(e))}" style="color:#1565c0">${esc(L.report)}</a></p>
			<p style="margin:14px 0 0;padding-top:8px;border-top:1px solid #dfe5ea;font-size:11.5px;color:#90a4ae">${esc(escFmt(L.foot, { d: generated }))}</p>
		</div>`;
	}

	/** Detalle técnico del correo: bloques monoespaciados con estilos en línea. */
	function escDevEmail(d, L, named) {
		const pre = (t) => `<pre style="margin:4px 0 8px;padding:10px 12px;background:#f5f7f9;border:1px solid #e3e7eb;border-radius:6px;font:12px/1.45 Consolas,'Courier New',monospace;color:#263238;white-space:pre-wrap;word-break:break-all">${esc(t)}</pre>`;
		const small = (t) => `<p style="margin:6px 0 2px;font-size:12.5px;color:#37474f">${t}</p>`;
		const ctx = escDevContext(d, L);
		let html = (named ? `<p style="margin:14px 0 2px;font-size:13.5px;font-weight:700;color:#263238">${esc(d.test_name)}</p>` : "") + (ctx.length ? small(ctx.map(([k, v]) => `${esc(k)}: <b>${esc(v)}</b>`).join(" · ")) : "");
		if (d.error || d.error_trace) html += small(`<b>${esc(L.stack)}</b>`) + pre([d.error, d.error_trace].filter(Boolean).join("\n\n"));
		if (d.repro?.length) html += small(`<b>${esc(L.repro)}</b>`) + pre(CF.reproScript(d.repro, d.commit, L === ESC_L.en ? "en" : "es"));
		for (const c of d.calls || []) {
			html += small(`<b>${esc(c.method)}</b> <code>${esc(c.url)}</code> → <b style="color:#c62828">${esc(c.outcome)}</b>${c.duration_ms ? ` · ${c.duration_ms} ms` : ""}`);
			if (c.trace_id || c.request_id) html += small([c.trace_id && `Trace ID <code>${esc(c.trace_id)}</code>`, c.request_id && `Request ID <code>${esc(c.request_id)}</code>`,
				c.logs_url && `<a href="${esc(c.logs_url)}">${esc(L.logs)}</a>`, c.trace_url && `<a href="${esc(c.trace_url)}">${esc(L.trace)}</a>`].filter(Boolean).join(" · "));
			if (c.baseline) html += small(esc(escBaseline(c, L)));
			html += pre(c.curl);
			if (c.response_body) html += small(esc(L.response)) + pre(c.response_body);
		}
		if (d.console?.length) html += small(`<b>${esc(L.console)}</b>`) + pre(d.console.map((m) => `[${m.level}] ${m.text}${m.location ? `  (${m.location})` : ""}`).join("\n"));
		if (d.artifacts?.length) html += small(`<b>${esc(L.artifacts)}</b>`) + d.artifacts.map((a) => small(`<a href="${esc(escAbs(a.url))}">${esc(a.kind)}: ${esc(a.name)}</a>`)).join("");
		return html;
	}

	/** El resumen con formato para pegar en un chat (Teams, Slack): negritas, listas y bloques de código
	 * como HTML, que es lo que esas apps respetan al pegar (el Markdown pegado queda con los ** a la vista). */
	function escChatHTML(e) {
		const L = ESC_L[e.lang] || ESC_L.es, f = e.facts;
		const p = (html) => `<p>${html}</p>`, b = (s) => `<b>${esc(s)}</b>`;
		const list = (tag, xs) => `<${tag}>${xs.map((x) => `<li>${esc(x)}</li>`).join("")}</${tag}>`;
		const out = [p(esc(`${L.doc} ${escRef(e)}`)), p(`${b((L[e.severity] || e.severity).toUpperCase())} · ${b(e.title)}`), p(esc(e.headline))];
		const qa = f.assessment;
		if (qa) {
			out.push(p(`${b(L.qa_verdict + ":")} ${esc(L[`qa_${qa.verdict}`] || qa.verdict)}`));
			if (qa.signals?.length) out.push(list("ul", qa.signals.map((s) => `[${s.level === "risk" ? L.qa_risk : L.qa_note}] ${s.text}`)));
		}
		[[L.what, e.what_happened], [L.impact, e.impact], [L.cause, e.root_cause]].forEach(([k, v]) => v && out.push(p(`${b(k + ":")} ${esc(v)}`)));
		if (e.audience !== "business" && f.network?.length && !f.dev?.some((d) => d.calls?.length)) {
			out.push(p(b(L.calls + ":")), list("ul", f.network.map((n) => `${n.method} ${n.path} → ${n.outcome}${n.duration_ms ? ` (${n.duration_ms} ms)` : ""}`)));
		}
		if (e.evidence?.length) out.push(p(b(L.evidence + ":")), list("ul", e.evidence));
		if (e.next_steps?.length) out.push(p(b(L.next + ":")), list("ol", e.next_steps));
		if (e.owner) out.push(p(`${b(L.owner + ":")} ${esc(e.owner)}`));
		if (f.dev?.length) out.push(p(b(L.tech + ":")), f.dev.map((d) => escDevEmail(d, L, !f.test_name || !e.test_id)).join(""));
		out.push(p(`${esc(`${L.run}: ${f.run_name} · ${f.env || "—"} · ${fmtDateTime(f.started_at)} · ${escFmt(L.failed, { f: f.failed, t: f.total })}`)}<br>
			<a href="${esc(escReportURL(e))}">${esc(L.report)}</a>`));
		return out.join("");
	}

	/** Copia con formato (text/html) y su versión en texto: la app donde se pega elige. Sin la Clipboard
	 * API (TraceReports abierto por HTTP en la red, no en localhost) va por el evento copy, que sí funciona. */
	async function copyRich(html, text) {
		if (navigator.clipboard?.write && window.ClipboardItem) {
			try {
				await navigator.clipboard.write([new ClipboardItem({ "text/html": new Blob([html], { type: "text/html" }), "text/plain": new Blob([text], { type: "text/plain" }) })]);
				return true;
			} catch { /* sin permiso: el evento copy */ }
		}
		return copyViaEvent({ "text/html": html, "text/plain": text });
	}

	function copyViaEvent(data) {
		const fill = (ev) => { Object.entries(data).forEach(([k, v]) => ev.clipboardData.setData(k, v)); ev.preventDefault(); };
		document.addEventListener("copy", fill);
		let ok = false;
		try { ok = document.execCommand("copy"); } catch { /* sin portapapeles */ }
		document.removeEventListener("copy", fill);
		return ok;
	}

	async function toDataURL(src) {
		return blobToDataURL(await (await fetch(src)).blob());
	}

	function blobToDataURL(blob) {
		return new Promise((ok, fail) => { const r = new FileReader(); r.onload = () => ok(r.result); r.onerror = fail; r.readAsDataURL(blob); });
	}

	/** Convierte la tarjeta en PNG: estilos copiados en línea + SVG foreignObject + canvas (sin librerías). */
	/**
	 * El estilo calculado de un elemento de la tarjeta, en línea para el PNG. Un elemento en línea
	 * (insignia, chip) no lleva su ancho ni su alto: los toma de su texto, como en pantalla. Con el ancho
	 * fijo de la pantalla (49.84px para «Baja»), al dibujar el PNG a otra escala el texto mide unas
	 * fracciones más y su última letra salta de línea («BAJ»). Las imágenes y los bloques (las columnas de
	 * la tabla) sí conservan sus medidas.
	 */
	const PNG_SIZE = new Set(["width", "height", "inline-size", "block-size", "min-width", "min-inline-size"]);
	function pngStyle(n) {
		const cs = getComputedStyle(n);
		const fixed = ["IMG", "CANVAS", "svg"].includes(n.tagName) || !cs.display.startsWith("inline");
		let css = "";
		for (const prop of cs) if (fixed || !PNG_SIZE.has(prop)) css += `${prop}:${cs.getPropertyValue(prop)};`;
		return css;
	}
	async function cardToPNG(card) {
		const clone = card.cloneNode(true);
		const src = [card, ...card.querySelectorAll("*")], dst = [clone, ...clone.querySelectorAll("*")];
		src.forEach((n, i) => dst[i].setAttribute("style", pngStyle(n)));
		clone.querySelectorAll(".esc-copy").forEach((b) => b.remove()); // en la imagen no hay a qué hacer clic
		for (const img of clone.querySelectorAll("img")) img.src = await toDataURL(img.getAttribute("src")).catch(() => "");
		const w = card.offsetWidth, h = card.offsetHeight, scale = 2;
		const xhtml = new XMLSerializer().serializeToString(clone);
		const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}"><foreignObject width="100%" height="100%">${xhtml}</foreignObject></svg>`;
		const img = new Image();
		img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
		await img.decode();
		const canvas = document.createElement("canvas");
		canvas.width = w * scale; canvas.height = h * scale;
		const ctx = canvas.getContext("2d");
		ctx.scale(scale, scale);
		ctx.drawImage(img, 0, 0);
		return new Promise((ok) => canvas.toBlob(ok, "image/png"));
	}

	function flash(btn, text, ok = true) {
		const old = btn.innerHTML;
		btn.innerHTML = `${icon(ok ? "i-check" : "i-fail")}${esc(text)}`;
		btn.classList.add(ok ? "cf-copied" : "cf-failed");
		setTimeout(() => { btn.innerHTML = old; btn.classList.remove("cf-copied", "cf-failed"); }, 1800);
	}

	/** PDF: una copia del reporte sola en la hoja y la impresión del navegador («Guardar como PDF»).
	 * El nombre del archivo sale del título del documento: el número del reporte. */
	function escPrintPDF(e) {
		const card = $("#esc-card");
		if (!card) return;
		document.getElementById("esc-print")?.remove();
		const holder = document.createElement("div");
		holder.id = "esc-print";
		holder.appendChild(card.cloneNode(true)).removeAttribute("id");
		document.body.appendChild(holder);
		const title = document.title;
		document.title = `${escRef(e)} · ${e.title}`.replace(/[\\/:*?"<>|]+/g, " ").slice(0, 120);
		document.body.classList.add("esc-printing");
		const done = () => {
			document.body.classList.remove("esc-printing");
			document.title = title;
			holder.remove();
			window.removeEventListener("afterprint", done);
		};
		window.addEventListener("afterprint", done);
		window.print();
		setTimeout(() => { if (document.body.classList.contains("esc-printing")) done(); }, 60_000); // si afterprint no llega
	}

	async function escShare(kind, btn) {
		const e = S.esc.data;
		if (!e) return;
		try {
			if (kind === "pdf") { escPrintPDF(e); return; }
			if (kind === "plain" || kind === "md") { CF.copyText(escText(e, kind), btn); return; }
			if (kind === "teams-copy" || kind === "slack") {
				// con formato: Teams y Slack muestran los ** o * del texto tal cual al pegarlo
				const ok = await copyRich(escChatHTML(e), escText(e, kind === "slack" ? "slack" : "plain"));
				flash(btn, ok ? tr("¡Copiado!") : tr("No se pudo"), ok); return;
			}
			if (kind === "email") {
				const ok = await copyRich(await escEmailHTML(e), escText(e, "plain"));
				flash(btn, ok ? tr("¡Copiado!") : tr("No se pudo"), ok); return;
			}
			if (kind === "png" && navigator.clipboard?.write && window.ClipboardItem) {
				// la imagen va como promesa dentro del ClipboardItem: el navegador guarda el permiso del clic
				// mientras se dibuja (si se espera antes, el permiso vence y la copia falla)
				btn.disabled = true;
				try {
					await navigator.clipboard.write([new ClipboardItem({ "image/png": cardToPNG($("#esc-card")) })]);
					btn.disabled = false;
					flash(btn, tr("¡Imagen copiada!")); return;
				} catch { btn.disabled = false; /* abajo: como HTML o descarga */ }
			}
			if (kind === "png" || kind === "png-dl") {
				btn.disabled = true;
				const blob = await cardToPNG($("#esc-card"));
				btn.disabled = false;
				if (kind === "png") {
					// sin Clipboard API (HTTP en la red): la imagen dentro de un HTML, que Teams y Outlook pegan
					const url = await blobToDataURL(blob);
					if (copyViaEvent({ "text/html": `<img src="${url}" alt="${esc(e.title)}">`, "text/plain": escText(e, "plain") })) {
						flash(btn, tr("¡Imagen copiada!"));
						if (!window.isSecureContext) {
							S.esc.msg = { ok: true, text: tr("TraceReports está abierto por HTTP, así que el navegador no deja copiar imágenes directamente: la copié con formato. Si al pegar no aparece, usa el botón PNG o abre TraceReports con HTTPS.") };
							renderEscalate();
						}
						return;
					}
				}
				const a = document.createElement("a");
				a.href = URL.createObjectURL(blob);
				a.download = `escalamiento-${e.run_id}${e.test_id ? `-${e.test_id}` : ""}-${e.audience}.png`;
				a.click();
				setTimeout(() => URL.revokeObjectURL(a.href), 2000);
				flash(btn, kind === "png" ? tr("Descargada (este navegador no copia imágenes)") : tr("Descargada"));
				return;
			}
			if (kind === "teams" || kind === "slack-send") {
				btn.disabled = true;
				const r = await apiSend("POST", "/api/v1/ui/escalate/send", { run_id: e.run_id, test_id: e.test_id, audience: e.audience, lang: e.lang, channel: kind === "teams" ? "teams" : "slack", no_ai: e.source !== "ai" });
				btn.disabled = false;
				flash(btn, tr("¡Enviado!"));
				S.esc.msg = r.with_link ? null : { ok: true, text: tr("Enviado sin link ni captura: configura PUBLIC_URL para que el mensaje incluya el link al reporte y la imagen del fallo.") };
				if (S.esc.msg) renderEscalate();
			}
		} catch (err) {
			btn.disabled = false;
			flash(btn, tr("No se pudo"), false);
			S.esc.msg = { ok: false, text: err.message };
			renderEscalate();
		}
	}

	/** Crea (o encuentra) el ticket del fallo en un tracker, con el resumen que se está viendo. */
	async function escTicket(provider, btn, force = false) {
		const e = S.esc.data;
		if (!e) return;
		btn.disabled = true;
		try {
			const r = await apiSend("POST", "/api/v1/ui/tickets", { run_id: e.run_id, test_id: e.test_id, audience: e.audience, lang: e.lang,
				provider, no_ai: e.source !== "ai", force });
			const k = r.ticket;
			S.esc.msg = r.existing
				? { ok: true, text: tr("Este fallo ya tiene un ticket, así que no se creó otro:"), link: k.url, key: k.key, force: provider }
				: { ok: true, text: tr("Ticket creado:"), link: k.url, key: k.key };
		} catch (err) {
			S.esc.msg = { ok: false, text: err.message };
		}
		btn.disabled = false;
		renderEscalate();
	}

	function renderEscalate() {
		const el = $("#view-escalate");
		const r = S.run, st = S.esc;
		const failed = r.tests.filter((t) => t.status === "FAIL");
		if (st.test && !failed.some((t) => t.id === st.test)) st.test = 0;
		if (STATIC) st.data = staticEscalation();
		else if (canAct()) loadCachedEscalation();
		const e = st.data;
		const cfg = S.config;
		const share = e ? `<div class="esc-share" role="toolbar" aria-label="${tr("Compartir")}">
				<div class="esc-share-group"><span class="esc-share-lbl">${tr("Copiar para")}</span>
					<button class="cf-btn cf-btn-sm" data-esc-share="teams-copy" data-tip="${tr("Con negritas, listas y código, para pegar en un chat o canal de Teams")}">${icon("i-teams")}Teams</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="slack" data-tip="${tr("Con negritas, listas y código, para pegar en Slack")}">${icon("i-slack")}Slack</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="email" data-tip="${tr("Con formato y la captura, para pegar en Outlook o Gmail")}">${icon("i-mail")}${tr("Correo")}</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="md" data-tip="${tr("Markdown, para Jira, GitHub, GitLab o Confluence")}">${icon("i-markdown")}Markdown</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="plain" data-tip="${tr("Texto simple, sin formato")}">${icon("i-copy")}${tr("Texto")}</button>
				</div>
				<div class="esc-share-group"><span class="esc-share-lbl">${tr("Imagen")}</span>
					<button class="cf-btn cf-btn-sm" data-esc-share="png" data-tip="${tr("Copia la tarjeta como imagen para pegarla en Teams, Slack o una presentación")}">${icon("i-camera")}${tr("Copiar imagen")}</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="png-dl" data-tip="${tr("Descarga la tarjeta como PNG")}">${icon("i-download")}PNG</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="pdf" data-tip="${tr("Abre la impresión con solo el reporte: elige «Guardar como PDF» para adjuntarlo a un correo o ticket")}">${icon("i-download")}PDF</button>
				</div>
				${cfg.teams || cfg.slack ? `<div class="esc-share-group"><span class="esc-share-lbl">${tr("Enviar al canal")}</span>
					${cfg.teams ? `<button class="cf-btn cf-btn-sm esc-send" data-esc-share="teams" data-tip="${tr("Publica el resumen en el canal de Teams configurado (TEAMS_WEBHOOK_URL)")}">${icon("i-teams")}Teams</button>` : ""}
					${cfg.slack ? `<button class="cf-btn cf-btn-sm esc-send" data-esc-share="slack-send" data-tip="${tr("Publica el resumen en el canal de Slack configurado (SLACK_WEBHOOK_URL)")}">${icon("i-slack")}Slack</button>` : ""}
				</div>` : ""}
				${cfg.trackers?.length ? `<div class="esc-share-group"><span class="esc-share-lbl">${tr("Ticket")}</span>
					${cfg.trackers.map((k) => `<button class="cf-btn cf-btn-sm" data-esc-ticket="${esc(k.id)}" data-tip="${tr("Crea un ticket en {t} con este resumen, la captura y el link al reporte. Si el mismo test ya tiene uno, te muestra ese en vez de duplicarlo.", { t: k.name })}">${icon("i-bug")}${esc(k.name)}</button>`).join("")}
				</div>` : ""}
			</div>` : "";
		const stale = e && st.stale && !st.loading ? `<p class="set-msg ok esc-stale" role="status">${icon("i-info")}<span>${tr("Llegó evidencia nueva desde que se armó este resumen.")} <button class="cf-btn cf-btn-sm" data-esc-gen>${tr("Regenerar")}</button></span></p>` : "";
		const preview = !canAct() && !STATIC
			? `<div class="card m-empty">${icon("i-megaphone")}<h5>${tr("Escalar no está disponible desde aquí")}</h5><p>${esc(actReason())}</p></div>`
			: st.loading ? `<div class="card esc-loading"><div class="shimmer"></div><div class="shimmer"></div><div class="shimmer short"></div>
				<p class="m-hint">${cfg.ai_enabled ? tr("La IA está escribiendo el resumen para {a}…", { a: tr(AUDIENCES.find((a) => a.id === st.audience).name) }) : tr("Armando el resumen…")}</p></div>`
			: e ? `${stale}<div class="esc-frame">${share}${escCard(e)}${escTimingHTML(e)}</div>`
			: `<div class="card m-empty">${icon("i-megaphone")}<h5>${tr("Elige qué escalar y para quién")}</h5>
				<p>${tr("El resumen junta el error, la captura, las llamadas al backend que fallaron y el diagnóstico, explicado para la audiencia que elijas. Después lo copias como texto o imagen, o lo envías a Teams o Slack.")}</p></div>`;
		setHTML(el, `<div class="page-head"><div><h4 class="page-title">${tr("Escalar un fallo")}</h4>
				<p class="page-sub">${tr("Convierte la evidencia del reporte en un resumen que cualquiera entiende, listo para compartir.")}</p></div></div>
			<div class="esc-layout">
				<aside class="card set-card esc-controls">
					<label class="field"><span class="field-label"><span class="esc-step">1</span>${tr("Qué escalar")}</span>
						<select id="esc-scope" ${failed.length ? "" : "disabled"}>
							<option value="0" ${!st.test ? "selected" : ""}>${tr("La ejecución completa ({n} fallos)", { n: failed.length })}</option>
							${failed.map((t) => `<option value="${t.id}" ${st.test === t.id ? "selected" : ""} data-no-i18n>${esc(t.name)}</option>`).join("")}
						</select></label>
					<fieldset class="aud-grid"><legend class="field-label"><span class="esc-step">2</span>${tr("Para quién")}</legend>
						${AUDIENCES.map((a) => `<label class="aud-opt ${st.audience === a.id ? "on" : ""}" data-tip="${esc(tr(a.desc))}"><input type="radio" name="esc-aud" value="${a.id}" ${st.audience === a.id ? "checked" : ""}>
							<span class="aud-icon">${icon(a.icon)}</span><span class="aud-text"><b>${tr(a.name)}</b><small>${tr(a.short)}</small></span></label>`).join("")}
					</fieldset>
					<div class="esc-row">
					<div class="field"><span class="field-label"><span class="esc-step">3</span>${tr("Idioma")}</span>
						<div class="seg" role="group" aria-label="${tr("Idioma del resumen")}"><button data-esc-lang="es" aria-pressed="${st.lang === "es"}" data-no-i18n>ES</button><button data-esc-lang="en" aria-pressed="${st.lang === "en"}" data-no-i18n>EN</button></div></div>
					<div class="field"><span class="field-label">${tr("Redacción")}</span>
						<div class="seg" role="group" aria-label="${tr("Redacción")}">
							<button data-esc-ai="1" aria-pressed="${!escNoAI()}" ${(STATIC ? Object.keys(STATIC.escalations || {}).some((k) => k.endsWith(":ai")) : cfg.ai_enabled) ? "" : "disabled"} data-tip="${esc(tr(cfg.ai_enabled ? "La IA redacta el resumen con la evidencia del reporte." : "Configura un proveedor de IA en Ajustes para usar esta opción."))}">${icon("i-spark")}${tr("Con IA")}</button>
							<button data-esc-ai="0" aria-pressed="${escNoAI()}" data-tip="${esc(tr("Plantilla armada con la evidencia: no usa cuota y nada sale a un proveedor externo."))}">${tr("Sin IA")}</button>
						</div></div>
					</div>
					${canAct() ? `<button class="cf-btn cf-btn-primary esc-go" data-esc-gen ${st.loading ? "disabled" : ""}>${icon("i-spark")}${e ? tr("Regenerar") : tr("Generar resumen")}</button>
						<p class="field-help">${!escNoAI() ? tr("Lo escribe la IA ({m}) con la evidencia del reporte. Queda guardado: verlo de nuevo no gasta cuota.", { m: cfg.ai_model })
						: cfg.ai_enabled ? tr("Se arma con una plantilla a partir de la evidencia: es inmediato, no usa cuota y la evidencia no sale a ningún proveedor externo.")
						: tr("Sin IA configurada se arma con una plantilla a partir de la evidencia. Configura un proveedor en Ajustes para un resumen redactado.")}</p>` : ""}
					${STATIC ? `<p class="field-help">${tr("Reporte exportado: el resumen se armó al exportar, con la evidencia de esta ejecución. Cópialo como texto o imagen; para enviarlo a Teams, Slack o un ticket, usa el servidor.")}</p>` : ""}
					${failed.length ? "" : `<p class="field-help">${tr("Esta ejecución no tiene fallos: el resumen será del resultado general.")}</p>`}
				</aside>
				<div class="esc-preview">${st.msg ? `<p class="set-msg ${st.msg.ok ? "ok" : "err"}" role="status">${icon(st.msg.ok ? "i-info" : "i-fail")}<span>${esc(st.msg.text)}${st.msg.link ? ` <a href="${esc(st.msg.link)}" target="_blank" rel="noopener" data-no-i18n>${esc(st.msg.key || st.msg.link)} ↗</a>` : ""}${st.msg.force ? ` <button class="cf-btn cf-btn-sm" data-esc-ticket="${esc(st.msg.force)}" data-force="1">${tr("Crear otro igual")}</button>` : ""}</span></p>` : ""}${preview}</div>
			</div>`);
	}

	/** Abre una sección de Ajustes (y la recuerda en este navegador). */
	function openSettingsTab(id, focus) {
		S.settings.tab = id;
		try { localStorage.setItem("tracereports-settings-tab", id); } catch { /* sin almacenamiento */ }
		renderSettings(true);
		if (focus) $(`[data-set-tab="${id}"]`)?.focus();
		window.scrollTo({ top: 0 });
	}

	function bindAIEscalateEvents() {
		const ai = $("#view-ai"), es = $("#view-escalate");
		ai.addEventListener("click", (e) => {
			const b = e.target.closest("[data-ai-rerun]");
			if (b) { aiRerun(b.dataset.aiRerun); return; }
			const t = e.target.closest("[data-ai-retest]");
			if (t) { aiRerun("test", Number(t.dataset.aiRetest)); }
		});
		// "Escalar" desde cualquier vista: abre la vista de escalamiento con ese test elegido
		document.addEventListener("click", (e) => {
			const b = e.target.closest("[data-escalate]");
			if (!b) return;
			e.preventDefault();
			S.esc.test = Number(b.dataset.escalate) || 0;
			CF.Drawer.close();
			setView("escalate");
		});
		es.addEventListener("change", (e) => {
			if (e.target.id === "esc-scope") { S.esc.test = Number(e.target.value); renderEscalate(); }
			if (e.target.name === "esc-aud") { S.esc.audience = e.target.value; renderEscalate(); }
		});
		es.addEventListener("click", (e) => {
			const l = e.target.closest("[data-esc-lang]");
			if (l) { S.esc.lang = l.dataset.escLang; renderEscalate(); return; }
			const m = e.target.closest("[data-esc-ai]");
			if (m) { S.esc.noAI = m.dataset.escAi === "0"; renderEscalate(); return; }
			if (e.target.closest("[data-esc-gen]")) { generateEscalation(!!S.esc.data); return; }
			const cp = e.target.closest("[data-esc-copy]");
			if (cp) { CF.copyText(cp.dataset.escCopy, cp); return; }
			const sh = e.target.closest("[data-esc-share]");
			if (sh) escShare(sh.dataset.escShare, sh);
			const tk = e.target.closest("[data-esc-ticket]");
			if (tk) escTicket(tk.dataset.escTicket, tk, tk.dataset.force === "1");
		});
	}

	// ---------- métricas (entre ejecuciones) ----------
	const I18N = window.TraceReportsI18n;
	const tr = (s, v) => I18N.t(s, v);
	const locale = () => (I18N.lang === "en" ? "en-US" : "es-CL");
	const fmtNum = (n, d = 0) => Number(n || 0).toLocaleString(locale(), { maximumFractionDigits: d, minimumFractionDigits: d });
	/** Duración legible: 850 ms · 12 s · 3 min 20 s · 1 h 12 min. */
	function fmtHuman(ms) {
		if (!ms) return "0 s";
		if (ms < 1000) return `${ms} ms`;
		const s = Math.round(ms / 1000);
		if (s < 60) return `${s} s`;
		const m = Math.floor(s / 60);
		if (m < 60) return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
		return `${Math.floor(m / 60)} h ${m % 60} min`;
	}

	async function apiSend(method, path, body) {
		const res = await fetch(path, { method, headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(body ?? {}) });
		const data = await res.json().catch(() => ({}));
		if (!res.ok) throw new Error(apiErrorText(data, res.status));
		return data;
	}
	/**
	 * El texto de un error de la API: si el servidor dice la causa (disco lleno, base ocupada...) se
	 * explica qué hacer; un error interno lleva su referencia, la misma que queda en el log del servidor.
	 */
	function apiErrorText(data, status) {
		const ref = data?.ref ? tr(" (referencia {ref}: búscala en el log del servidor)", { ref: data.ref }) : "";
		const known = {
			disk_full: tr("El disco de datos del servidor está lleno: libera espacio o baja la retención."),
			busy: tr("La base de datos está ocupada: reintenta en un momento."),
			read_only: tr("El servidor no puede escribir su carpeta de datos (permisos o disco de solo lectura)."),
			corrupt: tr("El archivo de la base de datos está dañado: restaura un respaldo."),
		};
		if (known[data?.kind]) return known[data.kind] + ref;
		if (data?.ref) return tr("Error interno del servidor.") + ref;
		return data?.error || `${status}`;
	}

	async function loadMetrics() {
		const m = S.metrics, current = turn("metrics");
		m.loading = true;
		$("#m-body")?.classList.add("is-loading"); // se conserva el gráfico anterior atenuado
		try {
			const q = new URLSearchParams({ days: m.days, suite: m.suite, env: m.env, tag: m.tag });
			if (m.custom?.from && m.custom?.to) {
				// el servidor acepta rangos de hasta 366 días (un punto por día en el gráfico)
				const span = (Date.parse(m.custom.to) - Date.parse(m.custom.from)) / 86400000 + 1;
				if (span > 366) throw new Error(tr("El rango puede tener como máximo 366 días (un año): elige uno más corto."));
				q.set("from", m.custom.from); q.set("to", m.custom.to);
			}
			const data = await api(`/api/v1/metrics?${q}`);
			if (!current()) return; // se cambió el filtro: vale la petición más nueva
			m.data = data;
			m.error = null;
		} catch (err) {
			if (!current()) return;
			m.error = err.message;
		}
		m.loading = false;
		if (S.view === "metrics") renderMetrics();
	}

	// ---------- mantenimiento ----------
	// Las ejecuciones con problemas de todos los proyectos, para cerrarlas o borrarlas en lote, y el
	// umbral del cierre automático por inactividad.
	const MAINT_STATES = [
		["", "Con problemas", "En curso o incompletas, de todos los proyectos."],
		["idle", "Sin actividad", "En curso, pero sin recibir datos hace más de 5 minutos: probablemente el proceso de los tests se detuvo."],
		["running", "En curso", "Todas las que siguen abiertas, reciban datos o no."],
		["incomplete", "Incompletas", "Cerradas con tests sin terminar: por Ctrl+C, un worker caído, a mano o por inactividad."],
		["manual", "Cerradas a mano", "Las que alguien cerró desde la interfaz, con su motivo."],
		["auto", "Cerradas por inactividad", "Las que se cerraron solas al pasar el tiempo sin actividad."],
	];
	const MAINT_OLDER = [["", "Cualquier fecha"], ["1", "Más de 1 hora"], ["24", "Más de 1 día"], ["168", "Más de 7 días"], ["720", "Más de 30 días"]];
	const STALE_CHOICES = [0.5, 1, 2, 6, 12, 24, 48, 0];
	const staleText = (h) => (h === 0 ? tr("Nunca") : h < 1 ? `${Math.round(h * 60)} min` : `${h} h`);

	async function loadStorage() {
		const m = S.maint;
		try {
			m.storage = await api("/api/v1/maintenance/storage");
			m.storageError = null;
		} catch (err) {
			m.storageError = err.message;
		}
		if (S.view === "maintenance") renderMaintenance();
	}

	/** Mantenimiento > Conexiones de los clientes: runs con problemas de entrega, pedidos lentos, credenciales. */
	async function loadConnections() {
		const m = S.maint;
		try {
			m.conn = { data: await api("/api/v1/maintenance/connections") };
		} catch (err) {
			m.conn = { error: err.message };
		}
		if (S.view === "maintenance") renderMaintenance();
	}

	/**
	 * Conexiones de los clientes: un resumen arriba (qué hay y cuánto) y cada lista plegada, de a 10
	 * filas. Con muchos workers, cada ejecución dejaba decenas de runs con el mismo problema y la tarjeta
	 * era una lista sin fin: ahora los runs van agrupados por ejecución, y la ejecución se abre para verlos.
	 */
	const CONN_PAGE = 10;
	function connUI() {
		return (S.maint.connUI ||= { open: {}, more: {}, exec: {} });
	}
	/** Los runs con problemas de entrega, juntos por ejecución (un run sin ejecución va solo). */
	function connGroups(runs) {
		const by = new Map();
		for (const r of runs) {
			const key = r.execution_id ? `x:${r.execution_id}` : `r:${r.id}`;
			if (!by.has(key)) by.set(key, { key, execution: r.execution_id || "", runs: [] });
			by.get(key).runs.push(r);
		}
		const sum = (g, k) => g.runs.reduce((n, r) => n + (r.delivery[k] || 0), 0);
		return [...by.values()].map((g) => ({
			...g,
			late: g.runs.filter((r) => r.delivery.late).length,
			lost: sum(g, "lost"), dropped: sum(g, "dropped"), rejected: sum(g, "rejected"), spooled: sum(g, "spooled"), retried: sum(g, "retried"),
			hosts: [...new Set(g.runs.map((r) => r.delivery.host).filter(Boolean))],
			at: Math.max(...g.runs.map((r) => r.delivery.late_at || r.started_at || 0)),
		})).sort((a, b) => b.at - a.at);
	}
	function connectionsCard() {
		const c = S.maint.conn, ui = connUI();
		const head = `<header class="stor-head"><span class="maint-stale-icon">${icon("i-plug")}</span><b>${tr("Conexiones de los clientes")}</b>
			<button type="button" class="cf-btn cf-btn-sm" data-conn-reload>${tr("Actualizar")}</button></header>`;
		if (!c) return `<section class="card conn-card" id="maint-connections">${head}<p class="m-hint">${tr("Cargando…")}</p></section>`;
		if (c.error) return `<section class="card conn-card" id="maint-connections">${head}<p class="m-hint">${esc(tr("No se pudo cargar: {e}", { e: c.error }))}</p></section>`;
		const d = c.data, runs = d.runs || [], slow = d.slow || [], auth = d.auth || [], stalls = d.stalls || [];
		const when = (ms) => ms ? `${fmtDateTime(ms)}` : "—";
		const secs = (ms) => `${window.TraceReportsLive?.num1(ms / 1000) ?? (ms / 1000).toFixed(1)} s`;
		const what = (v) => [v.late && `<span class="conn-tag late">${v.late === true ? tr("Creado tarde") : tr("{n} creados tarde", { n: v.late })}</span>${v.reason ? ` <span data-no-i18n>${esc(v.reason)}</span>` : ""}`,
			v.lost && tr("{n} perdidos", { n: v.lost }), v.dropped && tr("{n} descartados", { n: v.dropped }), v.rejected && tr("{n} rechazados", { n: v.rejected }),
			v.spooled && tr("{n} para reenviar", { n: v.spooled }), v.retried && tr("{n} reintentos", { n: v.retried })].filter(Boolean).join(" · ") || "—";
		// de a CONN_PAGE filas; "Mostrar más" suma de a 20
		const page = (sec, rows) => {
			const n = CONN_PAGE + (ui.more[sec] || 0), rest = rows.length - n;
			return { rows: rows.slice(0, n), more: rest > 0 ? `<button type="button" class="cf-btn cf-btn-sm conn-more" data-conn-more="${sec}">${tr("Mostrar {n} más", { n: Math.min(20, rest) })} <span class="m-hint">${tr("({n} en total)", { n: rows.length })}</span></button>` : "" };
		};
		// una sección plegable: el título dice cuántos hay; con problemas, el número se destaca
		const section = (sec, title, n, body, okText, foot = "") => `<details class="conn-sec" data-conn-sec="${sec}" ${ui.open[sec] ? "open" : ""}>
			<summary><span class="conn-sec-title">${esc(title)}</span><span class="conn-count ${n ? "warn" : "ok"}">${n ? n : icon("i-pass")}</span></summary>
			<div class="conn-sec-body">${n ? body : `<p class="conn-ok">${icon("i-pass")}${esc(okText)}</p>`}${foot}</div></details>`;

		const groups = connGroups(runs), gp = page("runs", groups);
		const runsBody = `<div class="maint-scroll"><table class="stor-table conn-table"><thead><tr><th>${tr("Ejecución")}</th><th class="lt-num">${tr("Runs")}</th><th>${tr("Qué pasó")}</th><th>${tr("Equipo")}</th><th>${tr("Última vez")}</th></tr></thead><tbody>
			${gp.rows.map((g) => {
				const one = g.runs.length === 1, r0 = g.runs[0], open = !one && ui.exec[g.key];
				const name = one ? `<a href="#run=${r0.id}&view=tests" data-no-i18n>#${r0.id} · ${esc(r0.name)}</a>${g.execution ? `<small data-no-i18n>${esc(g.execution)}</small>` : ""}`
					: `<button type="button" class="conn-exec" data-conn-exec="${esc(g.key)}" aria-expanded="${open ? "true" : "false"}">${icon("i-chevron")}<span data-no-i18n>${esc(g.execution)}</span></button>`;
				const sub = open ? g.runs.map((r) => `<tr class="conn-subrow"><td><a href="#run=${r.id}&view=tests" data-no-i18n>#${r.id} · ${esc(r.name)}</a></td><td></td>
					<td>${what(r.delivery)}${r.delivery.detail ? `<small class="conn-mono" data-no-i18n>${esc(r.delivery.detail)}</small>` : ""}</td>
					<td data-no-i18n>${esc([r.delivery.host, r.delivery.client].filter(Boolean).join(" · ") || "—")}</td><td>${when(r.delivery.late_at || r.started_at)}</td></tr>`).join("") : "";
				const v = one ? r0.delivery : { ...g, late: g.late };
				return `<tr class="conn-group"><td>${name}</td><td class="lt-num">${g.runs.length}</td><td>${what(v)}${one && r0.delivery.detail ? `<small class="conn-mono" data-no-i18n>${esc(r0.delivery.detail)}</small>` : ""}</td>
					<td data-no-i18n>${esc((one ? [r0.delivery.host, r0.delivery.client].filter(Boolean) : g.hosts).join(" · ") || "—")}</td><td>${when(g.at)}</td></tr>${sub}`;
			}).join("")}</tbody></table></div>${gp.more}`;

		const sp = page("slow", slow);
		const slowBody = `<div class="maint-scroll"><table class="stor-table conn-table"><thead><tr><th>${tr("Cuándo")}</th><th>${tr("Pedido")}</th><th>${tr("Respuesta")}</th><th class="lt-num">${tr("Tardó")}</th></tr></thead><tbody>
			${sp.rows.map((x) => `<tr><td>${when(x.at)}</td><td class="conn-mono" data-no-i18n>${esc(x.method)} ${esc(x.path)}</td><td>${x.status || "—"}</td><td class="lt-num">${esc(secs(x.ms))}</td></tr>`).join("")}</tbody></table></div>${sp.more}`;
		const slowFoot = `<p class="m-hint">${tr("Si un test dijo «timed out» y aquí no aparece ningún pedido lento a esa hora, el pedido no llegó al servidor: el problema está en la red o en Docker, no en TraceReports.")}</p>`;

		const tp = page("stalls", stalls);
		const stallsBody = `<div class="maint-scroll"><table class="stor-table conn-table"><thead><tr><th>${tr("Cuándo")}</th><th class="lt-num">${tr("Duró")}</th><th class="lt-num">${tr("Pedidos trabados")}</th><th>${tr("Qué esperaba")}</th><th class="lt-num">${tr("Espera por la base")}</th><th>${tr("Volcado")}</th></tr></thead><tbody>
			${tp.rows.map((x) => `<tr><td>${when(x.start)}${x.end ? "" : ` <span class="conn-tag late">${tr("en curso")}</span>`}</td>
				<td class="lt-num">${x.end ? esc(secs(x.end - x.start)) : "—"}</td><td class="lt-num">${x.max_stuck}</td>
				<td class="conn-mono" data-no-i18n>${(x.paths || []).map(esc).join("<br>")}</td><td class="lt-num">${x.end ? esc(secs(x.db_wait_ms || 0)) : "—"}</td>
				<td>${x.dump ? `<a href="/api/v1/maintenance/stalls/${encodeURIComponent(x.dump)}" download data-no-i18n>${esc(x.dump)}</a>` : "—"}</td></tr>`).join("")}</tbody></table></div>${tp.more}
			<p class="m-hint">${tr("El volcado dice qué estaba haciendo cada tarea del servidor en ese momento (qué tenía tomada la base de datos). Adjúntalo si reportas el problema.")}</p>`;

		const authBody = `<div class="maint-scroll"><table class="stor-table conn-table"><thead><tr><th>${tr("Dirección")}</th><th class="lt-num">${tr("Intentos")}</th><th>${tr("Estado")}</th></tr></thead><tbody>
			${auth.map((a) => `<tr><td class="conn-mono" data-no-i18n>${esc(a.ip)}</td><td class="lt-num">${a.failures}</td><td>${a.blocked_ms ? `<span class="conn-tag late">${esc(tr("bloqueada {n} min", { n: Math.ceil(a.blocked_ms / 60000) }))}</span>` : tr("en observación")}</td></tr>`).join("")}</tbody></table></div>
			<p class="m-hint">${tr("Suele ser un token mal puesto en el .env de los tests, o una pestaña con la sesión vencida.")}</p>`;

		// el resumen: cada número lleva a su sección (y la abre)
		const chips = [["runs", tr("{n} ejecuciones con problemas de entrega", { n: groups.length }), groups.length],
			["slow", tr("{n} pedidos lentos", { n: slow.length }), slow.length],
			["stalls", tr("{n} bloqueos", { n: stalls.length }), stalls.length],
			d.auth ? ["auth", tr("{n} credenciales rechazadas", { n: auth.length }), auth.length] : null].filter(Boolean);
		const summary = chips.every(([, , n]) => !n) ? `<p class="conn-ok">${icon("i-pass")}${esc(tr("Todo en orden: sin problemas de entrega, pedidos lentos ni bloqueos."))}</p>`
			: `<div class="conn-status">${chips.map(([sec, text, n]) => `<button type="button" class="conn-chip ${n ? "warn" : "ok"}" data-conn-jump="${sec}"><span class="dot"></span>${esc(text)}</button>`).join("")}</div>`;
		return `<section class="card conn-card" id="maint-connections" aria-label="${esc(tr("Conexiones de los clientes"))}">${head}${summary}
			${section("runs", tr("Runs con problemas de entrega (últimos {n} días)", { n: d.days }), groups.length, runsBody, tr("Ningún run con problemas de entrega en los últimos {n} días.", { n: d.days }))}
			${section("slow", tr("Pedidos lentos del servidor"), slow.length, slowBody, tr("Ningún pedido tardó más de {s} s desde que el servidor arrancó ({t}).", { s: Math.round((d.slow_after_ms || 2000) / 1000), t: fmtDateTime(d.started_at) }), slowFoot)}
			${section("stalls", tr("Bloqueos del servidor (varios pedidos sin respuesta más de {s} s a la vez)", { s: Math.round((d.stall_after_ms || 10000) / 1000) }), stalls.length, stallsBody, tr("Ningún bloqueo desde que el servidor arrancó ({t}).", { t: fmtDateTime(d.started_at) }))}
			${d.auth ? section("auth", tr("Credenciales rechazadas (últimos 15 min)"), auth.length, authBody, tr("Ninguna.")) : ""}</section>`;
	}

	async function loadMaintenance() {
		const m = S.maint;
		m.loading = true;
		loadStorage();
		loadConnections();
		const p = new URLSearchParams();
		if (m.state) p.set("state", m.state);
		["project", "environment", "branch"].forEach((k) => { if (m[k]) p.set(k, m[k]); });
		if (m.older) p.set("older_than_hours", m.older);
		try {
			const [data, facets] = await Promise.all([api(`/api/v1/maintenance?${p}`), m.facets || api("/api/v1/runs/facets").catch(() => ({}))]);
			const ids = new Set(data.runs.map((r) => r.id));
			Object.assign(m, { data, facets, error: null, selected: new Set([...m.selected].filter((id) => ids.has(id))) });
		} catch (err) {
			m.error = err.message;
		}
		m.loading = false;
		if (S.view === "maintenance") renderMaintenance();
	}

	function maintRow(r, act) {
		const m = S.maint, warn = S.maint.data?.idle_warn_ms || IDLE_WARN_MS;
		const live = r.status === "RUNNING"
			? (r.idle_ms >= warn ? `<small class="maint-idle">${tr("sin actividad hace {t}", { t: fmtIdle(r.idle_ms) })}</small>` : `<small>${tr("recibiendo datos")}</small>`)
			: r.incomplete ? `<small>${tr("incompleta")}</small>` : "";
		const tests = [`${r.total} tests`, r.running && tr("{n} en curso", { n: r.running }), r.interrupted && tr("{n} interrumpidos", { n: r.interrupted }), r.failed && tr("{n} fallidos", { n: r.failed })].filter(Boolean).join(" · ");
		const ctx = [r.project, r.environment, r.branch].filter(Boolean).map(esc).join(" · ") || "—";
		return `<tr class="${m.selected.has(r.id) ? "on" : ""}">
			${act ? `<td><input type="checkbox" data-maint-pick="${r.id}" ${m.selected.has(r.id) ? "checked" : ""} ${m.busy ? "disabled" : ""} aria-label="${tr("Elegir")} #${r.id}"></td>` : ""}
			<td><a href="#run=${r.id}&view=tests" data-no-i18n>#${r.id} · ${esc(r.name)}</a></td>
			<td data-no-i18n>${ctx}</td>
			<td>${statusLabel(r.status)} ${live}</td>
			<td>${tests}</td>
			<td>${fmtDateTime(r.started_at)}</td>
			<td>${r.closed_by ? `${esc(closedByText(r))}${r.close_reason ? `<small data-no-i18n>${esc(r.close_reason)}</small>` : ""}` : "—"}</td>
		</tr>`;
	}

	function maintAction(sel) {
		const m = S.maint;
		const busy = m.busy, off = busy ? "disabled" : "";
		const progress = (text) => `<p class="maint-progress" role="status"><span class="maint-spin" aria-hidden="true"></span>${esc(text)}</p>`;
		if (m.action === "close") {
			const n = sel.filter((r) => r.status === "RUNNING").length, f = m.closeForm || {};
			return `<form class="card maint-confirm" data-maint-close-form ${busy ? 'aria-busy="true"' : ""}>
				<label class="field"><span class="field-label">${tr("Motivo")}</span>
					<select name="reason" ${off}>${CLOSE_REASONS.map((r) => `<option value="${r}" ${f.reason === r ? "selected" : ""}>${tr(r)}</option>`).join("")}</select></label>
				<label class="field"><span class="field-label">${tr("Detalle (opcional)")}</span><input name="detail" maxlength="400" value="${esc(f.detail || "")}" ${off}></label>
				<p>${tr("Se cerrarán {n} ejecución(es) en curso: sus tests sin terminar quedarán como Interrumpido. Las ya cerradas no cambian.", { n })}</p>
				${busy ? progress(tr("Cerrando {n} ejecución(es)… cada una deja sus tests sin terminar como Interrumpido; puede tardar unos segundos.", { n: busy.n })) : ""}
				<div class="close-run-actions"><button type="submit" class="cf-btn cf-btn-sm cf-btn-primary" ${off}>${busy ? `<span class="maint-spin" aria-hidden="true"></span>${tr("Cerrando…")}` : `${icon("i-interrupted")}${tr("Cerrar {n} ejecución(es)", { n })}`}</button>
					<button type="button" class="cf-btn cf-btn-sm" data-maint-cancel ${off}>${tr("Cancelar")}</button></div></form>`;
		}
		if (m.action === "delete") {
			const n = sel.filter((r) => r.status !== "RUNNING").length;
			return `<div class="card maint-confirm danger" role="alertdialog" aria-label="${tr("Eliminar ejecuciones")}">
				<p><b>${tr("Se eliminarán {n} ejecución(es) con toda su evidencia: pasos, capturas, red y diagnósticos. No se puede deshacer.", { n })}</b></p>
				<p>${tr("Las que siguen en curso no se eliminan: ciérralas primero.")}</p>
				${busy ? progress(tr("Eliminando {n} ejecución(es) y su evidencia… puede tardar unos segundos.", { n: busy.n })) : ""}
				<div class="close-run-actions"><button type="button" class="cf-btn cf-btn-sm cf-btn-danger" data-maint-delete ${off} ${busy ? 'aria-busy="true"' : ""}>${busy ? `<span class="maint-spin" aria-hidden="true"></span>${tr("Eliminando…")}` : tr("Eliminar {n} ejecución(es)", { n })}</button>
					<button type="button" class="cf-btn cf-btn-sm" data-maint-cancel ${off}>${tr("Cancelar")}</button></div></div>`;
		}
		return "";
	}

	// cuántos días: opciones rápidas cortas (una suite diaria) y largas; "Otro…" escribe cualquiera
	const DAY_PRESETS = [1, 2, 3, 7, 14, 30, 60, 90, 180, 365];
	const MAX_DAYS = 3650;
	const retentionText = (d) => !d ? tr("Conservar todo") : d === 1 ? tr("1 día") : d === 365 ? tr("1 año")
		: d >= 60 && d % 30 === 0 ? tr("{n} meses", { n: d / 30 }) : tr("{n} días", { n: d });
	/**
	 * Selector de días ("Conservar", "Borrar lo de más de"): opciones rápidas y "Otro…", que abre un
	 * campo para escribir los días (1 a 3650). Un valor que no está entre las opciones se ve en el campo.
	 */
	function daysPicker(key, value, { zero = false, disabled = false } = {}) {
		const ui = (S.maint.daysCustom ||= {});
		const custom = ui[key] || (value > 0 && !DAY_PRESETS.includes(value));
		const off = disabled ? "disabled" : "";
		const opts = [...(zero ? [0] : []), ...DAY_PRESETS].map((d) => `<option value="${d}" ${!custom && d === value ? "selected" : ""}>${retentionText(d)}</option>`).join("");
		return `<span class="days-pick"><select data-days-pick="${key}" ${off}>${opts}<option value="custom" ${custom ? "selected" : ""}>${tr("Otro…")}</option></select>
			${custom ? `<input type="text" inputmode="numeric" pattern="[0-9]*" maxlength="4" class="days-input" data-days-input="${key}" value="${value > 0 ? value : ""}" ${off}
				aria-label="${esc(tr("Días (de 1 a {n})", { n: MAX_DAYS }))}" placeholder="${esc(tr("días"))}"><span class="days-unit">${tr("días")}</span>` : ""}</span>`;
	}
	/** Los días escritos en "Otro…": un entero de 1 a 3650, o null. */
	function typedDays(input) {
		const v = input.value.trim(), n = Number(v);
		return /^[0-9]+$/.test(v) && n >= 1 && n <= MAX_DAYS ? n : null;
	}

	/** Tarjeta de almacenamiento: disco, lo que ocupa la evidencia, ritmo, proyectos y retención. */
	function storageCard() {
		const m = S.maint, st = m.storage;
		if (m.storageError) return `<div class="card placeholder">${tr("No se pudo leer el almacenamiento: {e}", { e: m.storageError })}</div>`;
		if (!st) return `<div class="card placeholder">${tr("Midiendo el almacenamiento…")}</div>`;
		const ret = st.retention, disk = st.disk;
		const pct = disk ? Math.round(disk.used_percent) : null;
		const meter = disk ? `<div class="stor-meter ${st.warn ? "warn" : ""}" role="meter" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${pct}" aria-label="${esc(tr("Disco en uso"))}">
				<i style="width:${Math.min(100, pct)}%"></i></div>
			<p class="stor-disk">${tr("Disco: {p} % en uso · {free} libres de {total}", { p: pct, free: fmtBytes(disk.free), total: fmtBytes(disk.total) })}</p>` : "";
		const warn = st.warn ? `<p class="stor-warn" role="alert">${icon("i-warning")}${tr("El disco de los datos está al {u} % (más del {p} %) y TraceReports ocupa {b} de él. Si lo que crece es la evidencia, elige cuántos días conservar o limpia ahora; si no, libera espacio en el servidor.", { u: pct, p: st.warn_percent, b: fmtBytes(st.total_bytes) })}</p>` : "";
		const full = st.days_until_full !== undefined && st.days_until_full < 3650
			? tr("~{n} días", { n: st.days_until_full }) : st.bytes_per_day ? tr("más de 10 años") : "—";
		const stats = `<dl class="stor-stats">
			<div><dt>${tr("Evidencia guardada")}</dt><dd>${fmtBytes(st.total_bytes)}</dd><small>${tr("Base {db} · capturas y adjuntos {files}", { db: fmtBytes(st.db_bytes), files: fmtBytes(st.files_bytes) })}</small></div>
			<div><dt>${tr("Ejecuciones")}</dt><dd>${st.runs}</dd><small>${st.oldest_run ? tr("desde {d}", { d: fmtDateTime(st.oldest_run) }) : "—"}</small></div>
			<div><dt>${tr("Ritmo")}</dt><dd>${st.bytes_per_day ? tr("~{b}/día", { b: fmtBytes(st.bytes_per_day) }) : "—"}</dd><small>${tr("promedio desde la ejecución más antigua")}</small></div>
			<div><dt>${tr("Se llenaría en")}</dt><dd>${disk ? full : "—"}</dd><small>${tr("a este ritmo, sin limpiar")}</small></div>
		</dl>`;
		const orphan = st.orphan_bytes > 0 ? `<p class="m-hint">${tr("{b} son archivos que ninguna ejecución usa (de borrados interrumpidos o versiones anteriores).", { b: fmtBytes(st.orphan_bytes) })}</p>` : "";
		const projects = st.projects?.length ? `<details class="stor-projects"><summary>${tr("Qué ocupa cada proyecto")}</summary>
			<table class="stor-table"><thead><tr><th>${tr("Proyecto")}</th><th>${tr("Ejecuciones")}</th><th>Tests</th><th>${tr("Espacio (aprox.)")}</th></tr></thead>
			<tbody>${st.projects.map((p) => `<tr><td data-no-i18n>${esc(p.project || tr("(sin proyecto)"))}</td><td>${p.runs}</td><td>${p.tests}</td><td>${fmtBytes(p.bytes)}</td></tr>`).join("")}</tbody></table>
			${st.more_projects ? `<small>${tr("y {n} proyecto(s) más", { n: st.more_projects })}</small>` : ""}</details>` : "";
		// las páginas del Inspector (TRACEREPORTS_DOM_STEPS): lo que más crece; por ejecución, las más pesadas
		const dom = st.dom?.pages ? `<details class="stor-projects stor-dom"><summary>${tr("Páginas del Inspector: {b} en {n} páginas", { b: fmtBytes(st.dom.bytes), n: st.dom.pages })}</summary>
			<p class="m-hint">${tr("Se guardan comprimidas y los íconos una sola vez ({g} compartidos). Las guardadas con una versión anterior se comprimen al compactar.", { g: fmtBytes(st.dom.glyph_bytes || 0) })}</p>
			<table class="stor-table"><thead><tr><th>${tr("Ejecución")}</th><th>${tr("Páginas")}</th><th>${tr("Espacio")}</th></tr></thead>
			<tbody>${(st.dom.runs || []).map((r) => `<tr><td><a href="#run=${r.run_id}&view=tests" data-no-i18n>#${r.run_id} ${esc(r.name)}</a></td><td>${r.pages}</td><td>${fmtBytes(r.bytes)}</td></tr>`).join("")}</tbody></table></details>` : "";
		const last = (ret.last ? tr("Última limpieza: {d} · {r} ejecución(es) · liberó {b}", { d: fmtDateTime(ret.last.at), r: ret.last.runs, b: fmtBytes(ret.last.freed_bytes) }) : tr("Todavía no se limpió nada."))
			+ (ret.last?.error ? ` · <span class="stor-cut">${tr("se cortó con un error:")} <span data-no-i18n>${esc(ret.last.error)}</span></span>` : "");
		const next = ret.days && ret.next_at ? ` · ${tr("Próxima: {d}", { d: fmtDateTime(ret.next_at) })}` : "";
		const retention = `<div class="stor-retention">
			<label class="m-suite"><span>${tr("Conservar")}</span>${daysPicker("retention", ret.days, { zero: true, disabled: !ret.editable })}</label>
			<p>${ret.days ? tr("Cada {h} h se borran las ejecuciones terminadas de más de {n} días, con sus capturas.", { h: ret.every_hours, n: ret.days }) : tr("Sin retención: la evidencia se guarda para siempre.")}
				${ret.source === "env" ? `<small>${tr("Lo fija TRACEREPORTS_RETENTION_DAYS en el servidor.")}</small>` : ""}
				<small>${last}${next}</small></p>
			${ret.can_run ? `<button class="cf-btn cf-btn-sm cf-btn-danger-outline" data-stor-clean ${ret.running || m.clean ? "disabled" : ""}>${icon("i-close")}${ret.running ? tr("Limpiando…") : tr("Limpiar ahora…")}</button>` : ""}
		</div>`;
		return `<section class="card stor-card" aria-label="${esc(tr("Almacenamiento"))}">
			<header class="stor-head"><span class="maint-stale-icon">${icon("i-server")}</span><b>${tr("Almacenamiento")}</b></header>
			${warn}${meter}${stats}${orphan}${projects}${dom}${retention}${cleanConfirm()}${compactRow(st.compact)}
		</section>`;
	}

	/**
	 * Compactar la base: devuelve al disco el espacio de lo borrado. Reescribe toda la base (minutos en
	 * una grande, con la escritura tomada), así que no se hace al borrar: queda pendiente y se hace sola
	 * cuando no hay ejecuciones en curso, o a mano desde aquí, con una confirmación que dice qué pide.
	 */
	function compactRow(c) {
		if (!c) return "";
		const m = S.maint, k = m.compact, busy = c.running || k?.busy;
		const last = c.last ? tr("Última: {d} · liberó {b} en {s}", { d: fmtDateTime(c.last.at), b: fmtBytes(c.last.freed_bytes), s: fmtSecs(c.last.took_ms) }) : tr("Todavía no se compactó.");
		const state = c.pending && c.reclaimable_bytes > 0
			? tr("Hay ~{b} de lo borrado por devolver al disco. Se compacta sola cuando no hay ejecuciones en curso y los clientes llevan {m} min sin enviar.", { b: fmtBytes(c.reclaimable_bytes), m: c.quiet_minutes })
			: tr("Nada por devolver al disco.");
		const btn = c.can_run ? `<button class="cf-btn cf-btn-sm" data-stor-compact ${busy || k || !(c.reclaimable_bytes > 0) ? "disabled" : ""}>${busy ? `<span class="maint-spin" aria-hidden="true"></span>${tr("Compactando…")}` : `${icon("i-server")}${tr("Compactar ahora…")}`}</button>` : "";
		let confirm = "";
		if (k) {
			const room = c.free_bytes === undefined || c.free_bytes >= c.need_bytes;
			const off = k.busy ? "disabled" : "";
			confirm = `<div class="card maint-confirm" role="alertdialog" aria-label="${esc(tr("Compactar ahora"))}" ${k.busy ? 'aria-busy="true"' : ""}>
				<p><b>${tr("Se reescribe la base ({db}) para devolver ~{b} al disco.", { db: fmtBytes(m.storage.db_bytes), b: fmtBytes(c.reclaimable_bytes || 0) })}</b></p>
				<p>${tr("Necesita ~{n} libres en el disco de los datos (hay {f}). Mientras dura, los clientes esperan para guardar sus resultados: hazlo sin ejecuciones en curso.", { n: fmtBytes(c.need_bytes), f: c.free_bytes === undefined ? "—" : fmtBytes(c.free_bytes) })}</p>
				${c.running_runs ? `<p class="stor-warn">${icon("i-warning")}${tr("Hay {n} ejecución(es) en curso: sus clientes esperarán hasta que termine.", { n: c.running_runs })}</p>` : ""}
				${!room ? `<p class="set-msg err" role="alert">${tr("No hay espacio suficiente para compactar: libera espacio o borra más ejecuciones antes.")}</p>` : ""}
				${k.error ? `<p class="set-msg err" role="alert">${esc(k.error)}</p>` : ""}
				${k.busy ? `<p class="maint-progress" role="status"><span class="maint-spin" aria-hidden="true"></span>${esc(tr("Compactando la base… con una base grande puede tardar varios minutos; no cierres esta pestaña si quieres ver el resultado."))}</p>` : ""}
				<div class="close-run-actions"><button type="button" class="cf-btn cf-btn-sm cf-btn-primary" data-stor-compact-go ${k.busy || !room ? "disabled" : ""}>${k.busy ? `<span class="maint-spin" aria-hidden="true"></span>${tr("Compactando…")}` : tr("Compactar")}</button>
					<button type="button" class="cf-btn cf-btn-sm" data-stor-compact-cancel ${off}>${tr("Cancelar")}</button></div></div>`;
		}
		return `<div class="stor-retention stor-compact"><p><b>${tr("Compactar la base")}</b> ${esc(state)}<small>${last}</small></p>${btn}</div>${confirm}`;
	}

	/** Confirmación de "Limpiar ahora": cuántos días conservar y qué se borraría (vista previa). */
	function cleanConfirm() {
		const c = S.maint.clean;
		if (!c) return "";
		const p = c.preview;
		// un error no borra la vista previa: se ve qué se intentó y por qué no se pudo
		const err = c.error ? `<p class="set-msg err" role="alert">${esc(c.error)}</p>` : "";
		const body = err + (!p && c.error ? "" : !p ? `<p>${tr("Calculando qué se borraría…")}</p>`
			: !p.runs ? `<p>${tr("No hay ejecuciones terminadas de más de {n} días: no se borraría nada.", { n: c.days })}</p>`
			: `<p><b>${tr("Se borrarán {r} ejecución(es) terminadas de más de {n} días ({t} tests) y se liberarán ~{b} (lo de la base, al compactarla). No se puede deshacer.", { r: p.runs, n: c.days, t: p.tests, b: fmtBytes(p.freed_bytes_approx) })}</b></p>`);
		// mientras limpia: dice qué pasa, nada se puede cambiar y un segundo clic no lo repite
		const busy = c.busy, off = busy ? "disabled" : "";
		const progress = busy ? `<p class="maint-progress" role="status"><span class="maint-spin" aria-hidden="true"></span>${esc(tr("Limpiando {r} ejecución(es) de más de {n} días… se borran de a tandas y los clientes siguen enviando; con una base grande puede tardar unos minutos.", { r: p?.runs || 0, n: c.days }))}</p>` : "";
		return `<div class="card maint-confirm danger" role="alertdialog" aria-label="${esc(tr("Limpiar ahora"))}" ${busy ? 'aria-busy="true"' : ""}>
			<div class="clean-days"><label class="m-suite"><span>${tr("Borrar lo de más de")}</span>${daysPicker("clean", c.days, { disabled: busy })}</label>
				<small class="m-hint" data-clean-cutoff>${tr("Se borra lo que empezó antes del {d}", { d: fmtDateTime(Date.now() - c.days * 864e5) })}${S.maint.storage?.oldest_run ? ` · ${tr("la ejecución más antigua es del {d}", { d: fmtDateTime(S.maint.storage.oldest_run) })}` : ""}</small></div>
			${body}${progress}
			<div class="close-run-actions"><button type="button" class="cf-btn cf-btn-sm cf-btn-danger" data-stor-clean-go ${busy || !p?.runs ? "disabled" : ""}>${busy ? `<span class="maint-spin" aria-hidden="true"></span>${tr("Limpiando…")}` : tr("Limpiar")}</button>
				<button type="button" class="cf-btn cf-btn-sm" data-stor-clean-cancel ${off}>${tr("Cancelar")}</button></div></div>`;
	}

	/** Aplica los días elegidos: en "Limpiar ahora", la vista previa; en "Conservar", se guarda. */
	async function applyDays(key, days) {
		const m = S.maint;
		if (key === "clean") {
			if (m.clean && !m.clean.busy && !(days === m.clean.days && m.clean.preview)) { m.clean.days = days; await previewClean(); }
			return;
		}
		if (key !== "retention" || days === m.storage?.retention?.days) return;
		try {
			const r = await apiSend("PUT", "/api/v1/ui/maintenance/retention", { days });
			m.msg = { ok: true, text: r.days ? tr("Listo: se conservarán los últimos {n} días; lo más antiguo se borra en la próxima limpieza.", { n: r.days }) : tr("Listo: la evidencia se guarda para siempre.") };
		} catch (err) { m.msg = { ok: false, text: err.message }; }
		await loadStorage();
	}

	async function previewClean() {
		const c = S.maint.clean;
		if (!c) return;
		Object.assign(c, { preview: null, error: null });
		renderMaintenance();
		const days = c.days;
		try {
			const p = await api(`/api/v1/maintenance/storage/preview?days=${days}`);
			if (S.maint.clean === c && c.days === days) c.preview = p;
		} catch (err) { c.error = err.message; }
		renderMaintenance();
	}

	function renderMaintenance() {
		const el = $("#view-maintenance"), m = S.maint;
		if (!m.data && !m.loading && !m.error) loadMaintenance();
		const act = canAct();
		const runs = m.data?.runs || [];
		const sel = runs.filter((r) => m.selected.has(r.id));
		const st = m.data?.stale;
		const facet = (key, label) => `<label class="m-suite"><span>${tr(label)}</span><select data-maint-filter="${key}"><option value="">${tr("Todos")}</option>
			${(m.facets?.[key] || []).map((x) => `<option value="${esc(x.value)}" ${x.value === m[key] ? "selected" : ""} data-no-i18n>${esc(x.value)}</option>`).join("")}</select></label>`;
		const choices = st ? [...new Set([...STALE_CHOICES, st.hours])] : [];
		const stale = st ? `<div class="card maint-stale">
				<span class="maint-stale-icon">${icon("i-pause")}</span>
				<div class="maint-stale-text"><b>${tr("Cierre automático por inactividad")}</b>
					<p>${tr("Una ejecución en curso que deja de recibir datos se cierra sola, y sus tests sin terminar quedan como Interrumpido.")}</p>
					${st.source === "env" ? `<small>${tr("Lo fija TRACEREPORTS_STALE_RUN_HOURS en el servidor.")}</small>` : ""}</div>
				<label class="m-suite"><span>${tr("Cerrar tras")}</span><select data-maint-stale ${st.editable && act ? "" : "disabled"}>
					${choices.map((h) => `<option value="${h}" ${h === st.hours ? "selected" : ""}>${staleText(h)}</option>`).join("")}</select></label>
			</div>` : "";
		const table = m.error ? `<div class="card placeholder">${tr("No se pudo cargar: {e}", { e: m.error })}</div>`
			: !m.data ? `<div class="card placeholder">${tr("Cargando…")}</div>`
			: !runs.length ? `<div class="card m-empty">${icon("i-pass")}<h5>${tr("Nada que revisar")}</h5><p>${tr("No hay ejecuciones con este filtro.")}</p></div>`
			: `<div class="card maint-list">
				${act ? `<div class="maint-bulk" role="toolbar" aria-label="${tr("Acciones en lote")}">
					<span>${tr("{n} de {t} elegidas", { n: sel.length, t: runs.length })}</span>
					<button class="cf-btn cf-btn-sm" data-maint-act="close" ${!m.busy && sel.some((r) => r.status === "RUNNING") ? "" : "disabled"}>${icon("i-interrupted")}${tr("Cerrar como interrumpidas…")}</button>
					<button class="cf-btn cf-btn-sm cf-btn-danger-outline" data-maint-act="delete" ${!m.busy && sel.some((r) => r.status !== "RUNNING") ? "" : "disabled"}>${icon("i-close")}${tr("Eliminar…")}</button>
				</div>${maintAction(sel)}` : ""}
				<div class="maint-scroll"><table class="maint-table"><thead><tr>
					${act ? `<th><input type="checkbox" data-maint-all ${sel.length && sel.length === runs.length ? "checked" : ""} ${m.busy ? "disabled" : ""} aria-label="${tr("Elegir todas")}"></th>` : ""}
					<th>${tr("Ejecución")}</th><th>${tr("Proyecto · ambiente · rama")}</th><th>${tr("Estado")}</th><th>Tests</th><th>${tr("Inicio")}</th><th>${tr("Cierre")}</th>
				</tr></thead><tbody>${runs.map((r) => maintRow(r, act)).join("")}</tbody></table></div>
			</div>`;
		const typing = document.activeElement?.matches?.("[data-days-input]") ? { key: document.activeElement.dataset.daysInput, at: document.activeElement.selectionStart } : null;
		setHTML(el, `<div class="page-head"><div><h4 class="page-title">${tr("Mantenimiento")}</h4>
				<p class="page-sub">${tr("Cuánto ocupa la evidencia y cuánto se conserva, y las ejecuciones que quedaron colgadas o se cortaron, de todos los proyectos.")}</p></div></div>
			${storageCard()}
			${connectionsCard()}
			${stale}
			<div class="m-filters" role="toolbar" aria-label="${tr("Filtros de mantenimiento")}">
				<div class="seg" role="group" aria-label="${tr("Estado")}">${MAINT_STATES.map(([id, label, tip]) =>
					`<button data-maint-state="${id}" aria-pressed="${m.state === id}" data-tip="${esc(tr(tip))}">${tr(label)}</button>`).join("")}</div>
				${facet("project", "Proyecto")}${facet("environment", "Ambiente")}${facet("branch", "Rama")}
				<label class="m-suite"><span>${tr("Antigüedad")}</span><select data-maint-filter="older">
					${MAINT_OLDER.map(([v, label]) => `<option value="${v}" ${v === m.older ? "selected" : ""}>${tr(label)}</option>`).join("")}</select></label>
			</div>
			${m.msg ? `<p class="set-msg ${m.msg.ok ? "ok" : "err"}" role="status">${esc(m.msg.text)}</p>` : ""}
			${table}`);
		if (typing) {
			const inp = el.querySelector(`[data-days-input="${typing.key}"]`);
			if (inp && document.activeElement !== inp) { inp.focus(); inp.setSelectionRange(typing.at, typing.at); }
		}
		// "Ver conexiones →" del aviso de un run: se llega a esa tarjeta
		if (m.focusConn && m.conn?.data) { m.focusConn = false; $("#maint-connections")?.scrollIntoView({ block: "start", behavior: "smooth" }); }
	}

	async function maintRun(path, body, done) {
		const m = S.maint;
		if (m.busy) return; // ya hay una en curso: un segundo clic (o Enter) no la repite
		m.busy = { kind: path.endsWith("/delete") ? "delete" : "close", n: body.run_ids.length };
		renderMaintenance();
		try {
			const r = await apiSend("POST", path, body);
			m.msg = { ok: true, text: done(r) };
			const gone = path.endsWith("/delete") && body.run_ids.includes(S.runId);
			Object.assign(m, { action: null, selected: new Set() });
			if (gone) Object.assign(S, { runId: null, run: null, testId: null, test: null });
			await loadRuns();
			if (S.runId && (gone || body.run_ids.includes(S.runId))) await loadRun().catch(() => {});
		} catch (err) {
			m.msg = { ok: false, text: err.message };
		}
		m.busy = false;
		m.closeForm = null;
		await loadMaintenance();
		renderView();
	}

	function bindMaintenance() {
		const el = $("#view-maintenance"), m = S.maint;
		el.addEventListener("toggle", (e) => {
			const d = e.target.closest?.("[data-conn-sec]");
			if (d) connUI().open[d.dataset.connSec] = d.open;
		}, true);
		el.addEventListener("click", (e) => {
			if (e.target.closest("[data-conn-reload]")) { m.conn = null; renderMaintenance(); loadConnections(); return; }
			const cm = e.target.closest("[data-conn-more]");
			if (cm) { const u = connUI(); u.more[cm.dataset.connMore] = (u.more[cm.dataset.connMore] || 0) + 20; renderMaintenance(); return; }
			const cx = e.target.closest("[data-conn-exec]");
			if (cx) { const u = connUI(), k = cx.dataset.connExec; u.exec[k] = !u.exec[k]; renderMaintenance(); el.querySelector(`[data-conn-exec="${CSS.escape(k)}"]`)?.focus(); return; }
			const cj = e.target.closest("[data-conn-jump]");
			if (cj) {
				connUI().open[cj.dataset.connJump] = true; renderMaintenance();
				const sec = el.querySelector(`[data-conn-sec="${cj.dataset.connJump}"]`);
				sec?.scrollIntoView({ block: "start", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
				sec?.querySelector("summary")?.focus({ preventScroll: true });
				return;
			}
			const st = e.target.closest("[data-maint-state]");
			if (st) { Object.assign(m, { state: st.dataset.maintState, msg: null, action: null }); loadMaintenance(); renderMaintenance(); return; }
			if (m.busy && e.target.closest("[data-maint-act], [data-maint-cancel], [data-maint-delete]")) return;
			const a = e.target.closest("[data-maint-act]");
			if (a) { m.action = a.dataset.maintAct; m.msg = null; renderMaintenance(); return; }
			if (e.target.closest("[data-maint-cancel]")) { m.action = null; renderMaintenance(); return; }
			if (e.target.closest("[data-stor-clean]")) {
				m.clean = { days: m.storage?.retention?.days || 90, preview: null, busy: false };
				(m.daysCustom ||= {}).clean = false;
				m.msg = null;
				previewClean();
				return;
			}
			if (e.target.closest("[data-stor-compact]") && !m.compact) { m.compact = { busy: false }; m.msg = null; renderMaintenance(); return; }
			if (e.target.closest("[data-stor-compact-cancel]")) { if (!m.compact?.busy) { m.compact = null; renderMaintenance(); } return; }
			if (e.target.closest("[data-stor-compact-go]") && m.compact && !m.compact.busy) {
				const k = m.compact;
				Object.assign(k, { busy: true, error: null });
				renderMaintenance();
				apiSend("POST", "/api/v1/ui/maintenance/compact", {}).then(async (r) => {
					m.compact = null;
					m.msg = { ok: true, text: tr("Listo: la base se compactó y devolvió {b} al disco ({s}).", { b: fmtBytes(r.freed_bytes), s: fmtSecs(r.took_ms) }) };
					await loadStorage();
				}).catch((err) => { k.busy = false; k.error = err.message; renderMaintenance(); });
				return;
			}
			if (e.target.closest("[data-stor-clean-cancel]")) { if (!m.clean?.busy) { m.clean = null; renderMaintenance(); } return; }
			if (e.target.closest("[data-stor-clean-go]") && m.clean && !m.clean.busy) {
				const c = m.clean;
				c.busy = true;
				c.error = null;
				renderMaintenance();
				apiSend("POST", "/api/v1/ui/maintenance/retention/run", { days: c.days }).then(async (r) => {
					m.clean = null;
					m.msg = { ok: true, text: tr("Listo: se borraron {r} ejecución(es) y se liberaron {b}.", { r: r.runs, b: fmtBytes(r.freed_bytes) }) };
					if (S.runId && !(await api(`/api/v1/runs/${S.runId}`).catch(() => null))) Object.assign(S, { runId: null, run: null, testId: null, test: null });
					await loadRuns();
					await loadMaintenance();
				}).catch((err) => { c.busy = false; c.error = err.message; renderMaintenance(); });
				return;
			}
			if (e.target.closest("[data-maint-delete]")) {
				const ids = (m.data?.runs || []).filter((r) => m.selected.has(r.id) && r.status !== "RUNNING").map((r) => r.id);
				maintRun("/api/v1/ui/runs/delete", { run_ids: ids }, (r) => tr("Se eliminaron {n} ejecución(es).", { n: r.deleted }));
			}
		});
		el.addEventListener("change", async (e) => {
			const pick = e.target.closest("[data-maint-pick]");
			if (pick) { const id = Number(pick.dataset.maintPick); pick.checked ? m.selected.add(id) : m.selected.delete(id); renderMaintenance(); return; }
			if (e.target.closest("[data-maint-all]")) {
				m.selected = e.target.checked ? new Set((m.data?.runs || []).map((r) => r.id)) : new Set();
				renderMaintenance(); return;
			}
			const f = e.target.closest("[data-maint-filter]");
			if (f) { m[f.dataset.maintFilter] = f.value; m.msg = null; loadMaintenance(); return; }
			const dp = e.target.closest("[data-days-pick]");
			if (dp) {
				const key = dp.dataset.daysPick, ui = (m.daysCustom ||= {});
				if (dp.value === "custom") { // abre el campo con lo que había, listo para escribir
					ui[key] = true;
					renderMaintenance();
					const inp = el.querySelector(`[data-days-input="${key}"]`);
					inp?.focus();
					inp?.select();
					return;
				}
				ui[key] = false;
				await applyDays(key, Number(dp.value));
				return;
			}
			const di = e.target.closest("[data-days-input]");
			if (di) {
				const n = typedDays(di);
				if (n === null) { di.setCustomValidity(tr("Escribe un número de días entre 1 y {n}.", { n: MAX_DAYS })); di.reportValidity(); return; }
				di.setCustomValidity("");
				await applyDays(di.dataset.daysInput, n);
				return;
			}
			const stale = e.target.closest("[data-maint-stale]");
			if (stale) {
				try {
					const s = await apiSend("PUT", "/api/v1/ui/maintenance/stale", { hours: Number(stale.value) });
					m.data.stale = s;
					m.msg = { ok: true, text: s.hours ? tr("Listo: se cerrarán solas tras {t} sin actividad.", { t: staleText(s.hours) }) : tr("Listo: el cierre automático quedó apagado.") };
				} catch (err) { m.msg = { ok: false, text: err.message }; }
				renderMaintenance();
			}
		});
		// al limpiar, la vista previa sigue lo que se escribe (sin esperar a salir del campo)
		let typed = 0;
		el.addEventListener("input", (e) => {
			const di = e.target.closest("[data-days-input]");
			if (!di) return;
			di.setCustomValidity("");
			if (di.dataset.daysInput !== "clean") return;
			clearTimeout(typed);
			typed = setTimeout(() => { const n = typedDays(di); if (n !== null && m.clean && !m.clean.busy && n !== m.clean.days) applyDays("clean", n); }, 350);
		});
		el.addEventListener("keydown", (e) => {
			const di = e.target.closest("[data-days-input]");
			if (di && e.key === "Enter") { e.preventDefault(); di.dispatchEvent(new Event("change", { bubbles: true })); }
		});
		el.addEventListener("submit", (e) => {
			const f = e.target.closest("[data-maint-close-form]");
			if (!f) return;
			e.preventDefault();
			if (m.busy) return;
			const d = new FormData(f), detail = String(d.get("detail") || "").trim();
			m.closeForm = { reason: String(d.get("reason")), detail };
			const ids = (m.data?.runs || []).filter((r) => m.selected.has(r.id) && r.status === "RUNNING").map((r) => r.id);
			maintRun("/api/v1/ui/runs/close", { run_ids: ids, reason: tr(String(d.get("reason"))) + (detail ? `: ${detail}` : "") },
				(r) => tr("Se cerraron {n} ejecución(es) como interrumpidas.", { n: r.closed }));
		});
	}

	function renderMetrics() {
		const el = $("#view-metrics");
		const m = S.metrics;
		if (!m.data && !m.loading && !m.error) { loadMetrics(); }
		const suites = m.data?.suites || [];
		const headChanged = setHTML(el, `<div class="page-head">
				<div><h4 class="page-title">Métricas de calidad</h4>
				<p class="page-sub">Todas las ejecuciones terminadas del período, no solo la actual. Para ver la tendencia, detectar los tests que más tiempo hacen perder y decidir qué estabilizar primero.</p></div>
				${canAct() ? `<button class="cf-btn cf-btn-sm" data-weekly data-tip="${tr("El resumen de los últimos 7 días para el equipo: tendencia, lo que más falla y lo que se arregló. Con TRACEREPORTS_WEEKLY_SUMMARY se envía solo a Teams/Slack cada semana.")}">${icon("i-megaphone")}${tr("Resumen semanal")}</button>` : ""}
			</div>
			<div class="m-filters" role="toolbar" aria-label="Filtros de métricas">
				<div class="seg" role="group" aria-label="Período">
					${[7, 30, 90].map((d) => `<button data-m-days="${d}" aria-pressed="${!m.custom && m.days === d}" data-tip="Ejecuciones de los últimos ${d} días">${d} días</button>`).join("")}
					<button data-m-custom aria-pressed="${!!m.custom}" data-tip="Elige un rango de fechas">${icon("i-timeline")}Rango</button>
				</div>
				${m.custom ? `<div class="m-range"><input type="date" id="m-from" value="${esc(m.custom.from)}" max="${esc(m.custom.to)}" aria-label="Desde">
					<span aria-hidden="true">→</span><input type="date" id="m-to" value="${esc(m.custom.to)}" min="${esc(m.custom.from)}" aria-label="Hasta"></div>` : ""}
				<label class="m-suite" data-tip="Una suite es el nombre de la ejecución (start_run). Filtra para ver la calidad de una sola.">
					<span>Suite</span>
					<select id="m-suite"><option value="">Todas las suites</option>${suites.map((s) => `<option value="${esc(s)}" ${s === m.suite ? "selected" : ""}>${esc(s)}</option>`).join("")}</select>
				</label>
				<label class="m-suite" data-tip="El ambiente con que se reportó la ejecución (start_run environment).">
					<span>Ambiente</span>
					<select id="m-env"><option value="">Todos</option>${(m.data?.envs || []).map((s) => `<option value="${esc(s)}" ${s === m.env ? "selected" : ""}>${esc(s)}</option>`).join("")}</select>
				</label>
				<label class="m-suite" data-tip="Solo los tests con ese tag (category). Las ejecuciones y la tendencia se recalculan con ellos.">
					<span>Tag</span>
					<select id="m-tag"><option value="">Todos</option>${(m.data?.tag_set || []).map((s) => `<option value="${esc(s)}" ${s === m.tag ? "selected" : ""}>${esc(s)}</option>`).join("")}</select>
				</label>
				${m.suite || m.env || m.tag || m.custom ? `<button class="link-btn" data-m-clear>${icon("i-close")}Limpiar filtros</button>` : ""}
			</div>
			<div id="m-body"></div>`);
		if (headChanged) S.rendered["m-body"] = null; // #m-body es nuevo: hay que llenarlo
		const body = $("#m-body");
		if (m.error) { body.innerHTML = `<div class="card placeholder">No se pudieron cargar las métricas: ${esc(m.error)}</div>`; return; }
		if (!m.data) { body.innerHTML = `<div class="card placeholder">Cargando…</div>`; return; }
		body.classList.toggle("is-loading", m.loading);
		const d = m.data;
		const key = JSON.stringify([d.from, d.to, d.days, d.suite, d.env, d.tag, d.current, d.runs.length, I18N.lang]);
		if (S.rendered["m-body"] === key) return;
		S.rendered["m-body"] = key;
		if (!d.current.runs) {
			body.innerHTML = `<div class="card m-empty">${icon("i-chart")}<h5>${d.custom ? tr("Sin ejecuciones terminadas en ese rango con estos filtros") : `Sin ejecuciones terminadas en los últimos ${d.days} días`}</h5>
				<p>Las métricas se calculan con las ejecuciones cerradas (end_run / FinishRun). Prueba un período más largo u otra suite.</p></div>`;
			return;
		}
		body.innerHTML = kpiTiles(d) + `
			<div class="m-grid">
				<div class="card m-card m-wide">
					<div class="m-card-head"><h5>Tasa de éxito por día</h5><span class="m-hint">% de tests que pasaron (sin contar omitidos) · clic en un día para ver sus ejecuciones</span></div>
					<div class="m-chart m-chart-line"><canvas id="m-rate" role="img" aria-label="Tasa de éxito por día"></canvas></div>
					<div class="m-card-head m-sub"><h5>Tests fallidos por día</h5></div>
					<div class="m-chart m-chart-bars"><canvas id="m-fail" role="img" aria-label="Tests fallidos por día"></canvas></div>
					<details class="m-table-toggle"><summary>Ver como tabla</summary>${dailyTable(d)}</details>
				</div>
				${causesCard(d)}
				${tagsCard(d)}
				${failingCard(d)}
				${flakyCard(d)}
				${slowestCard(d)}
			</div>`;
		renderMetricsCharts();
	}

	/** Variación contra el período anterior: el color dice si es buena o mala noticia. */
	function delta(cur, prev, { unit = "", upIsGood = true, fmt = (v) => fmtNum(v, 1), hasPrev }) {
		if (!hasPrev) return `<span class="kpi-delta none">sin datos del período anterior</span>`;
		const diff = cur - prev;
		if (Math.abs(diff) < 0.05) return `<span class="kpi-delta flat">= igual que el período anterior</span>`;
		const good = diff > 0 === upIsGood;
		return `<span class="kpi-delta ${good ? "good" : "bad"}">${diff > 0 ? "▲" : "▼"} ${fmt(Math.abs(diff))}${unit} vs ${S.metrics.days} días anteriores</span>`;
	}

	function kpiTiles(d) {
		const c = d.current, p = d.previous, hasPrev = p.runs > 0;
		const tile = (label, value, dl, tip) => `<div class="card kpi" tabindex="0" data-tip="${esc(tip)}">
			<span class="kpi-label">${label}</span><span class="kpi-value">${value}</span>${dl}</div>`;
		return `<div class="kpi-grid">
			${tile("Tasa de éxito", `${fmtNum(c.pass_rate, 1)}%`, delta(c.pass_rate, p.pass_rate, { unit: " pp", hasPrev }),
				"Tests que pasaron sobre los ejecutados (sin contar omitidos). pp = puntos porcentuales.")}
			${tile("Ejecuciones", fmtNum(c.runs), delta(c.runs, p.runs, { fmt: (v) => fmtNum(v), upIsGood: true, hasPrev }),
				"Ejecuciones terminadas en el período.")}
			${tile("Tests fallidos", fmtNum(c.failed), delta(c.failed, p.failed, { fmt: (v) => fmtNum(v), upIsGood: false, hasPrev }),
				"Resultados FAIL en el período (un test que falla en 3 ejecuciones cuenta 3).")}
			${tile("Tests flaky", fmtNum(c.flaky_tests), delta(c.flaky_tests, p.flaky_tests, { fmt: (v) => fmtNum(v), upIsGood: false, hasPrev }),
				"Tests que alternan entre pasar y fallar: suelen ser esperas, datos o entorno, no bugs.")}
			${tile("Tiempo en tests fallidos", fmtHuman(c.fail_time_ms), delta(c.fail_time_ms / 60000, p.fail_time_ms / 60000, { unit: " min", upIsGood: false, hasPrev }),
				"Tiempo de ejecución consumido por tests que terminaron fallando: lo que cuesta no estabilizarlos.")}
			${tile("Duración media por ejecución", fmtHuman(c.avg_run_ms), delta(c.avg_run_ms / 60000, p.avg_run_ms / 60000, { unit: " min", upIsGood: false, hasPrev }),
				"Cuánto tarda en promedio una ejecución completa.")}
		</div>`;
	}

	const dayLabel = (day) => new Date(`${day}T12:00:00`).toLocaleDateString(locale(), { day: "2-digit", month: "short" });

	function dailyTable(d) {
		const rows = d.daily.filter((x) => x.runs).reverse();
		return `<table class="m-table"><thead><tr><th>Día</th><th class="num">Ejecuciones</th><th class="num">Pasaron</th><th class="num">Fallaron</th><th class="num">Tasa de éxito</th></tr></thead>
			<tbody>${rows.map((x) => `<tr><td>${esc(dayLabel(x.day))}</td><td class="num">${x.runs}</td><td class="num">${x.passed}</td><td class="num">${x.failed}</td><td class="num">${fmtNum(x.pass_rate, 1)}%</td></tr>`).join("")}</tbody></table>`;
	}

	function causesCard(d) {
		const total = d.causes.reduce((a, c) => a + c.count, 0);
		const max = Math.max(1, ...d.causes.map((c) => c.count));
		const name = (c) => (c ? AI_LABEL[c] || c : "Sin diagnóstico de IA");
		return `<div class="card m-card">
			<div class="m-card-head"><h5>Causas de los fallos</h5><span class="m-hint">según el diagnóstico de la IA · ${fmtNum(total)} fallos</span></div>
			${total ? `<ul class="hbars">${d.causes.map((c) => `<li tabindex="0" data-tip="${esc(name(c.category))}: ${c.count} de ${total} fallos">
				<span class="hb-label">${esc(name(c.category))}</span>
				<span class="hb-track"><i style="width:${((c.count / max) * 100).toFixed(1)}%" class="${c.category ? "" : "muted"}"></i></span>
				<span class="hb-value">${c.count} <small>${fmtNum((c.count / total) * 100)}%</small></span></li>`).join("")}</ul>`
				: `<p class="m-none">Sin fallos en el período.</p>`}
		</div>`;
	}

	function tagsCard(d) {
		return `<div class="card m-card">
			<div class="m-card-head"><h5>Estabilidad por categoría</h5><span class="m-hint">tasa de éxito por tag, la más baja primero</span></div>
			${d.tags.length ? `<ul class="hbars">${d.tags.map((g) => `<li tabindex="0" data-tip="${esc(g.tag)}: ${g.passed} pasaron, ${g.failed} fallaron">
				<span class="hb-label">${esc(g.tag)}</span>
				<span class="hb-track"><i style="width:${g.pass_rate}%" class="${g.pass_rate < 80 ? "bad" : "good"}"></i></span>
				<span class="hb-value">${fmtNum(g.pass_rate, 1)}%</span></li>`).join("")}</ul>`
				: `<p class="m-none">Los tests no tienen categorías (tags). Se envían en start_test(category="login, smoke").</p>`}
		</div>`;
	}

	/** Nombre de test que abre su historial completo (drawer). */
	/** "proyecto · ambiente · rama" de una fila, solo con las partes que varían entre filas del mismo test. */
	function metricsContext(t) {
		const d = S.metrics.data;
		if (!d) return "";
		const rows = [...(d.top_failing || []), ...(d.flaky || []), ...(d.slowest || [])].filter((x) => x.name === t.name);
		const differs = (f) => new Set(rows.map((x) => x[f] || "")).size > 1;
		return ["project", "env", "branch"].filter((f) => t[f] && differs(f)).map((f) => t[f]).join(" · ");
	}

	function testLink(t, list, i) {
		S.metrics.tests[`${list}:${i}`] = t;
		// el mismo test en otro proyecto, ambiente o rama es otra fila: se muestra lo que las distingue
		const ctx = metricsContext(t);
		return `<button class="m-test-link" data-m-test="${list}:${i}" data-no-i18n>${esc(t.name)}</button>${ctx ? ` <small class="m-env" data-no-i18n>${esc(ctx)}</small>` : ""}`;
	}

	const goLink = (t, label) => t.last_run_id
		? `<a href="#run=${t.last_run_id}&view=tests&test=${t.last_test_id}" class="m-go" data-tip="Abrir la ejecución más reciente de este test">${label}</a>` : "";

	function failingCard(d) {
		return `<div class="card m-card m-wide">
			<div class="m-card-head"><h5>Tests que más fallan</h5><span class="m-hint">ordenados por fallos; el tiempo es lo que se ejecutó para terminar fallando</span></div>
			${d.top_failing.length ? `<div class="table-wrap"><table class="m-table"><thead><tr><th>Test</th><th class="num">Fallos</th><th class="m-hide-sm">% de fallos</th><th class="num">Tiempo en fallos</th><th class="m-hide-sm">Último error</th><th></th></tr></thead><tbody>
				${d.top_failing.map((t, i) => `<tr><td class="m-name">${testLink(t, "fail", i)}</td><td class="num">${t.fails}/${t.runs}</td>
					<td class="m-hide-sm"><span class="m-rate"><i style="width:${t.fail_rate}%"></i></span> ${fmtNum(t.fail_rate)}%</td>
					<td class="num">${fmtHuman(t.fail_time_ms)}</td>
					<td class="m-err m-hide-sm" data-no-i18n title="${esc(t.last_error)}">${esc(t.last_error || "—")}</td><td>${goLink(t, "Ver →")}</td></tr>`).join("")}
				</tbody></table></div>` : `<p class="m-none">Ningún test falló en el período.</p>`}
		</div>`;
	}

	function flakyCard(d) {
		return `<div class="card m-card">
			<div class="m-card-head"><h5>Tests inestables (flaky)</h5><span class="m-hint">alternan entre pasar y fallar</span></div>
			${d.flaky.length ? `<div class="table-wrap"><table class="m-table"><thead><tr><th>Test</th><th>Últimos resultados</th><th class="num">Cambios</th></tr></thead><tbody>
				${d.flaky.map((t, i) => `<tr><td class="m-name">${testLink(t, "flaky", i)}</td>
					<td><span tabindex="0" data-tip="${esc(t.recent.join(" · "))}">${CF.sparkline(t.recent)}</span></td>
					<td class="num" data-tip="Veces que pasó de PASS a FAIL o al revés en ${t.runs} ejecuciones">${t.flips}</td></tr>`).join("")}
				</tbody></table></div>` : `<p class="m-none">No hay tests flaky en el período.</p>`}
		</div>`;
	}

	function slowestCard(d) {
		return `<div class="card m-card">
			<div class="m-card-head"><h5>Tests más lentos</h5><span class="m-hint">duración promedio; tendencia: últimas 3 ejecuciones vs las anteriores</span></div>
			${d.slowest.length ? `<div class="table-wrap"><table class="m-table"><thead><tr><th>Test</th><th class="num">Promedio</th><th class="num">Máximo</th><th class="num">Tendencia</th></tr></thead><tbody>
				${d.slowest.map((t, i) => `<tr><td class="m-name">${testLink(t, "slow", i)}</td><td class="num">${fmtHuman(t.avg_ms)}</td><td class="num">${fmtHuman(t.max_ms)}</td>
					<td class="num">${!t.trend_pct ? "—" : `<span class="${t.trend_pct > 10 ? "trend-bad" : t.trend_pct < -10 ? "trend-good" : ""}">${t.trend_pct > 0 ? "▲" : "▼"} ${fmtNum(Math.abs(t.trend_pct))}%</span>`}</td></tr>`).join("")}
				</tbody></table></div>` : `<p class="m-none">Sin datos de duración.</p>`}
		</div>`;
	}

	/** Ejecuciones de un día (clic en el gráfico). */
	function openDayDrawer(day) {
		const d = S.metrics.data;
		const runs = d.runs.filter((r) => new Date(r.started_at).toLocaleDateString("sv") === day.day).reverse();
		const bar = (r) => `<span class="dd-bar" aria-hidden="true"><i class="pass" style="flex:${r.passed}"></i><i class="fail" style="flex:${r.failed}"></i><i class="other" style="flex:${Math.max(0, r.total - r.passed - r.failed)}"></i></span>`;
		CF.Drawer.open({
			title: tr("Ejecuciones del {d}", { d: dayLabel(day.day) }),
			subtitle: tr("{r} ejecuciones · {p} tests pasaron · {f} fallaron · {x}% de éxito", { r: day.runs, p: day.passed, f: day.failed, x: fmtNum(day.pass_rate, 1) }),
			body: runs.length ? `<ul class="dd-list">${runs.map((r) => `<li>
					<div class="dd-row"><b data-no-i18n>${esc(r.name)}</b><span class="m-hint">${fmtTime(r.started_at)} · ${fmtHuman(r.duration_ms)}</span></div>
					<div class="dd-row">${bar(r)}<span class="dd-nums">${tr("{p} pasaron · {f} fallaron", { p: r.passed, f: r.failed })}</span></div>
					<div class="dd-actions"><a class="cf-btn cf-btn-sm" href="#run=${r.id}&view=tests">${icon("i-tests")}${tr("Abrir")}</a>
						${r.failed ? `<a class="cf-btn cf-btn-sm" href="#run=${r.id}&view=ai">${icon("i-spark")}${tr("Ver análisis IA")}</a>` : ""}</div>
				</li>`).join("")}</ul>`
				: `<div class="placeholder">${tr("Ese día no tuvo ejecuciones terminadas con estos filtros.")}</div>`,
		});
	}

	/** Historial completo de un test (clic en su nombre). */
	async function openTestDrawer(t) {
		const slot = CF.Drawer.open({ title: t.name, subtitle: tr("Historial del test"), body: `<div class="placeholder">${tr("Cargando…")}</div>` });
		let hist;
		try { hist = await api(`/api/v1/tests/${t.last_test_id}/history?limit=30`); } catch (err) { slot.innerHTML = `<div class="placeholder">${esc(err.message)}</div>`; return; }
		const asc = [...hist].reverse();
		const maxMs = Math.max(1, ...asc.map((h) => h.duration_ms || 0));
		const fails = hist.filter((h) => h.status === "FAIL");
		const lastFail = fails[0];
		slot.innerHTML = `<div class="td-kpis">
				<div><span>${tr("Ejecuciones")}</span><b>${hist.length}</b></div>
				<div><span>${tr("Fallos")}</span><b>${fails.length}</b></div>
				<div><span>${tr("% de fallos")}</span><b>${fmtNum(hist.length ? (fails.length / hist.length) * 100 : 0)}%</b></div>
				<div><span>${tr("Promedio")}</span><b>${fmtHuman(t.avg_ms)}</b></div>
			</div>
			<h6 class="td-h">${tr("Duración y resultado por ejecución")} <span class="m-hint">${tr("de la más antigua a la más reciente")}</span></h6>
			<div class="td-bars" role="img" aria-label="${esc(tr("Duración y resultado por ejecución"))}">${asc.map((h) => `<a href="#run=${h.run_id}&view=tests&test=${h.test_id}" class="td-bar ${lower(h.status)}"
				style="height:${Math.max(6, ((h.duration_ms || 0) / maxMs) * 100)}%" data-tip="${esc(`${fmtDateTime(h.started_at)} · ${h.status} · ${fmtHuman(h.duration_ms || 0)}${h.error ? " · " + h.error : ""}`)}"></a>`).join("")}</div>
			<table class="m-table td-table"><thead><tr><th>${tr("Fecha")}</th><th>${tr("Resultado")}</th><th class="num">${tr("Duración")}</th><th>${tr("Causa / error")}</th></tr></thead><tbody>
				${hist.map((h) => `<tr><td><a href="#run=${h.run_id}&view=tests&test=${h.test_id}">${fmtDateTime(h.started_at)}</a></td><td>${statusLabel(h.status)}</td>
					<td class="num">${fmtHuman(h.duration_ms || 0)}</td>
					<td class="td-err" data-no-i18n>${h.category ? `<span class="ai-cat ${esc(h.category)}">${esc(h.category)}</span> ` : ""}${esc(h.error || "")}</td></tr>`).join("")}
			</tbody></table>
			<div class="dd-actions">
				<a class="cf-btn cf-btn-sm" href="#run=${t.last_run_id}&view=tests&test=${t.last_test_id}">${icon("i-tests")}${tr("Abrir la última ejecución")}</a>
				${lastFail && canAct() ? `<a class="cf-btn cf-btn-sm cf-btn-primary" href="#run=${lastFail.run_id}&view=escalate" data-esc-pick="${lastFail.test_id}">${icon("i-megaphone")}${tr("Escalar el último fallo")}</a>` : ""}
			</div>`;
	}

	/** #rrggbb -> rgba con opacidad (el canvas no entiende color-mix en todos los navegadores). */
	function alpha(color, a) {
		const m = /^#?([0-9a-f]{6})$/i.exec(color.trim());
		if (!m) return "transparent";
		const n = parseInt(m[1], 16);
		return `rgba(${n >> 16}, ${(n >> 8) & 255}, ${n & 255}, ${a})`;
	}

	/** Línea vertical que sigue al puntero (crosshair) en los gráficos de tiempo. */
	const crosshair = {
		id: "crosshair",
		afterDraw(chart) {
			const a = chart.tooltip?.getActiveElements?.();
			if (!a?.length) return;
			const x = a[0].element.x, { top, bottom } = chart.chartArea, ctx = chart.ctx;
			ctx.save();
			ctx.strokeStyle = cssVar("--text-muted");
			ctx.globalAlpha = 0.5;
			ctx.lineWidth = 1;
			ctx.beginPath(); ctx.moveTo(x, top); ctx.lineTo(x, bottom); ctx.stroke();
			ctx.restore();
		},
	};

	function renderMetricsCharts() {
		const d = S.metrics.data;
		if (!d || !$("#m-rate")) return;
		["m-rate", "m-fail"].forEach((id) => { S.charts[id]?.chart.destroy(); delete S.charts[id]; });
		if (!window.Chart) {
			$$(".m-chart").forEach((c) => (c.innerHTML = `<div class="chart-fallback">${tr("Chart.js no disponible (sin conexión a internet)")}</div>`));
			return;
		}
		const labels = d.daily.map((x) => dayLabel(x.day));
		const text = cssVar("--text-muted"), grid = cssVar("--border"), line = cssVar("--primary"), fail = cssVar("--fail");
		Chart.defaults.font.family = cssVar("--font-ui");
		const tooltip = {
			backgroundColor: cssVar("--color-tooltip-bg"), titleColor: cssVar("--color-tooltip-text"), bodyColor: cssVar("--color-tooltip-text"),
			padding: 10, displayColors: false,
		};
		// los dos gráficos se leen juntos: mismo ancho de eje Y y mismas posiciones X (offset) para que los días coincidan
		const scales = (y) => ({
			x: { offset: true, grid: { display: false }, border: { color: grid }, ticks: { color: text, maxTicksLimit: 8, maxRotation: 0, autoSkipPadding: 12 } },
			y: { ...y, grid: { color: grid, lineWidth: 1 }, border: { display: false }, ticks: { color: text, maxTicksLimit: 5, ...y.ticks },
				afterFit: (sc) => { sc.width = 46; } },
		});
		const dayClick = {
			onClick: (evt, els, chart) => {
				const pts = chart.getElementsAtEventForMode(evt, "index", { intersect: false }, false);
				if (pts.length) openDayDrawer(d.daily[pts[0].index]);
			},
			onHover: (evt, els, chart) => { chart.canvas.style.cursor = chart.getElementsAtEventForMode(evt, "index", { intersect: false }, false).length ? "pointer" : "default"; },
		};
		S.charts["m-rate"] = { chart: new Chart($("#m-rate"), {
			type: "line",
			data: { labels, datasets: [{ data: d.daily.map((x) => (x.runs ? x.pass_rate : null)), borderColor: line, borderWidth: 2,
				backgroundColor: alpha(line, 0.1), fill: "origin", spanGaps: true, tension: 0,
				pointRadius: d.daily.map((x, i) => (x.runs && (i === d.daily.length - 1 || d.daily.filter((y) => y.runs).length < 12) ? 4 : 0)),
				pointHoverRadius: 5, pointBackgroundColor: line, pointBorderColor: cssVar("--surface"), pointBorderWidth: 2 }] },
			options: {
				...dayClick, maintainAspectRatio: false, animation: false, interaction: { mode: "index", intersect: false },
				scales: scales({ min: 0, max: 100, ticks: { callback: (v) => `${v}%` } }),
				plugins: { legend: { display: false }, tooltip: { ...tooltip, callbacks: {
					label: (c) => (c.raw == null ? tr("sin ejecuciones") : `${fmtNum(c.raw, 1)}% ${tr("de éxito")} · ${d.daily[c.dataIndex].runs} ${tr("ejecuciones")}`) } } },
			},
			plugins: [crosshair],
		}) };
		S.charts["m-fail"] = { chart: new Chart($("#m-fail"), {
			type: "bar",
			data: { labels, datasets: [{ data: d.daily.map((x) => x.failed), backgroundColor: fail, hoverBackgroundColor: fail,
				borderRadius: { topLeft: 4, topRight: 4 }, borderSkipped: "start", maxBarThickness: 24, categoryPercentage: 0.8, barPercentage: 0.9 }] },
			options: {
				...dayClick, maintainAspectRatio: false, animation: false, interaction: { mode: "index", intersect: false },
				scales: scales({ beginAtZero: true, ticks: { precision: 0 } }),
				plugins: { legend: { display: false }, tooltip: { ...tooltip, callbacks: { label: (c) => `${c.raw} ${tr("tests fallidos")}` } } },
			},
		}) };
	}

	// ---------- ajustes ----------
	const AI_LANGS = [["auto", "Automático (el idioma del equipo)"], ["es", "Español"], ["en", "English"]];
	const LANG_NAME = { es: "español", en: "inglés" };

	/** En qué idioma salen los diagnósticos ahora y de dónde viene esa decisión (Ajustes, .env, equipo). */
	function aiLanguageHelp(d) {
		const eff = d.ai_language_effective;
		const from = d.ai_language !== "auto" ? "" : d.ai_language_env ? tr("definido en el .env con TRACEREPORTS_AI_LANGUAGE")
			: eff ? tr("el idioma por defecto del equipo, de Ajustes → Apariencia") : "";
		const now = eff ? tr("Los diagnósticos se escriben en {l}", { l: tr(LANG_NAME[eff]) }) + (from ? ` (${from}).` : ".")
			: tr("Sin un idioma definido, la IA usa el de cada test (casi siempre inglés, por los nombres de los tests).");
		const locked = !d.editable && d.edit_reason === "locked" ? ` ${tr("Ajustes está bloqueado: para cambiarlo usa TRACEREPORTS_AI_LANGUAGE=es (o en) en el .env.")}` : "";
		return `<span class="field-help">${now}${locked} ${tr("Los diagnósticos que ya existen no cambian: usa Re-analizar.")}</span>`;
	}
	/**
	 * .env completo con datos de prueba, para la ayuda de "ajustes de solo lectura". La sección de IA
	 * que queda activa es la del proveedor configurado ahora (sin su key real). Devuelve texto plano.
	 */
	function envExample(d) {
		const prov = d.ai.provider || "gemini";
		const on = (id) => (id === prov ? "" : "# ");
		const docker = d.runtime === "docker";
		const lines = [
			`# ${tr("TraceReports · ejemplo de .env (datos de prueba: reemplázalos antes de usarlo)")}`,
			"",
			`# ── ${tr("Servidor")} ──`,
			"PORT=8080",
			docker ? `# DATA_DIR=/data            # ${tr("en Docker los datos van al volumen tracereports-data")}` : "DATA_DIR=./data",
			"",
			`# ── ${tr("Seguridad")} ──`,
			`# ${tr("Token con el que los tests escriben en la API (genera uno: python -c \"import secrets; print(secrets.token_hex(24))\").")}`,
			`# ${tr("Configura el mismo valor en la máquina o el CI que corre los tests.")}`,
			`TRACEREPORTS_TOKEN=${S.settings.gen.token || "demo-7f3c9a1e5b2d48c6a0f4e8b1"}`,
			`# ${tr("Usuario y clave para ver el reporte y cambiar los Ajustes (la UI pedirá login a todos).")}`,
			"TRACEREPORTS_UI_USER=qa-admin",
			`TRACEREPORTS_UI_PASSWORD=${S.settings.gen.password || "Cambiar-Esta-Clave-2026"}`,
			`# ${tr("1 = nadie puede cambiar la IA desde la pantalla (recomendado en servidores compartidos).")}`,
			`${d.edit_reason === "locked" ? "" : "# "}TRACEREPORTS_SETTINGS_LOCKED=1`,
			"",
			`# ── ${tr("Inteligencia artificial: deja activa UNA opción")} ──`,
			`# ${tr("Gemini (tiene capa gratuita)")}`,
			`${on("gemini")}AI_PROVIDER=gemini`,
			`${on("gemini")}AI_API_KEY=AIza-EJEMPLO-reemplaza-con-tu-key`,
			`# AI_MODEL=gemini-flash-lite-latest`,
			`# ${tr("Claude")}`,
			`${on("anthropic")}AI_PROVIDER=anthropic`,
			`${on("anthropic")}AI_API_KEY=sk-ant-EJEMPLO-reemplaza-con-tu-key`,
			`# AI_MODEL=claude-opus-5-5`,
			`# ${tr("OpenAI")}`,
			`${on("openai")}AI_PROVIDER=openai`,
			`${on("openai")}AI_API_KEY=sk-EJEMPLO-reemplaza-con-tu-key`,
			`# ${tr("Groq, OpenRouter, LM Studio… (compatible con OpenAI)")}`,
			`${on("openai_compatible")}AI_PROVIDER=openai_compatible`,
			`${on("openai_compatible")}AI_BASE_URL=https://api.groq.com/openai/v1`,
			`${on("openai_compatible")}AI_MODEL=llama-3.3-70b-versatile`,
			`${on("openai_compatible")}AI_API_KEY=gsk_EJEMPLO-reemplaza-con-tu-key`,
			`# ${tr("Ollama en tu máquina, sin key (antes: ollama pull llama3.1)")}`,
			`${on("ollama")}AI_PROVIDER=ollama`,
			`${on("ollama")}AI_MODEL=llama3.1`,
			`${on("ollama")}AI_BASE_URL=${docker ? "http://host.docker.internal:11434" : "http://localhost:11434"}`,
			"",
			`# ── ${tr("Notificaciones al terminar cada ejecución (opcional)")} ──`,
			"# TEAMS_WEBHOOK_URL=https://ejemplo.webhook.office.com/workflows/EJEMPLO",
			"# SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T0000/B0000/EJEMPLO",
			"# PUBLIC_URL=http://mi-servidor:8080",
			"# NOTIFY_ON=failures",
		];
		return lines.join("\n");
	}

	/** Resalta el .env: comentarios, claves y valores (el texto ya viene escapado línea por línea). */
	function envHighlight(text) {
		return text.split("\n").map((l) => {
			if (!l.trim()) return "";
			if (l.startsWith("# ") && !/^# [A-Z_]+=/.test(l)) return `<span class="cf-c">${esc(l)}</span>`;
			const off = l.startsWith("# ");
			const [k, ...v] = (off ? l.slice(2) : l).split("=");
			return `${off ? '<span class="cf-c"># </span>' : ""}<span class="${off ? "env-off" : "cf-k"}">${esc(k)}</span>=<span class="${off ? "env-off" : "cf-s"}">${esc(v.join("="))}</span>`;
		}).join("\n");
	}

	/** Ayuda cuando los ajustes son de solo lectura: por qué, pasos según cómo corre el servidor y un .env completo. */
	function readOnlyHelp(d) {
		const docker = d.runtime === "docker";
		const reason = ["locked", "login", "remote", "viewer"].includes(d.edit_reason) ? d.edit_reason : "remote";
		const env = envExample(d);
		S.settings.envText = env;
		const title = {
			locked: "El administrador bloqueó estos ajustes",
			login: "Inicia sesión para cambiar los ajustes",
			viewer: "Tu usuario es de solo lectura",
			remote: "Los ajustes están en solo lectura desde este navegador",
		}[reason];
		const why = {
			locked: "El servidor tiene TRACEREPORTS_SETTINGS_LOCKED=1: la IA se configura solo con el archivo .env, no desde esta pantalla.",
			login: "El servidor pide usuario y clave para cambiar los ajustes. Recarga la página e ingresa los de TRACEREPORTS_UI_USER y TRACEREPORTS_UI_PASSWORD.",
			viewer: "Entraste con el login de solo lectura (TRACEREPORTS_UI_VIEWER_USER): puedes ver todos los reportes, pero no cambiar ajustes ni usar acciones. Pídele el cambio a quien administra el servidor.",
			remote: "Por seguridad, sin un usuario y clave configurados nadie puede cambiar la IA desde la red: solo desde el mismo equipo donde corre el servidor. Así nadie más puede ver el uso de tu API key ni cambiarla.",
		}[reason];
		const steps = reason === "login" || reason === "viewer" ? "" : `<ol class="ro-steps">
			<li>${tr(docker ? "Abre (o crea) el archivo <code>.env</code> que está junto a <code>docker-compose.yml</code>." : "Crea (o abre) el archivo <code>.env</code> en la carpeta desde donde ejecutas el servidor.")}</li>
			<li>${tr(reason === "locked" ? "Cambia la sección de inteligencia artificial: deja activa una sola opción." : "Copia el ejemplo y reemplaza los datos de prueba: sobre todo <code>TRACEREPORTS_UI_USER</code>, <code>TRACEREPORTS_UI_PASSWORD</code> y tu API key.")}</li>
			<li>${tr(docker ? "Aplica los cambios: <code>docker compose up -d</code> (recrea el contenedor con el .env nuevo)." : "Reinicia el servidor: detenlo (Ctrl+C) y vuelve a ejecutar <code>./tracereports</code> o <code>go run ./cmd</code>.")}</li>
			<li>${tr(reason === "locked" ? "Recarga esta página: verás la configuración nueva." : "Recarga esta página e inicia sesión con ese usuario y clave: los ajustes quedarán editables.")}</li>
		</ol>`;
		const dockerNote = docker && reason === "remote"
			? `<p class="ro-note">${icon("i-info")}<span>${tr("Usas Docker: aunque abras <code>localhost</code>, la conexión llega desde la red de Docker y no cuenta como el mismo equipo. Por eso hace falta el login.")}</span></p>` : "";
		return `<div class="ro-help" role="note">
			<div class="ro-head">${icon("i-info")}<div><b>${tr(title)}</b><p>${tr(why)}</p></div></div>
			${dockerNote}
			${steps}
			${reason === "login" ? "" : `<details class="env-box" open>
				<summary>${tr("Ejemplo de <code>.env</code> completo (datos de prueba)")}</summary>
				<div class="env-bar"><span class="mono">.env</span><span class="env-actions">
					<button class="cf-btn cf-btn-sm" data-gen-secrets data-tip="Reemplaza el token y la clave de prueba por valores aleatorios seguros, generados en tu navegador">${icon("i-spark")}${S.settings.gen.password ? "Generar otros valores" : "Rellenar con valores seguros"}</button>
					<button class="cf-btn cf-btn-sm" data-env-copy data-tip="Copia el ejemplo completo para pegarlo en tu archivo .env">${icon("i-copy")}Copiar</button></span></div>
				<pre class="cf-code env-code">${envHighlight(env)}</pre>
				<p class="field-help">${tr("Las variables ya definidas en el sistema mandan sobre el archivo. Todo lo demás es opcional. Detalle de cada variable: <code>{doc}</code> en el repositorio.", { doc: `docs/${I18N.lang === "en" ? "en" : "es"}/configuration.md` })}</p>
			</details>`}
		</div>`;
	}

	async function loadSettings() {
		const st = S.settings;
		try {
			st.data = await api("/api/v1/settings");
			st.error = null;
			const ai = st.data.ai;
			st.form = { provider: ai.provider || "off", model: ai.model, base_url: ai.base_url, api_key: "", ai_language: st.data.ai_language };
		} catch (err) {
			st.error = err.message;
		}
		if (S.view === "settings") renderSettings(true);
	}

	// ---- conectar los tests (token de la API) ----
	/** Secretos generados en el navegador (crypto.getRandomValues): nunca se envían al servidor. */
	const randHex = (bytes) => [...crypto.getRandomValues(new Uint8Array(bytes))].map((b) => b.toString(16).padStart(2, "0")).join("");
	function randPassword(len = 20) {
		const cs = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789-_"; // sin caracteres ambiguos ni que rompan un .env
		// sin sesgo: se descartan los bytes del último tramo incompleto (256 no es múltiplo de cs.length)
		const limit = 256 - (256 % cs.length);
		let out = "";
		while (out.length < len) {
			for (const b of crypto.getRandomValues(new Uint8Array(len * 2))) {
				if (b < limit && out.length < len) out += cs[b % cs.length];
			}
		}
		return out;
	}

	const CONN_TABS = [
		{ id: "ps", label: "PowerShell", hint: "Solo para la terminal abierta: ideal para probar." },
		{ id: "setx", label: "Windows (permanente)", hint: "Queda guardado en tu usuario de Windows para las terminales nuevas." },
		{ id: "bash", label: "macOS / Linux", hint: "Para la terminal actual; para dejarlo fijo, agrégalo a ~/.bashrc o ~/.zshrc." },
		{ id: "github", label: "GitHub Actions", hint: "El token va como secret del repositorio, nunca escrito en el YAML." },
		{ id: "gitlab", label: "GitLab CI", hint: "El token va como variable enmascarada del proyecto, nunca escrito en el YAML." },
	];

	/** Comandos de cada pestaña con la URL de este servidor y el token (o un marcador si aún no hay). */
	function connSnippets(url, token) {
		const T = token || "<TOKEN>";
		const run = `pytest --tracereports          # ${tr("o: python mi_test.py · go test ./...")}`;
		return {
			ps: [`$env:TRACEREPORTS_URL = "${url}"`, `$env:TRACEREPORTS_TOKEN = "${T}"`, run].join("\n"),
			setx: [`setx TRACEREPORTS_URL "${url}"`, `setx TRACEREPORTS_TOKEN "${T}"`, `# ${tr("Abre una terminal nueva: setx no cambia la terminal actual.")}`].join("\n"),
			bash: [`export TRACEREPORTS_URL="${url}"`, `export TRACEREPORTS_TOKEN="${T}"`, run].join("\n"),
			github: [
				`# 1) ${tr("Settings → Secrets and variables → Actions → New repository secret")}`,
				`#    TRACEREPORTS_URL   = ${url}`,
				`#    TRACEREPORTS_TOKEN = ${tr("(pega aquí el token)")}`,
				`# 2) ${tr("En el workflow:")}`,
				"- name: Tests E2E",
				"  env:",
				"    TRACEREPORTS_URL: ${{ secrets.TRACEREPORTS_URL }}",
				"    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}",
				'  run: pytest tests/e2e --tracereports --tracereports-run "E2E ${{ github.ref_name }}"',
			].join("\n"),
			gitlab: [
				`# 1) ${tr("Settings → CI/CD → Variables → Add variable (marca \"Masked\")")}`,
				`#    TRACEREPORTS_URL   = ${url}`,
				`#    TRACEREPORTS_TOKEN = ${tr("(pega aquí el token)")}`,
				`# 2) ${tr("En .gitlab-ci.yml:")}`,
				"e2e:",
				"  script:",
				'    - pytest tests/e2e --tracereports --tracereports-run "E2E $CI_COMMIT_REF_NAME"',
			].join("\n"),
		};
	}

	/** Resaltado mínimo para shell / YAML: comentarios atenuados y el token marcado donde va. */
	function snipHighlight(text, token) {
		return text.split("\n").map((l) => {
			const t = l.trimStart();
			if (t.startsWith("#") && !t.startsWith("#    TRACEREPORTS")) return `<span class="cf-c">${esc(l)}</span>`;
			let h = esc(l);
			const mark = token || "&lt;TOKEN&gt;";
			if (h.includes(mark)) h = h.split(mark).join(`<mark class="tok-mark">${mark}</mark>`);
			return h;
		}).join("\n");
	}

	function snippetBlock(key, label, text, token) {
		S.settings.snips[key] = text;
		return `<div class="snip"><div class="env-bar"><span class="mono">${esc(label)}</span>
				<button class="cf-btn cf-btn-sm" data-copy-snip="${key}">${icon("i-copy")}Copiar</button></div>
			<pre class="cf-code env-code">${snipHighlight(text, token)}</pre></div>`;
	}

	function connectCard(d) {
		const st = S.settings;
		const tok = st.gen.token || "";
		const url = location.origin;
		const docker = d.runtime === "docker";
		const snips = connSnippets(url, tok);
		const tab = CONN_TABS.find((x) => x.id === st.connTab) || CONN_TABS[0];
		const ci = tab.id === "github" || tab.id === "gitlab";
		const localUrl = /^(localhost|127\.|\[::1\])/.test(location.hostname);
		const step = (n, title, body, done) => `<li class="conn-step ${done ? "done" : ""}">
			<span class="conn-num" aria-hidden="true">${done ? icon("i-check") : n}</span>
			<div class="conn-body"><h6>${title}${done ? ` <span class="conn-done">${tr("Listo")}</span>` : ""}</h6>${body}</div></li>`;
		const chk = st.check;
		return `<section class="card set-card" aria-labelledby="set-conn" id="connect-card">
			<div class="set-head"><h5 id="set-conn">${icon("i-plug")}Conectar tus tests</h5>
				<div class="conn-status">
					<span class="conn-chip ${d.token_set ? "ok" : "warn"}" tabindex="0" data-tip="${d.token_set ? "Solo quien tiene el token (tus tests, tu CI) puede crear reportes." : "Sin token, cualquiera que llegue al servidor puede crear o llenar reportes."}">
						<span class="dot"></span>${d.token_set ? "Token de API activo" : "Sin token de API"}</span>
					<span class="conn-chip ${d.ui_login ? "ok" : "off"}" tabindex="0" data-tip="${d.ui_login ? "La interfaz pide usuario y clave (TRACEREPORTS_UI_USER / TRACEREPORTS_UI_PASSWORD)." : "La interfaz no pide login: cualquiera con acceso a la red puede ver los reportes."}">
						<span class="dot"></span>${d.ui_login ? "Login de la interfaz activo" : "Interfaz sin login"}</span>
				</div></div>
			<p class="field-help">El token es la contraseña con la que tus tests escriben reportes. Va en dos lugares: en el servidor y donde corren los tests. Son cuatro pasos y se hacen una sola vez.</p>
			<ol class="conn-steps">
				${step(1, tr("Genera un token"), `
					<div class="conn-row">
						<button class="cf-btn ${tok ? "" : "cf-btn-primary"}" data-gen-token>${icon("i-spark")}${tok ? tr("Generar otro") : tr("Generar token")}</button>
						${tok ? `<code class="tok-value" data-no-i18n>${esc(tok)}</code><button class="cf-btn cf-btn-sm" data-copy-snip="token">${icon("i-copy")}${tr("Copiar")}</button>` : ""}
					</div>
					<p class="field-help">${tr("Se genera en tu navegador con un generador criptográfico y no se envía a ningún lado. Guárdalo como una contraseña.")}</p>
					${d.token_set ? `<p class="conn-warn">${icon("i-warning")}<span>${tr("El servidor ya tiene un token. Genera uno nuevo solo si quieres cambiarlo: tendrás que actualizarlo también en tus tests y en el CI.")}</span></p>` : ""}`, false)}
				${step(2, tr("Agrégalo al .env del servidor y reinícialo"), `
					${snippetBlock("env", ".env", `TRACEREPORTS_TOKEN=${tok || "<TOKEN>"}`, tok)}
					<p class="field-help">${tr(docker ? "Aplica el cambio con <code>docker compose up -d</code>." : "Reinicia el servidor: detenlo (Ctrl+C) y vuelve a ejecutar <code>./tracereports</code>.")}</p>`, d.token_set && !tok)}
				${step(3, tr("Configúralo donde corren los tests"), `
					<div class="cf-tabs conn-tabs" role="tablist" aria-label="${tr("Dónde corren los tests")}">
						${CONN_TABS.map((x) => `<button role="tab" aria-selected="${x.id === tab.id}" tabindex="${x.id === tab.id ? 0 : -1}" data-conn-tab="${x.id}">${esc(tr(x.label))}</button>`).join("")}
					</div>
					<div role="tabpanel">
						${snippetBlock("tab", tab.label, snips[tab.id], tok)}
						<p class="field-help">${esc(tr(tab.hint))} ${tr("Los clientes de Python y Go leen <code>TRACEREPORTS_URL</code> y <code>TRACEREPORTS_TOKEN</code> solos: no hay que cambiar código.")}</p>
						${ci && localUrl ? `<p class="conn-warn">${icon("i-warning")}<span>${tr("Desde el CI, <code>{url}</code> no es alcanzable: usa la URL pública de este servidor (la misma que pondrías en <code>PUBLIC_URL</code>).", { url })}</span></p>` : ""}
					</div>`, false)}
				${step(4, tr("Verifica que el servidor lo acepte"), `
					<div class="conn-row">
						<input type="password" id="tok-check" class="tok-input" value="${esc(st.checkInput ?? tok)}" placeholder="${tr("Pega el token")}" autocomplete="off" spellcheck="false" aria-label="${tr("Token a verificar")}">
						<button class="cf-btn" data-check-token ${st.checking ? "disabled" : ""}>${icon("i-pass")}${tr("Verificar")}</button>
					</div>
					${chk ? `<p class="set-msg ${chk.ok ? "ok" : "err"}" role="status">${icon(chk.ok ? "i-pass" : "i-fail")}<span>${esc(chk.text)}</span></p>` : ""}
					<p class="field-help">${tr("Al correr los tests con un token incorrecto no se cae nada: verás una advertencia 401 en la consola y la ejecución no aparecerá aquí.")}</p>`, chk?.ok)}
			</ol>
		</section>`;
	}

	async function checkToken() {
		const st = S.settings;
		const token = ($("#tok-check")?.value || "").trim();
		st.checkInput = token;
		if (!token) { st.check = { ok: false, text: tr("Pega un token para verificarlo.") }; renderSettings(true); return; }
		st.checking = true; renderSettings(true);
		try {
			// X-TraceReports-Token (y no Authorization) para no pisar el login de la interfaz
			const res = await fetch("/api/v1/auth/check", { headers: { "X-TraceReports-Token": token, Accept: "application/json" } });
			const r = await res.json();
			st.check = !r.token_required ? { ok: false, text: tr("El servidor todavía no exige token: completa el paso 2 y reinícialo.") }
				: r.token_valid ? { ok: true, text: tr("El servidor acepta este token: tus tests ya pueden crear reportes.") }
				: { ok: false, text: tr("El servidor no acepta este token. Revisa que sea el mismo de TRACEREPORTS_TOKEN en su .env y que lo hayas reiniciado.") };
		} catch (err) {
			st.check = { ok: false, text: err.message };
		}
		st.checking = false;
		renderSettings(true);
	}

	function renderSettings(force = false) {
		const el = $("#view-settings");
		const st = S.settings;
		if (!STATIC && !st.data && !st.error && !st.loading) { st.loading = true; loadSettings().finally(() => { st.loading = false; }); }
		if (!STATIC && !st.usage && !st.usageError && !st.usageLoading) loadUsage();
		if (!STATIC && st.tab === "usage" && !st.status && !st.statusLoading) loadStatus(false);
		if (!STATIC && !st.posture && !st.postureLoading) loadPosture();
		const head = `<div class="page-head"><div><h4 class="page-title">Ajustes</h4>
				<p class="page-sub">${STATIC ? "Reporte exportado: el idioma y el tema se guardan en este navegador." : "El idioma y el tema son de cada navegador. La IA es del servidor: aplica a todos."}</p></div></div>`;
		let html;
		if (STATIC) {
			html = head + appearanceCard();
		} else {
			// una sección a la vez, con un índice lateral que resume el estado de cada una
			const tabs = settingsTabs();
			const cur = tabs.find((t) => t.id === st.tab) || tabs[0];
			const body = cur.id === "look" ? appearanceCard()
				: st.error ? `<div class="card placeholder">No se pudieron cargar los ajustes: ${esc(st.error)}</div>`
				: !st.data ? `<div class="card placeholder">Cargando…</div>`
				: cur.id === "ai" ? aiCardSettings() : cur.id === "usage" ? usageCard()
				: cur.id === "security" ? postureCard() : connectCard(st.data);
			html = `${head}<div class="set-layout">
				<nav class="card set-nav" role="tablist" aria-label="Secciones de Ajustes" aria-orientation="vertical">
					${tabs.map((t) => `<button role="tab" id="set-tab-${t.id}" aria-selected="${t.id === cur.id}" aria-controls="set-panel" tabindex="${t.id === cur.id ? 0 : -1}" data-set-tab="${t.id}">
						<span class="set-nav-icon">${icon(t.icon)}</span>
						<span class="set-nav-text"><b>${t.title}</b><small class="set-nav-sub ${t.tone || ""}">${t.sub}</small></span></button>`).join("")}
				</nav>
				<div class="set-panel" id="set-panel" role="tabpanel" aria-labelledby="set-tab-${cur.id}">${body}</div>
			</div>`;
		}
		if (force) S.rendered["view-settings"] = null;
		setHTML(el, html);
	}

	/** Secciones de Ajustes con una línea de estado (para saber qué falta sin entrar). */
	function settingsTabs() {
		const d = S.settings.data;
		const theme = THEMES.find((t) => t.id === document.body.dataset.theme)?.name || "";
		const ai = d?.ai, prov = d?.providers?.find((x) => x.id === ai?.provider);
		return [
			{ id: "look", icon: "i-palette", title: "Apariencia", sub: `${theme} · ${I18N.lang === "en" ? "English" : "Español"}` },
			{ id: "ai", icon: "i-spark", title: "Inteligencia artificial",
				sub: !d ? "…" : ai.enabled ? `${prov?.name || ai.provider} · ${ai.model}` : "Desactivada", tone: d && !ai.enabled ? "muted" : "" },
			{ id: "usage", icon: "i-chart", title: "Uso de la IA",
				sub: S.settings.usage ? tr("{n} llamadas hoy", { n: fmtNum(S.settings.usage.usage.today.calls) }) : "…" },
			{ id: "conn", icon: "i-plug", title: "Conectar tus tests",
				sub: !d ? "…" : d.token_set ? "Token de API activo" : "Sin token de API", tone: d && !d.token_set ? "warn" : "ok" },
			{ id: "security", icon: "i-shield", title: "Revisión de seguridad", ...postureSummary(S.settings.posture) },
		];
	}

	/** Línea de estado de la postura en el índice de Ajustes. */
	const postureFails = (n) => (n === 1 ? tr("1 crítico") : tr("{n} críticos", { n }));

	function postureSummary(p) {
		if (!p) return { sub: "…" };
		if (p.error) return { sub: tr("Sin acceso"), tone: "muted" };
		if (p.fail) return { sub: postureFails(p.fail), tone: "warn" };
		if (p.warn) return { sub: tr("{n} por revisar", { n: p.warn }), tone: "warn" };
		return { sub: tr("Todo en orden"), tone: "ok" };
	}

	async function loadPosture() {
		const st = S.settings;
		st.postureLoading = true;
		try {
			st.posture = await api("/api/v1/security/posture");
		} catch (err) {
			st.posture = { error: err.message, checks: [] };
		}
		st.postureLoading = false;
		if (S.view === "settings") renderSettings(true);
	}

	const POSTURE_TONE = { ok: "ok", warn: "warn", fail: "bad" };
	const POSTURE_LABEL = { ok: "OK", warn: "Revisar", fail: "Crítico" };
	// por id estable del chequeo (el mismo de doctor --security -json): tema, ícono y título en el idioma de la interfaz
	const POSTURE_GROUPS = [
		["Acceso", ["auth.tokens", "auth.ui_login"]],
		["Red y salida de datos", ["network.exposure", "egress.destinations"]],
		["Datos y evidencia", ["data.encryption", "data.retention", "data.permissions", "evidence.redaction", "evidence.screenshots"]],
		["IA y servidor", ["ai.provider", "settings.locked", "process.user"]],
	];
	const POSTURE_CHECK = {
		"auth.tokens": ["i-key", "Tokens de la API"], "auth.ui_login": ["i-user", "Login de la interfaz"],
		"network.exposure": ["i-globe", "Exposición en la red"], "egress.destinations": ["i-send", "Destinos externos"],
		"data.encryption": ["i-lock", "Cifrado de la evidencia"], "data.retention": ["i-calendar", "Retención"],
		"data.permissions": ["i-folder", "Permisos de DATA_DIR"], "evidence.redaction": ["i-eye-off", "Ocultar secretos"],
		"evidence.screenshots": ["i-camera", "Política de capturas"], "ai.provider": ["i-spark", "Proveedor de IA"],
		"settings.locked": ["i-gear", "Ajustes bloqueados"], "process.user": ["i-server", "Usuario del proceso"],
	};
	// el detalle y el arreglo llegan en inglés, como en doctor --security: en español se traducen los conocidos
	// (con sus valores) y lo que no se reconozca se muestra tal cual
	const POSTURE_ES = [
		[/^admin and ingest tokens set and different$/, "tokens de administración y de ingesta definidos y distintos"],
		[/^TRACEREPORTS_INGEST_TOKEN is the same as TRACEREPORTS_TOKEN$/, "TRACEREPORTS_INGEST_TOKEN es igual a TRACEREPORTS_TOKEN"],
		[/^generate a different ingest token: (.+)$/, "genera un token de ingesta distinto: $1"],
		[/^listening on the network without any token: anyone who reaches the port writes and reads$/, "escucha en la red sin ningún token: cualquiera que llegue al puerto escribe y lee"],
		[/^set TRACEREPORTS_TOKEN and TRACEREPORTS_INGEST_TOKEN \((.+)\), or listen only on 127\.0\.0\.1$/, "define TRACEREPORTS_TOKEN y TRACEREPORTS_INGEST_TOKEN ($1), o escucha solo en 127.0.0.1"],
		[/^no token: fine on a laptop \(only this machine\), not on a shared server$/, "sin token: sirve en un notebook (solo este equipo), no en un servidor compartido"],
		[/^set TRACEREPORTS_TOKEN and TRACEREPORTS_INGEST_TOKEN before sharing the server$/, "define TRACEREPORTS_TOKEN y TRACEREPORTS_INGEST_TOKEN antes de compartir el servidor"],
		[/^only the admin token: test clients and CI use a credential that can change Settings$/, "solo el token de administración: los clientes de test y la CI usan una credencial que puede cambiar Ajustes"],
		[/^set TRACEREPORTS_INGEST_TOKEN for clients and CI, and keep TRACEREPORTS_TOKEN for admins$/, "define TRACEREPORTS_INGEST_TOKEN para los clientes y la CI, y deja TRACEREPORTS_TOKEN para administrar"],
		[/^reads require the UI login or a token$/, "para leer hace falta el login o un token"],
		[/^no UI login and TRACEREPORTS_ALLOWED_HOSTS=\*: anyone who reaches the port reads every report$/, "sin login y con TRACEREPORTS_ALLOWED_HOSTS=*: cualquiera que llegue al puerto lee todos los reportes"],
		[/^set TRACEREPORTS_UI_USER and TRACEREPORTS_UI_PASSWORD$/, "define TRACEREPORTS_UI_USER y TRACEREPORTS_UI_PASSWORD"],
		[/^no UI login: a token does not protect reads, and the Host check is not access control$/, "sin login: un token no protege la lectura, y revisar el Host no es control de acceso"],
		[/^set TRACEREPORTS_UI_USER and TRACEREPORTS_UI_PASSWORD \(and HTTPS on the proxy\)$/, "define TRACEREPORTS_UI_USER y TRACEREPORTS_UI_PASSWORD (y HTTPS en el proxy)"],
		[/^no UI login, but the server only listens on this machine$/, "sin login, pero el servidor solo escucha en este equipo"],
		[/^TRACEREPORTS_INSECURE=1: the server listens on the network even without credentials$/, "TRACEREPORTS_INSECURE=1: el servidor escucha en la red aunque no tenga credenciales"],
		[/^remove TRACEREPORTS_INSECURE and set the tokens and the UI login$/, "quita TRACEREPORTS_INSECURE y define los tokens y el login"],
		[/^listening only on this machine$/, "escucha solo en este equipo"],
		[/^only from (.+)$/, "solo desde $1"],
		[/^listening on the network for any source address$/, "escucha en la red para cualquier dirección de origen"],
		[/^inside Docker it listens on the container network; if the port is published only on 127\.0\.0\.1 \(the official docker-compose\.yml\), only this machine reaches it$/,
			"dentro de Docker escucha en la red del contenedor; si el puerto se publicó solo en 127.0.0.1 (el docker-compose.yml oficial), solo este equipo llega a él"],
		[/^to filter inside Docker too, set TRACEREPORTS_ALLOWED_CIDRS to the container network \(docker network inspect (.+)\) or the networks that need it$/,
			"para filtrar también dentro de Docker, define TRACEREPORTS_ALLOWED_CIDRS con la red del contenedor (docker network inspect $1) o las redes que lo necesitan"],
		[/^limit TRACEREPORTS_ALLOWED_CIDRS to the networks that need it \(or a firewall\)$/, "limita TRACEREPORTS_ALLOWED_CIDRS a las redes que lo necesitan (o usa un firewall)"],
		[/^local rules engine or off: no evidence leaves the server$/, "motor local de reglas o apagada: ninguna evidencia sale del servidor"],
		[/^provider (.+) is not in TRACEREPORTS_AI_ALLOWED_PROVIDERS$/, "el proveedor $1 no está en TRACEREPORTS_AI_ALLOWED_PROVIDERS"],
		[/^use an allowed provider, or add it to TRACEREPORTS_AI_ALLOWED_PROVIDERS if the policy permits$/, "usa un proveedor permitido, o agrégalo a TRACEREPORTS_AI_ALLOWED_PROVIDERS si la política lo permite"],
		[/^(.+) inside the network \((.+)\)$/, "$1 dentro de la red ($2)"],
		[/^(.+), explicitly allowed in TRACEREPORTS_AI_ALLOWED_PROVIDERS: redacted prompts leave the network$/, "$1, permitido explícitamente en TRACEREPORTS_AI_ALLOWED_PROVIDERS: los prompts salen de la red sin secretos"],
		[/^(.+): redacted prompts leave the network, and no TRACEREPORTS_AI_ALLOWED_PROVIDERS limits it$/, "$1: los prompts salen de la red sin secretos, y ningún TRACEREPORTS_AI_ALLOWED_PROVIDERS lo limita"],
		[/^use AI_PROVIDER=builtin or a local LLM, or set TRACEREPORTS_AI_ALLOWED_PROVIDERS to make the decision explicit$/, "usa AI_PROVIDER=builtin o un LLM local, o define TRACEREPORTS_AI_ALLOWED_PROVIDERS para dejar la decisión explícita"],
		[/^Settings is read-only: the configuration comes only from the environment$/, "Ajustes es de solo lectura: la configuración viene solo del entorno"],
		[/^any admin \(and every UI user\) can change the AI provider, its URL and key from the screen$/, "cualquier administrador (y cada usuario con login) puede cambiar desde la pantalla el proveedor de IA, su URL y su clave"],
		[/^set TRACEREPORTS_SETTINGS_LOCKED=1$/, "define TRACEREPORTS_SETTINGS_LOCKED=1"],
		[/^Settings editable, only from this machine$/, "Ajustes editable, solo desde este equipo"],
		[/^runs older than (\d+) days are deleted \(secure_delete, VACUUM\)$/, "las ejecuciones de más de $1 días se borran (secure_delete, VACUUM)"],
		[/^evidence is kept forever$/, "la evidencia se guarda para siempre"],
		[/^set TRACEREPORTS_RETENTION_DAYS to what your data policy allows$/, "define TRACEREPORTS_RETENTION_DAYS según lo que permita tu política de datos"],
		[/^give a valid TRACEREPORTS_DB_KEY or TRACEREPORTS_DB_KEY_FILE outside DATA_DIR \(the server will not start\)$/, "entrega un TRACEREPORTS_DB_KEY o TRACEREPORTS_DB_KEY_FILE válido fuera de DATA_DIR (el servidor no arrancará)"],
		[/^screenshots, traces, videos and DOM encrypted \(AES-256-GCM\), key outside DATA_DIR$/, "capturas, trazas, videos y DOM cifrados (AES-256-GCM), clave fuera de DATA_DIR"],
		[/^off: the evidence is as safe as the disk of DATA_DIR$/, "apagado: la evidencia está tan protegida como el disco de DATA_DIR"],
		[/^set TRACEREPORTS_ENCRYPT_AT_REST=1 with TRACEREPORTS_DB_KEY_FILE, and keep DATA_DIR on an encrypted volume$/, "define TRACEREPORTS_ENCRYPT_AT_REST=1 con TRACEREPORTS_DB_KEY_FILE, y deja DATA_DIR en un volumen cifrado"],
		[/^not applicable on this system \(ACLs\)$/, "no aplica en este sistema (ACL)"],
		[/^not applicable on this system$/, "no aplica en este sistema"],
		[/^running as root$/, "corre como root"],
		[/^run as an unprivileged user \(the official image already is nonroot\)$/, "córrelo con un usuario sin privilegios (la imagen oficial ya es nonroot)"],
		[/^unprivileged user \(uid (\d+)\)$/, "usuario sin privilegios (uid $1)"],
		[/^created with 0700 when the server starts$/, "se crea con 0700 al arrancar el servidor"],
		[/^(.+) is (0\d+): other users of the host can read the evidence$/, "$1 tiene $2: otros usuarios del equipo pueden leer la evidencia"],
		[/^(0\d+): only the server user$/, "$1: solo el usuario del servidor"],
		[/^none: no data leaves the server$/, "ninguno: ningún dato sale del servidor"],
		[/^(.+) — chat notifications carry test names and errors$/, "$1 — las notificaciones al chat llevan nombres de tests y errores"],
		[/^set NOTIFY_DETAIL=minimal \(counts and a link only\)$/, "define NOTIFY_DETAIL=minimal (solo conteos y un link)"],
		[/^secrets in headers, bodies and logs are masked before storing$/, "los secretos en headers, cuerpos y logs se ocultan antes de guardarse"],
		[/^TRACEREPORTS_REDACT=off: secrets are stored as received$/, "TRACEREPORTS_REDACT=off: los secretos se guardan tal como llegan"],
		[/^remove TRACEREPORTS_REDACT=off$/, "quita TRACEREPORTS_REDACT=off"],
		[/^every screenshot, trace and video is kept$/, "se guardan todas las capturas, trazas y videos"],
		[/^set TRACEREPORTS_SCREENSHOTS=failures \(or off\) and mask sensitive fields in the clients$/, "define TRACEREPORTS_SCREENSHOTS=failures (u off) y oculta los campos sensibles en los clientes"],
	];
	function postureText(s) {
		if (!s || window.TraceReportsI18n.lang === "en") return s || "";
		for (const [re, es] of POSTURE_ES) {
			const m = s.match(re);
			if (m) return es.replace(/\$(\d)/g, (_, i) => m[i]);
		}
		return s;
	}
	const postureTitle = (c) => (POSTURE_CHECK[c.id] ? tr(POSTURE_CHECK[c.id][1]) : c.title);
	const postureIcon = (c) => icon(POSTURE_CHECK[c.id]?.[0] || "i-shield");

	/** La revisión de seguridad del servidor: arriba el resumen, después lo que hay que arreglar con qué hacer,
	 * y lo que está en orden, compacto y por tema (el detalle se abre con un clic). */
	function postureCard() {
		const p = S.settings.posture;
		if (!p) return `<div class="card placeholder">${tr("Revisando la configuración…")}</div>`;
		if (p.error) return `<div class="card placeholder">${tr("No se pudo leer la revisión de seguridad")}: ${esc(p.error)}</div>`;
		const total = p.checks.length, tone = p.fail ? "bad" : p.warn ? "warn" : "ok";
		const pct = total ? Math.round((p.ok / total) * 100) : 0;
		const verdict = p.fail ? postureFails(p.fail) + (p.warn ? ` · ${tr("{n} por revisar", { n: p.warn })}` : "")
			: p.warn ? tr("{n} por revisar", { n: p.warn }) : tr("Todo en orden");
		// lo crítico primero; dentro de cada nivel, el orden del servidor
		const issues = p.checks.filter((c) => c.level !== "ok").sort((a, b) => (a.level === "fail" ? 0 : 1) - (b.level === "fail" ? 0 : 1));
		const issueRows = issues.map((c) => `<div class="posture-issue ${POSTURE_TONE[c.level] || "muted"}" data-check="${esc(c.id)}">
				<span class="posture-ico">${postureIcon(c)}</span>
				<div><b><span class="posture-level">${esc(tr(POSTURE_LABEL[c.level] || c.level))}</span> ${esc(postureTitle(c))}</b>
				<p class="m-hint" data-no-i18n>${esc(postureText(c.detail))}</p>
				${c.fix ? `<p class="posture-fix" data-no-i18n>→ ${esc(postureText(c.fix))}</p>` : ""}</div></div>`).join("");
		const ok = p.checks.filter((c) => c.level === "ok");
		const known = new Set(POSTURE_GROUPS.flatMap(([, ids]) => ids));
		const groups = [...POSTURE_GROUPS, ["Otros", ok.filter((c) => !known.has(c.id)).map((c) => c.id)]].map(([name, ids]) => {
			const items = ids.map((id) => ok.find((c) => c.id === id)).filter(Boolean);
			if (!items.length) return "";
			return `<div class="posture-group"><h6>${tr(name)}</h6><div class="posture-grid">${items.map((c) => `<details class="posture-item" data-check="${esc(c.id)}">
					<summary><span class="posture-ico">${postureIcon(c)}</span><span class="posture-name">${esc(postureTitle(c))}</span>${icon("i-check")}</summary>
					<p class="m-hint" data-no-i18n>${esc(postureText(c.detail))}</p></details>`).join("")}</div></div>`;
		}).join("");
		return `<section class="card set-card posture" aria-labelledby="set-posture">
			<div class="set-card-head"><h5 id="set-posture">${icon("i-shield")}${tr("Revisión de seguridad")}</h5>
				<button class="cf-btn cf-btn-sm" data-posture-refresh ${S.settings.postureLoading ? "disabled" : ""}>${tr("Volver a revisar")}</button></div>
			<div class="posture-hero ${tone}" role="status">
				<svg class="posture-ring" viewBox="0 0 36 36" aria-hidden="true"><circle cx="18" cy="18" r="15.9" pathLength="100"/><circle class="posture-ring-val" cx="18" cy="18" r="15.9" pathLength="100" stroke-dasharray="${pct} 100"/></svg>
				<div><b class="posture-verdict">${esc(verdict)}</b>
				<p class="posture-count">${tr("{ok} de {n} chequeos en orden", { ok: p.ok, n: total })}</p>
				<p class="field-help">${tr("Los mismos chequeos de tracereports doctor --security, sobre la configuración con que corre este servidor. Nunca muestra valores de credenciales.")}</p></div></div>
			${issues.length ? `<h6 class="posture-sub">${tr("Para revisar")}</h6><div class="posture-issues">${issueRows}</div>` : ""}
			${ok.length ? `<h6 class="posture-sub">${tr("En orden")}</h6><div class="posture-groups">${groups}</div>` : ""}
		</section>`;
	}

	async function loadUsage() {
		const st = S.settings;
		st.usageLoading = true;
		try {
			st.usage = await api("/api/v1/settings/ai/usage");
			st.usageError = null;
		} catch (err) {
			st.usageError = err.message;
		}
		st.usageLoading = false;
		if (S.view === "settings") renderSettings(true);
	}

	/** Estado general del proveedor de IA (su página de estado pública); force consulta de nuevo. */
	async function loadStatus(force) {
		const st = S.settings;
		st.statusLoading = true;
		try {
			st.status = await api(`/api/v1/settings/ai/status${force ? "?refresh=1" : ""}`);
		} catch (err) {
			st.status = { indicator: "unknown", error: err.message, components: [], incidents: [] };
		}
		st.statusLoading = false;
		if (S.view === "settings" && st.tab === "usage") renderSettings(true);
	}

	const STATUS_LABELS = { none: "Funcionando con normalidad", minor: "Degradación parcial", major: "Interrupción importante",
		critical: "Interrupción grave", maintenance: "Mantenimiento en curso", unknown: "Estado desconocido" };
	const STATUS_TONE = { none: "ok", minor: "warn", major: "bad", critical: "bad", maintenance: "info", unknown: "muted" };
	const COMPONENT_LABELS = { operational: "Operativo", degraded_performance: "Lento", partial_outage: "Interrupción parcial",
		major_outage: "Interrupción", under_maintenance: "En mantenimiento" };
	const INCIDENT_LABELS = { investigating: "Investigando", identified: "Identificado", monitoring: "Monitoreando", update: "Actualizado" };

	/** Caja de estado del proveedor: lo que dice su página y lo que observó TraceReports hoy. */
	function statusBox(u) {
		const st = S.settings, s = st.status;
		const ai = st.data?.ai;
		if (st.data && !ai?.enabled) return `<div class="prov-status muted"><span class="prov-dot"></span><div><b>${tr("IA desactivada")}</b><p class="m-hint">${tr("Configura un proveedor para ver su estado.")}</p></div></div>`;
		if (!s) return `<div class="prov-status muted"><span class="prov-dot"></span><div>${tr("Consultando el estado del proveedor…")}</div></div>`;
		const tone = STATUS_TONE[s.indicator] || "muted";
		const head = s.source === "none"
			? tr("{p} no publica un estado consultable", { p: s.name || s.provider })
			: `${esc(s.name || s.provider)}: ${tr(STATUS_LABELS[s.indicator] || STATUS_LABELS.unknown)}`;
		const comps = (s.components || []).map((c) => `<li><span data-no-i18n>${esc(c.name)}</span>: ${esc(tr(COMPONENT_LABELS[c.status] || c.status))}</li>`).join("");
		const incs = (s.incidents || []).map((i) => `<li>${i.url ? `<a href="${esc(i.url)}" target="_blank" rel="noopener" data-no-i18n>${esc(i.name)} ↗</a>` : `<span data-no-i18n>${esc(i.name)}</span>`} · ${esc(tr(INCIDENT_LABELS[i.status] || i.status))}</li>`).join("");
		const t = u?.today;
		const observed = t?.calls ? tr("Observado por TraceReports hoy: {e} de {n} llamadas con error · respuesta media {t}.", { e: fmtNum(t.errors), n: fmtNum(t.calls), t: t.timed ? fmtDur(t.avg_ms) : "—" }) : "";
		const when = s.checked_at ? new Date(s.checked_at).toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit" }) : "";
		return `<div class="prov-status ${tone}" role="status">
			<span class="prov-dot" aria-hidden="true"></span>
			<div>
				<b>${head}</b>${s.description && s.source === "statuspage" ? ` <span class="m-hint" data-no-i18n>(${esc(s.description)})</span>` : ""}${s.source === "local" ? ` <span class="m-hint">${esc(s.provider === "builtin" ? tr("Funciona dentro del servidor (reglas {v})", { v: s.description }) : s.indicator === "none" ? tr("Ollama responde (versión {v})", { v: s.description }) : tr("Ollama no responde en {u}", { u: s.description }))}</span>` : ""}
				${s.error ? `<p class="m-hint">${esc(tr("No se pudo consultar la página de estado: {e}", { e: s.error }))}</p>` : ""}
				${comps ? `<ul class="prov-status-list">${comps}</ul>` : ""}
				${incs ? `<p class="prov-status-sub">${tr("Incidentes abiertos")}</p><ul class="prov-status-list">${incs}</ul>` : ""}
				${observed ? `<p class="m-hint">${esc(observed)}</p>` : ""}
				<p class="m-hint">${s.page_url ? `<a href="${esc(s.page_url)}" target="_blank" rel="noopener">${tr("Ver página de estado")} ↗</a>` : ""}${when ? ` · ${esc(tr("Consultado a las {h}", { h: when }))}` : ""}</p>
			</div></div>`;
	}

	const USAGE_KINDS = { triage: "Diagnóstico de tests", run_summary: "Resumen de la ejecución", escalation: "Escalamientos", test: "Probar conexión", chat: "Preguntas a Trace Buddy" };

	/** Uso del proveedor de IA: llamadas y tokens que informó, hoy, 7 y 30 días (Ajustes → Uso de la IA). */
	function usageCard() {
		const st = S.settings;
		if (st.usageError) return `<div class="card placeholder">No se pudo cargar el uso de la IA: ${esc(st.usageError)}</div>`;
		if (!st.usage) return `<div class="card placeholder">Cargando…</div>`;
		const u = st.usage.usage, max = st.usage.max_per_run;
		const tokens = (t) => tr("{in} de entrada · {out} de salida", { in: fmtNum(t.input_tokens), out: fmtNum(t.output_tokens) });
		const tile = (label, t, tip) => `<div class="card kpi" tabindex="0" data-tip="${esc(tip)}">
			<span class="kpi-label">${label}</span><span class="kpi-value">${fmtNum(t.calls)}</span>
			<span class="kpi-delta">${esc(tokens(t))}</span>
			${t.calls ? `<span class="kpi-delta">${esc(tr("respuesta media {t}", { t: t.timed ? fmtDur(t.avg_ms) : "—" }))}</span>` : ""}
			${t.errors ? `<span class="kpi-delta bad">${esc(tr("{n} con error", { n: fmtNum(t.errors) }))}</span>` : ""}</div>`;
		const num = (n) => `<td class="num">${fmtNum(n)}</td>`;
		const rowsOf = (list, first) => list.map((r) => `<tr>${first(r)}${num(r.calls)}${num(r.errors)}${num(r.input_tokens)}${num(r.output_tokens)}<td class="num">${r.timed ? fmtDur(r.avg_ms) : "—"}</td></tr>`).join("");
		const head = (first) => `<thead><tr>${first}<th class="num">Llamadas</th><th class="num">Con error</th><th class="num">Tokens de entrada</th><th class="num">Tokens de salida</th><th class="num" data-tip="Lo que tardó el proveedor en responder, en promedio por llamada.">Respuesta media</th></tr></thead>`;
		const model = (r) => `<td><span data-no-i18n>${esc(r.provider)} · ${esc(r.model || "—")}</span></td>`;
		const empty = !u.last_30.calls;
		return `<section class="card set-card" aria-labelledby="set-usage">
			<div class="set-card-head"><h5 id="set-usage">Uso de la IA</h5>
				<button class="cf-btn cf-btn-sm" data-usage-refresh ${st.usageLoading ? "disabled" : ""}>Actualizar</button></div>
			<p class="field-help">Llamadas al proveedor de IA, los tokens que él mismo informó y cuánto tardó en responder, en la hora del servidor. Incluye reintentos y llamadas con error. El costo real es el de la factura de tu proveedor.</p>
			${statusBox(u)}
			<div class="kpi-grid">
				${tile(tr("Hoy"), u.today, tr("Llamadas de hoy."))}
				${tile(tr("Últimos 7 días"), u.last_7, tr("Hoy y los 6 días anteriores."))}
				${tile(tr("Últimos 30 días"), u.last_30, tr("Hoy y los 29 días anteriores."))}
			</div>
			${u.last_30.errors ? `<p class="m-hint">${esc(tr("Errores en 30 días: {list}.", { list: Object.entries(u.last_30.errors_by || {})
				.sort((a, b) => b[1] - a[1]).map(([r, n]) => tr("{n} por {r}", { n: fmtNum(n), r: tr(AI_FAIL[r] || AI_FAIL.other) })).join(" · ") }))}</p>` : ""}
			${u.last_30.calls && u.last_30.timed < u.last_30.calls ? `<p class="m-hint">${esc(tr("La respuesta media es de las llamadas con tiempo medido ({m} de {n}); las anteriores a esta versión no lo tienen.", { m: fmtNum(u.last_30.timed), n: fmtNum(u.last_30.calls) }))}</p>` : ""}
			${u.last_30.untracked ? `<p class="m-hint">${esc(u.last_30.untracked === 1
				? tr("1 llamada no informó tokens (una API compatible que no los devuelve, o un error antes de la respuesta).")
				: tr("{n} llamadas no informaron tokens (una API compatible que no los devuelve, o un error antes de la respuesta).", { n: fmtNum(u.last_30.untracked) }))}</p>` : ""}
			<p class="m-hint">${esc(max > 0
				? tr("Límite automático: {max} diagnósticos por ejecución (TRACEREPORTS_AI_MAX_PER_RUN). En 30 días, {n} tests quedaron sin diagnóstico automático por ese límite.", { max: fmtNum(max), n: fmtNum(u.skipped_by_budget) })
				: tr("Sin límite de diagnósticos automáticos por ejecución (TRACEREPORTS_AI_MAX_PER_RUN=0)."))}</p>
			${empty ? `<div class="placeholder">Sin llamadas a la IA en los últimos 30 días.</div>` : `
			<h6>Por modelo (30 días)</h6>
			<div class="table-scroll"><table class="m-table">${head("<th>Proveedor · modelo</th>")}<tbody>${rowsOf(u.by_model, model)}</tbody></table></div>
			<h6>Por tipo de uso (30 días)</h6>
			<div class="table-scroll"><table class="m-table">${head("<th>Tipo</th>")}<tbody>${rowsOf(u.by_kind, (r) => `<td>${esc(tr(USAGE_KINDS[r.kind] || r.kind))}</td>`)}</tbody></table></div>
			<h6>Por día</h6>
			<div class="table-scroll"><table class="m-table">${head("<th>Día</th><th>Proveedor · modelo</th>")}<tbody>${rowsOf(u.daily, (r) => `<td>${esc(dayLabel(r.day))}</td>${model(r)}`)}</tbody></table></div>`}
		</section>`;
	}

	function appearanceCard() {
		const cur = document.body.dataset.theme;
		const editable = S.settings.data?.editable;
		const serverLang = S.settings.data?.language || "";
		return `<section class="card set-card" aria-labelledby="set-look">
			<h5 id="set-look">Apariencia</h5>
			<div class="field">
				<span class="field-label">Idioma de la interfaz</span>
				<div class="seg" role="group" aria-label="Idioma">
					<button data-set-lang="es" aria-pressed="${I18N.lang === "es"}" lang="es" data-no-i18n>Español</button>
					<button data-set-lang="en" aria-pressed="${I18N.lang === "en"}" lang="en" data-no-i18n>English</button>
				</div>
				<p class="field-help">Se guarda en este navegador.${editable && serverLang !== I18N.lang ? ` <button class="link-btn" data-set-default-lang="${I18N.lang}">Usarlo por defecto para todo el equipo</button>` : ""}${serverLang ? ` <span class="m-hint">Idioma por defecto del equipo: ${serverLang === "en" ? "English" : "Español"}.</span>` : ""}</p>
			</div>
			<div class="field">
				<span class="field-label">Tema</span>
				<div class="theme-grid" role="radiogroup" aria-label="Tema">
					${THEMES.map((t) => `<button class="theme-option" role="radio" aria-checked="${t.id === cur}" data-theme-id="${t.id}">
						<span class="theme-swatch ${t.topnav ? "topnav" : ""}" style="--sw-bg:${t.sw.bg};--sw-top:${t.sw.top};--sw-nav:${t.sw.nav};--sw-card:${t.sw.card};--sw-accent:${t.sw.accent}">
							<i class="nav"></i><i class="c1"></i><i class="c2"></i><i class="dot"></i></span>
						<span><b data-no-i18n>${esc(t.name)}</b><small>${esc(t.desc)}</small></span></button>`).join("")}
				</div>
			</div>
			${buddyAvailable() ? `<div class="field">
				<span class="field-label" data-no-i18n>Trace Buddy</span>
				<div class="seg" role="group" aria-label="Trace Buddy">
					<button data-buddy-show="1" aria-pressed="${!buddyHidden()}">Mostrar</button>
					<button data-buddy-show="0" aria-pressed="${buddyHidden()}">Ocultar</button>
				</div>
				<p class="field-help">El asistente de QA del botón de abajo a la derecha. Se guarda en este navegador.</p>
			</div>` : ""}
		</section>`;
	}

	/** Estado de la API key guardada desde Ajustes cuando necesita atención (nunca su valor). */
	function secretKeyNotice(state) {
		const msg = {
			master_key_missing: "La API key guardada está cifrada y el servidor arrancó sin TRACEREPORTS_SECRET_KEY: no se puede usar. No se borró: vuelve a definir esa variable con la misma clave, o escribe la key de nuevo.",
			master_key_wrong: "La API key guardada se cifró con otra TRACEREPORTS_SECRET_KEY: no se puede usar. No se borró: vuelve a poner la clave anterior (o úsala como TRACEREPORTS_SECRET_KEY_PREVIOUS), o escribe la key de nuevo.",
			damaged: "La API key guardada está dañada y no se puede descifrar. Escríbela de nuevo.",
			plaintext: "La API key guardada por una versión anterior está sin cifrar. Define TRACEREPORTS_SECRET_KEY y ejecuta 'tracereports secrets migrate'.",
		}[state];
		return msg ? `<p class="set-msg ${state === "plaintext" ? "" : "err"}" role="status">${icon(state === "plaintext" ? "i-warning" : "i-fail")}<span>${tr(msg)}</span></p>` : "";
	}

	function aiCardSettings() {
		const st = S.settings, d = st.data, f = st.form;
		const ro = !d.editable;
		const provs = d.providers;
		const p = provs.find((x) => x.id === f.provider);
		const cur = d.ai;
		const curName = provs.find((x) => x.id === cur.provider)?.name;
		const sameProv = cur.provider === f.provider;
		const keyNote = sameProv && cur.key_set
			? tr("Guardada ({hint}, desde {src}). Déjala vacía para mantenerla.", { hint: cur.key_hint, src: cur.key_source === "ui" ? tr("Ajustes") : ".env" })
			: tr("Pega la API key");
		return `<section class="card set-card" aria-labelledby="set-ai">
			<div class="set-head"><h5 id="set-ai">${icon("i-spark")}Inteligencia artificial</h5>
				<span class="ai-status ${cur.enabled ? "on" : "off"}" role="status">${cur.enabled
					? `<span class="dot"></span>${tr("Activa")}: <b data-no-i18n>${esc(curName || cur.provider)}</b> · <span class="mono">${esc(cur.model)}</span>`
					: `<span class="dot"></span>${tr("Desactivada")}`}
					<span class="src-badge" data-tip="${d.ai_source === "ui" ? "Configurada desde esta pantalla: manda sobre el .env." : "Configurada con variables de entorno (.env)."}">${d.ai_source === "ui" ? "desde Ajustes" : "desde .env"}</span></span>
			</div>
			<p class="field-help">Diagnostica cada fallo y el resumen de la ejecución. Por defecto lo hace el motor local de reglas, sin IA y sin red. Para un diagnóstico redactado elige un LLM: en la nube (Gemini, Claude, OpenAI) o local con Ollama, donde los datos de tus tests no salen de tu red.</p>
			${ro ? readOnlyHelp(d) : ""}${secretKeyNotice(d.secrets?.saved_key)}
			<fieldset class="prov-grid" ${ro ? "disabled" : ""}>
				<legend class="field-label">Proveedor</legend>
				${[...provs, { id: "off", name: "Desactivada", note: "Sin diagnóstico automático." }].map((x) => `
					<label class="prov-card ${f.provider === x.id ? "on" : ""}">
						<input type="radio" name="ai-provider" value="${x.id}" ${f.provider === x.id ? "checked" : ""}>
						<span class="prov-name">${esc(x.name)}${x.id === cur.provider && cur.enabled ? ` <span class="prov-cur">${tr("actual")}</span>` : ""}</span>
						<span class="prov-note">${esc(x.note)}</span>
					</label>`).join("")}
			</fieldset>
			${p?.id === "builtin" ? `<p class="field-help">${tr("El motor local reconoce fallos de backend, red, credenciales, selectores, aserciones, tests inestables y ambientes caídos con reglas fijas. No necesita modelo, URL ni API key.")}</p>` : ""}
			${p && p.id !== "builtin" ? `<fieldset class="ai-fields" ${ro ? "disabled" : ""}>
				<label class="field"><span class="field-label">Modelo</span>
					<input type="text" data-ai-field="model" value="${esc(f.model)}" placeholder="${esc(p.default_model || "nombre-del-modelo")}" autocomplete="off" spellcheck="false">
					<span class="field-help">${p.default_model ? tr("Vacío = {m}.", { m: p.default_model }) : tr("El nombre exacto que usa ese servicio.")}${p.id === "ollama" ? ` ${tr("Descárgalo antes con")} <code>ollama pull ${esc(f.model || p.default_model)}</code>.` : ""}</span></label>
				<label class="field"><span class="field-label">URL de la API${p.needs_base_url ? "" : ` <small>${tr("(opcional)")}</small>`}</span>
					<input type="url" data-ai-field="base_url" value="${esc(f.base_url === p.default_base_url ? "" : f.base_url)}" placeholder="${esc(p.default_base_url || "https://api.groq.com/openai/v1")}" autocomplete="off" spellcheck="false">
					<span class="field-help">${p.id === "openai_compatible" ? tr("La URL base que termina en /v1. Ej.: https://api.groq.com/openai/v1, https://openrouter.ai/api/v1, http://localhost:1234/v1 (LM Studio).") : p.id === "ollama" ? tr("Donde corre Ollama. Si el servidor está en Docker: http://host.docker.internal:11434.") : tr("Solo si usas un proxy o un endpoint propio.")}</span></label>
				${p.needs_key || p.id === "openai_compatible" ? `<label class="field"><span class="field-label">API key${p.needs_key ? "" : ` <small>${tr("(si el servicio la pide)")}</small>`}</span>
					<input type="password" data-ai-field="api_key" value="" placeholder="${esc(keyNote)}" autocomplete="new-password" spellcheck="false">
					<span class="field-help">${d.secrets?.master_key_set ? tr("Se guarda cifrada en el servidor y nunca se vuelve a mostrar.")
						: tr("Para guardarla aquí el servidor necesita TRACEREPORTS_SECRET_KEY, así queda cifrada. También puedes ponerla en el .env.")}${p.key_url ? ` <a href="${esc(p.key_url)}" target="_blank" rel="noopener">${tr("Obtener una API key")} ↗</a>` : ""}</span></label>` : ""}
			</fieldset>` : ""}
			<label class="field field-inline"><span class="field-label">Idioma de los diagnósticos</span>
				<select data-ai-field="ai_language" ${ro ? "disabled" : ""}>${AI_LANGS.map(([v, l]) => `<option value="${v}" ${f.ai_language === v ? "selected" : ""}>${esc(l)}</option>`).join("")}</select></label>
			${aiLanguageHelp(d)}
			${ro ? "" : `<div class="set-actions">
				${p ? `<button class="cf-btn" data-ai-test ${st.busy ? "disabled" : ""}>${icon("i-plug")}Probar conexión</button>` : ""}
				<button class="cf-btn cf-btn-primary" data-ai-save ${st.busy ? "disabled" : ""}>${icon("i-check")}Guardar</button>
				${d.ai_source === "ui" ? `<button class="link-btn" data-ai-reset data-tip="Borra lo configurado aquí y usa de nuevo las variables de entorno del servidor.">Volver a la configuración del .env</button>` : ""}
			</div>`}
			${st.msg ? `<p class="set-msg ${st.msg.ok ? "ok" : "err"}" role="status">${icon(st.msg.ok ? "i-pass" : "i-fail")}<span>${esc(st.msg.text)}</span></p>` : ""}
			${st.msg?.hint && AI_HINTS[st.msg.hint] ? `<div class="ai-hint" role="note">${icon("i-info")}<div><p>${tr(AI_HINTS[st.msg.hint])}</p>
				${st.msg.suggest && !ro ? `<button class="cf-btn cf-btn-sm cf-btn-primary" data-use-model="${esc(st.msg.suggest)}">${icon("i-plug")}${tr("Usar {m} y probar", { m: st.msg.suggest })}</button>` : ""}</div></div>` : ""}
			<details class="info-box"><summary>¿Configurar aquí o en el .env?</summary>
				<p>Las dos sirven. El <code>.env</code> es la configuración base y la mejor opción en servidores, Docker y CI: queda versionada y no depende de nadie. Lo que guardes aquí manda sobre el <code>.env</code>, y "Volver a la configuración del .env" lo deshace.</p>
				<p>La API key guardada aquí queda en la base de datos del servidor (<code>DATA_DIR</code>) y nunca se envía al navegador. Para que nadie la cambie desde la interfaz: <code>TRACEREPORTS_SETTINGS_LOCKED=1</code>.</p>
			</details>
		</section>`;
	}

	function settingsInput(e) {
		const fld = e.target.closest("[data-ai-field]");
		if (fld && S.settings.form) S.settings.form[fld.dataset.aiField] = fld.value;
	}

	function aiPayload() {
		const f = S.settings.form;
		return { provider: f.provider, model: f.model.trim(), base_url: f.base_url.trim(), api_key: f.api_key.trim() || null };
	}

	// qué hacer ante los errores típicos de los proveedores (los clasifica el servidor: ai.Hint)
	const AI_HINTS = {
		no_credits: "La cuenta del proveedor no tiene saldo. La key es válida, pero el proveedor no atiende solicitudes hasta que agregues créditos en su panel de facturación. Mientras tanto puedes usar otro proveedor: Gemini tiene capa gratuita y Ollama es gratis en tu máquina.",
		model_unavailable: "Ese modelo no está disponible para tu cuenta (los proveedores retiran modelos antiguos para cuentas nuevas). Elige otro modelo.",
		quota_daily: "Se agotó la cuota gratuita diaria de ese modelo: vuelve a estar disponible mañana. Mientras tanto usa otro modelo (cada uno tiene su propia cuota) o activa la facturación en el proveedor.",
		rate_limit: "El proveedor está limitando la cantidad de solicitudes por minuto. TraceReports reintenta solo: prueba de nuevo en un minuto.",
		overloaded: "El modelo está saturado en este momento (le pasa a los modelos más nuevos). Prueba otro modelo o vuelve a intentarlo más tarde.",
	};

	async function settingsAction(kind) {
		const st = S.settings;
		st.busy = true; st.msg = null; renderSettings(true);
		try {
			if (kind === "test") {
				const r = await apiSend("POST", "/api/v1/settings/ai/test", aiPayload());
				st.msg = r.ok ? { ok: true, text: tr("Conectado: el modelo respondió en {s} s.", { s: fmtNum(r.ms / 1000, 1) }) }
					: { ok: false, text: tr("No funcionó: {e}", { e: r.error }), hint: r.hint, suggest: r.suggest };
			} else {
				st.data = kind === "reset" ? await apiSend("DELETE", "/api/v1/settings/ai")
					: await apiSend("PUT", "/api/v1/settings", { ai: aiPayload(), ai_language: st.form.ai_language });
				const ai = st.data.ai;
				st.form = { provider: ai.provider || "off", model: ai.model, base_url: ai.base_url, api_key: "", ai_language: st.data.ai_language };
				st.msg = { ok: true, text: kind === "reset" ? tr("Listo: la IA vuelve a usar la configuración del .env.") : tr("Guardado. Los próximos fallos se diagnostican con esta configuración.") };
				S.config = await api("/api/v1/config");
			}
		} catch (err) {
			st.msg = { ok: false, text: err.message };
		}
		st.busy = false;
		renderSettings(true);
	}

	function bindSettingsEvents() {
		const root = $("#view-settings");
		root.addEventListener("input", (e) => {
			if (e.target.id === "tok-check") { S.settings.checkInput = e.target.value; return; }
			settingsInput(e);
		});
		// pestañas de "Conectar tus tests": flechas para moverse entre ellas
		root.addEventListener("keydown", (e) => {
			if (e.target.id === "tok-check" && e.key === "Enter") { checkToken(); return; }
			const st = e.target.closest("[data-set-tab]");
			if (st && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
				e.preventDefault();
				const ids = settingsTabs().map((x) => x.id);
				openSettingsTab(ids[(ids.indexOf(st.dataset.setTab) + (e.key === "ArrowDown" ? 1 : -1) + ids.length) % ids.length], true);
				return;
			}
			const t = e.target.closest("[data-conn-tab]");
			if (!t || (e.key !== "ArrowRight" && e.key !== "ArrowLeft")) return;
			const ids = CONN_TABS.map((x) => x.id);
			S.settings.connTab = ids[(ids.indexOf(t.dataset.connTab) + (e.key === "ArrowRight" ? 1 : -1) + ids.length) % ids.length];
			renderSettings(true);
			$(`[data-conn-tab="${S.settings.connTab}"]`)?.focus();
		});
		root.addEventListener("change", (e) => {
			settingsInput(e);
			if (e.target.name === "ai-provider") {
				const st = S.settings, cur = st.data.ai, p = st.data.providers.find((x) => x.id === e.target.value);
				const same = cur.provider === e.target.value;
				st.form = { ...st.form, provider: e.target.value, model: same ? cur.model : "", base_url: same ? cur.base_url : (p?.default_base_url || ""), api_key: "" };
				st.msg = null;
				renderSettings(true);
			}
		});
		root.addEventListener("click", async (e) => {
			const setTab = e.target.closest("[data-set-tab]");
			if (setTab) { openSettingsTab(setTab.dataset.setTab, true); if (setTab.dataset.setTab === "usage") { loadUsage(); loadStatus(false); } return; }
			const buddyShow = e.target.closest("[data-buddy-show]");
			if (buddyShow) { buddySetHidden(buddyShow.dataset.buddyShow !== "1"); return; }
			if (e.target.closest("[data-posture-refresh]")) { loadPosture(); renderSettings(true); return; }
			if (e.target.closest("[data-usage-refresh]")) { loadUsage(); loadStatus(true); renderSettings(true); return; }
			const lang = e.target.closest("[data-set-lang]");
			if (lang) { I18N.setLang(lang.dataset.setLang); renderSettings(true); return; }
			const def = e.target.closest("[data-set-default-lang]");
			if (def) {
				try { S.settings.data = await apiSend("PUT", "/api/v1/settings", { language: def.dataset.setDefaultLang }); } catch (err) { S.settings.msg = { ok: false, text: err.message }; }
				renderSettings(true); return;
			}
			const th = e.target.closest(".theme-option");
			if (th) { applyTheme(th.dataset.themeId); renderSettings(true); return; }
			if (e.target.closest("[data-gen-token]")) {
				S.settings.gen.token = randHex(24); S.settings.check = null; S.settings.checkInput = null; renderSettings(true); return;
			}
			if (e.target.closest("[data-gen-secrets]")) {
				S.settings.gen.token = randHex(24); S.settings.gen.password = randPassword(); S.settings.check = null; S.settings.checkInput = null;
				renderSettings(true); return;
			}
			const tabBtn = e.target.closest("[data-conn-tab]");
			if (tabBtn) { S.settings.connTab = tabBtn.dataset.connTab; renderSettings(true); $(`[data-conn-tab="${S.settings.connTab}"]`)?.focus(); return; }
			const snip = e.target.closest("[data-copy-snip]");
			if (snip) {
				const k = snip.dataset.copySnip;
				CF.copyText(k === "token" ? S.settings.gen.token : S.settings.snips[k] || "", snip);
				return;
			}
			if (e.target.closest("[data-check-token]")) { checkToken(); return; }
			const envCopy = e.target.closest("[data-env-copy]");
			if (envCopy) { CF.copyText(S.settings.envText || "", envCopy); return; }
			const useModel = e.target.closest("[data-use-model]");
			if (useModel) { S.settings.form.model = useModel.dataset.useModel; settingsAction("test"); return; }
			if (e.target.closest("[data-ai-test]")) settingsAction("test");
			else if (e.target.closest("[data-ai-save]")) settingsAction("save");
			else if (e.target.closest("[data-ai-reset]")) settingsAction("reset");
		});
	}

	/** Vista previa del resumen semanal, con el botón para enviarlo ahora a Teams/Slack. */
	async function openWeekly(send = false) {
		let r;
		try {
			r = await apiSend("POST", "/api/v1/ui/summary/weekly", { send, lang: I18N.lang });
		} catch (err) {
			if (!send) { alert(err.message); return; }
			r = { summary: S.weekly, error: err.message };
		}
		const e = S.weekly = r.summary;
		const cfg = S.config;
		const lists = (e.Lists || []).map(([title, items]) => `<h6>${esc(title)}</h6><ul>${items.map((i) => `<li data-no-i18n>${esc(i)}</li>`).join("")}</ul>`).join("");
		const html = `<div class="weekly">
			<p class="weekly-period">${esc(e.Severity || "")}</p>
			<p class="weekly-headline ${e.Critical ? "down" : ""}">${esc(e.Headline)}</p>
			${(e.Sections || []).length ? `<table class="diff-table"><tbody>${e.Sections.map(([k, v]) => `<tr><td>${esc(k)}</td><td>${esc(v)}</td></tr>`).join("")}</tbody></table>` : ""}
			${lists}
			${r.error ? `<p class="set-msg err" role="status">${esc(r.error)}</p>` : ""}
			${r.sent?.length ? `<p class="set-msg ok" role="status">${tr("Enviado a {c}.", { c: r.sent.join(", ") })}</p>` : ""}
			${cfg.teams || cfg.slack
				? `<button class="cf-btn cf-btn-primary" data-weekly-send>${icon("i-megaphone")}${tr("Enviar ahora a Teams/Slack")}</button>`
				: `<p class="m-hint">${tr("Configura TEAMS_WEBHOOK_URL o SLACK_WEBHOOK_URL para enviarlo, y TRACEREPORTS_WEEKLY_SUMMARY (por ejemplo 'mon 09:00') para que salga solo cada semana.")}</p>`}
		</div>`;
		CF.Drawer.open({ title: e.Title, subtitle: tr("Lo que recibe el equipo"), body: html });
		$("[data-weekly-send]")?.addEventListener("click", (ev) => { ev.currentTarget.disabled = true; openWeekly(true); });
	}

	function bindMetricsEvents() {
		const root = $("#view-metrics");
		root.addEventListener("click", (e) => {
			if (e.target.closest("[data-weekly]")) { openWeekly(); return; }
			const b = e.target.closest("[data-m-days]");
			if (b && (Number(b.dataset.mDays) !== S.metrics.days || S.metrics.custom)) { S.metrics.days = Number(b.dataset.mDays); S.metrics.custom = null; S.rendered["m-body"] = null; loadMetrics(); renderMetrics(); }
		});
		root.addEventListener("change", (e) => {
			const m = S.metrics, id = e.target.id;
			if (id === "m-suite" || id === "m-env" || id === "m-tag") { m[id.slice(2)] = e.target.value; S.rendered["m-body"] = null; loadMetrics(); }
			if ((id === "m-from" || id === "m-to") && e.target.value) {
				m.custom = { ...m.custom, [id.slice(2)]: e.target.value };
				if (m.custom.from > m.custom.to) m.custom = id === "m-from" ? { ...m.custom, to: m.custom.from } : { ...m.custom, from: m.custom.to };
				S.rendered["m-body"] = null; loadMetrics(); renderMetrics();
			}
		});
		root.addEventListener("click", (e) => {
			const m = S.metrics;
			if (e.target.closest("[data-m-custom]")) {
				if (!m.custom) {
					const to = new Date(), from = new Date(Date.now() - (m.days - 1) * 86400000);
					m.custom = { from: from.toLocaleDateString("sv"), to: to.toLocaleDateString("sv") }; // sv = AAAA-MM-DD
					S.rendered["m-body"] = null; loadMetrics(); renderMetrics();
				}
				return;
			}
			if (e.target.closest("[data-m-clear]")) {
				Object.assign(m, { suite: "", env: "", tag: "", custom: null });
				S.rendered["m-body"] = null; loadMetrics(); renderMetrics(); return;
			}
			const tl = e.target.closest("[data-m-test]");
			if (tl) openTestDrawer(m.tests[tl.dataset.mTest]);
		});
		// "Escalar el último fallo" desde el historial de un test
		document.addEventListener("click", (e) => {
			const p = e.target.closest("[data-esc-pick]");
			if (p) { S.esc.test = Number(p.dataset.escPick); CF.Drawer.close(); }
		}, true);
	}

	// ---------- Trace Buddy: asistente de QA ----------
	// Solo se abre cuando se le llama; ocultarlo se recuerda en este navegador (Ajustes → Apariencia lo
	// vuelve a mostrar). Sin LLM responde con lo que TraceReports ya sabe (diagnósticos, la ejecución, la
	// revisión de seguridad); la primera burbuja dice si la IA configurada responde. Solo administración:
	// probar la IA gasta la key del equipo.
	const BUDDY_KEY = "tracereports-buddy";
	// El personaje: un pequeño ingeniero de QA con capucha (web/buddy/). La imagen tiene una sola expresión: el
	// ánimo (data-mood en #buddy: idle, thinking, success, error) se ve en el halo y en la pastilla de estado.
	const BUDDY_LOGO = `<img class="buddy-logo" src="buddy/tracebuddy-avatar.webp" alt="" width="512" height="512" decoding="async" draggable="false">`;
	const BUDDY_MOODS = { idle: "Disponible", thinking: "Analizando…", success: "Listo", error: "Revisa esto" };
	const BUDDY_PROVIDERS = { gemini: "Google Gemini", anthropic: "Anthropic Claude", openai: "OpenAI", openai_compatible: "Compatible con OpenAI", ollama: "Ollama" };
	const BUDDY_HINTS = {
		model_unavailable: "El modelo no está disponible para esta cuenta.",
		no_credits: "La cuenta del proveedor no tiene saldo: agrega créditos o cambia de proveedor.",
		quota_daily: "Se agotó la cuota diaria de este modelo.",
		rate_limit: "Hubo demasiadas llamadas seguidas: espera un momento y vuelve a probar.",
		overloaded: "El modelo está saturado en este momento.",
	};
	const BUDDY_ASKS = [
		["explain", "Explícame este fallo"], ["first", "¿Qué reviso primero?"], ["probe", "¿Funciona la IA?"],
		["connect", "¿Cómo conecto mis tests?"], ["security", "¿Está segura mi instalación?"], ["shots", "¿Por qué no hay capturas?"],
	];
	// "¿Por qué no hay capturas?" solo tiene sentido si el servidor descarta alguna
	const buddyAsks = () => BUDDY_ASKS.filter(([id]) => id !== "shots" || (S.config.screenshots && S.config.screenshots !== "all"));
	// waitTest: el test cuyo diagnóstico se está esperando para explicarlo apenas llegue
	const buddy = { open: false, msgs: [], probing: false, greeted: false, closeTimer: 0, waitTest: null, waitEl: null, chat: [], chatting: false, thoughts: 0 };
	// el chat libre necesita un modelo de lenguaje: el motor local diagnostica, pero no conversa
	const buddyCanChat = () => !!S.config.ai_enabled && !!S.config.ai_provider && S.config.ai_provider !== "builtin";
	const buddyHidden = () => { try { return localStorage.getItem(BUDDY_KEY) === "hidden"; } catch { return false; } };
	const buddyAvailable = () => canAct() && !!S.config.ai_enabled;

	function buddyMount() {
		if (STATIC || $("#buddy")) return;
		const root = document.createElement("div");
		root.id = "buddy";
		root.className = "buddy";
		root.dataset.mood = "idle";
		root.innerHTML = `<div class="buddy-thought" id="buddy-thought" hidden><button class="buddy-thought-text" type="button" data-buddy-thought></button>
				<button class="buddy-thought-x" type="button" data-buddy-quiet aria-label="No mostrar pensamientos">${icon("i-close")}</button></div>
			<div class="buddy-launch"><button class="buddy-fab" id="buddy-fab" type="button" aria-expanded="false" aria-controls="buddy-panel" aria-label="Abrir Trace Buddy"><span class="buddy-glow" aria-hidden="true"></span>${BUDDY_LOGO}<span class="buddy-3d" aria-hidden="true"></span></button>
				<span class="buddy-status" id="buddy-status" aria-hidden="true"><i></i><span>${tr(BUDDY_MOODS.idle)}</span></span></div>
			<section class="buddy-panel" id="buddy-panel" role="dialog" aria-labelledby="buddy-title" hidden>
				<header class="buddy-head"><span class="buddy-avatar">${BUDDY_LOGO}</span>
					<div><b id="buddy-title" data-no-i18n>Trace Buddy</b><small>Tu aliado en QA Automation</small></div>
					<button class="link-btn" type="button" data-buddy-hide>Ocultar</button>
					<button class="buddy-close" type="button" data-buddy-close aria-label="Cerrar">${icon("i-close")}</button></header>
				<div class="buddy-context" id="buddy-context"></div>
				<div class="buddy-log" id="buddy-log" aria-live="polite"></div>
				<div class="buddy-asks" id="buddy-asks" role="group" aria-label="Preguntas">${buddyAsks().map(([id, label]) => `<button class="buddy-ask" type="button" data-buddy-ask="${id}">${label}</button>`).join("")}</div>
				${buddyCanChat() ? `<form class="buddy-chat" id="buddy-chat">
					<textarea id="buddy-input" rows="1" maxlength="2000" placeholder="Pregunta por este fallo o el reporte…" aria-label="Tu pregunta"></textarea>
					<button class="buddy-send" type="submit" aria-label="Enviar">${icon("i-send")}</button></form>
				<p class="buddy-note">${tr("Las sugerencias de la IA se verifican con la evidencia del reporte.")}</p>` : ""}
			</section>`;
		document.body.appendChild(root);
		root.addEventListener("click", (e) => {
			if (e.target.closest("#buddy-fab")) { buddyToggle(!buddy.open); return; }
			if (e.target.closest("[data-buddy-thought]")) { buddyThoughtHide(); buddyToggle(true); return; }
			if (e.target.closest("[data-buddy-quiet]")) { buddyQuiet(); return; }
			if (e.target.closest("[data-buddy-close]")) { buddyToggle(false); return; }
			if (e.target.closest("[data-buddy-hide]")) { buddySetHidden(true); return; }
			const copyBtn = e.target.closest("[data-buddy-copy]");
			if (copyBtn) { copyText(copyBtn.closest(".buddy-tool").querySelector(".buddy-code").textContent, copyBtn); return; }
			const fmt = e.target.closest("[data-buddy-mock-fmt]");
			if (fmt && buddy.mockConn) {
				const card = fmt.closest("[data-buddy-mock]");
				card.querySelectorAll("[data-buddy-mock-fmt]").forEach((b) => b.classList.toggle("on", b === fmt));
				card.querySelector(".buddy-code").textContent = CF.MockGenerator.generate(fmt.dataset.buddyMockFmt, buddy.mockConn);
				return;
			}
			if (e.target.closest("[data-buddy-mock-open]") && buddy.mockConn) { openMockFor(buddy.mockConn); return; }
			if (e.target.closest("[data-buddy-inspector]") && S.test) {
				S.detailTab = "inspector"; renderTestDetail();
				$("#inspector-panel")?.scrollIntoView({ behavior: "smooth", block: "start" });
				return;
			}
			const ask = e.target.closest("[data-buddy-ask]");
			if (ask) { buddyAsk(ask.dataset.buddyAsk, ask.textContent); return; }
			const open = e.target.closest("[data-buddy-test]");
			if (open) { e.preventDefault(); buddyOpenTest(Number(open.dataset.buddyTest)); return; }
			if (e.target.closest("[data-view-link]")) buddyToggle(false); // el link lleva a otra vista
		});
		document.addEventListener("keydown", (e) => { if (e.key === "Escape" && buddy.open) { buddyToggle(false); $("#buddy-fab").focus(); } });
		root.addEventListener("submit", (e) => { if (e.target.id === "buddy-chat") { e.preventDefault(); buddyChatSend(); } });
		root.addEventListener("keydown", (e) => {
			// Enter envía; Shift+Enter hace un salto de línea
			if (e.target.id === "buddy-input" && e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); buddyChatSend(); }
		});
		root.addEventListener("input", (e) => { if (e.target.id === "buddy-input") buddyGrow(e.target); });
		// el aviso de idioma está en Trace Buddy y en la tarjeta del diagnóstico: sus botones valen en toda la página
		document.addEventListener("click", (e) => {
			const setLang = e.target.closest("[data-buddy-lang]");
			if (setLang) { buddySetLanguage(setLang.dataset.buddyLang, Number(setLang.dataset.test), setLang); return; }
			const again = e.target.closest("[data-buddy-reanalyze]");
			if (again) buddyReanalyze(Number(again.dataset.buddyReanalyze), again);
		});
		buddyRefresh();
		buddyLoad3D(root);
		buddy.thoughtTimer = setTimeout(buddyThink, 20000); // el primero, un saludo, a los 20 s
	}

	// ---------- pensamientos: cuando nadie le habla, de vez en cuando dice algo ----------
	// Sin volverse el clip de Office: solo con el panel cerrado y la pestaña visible, uno cada ~90 s como
	// máximo, se va solo a los 7 s, un clic abre el panel y la × lo silencia hasta cerrar la pestaña.
	const BUDDY_QUIET_KEY = "tracereports-buddy-quiet";
	const buddyIsQuiet = () => { try { return sessionStorage.getItem(BUDDY_QUIET_KEY) === "1"; } catch { return false; } };

	/** Qué pensar ahora: algo del reporte que se está viendo si lo hay; si no, saludar o aburrirse. */
	function buddyThoughtText() {
		const t = S.view === "tests" ? S.test : null;
		const failed = S.run ? S.run.tests.filter((x) => x.status === "FAIL").length : 0;
		const context = [];
		if (t?.status === "FAIL") context.push(tr("Este test falló. ¿Te explico qué pasó?"));
		if (failed > 1) context.push(tr("Hay {n} tests en rojo en esta ejecución. ¿Empezamos por el que más rompe?", { n: failed }));
		if (S.run && !failed) context.push(tr("Todo verde por aquí. ¡Buen trabajo!"));
		if (buddyCanChat()) context.push(tr("¡Estoy listo! Pregúntame lo que quieras del reporte."));
		const chatter = [
			tr("¡Hola! Estoy aquí si me necesitas."),
			tr("Ando un poco aburrido… ¿revisamos algo juntos?"),
			tr("¿Sabías que puedo explicarte un fallo con un clic?"),
			tr("Pssst… pásame el mouse por encima."),
			tr("Sin telemetría: lo que veo aquí, aquí se queda 🤐"),
			tr("Vivo en tu servidor, no en la nube. Tus datos no se comparten con nadie 🔒"),
		];
		if (buddy.thoughts === 0) return chatter[0];
		const pool = context.length && Math.random() < 0.65 ? context : chatter;
		return pool[Math.floor(Math.random() * pool.length)];
	}

	function buddyThink() {
		clearTimeout(buddy.thoughtTimer);
		buddy.thoughtTimer = setTimeout(buddyThink, 75000 + Math.random() * 45000);
		const root = $("#buddy");
		if (!root || root.hidden || buddy.open || document.hidden || buddyIsQuiet() || buddy.chatting || buddy.probing) return;
		const text = buddyThoughtText();
		buddySpeak(text, 7000);
		buddy.thoughts = (buddy.thoughts || 0) + 1;
		// cada frase con su gesto: "shhh" para la telemetría, brazos cruzados para cuidar tus datos
		if (text.includes("🤐")) buddy.three?.act("shush", "shush");
		else if (text.includes("🔒")) buddy.three?.act("proud", "proud");
		else buddy.three?.wave();
	}

	/** Dice algo en el globo (un pensamiento o una reacción), con la boca moviéndose. */
	function buddySpeak(text, ms) {
		const root = $("#buddy"), el = $("#buddy-thought");
		if (!root || !el || root.hidden || buddy.open) return;
		el.querySelector("[data-buddy-thought]").textContent = text;
		el.hidden = false;
		requestAnimationFrame(() => el.classList.add("show"));
		buddy.three?.talk(Math.min(3, 0.8 + text.length / 30));
		clearTimeout(buddy.thoughtHide);
		buddy.thoughtHide = setTimeout(buddyThoughtHide, ms);
	}

	// lo que dice el personaje 3D cuando le pasa algo: responde a algo que hizo la persona (cambiar el tema,
	// dejarle el cursor encima), así que sale aunque los pensamientos estén silenciados
	const BUDDY_REACTIONS = {
		changeStart: ["¿Tema nuevo? Necesito cambiar mi outfit… ¡no mires! 🙈", "¡Uy, cambio de look! Dame un segundo…"],
		changeEnd: ["¡Listo! ¿Qué tal me queda?", "Ta-dá ✨ ¿Te gusta mi outfit nuevo?"],
		shy: ["¡Me vas a hacer sonrojar! 😳", "¿Me estás mirando? 😳"],
	};
	function buddyReact(name) {
		const lines = BUDDY_REACTIONS[name];
		if (lines) buddySpeak(tr(lines[Math.floor(Math.random() * lines.length)]), name === "changeStart" ? 3200 : 3600);
	}

	function buddyThoughtHide() {
		const el = $("#buddy-thought");
		if (!el || el.hidden) return;
		el.classList.remove("show");
		setTimeout(() => { if (!el.classList.contains("show")) el.hidden = true; }, 220);
	}

	function buddyQuiet() {
		try { sessionStorage.setItem(BUDDY_QUIET_KEY, "1"); } catch { /* solo esta vez */ }
		buddyThoughtHide();
	}

	/** El personaje en 3D (web/buddy/buddy3d.js, con three.js): se carga después de que la página está lista y
	 * solo para quien ve a Trace Buddy. Si algo falla o no hay WebGL, se queda la imagen. */
	function buddyLoad3D(root) {
		if (root.hidden || buddy.three !== undefined) return;
		buddy.three = null; // cargando
		const start = () => import("./buddy/buddy3d.js").then((m) => {
			const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;
			const ctl = m.mountBuddy3D(root.querySelector(".buddy-3d"), { theme: document.body.dataset.theme, reducedMotion, onEvent: buddyReact });
			if (!ctl) return;
			buddy.three = ctl;
			root.classList.add("has-3d");
			ctl.setMood(root.dataset.mood || "idle");
		}).catch((err) => console.warn("Trace Buddy 3D:", err));
		(window.requestIdleCallback || ((f) => setTimeout(f, 1200)))(start);
	}

	/** Muestra u oculta el botón según el permiso, la IA y la preferencia del navegador. */
	function buddyRefresh() {
		const root = $("#buddy");
		if (!root) return;
		const show = buddyAvailable() && !buddyHidden();
		if (!show && buddy.open) buddyToggle(false);
		root.hidden = !show;
		buddy.three?.setVisible(show);
		if (show) buddyLoad3D(root);
	}

	function buddyMood(mood, ms = 0) {
		const root = $("#buddy");
		if (!root) return;
		clearTimeout(buddy.moodTimer);
		const set = (m) => {
			root.dataset.mood = m;
			buddy.three?.setMood(m);
			const label = $("#buddy-status span"), status = tr(BUDDY_MOODS[m] || BUDDY_MOODS.idle);
			if (label) label.textContent = status;
			$("#buddy-fab")?.setAttribute("aria-label", `${tr("Abrir Trace Buddy")} · ${status}`);
		};
		set(mood);
		if (ms) buddy.moodTimer = setTimeout(() => set(buddy.probing || buddy.waitTest || buddy.chatting ? "thinking" : "idle"), ms);
	}

	function buddySetHidden(hidden) {
		try { localStorage.setItem(BUDDY_KEY, hidden ? "hidden" : "shown"); } catch { /* sin almacenamiento: solo esta sesión */ }
		if (hidden) {
			buddyToggle(false);
			const root = $("#buddy");
			root.classList.add("leaving"); // se despide encogiéndose antes de ocultarse
			setTimeout(() => { root.classList.remove("leaving"); buddyRefresh(); }, 260);
		} else buddyRefresh();
		if (S.view === "settings") renderSettings(true);
	}

	/** La barra bajo la cabecera: la IA que responde de verdad (o el motor local) y el test que se está viendo. */
	function buddyContext() {
		const bar = $("#buddy-context");
		if (!bar) return;
		const llm = buddyCanChat();
		const mode = llm ? `${tr("IA conectada")} · <span data-no-i18n>${esc(S.config.ai_model || S.config.ai_provider)}</span>` : tr("Motor local");
		const t = S.view === "tests" && S.test ? S.test : null;
		const what = t ? `<span class="buddy-context-test" data-no-i18n title="${esc(t.name)}">${esc(t.name)}</span>`
			: S.run ? `<span class="buddy-context-test" data-no-i18n>${esc(S.run.name)}</span>` : "";
		bar.innerHTML = `<span class="buddy-mode ${llm ? "llm" : "local"}">${mode}</span>${what}`;
	}

	function buddyToggle(open) {
		const panel = $("#buddy-panel"), fab = $("#buddy-fab");
		if (!panel || buddy.open === open) return;
		buddy.open = open;
		fab.setAttribute("aria-expanded", String(open));
		buddy.three?.setPanel(open);
		fab.classList.toggle("active", open);
		clearTimeout(buddy.closeTimer);
		if (open) {
			buddyThoughtHide();
			fab.classList.remove("news");
			buddyContext();
			panel.hidden = false;
			requestAnimationFrame(() => panel.classList.add("open")); // un cuadro después, para que la transición corra
			if (!buddy.greeted) {
				buddy.greeted = true;
				buddySay(tr("¡Hola! Soy Trace Buddy. Te ayudo a entender los fallos de tus tests y a revisar tu instalación. Primero veo si la IA responde…"));
				buddyProbe(false);
			}
			setTimeout(() => panel.querySelector(".buddy-ask")?.focus(), 60);
		} else {
			panel.classList.remove("open");
			buddy.closeTimer = setTimeout(() => { panel.hidden = true; }, 240);
		}
	}

	const BUDDY_TYPING = `<span class="buddy-typing" aria-hidden="true"><i></i><i></i><i></i></span>`;

	/** El personaje 3D mueve la boca un rato, según lo largo de lo que dice. */
	function buddyTalk(el) {
		buddy.three?.talk(Math.min(4, 0.8 + (el.textContent || "").length / 70));
	}

	/** Agrega una burbuja y la devuelve (para completarla después con buddyFill). */
	function buddySay(html, from = "bot") {
		buddy.msgs.push({ html, from });
		const log = $("#buddy-log");
		if (!log) return null;
		const el = document.createElement("div");
		el.className = `buddy-msg ${from}`;
		el.innerHTML = html;
		log.appendChild(el);
		if (from === "bot" && !html.includes("buddy-typing")) buddyTalk(el);
		log.scrollTop = log.scrollHeight;
		return el;
	}

	/** Completa una burbuja que estaba "escribiendo…"; con el panel cerrado, el botón avisa que hay respuesta.
	 * Una respuesta a una pregunta va al final, donde se está mirando, si hubo mensajes después; un aviso que no
	 * responde a nada (el estado de la IA, inPlace) se queda donde empezó, para no meterse entre pregunta y respuesta. */
	function buddyFill(el, html, inPlace = false) {
		if (el?.isConnected && (inPlace || el === el.parentNode.lastElementChild)) { el.innerHTML = html; el.classList.remove("waiting"); buddyTalk(el); }
		else { el?.remove(); buddySay(html); }
		const log = $("#buddy-log");
		if (log) log.scrollTop = log.scrollHeight;
		if (!buddy.open) $("#buddy-fab")?.classList.add("news");
	}

	function buddyAsk(id, label) {
		buddySay(esc(label), "me");
		buddy.three?.nod();
		({ explain: buddyExplain, first: buddyFirst, probe: () => buddyProbe(true), connect: buddyConnect, security: buddySecurity, shots: buddyShots }[id] || (() => {}))();
	}

	async function buddyProbe(force) {
		if (buddy.probing) return;
		buddy.probing = true;
		$$("[data-buddy-ask=probe]").forEach((b) => (b.disabled = true));
		const el = buddySay(BUDDY_TYPING);
		el?.classList.add("waiting");
		buddyMood("thinking");
		let r;
		try {
			r = await apiSend("POST", `/api/v1/ui/ai/probe${force ? "?force=1" : ""}`, {});
		} catch (err) {
			r = { ok: false, state: "error", error: err.message };
		}
		buddy.probing = false;
		$$("[data-buddy-ask=probe]").forEach((b) => (b.disabled = false));
		buddyFill(el, buddyProbeText(r), !force); // la del saludo queda bajo el saludo; "¿Funciona la IA?" responde a esa pregunta
		buddyMood(r.ok ? "success" : r.state === "off" ? "idle" : "error", 2600);
	}

	/** La respuesta de /ui/ai/probe en palabras simples. */
	function buddyProbeText(r) {
		const aiLink = `<a href="#" data-view-link="settings" data-set-tab="ai">${tr("Ajustes → Inteligencia artificial")}</a>`;
		if (r.state === "off") return tr("Mi IA está apagada en este servidor.") + ` ${aiLink}`;
		if (r.state === "local") {
			return `<b>${tr("Uso el motor local de reglas")}</b> <span data-no-i18n>(${esc(r.model)})</span>. `
				+ tr("Diagnostico los fallos sin que nada salga del servidor, pero todavía no puedo conversar libremente. Para chatear conmigo, configura Ollama (un modelo en tu red) en")
				+ ` ${aiLink}.`;
		}
		const name = esc(tr(BUDDY_PROVIDERS[r.provider] || r.provider || ""));
		if (r.ok) {
			return `<span class="buddy-ok">✓</span> ${tr("¡Estoy conectado! Pienso con")} <b data-no-i18n>${name}</b> · <span data-no-i18n>${esc(r.model)}</span> · `
				+ tr("respondí en {t}", { t: fmtDur(r.ms) });
		}
		const why = BUDDY_HINTS[r.hint] ? tr(BUDDY_HINTS[r.hint]) : `<span data-no-i18n>${esc(r.error || "")}</span>`;
		const tryModel = r.suggest ? ` ${tr("Prueba con el modelo {m}.", { m: r.suggest })}` : "";
		return `<span class="buddy-bad">✗</span> ${tr("No pude conectar con")} <b data-no-i18n>${name}</b>${r.model ? ` <span data-no-i18n>(${esc(r.model)})</span>` : ""}: ${why}${esc(tryModel)} ${tr("Revisa")} ${aiLink}.`;
	}

	// palabras que piden una herramienta del reporte: el cURL de la llamada que falló o un mock de ese backend
	const BUDDY_TOOL_WORDS = {
		curl: /\b(curl|postman|insomnia|replic\w*|reproduc\w*|request|solicitud|petici[oó]n|endpoint|llamada)\b/i,
		mock: /\b(mock\w*|stub\w*|wiremock|simul\w*|intercept\w*)\b/i,
		inspector: /\b(dom|html|xpath|css|selector\w*|locator\w*|localizador\w*|elemento\w*|element\w*|esquelet\w*|skeleton\w*|spinner|cargando|loading|loader|wait|espera\w*|inspector|id)\b/i,
	};
	function buddyToolsFor(text, testId) {
		if (!testId) return [];
		return Object.keys(BUDDY_TOOL_WORDS).filter((k) => BUDDY_TOOL_WORDS[k].test(text));
	}

	/** El cURL de la llamada que falló, como lo arma "Copiar como cURL" (secretos como $VARIABLES). */
	function buddyCurlCard(curl) {
		return `<div class="buddy-tool"><b>${tr("cURL de la llamada que falló")}</b>
			<pre class="buddy-code" data-no-i18n>${esc(curl)}</pre>
			<div class="buddy-tool-actions"><button class="buddy-tool-btn" type="button" data-buddy-copy>${icon("i-copy")}${tr("Copiar")}</button></div>
			<small>${tr("En Postman: Import → Raw text → pega el cURL. Cambia las variables como $TOKEN por su valor real.")}</small></div>`;
	}

	/** Un mock del backend que falló (el mismo generador del botón "Generar mock"), con sus formatos. */
	async function buddyMockCard(testId) {
		const list = await loadNetwork(testId).catch(() => []);
		const conn = list.find((c) => isNetError(c) && c.status) || list.find(isNetError);
		if (!conn) return "";
		buddy.mockConn = conn;
		const first = CF.MockGenerator.FORMATS.find((f) => f.id === "wiremock") || CF.MockGenerator.FORMATS[0];
		return `<div class="buddy-tool" data-buddy-mock><b>${tr("Mock del backend que falló")}</b> <span class="m-hint" data-no-i18n>${esc(conn.method)} ${esc(conn.status || "")}</span>
			<div class="buddy-tool-formats">${CF.MockGenerator.FORMATS.map((f) => `<button type="button" class="buddy-tool-fmt ${f.id === first.id ? "on" : ""}" data-buddy-mock-fmt="${f.id}" data-no-i18n>${esc(f.label)}</button>`).join("")}</div>
			<pre class="buddy-code" data-no-i18n>${esc(CF.MockGenerator.generate(first.id, conn))}</pre>
			<div class="buddy-tool-actions"><button class="buddy-tool-btn" type="button" data-buddy-copy>${icon("i-copy")}${tr("Copiar")}</button>
				<button class="buddy-tool-btn" type="button" data-buddy-mock-open>${tr("Abrir el generador")}</button></div></div>`;
	}

	/** La página del fallo está guardada: el Inspector la muestra para probar el XPath y copiar el código. */
	function buddyInspectorCard() {
		return `<div class="buddy-tool"><b>${tr("La página del momento del fallo")}</b>
			<small>${tr("Ábrela en el Inspector: prueba el XPath sobre ella y, con clic derecho sobre un elemento, copia un XPath robusto y la espera para Selenium o Playwright.")}</small>
			<div class="buddy-tool-actions"><button class="buddy-tool-btn" type="button" data-buddy-inspector>${icon("i-target")}${tr("Abrir en el Inspector")}</button></div></div>`;
	}

	function buddyGrow(el) {
		el.style.height = "auto";
		el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
	}

	/** Una pregunta libre: va con la evidencia del test (o la ejecución) que se está viendo y las últimas
	 * respuestas de la conversación. El servidor la pasa por la redacción antes de enviarla al modelo. */
	/** Abre Trace Buddy y le hace una pregunta armada por la interfaz (p. ej. "Explícamelo" de la comparación).
	 * No se llama buddyAsk: esa es la de las preguntas sugeridas (seguridad, ¿funciona la IA?…), y con el
	 * mismo nombre esta la reemplazaba y cada sugerencia le llegaba al modelo como texto ("security"). */
	async function buddyAskText(text) {
		if (!buddyCanChat()) return;
		buddyToggle(true);
		const input = $("#buddy-input");
		if (!input || buddy.chatting) return;
		input.value = text;
		await buddyChatSend();
	}

	async function buddyChatSend() {
		const input = $("#buddy-input"), text = input?.value.trim();
		if (!text || buddy.chatting) return;
		input.value = "";
		buddyGrow(input);
		buddySay(esc(text), "me");
		buddy.three?.nod();
		buddy.chat.push({ role: "user", text });
		buddy.chatting = true;
		$("#buddy-chat .buddy-send").disabled = true;
		const el = buddySay(BUDDY_TYPING);
		el?.classList.add("waiting");
		buddyMood("thinking");
		const testId = S.view === "tests" && S.test ? S.test.id : 0;
		const tools = buddyToolsFor(text, testId);
		try {
			const r = await apiSend("POST", "/api/v1/ui/ai/chat", { run_id: S.runId || 0, test_id: testId, lang: I18N.lang, messages: buddy.chat.slice(-8), tools });
			buddy.chat.push({ role: "assistant", text: r.answer });
			buddyFill(el, `<div class="buddy-answer" data-no-i18n>${esc(r.answer).replace(/\n/g, "<br>")}</div>`);
			if (tools.includes("curl") && r.curl) buddySay(buddyCurlCard(r.curl));
			if (tools.includes("mock")) { const card = await buddyMockCard(testId); if (card) buddySay(card); }
			if (tools.includes("inspector") && S.test?.id === testId && hasInspector(S.test)) buddySay(buddyInspectorCard());
			buddyMood("success", 2000);
		} catch (err) {
			buddy.chat.pop(); // la pregunta sin respuesta no queda en el historial que viaja al modelo
			buddyFill(el, `${tr("No pude responder:")} <span data-no-i18n>${esc(err.message)}</span>`);
			buddyMood("error", 2600);
		}
		buddy.chatting = false;
		$("#buddy-chat .buddy-send").disabled = false;
		input.focus();
	}

	function buddyExplain() {
		const t = S.view === "tests" ? S.test : null;
		if (t && t.status === "FAIL" && t.triage?.state === "PENDING") { buddyWait(t); return; }
		if (!t || t.status !== "FAIL") {
			const first = S.run?.tests.find((x) => x.status === "FAIL");
			buddySay(first
				? `${tr("Este test no falló.")} <a href="#" data-buddy-test="${first.id}">${tr("Abrir el primer test fallido y explicarlo")}</a>`
				: tr("Abre un test fallido (en rojo) en Tests y vuelve a preguntarme: te explico qué pasó y qué revisar."));
			return;
		}
		buddySay(buddyExplainText(t));
	}

	/** Un diagnóstico en curso: se responde solo cuando llega (loadTest avisa con buddyTestLoaded). */
	function buddyWait(t) {
		if (buddy.waitTest === t.id) { buddySay(tr("Sigo en eso: te respondo aquí apenas termine.")); return; }
		buddy.waitTest = t.id;
		buddy.waitEl = buddySay(`${tr("Lo estoy analizando… te respondo aquí apenas termine.")} ${BUDDY_TYPING}`);
		buddy.waitEl?.classList.add("waiting");
		buddyMood("thinking");
	}

	function buddyTestLoaded(t) {
		if (buddy.open) buddyContext();
		if (buddy.waitTest !== t.id || t.triage?.state === "PENDING") return;
		const el = buddy.waitEl;
		buddy.waitTest = buddy.waitEl = null;
		buddyFill(el, buddyExplainText(t));
		buddyMood(t.triage?.state === "DONE" ? "success" : "error", 2600);
	}

	function buddyExplainText(t) {
		const tri = t.triage;
		if (!tri || tri.state !== "DONE") {
			return tr("Este fallo no tiene diagnóstico automático.") + (tri?.error ? ` <span data-no-i18n>${esc(tri.error)}</span>` : "")
				+ ` ${tr("Mira el error y los pasos del test, o usa Re-analizar en la vista IA.")}`;
		}
		const who = tri.provider === "builtin" ? tr("Lo analicé con mis reglas locales")
			: `${tr("Lo analicé con")} <span data-no-i18n>${esc(S.config.ai_model || tri.provider || "")}</span>`;
		return `<b data-no-i18n>${esc(t.name)}</b> <span class="ai-cat ${esc(tri.category)}">${esc(tri.category)}</span>
			<p data-no-i18n>${esc(tri.summary)}</p>${tri.suggestion ? `<p class="buddy-next" data-no-i18n>→ ${esc(tri.suggestion)}</p>` : ""}
			<small>${who}${tri.rule ? ` · <span data-no-i18n>${esc(tri.rule)}</span>` : ""}</small>${buddyLangNote(t)}`;
	}

	// palabras frecuentes que solo usa un idioma: alcanzan para distinguir un diagnóstico en español de uno en inglés
	const LANG_WORDS = {
		es: new Set(["el", "la", "los", "las", "del", "que", "en", "una", "por", "con", "para", "se", "fue", "antes", "falló", "está", "respondió", "revisa"]),
		en: new Set(["the", "of", "and", "to", "in", "an", "is", "was", "for", "with", "that", "before", "failed", "on", "check", "it"]),
	};
	/** El idioma de un texto ("es", "en", o "" si no se distingue con claridad). */
	function textLang(s) {
		let es = 0, en = 0;
		for (const w of String(s || "").toLowerCase().match(/[a-záéíóúñü]+/g) || []) {
			if (LANG_WORDS.es.has(w)) es++;
			if (LANG_WORDS.en.has(w)) en++;
		}
		if (es + en < 3) return "";
		return es > en * 1.5 ? "es" : en > es * 1.5 ? "en" : "";
	}

	/** Un diagnóstico en otro idioma que la interfaz: re-analizarlo, si el servidor ya escribe en el de la
	 * interfaz; si no, dónde se cambia (re-analizar daría el mismo idioma). */
	function buddyLangNote(t) {
		const tri = t.triage, ui = I18N.lang, got = textLang(`${tri.summary} ${tri.suggestion || ""}`);
		if (!got || got === ui || !LANG_NAME[ui]) return "";
		const was = tr(LANG_NAME[got]), want = tr(LANG_NAME[ui]);
		if (S.config.ai_language === ui) {
			return `<p class="buddy-lang">${tr("Este diagnóstico está en {a}.", { a: was })} <button class="link-btn" type="button" data-buddy-reanalyze="${t.id}">${tr("Re-analizarlo en {b}", { b: want })}</button></p>`;
		}
		// el servidor escribe en otro idioma: con Ajustes editable, un clic lo fija para todos y re-analiza este
		if (S.config.settings_editable) {
			return `<p class="buddy-lang">${tr("Este diagnóstico está en {a}.", { a: was })} <button class="link-btn" type="button" data-buddy-lang="${ui}" data-test="${t.id}">${tr("Escribir los diagnósticos en {b}", { b: want })}</button></p>`;
		}
		return `<p class="buddy-lang">${tr("Este diagnóstico está en {a}. Ajustes está bloqueado: para recibirlos en {b}, agrega TRACEREPORTS_AI_LANGUAGE={l} al .env y reinicia con docker compose up -d.", { a: was, b: want, l: ui })}</p>`;
	}

	/** Fija el idioma de los diagnósticos del servidor (Ajustes → IA) y re-analiza el test en ese idioma. */
	async function buddySetLanguage(lang, id, btn) {
		btn.disabled = true;
		try {
			await apiSend("PUT", "/api/v1/settings", { ai_language: lang });
		} catch (err) {
			btn.disabled = false;
			buddySay(`${tr("No pude cambiar el idioma:")} <span data-no-i18n>${esc(err.message)}</span>`);
			return;
		}
		S.config.ai_language = lang;
		S.settings.data = null; // Ajustes vuelve a leerse con el idioma nuevo
		buddySay(tr("Listo: desde ahora los diagnósticos se escriben en {b}. Re-analizo este…", { b: tr(LANG_NAME[lang]) }));
		await buddyReanalyze(id, btn);
	}

	async function buddyReanalyze(id, btn) {
		btn.disabled = true;
		try {
			await apiSend("POST", `/api/v1/ui/tests/${id}/analyze`, {});
		} catch (err) {
			btn.disabled = false;
			buddySay(`${tr("No pude pedir el análisis:")} <span data-no-i18n>${esc(err.message)}</span>`);
			return;
		}
		buddyWait({ id });
		// el test ya está PENDING en el servidor: recargarlo arranca el seguimiento y la respuesta llega sola
		await loadRun();
		if (S.testId === id) await loadTest();
	}

	async function buddyOpenTest(id) {
		S.testId = id;
		setView("tests");
		await loadTest();
		buddyExplain();
	}

	function buddyFirst() {
		const r = S.run;
		if (!r) {
			buddySay(tr("Todavía no hay ejecuciones. Empieza conectando tus tests:") + ` <a href="#" data-view-link="settings" data-set-tab="conn">${tr("Conectar tus tests")}</a>.`);
			return;
		}
		const failed = r.tests.filter((t) => t.status === "FAIL");
		if (!failed.length) { buddySay(tr("En esta ejecución no falló ningún test. Nada urgente que revisar.")); return; }
		const inc = r.summary?.incidents || [];
		const top = inc.slice(0, 3).map((i) => `<li><span data-no-i18n>${esc(i.title)}</span> · ${plural(i.test_ids.length, "test", "tests")}</li>`).join("");
		// el titular del diagnóstico ya dice cuántos fallaron; sin él, el conteo
		const head = r.summary?.headline ? `<b data-no-i18n>${esc(r.summary.headline)}</b>`
			: failed.length === 1 ? tr("Falló 1 test en esta ejecución.") : tr("Fallaron {n} tests en esta ejecución.", { n: failed.length });
		buddySay(`${head}
			${top ? `<p>${tr("Empieza por las causas que rompen más tests:")}</p><ol class="buddy-list">${top}</ol>` : ""}
			<p><a href="#" data-view-link="exceptions">${tr("Ver Errores")}</a> · <a href="#" data-view-link="dashboard">${tr("Ver Análisis")}</a></p>`);
	}

	function buddyConnect() {
		buddySay(tr("Tus tests envían sus resultados a este servidor con un cliente (pytest, Playwright, Selenium, Java…) y un token de ingesta. Los pasos para cada framework están en")
			+ ` <a href="#" data-view-link="settings" data-set-tab="conn">${tr("Ajustes → Conectar tus tests")}</a>.`);
	}

	function buddyShots() {
		const p = S.config.screenshots;
		const t = S.view === "tests" ? S.test : null;
		const here = t ? shotPolicyText(t) : "";
		const why = p === "off" ? tr("Este servidor no guarda ninguna captura, ni el trace ni el video (TRACEREPORTS_SCREENSHOTS=off).")
			: tr("Este servidor solo guarda las capturas de los tests y pasos que fallan (TRACEREPORTS_SCREENSHOTS=failures): las de un test que pasó se descartan antes de guardarse.");
		buddySay(`<p>${here || why}</p>
			<p>${tr("Es a propósito: una captura puede mostrar datos personales o claves en pantalla. Para guardarlas todas, quien administra el servidor pone TRACEREPORTS_SCREENSHOTS=all en el .env y reinicia con docker compose up -d. Las capturas anteriores no reaparecen.")}</p>`);
	}

	async function buddySecurity() {
		let p = S.settings.posture;
		if (!p || p.error) {
			try { p = S.settings.posture = await api("/api/v1/security/posture"); } catch (err) { p = { error: err.message }; }
		}
		const link = `<a href="#" data-view-link="settings" data-set-tab="security">${tr("Revisión de seguridad")}</a>`;
		if (p.error) { buddySay(tr("No pude leer la revisión de seguridad.") + ` <span data-no-i18n>${esc(p.error)}</span>`); return; }
		const issues = p.checks.filter((c) => c.level !== "ok");
		if (!issues.length) { buddySay(`<span class="buddy-ok">✓</span> ${tr("Los {n} chequeos de seguridad están en orden.", { n: p.checks.length })} ${link}`); buddyMood("success", 2600); return; }
		buddyMood("error", 2600);
		const first = issues.sort((a, b) => (a.level === "fail" ? 0 : 1) - (b.level === "fail" ? 0 : 1))[0];
		buddySay(`${tr("Hay {n} cosas por revisar.", { n: issues.length })} ${tr("La más importante:")} <b>${esc(postureTitle(first))}</b>
			<p data-no-i18n>${esc(postureText(first.detail))}</p>${first.fix ? `<p class="buddy-next" data-no-i18n>→ ${esc(postureText(first.fix))}</p>` : ""}<p>${link}</p>`);
	}

	// ---------- polling (live updates) ----------
	function needsPolling() {
		if (!S.run || S.liveState === "live") return false; // con SSE no hace falta consultar
		return (S.view === "escalate" && (S.esc.data || S.esc.loading))
			|| S.run.status === "RUNNING" || S.run.summary?.state === "PENDING"
			|| S.run.tests.some((t) => t.status === "RUNNING" || t.triage?.state === "PENDING");
	}

	async function pollLoop() {
		try {
			if (needsPolling()) {
				await loadRun();
				if (S.view === "tests") await loadTest();
			}
		} catch (err) { console.warn("poll failed", err); }
		setTimeout(pollLoop, POLL_MS);
	}

	async function runsLoop() {
		if (S.liveState === "live") { setTimeout(runsLoop, RUNS_POLL_MS); return; }
		try {
			await loadRuns();
			catchUpNewRuns(); // sin conexión en vivo, el sondeo también avisa las ejecuciones nuevas
			S.histGen++; // y sin eventos no se sabe qué terminó: el historial se vuelve a pedir
			if (!S.run && S.runId) { await loadRun(); await loadTest(); }
		} catch (err) { console.warn("runs refresh failed", err); }
		setTimeout(runsLoop, RUNS_POLL_MS);
	}

	// ---------- boot ----------
	async function init() {
		applyTheme(initialTheme(), false);
		$(".nav-global").hidden = !!STATIC; // las métricas cruzan ejecuciones: no van en el ZIP
		$(".nav-maint").hidden = !!STATIC; // mantenimiento actúa sobre el servidor
		// exportado: Escalar y Release van ya calculados (los reportes de versiones anteriores no los traen)
		$(".nav-server").hidden = !!STATIC && !STATIC.escalations;
		$(".nav-release").hidden = !!STATIC && !STATIC.release;
		readHash();
		bindEvents();
		CF.Tips.init();
		try {
			S.config = await api("/api/v1/config");
			window.TraceReportsI18n.setDefault(S.config.language); // idioma del equipo, si el usuario no eligió
			await loadRuns();
			S.seenRuns = new Set(S.runs.map((r) => r.id)); // las que ya estaban al abrir la página no se avisan
			await loadRun();
			await loadTest();
			writeHash();
		} catch (err) {
			console.error(err);
		}
		if (GLOBAL_VIEWS.has(S.view)) renderView(); // métricas y ajustes funcionan aunque no haya ejecuciones
		buddyMount();
		if (S.view === "search" && !STATIC) await loadRunSearch();
		if (S.view === "live" && !STATIC) loadLiveView();
		if (STATIC) return; // exported snapshot: nothing to poll
		try { S.autoScroll = localStorage.getItem("tracereports-autoscroll") !== "off"; } catch { /* ignore */ }
		new CF.LiveStream("/api/v1/stream", { onEvent: onLive, onStatus: setLiveState }).start();
		setInterval(renderLive, 30000); // pasa a "Sin actividad" aunque no lleguen eventos
		// "sin señal" aparece justamente cuando no llega nada: En vivo se vuelve a pedir cada 10 s
		setInterval(() => { if (S.view === "live" && !document.hidden) loadLiveView(); }, 10000);
		setTimeout(pollLoop, POLL_MS);
		setTimeout(runsLoop, RUNS_POLL_MS);
		sessionWatch();
	}

	// Con la página de ingreso: el usuario y Salir arriba, y un aviso 2 min antes de que la sesión venza
	// por inactividad ("Seguir conectado" la renueva). Se consulta cada 30 s sin contar como uso.
	async function sessionWatch() {
		const check = async () => {
			let s;
			try { s = await (await fetch("/api/v1/auth/session", { headers: { Accept: "application/json" } })).json(); } catch { return; }
			SESSION.info = s;
			const btn = $("#session-btn"), warn = $("#session-warn"), box = $("#session-picker");
			if (!btn || !warn || !box) return;
			box.hidden = !s.logged_in;
			if (!s.logged_in) {
				warn.hidden = true;
				if (s.login_required && s.expired) toLogin(new Response(JSON.stringify({ error: "session expired" })));
				return;
			}
			$("#session-user").textContent = s.user;
			const until = (ms) => new Date(ms).toLocaleTimeString(I18N.lang === "en" ? "en-GB" : "es-CL", { hour: "2-digit", minute: "2-digit" });
			btn.dataset.tip = tr("Sesión de {u} ({r}) · vence a las {h} sin uso · Clic para salir", { u: s.user, r: s.role === "viewer" ? tr("solo lectura") : "admin", h: until(s.idle_until) });
			$("#session-confirm-d").textContent = tr("Sales como {u}. Para volver, ingresa de nuevo.", { u: s.user });
			const left = s.idle_until - Date.now();
			warn.hidden = !(left > 0 && left < 120_000);
			if (!warn.hidden) $("#session-warn-text").textContent = tr("Tu sesión vence en {n} s por inactividad.", { n: Math.max(1, Math.round(left / 1000)) });
		};
		// salir pide confirmación: una ventanita anclada al botón (Cancelar con el foco: lo seguro primero);
		// Esc o un clic fuera la cierran y el foco vuelve al botón
		const confirmBox = $("#session-confirm"), sessBtn = $("#session-btn");
		const openConfirm = (open) => {
			if (!confirmBox || !sessBtn) return;
			confirmBox.hidden = !open;
			sessBtn.setAttribute("aria-expanded", String(open));
			if (open) confirmBox.querySelector("[data-session-cancel]").focus();
			else sessBtn.focus();
		};
		sessBtn?.addEventListener("click", () => openConfirm(confirmBox.hidden));
		confirmBox?.addEventListener("click", async (e) => {
			if (e.target.closest("[data-session-cancel]")) return openConfirm(false);
			const out = e.target.closest("[data-session-out]");
			if (!out) return;
			out.disabled = true;
			try { await fetch("/api/v1/auth/logout", { method: "POST", headers: { Accept: "application/json" } }); } catch { /* igual se va */ }
			location.href = "/login/?bye=1";
		});
		document.addEventListener("keydown", (e) => { if (e.key === "Escape" && confirmBox && !confirmBox.hidden) { e.stopPropagation(); openConfirm(false); } }, true);
		document.addEventListener("pointerdown", (e) => { if (confirmBox && !confirmBox.hidden && !e.target.closest("#session-picker")) { confirmBox.hidden = true; sessBtn.setAttribute("aria-expanded", "false"); } });
		$("#session-warn")?.addEventListener("click", async (e) => {
			if (!e.target.closest("[data-session-stay]")) return;
			SESSION.lastInput = Date.now();
			try { await fetch("/api/v1/config", { headers: { Accept: "application/json" } }); } catch { /* sigue el aviso */ }
			check();
		});
		await check();
		if (!SESSION.info?.login_required) return; // sin login configurado no hay nada que vigilar
		setInterval(check, 30_000);
	}

	init();
})();
