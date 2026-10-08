"""
Captura de red (backend) con Playwright para TraceReports.

Adaptado de Utils/network_capture.py del framework de automatización:
  - mismos nombres y mismo estado en el page (page._network_evidence), así que
    guardar_evidencia_red() del framework sigue funcionando encima de esta captura;
  - mismo enmascarado de datos sensibles (RUT, tokens, passwords, cookies...);
  - mismo fallback CDP para recuperar el post_data de ciertos POST.

Novedades:
  - duration_ms y resource_type por conexión;
  - reportar_red(page, cr): envía a TraceReports SOLO las conexiones nuevas desde el
    último envío (por test, no acumulado) y espera brevemente las que siguen en vuelo.

Uso:
    page = context.new_page()
    attach_listeners(page, logger=logger)        # ANTES del primer page.goto()
    ...
    resumen = reportar_red(page, cr, guardar_en="output/network", nombre_evento="test_001_login")
    # -> {"total": 42, "ok": 40, "errores": 2, "fallidas": 1, "archivo": "C:/.../network_test_001_login_1790.json"}
"""

import json
import os
import re
import time

_SENSITIVE_HEADER_NAMES = {"authorization", "cookie", "set-cookie", "x-auth-token"}
_BODY_RESOURCE_TYPES = {"xhr", "fetch"}


def _mask_sensitive(text):
    if not isinstance(text, str):
        return text
    text = re.sub(r'\b\d{7,8}-[\dkK]\b', '<rut>', text)
    text = re.sub(
        r'"(token|sig|session|auth|password|pwd|clave)"\s*:\s*"[^"]*"',
        r'"\1": "<masked>"',
        text,
        flags=re.IGNORECASE,
    )
    text = re.sub(
        r'\b(token|sig|session|auth|password|pwd|clave)=[^&\s"]+',
        r'\1=<masked>',
        text,
        flags=re.IGNORECASE,
    )
    return text


def _mask_headers(headers):
    if not headers:
        return {}
    return {
        key: "<masked>" if key.lower() in _SENSITIVE_HEADER_NAMES else _mask_sensitive(value)
        for key, value in headers.items()
    }


