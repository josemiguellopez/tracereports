// En vivo: lo que corre ahora, por ejecución. Qué terminó, qué sigue, qué está lento ("tortuga":
// más que su p90 o el doble de su p50) y qué dejó de enviar ("sin señal"), con carriles por worker
// (Gantt) y una tabla por archivo y test donde lo que pide acción va primero. Los estados los calcula
// el servidor (/api/v1/live); aquí solo se dibujan. Todo dato del cliente entra escapado.
(() => {
	"use strict";

	// estado → clase, ícono y texto (nunca solo color)
	const STATES = {
		no_signal: { cls: "nosig", icon: "i-warning", label: "Sin señal", rank: 0 },
		turtle: { cls: "turtle", icon: "i-running", label: "Tortuga", rank: 1 },
		running: { cls: "running", icon: "i-running", label: "En curso", rank: 2 },
		FAIL: { cls: "fail", icon: "i-fail", label: "Fallo", rank: 3 },
		INTERRUPTED: { cls: "interrupted", icon: "i-interrupted", label: "Interrumpido", rank: 4 },
		WARNING: { cls: "warning", icon: "i-warning", label: "Warning", rank: 5 },
		PASS: { cls: "pass", icon: "i-pass", label: "Pass", rank: 6 },
		SKIP: { cls: "skip", icon: "i-skip", label: "Skip", rank: 7 },
	};
	const stateOf = (s) => STATES[s] || { cls: "running", icon: "i-running", label: s, rank: 8 };
	const COUNT_ORDER = ["PASS", "FAIL", "SKIP", "WARNING", "INTERRUPTED", "running", "turtle", "no_signal"];

	/** 75 ms → "0 s", 65000 → "1 min 5 s", 3725000 → "1 h 2 min". */
	function fmtDur(ms) {
		const s = Math.max(0, Math.round((ms || 0) / 1000));
		if (s < 60) return `${s} s`;
		const m = Math.floor(s / 60);
		if (m < 60) return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
		return m % 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${Math.floor(m / 60)} h`;
	}

	/** Las corridas con algo que pide acción primero y abiertas; dentro, sus tests ya vienen ordenados. */
	function sortRuns(runs) {
		const worst = (r) => Math.min(9, ...(r.tests || []).map((t) => stateOf(t.state).rank));
		return [...runs].map((r) => ({ ...r, worst: worst(r) })).sort((a, b) => a.worst - b.worst || b.started_at - a.started_at);
	}

	/**
	 * Carriles del Gantt: un carril por worker (o por corrida si el cliente no dice el worker), cada
	 * test como una barra en % del tramo [inicio de la ejecución, ahora o su fin].
	 */
	function ganttLayout(exec, now) {
		const tests = exec.runs.flatMap((r) => (r.tests || []).map((t) => ({ ...t, lane: t.worker || `${r.name}` })));
		if (!tests.length) return { lanes: [], from: exec.started_at, to: now };
		const from = Math.min(exec.started_at, ...tests.map((t) => t.started_at));
		const to = Math.max(from + 1000, exec.running ? now : Math.max(...tests.map((t) => t.ended_at || t.started_at)));
		const span = to - from;
		const lanes = new Map();
		for (const t of tests) {
			const end = t.ended_at || now;
			const bar = { id: t.id, run_id: t.run_id, name: t.name, state: t.state, left: ((t.started_at - from) / span) * 100,
				width: Math.max(0.4, ((end - t.started_at) / span) * 100), elapsed: t.elapsed, factor: t.factor };
			if (!lanes.has(t.lane)) lanes.set(t.lane, []);
			lanes.get(t.lane).push(bar);
		}
		const natural = (a, b) => a.localeCompare(b, undefined, { numeric: true });
		return { from, to, lanes: [...lanes.entries()].sort((a, b) => natural(a[0], b[0])).map(([worker, bars]) => ({ worker, bars })) };
	}

	/** Marcas de tiempo del eje: 4-6 horas redondas dentro del tramo. */
	function ticks(from, to) {
		const span = to - from, steps = [60e3, 2 * 60e3, 5 * 60e3, 10 * 60e3, 15 * 60e3, 30 * 60e3, 3600e3, 2 * 3600e3];
		const step = steps.find((s) => span / s <= 6) || 4 * 3600e3;
		const out = [];
		for (let t = Math.ceil(from / step) * step; t <= to; t += step) out.push({ at: t, left: ((t - from) / span) * 100 });
		return out;
	}

	/** El progreso: con tests planificados, % y ETA; sin ellos, cuántos terminaron. */
	function progress(exec) {
		if (exec.planned > 0) {
			const pct = Math.min(100, Math.round((exec.done / exec.planned) * 100));
			return { pct, known: true, eta: exec.running && exec.eta_ms ? exec.eta_ms : 0 };
		}
		return { pct: exec.total ? Math.round((exec.done / exec.total) * 100) : 0, known: false, eta: 0 };
	}

	/**
	 * Dibuja la vista. data: {list, exec, now}; h: {t, esc, icon, canAct, clock(ms)}.
	 * Devuelve el HTML (app.js lo pone en #view-live y maneja los clics).
	 */
	function render(data, h) {
		const { t, esc, icon } = h;
		const now = data.now || Date.now();
		if (!data.list.length) {
			return `<div class="card live-empty">${icon("i-running")}<h3>${esc(t("No hay ejecuciones en curso"))}</h3>
				<p class="m-hint">${esc(t("Cuando un cliente empiece a enviar resultados, aquí ves qué terminó, qué sigue corriendo, qué está lento y qué dejó de enviar, por worker y por test."))}</p></div>`;
		}
		const chips = data.list.map((e) => `<button type="button" class="live-exec ${e.key === data.exec?.key ? "on" : ""}" data-live-exec="${esc(e.key)}" aria-pressed="${e.key === data.exec?.key}">
			<span class="live-dot ${e.running ? "on" : ""}" aria-hidden="true"></span><b data-no-i18n>${esc(e.title)}</b>
			<small>${e.running ? esc(t("en curso")) : esc(t("terminada"))} · ${e.done}/${e.planned || e.total}${e.counts.no_signal ? ` · <span class="nosig-txt">${esc(t("{n} sin señal", { n: e.counts.no_signal }))}</span>` : ""}${e.counts.FAIL ? ` · ${esc(t("{n} fallos", { n: e.counts.FAIL }))}` : ""}</small></button>`).join("");
		const e = data.exec;
		if (!e) return `<div class="live-execs" role="group" aria-label="${esc(t("Ejecuciones"))}">${chips}</div><p class="m-hint">${esc(t("Cargando…"))}</p>`;

		const p = progress(e);
		const counts = COUNT_ORDER.filter((k) => e.counts[k]).map((k) => {
			const st = stateOf(k);
			return `<span class="live-count ${st.cls}">${icon(st.icon)}<b>${e.counts[k]}</b> ${esc(t(st.label))}</span>`;
		}).join("");
		const meta = [e.project, e.environment, e.branch, e.commit && e.commit.slice(0, 7)].filter(Boolean).map((x) => `<span data-no-i18n>${esc(x)}</span>`).join(" · ");
		const cancel = e.running && h.canAct()
			? (e.cancel_requested
				? `<span class="live-cancelled">${icon("i-pause")}${esc(t("Cancelación pedida: cada proceso se detendrá al terminar el test en curso y saltará los que faltan, también los que empiecen después."))}</span>`
				: `<details class="live-cancel"><summary class="cf-btn cf-btn-sm">${icon("i-pause")}${esc(t("Cancelar ejecución…"))}</summary>
					<form data-live-cancel><p class="m-hint">${esc(t("Se detendrá al terminar el test en curso: los clientes con TRACEREPORTS_REMOTE_CONTROL=1 ven el pedido entre un test y otro, marcan los que faltan como Skip y cierran la ejecución. Alcanza también a las corridas que la ejecución abra después. No se mata ningún proceso."))}</p>
					<input name="reason" maxlength="300" placeholder="${esc(t("Motivo (opcional)"))}" aria-label="${esc(t("Motivo"))}">
					<button class="cf-btn cf-btn-sm cf-btn-danger" type="submit">${esc(t("Pedir cancelación"))}</button></form></details>`)
			: "";
		const head = `<div class="card live-head">
			<div class="live-title"><h3 data-no-i18n>${esc(e.title)}</h3>${e.running ? `<span class="live-badge on">${icon("i-running")}${esc(t("EN CURSO"))}</span>` : `<span class="live-badge">${esc(t("Terminada"))}</span>`}
				<span class="live-meta">${meta}</span>${cancel}</div>
			<div class="live-progress" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${p.pct}" aria-label="${esc(t("Progreso"))}">
				<div class="live-bar"><span style="width:${p.pct}%"></span></div>
				<span class="live-pct">${p.known ? `${p.pct}% · ${esc(t("{d} de {n} tests", { d: e.done, n: e.planned }))}` : esc(t("{d} terminados de {n} iniciados", { d: e.done, n: e.total }))}${p.eta ? ` · ${esc(t("ETA {t}", { t: fmtDur(p.eta) }))}` : ""}
				· ${esc(t("{n} workers", { n: e.workers }))} · ${esc(fmtDur((e.running ? now : e.ended_at) - e.started_at))}</span></div>
			<div class="live-counts">${counts}</div></div>`;

		const g = ganttLayout(e, now);
		const gantt = g.lanes.length ? `<div class="card live-gantt"><h6>${esc(t("Carriles por worker"))}</h6>
			<div class="lg-axis" aria-hidden="true">${ticks(g.from, g.to).map((k) => `<span style="left:${k.left}%">${esc(h.clock(k.at))}</span>`).join("")}</div>
			${g.lanes.map((l) => `<div class="lg-lane"><span class="lg-worker" data-no-i18n>${esc(l.worker)}</span><div class="lg-track">${l.bars.map((b) => {
				const st = stateOf(b.state);
				const tip = `${b.name} · ${t(st.label)} · ${fmtDur(b.elapsed)}${b.factor ? ` · ×${b.factor.toFixed(1)} p50` : ""}`;
				return `<a class="lg-bar ${st.cls}" href="#run=${b.run_id}&view=tests&test=${b.id}" style="left:${b.left}%;width:${b.width}%" data-tip="${esc(tip)}" aria-label="${esc(tip)}"><span data-no-i18n>${esc(b.name)}</span></a>`;
			}).join("")}</div></div>`).join("")}</div>` : "";

		const rows = sortRuns(e.runs).map((r) => {
			const open = r.worst <= 2 || e.runs.length === 1;
			const tests = (r.tests || []).map((x) => {
				const st = stateOf(x.state);
				const vs = x.p50 ? `<span class="lt-vs ${x.factor >= 2 ? "hi" : ""}">×${x.factor.toFixed(1)}</span>` : `<span class="m-hint">—</span>`;
				const ago = x.state === "running" || x.state === "turtle" || x.state === "no_signal" ? `<small>${esc(t("hace {t}", { t: fmtDur(x.idle) }))}</small>` : "";
				return `<tr class="lt-row ${st.cls}" data-live-test="${x.id}" data-live-run="${x.run_id}" tabindex="0">
					<td class="lt-name"><a href="#run=${x.run_id}&view=tests&test=${x.id}" data-no-i18n>${esc(x.name)}</a>${x.worker ? ` <span class="m-hint" data-no-i18n>${esc(x.worker)}</span>` : ""}</td>
					<td><span class="lt-state ${st.cls}">${icon(st.icon)}${esc(t(st.label))}</span></td>
					<td class="lt-num">${esc(fmtDur(x.elapsed))}</td><td class="lt-num">${vs}</td>
					<td class="lt-step"><span data-no-i18n>${esc(x.last_step || "—")}</span> ${ago}</td></tr>`;
			}).join("");
			const sum = COUNT_ORDER.filter((k) => (r.tests || []).some((x) => x.state === k)).map((k) => `<span class="lt-state ${stateOf(k).cls}">${icon(stateOf(k).icon)}${(r.tests || []).filter((x) => x.state === k).length}</span>`).join(" ");
			return `<details class="live-run" ${open ? "open" : ""}><summary><span data-no-i18n>${esc(r.name)}</span> <span class="live-run-sum">${sum}</span></summary>
				<table class="live-table"><thead><tr><th>${esc(t("Test"))}</th><th>${esc(t("Estado"))}</th><th>${esc(t("Dura."))}</th><th title="${esc(t("Duración frente a la mediana de sus ejecuciones anteriores"))}">${esc(t("vs p50"))}</th><th>${esc(t("Último paso"))}</th></tr></thead>
				<tbody>${tests || `<tr><td colspan="5" class="m-hint">${esc(t("Todavía sin tests."))}</td></tr>`}</tbody></table></details>`;
		}).join("");

		return `<div class="live-execs" role="group" aria-label="${esc(t("Ejecuciones"))}">${chips}</div>${head}${gantt}
			<div class="card live-runs"><h6>${esc(t("Archivo / test"))}</h6>${rows}
			<p class="m-hint">${esc(t("Tortuga: más lento que su p90 (o el doble de su mediana) en sus ejecuciones anteriores. Sin señal: nada del test hace {n} min.", { n: Math.round((data.no_signal_ms || 180000) / 60000 * 10) / 10 }))}</p></div>`;
	}

	window.TraceReportsLive = { STATES, fmtDur, sortRuns, ganttLayout, ticks, progress, render };
})();
