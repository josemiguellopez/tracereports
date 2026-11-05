"""Runs fantasma: el servidor tarda más que el timeout del cliente en crear la ejecución.

Antes, el timeout abría el circuito, el reintento no salía, el cliente pasaba a grabar en local y la
ejecución que el servidor sí creó quedaba RUNNING con 0 tests. Ahora el reintento (misma
Idempotency-Key) recibe el mismo run_id, todo con un tope total, y un servidor caído (conexión
rechazada o connect vencido) sigue pasando a local al instante.
"""

import json
import os
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
from tracereports.transport import ConnectTimeout, Sender, _Item, _is_timeout


class SlowRunServer:
    """Imita al servidor: tarda ``delay`` s en crear la ejecución y, como idemLocks, serializa las
    requests con la misma Idempotency-Key y devuelve la respuesta guardada (el mismo run_id)."""

    def __init__(self, delay=3.0):
        self.delay = delay
        self.posts = []           # Idempotency-Key de cada POST /api/v1/runs recibido
        self.created = []         # run_id creados (uno por clave nueva)
        self.answers = []         # run_id respondido a cada POST
        self.finished = []        # run_id cerrados
        self.lock = threading.Lock()
        self.key_locks = {}
        self.stored = {}          # Idempotency-Key -> respuesta
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _reply(self, status, out):
                raw = json.dumps(out).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                try:
                    self.wfile.write(raw)
                except OSError:
                    pass  # el cliente ya se fue por timeout: el servidor igual guardó la respuesta

            def do_POST(self):
                self.rfile.read(int(self.headers.get("Content-Length") or 0))
                key = self.headers.get("Idempotency-Key") or ""
                if self.path != "/api/v1/runs":
                    return self._reply(201, {"test_id": 11})
                with outer.lock:
                    outer.posts.append(key)
                    kl = outer.key_locks.setdefault(key, threading.Lock())
                with kl:
                    if key not in outer.stored:
                        time.sleep(outer.delay)
                        with outer.lock:
                            rid = len(outer.created) + 1
                            outer.created.append(rid)
                            outer.stored[key] = {"run_id": rid}
                    with outer.lock:
                        outer.answers.append(outer.stored[key]["run_id"])
                    return self._reply(201, outer.stored[key])

            def do_PATCH(self):
                self.rfile.read(int(self.headers.get("Content-Length") or 0))
                if self.path.startswith("/api/v1/runs/") and self.path.endswith("/finish"):
                    with outer.lock:
                        outer.finished.append(int(self.path.split("/")[4]))
                self._reply(200, {})

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


class SilentServer:
    """Acepta conexiones (el kernel completa el handshake en el backlog) y nunca responde."""

    def __init__(self):
        self.sock = socket.socket()
        self.sock.bind(("127.0.0.1", 0))
        self.sock.listen(64)
        self.url = f"http://127.0.0.1:{self.sock.getsockname()[1]}"

    def close(self):
        self.sock.close()


@pytest.fixture(autouse=True)
def clean_env(monkeypatch, tmp_path):
    for var in ("TRACEREPORTS_TIMEOUT", "TRACEREPORTS_RUN_TIMEOUT", "TRACEREPORTS_SPOOL_DIR", "TRACEREPORTS_SYNC",
                "TRACEREPORTS_DISABLED", "TRACEREPORTS_TOKEN", "TRACEREPORTS_OFFLINE", "TRACEREPORTS_OFFLINE_DIR"):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_BASE", str(tmp_path / "offline"))
    monkeypatch.chdir(tmp_path)  # sin .env del proyecto
    reset_env_file()
    yield
    reset_env_file()


@pytest.fixture
def slow_server():
    srv = SlowRunServer(delay=3.0)
    yield srv
    srv.close()


@pytest.fixture
def silent_server():
    srv = SilentServer()
    yield srv
    srv.close()


def _closed_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


# ------------------------------------------------------------------ el bug (servidor lento)

def test_slow_run_creation_is_not_a_ghost_run(slow_server):
    """El caso del cliente: timeout=2 y el servidor tarda 3 s en POST /api/v1/runs."""
    cr = TraceReports(slow_server.url, timeout=2)
    rid = cr.start_run("Regresión lenta")
    assert rid and rid > 0, "the run must be created on the server"
    assert not cr.recording, "a slow server is not a down server: no offline mode"
    assert slow_server.created == [rid], "a single run created"
    cr.end_run()
    assert slow_server.finished == [rid], "the run created on the server is closed"


def test_timeout_retry_reuses_the_key_and_gets_the_same_run_id(slow_server):
    """3.1 sola: con 2 s también para crear la ejecución, el primer intento vence; el reintento
    lleva la misma Idempotency-Key y recibe el mismo run_id (un solo run en el servidor)."""
    cr = TraceReports(slow_server.url, timeout=2, run_timeout=2)
    rid = cr.start_run("Regresión lenta")
    assert rid and rid > 0 and not cr.recording
    assert len(slow_server.posts) >= 2, "the first attempt timed out and was retried"
    assert len(set(slow_server.posts)) == 1, "every attempt carried the same Idempotency-Key"
    assert slow_server.created == [rid], "a single run created"
    assert set(slow_server.answers) == {rid}, "every attempt was answered with the same run_id"
    cr.end_run()
    assert slow_server.finished == [rid]


