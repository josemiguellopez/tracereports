"""Pruebas del transporte del cliente: reintentos, idempotencia, caídas, cola llena y spool.

    pip install pytest && pytest client/python/tests
"""

import json
import os
import socket
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from tracereports import TraceReports
from tracereports.transport import Sender


class FakeServer:
    """Servidor de TraceReports mínimo: guarda lo que recibe y puede fallar a propósito."""

    def __init__(self, port=0, fail_first=0):
        self.requests = []  # (method, path, idempotency key, body)
        self.fail_first = fail_first
        self.lock = threading.Lock()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _handle(self):
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                with outer.lock:
                    if outer.fail_first > 0:
                        outer.fail_first -= 1
                        self.send_response(503)
                        self.end_headers()
                        return
                    outer.requests.append((self.command, self.path, self.headers.get("Idempotency-Key"), body))
                out = {}
                if self.path == "/api/v1/runs":
                    out = {"run_id": 7}
                elif self.path.endswith("/tests"):
                    out = {"test_id": 11}
                raw = json.dumps(out).encode()
                self.send_response(201)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            do_POST = do_PATCH = _handle

        self.httpd = ThreadingHTTPServer(("127.0.0.1", port), Handler)
        self.port = self.httpd.server_address[1]
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    @property
    def url(self):
        return f"http://127.0.0.1:{self.port}"

    def messages(self):
        return [json.loads(b)["message"] for m, p, k, b in self.requests if p.endswith("/logs")]

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


@pytest.fixture(autouse=True)
def fast_backoff(monkeypatch):
    import tracereports.transport as tr
    monkeypatch.setattr(tr, "MAX_BACKOFF", 0.2)
    monkeypatch.setattr(tr, "CIRCUIT_SECONDS", 0.5)
    for var in ("TRACEREPORTS_SPOOL_DIR", "TRACEREPORTS_SYNC", "TRACEREPORTS_DISABLED", "TRACEREPORTS_TOKEN"):
        monkeypatch.delenv(var, raising=False)


def test_retries_keep_order_and_idempotency_key():
    srv = FakeServer(fail_first=3)
    try:
        cr = TraceReports(srv.url)
        cr.run_id = 7
        for i in range(20):
            cr.log_info(f"paso {i}", test_id=11)
        assert cr.flush(10) == 0
        assert srv.messages() == [f"paso {i}" for i in range(20)], "no evidence lost, order kept"
        keys = [k for m, p, k, b in srv.requests]
        assert len(set(keys)) == 20 and all(keys), "one idempotency key per event"
        assert cr.delivery["retried"] >= 1 and cr.delivery["sent"] == 20
    finally:
        srv.close()


def test_server_down_then_up_delivers_everything():
    port = free_port()
    cr = TraceReports(f"http://127.0.0.1:{port}", timeout=0.3)
    cr.run_id = 7
    t0 = time.perf_counter()
    for i in range(50):
        cr.log_info(f"paso {i}", test_id=11)
    elapsed = time.perf_counter() - t0
    assert elapsed < 0.5, f"queuing must not block the test while the server is down ({elapsed:.2f}s)"
    time.sleep(0.5)
    srv = FakeServer(port=port)  # el servidor vuelve
    try:
        assert cr.flush(15) == 0
        assert srv.messages() == [f"paso {i}" for i in range(50)]
    finally:
        srv.close()


def test_full_queue_drops_new_events_and_reports_it():
    port = free_port()
    cr = TraceReports(f"http://127.0.0.1:{port}", timeout=0.2, max_queue_items=5)
    cr.run_id = 7
    for i in range(12):
        cr.log_info(f"paso {i}", test_id=11)
    d = cr.delivery
    assert d["dropped"] >= 6 and d["pending"] <= 5
    assert cr.delivery_problems() > 0


