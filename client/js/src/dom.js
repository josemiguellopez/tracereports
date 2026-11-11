// Snapshot de la página al fallar un test: entrada del "AI Locator Recommender". No es el HTML
// completo, sino los elementos con los que se suele interactuar, con atributos y posición en
// pantalla. El mismo script que usan los clientes Python y Go.
//
// Lo que se tapa en las capturas (input[type=password], [data-tracereports-mask],
// TRACEREPORTS_SCREENSHOT_MASK) también se tapa aquí: el elemento sale con "•••" y el texto de un
// contenedor (una ficha con id) no lleva el de sus hijos tapados.

/**
 * Función que corre en el navegador (máximo 1500 elementos). `mask` es el grupo de selectores a tapar
 * (mask.js → maskSelector()). Todo lo que usa va adentro: Playwright y Selenium la envían como texto.
 */
export function domSnapshotInPage(mask) {
  const shown = (el) => {
    let covers = false;
    try { covers = !!(mask && el.querySelector(mask)); } catch { covers = false; }
    if (!covers) return el.innerText || "";
    let out = "";
    const walk = (n) => {
      for (const c of n.childNodes) {
        if (c.nodeType === 3) out += c.textContent + " ";
        else if (c.nodeType === 1 && !["SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE"].includes(c.tagName)) {
          let m = false;
          try { m = c.matches(mask); } catch { m = false; }
          if (m) out += " ••• "; else walk(c);
        }
      }
    };
    walk(el);
    return out;
  };
  const sel = "a,button,input,select,textarea,label,summary,[role],[data-testid],[data-test-id],[data-test],[id],[name],[aria-label],[placeholder],h1,h2,h3,h4,h5,h6";
  const out = [];
  for (const el of document.querySelectorAll(sel)) {
    if (out.length >= 1500) break;
    const r = el.getBoundingClientRect();
    const st = getComputedStyle(el);
    const visible = r.width > 0 && r.height > 0 && st.visibility !== "hidden" && st.display !== "none" && st.opacity !== "0";
    let covered = false;
    try { covered = !!(mask && el.closest(mask)); } catch { covered = false; }
    const testAttr = ["data-testid", "data-test-id", "data-test"].find((a) => el.hasAttribute(a)) || "";
    const label = covered ? "" : (el.getAttribute("aria-label") || (el.labels && el.labels[0] ? shown(el.labels[0]) : "") || "");
    let text = el.tagName === "INPUT" || el.tagName === "TEXTAREA" ? "" : shown(el).trim().replace(/\s+/g, " ");
    if (covered && text) text = "•••";
    out.push({
      tag: el.tagName.toLowerCase(), id: el.id || "", name: el.getAttribute("name") || "",
      role: el.getAttribute("role") || "", testid: testAttr ? el.getAttribute(testAttr) : "", testid_attr: testAttr,
      label: label.trim().slice(0, 120), placeholder: covered ? "" : (el.getAttribute("placeholder") || "").slice(0, 120),
      type: el.getAttribute("type") || "", text: text.slice(0, 120),
      x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), visible,
    });
  }
  return { url: location.href, title: document.title, viewport: { w: innerWidth, h: innerHeight }, elements: out };
}

/** Script como texto, para Selenium (`driver.executeScript(DOM_SCRIPT, maskSelector())`) y otros drivers. */
export const DOM_SCRIPT = `return (${domSnapshotInPage.toString()})(arguments[0]);`;
