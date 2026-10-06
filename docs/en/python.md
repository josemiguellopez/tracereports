# Python client and pytest plugin

🌐 **English** · [Español](../es/python.md)

```bash
pip install tracereports                       # from PyPI
# or, from a checkout of this repository:
pip install ./client/python
# or, the latest development version:
pip install "tracereports @ git+https://github.com/josemiguellopez/tracereports#subdirectory=client/python"
```

Standard library only. The client never breaks or slows down your suite:

- the evidence (steps, screenshots, network, DOM, results) is sent **in the background** from a
  bounded queue: the test does not wait for the network (measured: ~0.004 ms per queued step versus
  ~11 ms sending it inline; ~420 bytes per waiting event);
- failed sends (no connection, timeout, 5xx, 408, 429) are **retried** with backoff, and each one
  carries an `Idempotency-Key`: a retry **never duplicates** a step on the server;
- when the server does not answer, the calls that need its answer (create run or test) fail fast
  for 30 s instead of waiting their timeout in every test;
- `end_run()` waits for the queue (`TRACEREPORTS_FLUSH_TIMEOUT`, 30 s) before closing the run. Whatever
  could not be sent is saved in `TRACEREPORTS_SPOOL_DIR` to resend with
  `python -m tracereports.resend <folder>`; without that folder it is reported as lost. Resending
  goes in batches (a spool larger than the queue also makes progress), in order and without
  duplicates; several processes can share the folder (each file is taken by one, and a dead
  process's file is resumed after 10 minutes). It exits 0 only when nothing is left;
- **without a server**: if the run cannot be created (server down or wrong token), everything is
  recorded in `./tracereports-offline/<session>`; `tracereports report <folder>` builds the HTML report
  and `tracereports push <folder>` uploads it later (see [Without a server](offline.md));
- **full queue** (5000 events or 64 MB): new events are dropped, the queued ones are kept and it is
  counted in `cr.delivery["dropped"]`.

`cr.delivery` tells you what happened: `sent`, `retried`, `rejected` (the server answered 4xx),
`dropped`, `spooled`, `lost`, `pending` and `unregistered_tests`.

Variables it reads: `TRACEREPORTS_URL` (default `http://localhost:8080`), `TRACEREPORTS_TOKEN`,
`TRACEREPORTS_DISABLED=1` (turns reporting off), `TRACEREPORTS_PROJECT`, `TRACEREPORTS_SPOOL_DIR`,
`TRACEREPORTS_FLUSH_TIMEOUT`, `TRACEREPORTS_SYNC=1` (sends everything inline, to debug) and, for the context,
`TRACEREPORTS_BRANCH` / `TRACEREPORTS_COMMIT` (otherwise the GitHub Actions, GitLab, Azure DevOps, Jenkins,
Bitbucket or CircleCI variables, or `git`). A variable missing from the environment is read from the
project's `.env` ([details](configuration.md#the-projects-env-in-the-clients)).

## Option 1: pytest plugin (no code changes)

```bash
pytest --tracereports
pytest --tracereports --tracereports-run "Web regression" --tracereports-env "staging · chrome"
```

| Option | Equivalent variable | Description |
|---|---|---|
| `--tracereports` | — | Turns reporting on |
| `--tracereports-url URL` | `TRACEREPORTS_URL` | Server |
| `--tracereports-run NAME` | `TRACEREPORTS_RUN_NAME` | Run name (default: the project folder). Use a stable name so history and comparison work |
| `--tracereports-env TEXT` | `TRACEREPORTS_ENV` | Environment: browser, stage, version… |
| `--tracereports-project NAME` | `TRACEREPORTS_PROJECT` | Project (default: the root folder). With the environment and branch it defines which runs it is compared with |
| `--tracereports-zip DIR` | — | When finished, saves the report as a ZIP in `DIR` (CI artifact) |
| `--tracereports-no-network` | — | Do not capture the network |
| `--tracereports-no-screenshots` | — | No screenshot on failure (sensitive data on screen) |
| `--tracereports-no-dom` | — | Do not send the page snapshot on failure |
| `--tracereports-spool DIR` | `TRACEREPORTS_SPOOL_DIR` | Saves in `DIR` the evidence that could not be sent |
| `--tracereports-strict` | `TRACEREPORTS_STRICT=1` | The session fails if part of the evidence did not reach the server (useful in CI) |
| — | `TRACEREPORTS_RUN_ID` | Reports into an existing run (CI shards on several machines); whoever created it closes it |

Reported automatically:

- one run per session and one test per case, with the **docstring** as description and the module
  and *markers* as categories;
- each test's **identity** is its `nodeid` (`file::Class::test[params]`), separate from the visible
  name: two `test_login` in different files keep separate histories. If the nodeid changes (you
  renamed the file) or the parameters carry variable data, pin it with
  `@pytest.mark.tracereports_id("login-ok")`. Each parameter combination is a separate test;
- the **context**: project, environment, branch and commit. History, flaky detection and comparison
  only use runs of the same project, environment and branch;
- `PASS` / `FAIL` / `SKIP` status, the error message and the full traceback (input for the AI);
- with **pytest-rerunfailures**, the attempts stay inside the same test: one `WARNING` step per
  retry with the previous error, the evidence of the first failure is kept and the UI shows it as
  *passed after retry* (it counts as flakiness evidence);
- with **pytest-playwright** (`page` fixture): a **screenshot on failure**, **every HTTP call the
  browser made** during the test and a **page snapshot** used to suggest selectors when a locator
  breaks. It is the traffic seen from the browser, not the backend's internal logs.

Expected negative responses (a 401 the test checks on purpose): declare them so they are not
counted as errors nor proposed as the cause of a failure. The marker works on the test, the class
or the module (`pytestmark`):

```python
@pytest.mark.tracereports_expect(status=401, url="*/auth/validate*")
def test_invalid_login(page): ...

def test_other(page, tracereports):
    tracereports.expect_response([403, 404], url="/api/admin", method="GET")
```

To add your own steps and screenshots, use the `tracereports` fixture:

```python
def test_login(page, tracereports):
    """The admin reaches the Dashboard."""
    tracereports.log_info("Open the login page")
    page.goto("https://my-app/login")
    tracereports.attach_screenshot(page.screenshot(), "Login form")
    ...
    tracereports.log_pass("Logged in")
```

Full example: [`examples/pytest-playwright`](../../examples/pytest-playwright).

**pytest-xdist**: `pytest --tracereports -n 4` produces **a single report**. The controller creates the
run, the workers report into it (each test keeps its worker: `gw0`, `gw1`…) and it is closed when
all of them finish. If a worker crashes, its running tests are marked *Interrupted* and the run
**incomplete**: it never shows as passed.

## Option 2: client API (unittest, Selenium, scripts…)

```python
from tracereports import TraceReports

cr = TraceReports()                     # or TraceReports("https://tracereports.mycompany.com", token="...")
cr.start_run("Web regression", environment="staging")

cr.start_test("Valid login", category="login, smoke", description="Valid credentials")
cr.log_info("Open the login page")
cr.attach_screenshot(driver.get_screenshot_as_png(), "Form")   # Selenium
cr.attach_screenshot(page.screenshot(), "Form")                # Playwright
cr.attach_screenshot("screenshots/login.png", "From a file")
cr.log_pass("User logged in")
cr.end_test()                           # status derived from the steps, or end_test("FAIL", ...)

cr.end_run()
cr.download_report("output/")           # optional: ZIP to attach or archive
```

| Method | Description |
|---|---|
| `start_run(name, environment="")` | Creates the run |
| `start_test(name, category="", description="")` | Starts a test (`category` accepts comma-separated tags) |
| `log_info / log_pass / log_fail / log_warning / log_skip(msg)` | Adds a step |
| `attach_screenshot(bytes_or_path, message="", status="INFO")` | Uploads a screenshot as a step |
| `attach_network(connections)` | Uploads the test's network (see below) |
| `attach_dom(capturar_dom(page))` | On failure: page snapshot used to suggest selectors when a locator broke. The pytest plugin does it for you |
| `end_test(status=None, error_message="", error_trace="", exc=None)` | Closes the test. With `exc=` it takes message and traceback from the exception. A `FAIL` triggers the AI |
| `end_run()` | Closes the run: overall diagnosis and notifications |
| `download_report(dir)` | Downloads the run's ZIP |

Shortcuts so you don't manage the lifecycle by hand:

```python
@cr.track(category="smoke", on_failure=lambda self: self.driver.get_screenshot_as_png())
def test_login(self):
    """The docstring is the description."""
    ...

with cr.test("Checkout", category="e2e", on_failure=page.screenshot):
    ...
```

## Network capture with Playwright

```python
from tracereports import attach_listeners, reportar_red

page = context.new_page()
attach_listeners(page)                  # BEFORE the first page.goto()
...
summary = reportar_red(page, cr,        # at the end of each test, BEFORE cr.end_test()
                       guardar_en="output/network", nombre_evento="test_login")
# {"total": 42, "ok": 40, "errores": 2, "fallidas": 1, "archivo": ".../network_test_login_1790.json"}
```

- Sends only the calls of the **current test**, not accumulated ones.
- Masks `Authorization`, `Cookie`, tokens, passwords and national IDs (RUT).
- Stores the body of `/api/` calls, XHR/fetch and failed responses.
- `guardar_en=` (save to) also writes a JSON file with the **full** bodies. The server keeps up to
  256 KB per body, and the report shows that file's path when it trims one.

Helpers to check the backend inside the test:

```python
from tracereports import conexiones_del_test, esperar_conexion

c = esperar_conexion(page, lambda c: c["method"] == "POST" and "/auth" in c["url"])
assert c["status"] == 302
```

`conexiones_del_test` returns the calls of the current test; `esperar_conexion` waits for one that
matches.

Full example (Page Objects, multi-test workflow, negative tests and simulated backend failures):
[`examples/orangehrm`](../../examples/orangehrm).
