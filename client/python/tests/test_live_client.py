"""En vivo desde el cliente: execution_id (TRACEREPORTS_EXECUTION_ID o el build de CI), tests
planificados (progreso y ETA), cancelación cooperativa (TRACEREPORTS_REMOTE_CONTROL=1) y la página de
un WARNING (TRACEREPORTS_DOM_ON=fail,warning).

    pip install pytest && pytest client/python/tests/test_live_client.py
"""

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from tracereports import TraceReports
from tracereports._env import reset_env_file
from tracereports.context import default_execution_id

pytest_plugins = ["pytester"]

CI_VARS = ("TRACEREPORTS_PLANNED", "TRACEREPORTS_EXECUTION_ID", "BUILD_BUILDID", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "CI_PIPELINE_ID", "BUILD_TAG",
           "TRACEREPORTS_REMOTE_CONTROL", "TRACEREPORTS_DOM_ON", "TRACEREPORTS_DOM_STEPS", "TRACEREPORTS_TOKEN", "TRACEREPORTS_RUN_ID")


@pytest.fixture(autouse=True)
def clean_env(monkeypatch, tmp_path):
    for var in CI_VARS:
        monkeypatch.delenv(var, raising=False)
    monkeypatch.chdir(tmp_path)
    reset_env_file()
    yield
    reset_env_file()


class LiveServer:
    """Servidor mínimo con lo de En vivo: /plan y /control (o un servidor viejo, sin ellos)."""

    def __init__(self, cancel=False, reason="", old=False):
        self.requests = []
        self.cancel, self.reason, self.old = cancel, reason, old
        self.next_test = 10
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _reply(self, code, out):
                raw = json.dumps(out).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def _handle(self):
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                try:
                    parsed = json.loads(body) if body else None
                except ValueError:  # una captura (multipart)
                    parsed = None
                outer.requests.append((self.command, self.path, parsed))
                if outer.old and (self.path.endswith("/control") or self.path.endswith("/plan")):
                    return self._reply(404, {"error": "not found"})
                if self.path == "/api/v1/runs":
                    return self._reply(201, {"run_id": 7})
                if self.path.endswith("/tests"):
                    outer.next_test += 1
                    return self._reply(201, {"test_id": outer.next_test})
                if self.path.endswith("/control"):
                    return self._reply(200, {"cancel": outer.cancel, "reason": outer.reason})
                return self._reply(200, {})

            do_GET = do_POST = do_PATCH = _handle

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"

    def paths(self, method=None):
        return [p for m, p, b in self.requests if method in (None, m)]

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


@pytest.fixture
def server():
    srv = LiveServer()
    yield srv
    srv.close()


def test_execution_id_from_env_or_ci(monkeypatch):
    assert default_execution_id() == ""
    monkeypatch.setenv("BUILD_BUILDID", "4411")
    assert default_execution_id() == "azure-4411"
    monkeypatch.delenv("BUILD_BUILDID")
    monkeypatch.setenv("GITHUB_RUN_ID", "99")
    monkeypatch.setenv("GITHUB_RUN_ATTEMPT", "2")
    assert default_execution_id() == "github-99-2", "otro intento del mismo workflow es otra ejecución"
    monkeypatch.setenv("TRACEREPORTS_EXECUTION_ID", "suite-nocturna-0611")
    reset_env_file()
    assert default_execution_id() == "suite-nocturna-0611", "la variable propia manda"


