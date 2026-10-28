# Security hardening guide

🌐 **English** · [Español](../es/security-hardening.md)

How to get TraceReports ready for an environment with sensitive evidence: what to configure, what the
tool does on its own and what stays on your infrastructure's side. At the end there is a
[checklist](#checklist) to go through before opening it to a team.

TraceReports stores evidence of real runs: steps, screenshots, network calls, the DOM and the errors.
Treat it like any system that stores data from your test environments.

## What comes by default

| Control | Detail |
|---|---|
| Not open to the network | Without a token or login it listens only on this machine; in Docker or with a network `TRACEREPORTS_BIND` it **does not start**. See [Security](configuration.md#security) |
| Diagnosis with no data leaving | Without an AI key the [local engine](configuration.md#local-diagnosis-engine-no-llm) diagnoses: no network, no LLM |
| Masked secrets | Headers, cookies, tokens, passwords and sensitive keys in JSON, forms, URLs and the DOM are masked **before** they are stored. See [Sensitive data](configuration.md#sensitive-data-and-retention) |
| Covered screenshots | The clients cover `input[type=password]` and `[data-tracereports-mask]` before capturing |
| Secure deletion | SQLite `secure_delete` and `VACUUM` after each purge: deleted data is not left in the file |
| Private files | `DATA_DIR` with `0700` permissions; screenshots and artifacts `0600` |
| Minimal image | `distroless/static:nonroot`: no shell, no packages, unprivileged user |
| Verifiable releases | cosign signature, SBOM and provenance. See [Verifying a release](../../SECURITY.md#verifying-a-release) |

## Production profile

A starting `.env` for a shared server:

```ini
# access
TRACEREPORTS_TOKEN=            # openssl rand -hex 32: the admin token
TRACEREPORTS_INGEST_TOKEN=     # another one, restricted, for the clients and CI
TRACEREPORTS_UI_USER=qa
TRACEREPORTS_UI_PASSWORD=      # a long password
TRACEREPORTS_SETTINGS_LOCKED=1 # configuration only from the environment, never from the UI

# evidence
TRACEREPORTS_RETENTION_DAYS=90    # what your data policy allows
TRACEREPORTS_SCREENSHOTS=failures # or off if the screen shows data that cannot be shown
TRACEREPORTS_SCREENSHOT_MASK=     # CSS selectors of your application with sensitive data

# diagnosis with no data leaving: the local engine (default) or an LLM inside your network
# AI_PROVIDER=builtin
# AI_PROVIDER=ollama
# AI_BASE_URL=http://ollama.internal:11434
```

- **Token and ingest token**: clients and CI use `TRACEREPORTS_INGEST_TOKEN` (sends results and reads,
  never administers). `TRACEREPORTS_TOKEN` stays with whoever administers. See
  [Permissions of each credential](configuration.md#permissions-of-each-credential).
- **UI login**: protects the reads. Without it, reads are only accepted with a local `Host` or one from
  `TRACEREPORTS_ALLOWED_HOSTS`.
- **`TRACEREPORTS_SETTINGS_LOCKED=1`**: nobody changes the AI provider or its key from the screen.
- **HTTPS**: put the server behind a TLS proxy (Caddy, nginx, Traefik). HTTP Basic login without TLS
  travels in clear text. See [Docker → Deploy on a server](docker.md#deploy-on-a-server).

## Network

- Publish the port only where needed. The repository's `docker-compose.yml` publishes it only on
  `127.0.0.1` and `::1`, meant for a proxy on the same server.
- Limit who reaches the port with the **firewall** of the server, the cloud or the proxy: only the internal
  network the tests run from and the report is viewed from.
- Without a UI login, `TRACEREPORTS_ALLOWED_HOSTS` lists the names it is reached by; an unknown `Host`
  gets `403` (DNS rebinding protection).

## Hardened Docker

The repository's `docker-compose.yml` already has:

```yaml
read_only: true                 # writes only to /data (the volume) and /tmp
tmpfs:
  - /tmp:size=256m,mode=1777
cap_drop: [ALL]                 # no Linux capabilities
security_opt:
  - no-new-privileges:true      # nothing can gain privileges (setuid)
healthcheck:
  test: ["CMD", "/tracereports", "health"]
```

With `docker run`, the same:

```bash
docker run -d --name tracereports --read-only --tmpfs /tmp:size=256m,mode=1777 --cap-drop ALL \
  --security-opt no-new-privileges:true -p 127.0.0.1:8080:8080 -v tracereports-data:/data \
  --env-file .env ghcr.io/josemiguellopez/tracereports
```

`tracereports health` asks `GET /healthz`, which answers without credentials and without data (it also
works for a load balancer). On Kubernetes: `readOnlyRootFilesystem: true`, `runAsNonRoot: true`,
`allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]` and an `emptyDir` on `/tmp`.

## Volume encryption

The database (SQLite) and the screenshots live in `DATA_DIR`. Encrypt it on the host:

- **Linux**: the volume or disk on LUKS (`cryptsetup`).
- **Windows**: BitLocker on the `DATA_DIR` disk.
- **Cloud**: the provider's encrypted disk (EBS, Azure Managed Disks, Persistent Disk), with keys managed by
  your organization if the policy asks for it.

**Backups** inherit what they store: encrypt them too, and apply the same retention.

## Test data

- Use **synthetic data** in the environments where E2E tests run. No redaction replaces not having the data.
- Do not run E2E tests against **production** with data of real people.
- Mark what is sensitive in your application with `data-tracereports-mask` (or list it in
  `TRACEREPORTS_SCREENSHOT_MASK`) so no screenshot shows it.
- If some data arrives as free text without a recognizable key, add a pattern with
  `TRACEREPORTS_REDACT_PATTERNS`.

## Retention and deletion

- `TRACEREPORTS_RETENTION_DAYS` deletes, every 6 hours, the runs older than N days with their screenshots,
  network, DOM and artifacts. Without the variable nothing is deleted, and the server warns at startup.
- Deleted data is overwritten (`secure_delete`), the database is compacted (`VACUUM`) and the WAL emptied:
  the purged content is not left in the `.db` file.

## Data leaving the server

By default TraceReports sends nothing outside the server. What **you** configure does:

| Integration | What it sends | Recommendation |
|---|---|---|
| Cloud LLM (Gemini, Claude, OpenAI) | The diagnosis prompt, redacted (never screenshots) | Local engine or Ollama if the evidence cannot leave |
| Teams / Slack | Run summary: test names and errors | Do not turn them on in channels where that data must not be |
| Tickets (GitHub, Jira, Azure DevOps) | The escalation created by hand | Internal trackers only |
| `TRACEREPORTS_AI_STATUS_URL` | A request to the provider's status page | Turn it off if you do not use a cloud LLM |

## Verify what you install

Check the signature, SBOM and provenance of the binary or image before installing them:
[Verifying a release](../../SECURITY.md#verifying-a-release).

## Checklist

- [ ] `TRACEREPORTS_TOKEN` and `TRACEREPORTS_INGEST_TOKEN` generated with `openssl rand -hex 32`, different.
- [ ] UI login (`TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD`) and HTTPS on the proxy.
- [ ] `TRACEREPORTS_SETTINGS_LOCKED=1`.
- [ ] Network access limited by firewall to the right internal network.
- [ ] Container with `read_only`, `cap_drop: [ALL]`, `no-new-privileges` and an unprivileged user (the
      official image already is `nonroot`).
- [ ] `DATA_DIR` on an encrypted volume; encrypted backups.
- [ ] `TRACEREPORTS_RETENTION_DAYS` per the data policy.
- [ ] Diagnosis with no data leaving: local engine or Ollama/LLM inside the network.
- [ ] `TRACEREPORTS_SCREENSHOTS` (`failures` or `off`) and `TRACEREPORTS_SCREENSHOT_MASK` with the
      application's sensitive fields.
- [ ] Test environments with synthetic data; no E2E against production with real data.
- [ ] Teams/Slack webhooks only in channels where test names and errors may be.
- [ ] Release signature and SBOM verified.
- [ ] TraceReports added to your organization's asset inventory, with its owner.
