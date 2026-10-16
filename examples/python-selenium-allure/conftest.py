"""
Lo que ya tiene un proyecto de pytest + Selenium + Allure, más una línea para TraceReports.

- `driver`: Chrome con Selenium Manager (sin descargar chromedriver a mano). HEADLESS=0 lo muestra.
  `selenium_options(...)` activa los logs de red y consola de Chrome: con --tracereports el plugin
  envía solo la red (pestaña Red, Lab), la consola y, si el test falla, la captura y el DOM.
- Al fallar un test, la captura también va a Allure.
- `paso(...)`: un paso de Allure que, con --tracereports, también queda como paso en TraceReports.

Sin --tracereports la fixture `tracereports` no hace nada: la suite es la misma de siempre.
"""
import contextlib
import os

import allure
import pytest
from selenium import webdriver
from tracereports.selenium import selenium_options


@pytest.fixture
def driver():
    options = selenium_options(webdriver.ChromeOptions())  # logs de red y consola para TraceReports
    if os.environ.get("HEADLESS", "1") != "0":
        options.add_argument("--headless=new")
    options.add_argument("--window-size=1280,800")
    drv = webdriver.Chrome(options=options)  # Selenium Manager trae el driver
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
    # la captura al fallar, para Allure (a TraceReports la envía el plugin)
    drv = item.funcargs.get("driver")
    if call.when == "call" and call.excinfo is not None and drv is not None:
        try:
            allure.attach(drv.get_screenshot_as_png(), "Captura al fallar", allure.attachment_type.PNG)
        except Exception:
            pass
    yield
