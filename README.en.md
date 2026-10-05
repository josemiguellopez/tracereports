# TraceReports

🌐 **English** · [Español](GUIA-COMPLETA.md) · [Overview](README.md)

**The test report that tells you *why* a test failed, not just *that* it failed.**

TraceReports is a self-hosted test reporting server. It brings together in one place what today
is scattered across the HTML report, the backend logs and the QA engineer's head:

- **UI evidence**: every step with its screenshot.
- **HTTP traffic seen by the browser**: every network call the page made during the test (status,
  duration, headers and bodies), with *Copy as cURL*. This is what the browser observes of the
  backend, **not** the service's internal logs or traces: TraceReports does not capture those.
- **AI diagnosis**: classifies each failure (broken locator, backend down, logic bug,
  infrastructure) and groups the whole run into incidents by shared evidence: *"15 tests failed
  right after POST /auth/login returned 500"*. The cause the AI suggests is a hypothesis and is
  shown as such, next to the evidence it relies on.

![Run diagnosis](docs/img/diagnostico.png)

## What's included

| | |
|---|---|
| **Run diagnosis** | Groups failures by shared evidence (the same failed backend call right before the failure, or the same error signature) and explains in plain language what happened and what to do first. No failure is left out of the summary. |
| **Per-test AI Failure Triage** | Category, summary and suggestion for every failed test. With Gemini (free tier), Claude, OpenAI, any compatible API or a **local Ollama**. |
| **Broken locators** | When a selector stops working, suggests robust replacements (`get_by_role`, `data-testid`…) from the DOM at the moment of failure, ready to copy and highlighted on the screenshot. |
| **Replay** | Plays the test back step by step with its screenshots: play/pause, 1x/2x and keyboard. |
| **Network tab** | The HTTP calls the browser made in each test: OK / error, filters, highlighted JSON, HTML preview, cURL. Expected negative responses (a 401 checked on purpose) are marked and not counted as errors. |
| **Timeline** | Steps, screenshots and network calls on a single timeline, with a duration waterfall. |
| **History and flaky** | Each test's latest runs (by its stable identity, in the same project, environment and branch). Tells a **flaky** test (flips or passes after a retry) from a **persistent failure**, shows the failure rate and warns when there is little data. |
| **Latency vs. history** | Flags tests whose network got slower (p95) than in their previous runs, with per-endpoint detail. |
| **Backend mocks** | From a failed call, generates the stub for Playwright, Cypress or WireMock, with sensitive data masked. |
| **Live** | Steps show up while the test runs (Server-Sent Events), with automatic reconnection. |
| **Comparison** | New failures, fixed, still failing and slower tests compared with the previous run of the same context, or one you pick. |
| **Endpoints** | Ranking of the backend endpoints with errors and the slowest ones (p95) during the run. |
| **AI analysis** | *AI* menu: the causes of the run's failures, whether they repeat in previous runs and what to do; you can re-run the analysis (e.g. after a quota limit). |
| **Escalate** | Turns a failure into a summary for **Business**, **QA** or **Development**, with the screenshot, the failed calls and the diagnosis. Copy it as text, Slack, Markdown, formatted email or an **image**, or send it straight to Teams/Slack. |
| **Metrics** | Quality over time across runs, filterable by suite, environment, tag and date range: trend, most failing tests and the time they cost, flaky, slowest, causes and stability by category. Click a day or a test for the details. |
| **Teams / Slack** | When a run finishes, the summary reaches the channel with a *View report* button. |
| **Share** | Exports the run as a ZIP that opens without a server or internet (charts and fonts included), ideal to attach to an email. |
| **Sensitive data** | The server masks tokens, passwords, cookies and credentials (written as `key=value`, JSON, headers, Bearer/JWT or inside URLs) in the evidence and in names and test identities before storing anything, whatever client sent them; what is stored never reaches the AI, the ZIP or Teams/Slack. Not covered: free text without a key and what is visible in screenshots. Optional retention in days. |
| **Reliable delivery** | The Python client sends the evidence in the background with retries and idempotency (no duplicates, even when a spool is resent days later), merges pytest-xdist into one report and tells you if something did not arrive. |
| **Settings** | Language (English / Spanish), theme and AI provider with *Test connection*, no server restart. |
| **6 themes** | Trace, Trace Dark, Midnight, Paper, Pixel and Terminal. |

A single Go binary (UI included, embedded SQLite, no CGO). Clients for **Python** (pytest plugin),
**JavaScript/TypeScript** (Playwright Test reporter, Selenium), **Java** (JUnit 5, Selenium,
Playwright) and **Go**, plus a REST API for any other language.

