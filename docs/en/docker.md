# TraceReports with Docker

🌐 **English** · [Español](../es/docker.md)

How to use TraceReports on your machine with Docker and how to deploy it on a server. To try it
without Docker, see [Installation](installation.md#option-2-binary-with-go).

## How it works

| Piece | What it is |
|---|---|
| **Image** | TraceReports packaged (about 32 MB): the server and the web interface. Built once. |
| **Container** | The image running. It stays up in the background and Docker starts it again when the machine reboots (`restart: unless-stopped`). |
| **Volume** `tracereports-data` | Where the database and screenshots live. It survives stopping, removing or updating the container. |
| **`.env`** | Your configuration (token, login, AI). The container reads it on start. |

`docker-compose.yml` ties the four together: one command builds the image, creates the volume and
starts the container on port 8080. That port is reachable **only from the same machine**
(`127.0.0.1:8080:8080` and `[::1]:8080:8080`, IPv4 and IPv6): nobody else on your network can get in.

## Requirements

- **Windows**: [Docker Desktop](https://www.docker.com/products/docker-desktop/) with WSL 2. If
  Docker Desktop does not start the engine, open PowerShell **as administrator**, run
  `wsl --install --no-distribution` and reboot.
- **Linux**: Docker Engine with the `compose` plugin.
- **macOS**: Docker Desktop.

Check it works with `docker version`: it must show `Client` and `Server`.

## First start

In the project folder:

```bash
cp -n .env.example .env   # PowerShell: if (-not (Test-Path .env)) { Copy-Item .env.example .env }
docker compose up -d
```

> ⚠️ Copy `.env.example` **only if you do not have a `.env` yet**: a `cp` without `-n` replaces
> yours and you lose your keys. The commands above do not overwrite an existing one.

- The first time it builds the image (under a minute); afterwards it starts instantly.
- Open **<http://localhost:8080>**.
- `docker compose ps` must show the container `Up`.

### Minimum `.env`

Configure **token and login on your PC too**. The local-only port keeps people on your network
out, but does not protect you from what runs on your own machine (other programs, a malicious page
open in the browser), and your reports may hold sensitive evidence:

```ini
TRACEREPORTS_TOKEN=a-long-random-token   # tests send it to write
TRACEREPORTS_UI_USER=qa                  # user and password to view the reports
TRACEREPORTS_UI_PASSWORD=a-strong-password
GEMINI_API_KEY=                          # optional: AI diagnosis
```

After changing the `.env`, apply it with `docker compose up -d`. Every variable is in
[Configuration](configuration.md).

If you decide not to configure a login on your PC, the server still protects itself: it only serves
requests with `Host` `localhost`/`127.0.0.1` (defense against *DNS rebinding*). With a login, the
Settings screen is editable too.

> **Settings with Docker:** even if you open `localhost`, the connection comes from Docker's
> network and does not count as "the same machine". To change the AI or the language from the
> Settings screen, configure `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` and log in.

## Connect your tests

Your tests do not change: they point at the server URL and, if you configured a token, send it.

> Your tests take the token from the same project `.env` when it is not set in the terminal: run
> the tests from the project folder (or a subfolder) and nothing else is needed. In CI do not commit
> the `.env`: pass `TRACEREPORTS_TOKEN` as a pipeline *secret*, which takes precedence.
> [Details](configuration.md#the-projects-env-in-the-clients).

```bash
TRACEREPORTS_URL=http://localhost:8080
TRACEREPORTS_TOKEN=a-long-random-token
```

In PowerShell: `$env:TRACEREPORTS_URL="http://localhost:8080"`. On a server, use its HTTPS URL
(`https://reports.your-company.com`). Per language: [Python](python.md),
[JavaScript](javascript.md), [Java](java.md), [Go](go.md).

## Day to day

Always run these in the project folder. You can also start, stop and read the logs from Docker
Desktop, in the **Containers** tab.

| To | Command |
|---|---|
| Check it is running | `docker compose ps` |
| Follow the logs | `docker compose logs -f` |
| Stop it | `docker compose stop` |
| Start it again | `docker compose start` |
| Apply `.env` changes | `docker compose up -d` |
| Update to a new version | `git pull`, then `docker compose up -d --build` |
| Remove the container (data stays) | `docker compose down` |

> ⚠️ `docker compose down -v` **deletes the volume with all the data**. Do not use it unless you
> want to start from scratch.

## Data

- It lives in the volume `tracereports-data` (a fixed name, whatever the project folder is called).
  You can confirm it with `docker volume ls`.
- That volume is **not** the project's `data/` folder. What you reported running the server without
  Docker (`go run`) does not show up in the container unless you import it (below).
- On update, database migrations run automatically on start. Take a backup first.

### Bring the data of an installation without Docker

Do it **when you start using Docker**: step 2 replaces the database in the volume.

```powershell
# 1. Create the volume and stop the container
docker compose up -d
docker compose stop
# 2. Copy the local data folder into the volume (PowerShell, in the project folder)
docker run --rm -v tracereports-data:/data -v "${PWD}\data:/origen:ro" alpine sh -c "rm -f /data/tracereports.db /data/tracereports.db-wal /data/tracereports.db-shm && cp -a /origen/. /data/ && chown -R 65532:65532 /data"
# 3. Start
docker compose start
```

On Linux/macOS, replace `"${PWD}\data:/origen:ro"` with `"$PWD/data:/origen:ro"`. Stop the local
server (`go run`) first so the copy is consistent. The `chown` gives the files to the
unprivileged user the image runs as.

### Backup and restore

While the server runs, **do not copy only the `.db` file**: the latest data lives in the `-wal`
file. Use SQLite's online backup, which is consistent even while results keep arriving:

```bash
docker volume create tracereports-backup
docker run --rm -v tracereports-data:/data -v tracereports-backup:/backup alpine sh -c \
  "apk add -q sqlite && sqlite3 /data/tracereports.db '.backup /backup/tracereports.db' \
   && cp -a /data/screenshots /backup/ && chown -R 65532:65532 /backup"
```

To restore, start the container on the backup volume. Details and the variant without
Docker are in [Installation](installation.md#backup-and-restore).

## Deploy on a server

1. **Token and login are mandatory.** Set `TRACEREPORTS_TOKEN`, `TRACEREPORTS_UI_USER` and
   `TRACEREPORTS_UI_PASSWORD` in the `.env`.
2. **Always behind HTTPS.** HTTP Basic and the token travel in plain text without TLS. Put a
   reverse proxy (Caddy, nginx, Traefik) on the same server. `docker-compose.yml` already publishes
   8080 only locally (`127.0.0.1` and `[::1]`), so the proxy reaches it and nobody can get in
   directly from outside. Do not change it to `"8080:8080"` on a server exposed to the Internet.

3. **Scheduled backup** with the command above (for example with cron or Task Scheduler), and
   optionally `TRACEREPORTS_RETENTION_DAYS` so evidence does not pile up.

If the server has no UI login, add the domain to `TRACEREPORTS_ALLOWED_HOSTS` (or set
`PUBLIC_URL`); otherwise the proxy gets `403 host … is not allowed`. With a login it is not needed.

The proxy must do three things:

- **Body size:** at least 15 MB, the screenshot limit. nginx defaults to 1 MB.
- **No buffering on `/api/v1/stream`:** it is the live mode (SSE). With buffering, the interface
  does not update by itself.
- **Keep the `Host`**, for the CSRF protection.

**Caddy** (automatic Let's Encrypt certificate):

```caddy
reports.your-company.com {
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

**nginx** (the TLS part, `listen 443 ssl` and the certificates, is your server's usual one):

```nginx
server {
    server_name reports.your-company.com;
    client_max_body_size 20m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location /api/v1/stream {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_buffering off;
        proxy_read_timeout 1h;
    }
}
```

Every request that comes through a proxy (with `X-Forwarded-For`, `X-Real-IP` or `Forwarded`)
counts as remote. That is why Settings and the UI actions ask for a token or the login even when
the proxy is on the same machine, also with `TRACEREPORTS_LOCAL_ADMIN=1`.

## Without docker compose

```bash
docker build -t tracereports .
docker run -d --name tracereports -p 127.0.0.1:8080:8080 -p "[::1]:8080:8080" -v tracereports-data:/data --env-file .env \
  --restart unless-stopped tracereports
```

It uses the same `tracereports-data` volume as compose: the data is the same with either.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `docker` is not recognized | Docker Desktop was just installed: open a new terminal (or VS Code). |
| `docker version` shows no `Server` / error 500 | The engine did not start. Open Docker Desktop and wait for *Engine running*. On Windows, if WSL or virtualization is missing: `wsl --install --no-distribution` as administrator and reboot. |
| `port is already allocated` / the container does not start | Port 8080 is taken, for example by a `go run` of the server. Stop it or change the port (`"8081:8080"`). |
| Settings are "read-only" | With Docker the UI login is needed (see above). |
| Tests do not arrive | Check `TRACEREPORTS_URL` and the token. `docker compose logs -f` shows every rejected request. |
| `401 missing or invalid API token` in the tests | The server has a token and the tests cannot find it: run them from the project folder (so they read its `.env`) or set `TRACEREPORTS_TOKEN`. In CI, pass it as a *secret*. |
| `403 host … is not allowed` | Without a UI login only `localhost` is served. If you come in through another name or IP, add it to `TRACEREPORTS_ALLOWED_HOSTS` or configure the login. |
| Every request to `localhost` takes ~2 s, or tests end up "Interrupted" | On Windows, `localhost` is tried over IPv6 first. Publish the port on `[::1]` too (like the project's `docker-compose.yml`) or use `http://127.0.0.1:8080`. |
| Cannot get in from another machine on the network | Expected: the port is local only. To share it, use an HTTPS proxy or switch to `"8080:8080"` with token and login. |
| Permission error after restoring or importing | The `chown -R 65532:65532` on the volume was missing. |
| The interface does not update live behind the proxy | Disable buffering on `/api/v1/stream`. |
| Screenshots rejected behind the proxy (413) | Raise the proxy's body limit (`client_max_body_size` in nginx). |
