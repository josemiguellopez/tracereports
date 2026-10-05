"""
Contexto global de reporte — equivalente a Utils.extent_report_context.ExtentReportContex,
pero enviando los steps y screenshots a TraceReports en tiempo real.

Misma firma que el framework original para que migrar sea buscar/reemplazar:

    TraceReportsContext.log_info("Evidencia de ...", page=self.page, screenshot_name="x")

Si se pasa `page`, se toma un screenshot del viewport, se guarda en disco
(output/evidence/<run>/<screenshot_name>.png) y se adjunta al step en TraceReports.
"""

import os
import re
import time


class TraceReportsContext:
    _client = None
    _evidence_dir = None
    _logger = None

    @classmethod
    def setup(cls, client, evidence_dir=None, logger=None):
        cls._client = client
        cls._evidence_dir = evidence_dir
        cls._logger = logger
        if evidence_dir:
            os.makedirs(evidence_dir, exist_ok=True)

    # ─── API pública (mismos nombres que ExtentReportContex) ───────────────
    @classmethod
    def log_info(cls, message, page=None, screenshot_name=None):
        cls._log("INFO", message, page, screenshot_name)

    @classmethod
    def log_pass(cls, message, page=None, screenshot_name=None):
        cls._log("PASS", message, page, screenshot_name)

    @classmethod
    def log_error(cls, message, page=None, screenshot_name=None):
        cls._log("FAIL", message, page, screenshot_name)

    @classmethod
    def log_warning(cls, message, page=None, screenshot_name=None):
        cls._log("WARNING", message, page, screenshot_name)

    @classmethod
    def log_skip(cls, message, page=None, screenshot_name=None):
        cls._log("SKIP", message, page, screenshot_name)

    # ─── internos ──────────────────────────────────────────────────────────
    @classmethod
    def _log(cls, status, message, page, screenshot_name):
        if cls._logger:
            cls._logger.info(f"[{status}] {message}")
        if cls._client is None:
            return
        png = cls._capturar(page, screenshot_name or f"{status.lower()}_{int(time.time() * 1000)}") if page else None
        if png:
            cls._client.attach_screenshot(png, message, status=status)
        else:
            cls._client.log(status, message)

    @classmethod
    def _capturar(cls, page, screenshot_name):
        """Retorna los bytes PNG (o None si la página ya no está disponible)."""
        try:
            png = page.screenshot(timeout=5000)
        except Exception as e:  # página cerrada, navegando, etc. — la evidencia nunca rompe el test
            if cls._logger:
                cls._logger.warning(f"No se pudo capturar screenshot '{screenshot_name}': {e}")
            return None
        if cls._evidence_dir:
            nombre = re.sub(r"[^\w\-]+", "_", screenshot_name)
            with open(os.path.join(cls._evidence_dir, f"{nombre}.png"), "wb") as fh:
                fh.write(png)
        return png
