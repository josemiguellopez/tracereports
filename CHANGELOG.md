# Changelog

All notable changes to TraceReports. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/) (pre-1.0: minor versions may change behavior).

## [Unreleased]

### Added

**FixLab: Request Lab**
- **Open in Lab** on a failed call (Network tab) or a backend incident resends the call to see
  whether the failure still happens. The Lab rebuilds a fresh session from the recording (repeats
  the recorded login: cookies, or its JSON token as `Bearer`) and reads anti-CSRF tokens live from
  the page; you only type the secrets nobody can guess. Edit the request like in Postman, point it
  to another environment (`http://localhost:3000`) and get a verdict against the run: reproduced,
  no longer fails, session missing, different error or no connection. It warns when the original
  response looks made up by the test (no `date` nor `server` headers).
- API: `GET /api/v1/network/{id}/lab` and `POST /api/v1/ui/lab/send`. Same access rule as the UI
  actions; on a shared server only the hosts the test called and `TRACEREPORTS_LAB_HOSTS`;
  link-local addresses always blocked; `TRACEREPORTS_LAB=off` disables it. Typed values are never
  stored and the answer is masked before it is shown. Design: [docs/rfc/fixlab.md](docs/rfc/fixlab.md).

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

[0.2.0]: https://github.com/josemiguellopez/tracereports/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/josemiguellopez/tracereports/releases/tag/v0.1.0
