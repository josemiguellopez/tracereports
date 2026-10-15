# Allure: your Allure results, diagnosed

🌐 **English** · [Español](../es/allure.md)

Already using Allure? Keep it. Send the `allure-results` folder your tests already write and
TraceReports does what an Allure report leaves to you:

- **Each failure diagnosed with AI** (category, summary and what to check first), and the whole run
  grouped into incidents. No more opening the Allure report test by test.
- History per test, **flaky vs. broken**, run comparison, metrics, the release view, escalation to
  Teams/Slack and tickets.
- The steps (nested), screenshots, text and JSON attachments, retries and labels you already have.
- The command to run each failed test locally (pytest, Playwright, Maven/Gradle).

Nothing to change in your tests and no TraceReports client to install.

> **See it first:** `tracereports demo` loads the run *Shop · Allure (allure-pytest)*, the
> allure-results of a pytest + Playwright suite that only uses Allure. See the
> [quickstart](quickstart.md).

## 1. Your tests write allure-results

What you probably already have:

| Framework | Folder |
| --- | --- |
| pytest + allure-pytest | `pytest --alluredir=allure-results` |
| Playwright Test + allure-playwright | `allure-results` (or the `resultsDir` of the reporter) |
| JUnit 5 + allure-junit5 (Gradle) | `build/allure-results` |
| JUnit 5 / TestNG + Allure (Maven) | `target/allure-results` |
| Cucumber, Jest, Vitest, Cypress, behave… with their Allure adapter | the folder of the adapter |

## 2. Send it to TraceReports

```bash
tracereports push allure-results -project shop -name "Nightly #42" -branch main
```

- It prints the link to the run. The AI diagnosis arrives a few seconds later, live.
- `-url` (default `$TRACEREPORTS_URL` or `http://localhost:8080`) and `-token` (default
  `$TRACEREPORTS_TOKEN`) choose the server. A ZIP of the folder works too.
- `-name` defaults to the `buildName` of `executor.json`, or `Allure`. Also `-environment`.
- Each push creates a new run: push once per test run.

In CI, after the tests and even when they fail:

```yaml
# GitHub Actions
- name: Send the Allure results to TraceReports
  if: always()
  run: tracereports push allure-results -project shop -name "CI #${{ github.run_number }}" -branch "${{ github.ref_name }}"
  env:
    TRACEREPORTS_URL: ${{ vars.TRACEREPORTS_URL }}
    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
```

### Without a server

```bash
tracereports report allure-results -o report --ai
```

Builds a static HTML report (open `report/index.html`, no internet needed) with the AI diagnosis,
using the provider of your environment (`AI_PROVIDER` + `AI_API_KEY`, `GEMINI_API_KEY`…). Without
`--ai`, the same report without the diagnosis. Good for a CI artifact.

### With the API

```bash
cd allure-results && zip -qr ../allure.zip . && cd ..
curl -H "Authorization: Bearer $TRACEREPORTS_TOKEN" -H "Content-Type: application/zip" \
  --data-binary @allure.zip "$TRACEREPORTS_URL/api/v1/import/allure?project=shop&branch=main"
```

See [API · Importing Allure results](api.md#importing-allure-results).

## What it takes from Allure

| Allure | TraceReports |
| --- | --- |
| Each `*-result.json` | A test, identified by `fullName` and its parameters |
| `passed` / `failed`, `broken` / `skipped`, `unknown` | Pass / Fail / Skip |
| Results with the same `historyId` (retries) | One test with its attempts: flaky when a retry passed |
| Steps (nested) with their status and time | Steps of the test |
| Image attachments | Screenshots, in the steps and the replay |
| Text, JSON, CSV, HTML, URI attachments | A step with their content (up to 16 KB) |
| `tag`, `feature`, `story`, `epic` labels | Categories |
| `parentSuite` / `suite` / `subSuite` | The suite |
| `executor.json` `buildName`, the `framework` label | Run name and framework |

Tested with the results of [allure-examples](https://github.com/allure-examples): allure-pytest,
allure-playwright and allure-junit5.

## Limits

- Allure does not record the browser's network, so there is no **Network** tab, backend incident or
  [Lab](fixlab.md) for these runs, and the AI works from the error, the steps and the attachments.
  For that evidence, add the TraceReports client of your framework: it can live next to Allure.
- The broken-locator suggestions need the page's DOM, which Allure does not keep.
- Limits per import: 200 MB per ZIP and 15 MB per attachment.
