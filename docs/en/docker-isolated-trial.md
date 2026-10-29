# Try TraceReports with Docker without touching your installation

🌐 **English** · [Español](../es/docker-prueba-aislada.md)

A guide to **try a version of TraceReports with Docker, end to end, when you already have another one
installed**. The new one goes into another folder, with its own name and its own data. You try everything
(logins, encryption, your first test, the security protections) and **at the end everything is left as it
was**, with your usual installation running and its data intact.

> First installation? Follow [Install with Docker, step by step](docker-step-by-step.md).

**How to use it:** follow the steps **in order**. Each step has:

- 🎯 **What you will do** and why.
- 💻 **The command** to copy and paste into PowerShell.
- ✅ **You should see**: what appears when it worked.
- ⚠️ **If you see this**: what to do when something looks different.

> **Time:** about 45 minutes. The guide is written for **Windows (PowerShell)**. The screenshots show the
> interface in Spanish; it can be switched to English in **Settings → Appearance**.
>
> **Paste the blocks as they are.** None has example text to replace: the passwords are read from the trial's
> `.env` by the commands themselves. When a step says **"one line at a time"**, paste and run each block on
> its own: when several are pasted together, two may merge into one and the command fails.

---

## Contents

- [Part A — Prepare](#part-a--prepare) (steps 1 to 4)
- [Part B — Configure](#part-b--configure) (steps 5 and 6)
- [Part C — Start](#part-c--start) (steps 7 to 9)
- [Part D — Sign in and check](#part-d--sign-in-and-check) (steps 10 to 12)
- [Part E — Send the first test](#part-e--send-the-first-test) (steps 13 to 16)
- [Part F — Security checks](#part-f--security-checks) (steps 17 to 19)
- [Part G — Leave everything as it was](#part-g--leave-everything-as-it-was) (steps 20 to 22)
- [Troubleshooting](#troubleshooting) · [Checklist](#checklist)

---

## Part A — Prepare

### Step 1 — Open PowerShell

🎯 PowerShell is the window where you type commands.

1. Press the **Windows** key.
2. Type **PowerShell** and press **Enter**.

To **paste** a command: right click, or `Ctrl`+`V`. Then **Enter**.

> In Part E you will open a **second** window. Until then, always use this one.

---

### Step 2 — Check that Docker is running

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

⚠️ **If you see** `Cannot connect to the Docker daemon` or the third line fails: open **Docker Desktop** and
wait for **Engine running**. If it says `docker is not recognized`, close PowerShell, open it again and
retry.

---

### Step 3 — Find and stop your current installation (nothing is deleted)

🎯 Your current installation uses port 8080: we **stop** it while trying. Nothing is deleted; its data stays
in its volume.

**3.1 — Find your installation** (where it is and whether it is running):

💻

```powershell
docker ps -a --filter "label=com.docker.compose.service=tracereports" --format "{{.Names}}|{{.Status}}|{{.Labels}}" | ForEach-Object { $n, $s, $l = $_ -split '\|', 3; "$n  ($s)  folder: " + (($l -split ',' | Where-Object { $_ -like 'com.docker.compose.project.working_dir=*' }) -replace '^[^=]*=', '') }
```

✅ **You should see** something like:

```
tracereports-tracereports-1  (Up 14 minutes)  folder: C:\dev\tracereports
```

> 📝 **Write that folder down**: you need it at the end (step 21) to start your installation again.

**3.2 — Go to that folder** (change the path to the one the command showed) and **stop** the installation.
One line at a time:

```powershell
cd C:\dev\tracereports
```

```powershell
docker compose stop
```

✅ **You should see:** `Container tracereports-tracereports-1  Stopped`.

Check that the port is free:

```powershell
docker ps
```

✅ The list is **empty** (only the headers), or nothing shows `8080`.

---

### Step 4 — Download TraceReports into a trial folder

🎯 Download the project into a separate folder, `C:\prueba-docker`, as a newcomer would.

💻

```powershell
mkdir C:\prueba-docker
cd C:\prueba-docker
git clone https://github.com/josemiguellopez/tracereports.git
cd tracereports
git switch dev
dir docker-compose.yml, .env.example
```

> **`git switch dev` is required for now:** until the next version is released, the features in this guide
> (read-only login, evidence encryption, `doctor --security`) are on the `dev` branch. Without that command you
> would install the previous version and not see them.

✅ **You should see** `git clone` download the project (`Resolving deltas: 100% ... done.`) and, at the end,
both files listed. `git switch dev` replies `Switched to a new branch 'dev'`.

⚠️ **If you see** that the item already exists: a folder from a previous trial is left. Delete it with
`Remove-Item -Recurse -Force C:\prueba-docker` and repeat the step.

> **Important rule:** commands that start with `docker compose` **only work inside
> `C:\prueba-docker\tracereports`** (where `docker-compose.yml` is). In any other folder they answer
> `no configuration file provided: not found`. The check commands in this guide use `docker exec ...`, which
> works from any folder.

---

## Part B — Configure

### Step 5 — Separate the trial from your installation (very important)

🎯 Docker identifies each installation by the **name of its folder**. As this one is also called
`tracereports`, Docker would think it is the same as yours and **would replace your container and use your
data**. This file gives the trial **its own name, its own image and its own data volume**.

💻 Paste **the whole block** at once:

```powershell
@"
name: tracereports-prueba
services:
  tracereports:
    image: tracereports:prueba
volumes:
  tracereports-data:
    name: tracereports-prueba-data
"@ | Set-Content -Encoding ascii docker-compose.override.yml
```

Check that it is separate:

```powershell
docker compose config --format json | Select-String '"name": "tracereports-prueba'
```

✅ **You should see** three lines, **all with `tracereports-prueba`**:

```
  "name": "tracereports-prueba",
      "name": "tracereports-prueba_default",
      "name": "tracereports-prueba-data"
```

⚠️ If those lines do **not** show up, **stop here**: check that you are in `C:\prueba-docker\tracereports`
and repeat the step.

---

### Step 6 — Create the passwords and the trial's `.env`

🎯 TraceReports **does not start without passwords**. This block creates them at random and builds the
trial's `.env` configuration file:

| What | What for |
| --- | --- |
| Admin token | The "master" password of programs that administer |
| Ingest token | The password tests use to send results (it cannot administer) |
| `admin` user password | Sign in to the browser as administrator |
| `equipo` (team) user password | Sign in to the browser **only to look** |
| Encryption key | Encrypt the stored screenshots |

💻 Paste **the whole block** at once:

```powershell
function New-Secret { $b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); -join ($b | ForEach-Object { $_.ToString("x2") }) }
$b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); $dbkey = [Convert]::ToBase64String($b)
$lines = @(
  "# TraceReports - trial",
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

✅ **You should see** the users and their passwords (64 letters and digits each):

```
TRACEREPORTS_UI_USER=admin
TRACEREPORTS_UI_PASSWORD=3f9a0c...e71b
TRACEREPORTS_UI_VIEWER_USER=equipo
TRACEREPORTS_UI_VIEWER_PASSWORD=8b21dd...04aa
```

To see them later: `Get-Content C:\prueba-docker\tracereports\.env`.

> These passwords are **only for this trial** and are deleted in step 22. Still, do not share them.

What each line does: see the table in [step 4 of the installation guide](docker-step-by-step.md#step-4--create-the-passwords-and-the-env-file).

---

## Part C — Start

### Step 7 — Build and start the trial

💻

```powershell
docker compose up -d
```

✅ **You should see** it download and build (1 to 3 minutes the first time) and finish like this. Note that
**everything says `tracereports-prueba`**: it is the trial, not your installation.

```
 ✔ Image tracereports:prueba                    Built
 ✔ Network tracereports-prueba_default          Created
 ✔ Volume tracereports-prueba-data              Created
 ✔ Container tracereports-prueba-tracereports-1 Started
```

⚠️ **If you see** `port is already allocated`: your installation is still running. Go back to step 3.

---

### Step 8 — Check that it is healthy

💻

```powershell
docker compose ps
```

✅ **You should see** (real output):

```
NAME                                 IMAGE                 STATUS                    PORTS
tracereports-prueba-tracereports-1   tracereports:prueba   Up 12 seconds (healthy)   127.0.0.1:8080->8080/tcp, [::1]:8080->8080/tcp
```

If it says `(health: starting)`, wait 20 seconds and retry. If it says **`Restarting`**, see step 9.

---

### Step 9 — Look at the logs

💻

```powershell
docker compose logs --tail 25 tracereports
```

✅ **You should see** (real output; the dates change):

```
INFO evidence encrypted at rest (screenshots, traces, videos, DOM) key_id=1ad8abcbce9c5cd3
INFO screenshot policy screenshots=failures
INFO failure diagnosis: local rules engine (no LLM, no network); ...
INFO egress: no external destination configured (nothing leaves this server)
INFO retention enabled days=90
INFO TraceReports listening url=http://localhost:8080 bind=""
```

⚠️ **If you see** `refusing to start` or a message about `DB_KEY`: the `.env` is wrong. Repeat **step 6** and
then `docker compose up -d`.

---

## Part D — Sign in and check

### Step 10 — Sign in as administrator

1. Open the browser at **http://localhost:8080**
2. Username: `admin`. Password: the value of `TRACEREPORTS_UI_PASSWORD` in the trial's `.env`.

✅ **You should see** the TraceReports screen, **empty** (the trial has no results yet). This is how it looks
with data:

![TraceReports main screen with a run and its tests](../img/docker-guide/inicio.webp)

---

### Step 11 — Check security

In the browser: **Settings → Security check**.

![Settings, Security check with the checks in green](../img/docker-guide/postura.webp)

And from the command line (works from any folder):

💻

```powershell
docker exec tracereports-prueba-tracereports-1 /tracereports doctor --security
```

✅ **You should see** (real output) that it ends with:

```
11 ok, 1 warn, 0 fail
```

What matters is **`0 fail`**. The **Network exposure** `WARN` is normal with Docker on your computer (see
[installation guide, step 9](docker-step-by-step.md#step-9--check-security)).

<details>
<summary><b>Optional: turn "Network exposure" green</b></summary>

On your computer you are already protected: `docker-compose.yml` publishes the port only for `127.0.0.1`, so
nobody on your network reaches it. If you also want TraceReports to refuse any connection that does not come
from the Docker network, limit it to that network. Do it in **the same terminal window**, inside the
`tracereports` folder:

1. Look up your installation's network:

   💻

   ```powershell
   $red = docker network inspect tracereports-prueba_default --format "{{(index .IPAM.Config 0).Subnet}}"; $red
   ```

   ✅ **You should see** something like `172.18.0.0/16` (the numbers may differ).

2. Save it in the `.env`:

   ```powershell
   $e = Get-Content .env | Where-Object { $_ -notmatch '^TRACEREPORTS_ALLOWED_CIDRS=' }; $e += "TRACEREPORTS_ALLOWED_CIDRS=$red"; [IO.File]::WriteAllLines("$PWD\.env", $e, (New-Object Text.UTF8Encoding($false)))
   ```

3. Apply it:

   ```powershell
   docker compose up -d
   ```

4. Check it:

   ```powershell
   curl.exe -s -o NUL -w "%{http_code}" http://localhost:8080/api/v1/runs
   ```

   ✅ **`401`** (asks for login): fine, the filter lets you through. Reload **Settings → Security check**:
   "Network exposure" moves to **In order** ("only from 172.18.0.0/16").

   ❌ **`403`**: your Docker shows connections from another address. Find it in the log:

   ```powershell
   docker compose logs tracereports | Select-String "not in TRACEREPORTS_ALLOWED_CIDRS"
   ```

   The line ends with `ip=…`. Repeat step 2 with that IP, for example after `$red = "192.168.65.1/32"`, and
   step 3.

**To undo it:**

```powershell
$e = Get-Content .env | Where-Object { $_ -notmatch '^TRACEREPORTS_ALLOWED_CIDRS=' }; [IO.File]::WriteAllLines("$PWD\.env", $e, (New-Object Text.UTF8Encoding($false)))
```

and then `docker compose up -d`.

<details><summary>macOS / Linux</summary>

```bash
red=$(docker network inspect tracereports-prueba_default --format '{{(index .IPAM.Config 0).Subnet}}'); echo "$red"
grep -v '^TRACEREPORTS_ALLOWED_CIDRS=' .env > .env.tmp && echo "TRACEREPORTS_ALLOWED_CIDRS=$red" >> .env.tmp && mv .env.tmp .env
docker compose up -d
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/v1/runs
```

With `403`: `docker compose logs tracereports | grep "not in TRACEREPORTS_ALLOWED_CIDRS"`. To undo it:
`grep -v '^TRACEREPORTS_ALLOWED_CIDRS=' .env > .env.tmp && mv .env.tmp .env` and `docker compose up -d`.

</details>
</details>

In **Settings → Artificial intelligence** you will see **"An administrator locked these settings"**: that is
`TRACEREPORTS_SETTINGS_LOCKED=1`, as it should be.

![AI settings locked by the administrator](../img/docker-guide/ia-bloqueada.webp)

---

### Step 12 — Sign in as the read-only user

1. Open a **private window** (`Ctrl`+`Shift`+`N` in Chrome or Edge).
2. Go to **http://localhost:8080**.
3. Username: `equipo`. Password: `TRACEREPORTS_UI_VIEWER_PASSWORD` from the trial's `.env`.

✅ It sees the reports, but in **Settings** the **Security check** says **"No access"**:

![Settings with the read-only login](../img/docker-guide/viewer.webp)

Close the private window.

---

## Part E — Send the first test

### Step 13 — Prepare Python (second window)

1. Open **another** PowerShell window (keep the first one open).
2. Paste:

💻

```powershell
mkdir C:\prueba-docker\mis-pruebas
cd C:\prueba-docker\mis-pruebas
python -m venv .venv
.venv\Scripts\Activate.ps1
pip install pytest C:\prueba-docker\tracereports\client\python
```

✅ **You should see** the line start with `(.venv) PS C:\prueba-docker\mis-pruebas>` and `pip` end with
`Successfully installed ... pytest ... tracereports ...`.

⚠️ **If you see** `running scripts is disabled on this system`: paste
`Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`, answer **Y** and repeat the last two lines.

---

### Step 14 — Create an example test

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

✅ The file `test_first_report.py` is listed. One test passes and the other **fails on purpose**.

---

### Step 15 — Run the test and send the results

🎯 Tests use the **ingest token**. The block **reads it by itself** from the trial's `.env`.

💻 Paste **the whole block**:

```powershell
$env:TRACEREPORTS_URL = "http://127.0.0.1:8080"
$env:TRACEREPORTS_TOKEN = ((Get-Content C:\prueba-docker\tracereports\.env | Select-String '^TRACEREPORTS_INGEST_TOKEN=').Line -split '=', 2)[1]
"Token loaded: " + $env:TRACEREPORTS_TOKEN.Length + " characters"
pytest --tracereports --tracereports-run "My first report"
```

✅ **You should see** `Token loaded: 64 characters` and, at the end:

```
test_first_report.py .F                                                         [100%]
...
TraceReports: http://127.0.0.1:8080/#run=1&view=dashboard
FAILED test_first_report.py::test_cart_total - AssertionError: the total does not include the right tax
========================= 1 failed, 1 passed in 0.11s =========================
```

> **1 failure** is expected: we did it on purpose.

⚠️ **If you see** `Token loaded: 0 characters`: it did not find `C:\prueba-docker\tracereports\.env` (step 6).

---

### Step 16 — See the report

In the browser (as `admin`), reload the page (`F5`). Open **"My first report" → `test_cart_total`**:

![Report with the failed test and the local engine diagnosis](../img/docker-guide/primer-run.webp)

✅ The local engine's **diagnosis** says 120 was expected and `(100 + 19)` was obtained.

---

## Part F — Security checks

In the **second** window (the Python one).

### Step 17 — Screenshots are stored encrypted

💻 Paste **the whole block** (it creates a test image and sends it with the ingest token):

```powershell
$T = ((Get-Content C:\prueba-docker\tracereports\.env | Select-String '^TRACEREPORTS_INGEST_TOKEN=').Line -split '=', 2)[1]
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

Check how it was stored:

```powershell
docker exec tracereports-prueba-tracereports-1 /tracereports evidence status
```

✅ **You should see** (real output):

```
files: 1 encrypted (current key 1ad8abcbce9c5cd3)
DOM snapshots: none
```

In the browser, open **"Test with screenshot" → "Login fails"**: the blue screenshot **shows normally**, even
though it is encrypted on disk.

⚠️ **If you see** `missing or invalid API token`: the token was not read. Check step 15 (`Token loaded`).

---

### Step 18 — The ingest token cannot administer

💻 (`$H` from step 17 is still defined):

```powershell
try { Invoke-RestMethod -Headers $H -Uri "$B/security/posture" } catch { $_.Exception.Response.StatusCode.value__ }
```

✅ **You should see:** `403` (forbidden). Correct.

---

### Step 19 — The brake on wrong passwords (do it last)

> ⚠️ It blocks **your browser** for 15 minutes. To unblock it earlier:
> `docker restart tracereports-prueba-tracereports-1`.

💻

```powershell
1..10 | ForEach-Object { curl.exe -s -o NUL -w "%{http_code} " -u "admin:wrong-password" http://127.0.0.1:8080/api/v1/runs }
""
```

✅ **You should see** `401` ten times. Now with the **right** password (the block reads it from the `.env`):

```powershell
$P = ((Get-Content C:\prueba-docker\tracereports\.env | Select-String '^TRACEREPORTS_UI_PASSWORD=').Line -split '=', 2)[1]
curl.exe -s -o NUL -w "%{http_code}`n" -u "admin:$P" http://127.0.0.1:8080/api/v1/runs
```

✅ **You should see:** `429` (too many attempts), even with the right password. That is the brake working.

---

## Part G — Leave everything as it was

**One line at a time** in this whole part.

### Step 20 — Delete the trial

> ⚠️ The `-v` deletes the data volume. It is safe **only** in `C:\prueba-docker\tracereports`, where it
> deletes just the trial's data (`tracereports-prueba-data`). **Never** run it in your installation's folder.

💻

```powershell
deactivate
```

(Leaves the Python environment. If it says `deactivate` is not recognized, never mind: go on.)

```powershell
cd C:\prueba-docker\tracereports
```

```powershell
docker compose down -v
```

✅ **You should see** (real output), **everything with `prueba`**:

```
 ✔ Container tracereports-prueba-tracereports-1 Removed
 ✔ Network tracereports-prueba_default          Removed
 ✔ Volume tracereports-prueba-data              Removed
```

⚠️ If you see `tracereports-data` **without** `prueba`, stop and check step 5.

```powershell
docker image rm tracereports:prueba
```

✅ **You should see:** `Untagged: tracereports:prueba`.

---

### Step 21 — Start your installation again

🎯 Start the installation you stopped in step 3, with all its data. Use the folder you wrote down in
step 3.1:

```powershell
cd C:\dev\tracereports
```

```powershell
docker compose start
```

```powershell
docker compose ps
```

✅ **You should see** `tracereports-tracereports-1` **`Up`**. Open http://localhost:8080 and you will see your
usual data.

⚠️ **If you see** `has no container to start`: you are not in your installation's folder. Repeat the `cd`
with the folder from step 3.1.

---

### Step 22 — Delete the trial folder

First, **close** everything that may have the folder open: the **second** PowerShell window, **Notepad** if
you opened the `.env`, and any **File Explorer** window in `C:\prueba-docker`.

```powershell
cd C:\
```

```powershell
Remove-Item -Recurse -Force C:\prueba-docker
```

✅ Nothing is printed: the folder was deleted.

⚠️ **If it says** the folder `is being used by another process`: something is still open. Close this window,
open a new one and run only `Remove-Item -Recurse -Force C:\prueba-docker`.

🎉 **Done!** You tried TraceReports end to end and your installation is as it was.

---

## Troubleshooting

| What you see | What to do |
| --- | --- |
| `no configuration file provided: not found` | You are outside `C:\prueba-docker\tracereports`. `cd` into it or use the guide's `docker exec ...` commands |
| `port is already allocated` | Your installation is still running: step 3 |
| `refusing to start` in the logs | The trial's `.env` is wrong: step 6 and `docker compose up -d` |
| `missing or invalid API token` | The token was not read from the `.env`: check the `Token loaded` line of step 15 |
| `429` | 10 wrong passwords in 15 minutes: `docker restart tracereports-prueba-tracereports-1` |
| `has no container to start` | You are in another folder: go to the folder from step 3.1 |
| `C:\prueba-docker` cannot be deleted | Something has it open: close terminals, Notepad and File Explorer |

---

## Checklist

- [ ] 3 — Current installation found (folder written down) and stopped
- [ ] 5 — Trial separated (`tracereports-prueba`)
- [ ] 6 — Trial `.env` created
- [ ] 7–9 — Container `Up (healthy)` and logs with `evidence encrypted at rest`
- [ ] 10–12 — `admin` signs in, `doctor` with `0 fail`, `equipo` can look but not administer
- [ ] 15–16 — Test sent and report visible
- [ ] 17–19 — Encrypted screenshot, `403` and `429`
- [ ] 20–22 — Trial deleted, your installation running with its data and the trial folder removed
