# Scenarios recorded with the real clients

🌐 **English** · [Español](README.es.md)

Each scenario is a **real recording** of a TraceReports client (what pytest, Playwright JS,
Selenium Java or Go really send) running against the demo shop (`internal/demo`), plus what
TraceReports must do with it: statuses, incident, network, masked password, screenshots, suggested
selector, flaky test, the command to run it and that the Lab reproduces it.

The catalog and its expectations are in [`internal/demo/scenarios.go`](../../internal/demo/scenarios.go);
the recordings, in [`internal/demo/scenarios/`](../../internal/demo/scenarios) (embedded in the binary).

## What they are for

- **`tracereports selftest`** plays every scenario against a server (a temporary one or `-url`) and
  checks its expectations; it exits with code 1 if one fails. `TestScenarios`
  (`go test ./internal/demo`) does the same in CI: if a change breaks the path from a client to the
  report, it fails there.
- The demo and the playground use the same recordings. See [Demo, playground and selftest](../../docs/en/demo.md).

## Recording again

When a client changes what it sends, record its scenario again (from the repository root):

```bash
go run ./test/scenarios/record                       # all of them
go run ./test/scenarios/record -only pytest-ok       # one or several, comma separated
go run ./test/scenarios/record -python .venv/Scripts/python
```

The recorder starts the shop on `127.0.0.1:18180`, runs each test with `TRACEREPORTS_OFFLINE=always`
(it never uses a server) and normalizes the recording: the shop's URL becomes `http://__shop__` and the
paths of the machine that recorded it (the repository, the user's folder) are replaced. Then:
`go test ./internal/demo`.

Requirements, one per client:

| Client | Folder | Needs |
|---|---|---|
| pytest + Playwright | `python/` | Python with `pytest pytest-playwright ./client/python` and `playwright install chromium` |
| Playwright Test (JS) | `js/` | Node 18+ and `npm install` in that folder (the browser: `npx playwright install chromium`) |
| Selenium + JUnit 5 | `java/` | Java 17 and Chrome (Selenium Manager brings the driver) |
| Go | `go/` | Go |
| Allure (pytest + allure-pytest) | `allure/` | the Python of pytest + Playwright, plus `allure-pytest` |
| REST and JUnit XML | — | nothing: the recorder builds the API calls |

A new scenario: add it to `Scenarios` with its expectations, write its test in the matching client,
record it with `-only` and run `go test ./internal/demo`.
