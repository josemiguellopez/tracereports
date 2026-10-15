# Quickstart: the demo in 5 minutes

🌐 **English** · [Español](../es/quickstart.md)

One command starts everything the demo and the Lab need, on your machine, with nothing else to
install or configure. This page is the step-by-step version; the [demo guide](demo.md) has the
details.

## What runs

`tracereports demo` starts **two local services** in one process:

| Service | Address | What it is |
| --- | --- | --- |
| **TraceReports** (the report) | <http://localhost:8080> | The real server: the web report and its API (`/api/v1/…`), with a temporary database already loaded with runs |
| **Demo shop + playground** | <http://localhost:8180> | A small shop with a backend you can break (`/login`, `/products`, `/cart`, `/api/…`). Its home page is the **playground** |

The runs in the report were recorded with the real clients against that shop, so when the Lab
resends a failed call, it really reaches the shop's backend. Both listen on `127.0.0.1` only (no
firewall prompt, nothing reachable from other machines). If a port is busy, the next free one is
used and printed.

## 1. Get it

Requirements: **Go 1.26+** and Git. Nothing else: no Docker, Node, Python or browsers.

```bash
git clone https://github.com/josemiguellopez/tracereports.git
cd tracereports
go build -o tracereports ./cmd        # Windows: go build -o tracereports.exe ./cmd
```

This builds the `tracereports` binary once (about a minute). On Windows run it as
`.\tracereports.exe` instead of `./tracereports`.

> `tracereports demo` ships with the binaries since 0.3.0. With a downloaded binary, skip the build.

## 2. Optional: AI diagnosis

Without an API key everything works except the AI diagnosis. To have it, put your provider in a
`.env` file at the root of the repository (or in your environment):

```bash
# .env: Gemini has a free tier
AI_PROVIDER=gemini
AI_API_KEY=your-key
```

`GEMINI_API_KEY=…`, `ANTHROPIC_API_KEY=…` or `OPENAI_API_KEY=…` alone also work. The demo takes
**only** these AI variables from the `.env`: no tokens, webhooks or trackers. It spends tokens of
your key (a dozen diagnoses); `-ai=false` turns it off.

## 3. Start it

```bash
./tracereports demo
```

It prints:

```text
Loading the sample data…

  TraceReports demo · sample data ready (32 runs, 64 tests, 11 failed)

  ▸ Report:      http://localhost:8080/#run=32&view=tests
  ▸ Playground:  http://localhost:8180   (run each client's scenarios, break or fix the shop backend)
  …
  AI diagnosis: on (gemini, …), with the key of your environment or .env: it spends tokens. -ai=false turns it off.
  The sample data is deleted when you stop it (-keep to keep it).
  Ctrl+C to stop.
```

and opens the report in your browser. Keep the terminal open: closing it stops both services.

## 4. Try it (5 minutes)

- [ ] **The report.** The latest run is open: 2 of 3 tests failed. Open
      `test_checkout_pays_the_order`: steps, screenshot, network (the password arrives masked),
      console, the command to run it and, with AI, the diagnosis.
- [ ] **The Lab.** **Network** tab → `POST /api/orders` (500) → **Open in Lab** → password `demo` →
      **Send**. Verdict: **Reproduced**.
- [ ] **Fix the backend.** In the [playground](http://localhost:8180), turn the **Orders** switch to
      working. Back in the Lab, **Send** again: **No longer fails**.
- [ ] **Verify.** **Save as a check**, then on the incident press **Verify**: **Verified: fixed**.
- [ ] **The clients.** In the playground, **Run all and verify** sends what each client recorded
      (pytest, Playwright, Selenium, Go, REST, JUnit XML, Allure) and checks what the report shows.

The shop's user is `qa@demo.shop` with the password `demo`, also to open it in the browser.

## 5. From a terminal

With the demo running, in another terminal at the root of the repository:

```bash
# Resend the failed call from your machine (the Lab's "Copy command" gives you the URL)
TRACEREPORTS_LAB_PASSWORD=demo ./tracereports repro 'http://localhost:8080/api/v1/network/<id>/capsule'

# Check that TraceReports shows what every client sends (its own temporary server)
./tracereports selftest

# Let Claude Code read the incidents and verify the fix
claude mcp add tracereports -e TRACEREPORTS_URL=http://localhost:8080 -e TRACEREPORTS_LAB_PASSWORD=demo -- "$PWD/tracereports" mcp
```

On PowerShell set the variable first: `$env:TRACEREPORTS_LAB_PASSWORD="demo"`.

## 6. Stop it

**Ctrl+C** stops both services and deletes the sample data. To keep it:

```bash
./tracereports demo -keep                  # prints the folder when it stops
DATA_DIR=<that folder> ./tracereports      # reopens the report later (only the report, no shop)
```

| Flag | What it does |
| --- | --- |
| `-port 9090` | Port of the report (default: the first free one from 8080) |
| `-shop-port 9190` | Port of the shop and the playground (default: the first free one from 8180) |
| `-no-open` | Do not open the browser |
| `-ai=false` | No AI, even if there is a key |
| `-keep` | Keep the sample data |

## Troubleshooting

| What you see | What to do |
| --- | --- |
| *AI diagnosis is off in the demo* on a failed test | No API key was found: add it to `.env` (step 2) at the root where you run the command, and start the demo again |
| The AI diagnoses arrive late or some say the provider is busy | Free tiers have rate limits; TraceReports retries. Wait a minute or press *Re-analyze* |
| The Lab says **Session missing** | The shop password is `demo` |
| The Lab says **No connection** | The shop is not running (for example, a report reopened with `DATA_DIR`): start `./tracereports demo` |
| A different port than 8080/8180 | One was busy (on Windows, Hyper-V reserves some ranges); use the addresses it printed |
| `go: command not found` | Install Go 1.26+ from <https://go.dev/dl/> |
