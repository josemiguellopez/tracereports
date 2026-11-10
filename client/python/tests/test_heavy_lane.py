"""La evidencia pesada (red, DOM, consola, adjuntos) no deja esperando lo que cambia el resultado de
un test, viaja comprimida si el servidor lo acepta y cada intento espera según su tamaño.

El caso del cliente: 16 workers subiendo lotes de red de varios MB por una red saturada; cada lote
tardaba más que el tiempo de espera, se volvía a subir entero, y el cierre de los tests quedaba detrás
en la cola. Al terminar, end_run cerraba la ejecución con esos tests todavía "en curso": Interrumpidos.

    pytest client/python/tests/test_heavy_lane.py
"""

import gzip
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import tracereports.transport as tr
from tracereports.transport import Sender


class LaneServer:
    """Anota el orden de llegada; /network puede esperar (red lenta) o quedar retenida hasta release()."""

    def __init__(self, gzip_ok=True, network_delay=0.0, hold_first_network=False):
        self.got = []  # (path, content-encoding, body descomprimido)
        self.lock = threading.Lock()
        self.gate = threading.Event()
        self.holding = threading.Event()
        outer = self
        held = [not hold_first_network]

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _handle(self):
                raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                enc = self.headers.get("Content-Encoding") or ""
                body = gzip.decompress(raw) if enc == "gzip" else raw
                if self.path.endswith("/network"):
                    if not held[0]:
                        held[0] = True
                        outer.holding.set()
                        outer.gate.wait(10)
                    elif network_delay:
                        time.sleep(network_delay)
                with outer.lock:
                    outer.got.append((self.path, enc, body))
                out = json.dumps({}).encode()
                self.send_response(201)
                if gzip_ok:
                    self.send_header(tr.ACCEPT_ENCODING_HEADER, "gzip")
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(out)))
                self.end_headers()
                self.wfile.write(out)

            do_POST = do_PATCH = _handle

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"

    def paths(self):
        with self.lock:
            return [p for p, _, _ in self.got]

    def close(self):
        self.gate.set()
        self.httpd.shutdown()
        self.httpd.server_close()


def _json(n):
    return json.dumps({"n": n, "pad": "x" * n}).encode()


def test_test_results_overtake_heavy_evidence():
    srv = LaneServer(hold_first_network=True)
    try:
        s = Sender(srv.url, lambda h: h)
        s.enqueue("POST", "/api/v1/tests/1/network", _json(10), "application/json", 10)
        assert srv.holding.wait(5)  # el primer lote está subiendo
        s.enqueue("POST", "/api/v1/tests/1/network", _json(20), "application/json", 10)
        s.enqueue("POST", "/api/v1/tests/1/dom", _json(30), "application/json", 10)
        s.enqueue("POST", "/api/v1/tests/1/logs", _json(1), "application/json", 10)
        s.enqueue("PATCH", "/api/v1/tests/1/finish", _json(2), "application/json", 10)
        srv.gate.set()
        assert s.flush(10) == 0
        assert srv.paths() == ["/api/v1/tests/1/network", "/api/v1/tests/1/logs", "/api/v1/tests/1/finish",
                               "/api/v1/tests/1/network", "/api/v1/tests/1/dom"]
    finally:
        srv.close()


def test_a_slow_upload_does_not_hold_the_test_finish(monkeypatch):
    # cada intento de /network vence (la red tarda 2 s y la espera es 0.3 s): el cierre del test no espera
    monkeypatch.setattr(tr, "UPLOAD_MIN_BYTES_PER_S", 1 << 40)
    srv = LaneServer(network_delay=2.0)
    try:
        s = Sender(srv.url, lambda h: h)
        s.enqueue("POST", "/api/v1/tests/1/network", _json(10), "application/json", 0.3)
        time.sleep(0.05)
        start = time.monotonic()
        s.enqueue("PATCH", "/api/v1/tests/1/finish", _json(2), "application/json", 5)
        while "/api/v1/tests/1/finish" not in srv.paths() and time.monotonic() - start < 5:
            time.sleep(0.02)
        assert "/api/v1/tests/1/finish" in srv.paths()
        assert time.monotonic() - start < 1.5, "the finish waited behind the slow upload's backoff"
    finally:
        srv.close()


def test_gzip_only_after_the_server_says_so(monkeypatch):
    monkeypatch.delenv("TRACEREPORTS_GZIP", raising=False)
    big = _json(64 << 10)
    srv = LaneServer(gzip_ok=True)
    try:
        s = Sender(srv.url, lambda h: h)
        assert s.send_now("POST", "/api/v1/runs", b"{}", "application/json", 5) is not None  # aprende
        s.enqueue("POST", "/api/v1/tests/1/network", big, "application/json", 10)
        s.enqueue("POST", "/api/v1/tests/1/logs", _json(1), "application/json", 10)  # chico: sin comprimir
        assert s.flush(10) == 0
        enc = {p: (e, b) for p, e, b in srv.got}
        assert enc["/api/v1/runs"][0] == ""
        assert enc["/api/v1/tests/1/network"] == ("gzip", big)
        assert enc["/api/v1/tests/1/logs"][0] == ""
    finally:
        srv.close()


def test_no_gzip_for_an_older_server_or_when_turned_off(monkeypatch):
    big = _json(64 << 10)
    old = LaneServer(gzip_ok=False)
    try:
        s = Sender(old.url, lambda h: h)
        s.send_now("POST", "/api/v1/runs", b"{}", "application/json", 5)
        s.enqueue("POST", "/api/v1/tests/1/network", big, "application/json", 10)
        assert s.flush(10) == 0
        assert all(e == "" for _, e, _ in old.got)
    finally:
        old.close()
    monkeypatch.setenv("TRACEREPORTS_GZIP", "0")
    srv = LaneServer(gzip_ok=True)
    try:
        s = Sender(srv.url, lambda h: h)
        s.send_now("POST", "/api/v1/runs", b"{}", "application/json", 5)
        s.enqueue("POST", "/api/v1/tests/1/network", big, "application/json", 10)
        assert s.flush(10) == 0
        assert all(e == "" for _, e, _ in srv.got)
    finally:
        srv.close()


def test_queued_upload_waits_by_its_size(monkeypatch):
    seen = []

    class Resp:
        headers = {}

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

        def read(self):
            return b"{}"

    monkeypatch.setattr(tr._opener, "open", lambda req, timeout: seen.append(timeout) or Resp())
    s = Sender("http://127.0.0.1:1", lambda h: h)
    s.gzip_enabled = False
    item = tr._Item("POST", "/api/v1/tests/1/network", b"x" * (2 << 20), "application/json", 10)
    s._post(item, scale=True)
    s._post(tr._Item("POST", "/api/v1/tests/1/logs", b"{}", "application/json", 10), scale=True)
    assert seen[0] >= 10 + (2 << 20) / tr.UPLOAD_MIN_BYTES_PER_S - 0.01
    assert seen[1] < 10.01