def test_start_run_sends_execution_and_planned(server, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_EXECUTION_ID", "lanzador-1")
    reset_env_file()
    cr = TraceReports(server.url)
    cr.start_run("test_ficha.py", planned=12, detect_context=False)
    created = next(b for m, p, b in server.requests if p == "/api/v1/runs")
    assert created["execution_id"] == "lanzador-1" and created["planned"] == 12
    cr.set_planned(14)
    assert ("POST", "/api/v1/runs/7/plan", {"planned": 14}) in server.requests
    cr.start_run("otra", execution_id="", detect_context=False)
    assert server.requests[-1][2]["execution_id"] == "", "se puede pedir sin agrupar"


def test_remote_control_only_when_asked_and_throttled(server, monkeypatch):
    cr = TraceReports(server.url)
    cr.start_run("r", detect_context=False)
    assert cr.cancel_requested() == (False, "")
    assert not server.paths("GET"), "sin TRACEREPORTS_REMOTE_CONTROL no pregunta nunca"

    monkeypatch.setenv("TRACEREPORTS_REMOTE_CONTROL", "1")
    reset_env_file()
    cr = TraceReports(server.url)
    cr.start_run("r", detect_context=False)
    assert cr.cancel_requested() == (False, "")
    assert cr.cancel_requested() == (False, ""), "dentro de los 5 s no vuelve a preguntar"
    assert server.paths("GET") == ["/api/v1/runs/7/control"]
    server.cancel, server.reason = True, "se cayó el ambiente"
    assert cr.cancel_requested(every=0) == (True, "se cayó el ambiente")
    server.cancel = False
    assert cr.cancel_requested(every=0) == (True, "se cayó el ambiente"), "una vez cancelada, queda cancelada"


def test_an_older_server_never_cancels_nor_loses_evidence(monkeypatch):
    srv = LiveServer(old=True)
    try:
        monkeypatch.setenv("TRACEREPORTS_REMOTE_CONTROL", "1")
        reset_env_file()
        cr = TraceReports(srv.url)
        cr.start_run("r", detect_context=False)
        cr.set_planned(3)
        assert cr.cancel_requested(every=0) == (False, "")
        assert cr.cancel_requested(every=0) == (False, "")
        assert srv.paths("GET") == ["/api/v1/runs/7/control"], "un 404: no se vuelve a preguntar"
        assert cr.flush(5) == 0 and cr.delivery_problems() == 0, "ni el plan ni el control cuentan como evidencia perdida"
    finally:
        srv.close()


def test_no_server_no_control(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_REMOTE_CONTROL", "1")
    reset_env_file()
    cr = TraceReports("http://127.0.0.1:9")
    assert cr.cancel_requested(every=0) == (False, "")


def _snap(text):
    return {"url": "https://app/", "nodes": [{"i": 0, "p": -1, "tag": "body", "text": text, "x": 0, "y": 0, "w": 10, "h": 10, "visible": True}],
            "elements": []}


def test_warning_page_with_dom_on(server, monkeypatch):
    cr = TraceReports(server.url)
    cr.run_id = 7
    cr.dom_step_source = lambda: _snap("agenda")
    cr.log_warning("La grilla tardó 12 s", test_id=11)
    assert cr.flush(5) == 0
    assert not [p for p in server.paths() if p.endswith("/dom")], "sin TRACEREPORTS_DOM_ON=warning, no"

    monkeypatch.setenv("TRACEREPORTS_DOM_ON", "fail,warning")
    reset_env_file()
    cr = TraceReports(server.url)
    cr.run_id = 7
    cr.dom_step_source = lambda: _snap("agenda")
    cr.log_warning("La grilla tardó 12 s", test_id=11)
    cr.attach_screenshot(b"\x89PNG w", "La grilla tardó 12 s", status="WARNING", test_id=11)  # la misma pantalla: una vez
    cr.log_info("sigue", test_id=11)  # un paso normal no, sin TRACEREPORTS_DOM_STEPS
    assert cr.flush(5) == 0
    doms = [b for m, p, b in server.requests if p.endswith("/dom")]
    assert [(d["kind"], d["label"]) for d in doms] == [("warning", "La grilla tardó 12 s")]


def test_plugin_planned_and_cooperative_cancel(pytester, monkeypatch):
    srv = LiveServer()
    try:
        monkeypatch.setenv("TRACEREPORTS_REMOTE_CONTROL", "1")
        pytester.makepyfile("""
            def test_a():
                pass
            def test_b():
                pass
            def test_c():
                pass
        """)
        srv.cancel, srv.reason = True, "QA caído"
        result = pytester.runpytest("--tracereports", "--tracereports-url", srv.url, "-p", "no:cacheprovider")
        result.assert_outcomes(skipped=3)
        assert ("POST", "/api/v1/runs/7/plan", {"planned": 3}) in srv.requests
        finishes = [b for m, p, b in srv.requests if p.endswith("/finish") and "/tests/" in p]
        assert len(finishes) == 3 and all(f["status"] == "SKIP" for f in finishes)
        skips = [b for m, p, b in srv.requests if p.endswith("/logs") and b and b.get("status") == "SKIP"]
        assert len(skips) == 3 and all(b["message"] == "Cancelado desde TraceReports: QA caído" for b in skips), skips
        assert any(p == "/api/v1/runs/7/finish" for m, p, b in srv.requests), "la ejecución cierra normal"
    finally:
        srv.close()


def test_plugin_planned_can_be_turned_off(pytester, monkeypatch):
    srv = LiveServer()
    try:
        monkeypatch.setenv("TRACEREPORTS_PLANNED", "off")  # un lanzador propio informa por corrida
        pytester.makepyfile("""
            def test_a():
                pass
            def test_b():
                pass
        """)
        result = pytester.runpytest("--tracereports", "--tracereports-url", srv.url, "-p", "no:cacheprovider")
        result.assert_outcomes(passed=2)
        assert not [p for m, p, b in srv.requests if p.endswith("/plan")]
    finally:
        srv.close()
