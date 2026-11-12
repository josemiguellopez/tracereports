"""
Snapshot de la página: la usan el "AI Locator Recommender" y el Inspector.

Hay dos niveles (``TRACEREPORTS_DOM``):

- ``structure`` (por defecto): la página entera como árbol, con la caja, el texto propio y los
  estilos mínimos de cada elemento. Con eso el Inspector la redibuja y permite probar XPath o CSS
  sobre el momento del fallo sin volver a reproducirlo. Incluye además la lista de elementos de
  ``basic``, que usa el recomendador de selectores.
- ``basic``: solo los elementos con los que se suele interactuar (inputs, botones, links, elementos
  con rol, test-id, id o name), como hasta ahora.

Lo que se tapa en las capturas (``input[type=password]``, ``[data-tracereports-mask]`` y
``TRACEREPORTS_SCREENSHOT_MASK``) también se tapa aquí, y lo que alguien escribió en un campo nunca
viaja. Con ``TRACEREPORTS_DOM_STEPS=1`` también se guarda la página de cada paso con captura (ver
TraceReports.attach_screenshot), para recorrer el test entero en el Inspector.

    from tracereports import capturar_dom
    cr.attach_dom(capturar_dom(page))      # al fallar, ANTES de cr.end_test()
"""

import hashlib
import json
import time

from ._env import env
from .mask import mask_selector
from .network import _mask_sensitive

# El texto de un elemento como se ve, pero con lo tapado de sus hijos como "•••": el innerText de un
# contenedor (una ficha con id) incluye el texto de todo lo que tiene adentro, también lo enmascarado.
_SHOWN = """
  const shown = (el, mask) => {
    let covers = false;
    try { covers = !!(mask && el.querySelector(mask)); } catch (e) { covers = false; }
    if (!covers) return el.innerText || '';
    let out = '';
    const walk = (n) => {
      for (const c of n.childNodes) {
        if (c.nodeType === 3) out += c.textContent + ' ';
        else if (c.nodeType === 1 && !['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE'].includes(c.tagName)) {
          let m = false;
          try { m = c.matches(mask); } catch (e) { m = false; }
          if (m) out += ' ••• '; else walk(c);
        }
      }
    };
    walk(el);
    return out;
  };"""

# Corre en el navegador (Playwright: page.evaluate). Recibe el selector de lo que se tapa. Máximo 1500 elementos.
DOM_SCRIPT = """(mask) => {""" + _SHOWN + """
  const sel = 'a,button,input,select,textarea,label,summary,[role],[data-testid],[data-test-id],[data-test],[id],[name],[aria-label],[placeholder],h1,h2,h3,h4,h5,h6';
  const out = [];
  for (const el of document.querySelectorAll(sel)) {
    if (out.length >= 1500) break;
    const r = el.getBoundingClientRect();
    const st = getComputedStyle(el);
    const visible = r.width > 0 && r.height > 0 && st.visibility !== 'hidden' && st.display !== 'none' && st.opacity !== '0';
    let covered = false;
    try { covered = !!(mask && el.closest(mask)); } catch (e) { covered = false; }
    const testAttr = ['data-testid', 'data-test-id', 'data-test'].find((a) => el.hasAttribute(a)) || '';
    const label = covered ? '' : (el.getAttribute('aria-label') || (el.labels && el.labels[0] ? shown(el.labels[0], mask) : '') || '');
    let text = (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') ? '' : shown(el, mask).trim().replace(/\\s+/g, ' ');
    if (covered && text) text = '•••';
    out.push({
      tag: el.tagName.toLowerCase(), id: el.id || '', name: el.getAttribute('name') || '',
      role: el.getAttribute('role') || '', testid: testAttr ? el.getAttribute(testAttr) : '', testid_attr: testAttr,
      label: label.trim().slice(0, 120), placeholder: covered ? '' : (el.getAttribute('placeholder') || '').slice(0, 120),
      type: el.getAttribute('type') || '', text: text.slice(0, 120),
      x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), visible,
    });
  }
  return { url: location.href, title: document.title, viewport: { w: innerWidth, h: innerHeight }, elements: out };
}"""

