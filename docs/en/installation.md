# Installation

🌐 **English** · [Español](../es/installation.md)

TraceReports is a single binary: the web UI is embedded and data lives in a local SQLite
database. No external database, Node or CGO required.

## Option 1: Docker (recommended for servers)

```bash
git clone https://github.com/josemiguellopez/tracereports.git && cd tracereports
cp -n .env.example .env          # optional: token, AI, notifications
docker compose up -d
```

- UI: <http://localhost:8080>
- Data: the `tracereports-data` volume (database + screenshots). Backup: see [Backup and restore](#backup-and-restore).
- Full guide (day to day, importing data, server with HTTPS, troubleshooting): [Docker](docker.md).
- Logs: `docker compose logs -f`

Without compose:

```bash
docker build -t tracereports .
docker run -d -p 8080:8080 -v tracereports-data:/data --env-file .env tracereports
```

The image runs as an unprivileged user on a *distroless* base (no shell).

## Option 2: binary with Go

Requires Go 1.26 or later.

```bash
go build -o tracereports ./cmd        # Windows: -o tracereports.exe
./tracereports                        # listens on :8080 and stores data in ./data
```

If that folder has a `.env` file, the server reads it on startup (`cp -n .env.example .env`, which does not replace an existing `.env`).

For another OS: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tracereports ./cmd`.

During development you can use `go run ./cmd`. The UI is compiled into the binary, so restart the
server after changing files in `web/`.

## Try it with sample data

```bash
tracereports demo        # from the repository: go run ./cmd demo
```

It starts TraceReports with a temporary database and opens it in the browser: a team that tests a shop from
Python (Playwright and an API suite), JavaScript (Playwright), Java (Selenium + JUnit 5), Go and a Maven JUnit
XML import, with 12 nights of history, failures grouped into incidents, a broken selector, a flaky test, network
and screenshots. It also starts a **demo shop** (its page has a button to break or fix the backend) so you can
try the Lab end to end: resend the failed `POST /api/orders` (password `demo`), fix the shop, verify.

- Only on this machine (`127.0.0.1`) and isolated from your configuration: no tokens, Teams/Slack, tickets nor
  `.env`. The AI is off unless you pass `-ai` (it uses the provider of your environment and spends tokens).
- The data is deleted when you stop it with Ctrl+C; `-keep` keeps it. `-port` and `-shop-port` choose the ports
  (by default the first free ones from 8080 and 8180); `-no-open` does not open the browser.

## Check that it works: `tracereports selftest`

```bash
tracereports selftest                          # a temporary server on this machine
tracereports selftest -url https://tracereports.acme.com   # a deployed one (it leaves the scenario runs there)
```

It plays 13 scenarios **recorded with the real clients** (pytest + Playwright, Playwright Test, Selenium + JUnit 5,
`go test`, the REST API and a JUnit XML import), passing and failing, against the demo shop, and checks what
TraceReports shows: the statuses, the incident, the network, that the password arrives masked, the screenshots, the
suggested selector, the flaky test, the command to run each test and that the Lab reproduces the backend failure.
It exits with code 1 when an expectation fails, so it works after an upgrade or as a CI step. `-list` shows the
scenarios and `-only` runs some of them. How they are recorded: [test/scenarios](../../test/scenarios/README.md).

## First run

1. Open <http://localhost:8080>. With no runs you will see an empty screen.
2. Run an example:

   ```bash
   pip install pytest pytest-playwright ./client/python
   playwright install chromium        # or, with Chrome installed: --browser-channel chrome
   pytest examples/pytest-playwright --tracereports
   ```

3. When it finishes, the console prints the link to the run.

## Data

| Path | Contents |
|---|---|
| `DATA_DIR/tracereports.db` | Runs, tests, steps, network, diagnoses, settings (SQLite). |
| `DATA_DIR/screenshots/` | Screenshots |

`DATA_DIR` is `./data` by default (`/data` in Docker). To start from scratch, stop the server and
delete that folder. New tables of each version are created or migrated automatically on startup.

## Backup and restore

The database is SQLite in WAL mode: while the server runs, part of the latest data lives in
`tracereports.db-wal`. **Do not copy only the `.db` file while the server is running**: the copy may
miss the most recent data. Use SQLite's online backup, which is consistent even while the server
keeps receiving results, and then copy the screenshots (in that order, so no referenced screenshot
is missing).

**Docker** (the volume is `tracereports-data`; if you use another one, find it with `docker volume ls`):

```bash
docker volume create tracereports-backup
docker run --rm -v tracereports-data:/data -v tracereports-backup:/backup alpine sh -c \
  "apk add -q sqlite && sqlite3 /data/tracereports.db '.backup /backup/tracereports.db' \
   && cp -a /data/screenshots /backup/ && chown -R 65532:65532 /backup"
```

The `chown` gives the files to the image's unprivileged user, which could
otherwise read them but not write to them.

**Restore**: start the server on the backup volume (or copy its contents to a new volume), e.g.
`docker run -v tracereports-backup:/data ... tracereports`. Before restoring in production, check
the copy: `sqlite3 tracereports.db 'PRAGMA integrity_check'` must answer `ok`.

**Without Docker**: `sqlite3 data/tracereports.db '.backup backup.db'` and copy `data/screenshots/`;
or stop the server and copy the whole `data` folder (including `-wal` and `-shm`).

## Next step

- [Configuration](configuration.md): security, AI and notifications.
- [Python](python.md) · [Go](go.md) · [REST API](api.md)
