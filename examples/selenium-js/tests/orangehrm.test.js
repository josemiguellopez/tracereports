// Selenium WebDriver + node:test + TraceReports, contra la demo pública de OrangeHRM.
//
//   npm install && npm test                      # servidor en $TRACEREPORTS_URL o http://localhost:8080
//   HEADLESS=0 npm test                          # ver el navegador
//   TRACEREPORTS_DEMO_FAIL=1 npm test                 # incluye un fallo controlado (diagnóstico + locators)
//
// Cada test usa un navegador nuevo y corre dentro de `cr.test(...)`: si falla, se adjuntan la
// captura del momento y el snapshot del DOM.
import { after, before, test } from "node:test";
import { Builder, By, until } from "selenium-webdriver";
import chrome from "selenium-webdriver/chrome.js";
import { TraceReports } from "tracereports";
import { screenshot, withDriver } from "tracereports/selenium";

const URL = "https://opensource-demo.orangehrmlive.com/web/index.php/auth/login";
const FILE = "tests/orangehrm.test.js";
const cr = new TraceReports();

before(() => cr.startRun("OrangeHRM - Selenium JS", { environment: "demo pública · chrome", framework: "selenium" }));

after(async () => {
  await cr.finishRun();
  console.log(`TraceReports: ${cr.reportUrl}`);
});

function newDriver() {
  const options = new chrome.Options();
  if (process.env.HEADLESS !== "0") options.addArguments("--headless=new");
  options.addArguments("--window-size=1280,800");
  return new Builder().forBrowser("chrome").setChromeOptions(options).build(); // Selenium Manager trae el driver
}

/** Test de node:test reportado a TraceReports con identidad estable (archivo > título). */
function traceTest(title, tags, fn) {
  test(title, async () => {
    const driver = await newDriver();
    try {
      await cr.test(title, withDriver(driver, { key: `${FILE} > ${title}`, suite: FILE, category: tags }), (t) => fn(t, driver));
    } finally {
      await driver.quit();
    }
  });
}

async function login(driver, user, password) {
  await driver.get(URL);
  const username = await driver.wait(until.elementLocated(By.name("username")), 20_000);
  await username.sendKeys(user);
  await driver.findElement(By.name("password")).sendKeys(password);
  await driver.findElement(By.css("button[type='submit']")).click();
}

traceTest("el administrador llega al Dashboard", "login, smoke", async (t, driver) => {
  t.info("Abrir el formulario de login");
  await login(driver, "Admin", "admin123");
  const title = await driver.wait(until.elementLocated(By.css(".oxd-topbar-header-breadcrumb h6")), 20_000);
  await driver.wait(until.elementTextIs(title, "Dashboard"), 10_000);
  t.screenshot(await screenshot(driver), "Dashboard visible", "PASS");
});

traceTest("una contraseña incorrecta muestra Invalid credentials", "login, negativo", async (t, driver) => {
  await login(driver, "Admin", "clave-incorrecta");
  const alert = await driver.wait(until.elementLocated(By.css(".oxd-alert-content-text")), 20_000);
  await driver.wait(until.elementTextIs(alert, "Invalid credentials"), 10_000);
  t.pass("Se muestra la alerta");
});

traceTest("buscar un empleado por su Id", "pim", async (t, driver) => {
  await login(driver, "Admin", "admin123");
  await driver.wait(until.elementLocated(By.xpath("//span[text()='PIM']")), 20_000).click();
  const idCell = By.css(".oxd-table-body .oxd-table-card .oxd-table-cell:nth-child(2)");
  const first = await driver.wait(until.elementLocated(idCell), 30_000);
  const employeeId = (await first.getText()).trim();
  t.info(`Id tomado de la primera fila: ${employeeId}`);
  await driver.findElement(By.xpath("//label[text()='Employee Id']/../following-sibling::div//input")).sendKeys(employeeId);
  await driver.findElement(By.css("button[type='submit']")).click();
  await driver.wait(async () => {
    try {
      return (await (await driver.findElement(idCell)).getText()).trim() === employeeId;
    } catch {
      return false; // la tabla se vuelve a dibujar con el resultado
    }
  }, 20_000);
  t.screenshot(await screenshot(driver), `Resultado de buscar el Id ${employeeId}`, "PASS");
});

if (process.env.TRACEREPORTS_DEMO_FAIL) {
  traceTest("fallo controlado: selector que ya no existe", "demo", async (t, driver) => {
    await login(driver, "Admin", "admin123");
    await driver.wait(until.elementLocated(By.css("#boton-que-no-existe")), 5_000);
  });
}
