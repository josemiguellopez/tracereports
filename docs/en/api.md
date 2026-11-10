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
| POST | `/runs` | `{name, environment, project, branch, commit, framework, execution_id, planned}` | `201 {run_id}` |
| POST | `/runs/{run_id}/tests` | `{name, category, description, key, suite, params, worker}` | `201 {test_id}` |
| POST | `/tests/{test_id}/logs` | `{status, message, timestamp}` | `201` created step |
| POST | `/tests/{test_id}/screenshot` | multipart: `file` (PNG/JPEG/GIF/WEBP), `message`, `status` | `201 {url, log}` |
| POST | `/tests/{test_id}/artifact` | multipart: `file`, `kind` (`trace` or `video`), `name` | `201` artifact. Playwright trace (ZIP) or video (WebM/MP4), up to 100 MB |
| POST | `/tests/{test_id}/console` | `{entries: [{level, text, location, timestamp}]}` | `201 {stored, dropped}`. Browser console: `error`, `warning`, `pageerror`, `info`, `log`, `debug` (max 500 per test) |
| POST | `/tests/{test_id}/network` | `{connections: [Conn]}` (up to 5000 per batch, 48 MiB per request) | `201 {stored, errors}`. `body_size` is the original size in UTF-8 bytes; send `body_truncated: true` when the body arrives already cut, so the report does not show it as complete |
| POST | `/tests/{test_id}/dom` | Page snapshot on failure, or of a step with `kind: "step"` (see below), up to 4 MB / 2000 elements / 6000 nodes | `201` |
| PATCH | `/tests/{test_id}/finish` | `{status, error_message, error_trace, attempts}` | `200` test |
| PATCH | `/runs/{run_id}/finish` | `{interrupted, delivery}` (optional) | `200` run. `delivery`: how the evidence arrived according to the client, `{late, late_at, reason, detail, retried, rejected, dropped, lost, spooled, client, host}`; stored only when something went wrong, shown in the report and in Maintenance > Connections |
| POST | `/runs/{run_id}/plan` | `{planned}`: how many tests the client collected (progress and ETA in Live) | `200` |

- Step `status`: `INFO`, `PASS`, `FAIL`, `WARNING`, `SKIP`. `timestamp`: epoch in ms or
  RFC 3339 (optional, default now).
- Test `status`: `PASS`, `FAIL`, `WARNING`, `SKIP`, or empty to derive it from the steps.
  A `FAIL` triggers the AI diagnosis. A `PASS` with a `WARNING` step is stored as `WARNING`, unless
  `attempts` > 1 (it passed after a retry). The server also uses `INTERRUPTED` (it is not sent by
  clients): a test cut before it finished.
- Closing the run triggers the overall diagnosis and the Teams/Slack notifications. Tests still
  running are closed as `INTERRUPTED` (neither a failure nor a pass: out of the pass rate, flaky and
  the release gate, and not diagnosed by the AI) and the run is marked `incomplete` (`WARNING` unless
  something failed); so is a run closed with `{"interrupted": true}` (it never shows as passed). Closing an already closed run again
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
  Guarantees if the server crashes: creating runs and tests, steps, network, console, DOM, the
  records of screenshots and artifacts and verdicts store the write and its response in **one transaction**:
  both or neither, so a retry never duplicates them. The file of a screenshot or artifact is written
  first: a crash in between leaves an unused file, not duplicated evidence. Finishing tests and runs
  and quarantine set a state: repeating them gives the same result. Imports (JUnit, Allure)
  use several transactions: a crash halfway leaves an incomplete run and the retry imports again.
  What leaves the server (AI, Teams, Slack, tickets) is not *exactly once*.
