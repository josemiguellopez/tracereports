// Avisos de ejecuciones nuevas: qué se avisa y qué no (sin DOM, para probarlo aparte).
//
// - Se avisa por EJECUCIÓN, no por run: un lanzador que abre un run por archivo (o muchos workers en
//   paralelo) es una sola ejecución con muchas corridas; antes cada corrida volvía a abrir el aviso.
// - "Ahora no" silencia esa ejecución; "Silenciar 15 min" silencia todas por un rato. Las dos cosas
//   se ven y se deshacen en la campana de la barra superior.
// - Se guarda en el navegador (localStorage, si hay): lo silenciado vence solo (15 min / 12 h), así
//   nunca queda un aviso apagado para siempre sin que nadie lo recuerde.
(function () {
	"use strict";

	const KEY = "tracereports-run-notify";
	const SNOOZE_MS = 15 * 60 * 1000;
	const MUTE_TTL_MS = 12 * 60 * 60 * 1000; // una ejecución silenciada con "Ahora no" vuelve a avisar al día siguiente
	const MAX_MUTED = 50;

	/** La ejecución de un run: su execution_id o, sin él, el run solo. */
	function execKey(run) {
		return run?.execution_id ? `x:${run.execution_id}` : `r:${run?.id}`;
	}

	function empty() {
		return { until: 0, muted: {} };
	}

	/** Lo guardado, sin lo vencido. */
	function prune(state, now) {
		const out = { until: state?.until > now ? state.until : 0, muted: {} };
		const live = Object.entries(state?.muted || {}).filter(([, m]) => m && now - (m.at || 0) < MUTE_TTL_MS)
			.sort((a, b) => b[1].at - a[1].at).slice(0, MAX_MUTED);
		for (const [k, m] of live) out.muted[k] = { title: String(m.title || k), at: m.at };
		return out;
	}

	function load(now = Date.now(), storage = globalThis.localStorage) {
		try {
			return prune(JSON.parse(storage?.getItem(KEY) || "null") || empty(), now);
		} catch {
			return empty();
		}
	}

	function save(state, storage = globalThis.localStorage) {
		try {
			storage?.setItem(KEY, JSON.stringify(state));
		} catch { /* sin almacenamiento: vale para esta pestaña */ }
	}

	/** Silenciado del todo (minutos que quedan, redondeados hacia arriba) o 0. */
	function snoozeLeft(state, now) {
		return state.until > now ? Math.ceil((state.until - now) / 60000) : 0;
	}

	function snooze(state, now, ms = SNOOZE_MS) {
		return { ...state, until: now + ms };
	}

	function resume(state) {
		return { ...state, until: 0 };
	}

	function mute(state, key, title, now) {
		return { ...state, muted: { ...state.muted, [key]: { title: title || key, at: now } } };
	}

	function unmute(state, key) {
		const muted = { ...state.muted };
		delete muted[key];
		return { ...state, muted };
	}

	/**
	 * Las ejecuciones nuevas a partir de los runs nuevos: una por ejecución, con sus corridas y la más
	 * reciente (la que se abre). ``toast``: si corresponde mostrar el aviso (no silenciado del todo ni
	 * esa ejecución); la insignia de la barra las cuenta igual (no se pierde nada).
	 */
	function group(runs, state, now) {
		const by = new Map();
		for (const r of runs) {
			if (!r) continue;
			const k = execKey(r);
			const g = by.get(k) || { key: k, title: r.execution_id || r.name || `#${r.id}`, runs: [], latest: r };
			g.runs.push(r);
			if (r.id > g.latest.id) g.latest = r;
			by.set(k, g);
		}
		const list = [...by.values()].sort((a, b) => a.latest.id - b.latest.id);
		const loud = snoozeLeft(state, now) ? [] : list.filter((g) => !state.muted[g.key]);
		return { list, loud };
	}

	window.TraceReportsRunNotify = { KEY, SNOOZE_MS, MUTE_TTL_MS, execKey, empty, prune, load, save, snoozeLeft, snooze, resume, mute, unmute, group };
})();
