# Example: Selenium WebDriver (JavaScript, node:test)

🌐 **English** · [Español](README.es.md)

Three tests against the public [OrangeHRM](https://opensource-demo.orangehrmlive.com) demo (valid
login, wrong password, search an employee by Id) reported to TraceReports, plus a controlled failure
to see the AI diagnosis and the locator recommender.

```bash
# with the server running (go run ./cmd) on http://localhost:8080, or set TRACEREPORTS_URL
npm install
npm test
TRACEREPORTS_DEMO_FAIL=1 npm test     # include the controlled failure
HEADLESS=0 npm test              # watch the browser
```

The driver is created with `seleniumOptions(...)`, so each test also brings its **network** (Network tab,
backend incidents, Lab) and **console**: see [Selenium](../../docs/en/selenium.md). Selenium Manager downloads the matching chromedriver. If the server has a token, set `TRACEREPORTS_TOKEN`. Client documentation:
[docs/en/javascript.md](../../docs/en/javascript.md).
