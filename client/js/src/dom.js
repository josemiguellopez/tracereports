// Snapshot de la página al fallar un test: entrada del "AI Locator Recommender". No es el HTML
// completo, sino los elementos con los que se suele interactuar, con atributos y posición en
// pantalla. El mismo script que usan los clientes Python y Go.

/** Función que corre en el navegador (máximo 1500 elementos). */
export function domSnapshotInPage() {
  const sel = "a,button,input,select,textarea,label,summary,[role],[data-testid],[data-test-id],[data-test],[id],[name],[aria-label],[placeholder],h1,h2,h3,h4,h5,h6";
  const out = [];
  for (const el of document.querySelectorAll(sel)) {
    if (out.length >= 1500) break;
    const r = el.getBoundingClientRect();
    const st = getComputedStyle(el);
    const visible = r.width > 0 && r.height > 0 && st.visibility !== "hidden" && st.display !== "none" && st.opacity !== "0";
    const testAttr = ["data-testid", "data-test-id", "data-test"].find((a) => el.hasAttribute(a)) || "";
    const label = el.getAttribute("aria-label") || (el.labels && el.labels[0] ? el.labels[0].innerText : "") || "";
    const text = el.tagName === "INPUT" || el.tagName === "TEXTAREA" ? "" : (el.innerText || "").trim().replace(/\s+/g, " ");
    out.push({
      tag: el.tagName.toLowerCase(), id: el.id || "", name: el.getAttribute("name") || "",
      role: el.getAttribute("role") || "", testid: testAttr ? el.getAttribute(testAttr) : "", testid_attr: testAttr,
      label: label.trim().slice(0, 120), placeholder: (el.getAttribute("placeholder") || "").slice(0, 120),
      type: el.getAttribute("type") || "", text: text.slice(0, 120),
      x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), visible,
    });
  }
  return { url: location.href, title: document.title, viewport: { w: innerWidth, h: innerHeight }, elements: out };
}

/** Script como texto, para Selenium (`driver.executeScript`) y otros drivers. */
export const DOM_SCRIPT = `return (${domSnapshotInPage.toString()})();`;
