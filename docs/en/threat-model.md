# Threat model

[Español](../es/threat-model.md) · Reviewed for each minor release. Last review: the security hardening
epic (#34), cross-checked against the code.

TraceReports receives evidence from real test runs and shows it to a team. This document says what it
protects, from whom, through which doors, what stops each threat today and what is left to the operator.
It is the reference for the [hardening guide](security-hardening.md) and for `tracereports doctor --security`.
Where a control is partial, the table says so: an honest gap is more useful than an overstated mitigation.

## Assets

| Asset | Where it lives | Why it matters |
| --- | --- | --- |
| **Evidence** — screenshots, Playwright traces, videos, DOM snapshots | `DATA_DIR/screenshots`, table `test_dom` | Shows what was on the screen: personal or business data of the tested app |
| **Network captures** — URLs, headers, request and response bodies | SQLite (`network`) | May carry tokens, cookies and personal data |
| **Run data** — test names, errors, steps, logs | SQLite | Reveals internal hosts, users, the shape of the system |
| **Credentials of TraceReports** — `TRACEREPORTS_TOKEN`, ingest token, UI password | Environment / `.env` | Full or partial control of the server |
| **Third-party credentials** — AI key, tracker tokens, webhooks, `TRACEREPORTS_LAB_<NAME>` | Environment; the AI key optionally in SQLite, encrypted with `TRACEREPORTS_SECRET_KEY` | Access to paid services and to other systems |
| **Keys** — `TRACEREPORTS_SECRET_KEY`, `TRACEREPORTS_DB_KEY` | Environment or a secret file, never in `DATA_DIR` | Whoever has them reads what they protect |
| **AI prompts and answers** | Sent to the configured provider; diagnoses in SQLite | The prompt is built from the evidence |

## Actors and what each credential can do

There are no finer roles than these credentials. Plan access with this table in mind.

| Credential | Writes results | Reads everything | Administers (Settings, UI actions, tickets, Lab) |
| --- | --- | --- | --- |
| Ingest token (`TRACEREPORTS_INGEST_TOKEN`) — test clients and CI | yes | **yes** | no |
| Admin token (`TRACEREPORTS_TOKEN`) | yes | yes | yes |
| UI login (`TRACEREPORTS_UI_USER`) — whoever administers | — | yes | **yes** |
| Read-only login (`TRACEREPORTS_UI_VIEWER_USER`) — the rest of the team | no | yes | no |
| No credential, same machine, server without credentials (local mode) | yes | yes | yes |

Other actors: a **network attacker** who reaches the port without credentials; a **malicious or careless
insider** with a valid credential; a **compromised third party** (AI provider, chat workspace, tracker,
a build dependency); a **host or backup thief** with a copy of `DATA_DIR`.

## Surfaces and mitigations

| Surface | Threats | Mitigations in place | Gaps |
| --- | --- | --- | --- |
| **Ingest API** (`POST /api/v1/...`) | Writes without credentials; malicious uploads (zip bombs, fake images) | A token is required when credentials are set; the server **refuses to listen on the network with no credential at all** (#37); body limits per endpoint; image/trace/video checked by content; Allure ZIP limits on files and unzipped size (50/200 MiB); idempotency keys | No general rate limiting nor disk quota (put a proxy with limits in front); read timeout of 15 min per request, idle timeout of 2 min |
| **Reads** (UI, read API, `/screenshots/*`, export, SSE `/api/v1/stream`) | Reading reports without login; DNS rebinding | UI login; any valid token also reads; **source IP allowlist** `TRACEREPORTS_ALLOWED_CIDRS` applied before trusting proxy headers (#38); without login only local or allowed `Host` names (anti DNS rebinding); `/screenshots/*` CORS only for the Playwright Trace Viewer on `.zip`; `GET /healthz` returns no data | **With a token and no UI login, reads are open** to whoever reaches the port (the `Host` check is not access control: the client chooses the header). Live stream capped at 500 connections (503 past it). After 10 failed logins or tokens in 15 min an address gets 429 for the rest of the window |
| **Settings and UI actions** | Changing the AI provider or key to exfiltrate evidence | Require the admin token, the UI login or local mode — never the ingest token; JSON + `Origin` check (CSRF); `TRACEREPORTS_SETTINGS_LOCKED=1` makes Settings read-only; `AI_ALLOWED_PROVIDERS` checked when saving (#39); the AI key saved encrypted | Every UI user is an admin. The allowlist checks the **provider id, not its URL**: an admin can point `base_url` elsewhere and the saved key is reused. Use `SETTINGS_LOCKED=1` on shared servers |
| **Ingest writes from a browser** (CSRF) | A web page makes a developer's browser post fake runs to a local server | Every write the browser marks as coming from another site (`Origin` of another host, `Origin: null`, `Sec-Fetch-Site: cross-site`) is rejected with 403; test clients send neither header | |
| **Web UI** | Stored XSS from test names and errors; framing | Values escaped before inserting HTML; **Content-Security-Policy** (only this server's scripts, the inline ones by hash, no inline handlers), `X-Frame-Options: DENY` and `frame-ancestors 'none'`, `nosniff`, `Referrer-Policy: same-origin`; captured HTML previews in a `sandbox` iframe and served with `CSP: sandbox` | Styles allow inline attributes |
| **Ingest API** (`POST /api/v1/...`) | Writes without credentials; malicious uploads (zip bombs, fake images) | A token is required when credentials are set; the server **refuses to listen on the network with no credential at all** (#37); body limits per endpoint; image/trace/video checked by content; Allure ZIP limits on files and unzipped size (50/200 MiB); idempotency keys | No rate limiting, no disk quota, only a header read timeout |
| **Reads** (UI, read API, `/screenshots/*`, export, SSE `/api/v1/stream`) | Reading reports without login; DNS rebinding | UI login; any valid token also reads; **source IP allowlist** `TRACEREPORTS_ALLOWED_CIDRS` applied before trusting proxy headers (#38); without login only local or allowed `Host` names (anti DNS rebinding); `/screenshots/*` CORS only for the Playwright Trace Viewer on `.zip`; `GET /healthz` returns no data | **With a token and no UI login, reads are open** to whoever reaches the port (the `Host` check is not access control: the client chooses the header). No limit on SSE subscribers. No brute-force throttling on the login |
| **Settings and UI actions** | Changing the AI provider or key to exfiltrate evidence | Require the admin token, the UI login or local mode — never the ingest token; JSON + `Origin` check (CSRF); `TRACEREPORTS_SETTINGS_LOCKED=1` makes Settings read-only; `AI_ALLOWED_PROVIDERS` checked when saving (#39); the AI key saved encrypted | A saved or environment key is only sent to the URL it was configured for: changing the URL requires typing the key again (409 otherwise), also in "Test connection". The read-only login never administers. An admin who types a key can still send it anywhere: use `SETTINGS_LOCKED=1` on shared servers |
| **Ingest writes from a browser** (CSRF) | A web page makes a developer's browser post fake runs to a local server | `Host` check (only local names in local mode) | Ingest endpoints do not check `Origin` nor require JSON: in local mode without a token a cross-site form can create runs |
| **Web UI** | Stored XSS from test names and errors; framing | Values escaped before inserting HTML; captured HTML previews in a `sandbox` iframe and served with `CSP: sandbox` and `nosniff` | No Content-Security-Policy nor `X-Frame-Options` on the UI pages |
| **Evidence at rest** | Host or backup theft; leftovers after deletion | Owner-only permissions (`0700`/`0600`); retention with `secure_delete`, `VACUUM` and WAL checkpoint (#35); **optional application encryption** of screenshots, traces, videos and DOM (AES-256-GCM, #42); volume encryption (operator) | The database itself is not encrypted by the application (see residual risks) |
| **Screenshots content** | Sensitive fields captured | Client-side masking before the screenshot in the four clients; server policy `TRACEREPORTS_SCREENSHOTS=failures/off` (#45) | Masking depends on selectors |
| **Secrets inside the evidence** | Tokens and passwords stored or forwarded | Server-side redaction before storing (headers, keys, patterns), on by default | Catches known shapes, not every personal datum |
| **AI providers** | Evidence leaving the network; prompt injection from test output | Default **local rules engine, no network** (#43); local LLM profile (#44); provider allowlist; outbound destinations logged at startup; the diagnosis is advisory text and never executes actions | Cloud providers receive the redacted evidence; see Settings gap |
| **Notifications** (Teams, Slack) | Test names and errors in a chat with a wider audience | `NOTIFY_DETAIL=minimal`: counts and a link only (#40) | Full detail by default |
| **Trackers** (GitHub, Jira, Azure DevOps) | Evidence copied into another system | Created only by an explicit admin action (which includes any UI user), never automatically | The failure screenshot is attached: the tracker inherits it |
| **FixLab Request Lab** (UI, `repro`, MCP) | The server or a laptop used to reach internal hosts (SSRF) | From the UI on a shared server, only hosts recorded for the test plus `TRACEREPORTS_LAB_HOSTS`; link-local addresses always blocked; `TRACEREPORTS_LAB=off` disables it | The recorded hosts come from ingested data (anyone with the ingest token decides them); from the server's own machine, `repro` and the MCP tools there is no host restriction |
| **MCP server** (`tracereports mcp`) | An agent with too much power | Local over stdio, no network listener | It authenticates with the admin token, also for read-only calls |
| **Exports and static reports** (ZIP, `tracereports report`) | Evidence leaving the server | Exports require read access; the ZIP is **decrypted** by design (whoever exports could already see it) | `tracereports report` writes its files for the user only (`0700`/`0600`); once copied or published, treat its output as the evidence itself |
| **Client offline recordings and mirror** | Evidence on the CI disk | Replay is limited to ingest endpoints; folders `0700` and files `0600` (Linux, macOS) in the four clients | Recordings are written **before** server-side redaction |
| **Supply chain** | Tampered binary or image; vulnerable dependency | Signed releases (cosign keyless), SBOM and SLSA provenance (#36); govulncheck and CodeQL on every pull request; Scorecard weekly and on `main`; third-party actions pinned by commit SHA (GitHub's own `actions/*` by version tag); single static binary, no CGO | |
| **Container** | Escape or persistence after a compromise | Non-root distroless image, read-only root filesystem, `cap_drop: ALL`, `no-new-privileges` (hardened compose) | |

## Residual risks

Known and accepted, or left to the operator. The hardening guide and `doctor --security` point at them.

1. **A token without UI login leaves reads open.** Set the UI login, or restrict the network with
   `TRACEREPORTS_ALLOWED_CIDRS` / a firewall.
2. **No roles.** The ingest token reads everything; every UI user administers. A leaked CI token is a full
   read of the evidence.
3. **AI endpoint redirection.** An admin can change the AI `base_url` and reuse the stored key. Lock
   Settings (`TRACEREPORTS_SETTINGS_LOCKED=1`) on shared servers.
4. **No general rate limiting nor disk quota.** Failed logins are throttled and the live stream is
   capped, but a client with a valid token can still flood the server: put a proxy with limits in front of
   a shared server.
5. **Styles in the UI allow inline attributes** (`style-src 'unsafe-inline'`); scripts do not.
2. **Coarse roles.** The ingest token reads everything (CI downloads the ZIP with it): a leaked CI token is
   a full read of the evidence. Give the team the read-only login, not the admin one.
3. **Admins choose the AI endpoint.** The stored key cannot be redirected, but an admin may configure a new
   URL with a key they type. Lock Settings (`TRACEREPORTS_SETTINGS_LOCKED=1`) on shared servers.
4. **No rate limiting or brute-force protection**, no disk quota, no SSE subscriber limit: put a proxy
   with limits in front of a shared server.
5. **No Content-Security-Policy on the UI**, and ingest endpoints accept cross-site posts in local mode.
6. **The SQLite database is not encrypted by the application.** Network captures, errors and steps rely on
   volume encryption. The #41 spike measured a pure-Go encrypted SQLite (`ncruces/go-sqlite3` with the
   `xts` or `adiantum` VFS): `xts` cost +57 % ingest time on Linux and 3-4× slower queries, more than the
   accepted 30 %. Those VFS also **do not authenticate pages** (XTS and Adiantum are length-preserving: a
   modified page decrypts to garbage instead of failing) and are not certified implementations. If a later
   version adopts one, this caveat applies.
7. **Plaintext left on disk.** Rewriting evidence (`tracereports evidence encrypt`) or deleting it cannot
   guarantee that old blocks are wiped on SSDs or copy-on-write filesystems. Mitigation: encrypted volume.
8. **Files outside the server.** Static reports and client recordings are private to their user, but
   recordings are not redacted yet and CI artifacts follow the CI's own access rules. Keep them in private
   workspaces and artifacts.
9. **Lab and MCP targets come from ingested data**, and are unrestricted from the server's own machine.
10. **Key management is the operator's.** Losing `TRACEREPORTS_DB_KEY` loses the encrypted evidence;
    leaking it with a backup exposes it. The server refuses a key file inside `DATA_DIR`, nothing more.
11. **HTTP Basic without TLS.** The server does not terminate TLS; a proxy must.
12. **Client-side masking depends on selectors**; the screenshot policy is the backstop.
13. **One maintainer.** Response times depend on one person; see [GOVERNANCE.md](../../GOVERNANCE.md).
14. **Pre-1.0.** The REST API may still change; see the [API stability policy](api-stability.md).

## Review

Changes that add a surface (an endpoint, an outbound destination, a new kind of stored evidence) must
update this document in the same pull request. A reviewer other than the author checks it against the
code before each minor release.
