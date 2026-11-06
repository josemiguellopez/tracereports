"""TRACEREPORTS_DOM_STEPS=on-problem: las páginas de los pasos esperan en memoria y se mandan solo si el
test falla o tiene un WARNING; de un test que pasa, solo como base de "Comparar con el último PASS" cuando
el servidor no tiene una reciente. Con =1 se mandan siempre, como antes.

    pytest client/python/tests/test_dom_on_problem.py
"""

import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from tracereports import TraceReports
from tracereports._env import reset_env_file
from tracereports.dom import dom_steps_mode


@pytest.fixture(autouse=True)
def clean_env(monkeypatch, tmp_path):
    for var in ("TRACEREPORTS_DOM_STEPS", "TRACEREPORTS_DOM_ON", "TRACEREPORTS_TOKEN", "TRACEREPORTS_SCREENSHOTS"):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.chdir(tmp_path)
    reset_env_file()
    yield
    reset_env_file()


class Server:
    """baseline: None → 204 (no hay base), número → 200 con ese started_at, "old" → 404 (servidor anterior)."""

    def __init__(self, baseline=None):
        self.requests, self.baseline = [], baseline
        outer = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _handle(self):
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                try:
                    parsed = json.loads(body) if body else None
                except ValueError:
                    parsed = None
                outer.requests.append((self.command, self.path, parsed))
                code, out = 200, {}
                if self.path.endswith("/dom/baseline"):
                    if outer.baseline == "old":
                        code = 404
                    elif outer.baseline is None:
                        code = 204
                    else:
                        out = {"started_at": outer.baseline}
                raw = json.dumps(out).encode() if code != 204 else b""
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            do_GET = do_POST = do_PATCH = _handle

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"

    def doms(self):
        return [(b["kind"], b.get("label")) for m, p, b in self.requests if p.endswith("/dom") and b]

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


def _snap(text):
    return {"url": "https://app/", "nodes": [{"i": 0, "p": -1, "tag": "body", "text": text, "x": 0, "y": 0, "w": 10, "h": 10, "visible": True}],
            "elements": []}


def _client(srv, monkeypatch, mode="on-problem"):
    monkeypatch.setenv("TRACEREPORTS_DOM_STEPS", mode)
    reset_env_file()
    cr = TraceReports(srv.url)
    cr.run_id = 7
    pages = iter([_snap("login"), _snap("ficha"), _snap("guardar")])
    cr.dom_step_source = lambda: next(pages)
    return cr


def _steps(cr, tid=11):
    cr.attach_screenshot(b"\x89PNG a", "Abrir login", test_id=tid)
    cr.attach_screenshot(b"\x89PNG b", "Abrir la ficha", test_id=tid)


def test_mode_values(monkeypatch):
    for value, want in (("1", "all"), ("true", "all"), ("on-problem", "on-problem"), ("", "off"), ("0", "off")):
        monkeypatch.setenv("TRACEREPORTS_DOM_STEPS", value)
        reset_env_file()
        assert dom_steps_mode() == want, value


def test_a_failed_test_sends_its_pages_at_the_end(monkeypatch):
    srv = Server(baseline=int(time.time() * 1000))
    try:
        cr = _client(srv, monkeypatch)
        _steps(cr)
        assert cr.flush(5) == 0 and srv.doms() == [], "mientras corre, nada"
        cr.end_test("FAIL", error_message="boom", test_id=11)
        assert cr.flush(5) == 0
        assert srv.doms() == [("step", "Abrir login"), ("step", "Abrir la ficha")]
        assert not any(p.endswith("/dom/baseline") for m, p, b in srv.requests), "un fallo no pregunta la base"
    finally:
        srv.close()


def test_a_warning_step_counts_as_a_problem(monkeypatch):
    srv = Server(baseline=int(time.time() * 1000))
    try:
        cr = _client(srv, monkeypatch)
        _steps(cr)
        cr.log_warning("La grilla tardó 12 s", test_id=11)
        cr.end_test("PASS", test_id=11)  # el servidor lo deja WARNING
        assert cr.flush(5) == 0 and len(srv.doms()) == 2
    finally:
        srv.close()


def test_a_pass_with_a_recent_baseline_sends_nothing(monkeypatch):
    srv = Server(baseline=int(time.time() * 1000) - 3600_000)  # un PASS con páginas hace 1 h
    try:
        cr = _client(srv, monkeypatch)
        _steps(cr)
        cr.end_test("PASS", test_id=11)
        assert cr.flush(5) == 0 and srv.doms() == []
    finally:
        srv.close()


@pytest.mark.parametrize("baseline", [None, int(time.time() * 1000) - 8 * 24 * 3600_000])
def test_a_pass_without_a_recent_baseline_becomes_the_baseline(monkeypatch, baseline):
    srv = Server(baseline=baseline)
    try:
        cr = _client(srv, monkeypatch)
        _steps(cr)
        cr.end_test("PASS", test_id=11)
        assert cr.flush(5) == 0 and len(srv.doms()) == 2
    finally:
        srv.close()


def test_an_older_server_gets_no_pass_pages_and_loses_nothing(monkeypatch):
    srv = Server(baseline="old")
    try:
        cr = _client(srv, monkeypatch)
        _steps(cr)
        cr.end_test("PASS", test_id=11)
        assert cr.flush(5) == 0 and srv.doms() == [] and cr.delivery_problems() == 0
    finally:
        srv.close()


def test_mode_all_sends_every_page_right_away(monkeypatch):
    srv = Server()
    try:
        cr = _client(srv, monkeypatch, mode="1")
        _steps(cr)
        assert cr.flush(5) == 0 and len(srv.doms()) == 2, "como antes: en cada paso"
    finally:
        srv.close()
