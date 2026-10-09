"""Grabación sin servidor: si la ejecución no se puede crear (servidor caído, token incorrecto o
modo offline), la evidencia se graba en una carpeta en vez de perderse."""

import json
import os
import shutil
import socket
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

from tracereports import TraceReports
from tracereports.offline import MARKER

PNG = bytes.fromhex("89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de"
                    "0000000c4944415478 9c63f80f00000101000518d84e0000000049454e44ae426082".replace(" ", ""))


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def events(directory):
    out = []
    for name in sorted(os.listdir(directory)):
        if name.startswith("events-") and name.endswith(".jsonl"):
            with open(os.path.join(directory, name), encoding="utf-8") as f:
                out += [json.loads(line) for line in f if line.strip()]
    return out


class Counting(BaseHTTPRequestHandler):
    """Servidor que responde siempre lo mismo y cuenta las llamadas."""
    status = 401
    calls = 0

    def _reply(self):
        type(self).calls += 1
        self.send_response(self.status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"error":"invalid token"}')

    do_POST = do_PATCH = do_GET = _reply

    def log_message(self, *a):
        pass


@pytest.fixture
def unauthorized_server():
    Counting.calls = 0
    srv = HTTPServer(("127.0.0.1", 0), Counting)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    yield f"http://127.0.0.1:{srv.server_address[1]}"
    srv.shutdown()


def run_suite(cr):
    """Lo que hace una suite: una ejecución con un test fallido, un paso, una captura y la red."""
    cr.start_run("Checkout", environment="qa", project="shop", branch="dev", commit="abc")
    cr.start_test("test_login", key="tests/test_login.py::test_login")
    cr.log_info("abrir login")
    cr.attach_screenshot(PNG, "pantalla")
    cr.attach_network([{"method": "POST", "url": "https://api/login", "status": 500,
                        "request_headers": {"Authorization": "Bearer SECRET"}}])
    cr.end_test(status="FAIL", error_message="boom")
    cr.end_run()


