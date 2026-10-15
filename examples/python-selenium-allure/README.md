# Example: pytest + Selenium + Allure

🌐 **English** · [Español](README.es.md)

For whoever already automates with **Python, Selenium and Allure**: the usual tests against the
public OrangeHRM demo, and two ways to add TraceReports without leaving Allure.

Requirements: Python 3.9+, Chrome and a TraceReports server
([quickstart](../../docs/en/quickstart.md): `tracereports demo` works). From the repository root:

```bash
pip install -r examples/python-selenium-allure/requirements.txt ./client/python
```

## Level 1: keep Allure and send its results

Without changing a line of your tests:

```bash
pytest examples/python-selenium-allure --alluredir=allure-results
tracereports push allure-results -project orangehrm -name "Selenium + Allure"
```

`push` prints the link to the run. Every failure comes with its AI diagnosis (for example
*LOCATOR_CHANGED: the selector `#menu_pim_viewPimModule` no longer exists*), plus history, flaky
tests, metrics, escalation to Teams/Slack and tickets. Your `allure.step` steps, screenshots and
attachments are kept. More in the [Allure guide](../../docs/en/allure.md).

## Level 2: Allure and TraceReports in the same run

```bash
pytest examples/python-selenium-allure --alluredir=allure-results --tracereports
```

Allure writes its results as always and TraceReports also gets the run **live**. What
[conftest.py](conftest.py) adds, ready to copy into your project:

- `paso("text")`: an `allure.step` that is also a step in TraceReports.
- When a test fails, the browser screenshot goes to Allure and to TraceReports.

Without `--tracereports`, the `tracereports` fixture does nothing: the suite is the same as always.

## See a failure diagnosed

The tests pass against the demo. To see the diagnosis, add a controlled failure (a selector the menu
no longer has):

```bash
TRACEREPORTS_DEMO_FAIL=1 pytest examples/python-selenium-allure --alluredir=allure-results
```

On PowerShell: `$env:TRACEREPORTS_DEMO_FAIL="1"`. `HEADLESS=0` shows the browser.

## Without a server

```bash
tracereports report allure-results -o report --ai
```

An HTML report (`report/index.html`) with the AI diagnosis of the provider in your environment, to
keep as a CI artifact.

## What Selenium in Python does not have (yet)

With Selenium, the Python client captures neither the **browser's network** nor the **DOM** on
failure, so there is no Network tab, [Lab](../../docs/en/fixlab.md) or suggested selector; the
diagnosis uses the error, the steps and the screenshot. The network is captured with Playwright
(Python, JS, Java, Go); the DOM, and with it the suggested selector, also with Selenium in Java and JS.
