# Configuration

🌐 **English** · [Español](../es/configuration.md)

Everything is configured with environment variables, and all of them are optional. The easiest
way is a `.env` file (see [`.env.example`](../../.env.example)): both Docker Compose and the
binary (`./tracereports` or `go run ./cmd`, from the folder you run it in) read it. Variables
already set in the system take precedence over the file. `TRACEREPORTS_ENV_FILE` changes the file path.

When the Settings screen is read-only, it shows a complete `.env` example (with test data) and the
steps to apply it.

## Reference

| Variable | Default | What it does |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DATA_DIR` | `./data` | SQLite database and screenshots |
| `TRACEREPORTS_TOKEN` | — | Token required to **write** to the API (the test clients) |
| `TRACEREPORTS_SECRET_KEY` | — | Master key (32 bytes, base64 or hex) to store **encrypted** the AI API key typed in Settings. Without it, the key can only be given through the environment. See [Encrypted credentials](#credentials-saved-from-settings-encrypted) |
| `TRACEREPORTS_SECRET_KEY_PREVIOUS` | — | The previous master key, only while you change it (rotation) |
| `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` | — | Login (HTTP Basic) to **view** the reports |
| `TRACEREPORTS_ALLOWED_HOSTS` | — | Without UI login: names served besides `localhost` (comma separated; `*` = any). See [Security](#security) |
| `TRACEREPORTS_LOCAL_ADMIN` | — | `1`: with a token and no login, the same machine (no proxy) may change Settings and use the UI actions |
| `AI_PROVIDER` | inferred from the key | `gemini`, `anthropic`, `openai`, `openai_compatible` or `ollama` |
| `AI_MODEL` | the provider's default | Model (see the provider table) |
| `AI_API_KEY` | — | The provider's API key (Ollama does not use one) |
| `AI_BASE_URL` | the provider's API | API URL: proxy, your own endpoint, a compatible service or a remote Ollama |
| `GEMINI_API_KEY` / `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `OLLAMA_HOST` | — | Alternative to the `AI_*` variables: with just one of these the provider is picked automatically |
| `TRACEREPORTS_ENV_FILE` | `.env` | `KEY=value` file the server reads on startup |
| `TRACEREPORTS_SETTINGS_LOCKED` | — | `1`: the Settings screen becomes read-only; AI is configured only from the environment |
| `TEAMS_WEBHOOK_URL` | — | Sends each run's summary to Microsoft Teams |
| `SLACK_WEBHOOK_URL` | — | Sends each run's summary to Slack |
| `PUBLIC_URL` | — | Public URL of the server, for the *View report* button in notifications |
| `NOTIFY_ON` | `always` | `always`, or `failures` to notify only when something failed |
| `NETWORK_MAX_BODY_KB` | `256` | Maximum stored size of each network *response body*; `0` = store no bodies |
| `TRACEREPORTS_REDACT` | on | `off` stores secrets as received (not recommended) |
| `TRACEREPORTS_REDACT_HEADERS` / `TRACEREPORTS_REDACT_KEYS` | — | More headers and keys (JSON, query, form) to mask, comma separated |
| `TRACEREPORTS_REDACT_PATTERNS` | — | Extra regular expressions, separated by `;` (e.g. a national id: `\b\d{7,8}-[\dkK]\b`) |
| `TRACEREPORTS_RETENTION_DAYS` | — | Deletes the runs (and their screenshots) older than N days |
| `TRACEREPORTS_AI_MAX_PER_RUN` | `50` | Automatic AI diagnoses per run; the rest are left *not analyzed* and can be analyzed by hand. `0` = no limit |
| `TRACEREPORTS_STALE_RUN_HOURS` | `24` | Closes as *incomplete* a run still *in progress* that received nothing for N hours (the client died without closing it). `0` = never. See [Abandoned runs](#abandoned-runs) |

## Security

With no configuration the server is **open**: anyone who reaches the port can read and write
reports. That is fine on your machine, not on a shared server.

```bash
TRACEREPORTS_TOKEN=a-long-random-token      # generate it with: python -c "import secrets; print(secrets.token_hex(24))"
TRACEREPORTS_UI_USER=qa
TRACEREPORTS_UI_PASSWORD=a-strong-password
```

- **`TRACEREPORTS_TOKEN`**: writes (`POST`/`PATCH`) require `Authorization: Bearer <token>` or the
  `X-TraceReports-Token` header. The clients read it from the `TRACEREPORTS_TOKEN` variable or, when it
  is not set, from the project's `.env` (see below). The token also grants read access, for example
  to download the ZIP from CI.
- **Settings → Connect your tests** walks you through the token: it generates it in the browser,
  gives you the `.env` line, the commands for PowerShell, macOS/Linux, GitHub Actions and GitLab CI
  with your URL, and checks that the server accepts it.
- **`TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD`**: the browser asks for a user and password to see
  the UI and the read API.
- **Settings from the UI** (AI provider, default language): can be changed with the UI login or
  the token. Without login nor token (local mode), also from the machine where the server runs,
  connected directly: the real connection is checked and a request that comes through a proxy
  (`X-Forwarded-For`, `X-Real-IP`, `Forwarded`) never counts as local. With `TRACEREPORTS_TOKEN` the
  server counts as deployed and the same machine needs the login too, unless you set
  `TRACEREPORTS_LOCAL_ADMIN=1`. `TRACEREPORTS_SETTINGS_LOCKED=1` locks them completely.
  **With Docker** the login is needed even if you open `localhost`: the connection comes from
  Docker's network, not from the same machine.
- **Allowed Host (DNS rebinding)**: without a UI login, the server only serves requests without
  credentials that arrive with a local `Host` (`localhost`, `127.0.0.1`, `::1`), the one of
  `PUBLIC_URL` or one in `TRACEREPORTS_ALLOWED_HOSTS`. That way a malicious page open in your
  browser cannot read your reports by pointing its domain at your machine. Requests with a valid
  token, and all of them when there is a UI login, do not depend on the `Host`. If you serve without
  login under another name (a LAN IP, a proxy with a domain, a CI service alias), add it:
  `TRACEREPORTS_ALLOWED_HOSTS=reports.lan,192.168.1.10`. The `403 host … is not allowed` response
  says what is missing.
- Put the server behind HTTPS (a reverse proxy such as Caddy, nginx or Traefik). HTTP Basic
  without TLS travels in plain text.

### The project's `.env` in the clients

The Python, JavaScript and Java clients read `TRACEREPORTS_*` from the project's
`.env` file when the variable is not in the environment, with the same rules as the server. That
way the token lives in one place, ignored by git, and your tests use it without setting it in every
terminal or globally on the system.

- It is looked up from the folder you run the tests from upwards, never past the repository root
  (the folder with `.git`).
- The environment variable always wins: in CI, pass the token as a *secret* and do not commit the
  `.env`.
- Only `TRACEREPORTS_*` keys are used from the file: your AI keys stay there.
- `TRACEREPORTS_ENV_FILE=path/to/file` uses another file; `TRACEREPORTS_ENV_FILE=off` reads none.

Network captures are masked on the client before being sent: `Authorization`, `Cookie` and
`Set-Cookie` headers, tokens, passwords and national IDs (RUT). Even so, **do not report real
personal data** from production environments.

## AI diagnosis

Works with any of these providers:

| Provider | `AI_PROVIDER` | Default model | Notes |
|---|---|---|---|
| Google Gemini | `gemini` | `gemini-flash-lite-latest` | Free tier. Key at <https://aistudio.google.com/apikey> |
| Anthropic Claude | `anthropic` | `claude-opus-5-5` | Most accurate diagnoses, pay as you go. Key at <https://platform.claude.com/settings/keys> |
| OpenAI | `openai` | `gpt-5-mini` | Pay as you go |
| OpenAI-compatible | `openai_compatible` | — (required) | Groq, OpenRouter, DeepSeek, Mistral, LM Studio, vLLM… Needs `AI_BASE_URL` ending in `/v1` |
| Ollama | `ollama` | `llama3.1` | Local: test data **never leaves your network**. No key |

```bash
# Gemini (simplest: the key is enough)
GEMINI_API_KEY=...

# Claude
AI_PROVIDER=anthropic
AI_API_KEY=sk-ant-...
# AI_MODEL=claude-sonnet-5-5         # cheaper

# Ollama on your machine (first: ollama pull llama3.1)
AI_PROVIDER=ollama
# AI_BASE_URL=http://host.docker.internal:11434   # if TraceReports runs in Docker

# Groq, OpenRouter, LM Studio…
AI_PROVIDER=openai_compatible
AI_BASE_URL=https://api.groq.com/openai/v1
AI_MODEL=llama-3.3-70b-versatile
AI_API_KEY=...
```

With Claude, TraceReports enables Anthropic's server-side *fallback*: if the model declines to
analyze a failure for policy reasons, a fallback model answers within the same call.

### In the `.env` or on the Settings screen?

Both work, and they can be combined:

- **`.env`**: the base configuration. The best option for servers, Docker and CI: it is
  versioned, reviewed in your infrastructure repository and does not depend on anyone opening the
  UI.
- **Settings → Artificial intelligence**: switch provider without restarting the server, with
  *Test connection* before saving. What you save there **overrides the `.env`**; *Go back to the
  .env configuration* undoes it. The API key is stored **encrypted** in the server database
  (`DATA_DIR`), which requires `TRACEREPORTS_SECRET_KEY` (see below), and is never sent to the
  browser (the UI only shows its last 4 characters).

In production: configure with the `.env` and set `TRACEREPORTS_SETTINGS_LOCKED=1` so nobody can change
it from the UI. To try providers on your machine, the screen is faster.

The **diagnosis language** (automatic, Spanish or English) is also chosen in Settings.

What it does:

- **Per test** (when it ends in `FAIL`): category (`LOCATOR_CHANGED`, `BACKEND_TIMEOUT`,
  `LOGIC_BUG`, `INFRA_ERROR`), summary and suggestion. It uses the error message, the stack trace,
  the last step and the **backend calls that failed**.
- **Per run** (when it is closed): groups the failures into incidents by likely cause and writes a
  headline and a summary for the team.

Without an AI provider there is no AI text, but failures **are still grouped** by cause: the
backend call that failed, the category or the error message.

The analysis runs in the background and shows up in the UI as soon as it finishes. Requests are
processed a few at a time and retried automatically when a rate limit is hit.

### Credentials saved from Settings (encrypted)

The AI API key typed in **Settings** is stored in SQLite encrypted with AES-256-GCM. The master key
is **not** in the database: it comes from `TRACEREPORTS_SECRET_KEY`. Keys given through environment
variables (`AI_API_KEY`, `OPENAI_API_KEY`…) do not change and need no master key.

**Enable**

```bash
openssl rand -base64 32        # or: python -c "import secrets,base64; print(base64.b64encode(secrets.token_bytes(32)).decode())"
TRACEREPORTS_SECRET_KEY=<what it printed>
```

Keep it in your infrastructure's secret manager (not in the repository nor next to `DATA_DIR`).
Without it, saving a key from Settings answers an error that says what is missing: the key is
**never** stored unencrypted.

**Installations with a key saved by an earlier version (unencrypted)**: it keeps working, and
Settings and the log warn that it is in clear. To encrypt it, with the server stopped or running:

```bash
# 1. back up DATA_DIR (it contains the key in clear: keep it protected and delete it afterwards)
# 2. check the state (it never prints the key)
TRACEREPORTS_SECRET_KEY=... tracereports secrets status
# 3. dry run, then apply
TRACEREPORTS_SECRET_KEY=... tracereports secrets migrate -dry-run
TRACEREPORTS_SECRET_KEY=... tracereports secrets migrate
```

With Docker: `docker compose exec tracereports /tracereports secrets migrate` (the variable is
already in the container). The server never migrates by itself.

**If the master key is missing or changed**: the server starts anyway, the encrypted key is **not
deleted**, Settings shows why (*TRACEREPORTS_SECRET_KEY is missing* / *encrypted with another key*)
and the AI uses the environment key if there is one. Changing only the model answers an error
instead of silently using another key. Fix it by setting the right key or typing the key again.

**Rotate the key**: set the new one in `TRACEREPORTS_SECRET_KEY` and the previous one in
`TRACEREPORTS_SECRET_KEY_PREVIOUS`, run `tracereports secrets migrate`, then remove the previous one.

**Backup and restore**: a backup of `DATA_DIR` is only useful together with the master key it was
encrypted with: keep both, in different places. When restoring on another server set the same
`TRACEREPORTS_SECRET_KEY`. If the key is lost, the saved key cannot be recovered: type it again in
Settings (the rest of the database does not depend on it).

## Interface language

Spanish or English, under **Settings → Appearance**. Each browser remembers its own; whoever can
edit the settings can set the team's default language. With no choice, the browser's language is
used.

## Teams and Slack notifications

When each run is closed (`PATCH /runs/{id}/finish`), the server sends:

- status and statistics;
- the diagnosis headline and the main causes with their suggested action;
- the comparison with the previous run (new failures, fixed, flaky);
- a **View report** button if `PUBLIC_URL` is set.

**Microsoft Teams** (Workflows webhook):
1. In the channel: **⋯ → Workflows → "Post to a channel when a webhook request is received"**.
2. Pick the team and channel, and copy the generated URL.
3. Set it in `TEAMS_WEBHOOK_URL`.

**Slack**:
1. Create an app at <https://api.slack.com/apps> → **Incoming Webhooks** → turn them on.
2. **Add New Webhook to Workspace**, pick the channel and copy the URL.
3. Set it in `SLACK_WEBHOOK_URL`.

**Weekly summary**: with `TRACEREPORTS_WEEKLY_SUMMARY=mon 09:00` (day `sun`…`sat` and server
time) the configured channels get, every week, a summary of the last 7 days: whether the pass rate
went up or down against the previous week, runs, failed tests, flaky tests, what fails the most,
what already got fixed and the flaky tests to watch, with the link to **Metrics** (with
`PUBLIC_URL`). **Metrics → Weekly summary** shows the preview and can send it right away.
Language: the one in **Settings**.

**Retries**: each send is stored (one per channel) before the first attempt. If the channel does
not answer, answers `429` or a `5xx`, it is retried up to 8 times with a growing wait (30 s, 1 min,
2 min… up to 1 h, with random variation) honoring `Retry-After`; pending ones are resumed after a
server restart. A `4xx` (revoked webhook, rejected message) or a channel that is no longer configured
fails at once without retrying. A channel that already got it does not get it again because the
other one failed, and the same summary of the same run, or the weekly one of the same slot, is not
sent twice. Guarantee: *at least once* (if the server dies right after the channel got it, the retry
repeats it). The webhook URL is not stored in the database.

## Tickets in GitHub, Jira or Azure DevOps

From **Escalate**, the **Ticket** button opens an issue with the summary you are looking at (what
happened, likely cause, evidence and next steps), the error, the backend calls that failed and the
report link (with `PUBLIC_URL`). In Jira and Azure DevOps the failure screenshot is attached; in
GitHub, it is behind the link.

**No duplicates:** if the same test already has a ticket in that tracker (even from another run,
say last night's), it shows that one and offers *Create another one*. Tokens stay on the server:
the interface only learns which trackers exist.

| Variable | What it is |
| --- | --- |
| `TRACEREPORTS_GITHUB_REPO` / `TRACEREPORTS_GITHUB_TOKEN` | `owner/repo` and a token allowed to write issues |
| `TRACEREPORTS_GITHUB_API` | GitHub Enterprise API (default `https://api.github.com`) |
| `TRACEREPORTS_GITHUB_LABELS` | Comma-separated labels (default `bug`) |
| `TRACEREPORTS_JIRA_URL` / `TRACEREPORTS_JIRA_PROJECT` | `https://yourcompany.atlassian.net` and the project key (`SHOP`) |
| `TRACEREPORTS_JIRA_TOKEN` + `TRACEREPORTS_JIRA_EMAIL` | Jira Cloud: API token and its owner's email. Jira Server/Data Center: just the personal token (no email) |
| `TRACEREPORTS_JIRA_ISSUE_TYPE` / `TRACEREPORTS_JIRA_LABELS` | Type (default `Bug`) and labels (default `tracereports`) |
| `TRACEREPORTS_AZURE_URL` / `TRACEREPORTS_AZURE_PROJECT` | `https://dev.azure.com/yourorg` and the project |
| `TRACEREPORTS_AZURE_TOKEN` | Personal access token with *Work Items: Read & write* |
| `TRACEREPORTS_AZURE_TYPE` / `TRACEREPORTS_AZURE_TAGS` | Work item type (default `Bug`: the summary goes in *Repro Steps*) and tags |

A tracker shows up in the interface only when all its required variables are set. Through the API:
`POST /api/v1/ui/tickets` with `{run_id, test_id, provider, audience, lang, force}` and
`GET /api/v1/runs/{id}/tickets` (see the [API](api.md)).

A ticket is reused only for the same test **of the same project** and the same tracker destination
(GitHub repository, Jira or Azure DevOps project): two projects with a test of the same key do not
share tickets, and if the tracker starts pointing to another repository a new one is created.
Tickets created by earlier versions get their project back from their run; if that run was already
deleted, the ticket is kept and still shown in it, but it is not offered for other runs (the prudent
choice: better a new ticket than one of another project).

## Correlation with the backend logs

When a call carries a trace or request id in its headers, the call detail (**Network** tab) shows it
with **Open logs** and **Open trace** buttons, and tickets include it. Recognized: W3C
`traceparent`/`traceresponse`, B3 (Zipkin), Jaeger `uber-trace-id`, AWS X-Ray, Datadog, Google
Cloud and `X-Request-Id`, `X-Correlation-Id`, `Request-Id`, `cf-ray` and the like (response headers
first: they are the ones the backend used).

The links come from two templates:

| Variable | Example |
| --- | --- |
| `TRACEREPORTS_TRACE_URL` | Jaeger: `https://jaeger.acme.com/trace/{trace_id}` · Datadog APM: `https://app.datadoghq.com/apm/trace/{trace_id}` |
| `TRACEREPORTS_LOGS_URL` | Datadog: `https://app.datadoghq.com/logs?query=%40http.request_id%3A{request_id}&from_ts={from}&to_ts={to}` · Kibana: `https://kibana.acme.com/app/discover#/?_g=(time:(from:'{from_iso}',to:'{to_iso}'))&_a=(query:(language:kuery,query:'request.id:"{request_id}"'))` |

Available values (already URL-encoded): `{trace_id}`, `{request_id}`, `{from}` and `{to}` (epoch
ms: 2 minutes before and after the call), `{from_s}` and `{to_s}` (seconds), `{from_iso}` and
`{to_iso}`, `{host}`, `{path}`, `{method}` and `{status}`. For Grafana (Loki, Tempo) open Explore
with a sample search, copy the URL and replace the value with `{trace_id}`.

A template that needs an id the call does not have produces no link: it never opens an empty
search. Ids and links are kept in the exported report too.

## Quarantine of flaky tests

A known flaky test can be **quarantined** from its detail (*Quarantine* button, on tests that fail
or are marked flaky): a required reason, an owner and an expiry (7, 14, 30 or 90 days). While it
lasts:

- its failures **are still shown and counted**, but they do not turn the run red: if only
  quarantined tests fail, the run is yellow (`WARNING`), with the `quarantined` counter;
- it applies to the next runs of the same **project** (by the test identity) and to the run you are
  looking at, recomputed right away;
- when it **expires** it stops applying by itself: a test cannot stay hidden forever. The chip
  becomes *Quarantine expired*.

The PR comment tells quarantined failures apart too. Through the API: `POST /api/v1/ui/quarantine`,
`DELETE /api/v1/ui/quarantine/{test_id}` and `GET /api/v1/quarantine?project=…` (see the [API](api.md)).

## Test owners

Like a CODEOWNERS file: each test gets an owner (team or person) from rules; it shows on the test
and reaches **Escalate** and tickets as the *owner*. One rule per line, `<pattern> <owner>`, and
**the last one that matches wins**:

```text
# TRACEREPORTS_OWNERS_FILE=/config/OWNERS
*                          @qa-team
tests/checkout/*           Payments team
*::test_login*             @auth
tag:smoke                  @qa-smoke
suite:"Billing / *"        Billing
```

- A plain pattern is matched against the test identity (pytest nodeid, `file > title` in
  Playwright, `package.Class#method` in JUnit…); `tag:` against each tag; `suite:` against the suite.
- `*` is any text (including `/`); it is case-insensitive and must match the whole value. A
  pattern with spaces goes in double quotes; the owner is the rest of the line.
- `TRACEREPORTS_OWNERS_FILE` (a file) and/or `TRACEREPORTS_OWNERS` (inline rules separated by `;`).
  A missing file or a malformed rule stops the server from starting: better than wrong owners.

## Collaborative failure triage

On a failed test, **Classify this failure** saves a verdict (*product bug*, *broken test*,
*environment*, *test data*, *flaky* or *other*), a comment and who classified it (remembered in the
browser). The next time that test fails in the same project, its detail shows *In run #N it was
classified as…* with **Same verdict** to apply it in one click: nobody investigates the same
failure twice. Through the API: `POST /api/v1/ui/tests/{id}/verdict` (see the [API](api.md)).

## Playwright trace and video

The test detail shows its **video** (it plays right there) and its Playwright **trace**: download
it, open it with `npx playwright show-trace <file>` or, when the report is published over HTTPS
(`PUBLIC_URL`), with **Open in Trace Viewer** (the official viewer downloads the trace from your
browser; the server lets it read `.zip` files only).

- **Playwright Test (JS):** with `use: { trace: "retain-on-failure", video: "retain-on-failure" }`
  the reporter uploads them by itself.
- **Python:** `tracereports.attach_artifact(path, "trace")` or `"video"` (for example in a fixture
  after the test, with what pytest-playwright left in `test-results/`).
- **Java:** `test.artifact("trace", Path.of("trace.zip"))`. **Go:** `t.Artifact("trace", data, "trace.zip")`.

Only traces (ZIP) and videos (WebM or MP4) are accepted, validated by their content, up to 100 MB
each. Retention deletes them with their run, and the exported ZIP includes up to 100 MB of them.

## For whoever fixes it: reproduce and compare

- **Run it locally**: a test's detail has the command to run it on your machine, at the run's exact
  version (`git checkout <commit> && …`): `pytest '<nodeid>'`,
  `npx playwright test <file> -g '<title>' --project=<project>`, Maven/Gradle for JUnit or
  `go test ./<package>/... -run '^TestX$'`. It comes from the test identity, so the client must
  report it (all of them do).
- **Compare with the last time it passed**: on a failed call (**Network** tab), it finds the same
  call (same method, host and path, with ids normalized: `/orders/123` = `/orders/456`) in the most
  recent run where the test passed, in the same project, environment and branch (or, if none, the
  project), and shows what changed: status, duration, response headers (without the ones that always
  change, like `date` or `etag`) and the response and request bodies, field by field when they are
  JSON. Through the API: `GET /api/v1/network/{id}/baseline`.
- **Browser console**: a test's **Console** tab shows the `console.error`, `console.warn` and the
  JavaScript errors the page did not handle ("the button never appeared" is often a `TypeError` a
  second earlier). Errors also go to the AI diagnosis. Captured by itself with pytest-playwright,
  the `tracereports/playwright` fixtures and `PlaywrightEvidence.attach` (Java); in Go,
  `t.Console(...)`. At most 500 entries per test, with the text masked. Through the API:
  `POST /api/v1/tests/{id}/console` with `{entries: [{level, text, location, timestamp}]}`.

## Release: can we ship to production?

The **Release** view answers that question for the run you are looking at, on one screen anyone
understands: **Ready to ship**, **Can ship, with risks** or **Do not ship yet**, the criteria behind
the decision and the state of each functional area (the tests' tags).

| Criterion | Default | When broken |
| --- | --- | --- |
| The run finished complete | — | blocks |
| No test with a critical tag failed | no critical tags | blocks |
| Minimum pass rate (skipped and quarantined failures do not count) | 95 % | blocks |
| New failures against the previous run | 0 | risk |
| Quarantined failures | — | risk |
| Flaky tests | 3 | risk |

Set it with `TRACEREPORTS_RELEASE_GATE`, for example
`min_pass_rate=98; critical=smoke,checkout,login; max_new_failures=0; max_flaky=2`. A malformed
criterion stops the server from starting. Through the API: `GET /api/v1/runs/{id}/release`.

## History, flaky and comparison

No configuration needed. They are computed from the data that already exists:

- **Identity**: a test is identified by its **key** (the `nodeid` in pytest, `package/Test` in
  Go), not by its visible name. A test reported without a key (REST API without the field, data
  from before this version) is identified by its name and the UI marks its history *approximate*.
- **Context**: only runs of the same **project, environment and branch** are compared. Staging
  never mixes with production, nor a feature branch with main.
- **History**: the last execution of that identity in each run of the context.
- **Stability** (last 20 runs of the context; `WARNING` counts as not failed and `SKIP` is ignored):
  - **flaky**: flips between passing and failing at least twice in the last 10, or passed only
    after a retry. With fewer than 5 runs it is shown as *possibly* flaky (little data);
  - **persistent failure**: fails its last 3 or more runs in a row. A regression, not flakiness;
  - the percentage shown is the **failure rate**, with its sample (*failed 7 of 20*).
- **Comparison**: against the previous run of the same context that shares tests. When the branch
  has none, against the latest of the same project and environment on another branch (the UI says
  so). You can pick another base in the dashboard.

## Failure grouping (incidents)

Each failure goes to an incident by evidence, never by the AI category alone:

1. **The backend call that explains it**: the one that failed right before the failure (up to 2
   minutes before). A server-side error wins (no response, 5xx, 408, 429); a 4xx only counts when
   the application did not keep working with that host afterwards. **Expected** responses never
   count. The signature includes method, **host**, normalized path and outcome.
2. Otherwise, the **error signature**: exception type, message without numbers or ids and the
   innermost frame of the project's code.

Every failure stays in some incident. The AI describes at most the 8 largest (the rest are shown
with their evidence) and its cause is presented as a **hypothesis**, next to the evidence it relies
on; when the evidence is not enough, it says so instead of making a cause up.

## Sensitive data and retention

The server masks before storing: credential headers (`Authorization`, `Cookie`, `Set-Cookie`,
`X-API-Key`…), sensitive keys in JSON, query strings and forms (`password`, `token`, `secret`,
`api_key`, `session`… and keys ending like that), `Bearer` tokens, JWTs and credentials inside URLs.
It applies to the network, steps, errors, descriptions, the DOM snapshot and to names and
identities (test name, key, suite and category; run name, project, environment and branch),
whatever client sent them. A key carrying a secret is stored masked plus a hash keyed per
installation, so two tests that only differ in the secret do not merge their history. Since what is stored is already masked, it never reaches the AI, the ZIP or Teams/Slack (and
prompts and the ZIP go through the same filter too).

Limits: it recognizes secrets by their key or by unambiguous shapes. Sensitive data written as free
text without a key, or visible in a **screenshot**, is not detected: use `TRACEREPORTS_REDACT_PATTERNS`,
`--tracereports-no-screenshots`, `--tracereports-no-dom` or `NETWORK_MAX_BODY_KB=0` as needed, and
`TRACEREPORTS_RETENTION_DAYS` so evidence does not pile up.

### Abandoned runs

If the test process dies without closing the run (cancelled CI job, lost machine), it stays *in
progress*. After `TRACEREPORTS_STALE_RUN_HOURS` hours (24 by default) **without receiving anything**,
the server closes it as *incomplete*: the tests still running become *interrupted* failures, and the
diagnosis and the notification follow, once. Activity is the server time when the last write
arrived, not the times the client sends: a long run that keeps sending is never closed, and an
offline client uploading its recording late does not look abandoned. Whatever arrives later (a
step, the `finish`) is accepted as late evidence, without notifying again. Once closed, retention
treats it like any other.

## AI diagnosis after a restart

Diagnoses left halfway (*pending*) are resumed when the server starts. The same test is never
analyzed twice at the same time nor paid again if it already has a diagnosis (except with
*Re-analyze*). When the run summary was written while diagnoses were still pending, it says so and
is rewritten when they finish.
