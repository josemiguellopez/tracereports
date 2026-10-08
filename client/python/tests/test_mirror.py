"""Copia local durante entregas completas, caídas y procesos compartidos."""
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest
from tracereports import TraceReports
from test_offline import events, run_suite


@pytest.fixture
def server():
    state = {"paths": [], "fail": False, "id": 6}
    class Handler(BaseHTTPRequestHandler):
        def reply(self):
            self.rfile.read(int(self.headers.get("Content-Length", 0)))
            if state["fail"]:
                self.send_response(503)
                body = b"{}"
            else:
                state["paths"].append(self.path)
                self.send_response(201)
                value = {}
                if self.path == "/api/v1/runs":
                    value = {"run_id": 7}
                elif self.path.endswith("/tests"):
                    state["id"] += 1
                    value = {"test_id": state["id"]}
                body = json.dumps(value).encode()
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        do_POST = do_PATCH = reply
        def log_message(self, *args):
            pass
    srv = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    state["url"] = f"http://127.0.0.1:{srv.server_port}"
    yield state
    srv.shutdown()
    srv.server_close()


@pytest.fixture(autouse=True)
def config(monkeypatch):
    for key in ("RUN_ID", "OFFLINE_KEEP", "OFFLINE", "OFFLINE_DIR", "DISABLED", "SPOOL_DIR"):
        monkeypatch.delenv("TRACEREPORTS_" + key, raising=False)
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "1")


def marker(directory):
    return json.loads((Path(directory) / "tracereports-offline.json").read_text())


def client(server, tmp_path):
    return TraceReports(server["url"], offline="both", offline_dir=str(tmp_path), timeout=.1, flush_timeout=.1)


def test_both_keep_sends_and_records_once_with_independent_ids(server, tmp_path, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_KEEP", "1")
    cr = client(server, tmp_path)
    run_suite(cr)
    ev = events(tmp_path)
    assert len(server["paths"]) == len(ev) == 7
    assert cr.run_id == 7 and cr.report_url.startswith(server["url"])
    assert ev[0]["local_id"] < 0 and ev[1]["local_id"] < 0
    assert ev[0]["local_id"] != ev[1]["local_id"]
    assert ev[1]["path"] == f'/api/v1/runs/{ev[0]["local_id"]}/tests'
    assert ev[2]["path"] == f'/api/v1/tests/{ev[1]["local_id"]}/logs'
    assert marker(tmp_path)["mirror"]["complete"] is True
    assert Path(cr.offline_report).is_file()
    data = (tmp_path / "report/data.js").read_text(encoding="utf-8")
    assert "test_login" in data and "SECRET" not in data


def test_complete_delivery_cleans_raw_after_report(server, tmp_path):
    cr = client(server, tmp_path)
    run_suite(cr)
    assert sorted(p.name for p in tmp_path.iterdir()) == ["report", "tracereports-offline.json"]
    assert marker(tmp_path)["raw_removed"] is True
    cr.end_run()
    assert marker(tmp_path)["mirror"]["complete"] is True


def test_outage_keeps_new_tests_and_single_finish(server, tmp_path):
    cr = client(server, tmp_path)
    cr.start_run("outage")
    server["fail"] = True
    assert cr.start_test("after outage") < 0
    cr.log_info("local step")
    cr.end_test(status="PASS")
    cr.end_run()
    assert len(events(tmp_path)) == 5
    assert marker(tmp_path)["mirror"]["complete"] is False
    assert Path(cr.offline_report).is_file()
    assert "after outage" in (tmp_path / "report/data.js").read_text(encoding="utf-8")


def test_initial_failure_falls_back_without_mirror(server, tmp_path):
    server["fail"] = True
    cr = client(server, tmp_path)
    run_suite(cr)
    assert cr.run_id < 0
    assert "mirror" not in marker(tmp_path)
    assert len(events(tmp_path)) == 7


def test_missing_binary_keeps_raw(server, tmp_path, monkeypatch, caplog):
    monkeypatch.setenv("TRACEREPORTS_BIN", str(tmp_path / "not-installed"))
    cr = client(server, tmp_path)
    run_suite(cr)
    assert cr.offline_report is None and len(events(tmp_path)) == 7
    assert marker(tmp_path)["mirror"]["complete"] is True
    assert "tracereports report" in caplog.text


def test_disk_error_warns_once_and_does_not_stop_server(server, tmp_path, monkeypatch, caplog):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    cr = client(server, tmp_path)
    cr.start_run("disk")
    cr._sender.recorder.close()
    cr.start_test("still sent")
    cr.log_info("step")
    cr.end_test(status="PASS")
    cr.end_run()
    assert len(server["paths"]) == 5
    assert caplog.text.count("stopped local copy") == 1
    assert marker(tmp_path)["mirror"]["complete"] is False


@pytest.mark.parametrize("crash", [False, True])
def test_two_processes_share_recording_and_crash_blocks_cleanup(server, tmp_path, crash):
    cr = client(server, tmp_path)
    cr.start_run("shared")
    cr.start_test("owner")
    cr.end_test(status="PASS")
    source = f'''
from tracereports import TraceReports
import os
c=TraceReports({server["url"]!r}, offline="both", offline_dir={str(tmp_path)!r})
c.join_run(7)
c.start_test("worker")
c.log_info("worker step")
c.end_test(status="PASS")
{"os._exit(0)" if crash else "c.end_run()"}
'''
    subprocess.run([sys.executable, "-c", source], check=True, timeout=30)
    cr.end_run()
    assert marker(tmp_path)["mirror"]["complete"] is (not crash)
    assert marker(tmp_path).get("raw_removed", False) is (not crash)
    assert "worker step" in (tmp_path / "report/data.js").read_text(encoding="utf-8")


def test_missing_shared_mapping_is_incomplete(server, tmp_path, caplog):
    cr = client(server, tmp_path)
    cr.join_run(7)
    cr.start_test("unmapped")
    cr.end_test(status="PASS")
    cr.end_run()
    assert "missing local id" in caplog.text
    assert marker(tmp_path)["mirror"]["complete"] is False
    assert all("/-" in e["path"] for e in events(tmp_path))


def test_spooled_evidence_prevents_cleanup(server, tmp_path):
    cr = client(server, tmp_path)
    cr.start_run("spooled")
    cr._sender.sender.stats["spooled"] = 1
    cr.start_test("test")
    cr.end_test(status="PASS")
    cr.end_run()
    assert marker(tmp_path)["mirror"]["complete"] is False
    assert events(tmp_path)


def test_pytest_xdist_both_merges_workers_and_cleans(server, tmp_path):
    suite = tmp_path / "test_shared.py"
    suite.write_text('import pytest\n@pytest.mark.parametrize("n", range(4))\ndef test_shared(n, tracereports):\n    tracereports.log_info("worker evidence " + str(n))\n')
    rec = tmp_path / "rec"
    result = subprocess.run([sys.executable, "-m", "pytest", str(suite), "-n", "2", "--tracereports",
                             "--tracereports-url", server["url"], "-q"],
                            env=dict(os.environ, TRACEREPORTS_OFFLINE="both", TRACEREPORTS_OFFLINE_DIR=str(rec)),
                            capture_output=True, text=True, timeout=60)
    assert result.returncode == 0, result.stdout + result.stderr
    assert "4 passed" in result.stdout
    assert marker(rec)["mirror"]["complete"] is True
    assert marker(rec)["raw_removed"] is True
    data = (rec / "report/data.js").read_text(encoding="utf-8")
    for n in range(4):
        assert "worker evidence " + str(n) in data
