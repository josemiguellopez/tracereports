"""Una grabación con un evento de más de 16 MiB la lee el CLI (report)."""

import os
import shutil
import subprocess

import pytest

from tracereports import TraceReports


def _binary():
    return os.environ.get("TRACEREPORTS_BIN") or shutil.which("tracereports")


@pytest.mark.skipif(not _binary(), reason="needs the tracereports binary ($TRACEREPORTS_BIN or PATH)")
def test_an_event_over_16_mib_can_be_reported(tmp_path, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    rec = tmp_path / "rec"
    cr = TraceReports(offline="always", offline_dir=str(rec))
    cr.start_run("offline-large")
    tid = cr.start_test("large POST")
    cr.log_info("antes del upload", test_id=tid)
    # la línea queda en UTF-8: 'ñ"x' ocupa 5 bytes (\" escapada)
    cr.attach_network([{"method": "POST", "url": "https://app/api/upload", "status": 500,
                        "post_data": 'ñ"x' * (4 << 20), "response_body": "{}"}], test_id=tid)
    cr.log_info("después del upload", test_id=tid)
    cr.end_test("FAIL", test_id=tid)
    cr.end_run()
    events = [f for f in os.listdir(rec) if f.startswith("events-")]
    with open(rec / events[0], "rb") as f:
        sizes = [len(line) for line in f]
    assert max(sizes) > 16 << 20, "the fixture writes a line over 16 MiB"
    out = tmp_path / "report"
    res = subprocess.run([_binary(), "report", str(rec), "-o", str(out)], capture_output=True, text=True, timeout=120)
    assert res.returncode == 0, res.stderr
    data = (out / "data.js").read_text(encoding="utf-8")
    assert "https://app/api/upload" in data
    assert data.index("antes del upload") < data.index("después del upload")