def attach_listeners(
    page,
    logger=None,
    incluir_bodies_api=True,
    incluir_bodies_error=True,
    patrones_api=("/api/",),
    max_body_chars=None,
):
    """
    Registra los listeners de red en el page. Debe llamarse INMEDIATAMENTE después de
    crear el page, antes de cualquier page.goto() (Playwright no tiene buffer retroactivo).

    El body de la response se lee en el momento en que llega (después el navegador puede
    descartarlo). Se guarda el body de: URLs que contienen algún `patrones_api`, requests
    XHR/fetch, y respuestas con status >= 400. Por defecto el body se conserva COMPLETO
    (como network_capture.py) para que reportar_red(..., guardar_en=...) lo deje entero en
    disco; `max_body_chars` permite acotarlo si la memoria importa más que la evidencia.
    """
    page._network_evidence = []
    page._network_by_request = {}
    page._network_reported_idx = 0
    # consola del navegador: errores y advertencias, y errores de JavaScript sin manejar
    page._console_evidence = []
    page._console_reported_idx = 0

    def _on_console(msg):
        try:
            level = {"warn": "warning"}.get(msg.type, msg.type)
            if level not in ("error", "warning"):
                return
            loc = msg.location or {}
            where = f"{loc.get('url', '')}:{loc.get('lineNumber', '')}:{loc.get('columnNumber', '')}" if loc.get("url") else ""
            page._console_evidence.append({"level": level, "text": _mask_sensitive(msg.text), "location": where,
                                           "timestamp": int(time.time() * 1000)})
        except Exception as e:
            if logger:
                logger.debug(f"No se pudo leer un mensaje de consola: {e}")

    def _on_pageerror(err):
        page._console_evidence.append({"level": "pageerror", "text": _mask_sensitive(str(err)), "location": "",
                                       "timestamp": int(time.time() * 1000)})

    try:
        page.on("console", _on_console)
        page.on("pageerror", _on_pageerror)
    except Exception as e:
        if logger:
            logger.debug(f"Sin captura de consola: {e}")
    page._network_postdata_cdp_pendientes = {}
    page._network_conexiones_esperando_postdata = []

    def _on_request(request):
        try:
            conexion = {
                "requestId": id(request),
                "url": _mask_sensitive(request.url),
                "method": request.method,
                "resource_type": request.resource_type,
                "request_headers": _mask_headers(request.headers),
                "post_data": _mask_sensitive(request.post_data or ""),
                "has_post_data": bool(request.post_data),
                "wall_time": time.time(),
            }
            page._network_evidence.append(conexion)
            page._network_by_request[request] = conexion

            if not conexion["has_post_data"]:
                clave = (request.method, request.url)
                cola = page._network_postdata_cdp_pendientes.get(clave)
                if cola:
                    conexion["post_data"] = _mask_sensitive(cola.pop(0))
                    conexion["has_post_data"] = True
                    conexion["post_data_via_cdp"] = True
                else:
                    page._network_conexiones_esperando_postdata.append((request.method, request.url, conexion))
        except Exception as e:
            if logger:
                logger.debug(f"No se pudo registrar request: {e}")

    def _on_response(response):
        try:
            conexion = page._network_by_request.get(response.request)
            if conexion is None:
                return
            conexion["status"] = response.status
            conexion["status_text"] = response.status_text
            conexion["mime_type"] = (response.headers or {}).get("content-type", "")
            conexion["response_headers"] = _mask_headers(response.headers)
            conexion["response_url"] = _mask_sensitive(response.url)
        except Exception as e:
            if logger:
                logger.debug(f"No se pudo registrar response: {e}")
            return

        url = (conexion.get("url") or "").lower()
        es_api = any(p in url for p in patrones_api) or conexion.get("resource_type") in _BODY_RESOURCE_TYPES
        if (incluir_bodies_api and es_api) or (incluir_bodies_error and response.status >= 400):
            try:
                body = response.text()
                # el recorte se marca aquí, donde ocurre; el tamaño original va en bytes UTF-8 (como el
                # servidor). Enmascarar cambia el largo, pero no es un recorte
                conexion["body_size"] = len(body.encode("utf-8", "surrogatepass"))
                conexion["body_truncated"] = max_body_chars is not None and len(body) > max_body_chars
                conexion["response_body"] = _mask_sensitive(body if max_body_chars is None else body[:max_body_chars])
            except Exception as e:
                if logger:
                    logger.debug(f"No se pudo leer el body de {conexion.get('url')}: {e}")

    def _duracion(request, conexion):
        try:
            timing = request.timing or {}
            if timing.get("responseEnd", -1) > 0:
                return int(timing["responseEnd"])
        except Exception:
            pass
        return int((time.time() - conexion["wall_time"]) * 1000)

    def _on_request_finished(request):
        conexion = page._network_by_request.get(request)
        if conexion is not None:
            conexion["duration_ms"] = _duracion(request, conexion)

    def _on_request_failed(request):
        try:
            conexion = page._network_by_request.get(request)
            if conexion is None:
                return
            conexion["failed"] = True
            conexion["error_text"] = request.failure
            conexion["duration_ms"] = int((time.time() - conexion["wall_time"]) * 1000)
        except Exception as e:
            if logger:
                logger.debug(f"No se pudo registrar requestfailed: {e}")

    page.on("request", _on_request)
    page.on("response", _on_response)
    page.on("requestfinished", _on_request_finished)
    page.on("requestfailed", _on_request_failed)

    # Fallback CDP para post_data (solo Chromium): ver network_capture.py original.
    try:
        cdp = page.context.new_cdp_session(page)
        cdp.send("Network.enable")

        def _on_cdp_request_will_be_sent(params):
            try:
                req = params.get("request") or {}
                if not req.get("hasPostData") or req.get("postData"):
                    return
                request_id = params.get("requestId")
                if not request_id:
                    return
                post_data_cdp = cdp.send("Network.getRequestPostData", {"requestId": request_id}).get("postData")
                if not post_data_cdp:
                    return
                metodo, url = req.get("method"), req.get("url")
                pendientes = page._network_conexiones_esperando_postdata
                for i, (m, u, conexion) in enumerate(pendientes):
                    if m == metodo and u == url:
                        conexion["post_data"] = _mask_sensitive(post_data_cdp)
                        conexion["has_post_data"] = True
                        conexion["post_data_via_cdp"] = True
                        pendientes.pop(i)
                        return
                page._network_postdata_cdp_pendientes.setdefault((metodo, url), []).append(post_data_cdp)
            except Exception as e:
                if logger:
                    logger.debug(f"No se pudo obtener postData via CDP: {e}")

        cdp.on("Network.requestWillBeSent", _on_cdp_request_will_be_sent)
    except Exception as e:
        if logger:
            logger.debug(f"Sin sesión CDP para el fallback de postData (¿navegador no Chromium?): {e}")


