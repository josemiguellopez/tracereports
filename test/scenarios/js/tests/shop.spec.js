// Escenarios de TraceReports grabados con el cliente de JavaScript (Playwright Test). Corren contra
// la tienda de la demo: `go run ./test/scenarios/record` los ejecuta uno a uno.
import { test, expect } from "tracereports/playwright";

const SHOP = process.env.SHOP_URL || "http://127.0.0.1:18180";
const PASSWORD = process.env.SHOP_PASSWORD || "demo";

async function signIn(page, tracereports) {
	tracereports.info("Open the sign-in page");
	await page.goto(`${SHOP}/login`);
	await page.fill("#password", PASSWORD);
	await page.click("#sign-in");
	await expect(page.getByRole("heading", { name: "Products" })).toBeVisible();
	tracereports.pass("Signed in: the catalog is visible");
}

test.describe("Shop", () => {
	test("signs in and sees the catalog", async ({ page, tracereports }) => {
		await signIn(page, tracereports);
		await expect(page.locator(".product-card")).toHaveCount(3);
		await test.info().attach("Catalog", { body: await page.screenshot(), contentType: "image/png" });
	});

	test("pays an order with a card", async ({ page, tracereports }) => {
		await signIn(page, tracereports);
		await page.goto(`${SHOP}/product/P-2`);
		await page.getByTestId("add-to-cart").click();
		await page.goto(`${SHOP}/cart`);
		tracereports.info("Pay with the card ending 4242");
		await page.click("#pay");
		await expect(page.locator(".order-confirmed")).toBeVisible({ timeout: 5000 });
	});

	test("shows recommendations on the product page", async ({ page, tracereports }) => {
		await signIn(page, tracereports);
		await page.goto(`${SHOP}/product/P-1`);
		await expect(page.getByText("Trail socks")).toBeVisible({ timeout: 3000 });
		tracereports.pass("Recommendations visible");
	});
});
