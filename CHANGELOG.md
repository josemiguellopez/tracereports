# Changelog

All notable changes to TraceReports. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/) (pre-1.0: minor versions may change behavior).

## [Unreleased]

### Added

**Selenium: network, console and suggested selectors**
- The clients read Chrome's and Edge's performance and browser logs, so Selenium tests get the **Network** tab
  (with the bodies of the APIs and errors, through CDP), backend incidents, the **Lab** and the **Console** tab,
  like Playwright. One line where the driver is created:
  - Python: `tracereports.selenium.selenium_options(options)`; with `pytest --tracereports` the plugin finds the
    driver in the test's fixtures and sends each test's network and console, and the screenshot and DOM when it
    fails. Without pytest, `report_selenium(driver, cr)`.
  - JavaScript: `seleniumOptions(options)`; `withDriver` now also sends the network and console. `cr.test` gets
    `onStart` and `onEnd` hooks.
  - Java: `SeleniumEvidence.options(options)`; `SeleniumEvidence.attach` now also sends the network and console
    (`CdpNetwork` turns the log into connections).
- Suggested selector for Selenium with pytest: the locator is read from the failing line of the traceback
  (`By.CSS_SELECTOR, "#menu"`), since a `TimeoutException` does not name it.
- The demo's Selenium scenarios now carry the browser's network.
- [Selenium guide](docs/en/selenium.md) (English and Spanish), linked from the client guides, their READMEs and
  the examples, which now create the driver with the helper.

## [0.3.0] - 2026-10-15

The clients did not change: Python 0.3.0, JavaScript 0.2.0, Java 0.2.0 and the Go client keep working with it.

### Added