def test_wrong_token_records_instead_of_losing_everything(unauthorized_server, tmp_path, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    cr = TraceReports(unauthorized_server, token="wrong", offline_dir=str(tmp_path / "rec"), async_send=False)
    run_suite(cr)
    assert cr.recording and cr.offline_dir == str(tmp_path / "rec")
    assert cr.run_id < 0
    evs = events(cr.offline_dir)
    paths = [(e["method"], e["path"]) for e in evs]
    run_local = evs[0]["local_id"]
    test_local = evs[1]["local_id"]
    assert paths == [
        ("POST", "/api/v1/runs"),
        ("POST", f"/api/v1/runs/{run_local}/tests"),
        ("POST", f"/api/v1/tests/{test_local}/logs"),
        ("POST", f"/api/v1/tests/{test_local}/screenshot"),
        ("POST", f"/api/v1/tests/{test_local}/network"),
        ("PATCH", f"/api/v1/tests/{test_local}/finish"),
        ("PATCH", f"/api/v1/runs/{run_local}/finish"),
    ]
    assert run_local < 0 and test_local < 0 and run_local != test_local
    assert evs[0]["body"]["name"] == "Checkout" and evs[0]["body"]["commit"] == "abc"
    assert all(e["ts"] > 0 for e in evs) and [e["seq"] for e in evs] == list(range(1, 8))
    shot = evs[3]
    assert shot["content_type"].startswith("multipart/form-data") and "body" not in shot
    with open(os.path.join(cr.offline_dir, shot["body_file"]), "rb") as f:
        assert PNG in f.read()
    with open(os.path.join(cr.offline_dir, MARKER), encoding="utf-8") as f:
        marker = json.load(f)
    assert marker["format"] == "tracereports-offline" and marker["version"] == 1 and marker["id"]
    assert cr.delivery["recorded"] == 7 and cr.delivery_problems() == 0


def test_unreachable_server_records_in_a_new_session_folder(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    monkeypatch.delenv("TRACEREPORTS_OFFLINE_DIR", raising=False)
    cr = TraceReports(f"http://127.0.0.1:{free_port()}", timeout=0.5, async_send=False)
    run_suite(cr)
    assert cr.recording
    assert os.path.dirname(cr.offline_dir) == "tracereports-offline"  # una carpeta por sesión
    assert len(events(cr.offline_dir)) == 7


def test_offline_off_records_nothing(unauthorized_server, tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    cr = TraceReports(unauthorized_server, offline="off", async_send=False)
    run_suite(cr)
    assert not cr.recording and cr.run_id is None
    assert not os.path.exists(tmp_path / "tracereports-offline")


def test_disabled_client_records_nothing(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    cr = TraceReports(f"http://127.0.0.1:{free_port()}", enabled=False)
    run_suite(cr)
    assert not cr.recording
    assert not os.path.exists(tmp_path / "tracereports-offline")


def test_always_never_contacts_a_server(unauthorized_server, tmp_path, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    cr = TraceReports(unauthorized_server, offline="always", offline_dir=str(tmp_path), async_send=False)
    run_suite(cr)
    assert Counting.calls == 0
    assert len(events(str(tmp_path))) == 7


def test_xdist_workers_share_the_recording(tmp_path, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    rec = str(tmp_path / "rec")
    controller = TraceReports(offline="always", offline_dir=rec)
    run_id = controller.start_run("xdist")
    worker = TraceReports(f"http://127.0.0.1:{free_port()}")
    worker.join_run(run_id, offline_dir=controller.offline_dir)
    assert worker.recording and worker.offline_dir == rec
    worker._sender._pid += 1  # otro proceso: otros ids locales y otro archivo
    worker.start_test("a")
    worker.end_test(status="PASS")
    controller.end_run()
    files = [n for n in os.listdir(rec) if n.startswith("events-")]
    assert len(files) == 2
    evs = events(rec)
    test_create = next(e for e in evs if e["path"].endswith("/tests"))
    assert test_create["path"] == f"/api/v1/runs/{run_id}/tests"


def binary():
    return (os.environ.get("TRACEREPORTS_BIN") or os.environ.get("TRACEREPORTS_TEST_BIN")) or shutil.which("tracereports")


@pytest.mark.skipif(not binary(), reason="needs the tracereports binary ($TRACEREPORTS_BIN or PATH)")
def test_end_to_end_report_without_server(unauthorized_server, tmp_path, monkeypatch):
    """Con el binario instalado, end_run arma el reporte HTML solo, sin servidor."""
    monkeypatch.delenv("TRACEREPORTS_OFFLINE_REPORT", raising=False)
    cr = TraceReports(unauthorized_server, token="wrong", offline_dir=str(tmp_path / "rec"), async_send=False)
    run_suite(cr)
    assert cr.offline_report and os.path.exists(cr.offline_report)
    with open(os.path.join(os.path.dirname(cr.offline_report), "data.js"), encoding="utf-8") as f:
        data = f.read()
    assert "Checkout" in data and "test_login" in data and "abrir login" in data
    assert "SECRET" not in data  # el enmascarado del servidor también corre sin servidor
    zip_path = cr.download_report(str(tmp_path / "zip"))
    assert zip_path and os.path.getsize(zip_path) > 0
    # la misma grabación se puede reportar de nuevo a mano
    out = tmp_path / "manual"
    subprocess.run([binary(), "report", "-o", str(out), cr.offline_dir], check=True, capture_output=True)
    assert (out / "index.html").exists()


def test_attach_artifact_is_recorded(tmp_path, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    cr = TraceReports(offline="always", offline_dir=str(tmp_path))
    cr.start_run("Artefactos")
    cr.start_test("t")
    trace = tmp_path / "trace.zip"
    trace.write_bytes(b"PK\x03\x04zip")
    assert cr.attach_artifact(str(trace), "trace")
    assert cr.attach_artifact(b"\x1a\x45\xdf\xa3webm", "video", name="grabación")
    assert not cr.attach_artifact(b"x", "har")  # solo trace o video
    assert not cr.attach_artifact(str(tmp_path / "missing.zip"), "trace")
    cr.end_test(status="FAIL")
    cr.end_run()
    uploads = [e for e in events(str(tmp_path)) if e["path"].endswith("/artifact")]
    assert len(uploads) == 2
    first = (tmp_path / uploads[0]["body_file"]).read_bytes()
    assert b'name="kind"\r\n\r\ntrace' in first and b"trace.zip" in first and b"PK\x03\x04zip" in first
    assert "grabación".encode() in (tmp_path / uploads[1]["body_file"]).read_bytes()


@pytest.mark.skipif(not binary(), reason="needs the tracereports binary ($TRACEREPORTS_BIN or PATH)")
def test_artifact_reaches_the_static_report(tmp_path, monkeypatch):
    monkeypatch.delenv("TRACEREPORTS_OFFLINE_REPORT", raising=False)
    cr = TraceReports(offline="always", offline_dir=str(tmp_path / "rec"))
    cr.start_run("Con video")
    cr.start_test("t")
    cr.attach_artifact(b"\x1a\x45\xdf\xa3" + b"\x00" * 64, "video")
    cr.end_test(status="FAIL", error_message="x")
    cr.end_run()
    report_dir = os.path.dirname(cr.offline_report)
    videos = [n for n in os.listdir(os.path.join(report_dir, "screenshots")) if n.endswith(".webm")]
    assert len(videos) == 1


def test_two_recorders_in_one_folder_never_repeat_local_ids(tmp_path, monkeypatch):
    """Dos grabadores que comparten la carpeta (aquí con el mismo pid) reservan bloques distintos en
    ids/: no repiten ids, que son exactos en JavaScript y no chocan con grabaciones anteriores."""
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    rec = str(tmp_path / "rec")
    a = TraceReports(offline="always", offline_dir=rec, async_send=False)
    b = TraceReports(offline="always", offline_dir=rec, async_send=False)
    ids = [a.start_run("suite A"), b.start_run("suite B")]
    a.start_test("test A")
    b.start_test("test B")
    b._sender._next_id = 100_000 - 1  # el bloque de B se agota: reserva otro
    b.end_test(status="FAIL", error_message="only B failed")
    b.start_test("test B2")
    a.end_test(status="PASS")
    b.end_test(status="PASS")
    a.end_run()
    b.end_run()
    ids += [e["local_id"] for e in events(rec) if e["path"].endswith("/tests")]
    assert len(ids) == 5 and len(set(ids)) == 5, ids
    assert all(-(2**53) < i < -10**11 for i in ids), ids
    assert len(os.listdir(os.path.join(rec, "ids"))) == 3
    if not binary():
        return
    out = tmp_path / "report"
    subprocess.run([binary(), "report", rec, "-o", str(out)], check=True, capture_output=True)
    runs = {}
    for name in os.listdir(out):
        if name.startswith("run-"):
            js = (out / name / "data.js").read_text(encoding="utf-8")
            data = json.loads(js[len("window.TRACEREPORTS_STATIC = "):].strip().rstrip(";"))
            runs[data["run"]["name"]] = sorted(t["name"] for t in data["run"]["tests"])
    assert runs == {"suite A": ["test A"], "suite B": ["test B", "test B2"]}
