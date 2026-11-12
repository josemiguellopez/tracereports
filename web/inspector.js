// Inspector: reconstruye la página del momento del fallo (y las de los pasos, con
// TRACEREPORTS_DOM_STEPS) a partir del snapshot estructural, para inspeccionarla, probar XPath/CSS y
// copiar locators robustos sin volver a reproducir el fallo. La reconstrucción se "arma" con un
// enjambre de cursores (idea visual de swarm-site; implementación propia) y termina en el fallo.
// Los datos del snapshot son de un cliente cualquiera: se dibujan nodo por nodo, nunca como HTML.
(() => {
	"use strict";

	// ─── locators (funciones puras: test/web/inspector.test.js) ─────────────────────────────────
	// translate() de XPath 1.0: minúsculas y sin tildes ("Médico" = "medico"); la ñ se mantiene
	const FROM = "ABCDEFGHIJKLMNOPQRSTUVWXYZÁÉÍÓÚÜÀÈÌÒÙáéíóúüàèìòùÑ";
	const TO = "abcdefghijklmnopqrstuvwxyzaeiouuaeiouaeiouuaeiouñ";
	const fold = (s) => [...String(s ?? "")].map((c) => { const k = FROM.indexOf(c); return k >= 0 ? TO[k] : c.toLowerCase(); }).join("").replace(/\s+/g, " ").trim();
	const lit = (s) => (!s.includes("'") ? `'${s}'` : !s.includes('"') ? `"${s}"` : `concat('${s.split("'").join(`', "'", '`)}')`);
	const tr = (what) => `translate(${what},'${FROM}','${TO}')`;
	const GENERIC = new Set(["row", "col", "container", "wrapper", "content", "inner", "box", "flex", "grid", "item", "active", "show", "d-flex"]);
	// ids que genera un framework (":r1:", "x8f3a9c21d", "mat-input-12"): cambian entre ejecuciones.
	// "ficha-cliente" no: un sufijo de solo letras es una palabra.
	const autoId = (v) => /\d{3,}|[a-f0-9]{8,}|^[a-z]+-(?=[a-z]*\d)[a-z0-9]{2,}$|^:/i.test(v || "");

	/** El texto que identifica al elemento: el suyo, o su etiqueta accesible, placeholder o title. */
	function labelOf(n) {
		if (n.text && n.tag !== "input" && n.text !== "•••") return { by: "normalize-space(.)", text: n.text };
		for (const a of ["aria-label", "placeholder", "title"]) if (n.attrs?.[a]) return { by: "@" + a, text: n.attrs[a] };
		return null;
	}
	/** XPath por texto, sin distinguir mayúsculas ni tildes, con las primeras palabras (un texto largo cambia más). */
	function robustXPath(n) {
		const l = labelOf(n);
		if (!l) return null;
		return `//${n.tag}[contains(${tr(l.by)},${lit(fold(l.text).split(" ").slice(0, 6).join(" "))})]`;
	}
	/** Contenedores estables del elemento, del más cercano al más lejano. */
	function anchorsOf(n, nodes) {
		const out = [];
		for (let a = nodes[n.p]; a; a = nodes[a.p]) {
			if (a.tag === "body" || a.tag === "html") break;
			const at = a.attrs || {};
			if (at["data-testid"]) out.push(`//*[@data-testid=${lit(at["data-testid"])}]`);
			if (at.id && !autoId(at.id)) out.push(`//*[@id=${lit(at.id)}]`);
			for (const c of (at.class || "").split(/\s+/).filter((c) => c && !GENERIC.has(c) && !/\d/.test(c))) {
				out.push(`//${a.tag}[contains(concat(' ',normalize-space(@class),' '),' ${c} ')]`);
			}
			if (a.p < 0) break;
		}
		return out;
	}
	/** XPath anclado al contenedor estable más cercano que lo deja único; por posición como último recurso. */
	function relativeXPath(n, nodes, count, matches) {
		const base = robustXPath(n) || `//${n.tag}`;
		for (const anc of anchorsOf(n, nodes)) { const x = anc + base; if (count(x) === 1) return x; }
		const all = matches ? matches(base) : [];
		const k = all.indexOf(n.i);
		return k >= 0 && all.length > 1 ? `(${base})[${k + 1}]` : null;
	}
	function stableCSS(n, nodes) {
		const at = n.attrs || {};
		if (at["data-testid"]) return `[data-testid="${at["data-testid"]}"]`;
		if (at.id && !autoId(at.id)) return "#" + at.id;
		if (at.name) return `${n.tag}[name="${at.name}"]`;
		const own = (at.class || "").split(/\s+/).filter((c) => c && !GENERIC.has(c));
		const p = nodes[n.p], pc = (p?.attrs?.class || "").split(/\s+/).filter((c) => c && !GENERIC.has(c))[0];
		return (pc ? `${p.tag}.${pc} > ` : "") + n.tag + own.map((c) => "." + c).join("");
	}
	/** El mejor locator: un atributo estable si es único; si no, el XPath robusto o el relativo. */
	function best(n, nodes, count, matches) {
		const at = n.attrs || {}, css = stableCSS(n, nodes);
		if ((at["data-testid"] || (at.id && !autoId(at.id)) || at.name) && count(css) === 1) return { kind: "css", expr: css };
		const x = robustXPath(n);
		if (x && count(x) === 1) return { kind: "xpath", expr: x };
		const r = relativeXPath(n, nodes, count, matches);
		return r ? { kind: "xpath", expr: r } : { kind: "css", expr: css };
	}
	/** Nombre de constante: id o data-testid primero (no cambian con el texto); nunca empieza con dígito. */
	function constName(n) {
		const at = n.attrs || {};
		const base = fold(at["data-testid"] || at.id || labelOf(n)?.text || n.tag).normalize("NFD")
			.replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "").toUpperCase().slice(0, 40) || "ELEMENTO";
		return /^\d/.test(base) ? "ELEMENTO_" + base : base;
	}
	const interactive = (n) => ["button", "a", "input", "select", "textarea", "summary"].includes(n.tag) || /^(button|link|tab|menuitem|checkbox|option|radio)$/.test(n.attrs?.role || "");
	const dq = (s) => s.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
	const sq = (s) => s.replace(/\\/g, "\\\\").replace(/'/g, "\\'");
	/** Código listo para pegar: Selenium (Python, Java), Playwright (Python, JS), Page Object y espera. */
	function snippets(n, nodes, count, matches) {
		const b = best(n, nodes, count, matches), by = b.kind === "css" ? "CSS_SELECTOR" : "XPATH", byJ = b.kind === "css" ? "cssSelector" : "xpath";
		const at = n.attrs || {};
		const role = n.tag === "button" || at.role === "button" ? "button" : n.tag === "a" ? "link" : "";
		const txt = n.text && n.text !== "•••" ? n.text : "";
		const pwPy = at["data-testid"] ? `page.get_by_test_id("${dq(at["data-testid"])}")`
			: role && txt ? `page.get_by_role("${role}", name="${dq(txt)}")` : `page.locator("${b.kind === "xpath" ? "xpath=" : ""}${dq(b.expr)}")`;
		const pwJs = at["data-testid"] ? `page.getByTestId('${sq(at["data-testid"])}')`
			: role && txt ? `page.getByRole('${role}', { name: '${sq(txt)}' })` : `page.locator('${b.kind === "xpath" ? "xpath=" : ""}${sq(b.expr)}')`;
		const wait = interactive(n) ? "element_to_be_clickable" : "visibility_of_element_located";
		return [
			{ id: "sel-py", label: "Selenium · Python", code: `driver.find_element(By.${by}, "${dq(b.expr)}")` },
			{ id: "sel-java", label: "Selenium · Java", code: `driver.findElement(By.${byJ}("${dq(b.expr)}"))` },
			{ id: "pw-py", label: "Playwright · Python", code: pwPy },
			{ id: "pw-js", label: "Playwright · JS/TS", code: pwJs },
			{ id: "po", label: "Constante para Page Object (Python)", code: `${constName(n)} = (By.${by}, "${dq(b.expr)}")` },
			{ id: "wait", label: "Espera explícita (Selenium Python)", code: `WebDriverWait(driver, 15).until(EC.${wait}((By.${by}, "${dq(b.expr)}")))` },
		];
	}
	/** El elemento más parecido a un selector que ya no existe (por las palabras del selector). */
	// palabras de un selector que no dicen qué elemento es (las tiene media página)
	const NOISE = new Set(["xpath", "css", "contains", "translate", "normalize", "space", "text", "div", "span", "btn", "button", "input", "link", "form", "field", "item", "icon", "class", "role", "name", "get", "by"]);
	function similarTo(sel, nodes) {
		const want = fold(sel).split(/[^a-z0-9ñ]+/).filter((w) => w.length > 1 && !NOISE.has(w));
		let bestNode = null, bestScore = 0;
		for (const n of nodes) {
			if (!n.visible || !n.w) continue;
			const hay = fold([n.attrs?.id, n.attrs?.["data-testid"], n.attrs?.class, n.attrs?.name, n.attrs?.["aria-label"], n.attrs?.placeholder, n.text].filter(Boolean).join(" ")).split(/[^a-z0-9ñ]+/);
			const score = want.filter((w) => hay.includes(w)).length + (interactive(n) ? 0.5 : 0);
			if (score > bestScore) { bestScore = score; bestNode = n; }
		}
		return bestScore >= 1 ? bestNode : null;
	}
	/**
	 * Cada nodo con su estilo: el servidor guarda cada estilo distinto una vez (styles) y el nodo apunta al
	 * suyo (s). Un snapshot anterior trae el estilo en cada nodo y queda igual. Devuelve el mismo snapshot.
	 */
	function expandStyles(snap) {
		const table = Array.isArray(snap?.styles) ? snap.styles : null;
		if (table) for (const n of snap.nodes || []) if (!n.style && Number.isInteger(n.s) && table[n.s]) n.style = table[n.s];
		return snap;
	}
	/**
	 * Fidelidad: la parte de los píxeles de dos imágenes del mismo tamaño (RGBA, ya reducidas) cuyo color
	 * se parece (ningún canal difiere más de `tol`). A esa escala un texto es una mancha gris: lo que baja
	 * la cifra es lo que se ve distinto, como un bloque negro donde había barras grises.
	 */
	function fidelity(a, b, tol = 48) {
		const len = Math.min(a.data.length, b.data.length);
		let same = 0;
		for (let i = 0; i < len; i += 4) {
			const d = Math.max(Math.abs(a.data[i] - b.data[i]), Math.abs(a.data[i + 1] - b.data[i + 1]), Math.abs(a.data[i + 2] - b.data[i + 2]));
			if (d <= tol) same++;
		}
		return len ? same / (len / 4) : 0;
	}
	/** Bajo esta fidelidad el Inspector avisa que la reconstrucción no es fiable para ese caso. */
	const FIDELITY_WARN = 0.85;
	/** Un snapshot básico (solo elements, clientes anteriores) como nodos planos: el Inspector lo dibuja igual. */
	function fromElements(snap) {
		const vw = snap.viewport?.w || 1280, vh = snap.viewport?.h || 800;
		const nodes = [{ i: 0, p: -1, tag: "body", attrs: {}, text: "", x: 0, y: 0, w: vw, h: vh, visible: true, depth: 0, style: { backgroundColor: "rgb(255, 255, 255)" } }];
		for (const e of snap.elements || []) {
			const attrs = {};
			if (e.id) attrs.id = e.id;
			if (e.name) attrs.name = e.name;
			if (e.role) attrs.role = e.role;
			if (e.testid) attrs[e.testid_attr || "data-testid"] = e.testid;
			if (e.label) attrs["aria-label"] = e.label;
			if (e.placeholder) attrs.placeholder = e.placeholder;
			if (e.type) attrs.type = e.type;
			nodes.push({ i: nodes.length, p: 0, tag: e.tag || "div", attrs, text: e.text || "", x: e.x, y: e.y, w: e.w, h: e.h, visible: e.visible, depth: 1,
				style: { borderTopWidth: "1px", borderTopColor: "rgb(176, 190, 197)", color: "rgb(38, 50, 56)", fontSize: "13px", borderRadius: "4px" } });
		}
		return { ...snap, nodes, page: { w: vw, h: vh }, basic: true };
	}
	// ─── comparar con el último PASS (funciones puras) ─────────────────────────────────────────────
	/**
	 * Una clave por nodo que sobrevive entre dos ejecuciones: su id estable, test-id, name o aria-label; si
	 * no tiene, la de su padre más su tag y su posición entre los hermanos del mismo tag. El texto no
	 * forma parte de la clave: un texto distinto es un cambio del mismo elemento.
	 */
	function nodeKeys(nodes) {
		const keys = [], nth = new Map();
		for (const n of nodes) {
			const at = n.attrs || {};
			let k;
			if (n.p < 0) k = n.tag;
			else if (at.id && !autoId(at.id)) k = `${n.tag}#${at.id}`;
			else if (at["data-testid"]) k = `${n.tag}[testid=${at["data-testid"]}]`;
			else if (at.name) k = `${keys[n.p]}>${n.tag}[name=${at.name}]`;
			else if (at["aria-label"]) k = `${keys[n.p]}>${n.tag}[aria=${at["aria-label"]}]`;
			else {
				// con su primera clase: un div.skeleton nuevo en el lugar de un div.acciones no es "el mismo"
				const cls = (at.class || "").trim().split(/\s+/)[0] || "";
				const sib = `${n.p}:${n.tag}.${cls}`, c = (nth.get(sib) || 0) + 1;
				nth.set(sib, c);
				k = `${keys[n.p] ?? ""}>${n.tag}${cls ? "." + cls : ""}:${c}`;
			}
			keys[n.i] = k;
		}
		return keys;
	}
	const DIFF_ATTRS = ["id", "class", "name", "role", "type", "placeholder", "href", "title", "aria-label", "data-testid", "for", "alt"];
	/**
	 * Lo que cambió entre la página del último PASS (base) y la de ahora (cur): elementos nuevos, que ya no
	 * están (solo el de más arriba de cada rama, con cuántos tenía dentro) y cambiados (atributos, texto o
	 * visibilidad). Solo cuenta lo visible en alguna de las dos.
	 */
	function diffSnapshots(base, cur) {
		const kb = nodeKeys(base), kc = nodeKeys(cur);
		const byKey = new Map(), seen = new Set();
		base.forEach((n) => byKey.set(kb[n.i], n));
		const added = [], changed = [], addedSet = new Set();
		for (const n of cur) {
			const b = byKey.get(kc[n.i]);
			if (!b) {
				if (n.visible && !addedSet.has(n.p)) added.push(n.i);
				addedSet.add(n.i); // sus hijos van con él
				continue;
			}
			seen.add(kb[b.i]);
			if (!n.visible && !b.visible) continue;
			const fields = [];
			for (const a of DIFF_ATTRS) if ((n.attrs?.[a] || "") !== (b.attrs?.[a] || "")) fields.push({ what: a, before: b.attrs?.[a] || "", after: n.attrs?.[a] || "" });
			if ((n.text || "") !== (b.text || "")) fields.push({ what: "text", before: b.text || "", after: n.text || "" });
			if (!!n.visible !== !!b.visible) fields.push({ what: "visible", before: String(!!b.visible), after: String(!!n.visible) });
			if (fields.length) changed.push({ i: n.i, fields });
		}
		const removedSet = new Set(), removed = [];
		for (const b of base) {
			if (seen.has(kb[b.i])) continue;
			removedSet.add(b.i);
			if (b.visible && !removedSet.has(b.p)) removed.push({ ...b, inside: 0 });
		}
		// cuántos había dentro de cada rama que ya no está
		for (const r of removed) r.inside = base.filter((x) => x.i !== r.i && removedSet.has(x.i) && kb[x.i].startsWith(kb[r.i] + ">")).length;
		return { added, removed, changed };
	}
	/** Cómo se nombra un elemento en la lista de cambios: tag#id.clase «texto». */
	const nodeName = (n) => `${n.tag}${n.attrs?.id ? "#" + n.attrs.id : ""}${n.attrs?.class ? "." + n.attrs.class.trim().split(/\s+/).slice(0, 2).join(".") : ""}${n.text ? ` «${n.text.slice(0, 30)}»` : ""}`;

	// ─── cómo pintar la página (función pura) ───────────────────────────────────────────────────────
	const CLIPS = /hidden|auto|scroll|clip/;
	/**
	 * Cómo pinta el navegador cada nodo, ahora que en el Inspector van sueltos (posición absoluta, sin
	 * anidar): con zIndex = 10 + profundidad, un modal de Bootstrap (al final del body, poca profundidad)
	 * quedaba detrás de la página y su backdrop negro (opacity 0.5) salía negro sólido.
	 *
	 * - capa: como los stacking contexts reales. Cada nodo que abre uno (posicionado con z-index, fixed o
	 *   sticky, u opacity < 1) aporta [z-index, orden en el documento]; dentro de un contexto manda el
	 *   z-index y después el orden del documento (n.i es preorden: lo que va después queda encima). Así el
	 *   contenido del modal (1055) va sobre el backdrop (1050) y los dos sobre la página. Un snapshot viejo,
	 *   sin zIndex, queda en el orden del documento;
	 * - opacidad: la del nodo por la de sus ancestros (sus hijos ya no van dentro de él);
	 * - recorte: la caja de los ancestros con overflow hidden/auto/scroll (un historial largo no se sale de
	 *   su panel). Un position: fixed no se recorta.
	 * Devuelve, por n.i: rank (orden de pintado), opacity y clip ({x, y, w, h} o null).
	 */
	function paintPlan(nodes) {
		const key = [], ctx = [], opacity = [], clip = [], inner = [];
		for (const n of nodes) {
			const st = n.style || {}, p = n.p >= 0 ? n.p : -1;
			const pos = st.position && st.position !== "static";
			const zi = parseInt(st.zIndex, 10), z = pos && Number.isFinite(zi) ? zi : 0;
			const op = parseFloat(st.opacity);
			const own = Number.isFinite(op) ? op : 1;
			key[n.i] = [...(p >= 0 ? ctx[p] : []), [z, n.i]];
			const opens = (pos && Number.isFinite(zi)) || st.position === "fixed" || st.position === "sticky" || own < 1;
			ctx[n.i] = opens ? key[n.i] : (p >= 0 ? ctx[p] : []);
			opacity[n.i] = own * (p >= 0 ? opacity[p] : 1);
			let c = p >= 0 ? inner[p] : null;
			if (st.position === "fixed") c = null;
			clip[n.i] = c;
			const ov = `${st.overflowX || ""} ${st.overflowY || ""}`;
			inner[n.i] = CLIPS.test(ov) && n.w > 0 && n.h > 0 ? intersect(c, { x: n.x, y: n.y, w: n.w, h: n.h }) : c;
		}
		const cmp = (a, b) => {
			for (let k = 0; k < Math.min(a.length, b.length); k++) {
				if (a[k][0] !== b[k][0]) return a[k][0] - b[k][0];
				if (a[k][1] !== b[k][1]) return a[k][1] - b[k][1];
			}
			return a.length - b.length; // el que abre el contexto, debajo de lo que tiene adentro
		};
		const rank = [];
		nodes.map((n) => n.i).sort((a, b) => cmp(key[a], key[b])).forEach((i, r) => { rank[i] = r; });
		return { rank, opacity, clip };
	}
	function intersect(a, b) {
		if (!a) return b;
		const x = Math.max(a.x, b.x), y = Math.max(a.y, b.y);
		return { x, y, w: Math.max(0, Math.min(a.x + a.w, b.x + b.w) - x), h: Math.max(0, Math.min(a.y + a.h, b.y + b.h) - y) };
	}
	/** El recorte de un nodo como clip-path (relativo a su caja); "hidden" si queda entero afuera, "" si nada. */
	function clipInset(n, c) {
		if (!c) return "";
		const top = Math.max(0, c.y - n.y), left = Math.max(0, c.x - n.x);
		const right = Math.max(0, n.x + n.w - (c.x + c.w)), bottom = Math.max(0, n.y + n.h - (c.y + c.h));
		if (left + right >= n.w || top + bottom >= n.h) return "hidden";
		return top || left || right || bottom ? `inset(${top}px ${right}px ${bottom}px ${left}px)` : "";
	}

	/** Quita el prefijo de Playwright (xpath=, css=) para evaluar el selector sobre el snapshot. */
	const plainSelector = (s) => String(s || "").replace(/^(xpath|css)=/, "").trim();

	// ─── vista ────────────────────────────────────────────────────────────────────────────────────
	const AGENTS = ["#1f6feb", "#8250df", "#d4580a", "#0f8a7a", "#bf3989"];
	const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
	const transparent = (c) => !c || c === "transparent" || /rgba\(.*,\s*0\)$/.test(c);
	const ease = (p) => (p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2);
	const frame = () => new Promise((r) => requestAnimationFrame(r));
	const CANCEL = Symbol("cancel");
	const ARROW = (c) => `<svg viewBox="0 0 20 20" aria-hidden="true"><path d="M3 2 L3 16 L7 12 L10 18 L12.5 17 L9.6 11 L15 11 Z" fill="${c}" stroke="#fff" stroke-width="1.2" stroke-linejoin="round"/></svg>`;
	const PREF = "tracereports-inspector-instant";

	/**
	 * Monta el Inspector en root. opts: { snapshot (el del fallo o null), steps ([{seq,label,at}]),
	 * loadStep(seq) → Promise<snapshot>, failed ({selector, error}), screenshot (url), t (traducción) }.
	 */
	/** El texto que se ve: una ligadura de Material ("home") la dibuja su ícono, no la palabra. */
	const shownText = (n) => (n.icons || []).some((ic) => ic.pos === "text") ? "" : n.text || n.shown || (n.filled ? "•••" : "") || placeholderOf(n);
	/** El placeholder de un campo vacío: se ve en la página, en su color atenuado. Nunca lo escrito. */
	const placeholderOf = (n) => (n.tag === "input" || n.tag === "textarea") && !n.text && !n.shown && !n.filled ? n.attrs?.placeholder || "" : "";

	/**
	 * Dónde va cada ícono dentro de su caja (en px desde la esquina): solo el ícono (un <i class="fa">,
	 * una ligadura, una imagen) va centrado; ::before, a la izquierda del texto; ::after, a la derecha.
	 */
	function iconBox(n, ic) {
		const cy = Math.max(0, (n.h - ic.h) / 2);
		const alone = !shownText(n) || ic.pos === "text" || ic.pos === "img";
		if (alone) return { x: Math.max(0, (n.w - ic.w) / 2), y: cy };
		if (ic.pos === "before") return { x: Math.max(0, (n.tx ?? ic.w + 4) - ic.w - 4), y: cy };
		return { x: Math.max(0, n.w - ic.w - 2), y: cy };
	}

	/** Los íconos del nodo como imágenes (nunca HTML): los glifos del cliente y el <svg> ya limpio en el servidor. */
	function drawIcons(el, n, glyphs) {
		if (n.svg && n.tag === "svg") {
			const img = document.createElement("img");
			img.className = "insp-svg"; img.alt = ""; img.draggable = false;
			img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(n.svg);
			el.appendChild(img);
			el.classList.add("has-icon");
		}
		for (const ic of n.icons || []) {
			const src = glyphs[ic.g];
			if (!src || !/^data:image\/png;base64,/.test(src)) continue;
			const img = document.createElement("img"), at = iconBox(n, ic);
			img.className = "insp-g"; img.alt = ""; img.draggable = false; img.src = src;
			Object.assign(img.style, { left: at.x + "px", top: at.y + "px", width: ic.w + "px", height: ic.h + "px" });
			el.appendChild(img);
			el.classList.add("has-icon");
		}
	}

	function mount(root, opts) {
		const t = opts.t || ((s, v) => (v ? s.replace(/\{(\w+)\}/g, (m, k) => (k in v ? v[k] : m)) : s));
		let speed = 1, paused = false, runId = 0, current = null, mirror = null, els = [], testCursor = null, ctx = null;
		let curPage = -1, diffOn = false, diffReq = 0;
		const pages = [...(opts.steps || []).map((s) => ({ kind: s.kind === "warning" ? "warning" : "step", seq: s.seq, label: s.label })),
			...(opts.snapshot ? [{ kind: "fail", label: opts.failed?.selector ? t("Momento del fallo: {s}", { s: opts.failed.selector }) : t("Momento del fallo") }] : [])];
		let prefInstant = false;
		try { prefInstant = localStorage.getItem(PREF) === "1"; } catch { /* sin almacenamiento */ }
		if (matchMedia("(prefers-reduced-motion: reduce)").matches) prefInstant = true;

		root.innerHTML = `<div class="insp" data-no-i18n>
			<div class="insp-toolbar" role="toolbar" aria-label="${esc(t("Reproducción"))}">
				<button class="cf-btn cf-btn-sm cf-btn-primary" data-insp="play" type="button">▶ ${esc(t("Reconstruir"))}</button>
				<button class="cf-btn cf-btn-sm" data-insp="pause" type="button" aria-pressed="false">⏸ ${esc(t("Pausa"))}</button>
				<span class="seg insp-speed" role="group" aria-label="${esc(t("Velocidad"))}"><button type="button" data-speed="1" aria-pressed="true">1×</button><button type="button" data-speed="2" aria-pressed="false">2×</button><button type="button" data-speed="4" aria-pressed="false">4×</button></span>
				<button class="cf-btn cf-btn-sm" data-insp="instant" type="button">${esc(t("Ver al instante"))}</button>
				${opts.loadBaseline ? `<button class="cf-btn cf-btn-sm" data-insp="diff" type="button" aria-pressed="false">⇄ ${esc(t("Comparar con el último PASS"))}</button>`
					: opts.compareUnavailable ? `<button class="cf-btn cf-btn-sm" type="button" disabled title="${esc(opts.compareUnavailable)}" aria-describedby="insp-nocompare">⇄ ${esc(t("Comparar con el último PASS"))}</button><span class="m-hint insp-nocompare" id="insp-nocompare">${esc(opts.compareUnavailable)}</span>` : ""}
				${opts.screenshot ? `<label class="insp-ovl"><input type="checkbox" data-insp="ovl"> ${esc(t("Superponer captura"))} <input type="range" data-insp="ovlop" min="0" max="100" value="55" aria-label="${esc(t("Opacidad de la captura"))}"></label>` : ""}
				<span class="insp-status" aria-live="polite"></span>
				<span class="insp-fid" hidden title="${esc(t("Cuánto se parece la página reconstruida a la captura del momento del fallo"))}"></span>
			</div>
			${opts.notice ? `<p class="insp-notice" role="note">${esc(opts.notice)}</p>` : ""}
			${!pages.length ? `<p class="insp-notice" role="note">${esc(t("Este reporte no trae las páginas de este test."))}</p>` : ""}
			<div class="insp-main">
				<div class="insp-left">
					<div class="insp-viewport"><div class="insp-stage"><div class="insp-layer"></div>${opts.screenshot ? `<img class="insp-overlay" alt="${esc(t("Captura del momento del fallo"))}">` : ""}<div class="insp-cursors"></div></div></div>
					<p class="insp-notice insp-fidwarn" hidden></p>
					<p class="insp-basic m-hint" hidden>${esc(t("Snapshot básico (solo los elementos interactivos): con el cliente actualizado se ve la página completa."))}</p>
					<ol class="insp-steps" aria-label="${esc(t("Páginas del test"))}"></ol>
				</div>
				<aside class="insp-side">
					<div class="insp-card insp-diff" hidden><h6>${esc(t("Cambios desde el último PASS"))}</h6><div class="insp-diffbody"></div></div>
					<div class="insp-card insp-fail"><h6>${esc(t("Selector que falló"))}</h6><code class="insp-failsel"></code><div class="insp-failinfo m-hint">${esc(t("Se marca al terminar la reconstrucción."))}</div></div>
					<div class="insp-card"><h6>${esc(t("Probar XPath / CSS"))}</h6>
						<input type="text" class="insp-probe" spellcheck="false" placeholder="//button[normalize-space(.)='…']  ·  .clase button" aria-label="${esc(t("Expresión XPath o CSS"))}">
						<div class="insp-probeout m-hint">${esc(t("Se evalúa sobre la página reconstruida."))}</div></div>
					<div class="insp-card insp-el"><h6>${esc(t("Elemento"))}</h6><p class="m-hint">${esc(t("Clic en un elemento para ver sus atributos. Clic derecho: opciones de QA / Automation (XPath robusto y código listo para Selenium o Playwright)."))}</p></div>
				</aside>
			</div></div>`;
		const $ = (s) => root.querySelector(s);
		const stage = $(".insp-stage"), layer = $(".insp-layer"), cursors = $(".insp-cursors"), viewport = $(".insp-viewport");
		const bubble = document.createElement("div"); bubble.className = "insp-bubble"; bubble.style.opacity = 0;
		const tip = document.createElement("div"); tip.className = "insp-tip"; tip.dataset.noI18n = ""; tip.hidden = true; document.body.appendChild(tip);
		$(".insp-failsel").textContent = opts.failed?.selector || "—";

		// ---------- espejo del snapshot: un documento aparte para XPath y CSS ----------
		function buildMirror(nodes) {
			const doc = document.implementation.createHTMLDocument("snapshot");
			const m = [];
			for (const n of nodes) {
				let el;
				if (n.tag === "body") el = doc.body; else { try { el = doc.createElement(n.tag); } catch { el = doc.createElement("div"); } }
				for (const [k, v] of Object.entries(n.attrs || {})) { try { el.setAttribute(k, v); } catch { /* atributo inválido */ } }
				el.setAttribute("data-tr-i", n.i);
				if (n.text && n.tag !== "input") el.appendChild(doc.createTextNode(n.text));
				if (n.p >= 0 && m[n.p]) m[n.p].appendChild(el);
				m[n.i] = el;
			}
			return doc;
		}
		function query(expr) {
			expr = plainSelector(expr);
			if (!expr || !mirror) return { list: [] };
			try {
				if (/^(\/|\(|\.\/)/.test(expr)) {
					const r = mirror.evaluate(expr, mirror, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
					const list = [];
					for (let k = 0; k < r.snapshotLength; k++) { const x = r.snapshotItem(k); if (x.nodeType === 1 && x.hasAttribute("data-tr-i")) list.push(+x.getAttribute("data-tr-i")); }
					return { list, kind: "XPath" };
				}
				return { list: [...mirror.querySelectorAll(expr)].map((x) => +x.getAttribute("data-tr-i")), kind: "CSS" };
			} catch (e) { return { error: t("Expresión no válida: {e}", { e: String(e.message || e).split("\n")[0] }) }; }
		}
		const count = (e) => query(e).list?.length ?? 0;
		const matches = (e) => query(e).list || [];

		// ---------- escenario ----------
		function show(snap) {
			const s = snap.nodes?.length ? expandStyles(snap) : fromElements(snap);
			current = { snap: s, nodes: s.nodes, W: s.page?.w || s.viewport?.w || 1280, H: s.page?.h || s.viewport?.h || 800 };
			$(".insp-basic").hidden = !s.basic;
			mirror = buildMirror(current.nodes);
			layer.innerHTML = ""; cursors.innerHTML = ""; testCursor = null; els = [];
			stage.style.width = current.W + "px"; stage.style.height = current.H + "px";
			stage.style.background = current.nodes[0]?.style?.backgroundColor || "#fff";
			const plan = paintPlan(current.nodes); // capas, opacidad y recorte como en el navegador
			for (const n of current.nodes) {
				if (n.tag === "body" || !n.visible || !(n.w > 0) || !(n.h > 0)) continue;
				const clipped = clipInset(n, plan.clip[n.i]);
				if (clipped === "hidden") continue; // entero fuera de su panel con scroll: no se ve
				const el = document.createElement("div");
				el.className = "insp-n"; el.dataset.i = n.i;
				el.dataset.clip = clipped;
				const st = n.style || {}, fs = parseFloat(st.fontSize) || 14;
				// estilos del snapshot (limpios en el servidor) asignados propiedad por propiedad, nunca como HTML
				Object.assign(el.style, { left: n.x + "px", top: n.y + "px", width: n.w + "px", height: n.h + "px", zIndex: 10 + plan.rank[n.i],
					color: st.color || "", fontSize: st.fontSize || "", fontWeight: st.fontWeight || "", fontFamily: st.fontFamily || "",
					borderRadius: st.borderRadius || "", lineHeight: !st.lineHeight || st.lineHeight === "normal" ? "1.2" : st.lineHeight });
				// una caja girada (pestañas verticales con rotate): su tamaño de layout, centrado donde se ve, con el giro
				if (n.tf && n.lw > 0 && n.lh > 0) Object.assign(el.style, { left: n.x + (n.w - n.lw) / 2 + "px", top: n.y + (n.h - n.lh) / 2 + "px",
					width: n.lw + "px", height: n.lh + "px", transform: n.tf });
				if (st.writingMode && st.writingMode !== "horizontal-tb") el.style.writingMode = st.writingMode;
				if (plan.opacity[n.i] < 1) el.style.opacity = String(Math.round(plan.opacity[n.i] * 1000) / 1000);
				// un texto de varias líneas (un párrafo de un modal, "16:43<br>Compra en línea") se ajusta como en la página
				const lh = parseFloat(st.lineHeight) || fs * 1.2;
				// el alto de una línea se mide a lo ancho si el texto va de arriba abajo, y sin el giro de la caja
				const vertical = /^(vertical|sideways)/.test(st.writingMode || "");
				const across = n.tf ? (vertical ? n.lw : n.lh) : (vertical ? n.w : n.h);
				const oneLine = /^(nowrap|pre)$/.test(st.whiteSpace || "") || st.textOverflow === "ellipsis";
				if ((n.text || "").includes("\n") || (across > lh * 1.8 && !oneLine)) el.classList.add("wrap");
				if (clipped && !n.tf) el.style.clipPath = clipped; // el recorte va en coordenadas de la página
				if (!("borderBottomWidth" in st)) { // snapshot anterior: solo el borde de arriba, para los cuatro lados
					if (parseFloat(st.borderTopWidth) > 0) el.style.border = `${st.borderTopWidth} solid ${st.borderTopColor || "#999"}`;
				} else for (const side of ["Top", "Right", "Bottom", "Left"]) {
					const w = st[`border${side}Width`];
					if (parseFloat(w) > 0) el.style[`border${side}`] = `${w} solid ${st[`border${side}Color`] || "#999"}`;
				}
				if (/gradient\(/.test(st.backgroundImage || "")) el.style.backgroundImage = st.backgroundImage;
				if (st.boxShadow && st.boxShadow !== "none") el.style.setProperty("--pg-shadow", st.boxShadow);
				el.dataset.bg = transparent(st.backgroundColor) ? "" : st.backgroundColor;
				if (n.tag === "svg" || n.tag === "img" || n.tag === "canvas") el.classList.add("img");
				if (across <= fs * 2.4 || ["button", "a"].includes(n.tag)) el.classList.add("mid");
				if (n.tag === "button" || (!transparent(st.backgroundColor) && n.text && n.w < 220)) el.classList.add("center");
				const tx = document.createElement("span"); tx.className = "t";
				if (n.tx !== undefined && n.tx !== null) Object.assign(tx.style, { position: "absolute", left: n.tx + "px", top: (n.ty || 0) + "px" });
				if (["input", "select", "textarea"].includes(n.tag)) el.classList.add("ctl");
				if (placeholderOf(n)) { tx.classList.add("ph"); tx.style.color = n.style?.placeholderColor || ""; }
				if (st.textOverflow === "ellipsis") { // un texto largo cortado con «…» como en la página
					tx.classList.add("ellipsis");
					if (tx.style.position === "absolute") tx.style.maxWidth = Math.max(0, n.w - n.tx) + "px";
				}
				el.appendChild(tx);
				drawIcons(el, n, current.snap.glyphs || {});
				layer.appendChild(el);
				els[n.i] = el;
			}
			const ov = $(".insp-overlay");
			if (ov) {
				ov.src = opts.screenshot; ov.style.width = (current.snap.viewport?.w || current.W) + "px"; ov.hidden = snap !== opts.snapshot;
				Object.assign(ov.style, { left: (current.snap.scroll?.x || 0) + "px", top: (current.snap.scroll?.y || 0) + "px" }); // la parte de la página que muestra
			}
			if (snap === opts.snapshot && opts.screenshot && s.nodes?.length && !s.basic) measureSoon();
			fit();
			if (diffOn) applyDiff();
		}

		// ---------- comparar con el último PASS ----------
		/** El paso de esta página: el suyo, o para la del fallo, el último paso antes de fallar. */
		function pageLabel() {
			const p = pages[curPage];
			if (!p) return "";
			if (p.kind !== "fail") return p.label || "";
			return [...pages.slice(0, curPage)].reverse().find((x) => x.kind === "step")?.label || "";
		}
		function clearDiff() {
			cursors.querySelectorAll(".insp-diff-ghost").forEach((g) => g.remove());
			layer.querySelectorAll(".diff-added, .diff-changed").forEach((e) => e.classList.remove("diff-added", "diff-changed"));
		}
		async function applyDiff() {
			const req = ++diffReq, card = $(".insp-diff"), body = $(".insp-diffbody");
			card.hidden = false;
			body.textContent = t("Buscando el último PASS de este test…");
			let base;
			try { base = await opts.loadBaseline(pageLabel()); } catch { base = null; }
			if (req !== diffReq || !diffOn || !current) return;
			clearDiff();
			if (!base?.snapshot) {
				body.textContent = t("No hay un PASS anterior de este test con sus páginas guardadas. Con TRACEREPORTS_DOM_STEPS=1 cada ejecución guarda las páginas de sus pasos y se puede comparar.");
				return;
			}
			const bs = base.snapshot.nodes?.length ? expandStyles(base.snapshot) : fromElements(base.snapshot);
			const d = diffSnapshots(bs.nodes, current.nodes);
			d.added.forEach((i) => els[i]?.classList.add("diff-added"));
			d.changed.forEach((c) => els[c.i]?.classList.add("diff-changed"));
			for (const r of d.removed) {
				if (!(r.w > 0 && r.h > 0)) continue;
				const g = document.createElement("div"); g.className = "insp-diff-ghost";
				Object.assign(g.style, { left: r.x + "px", top: r.y + "px", width: r.w + "px", height: r.h + "px" });
				const l = document.createElement("span"); l.textContent = `${nodeName(r)} · ${t("ya no está")}`; g.appendChild(l);
				cursors.appendChild(g);
			}
			const when = base.started_at ? new Date(base.started_at).toLocaleString() : "";
			const item = (cls, label, text, i) => `<li class="${cls}"${i !== undefined ? ` data-diff-node="${i}" tabindex="0"` : ""}><b>${esc(label)}</b> <span>${esc(text)}</span></li>`;
			const list = [
				...d.removed.map((r) => item("rm", t("ya no está"), nodeName(r) + (r.inside ? ` (${t("{n} adentro", { n: r.inside })})` : ""))),
				...d.added.map((i) => item("add", t("nuevo"), nodeName(current.nodes[i]), i)),
				...d.changed.map((c) => item("chg", t("cambió"), `${nodeName(current.nodes[c.i])}: ${c.fields.map((f) => `${f.what} «${f.before.slice(0, 40)}» → «${f.after.slice(0, 40)}»`).join(", ")}`, c.i)),
			];
			body.innerHTML = `<p class="m-hint">${esc(t("Contra #{run} · {when}", { run: base.run_id, when }))}${base.same_step ? "" : ` · ${esc(t("otra página del test (no se encontró el mismo paso)"))}`}</p>
				<p class="insp-diffsum"><span class="rm">${esc(t("{n} ya no están", { n: d.removed.length }))}</span> · <span class="add">${esc(t("{n} nuevos", { n: d.added.length }))}</span> · <span class="chg">${esc(t("{n} cambiaron", { n: d.changed.length }))}</span></p>
				${list.length ? `<ul class="insp-difflist">${list.slice(0, 40).join("")}</ul>${list.length > 40 ? `<p class="m-hint">${esc(t("y {n} más", { n: list.length - 40 }))}</p>` : ""}` : `<p>${esc(t("Ningún cambio en lo visible: la página es la misma que en el último PASS."))}</p>`}`;
		}
		function fit() {
			if (!current) return;
			const scale = Math.min(1, viewport.clientWidth / current.W);
			stage.style.transform = `scale(${scale})`;
			viewport.style.height = Math.ceil(current.H * scale) + "px";
		}
		const ro = new ResizeObserver(fit); ro.observe(viewport);
		const drawable = () => current.nodes.filter((n) => els[n.i]);
		function hideAll() { for (const n of drawable()) { const el = els[n.i]; el.style.clipPath = "inset(0 100% 100% 0)"; el.style.backgroundColor = ""; el.firstChild.textContent = ""; } }
		function showAll() { for (const n of drawable()) { const el = els[n.i]; el.style.clipPath = el.dataset.clip || "none"; el.style.backgroundColor = el.dataset.bg; el.firstChild.textContent = shownText(n); el.classList.remove("drawing"); } }

		async function tween(ms, id, fn) {
			let tt = 0, last = performance.now();
			fn && fn(0);
			while (tt < ms) {
				await frame();
				if (id !== runId) throw CANCEL;
				const now = performance.now();
				if (!paused) tt += (now - last) * speed;
				last = now;
				fn && fn(Math.min(1, tt / ms));
			}
		}
		function makeCursor(color, label, cls = "") {
			const c = document.createElement("div");
			c.className = "insp-cur " + cls;
			c.innerHTML = ARROW(color) + `<b style="background:${color}">${esc(label)}</b>`;
			c.pos = { x: current.W / 2, y: current.H + 30 };
			c.style.transform = `translate(${c.pos.x}px, ${c.pos.y}px)`;
			cursors.appendChild(c);
			return c;
		}
		const place = (c, x, y) => { c.pos = { x, y }; c.style.transform = `translate(${x}px, ${y}px)`; };
		async function moveTo(c, x, y, ms, id) {
			const from = { ...c.pos };
			await tween(ms, id, (p) => { const e = ease(p); place(c, from.x + (x - from.x) * e, from.y + (y - from.y) * e); });
		}

		// ---------- fidelidad: la página reconstruida contra la captura ----------
		let fidReq = 0, fidDone = null;
		function measureSoon() {
			const req = ++fidReq;
			if (fidDone !== null) return showFidelity(fidDone);
			setTimeout(() => measure().then((f) => {
				if (req !== fidReq || f === null) return;
				fidDone = f; showFidelity(f);
				opts.onFidelity?.(f, current.snap.mode || "structure");
			}).catch(() => { /* sin cifra: la captura no se pudo leer */ }), 60);
		}
		function showFidelity(f) {
			const pct = Math.round(f * 100), badge = $(".insp-fid"), warn = $(".insp-fidwarn");
			if (!badge) return;
			badge.hidden = false; badge.textContent = t("Fidelidad: {n} %", { n: pct });
			badge.classList.toggle("low", f < FIDELITY_WARN);
			warn.hidden = f >= FIDELITY_WARN;
			warn.textContent = t("La reconstrucción difiere de la captura ({n} %): usa «Superponer captura» para ver la página real.", { n: pct });
		}
		const loadImg = (src) => new Promise((res, rej) => { const im = new Image(); im.onload = () => res(im); im.onerror = rej; im.src = src; });
		/** Las reglas de la hoja del Inspector que dibujan los nodos, para la copia que se rasteriza. */
		function nodeRules() {
			let css = "";
			for (const sh of document.styleSheets) {
				let rules;
				try { rules = sh.cssRules; } catch { continue; }
				for (const r of rules) {
					if (r.selectorText && /\.insp-(n|layer)\b/.test(r.selectorText) && !/hover|\.sel|match|similar|failed-el|diff|drawing|caret/.test(r.selectorText)) css += r.cssText + "\n";
				}
			}
			return css;
		}
		/** Dibuja la parte de la página que muestra la captura y la compara con ella, a 240 px de ancho. */
		async function measure() {
			const s = current.snap, vw = s.viewport?.w || current.W, vh = s.viewport?.h || current.H, sx = s.scroll?.x || 0, sy = s.scroll?.y || 0;
			const W = 240, H = Math.max(1, Math.round((W * vh) / vw));
			const canvas = () => {
				const c = document.createElement("canvas"); c.width = W; c.height = H;
				const g = c.getContext("2d", { willReadFrequently: true }); g.fillStyle = "#fff"; g.fillRect(0, 0, W, H);
				return g;
			};
			const shot = await loadImg(opts.screenshot), k = shot.naturalWidth / vw;
			const ga = canvas();
			if (shot.naturalHeight / k > vh * 1.15) ga.drawImage(shot, sx * k, sy * k, vw * k, Math.min(vh * k, shot.naturalHeight - sy * k), 0, 0, W, H); // captura de la página entera
			else ga.drawImage(shot, 0, 0, W, H);
			// la reconstrucción como queda al terminar de armarse, sin la animación ni las marcas del Inspector
			const copy = layer.cloneNode(true);
			for (const el of copy.querySelectorAll(".insp-n")) {
				const n = current.nodes[+el.dataset.i];
				el.classList.remove("hover", "sel", "match", "similar", "failed-el", "diff-added", "diff-changed", "drawing");
				el.style.clipPath = el.dataset.clip || ""; el.style.backgroundColor = el.dataset.bg || "";
				const tx = el.querySelector(".t");
				if (tx) tx.textContent = shownText(n);
			}
			copy.querySelectorAll("img").forEach((i) => i.remove());
			const bg = current.nodes[0]?.style?.backgroundColor || "#fff";
			const style = document.createElement("style"); style.textContent = nodeRules();
			const wrap = document.createElement("div");
			wrap.setAttribute("style", `position:relative;width:${vw}px;height:${vh}px;overflow:hidden;color:#222;font-family:Arial,sans-serif`);
			wrap.style.background = bg;
			const page = document.createElement("div");
			page.setAttribute("style", `position:absolute;left:${-sx}px;top:${-sy}px;width:${current.W}px;height:${current.H}px`);
			page.appendChild(copy); wrap.append(style, page);
			const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${vw}" height="${vh}"><foreignObject width="100%" height="100%">${new XMLSerializer().serializeToString(wrap)}</foreignObject></svg>`;
			const gb = canvas();
			gb.drawImage(await loadImg("data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg)), 0, 0, W, H);
			// los íconos y los SVG son imágenes: dentro del foreignObject no se cargan, se dibujan encima
			const f = W / vw;
			for (const img of layer.querySelectorAll(".insp-n img")) {
				const n = current.nodes[+img.closest(".insp-n").dataset.i];
				const box = img.classList.contains("insp-svg") ? { x: 0, y: 0, w: n.w, h: n.h }
					: { x: parseFloat(img.style.left) || 0, y: parseFloat(img.style.top) || 0, w: parseFloat(img.style.width) || 0, h: parseFloat(img.style.height) || 0 };
				if (!(box.w > 0 && box.h > 0)) continue;
				try { gb.drawImage(await loadImg(img.src), (n.x + box.x - sx) * f, (n.y + box.y - sy) * f, box.w * f, box.h * f); } catch { /* sin ese ícono */ }
			}
			return fidelity(ga.getImageData(0, 0, W, H), gb.getImageData(0, 0, W, H));
		}

		// ---------- el enjambre arma la página ----------
		async function construct(id) {
			const order = drawable().sort((a, b) => a.depth - b.depth || a.y - b.y || a.x - b.x);
			const done = new Map(), total = order.length, status = $(".insp-status");
			let built = 0;
			order.forEach((n) => { let r; const pr = new Promise((res) => (r = res)); done.set(n.i, { pr, r }); });
			const queue = [...order];
			// con muchos nodos, más cursores y pasos más cortos: la página se arma en segundos
			const k = total > 600 ? 3.5 : total > 200 ? 2 : 1;
			const agents = AGENTS.map((color, j) => ({ color, c: makeCursor(color, "#" + (j + 1)) }));
			status.textContent = t("Reconstruyendo {n} de {t}", { n: 0, t: total });
			await Promise.all(agents.map(async (a) => {
				while (queue.length) {
					const n = queue.shift();
					const parent = done.get(n.p);
					if (parent) await parent.pr; // un hijo se dibuja cuando su contenedor ya existe
					const el = els[n.i];
					el.style.setProperty("--c", a.color);
					await moveTo(a.c, n.x, n.y, 240 / k, id);
					el.classList.add("drawing");
					await tween(Math.min(520, 150 + (n.w + n.h) / 6) / k, id, (p) => {
						const e = ease(p);
						el.style.clipPath = `inset(0 ${100 - e * 100}% ${100 - e * 100}% 0)`;
						place(a.c, n.x + n.w * e, n.y + n.h * e);
					});
					el.style.clipPath = el.dataset.clip || "none"; // el recorte de su panel con scroll, si tiene
					el.style.backgroundColor = el.dataset.bg;
					const shown = shownText(n);
					if (shown) {
						const tx = el.firstChild, typed = document.createTextNode(""), caret = document.createElement("span");
						caret.className = "caret"; tx.textContent = ""; tx.append(typed, caret);
						const per = Math.max(8, Math.min(34, 650 / shown.length)) / k;
						await tween(shown.length * per, id, (p) => { typed.data = shown.slice(0, Math.round(p * shown.length)); });
						tx.textContent = shown;
					}
					el.classList.remove("drawing");
					built++;
					status.textContent = t("Reconstruyendo {n} de {t}", { n: built, t: total });
					done.get(n.i).r();
				}
				await moveTo(a.c, a.c.pos.x, current.H + 40, 350, id);
				a.c.remove();
			}));
			status.textContent = t("{n} elementos reconstruidos", { n: total });
		}

		// ---------- el recorrido del test: sus páginas y el fallo ----------
		function say(text, n, fail) {
			if (!bubble.isConnected) cursors.appendChild(bubble);
			bubble.textContent = text; bubble.className = "insp-bubble" + (fail ? " fail" : ""); bubble.style.opacity = 1;
			const bw = Math.min(360, bubble.offsetWidth || 260), W = current.W;
			if (!n) { bubble.style.left = "16px"; bubble.style.top = "16px"; return; }
			const right = n.x + n.w + 22;
			bubble.style.left = (right + bw < W ? right : Math.max(4, n.x - bw - 22)) + "px";
			bubble.style.top = Math.max(4, n.y + n.h / 2 - 16) + "px";
		}
		function renderSteps(active = -1, failed = false) {
			$(".insp-steps").innerHTML = pages.map((p, k) => {
				const cls = (k < active ? "done" : k === active ? (failed && p.kind === "fail" ? "failed" : "active") : "") + (p.kind === "warning" ? " warn" : "");
				const tag = p.kind === "fail" ? esc(t("FALLÓ")) : p.kind === "warning" ? "⚠ WARNING" : "";
				return `<li class="${cls}" data-page="${k}" tabindex="0"><span class="num">${k + 1}</span><span>${esc(p.label || t("Paso {n}", { n: k + 1 }))}</span><small>${tag}</small></li>`;
			}).join("");
		}
		async function pageSnap(p) { return p.kind === "fail" ? opts.snapshot : await opts.loadStep(p.seq); }
		function markFailure(instant) {
			cursors.querySelectorAll(".insp-ghost, .insp-simlabel").forEach((g) => g.remove());
			layer.querySelectorAll(".similar, .failed-el").forEach((e) => e.classList.remove("similar", "failed-el"));
			const sel = opts.failed?.selector;
			// el backend falló justo antes: el elemento no apareció por eso, no por el selector
			const bk = opts.backend, cause = bk ? t(bk.status ? "El backend respondió {s} justo antes del fallo ({c}): no es un problema del selector."
				: "El backend no respondió justo antes del fallo ({c}): no es un problema del selector.", { s: bk.status, c: `${bk.method} ${bk.endpoint}` }) : "";
			if (!sel) { $(".insp-failinfo").textContent = cause || t("No se pudo extraer el selector del error."); return; }
			const hit = query(sel);
			if (!testCursor) testCursor = makeCursor("#0f1b2b", "test", "test");
			if (hit.list?.length) { // existía en la página: el fallo fue de otra cosa (oculto, tapado, a destiempo)
				const n = current.nodes[hit.list[0]];
				els[n.i]?.classList.add("failed-el");
				// oculto (caja 0×0): se apunta al contenedor visible más cercano, donde debería aparecer
				let at = n;
				while (at && !(at.visible && at.w > 0 && at.h > 0) && at.p >= 0) at = current.nodes[at.p];
				if (at !== n) els[at.i]?.classList.add("failed-el");
				place(testCursor, at.x + at.w / 2, at.y + at.h / 2);
				say(t("{s} existe en la página: el fallo no fue el selector", { s: sel }), at, true);
				$(".insp-failinfo").textContent = (cause ? cause + " " : "") + (n.visible ? t("El elemento existe y es visible en el momento del fallo: revisa si estaba tapado, deshabilitado o si el test fue antes de tiempo.")
					: t("El elemento existe pero no es visible en el momento del fallo."));
				select(n.i);
				return;
			}
			const sim = cause ? null : similarTo(sel, current.nodes); // con el backend caído, un "parecido" despistaría
			if (!sim) { // nada que se le parezca: solo el aviso, sin marcar un elemento cualquiera
				place(testCursor, 24, 24);
				say(t("{s} no existe en la página", { s: sel }), null, true);
				$(".insp-failinfo").textContent = cause || t("No existe en el momento del fallo y no hay un elemento parecido: puede ser un fallo de flujo o de contenido.");
				return;
			}
			const n = sim;
			place(testCursor, n.x + n.w / 2, n.y + n.h / 2);
			say(t("{s} no existe en la página", { s: sel }), n, true);
			const g = document.createElement("div"); g.className = "insp-ghost";
			Object.assign(g.style, { left: n.x - 6 + "px", top: n.y - 6 + "px", width: n.w + 12 + "px", height: n.h + 12 + "px" });
			const gl = document.createElement("span"); gl.textContent = `${sel} · ${t("no está")}`; g.appendChild(gl);
			cursors.appendChild(g);
			const b = best(sim, current.nodes, count, matches);
			els[sim.i]?.classList.add("similar");
			const lab = document.createElement("div"); lab.className = "insp-simlabel";
			Object.assign(lab.style, { left: sim.x + "px", top: sim.y + sim.h + 30 + "px" });
			lab.textContent = t("Parecido: {s}", { s: b.expr });
			cursors.appendChild(lab);
			$(".insp-failinfo").innerHTML = `${esc(t("No existe en el momento del fallo. El elemento más parecido:"))}<div class="insp-cands">${candHTML(b.expr)}</div>`;
			select(sim.i);
		}
		async function playAll(instant) {
			const id = ++runId; paused = false; setPaused(false);
			if (!pages.length) return;
			try {
				for (let k = 0; k < pages.length; k++) {
					const p = pages[k];
					renderSteps(k);
					const snap = await pageSnap(p);
					if (id !== runId) return;
					if (!snap) continue;
					curPage = k;
					show(snap);
					if (k === 0 && !instant) { hideAll(); await construct(id); } else { showAll(); counted(); }
					if (p.kind === "fail") { markFailure(instant); renderSteps(k, true); break; }
					if (!testCursor) testCursor = makeCursor("#0f1b2b", "test", "test");
					say(p.label || t("Paso {n}", { n: k + 1 }), null);
					if (!instant) await tween(1400, id);
				}
			} catch (e) { if (e !== CANCEL) throw e; }
		}
		/** Los elementos de la página que se está viendo (cambia con cada paso). */
		function counted() { $(".insp-status").textContent = t("{n} elementos reconstruidos", { n: drawable().length }); }
		async function jump(k) {
			runId++;
			const p = pages[k], snap = await pageSnap(p);
			if (!snap) return;
			curPage = k;
			show(snap); showAll(); counted(); renderSteps(k, p.kind === "fail");
			if (p.kind === "fail") markFailure(true); else { testCursor = makeCursor("#0f1b2b", "test", "test"); say(p.label || t("Paso {n}", { n: k + 1 }), null); }
		}

		// ---------- inspeccionar, probar y el menú de QA / Automation ----------
		const candHTML = (expr) => { const k = count(expr); return `<div class="insp-cand"><code>${esc(expr)}</code><span class="u ${k === 1 ? "ok" : "no"}">${k === 1 ? esc(t("único ✓")) : esc(t("{n} coinciden", { n: k }))}</span></div>`; };
		function pathOf(n) { const p = []; for (let x = n; x; x = current.nodes[x.p]) { p.unshift(x.tag + (x.attrs?.id ? "#" + x.attrs.id : "")); if (x.p < 0) break; } return p.join(" › "); }
		function select(i) {
			layer.querySelectorAll(".insp-n.sel").forEach((e) => e.classList.remove("sel"));
			const n = current.nodes[i];
			if (!n) return;
			els[i]?.classList.add("sel");
			const attrs = Object.entries(n.attrs || {}).map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join("");
			const cands = [best(n, current.nodes, count, matches).expr, robustXPath(n)].filter((x, j, a) => x && a.indexOf(x) === j);
			$(".insp-el").innerHTML = `<h6>${esc(t("Elemento"))}</h6><div class="insp-path">${esc(pathOf(n))}</div>
				<dl class="insp-kv"><dt>tag</dt><dd>${esc(n.tag)}</dd>${attrs}<dt>${esc(t("texto"))}</dt><dd>${esc(n.text || "—")}</dd><dt>${esc(t("caja"))}</dt><dd>${n.x}, ${n.y} · ${n.w}×${n.h}</dd></dl>
				<h6>${esc(t("Selectores candidatos"))}</h6><div class="insp-cands">${cands.map(candHTML).join("")}</div>
				<p class="m-hint">${esc(t("Clic derecho sobre el elemento: XPath robusto y código listo para Selenium o Playwright."))}</p>`;
		}
		function toast(msg) { const x = document.createElement("div"); x.className = "insp-toast"; x.dataset.noI18n = ""; x.setAttribute("role", "status"); x.textContent = msg; document.body.appendChild(x); setTimeout(() => x.remove(), 1600); }
		async function copy(text, what) {
			try { await navigator.clipboard.writeText(text); toast(t("{w} copiado", { w: what })); }
			catch {
				const ta = document.createElement("textarea"); ta.value = text; document.body.appendChild(ta); ta.select();
				try { document.execCommand("copy"); toast(t("{w} copiado", { w: what })); } catch { toast(t("No se pudo copiar")); }
				ta.remove();
			}
		}
		let openedAt = 0;
		function closeMenu() { ctx?.remove(); ctx = null; }
		// al abrir, el detalle del elemento cambia de alto y el navegador puede ajustar el scroll (scroll
		// anchoring): eso no es la persona desplazándose, no cierra el menú
		const onScroll = () => { if (ctx && performance.now() - openedAt > 300) closeMenu(); };
		/** El cambio para el test: el selector que falló y el nuevo, con el código listo para pegar. */
		function replaceWith(n, expr) {
			const card = $(".insp-fail");
			card.querySelector(".insp-replace")?.remove();
			const box = document.createElement("div"); box.className = "insp-replace";
			const lines = snippets(n, current.nodes, count, matches).filter((s) => ["sel-py", "pw-py", "pw-js", "sel-java"].includes(s.id));
			box.innerHTML = `<b>${esc(t("Reemplazo"))}</b><span>${esc(t("Antes"))}: <code>${esc(opts.failed.selector)}</code></span><span>${esc(t("Después"))}: <code>${esc(expr)}</code></span>
				${lines.map((s) => `<button class="cf-btn cf-btn-sm" type="button" data-copy="${esc(s.code)}" data-what="${esc(t(s.label))}">${esc(t("Copiar"))} ${esc(t(s.label))}</button>`).join("")}`;
			box.addEventListener("click", (e) => { const b = e.target.closest("[data-copy]"); if (b) copy(b.dataset.copy, b.dataset.what); });
			card.appendChild(box);
			copy(expr, t("Reemplazo"));
		}
		function openMenu(n, x, y) {
			closeMenu(); select(n.i);
			const nodes = current.nodes;
			const badge = (expr) => { const k = count(expr); return `<span class="u ${k === 1 ? "ok" : "no"}">${k === 1 ? esc(t("único ✓")) : k === 0 ? esc(t("sin coincidencias")) : esc(t("{n} coinciden", { n: k }))}</span>`; };
			const item = (label, code, act, withBadge = true) => `<button class="it" type="button" role="menuitem" data-act="${act}" data-code="${esc(code)}"><span class="lbl">${esc(label)}</span>${withBadge ? badge(code) : "<span></span>"}<code>${esc(code)}</code></button>`;
			const rx = robustXPath(n), rel = relativeXPath(n, nodes, count, matches), css = stableCSS(n, nodes), b = best(n, nodes, count, matches);
			ctx = document.createElement("div"); ctx.className = "insp-ctx"; ctx.dataset.noI18n = ""; ctx.setAttribute("role", "menu");
			ctx.innerHTML = `<header><b>QA / Automation</b><span>${esc(n.tag)}${n.text ? " «" + esc(n.text.slice(0, 40)) + "»" : ""}</span></header>
				<div class="grp">${esc(t("Locators robustos"))}</div>
				${rx ? item(t("XPath robusto (sin mayúsculas ni tildes)"), rx, "copy") : ""}
				${rel && rel !== rx ? item(t("XPath relativo a un contenedor estable"), rel, "copy") : ""}
				${item(t("CSS estable"), css, "copy")}
				<div class="grp">${esc(t("Copiar como"))}</div>
				${snippets(n, nodes, count, matches).map((s) => item(t(s.label), s.code, "copy", false)).join("")}
				<div class="grp">${esc(t("Acciones"))}</div>
				${item(t("Probar en el probador"), b.expr, "probe", false)}
				${opts.failed?.selector ? item(t("Usar como reemplazo del selector que falló"), b.expr, "replace") : ""}`;
			document.body.appendChild(ctx);
			openedAt = performance.now();
			const r = ctx.getBoundingClientRect();
			ctx.style.left = Math.max(8, Math.min(x, innerWidth - r.width - 8)) + "px";
			ctx.style.top = Math.max(8, Math.min(y, innerHeight - r.height - 8)) + "px";
			ctx.querySelector("button.it")?.focus();
			ctx.addEventListener("click", (e) => {
				const it = e.target.closest("button.it"); if (!it) return;
				const code = it.dataset.code;
				if (it.dataset.act === "copy") copy(code, it.querySelector(".lbl").textContent);
				if (it.dataset.act === "probe") { const pr = $(".insp-probe"); pr.value = code; pr.dispatchEvent(new Event("input")); pr.focus(); }
				if (it.dataset.act === "replace") replaceWith(n, code);
				closeMenu();
			});
			ctx.addEventListener("keydown", (e) => {
				const items = [...ctx.querySelectorAll("button.it")], k = items.indexOf(document.activeElement);
				if (e.key === "ArrowDown") { e.preventDefault(); items[(k + 1) % items.length].focus(); }
				if (e.key === "ArrowUp") { e.preventDefault(); items[(k - 1 + items.length) % items.length].focus(); }
				if (e.key === "Escape") closeMenu();
			});
		}

		// ---------- eventos ----------
		const nodeAt = (e) => { const el = e.target.closest(".insp-n"); return el ? current.nodes[+el.dataset.i] : null; };
		layer.addEventListener("mousemove", (e) => {
			const el = e.target.closest(".insp-n");
			layer.querySelectorAll(".insp-n.hover").forEach((x) => x !== el && x.classList.remove("hover"));
			if (!el) { tip.hidden = true; return; }
			el.classList.add("hover");
			const n = current.nodes[+el.dataset.i];
			tip.textContent = `${n.tag}${n.attrs?.id ? "#" + n.attrs.id : ""}${n.attrs?.class ? "." + n.attrs.class.trim().split(/\s+/).join(".") : ""} · ${n.w}×${n.h}`;
			tip.hidden = false; tip.style.left = e.clientX + 14 + "px"; tip.style.top = e.clientY + 14 + "px";
		});
		layer.addEventListener("mouseleave", () => { tip.hidden = true; layer.querySelectorAll(".insp-n.hover").forEach((x) => x.classList.remove("hover")); });
		layer.addEventListener("click", (e) => { const n = nodeAt(e); if (n) select(n.i); });
		layer.addEventListener("contextmenu", (e) => { const n = nodeAt(e); if (!n) return; e.preventDefault(); tip.hidden = true; openMenu(n, e.clientX, e.clientY); });
		const outside = (e) => { if (ctx && !ctx.contains(e.target)) closeMenu(); };
		const onKey = (e) => { if (e.key === "Escape") closeMenu(); };
		document.addEventListener("click", outside);
		addEventListener("keydown", onKey);
		addEventListener("scroll", onScroll, true);
		$(".insp-probe").addEventListener("input", (e) => {
			layer.querySelectorAll(".insp-n.match").forEach((x) => x.classList.remove("match"));
			const out = $(".insp-probeout"), v = e.target.value;
			if (!v.trim()) { out.className = "insp-probeout m-hint"; out.textContent = t("Se evalúa sobre la página reconstruida."); return; }
			const r = query(v);
			if (r.error) { out.className = "insp-probeout count zero"; out.textContent = r.error; return; }
			r.list.forEach((i) => els[i]?.classList.add("match"));
			const k = r.list.length;
			out.className = "insp-probeout";
			out.innerHTML = `<span class="count ${k === 0 ? "zero" : k === 1 ? "one" : "many"}">${k === 0 ? esc(t("Ninguna coincidencia")) : k === 1 ? esc(t("1 coincidencia: único")) : esc(t("{n} coincidencias", { n: k }))}</span> <span class="m-hint">(${r.kind})</span>`;
		});
		function setPaused(v) { paused = v; const b = $('[data-insp="pause"]'); b.setAttribute("aria-pressed", String(v)); b.textContent = v ? `▶ ${t("Seguir")}` : `⏸ ${t("Pausa")}`; }
		root.addEventListener("click", (e) => {
			const a = e.target.closest("[data-insp]");
			if (a?.dataset.insp === "play") { try { localStorage.setItem(PREF, "0"); } catch { /* */ } playAll(false); }
			if (a?.dataset.insp === "instant") { try { localStorage.setItem(PREF, "1"); } catch { /* */ } playAll(true); }
			if (a?.dataset.insp === "pause") setPaused(!paused);
			if (a?.dataset.insp === "diff") {
				diffOn = !diffOn;
				a.setAttribute("aria-pressed", String(diffOn));
				if (diffOn) applyDiff(); else { diffReq++; clearDiff(); $(".insp-diff").hidden = true; }
			}
			const dn = e.target.closest("[data-diff-node]");
			if (dn) select(+dn.dataset.diffNode);
			const sp = e.target.closest("[data-speed]");
			if (sp) { speed = +sp.dataset.speed; root.querySelectorAll("[data-speed]").forEach((x) => x.setAttribute("aria-pressed", String(x === sp))); }
			const li = e.target.closest(".insp-steps li");
			if (li) jump(+li.dataset.page);
		});
		root.addEventListener("keydown", (e) => { const li = e.target.closest(".insp-steps li"); if (li && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); jump(+li.dataset.page); } });
		const ovl = () => { const o = $(".insp-overlay"); if (o) o.style.opacity = $('[data-insp="ovl"]').checked ? $('[data-insp="ovlop"]').value / 100 : 0; };
		$('[data-insp="ovl"]')?.addEventListener("change", ovl);
		$('[data-insp="ovlop"]')?.addEventListener("input", () => { $('[data-insp="ovl"]').checked = true; ovl(); });

		renderSteps(-1);
		playAll(prefInstant);
		return {
			destroy() { runId++; ro.disconnect(); tip.remove(); closeMenu(); document.removeEventListener("click", outside); removeEventListener("keydown", onKey); removeEventListener("scroll", onScroll, true); },
			query, select,
		};
	}

	window.TraceReportsInspector = { fold, lit, labelOf, robustXPath, anchorsOf, relativeXPath, stableCSS, best, constName, snippets, similarTo, fromElements, expandStyles, fidelity, plainSelector, nodeKeys, diffSnapshots, nodeName, paintPlan, clipInset, shownText, iconBox, drawIcons, mount };
})();