def test_unsent_events_go_to_spool_and_resend(tmp_path):
    port = free_port()
    cr = TraceReports(f"http://127.0.0.1:{port}", timeout=0.2, spool_dir=str(tmp_path), flush_timeout=0.5)
    cr.run_id = 7
    cr.log_info("antes de la caída", test_id=11)
    cr.attach_screenshot(b"\x89PNG fake", "captura", test_id=11)
    cr.end_run()  # el servidor no responde: queda en el spool
    assert cr.delivery["spooled"] == 3  # paso + captura + cierre de la ejecución
    srv = FakeServer(port=port)
    try:
        s = Sender(srv.url, lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
        assert s.load_spool() == 3 and s.flush(10) == 0
        assert os.listdir(tmp_path) == []
        paths = [p for m, p, k, b in srv.requests]
        assert paths[0].endswith("/logs") and paths[1].endswith("/screenshot") and paths[2] == "/api/v1/runs/7/finish"
    finally:
        srv.close()


def test_client_identity_context_and_expected_responses(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_BRANCH", "feature/x")
    monkeypatch.setenv("TRACEREPORTS_COMMIT", "abc123")
    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw3")
    srv = FakeServer()
    try:
        cr = TraceReports(srv.url)
        assert cr.start_run("Suite", environment="qa", project="shop") == 7
        tid = cr.start_test("Login", key="tests/test_a.py::test_login", suite="tests/test_a.py")
        cr.expect_response(401, url="*/api/auth*", test_id=tid)
        res = cr.attach_network([
            {"method": "POST", "url": "https://app/api/auth", "status": 401},
            {"method": "GET", "url": "https://app/api/me", "status": 500},
        ], test_id=tid)
        cr.end_test("PASS", attempts=2, test_id=tid)
        cr.end_run()
        assert res == {"stored": 2, "errors": 1}
        bodies = {p: json.loads(b) for m, p, k, b in srv.requests}
        run = bodies["/api/v1/runs"]
        assert run["project"] == "shop" and run["branch"] == "feature/x" and run["commit"] == "abc123"
        test = bodies["/api/v1/runs/7/tests"]
        assert test["key"] == "tests/test_a.py::test_login" and test["worker"] == "gw3"
        conns = bodies["/api/v1/tests/11/network"]["connections"]
        assert [c["expected"] for c in conns] == [True, False]
        assert bodies["/api/v1/tests/11/finish"]["attempts"] == 2
        assert bodies["/api/v1/runs/7/finish"] == {"interrupted": False}
        assert [p for m, p, k, b in srv.requests][-1] == "/api/v1/runs/7/finish", "the run closes after the queue is flushed"
    finally:
        srv.close()


# ─── spool: durabilidad y límites ───

def _items_in(folder):
    out = []
    for name in os.listdir(folder):
        with open(os.path.join(folder, name), encoding="utf-8") as fh:
            out += [json.loads(line)["path"] for line in fh if line.strip()]
    return out


def test_drain_while_sending_never_loses_another_event(tmp_path):
    """A en vuelo + drenado + B encolado: B se envía y A queda a salvo (enviado y en el spool)."""
    s = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    started, release, posted = threading.Event(), threading.Event(), []

    def fake_post(item):
        posted.append(item.path)
        if item.path == "/A":
            started.set()
            release.wait(5)
        return "ok", {}

    s._post = fake_post
    s.enqueue("POST", "/A", b"a", "application/json", 1)
    assert started.wait(5)
    s.drain_to_spool()                       # se lleva A (en vuelo) al spool
    s.enqueue("POST", "/B", b"b", "application/json", 1)
    release.set()
    assert s.flush(5) == 0
    assert posted == ["/A", "/B"], "B must be sent, not silently removed"
    assert _items_in(tmp_path) == ["/A"], "A was in flight when drained: it is kept in the spool"
    assert s.stats["sent"] == 2 and s.stats["lost"] == 0
    assert s._thread.is_alive(), "the worker must survive"


def test_drain_while_sending_without_spool_counts_it(tmp_path):
    s = Sender("http://unused", lambda extra: dict(extra or {}))
    started, release = threading.Event(), threading.Event()

    def fake_post(item):
        started.set()
        release.wait(5)
        return "retry", None  # el envío en curso falla

    s._post = fake_post
    s.enqueue("POST", "/A", b"a", "application/json", 1)
    assert started.wait(5)
    assert s.drain_to_spool() == 1 and s.stats["lost"] == 1, "nothing disappears without being counted"
    release.set()
    assert s.flush(2) == 0 and s.pending == 0


ONE_SPOOL = "tracereports_spool_1700000000000_000000_1-a_0000.jsonl"


def _spool_one(folder, path="/api/v1/tests/1/logs"):
    from tracereports.transport import _Item
    with open(os.path.join(folder, ONE_SPOOL), "w", encoding="utf-8") as fh:
        fh.write(_Item("POST", path, b"{}", "application/json", 1).to_json() + "\n")


def test_spool_file_survives_until_delivery(tmp_path, monkeypatch):
    _spool_one(tmp_path)
    s = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    monkeypatch.setattr(s, "_ensure_thread", lambda: None)  # nada se entrega
    assert s.load_spool() == 1 and s.pending == 1
    assert len(os.listdir(tmp_path)) == 1, "the durable copy must stay while the event is not delivered"

    # el proceso "muere": otra instancia retoma el archivo reclamado cuando queda viejo
    import tracereports.transport as tr
    monkeypatch.setattr(tr, "SPOOL_STALE_SECONDS", 0.0)
    srv = FakeServer()
    try:
        s2 = Sender(srv.url, lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
        time.sleep(0.05)
        assert s2.load_spool() == 1 and s2.flush(5) == 0
        assert os.listdir(tmp_path) == [], "the file is removed only after the event was delivered"
        assert len(srv.requests) == 1
    finally:
        srv.close()


def test_spool_respects_queue_limits_and_concurrent_consumers(tmp_path):
    s = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path), max_items=0)
    _spool_one(tmp_path)
    assert s.load_spool() == 0 and s.pending == 0, "max_items must also apply to the spool"
    assert os.listdir(tmp_path) == [ONE_SPOOL], "a file that does not fit is left as it was"

    # un spool grande se escribe en tandas y dos consumidores no toman el mismo archivo
    big = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path / "big"))
    for i in range(450):
        big._q.append(__import__("tracereports.transport", fromlist=["_Item"])._Item("POST", f"/e{i}", b"x", "application/json", 1))
    big.drain_to_spool()
    assert len(os.listdir(tmp_path / "big")) == 3
    a = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path / "big"), max_items=250)
    b = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path / "big"), max_items=1000)
    a._ensure_thread = b._ensure_thread = lambda: None
    na, nb = a.load_spool(), b.load_spool()
    assert na <= 250 and na + nb == 450, (na, nb)