# ---------------------------------------------------------- tope total (acepta y no responde)

def test_run_creation_has_a_total_cap_with_a_silent_server(silent_server, monkeypatch):
    """Un servidor que acepta y no responde no frena cada proceso retries × run_timeout."""
    monkeypatch.setattr(client_mod, "RUN_TOTAL_SECONDS", 3.0)
    cr = TraceReports(silent_server.url, timeout=2, run_timeout=2)
    t0 = time.perf_counter()
    rid = cr.start_run("Servidor colgado")
    elapsed = time.perf_counter() - t0
    assert 2.9 < elapsed < 4.0, f"retried until the cap and no longer ({elapsed:.2f}s; without the cap 6 s)"
    assert cr.recording and rid is not None and rid < 0, "after the cap it records locally"


def test_total_cap_defaults():
    assert client_mod.RUN_TOTAL_SECONDS == 15.0
    assert TraceReports("http://127.0.0.1:1")._run_total() == 15.0, "10 s per attempt, 15 s in total"
    assert TraceReports("http://127.0.0.1:1", run_timeout=25)._run_total() == 25.0, "always at least one attempt"


def test_sender_total_shortens_the_last_attempt(silent_server):
    s = Sender(silent_server.url, lambda h: h)
    t0 = time.perf_counter()
    assert s.send_now("POST", "/api/v1/runs", b"{}", "application/json", 2, total=2.5) is None
    elapsed = time.perf_counter() - t0
    assert 2.4 < elapsed < 3.2, f"2 s + the 0.5 s left, not 3 × 2 s ({elapsed:.2f}s)"
    assert s.server_down(), "after the cap the circuit opens"


# ------------------------------------------- timeout al conectar vs. esperando la respuesta

def test_response_timeout_is_a_timeout_and_does_not_open_the_circuit(silent_server):
    s = Sender(silent_server.url, lambda h: h)
    kind, _ = s._post(_Item("POST", "/api/v1/runs", b"{}", "application/json", 0.5), trip_on_timeout=False)
    assert kind == "timeout"
    assert not s.server_down(), "the caller retries first"


def test_connect_timeout_is_a_down_server(monkeypatch):
    """El connect vence (host que no contesta el SYN; en Windows, un puerto cerrado de localhost).
    Se distingue por el tipo de excepción, no por el texto: ambos errores dicen "timed out"."""
    def no_answer(*a, **kw):
        raise socket.timeout("timed out")
    monkeypatch.setattr(socket, "create_connection", no_answer)
    s = Sender("http://127.0.0.1:9", lambda h: h)
    kind, _ = s._post(_Item("POST", "/api/v1/runs", b"{}", "application/json", 2), trip_on_timeout=False)
    assert kind == "retry" and s.server_down(), "opens the circuit at once"
    s._down_until = 0.0
    assert s.send_now("POST", "/api/v1/runs", b"{}", "application/json", 2) is None
    assert s.stats["retried"] == 0, "a connect timeout is not retried"


def test_is_timeout_uses_exception_types_only():
    same_text = "timed out"
    assert _is_timeout(socket.timeout(same_text))
    assert _is_timeout(TimeoutError(same_text))
    assert _is_timeout(urllib.error.URLError(socket.timeout(same_text)))
    assert not _is_timeout(ConnectTimeout(same_text))
    assert not _is_timeout(urllib.error.URLError(ConnectTimeout(same_text)))
    assert not _is_timeout(ConnectionRefusedError(same_text))
    assert not _is_timeout(urllib.error.URLError(same_text)), "a plain message is not a timeout"
    assert not _is_timeout(OSError(same_text))


# --------------------------------------------------------------------------- servidor caído

def test_server_down_goes_offline_fast():
    cr = TraceReports(f"http://127.0.0.1:{_closed_port()}", timeout=2)
    t0 = time.perf_counter()
    rid = cr.start_run("Sin servidor")
    elapsed = time.perf_counter() - t0
    assert cr.recording and rid is not None and rid < 0, "records locally without a server"
    # un solo intento: en Linux el rechazo es inmediato; en Windows un connect a un puerto cerrado
    # de localhost tarda ~2 s (reintenta el SYN)
    assert elapsed < 4.0, f"offline must be fast ({elapsed:.2f}s)"


def test_connection_refused_opens_the_circuit_for_later_calls():
    s = Sender(f"http://127.0.0.1:{_closed_port()}", lambda h: h)
    assert s.send_now("POST", "/api/v1/runs", b"{}", "application/json", 2) is None
    assert s.server_down(), "a refused connection opens the circuit"
    t0 = time.perf_counter()
    assert s.send_now("POST", "/api/v1/runs/1/tests", b"{}", "application/json", 2) is None
    assert time.perf_counter() - t0 < 0.05, "with the circuit open the next call fails at once"
    assert s.stats["retried"] == 0, "a refused connection is not retried"


