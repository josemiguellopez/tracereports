"""
Capturas sin datos sensibles: se tapan antes de capturar.

- ``TRACEREPORTS_SCREENSHOT_MASK``: selectores CSS a tapar (un grupo CSS: ``"input[name=rut], .tarjeta"``).
- Siempre se tapan ``input[type=password]`` y los elementos con el atributo ``data-tracereports-mask``;
  ``TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off`` lo desactiva.
- ``TRACEREPORTS_SCREENSHOTS``: ``all`` (por defecto), ``failures`` (solo la captura de un paso FAIL) u
  ``off`` (ninguna). El servidor aplica la misma política por su cuenta.

Playwright usa su opción nativa ``mask``. Selenium no la tiene: se ponen recuadros del mismo color
sobre los elementos, se captura y se quitan, también si la captura falla, así el DOM queda igual.
"""

from __future__ import annotations

import logging
from typing import Any, Optional

from ._env import env

log = logging.getLogger("tracereports")

#: Lo que se tapa siempre (salvo TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off).
DEFAULT_MASK = "input[type=password], [data-tracereports-mask]"
#: Color de los recuadros: el mismo que usa Playwright por defecto.
MASK_COLOR = "#FF00FF"

_OVERLAY_ATTR = "data-tracereports-mask-overlay"


def mask_selector() -> str:
    """El grupo de selectores CSS a tapar ("" = nada)."""
    parts = []
    if (env("SCREENSHOT_MASK_DEFAULTS") or "").strip().lower() not in ("off", "0", "false", "no"):
        parts.append(DEFAULT_MASK)
    extra = (env("SCREENSHOT_MASK") or "").strip().strip(",")
    if extra:
        parts.append(extra)
    return ", ".join(parts)


def screenshots_policy() -> str:
    """TRACEREPORTS_SCREENSHOTS: all, failures u off (un valor desconocido cuenta como all)."""
    value = (env("SCREENSHOTS") or "").strip().lower()
    return value if value in ("failures", "off") else "all"


def screenshot_allowed(status: str) -> bool:
    """Si la política deja enviar una captura de un paso con este estado."""
    policy = screenshots_policy()
    if policy == "off":
        return False
    return policy == "all" or status.upper() == "FAIL"


def playwright_screenshot(page: Any, **kwargs: Any) -> bytes:
    """``page.screenshot()`` con los elementos sensibles tapados (opción ``mask`` de Playwright)."""
    selector = mask_selector()
    if selector:
        kwargs.setdefault("mask", [page.locator(selector)])
        kwargs.setdefault("mask_color", MASK_COLOR)
    return page.screenshot(**kwargs)


# Recuadros fijos sobre cada elemento visible del selector (la captura de Selenium es del viewport).
_COVER_JS = """
const [sel, color, attr] = arguments;
let n = 0;
for (const el of document.querySelectorAll(sel)) {
  const r = el.getBoundingClientRect();
  if (!r.width || !r.height) continue;
  const d = document.createElement("div");
  d.setAttribute(attr, "");
  d.style.cssText = `position:fixed;left:${r.left}px;top:${r.top}px;width:${r.width}px;height:${r.height}px;` +
    `background:${color};z-index:2147483647;pointer-events:none;margin:0;padding:0;border:0`;
  document.documentElement.appendChild(d);
  n++;
}
return n;
"""

_UNCOVER_JS = """
for (const d of document.querySelectorAll("[" + arguments[0] + "]")) d.remove();
"""


def selenium_screenshot(driver: Any) -> Optional[bytes]:
    """PNG del viewport con los elementos sensibles tapados, o None si el navegador ya no responde.
    Los recuadros se quitan siempre, aunque la captura falle."""
    selector = mask_selector()
    covered = False
    try:
        if selector:
            try:
                driver.execute_script(_COVER_JS, selector, MASK_COLOR, _OVERLAY_ATTR)
                covered = True
            except Exception as err:  # sin poder tapar no se captura: mejor sin captura que sin máscara
                log.warning("tracereports: could not cover %r before the screenshot: %s", selector, err)
                return None
        return driver.get_screenshot_as_png()
    except Exception:
        return None
    finally:
        if covered:
            try:
                driver.execute_script(_UNCOVER_JS, _OVERLAY_ATTR)
            except Exception:
                pass
