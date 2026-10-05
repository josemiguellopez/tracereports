# Example: Playwright for Java (JUnit 5)

🌐 **English** · [Español](README.es.md)

Three tests against the public [OrangeHRM](https://opensource-demo.orangehrmlive.com) demo (valid
login, wrong password, search an employee by Id) reported to TraceReports, plus a controlled failure
to see the AI diagnosis and the locator recommender.

```bash
# with the server running (go run ./cmd) on http://localhost:8080, or set TRACEREPORTS_URL
./gradlew test
TRACEREPORTS_DEMO_FAIL=1 ./gradlew test     # include the controlled failure
HEADLESS=0 ./gradlew test              # watch the browser
```

Uses the installed Chrome without downloading browsers (`BROWSER_CHANNEL=` and `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=0` to use Playwright's Chromium). If the server has a token, set `TRACEREPORTS_TOKEN`. Client documentation:
[docs/en/java.md](../../docs/en/java.md).