# El snapshot estructural. Recibe el selector de lo que se tapa (mask.py). Máximo 5000 nodos.
STRUCT_SCRIPT = r"""(mask) => {""" + _SHOWN + r"""
  const KEEP = ['id', 'class', 'name', 'role', 'type', 'placeholder', 'href', 'title', 'for', 'alt', 'data-testid', 'data-test', 'data-test-id'];
  const STYLE = ['display', 'position', 'backgroundColor', 'color', 'fontSize', 'fontWeight', 'fontFamily', 'borderTopWidth',
    'borderTopColor', 'borderRadius', 'opacity', 'textAlign', 'lineHeight', 'zIndex', 'overflowX', 'overflowY'];
  const SEL = 'a,button,input,select,textarea,label,summary,[role],[data-testid],[data-test-id],[data-test],[id],[name],[aria-label],[placeholder],h1,h2,h3,h4,h5,h6';
  const SKIP = new Set(['script', 'style', 'noscript', 'template', 'link', 'meta', 'head']);
  const nodes = [], elements = [];
  const hidden = (el) => { try { return !!(mask && el.closest(mask)); } catch (e) { return false; } };
  const sx = window.scrollX, sy = window.scrollY;
  // Los íconos, como se ven: un ícono de fuente (Font Awesome, Material: un carácter en ::before o una
  // ligadura) se dibuja aquí en un canvas con la fuente que la página ya cargó y viaja como un PNG chico,
  // una sola vez por snapshot (glyphs). Sin la fuente el Inspector no podría dibujarlo, y la fuente misma
  // (con su licencia) nunca sale de la página. Lo mismo para las imágenes chicas (íconos PNG).
  const glyphs = {}, gkeys = new Map();
  let gbytes = 0;
  const cv = document.createElement('canvas');
  const store = (key, w, h, draw) => {
    if (gkeys.has(key)) return gkeys.get(key);
    if (gkeys.size >= 250 || gbytes > 1500000 || !(w >= 1) || !(h >= 1) || w > 256 || h > 256) return null;
    try {
      cv.width = Math.ceil(w * 2); cv.height = Math.ceil(h * 2);
      const c = cv.getContext('2d'); c.setTransform(2, 0, 0, 2, 0, 0); c.clearRect(0, 0, w, h);
      draw(c);
      const url = cv.toDataURL('image/png');
      if (url.length > 24000) return null;
      const g = { id: 'g' + gkeys.size, w: Math.round(w), h: Math.round(h) };
      gkeys.set(key, g); glyphs[g.id] = url; gbytes += url.length;
      return g;
    } catch (e) { return null; } // una imagen de otro origen "ensucia" el canvas: queda la caja
  };
  const glyph = (txt, cs) => {
    const fs = parseFloat(cs.fontSize) || 14, font = cs.fontStyle + ' ' + cs.fontWeight + ' ' + fs + 'px ' + cs.fontFamily;
    const ctx = cv.getContext('2d'); ctx.font = font;
    const w = Math.ceil(ctx.measureText(txt).width), h = Math.ceil(fs * 1.25);
    return store(txt + '|' + font + '|' + cs.color, w, h, (c) => { c.font = font; c.fillStyle = cs.color; c.textBaseline = 'middle'; c.fillText(txt, 0, h / 2); });
  };
  const unq = (v) => { const m = /^["']([\s\S]*)["']$/.exec(v || ''); return m ? m[1].replace(/\\([0-9a-fA-F]{1,6})\s?/g, (x, hx) => String.fromCodePoint(parseInt(hx, 16))) : null; };
  const ICON_FONT = /material (icons|symbols)/i;
  const PAINT = ['fill', 'stroke', 'stroke-width', 'opacity', 'fill-opacity', 'stroke-opacity', 'fill-rule', 'stroke-linecap', 'stroke-linejoin'];
  // lo que no se pinta: definiciones, paradas de un gradiente y animaciones (en <animate>, fill es freeze/remove)
  const NO_PAINT = /^(defs|clippath|mask|lineargradient|radialgradient|stop|pattern|symbol|title|desc|metadata|animate|animatetransform|animatemotion|set|mpath)$/i;
  const REFS = ['fill', 'stroke', 'clip-path', 'mask', 'filter', 'marker-start', 'marker-mid', 'marker-end'];
  const URL_REF = /url\(\s*["']?[^"'()\s#]*#([^"'()\s]+)["']?\s*\)/g;
  // un <svg> en línea, con sus colores resueltos (el CSS de la página no viaja), sus <use href="#…"> adentro
  // y los gradientes, recortes y máscaras a los que apunta, como url(#id) dentro del mismo <svg>
  const svgOf = (el, r) => {
    const all = [el, ...el.querySelectorAll('*')];
    if (all.length > 250) return '';
    const clone = el.cloneNode(true), copy = [clone, ...clone.querySelectorAll('*')];
    all.forEach((o, i) => { const k = copy[i], s = getComputedStyle(o); if (k && !NO_PAINT.test(o.localName)) for (const p of PAINT) { const v = s.getPropertyValue(p); if (v) k.setAttribute(p, v); } });
    for (const u of clone.querySelectorAll('use')) {
      const ref = u.getAttribute('href') || u.getAttribute('xlink:href') || '';
      const src = ref.startsWith('#') ? document.getElementById(ref.slice(1)) : null;
      if (!src) continue;
      if (src.getAttribute('viewBox') && !clone.getAttribute('viewBox')) clone.setAttribute('viewBox', src.getAttribute('viewBox'));
      const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
      for (const p of PAINT) if (u.getAttribute(p)) g.setAttribute(p, u.getAttribute(p));
      for (const c of src.childNodes) g.appendChild(c.cloneNode(true));
      u.replaceWith(g);
    }
    clone.querySelectorAll('animate,animateTransform,animateMotion,set,script,foreignObject').forEach((a) => a.remove());
    // url("https://host/página#grad") (algunas librerías la arman así por <base href>) → url(#grad), y lo que
    // vive en otro <svg> de la página (un sprite oculto con los gradientes) se copia a un <defs> propio
    const has = (id) => { try { return !!clone.querySelector('#' + CSS.escape(id)); } catch (e) { return false; } };
    let defs = null, pulled = 0;
    const pull = (id) => {
      if (has(id)) return true;
      const src = document.getElementById(id);
      if (!src || !(src instanceof SVGElement) || src.contains(el) || ++pulled > 20) return false;
      if (!defs) { defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs'); clone.insertBefore(defs, clone.firstChild); }
      defs.appendChild(src.cloneNode(true));
      return true;
    };
    for (const k of [clone, ...clone.querySelectorAll('*')]) {
      for (const a of REFS) {
        const v = k.getAttribute(a);
        if (v && v.includes('url(')) k.setAttribute(a, v.replace(URL_REF, (m, id) => (pull(id) ? 'url(#' + id + ')' : m)));
      }
      for (const a of ['href', 'xlink:href']) {
        const v = k.getAttribute(a);
        if (v && /^[^#]+#./.test(v) && v.split('#')[0] === location.href.split('#')[0]) k.setAttribute(a, '#' + v.split('#')[1]);
      }
    }
    clone.setAttribute('width', Math.round(r.width)); clone.setAttribute('height', Math.round(r.height));
    // como imagen, "currentColor" sería negro: el color que tiene el <svg> en la página
    const s = new XMLSerializer().serializeToString(clone).replace(/currentColor/g, getComputedStyle(el).color);
    return s.length <= 12000 ? s : '';
  };
  const walk = (el, parent, depth) => {
    if (nodes.length >= 5000 || depth > 60) return;
    const tag = el.tagName.toLowerCase();
    if (SKIP.has(tag)) return;
    const r = el.getBoundingClientRect(), st = getComputedStyle(el);
    const visible = r.width > 0 && r.height > 0 && st.visibility !== 'hidden' && st.display !== 'none' && st.opacity !== '0';
    const covered = hidden(el);
    const attrs = {};
    for (const a of KEEP) if (el.hasAttribute(a)) attrs[a] = String(el.getAttribute(a)).slice(0, 200);
    for (const a of el.getAttributeNames()) if (a.startsWith('aria-') && Object.keys(attrs).length < 30) attrs[a] = String(el.getAttribute(a)).slice(0, 200);
    let text = '', tb = null;
    for (const c of el.childNodes) {
      if (c.nodeType === 1 && c.tagName === 'BR') { if (text) text += '\n'; continue; } // "16:43<br>Control" son dos líneas
      if (c.nodeType !== 3 || !c.textContent.trim()) continue;
      if (text && !/\s$/.test(text)) text += ' ';
      text += c.textContent;
      const rg = document.createRange(); rg.selectNodeContents(c);
      const b = rg.getBoundingClientRect();
      if (b.width) tb = tb ? { x: Math.min(tb.x, b.x), y: Math.min(tb.y, b.y) } : { x: b.x, y: b.y };
    }
    text = text.replace(/[^\S\n]+/g, ' ').replace(/ *\n[\s]*/g, '\n').trim().slice(0, 300);
    if (tag === 'input' || tag === 'textarea' || tag === 'select') text = ''; // lo que alguien escribió no viaja
    if (covered) { text = text ? '•••' : ''; delete attrs.placeholder; delete attrs.title; delete attrs['aria-label']; }
    const style = {};
    for (const k of STYLE) style[k] = st[k];
    const node = { i: nodes.length, p: parent, tag, attrs, text, x: Math.round(r.x + sx), y: Math.round(r.y + sy),
      w: Math.round(r.width), h: Math.round(r.height), visible, depth, style };
    if (tb) { node.tx = Math.round(tb.x - r.x); node.ty = Math.round(tb.y - r.y); }
    if (visible && !covered) {
      const icons = [];
      // ::before / ::after con uno a tres caracteres: el ícono de fuente (o una marca como "*")
      for (const pos of ['before', 'after']) {
        const ps = getComputedStyle(el, '::' + pos), txt = unq(ps.content);
        if (!txt || !txt.trim() || [...txt].length > 3 || ps.display === 'none' || ps.visibility === 'hidden') continue;
        const g = glyph(txt, ps);
        if (g) icons.push({ g: g.id, pos, w: g.w, h: g.h });
      }
      // Material Icons / Symbols: la palabra ("home") es una ligadura que la fuente dibuja como ícono
      if (text && !el.children.length && text.length <= 40 && ICON_FONT.test(st.fontFamily)) {
        const g = glyph(text, st);
        if (g) icons.push({ g: g.id, pos: 'text', w: g.w, h: g.h });
      }
      // una imagen chica (un ícono PNG): se copia si es del mismo origen
      if (tag === 'img' && el.complete && el.naturalWidth && r.width <= 64 && r.height <= 64) {
        const g = store('img|' + el.currentSrc + '|' + Math.round(r.width) + 'x' + Math.round(r.height), r.width, r.height, (c) => c.drawImage(el, 0, 0, r.width, r.height));
        if (g) icons.push({ g: g.id, pos: 'img', w: g.w, h: g.h });
      }
      if (icons.length) node.icons = icons.slice(0, 4);
      if (tag === 'svg') { try { const s = svgOf(el, r); if (s) node.svg = s; } catch (e) { /* queda la caja */ } }
    }
    nodes.push(node);
    if (elements.length < 1500 && el.matches(SEL)) {
      const testAttr = ['data-testid', 'data-test-id', 'data-test'].find((a) => el.hasAttribute(a)) || '';
      const label = covered ? '' : (el.getAttribute('aria-label') || (el.labels && el.labels[0] ? shown(el.labels[0], mask) : '') || '');
      const inner = (tag === 'input' || tag === 'textarea') ? '' : shown(el, mask).trim().replace(/\s+/g, ' ');
      elements.push({ tag, id: el.id || '', name: el.getAttribute('name') || '', role: el.getAttribute('role') || '',
        testid: testAttr ? el.getAttribute(testAttr) : '', testid_attr: testAttr, label: label.trim().slice(0, 120),
        placeholder: covered ? '' : (el.getAttribute('placeholder') || '').slice(0, 120), type: el.getAttribute('type') || '',
        text: covered && inner ? '•••' : inner.slice(0, 120),
        x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), visible });
    }
    if (tag === 'svg') return; // el dibujo viaja en node.svg; sus partes no son nodos
    for (const c of el.children) walk(c, node.i, depth + 1);
    if (el.shadowRoot) for (const c of el.shadowRoot.children) walk(c, node.i, depth + 1);
  };
  walk(document.body, -1, 0);
  const doc = document.documentElement;
  const out = { url: location.href, title: document.title, viewport: { w: innerWidth, h: innerHeight },
    page: { w: Math.max(doc.scrollWidth, innerWidth), h: Math.max(doc.scrollHeight, innerHeight) }, nodes, elements };
  if (gkeys.size) out.glyphs = glyphs;
  return out;
}"""


