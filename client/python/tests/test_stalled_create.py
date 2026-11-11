"""La conexión se cuelga al crear la ejecución (feedback 10-11: Docker Desktop en una VM Windows).

El cliente conectaba, el pedido no recibía respuesta y a los 15 s pasaba a grabar en local: la
ejecución nunca llegaba al servidor aunque este respondiera segundos después. Ahora:

- el aviso dice qué pasó ("did not answer within 30 s", "wrong token", "server down or unreachable")
  en vez de "server down or wrong token" para todo;
- los reintentos duran hasta 30 s (un corte breve ni pasa a local);
- si igual pasó a local, al cerrar se prueba el servidor y, si responde, se sube la grabación
  (``tracereports push``, idempotente): la ejecución queda en el servidor con sus tests.
"""

import json
import logging
import os
import re
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

import tracereports.client as client_mod
from tracereports import TraceReports
from tracereports._env import reset_env_file


@pytest.fixture(autouse=True)
def clean_env(monkeypatch, tmp_path):
    for var in ("TRACEREPORTS_TIMEOUT", "TRACEREPORTS_RUN_TIMEOUT", "TRACEREPORTS_OFFLINE", "TRACEREPORTS_OFFLINE_DIR",
                "TRACEREPORTS_OFFLINE_PUSH", "TRACEREPORTS_TOKEN", "TRACEREPORTS_URL", "TRACEREPORTS_SYNC"):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.setenv("TRACEREPORTS_ENV_FILE", str(tmp_path / "no.env"))
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_BASE", str(tmp_path / "offline"))
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    reset_env_file()
    yield
    reset_env_file()


class StallProxy:
    """Como el reenvío de puertos de Docker Desktop cuando se traba: acepta la conexión y no la entrega
    mientras ``stalled``; después reenvía todo al servidor de verdad (o responde /healthz si no hay)."""

    def __init__(self, target=None):
        self.stalled = True
        self.release = threading.Event()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _forward(self):
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                if self.path == "/__recover":  # para una suite de pytest: "la red volvió"
                    outer.recover()
                if outer.stalled:
                    outer.release.wait(60)  # colgado: el cliente vence esperando la respuesta
                    return
                if target is None:
                    status, raw = (200, b"ok") if self.path in ("/healthz", "/__recover") else (404, b"{}")
                else:
                    headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "content-length", "connection", "accept-encoding")}
                    req = urllib.request.Request(target + self.path, data=body or None, method=self.command, headers=headers)
                    try:
                        with urllib.request.urlopen(req, timeout=30) as resp:
                            status, raw = resp.status, resp.read()
                    except urllib.error.HTTPError as err:
                        status, raw = err.code, err.read()
                try:
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except OSError:
                    pass

            do_GET = do_POST = do_PATCH = do_PUT = _forward

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def recover(self):
        self.stalled = False
        self.release.set()

    def close(self):
        self.release.set()
        self.httpd.shutdown()
        self.httpd.server_close()


def _closed_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


REAL_RUN = subprocess.run


def only_push(fn):
    """Reemplaza subprocess.run solo para `tracereports push` (git y lo demás corren de verdad)."""
    def run(cmd, *a, **kw):
        return fn(cmd, **kw) if len(cmd) > 1 and cmd[1] == "push" else REAL_RUN(cmd, *a, **kw)
    return run


def _bin():
    return os.environ.get("TRACEREPORTS_BIN") or os.environ.get("TRACEREPORTS_TEST_BIN")


# ------------------------------------------------------------------ el aviso dice qué pasó

def test_warning_says_the_server_did_not_answer(monkeypatch, caplog):
    monkeypatch.setattr(client_mod, "RUN_TOTAL_SECONDS", 1.5)
    proxy = StallProxy()
    try:
        cr = TraceReports(proxy.url, timeout=1, run_timeout=1)
        with caplog.at_level(logging.WARNING, logger="tracereports"):
            cr.start_run("colgado")
        assert cr.recording
        assert "the server did not answer within 2 s" in caplog.text, caplog.text
        assert "wrong token" not in caplog.text, "a timeout is not a token problem"
    finally:
        proxy.close()


