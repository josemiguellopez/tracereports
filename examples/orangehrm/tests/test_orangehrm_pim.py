# OrangeHRM (demo pública) — Login → scraping Dashboard → scraping PIM → búsqueda → logout,
# reportado en tiempo real a TraceReports con screenshots en cada paso.
#
# test_001: login válido (precondición del resto).
# test_002: scraping del Dashboard (usuario, widgets, menú lateral).
# test_003: scraping de PIM → Employee List; guarda cls.empleados y el Id a buscar.
# test_004: búsqueda por Employee Id usando un Id extraído en test_003.
# test_005: cerrar sesión.
# Workflow E2E Multipartido: si un paso de precondición falla, los siguientes se omiten
# (SKIP en el reporte) con el motivo del fallo original.
#
# Ejecutar (con el servidor TraceReports levantado):
#   python examples/orangehrm/tests/test_orangehrm_pim.py
#   HEADLESS=0  → ver el navegador

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from base_test import OrangeHrmBaseTest  # noqa: E402
from utils.tracereports_context import TraceReportsContext  # noqa: E402


class TestOrangeHrmPim(OrangeHrmBaseTest):
    NOMBRE_RUN = "OrangeHRM - PIM"
    CATEGORIA = "OrangeHRM"

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        # Estado compartido entre tests (Workflow E2E Multipartido).
        cls.empleados = []
        cls.employee_id_busqueda = None

    # ─── test_001 — LOGIN (precondición) ───────────────────────────────────
    def test_001_login(self):
        self._skip_if_workflow_failed(f"Precondición: login USER:{self.data_test['USER']}")
        start_time = self._iniciar_test(
            f"Precondición: login USER:{self.data_test['USER']}",
            "Login con credenciales válidas y llegada al Dashboard",
            categoria="OrangeHRM, Login, Smoke",
        )
        try:
            pasos = [
                (lambda: self.login_page.abrir_navegador(self.data_test["BASE_URL"]),
                 "No se pudo navegar a la pantalla de login"),
                (lambda: self.login_page.ingresar_credenciales(self.data_test["USER"], self.data_test["PASSWORD"]),
                 "Las credenciales no quedaron ingresadas en el formulario de login"),
                (lambda: self.login_page.hacer_clic_login(),
                 "No se confirmó la llegada al Dashboard tras el login"),
            ]
            for accion, mensaje in pasos:
                if not self._step_require(accion(), mensaje):
                    return self._abortar_workflow(mensaje)
        except Exception as e:
            error_file, error_line = self._registrar_excepcion(e)
            self._abortar_workflow(f"Excepción en {self._testMethodName} ({error_file}:{error_line}): {e}")
            raise
        finally:
            self._finalizar_test(start_time)

    # ─── test_002 — SCRAPING DASHBOARD ─────────────────────────────────────
    def test_002_scraping_dashboard(self):
        self._skip_if_workflow_failed("Scraping del Dashboard")
        start_time = self._iniciar_test(
            "Scraping del Dashboard",
            "Lee usuario autenticado, widgets y opciones del menú lateral",
            categoria="OrangeHRM, Dashboard, Scraping",
        )
        try:
            nombre = self.dashboard_page.obtener_nombre_usuario_header()
            if not self._step_require(bool(nombre.strip()), "No se pudo leer el nombre de usuario del header"):
                return self._abortar_workflow("No se pudo leer el nombre de usuario del header")

            widgets = self.dashboard_page.obtener_widgets()
            if not self._step_require(
                self.dashboard_page.validar_widgets_esperados(widgets, self.data_test["WIDGETS_ESPERADOS"]),
                "El Dashboard no muestra los widgets esperados",
            ):
                return  # dato informativo: no bloquea PIM

            opciones = self.dashboard_page.obtener_opciones_menu()
            if "PIM" not in opciones:
                self._step_require(False, "La opción 'PIM' no está en el menú lateral")
                return self._abortar_workflow("La opción 'PIM' no está en el menú lateral")
        except Exception as e:
            error_file, error_line = self._registrar_excepcion(e)
            self._abortar_workflow(f"Excepción en {self._testMethodName} ({error_file}:{error_line}): {e}")
            raise
        finally:
            self._finalizar_test(start_time)

    # ─── test_003 — SCRAPING PIM → EMPLOYEE LIST ───────────────────────────
    def test_003_scraping_lista_empleados(self):
        self._skip_if_workflow_failed("Scraping de PIM - Employee List")
        start_time = self._iniciar_test(
            "Scraping de PIM - Employee List",
            f"Extrae hasta {self.data_test['MAX_FILAS_SCRAPING']} empleados y valida la estructura de la tabla",
            categoria="OrangeHRM, PIM, Scraping",
        )
        try:
            if not self._step_require(self.dashboard_page.ir_a_modulo("PIM"), "No se pudo abrir el módulo PIM"):
                return self._abortar_workflow("No se pudo abrir el módulo PIM")

            total = self.pim_page.obtener_total_registros()
            if not self._step_require(total is not None and total > 0, f"El contador de registros no es válido: {total}"):
                return self._abortar_workflow("La lista de empleados está vacía o no se pudo leer")

            empleados = self.pim_page.extraer_empleados(self.data_test["MAX_FILAS_SCRAPING"], self.evidence_dir)
            if not self._step_require(
                self.pim_page.validar_empleados_extraidos(empleados, self.data_test["COLUMNAS_TABLA_EMPLEADOS"]),
                "El scraping de la tabla de empleados no es válido",
            ):
                return self._abortar_workflow("El scraping de la tabla de empleados no es válido")

            self.__class__.empleados = empleados
            self.__class__.employee_id_busqueda = next((e["Id"] for e in empleados if e.get("Id")), None)
            if not self._step_require(
                self.employee_id_busqueda is not None, "Ningún empleado extraído tiene Id para buscar",
            ):
                return self._abortar_workflow("Ningún empleado extraído tiene Id para buscar")
            TraceReportsContext.log_info(f"Id seleccionado para la búsqueda de test_004: '{self.employee_id_busqueda}'")
        except Exception as e:
            error_file, error_line = self._registrar_excepcion(e)
            self._abortar_workflow(f"Excepción en {self._testMethodName} ({error_file}:{error_line}): {e}")
            raise
        finally:
            self._finalizar_test(start_time)

    # ─── test_004 — BÚSQUEDA POR EMPLOYEE ID ───────────────────────────────
    def test_004_buscar_empleado_por_id(self):
        titulo = f"Buscar empleado por Id '{self.employee_id_busqueda}'"
        self._skip_if_workflow_failed(titulo)
        start_time = self._iniciar_test(
            titulo,
            "Busca por Employee Id un empleado obtenido en el scraping y valida que aparezca en los resultados",
            categoria="OrangeHRM, PIM, Búsqueda",
        )
        try:
            # No aborta el workflow: cerrar sesión (test_005) sigue siendo válido.
            self._step_require(
                self.pim_page.buscar_empleado_por_id(self.employee_id_busqueda),
                f"La búsqueda por Id '{self.employee_id_busqueda}' no devolvió el empleado esperado",
            )
        except Exception as e:
            self._registrar_excepcion(e)
            raise
        finally:
            self._finalizar_test(start_time)

    # ─── test_005 — CERRAR SESIÓN ──────────────────────────────────────────
    def test_005_cerrar_sesion(self):
        self._skip_if_workflow_failed("Cerrar sesión")
        start_time = self._iniciar_test(
            "Cerrar sesión",
            "Logout desde el menú de usuario y vuelta al formulario de login",
            categoria="OrangeHRM, Login, Smoke",
        )
        try:
            if not self._step_require(self.dashboard_page.abrir_menu_usuario(), "No se abrió el menú de usuario"):
                return
            self._step_require(
                self.dashboard_page.hacer_clic_cerrar_sesion(),
                "Tras 'Logout' no se volvió al formulario de login vacío",
            )
        except Exception as e:
            self._registrar_excepcion(e)
            raise
        finally:
            self._finalizar_test(start_time)


if __name__ == "__main__":
    unittest.main(verbosity=2)
