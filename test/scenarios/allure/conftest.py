"""Lo que suele agregar una suite con Allure: la captura de la página cuando un test falla."""
import allure
import pytest


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()
    page = item.funcargs.get("page")
    if report.when == "call" and report.failed and page is not None:
        allure.attach(page.screenshot(), "Screenshot on failure", allure.attachment_type.PNG)
