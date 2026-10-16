# Selenium: red, consola y selectores sugeridos

🌐 [English](../en/selenium.md) · **Español**

¿Usas Selenium? Tienes el mismo reporte que con Playwright: las llamadas que hizo el navegador, la
consola, la captura y el selector sugerido cuando uno se rompe. Solo agregas **una línea** donde creas
el driver.

| Qué obtienes | De dónde sale |
| --- | --- |
| Pestaña **Red**: cada llamada con su status, headers, tiempos y el body de las APIs y los errores | Del log de rendimiento de Chrome |
| **Incidente de backend** ("POST /api/orders → 500") y el **[Lab](fixlab.md)** para reenviar esa llamada | De la red |
| Pestaña **Consola**: errores y warnings de JavaScript | Del log del navegador |
| **Captura** cuando un test falla | Automática |
| **Selector sugerido** cuando se rompe un locator | Del DOM de la página al fallar |

Funciona con **Chrome y Edge** (Firefox no tiene log de rendimiento: ahí igual tienes pasos, captura y
DOM).

## Python con pytest

1. Instala el cliente (`pip install tracereports`) y crea el driver con `selenium_options`:

```python
# conftest.py
import pytest
from selenium import webdriver
from tracereports.selenium import selenium_options

@pytest.fixture
def driver():
    options = selenium_options(webdriver.ChromeOptions())   # ← la única línea para TraceReports
    drv = webdriver.Chrome(options=options)
    yield drv
    drv.quit()
```

2. Corre tus tests como siempre, con `--tracereports`:

```bash
python -m pytest --tracereports
```

Listo. El plugin encuentra el driver en tus fixtures (se llame como se llame: `driver`, `browser`…) y en
cada test envía su red y su consola, y si falla la captura y el DOM. Tus tests no cambian.

¿Ya usas Allure? Ver [examples/python-selenium-allure](../../examples/python-selenium-allure/README.es.md):
Allure y TraceReports en la misma corrida.

## Python sin pytest (unittest, scripts)

```python
from selenium import webdriver
from tracereports import TraceReports
from tracereports.selenium import selenium_options, report_selenium

driver = webdriver.Chrome(options=selenium_options(webdriver.ChromeOptions()))
cr = TraceReports()
cr.start_run("Regresión web")

cr.start_test("Pago")
try:
    ...                                    # tu test
    report_selenium(driver, cr)            # red y consola
    cr.end_test("PASS")
except Exception as e:
    report_selenium(driver, cr, failed=True)   # + captura y DOM
    cr.end_test("FAIL", exc=e)

cr.end_run()
```

## JavaScript (selenium-webdriver)

```js
import { Builder } from "selenium-webdriver";
import chrome from "selenium-webdriver/chrome.js";
import { TraceReports } from "tracereports";
import { seleniumOptions, withDriver } from "tracereports/selenium";

const options = seleniumOptions(new chrome.Options());      // ← logs de red y consola
const driver = await new Builder().forBrowser("chrome").setChromeOptions(options).build();

const cr = new TraceReports();
await cr.startRun("Regresión web", { framework: "selenium" });
await cr.test("Pago", withDriver(driver, { key: "pago.test.js > paga" }), async (t) => {
  // tu test
});
await cr.finishRun();
```

`withDriver` envía la red y la consola de cada test y, si falla, la captura y el DOM. Sirve con
cualquier runner (node:test, Mocha, Jest). Ejemplo completo:
[examples/selenium-js](../../examples/selenium-js).

## Java (JUnit 5)

```java
@ExtendWith(TraceReportsExtension.class)
class PagoTest {
    WebDriver driver;

    @BeforeEach
    void abrir(TraceTest t) {
        ChromeOptions options = SeleniumEvidence.options(new ChromeOptions());   // ← logs de red y consola
        driver = new ChromeDriver(options);
        SeleniumEvidence.attach(t, driver);   // red y consola del test; captura y DOM si falla
    }

    @AfterEach
    void cerrar() { driver.quit(); }

    @Test
    void pagaElPedido() { /* tu test */ }
}
```

Ejemplo completo: [examples/selenium-java](../../examples/selenium-java). Edge: `SeleniumEvidence.options(new EdgeOptions())`.

## Go

El cliente de Go se usa con playwright-go, que ya captura la red: ver [Go](go.md).

## Cómo funciona

Chrome y Edge pueden escribir en un log ("performance") todo lo que la página hace en la red. El helper
activa ese log al crear el driver y, al terminar cada test, lo lee y lo convierte en las mismas
llamadas que produce la captura de Playwright. Los bodies se le piden al navegador por el DevTools
Protocol (CDP). Leer el log lo vacía, así que cada test recibe solo sus llamadas, aunque varios tests
compartan el driver.

## Si algo no aparece

| Qué ves | Por qué, y qué hacer |
| --- | --- |
| No hay pestaña **Red** | El driver no se creó con el helper (`selenium_options`, `seleniumOptions`, `SeleniumEvidence.options`), o es Firefox |
| La llamada está pero sin body | La página navegó después de esa llamada y Chrome ya no la guarda. Están el status, los headers y los tiempos, y el Lab igual puede reenviarla |
| Sin body en un driver remoto (Selenium Grid) | Los bodies necesitan CDP, que un driver remoto puede no ofrecer. Pasos, captura y DOM funcionan siempre |
| No hay selector sugerido | El error no dice qué locator falló. Con pytest se lee de la línea que falló (`By.ID, "pagar"`); con JUnit y JS, del mensaje de Selenium |
| ¿Contraseñas o tokens en el reporte? | No: se enmascaran antes de guardarse (y, en Python, también antes de salir de la máquina) |

Más: [Python](python.md) · [JavaScript](javascript.md) · [Java](java.md) · [FixLab](fixlab.md).
