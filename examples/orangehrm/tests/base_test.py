# Infraestructura común de los tests de OrangeHRM (equivalente a la sección INFRAESTRUCTURA
# que el framework copia en cada archivo de test): navegador + captura de red, run de
# TraceReports, _step_require, workflow multipartido, _finalizar_test y cierre.
#
# Cada archivo de test hereda de OrangeHrmBaseTest y define NOMBRE_RUN / CATEGORIA.

import datetime
import json
import logging
import os
import sys
import time
import traceback
import unittest
import uuid

current_dir = os.path.dirname(os.path.abspath(__file__))
project_root = os.path.abspath(os.path.join(current_dir, ".."))          # examples/orangehrm
client_dir = os.path.abspath(os.path.join(project_root, "..", "..", "client", "python"))  # o: pip install ./client/python
for path in (project_root, client_dir):
    if path not in sys.path:
        sys.path.insert(0, path)

from playwright.sync_api import Error as PlaywrightError, sync_playwright  # noqa: E402

from tracereports import TraceReports, attach_listeners, capturar_dom, conexiones_del_test, reportar_red  # noqa: E402
from pages.dashboard_page import Dashboard  # noqa: E402
from pages.login_page import Login  # noqa: E402
from pages.pim_page import Pim  # noqa: E402
from pages.seguridad_page import Seguridad  # noqa: E402
from utils.tracereports_context import TraceReportsContext  # noqa: E402


def _obtener_ubicacion_error(exc: Exception):
    """(archivo, línea) del frame MÁS PROFUNDO del traceback — donde ocurrió realmente
    la excepción (Page Object, Function...), no donde se capturó."""
    frames = traceback.extract_tb(exc.__traceback__)
    if not frames:
        return "?", 0
    return os.path.basename(frames[-1].filename), frames[-1].lineno


def _lanzar_navegador(playwright, headless: bool, logger):
    """Chromium de Playwright; si no está instalado, cae a Chrome/Edge del sistema.
    Forzar uno con BROWSER_CHANNEL=chrome|msedge."""
    canales = [os.getenv("BROWSER_CHANNEL")] if os.getenv("BROWSER_CHANNEL") else [None, "chrome", "msedge"]
    ultimo_error = None
    for canal in canales:
        try:
            browser = playwright.chromium.launch(channel=canal, headless=headless)
            logger.info(f"Navegador lanzado: {canal or 'chromium (playwright)'} headless={headless}")
            return browser, canal or "chromium"
        except PlaywrightError as e:
            ultimo_error = e
            logger.warning(f"No se pudo lanzar '{canal or 'chromium'}': {str(e).splitlines()[0]}")
    raise RuntimeError(f"No hay navegador disponible (ejecuta 'playwright install chromium'): {ultimo_error}")


