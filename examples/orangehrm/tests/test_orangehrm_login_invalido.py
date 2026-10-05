# OrangeHRM (demo pública) — casos negativos de acceso, validando la UI Y la red del backend.
# Pensado para revisar la pestaña "Red" del reporte: incluye respuestas OK, redirects (302)
# y errores reales (401, 404).
#
# test_001: contraseña incorrecta → alerta "Invalid credentials" + POST /auth/validate → 302.
# test_002: campos vacíos → "Required" en ambos campos y NINGÚN request al backend.
# test_003: API sin sesión → fetch a /api/v2/pim/employees → 401 "Session expired".
# test_004: página protegida sin sesión → 302 → formulario de login.
# test_005: ruta inexistente → 404.
# Tests independientes: cada uno parte del formulario de login; uno que falle no omite al resto.
# Todos deben PASAR: aquí el 401/404 es la respuesta correcta del sistema. Los escenarios donde
# el backend falla y el test debe FALLAR están en test_orangehrm_errores_backend.py.
#
# Ejecutar (con el servidor TraceReports levantado):
#   python examples/orangehrm/tests/test_orangehrm_login_invalido.py
#   HEADLESS=0  → ver el navegador

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from base_test import OrangeHrmBaseTest  # noqa: E402


class TestOrangeHrmLoginInvalido(OrangeHrmBaseTest):
    NOMBRE_RUN = "OrangeHRM - Login inválido"
    CATEGORIA = "OrangeHRM, Login, Negativo"

    # ─── test_001 — CONTRASEÑA INCORRECTA ──────────────────────────────────
    def test_001_login_password_incorrecta(self):
        d = self.data_test
        self._ejecutar(
            f"Login con contraseña incorrecta USER:{d['USER']}",
            "El backend rechaza la contraseña (POST /auth/validate → 302) y la UI muestra 'Invalid credentials'",
            "OrangeHRM, Login, Negativo",
            [
                (lambda: self.login_page.abrir_navegador(d["BASE_URL"]), "No se pudo navegar a la pantalla de login"),
                (lambda: self.login_page.ingresar_credenciales(d["USER"], d["PASSWORD_INVALIDA"]),
                 "Las credenciales no quedaron ingresadas en el formulario"),
                (lambda: self.login_page.hacer_clic_login_esperando_error(d["MENSAJE_CREDENCIALES_INVALIDAS"]),
                 "La UI no mostró la alerta de credenciales inválidas"),
                (lambda: self.seguridad_page.validar_login_enviado_al_backend(),
                 "La red no muestra el POST de login rechazado por el backend"),
            ],
        )

    # ─── test_002 — CAMPOS VACÍOS ──────────────────────────────────────────
    def test_002_login_campos_vacios(self):
        d = self.data_test
        self._ejecutar(
            "Login con campos vacíos",
            "La validación es del navegador: 'Required' en ambos campos y ningún request al backend",
            "OrangeHRM, Login, Negativo, Validaciones",
            [
                (lambda: self.login_page.abrir_navegador(d["BASE_URL"]), "No se pudo navegar a la pantalla de login"),
                (lambda: self.seguridad_page.hacer_clic_login_sin_datos(),
                 "El formulario vacío no se validó correctamente en el navegador"),
            ],
        )

    # ─── test_003 — API SIN SESIÓN ─────────────────────────────────────────
    def test_003_api_sin_sesion_rechazada(self):
        d = self.data_test
        self._ejecutar(
            "API de empleados sin sesión",
            f"GET {d['API_SIN_SESION']} sin login debe responder 401 '{d['MENSAJE_SESION_EXPIRADA']}'",
            "OrangeHRM, Seguridad, API",
            [
                (lambda: self.login_page.abrir_navegador(d["BASE_URL"]), "No se pudo navegar a la pantalla de login"),
                (lambda: self.seguridad_page.consultar_api_sin_sesion(d["API_SIN_SESION"], 401, d["MENSAJE_SESION_EXPIRADA"]),
                 "La API de empleados no rechazó el acceso sin sesión como se esperaba"),
            ],
        )

    # ─── test_004 — PÁGINA PROTEGIDA ───────────────────────────────────────
    def test_004_pagina_protegida_redirige_a_login(self):
        d = self.data_test
        self._ejecutar(
            "Página protegida sin sesión",
            f"{d['PAGINA_PROTEGIDA']} sin login debe redirigir (302) al formulario de login",
            "OrangeHRM, Seguridad",
            [
                (lambda: self.seguridad_page.abrir_pagina_protegida(d["PAGINA_PROTEGIDA"]),
                 "La página protegida no redirigió al login"),
            ],
        )

    # ─── test_005 — RUTA INEXISTENTE ───────────────────────────────────────
    def test_005_ruta_inexistente_404(self):
        d = self.data_test
        self._ejecutar(
            "Ruta inexistente",
            f"{d['RUTA_INEXISTENTE']} debe responder 404 Not Found",
            "OrangeHRM, Seguridad",
            [
                (lambda: self.seguridad_page.abrir_ruta_inexistente(d["RUTA_INEXISTENTE"]),
                 "La ruta inexistente no respondió 404"),
            ],
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