- `TRACEREPORTS_INGEST_TOKEN` works for all these writes like `TRACEREPORTS_TOKEN`, but not for
  Settings nor the `/ui/*` actions (see [Permissions](configuration.md#permissions-of-each-credential)).
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

The Python client also sends `page` (`{w, h}`, the whole page) and `nodes`, the page as a flat tree that
the **Inspector** redraws: each node has `i` (its index), `p` (the parent's index, `-1` for `body`; a
parent always comes first), `tag`, `attrs`, its own `text`, its box (`x`, `y`, `w`, `h`, page
coordinates), `visible`, `depth`, `tx`/`ty` (where the text starts inside the box) and `style`. The server
keeps only known attributes (`id`, `class`, `name`, `role`, `type`, `placeholder`, `href`, `title`, `for`,
`alt`, `data-test*`, `aria-*`) and styles (colors, font, border, display, opacity…; no `url()`), masks
secrets in texts and values, and drops a tree that is not well formed. With `"kind": "step"` and
`"label"` it is the page of a step (up to 40 per test); without them, the page at the failure.

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
- Limits: 200 MB per ZIP and 15 MB per attachment; what is read decompressed, 1 GB in total and
  256 MB across the `*-result.json` files (it measures what is actually read, not what the ZIP
  declares). Past them it answers `413`. The ZIP is kept in a temporary file while importing (always
  deleted) and up to 2 imports run at a time; the rest wait for their turn.

## Reads (used by the UI)

| Method | Path | Description |
|---|---|---|
| GET | `/config` | AI on/off, provider, model and default language |
| GET | `/metrics?days=30&suite=` | Metrics across finished runs: KPIs (and the previous period's), daily series, most failing, flaky and slowest tests, causes and tags |
| GET | `/settings` | Settings: language, AI provider (without the key: only its last 4 characters), whether they can be edited and why not |
| GET | `/auth/check` | With `X-TraceReports-Token`: `{token_required, token_sent, token_valid}`. Checks a token without writing anything |
| GET | `/runs?limit=50` | Runs with counters (`total`, `passed`, `failed`, `skipped`, `warning`, `running`, `interrupted`, `quarantined`). A run still `RUNNING` carries `idle_ms` (time since it last received anything); a closed one may carry `closed_by` (a UI user, `manual` without a login, or `auto` for inactivity) and `close_reason` |
| GET | `/maintenance/connections` | Client connections: `{days, runs, slow, slow_after_ms, started_at, auth}`. `runs`: the runs of the last 7 days with a delivery report (created late, retries, lost); `slow`: the last 50 requests over 2 s since the server started; `auth` (Settings permission only): the addresses with rejected credentials in the last 15 min |
| GET | `/maintenance?state=&project=&environment=&branch=&older_than_hours=` | Runs with problems across projects, newest first (up to 200): `state` empty (running or incomplete), `idle` (running, no activity for 5 min), `running`, `incomplete`, `manual` (closed by hand) or `auto` (closed for inactivity). Answers `{runs, idle_warn_ms, stale: {hours, source, editable}}`; `source` is `env`, `ui` or `default` |
| GET | `/runs/search?...` | Paginated history for Search: `{items, next_cursor, total_estimate}` |
| GET | `/runs/facets?...` | Frequent filter values for Search: project, environment, branch and tag |
| GET | `/runs/{run_id}` | Run with its tests (includes `flaky`, `flaky_info`, `net_drift`, network counters) and `summary` (diagnosis) |
| GET | `/runs/{run_id}/compare?base={id}` | Comparison (`base` default: the related previous run): `base_run`, `base_reason`, `new_failures`, `fixed`, `still_failing`, `changed` (status changed without a failure, e.g. `PASS` → `SKIP`), `unchanged`, `slower`, `new_tests` (only in this run), `missing` (only in the base), `common` (tests in both: `0` means nothing to compare) and `base_later` (the base started after this run) |
| GET | `/runs/{run_id}/baselines` | Up to 100 finished runs of the same project and environment that can be the base, newest first, each with `shared` (tests in common with this run) |
| GET | `/runs/{run_id}/endpoints` | Backend endpoint ranking |
| GET | `/runs/{run_id}/export` | Self-contained report ZIP |
| GET | `/tests/{test_id}` | Test with steps and diagnosis |
| GET | `/tests/{test_id}/history` | The test's latest runs (by name) |
| GET | `/tests/{test_id}/network` | The test's network calls |
| GET | `/tests/{test_id}/locator` | Broken selector and suggested replacements (`204` if the failure was not a selector) |
| GET | `/tests/{test_id}/dom` | Page snapshot at the failure, as stored (`204` if there is none) |
| GET | `/tests/{test_id}/dom/steps` | Step snapshots: `[{seq, label, at}]` |
| GET | `/tests/{test_id}/dom/steps/{seq}` | One step snapshot (`404` if it does not exist) |
| GET | `/tests/{test_id}/dom/baseline?label=` | The page of the same step in the test's last PASS that kept its pages (`204` if none) |
| GET | `/live` | Live: executions running or finished in the last 2 h, with counts, progress and ETA |
| GET | `/live/execution?key=` | One execution with its runs and tests (state `running`, `turtle`, `no_signal` or the result, duration, p50/p90, last step) |
| GET | `/runs/{run_id}/control` | `{cancel, by, reason}`: asked by clients with remote control between tests |
| GET | `/tests/{test_id}/drift` | p95 per endpoint of the test vs. the median of its previous runs |
| GET | `/stream?run={id}` | Live events (Server-Sent Events): `run`, `test`, `log`, `network`, `triage`, `summary` |
| GET | `/network/{id}/body` | Full stored body (JSON or plain text, never executable HTML) |
| GET | `/network/{id}/baseline` | The same call in the last run where the test passed (`{conn, run_id, test_id, same_context}`), or `204` |
| GET | `/screenshots/{file}` | Screenshot (outside `/api/v1`) |

### Search runs

`GET /runs/search` accepts `q` (at most 200 characters), `project`, `environment`, `branch`,
`tag`, `status` (repeat it or use comma-separated values), `incomplete`, `flaky`, `from`,
`to` (`YYYY-MM-DD`), `sort` (`recent`, `oldest`, `duration`, `failures`), `limit` (1–100), and
`cursor`. `q` searches the run name, project, branch, commit and environment, the test name, key
and error message, and the method and URL of the failed network calls (status 400 or more, or no
answer), without returning tests. `%` and `_` are literal characters. Every item includes counters, duration
and `flaky_count`; the opaque cursor keeps a page stable when new runs arrive. `GET /runs/facets`
accepts the same filters and returns at most 20 values for each group.

## Settings (Settings screen)

| Method | Path | Body | Response |
|---|---|---|---|
| PUT | `/settings` | `{language?, ai_language?, ai?: {provider, model, base_url, api_key?}}` | Updated settings |
| POST | `/settings/ai/test` | `{provider, model, base_url, api_key?}` | `{ok, ms}` or `{ok: false, error}` (saves nothing) |
| DELETE | `/settings/ai` | — | Goes back to the environment's AI configuration |
| GET | `/settings/ai/status` | — | General state of the configured AI provider from its public status page (OpenAI, Anthropic; Ollama: whether it answers; Gemini and compatible APIs publish none that can be read): `{provider, source, indicator, description, components, incidents, page_url, error, checked_at}`. Cached for a minute; `?refresh=1` checks again. Only a GET to that page |
| GET | `/settings/ai/usage` | — | AI provider usage (Settings → AI usage): `{usage: {today, last_7, last_30, by_model, by_kind, daily, skipped_by_budget}, max_per_run}`. Every total has `calls`, `errors`, `errors_by` (by reason: `rate_limit`, `server`, `auth`, `timeout`, `network`, `other`), `input_tokens`, `output_tokens` (as reported by the provider), `untracked` (calls without token data), `duration_ms`, `timed` (calls with a measured time) and `avg_ms` (average of the measured ones). Dates in server time; 90 days are kept. No costs |

- `provider`: `gemini`, `anthropic`, `openai`, `openai_compatible`, `ollama` or `off`.
  An empty or missing `api_key` keeps that provider's current key (the saved one or the
  environment's).
- `language`: `es` or `en` (the UI's default language). `ai_language`: `auto`, `es` or `en`.
- They require `Content-Type: application/json`. They are accepted with the token
  (`TRACEREPORTS_TOKEN`) or with the UI login. Without login nor token configured (local mode) also
  from the server's own machine, connected directly (not through a proxy). With `TRACEREPORTS_TOKEN`
  the same machine needs credentials too, unless `TRACEREPORTS_LOCAL_ADMIN=1`. `TRACEREPORTS_SETTINGS_LOCKED=1` always rejects them (`403`).

### Security check (experimental)

`GET /security/posture` returns the checks of `tracereports doctor --security` on the configuration the
server runs with: `{version, checks: [{id, level, title, detail, fix}], ok, warn, fail}` (`level`: `ok`,
`warn` or `fail`). Needs `TRACEREPORTS_TOKEN`, the UI login or the same machine in local mode (the ingest
token gets 403). It never includes credential values.

## Actions from the interface (AI and escalation)

They require `Content-Type: application/json` and the same rule as Settings: token, UI login or,
in local mode, the server's own machine.

| Method | Path | Body | Response |
|---|---|---|---|
| POST | `/ui/ai/probe` | `{}` (`?force=1` skips the 20 s cache) | Whether the AI configured right now answers (Trace Buddy): `{ok, state, provider, model, ms}`, `state` = `llm`, `local` (rules engine, nothing to call) or `off`; on error `{ok: false, error, hint, suggest}`. Uses the active configuration, so it also works with Settings locked; one tiny fixed request, logged in AI usage |
| POST | `/ui/ai/chat` | `{run_id, test_id, lang, messages: [{role, text}]}` | A free question to Trace Buddy: `{answer, provider, model}`. Needs an LLM (`409` with `code: needs_llm` on the local engine). The model gets the evidence of the run or test (`run_id`/`test_id`, both optional), the last 8 turns of up to 2000 characters, all through the redaction. Up to 60 questions per hour per server (`429`); logged in AI usage as `chat` |
| POST | `/ui/tests/{test_id}/analyze` | — | `202 {queued}`: diagnoses a failed test again |
| POST | `/ui/runs/{run_id}/analyze` | `{all?}` | `202 {queued}`: diagnoses the failures without a diagnosis (or all with `all`) and the run summary |
| POST | `/ui/escalate` | `{run_id, test_id, audience, lang, regenerate?, no_ai?}` | Summary for `business`, `qa` or `dev` (`test_id` 0 = the whole run). A run without failures carries `facts.assessment`: the QA verdict (`clean`, `observations` or `risks`, computed by rules, not by the AI) and its `signals` (`level` `risk`/`note`, `kind`, `test`, `text`); with risks the severity is `medium`. When it finished, the AI writes a summary of what was validated (`kind: success`) but not the verdict, the severity nor the evidence; if its text describes a failure it is discarded (`ai_skipped: invented_failure`). A run still running, incomplete or all skipped is built without AI (`ai_skipped: no_failures`). Cached when written by AI, with `timing`: `{total_ms, provider_ms, wait_ms, own_ms, calls, attempts}` (time waiting for the provider, pauses between retries and TraceReports' own time; `attempts`: every call with `ms`, `ok`, `status`, `reason` and the following pause `wait_ms`/`wait_asked`). On a 429/5xx it waits what the provider asks (`Retry-After`, `retry-after-ms` or Gemini's `RetryInfo`), up to 90 s |
| POST | `/ui/runs/{run_id}/close` | `{reason}` | Closes a `RUNNING` run by hand: its unfinished tests become `INTERRUPTED` with the reason (redacted, up to 500 characters) and the run records `closed_by` and `close_reason`. `409` if it is already closed. Then the run diagnosis and the notification follow, as for any close |
| POST | `/ui/runs/close` | `{run_ids, reason}` | The same for up to 200 runs: `{closed, skipped}` (already closed or unknown ones are skipped) |
| POST | `/ui/runs/delete` | `{run_ids}` | Deletes up to 200 **closed** runs with all their evidence (screenshots and artifacts included; the database is compacted): `{deleted, skipped}`. A running run is skipped |
| PUT | `/ui/maintenance/stale` | `{hours}` | The inactivity threshold of the automatic close (`0` = never, up to 720), applied without a restart. Needs the Settings permission; `409` when `TRACEREPORTS_STALE_RUN_HOURS` sets it |
| POST | `/ui/escalate/send` | `{run_id, test_id, audience, lang, channel}` | Posts the summary to `teams` or `slack`. With `PUBLIC_URL` it includes the link and the screenshot |
| POST | `/ui/tickets` | `{run_id, test_id, provider, audience, lang, force}` | Creates a ticket in `github`, `jira` or `azure` with the summary (default for `dev`). `201 {ticket}`; if the same failure already has one, `200 {ticket, existing: true}` (unless `force`). If it is unknown whether the tracker created it, `502 {uncertain: true}` and then `409` until `force` is sent. See [Configuration](configuration.md#tickets-in-github-jira-or-azure-devops) |
| POST | `/ui/summary/weekly` | `{send, lang}` | Summary of the last 7 days: `{summary, sent}`. With `send: true` it posts it to Teams/Slack |
| POST | `/ui/quarantine` | `{test_id, reason, owner, days}` | Quarantines the test (in its project) for `days` days (default 14, max 180). `reason` is required. Answers the quarantine and the recomputed run |
| DELETE | `/ui/quarantine/{test_id}` | `{}` | Lifts the test's quarantine |
| GET | `/quarantine?project=…` | — | Quarantines of the project (`all=1`: every project), with `active` |
| POST | `/ui/tests/{test_id}/verdict` | `{verdict, comment, author}` | Classifies the failure: `product_bug`, `test_bug`, `environment`, `data`, `flaky` or `other`. Tests in `GET /runs/{id}` and `/tests/{id}` carry `verdict`, `previous_verdict` (from an earlier run) and `owner` |
| GET | `/runs/{run_id}/tickets` | — | Tickets of the run's failures (also those opened from earlier runs of the same test) |
| GET | `/runs/{run_id}/release` | — | Can we ship? `{decision: go|risk|no_go, pass_rate, gate, checks, features}` (`TRACEREPORTS_RELEASE_GATE`) |

Related reads: `GET /runs/{run_id}/recurrence` (in how many previous runs of the same suite each
incident showed up) and `GET /runs/{run_id}/escalation?test=&audience=&lang=` (the saved summary,
if the evidence did not change since it was written, or `204`). `GET /metrics` also accepts `env`, `tag`, `from` and `to` (`YYYY-MM-DD`), and
`GET /tests/{test_id}/history` accepts `limit` (up to 60) and `newer` (also up to that many later runs,
first in the list). `GET /tests/{test_id}/locator` also says `has_dom` and `dom_elements` (whether the
page snapshot arrived, so no suggestions with a snapshot means none fit) and `failed_selector_truncated`
(the selector reached the server cut: no replacements are searched).

FixLab (the Lab, checks, Verify and capsules: `/network/{id}/lab`, `/ui/lab/send`, `/ui/lab/checks`,
`/ui/lab/verify`, `/runs/{run_id}/lab`, `/network/{id}/capsule`, `/runs/{run_id}/lab/capsule`): see
[FixLab · API](fixlab.md#api).