**Try it in a minute**
- `tracereports demo`: the server with runs recorded with the real clients (pytest + Playwright, Playwright Test,
  Selenium + JUnit 5, `go test`, REST and a JUnit XML import; 12 nights of history plus tonight's suites) and a demo
  shop whose page is a **playground**: break or fix its backend to try the Lab, and send any scenario or all of them,
  with the requests sent, the checks and a link to the run. Temporary, only on this machine and isolated from your
  configuration. The playground is animated with [Motion](https://motion.dev) (MIT, bundled: works offline).
  It diagnoses with AI when your environment or `.env` has a provider key (only the AI variables are read from
  the `.env`; `-ai=false` turns it off) and says so in the guide it prints.
- `TRACEREPORTS_BIND` chooses the address to listen on (`127.0.0.1` = only this machine).
- `tracereports selftest`: plays the scenarios recorded with the real clients (pytest + Playwright, Playwright Test,
  Selenium + JUnit 5, `go test`, REST, JUnit XML) against a temporary server or `-url`, and checks what TraceReports
  shows (statuses, incidents, masking, screenshots, suggested selectors, flaky tests, local commands, the Lab).
  Exit code 1 on a failed expectation. The same check runs in CI (`go test ./internal/demo`). With `-url` it sends
  `TRACEREPORTS_TOKEN` to a protected server.
- The Lab finds the session token in a fresh login answer when the recorded one has no body (a page that
  navigates right after signing in).

**Command line**
- `tracereports version` (also `-version`, `--version`); release builds report their version (also to MCP
  clients, which got `dev` before).
- `tracereports report -ai` takes the AI provider from the `.env` too, like the server and the demo.

**Allure**
- `tracereports push` also takes an `allure-results` folder (or its ZIP) and JUnit XML: it imports them into
  the server, where every failure gets the AI diagnosis (`-name`, `-project`, `-branch`, `-environment`, and the
  token of `-token` / `TRACEREPORTS_TOKEN`). Before, only `curl` with a ZIP.
- The demo, the playground and `selftest` include an Allure scenario: the allure-results of a pytest +
  Playwright suite that only uses Allure, recorded against the demo shop.
- [`examples/python-selenium-allure`](examples/python-selenium-allure): pytest + Selenium + Allure, sending the
  allure-results or adding the plugin to the same run (screenshots and steps to both).
- [Allure guide](docs/en/allure.md), tested with the results of allure-pytest, allure-playwright and
  allure-junit5 from [allure-examples](https://github.com/allure-examples).

**FixLab: Request Lab**
- **Open in Lab** on a failed call (Network tab) or a backend incident resends the call to see
  whether the failure still happens. The Lab rebuilds a fresh session from the recording (repeats
  the recorded login: cookies, or its JSON token as `Bearer`) and reads anti-CSRF tokens live from
  the page; you only type the secrets nobody can guess. Edit the request like in Postman, point it
  to another environment (`http://localhost:3000`) and get a verdict against the run: reproduced,
  no longer fails, session missing, different error or no connection. It warns when the original
  response looks made up by the test (no `date` nor `server` headers).
- **Save as a check** (the call plus the expected status and texts) and **Verify** on the incident:
  runs its checks and records **Verified: fixed** or **still failing**, with the before and after.
- **Capsule** (JSON with the context, the test command and each call with its checks) and two new
  commands: `tracereports repro <capsule|URL>` resends the calls from your machine and
  `tracereports verify <capsule|URL>` runs the checks, exit code 1 if one fails (CI). The report
  copies both commands. Values with `-var`, `TRACEREPORTS_LAB_<NAME>` or a hidden prompt.
- `tracereports mcp`: MCP server for Claude Code and other agents (`list_runs`, `get_incidents`,
  `get_test`, `reproduce`, `verify_fix`). Read-only on TraceReports; secrets stay in the MCP
  server's environment and never reach the agent.
- **Export** a Lab request to Postman (collection with the login script), Bruno (`.bru`) or `.http`
  (VS Code REST Client), keeping the `{{variables}}` and no values.
- API: `GET /api/v1/network/{id}/lab`, `GET /api/v1/network/{id}/capsule`,
  `GET /api/v1/runs/{id}/lab/capsule`, `POST /api/v1/ui/lab/send`, `POST|DELETE /api/v1/ui/lab/checks`,
  `POST /api/v1/ui/lab/verify` and `GET /api/v1/runs/{id}/lab`. Same access rule as the UI
  actions; on a shared server only the hosts the test called and `TRACEREPORTS_LAB_HOSTS`;
  link-local addresses always blocked; `TRACEREPORTS_LAB=off` disables it. Typed values are never
  stored and the answer is masked before it is shown. Design: [docs/rfc/fixlab.md](docs/rfc/fixlab.md).

**Docs**
- A [command line reference](docs/en/cli.md): every `tracereports` command, its flags, exit codes and which
  configuration (environment or `.env`) it reads.
- A [quickstart](docs/en/quickstart.md) to start the demo's services step by step, and guides for
  [FixLab](docs/en/fixlab.md) and for the [demo, playground and selftest](docs/en/demo.md), in English
  and Spanish, linked from the READMEs, installation, configuration, API and CI.

### Fixed

- A failure whose element never showed up because the backend failed right before (no answer, 5xx, 408, 429) is no
  longer diagnosed as a broken selector: no misleading suggestions, neither on the screen nor in the AI prompt.
- The broken selector is the one Playwright was waiting for, not an earlier getter the traceback quotes.
- Without network evidence (Allure or JUnit imports), a failure the AI attributes to the backend or the
  infrastructure no longer shows a "broken selector" card with nothing to suggest.
- Allure import: a label or parameter with a number, boolean or null value (`allure.id(123)`, parametrized
  tests) no longer rejects the whole import.
- The command to run a test locally for Allure imports: pytest gets a node id (`pkg/module.py::test`, not the
  Allure `pkg.module#test`), and allure-playwright (`file:line`) and allure-junit5 (`package.Class.method`) get
  one too.
- `tracereports secrets -h` prints its usage instead of an error; `tracereports help` lists `version` and
  describes `push` and `report` with their Allure and JUnit inputs.
- `tracereports demo -keep`: the kept data is no longer deleted by the cleanup of old demos 12 hours later.
- Without AI, a failed test no longer asks only for `GEMINI_API_KEY`: it points to Settings → AI or `AI_PROVIDER`
  (in the demo, to the `.env`), and the message is translated in English.

## [0.2.0] - 2026-10-09

Client versions released with it: **Python 0.3.0** (PyPI), **JavaScript 0.2.0** (npm),
**Java 0.2.0** and **Go `client/go/v0.2.0`**.

### Added

**Find and share**
- **Search** view (`GET /api/v1/runs/search`, `/runs/facets`): search runs by name, project, branch,
  commit or test name; filters as removable chips, saved searches, shareable URLs, a preview panel
  (incidents, failed tests, release decision, changes) and compare from the list.
- **Ctrl/Cmd+K** command palette: runs, tests of the open run and views. **Recents** menu in the header.

**Without a server, and a report on your PC**
- `tracereports report` builds the static HTML report from a recording, a JUnit XML file or Allure
  results; `tracereports push` uploads a recording later.
- Clients record the evidence locally when the run cannot be created (server down or wrong token).
- `TRACEREPORTS_OFFLINE=both`: send to the server **and** keep a local copy of every run, one folder
  per run (`TRACEREPORTS_OFFLINE_BASE`, named after the run), with its HTML report built at the end.
- The clients download the report binary from GitHub Releases when it is missing (SHA-256 verified,
  per-user cache; `TRACEREPORTS_BIN_DOWNLOAD=0` turns it off).
- Exported reports include **Escalate** (template summaries) and the **Release** decision.
- Guide: [Local report on your PC](docs/en/local-report.md).

**Imports and CI**
- `POST /api/v1/import/junit` and `/import/allure`.
- `tracereports pr-comment`: the run summary on GitHub pull requests and GitLab merge requests.
- `TRACEREPORTS_INGEST_TOKEN`: a restricted token for CI that cannot change Settings.

**For QA and developers**
- Quarantine of known flaky tests (owner, reason, expiry), test owners from CODEOWNERS-like rules
  and failure verdicts remembered across runs.
- Playwright trace and video per test (Trace Viewer link), browser console capture in every client.
- Command to rerun a failed test locally at its commit; compare a failed call with the last time the
  test passed; links from failed calls to backend logs and traces.
- Technical detail for developers in escalations and tickets.

**For the team**
- **Release** view ("can we ship?") with a configurable gate (`TRACEREPORTS_RELEASE_GATE`).
- Tickets in GitHub, Jira and Azure DevOps from Escalate, without duplicates.
- Weekly quality summary to Teams/Slack (`TRACEREPORTS_WEEKLY_SUMMARY`).
- Settings → **AI usage**: calls, tokens, response times, errors by reason and the provider's status;
  Escalate shows how long the AI answer took and why it retried.
- Webhook deliveries are stored and retried; abandoned runs are closed as incomplete
  (`TRACEREPORTS_STALE_RUN_HOURS`).

### Changed

- Run, notification, PR comment and escalation texts describe the real state of a run: in progress,
  incomplete, skipped or with warnings are no longer presented as successful.
- The pass rate is `PASS` over the tests that ran, the same everywhere (a `WARNING` is not a pass).
- The automatic AI budget (`TRACEREPORTS_AI_MAX_PER_RUN`) counts one diagnosis per test result.
- Network evidence is sent in batches by size (8 MiB / 200 calls); `body_size` is in UTF-8 bytes and
  `body_truncated` marks a body that arrived already cut (also in the TypeScript types).
- The header run selector became **Recents** plus **Search all**.

### Fixed

- Security and robustness fixes from several external audits, among them: recordings can only
  replay evidence calls; secrets are masked in exported JSON, long or escaped keys and values cut
  at the end; credentials are scrubbed from AI and tracker errors; ticket creation never duplicates;
  late or stale web responses no longer overwrite the current selection; cached escalations follow
  the evidence; endpoint drift keeps hosts apart; generated mocks are valid code.
- AI usage counts every provider attempt (schema fallback, SDK retries, connection test).

### Upgrade notes

- **Back up `DATA_DIR` first**: the first start adds tables and columns, and builds the search index
  (it can take a few seconds on a large history; later starts are immediate).
- An environment variable defined **empty** now wins over the project's `.env` in the clients, as on
  the server.
- Do not set `TRACEREPORTS_OFFLINE_DIR` in an everyday `.env`: it is the folder shared by the
  processes of one run. Use `TRACEREPORTS_OFFLINE_BASE` for one folder per run.
- Escalation summaries cached by 0.1.0 are written again once (the cache now follows the evidence).
- If the server asks for it on start: `TRACEREPORTS_AI_BUDGET_RECOVERY` or `TRACEREPORTS_LEGACY_DB`
  (see [Configuration](docs/en/configuration.md)).

## [0.1.0] - 2026-10-04

First public release: self-hosted report server (single Go binary with SQLite), web UI with six
themes, AI diagnosis (Gemini, Claude, OpenAI, compatible APIs, Ollama), escalation summaries,
Teams/Slack notifications and clients for Python, JavaScript/TypeScript, Java and Go.

[Unreleased]: https://github.com/josemiguellopez/tracereports/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/josemiguellopez/tracereports/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/josemiguellopez/tracereports/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/josemiguellopez/tracereports/releases/tag/v0.1.0
