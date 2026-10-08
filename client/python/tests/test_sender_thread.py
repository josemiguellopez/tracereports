"""Un solo hilo de envío por Sender, aunque varios productores encolen a la vez.

    pytest client/python/tests/test_sender_thread.py
"""

import json
import threading
import time

import tracereports.transport as tr
from tracereports.transport import Sender

from test_transport import FakeServer


def _senders_started(monkeypatch):
    """Cuenta los hilos de envío que arrancan y ensancha la ventana entre "no hay hilo" y "ya hay
    uno": sin coordinación, dos productores crean un hilo cada uno. Con coordinación, el segundo
    solo espera (no hay barrera que obligue a solaparse, así que una implementación correcta pasa)."""
    started = []
    real = threading.Thread

    class Watched(real):
        def __init__(self, *a, **kw):
            if kw.get("name") == "tracereports-sender":
                time.sleep(0.2)
            super().__init__(*a, **kw)

        def start(self):
            if self.name == "tracereports-sender":
                started.append(self)
            super().start()

    monkeypatch.setattr(tr.threading, "Thread", Watched)
    return started


def _sent_numbers(srv):
    return [json.loads(b)["n"] for m, p, k, b in srv.requests]


def test_concurrent_producers_start_a_single_sender(monkeypatch):
    srv = FakeServer()
    try:
        started = _senders_started(monkeypatch)
        s = Sender(srv.url, lambda h: h)
        go = threading.Barrier(8)

        def produce(w):
            go.wait()
            for i in range(5):
                s.enqueue("POST", "/api/v1/tests/1/logs", json.dumps({"n": w * 100 + i}).encode(),
                          "application/json", 5)

        workers = [threading.Thread(target=produce, args=(w,)) for w in range(8)]
        for w in workers:
            w.start()
        for w in workers:
            w.join()
        assert s.flush(10) == 0
        assert len(started) == 1, f"one sender thread, got {len(started)}"
        got = _sent_numbers(srv)
        assert sorted(got) == sorted(w * 100 + i for w in range(8) for i in range(5)), "nothing lost or duplicated"
        for w in range(8):  # el orden de cada productor se conserva
            mine = [n for n in got if n // 100 == w]
            assert mine == sorted(mine)
        assert s.stats["sent"] == 40 and s.pending == 0
    finally:
        srv.close()


def test_pending_and_flush_count_the_event_in_flight(monkeypatch):
    """Con un evento en vuelo, pending y flush lo cuentan: dos hilos pisándose el único lugar de
    "en vuelo" harían que flush termine con eventos todavía sin confirmar."""
    srv = FakeServer()
    release = threading.Event()
    real_post = Sender._post

    def slow_post(self, item):
        release.wait(5)
        return real_post(self, item)

    try:
        started = _senders_started(monkeypatch)
        monkeypatch.setattr(Sender, "_post", slow_post)
        s = Sender(srv.url, lambda h: h)
        go = threading.Barrier(4)

        def produce(w):
            go.wait()
            s.enqueue("POST", "/api/v1/tests/1/logs", json.dumps({"n": w}).encode(), "application/json", 5)

        workers = [threading.Thread(target=produce, args=(w,)) for w in range(4)]
        for w in workers:
            w.start()
        for w in workers:
            w.join()
        time.sleep(0.1)
        assert s.pending == 4, "one in flight + three waiting"
        assert s.flush(0.3) == 4, "nothing confirmed yet"
        release.set()
        assert s.flush(10) == 0
        assert len(started) == 1
        assert sorted(_sent_numbers(srv)) == [0, 1, 2, 3] and s.stats["sent"] == 4
    finally:
        release.set()
        srv.close()

