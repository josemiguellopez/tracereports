"""
Selenium WebDriver (Chrome / Edge) con TraceReports: red, consola, captura y DOM.

Selenium no da la red del navegador como Playwright, pero Chrome y Edge la escriben en su log de
rendimiento ("performance") si se activa al crear el driver. Este módulo lee ese log y lo convierte
en las mismas conexiones que la captura de Playwright: pestaña "Red", Lab, diagnóstico con la red y
selectores sugeridos al fallar.

    from selenium import webdriver
    from tracereports.selenium import selenium_options

    options = selenium_options(webdriver.ChromeOptions())   # activa los logs de red y de consola
    driver = webdriver.Chrome(options=options)

Con pytest no hace falta más: `pytest --tracereports` encuentra el driver en las fixtures del test
y envía la red y la consola de cada test, y al fallar la captura y el DOM. Sin pytest:

    from tracereports.selenium import report_selenium
    report_selenium(driver, cr)                    # al final de cada test, ANTES de cr.end_test()
    report_selenium(driver, cr, failed=True)       # si falló: además captura y DOM

Límites: solo Chrome y Edge (Firefox no tiene log de rendimiento). El body de una llamada hecha
antes de que la página navegara puede no estar disponible ya; el resto de la llamada sí.
"""

import base64
import json

from .dom import DOM_SCRIPT, _mask_dom
from .network import _mask_headers, _mask_sensitive

_LOG_PREFS = {"performance": "ALL", "browser": "ALL"}
_BODY_TYPES = {"xhr", "fetch"}
_TEXT_MIME = ("json", "text", "xml", "javascript", "html", "x-www-form-urlencoded", "graphql")


def selenium_options(options):
    """Activa en las opciones de Chrome o Edge los logs de rendimiento (red) y del navegador
    (consola). Retorna las mismas opciones."""
    options.set_capability("goog:loggingPrefs", dict(_LOG_PREFS))
    if type(options).__name__.lower().startswith("edge"):
        options.set_capability("ms:loggingPrefs", dict(_LOG_PREFS))
    return options


def _events(entries):
    """Los eventos Network.* del log de rendimiento, en orden."""
    for entry in entries or []:
        try:
            msg = json.loads(entry["message"])["message"]
        except (KeyError, TypeError, ValueError):
            continue
        method = msg.get("method") or ""
        if method.startswith("Network."):
            yield method, msg.get("params") or {}


def connections_from_log(entries):
    """Convierte las entradas de `driver.get_log("performance")` en conexiones (el formato de
    attach_network). Agrega a cada una "_request_id" para pedir su body después."""
    by_id, order, extra_req, extra_res = {}, [], {}, {}

    def new_conn(rid, p):
        req = p.get("request") or {}
        c = {
            "_request_id": rid,
            "url": req.get("url") or "",
            "method": req.get("method") or "",
            "resource_type": (p.get("type") or "").lower(),
            "request_headers": dict(req.get("headers") or {}),
            "post_data": req.get("postData") or "",
            "has_post_data": bool(req.get("hasPostData") or req.get("postData")),
            "wall_time": p.get("wallTime"),
            "_ts": p.get("timestamp"),
        }
        by_id[rid] = c
        order.append(c)
        return c

    def set_response(c, r):
        c["status"] = int(r.get("status") or 0)
        c["status_text"] = r.get("statusText") or ""
        c["mime_type"] = r.get("mimeType") or ""
        c["response_headers"] = dict(r.get("headers") or {})
        c["response_url"] = r.get("url") or c["url"]

    for method, p in _events(entries):
        rid = p.get("requestId")
        if not rid:
            continue
        if method == "Network.requestWillBeSent":
            prev = by_id.get(rid)
            if prev is not None and p.get("redirectResponse"):
                # la redirección cierra el salto anterior con su propia respuesta
                set_response(prev, p["redirectResponse"])
                if prev.get("_ts") is not None and p.get("timestamp") is not None:
                    prev["duration_ms"] = round((p["timestamp"] - prev["_ts"]) * 1000)
                prev["_request_id"] = None
            new_conn(rid, p)
        elif method == "Network.requestWillBeSentExtraInfo":
            extra_req.setdefault(rid, {}).update(p.get("headers") or {})
        elif method == "Network.responseReceived" and rid in by_id:
            c = by_id[rid]
            set_response(c, p.get("response") or {})
            if p.get("type"):
                c["resource_type"] = p["type"].lower()
        elif method == "Network.responseReceivedExtraInfo":
            extra_res.setdefault(rid, {}).update(p.get("headers") or {})
        elif method == "Network.loadingFinished" and rid in by_id:
            c = by_id[rid]
            if c.get("_ts") is not None and p.get("timestamp") is not None:
                c["duration_ms"] = round((p["timestamp"] - c["_ts"]) * 1000)
        elif method == "Network.loadingFailed" and rid in by_id:
            c = by_id[rid]
            c["failed"] = True
            c["error_text"] = p.get("errorText") or ("canceled" if p.get("canceled") else "failed")
            if c.get("_ts") is not None and p.get("timestamp") is not None:
                c["duration_ms"] = round((p["timestamp"] - c["_ts"]) * 1000)

    out = []
    for c in order:
        if c["url"].startswith(("data:", "blob:")):
            continue
        rid = c.get("_request_id")
        if rid in extra_req:  # los headers reales que salieron, con la cookie de sesión
            c["request_headers"] = {**c["request_headers"], **extra_req[rid]}
        if rid in extra_res and c.get("response_headers") is not None:
            c["response_headers"] = {**c["response_headers"], **extra_res[rid]}
        c.pop("_ts", None)
        out.append(c)
    return out


