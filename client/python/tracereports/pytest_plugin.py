"""
Plugin de pytest: reporta la suite a TraceReports sin cambiar el código de los tests.

    pytest --tracereports                                   # servidor en $TRACEREPORTS_URL o localhost:8080
    pytest --tracereports --tracereports-run "Regresión" --tracereports-env staging
    pytest --tracereports -n 4                              # pytest-xdist: un solo reporte consolidado

Qué hace:
  - una ejecución (run) por sesión de pytest y un test por cada caso. Con pytest-xdist el
    controlador crea la ejecución y los workers reportan en ella; se cierra cuando terminan
    todos (si un worker se cae, sus tests quedan como interrumpidos y la ejecución, incompleta);
  - identidad estable de cada test: su nodeid (archivo::clase::test[parámetros]), aparte del
    nombre visible. Dos tests con el mismo nombre en archivos distintos no mezclan su historial.
    @pytest.mark.tracereports_id("login-ok") la fija a mano si el nodeid cambia;
  - contexto: proyecto (--tracereports-project / $TRACEREPORTS_PROJECT / la carpeta), rama y commit (del CI
    o de git). El historial y las comparaciones solo usan ejecuciones del mismo contexto;
  - estado PASS/FAIL/SKIP con el mensaje y el traceback del fallo (entrada del AI Triage). Con
    pytest-rerunfailures, los intentos quedan en el mismo test ("pasó tras reintento") y la
    evidencia del primer fallo se conserva;
  - con pytest-playwright (fixture `page`): captura de pantalla al fallar, snapshot del DOM y
    captura de red del test (pestaña "Red"), sin configurar nada;
  - @pytest.mark.tracereports_expect(status=401, url="/api/auth") declara una respuesta negativa que
    el test verifica a propósito (también en la clase o el módulo): no cuenta como error ni como
    causa del fallo;
  - fixture `tracereports` para agregar pasos y capturas propias:

        def test_login(page, tracereports):
            tracereports.log_info("Abrir login")
            tracereports.attach_screenshot(page.screenshot(), "Formulario de login")

La evidencia se envía en segundo plano con reintentos; al terminar, el resumen de la terminal
dice si algo no llegó. --tracereports-strict hace fallar la sesión en ese caso (útil en CI).

Configuración por variables de entorno equivalentes: TRACEREPORTS_URL, TRACEREPORTS_TOKEN, TRACEREPORTS_RUN_NAME,
TRACEREPORTS_ENV, TRACEREPORTS_PROJECT, TRACEREPORTS_RUN_ID (unirse a una ejecución ya creada, p. ej. shards de
CI), TRACEREPORTS_SPOOL_DIR, TRACEREPORTS_STRICT. Con --tracereports-zip DIR el reporte queda además en un ZIP
(se abre sin servidor ni internet), ideal para adjuntarlo como artefacto del pipeline.

Sin servidor (no responde, o el token es incorrecto) la evidencia no se pierde: se graba en
./tracereports-offline/<sesión> ($TRACEREPORTS_OFFLINE_DIR) y `tracereports report <carpeta>` arma el reporte
HTML; `tracereports push <carpeta>` la sube después. --tracereports-offline DIR graba siempre, sin servidor.
"""

import os

import pytest

from ._env import env
from .client import TraceReports
from .dom import capturar_dom
from .network import attach_listeners, reportar_red

_TEST_ID = pytest.StashKey[int]()
_OUTCOME = pytest.StashKey[dict]()
_PAGE = pytest.StashKey[object]()


def _page(item):
    """Página de pytest-playwright del test (pytest borra item.funcargs tras el teardown)."""
    page = item.stash.get(_PAGE, None)
    if page is None:
        page = (getattr(item, "funcargs", None) or {}).get("page")
    return page


def _truthy(value):
    return str(value or "").lower() in ("1", "true", "yes")


