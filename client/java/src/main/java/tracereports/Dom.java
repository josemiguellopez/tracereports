package tracereports;

/**
 * Snapshot de la página al fallar (entrada del recomendador de locators): los elementos con los
 * que se suele interactuar, con atributos y posición. El mismo script que usan los demás clientes.
 *
 * <p>Lo que se tapa en las capturas ({@code input[type=password]}, {@code [data-tracereports-mask]},
 * {@code TRACEREPORTS_SCREENSHOT_MASK}; ver {@link Mask#selector()}) también se tapa aquí: el elemento
 * sale con "•••" y el texto de un contenedor (una ficha con id) no lleva el de sus hijos tapados.
 */
public final class Dom {
    private Dom() {}

    /** Función para Playwright: {@code page.evaluate(Dom.FUNCTION, Mask.selector())}. */
    public static final String FUNCTION = """
            (mask) => {
              const shown = (el) => {
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
                      if (m) out += ' \\u2022\\u2022\\u2022 '; else walk(c);
                    }
                  }
                };
                walk(el);
                return out;
              };
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
                const label = covered ? '' : (el.getAttribute('aria-label') || (el.labels && el.labels[0] ? shown(el.labels[0]) : '') || '');
                let text = (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') ? '' : shown(el).trim().replace(/\\s+/g, ' ');
                if (covered && text) text = '\\u2022\\u2022\\u2022';
                out.push({
                  tag: el.tagName.toLowerCase(), id: el.id || '', name: el.getAttribute('name') || '',
                  role: el.getAttribute('role') || '', testid: testAttr ? el.getAttribute(testAttr) : '', testid_attr: testAttr,
                  label: label.trim().slice(0, 120), placeholder: covered ? '' : (el.getAttribute('placeholder') || '').slice(0, 120),
                  type: el.getAttribute('type') || '', text: text.slice(0, 120),
                  x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), visible,
                });
              }
              return { url: location.href, title: document.title, viewport: { w: innerWidth, h: innerHeight }, elements: out };
            }""";

    /** Script para Selenium: {@code executeScript(Dom.SCRIPT, Mask.selector())}. */
    public static final String SCRIPT = "return (" + FUNCTION + ")(arguments[0]);";
}
