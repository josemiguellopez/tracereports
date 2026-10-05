// Ejemplo mínimo contra la demo pública de OrangeHRM. Importar `test` desde "tracereports/playwright"
// (en vez de "@playwright/test") agrega la captura de red y el DOM al fallar; el resto es Playwright normal.
import { test, expect } from "tracereports/playwright";

const URL = "https://opensource-demo.orangehrmlive.com/web/index.php/auth/login";

async function login(page, user, password) {
  await page.goto(URL);
  await page.fill("input[name='username']", user);
  await page.fill("input[name='password']", password);
  await page.click("button[type='submit']");
}

test.describe("Login", () => {
  test("el administrador llega al Dashboard @smoke", async ({ page }) => {
    await test.step("Abrir el formulario de login", async () => {
      await page.goto(URL);
      await expect(page.locator("input[name='username']")).toBeVisible();
    });
    await test.step("Ingresar con Admin", () => login(page, "Admin", "admin123"));
    await expect(page.locator(".oxd-topbar-header-breadcrumb h6")).toHaveText("Dashboard");
    await test.info().attach("Dashboard visible", { body: await page.screenshot(), contentType: "image/png" });
  });

  test("una contraseña incorrecta muestra Invalid credentials @negativo", async ({ page, tracereports }) => {
    tracereports.info("Login con una clave que no corresponde");
    await login(page, "Admin", "clave-incorrecta");
    await expect(page.locator(".oxd-alert-content-text")).toHaveText("Invalid credentials");
  });
});

test("buscar un empleado por su Id @pim", async ({ page, tracereports }) => {
  await login(page, "Admin", "admin123");
  await page.click("//span[text()='PIM']");
  const firstRow = page.locator(".oxd-table-body .oxd-table-card").first();
  await expect(firstRow).toBeVisible({ timeout: 30_000 });
  const employeeId = (await firstRow.locator(".oxd-table-cell").nth(1).innerText()).trim();
  tracereports.info(`Id tomado de la primera fila: ${employeeId}`);

  await page.fill("//label[text()='Employee Id']/../following-sibling::div//input", employeeId);
  await page.click("button[type='submit']");
  await expect(page.locator(".oxd-table-body .oxd-table-card").first().locator(".oxd-table-cell").nth(1)).toHaveText(employeeId);
});

// Fallo controlado para ver el diagnóstico y el recomendador de locators: TRACEREPORTS_DEMO_FAIL=1
test("fallo controlado: selector que ya no existe @demo", async ({ page }) => {
  test.skip(!process.env.TRACEREPORTS_DEMO_FAIL, "define TRACEREPORTS_DEMO_FAIL=1 para verlo fallar");
  test.info().annotations.push({ type: "issue", description: "selector roto a propósito" });
  await login(page, "Admin", "admin123");
  await page.click("#boton-que-no-existe", { timeout: 5_000 });
});
