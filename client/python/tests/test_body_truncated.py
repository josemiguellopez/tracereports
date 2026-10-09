"""body_truncated y el tamaño original (bytes UTF-8, como el servidor) se conservan desde la captura.

    pytest client/python/tests/test_body_truncated.py
"""

import json
import os
import socket
import subprocess
import tempfile
import time
import urllib.request

import pytest

from tracereports import TraceReports
from tracereports.network import attach_listeners

from test_transport import FakeServer


class _Req:
    def __init__(self, url):
        self.url, self.method, self.resource_type, self.headers, self.post_data = url, "GET", "fetch", {}, None


class _Res:
    def __init__(self, req, body):
        self.request, self.status, self.status_text, self.url = req, 200, "OK", req.url
        self.headers = {"content-type": "application/json"}
        self._body = body

    def text(self):
        return self._body


class _Page:
    """Página mínima: guarda los listeners y deja dispararlos (sin navegador)."""

    def __init__(self):
        self.handlers = {}

    def on(self, event, fn):
        self.handlers[event] = fn

    def respond(self, url, body):
        req = _Req(url)
        self.handlers["request"](req)
        self.handlers["response"](_Res(req, body))
        return self._network_evidence[-1]


def capture(body, **kw):
    page = _Page()
    attach_listeners(page, **kw)
    return page.respond("https://app/api/data", body)


def test_capture_marks_its_own_cut_with_the_original_bytes():
    body = "ñ" * 3000
    c = capture(body, max_body_chars=1000)
    assert c["body_truncated"] is True
    assert c["body_size"] == len(body.encode("utf-8"))
    assert len(c["response_body"]) == 1000
    whole = capture(body)
    assert not whole.get("body_truncated") and whole["body_size"] == len(body.encode("utf-8"))


def _sent(conn):
    srv = FakeServer()
    try:
        cr = TraceReports(srv.url)
        cr.run_id = 7
        cr.attach_network([conn], test_id=11)
        assert cr.flush(10) == 0
        return [c for m, p, k, b in srv.requests if p.endswith("/network") for c in json.loads(b)["connections"]][0]
    finally:
        srv.close()


def test_client_keeps_an_explicit_flag_and_the_original_size():
    c = _sent({"method": "GET", "url": "https://app/api", "status": 200, "response_body": '{"a":', "body_truncated": True,
               "body_size": 900000})
    assert c["body_truncated"] is True and c["body_size"] == 900000
    # sin tamaño: el indicador del llamador manda igual
    c = _sent({"method": "GET", "url": "https://app/api", "status": 200, "response_body": '{"a":', "body_truncated": True})
    assert c["body_truncated"] is True


def test_a_whole_body_is_not_marked_even_if_masking_shortened_it():
    # la captura enmascara (más corto que el original): eso no es un recorte
    masked = capture('{"password":"' + "s" * 500 + '","ok":true}')
    assert masked["body_size"] > len(masked["response_body"].encode("utf-8"))
    c = _sent(masked)
    assert c["body_truncated"] is False


def test_the_sdk_cut_is_marked_with_bytes():
    body = "😀" * 300000  # 300.000 caracteres > 256 K: el SDK recorta (Python cuenta caracteres)
    c = _sent({"method": "GET", "url": "https://app/api", "status": 200, "response_body": body})
    assert c["body_truncated"] is True
    assert c["body_size"] == len(body.encode("utf-8"))


def _free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


@pytest.mark.skipif(not (os.environ.get("TRACEREPORTS_BIN") or os.environ.get("TRACEREPORTS_TEST_BIN")), reason="needs $TRACEREPORTS_BIN")
def test_capture_client_server_round_trip(tmp_path):
    port = _free_port()
    data = tmp_path / "data"
    env = dict(os.environ, PORT=str(port), DATA_DIR=str(data), TRACEREPORTS_ENV_FILE=str(tmp_path / "no.env"),
               TRACEREPORTS_TOKEN="", TRACEREPORTS_INGEST_TOKEN="", TRACEREPORTS_UI_USER="", TRACEREPORTS_UI_PASSWORD="",
               AI_PROVIDER="", TEAMS_WEBHOOK_URL="", SLACK_WEBHOOK_URL="")
    proc = subprocess.Popen([(os.environ.get("TRACEREPORTS_BIN") or os.environ["TRACEREPORTS_TEST_BIN"])], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    url = f"http://127.0.0.1:{port}"
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen(url + "/api/v1/runs", timeout=1)
                break
            except Exception:
                time.sleep(0.1)
        body = "ñ" * 3000
        conn = capture(body, max_body_chars=1000)
        cr = TraceReports(url)
        cr.start_run("py body flag")
        tid = cr.start_test("t")
        cr.attach_network([conn], test_id=tid)
        cr.end_test("PASS", test_id=tid)
        cr.end_run()
        stored = json.loads(urllib.request.urlopen(f"{url}/api/v1/tests/{tid}/network", timeout=5).read())
        stored = stored if isinstance(stored, list) else stored["connections"]
        assert stored[0]["body_truncated"] is True
        assert stored[0]["body_size"] == len(body.encode("utf-8"))
    finally:
        proc.terminate()
        proc.wait(10)
