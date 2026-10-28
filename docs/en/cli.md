# Command line: `tracereports`

🌐 **English** · [Español](../es/cli.md)

One binary does everything: without a command it is the server; with a command it is a tool for your
terminal or your pipeline. `tracereports help` lists the commands, and each one explains its flags
with `-h`.

| Command | What it does | Needs a server |
| --- | --- | --- |
| [`tracereports`](#the-server) | Starts the server and the web report | — |
| [`demo`](#demo) | The server with sample data and a demo shop, only on this machine | no (starts its own) |
| [`report`](#report) | A static HTML report from a recording, JUnit XML or Allure results | no |
| [`push`](#push) | Uploads a recording, allure-results or JUnit XML to a server | yes |
| [`pr-comment`](#pr-comment) | Comments the run summary on the pull request or merge request | optional |
| [`repro`](#repro) | Resends the calls of a FixLab capsule with a fresh session | no (a capsule file or URL) |
| [`verify`](#verify) | Runs the checks of a capsule; exit code 1 if one fails | no (a capsule file or URL) |
| [`mcp`](#mcp) | MCP server (stdio) for Claude Code and other agents | yes |
| [`selftest`](#selftest) | Plays the scenarios recorded with the real clients and checks the results | no (starts its own) |
| [`secrets`](#secrets) | Status and migration of the credentials saved from Settings | no (the data folder) |
| [`evidence`](#evidence) | Status, encryption and key rotation of the evidence encrypted at rest | no (the data folder) |
| [`version`](#version) | Prints the version | no |

**Getting it:** download the binary for your OS from the
[latest release](https://github.com/josemiguellopez/tracereports/releases/latest), or build it from the
repository with `go build -o tracereports ./cmd` (Windows: `tracereports.exe`, run as
`.\tracereports.exe`). `go run ./cmd <command>` also works. With Docker:
`docker run --rm ghcr.io/josemiguellopez/tracereports <command>`.

**Flags** go before or after the inputs (`report rec -o out` = `report -o out rec`) and are written with one
or two dashes (`-ai` = `--ai`). A boolean flag is turned off with `=false` (`-ai=false`).

**Exit code:** `0` when it worked, `1` on an error (and, for `verify` and `selftest`, when a check fails).

## The server

```bash
tracereports
```

Configured with environment variables or a `.env` file in the current folder (`TRACEREPORTS_ENV_FILE`
chooses another one; variables already set win): port, data folder, token, AI, notifications,
masking… See [configuration](configuration.md). Typical ones:

```bash
PORT=8080 DATA_DIR=./data TRACEREPORTS_TOKEN=… GEMINI_API_KEY=… tracereports
```

## demo

```bash
tracereports demo [-port N] [-shop-port N] [-keep] [-no-open] [-ai=false]
```

The server with real runs recorded with every client and a demo shop whose backend you can break and
fix, only on this machine and isolated from your configuration (it only takes the AI provider from
your environment or `.env`). Step by step: [quickstart](quickstart.md); details: [demo](demo.md).

| Flag | Default | |
| --- | --- | --- |
| `-port` | first free from 8080 | Port of the report |
| `-shop-port` | first free from 8180 | Port of the shop and the playground |
| `-keep` | off | Keep the sample data when it stops |
| `-no-open` | off | Do not open the browser |
| `-ai` | on when there is a key | `-ai=false`: no AI diagnosis (it spends tokens of your key) |

## report

```bash
tracereports report [flags] INPUT...
```

Builds a static HTML report, without a server: `index.html` opens with a double click, no internet
needed. Each INPUT is a recording folder (what a client saved without a server), a JUnit XML file, a
folder of `*.xml` (one run), or an `allure-results` folder or its ZIP.

| Flag | Default | |
| --- | --- | --- |
| `-o` | `tracereports-report` | Output folder (one subfolder per run if there are several) |
| `-zip` | — | Also write the report as a ZIP (CI artifact, email) |
| `-ai` | off | Diagnose the failures with the AI of the environment or the `.env` |
| `-name`, `-project`, `-branch`, `-environment` | — | Run context for JUnit XML and Allure inputs |

```bash
tracereports report output/tracereports/my-run -o report
tracereports report results.xml -o report -zip report.zip
tracereports report allure-results -o report --ai
```

More in [without a server](offline.md) and [Allure](allure.md).

## push

```bash
tracereports push [flags] INPUT...
```

Uploads to a server, where the failures get the AI diagnosis. Each INPUT is a recording folder
(pushing it again duplicates nothing), an `allure-results` folder or its ZIP, or JUnit XML (a file or
a folder of `*.xml`). An Allure or JUnit import creates a new run each time. It prints the link to
each run.

| Flag | Default | |
| --- | --- | --- |
| `-url` | `$TRACEREPORTS_URL` or `http://localhost:8080` | Server |
| `-token` | `$TRACEREPORTS_TOKEN` | Write token (the ingest token works) |
| `-name`, `-project`, `-branch`, `-environment` | — | Run context for Allure and JUnit XML inputs |
| `-force` | off | Upload a complete local copy (`TRACEREPORTS_OFFLINE=both`) as a separate run |

```bash
tracereports push output/tracereports/my-run
tracereports push allure-results -project shop -name "Nightly #42" -branch main
tracereports push target/surefire-reports -project shop
```

## pr-comment

```bash
tracereports pr-comment [flags]
```

Comments the run summary on the pull request (GitHub Actions) or merge request (GitLab CI) of the
current job: failures, what changed against the previous run, incidents with their likely cause and
the report link. The next push updates the same comment. GitHub: `GITHUB_TOKEN` with
`pull-requests: write`; GitLab: `GITLAB_TOKEN` or `TRACEREPORTS_GITLAB_TOKEN` (api scope). Outside a pull request it prints the
comment (and adds it to the GitHub job summary).

| Flag | Default | |
| --- | --- | --- |
| `-url`, `-token` | `$TRACEREPORTS_URL`, `$TRACEREPORTS_TOKEN` | Server |
| `-run` | the latest run of this commit | Run id |
| `-from` | — | Without a server: a recording, JUnit XML or allure-results to summarize |
| `-name` | — | Run name for `-from` JUnit XML or Allure |
| `-link` | the server report | Report link |
| `-lang` | the server's language, or `en` | `es` or `en` |
| `-wait` | `1m30s` | How long to wait for the run diagnosis |
| `-dry-run` | off | Print the comment without posting it |

See [CI · pull request comment](ci.md#pull-request-comment).

## repro

```bash
tracereports repro [-var name=value]... [-base URL] <capsule.json | capsule URL>
```

Shows how to run the failed test locally and resends each call of a [FixLab](fixlab.md) capsule from
your machine, with a fresh session. Before using a secret it prints where it will be sent.

| Flag | |
| --- | --- |
| `-var name=value` | Value of a `{{variable}}` (repeatable); or `TRACEREPORTS_LAB_<NAME>`; or typed without being shown |
| `-base URL` | Send to another environment, e.g. `http://localhost:3000` (default: the run's host) |

To read a capsule from a protected server: `TRACEREPORTS_TOKEN` or `TRACEREPORTS_UI_USER` /
`TRACEREPORTS_UI_PASSWORD`, only sent to the server of `TRACEREPORTS_URL`.

```bash
TRACEREPORTS_LAB_PASSWORD=… tracereports repro 'http://localhost:8080/api/v1/network/42/capsule'
```

## verify

```bash
tracereports verify [-var name=value]... [-base URL] <capsule.json | capsule URL>
```

Runs the checks saved on an incident and exits with code **1** if one does not pass: a CI step that
confirms a fix. Same flags and credentials as `repro`.

```bash
tracereports verify 'http://localhost:8080/api/v1/runs/7/lab/capsule?incident=<key>' -base http://localhost:3000
```

## mcp

```bash
tracereports mcp
```

MCP server (stdio) for Claude Code or any MCP client: `list_runs`, `get_incidents`, `get_test`,
`reproduce` and `verify_fix`. It reads from `TRACEREPORTS_URL` (default `http://localhost:8080`; with
`TRACEREPORTS_TOKEN` or `TRACEREPORTS_UI_USER`/`_PASSWORD` if needed) and reproduces or verifies from
this machine with the secrets of `TRACEREPORTS_LAB_<NAME>`, which the agent never sees.

```bash
claude mcp add tracereports -e TRACEREPORTS_URL=http://localhost:8080 -e TRACEREPORTS_LAB_PASSWORD=… -- tracereports mcp
```

See [FixLab · MCP](fixlab.md#for-ai-agents-mcp).

## selftest

```bash
tracereports selftest [-url URL] [-only id,id] [-list]
```

Plays the scenarios recorded with the real clients (pytest, Playwright JS, Selenium Java, Go, REST,
JUnit XML, Allure) and checks what TraceReports shows; exit code 1 if an expectation fails.

| Flag | Default | |
| --- | --- | --- |
| `-url` | a temporary server on this machine | A deployed TraceReports (with `TRACEREPORTS_TOKEN` if it is protected) |
| `-only` | all | Scenario ids, comma separated |
| `-list` | — | List the scenarios and exit |

See [selftest](demo.md#tracereports-selftest).

## secrets

```bash
tracereports secrets status|migrate [-data-dir DIR] [-dry-run]
```

`status` says how each credential saved from Settings is stored (never its value); `migrate` encrypts
with `TRACEREPORTS_SECRET_KEY` the ones saved in clear by older versions, and re-encrypts with the new
key the ones encrypted with `TRACEREPORTS_SECRET_KEY_PREVIOUS`. `-data-dir` defaults to `DATA_DIR` or
`./data`; `-dry-run` only says what would change. Back up the data folder first. See
[configuration · security](configuration.md#security).

## evidence

```bash
tracereports evidence status|encrypt|rekey [-data-dir DIR] [-dry-run]
```

`status` says how the screenshots, traces, videos and DOM snapshots are stored: in clear, or encrypted and
with which key (its id, never the key or the content). `encrypt` encrypts with `TRACEREPORTS_DB_KEY` what
was kept in clear and re-encrypts what `TRACEREPORTS_DB_KEY_PREVIOUS` encrypted; `rekey` does the same
after rotating the key. Stop the server and back up the data folder first. See
[evidence encryption](security-hardening.md#application-level-evidence-encryption-optional).

## health

```bash
tracereports health                       # GET /healthz of the server of this machine (PORT, TRACEREPORTS_BIND)
tracereports health -url http://host:8080
```

Exit code 0 if the server answers, 1 if not. It is the healthcheck of the Docker image (which has no shell
or curl). `GET /healthz` answers `{"status":"ok"}` without credentials and without data.

## version

```bash
tracereports version        # also -version, --version
```

## Which configuration each command reads

| Command | Environment | `.env` of the current folder |
| --- | --- | --- |
| server (`tracereports`) | all | all (variables already set win) |
| `demo` | only the AI provider | only the AI provider |
| `report -ai` | the AI provider | only the AI provider |
| `secrets` | `DATA_DIR`, `TRACEREPORTS_SECRET_KEY…` | all |
| `evidence` | `DATA_DIR`, `TRACEREPORTS_DB_KEY…` | all |
| `push`, `pr-comment`, `repro`, `verify`, `mcp`, `selftest` | `TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`, `TRACEREPORTS_LAB_<NAME>`… | no |

The test clients (pytest, Playwright, Java, Go) have their own variables: see their guides
([Python](python.md), [JavaScript](javascript.md), [Java](java.md), [Go](go.md)).
