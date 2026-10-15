# Demo, playground and selftest

🌐 **English** · [Español](../es/demo.md)

> **First time?** The [quickstart](quickstart.md) starts the demo step by step in 5 minutes.

Three ways to see TraceReports working without wiring it into your own project first. They all use
the same **scenarios**: runs recorded with the real clients (pytest + Playwright, Playwright Test,
Selenium + JUnit 5, `go test`, the REST API, a JUnit XML import and an Allure import) against a small
demo shop.

| | What it is | For whom |
| --- | --- | --- |
| [`tracereports demo`](#tracereports-demo) | The server with sample data and the demo shop, in a minute | Anyone who wants to try it |
| [Playground](#the-playground) | The shop's page: send each client's scenarios and see what TraceReports shows | Anyone who wants to see what each client sends |
| [`tracereports selftest`](#tracereports-selftest) | Plays every scenario and checks the results, exit code 1 on failure | Whoever deploys or upgrades TraceReports |

## `tracereports demo`

```bash
tracereports demo          # with the released binary
go run ./cmd demo          # from a clone of the repository
```

It prints two addresses and opens the report in the browser:

```text
  TraceReports demo · sample data ready (32 runs, 64 tests, 11 failed)

  ▸ Report:      http://localhost:8080/#run=32&view=tests
  ▸ Playground:  http://localhost:8180
```

**What you get:** a team that tests the shop with every client. There are 12 nights of history
(a flaky test, failures that were already fixed) and tonight's runs, when the orders backend broke.
Every run is a real recording, so you see exactly what each client sends: steps, screenshots, the
browser's network with the password masked, the console, the command to run each test and the
suggested selector when one changed.

### Suggested tour

1. The latest run opens by itself: 2 of 3 tests failed. Open `test_checkout_pays_the_order`:
   steps, the browser's screenshot, network (password masked), console and the command to run it.
2. `test_add_to_cart_with_the_old_selector` failed because a selector changed: TraceReports suggests
   the new one.
3. **Network** tab → the `POST /api/orders` 500 → **Open in Lab** → password `demo` → **Send**: the
   verdict is *Reproduced*.
4. In the playground, switch the orders backend to working and send again: *No longer fails*.
   **Save as a check**, then on the incident press **Verify**: *Verified: fixed*.
5. **Search**, **Metrics** and **Release**: the history, the flaky test and the failures already
   fixed.
6. In a terminal, `tracereports repro` / `verify` with the command the Lab copies, or
   `tracereports mcp` for Claude Code (`TRACEREPORTS_URL=http://localhost:8080`). See
   [FixLab](fixlab.md).

### Options

| Flag | Default | What it does |
| --- | --- | --- |
| `-port` | first free port from 8080 | Port of the report |
| `-shop-port` | first free port from 8180 | Port of the demo shop and the playground |
| `-keep` | off | Keep the sample data when the demo stops (it prints the folder; `DATA_DIR=<folder> tracereports` opens it again) |
| `-no-open` | off | Do not open the browser |
| `-ai=false` | AI on when there is a key | Turn off the AI diagnosis. By default the demo uses the AI provider of your environment or `.env` (`AI_PROVIDER` + `AI_API_KEY`, `GEMINI_API_KEY`…) when it finds one, and that **spends tokens** |

### Isolated from your configuration

- It listens on `127.0.0.1` only: nothing is reachable from other machines and Windows does not
  show a firewall prompt.
- It ignores every `TRACEREPORTS_*` variable, Teams/Slack webhooks, `PUBLIC_URL` and trackers. Of
  your environment and the `.env` of the folder where you run it, it only takes the AI provider
  (the guide it prints says whether the AI is on); `-ai=false` leaves it off.
- The database lives in a temporary folder and is deleted with Ctrl+C. Folders left by a demo that
  was killed are cleaned up on the next start (after 12 hours).

## The playground

The demo shop's home page (<http://localhost:8180>). Everything else in the shop (`/login`,
`/products`, `/cart`…) is the app the scenarios were recorded against.

- **Scenarios.** One card per scenario, filterable by client and by kind (OK, NOK, flaky, suite).
  **Send** plays the recording against TraceReports with fresh timestamps and shows the requests it
  sent, what it checked (statuses, incident, network, masked password, screenshot, suggested
  selector, Lab…) and a link to the run.
- **Run all and verify.** Sends every scenario with its checks, like `tracereports selftest`.
- **Shop backend.** Switches to break or fix the shop while you try the Lab by hand:

  | Switch | Effect |
  | --- | --- |
  | Orders | `POST /api/orders` answers 500 (connection pool exhausted) |
  | Products | `GET /api/products` takes 3 s |
  | Recommendations | `GET /api/recommendations` fails every other call (503) |

  A scenario sets the switches it needs while it plays and then restores yours.
- **Terminal.** Logs each scenario you send, with its result.
- Spanish and English, keyboard accessible, and it respects *reduce motion*. Its animations use
  [Motion](https://motion.dev) (MIT), bundled in the binary: it works offline.

### Scenarios

| Id | Client | Kind |
| --- | --- | --- |
| `rest-ok` / `rest-nok-backend` | REST API | passes / backend answers 500 |
| `pytest-ok` / `pytest-nok-backend` / `pytest-nok-selector` | pytest + Playwright | sign in / checkout fails (500) / a selector changed |
| `playwright-ok` / `playwright-nok-backend` / `playwright-flaky` | Playwright Test (JS) | sign in / checkout fails (500) / passes on the retry |
| `selenium-ok` / `selenium-nok-selector` | Selenium + JUnit 5 (Java) | sign in / a selector changed |
| `go-ok` / `go-nok-backend` | Go (`go test`) | API passes / API answers 500 |
| `junit-import` | JUnit XML (Maven Surefire) | passed, failed and skipped tests, no client |
| `allure-import` | Allure (pytest + allure-pytest) | the allure-results of a suite that only uses Allure: steps, screenshots and each failure diagnosed. See [Allure](allure.md) |
| `pytest-suite`, `playwright-suite`, `selenium-suite`, `go-suite` | each client | the whole suite in one run |

## `tracereports selftest`

```bash
tracereports selftest                                        # a temporary server on this machine
tracereports selftest -url https://tracereports.acme.com     # a deployed one
tracereports selftest -list                                  # list the scenarios
tracereports selftest -only pytest-nok-backend,go-ok         # some of them
```

It plays every scenario against the demo shop and checks what TraceReports shows: the statuses,
the incident, the network, that the password arrives masked, the screenshots, the suggested
selector, the flaky test, the command to run each test and that the Lab reproduces the backend
failure. It exits with code **1** when an expectation fails, so it works after an upgrade or as a
CI step:

```yaml
- name: TraceReports still shows what the clients send
  run: tracereports selftest -url ${{ vars.TRACEREPORTS_URL }}
  env:
    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
```

With `-url` it sends `TRACEREPORTS_TOKEN` when it is set (a protected server), and the scenario runs
stay on that server (project `demo-shop`). The shop runs on the machine that runs the selftest and
the Lab is tried from there, so the server does not need to reach it. The checks need the default
masking: with `TRACEREPORTS_REDACT=off` the Lab has no variables to rebuild and those checks fail.

## How the scenarios are made

The catalog and its expectations are in
[`internal/demo/scenarios.go`](../../internal/demo/scenarios.go) and the recordings in
[`internal/demo/scenarios/`](../../internal/demo/scenarios), embedded in the binary.
`go test ./internal/demo` plays them in CI, so a change that breaks the path from a client to the
report fails there. To record them again or add one, see
[test/scenarios](../../test/scenarios/README.md).

## Troubleshooting

- **A port is busy** (on Windows, Hyper-V reserves some ranges): the demo picks the next free one;
  `-port` / `-shop-port` choose them.
- **The browser did not open:** open the *Report* address it printed.
- **The Lab says "session missing"** in the demo: the shop password is `demo`.
- **I want to keep exploring tomorrow:** `tracereports demo -keep`, then
  `DATA_DIR=<folder> tracereports` with the folder it printed.
