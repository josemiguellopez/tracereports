# Selenium: network, console and suggested selectors

🌐 **English** · [Español](../es/selenium.md)

Using Selenium? You get the same report as with Playwright: the calls the browser made, the console,
the screenshot and the suggested selector when one breaks. You only add **one line** where you create
the driver.

| What you get | How |
| --- | --- |
| **Network** tab: every call with status, headers, timings and the body of the APIs and errors | From Chrome's performance log |
| **Backend incident** ("POST /api/orders → 500") and the **[Lab](fixlab.md)** to resend that call | From the network |
| **Console** tab: JavaScript errors and warnings | From the browser log |
| **Screenshot** when a test fails | Automatic |
| **Suggested selector** when a locator breaks | The page's DOM at the failure |

Works with **Chrome and Edge** (Firefox has no performance log: there you still get steps, screenshot
and DOM).

## Python with pytest

1. Install the client (`pip install tracereports`) and create the driver with `selenium_options`:

```python
# conftest.py
import pytest
from selenium import webdriver
from tracereports.selenium import selenium_options

@pytest.fixture
def driver():
    options = selenium_options(webdriver.ChromeOptions())   # ← the only line for TraceReports
    drv = webdriver.Chrome(options=options)
    yield drv
    drv.quit()
```

2. Run your tests as always, with `--tracereports`:

```bash
python -m pytest --tracereports
```

That's it. The plugin finds the driver in your fixtures (whatever its name: `driver`, `browser`…) and
for each test sends its network and console, and the screenshot and DOM when it fails. Your tests do
not change.

Already using Allure? See [examples/python-selenium-allure](../../examples/python-selenium-allure):
Allure and TraceReports in the same run.

## Python without pytest (unittest, scripts)

```python
from selenium import webdriver
from tracereports import TraceReports
from tracereports.selenium import selenium_options, report_selenium

driver = webdriver.Chrome(options=selenium_options(webdriver.ChromeOptions()))
cr = TraceReports()
cr.start_run("Web regression")

cr.start_test("Checkout")
try:
    ...                                    # your test
    report_selenium(driver, cr)            # network and console
    cr.end_test("PASS")
except Exception as e:
    report_selenium(driver, cr, failed=True)   # + screenshot and DOM
    cr.end_test("FAIL", exc=e)

cr.end_run()
```

## JavaScript (selenium-webdriver)

```js
import { Builder } from "selenium-webdriver";
import chrome from "selenium-webdriver/chrome.js";
import { TraceReports } from "tracereports";
import { seleniumOptions, withDriver } from "tracereports/selenium";

const options = seleniumOptions(new chrome.Options());      // ← logs of network and console
const driver = await new Builder().forBrowser("chrome").setChromeOptions(options).build();

const cr = new TraceReports();
await cr.startRun("Web regression", { framework: "selenium" });
await cr.test("Checkout", withDriver(driver, { key: "checkout.test.js > pays" }), async (t) => {
  // your test
});
await cr.finishRun();
```

`withDriver` sends the network and console of each test and, if it fails, the screenshot and DOM. It
works with any runner (node:test, Mocha, Jest). Full example:
[examples/selenium-js](../../examples/selenium-js).

## Java (JUnit 5)

```java
@ExtendWith(TraceReportsExtension.class)
class CheckoutTest {
    WebDriver driver;

    @BeforeEach
    void open(TraceTest t) {
        ChromeOptions options = SeleniumEvidence.options(new ChromeOptions());   // ← logs of network and console
        driver = new ChromeDriver(options);
        SeleniumEvidence.attach(t, driver);   // network and console of the test; screenshot and DOM if it fails
    }

    @AfterEach
    void close() { driver.quit(); }

    @Test
    void paysTheOrder() { /* your test */ }
}
```

Full example: [examples/selenium-java](../../examples/selenium-java). Edge: `SeleniumEvidence.options(new EdgeOptions())`.

## Go

The Go client is used with playwright-go, which already captures the network: see [Go](go.md).

## How it works

Chrome and Edge can write everything the page does on the network into a log ("performance"). The
helper turns on that log when the driver is created and, at the end of each test, reads it and turns it
into the same calls the Playwright capture produces. The bodies are asked to the browser through the
DevTools Protocol (CDP). Reading the log empties it, so each test gets only its own calls, even with a
driver shared by several tests.

## When something is missing

| What you see | Why, and what to do |
| --- | --- |
| No **Network** tab | The driver was not created with the helper (`selenium_options`, `seleniumOptions`, `SeleniumEvidence.options`), or it is Firefox |
| The call is there but without its body | The page navigated after that call and Chrome no longer keeps it. The status, headers and timings are there, and the Lab can still resend it |
| No body on a remote driver (Selenium Grid) | The bodies need CDP, which a remote driver may not offer. Steps, screenshot and DOM always work |
| No suggested selector | The error does not say which locator failed. With pytest it is read from the failing line (`By.ID, "pay"`); with JUnit and JS, from Selenium's message |
| Passwords or tokens in the report? | No: they are masked before they are stored (and, in Python, also before they leave the machine) |

More: [Python](python.md) · [JavaScript](javascript.md) · [Java](java.md) · [FixLab](fixlab.md).
