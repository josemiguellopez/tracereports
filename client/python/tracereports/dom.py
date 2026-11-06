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
    'borderTopColor', 'borderRadius', 'opacity', 'textAlign', 'lineHeight'];
  const SEL = 'a,button,input,select,textarea,label,summary,[role],[data-testid],[data-test-id],[data-test],[id],[name],[aria-label],[placeholder],h1,h2,h3,h4,h5,h6';
  const SKIP = new Set(['script', 'style', 'noscript', 'template', 'link', 'meta', 'head']);
  const nodes = [], elements = [];
  const hidden = (el) => { try { return !!(mask && el.closest(mask)); } catch (e) { return false; } };
  const sx = window.scrollX, sy = window.scrollY;
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
      if (c.nodeType !== 3 || !c.textContent.trim()) continue;
      text += c.textContent;
      const rg = document.createRange(); rg.selectNodeContents(c);
      const b = rg.getBoundingClientRect();
      if (b.width) tb = tb ? { x: Math.min(tb.x, b.x), y: Math.min(tb.y, b.y) } : { x: b.x, y: b.y };
    }
    text = text.replace(/\s+/g, ' ').trim().slice(0, 300);
    if (tag === 'input' || tag === 'textarea' || tag === 'select') text = ''; // lo que alguien escribió no viaja
    if (covered) { text = text ? '•••' : ''; delete attrs.placeholder; delete attrs.title; delete attrs['aria-label']; }
    const style = {};
    for (const k of STYLE) style[k] = st[k];
    const node = { i: nodes.length, p: parent, tag, attrs, text, x: Math.round(r.x + sx), y: Math.round(r.y + sy),
      w: Math.round(r.width), h: Math.round(r.height), visible, depth, style };
    if (tb) { node.tx = Math.round(tb.x - r.x); node.ty = Math.round(tb.y - r.y); }
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
    if (tag === 'svg') return; // el dibujo no se copia: una caja basta (y la captura lo muestra)
    for (const c of el.children) walk(c, node.i, depth + 1);
    if (el.shadowRoot) for (const c of el.shadowRoot.children) walk(c, node.i, depth + 1);
  };
  walk(document.body, -1, 0);
  const doc = document.documentElement;
  return { url: location.href, title: document.title, viewport: { w: innerWidth, h: innerHeight },
    page: { w: Math.max(doc.scrollWidth, innerWidth), h: Math.max(doc.scrollHeight, innerHeight) }, nodes, elements };
}"""


def dom_level() -> str:
    """TRACEREPORTS_DOM: structure (por defecto) o basic."""
    return "basic" if (env("DOM") or "").strip().lower() == "basic" else "structure"


def dom_steps_enabled() -> bool:
    """TRACEREPORTS_DOM_STEPS=1: guardar también la página de cada paso con captura."""
    return (env("DOM_STEPS") or "").strip().lower() in ("1", "true", "yes", "on")


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