def conexiones_del_test(page):
    """Conexiones capturadas desde el último reportar_red(): las del test en curso.
    Útil para validar el backend dentro del test (p. ej. "se envió el POST de login")."""
    return list(getattr(page, "_network_evidence", [])[getattr(page, "_network_reported_idx", 0):])


def esperar_conexion(page, predicado, timeout_ms=5000):
    """Espera hasta que una conexión del test cumpla `predicado` y tenga respuesta (o falle).
    Retorna la conexión o None."""
    limite = time.time() + timeout_ms / 1000
    while True:
        for c in conexiones_del_test(page):
            if predicado(c) and (c.get("status") or c.get("failed")):
                return c
        if time.time() >= limite:
            return None
        page.wait_for_timeout(100)


def _en_vuelo(conexiones):
    return [c for c in conexiones if not c.get("status") and not c.get("failed")]


def resumir_red(conexiones):
    """{"total", "ok", "errores", "fallidas"} — errores = HTTP >= 400 o sin respuesta."""
    fallidas = sum(1 for c in conexiones if c.get("failed"))
    http_error = sum(1 for c in conexiones if not c.get("failed") and (c.get("status") or 0) >= 400)
    return {
        "total": len(conexiones),
        "ok": sum(1 for c in conexiones if not c.get("failed") and 0 < (c.get("status") or 0) < 400),
        "errores": fallidas + http_error,
        "fallidas": fallidas,
    }


def guardar_red_local(conexiones, carpeta, nombre_evento):
    """Guarda las conexiones COMPLETAS (sin recortar bodies) en carpeta/network_<evento>_<ts>.json
    y retorna la ruta absoluta (o None si no se pudo escribir)."""
    os.makedirs(carpeta, exist_ok=True)
    nombre = "".join(ch if ch.isalnum() or ch in "-_" else "_" for ch in nombre_evento)
    ruta = os.path.abspath(os.path.join(carpeta, f"network_{nombre}_{int(time.time())}.json"))
    with open(ruta, "w", encoding="utf-8") as fh:
        json.dump(conexiones, fh, ensure_ascii=False, indent=2, default=str)
    return ruta


def reportar_red(page, cr, test_id=None, esperar_en_vuelo_ms=2000, logger=None, guardar_en=None, nombre_evento=None):
    """
    Envía a TraceReports las conexiones capturadas desde el último envío (las del test
    actual) y retorna su resumen. Antes espera hasta `esperar_en_vuelo_ms` a que terminen
    las requests todavía sin respuesta, para no reportarlas incompletas.

    guardar_en: carpeta donde dejar además un JSON con la captura COMPLETA del test. El
    servidor guarda como máximo 256 KB por body; con esto el reporte indica la ruta exacta
    del archivo que tiene el body entero (p. ej. catálogos de varios MB).
    """
    capturadas = getattr(page, "_network_evidence", None)
    if capturadas is None:
        if logger:
            logger.warning("reportar_red: el page no tiene attach_listeners() — no hay captura de red")
        return None

    desde = getattr(page, "_network_reported_idx", 0)
    limite = time.time() + esperar_en_vuelo_ms / 1000
    while _en_vuelo(capturadas[desde:]) and time.time() < limite:
        try:
            page.wait_for_timeout(100)  # deja que Playwright procese los eventos pendientes
        except Exception:
            break

    nuevas = capturadas[desde:]
    page._network_reported_idx = desde + len(nuevas)
    resumen = resumir_red(nuevas)
    if nuevas and guardar_en:
        try:
            ruta = guardar_red_local(nuevas, guardar_en, nombre_evento or "test")
            for conexion in nuevas:
                conexion["evidence_file"] = ruta
            resumen["archivo"] = ruta
        except OSError as e:
            if logger:
                logger.warning(f"No se pudo guardar la evidencia de red local: {e}")
    if nuevas:
        cr.attach_network(nuevas, test_id=test_id)
    consola = getattr(page, "_console_evidence", None)
    if consola is not None:
        desde_consola = getattr(page, "_console_reported_idx", 0)
        nuevas_consola = consola[desde_consola:]
        page._console_reported_idx = desde_consola + len(nuevas_consola)
        if nuevas_consola:
            cr.attach_console(nuevas_consola, test_id=test_id)
    if logger:
        logger.info(f"Red reportada: {resumen}")
    return resumen
