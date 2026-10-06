# Without a server: record, report and upload later

🌐 **English** · [Español](../es/offline.md)

If the server is missing, does not answer or rejects the token (say, someone forgot the key in
CI), the clients **do not lose the evidence**: they record it in a folder. With it you can:

- **build the HTML report without a server**: `tracereports report <folder>`. It is the same folder
  as "Export ZIP": open it with a double click, no internet, with incidents, timeline, network,
  cURL, locators and replay;
- **upload it to the server later**: `tracereports push <folder>`, once you have the right token.

The same `report` command also builds the report from **JUnit XML**, without a server:

```bash
tracereports report results.xml -o report/              # one file
tracereports report target/surefire-reports -o report/  # a folder of *.xml (one run)
```

## How it works

1. On start, the client tries to create the run on the server. If it can't (server down, no
   connection, `401` because of a wrong token), it says so and switches to **recording**: it writes
   the same API calls it would have made into the folder, with their real time. Tests never break.
2. At the end, if the `tracereports` binary is installed (in the `PATH` or `TRACEREPORTS_BIN`), the
   client builds the report by itself in `<folder>/report/index.html`. Otherwise it prints the command.
3. `report` replays the recording into an in-memory server (same code, same secret masking) and
   exports the report. `push` replays it against a real server.

Pushing the same recording twice duplicates nothing: each event carries an `Idempotency-Key`
derived from the recording. A run left open (the process was cut) is closed as incomplete, at the
time of its last event.

## Configuration (every client)

| Variable | Default | What it does |
| --- | --- | --- |
| `TRACEREPORTS_OFFLINE` | `auto` | `auto`: record only if the run could not be created. `always`: record without trying a server. `off`: never record (previous behavior) |
| `TRACEREPORTS_OFFLINE_DIR` | `./tracereports-offline/<date-time>-<id>` | Recording folder. Without it, a new folder per session |
| `TRACEREPORTS_BIN` | the `tracereports` in the `PATH` | Binary the client uses to build the report at the end |
| `TRACEREPORTS_OFFLINE_REPORT` | `1` | `0`: do not build the report at the end (record only) |

Add `tracereports-offline/` to your `.gitignore`: a recording may carry screenshots and test data.

Per client:

- **pytest**: `--tracereports-offline DIR` always records into `DIR`. With pytest-xdist, the
  controller and each worker write their own file in the same folder and the report joins them.
  With `--tracereports-strict`, falling back to recording fails the session (the evidence did not
  reach the server). API: `TraceReports(offline_dir=..., offline="always")`, `cr.recording`,
  `cr.offline_dir`, `cr.offline_report`.
- **Playwright Test (JS)**: reporter options `offlineDir` and `offline`; API:
  `new TraceReports({ offlineDir, offline })`, `cr.recording`, `cr.offlineDir`, `cr.offlineReport`.
- **Java**: `-Dtracereports.offline=always`, `-Dtracereports.offlineDir=...` (or the variables);
  `cr.recording()`, `cr.offlineDir()`, `cr.offlineReport()`.
- **Go**: `Client.Offline`, `Client.OfflineDir`, `c.Recording()`, `c.RecordingDir()`,
  `c.OfflineReport`.

With CI shards (`TRACEREPORTS_RUN_ID`) and no server, the run id is negative (local): every shard
must use the same `TRACEREPORTS_OFFLINE_DIR`.

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
(screenshots). Each line is one API call:

```json
{"seq": 3, "ts": 1791319221317, "method": "POST", "path": "/api/v1/tests/-2846400002/logs",
 "content_type": "application/json", "body": {"status": "INFO", "message": "open login"}}
```

Negative ids are local; the call that creates a run or a test declares its own in `local_id`. The
time (`ts`) travels as the `X-TraceReports-Timestamp` header (see the [API](api.md)).