def dom_level() -> str:
    """TRACEREPORTS_DOM: structure (por defecto) o basic."""
    return "basic" if (env("DOM") or "").strip().lower() == "basic" else "structure"


def dom_steps_mode() -> str:
    """TRACEREPORTS_DOM_STEPS: qué tests guardan la página de cada paso con captura.

    - ``1`` (``all``): todos, también los PASS (son la base de "Comparar con el último PASS").
    - ``on-problem``: solo los que terminan en FAIL o tuvieron un WARNING; las páginas esperan en memoria
      hasta que el test termina. De los que pasan se manda una base de comparación cuando el servidor no
      tiene un PASS de ese test con páginas de los últimos 7 días.
    - vacío u otro valor: ninguno (la página del fallo se guarda igual).
    ``TRACEREPORTS_DOM_ON`` no limita esto: solo agrega la página de cada WARNING."""
    v = (env("DOM_STEPS") or "").strip().lower()
    if v in ("1", "true", "yes", "on", "all"):
        return "all"
    if v in ("on-problem", "on_problem", "problem"):
        return "on-problem"
    return "off"


def dom_steps_enabled() -> bool:
    """TRACEREPORTS_DOM_STEPS: ``1`` u ``on-problem`` (ver dom_steps_mode)."""
    return dom_steps_mode() != "off"


