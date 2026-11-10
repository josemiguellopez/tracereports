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
| `TRACEREPORTS_TOKEN` | — | Token required to **write** to the API (the test clients). It also administers: changes Settings and uses the UI actions |
| `TRACEREPORTS_INGEST_TOKEN` | — | **Restricted** token for clients and CI: sends results and reads, never changes Settings nor uses UI actions. See [Permissions](#permissions-of-each-credential) |
| `TRACEREPORTS_SECRET_KEY` | — | Master key (32 bytes, base64 or hex) to store **encrypted** the AI API key typed in Settings. Without it, the key can only be given through the environment. See [Encrypted credentials](#credentials-saved-from-settings-encrypted) |
| `TRACEREPORTS_SECRET_KEY_PREVIOUS` | — | The previous master key, only while you change it (rotation) |
| `TRACEREPORTS_ENCRYPT_AT_REST` | — | `1` = encrypts new screenshots, traces, videos and DOM snapshots (AES-256-GCM). Needs `TRACEREPORTS_DB_KEY`. See [Evidence encryption](security-hardening.md#application-level-evidence-encryption-optional) |
| `TRACEREPORTS_DB_KEY` / `TRACEREPORTS_DB_KEY_FILE` | — | Its key (32 bytes, base64 or hex), or a file holding it; never inside `DATA_DIR` |
| `TRACEREPORTS_DB_KEY_PREVIOUS` / `_FILE` | — | The previous key, only while rotating it (`tracereports evidence rekey`) |
| `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` | — | Login to **view** the reports: the sign-in page (`/login/`) in the browser, HTTP Basic for the API |
| `TRACEREPORTS_SESSION_IDLE_MIN` / `_MAX_HOURS` / `_REMEMBER_DAYS` | `30` / `8` / `7` | Sign-in page session: it expires after this many minutes without use, or after this many hours anyway; "Remember me" keeps it this many days |
| `TRACEREPORTS_UI_VIEWER_USER` / `TRACEREPORTS_UI_VIEWER_PASSWORD` | — | **Read-only** login for the rest of the team: sees everything, never sends results, changes Settings nor uses actions. A different user from the admin one |
| `TRACEREPORTS_ALLOWED_HOSTS` | — | Without UI login: names served besides `localhost` (comma separated; `*` = any). See [Security](#security) |
| `TRACEREPORTS_ALLOWED_CIDRS` | — (all) | Only these networks get in (`10.0.0.0/8,fd00::/8`, or single IPs); the rest gets `403`, even with a token. This machine always gets in. See [Restrict by source network](#restrict-by-source-network) |
| `TRACEREPORTS_TRUSTED_PROXIES` | — | Proxies whose `X-Forwarded-For` / `X-Real-IP` is trusted to know the client IP. Without it those headers are ignored |
| `TRACEREPORTS_LOCAL_ADMIN` | — | `1`: with a token and no login, the same machine (no proxy) may change Settings and use the UI actions |
| `AI_PROVIDER` | inferred from the key; without a key, `builtin` | `builtin` (local engine, no LLM), `gemini`, `anthropic`, `openai`, `openai_compatible`, `ollama` or `off` (no diagnosis). See [Local engine](#local-diagnosis-engine-no-llm) |
| `AI_MODEL` | the provider's default | Model (see the provider table) |
| `AI_API_KEY` | — | The provider's API key (Ollama does not use one) |
| `AI_BASE_URL` | the provider's API | API URL: proxy, your own endpoint, a compatible service or a remote Ollama |
| `GEMINI_API_KEY` / `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `OLLAMA_HOST` | — | Alternative to the `AI_*` variables: with just one of these the provider is picked automatically |
| `TRACEREPORTS_AI_BUILTIN_HINT` | `1` | With an LLM configured, its prompt first includes the local engine's conclusion (already redacted). `0` removes it |
| `TRACEREPORTS_AI_LANGUAGE` | `auto` | Language of the diagnoses: `es`, `en` or `auto` (the team's default language; without one, each test's). What is chosen in **Settings → Artificial intelligence** takes precedence over this variable; with `TRACEREPORTS_SETTINGS_LOCKED=1` this is how to set it. Existing diagnoses do not change: re-analyze them |
| `TRACEREPORTS_AI_ALLOWED_PROVIDERS` | — (all) | Allowed providers, comma separated (`builtin,ollama`). One outside the list is never used. See [Allowed providers](#allowed-providers-and-data-leaving-the-server) |
| `TRACEREPORTS_ENV_FILE` | `.env` | `KEY=value` file the server reads on startup |
| `TRACEREPORTS_SETTINGS_LOCKED` | — | `1`: the Settings screen becomes read-only; AI is configured only from the environment |
| `TEAMS_WEBHOOK_URL` | — | Sends each run's summary to Microsoft Teams |
| `SLACK_WEBHOOK_URL` | — | Sends each run's summary to Slack |
| `PUBLIC_URL` | — | Public URL of the server, for the *View report* button in notifications |
| `TRACEREPORTS_BIND` | all interfaces; without credentials, only this machine | Address to listen on: `127.0.0.1` or `localhost` serves only this machine, over IPv4 and IPv6 (`::1`), with no firewall prompt on Windows. See [Security](#security) |
| `TRACEREPORTS_INSECURE` | — | `1`: start listening on the network **without** a token or login (only in a closed lab network). See [Security](#security) |
| `NOTIFY_ON` | `always` | `always`, or `failures` to notify only when something failed |
| `NOTIFY_DETAIL` | `full` | `minimal`: Teams and Slack get only counts, status and the link; no test names, errors, backend URLs or AI text. See [Notifications](#teams-and-slack-notifications) |
| `NETWORK_MAX_BODY_KB` | `256` | Maximum stored size of each network *response body*; `0` = store no bodies |
| `TRACEREPORTS_REDACT` | on | `off` stores secrets as received (not recommended) |
| `TRACEREPORTS_REDACT_HEADERS` / `TRACEREPORTS_REDACT_KEYS` | — | More headers and keys (JSON, query, form) to mask, comma separated |
| `TRACEREPORTS_REDACT_PATTERNS` | — | Extra regular expressions, separated by `;` (e.g. a national id: `\b\d{7,8}-[\dkK]\b`) |
| `TRACEREPORTS_RETENTION_DAYS` | — | Deletes the runs (and their screenshots) older than N days, every 6 h; without it, the days chosen in Maintenance (see [Storage and retention](#storage-and-retention)) |
| `TRACEREPORTS_SCREENSHOTS` | `all` | Which screenshots the server keeps: `all`, `failures` (only of a failed step or test) or `off` (none, no trace or video either). See [Screenshots](#screenshots) |
| `TRACEREPORTS_LAB` | on | `off` disables the Lab (resending captured calls from the report) |
| `TRACEREPORTS_LAB_HOSTS` | — | Extra hosts a shared server's Lab may call, comma separated (`staging.acme.com`, `api.acme.com:8443`, `*.acme.internal`). An internal host listed here is called without asking for confirmation. See [FixLab](fixlab.md#security) |
| `TRACEREPORTS_LAB_STRICT` | off | `1`: a shared server's Lab never calls an internal host (private IP or this machine) outside `TRACEREPORTS_LAB_HOSTS`; without it, the person confirms it |
| `TRACEREPORTS_AI_STATUS_URL` | — | Status page (Statuspage format, `…/api/v2/summary.json`) that Settings → AI usage shows instead of the provider's: a mirror, a proxy or the page of an OpenAI-compatible provider |
| `TRACEREPORTS_AI_MAX_PER_RUN` | `50` | Automatic AI diagnoses per run; the rest are left *not analyzed* and can be analyzed by hand. It counts one diagnosis per test **result**: resending the same result, a restart or a provider error do not spend another one, and a manual *Re-analyze* does not count. `0` = no limit |
| `TRACEREPORTS_AI_BUDGET_RECOVERY` | — | Only if the server asks for it on start (a database left halfway by an older version): `backfill` counts the diagnoses already made as spent, `keep` leaves the count as it is |
| `TRACEREPORTS_LEGACY_DB` | — | Only if the server asks for it on start: `ignore` starts with a new database when `DATA_DIR` has `#`, `?` or `%` and an older version kept the database elsewhere (move it instead to keep the data) |
| `TRACEREPORTS_STALE_RUN_HOURS` | `24` | Closes as *incomplete* a run still *in progress* that received nothing for N hours (the client died without closing it); its unfinished tests become *Interrupted*. Decimals allowed (`0.5`). `0` = never. Without the variable, it is set from *Maintenance*. See [Abandoned runs](#abandoned-runs) |
| `TRACEREPORTS_REDACT_PII` | on | Masks RUTs, phone numbers and e-mails in the DOM snapshot (text and text attributes; the locator attributes stay: id, class, data-test*, placeholder and aria-label). Dates are not touched. `off` turns it off |
| `TRACEREPORTS_NO_SIGNAL_MIN` | `3` | Live: a running test is *without signal* when nothing of it arrived for N minutes (server clock). Decimals allowed. See [Live](#live) |
| `TRACEREPORTS_LIVE_ALERTS` | — | `1` sends the Live alerts (a test without signal, a slow test, too many failures) to the Teams/Slack webhooks, once each |
| `TRACEREPORTS_ALERT_TURTLE` | — | Also alert a test that runs N times its p90 (e.g. `3`) |
| `TRACEREPORTS_ALERT_FAILURES` | — | Also alert an execution still running with more than N failures |

## Security

**The server is never left open to the network without credentials** (*fail-closed*):

| Credentials (`TRACEREPORTS_TOKEN`, `TRACEREPORTS_INGEST_TOKEN` or the UI login) | `TRACEREPORTS_BIND` | Result |
|---|---|---|
| Yes | any | Listens where `TRACEREPORTS_BIND` says (empty: every interface) |
| No | empty | Listens **only on this machine** (`127.0.0.1` and `::1`), with a warning |
| No | `127.0.0.1`, `::1` or `localhost` | Listens only on this machine |
| No | `0.0.0.0`, `::` or a network IP | **Does not start** and explains what to set |
| No | Docker (official image) | **Does not start**: inside the container it always listens on every interface, even when the port is published only on `127.0.0.1` |

`TRACEREPORTS_INSECURE=1` accepts the risk knowingly (a closed lab network): it starts as before,
with a warning in the log. Without credentials, anyone who reaches the port can read and write
reports.

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
- **`TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD`**: the UI opens on a sign-in page (`/login/`) and keeps the
  session in an `HttpOnly`, `SameSite=Strict` cookie (`Secure` with HTTPS); only a hash of it is stored. It expires
  after `TRACEREPORTS_SESSION_IDLE_MIN` without use (a forgotten tab does not keep it alive) or
  `TRACEREPORTS_SESSION_MAX_HOURS` anyway; "Remember me" keeps it `TRACEREPORTS_SESSION_REMEMBER_DAYS`. Changing the
  password ends every session. Failed sign-ins count for the same brake as HTTP Basic and the tokens. The
  read API keeps accepting HTTP Basic and the tokens, as before.
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

### Restrict by source network

Besides the firewall, the server can enforce on its own which networks get in:

```ini
TRACEREPORTS_ALLOWED_CIDRS=10.0.0.0/8,192.168.10.0/24,fd00::/8
TRACEREPORTS_TRUSTED_PROXIES=10.0.0.5        # the reverse proxy, if there is one
```

- Every request whose source IP is not in the list gets `403` (UI, API and `/screenshots/*`), **even with a
  valid token**. This machine (`127.0.0.1`, `::1`) always gets in.
- The source IP is the connection's. `X-Forwarded-For` and `X-Real-IP` are only trusted when the connection
  comes from a proxy of `TRACEREPORTS_TRUSTED_PROXIES`, and then the last hop that is not a trusted proxy is
  taken: a client that sends its own `X-Forwarded-For` does not get around the filter.
- An invalid value stops the server from starting. Each refused IP is logged once.

### Permissions of each credential

| Credential | Sends results | Reads reports | Changes Settings and uses UI actions |
|---|---|---|---|
| `TRACEREPORTS_TOKEN` | Yes | Yes | **Yes** (kept like that for compatibility) |
| `TRACEREPORTS_INGEST_TOKEN` | Yes | Yes | No, not even from the same machine |
| UI login (`TRACEREPORTS_UI_USER`/`_PASSWORD`) | No | Yes | Yes |
| Read-only login (`TRACEREPORTS_UI_VIEWER_USER`/`_PASSWORD`) | No | Yes | No, not even from the same machine |
| No credentials | Only with no token configured | Only without UI login (and through an allowed host) | Only in local mode, from the same machine |

- **Two logins**: `TRACEREPORTS_UI_USER` administers (Settings, escalations, tickets, Lab);
  `TRACEREPORTS_UI_VIEWER_USER` is for the rest of the team and only reads. Nobody needs the login that
  can change the AI just to see reports.
- **The AI key cannot be redirected**: a saved or environment key is only sent to the URL it was
  configured for. Changing the API URL in Settings requires typing the key again; otherwise it is
  rejected (409), so an admin cannot point the URL to their own server and receive the key.
- **A token alone does not protect reads**: without UI login, anyone reaching the server through an
  allowed host (`localhost`, `PUBLIC_URL` or `TRACEREPORTS_ALLOWED_HOSTS`) sees the reports. On a
  shared server configure the login too.
- Recommended in CI: `TRACEREPORTS_INGEST_TOKEN` in the pipeline and `TRACEREPORTS_TOKEN` only for
  whoever administers. Clients send it like the other one (`TRACEREPORTS_TOKEN=<ingest token>` in
  the pipeline environment, or `Authorization: Bearer`). `GET /api/v1/auth/check` tells the scope:
  `token_scope` = `admin` or `ingest`.
- Without UI login, a request with a valid token (admin or ingest) is answered from any host, so
  CI does not need to be listed in `TRACEREPORTS_ALLOWED_HOSTS`; requests without credentials from
  unlisted hosts are still rejected.
- Setting only `TRACEREPORTS_INGEST_TOKEN` also counts as a deployed server: the same machine needs
  the login to change Settings, unless `TRACEREPORTS_LOCAL_ADMIN=1`.
- **Pending (incompatible change, not applied)**: making `TRACEREPORTS_TOKEN` stop administering. It
  would break whoever changes Settings with it from scripts today; it will be evaluated for a major
  version, announced beforehand. Meanwhile, use the ingest token in pipelines.

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

## Local diagnosis engine (no LLM)

With nothing configured, each failure and each run get a diagnosis from the **local rules engine**
(`AI_PROVIDER=builtin`): a catalog of deterministic rules inside the same binary. **It uses no network,
needs no model or API key and costs nothing**: test data never leaves the server. It always gives the
same answer for the same evidence, in milliseconds, and each conclusion cites the evidence it relies on
(the call, the step, the line). The UI labels it **Local engine** with the rule's name, never as AI.

The engine uses what TraceReports already correlates: the backend call that caused the failure, the error
message and trace, the failed step, the page's JavaScript errors, the test's history and how much of the
run failed. The rule with the highest priority wins (on a tie, the most confident one):

| Rule | Category | When it applies |
|---|---|---|
| `backend_5xx` | `BACKEND_TIMEOUT` | A backend call answered 5xx right before the failure |
| `backend_rate_limited` | `BACKEND_TIMEOUT` | It answered 429 |
| `network_unreachable` | `INFRA_ERROR` | The call got no answer because of DNS, a refused connection, TLS or a cut connection |
| `backend_no_response` | `BACKEND_TIMEOUT` | No answer because of a timeout, or 408 |
| `auth_rejected` | `INFRA_ERROR` | 401/403: the test's session or credentials |
| `backend_status` | `BACKEND_TIMEOUT` | No network captured; the test expected another status and got 5xx/429/408 (Go, REST, JUnit) |
| `network_error` | `INFRA_ERROR` | The error message is about DNS, a refused connection, TLS or a connection exception (`ConnectionError`) |
| `js_error` | `LOGIC_BUG` | An uncaught JavaScript error and the expected element never showed up |
| `backend_client_error` | `LOGIC_BUG` | A 4xx the application did not recover from |
| `locator_not_found` | `LOCATOR_CHANGED` | The selector did not find the element |
| `assertion` | `LOGIC_BUG` | A check failed (cites the expected value, the actual one and the line) |
| `environment_down` | `INFRA_ERROR` | 80 % or more of a run of at least 5 tests failed |
| `timeout` | `BACKEND_TIMEOUT` | A timeout with no network evidence |
| `flaky` | `INFRA_ERROR` | The test alternates between passing and failing and there is no other evidence |
| `no_conclusive_rule` | `LOGIC_BUG` | None applies: it says so and cites the best evidence |

If the test is flaky, the conclusion adds it as a note. The run summary counts the incidents by kind and,
when almost everything failed, points at the environment.

### Local engine, local LLM or cloud LLM

How to run a local LLM (Docker Compose `llm` profile, llama.cpp, LM Studio, vLLM…): [Local LLM](local-ai.md).

| | Local engine (`builtin`) | Local LLM (Ollama, llama.cpp, LM Studio…) | Cloud LLM |
|---|---|---|---|
| Data leaving the server | Never | Stays in your network | Yes (redacted) |
| Setup | None | A runtime and a model | An API key |
| Cost | Zero | Hardware | Per use |
| Result | Deterministic, citing the rule | Written; varies | Written; the most accurate |
| Uncommon failures | "No conclusive rule" | Proposes a hypothesis | Proposes a hypothesis |

With an LLM configured, the local engine **runs first** and the model gets its conclusion (redacted) to
confirm or correct it: the LLM writes up and enriches, but the evidence and the grouping do not depend on
it. `TRACEREPORTS_AI_BUILTIN_HINT=0` turns that off. `AI_PROVIDER=off` turns every diagnosis off. The
`TRACEREPORTS_AI_MAX_PER_RUN` limit does not apply to the local engine (it costs nothing), and its
diagnoses do not count in **AI usage**. `tracereports report` uses it too without `--ai`.

### Adding a rule

The rules live in [`internal/ai/builtin`](../../internal/ai/builtin): each one is a function that reads the
evidence (`Input`) and, when it applies, proposes a category, a confidence and the data of its texts. To
contribute one:

1. Add it to `Catalog` in `builtin.go` with a priority that does not clash with another.
2. Write its texts in Spanish **and** English in `templates.go` (a test requires the same variables).
3. Add cases to `builtin_test.go`: one that triggers it, one that does not and the edge of its condition.
4. If the meaning of the catalog changes, bump `Version` (`rules-v1` → `rules-v2`).
5. `go run ./cmd selftest` shows the hit rate over the recorded scenarios (minimum 90 %).

## AI diagnosis

Besides the local engine, it works with any of these providers:

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

### Allowed providers and data leaving the server

If the evidence must not leave your network, allow only the local providers:

```ini
TRACEREPORTS_AI_ALLOWED_PROVIDERS=builtin,ollama
```

- A key of a provider outside the list (a `GEMINI_API_KEY` forgotten in the environment) **is ignored**: the
  log says so and the local engine diagnoses if it is allowed, or nobody does.
- Settings only offers the allowed ones; saving another answers `400` and "Test connection" does not call it.
- A configuration saved earlier from Settings with a provider that is no longer allowed is not loaded.
- An unknown name in the list (`olama`) stops the server from starting.

At startup the server logs **every external destination** its configuration uses (the AI and its status
page, Teams, Slack, GitHub, Jira, Azure DevOps and the extra Lab hosts), with only the host and whether it
is local, private or public:

```text
INFO egress kind=ai name="Ollama (local)" host=127.0.0.1:11434 scope=loopback sends="diagnosis prompts (redacted; never screenshots)"
INFO egress: no external destination configured (nothing leaves this server)
```

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

**Shared channels (`NOTIFY_DETAIL=minimal`)**: if the channel must not see test names, errors or the
diagnosis, with `NOTIFY_DETAIL=minimal` only the run name, the counts (OK, failed, skipped, changes since
the previous run), the status and the **View report** button arrive. It applies to everything that is sent:
the notice of each run, the weekly summary (its numbers, without the list of tests) and escalations (only the
severity and the link; the details stay in the report).

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
Language: the one in **Settings**. If the server was asleep or stopped at that time, it sends the
pending summary once when it wakes up and goes on with the next week (it does not send one per
missed week).

**Pass rate** (metrics, weekly summary and release): `PASS` over the tests that ran, that is,
everything but `SKIP`. A `WARNING` ran and did not pass.

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

**No duplicates, even if something fails halfway:** two requests for the same failure and tracker are
handled one after the other. If the tracker's answer is lost (timeout, `5xx`, the server stopped
right then), the ticket may exist anyway: TraceReports answers `502` with `uncertain: true` and,
until you check the tracker, `409` to the next attempts. Send `force` to create another one on
purpose. The tracker's token never appears in the error shown (`[credential]`).

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

**Similar incidents**: each classified failure goes into an index with its signature (the backend call
that caused it or the normalized error, the category and the local engine's rule). When another test fails,
its diagnosis shows up to three already classified failures **of the same project** that look at least 60 %
like it, with their verdict and comment: *"92 % · pays an order · Environment · connection pool exhausted"*.
It is TF-IDF over the signature, no AI and no network; the percentage is similarity, not probability.
Retention deletes the entry with the evidence. Through the API: `GET /api/v1/tests/{id}/similar`.

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

- **Run it locally**: a test's detail has the commands to run it on your machine: only that test
  (`pytest '<nodeid>'`, `npx playwright test <file> -g '<title>' --project=<project>`, Maven/Gradle
  for JUnit or `go test ./<package>/... -run '^TestX$'`) and its whole file or class. When the error
  says the test needed an earlier one (empty data, a step skipped because a previous one failed),
  the whole file comes first: alone, that test fails for another reason. The run's commit is a note
  (`git checkout <commit>` only if your copy is on another version), never chained to the command.
  The same goes for Escalate (Development), tickets, the Lab capsule and MCP. It comes from the test
  identity, so the client must report it (all of them do).
- **Resend it in the Lab (FixLab)**: *Open in Lab* on a failed call (**Network** tab) or on a
  backend incident resends the call with a fresh session (it repeats the recorded login and reads
  anti-CSRF tokens live; you only type the password) to see whether the failure **still happens**:
  **reproduced**, **no longer fails**, **session missing**, **different error** or **no
  connection**. Save it as a **check** and press **Verify** on the incident once it is fixed;
  export it to Postman, Bruno or `.http`; take it to your terminal or CI with
  `tracereports repro` / `verify`, or to Claude Code with `tracereports mcp`. On a shared server it
  only calls the hosts the test called and `TRACEREPORTS_LAB_HOSTS`, and asks before an internal host
  that is not in that list (`TRACEREPORTS_LAB_STRICT=1` refuses it); `TRACEREPORTS_LAB=off`
  disables it. Full guide: [FixLab](fixlab.md).
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
| Minimum pass rate (skipped and quarantined failures do not count; a `WARNING` is not a pass) | 95 % | blocks |
| New failures against the previous run | 0 | risk |
| Quarantined failures | — | risk |
| Flaky tests | 3 | risk |

Set it with `TRACEREPORTS_RELEASE_GATE`, for example
`min_pass_rate=98; critical=smoke,checkout,login; max_new_failures=0; max_flaky=2`. A malformed
criterion stops the server from starting. Through the API: `GET /api/v1/runs/{id}/release`.

## Live

**Live** (side menu) shows what is running now, per execution, with what the server already receives
from any client:

- **Executions**: the runs of one execution together. They share `execution_id` (the clients send
  `TRACEREPORTS_EXECUTION_ID` or the CI build id); without it, runs of the same project, environment, branch
  and commit that start one after another (a launcher that creates a run per file).
- **Progress and ETA** when the client says how many tests it collected (the pytest plugin does).
- **Turtle**: a running test slower than its p90 in its previous runs (or twice its median). **No signal**:
  nothing of it arrived for `TRACEREPORTS_NO_SIGNAL_MIN` minutes (3 by default). They go first.
- **Status tiles** (Pass, Fail, Warning, Running, Turtle, No signal) that filter the table with a click, and
  **Only with problems** (fail, interrupted, turtle, no signal), on by default with more than 20 runs or lanes.
- **Timeline**: a minimap of the whole execution (drag to zoom into a stretch; −, + and Fit) and the lanes
  per worker. Without `worker` (a run per file), lanes are reused like processes: each run takes the first
  free lane, so 86 runs in 12 processes show as 12 lanes.
- A **table** grouped by file, worker or status, most severe first, with the duration against the median
  and the last step (one line, without URLs; the full text in the tooltip), and a search over test, file,
  worker or step. With many files, the ones without problems fold into one line.
- **Side panel**: clicking a test (row or bar) shows its status and, in tabs, its **trace** (the steps and
  the backend calls on one timeline, failed calls in red and the moment of the failure marked), the last 5
  steps and the latest screenshot, without leaving Live; "Open the test" goes to the detail.
- **Execution health**: failure rate (and the difference against the previous execution of the same
  project), typical (p50) and slow (p95) duration and pace in tests per minute, each with its sparkline.
- **Diagnosis**: errors grouped by signature (a click filters), the slowest against their history, the
  **execution backend** (endpoints of all its runs, errors first, with p95) and the recent activity. On the
  minimap, the failure or no-signal icon marks where each one happened.
- **Facets** beside the table (status, error, file, worker), several at once, and the table sorts by status,
  name, duration or against the median. **Pause** freezes the view to analyze it.
- **Needs attention**, on top: the tests that failed, have no signal, were interrupted or are turtles, with
  file, worker, duration and ×p50; one click opens the detail.
- The chosen statuses and the search go in the link (`&f=fail,warn`, `&lq=text`): sharing it opens the view
  already filtered. Values of `f`: `fail`, `nosig`, `turtle`, `warn`, `run`, `pass`, `skip`, `int`.
- `/` jumps to the search box and `Esc` clears it. The last step fits one line, with XPaths shortened and the
  stack trace in a badge that opens below; the table has a **Worker** column and "Expand all / Collapse all".
- **Past executions**: the list shows the last 2 hours, but any execution opens with the same view by its
  key or by one of its runs (`#view=live&exec=r:888`). **Observability**, in the top bar, opens the
  execution of the run being looked at.
- **Show** (All / With problems) and **Group by** (file, worker, status) are segmented controls: they answer
  in the same frame as the click (the control changes and a thin bar says it is recomputing) and a status
  line says what is shown: "Showing 6 of 234 tests · Only with problems · Grouped by file".
- **Execution map**, in two views. **Field** (default): the execution as a torus of particles read like a clock; the angle is time (the start
  on top, clockwise, with pauses compressed) and each ring a worker or process. Each test is a point where it
  ended (or where it is, on the red "now" hand), bigger the slower it was, with a thin trail of how long it
  lasted; failures glow. Hovering shows it, a click (or the arrows and Enter) opens its detail. Around it, the
  channels (tests, failures, network, workers: each filters or groups), the **real ingestion** (events of this
  execution that arrived through the stream in the last minute and how long ago the last one; finished or
  without events it never says "active"), progress, the dominant anomaly (a click filters by its signature),
  the latest signals and the error pressure. Every incoming event fires a pulse from its channel to its test. The field can be handled: drag rotates and tilts, Shift + drag moves,
  +/− (or Ctrl + wheel; the wheel in full screen) zooms, F goes full screen (Esc returns), 0 resets the view;
  a double click on a particle opens the test. **Pause view** (or Space) freezes the field to look into
  something while ingestion goes on in the server: it counts the events arriving and "Resume (+N)" catches up.
  **Grid**: one square per test, in the order they started; a click opens its detail. On top, a **ticker** with the latest events that stops
  on hover, and a clock.
- The **timeline** starts at the first test (not at the start of the execution), marks **now** with a line
  and compresses long pauses with no test running (≈, with how long they lasted). Ctrl + wheel zooms in or
  out; **Fit to window** goes back to everything; **Hide idle** keeps only the lanes that are running.
  Without `worker`, lanes are called **Process 1, Process 2…** (a parallel lane); the legend explains it, and
  **×1.7** too (took 1.7 times normal: its median in earlier executions).
- Long names carry the **ellipsis in the middle** (the start and the end, which tells one test from another,
  stay visible), with the full name in the tooltip and a button to copy it; the Test column widens by
  dragging its edge (or with ← →) and **Names on 2 lines** shows them whole. Both preferences are remembered
  in that browser.
- Choosing a status (or a facet, or searching) shows in the **timeline**: matches stand out, lanes without a
  match fold into one line, the minimap counts only those and the title says "3 of 234 tests match". Hovering a
  lane highlights its tests in the table.
- **New failures**: every failed test says whether it passed in its previous execution ("New") or was already
  failing. Shown in Needs attention (which has its own search box and status filter, "new" included), the table
  and **Grouped errors**, where each error opens with the tests it belongs to.
- The side panel shows the **test's lane** with what ran before and after it. When the execution ends, a
  **closing summary** with duration, results, new failures and slow tests, and "Escalate the failures".
- **Keyboard**: J/K next or previous needing attention, O opens the test, P pauses, C compact mode (no
  diagnosis cards nor charts, for a small screen or a TV), ? shows the shortcuts; a double click on a row opens
  the test.
- Every control, icon, column and status has a **tooltip** (mouse or keyboard focus) saying what it is or
  does. Statuses have their own icon set (the turtle is an icon, not the word).
- **Charts**: results over time (by status), cumulative failure rate (with the previous one as a reference)
  and the duration distribution (with p50 and p95); each with "Show data" as a table.
- The **execution picker** shows the time, how long ago it started or ended and whether it is running,
  recent, from today or earlier; with more than 8 executions it has a search box and groups them by age.
- **Cancel execution…** (admin): a cooperative cancel. No process is killed: clients with
  `TRACEREPORTS_REMOTE_CONTROL=1` see it between tests, skip the rest and close the run.
- **Alerts** to Teams/Slack with `TRACEREPORTS_LIVE_ALERTS=1` (see the reference).

It updates through the live events (SSE) and every 10 s, so a test without signal shows up although nothing
arrives.

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

### Screenshots

A screenshot can show personal data or credentials, and text redaction does not reach it. The server
decides which images it keeps with `TRACEREPORTS_SCREENSHOTS`, regardless of how each suite is
configured:

| Value | What it keeps |
|---|---|
| `all` (default) | Every screenshot, trace and video |
| `failures` | Only screenshots of a failed step or a failed test; traces and videos only of failed tests |
| `off` | No image, trace or video (steps, network and errors still arrive) |

An image the policy does not allow is refused with `403` **before** it is written: no file is ever
left on disk. It also applies to Allure imports and `tracereports report`. An unknown value stops the
server from starting.

TraceReports does **not** recognize text inside images (OCR) to cover it: it is slow, needs models
outside the binary and is never complete (data the OCR misses leaks anyway). What works is not
capturing it: synthetic data in test environments, this policy and the clients' masking before the
capture.

### Abandoned runs

If the test process dies without closing the run (cancelled CI job, IDE *Stop*, closed terminal, lost
machine), it stays *in progress*. After `TRACEREPORTS_STALE_RUN_HOURS` hours (24 by default) **without
receiving anything**, the server closes it as *incomplete*: the tests still running become
**Interrupted** (`INTERRUPTED`), with a message that says how long it was idle, and the diagnosis and
the notification follow, once. Activity is the server time when the last write arrived, not the times
the client sends: a long run that keeps sending is never closed, and an offline client uploading its
recording late does not look abandoned. Whatever arrives later (a step, the `finish`) is accepted as
late evidence, without notifying again. Once closed, retention treats it like any other.

An **Interrupted** test did not get to finish: it is neither a failure nor a pass (out of the pass
rate, flaky and the Release gate), the AI does not analyze it and the run stays *Warning*, incomplete.
The same happens to the tests still running when the client closes with `{"interrupted": true}`
(`Ctrl+C`) or a worker crashes. Tests that earlier versions closed as `FAIL` "Interrumpido…" are turned
into `INTERRUPTED` when the server starts.

**By hand, without waiting.** A running run with no data for 5 minutes shows *No activity for X min*
with a **Close run…** button (admin): it asks for a reason, closes the pending tests as Interrupted and
records who closed it and why. The **Maintenance** view lists the runs with problems across projects (no
activity, running, incomplete, closed by hand or for inactivity), filtered by project, environment,
branch and age, to close or delete them in bulk (a running run is not deleted: close it first).

**The threshold from the interface.** In Maintenance, *Close after* changes the threshold (30 min to
48 h, or *Never*) without restarting the server; it needs the same permission as Settings. If the server
has `TRACEREPORTS_STALE_RUN_HOURS`, the variable wins and the selector is locked.

### Storage and retention

The evidence lives in the data folder (`DATA_DIR`; in Docker, the `tracereports-data` volume mounted at
`/data`): the SQLite database (runs, steps, network, DOM) and `screenshots/` (screenshots and
attachments). **Maintenance → Storage** shows:

- the disk of that folder: how much is in use and free, with a warning (also in the log, every 6 h) from
  80 %, saying how much of it TraceReports takes;
- the evidence stored (database and files), how many runs and since when, the average growth per day and
  how long the disk would last at that pace;
- what each project takes (its files plus its share of the database, approximate);
- files no run uses (left by interrupted deletions or earlier versions).

**Retention.** *Keep* chooses how many days of runs to keep (30, 60, 90, 180, 365 or everything) without
restarting the server; it needs the same permission as Settings. Every 6 hours the finished runs older
than that are deleted with their screenshots and the database is compacted. `TRACEREPORTS_RETENTION_DAYS`
wins over the interface and locks the selector. Without either, nothing is deleted (and the server says
so when it starts).

**Clean up now…** (admin) asks how many days to keep, shows first what would be deleted (runs, tests and
the space it would free) and deletes only after confirming. The last cleanup (when, what and how much it
freed) and the next one are shown below the selector. A run still in progress is never deleted.

API: `GET /maintenance/storage`, `GET /maintenance/storage/preview?days=N`, `PUT /ui/maintenance/retention`
(`{"days": N}`, 0 = keep everything) and `POST /ui/maintenance/retention/run` (`{"days": N}`, or `{}` for
the retention in force).

## AI diagnosis after a restart

Diagnoses left halfway (*pending*) are resumed when the server starts. The same test is never
analyzed twice at the same time nor paid again if it already has a diagnosis (except with
*Re-analyze*). When the run summary was written while diagnoses were still pending, it says so and
is rewritten when they finish.