| Timeline: steps + network | History and flaky |
|---|---|
| ![Timeline](docs/img/timeline.png) | ![History](docs/img/historial.png) |

## Quick start

**1. Start the server** (pick one):

```bash
docker compose up -d          # with Docker
go run ./cmd                  # with Go 1.26+
```

Open <http://localhost:8080>.

**2. Report your tests.** With pytest there are no code changes:

```bash
pip install pytest pytest-playwright tracereports
pytest --tracereports
```

With [JavaScript/TypeScript](docs/en/javascript.md) (Playwright Test or Selenium), [Java](docs/en/java.md)
(JUnit 5 with Selenium or Playwright), [Go](docs/en/go.md) or any other language through the
[REST API](docs/en/api.md).

**3. (Optional) Turn on AI and notifications** with environment variables:
`GEMINI_API_KEY` (or Claude, OpenAI, Ollama…), `TEAMS_WEBHOOK_URL`, `SLACK_WEBHOOK_URL`. AI can also
be configured under **Settings**, without a restart. See [configuration](docs/en/configuration.md).

**4. (Recommended if the server is not just for you) Protect it with a token.** Generate one with
Python (works the same on Windows, macOS and Linux):

```bash
python -c "import secrets; print(secrets.token_hex(24))"
```

Put it in the server's `.env` (`TRACEREPORTS_TOKEN=...`) and restart it. Then set **the same value**
wherever the tests run: the clients read it from the environment variable on their own.

```bash
export TRACEREPORTS_TOKEN="the-generated-token"    # macOS/Linux; PowerShell: $env:TRACEREPORTS_TOKEN = "..."
pytest --tracereports
```

**Settings → Connect your tests** has the commands for each terminal and CI, plus a button to check
that the server accepts the token. More in [configuration](docs/en/configuration.md#security).

## Examples

They all use the public [OrangeHRM](https://opensource-demo.orangehrmlive.com) demo:

| Example | For | How to run it |
|---|---|---|
| [`examples/pytest-playwright`](examples/pytest-playwright) | pytest: the minimum, just the plugin | `pytest examples/pytest-playwright --tracereports` |
| [`examples/playwright-js`](examples/playwright-js) | Playwright Test (JS): reporter + fixtures | `cd examples/playwright-js && npm install && npm test` |
| [`examples/selenium-js`](examples/selenium-js) | Selenium WebDriver with node:test | `cd examples/selenium-js && npm install && npm test` |
| [`examples/selenium-java`](examples/selenium-java) | Selenium with JUnit 5 | `cd examples/selenium-java && ./gradlew test` |
| [`examples/playwright-java`](examples/playwright-java) | Playwright for Java with JUnit 5 | `cd examples/playwright-java && ./gradlew test` |
| [`examples/playwright-go`](examples/playwright-go) | Go with playwright-go | `cd examples/playwright-go && go test -v ./...` |
| [`examples/orangehrm`](examples/orangehrm) | Page Object framework with unittest: network capture, negative tests and simulated backend failures | `python examples/orangehrm/tests/test_orangehrm_pim.py` |

The example test code and its messages are in Spanish; the report UI can be switched to English.

## Documentation

- [Installation](docs/en/installation.md): binary, Docker, first run.
- [Docker](docs/en/docker.md): day to day, data, backup and deploying on a server with HTTPS.
- [Configuration](docs/en/configuration.md): environment variables, security, AI, language, Teams/Slack.
- [Python client and pytest plugin](docs/en/python.md)
- [JavaScript/TypeScript client: Playwright Test and Selenium](docs/en/javascript.md)
- [Java client: JUnit 5, Selenium and Playwright](docs/en/java.md)
- [Go client and playwright-go](docs/en/go.md)
- [REST API](docs/en/api.md)
- [Continuous integration](docs/en/ci.md)
- [UI tokens and components](docs/en/ui-tokens.md): for new themes, components or translations.

## Structure

```
cmd/                 server (main)
internal/api         REST API, authentication, settings, ZIP export
internal/db          SQLite: runs, tests, steps, network, history, metrics, settings
internal/ai          AI diagnosis (Gemini, Claude, OpenAI, compatible APIs, Ollama)
internal/locator     selector suggestions when a locator breaks
internal/live        live events (Server-Sent Events)
internal/notify      Teams and Slack notifications
web/                 interface (HTML/CSS/JS with no build step, embedded in the binary; English and Spanish)
client/python        Python client + pytest plugin
client/js            JavaScript/TypeScript client + Playwright Test reporter
client/java          Java client + JUnit 5 extension
client/go            Go client
examples/            runnable examples
docs/en, docs/es     documentation in English and Spanish
```

## License

[Apache-2.0](LICENSE).