def test_timeout_warning_says_where_it_connected_and_how_long_it_waited(monkeypatch, caplog):
    """Conectó al instante y nadie respondió: el pedido se trabó después de conectar (reenvío de
    puertos, servidor), no en la red ni en el nombre."""
    monkeypatch.setattr(client_mod, "RUN_TOTAL_SECONDS", 1.0)
    proxy = StallProxy()
    try:
        cr = TraceReports(proxy.url, timeout=1, run_timeout=1)
        with caplog.at_level(logging.WARNING, logger="tracereports"):
            cr.start_run("colgado")
        port = proxy.url.rsplit(":", 1)[1]
        assert re.search(rf"POST /api/v1/runs failed: timed out \(connected to 127\.0\.0\.1:{port} in \d+ ms, no answer in 1 s\)",
                         caplog.text), caplog.text
    finally:
        proxy.close()


def test_connect_timeout_says_how_long_it_tried():
    from tracereports.transport import _HTTPConnection, ConnectTimeout
    conn = _HTTPConnection("10.255.255.1", 9, timeout=0.3)  # no enrutable: el connect vence
    with pytest.raises((ConnectTimeout, OSError)) as err:
        conn.connect()
    if isinstance(err.value, ConnectTimeout):
        assert re.search(r"connect timed out after 0\.\d s", str(err.value)), str(err.value)


def test_warning_for_a_server_that_is_down(caplog):
    cr = TraceReports(f"http://127.0.0.1:{_closed_port()}", timeout=1, run_timeout=1)
    with caplog.at_level(logging.WARNING, logger="tracereports"):
        cr.start_run("caído")
    assert cr.recording and "server down or unreachable" in caplog.text, caplog.text


def test_warning_for_a_wrong_token(caplog):
    class Deny(BaseHTTPRequestHandler):
        def log_message(self, *a):
            pass

        def do_POST(self):
            self.rfile.read(int(self.headers.get("Content-Length") or 0))
            self.send_response(401)
            self.send_header("Content-Length", "2")
            self.end_headers()
            self.wfile.write(b"{}")

    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Deny)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    try:
        cr = TraceReports(f"http://127.0.0.1:{httpd.server_address[1]}", token="malo", timeout=1, run_timeout=1)
        with caplog.at_level(logging.WARNING, logger="tracereports"):
            cr.start_run("token")
        assert cr.recording and "(wrong token)" in caplog.text, caplog.text
    finally:
        httpd.shutdown()
        httpd.server_close()


def test_total_cap_is_30_seconds():
    assert client_mod.RUN_TOTAL_SECONDS == 30.0
    assert TraceReports("http://127.0.0.1:1")._run_total() == 30.0


# ------------------------------------------------------------------ al cerrar, se sube

def test_end_run_pushes_the_recording_when_the_server_answers_again(monkeypatch):
    monkeypatch.setattr(client_mod, "RUN_TOTAL_SECONDS", 1.0)
    calls = []

    def fake_run(cmd, **kw):
        calls.append((cmd, kw["env"]))
        return subprocess.CompletedProcess(cmd, 0, stdout="Report: http://srv/#run=42&view=tests\nrec: 9 events sent\n", stderr="")

    monkeypatch.setattr(client_mod, "resolve_binary", lambda *a, **k: "tracereports")
    monkeypatch.setattr(client_mod.subprocess, "run", only_push(fake_run))
    proxy = StallProxy()
    try:
        cr = TraceReports(proxy.url, token="tok", timeout=1, run_timeout=1)
        cr.start_run("corte breve")
        assert cr.recording, "the run could not be created: it records"
        cr.start_test("t")
        cr.end_test("PASS")
        proxy.recover()  # el servidor vuelve a responder
        cr.end_run()
        assert [c[0] for c in calls] == [["tracereports", "push", cr.offline_dir]], "the recording is pushed once"
        assert calls[0][1]["TRACEREPORTS_URL"] == proxy.url and calls[0][1]["TRACEREPORTS_TOKEN"] == "tok"
        assert cr.pushed_report_url == "http://srv/#run=42&view=tests"
    finally:
        proxy.close()


