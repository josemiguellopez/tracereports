/* TraceReports — idioma de la interfaz.
 *
 * La interfaz se escribe en español. Este módulo la traduce en el DOM a medida que se dibuja
 * (MutationObserver): texto y los atributos visibles (placeholder, title, aria-label, data-tip…).
 * Así ninguna plantilla de app.js necesita cambiar. Lo que es contenido del usuario (nombres de
 * tests, pasos, errores, código, texto de la IA) no se toca: ver SKIP.
 *
 * Para textos que no están en el DOM (gráficos en canvas) usar TraceReportsI18n.t("texto").
 */
(() => {
	"use strict";

	const KEY = "tracereports-lang";
	const LANGS = ["es", "en"];
	const ATTRS = ["placeholder", "title", "aria-label", "data-tip", "data-tip-title", "data-tip-keys", "alt", "data-caption"];
	// contenido del usuario o técnico: nunca se traduce
	const SKIP = "script,style,code,pre,textarea,.cf-code,[data-no-i18n],.step-text,.test-name,.detail-name,.report-name," +
		".report-env,.error-msg,.net-url,.mono,.ai-card p,.suggestion,.incident p,.loc-code,.tl-label,.cf-mock-meta,.bbox-legend code";

	// ---- diccionario español -> inglés ----
	// exact: frase completa (sin espacios al borde). patterns: [regex, reemplazo] para frases con datos.
	const EN = { exact: {}, patterns: [] };

	function stored() {
		try { const l = localStorage.getItem(KEY); return LANGS.includes(l) ? l : null; } catch { return null; }
	}
	function browserLang() {
		return /^es\b/i.test(navigator.language || "") ? "es" : "en";
	}

	let lang = stored() || browserLang();
	let explicit = !!stored();

	/** Traduce una frase; devuelve la misma si no hay traducción. */
	function t(es, vars) {
		let out = es;
		if (lang === "en") out = lookup(es) ?? es;
		if (vars) out = out.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? vars[k] : m));
		return out;
	}

	function lookup(raw) {
		const s = raw.trim();
		if (!s) return null;
		const hit = EN.exact[s];
		if (hit != null) return raw.replace(s, hit);
		for (const [re, rep] of EN.patterns) {
			if (re.test(s)) return raw.replace(s, s.replace(re, rep));
		}
		return null;
	}

	// ---- traducción del DOM, conservando el original para poder cambiar de idioma ----
	const origText = new WeakMap(); // Text -> español
	const doneText = new WeakMap(); // Text -> lo último que escribimos (para ignorar nuestro propio cambio)
	const origAttr = new WeakMap(); // Element -> {attr: español}

	const skipped = (el) => !!(el && el.closest && el.closest(SKIP));

	function translateText(node) {
		if (doneText.get(node) === node.data) return; // lo escribimos nosotros
		if (skipped(node.parentElement)) return;
		const original = node.data;
		origText.set(node, original);
		const out = lang === "en" ? lookup(original) : null;
		if (out != null && out !== original) { node.data = out; doneText.set(node, out); }
		else doneText.set(node, original);
	}

	function translateAttrs(el) {
		if (skipped(el) && !el.matches?.("[data-tip],[data-tip-title]")) return;
		let orig = origAttr.get(el);
		for (const a of ATTRS) {
			if (!el.hasAttribute(a)) continue;
			const v = el.getAttribute(a);
			if (!orig) { orig = {}; origAttr.set(el, orig); }
			if (orig[a] && (v === orig[a].es || v === orig[a].out)) continue;
			const out = lang === "en" ? lookup(v) : null;
			orig[a] = { es: v, out: out ?? v };
			if (out != null && out !== v) el.setAttribute(a, out);
		}
	}

	function translateTree(root) {
		if (!root) return;
		if (root.nodeType === 3) { translateText(root); return; }
		if (root.nodeType !== 1 && root.nodeType !== 9 && root.nodeType !== 11) return;
		if (root.nodeType === 1) translateAttrs(root);
		const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT);
		let n;
		while ((n = walker.nextNode())) {
			if (n.nodeType === 3) translateText(n);
			else translateAttrs(n);
		}
	}

	/** Vuelve a aplicar el idioma a todo el documento desde los textos originales. */
	function retranslate() {
		const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT);
		let n;
		while ((n = walker.nextNode())) {
			if (n.nodeType === 3) {
				const es = origText.get(n);
				if (es != null && n.data === doneText.get(n)) n.data = es; // vuelve al original…
				doneText.delete(n); // …y se traduce de nuevo (si no, translateText lo toma como propio)
				translateText(n);
			} else {
				const orig = origAttr.get(n);
				if (orig) for (const [a, v] of Object.entries(orig)) { if (n.getAttribute(a) === v.out) n.setAttribute(a, v.es); }
				origAttr.delete(n);
				translateAttrs(n);
			}
		}
	}

	const observer = new MutationObserver((records) => {
		for (const r of records) {
			if (r.type === "characterData") translateText(r.target);
			else if (r.type === "attributes") translateAttrs(r.target);
			else r.addedNodes.forEach(translateTree);
		}
	});

	function start() {
		document.documentElement.lang = lang;
		translateTree(document.body);
		observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ATTRS });
		document.documentElement.classList.remove("i18n-wait");
	}

	/** Cambia el idioma. persist=false: preferencia del servidor (no pisa la del usuario). */
	function setLang(l, { persist = true } = {}) {
		if (!LANGS.includes(l)) return;
		if (persist) { try { localStorage.setItem(KEY, l); } catch { /* ignore */ } explicit = true; }
		if (l === lang) return;
		lang = l;
		document.documentElement.lang = l;
		observer.disconnect();
		retranslate();
		observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ATTRS });
		document.dispatchEvent(new CustomEvent("tracereports:lang", { detail: l }));
	}

	/** Idioma por defecto del servidor (Ajustes): solo si el usuario no eligió uno. */
	function setDefault(l) { if (!explicit && l) setLang(l, { persist: false }); }

	window.TraceReportsI18n = { t, setLang, setDefault, get lang() { return lang; }, get explicit() { return explicit; }, LANGS, EN };

	// i18n.en.js (el diccionario) carga después: se empieza cuando el documento está listo
	document.addEventListener("DOMContentLoaded", start);
})();
