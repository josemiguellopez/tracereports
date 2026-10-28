# Install TraceReports with Docker, step by step

🌐 **English** · [Español](../es/docker-paso-a-paso.md)

A guide to install TraceReports with Docker **from scratch**, even if you have never used a terminal. At the
end you will have TraceReports running with strong passwords, you will see your first test in the report
and you will know how to check that security is configured well.

**How to use it:** follow the steps **in order**. Each step has:

- 🎯 **What you will do** and why.
- 💻 **The command** to copy and paste.
- ✅ **You should see**: what appears when it worked.
- ⚠️ **If you see this**: what to do when something looks different.

The commands are written for **Windows (PowerShell)**. Where they differ on macOS or Linux, there is a
collapsible **"macOS / Linux"** block below.

> **Already have TraceReports installed with Docker on this computer?** Read the note at the end of
> [step 3](#step-3--download-tracereports) first.
>
> **Time:** about 30 minutes. The first time, Docker downloads and builds the image (1 to 3 minutes).
>
> **Already know Docker and only need the reference?** Go to [Docker](docker.md). For a server shared by a
> company, follow the [security hardening guide](security-hardening.md) after this one.
>
> The screenshots show the interface in Spanish; it can be switched to English in **Settings → Appearance**.

---

## Contents

- [Part A — Prepare](#part-a--prepare) (steps 1 to 3)
- [Part B — Configure](#part-b--configure) (step 4)
- [Part C — Start](#part-c--start) (steps 5 to 7)
- [Part D — Sign in and check](#part-d--sign-in-and-check) (steps 8 to 10)
- [Part E — Your first test](#part-e--your-first-test) (steps 11 to 14)
- [Part F — Check security (optional)](#part-f--check-security-optional) (steps 15 to 17)
- [Day to day](#day-to-day) · [Uninstall](#uninstall) · [Troubleshooting](#troubleshooting) · [Checklist](#checklist)

---

## Part A — Prepare

### Step 1 — Open the terminal

🎯 The terminal is the window where you type commands.

**Windows:** press the **Windows** key, type **PowerShell** and press **Enter**.
To **paste** a command: right click, or `Ctrl`+`V`. Then **Enter**.

<details><summary>macOS / Linux</summary>

- **macOS:** `Cmd`+`Space`, type **Terminal** and press Enter. Paste: `Cmd`+`V`.
- **Linux:** `Ctrl`+`Alt`+`T`. Paste: `Ctrl`+`Shift`+`V`.

</details>

---

### Step 2 — Install Docker and check that it is running

🎯 Docker is the program that runs TraceReports in an isolated, secure "box" (a **container**).

**If you do not have it yet:**

- **Windows and macOS:** install **Docker Desktop** from [docs.docker.com/desktop](https://docs.docker.com/desktop/)
  with the default options. Open it from the Start menu (or Applications), accept the terms and wait until it
  says **Engine running**.
- **Linux (Ubuntu):** install Docker Engine following
  [docs.docker.com/engine/install/ubuntu](https://docs.docker.com/engine/install/ubuntu/).

> License: Docker Desktop requires a paid subscription in companies with more than 250 employees or more
> than USD 10 million in annual revenue. Docker Engine on Linux has no such condition.

**Check** that it works:

💻

```powershell
docker --version
docker compose version
docker info --format "{{.ServerVersion}}"
```

✅ **You should see** three lines, for example:

```
Docker version 29.8.1, build ...
Docker Compose version v5.5.1
29.8.1
```

⚠️ **If you see this:**

- `Cannot connect to the Docker daemon`, or the third line fails → Docker is not running. Open **Docker
  Desktop** and wait for **Engine running**. On Windows, if the engine does not start: open PowerShell **as
  administrator**, run `wsl --install --no-distribution` and restart the computer.
- `docker is not recognized` → close the terminal, open it again and retry.

---

### Step 3 — Download TraceReports

🎯 Get the project folder. It includes `docker-compose.yml`, which tells Docker how to run TraceReports
securely.

You need **git**. If `git --version` does not answer, install it from
[git-scm.com/downloads](https://git-scm.com/downloads) with the default options and open a new terminal.

💻

```powershell
cd $HOME
git clone https://github.com/josemiguellopez/tracereports.git
cd tracereports
git switch dev
dir docker-compose.yml, .env.example
```

> **`git switch dev` is required for now:** until the next version is released, the features in this guide
> (read-only login, evidence encryption, `doctor --security`) are on the `dev` branch. Without that command you
> would install the previous version and not see them.

✅ **You should see** `git clone` download the project (`Resolving deltas: 100% ... done.`) and, at the
end, both files listed. `git switch dev` replies `Switched to a new branch 'dev'`.

<details><summary>macOS / Linux</summary>

```bash
cd ~
git clone https://github.com/josemiguellopez/tracereports.git
cd tracereports
ls docker-compose.yml .env.example
```

</details>

> **Important rule:** commands that start with `docker compose` **only work inside the `tracereports`
> folder** (where `docker-compose.yml` is). In any other folder they answer
> `no configuration file provided: not found`. If you close the terminal, come back with
> `cd $HOME\tracereports`.

<details>
<summary><b>Already have TraceReports installed with Docker on this computer?</b> (rare case: open it only if it is yours)</summary>

- If you want to **try** this version and **go back to yours** at the end, follow
  [Try without touching your installation](docker-isolated-trial.md) instead of this guide.
- If you want **both to live side by side**: Docker identifies each installation by the name of its folder,
  so the new one would replace the previous one and use its data. Before step 4, stop the previous one and
  separate the new one with a `docker-compose.override.yml`, as in steps
  [3](docker-isolated-trial.md#step-3--find-and-stop-your-current-installation-nothing-is-deleted) and
  [5](docker-isolated-trial.md#step-5--separate-the-trial-from-your-installation-very-important) of that guide
  (change `prueba` to any name you like). From then on, the names you see carry that suffix.

</details>

---

## Part B — Configure

### Step 4 — Create the passwords and the `.env` file

🎯 TraceReports **does not start without passwords**: that way it never ends up open by mistake. This step
creates every password it needs at random and stores them in the `.env` configuration file.

| What | What for |
| --- | --- |
| Admin token | The "master" password of programs that administer |
| Ingest token | The password your tests use to send results (it cannot administer) |
| `admin` user password | Sign in to the browser as administrator |
| `equipo` (team) user password | Sign in to the browser **only to look** |
| Encryption key | Encrypt the screenshots stored on disk |

💻 Inside the `tracereports` folder, copy and paste **the whole block** at once:

```powershell
function New-Secret { $b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); -join ($b | ForEach-Object { $_.ToString("x2") }) }
$b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); $dbkey = [Convert]::ToBase64String($b)
$lines = @(
  "# TraceReports",
  "TRACEREPORTS_TOKEN=$(New-Secret)",
  "TRACEREPORTS_INGEST_TOKEN=$(New-Secret)",
  "TRACEREPORTS_UI_USER=admin",
  "TRACEREPORTS_UI_PASSWORD=$(New-Secret)",
  "TRACEREPORTS_UI_VIEWER_USER=equipo",
  "TRACEREPORTS_UI_VIEWER_PASSWORD=$(New-Secret)",
  "TRACEREPORTS_SETTINGS_LOCKED=1",
  "TRACEREPORTS_RETENTION_DAYS=90",
  "TRACEREPORTS_SCREENSHOTS=failures",
  "TRACEREPORTS_ENCRYPT_AT_REST=1",
  "TRACEREPORTS_DB_KEY=$dbkey",
  "AI_PROVIDER=builtin",
  "TRACEREPORTS_AI_ALLOWED_PROVIDERS=builtin",
  "NOTIFY_DETAIL=minimal"
)
[IO.File]::WriteAllLines("$PWD\.env", $lines, (New-Object Text.UTF8Encoding($false)))
Get-Content .env | Select-String "_USER=|_PASSWORD="
```

✅ **You should see** your users and their passwords (64 letters and digits each):

```
TRACEREPORTS_UI_USER=admin
TRACEREPORTS_UI_PASSWORD=3f9a0c...e71b
TRACEREPORTS_UI_VIEWER_USER=equipo
TRACEREPORTS_UI_VIEWER_PASSWORD=8b21dd...04aa
```

To see the passwords later: `Get-Content .env` (inside the `tracereports` folder).

> **Very important:**
>
> - **Keep a copy** of the passwords in your password manager. `TRACEREPORTS_DB_KEY` is the only way to read
>   the encrypted screenshots: if you lose it, they cannot be recovered.
> - The `.env` holds passwords: **never commit it to git or share it** by chat or email.

<details><summary>macOS / Linux</summary>

```bash
cat > .env <<EOF
# TraceReports
TRACEREPORTS_TOKEN=$(openssl rand -hex 32)
TRACEREPORTS_INGEST_TOKEN=$(openssl rand -hex 32)
TRACEREPORTS_UI_USER=admin
TRACEREPORTS_UI_PASSWORD=$(openssl rand -hex 32)
TRACEREPORTS_UI_VIEWER_USER=equipo
TRACEREPORTS_UI_VIEWER_PASSWORD=$(openssl rand -hex 32)
TRACEREPORTS_SETTINGS_LOCKED=1
TRACEREPORTS_RETENTION_DAYS=90
TRACEREPORTS_SCREENSHOTS=failures
TRACEREPORTS_ENCRYPT_AT_REST=1
TRACEREPORTS_DB_KEY=$(openssl rand -base64 32)
AI_PROVIDER=builtin
TRACEREPORTS_AI_ALLOWED_PROVIDERS=builtin
NOTIFY_DETAIL=minimal
EOF
chmod 600 .env
grep -E "_USER=|_PASSWORD=" .env
```

</details>

What each line means:

| Line | What it does |
| --- | --- |
| `TRACEREPORTS_TOKEN` / `TRACEREPORTS_INGEST_TOKEN` | The programs' passwords |
| `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` | Admin login |
| `TRACEREPORTS_UI_VIEWER_USER` / `TRACEREPORTS_UI_VIEWER_PASSWORD` | Read-only login |
| `TRACEREPORTS_SETTINGS_LOCKED=1` | Nobody changes the configuration from the screen; only from this file |
| `TRACEREPORTS_RETENTION_DAYS=90` | Deletes what is older than 90 days automatically |
| `TRACEREPORTS_SCREENSHOTS=failures` | Keeps screenshots only of what failed |
| `TRACEREPORTS_ENCRYPT_AT_REST=1` + `TRACEREPORTS_DB_KEY` | Encrypts screenshots, videos and DOM |
| `AI_PROVIDER=builtin` | Diagnosis with local rules: **no data leaves** your computer |
| `NOTIFY_DETAIL=minimal` | If you connect Teams or Slack, no test names nor errors are sent |

Every option is in [Configuration](configuration.md).

---

## Part C — Start

### Step 5 — Build and start TraceReports

🎯 Docker builds the TraceReports image and starts it **in the background**.

💻

```powershell
docker compose up -d
```

✅ **You should see** it download and build (1 to 3 minutes the first time) and finish like this:

```
 ✔ Image tracereports:latest                     Built
 ✔ Network tracereports_default                  Created
 ✔ Volume tracereports-data                      Created
 ✔ Container tracereports-tracereports-1         Started
```

⚠️ **If you see this:**

- `port is already allocated` → another program uses port 8080 (another TraceReports installation?). See
  the note at the end of [step 3](#step-3--download-tracereports).
- A network error while downloading → check your connection and retry.

---

### Step 6 — Check that it is healthy

💻

```powershell
docker compose ps
```

✅ **You should see** the container **`Up`** and, after a few seconds, **`(healthy)`**:

```
NAME                          IMAGE                 STATUS                    PORTS
tracereports-tracereports-1   tracereports:latest   Up 12 seconds (healthy)   127.0.0.1:8080->8080/tcp, [::1]:8080->8080/tcp
```

If it says `(health: starting)`, wait 20 seconds and run it again. If it says **`Restarting`**, see step 7.

> The port is published **only for this computer** (`127.0.0.1`): nobody else on your network can get in. To
> share it with your team, see [Docker → Deploy on a server](docker.md#deploy-on-a-server).

---

### Step 7 — Look at the logs

🎯 The logs tell what TraceReports is doing and whether there is a problem.

💻

```powershell
docker compose logs --tail 25 tracereports
```

✅ **You should see** these lines (the dates change):

```
INFO evidence encrypted at rest (screenshots, traces, videos, DOM) key_id=1ad8abcbce9c5cd3
INFO screenshot policy screenshots=failures
INFO failure diagnosis: local rules engine (no LLM, no network); ...
INFO egress: no external destination configured (nothing leaves this server)
INFO retention enabled days=90
INFO TraceReports listening url=http://localhost:8080 bind=""
```

⚠️ **If you see this:**

- `refusing to start` → the passwords are missing: the `.env` was not created properly or is not in this
  folder. Repeat **step 4** and then `docker compose up -d`.
- A message about `DB_KEY` → the encryption key is wrong. Repeat **step 4** and then `docker compose up -d`.

---

## Part D — Sign in and check

### Step 8 — Sign in as administrator

1. Open the browser at **http://localhost:8080**
2. A window asks for **username and password**:
   - Username: `admin`
   - Password: the value of `TRACEREPORTS_UI_PASSWORD` in the `.env` (copy and paste it).

✅ **You should see** the TraceReports screen. At first it is **empty** because there are no tests yet. This
is how it looks once it has results:

![TraceReports main screen with a run and its tests](../img/docker-guide/inicio.webp)

⚠️ **If the window keeps asking for the password:** it does not match the `.env`. Copy it again, without
trailing spaces.

---

### Step 9 — Check security

🎯 TraceReports checks its own security configuration.

In the browser: **Settings** (bottom left) → **Security check**.

![Settings, Security check with the checks in green](../img/docker-guide/postura.webp)

✅ Most of them green (**OK**). With Docker on your computer it is normal for **"Network exposure"** to show
as **Review**: inside the container TraceReports listens on the *container's* network, even though the port
is only published for your computer. On a server it is solved with `TRACEREPORTS_ALLOWED_CIDRS` (see the
[security hardening guide](security-hardening.md)).

The same from the command line (inside the `tracereports` folder):

💻

```powershell
docker compose exec tracereports /tracereports doctor --security
```

✅ **You should see** (real output of an installation made with this guide):

```
OK    API tokens                   admin and ingest tokens set and different
OK    UI login                     reads require the UI login or a token
WARN  Network exposure             listening on the network for any source address
                                   → limit TRACEREPORTS_ALLOWED_CIDRS to the networks that need it (or a firewall)
OK    AI provider                  local rules engine or off: no evidence leaves the server
OK    Settings locked              Settings is read-only: the configuration comes only from the environment
OK    Retention                    runs older than 90 days are deleted (secure_delete, VACUUM)
OK    Evidence encryption at rest  screenshots, traces, videos and DOM encrypted (AES-256-GCM), key outside DATA_DIR
OK    Process user                 unprivileged user (uid 65532)
OK    DATA_DIR permissions         0700: only the server user
OK    Outbound destinations        none: no data leaves the server
OK    Secret redaction             secrets in headers, bodies and logs are masked before storing
OK    Screenshot policy            TRACEREPORTS_SCREENSHOTS=failures

11 ok, 1 warn, 0 fail
```

What matters is **`0 fail`**.

In **Settings → Artificial intelligence** you will see **"The administrator locked these settings"**: that
is `TRACEREPORTS_SETTINGS_LOCKED=1`, as it should be.

![AI settings locked by the administrator](../img/docker-guide/ia-bloqueada.webp)

---

### Step 10 — Sign in as the read-only user

🎯 Check that the `equipo` user can look but **not** administer.

1. Open a **private window** in the browser (`Ctrl`+`Shift`+`N` in Chrome or Edge). The browser remembers the
   previous login; the private window lets you sign in as another user.
2. Go to **http://localhost:8080**.
3. Username: `equipo`. Password: `TRACEREPORTS_UI_VIEWER_PASSWORD` from the `.env`.

✅ It sees the same reports, but in **Settings** the **Security check** says **"No access"**:

![Settings with the read-only login: Security check without access](../img/docker-guide/viewer.webp)

Close the private window.

---

## Part E — Your first test

### Step 11 — Prepare Python (in another terminal)

🎯 We will run an automated test with **pytest** (Python) that sends its results to TraceReports.

You need **Python 3**. If `python --version` does not answer, install it from
[python.org/downloads](https://www.python.org/downloads/) and tick **"Add python.exe to PATH"** in the
installer.

Open **another** terminal (keep the first one open) and paste:

💻

```powershell
mkdir $HOME\my-tests
cd $HOME\my-tests
python -m venv .venv
.venv\Scripts\Activate.ps1
pip install pytest $HOME\tracereports\client\python
```

✅ **You should see** the start of the line change to `(.venv) PS ...\my-tests>` and `pip` end with
`Successfully installed ... pytest ... tracereports ...`.

⚠️ **If you see** `running scripts is disabled on this system`: paste
`Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`, answer **Y** and repeat the last two lines.

<details><summary>macOS / Linux</summary>

```bash
mkdir -p ~/my-tests && cd ~/my-tests
python3 -m venv .venv
source .venv/bin/activate
pip install pytest ~/tracereports/client/python
```

</details>

---

### Step 12 — Create an example test

🎯 Two tests: one passes and the other **fails on purpose**, to see how a failure is shown.

💻 Paste **the whole block**:

```powershell
@'
def test_basic_sum():
    """The sum of two numbers gives the expected result."""
    assert 2 + 2 == 4


def test_cart_total():
    """The cart total includes the tax."""
    price, tax = 100, 19
    assert price + tax == 120, "the total does not include the right tax"
'@ | Set-Content -Encoding utf8 test_first_report.py
dir test_first_report.py
```

✅ The file `test_first_report.py` is listed.

<details><summary>macOS / Linux</summary>

```bash
cat > test_first_report.py <<'EOF'
def test_basic_sum():
    """The sum of two numbers gives the expected result."""
    assert 2 + 2 == 4


def test_cart_total():
    """The cart total includes the tax."""
    price, tax = 100, 19
    assert price + tax == 120, "the total does not include the right tax"
EOF
```

</details>

---

### Step 13 — Run the test and send the results

🎯 Tests use the **ingest token** (the one that only sends results). This block **reads it by itself** from
the `.env`: there is nothing to copy by hand.

💻 Paste **the whole block**:

```powershell
$env:TRACEREPORTS_URL = "http://127.0.0.1:8080"
$env:TRACEREPORTS_TOKEN = ((Get-Content $HOME\tracereports\.env | Select-String '^TRACEREPORTS_INGEST_TOKEN=').Line -split '=', 2)[1]
"Token loaded: " + $env:TRACEREPORTS_TOKEN.Length + " characters"
pytest --tracereports --tracereports-run "My first report"
```

✅ **You should see** `Token loaded: 64 characters` first and, at the end:

```
test_first_report.py .F                                                         [100%]
...
E       AssertionError: the total does not include the right tax
E       assert (100 + 19) == 120
TraceReports: http://127.0.0.1:8080/#run=1&view=dashboard
FAILED test_first_report.py::test_cart_total - AssertionError: the total does not include the right tax
========================= 1 failed, 1 passed in 0.11s =========================
```

> **1 failure** is expected: we did it on purpose.

⚠️ **If you see this:**

- `Token loaded: 0 characters` → it did not find the `.env`. Check that `$HOME\tracereports\.env` exists
  (step 4).
- A **401** warning or no `TraceReports:` line → the token does not match the server's. If you changed the
  `.env` after starting, apply it with `docker compose up -d` (in the `tracereports` folder).
- A `tracereports-offline` folder appeared → TraceReports was not answering. Check step 6.

> On Windows use `http://127.0.0.1:8080` rather than `localhost` for the tests: with `localhost` each
> connection can take about 2 seconds.

<details><summary>macOS / Linux</summary>

```bash
export TRACEREPORTS_URL=http://127.0.0.1:8080
export TRACEREPORTS_TOKEN="$(grep '^TRACEREPORTS_INGEST_TOKEN=' ~/tracereports/.env | cut -d= -f2-)"
echo "Token loaded: ${#TRACEREPORTS_TOKEN} characters"
pytest --tracereports --tracereports-run "My first report"
```

</details>

---

### Step 14 — See the report

1. Go back to the browser where you signed in as `admin` (or open **http://localhost:8080**).
2. Reload the page (`F5`).

✅ **You should see** the run **"My first report"** with **1 test passed and 1 failed**. Click
`test_cart_total`:

![Report with the failed test and the local engine diagnosis](../img/docker-guide/primer-run.webp)

- At the top, the **diagnosis** of the local engine: 120 was expected and `(100 + 19)` was obtained.
- Below, the **error** and the **steps**.

With browser tests (Playwright or Selenium), each failed test also has its **screenshots** and **network
calls**, with passwords masked:

![Detail of a failed test with diagnosis, error and steps](../img/docker-guide/test-fallido.webp)

![Network tab with the password masked](../img/docker-guide/red.webp)

To connect your real tests (Playwright, Selenium, JUnit 5, Go), see **Settings → Connect your tests** or the
docs of each client: [Python](python.md), [JavaScript](javascript.md), [Java](java.md), [Go](go.md).

🎉 **TraceReports is installed and working.**

---

## Part F — Check security (optional)

These steps check that the protections work. Run them in the Python terminal (the one from step 11).

### Step 15 — Screenshots are stored encrypted

🎯 We send a screenshot of a failed step and check that it is **encrypted** on disk but shows normally on
screen.

💻 Paste **the whole block** (it creates a test image and sends it with the ingest token):

```powershell
$T = ((Get-Content $HOME\tracereports\.env | Select-String '^TRACEREPORTS_INGEST_TOKEN=').Line -split '=', 2)[1]
$B = "http://127.0.0.1:8080/api/v1"
$H = @{ Authorization = "Bearer $T" }
Add-Type -AssemblyName System.Drawing
$bmp = New-Object Drawing.Bitmap 400, 200
$g = [Drawing.Graphics]::FromImage($bmp); $g.Clear([Drawing.Color]::SteelBlue)
$g.DrawString("test screenshot", (New-Object Drawing.Font "Arial", 20), [Drawing.Brushes]::White, 20, 80)
$bmp.Save("$PWD\screenshot.png", [Drawing.Imaging.ImageFormat]::Png); $g.Dispose(); $bmp.Dispose()
$run  = Invoke-RestMethod -Method Post -Headers $H -ContentType "application/json" -Uri "$B/runs" -Body '{"name":"Test with screenshot"}'
$test = Invoke-RestMethod -Method Post -Headers $H -ContentType "application/json" -Uri "$B/runs/$($run.run_id)/tests" -Body '{"name":"Login fails"}'
curl.exe -s -H "Authorization: Bearer $T" -F "file=@screenshot.png" -F "status=FAIL" -F "message=screen of the failure" "$B/tests/$($test.test_id)/screenshot"
Invoke-RestMethod -Method Patch -Headers $H -ContentType "application/json" -Uri "$B/tests/$($test.test_id)/finish" -Body '{"status":"FAIL","error_message":"TimeoutError: the dashboard did not show up"}' | Out-Null
Invoke-RestMethod -Method Patch -Headers $H -Uri "$B/runs/$($run.run_id)/finish" | Out-Null
```

✅ The `curl.exe` line answers a text that starts with `{"log":` and ends with `"url":"/screenshots/..."`.

Now check how it was stored. Go to the `tracereports` folder and ask the container:

```powershell
cd $HOME\tracereports
docker compose exec tracereports /tracereports evidence status
cd $HOME\my-tests
```

✅ **You should see** (real output):

```
files: 1 encrypted (current key 1ad8abcbce9c5cd3)
DOM snapshots: none
```

The screenshot is **encrypted** on disk. In the browser, open **"Test with screenshot" → "Login fails"**:
the blue screenshot **shows normally**.

---

### Step 16 — The ingest token cannot administer

🎯 Your tests' token must not be able to see the security configuration.

💻 (`$H` from step 15 is still defined):

```powershell
try { Invoke-RestMethod -Headers $H -Uri "$B/security/posture" } catch { $_.Exception.Response.StatusCode.value__ }
```

✅ **You should see:** `403` (forbidden). Correct.

---

### Step 17 — The brake on wrong passwords (do it last)

🎯 After **10 wrong passwords** in a row, TraceReports blocks that connection for 15 minutes.

> ⚠️ This also blocks **your browser** for 15 minutes. To unblock it earlier, restart TraceReports:
> `docker compose restart` (in the `tracereports` folder).

💻

```powershell
1..10 | ForEach-Object { curl.exe -s -o NUL -w "%{http_code} " -u "admin:wrong-password" http://127.0.0.1:8080/api/v1/runs }
""
```

✅ **You should see** `401` ten times. Now with the **right** password (the block reads it from the `.env`):

```powershell
$P = ((Get-Content $HOME\tracereports\.env | Select-String '^TRACEREPORTS_UI_PASSWORD=').Line -split '=', 2)[1]
curl.exe -s -o NUL -w "%{http_code}`n" -u "admin:$P" http://127.0.0.1:8080/api/v1/runs
```

✅ **You should see:** `429` (too many attempts), even with the right password. That is the brake working.

---

## Day to day

Always inside the `tracereports` folder:

| What you want | Command |
| --- | --- |
| See whether it is running | `docker compose ps` |
| See the logs | `docker compose logs -f tracereports` (`Ctrl`+`C` to quit) |
| Restart | `docker compose restart` |
| Stop (data is kept) | `docker compose stop` |
| Start again | `docker compose start` |
| Apply changes to the `.env` | `docker compose up -d` |
| Update to a new version | Make a backup, then `git pull` and `docker compose up -d --build` |
| Check security | `docker compose exec tracereports /tracereports doctor --security` |

> **Never** run `docker compose down -v` if you want to keep your data: the `-v` **deletes the volume with
> all the data**.

For **backups**, see [Docker → Backup and restore](docker.md#backup-and-restore). To let other people in
(HTTPS, allowed networks, firewall), see [Docker → Deploy on a server](docker.md#deploy-on-a-server) and the
[security hardening guide](security-hardening.md).

---

## Uninstall

Only if you want to **delete TraceReports and all its data**. Paste **one line at a time** (when several are
pasted together, two may merge into one and the command fails).

1. Leave the Python environment and go back to the `tracereports` folder:

   ```powershell
   deactivate
   ```

   ```powershell
   cd $HOME\tracereports
   ```

2. Delete the container and **the data** (the volume):

   ```powershell
   docker compose down -v
   ```

3. Delete the image:

   ```powershell
   docker image rm tracereports:latest
   ```

   If you separated this installation from another one (note in step 3), the image has the name you chose
   (`tracereports:<name>`), and `down -v` deleted only this one's data: the other one is intact.

4. Close the other terminal, Notepad and any File Explorer window in those folders, then delete the folders:

   ```powershell
   cd $HOME
   ```

   ```powershell
   Remove-Item -Recurse -Force $HOME\tracereports, $HOME\my-tests
   ```

⚠️ If it says the folder `is being used by another process`, something still has it open: close this
terminal, open a new one and repeat only the last line.

---

## Troubleshooting

| What you see | What to do |
| --- | --- |
| `no configuration file provided: not found` | You are outside the `tracereports` folder. Run `cd $HOME\tracereports` |
| `Cannot connect to the Docker daemon` | Docker is not running: open Docker Desktop and wait for **Engine running** |
| `port is already allocated` | Another installation uses port 8080. See the note at the end of [step 3](#step-3--download-tracereports) |
| The log says `refusing to start` | The passwords are missing from the `.env`: repeat step 4 and `docker compose up -d` |
| `missing or invalid API token` | The token is not the ingest token from the `.env`, or it still has an example text |
| The browser keeps asking for the password | The password does not match the `.env` |
| `429` | 10 wrong passwords in 15 minutes: wait or `docker compose restart` |
| `has no container to start` | You are in another folder: go to the folder where you installed TraceReports |
| The folder cannot be deleted | Something has it open: close terminals, Notepad and File Explorer |

More cases in [Docker → Troubleshooting](docker.md#troubleshooting).

---

## Checklist

- [ ] 2 — Docker running (`docker info` answers)
- [ ] 3 — Project downloaded
- [ ] 4 — `.env` with passwords created and passwords kept in your password manager
- [ ] 5–6 — Container `Up (healthy)`
- [ ] 7 — Logs with `evidence encrypted at rest`
- [ ] 8 — Signed in as `admin`
- [ ] 9 — `doctor --security` with `0 fail`
- [ ] 10 — `equipo` can look but not administer
- [ ] 13–14 — First test sent and visible in the report
- [ ] 15–17 (optional) — Encrypted screenshot, the ingest token gets `403` and the brake gives `429`
