# Example: Go + playwright-go

🌐 **English** · [Español](README.es.md)

Idiomatic Go tests (`go test`) against the OrangeHRM demo, with steps, screenshots, backend network
capture and failure reporting.

```bash
cd examples/playwright-go
go test -v ./...                          # downloads the driver and Chromium the first time
BROWSER_CHANNEL=chrome go test -v ./...   # uses your installed Chrome
HEADLESS=0 go test -v ./...               # watch the browser
```

- `orangehrm_test.go`: the tests and the `run(...)` helper, which opens a page per test and
  reports everything to TraceReports.
- `network.go`: the page's network capture, with sensitive-data masking.
- `dom.go`: the page snapshot sent on failure (used to suggest selectors).

Client details: [docs/en/go.md](../../docs/en/go.md).
