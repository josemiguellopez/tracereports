"""
pytest + Selenium + Allure contra la demo pública de OrangeHRM, y cómo sumar TraceReports.

    python -m pip install -r examples/python-selenium-allure/requirements.txt ./client/python

    # 1. Lo de siempre: Allure
    python -m pytest examples/python-selenium-allure --alluredir=allure-results
    # 2. TraceReports diagnostica esos resultados con IA (sin tocar los tests)
    tracereports push allure-results -project orangehrm -name "Selenium + Allure"

    # o las dos cosas en la misma corrida: Allure y TraceReports en vivo
    python -m pytest examples/python-selenium-allure --alluredir=allure-results --tracereports

TRACEREPORTS_DEMO_FAIL=1 agrega un fallo controlado (un selector que ya no existe) para ver el
diagnóstico. HEADLESS=0 muestra el navegador.
"""
import os

import allure
import pytest
from selenium.webdriver.common.by import By
from selenium.webdriver.support import expected_conditions as EC
from selenium.webdriver.support.ui import WebDriverWait

URL = "https://opensource-demo.orangehrmlive.com/web/index.php/auth/login"


def esperar(driver, locator, segundos=20):
    return WebDriverWait(driver, segundos).until(EC.visibility_of_element_located(locator))


def login(driver, paso, usuario, clave):
    with paso("Abrir el login de OrangeHRM"):
        driver.get(URL)
        esperar(driver, (By.NAME, "username"))
    with paso(f"Iniciar sesión como {usuario}"):
        driver.find_element(By.NAME, "username").send_keys(usuario)
        driver.find_element(By.NAME, "password").send_keys(clave)
        driver.find_element(By.CSS_SELECTOR, "button[type='submit']").click()


@allure.feature("Login")
@allure.severity(allure.severity_level.CRITICAL)
def test_login_correcto(driver, paso):
    """El administrador inicia sesión y llega al Dashboard."""
    login(driver, paso, "Admin", "admin123")
    with paso("El Dashboard es visible"):
        titulo = esperar(driver, (By.CSS_SELECTOR, ".oxd-topbar-header-breadcrumb h6"))
        assert titulo.text == "Dashboard"
        allure.attach(driver.get_screenshot_as_png(), "Dashboard", allure.attachment_type.PNG)


@allure.feature("Login")
def test_login_password_incorrecta(driver, paso):
    """Una contraseña incorrecta muestra 'Invalid credentials'."""
    login(driver, paso, "Admin", "clave-incorrecta")
    with paso("Aparece la alerta de credenciales inválidas"):
        alerta = esperar(driver, (By.CSS_SELECTOR, ".oxd-alert-content-text"))
        assert alerta.text == "Invalid credentials"


@allure.feature("PIM")
@pytest.mark.skipif(os.environ.get("TRACEREPORTS_DEMO_FAIL") != "1", reason="fallo controlado: TRACEREPORTS_DEMO_FAIL=1")
def test_abrir_pim_con_selector_antiguo(driver, paso):
    """Usa un id que el menú ya no tiene: falla a propósito para ver el diagnóstico."""
    login(driver, paso, "Admin", "admin123")
    with paso("Abrir el módulo PIM"):
        esperar(driver, (By.CSS_SELECTOR, "#menu_pim_viewPimModule"), segundos=5).click()
