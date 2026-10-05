import time

from tracereports import conexiones_del_test, esperar_conexion
from locators.login_locator import LoginLocator
from locators.seguridad_locator import SeguridadLocator
from utils.tracereports_context import TraceReportsContext
from utils.function import By, Function


class Seguridad:
    """
    Page Object de los casos negativos de acceso: login inválido, validaciones del
    formulario y acceso sin sesión (API, páginas protegidas, rutas inexistentes).

    Las validaciones combinan la UI con la evidencia de red capturada: se verifica lo que
    ve el usuario Y lo que respondió el backend (status, mensaje, si hubo o no request).
    """

    def __init__(self, page, logger, error_list, data_test, warning_list=None):
        self.page = page
        self.logger = logger
        self.error_list = error_list
        self.data_test = data_test
        self.warning_list = warning_list if warning_list is not None else []
        self.f = Function(page, logger, error_list)
        self.loc = SeguridadLocator

    def _url(self, ruta: str) -> str:
        return self.data_test["BASE_HOST"].rstrip("/") + ruta

    # ─── formulario de login ───────────────────────────────────────────────
    def validar_login_enviado_al_backend(self) -> bool:
        """Resultado esperado: el navegador envió POST /auth/validate y el backend respondió
        con redirect (302) de vuelta al login."""
        c = esperar_conexion(self.page, lambda c: c["method"] == "POST" and "/auth/validate" in c["url"])
        if c is None:
            return self.f.fallo_validacion(
                "No se registró el POST de login en la red",
                esperado="POST /auth/validate",
                encontrado="ningún request de login",
                screenshot_name=f"error_login_no_enviado_{int(time.time())}",
            )
        if c.get("status") != 302:
            return self.f.fallo_validacion(
                "El backend respondió el login inválido con un status inesperado",
                esperado="302 (redirect al formulario de login)",
                encontrado=f"{c.get('status') or 'sin respuesta'} {c.get('status_text', '')}",
                screenshot_name=f"error_status_login_{int(time.time())}",
            )
        TraceReportsContext.log_info(
            f"Evidencia de red: POST /auth/validate → {c['status']} en {c.get('duration_ms', '?')} ms"
        )
        return True

    def hacer_clic_login_sin_datos(self) -> bool:
        """Resultado esperado: 'Required' bajo Username y Password, sin enviar nada al backend."""
        self.f.send_text(By.NAME, LoginLocator.NAME.TXT_USERNAME, "")
        self.f.send_text(By.NAME, LoginLocator.NAME.TXT_PASSWORD, "")
        self.f.find_element_click(By.XPATH, LoginLocator.XPATH.BTN_LOGIN)
        return self.validar_campos_requeridos() and self.validar_sin_request_de_login()

    def validar_campos_requeridos(self) -> bool:
        if not self.f.wait_element(By.XPATH, self.loc.XPATH.LIST_MENSAJES_REQUERIDO, wait_type="visible"):
            return self.f.fallo_validacion(
                "No se mostraron los mensajes de campo obligatorio",
                esperado="'Required' bajo Username y Password",
                encontrado="sin mensajes de validación",
                screenshot_name=f"error_sin_required_{int(time.time())}",
            )
        mensajes = self.f.get_all_texts(By.XPATH, self.loc.XPATH.LIST_MENSAJES_REQUERIDO)
        if mensajes != ["Required", "Required"]:
            return self.f.fallo_validacion(
                "Los mensajes de validación no son los esperados",
                esperado="['Required', 'Required']",
                encontrado=str(mensajes),
                screenshot_name=f"error_mensajes_required_{int(time.time())}",
            )
        TraceReportsContext.log_info(
            "Evidencia de validación 'Required' en Username y Password",
            page=self.page,
            screenshot_name=f"campos_requeridos_{int(time.time())}",
        )
        return True

    def validar_sin_request_de_login(self) -> bool:
        """La validación es del lado del cliente: no debe salir ningún POST al backend."""
        self.page.wait_for_timeout(800)  # margen por si el formulario igual intentara enviarse
        enviados = [c for c in conexiones_del_test(self.page) if c["method"] == "POST" and "/auth/validate" in c["url"]]
        if enviados:
            return self.f.fallo_validacion(
                "El formulario vacío se envió al backend",
                esperado="ningún POST /auth/validate (validación en el navegador)",
                encontrado=f"{len(enviados)} POST /auth/validate",
                screenshot_name=f"error_login_vacio_enviado_{int(time.time())}",
            )
        TraceReportsContext.log_info("Evidencia de red: el formulario vacío NO se envió al backend")
        return True

    # ─── acceso sin sesión ─────────────────────────────────────────────────
    def consultar_api_sin_sesion(self, ruta: str, status_esperado: int, mensaje_esperado: str) -> bool:
        """Hace un fetch() desde la página (como lo haría el frontend) sin estar logueado.
        Resultado esperado: el backend lo rechaza con `status_esperado` y `mensaje_esperado`."""
        respuesta = self.page.evaluate(
            """async (url) => {
                const r = await fetch(url, { headers: { accept: "application/json" } });
                return { status: r.status, body: await r.text() };
            }""",
            ruta,
        )
        TraceReportsContext.log_info(
            f"Evidencia de consultar la API sin sesión: GET {ruta} → {respuesta['status']}",
            page=self.page,
            screenshot_name=f"api_sin_sesion_{int(time.time())}",
        )
        c = esperar_conexion(self.page, lambda c: ruta in c["url"])
        if c is None:
            return self.f.fallo_validacion(
                "La consulta a la API no quedó registrada en la red",
                esperado=f"GET {ruta}",
                encontrado="sin conexión capturada",
                screenshot_name=f"error_api_no_capturada_{int(time.time())}",
            )
        if c.get("status") != status_esperado:
            return self.f.fallo_validacion(
                "La API respondió sin sesión con un status inesperado (¿expone datos sin autenticar?)",
                esperado=str(status_esperado),
                encontrado=f"{c.get('status')} {c.get('status_text', '')}",
                screenshot_name=f"error_status_api_{int(time.time())}",
            )
        return self.f.validate_text_by_contains(mensaje_esperado, c.get("response_body", ""), "Mensaje de la API sin sesión")

    def abrir_pagina_protegida(self, ruta: str) -> bool:
        """Resultado esperado: el backend redirige (302) al login y se muestra el formulario."""
        self.page.goto(self._url(ruta), wait_until="domcontentloaded", timeout=60000)
        c = esperar_conexion(self.page, lambda c: c["url"].endswith(ruta) and c.get("resource_type") == "document")
        if c is None or c.get("status") != 302:
            return self.f.fallo_validacion(
                "La página protegida no redirigió al login",
                esperado=f"GET {ruta} → 302",
                encontrado=f"{(c or {}).get('status', 'sin respuesta')}",
                screenshot_name=f"error_pagina_protegida_{int(time.time())}",
            )
        if "/auth/login" not in self.page.url or not self.f.wait_element(By.NAME, LoginLocator.NAME.TXT_USERNAME):
            return self.f.fallo_validacion(
                "Tras el redirect no se mostró el formulario de login",
                esperado="URL /auth/login con el formulario visible",
                encontrado=self.page.url,
                screenshot_name=f"error_redirect_login_{int(time.time())}",
            )
        TraceReportsContext.log_info(
            f"Evidencia de redirect: GET {ruta} → 302 → /auth/login",
            page=self.page,
            screenshot_name=f"redirect_login_{int(time.time())}",
        )
        return True

    def abrir_ruta_inexistente(self, ruta: str) -> bool:
        """Resultado esperado: el backend responde 404 Not Found."""
        respuesta = self.page.goto(self._url(ruta), wait_until="domcontentloaded", timeout=60000)
        status = respuesta.status if respuesta else None
        TraceReportsContext.log_info(
            f"Evidencia de abrir una ruta inexistente: GET {ruta} → {status}",
            page=self.page,
            screenshot_name=f"ruta_inexistente_{int(time.time())}",
        )
        if status != 404:
            return self.f.fallo_validacion(
                "Una ruta inexistente no respondió 404",
                esperado="404 Not Found",
                encontrado=str(status),
                screenshot_name=f"error_ruta_inexistente_{int(time.time())}",
            )
        return True