# ─── spool: archivos grandes, orden y concurrencia ───

def _big_spool(folder, n, name="tracereports_spool_1700000000000_000000_4242-a_0000.jsonl"):
    """Un solo archivo con n eventos (más de los que caben de una vez en la cola)."""
    from tracereports.transport import _Item
    os.makedirs(folder, exist_ok=True)
    with open(os.path.join(folder, name), "w", encoding="utf-8") as fh:
        for i in range(n):
            fh.write(_Item("POST", f"/api/v1/tests/1/logs?e={i}", b"{}", "application/json", 1).to_json() + "\n")


def test_drains_in_the_same_millisecond_never_overwrite(tmp_path, monkeypatch):
    import tracereports.transport as tr
    monkeypatch.setattr(tr.time, "time", lambda: 1_700_000_000.123)
    s = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    s._ensure_thread = lambda: None
    s.enqueue("POST", "/A", b"a", "application/json", 1)
    s.drain_to_spool()
    s.enqueue("POST", "/B", b"b", "application/json", 1)
    s.drain_to_spool()
    assert sorted(_items_in(tmp_path)) == ["/A", "/B"], "both drains must survive"
    assert s.stats["spooled"] == 2 and s.stats["lost"] == 0
    # y se reenvían en el orden en que se guardaron
    r = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    r._ensure_thread = lambda: None
    r.load_spool()
    assert [it.path for it in r._q] == ["/A", "/B"]


def test_old_file_just_claimed_is_not_taken_by_another_instance(tmp_path, monkeypatch):
    _spool_one(tmp_path)
    old = time.time() - 3600
    f = os.path.join(tmp_path, os.listdir(tmp_path)[0])
    os.utime(f, (old, old))
    a = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    b = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    a._ensure_thread = b._ensure_thread = lambda: None
    assert a.load_spool() == 1
    assert b.load_spool() == 0 and b.spool_report["busy"] == 1, "a live claim is not stale"
    # el dueño murió: nadie renueva el claim y, pasado el umbral, otra instancia lo retoma
    claimed = os.path.join(tmp_path, os.listdir(tmp_path)[0])
    os.utime(claimed, (old, old))
    c = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    c._ensure_thread = lambda: None
    assert c.load_spool() == 1


def test_big_file_is_loaded_in_batches_and_keeps_the_rest(tmp_path):
    _big_spool(tmp_path, 200)
    s = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path), max_items=100)
    s._ensure_thread = lambda: None
    assert s.load_spool() == 100 and s.pending == 100
    assert sum(1 for _ in _items_in(tmp_path)) == 200, "loaded part (claimed) + durable rest"
    order = [it.path for it in s._q]
    assert order == [f"/api/v1/tests/1/logs?e={i}" for i in range(100)]


