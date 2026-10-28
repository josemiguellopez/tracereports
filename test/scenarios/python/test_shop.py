"""Escenarios de TraceReports grabados con el cliente de Python (pytest + Playwright).

Corren contra la tienda de la demo (internal/demo): `go run ./test/scenarios/record` los ejecuta
uno a uno con TRACEREPORTS_OFFLINE=always y guarda cada grabación en internal/demo/scenarios.
"""
import os

from playwright.sync_api import Page, expect
from tracereports import playwright_screenshot

SHOP = os.environ.get("SHOP_URL", "http://127.0.0.1:18180")
PASSWORD = os.environ.get("SHOP_PASSWORD", "demo")


def sign_in(page: Page, tracereports):
    tracereports.log_info("Open the sign-in page")
    page.goto(f"{SHOP}/login")
    page.fill("#password", PASSWORD)
    page.click("#sign-in")
    expect(page.get_by_role("heading", name="Products")).to_be_visible()
    tracereports.log_pass("Signed in: the catalog is visible")


def test_sign_in_shows_the_catalog(page: Page, tracereports):
    """A customer signs in and sees the three products of the catalog."""
    sign_in(page, tracereports)
    expect(page.locator(".product-card")).to_have_count(3)
    # con la tarjeta de socio (data-tracereports-mask) tapada
    tracereports.attach_screenshot(playwright_screenshot(page), "Catalog with 3 products")
    tracereports.log_pass("3 products listed")


def test_checkout_pays_the_order(page: Page, tracereports):
    """Buy a product with a card: the order must be confirmed."""
    sign_in(page, tracereports)
    page.goto(f"{SHOP}/product/P-1")
    page.get_by_test_id("add-to-cart").click()
    tracereports.log_pass("Product added to the cart")
    page.goto(f"{SHOP}/cart")
    tracereports.log_info("Pay with the card ending 4242")
    page.click("#pay")
    expect(page.locator(".order-confirmed")).to_be_visible(timeout=5000)
    tracereports.log_pass("Order confirmed")


def test_add_to_cart_with_the_old_selector(page: Page, tracereports):
    """The product page changed its markup; this test still uses the old selector."""
    sign_in(page, tracereports)
    page.goto(f"{SHOP}/product/P-1")
    tracereports.log_info("Click \"Add to cart\" (#add-to-cart-btn)")
    page.click("#add-to-cart-btn", timeout=3000)
    expect(page.get_by_text("Added to your cart")).to_be_visible()
