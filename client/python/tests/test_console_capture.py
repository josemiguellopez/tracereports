"""Captura de la consola del navegador con Playwright real (se salta sin Playwright o sin Chromium)."""

import json
import os

import pytest

from tracereports import TraceReports
from tracereports.network import attach_listeners, reportar_red

sync_api = pytest.importorskip("playwright.sync_api")

PAGE = """<html><body><script>
console.log("ruido que no se guarda");
console.warn("API deprecada");
console.error("Fallo al cargar /api/cart: 500 token=SECRETO123");
setTimeout(() => { throw new TypeError("cannot read properties of undefined (reading 'total')"); }, 0);
</script></body></html>"""


def test_console_errors_and_page_errors_are_captured(tmp_path, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    try:
        pw = sync_api.sync_playwright().start()
        browser = pw.chromium.launch()
    except Exception as err:  # sin navegador instalado
        pytest.skip(f"chromium not available: {err}")
    try:
        page = browser.new_page()
        attach_listeners(page)
        cr = TraceReports(offline="always", offline_dir=str(tmp_path))
        cr.start_run("consola")
        tid = cr.start_test("t")
        page.set_content(PAGE)
        page.wait_for_timeout(300)
        reportar_red(page, cr, test_id=tid, esperar_en_vuelo_ms=0)
        cr.end_test(status="FAIL")
        cr.end_run()
    finally:
        browser.close()
        pw.stop()
    events = []
    for name in os.listdir(tmp_path):
        if name.startswith("events-"):
            with open(tmp_path / name, encoding="utf-8") as f:
                events += [json.loads(x) for x in f if x.strip()]
    sent = [e for e in events if e["path"].endswith("/console")]
    assert len(sent) == 1
    entries = sent[0]["body"]["entries"]
    levels = [e["level"] for e in entries]
    assert levels == ["warning", "error", "pageerror"], levels  # console.log no se guarda
    assert "SECRETO123" not in json.dumps(entries)  # enmascarado en el cliente también
    assert "reading 'total'" in entries[2]["text"]
    assert all(e["timestamp"] > 0 for e in entries)