def test_resend_cli_sends_a_spool_file_bigger_than_the_queue(tmp_path, capsys):
    from tracereports import resend
    _big_spool(tmp_path, 5001)
    srv = FakeServer()
    try:
        assert resend.main([str(tmp_path), "--url", srv.url, "--timeout", "120"]) == 0
        sent = [p for m, p, k, b in srv.requests]
        assert sent == [f"/api/v1/tests/1/logs?e={i}" for i in range(5001)], "all events, in order"
        assert os.listdir(tmp_path) == []
    finally:
        srv.close()


def test_resend_cli_does_not_announce_success_when_evidence_remains(tmp_path, capsys):
    from tracereports import resend
    import tracereports.transport as tr
    _big_spool(tmp_path, 3)
    code = resend.main([str(tmp_path), "--url", "http://127.0.0.1:9", "--timeout", "1"])
    out = capsys.readouterr().out
    assert code == 1 and "No hay eventos pendientes" not in out and "Quedan" in out
    assert len(_items_in(tmp_path)) == 3, "nothing lost"


def test_chunks_of_one_drain_are_resent_in_order(tmp_path):
    s = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    s._ensure_thread = lambda: None
    for i in range(450):
        s.enqueue("POST", f"/e{i}", b"x", "application/json", 1)
    s.drain_to_spool()
    assert len(os.listdir(tmp_path)) == 3
    r = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path))
    r._ensure_thread = lambda: None
    r.load_spool()
    assert [it.path for it in r._q] == [f"/e{i}" for i in range(450)]


# ─── Carga del spool con productores concurrentes ───

def _blocking(monkeypatch, target, attr):
    """Bloquea target.attr la primera vez que se llama hasta que el test lo libera (sin sleeps)."""
    entered, release = threading.Event(), threading.Event()
    original = getattr(target, attr)
    calls = []

    def wrapper(*a, **kw):
        if not calls:
            calls.append(1)
            entered.set()
            assert release.wait(5)
        return original(*a, **kw)

    monkeypatch.setattr(target, attr, wrapper)
    return entered, release


def test_load_spool_never_exceeds_limits_with_concurrent_enqueue(tmp_path, monkeypatch):
    """Reproducción del informe: un evento nuevo entra mientras load_spool lee el archivo."""
    from tracereports.transport import _Item
    _spool_one(tmp_path, path="/old")
    s = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path), max_items=1, max_bytes=2)
    s._ensure_thread = lambda: None
    entered, release = _blocking(monkeypatch, _Item, "from_json")
    t = threading.Thread(target=s.load_spool)
    t.start()
    assert entered.wait(5)              # ya reclamó y está leyendo el archivo
    assert s.enqueue("POST", "/new", b"{}", "application/json", 1)
    release.set()
    t.join(5)
    assert s.pending <= 1 and s._bytes <= 2, (s.pending, s._bytes)
    assert [it.path for it in s._q] == ["/new"]
    assert _items_in(tmp_path) == ["/old"], "what did not fit stays on disk for the next batch"


def test_reserved_capacity_is_not_taken_while_a_file_is_split(tmp_path, monkeypatch):
    """La reserva vale mientras se divide el archivo: ni enqueue ni otra carga la usan."""
    _big_spool(tmp_path, 3)
    s = Sender("http://unused", lambda extra: dict(extra or {}), spool_dir=str(tmp_path), max_items=2)
    s._ensure_thread = lambda: None
    entered, release = _blocking(monkeypatch, s, "_write_file")
    t = threading.Thread(target=s.load_spool)
    t.start()
    assert entered.wait(5)              # reservó 2 lugares y está escribiendo el resto
    assert not s.enqueue("POST", "/new", b"x", "application/json", 1), "reserved slots are not free"
    assert s.load_spool() == 0, "a second load on the same Sender respects the reservation"
    release.set()
    t.join(5)
    assert s.pending == 2 and [it.path for it in s._q] == ["/api/v1/tests/1/logs?e=0", "/api/v1/tests/1/logs?e=1"]
    assert sorted(_items_in(tmp_path)) == sorted(f"/api/v1/tests/1/logs?e={i}" for i in range(3)), \
        "loaded part (claimed) + the rest, all durable"
    assert s._reserved_items == 0 and s._reserved_bytes == 0
