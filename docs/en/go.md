# Go client and playwright-go

🌐 **English** · [Español](../es/go.md)

The client lives in [`client/go`](../../client/go) (package `tracereports`, standard library only).

```bash
go get github.com/josemiguellopez/tracereports/client/go@latest
```

```go
import tracereports "github.com/josemiguellopez/tracereports/client/go"
```

> It is a separate module inside the repository: its versions are published with `client/go/vX.Y.Z`
> tags (without a tag, `@latest` takes the latest commit of the default branch). The
> [`examples/playwright-go`](../../examples/playwright-go) example uses a `replace` to `../../client/go`
> so it builds against the code of the same checkout; your project does not need it.

Like the Python one, the client is *best effort*: it uses short timeouts, retries failed sends
twice (no connection, 5xx, 408, 429) with the same `Idempotency-Key` —so a retry never duplicates
a step— and, after 3 connection failures in a row, stops trying for 30 s instead of waiting the
timeout in every test. Unlike the Python one, sends are synchronous (no background queue). A nil
`*tracereports.Test` is safe to use, so if the server does not answer your tests keep running.

It reads `TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`, `TRACEREPORTS_DISABLED`, `TRACEREPORTS_PROJECT`, `TRACEREPORTS_RUN_ID`
(join an existing run) and the branch and commit from `TRACEREPORTS_BRANCH` / `TRACEREPORTS_COMMIT`, the
most common CI variables or `git`.

## API

```go
c := tracereports.New("")                                  // $TRACEREPORTS_URL or http://localhost:8080
c.StartRun("Web regression", "staging")

// stable identity: package + t.Name() (includes subtests). History follows the key, not the name.
t, _ := c.StartTestWithKey(tracereports.Key("shop/login", "TestLoginOK"), "Valid login", "login, smoke", "The admin reaches the Dashboard")
t.Info("Open the login page")
png, _ := page.Screenshot()
t.Screenshot(png, "Login form", tracereports.Info)
t.Pass("User logged in")
t.Network(connections)                                // optional: Network tab (before Finish)
t.Finish(tracereports.Pass, "", "")                        // or tracereports.Fail, "message", "trace"

c.FinishRun()
fmt.Println(c.ReportURL())
```

| Method | Description |
|---|---|
| `New(url)` | Client (`Token` and `HTTP` can be adjusted) |
| `StartRun(name, env)` / `FinishRun()` | Opens and closes the run (the client's `Project`, `Branch` and `Commit` define its context) |
| `FinishRunInterrupted()` | Closes the run as incomplete (cancelled suite) |
| `StartTestWithKey(key, name, category, description)` | Starts a test with a stable identity (`tracereports.Key(package, t.Name())`) |
| `StartTest(name, category, description)` | Same, identified by its name (approximate history) |
| `Test.Log(status, msg)`, `Info`, `Pass`, `Fail`, `Warn` | Steps |
| `Test.Screenshot(png, msg, status)` | Screenshot as a step |
| `Test.Network([]tracereports.Conn)` | The test's network calls |
| `Test.DOM(snapshot)` | On failure: page snapshot (see `domScript` in `examples/playwright-go`) used to suggest selectors |
| `Test.Finish(status, errorMessage, errorTrace)` | Closes the test. `FAIL` triggers the AI |
| `Test.FinishAttempts(status, msg, trace, attempts)` | Closes a retried test: a `PASS` with `attempts` > 1 shows as *passed after retry* |
| `Conn.Expected` | Marks a negative response the test expects (not counted as an error nor as a cause) |

## playwright-go example

[`examples/playwright-go`](../../examples/playwright-go) contains idiomatic tests (`go test`)
against OrangeHRM, with screenshots, network capture and failure reporting.

```bash
cd examples/playwright-go
go test -v ./...                          # downloads the driver and Chromium the first time
BROWSER_CHANNEL=chrome go test -v ./...   # uses your installed Chrome
HEADLESS=0 go test -v ./...               # watch the browser
```

Pieces of the example:

- `network.go`: network capture for a page (`OnRequest`, `OnResponse`, `OnRequestFinished`,
  `OnRequestFailed`) with sensitive-data masking. The body is read in `RequestFinished`, inside the
  event, because it is the only moment playwright-go can read it reliably.
- `dom.go`: the page snapshot sent on failure.
- `orangehrm_test.go`: `TestMain` opens the browser and the run. The `run(...)` helper creates a
  page per test, reports steps and screenshots and, when done, sends the network and the result,
  with an automatic screenshot if the test failed.

> Calls still in flight when the test navigates to another page have no body available: that is a
> browser limitation, not the client's.
