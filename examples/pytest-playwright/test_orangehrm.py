"""
Ejemplo mínimo con pytest + pytest-playwright: el plugin de TraceReports reporta todo solo.

    pip install pytest pytest-playwright ./client/python
    playwright install chromium            # o usa tu Chrome: --browser-channel chrome
    pytest examples/pytest-playwright --tracereports

Sin código extra se reporta: estado de cada test, error + traceback (AI Triage), captura al
fallar y toda la red del backend. La fixture `tracereports` agrega pasos y capturas propias.
"""

import pytest
from playwright.sync_api import Page, expect

URL = "https://opensource-demo.orangehrmlive.com/web/index.php/auth/login"


def login(page: Page, usuario: str, clave: str) -> None:
    page.goto(URL)
    page.fill("input[name='username']", usuario)
    page.fill("input[name='password']", clave)
    page.click("button[type='submit']")


@pytest.mark.smoke
def test_login_correcto(page: Page, tracereports):
    """El administrador inicia sesión y llega al Dashboard."""
    tracereports.log_info("Abrir el formulario de login")
    page.goto(URL)
    expect(page.locator("input[name='username']")).to_be_visible()  # la SPA dibuja el form después del load
    tracereports.attach_screenshot(page.screenshot(), "Formulario de login")

    login(page, "Admin", "admin123")
    expect(page.locator(".oxd-topbar-header-breadcrumb h6")).to_have_text("Dashboard")
    tracereports.attach_screenshot(page.screenshot(), "Dashboard visible", status="PASS")


@pytest.mark.negativo
def test_login_password_incorrecta(page: Page, tracereports):
    """Una contraseña incorrecta muestra 'Invalid credentials'."""
    login(page, "Admin", "clave-incorrecta")
    alerta = page.locator(".oxd-alert-content-text")
    expect(alerta).to_have_text("Invalid credentials")
    tracereports.attach_screenshot(page.screenshot(), "Alerta de credenciales inválidas", status="PASS")


@pytest.mark.pim
def test_buscar_empleado_por_id(page: Page, tracereports):
    """Buscar en PIM un empleado por su Id devuelve ese empleado."""
    login(page, "Admin", "admin123")
    page.click("//span[text()='PIM']")
    primera_fila = page.locator(".oxd-table-body .oxd-table-card").first
    expect(primera_fila).to_be_visible(timeout=30000)
    employee_id = primera_fila.locator(".oxd-table-cell").nth(1).inner_text().strip()
    tracereports.log_info(f"Id tomado de la primera fila: {employee_id}")

    page.fill("//label[text()='Employee Id']/../following-sibling::div//input", employee_id)
    page.click("button[type='submit']")
    expect(page.locator(".oxd-table-body .oxd-table-card").first.locator(".oxd-table-cell").nth(1)).to_have_text(employee_id)
    tracereports.attach_screenshot(page.screenshot(), f"Resultado de buscar el Id {employee_id}", status="PASS")
