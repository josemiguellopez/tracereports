# OrangeHRM (demo pública) — ESCENARIOS DE FALLA CONTROLADA DEL BACKEND.
#
# Todos los tests de este archivo FALLAN A PROPÓSITO: simulan que el backend no responde
# (con la intercepción de red de Playwright) y verifican que el reporte muestre un fallo
# completo y explicable: test en FAIL, captura del momento, error con la llamada de backend
# que falló, pestaña "Red" con la conexión en rojo y AI Failure Triage.
# No se mezcla con los tests que deben pasar (test_orangehrm_pim.py / login_invalido.py).
#
# test_001: login con el backend de autenticación caído → POST /auth/validate sin respuesta.
# test_002: login con el backend respondiendo 500 → POST /auth/validate → 500.
# test_003: login OK, pero la API de empleados no responde (timeout) → PIM no carga.
# test_004: el botón de login se busca con un selector desactualizado (locator drift) → el
#           reporte propone selectores robustos y marca el botón real sobre la captura.
#
# Ejecutar (con el servidor TraceReports levantado):
#   python examples/orangehrm/tests/test_orangehrm_errores_backend.py
#   HEADLESS=0  → ver el navegador

import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from base_test import OrangeHrmBaseTest  # noqa: E402
from utils.tracereports_context import TraceReportsContext  # noqa: E402
from utils.function import By  # noqa: E402


# ─── fallas simuladas del backend (intercepción de red de Playwright) ─────────
# La request sale del navegador y se corta/responde ahí mismo: para la captura de red y para
# el reporte es idéntica a una caída real, pero repetible y sin depender del backend.
def simular_conexion_rechazada(page, patron):
    page.route(patron, lambda route: route.abort("connectionrefused"))


def simular_timeout(page, patron):
    page.route(patron, lambda route: route.abort("timedout"))


def simular_error_http(page, patron, status, body, solo_metodo=None):
    def handler(route):
        if solo_metodo and route.request.method != solo_metodo:
            route.continue_()
        else:
            route.fulfill(status=status, content_type="application/json", body=json.dumps(body))
    page.route(patron, handler)

# Los fallos se detectan rápido: no hace falta esperar los 30 s normales del Dashboard/PIM.
TIMEOUT_FALLA_MS = 10000

# Selector "de antes del rediseño": el botón ya no tiene ese atributo.
BOTON_LOGIN_SELECTOR_VIEJO = "//button[@data-test='login-submit']"


