# Without a server: record, report and upload later

🌐 **English** · [Español](../es/offline.md)

If the server is missing, does not answer or rejects the token (say, someone forgot the key in
CI), the clients **do not lose the evidence**: they record it in a folder. With it you can:

- **build the HTML report without a server**: `tracereports report <folder>`. It is the same folder
  as "Export ZIP": open it with a double click, no internet, with incidents, timeline, network,
  cURL, locators, replay, the **Escalate** summaries (template, ready to copy as text or an image)
  and the **Release** decision of that run;
- **upload it to the server later**: `tracereports push <folder>`, once you have the right token.

The same `report` command also builds the report from **JUnit XML** or **Allure**, without a server:

```bash
tracereports report results.xml -o report/              # one JUnit XML file
tracereports report target/surefire-reports -o report/  # a folder of *.xml (one run)
tracereports report allure-results -o report/           # Allure results (or their ZIP)
```

## How it works

1. On start, the client tries to create the run on the server. If it can't (server down, no
   connection, `401` because of a wrong token), it says so and switches to **recording**: it writes
   the same API calls it would have made into the folder, with their real time. Tests never break.
2. At the end, the client builds `<folder>/report/index.html` using `TRACEREPORTS_BIN`, then `PATH`,
   then the verified user cache. If needed, it downloads the pinned **v0.2.0** renderer from
   `josemiguellopez/tracereports` GitHub Releases and verifies the archive against that release's
   `checksums.txt` (SHA-256). This works in `auto`, `always` and `both`, even with a rejected token.
3. `report` replays the recording into an in-memory server (same code, same secret masking) and
   exports the report. `push` replays it against a real server.

Pushing the same recording twice duplicates nothing: each event carries an `Idempotency-Key`
derived from the recording. A run left open (the process was cut) is closed as incomplete, at the
time of its last event; if that close fails, `push` ends with an error: push again and the run is
closed once.

## Configuration (every client)

| Variable | Default | What it does |
| --- | --- | --- |
| `TRACEREPORTS_OFFLINE` | `auto` | `auto`: record only if the run could not be created. `always`: record without trying a server. `both`: send to the server and keep a local copy. `off`: never record (previous behavior) |
| `TRACEREPORTS_OFFLINE_BASE` | `./tracereports-offline` | Folder where **each run creates its own** recording folder (`<run-name>-<YYYYMMDD-HHMMSS>-<6 hex>`). The name is lower-case ASCII, hyphenated and at most 40 characters; runs never clash. Use this one to keep the evidence in your project's `output` |
| `TRACEREPORTS_OFFLINE_DIR` | — | **Exact** folder of the recording, shared by every process of one run (pytest-xdist workers, CI shards). Every run that uses it writes into the same folder: do not set it in a `.env` used run after run |
| `TRACEREPORTS_OFFLINE_NAME` | run name | Optional label for an automatically created folder when a run name is unavailable |
| `TRACEREPORTS_OFFLINE_KEEP` | empty | `1`: always retain the raw events, bodies and ids of a local copy |
| `TRACEREPORTS_BIN` | `PATH`, then cache/download | Explicit binary path; takes priority, including when the configured path fails |
| `TRACEREPORTS_BIN_DOWNLOAD` | `1` | `0`: disable downloads; installed and already cached binaries still work |
| `TRACEREPORTS_BIN_BASE_URL` | GitHub Releases | Local HTTP test server only (loopback); release paths are `/v0.2.0/checksums.txt` and `/v0.2.0/<asset>` |
| `TRACEREPORTS_OFFLINE_REPORT` | `1` | `0`: do not build the report at the end (record only) |

Add `tracereports-offline/` to your `.gitignore`: a recording may carry screenshots and test data.

