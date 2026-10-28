# Threat model

[Español](../es/threat-model.md) · Reviewed for each minor release. Last review: the security hardening
epic (#34).

TraceReports receives evidence from real test runs and shows it to a team. This document says what it
protects, from whom, through which doors, what stops each threat today and what is left to the operator.
It is the reference for the [hardening guide](security-hardening.md) and for `tracereports doctor --security`.

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

## Actors

- **Test clients and CI** (trusted to write): send results with the ingest token.
- **Team members** (trusted to read): browse reports with the UI login.
- **Administrator**: holds `TRACEREPORTS_TOKEN`, changes Settings, runs the CLI on the host.
- **Network attacker**: anyone who reaches the port without credentials (another tenant of the network,
  a compromised workstation, the internet if published by mistake).
- **Malicious or careless insider**: a valid user who exports or forwards more than they should.
- **Compromised third party**: the AI provider, a chat workspace, a tracker or a dependency of the build.
- **Host or backup thief**: gets a copy of `DATA_DIR` or of a backup, without the running process.

## Surfaces and mitigations

| Surface | Threats | Mitigations in place |
| --- | --- | --- |
| **Ingest API** (`POST /api/v1/...`) | Writes without credentials; floods; malicious uploads (zip bombs, fake images) | Token or ingest token required; **fail-closed** when listening on the network without credentials (#37); body limits per endpoint; image/trace/video type checks by content; Allure ZIP limits on files and unzipped size; idempotency keys |
| **UI and read API** | Reading reports without login; DNS rebinding; reaching the server from the wrong network | HTTP Basic login; without login only local or allowed `Host` names; **source IP allowlist** `TRACEREPORTS_ALLOWED_CIDRS` with trusted proxies (#38); `GET /healthz` returns no data |
| **Settings** | Changing the AI provider or key to exfiltrate evidence | Only `TRACEREPORTS_TOKEN` (never the ingest token); `TRACEREPORTS_SETTINGS_LOCKED=1`; `AI_ALLOWED_PROVIDERS` also checked when saving (#39); the AI key saved encrypted |
| **Evidence at rest** | Host or backup theft; leftovers after deletion | Owner-only permissions (`0700`/`0600`); retention with `secure_delete`, `VACUUM` and WAL checkpoint (#35); **optional application encryption** of screenshots, traces, videos and DOM (AES-256-GCM, #42); volume encryption (operator) |
| **Screenshots content** | Sensitive fields captured | Client-side masking before the screenshot in the four clients; server policy `TRACEREPORTS_SCREENSHOTS=failures/off` (#45) |
| **Secrets inside the evidence** | Tokens and passwords stored or forwarded | Redaction before storing (headers, keys, patterns), on by default; secrets never logged |
| **AI providers** | Evidence leaving the network; prompt injection from test output | Default **local rules engine, no network** (#43); local LLM profile (#44); provider allowlist; outbound destinations logged at startup; the diagnosis is advisory text, it never executes actions |
| **Notifications** (Teams, Slack) | Test names and errors in a chat with a wider audience | `NOTIFY_DETAIL=minimal`: counts and a link only (#40) |
| **Trackers** (GitHub, Jira, Azure DevOps) | Evidence copied into another system | Created only by an explicit action of an administrator (admin token or UI actions), never automatically; the failure screenshot is attached, so the tracker inherits it |
| **FixLab Request Lab** | The server used as a proxy to internal hosts (SSRF) | On a shared server it only sends to the hosts the test talked to plus `TRACEREPORTS_LAB_HOSTS`; `TRACEREPORTS_LAB=off` disables it; Lab secrets stay on the machine running `repro` |
| **MCP server** (`tracereports mcp`) | An agent calling tools with too much power | Runs locally over stdio with the user's own credentials; no network listener |
| **Exports** (ZIP, static report) | Evidence leaving the server | Requires read access; the ZIP is **decrypted** by design (whoever exports could already see it) — treat it as the evidence itself |
| **Supply chain** | Tampered binary or image; vulnerable dependency | Signed releases (cosign keyless), SBOM and SLSA provenance (#36); govulncheck, CodeQL and Scorecard on every change; actions pinned by SHA; single static binary, no CGO |
| **Container** | Escape or persistence after a compromise | Non-root image, read-only root filesystem, `cap_drop: ALL`, `no-new-privileges` (hardened compose) |

## Residual risks

These are known and accepted, or left to the operator. Each one is mentioned in the hardening guide.

1. **The SQLite database is not encrypted by the application.** Network captures, errors and steps rely on
   volume encryption. The #41 spike measured a pure-Go encrypted SQLite (`ncruces/go-sqlite3` with the
   `xts` or `adiantum` VFS): `xts` cost +57 % ingest time on Linux and 3-4× slower queries, more than the
   accepted 30 %. Those VFS also **do not authenticate pages** (XTS and Adiantum are length-preserving: a
   modified page decrypts to garbage instead of failing) and are not certified implementations. If a later
   version adopts one, this caveat applies.
2. **Plaintext left on disk.** Rewriting evidence (`tracereports evidence encrypt`) or deleting it cannot
   guarantee that old blocks are wiped on SSDs or copy-on-write filesystems. Mitigation: encrypted volume.
3. **Key management is the operator's.** Losing `TRACEREPORTS_DB_KEY` loses the encrypted evidence;
   leaking it together with a backup exposes it. The server refuses a key file inside `DATA_DIR`, nothing more.
4. **HTTP Basic without TLS.** The server does not terminate TLS; a proxy must. Without it the UI password
   travels in clear.
5. **Client-side masking depends on selectors.** A sensitive field without a selector is captured. The
   screenshot policy is the backstop.
6. **Cloud AI providers see the redacted evidence.** Redaction catches known secret shapes, not every
   personal datum. Use the local engine or a local LLM when the evidence cannot leave.
7. **One maintainer.** Response times depend on one person; see [GOVERNANCE.md](../../GOVERNANCE.md).
8. **Pre-1.0.** The REST API may still change; see the [API stability policy](api-stability.md).

## Review

Changes that add a surface (an endpoint, an outbound destination, a new kind of stored evidence) must
update this document in the same pull request. A reviewer other than the author checks it before each
minor release.
