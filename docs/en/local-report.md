# Local report on your PC

🌐 **English** · [Español](../es/local-report.md)

When you run your tests from your own PC, TraceReports can give you **two reports of the same run**:

- **on the server**, as always, for the whole team;
- **on your PC**, an `index.html` of that run that opens with a double click, with no server and no
  internet: steps, screenshots, network, replay, the **Escalate** summary and the **Release**
  decision.

The local report also shows up if you **forgot the token**, if the server is **down** or if it went
down **halfway** through the suite: the evidence of your run is never lost.

> This guide is for whoever runs the tests. The technical details (recording format, shards,
> `push --force`) are in [Without a server: record, report and upload later](offline.md).

## 1. Set it up in 2 minutes

Add these lines to the `.env` of your test project (the same one with `TRACEREPORTS_URL` and
`TRACEREPORTS_TOKEN`). The Python, JavaScript, Java and Go clients read it by themselves:

```dotenv
# send to the server and also keep a local copy of every run
TRACEREPORTS_OFFLINE=both
# folder where each run creates its own (relative to where you start the tests)
TRACEREPORTS_OFFLINE_BASE=output/tracereports
```

| Variable | What it does |
| --- | --- |
| `TRACEREPORTS_OFFLINE=both` | Sends to the server **and** keeps the local copy. With the default (`auto`) it records locally only if the server does not answer or rejects the token |
| `TRACEREPORTS_OFFLINE_BASE` | Where the folder of each run is created. It is created if it does not exist |
| `TRACEREPORTS_BIN` | Optional: path of the `tracereports` binary (see step 2) |
| `TRACEREPORTS_OFFLINE_KEEP=1` | Optional: also keep the raw recording when everything arrived |

Do not use `TRACEREPORTS_OFFLINE_DIR` in an everyday `.env`: it is a **fixed** folder meant to be
shared by the processes of one run (workers, shards), and every run would end up writing into the
same place.

## 2. The binary that builds the HTML

The HTML is built by the `tracereports` binary with the **same code as the server** (masking of
passwords and tokens, incidents, timeline…), so it matches what the server exports. The clients look
for it in this order:

1. `TRACEREPORTS_BIN` from the `.env` or the environment;
2. the `PATH`;
3. your user's cache;
4. **automatic download** from GitHub Releases, once, checking its SHA-256.