def pytest_addoption(parser):
    group = parser.getgroup("tracereports", "TraceReports")
    group.addoption("--tracereports", action="store_true", help="reportar la ejecución a TraceReports")
    group.addoption("--tracereports-url", default=None, help="URL del servidor (default: $TRACEREPORTS_URL o http://localhost:8080)")
    group.addoption("--tracereports-run", default=None, help="nombre de la ejecución (default: $TRACEREPORTS_RUN_NAME o la carpeta del proyecto)")
    group.addoption("--tracereports-env", default=None, help="ambiente: navegador, entorno, versión... (default: $TRACEREPORTS_ENV)")
    group.addoption("--tracereports-project", default=None, help="proyecto (default: $TRACEREPORTS_PROJECT o la carpeta raíz)")
    group.addoption("--tracereports-no-network", action="store_true", help="no capturar la red del backend con Playwright")
    group.addoption("--tracereports-no-screenshots", action="store_true", help="no tomar captura de pantalla al fallar (datos sensibles en pantalla)")
    group.addoption("--tracereports-no-dom", action="store_true", help="no enviar el snapshot del DOM al fallar")
    group.addoption("--tracereports-zip", default=None, metavar="DIR", help="al terminar, guardar el reporte en ZIP en DIR (útil como artefacto de CI)")
    group.addoption("--tracereports-spool", default=None, metavar="DIR", help="carpeta donde guardar la evidencia que no se pudo enviar (default: $TRACEREPORTS_SPOOL_DIR)")
    group.addoption("--tracereports-offline", default=None, metavar="DIR", help="grabar la evidencia en DIR sin usar un servidor (reporte con `tracereports report DIR`)")
    group.addoption("--tracereports-strict", action="store_true", help="falla la sesión si parte de la evidencia no llegó al servidor (default: $TRACEREPORTS_STRICT)")


def pytest_configure(config):
    config.addinivalue_line("markers", "tracereports_id(key): identidad estable del test en TraceReports (reemplaza el nodeid)")
    config.addinivalue_line("markers", "tracereports_expect(status, url='', method=''): respuesta HTTP negativa esperada por el test")
    if config.getoption("--tracereports"):
        config.pluginmanager.register(TraceReportsPlugin(config), "tracereports-plugin")


@pytest.fixture
def tracereports(request):
    """Cliente de TraceReports ligado al test en curso (no-op si no se usa --tracereports)."""
    plugin = request.config.pluginmanager.get_plugin("tracereports-plugin")
    return plugin.cr if plugin else TraceReports(enabled=False)


def _identity(item):
    """(key, suite, params) del test: nodeid salvo que @tracereports_id lo fije."""
    marker = item.get_closest_marker("tracereports_id")
    key = str(marker.args[0]) if marker and marker.args else item.nodeid
    suite = item.nodeid.rsplit("::", 1)[0] if "::" in item.nodeid else item.nodeid
    callspec = getattr(item, "callspec", None)
    params = getattr(callspec, "id", "") if callspec is not None else ""
    return key, suite, params