def dom_on() -> "set[str]":
    """TRACEREPORTS_DOM_ON: cuándo se guarda la página además de los pasos. ``fail`` (siempre) y,
    con ``fail,warning``, también en cada paso WARNING, que muchas veces anticipa el fallo."""
    values = {v.strip().lower() for v in (env("DOM_ON") or "fail").split(",") if v.strip()}
    return values | {"fail"}


def _tag(snap, kind, label):
    if isinstance(snap, dict):
        snap["kind"] = kind
        snap["at"] = int(time.time() * 1000)
        if label:
            snap["label"] = str(label)[:300]
    return snap


def capturar_dom(page, kind: str = "fail", label: str = ""):
    """Snapshot de la página (con lo sensible tapado), o None si la página ya no está disponible.
    ``kind="step"`` y ``label`` marcan la página de un paso. La evidencia nunca rompe el test."""
    try:
        if dom_level() == "basic":
            snap = page.evaluate(DOM_SCRIPT, mask_selector())
        else:
            snap = page.evaluate(STRUCT_SCRIPT, mask_selector())
    except Exception:
        return None
    return _tag(_mask_dom(snap), kind, label)


def _mask_dom(snap):
    """Enmascara los textos del snapshot (también lo usa la captura de Selenium)."""
    if not isinstance(snap, dict):
        return None
    for el in snap.get("elements", []):
        for key in ("text", "label", "placeholder"):
            el[key] = _mask_sensitive(el.get(key) or "")
    for n in snap.get("nodes", []):
        if n.get("text"):
            n["text"] = _mask_sensitive(n["text"])
        attrs = n.get("attrs") or {}
        for key, value in list(attrs.items()):
            attrs[key] = _mask_sensitive(value or "")
    snap["url"] = _mask_sensitive(snap.get("url") or "")
    return snap


def dom_signature(snap) -> str:
    """Huella de lo que muestra la página: dos pasos con la misma pantalla no se guardan dos veces."""
    nodes = snap.get("nodes") or snap.get("elements") or []
    shape = [snap.get("url", "")] + [[n.get("tag"), n.get("text", ""), n.get("x"), n.get("y"), n.get("w"), n.get("h"), n.get("visible")] for n in nodes]
    return hashlib.sha1(json.dumps(shape, ensure_ascii=False).encode()).hexdigest()
