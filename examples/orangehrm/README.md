# Example: Page Object framework with unittest

🌐 **English** · [Español](README.es.md)

A "real" automation framework (Page Objects, separate locators, a workflow of several tests that
depend on each other) reporting to TraceReports with the Python client.

```bash
pip install playwright ./client/python      # from the repository root
python examples/orangehrm/tests/test_orangehrm_pim.py
```

| File | What it shows | Expected result |
|---|---|---|
| `tests/test_orangehrm_pim.py` | Login → Dashboard and PIM scraping → search → logout. If a precondition step fails, the following ones are SKIPped with the reason | pass |
| `tests/test_orangehrm_login_invalido.py` | Negative cases, also checked on the network: 302, no request, 401, 404 | pass |
| `tests/test_orangehrm_errores_backend.py` | Simulated backend failures (down, 500, timeout) and an outdated selector, to see a complete failure: screenshot, error with the failed call, red network, diagnosis and suggested locators | **fail on purpose** |

`HEADLESS=0` shows the browser, and `BROWSER_CHANNEL=chrome` uses your Chrome instead of
Playwright's Chromium. Local evidence (screenshots, network JSON and log) ends up in
`examples/orangehrm/output/`. With `TRACEREPORTS_OFFLINE=both` a copy of the report also stays in
`output/tracereports/<run>/report/index.html`, even if the server does not answer.

The test code, step messages and file names are in Spanish, as in the team this framework comes
from; the report UI can be switched to English under Settings.

Structure:

```
tests/base_test.py                shared infrastructure: browser, network capture, test lifecycle
pages/                            Page Objects (each action checks its result and returns a bool)
locators/                         selectors
utils/function.py                 Playwright helpers (waits, reads, checks)
utils/tracereports_context.py   steps + screenshots to TraceReports
data/data_test.json               test data
```
