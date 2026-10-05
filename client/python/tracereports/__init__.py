"""
TraceReports — cliente Python.

    from tracereports import TraceReports                  # API REST (steps, capturas, red)
    from tracereports import attach_listeners, reportar_red # captura de red con Playwright

Con pytest no hace falta código: `pytest --tracereports` (ver tracereports.pytest_plugin).
"""

from .client import TraceReports, __version__
from .dom import capturar_dom
from .network import (
    attach_listeners,
    conexiones_del_test,
    esperar_conexion,
    guardar_red_local,
    reportar_red,
    resumir_red,
)

__all__ = [
    "TraceReports",
    "attach_listeners",
    "capturar_dom",
    "conexiones_del_test",
    "esperar_conexion",
    "guardar_red_local",
    "reportar_red",
    "resumir_red",
    "__version__",
]