With the automatic download there is nothing to do. If you prefer to install it yourself (or have
no internet), download it from [Releases](https://github.com/josemiguellopez/tracereports/releases):

| System | File | Steps |
| --- | --- | --- |
| Windows | `tracereports_<version>_windows_amd64.zip` | Unzip it and set `TRACEREPORTS_BIN=C:/path/tracereports.exe` |
| macOS Apple Silicon (M1, M2, M3…) | `..._darwin_arm64.tar.gz` | See below |
| macOS Intel | `..._darwin_amd64.tar.gz` | See below |
| Linux | `..._linux_amd64.tar.gz` or `_arm64` | `chmod +x tracereports` and put it in the `PATH` |

On macOS:

```bash
tar -xzf tracereports_*_darwin_arm64.tar.gz
chmod +x tracereports
xattr -d com.apple.quarantine tracereports   # macOS blocks downloaded binaries that are not signed
sudo mv tracereports /usr/local/bin/          # or use TRACEREPORTS_BIN with the path where you keep it
```

With Go and the repository you can also build it: `go build -o tracereports.exe ./cmd`
(on macOS or Linux, `-o tracereports`).

The cache lives in `%LOCALAPPDATA%\tracereports\` (Windows), `~/Library/Caches/tracereports/`
(macOS) or `~/.cache/tracereports/` (Linux). To never download: `TRACEREPORTS_BIN_DOWNLOAD=0`.

### 2.1 Step by step: use your own `tracereports.exe`, without downloading from GitHub

Useful if your network has no access to GitHub, if the version is not published yet or if your team
hands out the binary itself.

1. **Get the binary**, one of these ways:
   - build it from the repository (you need Go):

     ```powershell
     cd C:\dev\tracereports
     go build -o tracereports.exe ./cmd
     ```

   - or copy the `tracereports.exe` your team gives you, or the one from [Releases](https://github.com/josemiguellopez/tracereports/releases).
2. **Keep it in a fixed path**, for example `C:\tools\tracereports\tracereports.exe` (on macOS or
   Linux, `/usr/local/bin/tracereports` or your tools folder).
3. **Check that it works**: `C:\tools\tracereports\tracereports.exe report --help` must print the
   help.
4. **Point to it in your `.env`** and turn the download off:

   ```dotenv
   TRACEREPORTS_OFFLINE=both
   TRACEREPORTS_OFFLINE_BASE=output/tracereports
   TRACEREPORTS_BIN=C:/tools/tracereports/tracereports.exe
   TRACEREPORTS_BIN_DOWNLOAD=0
   ```

   Use `/` in the path, as in the example.
5. **Run your tests** as usual. At the end, the console shows the path of that run's
   `report/index.html`.
6. **To build by hand** a folder that was left without HTML:

   ```powershell
   C:\tools\tracereports\tracereports.exe report output\tracereports\<folder> -o output\tracereports\<folder>\report
   ```

When you update TraceReports, replace the binary with the one of the same version too.

## 3. What stays in your folder

Each run creates its own folder with the **run name**, the date-time and an id:

```
output/tracereports/
├── orangehrm-pim-20261009-100431-02553c/
│   └── report/
│       └── index.html        ← open it with a double click
└── orangehrm-pim-20261009-101210-7f3a91/
    └── report/index.html
```

So you can start several runs **in parallel** without them clashing, and tell which is which.

| At the end | The folder keeps |
| --- | --- |
| Everything reached the server and the HTML was built | Only `report/` |
| Something did not reach the server (forgotten token, server down) | `report/` **and** the raw recording, to upload it later |
| The HTML could not be built (no binary and no internet) | The raw recording and a warning with the command to build it |
| `TRACEREPORTS_OFFLINE_KEEP=1` | Always `report/` and the raw recording |

**The raw recording (`events-*.jsonl`, `bodies/`) holds the data as the test captured it, without
masking.** The `index.html` is masked. Do not share the raw recording and add the folder to your
`.gitignore`.

At the end, the console (the pytest summary, the Playwright reporter or the Java and Go log) shows
both locations: the URL of the report on the server and the path of the local `index.html`.

## 4. What the local report includes

| Includes | Does not include (needs the server) |
| --- | --- |
| Steps, screenshots, timeline and replay | **Metrics** (they span many runs) |
| Network with *Copy as cURL* and mocks | Sending to **Teams or Slack** and creating **tickets** |
| Diagnosis and suggested locators | Generating or re-analyzing with **AI** (the existing diagnosis is shown) |
| **Escalate**: the summary of the run and of each failed test, for Business, QA and Development, in English and Spanish, ready to copy as text, Markdown, email or an image | Server **Settings** |
| **Release**: that run's decision with its criteria | Comparing with earlier runs |

## 5. Common cases

- **I forgot the token.** The server rejects the run, but the local `index.html` is built anyway.
  When you have the token: `tracereports push <folder>` and the run reaches the server.
- **The server went down halfway through the suite.** The tests go on; the local copy stays
  complete, with its HTML and the raw recording. Upload it later with `push`: it creates a complete
  run, separate from the partial one that made it.
- **Several runs at once.** Each one has its folder (step 3).
- **No internet.** Install the binary by hand (step 2) and set `TRACEREPORTS_BIN_DOWNLOAD=0`.

## 6. Upload it to the server later

```bash
tracereports push output/tracereports/orangehrm-pim-20261009-100431-02553c --token <token>
```

Pushing the same folder twice duplicates nothing. If the copy had already arrived complete, `push`
refuses to duplicate it. If only `report/` is left, there is nothing to upload: it was on the
server already.

## 7. If the `index.html` does not show up

| What you see | Cause | Fix |
| --- | --- | --- |
| Warning *local report unavailable (…404…)* | That binary version is not published on GitHub yet | Use a local binary (step 2.1) |
| Warning about disabled download or no network | `TRACEREPORTS_BIN_DOWNLOAD=0` or no internet | Same as above |
| The folder has `events-*.jsonl` but no `report/` | The binary was not found | Build it by hand: `tracereports report <folder> -o <folder>/report` |
| No folder was created | `TRACEREPORTS_OFFLINE` is not `both` and the server answered | Check the `.env` (step 1) |
| A run waited ~60 s at the end | Another run was downloading the binary, or an interrupted download left its lock | It fixes itself: a lock older than 5 minutes is taken over |

The warnings always explain what happened and the command to fix it. Tests never fail because of
the local report.

## 8. Full example with OrangeHRM

The [`examples/orangehrm`](../../examples/orangehrm) example already keeps its local copy in
`examples/orangehrm/output/tracereports/`, wherever the command is started from.

1. In the `.env` at the root of the repository:

   ```dotenv
   TRACEREPORTS_URL=http://localhost:8080
   TRACEREPORTS_TOKEN=<your token>
   TRACEREPORTS_OFFLINE=both
   ```

2. Run: `python examples/orangehrm/tests/test_orangehrm_pim.py`.
3. At the end, the console shows the server URL and the path of the local report. Open
   `examples/orangehrm/output/tracereports/orangehrm-pim-<date>-<id>/report/index.html`.
4. Try the forgotten-token case: change `TRACEREPORTS_TOKEN` to a wrong one and run again. The
   server rejects the run, but the local report shows up anyway.
