# REST API

🌐 **English** · [Español](../es/api.md)

Base: `http://<server>:8080/api/v1`. Everything is JSON, except screenshot uploads (multipart) and
the export (ZIP). Errors answer `{"error": "message"}`.

**Authentication**: if the server has `TRACEREPORTS_TOKEN`, writes require
`Authorization: Bearer <token>` or `X-TraceReports-Token: <token>`. If it has `TRACEREPORTS_UI_USER` /
`TRACEREPORTS_UI_PASSWORD`, reads require HTTP Basic or the same token. See
[configuration](configuration.md#security).

## Minimal flow

```bash
H='Authorization: Bearer my-token'
RUN=$(curl -s -X POST -H "$H" localhost:8080/api/v1/runs -d '{"name":"Smoke","environment":"qa"}' | jq .run_id)
TEST=$(curl -s -X POST -H "$H" localhost:8080/api/v1/runs/$RUN/tests -d '{"name":"Login","category":"smoke"}' | jq .test_id)
curl -s -X POST -H "$H" localhost:8080/api/v1/tests/$TEST/logs -d '{"status":"INFO","message":"Open login"}'
curl -s -X POST -H "$H" -F file=@login.png -F message="Form" localhost:8080/api/v1/tests/$TEST/screenshot
curl -s -X PATCH -H "$H" localhost:8080/api/v1/tests/$TEST/finish -d '{"status":"FAIL","error_message":"Timeout waiting for the Dashboard"}'
curl -s -X PATCH -H "$H" localhost:8080/api/v1/runs/$RUN/finish
```

## Writes (used by the test clients)

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/runs` | `{name, environment, project, branch, commit, framework}` | `201 {run_id}` |
| POST | `/runs/{run_id}/tests` | `{name, category, description, key, suite, params, worker}` | `201 {test_id}` |
| POST | `/tests/{test_id}/logs` | `{status, message, timestamp}` | `201` created step |
| POST | `/tests/{test_id}/screenshot` | multipart: `file` (PNG/JPEG/GIF/WEBP), `message`, `status` | `201 {url, log}` |
| POST | `/tests/{test_id}/artifact` | multipart: `file`, `kind` (`trace` or `video`), `name` | `201` artifact. Playwright trace (ZIP) or video (WebM/MP4), up to 100 MB |
| POST | `/tests/{test_id}/console` | `{entries: [{level, text, location, timestamp}]}` | `201 {stored, dropped}`. Browser console: `error`, `warning`, `pageerror`, `info`, `log`, `debug` (max 500 per test) |
| POST | `/tests/{test_id}/network` | `{connections: [Conn]}` (up to 5000 per batch) | `201 {stored, errors}` |
| POST | `/tests/{test_id}/dom` | Page snapshot on failure (see below), up to 4 MB / 2000 elements | `201` |
| PATCH | `/tests/{test_id}/finish` | `{status, error_message, error_trace, attempts}` | `200` test |
| PATCH | `/runs/{run_id}/finish` | `{interrupted}` (optional) | `200` run |

- Step `status`: `INFO`, `PASS`, `FAIL`, `WARNING`, `SKIP`. `timestamp`: epoch in ms or
  RFC 3339 (optional, default now).
- Test `status`: `PASS`, `FAIL`, `WARNING`, `SKIP`, or empty to derive it from the steps.
  A `FAIL` triggers the AI diagnosis.
- Closing the run triggers the overall diagnosis and the Teams/Slack notifications. Tests still
  running are closed as `FAIL` (*Interrupted*) and the run is marked `incomplete`; so is a run
  closed with `{"interrupted": true}` (it never shows as passed). Closing an already closed run again
  (retry, spool) keeps the `incomplete` mark and does not repeat the diagnosis or the notifications.
  A result that arrives after the close is accepted and recomputes the run status: it never stays
  `PASS` with `FAIL` or still running tests. If the result changes (status or error), the run
  summary turns pending and is rebuilt, with or without AI, without notifying again; an identical
  resend leaves it alone. If a finished test gets another result (another status, error or trace),
  its previous AI diagnosis is dropped: a new `FAIL` is diagnosed again (pending meanwhile) and a
  `PASS` keeps no failure diagnosis. Steps, network or DOM arriving after the close are stored in their test
  but do not rebuild the summary (use *Re-analyze* if needed).
- `key`: stable identity of the test inside the project (without it, history is matched by name).
  If the key or the name carry a secret (`test_login[token=…]`) it is stored masked plus a hash keyed
  per installation: the secret is not stored and each test keeps its own history.
  `project`, `environment` and `branch` define which runs it is compared with. `attempts` > 1
  means retries.
- **Idempotency**: with the header `Idempotency-Key: <unique id>` on a POST/PATCH, repeating the
  same request returns the first response without writing again (header `Idempotent-Replayed: true`).
  Use it when retrying so a timeout never duplicates steps or screenshots. Keys of writes that belong
  to a run last as long as the run (deleted by retention); the others, 24 h. When upgrading from an
  earlier version, the keys already stored are linked to their run through the path or the response
  that created the run; those of runs already deleted expire after 24 h.
- Incoming data is masked before it is stored (see [Configuration](configuration.md#sensitive-data-and-retention)).

`Conn` (all optional except `method` and `url`):

```json
{
  "method": "POST", "url": "https://app/api/login", "status": 500, "status_text": "Internal Server Error",
  "resource_type": "fetch", "mime_type": "application/json",
  "failed": false, "error_text": "", "started_at": 1790990000000, "duration_ms": 2310,
  "request_headers": {"content-type": "application/json"}, "post_data": "{\"user\":\"qa\"}",
  "response_headers": {"content-type": "application/json"}, "response_body": "{\"error\":\"...\"}",
  "body_size": 9821, "evidence_file": "C:/evidence/network_test_login.json"
}
```

A call counts as an **error** if `failed` is `true` (no response) or `status >= 400`, unless it
comes with `"expected": true`: a negative response the test checks on purpose (not counted as an
error nor as the cause of a failure).

### DOM snapshot

Sent on failure, before closing the test. With it, if the failure was a broken selector, the report
suggests replacement selectors and the AI picks the most likely one. The clients already build it
(`capturar_dom` in Python, `domScript` in the Go example):

```json
{
  "url": "https://app/login", "title": "Login", "viewport": {"w": 1366, "h": 768},
  "elements": [{"tag": "button", "type": "submit", "text": "Login", "role": "", "testid": "", "testid_attr": "",
                "id": "", "name": "", "label": "", "placeholder": "",
                "visible": true, "x": 315, "y": 560, "w": 464, "h": 46}]
}
```

### Real time of an event

Every write accepts the optional header `X-TraceReports-Timestamp: <epoch ms>`: when the event really
happened. `tracereports report` and `tracereports push` use it when replaying a
[recording made without a server](offline.md), so the report keeps the original times (start and end
of the run and of each test, steps and screenshots). Without it, the arrival time counts.

### Importing a JUnit XML report

`POST /import/junit` creates a finished run from one or more JUnit XML reports, the format almost
every runner writes (pytest `--junitxml`, Maven Surefire, Gradle, Playwright, Jest, Cypress,
gotestsum, .NET…). No client and no change to the tests are needed.

```bash
# one file as the body
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/xml" --data-binary @report.xml \
  "$URL/api/v1/import/junit?name=Nightly&project=shop&branch=main&commit=$SHA"
# several files: one "file" field per report
curl -H "Authorization: Bearer $TOKEN" -F file=@target/surefire-reports/TEST-A.xml -F file=@TEST-B.xml \
  "$URL/api/v1/import/junit?name=Nightly"
```

- Query (all optional): `name` (defaults to the suite name when there is only one, or `JUnit`),
  `environment`, `project`, `branch`, `commit`, `framework` (defaults to `junit`).
- Response `201 {run_id, status, tests, passed, failed, skipped, report}`; `report` is the link to
  the report (absolute when `PUBLIC_URL` is set).
- Each `<testcase>` becomes a test identified by `classname#name`: importing the same report every
  night builds its history, flaky detection and comparison. `<failure>` and `<error>` are `FAIL`,
  `<skipped>` is `SKIP`; Surefire reruns (`<flakyFailure>`, `<rerunFailure>`…) count as attempts.
  `<system-out>` and `<system-err>` become steps (up to 16 KB each).
- The report's times and durations are used (`timestamp` and `time`); without a `timestamp`, the
  run ends at the moment of the import.
- It goes through the same masking as everything else, and failures are diagnosed by the AI and
  notified like in a normal run. Limit: 50 MB per import. It brings no screenshots or network:
  the clients add those.

### Importing Allure results

`POST /import/allure` creates a finished run from an `allure-results` folder (allure-pytest,
allure-junit5, allure-testng, allure-playwright, allure-cucumber…) compressed as a ZIP, as the body
(`Content-Type: application/zip`) or in the `file` field of a multipart form:

```bash
cd allure-results && zip -qr ../allure.zip . && cd ..
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/zip" --data-binary @allure.zip \
  "$URL/api/v1/import/allure?project=shop&branch=main"
```

- Same query and response as `/import/junit`. Default name: the `buildName` of `executor.json`, or
  `Allure`; framework: the `framework` label of the results.
- Each `*-result.json` is a test identified by `fullName [parameters]`. `failed` and `broken` are
  `FAIL`, `skipped` (and `unknown`) is `SKIP`. Executions with the same `historyId` (retries) are
  merged into one test with its attempts, keeping the result of the last one.
- Steps (nested, indented), with their status and time, become test steps; image attachments become
  screenshots; text or JSON ones a step with their content (up to 16 KB). The `tag`, `feature`,
  `story` and `epic` labels are the categories; `parentSuite / suite / subSuite`, the suite.
- Limit: 200 MB per ZIP and 15 MB per attachment.

## Reads (used by the UI)

| Method | Path | Description |
|---|---|---|
| GET | `/config` | AI on/off, provider, model and default language |
| GET | `/metrics?days=30&suite=` | Metrics across finished runs: KPIs (and the previous period's), daily series, most failing, flaky and slowest tests, causes and tags |
| GET | `/settings` | Settings: language, AI provider (without the key: only its last 4 characters), whether they can be edited and why not |
| GET | `/auth/check` | With `X-TraceReports-Token`: `{token_required, token_sent, token_valid}`. Checks a token without writing anything |
| GET | `/runs?limit=50` | Runs with counters |
| GET | `/runs/{run_id}` | Run with its tests (includes `flaky`, `flaky_info`, `net_drift`, network counters) and `summary` (diagnosis) |
| GET | `/runs/{run_id}/compare?base={id}` | Comparison (`base` default: the related previous run) |
| GET | `/runs/{run_id}/endpoints` | Backend endpoint ranking |
| GET | `/runs/{run_id}/export` | Self-contained report ZIP |
| GET | `/tests/{test_id}` | Test with steps and diagnosis |
| GET | `/tests/{test_id}/history` | The test's latest runs (by name) |
| GET | `/tests/{test_id}/network` | The test's network calls |
| GET | `/tests/{test_id}/locator` | Broken selector and suggested replacements (`204` if the failure was not a selector) |
| GET | `/tests/{test_id}/drift` | p95 per endpoint of the test vs. the median of its previous runs |
| GET | `/stream?run={id}` | Live events (Server-Sent Events): `run`, `test`, `log`, `network`, `triage`, `summary` |
| GET | `/network/{id}/body` | Full stored body (JSON or plain text, never executable HTML) |
| GET | `/network/{id}/baseline` | The same call in the last run where the test passed (`{conn, run_id, test_id, same_context}`), or `204` |
| GET | `/screenshots/{file}` | Screenshot (outside `/api/v1`) |

## Settings (Settings screen)

| Method | Path | Body | Response |
|---|---|---|---|
| PUT | `/settings` | `{language?, ai_language?, ai?: {provider, model, base_url, api_key?}}` | Updated settings |
| POST | `/settings/ai/test` | `{provider, model, base_url, api_key?}` | `{ok, ms}` or `{ok: false, error}` (saves nothing) |
| DELETE | `/settings/ai` | — | Goes back to the environment's AI configuration |

- `provider`: `gemini`, `anthropic`, `openai`, `openai_compatible`, `ollama` or `off`.
  An empty or missing `api_key` keeps that provider's current key (the saved one or the
  environment's).
- `language`: `es` or `en` (the UI's default language). `ai_language`: `auto`, `es` or `en`.
- They require `Content-Type: application/json`. They are accepted with the token
  (`TRACEREPORTS_TOKEN`) or with the UI login. Without login nor token configured (local mode) also
  from the server's own machine, connected directly (not through a proxy). With `TRACEREPORTS_TOKEN`
  the same machine needs credentials too, unless `TRACEREPORTS_LOCAL_ADMIN=1`. `TRACEREPORTS_SETTINGS_LOCKED=1` always rejects them (`403`).

## Actions from the interface (AI and escalation)

They require `Content-Type: application/json` and the same rule as Settings: token, UI login or,
in local mode, the server's own machine.

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/ui/tests/{test_id}/analyze` | — | `202 {queued}`: diagnoses a failed test again |
| POST | `/ui/runs/{run_id}/analyze` | `{all?}` | `202 {queued}`: diagnoses the failures without a diagnosis (or all with `all`) and the run summary |
| POST | `/ui/escalate` | `{run_id, test_id, audience, lang, regenerate?}` | Summary for `business`, `qa` or `dev` (`test_id` 0 = the whole run). Cached when written by AI |
| POST | `/ui/escalate/send` | `{run_id, test_id, audience, lang, channel}` | Posts the summary to `teams` or `slack`. With `PUBLIC_URL` it includes the link and the screenshot |
| POST | `/ui/tickets` | `{run_id, test_id, provider, audience, lang, force}` | Creates a ticket in `github`, `jira` or `azure` with the summary (default for `dev`). `201 {ticket}`; if the same failure already has one, `200 {ticket, existing: true}` (unless `force`). See [Configuration](configuration.md#tickets-in-github-jira-or-azure-devops) |
| POST | `/ui/quarantine` | `{test_id, reason, owner, days}` | Quarantines the test (in its project) for `days` days (default 14, max 180). `reason` is required. Answers the quarantine and the recomputed run |
| DELETE | `/ui/quarantine/{test_id}` | `{}` | Lifts the test's quarantine |
| GET | `/quarantine?project=…` | — | Quarantines of the project (`all=1`: every project), with `active` |
| POST | `/ui/tests/{test_id}/verdict` | `{verdict, comment, author}` | Classifies the failure: `product_bug`, `test_bug`, `environment`, `data`, `flaky` or `other`. Tests in `GET /runs/{id}` and `/tests/{id}` carry `verdict`, `previous_verdict` (from an earlier run) and `owner` |
| GET | `/runs/{run_id}/tickets` | — | Tickets of the run's failures (also those opened from earlier runs of the same test) |

Related reads: `GET /runs/{run_id}/recurrence` (in how many previous runs of the same suite each
incident showed up) and `GET /runs/{run_id}/escalation?test=&audience=&lang=` (the saved summary,
or `204`). `GET /metrics` also accepts `env`, `tag`, `from` and `to` (`YYYY-MM-DD`), and
`GET /tests/{test_id}/history` accepts `limit` (up to 60).
