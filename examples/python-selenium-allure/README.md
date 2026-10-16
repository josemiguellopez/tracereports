# Example: pytest + Selenium + Allure

🌐 **English** · [Español](README.es.md)

For whoever already automates with **Python, Selenium and Allure**: the usual tests against the
public OrangeHRM demo, and two ways to add TraceReports without leaving Allure.

Requirements: Python 3.9+, Chrome, a TraceReports server
([quickstart](../../docs/en/quickstart.md): `tracereports demo` works) and the `tracereports` command:
download it from the [latest release](https://github.com/josemiguellopez/tracereports/releases/latest)
or build it with `go build -o tracereports ./cmd` (on Windows `tracereports.exe`, run as
`.\tracereports.exe`). From the repository root:

```bash
python -m pip install -r examples/python-selenium-allure/requirements.txt ./client/python
```

## Level 1: keep Allure and send its results

Without changing a line of your tests:

```bash
python -m pytest examples/python-selenium-allure --alluredir=allure-results
tracereports push allure-results -project orangehrm -name "Selenium + Allure"
```

`push` prints the link to the run. Every failure comes with its AI diagnosis (for example
*LOCATOR_CHANGED: the selector `#menu_pim_viewPimModule` no longer exists*), plus history, flaky
tests, metrics, escalation to Teams/Slack and tickets. Your `allure.step` steps, screenshots and
attachments are kept. More in the [Allure guide](../../docs/en/allure.md).

`--alluredir` only writes the raw results (JSON and screenshots), not a report. To see the usual
Allure report (needs Java and Node, nothing installed globally):

```bash
npx -y allure-commandline generate --single-file allure-results -o allure-report --clean
```

and open `allure-report/index.html`. Delete `allure-results` before each run: Allure does not clean
it and adds up the previous results.

## Level 2: Allure and TraceReports in the same run

```bash
python -m pytest examples/python-selenium-allure --alluredir=allure-results --tracereports
```

Allure writes its results as always and TraceReports also gets the run **live**, with each test's
**network** (Network tab, backend incidents, [Lab](../../docs/en/fixlab.md)), **console**, and the
**screenshot** and **suggested selector** when it fails. What [conftest.py](conftest.py) adds, ready to
copy into your project:

- `selenium_options(webdriver.ChromeOptions())`: the one line that turns on Chrome's network and
  console logs. The plugin does the rest.
- `paso("text")`: an `allure.step` that is also a step in TraceReports.
- When a test fails, the browser screenshot also goes to Allure.

Without `--tracereports`, the `tracereports` fixture does nothing: the suite is the same as always.

## See a failure diagnosed

The tests pass against the demo. To see the diagnosis, add a controlled failure (a selector the menu
no longer has):

```bash
TRACEREPORTS_DEMO_FAIL=1 python -m pytest examples/python-selenium-allure --alluredir=allure-results
```

On PowerShell: `$env:TRACEREPORTS_DEMO_FAIL="1"`. It uses `python -m pytest` (and `python -m pip`)
because that works even when the Python `Scripts` folder is not in the PATH. `HEADLESS=0` shows the browser.

## Without a server

```bash
tracereports report allure-results -o report --ai
```

An HTML report (`report/index.html`) with the AI diagnosis of the provider in your environment, to
keep as a CI artifact.

## Level 1 or level 2?

Level 1 (only `allure-results`) gives the AI diagnosis, history and everything Allure recorded. Allure
does not record the browser's network nor the page's DOM, so level 2 adds what only TraceReports
captures: the Network tab, backend incidents, the Lab and the suggested selector. Both work with
Chrome and Edge; more in [Selenium](../../docs/en/selenium.md).
