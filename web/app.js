/* TraceReports frontend — vanilla JS, no build step. */
(() => {
	"use strict";

	const POLL_MS = 3000;
	const RUNS_POLL_MS = 10000;
	const STATUS_ICON = { PASS: "i-pass", FAIL: "i-fail", WARNING: "i-warning", SKIP: "i-skip", INFO: "i-info", RUNNING: "i-running" };
	const AI_LABEL = {
		LOCATOR_CHANGED: "Locator cambiado",
		BACKEND_TIMEOUT: "Timeout de backend",
		LOGIC_BUG: "Bug de lógica",
		INFRA_ERROR: "Error de infraestructura",
	};

	const S = {
		config: { ai_enabled: false, ai_model: "" },
		runs: [], runId: null, run: null,
		view: "tests", testId: null, test: null,
		status: "", search: "", stepFilter: "",
		detailTab: "steps", net: { key: null, list: null }, netFilter: "", netSearch: "", netApiOnly: false,
		hist: {}, insights: { key: null }, tlAll: false, epAll: false,
		loc: {}, locLang: "python", replay: { testId: null, index: 0, speed: 1 }, player: null,
		liveState: "off", autoScroll: true, activity: {}, newRuns: [], toastClosed: false,
		catSel: null, excSel: null,
		metrics: { days: 30, suite: "", env: "", tag: "", custom: null, data: null, loading: false, error: null, tests: {} },
		runSearch: { q: "", project: "", environment: "", branch: "", tag: "", owner: "", status: "", from: "", to: "", incomplete: false, flaky: false, sort: "recent", items: [], cursor: "", total: 0, loading: false, error: null, selected: 0, compare: [], facets: {}, timer: 0, newCount: 0 },
		aiv: { rec: {}, recKey: null, busy: false, msg: null },
		esc: { test: 0, audience: "business", lang: window.TraceReportsI18n.lang, noAI: null, data: null, loading: false, msg: null, loadedKey: null, evidence: 0 },
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

	async function api(path) {
		if (STATIC) return staticApi(path);
		const res = await fetch(path, { headers: { Accept: "application/json" } });
		if (!res.ok) throw new Error(`${res.status} ${path}`);
		return res.json();
	}

	// ---------- routing (hash) ----------
	function readHash() {
		const p = new URLSearchParams(location.hash.slice(1));
		if (p.get("view")) S.view = p.get("view");
		if (p.get("run")) S.runId = Number(p.get("run"));
		if (p.get("test")) S.testId = Number(p.get("test"));
		if (p.get("view") === "search") {
			const x = S.runSearch;
			["q", "project", "environment", "branch", "tag", "owner", "status", "from", "to", "sort"].forEach((k) => { x[k] = p.get(k) || (k === "sort" ? "recent" : ""); });
			x.incomplete = p.get("incomplete") === "true"; x.flaky = p.get("flaky") === "true";
		}
	}
	function writeHash() {
		const p = new URLSearchParams();
		if (S.runId) p.set("run", S.runId);
		p.set("view", S.view);
		if (S.testId && S.view === "tests") p.set("test", S.testId);
		if (S.view === "search") {
			const x = S.runSearch;
			["q", "project", "environment", "branch", "tag", "owner", "status", "from", "to"].forEach((k) => { if (x[k]) p.set(k, x[k]); });
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
			`<option value="${r.id}">#${r.id} · ${esc(r.name)}${r.status === "RUNNING" ? " (en curso)" : r.incomplete ? " (incompleta)" : ""}${r.branch ? ` · ${esc(r.branch)}` : ""} — ${fmtDateTime(r.started_at)}</option>`).join("");
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
		if (S.run && JSON.stringify(S.run) !== JSON.stringify(run)) invalidateEscalation();
		S.run = run;
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
		if (S.test?.id === id && JSON.stringify(S.test) !== JSON.stringify(test)) {
			invalidateEscalation();
			if (S.view === "escalate") renderEscalate();
		}
		S.test = test;
		renderTestDetail();
	}

	// ---------- header ----------
	function renderHeader() {
		const r = S.run;
		$("#report-name").textContent = r.name;
		if ($("#run-select").value !== String(r.id)) $("#run-select").value = String(r.id);
		$("#report-env").textContent = r.environment || "";
		$("#suite-started").textContent = fmtDateTime(r.started_at);
		renderLive();
		document.title = `${S.newRuns.length ? "● " : ""}${r.name} · TraceReports`;
		$("#export-li").hidden = !!STATIC;
		$("#export-btn").href = `/api/v1/runs/${r.id}/export`;
		if (STATIC) {
			const pill = $("#static-pill");
			pill.textContent = `Exportado ${fmtDateTime(STATIC.exported_at)}`;
			pill.hidden = false;
		}
	}

	// ---------- view switching ----------
	function setView(view) {
		S.view = view;
		$$(".side-nav a").forEach((a) => a.classList.toggle("active", a.dataset.view === view));
		renderView();
		writeHash();
		if (view === "search" && !STATIC && !S.runSearch.items.length && !S.runSearch.loading) loadRunSearch();
	}

	// vistas que no dependen de la ejecución elegida
	const GLOBAL_VIEWS = new Set(["search", "metrics", "settings"]);

	function renderView() {
		const global = GLOBAL_VIEWS.has(S.view);
		if (!S.run && !global) return;
		$$(".view").forEach((v) => (v.hidden = v.id !== `view-${S.view}`));
		$("#empty-state").hidden = global || S.runs.length > 0;
		document.body.classList.toggle("view-global", global);
		if (global) $("#run-banner").hidden = true; else renderRunBanner();
		document.body.classList.toggle("view-dashboard", S.view === "dashboard");
		$$(".side-nav a").forEach((a) => a.classList.toggle("active", a.dataset.view === S.view));
		({ search: renderRunSearch, tests: renderTests, categories: renderCategories, exceptions: renderExceptions, dashboard: renderDashboard,
			ai: renderAI, escalate: renderEscalate, metrics: renderMetrics, settings: renderSettings, release: renderRelease }[S.view] || renderTests)();
	}

	// ---------- run explorer ----------
	function searchParams(cursor = "") {
		const x = S.runSearch, p = new URLSearchParams();
		["q", "project", "environment", "branch", "tag", "owner", "status", "from", "to", "sort"].forEach((k) => { if (x[k]) p.set(k, x[k]); });
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
			el.innerHTML = `<option value="">${initial}</option>${(x.facets[key] || []).map((f) => `<option value="${esc(f.value)}">${esc(f.value)} (${f.count})</option>`).join("")}`;
			el.value = x[key] || "";
		});
		["status", "from", "to", "owner"].forEach((key) => { const el = $(`[data-search-filter="${key}"]`); if (el) el.value = x[key] || ""; });
		const sort = $("#search-sort"); if (sort) sort.value = x.sort;
		renderSavedRunSearches();
	}
	function savedRunSearches() { try { return JSON.parse(localStorage.getItem("tracereports-run-searches") || "[]"); } catch { return []; } }
	function renderSavedRunSearches() { const el = $("#search-saved"); if (!el) return; const items = savedRunSearches(); el.innerHTML = `<option value="">${tr("Búsquedas guardadas")}</option>${items.map((item, i) => `<option value="${i}">${esc(item.name)}</option>`).join("")}`; }
	function saveRunSearch() { const name = window.prompt(tr("Nombre de la búsqueda")); if (!name?.trim()) return; const x = S.runSearch, query = {}; ["q", "project", "environment", "branch", "tag", "owner", "status", "from", "to", "sort", "incomplete", "flaky"].forEach((key) => { query[key] = x[key]; }); try { const items = savedRunSearches().filter((item) => item.name !== name.trim()).slice(-9); items.push({ name: name.trim(), query }); localStorage.setItem("tracereports-run-searches", JSON.stringify(items)); renderSavedRunSearches(); } catch { /* optional convenience only */ } }
	function runSearchChips() {
		const x = S.runSearch, names = { q: "Texto", project: "Proyecto", environment: "Ambiente", branch: "Rama", tag: "Tag", owner: "Dueño", status: "Estado", from: "Desde", to: "Hasta", incomplete: "Incompletas", flaky: "Flaky" };
		return Object.entries(names).filter(([key]) => x[key]).map(([key, name]) => `<button class="search-chip" data-search-remove="${key}">${tr(name)}: ${key === "incomplete" || key === "flaky" ? "✓" : esc(x[key])}<b aria-hidden="true">×</b><span class="sr-only">${tr("Quitar")}</span></button>`).join("");
	}
	function renderRunSearch() {
		const x = S.runSearch, root = $("#view-search"); if (!root) return;
		if (STATIC) { root.innerHTML = `<div class="card placeholder">${tr("La búsqueda de historial no está disponible en un reporte exportado.")}</div>`; return; }
		const input = $("#run-search-input"); if (input && input.value !== x.q) input.value = x.q;
		renderSearchFilters();
		$("#search-chips").innerHTML = runSearchChips();
		$("#search-result-count").textContent = x.loading ? tr("Buscando…") : x.error ? tr("No se pudo buscar") : tr("{n} resultados", { n: x.total });
		const list = $("#run-search-results");
		if (x.error) list.innerHTML = `<div class="placeholder">${tr("No se pudieron cargar las ejecuciones.")} <button data-search-retry>${tr("Reintentar")}</button></div>`;
		else if (x.loading && !x.items.length) list.innerHTML = `<div class="placeholder"><div class="shimmer"></div><div class="shimmer"></div><div class="shimmer"></div></div>`;
		else if (!x.items.length) list.innerHTML = `<div class="placeholder">${tr("Sin resultados: quita un filtro o prueba otra búsqueda.")}</div>`;
		else list.innerHTML = x.items.map((r) => `<button class="search-row" data-search-run="${r.id}" role="option" aria-selected="${x.selected === r.id}">
			<span class="search-state ${lower(r.status)}" style="--c:var(--${lower(r.status)})"></span><span class="search-main"><b>${esc(r.name)}</b><span>${[r.branch, r.environment, r.project].filter(Boolean).map(esc).join(" · ")}</span></span>
			<span class="search-meta" title="${esc(fmtDateTime(r.started_at))}">${esc(relativeRun(r.started_at))}</span><span class="search-count">${r.passed}/${r.total} OK${r.failed ? ` · <b class="fail">${r.failed} ${tr("fallos")}</b>` : ""}${r.flaky_count ? ` · <b class="flaky">${r.flaky_count} flaky</b>` : ""}</span><label title="${tr("Marcar para comparar")}" onclick="event.stopPropagation()"><input type="checkbox" data-search-compare="${r.id}" ${x.compare.includes(r.id) ? "checked" : ""}></label></button>`).join("");
		$("#search-more").hidden = !x.cursor || x.loading;
		if (!x.selected) renderSearchPreview();
	}
	function renderSearchPreview(detail) {
		const x = S.runSearch, el = $("#run-search-preview"); if (!el) return;
		const r = detail || x.items.find((item) => item.id === x.selected);
		if (!r) { el.innerHTML = `<p>${tr("Selecciona una ejecución para ver el resumen.")}</p>`; return; }
		if (!detail) { el.innerHTML = `<div class="placeholder">${tr("Cargando vista previa…")}</div>`; return; }
		const failed = detail.tests?.filter((t) => t.status === "FAIL").slice(0, 4) || [];
		el.innerHTML = `<h3>${esc(detail.name)}</h3><p>${statusLabel(detail.status)} · ${esc(fmtDateTime(detail.started_at))}</p><dl><dt>${tr("Tests")}</dt><dd>${detail.passed}/${detail.total} OK · ${detail.failed} ${tr("fallos")}</dd><dt>${tr("Duración")}</dt><dd>${esc(fmtDuration(durationOf(detail)))}</dd><dt>${tr("Release")}</dt><dd id="search-release">${tr("Cargando…")}</dd></dl>${failed.length ? `<p><b>${tr("Tests fallidos")}</b><br>${failed.map((t) => esc(t.name)).join("<br>")}</p>` : ""}<div class="search-preview-actions"><button data-search-open="${detail.id}">${tr("Ver reporte")}</button><button data-search-compare-open="${detail.id}" ${x.compare.length !== 1 || x.compare[0] === detail.id ? "disabled" : ""}>${tr("Comparar")}</button></div>`;
	}
	async function selectSearchRun(id) {
		const x = S.runSearch; x.selected = id; renderRunSearch(); const current = turn("search-preview");
		try { const [detail, release] = await Promise.all([api(`/api/v1/runs/${id}`), api(`/api/v1/runs/${id}/release`).catch(() => null)]); if (!current() || x.selected !== id) return; renderSearchPreview(detail); const cell = $("#search-release"); if (cell) cell.textContent = release?.decision || release?.status || tr("Sin decisión"); } catch { if (current()) $("#run-search-preview").innerHTML = `<p>${tr("No se pudo cargar la vista previa.")}</p>`; }
	}
	async function compareSearchRuns(first, second) {
		await switchRun(second); setView("dashboard"); await chooseBase(first);
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
			: S.detailTab === "replay" && shots > 0 ? "replay" : "steps";
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
		</div>` : "";

		// La captura va dentro de "Detalle", debajo del texto del step.
		const counts = {};
		for (const l of t.logs) counts[l.status] = (counts[l.status] || 0) + 1;
		const chips = STEP_FILTERS.filter(([st]) => counts[st]).map(([st, cls, ic, name]) =>
			`<button class="step-chip ${cls} ${S.stepFilter === st ? "active" : ""}" data-step-filter="${st}" data-tip="Mostrar solo los pasos ${esc(name)} (${counts[st]})">${icon(ic)}${name} ${counts[st]}</button>`).join("");
		const logs = S.stepFilter ? t.logs.filter((l) => l.status === S.stepFilter) : t.logs;
		const rows = logs.map((l) => stepRow(l)).join("");
		const steps = `${t.logs.length > 1 ? `<div class="steps-toolbar"><span class="lbl">Filtrar:</span>
				<button class="step-chip all ${!S.stepFilter ? "active" : ""}" data-step-filter="" data-tip="Mostrar todos los pasos">Todos ${t.logs.length}</button>${chips}</div>` : ""}
			<table class="table-results">
				<thead><tr><th>Estado</th><th>Hora</th><th>Detalle</th></tr></thead>
				<tbody>${rows || `<tr><td colspan="3" class="no-steps">${S.stepFilter ? "Ningún step con ese estado." : "Sin pasos registrados."}</td></tr>`}</tbody>
			</table>`;

		const tabs = `<div class="detail-tabs">
			<button data-tab="steps" class="${tab === "steps" ? "active" : ""}" data-tip-title="Steps" data-tip="Cada acción y validación del test en orden, con sus capturas debajo. Filtra por estado con los chips.">${icon("i-tests")}Steps <span class="tab-count">${t.logs.length}</span></button>
			<button data-tab="timeline" class="${tab === "timeline" ? "active" : ""}" data-tip-title="Timeline" data-tip="Pasos, capturas y llamadas al backend en una sola línea de tiempo, con lo que duró cada llamada: muestra qué esperaba el test cuando se trabó.">${icon("i-timeline")}Timeline</button>
			${shots ? `<button data-tab="replay" class="${tab === "replay" ? "active" : ""}" data-tip-title="Replay" data-tip="Reproduce el test como un video, paso a paso, con la captura de cada momento. Útil para ver cómo llegó la pantalla al fallo." data-tip-keys="Espacio: play/pausa · ← →: pasos">${icon("i-play")}Replay <span class="tab-count">${shots}</span></button>` : ""}
			${t.network_total > 0 ? `<button data-tab="network" class="${tab === "network" ? "active" : ""}" data-tip-title="Red" data-tip="Las llamadas al backend que hizo la página durante el test: status, tiempos, headers y body. Desde una llamada con error puedes generar un mock.">${icon("i-network")}Red <span class="tab-count">${t.network_total}</span>
				${t.network_errors ? `<span class="tab-err">${t.network_errors} con error</span>` : `<span class="tab-ok">OK</span>`}</button>` : ""}
			${consoleList.length ? `<button data-tab="console" class="${tab === "console" ? "active" : ""}" data-tip-title="${tr("Consola")}" data-tip="${tr("Errores y advertencias de la consola del navegador durante el test, y los errores de JavaScript que la página no manejó.")}">${icon("i-warning")}${tr("Consola")} <span class="tab-count">${consoleList.length}</span>
				${t.console_errors ? `<span class="tab-err">${tr(t.console_errors === 1 ? "1 error" : "{n} errores", { n: t.console_errors })}</span>` : ""}</button>` : ""}
		</div>`;
		const panel = tab === "network" ? `<div class="net-panel" id="net-panel"></div>`
			: tab === "timeline" ? `<div class="timeline" id="timeline-panel"></div>`
			: tab === "replay" ? `<div class="replay-panel" id="replay-panel"></div>`
			: tab === "console" ? consolePanel(consoleList) : steps;
		const body = `<div class="card content-card">${tabs}${panel}</div>`;

		if (setHTML(el, head + aiCard(t) + error + artifactsBlock(t) + body)) { S.rendered["net-list"] = null; S.rendered["timeline-panel"] = null; }
		if (tab === "network") ensureNetworkPanel(t);
		if (tab === "timeline") ensureTimeline(t);
		if (tab === "replay") mountReplay(t); else if (S.player) { S.player.destroy(); S.player = null; }
		const bar = $("#test-detail .detail-tabs"), act = bar && $("button.active", bar);
		if (act) { // pestaña activa visible en pantallas angostas
			const r = act.getBoundingClientRect(), br = bar.getBoundingClientRect();
			if (r.left < br.left || r.right > br.right) bar.scrollLeft += r.left - br.left - 8;
		}
		ensureHistory(t);
		ensureLocator(t);
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
				<p style="margin-top:8px">Diagnóstico con IA desactivado. Define <code>GEMINI_API_KEY</code> en el servidor para clasificar fallos automáticamente.</p>${loc}</div>`;
		}
		if (tr.state === "PENDING" && STATIC) {
			return `<div class="card ai-card muted"><div class="ai-head">${title}</div>
				<p style="margin-top:8px">El análisis de IA aún no había terminado cuando se exportó este reporte.</p></div>`;
		}
		if (tr.state === "PENDING") {
			return `<div class="card ai-card"><div class="ai-head">${title}<span class="ai-model">Analizando con ${esc(S.config.ai_model)}…</span></div>
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
				<span class="ai-model">${esc(S.config.ai_model)}</span></div>
			<h6>Resumen</h6><p>${esc(tr.summary)}</p>
			<h6>Sugerencia</h6><p class="suggestion">${esc(tr.suggestion)}</p>
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

	function fmtBytes(n) {
		if (n < 1024) return `${n} B`;
		if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`;
		return `${(n / 1048576).toFixed(1)} MB`;
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
				${isNetError(c) && c.status ? `<button class="cf-btn cf-btn-sm cf-btn-mock" data-mock data-tip="Genera un stub de esta respuesta (Playwright, Cypress o WireMock) para reproducir el error sin depender del backend">${icon("i-bolt")}Mock</button>` : ""}</summary>
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
			URL: c.url, Método: c.method,
			Estado: c.failed ? `sin respuesta (${c.error_text})` : `${c.status || "—"} ${c.status_text || ""}`,
			Tipo: [c.resource_type, c.mime_type].filter(Boolean).join(" · ") || "—",
			Inicio: c.started_at ? fmtTime(c.started_at) : "—",
			Duración: c.duration_ms != null ? `${c.duration_ms} ms` : "—",
			...(c.evidence_file ? { "Evidencia local": c.evidence_file } : {}),
		};
		const loc = bodyLocation(c);
		const compare = isNetError(c) && !STATIC
			? `<button class="cf-btn cf-btn-sm" data-baseline data-tip="${tr("Busca esta misma llamada en la última ejecución donde el test pasó y muestra qué cambió: status, headers y body.")}">${icon("i-timeline")}${tr("Comparar con la última vez que pasó")}</button>` : "";
		return `<div class="net-actions">${compare}<button class="cf-btn cf-btn-sm cf-btn-mock" data-mock>${icon("i-bolt")}Generar mock / stub</button><button class="net-curl" data-curl="1">Copiar como cURL</button></div>`
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
			<div class="card"><h5>${tr("Funcionalidades")}</h5><div class="rel-features">${features}</div></div>`);
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

	/** Comandos para correr este test en local (según el framework y la identidad del test). */
	function reproBlock(t) {
		const cmds = CF.reproCommands({ framework: S.run?.framework, key: t.key, name: t.name, commit: S.run?.commit });
		if (!cmds.length) return "";
		return `<details class="repro"><summary>${tr("Reproducir en local")}</summary>
			${cmds.map((c) => `<div class="repro-row"><span class="repro-lbl">${esc(c.label)}</span><code data-no-i18n>${esc(c.cmd)}</code>
				<button class="copy-btn" data-copy="${esc(c.cmd)}">${tr("Copiar")}</button></div>`).join("")}
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
		load.then((rep) => { S.loc[key] = rep; if (S.test?.id === t.id) renderTestDetail(); })
			.catch(() => { S.loc[key] = false; });
	}

	function orderedSuggestions(rep) {
		const list = rep.suggestions.map((s, i) => ({ ...s, i, pick: rep.ai_pick && (rep.ai_pick === s.python || rep.ai_pick === s.js) }));
		return list.sort((a, b) => (b.pick ? 1 : 0) - (a.pick ? 1 : 0));
	}

	function locatorCard(t) {
		const rep = S.loc[locKey(t)];
		if (!rep) return "";
		const sugg = orderedSuggestions(rep);
		const snippet = (s) => (S.locLang === "js" ? s.js : s.python);
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
				<span class="loc-title">Selector roto${sugg.length ? ` · ${sugg.length} reemplazo${sugg.length === 1 ? "" : "s"} sugerido${sugg.length === 1 ? "" : "s"}` : ""}</span>
				<svg class="chev" aria-hidden="true"><use href="#i-chevron"/></svg></summary>
			<div class="loc-body">
				<dl class="loc-diff"><dt>Fallido</dt><dd><code class="loc-failed">${esc(rep.failed_selector || "(no se pudo extraer del error)")}</code></dd>
					${rep.page_url ? `<dt>Página</dt><dd class="mono" style="font-size:12px;word-break:break-all">${esc(rep.page_url)}</dd>` : ""}</dl>
				${sugg.length ? `<div class="loc-toolbar"><span>Sugeridos, del más robusto al menos robusto</span>
					<div class="loc-lang" role="group" aria-label="Lenguaje del snippet">
						<button data-loc-lang="python" aria-pressed="${S.locLang === "python"}">Python</button><button data-loc-lang="js" aria-pressed="${S.locLang === "js"}">JS / TS</button></div></div>
					<ol class="loc-list">${items}</ol>`
					: `<p class="loc-empty">No hay snapshot de la página para proponer reemplazos. Los clientes lo envían al fallar (<code>capturar_dom</code> en Python, <code>Test.DOM</code> en Go).</p>`}
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
			subtitle: `${s.element} · ${S.locLang === "js" ? s.js : s.python}`,
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

	function setLiveState(state) {
		S.liveState = state;
		renderLive();
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
		if (sameRun && ["run", "test", "triage", "summary", "log", "network", "console", "artifact"].includes(type)) {
			invalidateEscalation();
			if (S.view === "escalate") renderEscalate();
		}
		if (e.run_id) S.activity[e.run_id] = Date.now();
		if (type === "run" && e.data?.action === "created") loadRuns().then(() => notifyNewRun(e.run_id)).catch(() => {});
		if (type === "log") {
			if (e.test_id === S.testId) appendLiveStep(e.data);
			return;
		}
		if (sameRun) scheduleRun();
		if (e.test_id && e.test_id === S.testId) scheduleTest();
	}

	/** Cambia a otra ejecución (selector de la barra o aviso de ejecución nueva). */
	async function switchRun(id) {
		S.runId = id;
		$("#run-select").value = String(id);
		S.testId = null; S.test = null; S.rendered = {};
		S.newRuns = S.newRuns.filter((x) => x !== id); // ya la está viendo
		renderNewRuns();
		writeHash();
		await loadRun(); await loadTest();
	}

	/**
	 * Empezó otra ejecución: no se cambia sola (quien mira un reporte no pierde lo que estaba
	 * viendo). Un aviso arriba ofrece verla en vivo; con "Ahora no" queda la insignia junto al
	 * selector y un punto en la pestaña del navegador. Si no se estaba viendo ninguna, se abre.
	 */
	function notifyNewRun(id) {
		const r = S.runs.find((x) => x.id === id);
		if (!r || id === S.runId || S.newRuns.includes(id)) return;
		if (!S.run) { switchRun(id).catch(() => {}); return; }
		S.newRuns.push(id);
		S.toastClosed = false; // cada ejecución nueva vuelve a avisar
		renderNewRuns();
	}

	function renderNewRuns() {
		const runs = S.newRuns.map((id) => S.runs.find((x) => x.id === id)).filter(Boolean);
		const latest = runs[runs.length - 1];
		const badge = $("#new-run-badge");
		badge.hidden = !latest;
		document.title = document.title.replace(/^● /, "");
		let el = $("#run-toast");
		if (!latest) { if (el) el.hidden = true; return; }
		document.title = `● ${document.title}`;
		badge.dataset.run = String(latest.id);
		$(".nrb-text", badge).textContent = tr(runs.length === 1 ? "1 nueva" : "{n} nuevas", { n: runs.length });
		badge.dataset.tip = tr("Ejecuciones que empezaron mientras mirabas esta. Clic para ver la última en vivo.");
		if (!el) {
			el = document.createElement("div");
			el.id = "run-toast";
			el.className = "run-toast";
			el.setAttribute("role", "status");
			el.setAttribute("aria-live", "polite");
			el.addEventListener("click", (ev) => {
				if (ev.target.closest("[data-toast-open]")) { el.hidden = true; switchRun(Number(el.dataset.run)).catch(() => {}); }
				else if (ev.target.closest("[data-toast-close]")) { S.toastClosed = true; el.hidden = true; }
			});
			document.body.appendChild(el);
		}
		el.dataset.run = String(latest.id);
		const where = [latest.environment, latest.branch].filter(Boolean).map(esc).join(" · ");
		el.innerHTML = `<span class="run-toast-tag"><span class="dot" aria-hidden="true"></span>${tr("En vivo")}</span>
			<div class="run-toast-body">
				<b class="run-toast-title">${runs.length === 1 ? tr("Empezó otra ejecución") : tr("{n} ejecuciones nuevas en curso", { n: runs.length })}</b>
				<span class="run-toast-sub" data-no-i18n>#${latest.id} · ${esc(latest.name)}${where ? ` · ${where}` : ""}</span>
			</div>
			<button class="run-toast-open" data-toast-open>${tr("Ver en vivo")} →</button>
			<button class="run-toast-later" data-toast-close>${tr("Ahora no")}</button>`;
		el.hidden = S.toastClosed;
		if (!el.hidden) { el.classList.remove("run-toast-in"); void el.offsetWidth; el.classList.add("run-toast-in"); }
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
	function ensureHistory(t) {
		const key = `${t.id}:${t.status}`;
		if (S.hist[key] !== undefined) return;
		S.hist[key] = null; // cargando
		api(`/api/v1/tests/${t.id}/history`)
			.then((h) => { S.hist[key] = h; if (S.test?.id === t.id) renderTestDetail(); })
			.catch(() => { S.hist[key] = []; });
	}

	function historyStrip(t) {
		const hist = S.hist[`${t.id}:${t.status}`];
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
			<span class="history-lbl" tabindex="0" data-tip="Resultado de este mismo test en sus últimas ejecuciones del mismo proyecto, ambiente y rama, de la más antigua a la actual. Clic en un cuadro para abrir esa ejecución.">Historial</span><span class="hist-cells">${cells}</span>
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
	const INCIDENT_ICON = { backend: "i-plug", ai: "i-spark", error: "i-warning" };

	/** Hechos observados de un incidente (llamada, error, captura), con enlace al test. */
	function incidentEvidence(inc) {
		// el error de un incidente "error" ya es su título: no se repite
		const ev = (inc.evidence || []).filter((e) => e.kind !== "screenshot" && !(e.kind === "error" && e.text === inc.title)).slice(0, 3);
		if (!ev.length && !inc.location) return "";
		const tr = window.TraceReportsI18n.t;
		return `<ul class="inc-evidence" aria-label="${esc(tr("Evidencia observada"))}">${ev.map((e) =>
			`<li><a data-goto="${e.test_id}" data-no-i18n>${esc(e.text)}</a></li>`).join("")}${inc.location ? `<li class="mono" data-no-i18n>${esc(inc.location)}</li>` : ""}</ul>`;
	}

	/** Causa: hipótesis de la IA (nunca confirmada), o la IA dijo que la evidencia no alcanza. */
	function incidentCause(inc, cls) {
		const tr = window.TraceReportsI18n.t;
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
				<div class="inc-title"><b data-no-i18n>${esc(inc.title)}</b><span class="inc-count">${inc.test_ids.length} test${inc.test_ids.length === 1 ? "" : "s"}</span></div>
				${incidentCause(inc, "inc-cause")}
				${inc.action ? `<div class="inc-action">→ ${esc(inc.action)}</div>` : ""}
				${compact ? "" : incidentEvidence(inc)}
				${compact ? "" : `<div class="inc-tests">${inc.test_ids.map((id, i) => `<a data-goto="${id}">${esc(inc.test_names[i])}</a>`).join("")}</div>`}
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
		if (sum.state === "PENDING") {
			setHTML(el, `<div class="sum-head">${icon("i-spark")}<span>Analizando la ejecución…</span></div><div class="shimmer"></div><div class="shimmer short"></div>`);
			return;
		}
		if (sum.state === "ERROR") {
			setHTML(el, `<div class="sum-head">${icon("i-spark")}<span>No se pudo generar el diagnóstico: ${esc(sum.error)}</span></div>`);
			return;
		}
		setHTML(el, `<div class="sum-head ${S.run.failed ? "has-fail" : "all-ok"}">${icon(S.run.failed ? "i-spark" : "i-pass")}
				<h3>${esc(sum.headline)}</h3>${sum.ai ? `<span class="ai-model">${esc(S.config.ai_model)}</span>` : ""}</div>
			${summaryBody(sum, false)}`);
	}

	function renderRunBanner() {
		const el = $("#run-banner");
		const sum = S.run?.summary;
		const show = S.view === "tests" && sum?.state === "DONE" && S.run.failed > 0;
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
				Object.assign(S.insights, { compare, endpoints, baselines });
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

	function renderInsights() {
		const { compare, endpoints } = S.insights;
		const list = (items, cls, label) => items?.length ? `<div class="cmp-group ${cls}"><h6>${label} <span>${items.length}</span></h6>
			${items.map((i) => `<a data-goto="${i.test_id}">${esc(i.name)}${i.base_duration_ms && cls === "slower"
				? ` <small>${fmtDuration(i.base_duration_ms)} → ${fmtDuration(i.duration_ms)}</small>` : ""}</a>`).join("")}</div>` : "";
		let cmp = `<h5>Comparación</h5><div class="placeholder">Cargando…</div>`;
		const bases = S.insights.baselines || [];
		const picker = bases.length ? `<label class="cmp-base"><span>Comparar con</span>
			<select id="cmp-base"><option value="0" ${!S.insights.base ? "selected" : ""}>Automática</option>
				${bases.map((b) => `<option value="${b.id}" ${S.insights.base === b.id ? "selected" : ""}>#${b.id} · ${esc(b.name)}${b.branch ? ` · ${esc(b.branch)}` : ""} — ${fmtDateTime(b.started_at)}</option>`).join("")}</select></label>` : "";
		if (compare) {
			cmp = !compare.base_run
				? `<h5>Comparación</h5>${picker}<div class="placeholder">No hay una ejecución anterior del mismo proyecto y ambiente con estos tests: no hay con qué comparar.</div>`
				: `<h5>Comparación con <a href="#run=${compare.base_run.id}&view=dashboard">#${compare.base_run.id}</a> <small>${fmtDateTime(compare.base_run.started_at)}${compare.base_run.branch ? ` · ${esc(compare.base_run.branch)}` : ""}</small></h5>
					${picker}${compare.base_reason ? `<p class="m-hint cmp-reason">${esc(BASE_REASON[compare.base_reason] || "")}</p>` : ""}
					<div class="cmp-chips">
						<span class="cmp-chip new_failures">${plural(compare.new_failures.length, "fallo nuevo", "fallos nuevos")}</span>
						<span class="cmp-chip fixed">${plural(compare.fixed.length, "arreglado", "arreglados")}</span>
						<span class="cmp-chip still">${plural(compare.still_failing.length, "sigue fallando", "siguen fallando")}</span>
						<span class="cmp-chip slower">${plural(compare.slower.length, "más lento", "más lentos")}</span>
					</div>
					${!compare.new_failures.length && !compare.fixed.length && !compare.still_failing.length && !compare.slower.length
						? `<div class="cmp-same">${icon("i-check")}Sin cambios: los mismos resultados que la ejecución #${compare.base_run.id}.</div>` : ""}
					${list(compare.new_failures, "new_failures", "Fallos nuevos")}${list(compare.fixed, "fixed", "Arreglados")}
					${list(compare.still_failing, "still", "Siguen fallando")}${list(compare.slower, "slower", "Más lentos")}
					${list(compare.new_tests, "new_tests", "Tests nuevos")}`;
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

		const C = ["--pass", "--fail", "--warning", "--skip", "--running", "--info"].map(cssVar);
		donut("test-analysis", ["Pass", "Fail", "Warning", "Skip", window.TraceReportsI18n.t("En curso")], [r.passed, r.failed, r.warning, r.skipped, r.running], C.slice(0, 5));
		donut("step-analysis", ["Pass", "Fail", "Warning", "Skip", "Info"], ["PASS", "FAIL", "WARNING", "SKIP", "INFO"].map((k) => ss[k] || 0), [C[0], C[1], C[2], C[3], C[5]]);

		renderRunSummary();
		ensureInsights();

		$("#env-table").innerHTML = [
			["Ambiente", r.environment || "—"],
			...(r.project ? [["Proyecto", r.project]] : []),
			...(r.branch ? [["Rama", r.branch]] : []),
			...(r.commit ? [["Commit", r.commit.slice(0, 12)]] : []),
			["Estado", r.incomplete ? `${r.status} · incompleta (tests sin terminar o ejecución interrumpida)` : r.status],
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
		{ id: "midnight", name: "Midnight", desc: "Oscuro profundo, sidebar flotante y acento violeta.",
			sw: { bg: "#08080d", top: "#101017", nav: "#1d1b2e", card: "#16161f", accent: "#a78bfa" } },
		{ id: "paper", name: "Paper", desc: "Editorial: serifa, papel cálido y navegación superior.", topnav: true,
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
		paletteItems = views.concat(paletteItems.filter((x) => x.type === "run"));
		if (paletteIndex >= paletteItems.length) paletteIndex = 0;
		el.innerHTML = paletteItems.length ? paletteItems.map((item, i) => `<button class="command-option ${i === paletteIndex ? "active" : ""}" role="option" aria-selected="${i === paletteIndex}" data-command-index="${i}">${item.type === "run" ? `${icon(STATUS_ICON[item.run.status] || "i-list")}<span>${esc(item.run.name)}<br><small>#${item.run.id} · ${esc(item.run.branch || item.run.environment || "")}</small></span>` : `${icon("i-list")}<span>${tr("Ir a")} ${tr(item.name)}</span>`}</button>`).join("") : `<p class="placeholder">${tr("Sin coincidencias")}</p>`;
	}
	function openPalette() { if (STATIC) return; const box = $("#command-palette"); paletteFocus = document.activeElement; box.hidden = false; $("#command-input").value = ""; paletteItems = []; paletteIndex = 0; renderPalette(); $("#command-input").focus(); }
	function closePalette() { const box = $("#command-palette"); if (box.hidden) return; box.hidden = true; paletteFocus?.focus?.(); }
	async function searchPalette() {
		const q = $("#command-input").value.trim(), current = turn("command-search");
		paletteItems = []; renderPalette(); if (!q) return;
		try { const data = await api(`/api/v1/runs/search?q=${encodeURIComponent(q)}&limit=8`); if (!current()) return; paletteItems = data.items.map((run) => ({ type: "run", run })); paletteIndex = 0; renderPalette(); } catch { /* navigation remains available */ }
	}
	function activatePalette(item) { if (!item) return; closePalette(); if (item.type === "run") switchRun(item.run.id).catch(() => {}); else setView(item.view); }

	// ---------- events ----------
	function bindEvents() {
		bindMetricsEvents();
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
		$("#recents-search").addEventListener("click", () => setView("search"));
		$("#run-search-input").addEventListener("input", (e) => { S.runSearch.q = e.target.value; scheduleRunSearch(); });
		$("#search-filters").addEventListener("change", (e) => { const key = e.target.dataset.searchFilter; if (!key) return; S.runSearch[key] = e.target.value; scheduleRunSearch(); });
		$("#search-filters").addEventListener("input", (e) => { const key = e.target.dataset.searchFilter; if (key === "owner") { S.runSearch.owner = e.target.value; scheduleRunSearch(); } });
		$("#search-sort").addEventListener("change", (e) => { S.runSearch.sort = e.target.value; scheduleRunSearch(); });
		$("#search-shortcuts").addEventListener("click", (e) => {
			const quick = e.target.closest("[data-search-quick]");
			if (quick) { const x = S.runSearch; Object.assign(x, { status: "", flaky: false, incomplete: false }); if (quick.dataset.searchQuick === "fail") x.status = "FAIL"; if (quick.dataset.searchQuick === "flaky") x.flaky = true; if (quick.dataset.searchQuick === "running") x.status = "RUNNING"; if (quick.dataset.searchQuick === "incomplete") x.incomplete = true; scheduleRunSearch(); return; }
			if (e.target.closest("[data-search-owner]")) { const owner = window.prompt(tr("Nombre del dueño")); if (owner != null) { S.runSearch.owner = owner.trim(); scheduleRunSearch(); } return; }
			const range = e.target.closest("[data-search-range]"); if (range) { const days = Number(range.dataset.searchRange); const to = new Date(), from = new Date(to); from.setDate(from.getDate() - Math.max(0, days - 1)); S.runSearch.from = from.toLocaleDateString("sv"); S.runSearch.to = to.toLocaleDateString("sv"); scheduleRunSearch(); return; }
			if (e.target.closest("[data-search-save]")) { saveRunSearch(); return; }
			if (e.target.closest("[data-search-clear]")) { Object.assign(S.runSearch, { q: "", project: "", environment: "", branch: "", tag: "", owner: "", status: "", from: "", to: "", incomplete: false, flaky: false, sort: "recent" }); scheduleRunSearch(); }
		});
		$("#search-saved").addEventListener("change", (e) => { const item = savedRunSearches()[Number(e.target.value)]; if (!item) return; Object.assign(S.runSearch, item.query, { items: [], cursor: "", selected: 0 }); scheduleRunSearch(); e.target.value = ""; });
		$("#search-chips").addEventListener("click", (e) => { const b = e.target.closest("[data-search-remove]"); if (!b) return; const key = b.dataset.searchRemove; S.runSearch[key] = key === "incomplete" || key === "flaky" ? false : ""; scheduleRunSearch(); });
		$("#run-search-results").addEventListener("click", (e) => {
			const check = e.target.closest("[data-search-compare]");
			if (check) { const id = Number(check.dataset.searchCompare), x = S.runSearch; x.compare = check.checked ? [...x.compare.filter((n) => n !== id), id].slice(-2) : x.compare.filter((n) => n !== id); renderRunSearch(); if (x.compare.length === 2) compareSearchRuns(x.compare[0], x.compare[1]).catch(() => {}); return; }
			const row = e.target.closest("[data-search-run]"); if (row) selectSearchRun(Number(row.dataset.searchRun));
		});
		$("#run-search-preview").addEventListener("click", (e) => { const open = e.target.closest("[data-search-open]"); if (open) switchRun(Number(open.dataset.searchOpen)).catch(() => {}); const cmp = e.target.closest("[data-search-compare-open]"); if (cmp) compareSearchRuns(S.runSearch.compare[0], Number(cmp.dataset.searchCompareOpen)).catch(() => {}); });
		$("#search-more").addEventListener("click", () => loadRunSearch(false));
		$("#run-search-results").addEventListener("click", (e) => { if (e.target.closest("[data-search-retry]")) loadRunSearch(); });
		$("#new-run-badge").addEventListener("click", (e) => switchRun(Number(e.currentTarget.dataset.run)));
		$("#compare-card").addEventListener("change", (e) => {
			if (e.target.id === "cmp-base") chooseBase(Number(e.target.value));
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
			const lang = e.target.closest("[data-loc-lang]");
			if (lang) { S.locLang = lang.dataset.locLang; renderTestDetail(); return; }
			const copyBtn = e.target.closest("[data-loc-copy]");
			if (copyBtn) {
				const s = currentLocator()?.suggestions[Number(copyBtn.dataset.locCopy)];
				if (s) CF.copyText(S.locLang === "js" ? s.js : s.python, copyBtn);
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
			if (S.view === "tests") await loadTest();
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
					${!sum.ai ? `<p class="m-hint">${tr("Agrupado sin IA: cada incidente junta los fallos con la misma llamada de backend fallida justo antes del fallo, o con la misma firma de error.")}</p>` : ""}
					${summaryCoverage(sum)}`;
			const incidents = (sum?.incidents || []).map((inc) => `<article class="card incident-card">
				<header><span class="inc-kind ${esc(inc.kind)}">${esc({ backend: tr("Backend"), ai: tr("Causa IA"), error: tr("Error") }[inc.kind] || inc.kind)}</span>
					<h6 data-no-i18n>${esc(AI_LABEL[inc.title] ? tr(AI_LABEL[inc.title]) : inc.title)}</h6>
					<span class="inc-count">${inc.test_ids.length === 1 ? tr("1 test") : tr("{n} tests", { n: inc.test_ids.length })}</span>${recurrencePill(inc.key)}</header>
				${incidentEvidence(inc)}
				${incidentCause(inc, "inc-cause-p")}
				${inc.action ? `<p><b>${tr("Qué hacer")}:</b> ${esc(inc.action)}</p>` : ""}
				<ul class="inc-tests">${inc.test_ids.map((id, i) => `<li><a href="#run=${r.id}&view=tests&test=${id}" data-no-i18n>${esc(inc.test_names[i] || "#" + id)}</a></li>`).join("")}</ul>
				${canAct() ? `<div class="inc-actions"><button class="cf-btn cf-btn-sm" data-escalate="${inc.test_ids.length === 1 ? inc.test_ids[0] : 0}">${icon("i-megaphone")}${tr("Escalar")}</button></div>` : ""}
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
			more: "… {n} líneas más en el reporte", clipped: "Respuesta recortada: completa en el reporte" },
		en: { critical: "Critical severity", high: "High severity", medium: "Medium severity", low: "Low severity",
			business: "Business", qa: "QA", dev: "Development", what: "What happened", impact: "Impact", cause: "Likely cause",
			evidence: "Evidence", next: "Next steps", owner: "Suggested owner", calls: "Backend calls that failed",
			shot: "Screenshot at the moment of failure", run: "Run", env: "Environment", result: "Result", failed: "{f} of {t} tests failed",
			report: "View full report", by_ai: "Summary written with AI ({m})", by_tpl: "Summary built from the report's evidence",
			flaky: "Flaky test: it failed {x} of its recent runs",
			tech: "Technical detail", branch: "Branch", test: "Test", attempts: "Attempts", stack: "Error and stack trace", repro: "Run it locally",
			response: "Response", console: "Browser console", artifacts: "Trace and video", logs: "View logs", trace: "View trace", copy: "Copy",
			baseline: "The last time the test passed (run #{r}) it answered HTTP {s}", other_ctx: " · on another branch or environment",
			more: "… {n} more lines in the report", clipped: "Response cut: the full one is in the report" },
	};
	const escFmt = (s, v) => s.replace(/\{(\w+)\}/g, (m, k) => (k in v ? v[k] : m));
	const escKey = () => `${S.run?.id}:${S.esc.test}:${S.esc.audience}:${S.esc.lang}:${escNoAI() ? "tpl" : "ai"}:${S.esc.evidence}`;
	/** Sin IA: elegido por la persona, o porque no hay IA configurada. */
	const escNoAI = () => !S.config.ai_enabled || !!S.esc.noAI;

	// El resumen en memoria y las respuestas en vuelo pertenecen a la evidencia anterior.
	// Retirarlo no genera IA: el cache del servidor valida su huella; regenerar sigue siendo explícito.
	function invalidateEscalation() {
		S.esc.evidence++;
		S.esc.loadedKey = null; S.esc.data = null; S.esc.msg = null; S.esc.loading = false;
		turn("escalate"); turn("escalate-cache");
	}

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
		S.esc.loadedKey = key; S.esc.data = null; S.esc.msg = null;
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
		const key = escKey(), current = turn("escalate");
		turn("escalate-cache"); // una lectura anterior no pisa la generación solicitada
		S.esc.loadedKey = key;
		S.esc.loading = true; S.esc.msg = null; renderEscalate();
		try {
			const data = await apiSend("POST", "/api/v1/ui/escalate", { run_id: S.run.id, test_id: S.esc.test, audience: S.esc.audience, lang: S.esc.lang, regenerate: !!regenerate, no_ai: escNoAI() });
			// otro test, público o idioma elegido mientras tanto: el resultado y su aviso son de otro escalado
			if (current() && escKey() === key) {
				S.esc.data = data;
				if (data.ai_error) S.esc.msg = { ok: false, text: tr("La IA no respondió, así que se armó con la plantilla. Detalle: {e}", { e: data.ai_error }) };
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
			${d.repro?.length ? `<h4>${esc(L.repro)}</h4>${d.repro.map((r) => block(r.cmd)).join("")}` : ""}
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
		if (d.repro?.length) out.push("", B(L.repro + ":"), fence(d.repro.map((r) => r.cmd).join("\n"), "bash"));
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

	function escCard(e) {
		const L = ESC_L[e.lang] || ESC_L.es, f = e.facts;
		const meta = [[L.run, f.run_name], [L.env, f.env || "—"], [L.result, escFmt(L.failed, { f: f.failed, t: f.total })], ["", fmtDateTime(f.started_at)]];
		const showCalls = e.audience !== "business" && f.network?.length && !f.dev?.some((d) => d.calls?.length);
		return `<article class="esc-card sev-${esc(e.severity)}" id="esc-card" lang="${esc(e.lang)}" data-no-i18n>
			<div class="esc-band"></div>
			<header class="esc-head">
				<div class="esc-tags"><span class="esc-sev">${esc(L[e.severity] || e.severity)}</span><span class="esc-aud">${esc(L[e.audience])}</span></div>
				<span class="esc-brand">${LOGO_SVG}<span>tracereports<span class="esc-cursor">_</span></span></span>
			</header>
			<h2 class="esc-title">${esc(e.title)}</h2>
			<p class="esc-headline">${esc(e.headline)}</p>
			<dl class="esc-meta">${meta.map(([k, v]) => `<div>${k ? `<dt>${esc(k)}</dt>` : ""}<dd>${esc(v)}</dd></div>`).join("")}</dl>
			${f.screenshot ? `<figure class="esc-shot"><img src="${esc(f.screenshot)}" alt="${esc(L.shot)}"><figcaption>${esc(L.shot)}${f.shot_caption ? ` · ${esc(f.shot_caption)}` : ""}</figcaption></figure>` : ""}
			<div class="esc-grid">
				<section><h3>${esc(L.what)}</h3><p>${esc(e.what_happened)}</p></section>
				<section><h3>${esc(L.impact)}</h3><p>${esc(e.impact)}</p></section>
				<section class="esc-wide"><h3>${esc(L.cause)}</h3><p>${esc(e.root_cause)}</p></section>
			</div>
			${showCalls ? `<section><h3>${esc(L.calls)}</h3><table class="esc-calls">${f.network.map((n) => `<tr><td><b>${esc(n.method)}</b></td><td class="esc-path">${esc(n.path)}</td>
				<td class="esc-st">${esc(n.outcome)}</td><td class="esc-ms">${n.duration_ms ? `${n.duration_ms} ms` : ""}</td></tr>`).join("")}</table></section>` : ""}
			${e.evidence?.length ? `<section><h3>${esc(L.evidence)}</h3><ul>${e.evidence.map((x) => `<li>${esc(x)}</li>`).join("")}</ul></section>` : ""}
			${f.flaky ? `<p class="esc-flaky">${esc(escFmt(L.flaky, { x: f.flaky }))}</p>` : ""}
			${e.next_steps?.length ? `<section><h3>${esc(L.next)}</h3><ol>${e.next_steps.map((x) => `<li>${esc(x)}</li>`).join("")}</ol></section>` : ""}
			${e.owner ? `<p class="esc-owner"><span>${esc(L.owner)}</span> <b>${esc(e.owner)}</b></p>` : ""}
			${f.dev?.length ? `<section class="esc-dev"><h3>${esc(L.tech)}</h3>${f.dev.map((d) => escDevHTML(d, L, !f.test_name)).join("")}</section>` : ""}
			<footer class="esc-foot"><a href="${esc(escReportURL(e))}" target="_blank" rel="noopener">${esc(L.report)} ↗</a>
				<span>${esc(e.source === "ai" ? escFmt(L.by_ai, { m: e.model || "" }) : L.by_tpl)}</span></footer>
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
		const lines = [`${style === "plain" ? `[${(L[e.severity] || e.severity).toUpperCase()}]` : `${B(L[e.severity] || e.severity)} ·`} ${B(e.title)}`, e.headline, ""];
		[[L.what, e.what_happened], [L.impact, e.impact], [L.cause, e.root_cause]].forEach(([k, v]) => v && lines.push(`${B(k + ":")} ${v}`));
		if (e.audience !== "business" && f.network?.length && !f.dev?.some((d) => d.calls?.length)) {
			lines.push("", B(L.calls + ":"));
			f.network.forEach((n) => lines.push(`• ${n.method} ${n.path} → ${n.outcome}${n.duration_ms ? ` (${n.duration_ms} ms)` : ""}`));
		}
		if (e.evidence?.length) { lines.push("", B(L.evidence + ":")); e.evidence.forEach((x) => lines.push(`• ${x}`)); }
		if (e.next_steps?.length) { lines.push("", B(L.next + ":")); e.next_steps.forEach((x, i) => lines.push(`${i + 1}. ${x}`)); }
		if (e.owner) lines.push("", `${B(L.owner + ":")} ${e.owner}`);
		if (f.dev?.length) { lines.push("", B(L.tech + ":")); f.dev.forEach((d) => lines.push(...escDevText(d, L, style, B, !f.test_name))); }
		lines.push("", `${L.run}: ${f.run_name} · ${f.env || "—"} · ${fmtDateTime(f.started_at)} · ${escFmt(L.failed, { f: f.failed, t: f.total })}`);
		lines.push(`${L.report}: ${escReportURL(e)}`);
		return lines.join("\n");
	}

	/** HTML con estilos en línea para pegar en un correo (Outlook, Gmail) con formato. */
	async function escEmailHTML(e) {
		const L = ESC_L[e.lang] || ESC_L.es, f = e.facts;
		const color = { critical: "#c62828", high: "#e65100", medium: "#b26a00", low: "#2e7d32" }[e.severity] || "#455a64";
		const h3 = (t) => `<h3 style="margin:18px 0 6px;font-size:14px;color:#263238">${esc(t)}</h3>`;
		const p = (t) => `<p style="margin:0;font-size:14px;line-height:1.5;color:#37474f">${esc(t)}</p>`;
		const shot = f.screenshot ? await toDataURL(f.screenshot).catch(() => "") : "";
		return `<div style="font-family:Segoe UI,Arial,sans-serif;max-width:680px;border:1px solid #e0e0e0;border-top:6px solid ${color};border-radius:8px;padding:20px 24px">
			<div style="font-size:12px;font-weight:700;color:${color};text-transform:uppercase;letter-spacing:.05em">${esc(L[e.severity])} · ${esc(L[e.audience])}</div>
			<h2 style="margin:6px 0 4px;font-size:20px;color:#1c2833">${esc(e.title)}</h2>${p(e.headline)}
			<p style="margin:10px 0 0;font-size:12px;color:#78909c">${esc(L.run)}: ${esc(f.run_name)} · ${esc(f.env || "—")} · ${esc(fmtDateTime(f.started_at))} · ${esc(escFmt(L.failed, { f: f.failed, t: f.total }))}</p>
			${shot ? `<img src="${shot}" alt="${esc(L.shot)}" style="margin-top:14px;max-width:100%;border:1px solid #e0e0e0;border-radius:6px">` : ""}
			${h3(L.what)}${p(e.what_happened)}${h3(L.impact)}${p(e.impact)}${h3(L.cause)}${p(e.root_cause)}
			${e.audience !== "business" && f.network?.length && !f.dev?.some((d) => d.calls?.length) ? h3(L.calls) + `<ul style="margin:0;padding-left:20px;font-size:13px;color:#37474f">${f.network.map((n) => `<li><code>${esc(n.method)} ${esc(n.path)}</code> → ${esc(n.outcome)}</li>`).join("")}</ul>` : ""}
			${e.evidence?.length ? h3(L.evidence) + `<ul style="margin:0;padding-left:20px;font-size:13px;color:#37474f">${e.evidence.map((x) => `<li>${esc(x)}</li>`).join("")}</ul>` : ""}
			${e.next_steps?.length ? h3(L.next) + `<ol style="margin:0;padding-left:20px;font-size:13px;color:#37474f">${e.next_steps.map((x) => `<li>${esc(x)}</li>`).join("")}</ol>` : ""}
			${e.owner ? `<p style="margin:16px 0 0;font-size:13px;color:#37474f">${esc(L.owner)}: <b>${esc(e.owner)}</b></p>` : ""}
			${f.dev?.length ? h3(L.tech) + f.dev.map((d) => escDevEmail(d, L, !f.test_name)).join("") : ""}
			<p style="margin:16px 0 0"><a href="${esc(escReportURL(e))}" style="color:#1565c0">${esc(L.report)}</a></p>
		</div>`;
	}

	/** Detalle técnico del correo: bloques monoespaciados con estilos en línea. */
	function escDevEmail(d, L, named) {
		const pre = (t) => `<pre style="margin:4px 0 8px;padding:10px 12px;background:#f5f7f9;border:1px solid #e3e7eb;border-radius:6px;font:12px/1.45 Consolas,'Courier New',monospace;color:#263238;white-space:pre-wrap;word-break:break-all">${esc(t)}</pre>`;
		const small = (t) => `<p style="margin:6px 0 2px;font-size:12.5px;color:#37474f">${t}</p>`;
		const ctx = escDevContext(d, L);
		let html = (named ? `<p style="margin:14px 0 2px;font-size:13.5px;font-weight:700;color:#263238">${esc(d.test_name)}</p>` : "") + (ctx.length ? small(ctx.map(([k, v]) => `${esc(k)}: <b>${esc(v)}</b>`).join(" · ")) : "");
		if (d.error || d.error_trace) html += small(`<b>${esc(L.stack)}</b>`) + pre([d.error, d.error_trace].filter(Boolean).join("\n\n"));
		if (d.repro?.length) html += small(`<b>${esc(L.repro)}</b>`) + pre(d.repro.map((r) => r.cmd).join("\n"));
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

	async function toDataURL(src) {
		const blob = await (await fetch(src)).blob();
		return new Promise((ok, fail) => { const r = new FileReader(); r.onload = () => ok(r.result); r.onerror = fail; r.readAsDataURL(blob); });
	}

	/** Convierte la tarjeta en PNG: estilos copiados en línea + SVG foreignObject + canvas (sin librerías). */
	async function cardToPNG(card) {
		const clone = card.cloneNode(true);
		const src = [card, ...card.querySelectorAll("*")], dst = [clone, ...clone.querySelectorAll("*")];
		src.forEach((n, i) => {
			const cs = getComputedStyle(n);
			let css = "";
			for (const prop of cs) css += `${prop}:${cs.getPropertyValue(prop)};`;
			dst[i].setAttribute("style", css);
		});
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

	async function escShare(kind, btn) {
		const e = S.esc.data;
		if (!e) return;
		try {
			if (kind === "plain" || kind === "slack" || kind === "md") { CF.copyText(escText(e, kind), btn); return; }
			if (kind === "email") {
				const html = await escEmailHTML(e);
				if (!navigator.clipboard?.write || !window.ClipboardItem) { CF.copyText(escText(e, "plain"), btn); return; }
				await navigator.clipboard.write([new ClipboardItem({ "text/html": new Blob([html], { type: "text/html" }), "text/plain": new Blob([escText(e, "plain")], { type: "text/plain" }) })]);
				flash(btn, tr("¡Copiado!")); return;
			}
			if (kind === "png" || kind === "png-dl") {
				btn.disabled = true;
				const blob = await cardToPNG($("#esc-card"));
				btn.disabled = false;
				if (kind === "png" && navigator.clipboard?.write && window.ClipboardItem) {
					await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
					flash(btn, tr("¡Imagen copiada!")); return;
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
				<div class="esc-share-group"><span class="esc-share-lbl">${tr("Copiar")}</span>
					<button class="cf-btn cf-btn-sm" data-esc-share="plain" data-tip="${tr("Texto simple, para un correo o cualquier chat")}">${icon("i-copy")}${tr("Texto")}</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="slack" data-tip="${tr("Con el formato de Slack (*negritas*)")}">${icon("i-copy")}Slack</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="md" data-tip="${tr("Markdown: Teams, Jira, GitHub, Confluence")}">${icon("i-copy")}Teams / Markdown</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="email" data-tip="${tr("Con formato y la captura, para pegar en Outlook o Gmail")}">${icon("i-copy")}${tr("Correo")}</button>
				</div>
				<div class="esc-share-group"><span class="esc-share-lbl">${tr("Imagen")}</span>
					<button class="cf-btn cf-btn-sm" data-esc-share="png" data-tip="${tr("Copia la tarjeta como imagen para pegarla en Teams, Slack o una presentación")}">${icon("i-camera")}${tr("Copiar imagen")}</button>
					<button class="cf-btn cf-btn-sm" data-esc-share="png-dl" data-tip="${tr("Descarga la tarjeta como PNG")}">${icon("i-download")}PNG</button>
				</div>
				${cfg.teams || cfg.slack ? `<div class="esc-share-group"><span class="esc-share-lbl">${tr("Enviar")}</span>
					${cfg.teams ? `<button class="cf-btn cf-btn-sm cf-btn-primary" data-esc-share="teams" data-tip="${tr("Publica el resumen en el canal de Teams configurado (TEAMS_WEBHOOK_URL)")}">${icon("i-megaphone")}Teams</button>` : ""}
					${cfg.slack ? `<button class="cf-btn cf-btn-sm cf-btn-primary" data-esc-share="slack-send" data-tip="${tr("Publica el resumen en el canal de Slack configurado (SLACK_WEBHOOK_URL)")}">${icon("i-megaphone")}Slack</button>` : ""}
				</div>` : ""}
				${cfg.trackers?.length ? `<div class="esc-share-group"><span class="esc-share-lbl">${tr("Ticket")}</span>
					${cfg.trackers.map((k) => `<button class="cf-btn cf-btn-sm" data-esc-ticket="${esc(k.id)}" data-tip="${tr("Crea un ticket en {t} con este resumen, la captura y el link al reporte. Si el mismo test ya tiene uno, te muestra ese en vez de duplicarlo.", { t: k.name })}">${icon("i-bug")}${esc(k.name)}</button>`).join("")}
				</div>` : ""}
			</div>` : "";
		const preview = !canAct() && !STATIC
			? `<div class="card m-empty">${icon("i-megaphone")}<h5>${tr("Escalar no está disponible desde aquí")}</h5><p>${esc(actReason())}</p></div>`
			: st.loading ? `<div class="card esc-loading"><div class="shimmer"></div><div class="shimmer"></div><div class="shimmer short"></div>
				<p class="m-hint">${cfg.ai_enabled ? tr("La IA está escribiendo el resumen para {a}…", { a: tr(AUDIENCES.find((a) => a.id === st.audience).name) }) : tr("Armando el resumen…")}</p></div>`
			: e ? `<div class="esc-frame">${share}${escCard(e)}${escTimingHTML(e)}</div>`
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
		if (!res.ok) throw new Error(data.error || `${res.status}`);
		return data;
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
	const AI_LANGS = [["auto", "Automático (el idioma de cada test)"], ["es", "Español"], ["en", "English"]];
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
		const reason = ["locked", "login", "remote"].includes(d.edit_reason) ? d.edit_reason : "remote";
		const env = envExample(d);
		S.settings.envText = env;
		const title = {
			locked: "El administrador bloqueó estos ajustes",
			login: "Inicia sesión para cambiar los ajustes",
			remote: "Los ajustes están en solo lectura desde este navegador",
		}[reason];
		const why = {
			locked: "El servidor tiene TRACEREPORTS_SETTINGS_LOCKED=1: la IA se configura solo con el archivo .env, no desde esta pantalla.",
			login: "El servidor pide usuario y clave para cambiar los ajustes. Recarga la página e ingresa los de TRACEREPORTS_UI_USER y TRACEREPORTS_UI_PASSWORD.",
			remote: "Por seguridad, sin un usuario y clave configurados nadie puede cambiar la IA desde la red: solo desde el mismo equipo donde corre el servidor. Así nadie más puede ver el uso de tu API key ni cambiarla.",
		}[reason];
		const steps = reason === "login" ? "" : `<ol class="ro-steps">
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
		return [...crypto.getRandomValues(new Uint32Array(len))].map((n) => cs[n % cs.length]).join("");
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
				: cur.id === "ai" ? aiCardSettings() : cur.id === "usage" ? usageCard() : connectCard(st.data);
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
		];
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
				<b>${head}</b>${s.description && s.source === "statuspage" ? ` <span class="m-hint" data-no-i18n>(${esc(s.description)})</span>` : ""}${s.source === "local" ? ` <span class="m-hint">${esc(s.indicator === "none" ? tr("Ollama responde (versión {v})", { v: s.description }) : tr("Ollama no responde en {u}", { u: s.description }))}</span>` : ""}
				${s.error ? `<p class="m-hint">${esc(tr("No se pudo consultar la página de estado: {e}", { e: s.error }))}</p>` : ""}
				${comps ? `<ul class="prov-status-list">${comps}</ul>` : ""}
				${incs ? `<p class="prov-status-sub">${tr("Incidentes abiertos")}</p><ul class="prov-status-list">${incs}</ul>` : ""}
				${observed ? `<p class="m-hint">${esc(observed)}</p>` : ""}
				<p class="m-hint">${s.page_url ? `<a href="${esc(s.page_url)}" target="_blank" rel="noopener">${tr("Ver página de estado")} ↗</a>` : ""}${when ? ` · ${esc(tr("Consultado a las {h}", { h: when }))}` : ""}</p>
			</div></div>`;
	}

	const USAGE_KINDS = { triage: "Diagnóstico de tests", run_summary: "Resumen de la ejecución", escalation: "Escalamientos", test: "Probar conexión" };

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
			<p class="field-help">Diagnostica cada fallo y el resumen de la ejecución. Elige el proveedor: en la nube (Gemini, Claude, OpenAI) o local con Ollama, donde los datos de tus tests no salen de tu red.</p>
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
			${p ? `<fieldset class="ai-fields" ${ro ? "disabled" : ""}>
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
			if (!S.run && S.runId) { await loadRun(); await loadTest(); }
		} catch (err) { console.warn("runs refresh failed", err); }
		setTimeout(runsLoop, RUNS_POLL_MS);
	}

	// ---------- boot ----------
	async function init() {
		applyTheme(initialTheme(), false);
		$(".nav-global").hidden = !!STATIC; // las métricas cruzan ejecuciones: no van en el ZIP
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
			await loadRun();
			await loadTest();
			writeHash();
		} catch (err) {
			console.error(err);
		}
		if (GLOBAL_VIEWS.has(S.view)) renderView(); // métricas y ajustes funcionan aunque no haya ejecuciones
		if (STATIC) return; // exported snapshot: nothing to poll
		try { S.autoScroll = localStorage.getItem("tracereports-autoscroll") !== "off"; } catch { /* ignore */ }
		new CF.LiveStream("/api/v1/stream", { onEvent: onLive, onStatus: setLiveState }).start();
		setInterval(renderLive, 30000); // pasa a "Sin actividad" aunque no lleguen eventos
		setTimeout(pollLoop, POLL_MS);
		setTimeout(runsLoop, RUNS_POLL_MS);
	}

	init();
})();
