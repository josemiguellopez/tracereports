# JavaScript / TypeScript client

🌐 **English** · [Español](../es/javascript.md)

The client lives in [`client/js`](../../client/js) (package `tracereports`, Node 18+, no
dependencies, ESM with types). It works with **Playwright Test** (reporter + fixtures), **Selenium
WebDriver** and any runner (node:test, Mocha, Jest…).

```bash
npm install --save-dev tracereports                          # from npm
npm install --save-dev ../path/to/tracereports/client/js     # or from a checkout of this repository
```

Like the other clients, it never breaks or slows down your suite: the evidence is sent in the
background from a queue with retries and `Idempotency-Key` (a retry never duplicates a step); when the
server does not answer, methods are no-ops and `cr.delivery` tells you what did not arrive. There is no
disk queue: whatever could not be sent at the end is counted in `delivery.lost`.

**Without a server**: if the run cannot be created (server down or wrong token), the evidence is not
lost: it is recorded in `./tracereports-offline/<session>` and `tracereports report <folder>` builds the
HTML report, or `tracereports push <folder>` uploads it later. See [Without a server](offline.md).

Variables it reads: `TRACEREPORTS_URL` (default `http://localhost:8080`), `TRACEREPORTS_TOKEN`,
`TRACEREPORTS_DISABLED=1`, `TRACEREPORTS_PROJECT`, `TRACEREPORTS_RUN_NAME`, `TRACEREPORTS_ENV`, `TRACEREPORTS_RUN_ID` (join an
existing run, e.g. shards), `TRACEREPORTS_FLUSH_TIMEOUT` (seconds), `TRACEREPORTS_STRICT=1` and the branch and
commit (`TRACEREPORTS_BRANCH` / `TRACEREPORTS_COMMIT`, the most common CI variables or `git`). A variable
missing from the environment is read from the project's `.env`
([details](configuration.md#the-projects-env-in-the-clients)).

Local copy while sending: `new TraceReports({ offline: "both" })` or `TRACEREPORTS_OFFLINE=both`; see [offline recording](offline.md) for retention rules.

## Playwright Test (no test changes)

```js
// playwright.config.js
export default defineConfig({
  reporter: [["list"], ["tracereports/reporter", { runName: "Web regression", environment: "staging" }]],
  use: { screenshot: "only-on-failure" },
});
```

```js
// in your specs, import `test` from tracereports/playwright instead of @playwright/test
import { test, expect } from "tracereports/playwright";

test("invalid login", async ({ page, tracereports }) => {
  tracereports.expectResponse(401, { url: "*/auth/validate*" });   // expected negative response
  tracereports.info("Log in with a wrong password");                 // custom step
  ...
});
```

If you already extend `test`, use `withTraceReports(myTest)`.

What gets reported:

- one run per invocation (every worker reports into it), with project, branch and commit;
- each test's **stable identity**: `file > describe > title [Playwright project]`;
- `test.step` as steps, the error and stack, attached screenshots;
- **retries**: the attempts stay inside the same test and the UI shows *passed after retry*;
- the outcome follows Playwright: with `test.fail()`, failing is the expected result (reported as
  PASS, with the error as a warning step) and passing is a failure (*expected to fail*);
- with the fixtures: **every HTTP call the browser made** (Network tab) and, on failure, the **DOM
  snapshot** (locator recommender) and a screenshot if the project does not take one;
- `TRACEREPORTS_STRICT=1` (or the `strict` option) fails the run if part of the evidence did not arrive
  or the server did not confirm the run was closed (`delivery.runNotClosed`).

Full example: [`examples/playwright-js`](../../examples/playwright-js).

## Selenium WebDriver (or any runner)

```js
import { TraceReports } from "tracereports";
import { screenshot, withDriver } from "tracereports/selenium";

const cr = new TraceReports();
await cr.startRun("Web regression", { environment: "chrome", framework: "selenium" });

await cr.test("Valid login", withDriver(driver, { key: "login.test.js > ok", category: "smoke" }), async (t) => {
  await driver.get(URL);
  t.info("Login form");
  t.screenshot(await screenshot(driver), "Form", "PASS");
});

await cr.finishRun();   // waits for the queue and closes the run
```

`withDriver` attaches the **screenshot** and the **DOM snapshot** when the test fails and re-throws
the error so the runner sees it. Selenium does not expose the browser's network: for the Network tab
use Playwright or send the calls with `t.network([...])`.

Full example with `node:test`: [`examples/selenium-js`](../../examples/selenium-js).

## API

| Method | Description |
|---|---|
| `new TraceReports({ baseUrl, token, ... })` | Client (all optional) |
| `startRun(name, { environment, project, branch, commit, framework })` / `finishRun({ interrupted })` | Opens and closes the run |
| `startTest(name, { key, category, description, suite, params, worker })` | Returns a `TraceTest` (no-op if the server did not answer) |
| `test(name, opts, fn)` | Runs `fn(t)` as a test and closes it by its outcome; `opts.onFailure` attaches evidence |
| `t.info/pass/fail/warn/skip(msg)`, `t.log(status, msg)` | Steps |
| `t.screenshot(bufferOrBase64, msg, status)` | Screenshot (Playwright Buffer or Selenium base64) |
| `t.network(connections)`, `t.expectResponse(status, { url, method })` | The test's network and expected negatives |
| `t.dom(snapshot)` | Page snapshot (`DOM_SCRIPT`, `captureDom`) |
| `t.finish(status, { error, errorMessage, errorTrace, attempts })` | Closes the test. `FAIL` triggers the AI |
| `cr.delivery`, `cr.flush(ms)`, `cr.reportUrl` | Delivery status, wait for the queue, link |

Client tests: `cd client/js && npm test`.