class TestOrangeHrmErroresBackend(OrangeHrmBaseTest):
    NOMBRE_RUN = "OrangeHRM - Errores de backend (simulados)"
    CATEGORIA = "OrangeHRM, Falla simulada"

    def setUp(self):
        super().setUp()
        self.context.clear_cookies()  # cada test parte sin sesión

    def tearDown(self):
        self.page.unroute_all()       # el siguiente test parte con el backend real

    def _simular(self, descripcion, simulacion):
        """Paso que activa la falla y lo deja registrado en el reporte (para que nadie
        confunda el fallo simulado con una caída real)."""
        simulacion()
        TraceReportsContext.log_warning(f"FALLA SIMULADA: {descripcion}")
        return True

    # ─── test_001 — BACKEND DE LOGIN CAÍDO ─────────────────────────────────
    def test_001_login_backend_caido(self):
        d = self.data_test
        self._ejecutar(
            f"Login con backend caído USER:{d['USER']}",
            "El servicio de autenticación no responde (conexión rechazada): el usuario no puede ingresar",
            "OrangeHRM, Falla simulada, Login",
            [
                (lambda: self.login_page.abrir_navegador(d["BASE_URL"]), "No se pudo navegar a la pantalla de login"),
                (lambda: self._simular(
                    "POST /auth/validate → conexión rechazada (backend de autenticación caído)",
                    lambda: simular_conexion_rechazada(self.page, "**/auth/validate")),
                 "No se pudo activar la falla simulada"),
                (lambda: self.login_page.ingresar_credenciales(d["USER"], d["PASSWORD"]),
                 "Las credenciales no quedaron ingresadas en el formulario"),
                (lambda: self.login_page.hacer_clic_login(timeout_ms=TIMEOUT_FALLA_MS),
                 "No se pudo iniciar sesión: el backend de autenticación no respondió"),
            ],
        )

    # ─── test_002 — BACKEND DE LOGIN CON ERROR 500 ─────────────────────────
    def test_002_login_backend_error_500(self):
        d = self.data_test
        self._ejecutar(
            f"Login con error 500 del backend USER:{d['USER']}",
            "El servicio de autenticación responde 500: el usuario no puede ingresar",
            "OrangeHRM, Falla simulada, Login",
            [
                (lambda: self.login_page.abrir_navegador(d["BASE_URL"]), "No se pudo navegar a la pantalla de login"),
                (lambda: self._simular(
                    "POST /auth/validate → 500 Internal Server Error",
                    lambda: simular_error_http(
                        self.page, "**/auth/validate", 500,
                        {"error": {"status": 500, "message": "Authentication service unavailable: database connection pool exhausted"}},
                        solo_metodo="POST")),
                 "No se pudo activar la falla simulada"),
                (lambda: self.login_page.ingresar_credenciales(d["USER"], d["PASSWORD"]),
                 "Las credenciales no quedaron ingresadas en el formulario"),
                (lambda: self.login_page.hacer_clic_login(timeout_ms=TIMEOUT_FALLA_MS),
                 "No se pudo iniciar sesión: el backend de autenticación respondió con error"),
            ],
        )

    # ─── test_003 — API DE EMPLEADOS EN TIMEOUT ────────────────────────────
    def test_003_lista_empleados_api_timeout(self):
        d = self.data_test
        self._ejecutar(
            "Lista de empleados con la API en timeout",
            "El login funciona, pero GET /api/v2/pim/employees no responde: PIM queda sin datos",
            "OrangeHRM, Falla simulada, PIM",
            [
                (lambda: self.login_page.abrir_navegador(d["BASE_URL"]), "No se pudo navegar a la pantalla de login"),
                (lambda: self.login_page.ingresar_credenciales(d["USER"], d["PASSWORD"]),
                 "Las credenciales no quedaron ingresadas en el formulario"),
                (lambda: self.login_page.hacer_clic_login(), "No se confirmó la llegada al Dashboard tras el login"),
                (lambda: self._simular(
                    "GET /api/v2/pim/employees → timeout (la API de empleados no responde)",
                    lambda: simular_timeout(self.page, "**/api/v2/pim/employees*")),
                 "No se pudo activar la falla simulada"),
                (lambda: self.dashboard_page.ir_a_modulo("PIM"), "No se pudo abrir el módulo PIM"),
                (lambda: bool(self.pim_page.extraer_empleados(5, timeout_ms=TIMEOUT_FALLA_MS)),
                 "La lista de empleados no cargó: la API de empleados no respondió"),
            ],
        )


    # ─── test_004 — LOCATOR DESACTUALIZADO ─────────────────────────────────
    def test_004_login_selector_desactualizado(self):
        d = self.data_test
        self.login_page.f.timeout_ms = 5000  # el elemento no va a aparecer: fallar rápido
        try:
            self._ejecutar(
                f"Login con selector desactualizado USER:{d['USER']}",
                "El botón de login cambió en el último deploy y el test todavía usa el selector viejo",
                "OrangeHRM, Falla simulada, Locator",
                [
                    (lambda: self.login_page.abrir_navegador(d["BASE_URL"]), "No se pudo navegar a la pantalla de login"),
                    (lambda: self.login_page.ingresar_credenciales(d["USER"], d["PASSWORD"]),
                     "Las credenciales no quedaron ingresadas en el formulario"),
                    (lambda: self._simular(
                        f"el test busca el botón con un selector que ya no existe: {BOTON_LOGIN_SELECTOR_VIEJO}",
                        lambda: None), "No se pudo registrar la falla simulada"),
                    (lambda: self.login_page.f.find_element_click(By.XPATH, BOTON_LOGIN_SELECTOR_VIEJO) or True,
                     "No se pudo presionar el botón de login"),
                ],
            )
        finally:
            self.login_page.f.timeout_ms = 15000


if __name__ == "__main__":
    unittest.main(verbosity=2)