# ------------------------------------------------------------------------------- variables

def test_timeouts_come_from_the_environment(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_TIMEOUT", "7")
    cr = TraceReports("http://127.0.0.1:1")
    assert cr.timeout == 7.0
    assert cr.run_timeout == 10.0, "creating/closing the run keeps at least 10 s"
    monkeypatch.setenv("TRACEREPORTS_RUN_TIMEOUT", "25")
    assert TraceReports("http://127.0.0.1:1").run_timeout == 25.0
    monkeypatch.setenv("TRACEREPORTS_TIMEOUT", "15")
    monkeypatch.delenv("TRACEREPORTS_RUN_TIMEOUT")
    assert TraceReports("http://127.0.0.1:1").run_timeout == 15.0, "never shorter than the general timeout"


def test_default_timeouts_and_explicit_arguments_win(monkeypatch):
    cr = TraceReports("http://127.0.0.1:1")
    assert cr.timeout == 2.0 and cr.run_timeout == 10.0
    monkeypatch.setenv("TRACEREPORTS_TIMEOUT", "7")
    monkeypatch.setenv("TRACEREPORTS_RUN_TIMEOUT", "25")
    cr = TraceReports("http://127.0.0.1:1", timeout=3, run_timeout=4)
    assert cr.timeout == 3 and cr.run_timeout == 4


# ------------------------------------------------------------------------- servidor real

class DelayProxy:
    """Proxy delante del servidor real: reenvía cada request, y a la primera POST /api/v1/runs le
    retiene la respuesta ``delay`` s (el servidor ya creó la ejecución; el cliente se va por timeout)."""

    def __init__(self, target, delay=3.0):
        self.keys = []
        self.lock = threading.Lock()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _forward(self):
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "content-length", "connection")}
                req = urllib.request.Request(target + self.path, data=body or None, method=self.command, headers=headers)
                try:
                    with urllib.request.urlopen(req, timeout=30) as resp:
                        status, raw = resp.status, resp.read()
                except urllib.error.HTTPError as err:
                    status, raw = err.code, err.read()
                if self.command == "POST" and self.path == "/api/v1/runs":
                    with outer.lock:
                        outer.keys.append(self.headers.get("Idempotency-Key"))
                        first = len(outer.keys) == 1
                    if first:
                        time.sleep(delay)
                try:
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except OSError:
                    pass

            do_GET = do_POST = do_PATCH = _forward

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


@pytest.mark.skipif(not (os.environ.get("TRACEREPORTS_BIN") or os.environ.get("TRACEREPORTS_TEST_BIN")), reason="needs $TRACEREPORTS_BIN")
def test_real_server_slow_create_run_gives_one_run(tmp_path):
    """Contra el servidor real (su idempotencia de verdad): el primer intento vence, el reintento
    recibe la ejecución guardada y en el servidor queda una sola, cerrada (no RUNNING)."""
    port = _closed_port()
    env = dict(os.environ, PORT=str(port), DATA_DIR=str(tmp_path / "data"), TRACEREPORTS_ENV_FILE=str(tmp_path / "no.env"),
               TRACEREPORTS_TOKEN="", TRACEREPORTS_INGEST_TOKEN="", TRACEREPORTS_UI_USER="", TRACEREPORTS_UI_PASSWORD="",
               AI_PROVIDER="", TEAMS_WEBHOOK_URL="", SLACK_WEBHOOK_URL="")
    bin_path = os.environ.get("TRACEREPORTS_BIN") or os.environ["TRACEREPORTS_TEST_BIN"]
    proc = subprocess.Popen([bin_path], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    url = f"http://127.0.0.1:{port}"
    proxy = None
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen(url + "/api/v1/runs", timeout=1)
                break
            except Exception:
                time.sleep(0.1)
        proxy = DelayProxy(url, delay=3.0)
        cr = TraceReports(proxy.url, timeout=2, run_timeout=2)
        rid = cr.start_run("proxy slow create")
        assert rid and rid > 0 and not cr.recording
        assert len(proxy.keys) >= 2 and len(set(proxy.keys)) == 1, "retried with the same Idempotency-Key"
        tid = cr.start_test("t")
        cr.end_test("PASS", test_id=tid)
        cr.end_run()
        runs = json.loads(urllib.request.urlopen(url + "/api/v1/runs", timeout=5).read())
        runs = runs if isinstance(runs, list) else runs.get("runs", [])
        mine = [r for r in runs if r.get("name") == "proxy slow create"]
        assert [r["id"] for r in mine] == [rid], "a single run on the real server, the one the client got"
        assert mine[0]["status"] != "RUNNING", "and it was closed"
    finally:
        if proxy:
            proxy.close()
        proc.terminate()
        proc.wait(10)