def _wants_body(c):
    if c.get("failed") or not c.get("_request_id"):
        return False
    if c.get("resource_type") in _BODY_TYPES or (c.get("status") or 0) >= 400:
        mime = (c.get("mime_type") or "").lower()
        return not mime or any(t in mime for t in _TEXT_MIME)
    return False


def _cdp(driver, cmd, params):
    try:
        return driver.execute_cdp_cmd(cmd, params) or {}
    except Exception:
        return {}  # otro navegador, o el recurso ya no existe (la página navegó)


def capture_network(driver, max_body_chars=None):
    """Las llamadas de red desde la última lectura (el log se vacía al leerlo: cada test lee las
    suyas), con los bodies de las APIs y de las respuestas con error, ya enmascaradas.
    Lista vacía si el driver no tiene el log de rendimiento activado (ver selenium_options)."""
    try:
        entries = driver.get_log("performance")
    except Exception:
        return []
    conns = connections_from_log(entries)
    for c in conns:
        rid = c.get("_request_id")
        if c.get("has_post_data") and not c.get("post_data") and rid:
            c["post_data"] = _cdp(driver, "Network.getRequestPostData", {"requestId": rid}).get("postData") or ""
            c["post_data_via_cdp"] = bool(c["post_data"])
        if _wants_body(c):
            res = _cdp(driver, "Network.getResponseBody", {"requestId": rid})
            body = res.get("body")
            if body is not None:
                if res.get("base64Encoded"):
                    try:
                        body = base64.b64decode(body).decode("utf-8")
                    except (ValueError, UnicodeDecodeError):
                        body = None
                if body is not None:
                    c["body_size"] = len(body.encode("utf-8", "surrogatepass"))
                    c["body_truncated"] = max_body_chars is not None and len(body) > max_body_chars
                    c["response_body"] = body if max_body_chars is None else body[:max_body_chars]
    for c in conns:
        c.pop("_request_id", None)
        c["url"] = _mask_sensitive(c["url"])
        c["request_headers"] = _mask_headers(c.get("request_headers"))
        c["response_headers"] = _mask_headers(c.get("response_headers"))
        c["post_data"] = _mask_sensitive(c.get("post_data") or "")
        if "response_body" in c:
            c["response_body"] = _mask_sensitive(c["response_body"])
    return conns


_LEVELS = {"SEVERE": "error", "WARNING": "warning"}


def capture_console(driver):
    """Errores y warnings de la consola del navegador desde la última lectura (el formato de
    attach_console). Las excepciones sin capturar quedan como "pageerror"."""
    try:
        entries = driver.get_log("browser")
    except Exception:
        return []
    out = []
    for e in entries or []:
        level = _LEVELS.get(str(e.get("level") or "").upper())
        if not level:
            continue
        if level == "error" and e.get("source") == "javascript":
            level = "pageerror"
        out.append({"level": level, "text": _mask_sensitive(e.get("message") or ""), "location": "",
                    "timestamp": int(e.get("timestamp") or 0)})
    return out


def capture_dom(driver):
    """Snapshot de los elementos de la página (selectores sugeridos al fallar), o None."""
    try:
        return _mask_dom(driver.execute_script("return (" + DOM_SCRIPT + ")();"))
    except Exception:
        return None


def screenshot(driver):
    """Captura de pantalla en PNG, o None si el navegador ya no responde."""
    try:
        return driver.get_screenshot_as_png()
    except Exception:
        return None


def is_webdriver(obj):
    """Si obj parece un WebDriver de Selenium (sin importar selenium)."""
    return all(hasattr(obj, a) for a in ("get_screenshot_as_png", "execute_script", "get_log", "session_id"))


def report_selenium(driver, cr, test_id=None, failed=False, max_body_chars=None):
    """Envía la red y la consola del test (desde la última lectura) y, si falló, la captura y el
    DOM. Retorna cuántas llamadas se enviaron. Nunca rompe el test."""
    try:
        if failed:
            png = screenshot(driver)
            if png:
                cr.attach_screenshot(png, "Captura al fallar", status="FAIL", test_id=test_id)
            cr.attach_dom(capture_dom(driver), test_id=test_id)
        conns = capture_network(driver, max_body_chars=max_body_chars)
        if conns:
            cr.attach_network(conns, test_id=test_id)
        console = capture_console(driver)
        if console:
            cr.attach_console(console, test_id=test_id)
        return len(conns)
    except Exception:
        return 0


def discard_logs(driver):
    """Vacía los logs del driver: lo de antes del test (otro test con el mismo driver) no se envía."""
    for kind in ("performance", "browser"):
        try:
            driver.get_log(kind)
        except Exception:
            pass