class OrangeHrmBaseTest(unittest.TestCase):
    NOMBRE_RUN = "OrangeHRM"
    CATEGORIA = "OrangeHRM"

    # ─── helpers de workflow ───────────────────────────────────────────────
    def _archivo_test(self) -> str:
        return os.path.basename(sys.modules[type(self).__module__].__file__)

    def _step_require(self, resul_test_ok: bool, msg: str) -> bool:
        if not resul_test_ok:
            full = f"{msg} | file: {self._archivo_test()}"
            self.logger.error(full)
            self.test_errors_list.append(full)
            return False
        return True

    def _abortar_workflow(self, motivo: str):
        """Marca el workflow como fallido: los tests siguientes se reportan como SKIP."""
        self.__class__._workflow_failed = True
        self.__class__._workflow_failure_step = self._testMethodName
        self.__class__._workflow_failure_reason = motivo

    def _skip_if_workflow_failed(self, test_title: str, test_desc: str = ""):
        if not self.__class__._workflow_failed:
            return
        reason = (
            f"Test '{self._testMethodName}' omitido — paso anterior falló: "
            f"'{self.__class__._workflow_failure_step}' — {self.__class__._workflow_failure_reason}"
        )
        self.cr.start_test(test_title, category=self.CATEGORIA, description=test_desc)
        TraceReportsContext.log_skip(
            f"Test omitido por fallo previo: {reason}",
            page=self.page,
            screenshot_name=f"skip_{self._testMethodName}",
        )
        self.cr.end_test("SKIP")
        self.skipTest(reason)

    def _iniciar_test(self, titulo: str, descripcion: str, categoria: str = None):
        self.logger.info(f"Inicio: {self._testMethodName} - {datetime.datetime.now()}")
        self.cr.start_test(titulo, category=categoria or self.CATEGORIA, description=descripcion)
        return datetime.datetime.now()

    def _ejecutar(self, titulo, descripcion, categoria, pasos):
        """Test simple: corre una lista de (acción, mensaje de error) con _step_require y se
        detiene en el primer paso que falla (para tests independientes, sin workflow)."""
        start_time = self._iniciar_test(titulo, descripcion, categoria=categoria)
        try:
            for accion, mensaje in pasos:
                if not self._step_require(accion(), mensaje):
                    return
        except Exception as e:
            self._registrar_excepcion(e)
            raise
        finally:
            self._finalizar_test(start_time)

    def _registrar_excepcion(self, e: Exception):
        error_file, error_line = _obtener_ubicacion_error(e)
        TraceReportsContext.log_error(
            f"Error en {error_file}:{error_line}: {type(e).__name__}: {e}",
            page=self.page,
            screenshot_name=f"error_{self._testMethodName}",
        )
        self.test_errors_list.append(f"Error en {self._testMethodName} ({error_file}:{error_line}): {type(e).__name__}: {e}")
        self._exception_trace = traceback.format_exc()
        return error_file, error_line

    # ─── setup ─────────────────────────────────────────────────────────────
    @classmethod
    def setUpClass(cls):
        cls.unique_id = f"{time.strftime('%Y%m%d_%H%M%S')}_{str(uuid.uuid4()).split('-')[0]}"
        output_dir = os.path.join(project_root, "output")
        cls.evidence_dir = os.path.join(output_dir, "evidence", cls.unique_id)
        os.makedirs(os.path.join(output_dir, "logs"), exist_ok=True)

        cls.logger = logging.getLogger(f"orangehrm.{cls.unique_id}")
        cls.logger.setLevel(logging.DEBUG)
        handler = logging.FileHandler(os.path.join(output_dir, "logs", f"{cls.unique_id}.log"), encoding="utf-8")
        handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(message)s"))
        cls.logger.addHandler(handler)

        with open(os.path.join(project_root, "data", "data_test.json"), encoding="utf-8") as fh:
            cls.data_test = json.load(fh)

        cls.test_errors_list = []
        cls.test_warnings_list = []
        cls._workflow_failed = False
        cls._workflow_failure_step = ""
        cls._workflow_failure_reason = ""

        headless = os.getenv("HEADLESS", "1") != "0"
        cls.playwright = sync_playwright().start()
        cls.browser, canal = _lanzar_navegador(cls.playwright, headless, cls.logger)
        cls.context = cls.browser.new_context(viewport={"width": 1366, "height": 768}, locale="en-US")
        cls.page = cls.context.new_page()
        # Captura de red: ANTES del primer goto (Playwright no tiene buffer retroactivo).
        attach_listeners(cls.page, logger=cls.logger)

        # TraceReports: una ejecución por clase de test.
        # Sin servidor, o con TRACEREPORTS_OFFLINE=both, la evidencia local queda en output/tracereports/<corrida>,
        # venga de donde venga el comando.
        cls.cr = TraceReports(timeout=3.0, upload_timeout=8.0, offline_base=os.path.join(output_dir, "tracereports"))
        cls.cr.start_run(
            cls.NOMBRE_RUN,  # nombre estable: el historial y la comparación agrupan por ejecución
            environment=f"demo pública · {canal} · {'headless' if headless else 'headed'}",
        )
        TraceReportsContext.setup(cls.cr, evidence_dir=cls.evidence_dir, logger=cls.logger)

        page_args = (cls.page, cls.logger, cls.test_errors_list, cls.data_test, cls.test_warnings_list)
        cls.login_page = Login(*page_args)
        cls.dashboard_page = Dashboard(*page_args)
        cls.pim_page = Pim(*page_args)
        cls.seguridad_page = Seguridad(*page_args)

    def setUp(self):
        self.test_errors_list.clear()
        self.test_warnings_list.clear()
        self._exception_trace = None

    # ─── INFRAESTRUCTURA ───────────────────────────────────────────────────
    def _finalizar_test(self, start_time):
        elapsed = datetime.datetime.now() - start_time
        file_name = self._archivo_test()

        for warning in self.test_warnings_list:
            self.logger.warning(f"Advertencia: {warning}")
            TraceReportsContext.log_warning(f"Advertencia: {warning}")

        # Si el test falló, el mensaje de error incluye las llamadas de backend que fallaron:
        # así el bloque de error y el AI Triage apuntan a la causa real (backend caído, 500...).
        if self.test_errors_list:
            # snapshot de la página: si el fallo fue un locator roto, el reporte sugiere reemplazos
            self.cr.attach_dom(capturar_dom(self.page))
            for c in [c for c in conexiones_del_test(self.page) if c.get("failed") or (c.get("status") or 0) >= 400][:3]:
                resultado = f"sin respuesta ({c.get('error_text')})" if c.get("failed") else f"HTTP {c['status']} {c.get('status_text', '')}"
                self.test_errors_list.append(f"Backend: {c['method']} {c['url']} → {resultado.strip()}")

        # Evidencia de red del test (solo las conexiones nuevas) → pestaña "Red" del reporte.
        # Se envía ANTES de end_test para que el AI Triage de un FAIL vea los errores de backend.
        red = reportar_red(
            self.page, self.cr, logger=self.logger,
            guardar_en=os.path.join(self.evidence_dir, "network"), nombre_evento=self._testMethodName,
        )
        if red and red["total"]:
            resumen_red = f"Evidencia de red: {red['total']} conexiones — {red['ok']} OK, {red['errores']} con error"
            if red["errores"]:
                TraceReportsContext.log_warning(f"{resumen_red} (ver pestaña Red)")
            else:
                TraceReportsContext.log_info(resumen_red)

        if not self.test_errors_list:
            TraceReportsContext.log_pass(f"Test finalizado sin errores - {file_name} ({elapsed})", page=self.page)
            self.cr.end_test("WARNING" if self.test_warnings_list else "PASS")
        else:
            # error_message + error_trace son la entrada del "AI Failure Triage" de TraceReports.
            self.cr.end_test(
                "FAIL",
                error_message="\n".join(self.test_errors_list),
                error_trace=self._exception_trace or "",
            )

        # Si hay una excepción en curso se deja propagar (conserva su traceback);
        # si fue un fallo de validación, se marca el test como fallido acá.
        if self.test_errors_list and self._exception_trace is None:
            self.fail(f"Errores en {file_name}: {self.test_errors_list}")

    @classmethod
    def tearDownClass(cls):
        for cerrar in (cls.context.close, cls.browser.close, cls.playwright.stop):
            try:
                cerrar()
            except Exception as e:
                cls.logger.warning(f"Error al cerrar el navegador: {e}")
        cls.cr.end_run()
        if cls.cr.run_id:
            print(f"\nReporte: {cls.cr.base_url}/#run={cls.cr.run_id}&view=tests")
        print(f"Evidencia local: {cls.evidence_dir}")
