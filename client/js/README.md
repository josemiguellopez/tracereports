# tracereports (JavaScript client)

🌐 **English** · [Español][readme-es]

Client for the [TraceReports][tracereports] server: Playwright Test (reporter + fixtures),
Selenium WebDriver and any runner. Node 18+, no dependencies.

```bash
npm install --save-dev tracereports
```

- **Playwright Test**: the reporter sends each test with its steps, error and screenshots, plus the
  traces and videos Playwright keeps; with the fixtures, also the network the browser saw, the DOM
  at the failure and the browser console.
- **Selenium WebDriver or any runner**: with `seleniumOptions(options)` and `withDriver`, each test's network
  (Network tab, Lab) and console, and the screenshot and DOM on failure ([Selenium guide][selenium]).
- **Never slows down or breaks your tests**: evidence goes out in the background with retries and
  no duplicates; shards can join the same run.
- **Without a server**: if the server is down or rejects the token, the run is recorded locally;
  `tracereports report <folder>` builds the HTML report and `tracereports push <folder>` uploads it later.
  With `TRACEREPORTS_OFFLINE=both` it also keeps that local copy while sending to the server
  ([local report guide][local-report]).
- **Configuration** from the environment or the project's `.env` (`TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`…).

Full documentation: [docs/en/javascript.md][javascript].

Without an installed binary, closing downloads and verifies the v0.2.0 renderer in the user cache; `TRACEREPORTS_BIN_DOWNLOAD=0` supports offline use with an installed or cached binary. See [offline mode][offline].

[tracereports]: https://github.com/josemiguellopez/tracereports
[selenium]: https://github.com/josemiguellopez/tracereports/blob/main/docs/en/selenium.md
[local-report]: https://github.com/josemiguellopez/tracereports/blob/main/docs/en/local-report.md
[javascript]: https://github.com/josemiguellopez/tracereports/blob/main/docs/en/javascript.md
[offline]: https://github.com/josemiguellopez/tracereports/blob/main/docs/en/offline.md
[readme-es]: https://github.com/josemiguellopez/tracereports/blob/main/client/js/README.es.md