def test_no_push_while_the_server_still_does_not_answer(monkeypatch, caplog):
    monkeypatch.setattr(client_mod, "RUN_TOTAL_SECONDS", 1.0)
    monkeypatch.setattr(client_mod.subprocess, "run", only_push(lambda *a, **k: pytest.fail("pushed to a server that does not answer")))
    cr = TraceReports(f"http://127.0.0.1:{_closed_port()}", timeout=1, run_timeout=1)
    cr.start_run("sigue caído")
    with caplog.at_level(logging.WARNING, logger="tracereports"):
        cr.end_run()
    assert cr.pushed_report_url is None
    assert f"upload it later with `tracereports push {cr.offline_dir}`" in caplog.text


def test_push_can_be_turned_off(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_PUSH", "0")
    monkeypatch.setattr(client_mod, "RUN_TOTAL_SECONDS", 1.0)
    monkeypatch.setattr(client_mod.subprocess, "run", only_push(lambda *a, **k: pytest.fail("pushed with TRACEREPORTS_OFFLINE_PUSH=0")))
    proxy = StallProxy()
    try:
        cr = TraceReports(proxy.url, timeout=1, run_timeout=1)
        cr.start_run("sin subir")
        proxy.recover()
        cr.end_run()
        assert cr.pushed_report_url is None
    finally:
        proxy.close()


def test_offline_always_is_never_pushed(monkeypatch):
    """Grabar sin servidor a propósito (offline=always) no es un corte: no se sube solo."""
    monkeypatch.setattr(client_mod.subprocess, "run", only_push(lambda *a, **k: pytest.fail("pushed an offline=always recording")))
    proxy = StallProxy()
    proxy.recover()
    try:
        cr = TraceReports(proxy.url, offline="always")
        cr.start_run("a propósito")
        cr.end_run()
        assert cr.pushed_report_url is None
    finally:
        proxy.close()


# ------------------------------------------------------------------ de punta a punta, servidor real

@pytest.mark.skipif(not _bin(), reason="needs $TRACEREPORTS_BIN")
def test_real_server_behind_a_stalled_forward_gets_the_run(tmp_path, monkeypatch):
    """Un caso real con el servidor real: el reenvío se traba al crear la ejecución (el cliente
    pasa a local), se recupera durante los tests y, al cerrar, la ejecución llega con sus tests."""
    monkeypatch.setattr(client_mod, "RUN_TOTAL_SECONDS", 2.0)
    monkeypatch.setenv("TRACEREPORTS_BIN", _bin())
    port = _closed_port()
    env = dict(os.environ, PORT=str(port), DATA_DIR=str(tmp_path / "data"), TRACEREPORTS_ENV_FILE=str(tmp_path / "no.env"),
               TRACEREPORTS_TOKEN="tok-e2e", TRACEREPORTS_INGEST_TOKEN="", TRACEREPORTS_UI_USER="", TRACEREPORTS_UI_PASSWORD="",
               AI_PROVIDER="", TEAMS_WEBHOOK_URL="", SLACK_WEBHOOK_URL="")
    proc = subprocess.Popen([_bin()], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    url = f"http://127.0.0.1:{port}"
    proxy = None
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen(url + "/healthz", timeout=1)
                break
            except Exception:
                time.sleep(0.1)
        proxy = StallProxy(url)
        cr = TraceReports(proxy.url, token="tok-e2e", timeout=1, run_timeout=1)
        cr.start_run("shop/test_register_support_ticket_chrome.py", execution_id="suite-corte", detect_context=False)
        assert cr.recording, "the stalled forward made it record locally"
        proxy.recover()
        for name, status in (("test_paso_1", "PASS"), ("test_paso_2", "FAIL")):
            cr.start_test(name)
            cr.log_info("click en guardar")
            cr.end_test(status)
        cr.end_run()
        assert cr.pushed_report_url and "#run=" in cr.pushed_report_url, "uploaded at the end"
        req = urllib.request.Request(url + "/api/v1/runs", headers={"Authorization": "Bearer tok-e2e"})
        runs = json.loads(urllib.request.urlopen(req, timeout=5).read())
        runs = runs if isinstance(runs, list) else runs.get("runs", [])
        mine = [r for r in runs if r.get("name") == "shop/test_register_support_ticket_chrome.py"]
        assert len(mine) == 1, f"one run on the server: {mine}"
        assert mine[0]["execution_id"] == "suite-corte", "in its execution (Live groups it with the rest of the suite)"
        assert mine[0]["total"] == 2 and mine[0]["status"] != "RUNNING", mine[0]
        # y cuenta cómo llegó: creado tarde, por qué y a qué dirección conectó (Mantenimiento > Conexiones)
        req = urllib.request.Request(f"{url}/api/v1/runs/{mine[0]['id']}", headers={"Authorization": "Bearer tok-e2e"})
        d = json.loads(urllib.request.urlopen(req, timeout=5).read())["delivery"]
        assert d["late"] is True and d["reason"] == "the server did not answer within 2 s", d
        assert d["detail"].startswith("connected to 127.0.0.1:") and "no answer in" in d["detail"], d
        assert d["client"].startswith("python ") and d["host"], d
        req = urllib.request.Request(f"{url}/api/v1/maintenance/connections", headers={"Authorization": "Bearer tok-e2e"})
        c = json.loads(urllib.request.urlopen(req, timeout=5).read())
        assert [r["id"] for r in c["runs"]] == [mine[0]["id"]], c
    finally:
        if proxy:
            proxy.close()
        proc.terminate()
        proc.wait(10)


# ------------------------------------------------------------------ el plugin de pytest

pytest_plugins = ["pytester"]


def test_plugin_says_the_run_was_uploaded_at_the_end(pytester, monkeypatch):
    monkeypatch.setattr(client_mod, "RUN_TOTAL_SECONDS", 1.0)
    monkeypatch.setattr(client_mod, "resolve_binary", lambda *a, **k: "tracereports")
    monkeypatch.setattr(client_mod.subprocess, "run", only_push(
        lambda cmd, **kw: subprocess.CompletedProcess(cmd, 0, stdout="Report: http://srv/#run=42&view=tests\n", stderr="")))
    proxy = StallProxy()
    monkeypatch.setenv("RECOVER_URL", proxy.url + "/__recover")
    try:
        pytester.makepyfile("""
            import os, urllib.request

            def test_la_red_vuelve():
                urllib.request.urlopen(os.environ["RECOVER_URL"], timeout=5)
        """)
        result = pytester.runpytest_inprocess("--tracereports", "--tracereports-url", proxy.url, "-p", "no:cacheprovider")
        result.assert_outcomes(passed=1)
        result.stdout.fnmatch_lines(["*TraceReports (uploaded at the end): http://srv/#run=42&view=tests*"])
    finally:
        proxy.close()


def test_delivery_report_only_when_something_went_wrong(monkeypatch):
    sent = []
    cr = TraceReports("http://127.0.0.1:1", timeout=1, run_timeout=1)
    monkeypatch.setattr(cr, "_request", lambda method, path, payload, *a, **k: sent.append((path, payload)) or {"ok": True})
    cr.run_id = 5
    cr.end_run()
    assert sent[-1] == ("/api/v1/runs/5/finish", {"interrupted": False}), "nada que reportar: el cierre de siempre"
    cr._sender.stats["retried"] = 3
    cr.end_run(run_id=5)
    report = sent[-1][1]["delivery"]
    assert report["retried"] == 3 and "rejected" not in report and report["client"] == f"python {client_mod.__version__}"
    assert "late" not in report
