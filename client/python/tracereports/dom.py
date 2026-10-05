"""
Snapshot de la página al fallar un test: entrada del "AI Locator Recommender".

No se guarda el HTML completo, sino los elementos con los que se suele interactuar (inputs,
botones, links, elementos con rol, test-id, id o name) con sus atributos y su posición en
pantalla. Con eso el servidor propone selectores robustos para reemplazar el que se rompió y
la UI dibuja el recuadro del elemento sobre la captura del fallo.

    from tracereports import capturar_dom
    cr.attach_dom(capturar_dom(page))      # al fallar, ANTES de cr.end_test()
"""

from .network import _mask_sensitive

# Corre en el navegador (Playwright: page.evaluate). Máximo 1500 elementos.
DOM_SCRIPT = """() => {
  const sel = 'a,button,input,select,textarea,label,summary,[role],[data-testid],[data-test-id],[data-test],[id],[name],[aria-label],[placeholder],h1,h2,h3,h4,h5,h6';
  const out = [];
  for (const el of document.querySelectorAll(sel)) {
    if (out.length >= 1500) break;
    const r = el.getBoundingClientRect();
    const st = getComputedStyle(el);
    const visible = r.width > 0 && r.height > 0 && st.visibility !== 'hidden' && st.display !== 'none' && st.opacity !== '0';
    const testAttr = ['data-testid', 'data-test-id', 'data-test'].find((a) => el.hasAttribute(a)) || '';
    const label = el.getAttribute('aria-label') || (el.labels && el.labels[0] ? el.labels[0].innerText : '') || '';
    const text = (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') ? '' : (el.innerText || '').trim().replace(/\\s+/g, ' ');
    out.push({
      tag: el.tagName.toLowerCase(), id: el.id || '', name: el.getAttribute('name') || '',
      role: el.getAttribute('role') || '', testid: testAttr ? el.getAttribute(testAttr) : '', testid_attr: testAttr,
      label: label.trim().slice(0, 120), placeholder: (el.getAttribute('placeholder') || '').slice(0, 120),
      type: el.getAttribute('type') || '', text: text.slice(0, 120),
      x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), visible,
    });
  }
  return { url: location.href, title: document.title, viewport: { w: innerWidth, h: innerHeight }, elements: out };
}"""


def capturar_dom(page):
    """Snapshot de los elementos de la página (con datos sensibles enmascarados), o None si
    la página ya no está disponible. La evidencia nunca rompe el test."""
    try:
        snap = page.evaluate(DOM_SCRIPT)
    except Exception:
        return None
    for el in snap.get("elements", []):
        for key in ("text", "label", "placeholder"):
            el[key] = _mask_sensitive(el.get(key) or "")
    snap["url"] = _mask_sensitive(snap.get("url") or "")
    return snap