class TraceReportsPlugin:
    def __init__(self, config):
        self.config = config
        offline_dir = config.getoption("--tracereports-offline")
        self.cr = TraceReports(config.getoption("--tracereports-url"), spool_dir=config.getoption("--tracereports-spool"),
                               offline_dir=offline_dir, offline="always" if offline_dir else None)
        self.capture_network = not config.getoption("--tracereports-no-network")
        self.capture_screenshots = not config.getoption("--tracereports-no-screenshots")
        self.capture_dom = not config.getoption("--tracereports-no-dom")
        self.strict = config.getoption("--tracereports-strict") or _truthy(env("STRICT"))
        self.workerinput = getattr(config, "workerinput", None)  # presente en workers de xdist
        self.crashed_workers = []
        self.worker_problems = 0
        self.problems = []

    # ─── sesión ────────────────────────────────────────────────────────────
    def _ensure_run(self):
        """Crea (o se une a) la ejecución una sola vez. En un worker de xdist usa la del controlador."""
        if self.cr.run_id:
            return
        if self.workerinput is not None:
            self.cr.join_run(self.workerinput.get("tracereports_run_id"), offline_dir=self.workerinput.get("tracereports_offline_dir"))
            return
        shared = env("RUN_ID")
        if shared:
            self.cr.join_run(shared)
            return
        root = str(self.config.rootpath)
        name = self.config.getoption("--tracereports-run") or env("RUN_NAME") or os.path.basename(root)
        environment = self.config.getoption("--tracereports-env") or env("ENV") or ""
        project = self.config.getoption("--tracereports-project") or env("PROJECT") or os.path.basename(root)
        self.cr.start_run(name, environment=environment, project=project, framework="pytest")

    def pytest_sessionstart(self, session):
        self._ensure_run()

    @pytest.hookimpl(optionalhook=True)
    def pytest_configure_node(self, node):
        """xdist (controlador): todos los workers reportan en la misma ejecución."""
        self._ensure_run()
        # como texto: execnet manda los int como 32 bits y un id local (sin servidor) no cabe
        node.workerinput["tracereports_run_id"] = str(self.cr.run_id) if self.cr.run_id else None
        node.workerinput["tracereports_offline_dir"] = self.cr.offline_dir

    @pytest.hookimpl(optionalhook=True)
    def pytest_testnodedown(self, node, error):
        """xdist (controlador): un worker terminó; si se cayó, la ejecución queda incompleta."""
        if error:
            self.crashed_workers.append(getattr(node, "gateway", None) and node.gateway.id or "worker")
        out = getattr(node, "workeroutput", None) or {}
        self.worker_problems += int(out.get("tracereports_problems", 0))

    def pytest_sessionfinish(self, session, exitstatus):
        if self.workerinput is not None:
            # worker: entrega su evidencia; el controlador cierra la ejecución
            self.cr.flush()
            self.cr._sender.drain_to_spool()
            if self.cr.recording:
                self.cr._sender.close()  # el controlador arma el reporte con todos los archivos
            self.config.workeroutput["tracereports_problems"] = self.cr.delivery_problems()
            return
        if not self.cr.run_id:
            if self.cr.enabled:
                self.problems.append("no se pudo crear la ejecución en el servidor")
            self._apply_strict(session)
            return
        if self.cr.run_created:
            interrupted = bool(self.crashed_workers) or exitstatus == pytest.ExitCode.INTERRUPTED or bool(session.shouldstop)
            self.cr.end_run(interrupted=interrupted)
            zip_dir = self.config.getoption("--tracereports-zip")
            if zip_dir:
                self.zip_path = self.cr.download_report(zip_dir)
        else:
            self.cr.flush()
            self.cr._sender.drain_to_spool()
        if self.cr.recording and self.cr.offline_mode == "auto":
            self.problems.append(f"sin servidor: la evidencia quedó grabada en {self.cr.offline_dir}")
        lost = self.cr.delivery_problems() + self.worker_problems
        if lost:
            self.problems.append(f"{lost} eventos de evidencia no llegaron al servidor")
        if self.crashed_workers:
            self.problems.append(f"workers caídos: {', '.join(map(str, self.crashed_workers))} (ejecución incompleta)")
        self._apply_strict(session)

    def _apply_strict(self, session):
        if self.strict and self.problems and session.exitstatus == pytest.ExitCode.OK:
            session.exitstatus = pytest.ExitCode.TESTS_FAILED

    def pytest_terminal_summary(self, terminalreporter):
        if self.workerinput is not None:
            return
        if self.cr.recording:
            where = self.cr.offline_report or f"`tracereports report {self.cr.offline_dir} -o reporte`"
            terminalreporter.write_line(f"TraceReports (sin servidor): evidencia en {self.cr.offline_dir}; reporte: {where}; "
                                        f"para subirla: `tracereports push {self.cr.offline_dir}`")
        elif self.cr.run_id:
            terminalreporter.write_line(f"TraceReports: {self.cr.base_url}/#run={self.cr.run_id}&view=dashboard")
        if getattr(self, "zip_path", None):
            terminalreporter.write_line(f"TraceReports ZIP: {self.zip_path}")
        d = self.cr.delivery
        if self.problems:
            detail = ", ".join(f"{k}={d[k]}" for k in ("sent", "retried", "rejected", "dropped", "spooled", "lost", "unregistered_tests") if d[k])
            terminalreporter.write_line(f"TraceReports: atención: {'; '.join(self.problems)} ({detail})"
                                        + (" — --tracereports-strict: la sesión falla" if self.strict else ""),
                                        yellow=True)

    # ─── cada test ─────────────────────────────────────────────────────────
    @pytest.hookimpl(hookwrapper=True)
    def pytest_runtest_protocol(self, item, nextitem):
        doc = (getattr(item.function, "__doc__", None) or "").strip() if hasattr(item, "function") else ""
        markers = [m.name for m in item.iter_markers()
                   if m.name not in ("parametrize", "usefixtures", "skip", "skipif", "xfail", "tracereports_id", "tracereports_expect",
                                     "flaky")]
        category = ", ".join([item.module.__name__.split(".")[-1], *markers]) if hasattr(item, "module") else ", ".join(markers)
        key, suite, params = _identity(item)
        test_id = self.cr.start_test(item.name, category=category, description=doc.splitlines()[0] if doc else "",
                                     key=key, suite=suite, params=params)
        if test_id:
            item.stash[_TEST_ID] = test_id
            for m in item.iter_markers("tracereports_expect"):
                status = m.kwargs.get("status", m.args[0] if m.args else None)
                if status is not None:
                    self.cr.expect_response(status, url=m.kwargs.get("url", ""), method=m.kwargs.get("method", ""), test_id=test_id)
        item.stash[_OUTCOME] = {"status": None, "message": "", "trace": "", "attempts": 0}
        yield
        self._finish(item)

    @pytest.hookimpl(hookwrapper=True)
    def pytest_runtest_call(self, item):
        # La captura de red se engancha antes de que el test navegue (la fixture page ya existe).
        page = (getattr(item, "funcargs", None) or {}).get("page")
        if page is not None:
            item.stash[_PAGE] = page
            if self.capture_network and not hasattr(page, "_network_evidence"):
                attach_listeners(page)
        yield
        # Con la página todavía abierta: esperar (máx. 2 s) las llamadas en vuelo y enviar la red.
        # Después del teardown pytest-playwright cierra la página y ya no se puede esperar.
        test_id = item.stash.get(_TEST_ID, None)
        if page is not None and test_id and hasattr(page, "_network_evidence"):
            reportar_red(page, self.cr, test_id=test_id, esperar_en_vuelo_ms=2000)

    @pytest.hookimpl(hookwrapper=True)
    def pytest_runtest_makereport(self, item, call):
        report = (yield).get_result()
        outcome = item.stash.get(_OUTCOME, None)
        if outcome is None:
            return
        if report.when == "setup":
            # cada intento empieza con su setup (pytest-rerunfailures repite setup/call/teardown)
            outcome["attempts"] += 1
            if outcome["attempts"] > 1:
                previous = outcome["message"] or "falló"
                test_id = item.stash.get(_TEST_ID, None)
                self.cr.log_warning(f"Reintento {outcome['attempts']}: el intento {outcome['attempts'] - 1} falló — {previous}", test_id=test_id)
                outcome.update(status=None, message="", trace="")
        if report.skipped:
            reason = report.longrepr[2] if isinstance(report.longrepr, tuple) else str(report.longrepr or "")
            if outcome["status"] is None:
                outcome.update(status="SKIP", message=reason.replace("Skipped: ", ""))
        elif report.failed:
            if outcome["status"] != "FAIL":
                text = report.longreprtext or ""
                last = [ln for ln in text.splitlines() if ln.startswith("E ")]
                outcome.update(status="FAIL", message=(" ".join(ln[2:].strip() for ln in last[:3]) or text[:300]).strip(), trace=text)
                if report.when != "call":
                    outcome["message"] = f"Error en {report.when}: " + outcome["message"]
                self._evidence_on_failure(item)
        elif report.when == "call" and outcome["status"] is None:
            outcome["status"] = "PASS"

    def _evidence_on_failure(self, item):
        page = _page(item)
        if page is None:
            return
        test_id = item.stash.get(_TEST_ID, None)
        if self.capture_screenshots:
            try:
                self.cr.attach_screenshot(page.screenshot(timeout=5000), "Captura al fallar", status="FAIL", test_id=test_id)
            except Exception:
                pass  # la página puede estar cerrada: la evidencia nunca rompe el test
        if self.capture_dom:
            # snapshot de los elementos: el reporte recomienda selectores si el fallo fue de locator
            self.cr.attach_dom(capturar_dom(page), test_id=test_id)

    def _finish(self, item):
        test_id = item.stash.get(_TEST_ID, None)
        if not test_id:
            return
        outcome = item.stash[_OUTCOME]
        page = _page(item)
        if page is not None and hasattr(page, "_network_evidence"):
            # lo que llegó durante el teardown (la página ya puede estar cerrada: sin esperar)
            reportar_red(page, self.cr, test_id=test_id, esperar_en_vuelo_ms=0)
        if outcome["status"] == "FAIL":
            self.cr.log_fail(outcome["message"] or "Falló", test_id=test_id)
        self.cr.end_test(outcome["status"] or "PASS", error_message=outcome["message"] if outcome["status"] == "FAIL" else "",
                         error_trace=outcome["trace"], test_id=test_id, attempts=outcome["attempts"])
