"""
Lo que ya tiene un proyecto de pytest + Selenium + Allure, más dos líneas para TraceReports.

- `driver`: Chrome con Selenium Manager (sin descargar chromedriver a mano). HEADLESS=0 lo muestra.
- Al fallar un test, la captura va a Allure y, si corres con --tracereports, también a TraceReports.
- `paso(...)`: un paso de Allure que, con --tracereports, también queda como paso en TraceReports.

Sin --tracereports la fixture `tracereports` no hace nada: la suite es la misma de siempre.
"""
import contextlib
import os

import allure
import pytest
from selenium import webdriver


@pytest.fixture
def driver(tracereports):
    options = webdriver.ChromeOptions()
    if os.environ.get("HEADLESS", "1") != "0":
        options.add_argument("--headless=new")
    options.add_argument("--window-size=1280,800")
    drv = webdriver.Chrome(options=options)  # Selenium Manager trae el driver
    drv.implicitly_wait(0)
    yield drv
    drv.quit()


@pytest.fixture
def paso(tracereports):
    """with paso("Iniciar sesión"): ... -> allure.step + un paso en TraceReports."""

    @contextlib.contextmanager
    def _paso(texto):
        tracereports.log_info(texto)
        with allure.step(texto):
            yield

    return _paso


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    # antes del yield: el test sigue abierto en TraceReports y el navegador sigue vivo
    drv = item.funcargs.get("driver")
    if call.when == "call" and call.excinfo is not None and drv is not None:
        try:
            png = drv.get_screenshot_as_png()
        except Exception:
            png = None
        if png:
            allure.attach(png, "Captura al fallar", allure.attachment_type.PNG)
            tr = item.funcargs.get("tracereports")
            if tr is not None:
                tr.attach_screenshot(png, "Captura al fallar", status="FAIL")
    yield
