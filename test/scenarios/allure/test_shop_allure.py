"""La tienda de la demo probada por un equipo que solo usa Allure (pytest + Playwright + allure-pytest).

Sin cliente de TraceReports: `go run ./test/scenarios/record -only allure-import` corre esta suite con
--alluredir y guarda la carpeta allure-results, que TraceReports importa (POST /import/allure,
`tracereports push allure-results`). Muestra lo que gana un equipo con el reporte de Allure que ya
tiene: los fallos diagnosticados con IA, sin revisar el reporte de Allure test por test.
"""
import os

import allure
from playwright.sync_api import Page, expect

SHOP = os.environ.get("SHOP_URL", "http://127.0.0.1:18180")
PASSWORD = os.environ.get("SHOP_PASSWORD", "demo")


def sign_in(page: Page):
    with allure.step("Open the sign-in page"):
        page.goto(f"{SHOP}/login")
    with allure.step("Sign in as qa@demo.shop"):
        page.fill("#password", PASSWORD)
        page.click("#sign-in")
        expect(page.get_by_role("heading", name="Products")).to_be_visible()


@allure.feature("Catalog")
@allure.story("Sign in")
@allure.severity(allure.severity_level.CRITICAL)
def test_sign_in_shows_the_catalog(page: Page):
    """A customer signs in and sees the three products of the catalog."""
    sign_in(page)
    with allure.step("The catalog lists 3 products"):
        expect(page.locator(".product-card")).to_have_count(3)
        allure.attach(page.screenshot(), "Catalog", allure.attachment_type.PNG)


@allure.feature("Checkout")
@allure.story("Pay an order")
@allure.severity(allure.severity_level.BLOCKER)
def test_checkout_pays_the_order(page: Page):
    """Buy a product with a card: the order must be confirmed."""
    sign_in(page)
    with allure.step("Add the trail running shoes to the cart"):
        page.goto(f"{SHOP}/product/P-1")
        page.get_by_test_id("add-to-cart").click()
    with allure.step("Pay with the card ending 4242"):
        page.goto(f"{SHOP}/cart")
        page.click("#pay")
    with allure.step("The order is confirmed"):
        expect(page.locator(".order-confirmed")).to_be_visible(timeout=5000)


@allure.feature("Cart")
@allure.story("Add to cart")
def test_add_to_cart_with_the_old_selector(page: Page):
    """Still uses the old #add-to-cart-btn id: the page now has data-testid="add-to-cart"."""
    sign_in(page)
    with allure.step("Open the product page"):
        page.goto(f"{SHOP}/product/P-1")
    with allure.step("Click Add to cart"):
        page.click("#add-to-cart-btn", timeout=3000)