The cache is `%LOCALAPPDATA%\tracereports\0.2.0\<os>_<arch>\` on Windows,
`${XDG_CACHE_HOME:-~/.cache}/tracereports/0.2.0/<os>_<arch>/` on Linux, and
`~/Library/Caches/tracereports/0.2.0/<os>_<arch>/` on macOS (amd64 or arm64).
All four clients share it, verify the cached executable's saved hash, and coordinate downloads
with an exclusive directory lock. Temporary files are renamed into place only after verification.
Downloading and waiting for another process share a 60-second budget, only at the end of the run.
If a process is killed during installation, remove its `<os>_<arch>.lock` directory only after
checking that no installer is still running; another run can then retry.

Without internet, set `TRACEREPORTS_BIN_DOWNLOAD=0` and install a binary with `report` support
via `TRACEREPORTS_BIN`/`PATH`, or reuse a previously populated cache. The pinned release must be
published before automatic downloading can succeed. If downloading, verification or rendering
fails, tests continue and raw evidence stays intact: one warning explains how to install the
binary and run `tracereports report <folder>`, or `tracereports push <folder>` with the correct
token once the server is available. In `both`, successful delivery plus an existing HTML report
still controls cleanup. The final pytest summary / JS reporter / Java and Go logs show the server
URL and local `index.html` when both exist. Open that file directly; viewing it needs no internet.

Per client:

- **pytest**: `--tracereports-offline DIR` always records into `DIR`. With pytest-xdist, the
  controller and each worker write their own file in the same folder and the report joins them.
  With `--tracereports-strict`, falling back to recording fails the session (the evidence did not
  reach the server). API: `TraceReports(offline_dir=..., offline_base=..., offline="always")`, `cr.recording`,
  `cr.offline_dir`, `cr.offline_report`.
- **Playwright Test (JS)**: reporter options `offlineDir`, `offlineBase` and `offline`; API:
  `new TraceReports({ offlineDir, offlineBase, offlineName, offline })`, `cr.recording`, `cr.offlineDir`, `cr.offlineReport`.
- **Java**: `-Dtracereports.offline=always`, `-Dtracereports.offlineDir=...`, `-Dtracereports.offlineBase=...` (or the variables);
  `cr.recording()`, `cr.offlineDir()`, `cr.offlineReport()`.
- **Go**: `Client.Offline`, `Client.OfflineDir`, `Client.OfflineBase`, `Client.OfflineName`, `c.Recording()`, `c.RecordingDir()`,
  `c.OfflineReport`.

With CI shards (`TRACEREPORTS_RUN_ID`) and no server, the run id is negative (local): every shard
must use the same `TRACEREPORTS_OFFLINE_DIR`.

## Local copy while sending (`both`)

Set `TRACEREPORTS_OFFLINE=both` to keep the evidence even if the server goes down halfway through
the suite. If creating the run fails, it records just like `auto`. Otherwise, each logical call
is sent and recorded once, with negative local ids. Tests started after an outage are recorded
too. The default remains `auto`.

```python
cr = TraceReports(offline="both")
```

JavaScript: `new TraceReports({ offline: "both" })` (also a reporter option). Java:
`-Dtracereports.offline=both`. Go: `c.Offline = "both"`. pytest reads the environment variable.
The report URL continues to point to the server; `offline_report`, `offlineReport`,
`offlineReport()` and `OfflineReport` expose the local HTML separately.

| At the end | Retained files |
| --- | --- |
| Everything delivered and HTML generated | `report/` and the marker with `raw_removed: true` |
| Delivery incomplete or a worker did not confirm closing | HTML when available, plus raw recording |
| Missing binary, report disabled or generation failed | Raw recording and a command to build the report |
| `TRACEREPORTS_OFFLINE_KEEP=1` | HTML and raw recording |

**Raw recordings contain unmasked data.** HTML is generated using the server's secret masking.
Protect the folder and avoid publishing events and `bodies/` as public artifacts.

Cleanup requires every process to finish with an empty queue and zero rejected, dropped, lost
and (in Python) spooled events. The owner must also confirm the run's finish request. Each process
registers its status before recording and updates it on close. Only the owner deletes raw files;
an interrupted process, damaged recording or activity during report generation preserves them.
`TRACEREPORTS_OFFLINE_REPORT=0` also prevents cleanup.

Shards must share `TRACEREPORTS_OFFLINE_DIR` and the real `TRACEREPORTS_RUN_ID`; pytest-xdist
passes the folder to its workers. The owner stores the local run id in `ids/server-<id>`.
A missing mapping produces a warning and prevents that part from being fully reconstructed.
Use a new directory for each session; a directory with `raw_removed` contains only the report.

The marker adds `mirror.server`, `mirror.runs` (`local`/`server` pairs) and `mirror.complete`:

- Complete copy: `push` refuses to duplicate it. `tracereports push --force <folder>` uploads
  retained raw evidence as a separate run; repeating that upload remains idempotent.
- Incomplete copy: `push` warns about the existing partial run and creates a separate complete run.
- `raw_removed: true`: `push` fails even with `--force`; only HTML remains.
- No `mirror`: older recordings behave as before. `report` accepts either kind while raw data exists.

## The binary

It ships in the [releases](https://github.com/josemiguellopez/tracereports/releases/latest) (Linux,
macOS and Windows) and in the Docker image:

```bash
docker run --rm --user "$(id -u):$(id -g)" -v "$PWD:/w" -w /w ghcr.io/josemiguellopez/tracereports report tracereports-offline/<session> -o report
```

`tracereports report --help` and `tracereports push --help` list every option (`--zip`, `--name`,
`--project`, `--ai` to diagnose with the AI configured in the environment, `--url`, `--token`…).
Without `--ai`, `report` makes no network call.

## In CI

```yaml
- name: Tests
  run: pytest --tracereports          # if the server or the token fail, it records
- name: Report (with or without a server)
  if: always()
  run: |
    for d in tracereports-offline/*/; do tracereports report "$d" -o "report/$(basename "$d")"; done
- uses: actions/upload-artifact@v7
  if: always()
  with: { name: tracereports, path: report/ }
```

## Recording format

A folder with `tracereports-offline.json` (`{"format": "tracereports-offline", "version": 1,
"id": …}`), one `events-<pid>-<id>.jsonl` per process and `bodies/` for what is not JSON
(screenshots). Each line is one API call (up to 49 MiB, enough for the largest network request the
server accepts):

```json
{"seq": 3, "ts": 1791319221317, "method": "POST", "path": "/api/v1/tests/-2846400002/logs",
 "content_type": "application/json", "body": {"status": "INFO", "message": "open login"}}
```

Negative ids are local; the call that creates a run or a test declares its own in `local_id`. The
time (`ts`) travels as the `X-TraceReports-Timestamp` header (see the [API](api.md)).
