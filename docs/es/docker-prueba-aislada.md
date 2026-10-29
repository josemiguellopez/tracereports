# Probar TraceReports con Docker sin tocar tu instalación

🌐 [English](../en/docker-isolated-trial.md) · **Español**

Guía para **probar una versión de TraceReports con Docker, de 0 a 100, cuando ya tienes otra instalada**:
la nueva se instala en otra carpeta, con su propio nombre y sus propios datos; pruebas todo (logins,
cifrado, tu primera prueba, las protecciones de seguridad) y **al final se deja todo como estaba**, con tu
instalación de siempre encendida y sus datos intactos.

> ¿Es tu primera instalación? Sigue [Instalar con Docker, paso a paso](docker-paso-a-paso.md).

**Cómo usarla:** haz los pasos **en orden**. Cada paso tiene:

- 🎯 **Qué vas a hacer** y para qué.
- 💻 **El comando** para copiar y pegar en PowerShell.
- ✅ **Deberías ver**: lo que aparece si salió bien.
- ⚠️ **Si ves esto**: qué hacer si algo sale distinto.

> **Tiempo:** unos 45 minutos. La guía está escrita para **Windows (PowerShell)**.
>
> **Pega los bloques tal cual.** Ninguno tiene textos de ejemplo para reemplazar: las claves se leen solas
> del `.env` de la prueba. Cuando un paso diga **"una línea a la vez"**, pega y ejecuta cada bloque por
> separado: si pegas varias juntas, a veces dos se unen en una y el comando falla.

---

## Índice

- [Parte A — Preparar](#parte-a--preparar) (pasos 1 a 4)
- [Parte B — Configurar](#parte-b--configurar) (pasos 5 y 6)
- [Parte C — Arrancar](#parte-c--arrancar) (pasos 7 a 9)
- [Parte D — Entrar y revisar](#parte-d--entrar-y-revisar) (pasos 10 a 12)
- [Parte E — Enviar la primera prueba](#parte-e--enviar-la-primera-prueba) (pasos 13 a 16)
- [Parte F — Pruebas de seguridad](#parte-f--pruebas-de-seguridad) (pasos 17 a 19)
- [Parte G — Dejar todo como estaba](#parte-g--dejar-todo-como-estaba) (pasos 20 a 22)
- [Problemas frecuentes](#problemas-frecuentes) · [Checklist](#checklist)

---

## Parte A — Preparar

### Paso 1 — Abrir PowerShell

🎯 PowerShell es la ventana donde escribes los comandos.

1. Presiona la tecla **Windows**.
2. Escribe **PowerShell** y presiona **Enter**.

Para **pegar** un comando: clic derecho, o `Ctrl`+`V`. Luego **Enter**.

> En la Parte E abrirás una **segunda** ventana. Hasta entonces, usa siempre esta.

---

### Paso 2 — Comprobar que Docker está encendido

💻

```powershell
docker --version
docker compose version
docker info --format "{{.ServerVersion}}"
```

✅ **Deberías ver** tres líneas, por ejemplo:

```
Docker version 29.8.1, build ...
Docker Compose version v5.5.1
29.8.1
```

⚠️ **Si ves** `Cannot connect to the Docker daemon` o la tercera línea da error: abre **Docker Desktop** y
espera a que diga **Engine running**. Si dice `docker no se reconoce`, cierra PowerShell, ábrelo de nuevo y
repite.

---

### Paso 3 — Encontrar y detener tu instalación actual (sin borrar nada)

🎯 Tu instalación actual usa el puerto 8080: la **detenemos** mientras probamos. No se borra nada; sus datos
quedan en su volumen.

**3.1 — Encuentra tu instalación** (dónde está y si está encendida):

💻

```powershell
docker ps -a --filter "label=com.docker.compose.service=tracereports" --format "{{.Names}}|{{.Status}}|{{.Labels}}" | ForEach-Object { $n, $s, $l = $_ -split '\|', 3; "$n  ($s)  carpeta: " + (($l -split ',' | Where-Object { $_ -like 'com.docker.compose.project.working_dir=*' }) -replace '^[^=]*=', '') }
```

✅ **Deberías ver** algo como:

```
tracereports-tracereports-1  (Up 14 minutes)  carpeta: C:\dev\tracereports
```

> 📝 **Anota esa carpeta**: la necesitas al final (paso 21) para volver a encender tu instalación.

**3.2 — Ve a esa carpeta** (cambia la ruta por la que te mostró el comando) y **detén** la instalación.
Una línea a la vez:

```powershell
cd C:\dev\tracereports
```

```powershell
docker compose stop
```

✅ **Deberías ver:** `Container tracereports-tracereports-1  Stopped`.

Comprueba que el puerto quedó libre:

```powershell
docker ps
```

✅ La lista está **vacía** (solo los títulos), o no aparece nada con `8080`.

---

### Paso 4 — Descargar TraceReports en una carpeta de prueba

🎯 Descargar el proyecto en una carpeta aparte, `C:\prueba-docker`, como lo haría alguien nuevo.

💻

```powershell
mkdir C:\prueba-docker
cd C:\prueba-docker
git clone https://github.com/josemiguellopez/tracereports.git
cd tracereports
git switch dev
dir docker-compose.yml, .env.example
```

> **`git switch dev` es obligatorio por ahora:** mientras la próxima versión no esté publicada, las funciones
> de esta guía (login de solo lectura, cifrado de la evidencia, `doctor --security`) están en la rama `dev`.
> Sin ese comando instalarías la versión anterior y no verías esas novedades.

✅ **Deberías ver** que `git clone` descarga el proyecto (`Resolving deltas: 100% ... done.`) y, al final,
los dos archivos listados. `git switch dev` responde `Switched to a new branch 'dev'`.

⚠️ **Si ves** `Ya existe un elemento con el nombre especificado`: quedó una carpeta de una prueba anterior.
Bórrala con `Remove-Item -Recurse -Force C:\prueba-docker` y repite el paso.

> **Regla importante:** los comandos que empiezan con `docker compose` **solo funcionan dentro de
> `C:\prueba-docker\tracereports`** (donde está el `docker-compose.yml`). Si estás en otra carpeta responden
> `no configuration file provided: not found`. Los comandos de verificación de esta guía usan
> `docker exec ...`, que funciona desde cualquier carpeta.

---

## Parte B — Configurar

### Paso 5 — Separar la prueba de tu instalación (muy importante)

🎯 Docker identifica cada instalación por el **nombre de su carpeta**. Como esta también se llama
`tracereports`, Docker creería que es la misma que la tuya y **reemplazaría tu contenedor y usaría tus
datos**. Este archivo le da a la prueba **su propio nombre, su propia imagen y su propio volumen de datos**.

💻 Pega **todo el bloque** de una vez:

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

Comprueba que quedó separada:

```powershell
docker compose config --format json | Select-String '"name": "tracereports-prueba'
```

✅ **Deberías ver** tres líneas, **todas con `tracereports-prueba`**:

```
  "name": "tracereports-prueba",
      "name": "tracereports-prueba_default",
      "name": "tracereports-prueba-data"
```

⚠️ Si **no** aparecen esas líneas, **no sigas**: revisa que estás en `C:\prueba-docker\tracereports` y repite
el paso.

---

### Paso 6 — Crear las contraseñas y el `.env` de la prueba

🎯 TraceReports **no arranca sin contraseñas**. Este bloque las crea al azar y arma el archivo de
configuración `.env` de la prueba:

| Qué | Para qué |
| --- | --- |
| Token de administración | Contraseña "maestra" de los programas que administran |
| Token de ingesta | Contraseña que usan las pruebas para enviar resultados (no puede administrar) |
| Clave del usuario `admin` | Entrar al navegador como administrador |
| Clave del usuario `equipo` | Entrar al navegador **solo para mirar** |
| Clave de cifrado | Cifrar las capturas de pantalla guardadas |

💻 Pega **todo el bloque** de una vez:

```powershell
function New-Secret { $b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); -join ($b | ForEach-Object { $_.ToString("x2") }) }
$b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); $dbkey = [Convert]::ToBase64String($b)
$lines = @(
  "# TraceReports - prueba",
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

✅ **Deberías ver** los usuarios y sus claves (64 letras y números cada una):

```
TRACEREPORTS_UI_USER=admin
TRACEREPORTS_UI_PASSWORD=3f9a0c...e71b
TRACEREPORTS_UI_VIEWER_USER=equipo
TRACEREPORTS_UI_VIEWER_PASSWORD=8b21dd...04aa
```

Para verlas más adelante: `Get-Content C:\prueba-docker\tracereports\.env`.

> Son claves **solo de esta prueba** y se borran en el paso 22. Aun así, no las compartas.

Qué hace cada línea: ver la tabla del [paso 4 de la guía de instalación](docker-paso-a-paso.md#paso-4--crear-las-contraseñas-y-el-archivo-env).

---

## Parte C — Arrancar

### Paso 7 — Construir y arrancar la prueba

💻

```powershell
docker compose up -d
```

✅ **Deberías ver** que descarga y construye (1 a 3 minutos la primera vez) y termina así. Fíjate que **todo
dice `tracereports-prueba`**: es la prueba, no tu instalación.

```
 ✔ Image tracereports:prueba                    Built
 ✔ Network tracereports-prueba_default          Created
 ✔ Volume tracereports-prueba-data              Created
 ✔ Container tracereports-prueba-tracereports-1 Started
```

⚠️ **Si ves** `port is already allocated`: tu instalación sigue encendida. Vuelve al paso 3.

---

### Paso 8 — Comprobar que está sano

💻

```powershell
docker compose ps
```

✅ **Deberías ver** (salida real):

```
NAME                                 IMAGE                 STATUS                    PORTS
tracereports-prueba-tracereports-1   tracereports:prueba   Up 12 seconds (healthy)   127.0.0.1:8080->8080/tcp, [::1]:8080->8080/tcp
```

Si dice `(health: starting)`, espera 20 segundos y repite. Si dice **`Restarting`**, mira el paso 9.

---

### Paso 9 — Mirar los registros (logs)

💻

```powershell
docker compose logs --tail 25 tracereports
```

✅ **Deberías ver** (salida real; las fechas cambian):

```
INFO evidence encrypted at rest (screenshots, traces, videos, DOM) key_id=1ad8abcbce9c5cd3
INFO screenshot policy screenshots=failures
INFO failure diagnosis: local rules engine (no LLM, no network); ...
INFO egress: no external destination configured (nothing leaves this server)
INFO retention enabled days=90
INFO TraceReports listening url=http://localhost:8080 bind=""
```

⚠️ **Si ves** `refusing to start` o un mensaje con `DB_KEY`: el `.env` no quedó bien. Repite el **paso 6** y
luego `docker compose up -d`.

---

## Parte D — Entrar y revisar

### Paso 10 — Entrar como administrador

1. Abre el navegador en **http://localhost:8080**
2. Usuario: `admin`. Contraseña: el valor de `TRACEREPORTS_UI_PASSWORD` del `.env` de la prueba.

✅ **Deberías ver** la pantalla de TraceReports **vacía** (la prueba todavía no tiene resultados). Así se ve
con datos:

![Pantalla principal de TraceReports con una ejecución y sus tests](../img/docker-guide/inicio.webp)

---

### Paso 11 — Revisar la seguridad

En el navegador: **Ajustes → Revisión de seguridad**.

![Ajustes, Revisión de seguridad con los chequeos en verde](../img/docker-guide/postura.webp)

Y por comando (funciona desde cualquier carpeta):

💻

```powershell
docker exec tracereports-prueba-tracereports-1 /tracereports doctor --security
```

✅ **Deberías ver** (salida real) que termina con:

```
11 ok, 1 warn, 0 fail
```

Lo importante es **`0 fail`**. El `WARN` de **Network exposure** es normal con Docker en tu equipo (ver
[guía de instalación, paso 9](docker-paso-a-paso.md#paso-9--revisar-la-seguridad)).

<details>
<summary><b>Opcional: dejar en verde «Exposición en la red»</b></summary>

En tu equipo ya estás protegido: el `docker-compose.yml` publica el puerto solo para `127.0.0.1`, así que nadie
de tu red llega a él. Si además quieres que TraceReports rechace cualquier conexión que no venga de la red de
Docker, limítalo a esa red. Hazlo en **la misma ventana** de la terminal, dentro de la carpeta `tracereports`:

1. Mira la red de tu instalación:

   💻

   ```powershell
   $red = docker network inspect tracereports-prueba_default --format "{{(index .IPAM.Config 0).Subnet}}"; $red
   ```

   ✅ **Deberías ver** algo como `172.18.0.0/16` (los números pueden ser otros).

2. Guárdala en el `.env`:

   ```powershell
   $e = @(Get-Content .env | Where-Object { $_ -notmatch '^TRACEREPORTS_ALLOWED_CIDRS=' }); $e += "TRACEREPORTS_ALLOWED_CIDRS=$red"; [IO.File]::WriteAllLines("$PWD\.env", $e, (New-Object Text.UTF8Encoding($false)))
   ```

3. Aplícalo:

   ```powershell
   docker compose up -d
   ```

4. Compruébalo:

   ```powershell
   curl.exe -s -o NUL -w "%{http_code}" http://localhost:8080/api/v1/runs
   ```

   ✅ **`401`** (pide login): está bien, el filtro te deja pasar. Recarga **Ajustes → Revisión de seguridad**:
   "Exposición en la red" pasa a **En orden** ("solo desde 172.18.0.0/16").

   ❌ **`403`**: tu Docker muestra las conexiones desde otra dirección. Búscala en el log:

   ```powershell
   docker compose logs tracereports | Select-String "not in TRACEREPORTS_ALLOWED_CIDRS"
   ```

   La línea termina con `ip=…`. Repite el paso 2 con esa IP, por ejemplo después de `$red = "192.168.65.1/32"`, y
   el paso 3.

**Para deshacerlo:**

```powershell
$e = @(Get-Content .env | Where-Object { $_ -notmatch '^TRACEREPORTS_ALLOWED_CIDRS=' }); [IO.File]::WriteAllLines("$PWD\.env", $e, (New-Object Text.UTF8Encoding($false)))
```

y después `docker compose up -d`.

<details><summary>macOS / Linux</summary>

```bash
red=$(docker network inspect tracereports-prueba_default --format '{{(index .IPAM.Config 0).Subnet}}'); echo "$red"
grep -v '^TRACEREPORTS_ALLOWED_CIDRS=' .env > .env.tmp && echo "TRACEREPORTS_ALLOWED_CIDRS=$red" >> .env.tmp && mv .env.tmp .env
docker compose up -d
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/v1/runs
```

Con `403`: `docker compose logs tracereports | grep "not in TRACEREPORTS_ALLOWED_CIDRS"`. Para deshacerlo:
`grep -v '^TRACEREPORTS_ALLOWED_CIDRS=' .env > .env.tmp && mv .env.tmp .env` y `docker compose up -d`.

</details>
</details>

En **Ajustes → Inteligencia artificial** verás **"El administrador bloqueó estos ajustes"**: es por
`TRACEREPORTS_SETTINGS_LOCKED=1`, como debe ser.

![Ajustes de IA bloqueados por el administrador](../img/docker-guide/ia-bloqueada.webp)

---

### Paso 12 — Entrar como usuario de solo lectura

1. Abre una **ventana privada** (`Ctrl`+`Shift`+`N` en Chrome o Edge).
2. Ve a **http://localhost:8080**.
3. Usuario: `equipo`. Contraseña: `TRACEREPORTS_UI_VIEWER_PASSWORD` del `.env` de la prueba.

✅ Ve los reportes, pero en **Ajustes** la **Revisión de seguridad** dice **"Sin acceso"**:

![Ajustes con el login de solo lectura](../img/docker-guide/viewer.webp)

Cierra la ventana privada.

---

## Parte E — Enviar la primera prueba

### Paso 13 — Preparar Python (segunda ventana)

1. Abre **otra** ventana de PowerShell (deja la primera abierta).
2. Pega:

💻

```powershell
mkdir C:\prueba-docker\mis-pruebas
cd C:\prueba-docker\mis-pruebas
python -m venv .venv
.venv\Scripts\Activate.ps1
pip install pytest C:\prueba-docker\tracereports\client\python
```

✅ **Deberías ver** que la línea empieza con `(.venv) PS C:\prueba-docker\mis-pruebas>` y que `pip` termina
con `Successfully installed ... pytest ... tracereports ...`.

⚠️ **Si ves** `la ejecución de scripts está deshabilitada en este sistema`: pega
`Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`, responde **S** y repite las dos últimas líneas.

---

### Paso 14 — Crear una prueba de ejemplo

💻 Pega **todo el bloque**:

```powershell
@'
def test_suma_basica():
    """La suma de dos numeros da el resultado esperado."""
    assert 2 + 2 == 4


def test_total_del_carrito():
    """El total del carrito incluye el impuesto."""
    precio, impuesto = 100, 19
    assert precio + impuesto == 120, "el total no incluye el impuesto correcto"
'@ | Set-Content -Encoding utf8 test_primer_reporte.py
dir test_primer_reporte.py
```

✅ Se lista el archivo `test_primer_reporte.py`. Una prueba pasa y la otra **falla a propósito**.

---

### Paso 15 — Correr la prueba y enviar los resultados

🎯 Las pruebas usan el **token de ingesta**. El bloque lo **lee solo** del `.env` de la prueba.

💻 Pega **todo el bloque**:

```powershell
$env:TRACEREPORTS_URL = "http://127.0.0.1:8080"
$env:TRACEREPORTS_TOKEN = ((Get-Content C:\prueba-docker\tracereports\.env | Select-String '^TRACEREPORTS_INGEST_TOKEN=').Line -split '=', 2)[1]
"Token cargado: " + $env:TRACEREPORTS_TOKEN.Length + " caracteres"
pytest --tracereports --tracereports-run "Mi primer reporte"
```

✅ **Deberías ver** `Token cargado: 64 caracteres` y al final (salida real):

```
test_primer_reporte.py .F                                                       [100%]
...
TraceReports: http://127.0.0.1:8080/#run=1&view=dashboard
FAILED test_primer_reporte.py::test_total_del_carrito - AssertionError: el total no incluye el impuesto correcto
========================= 1 failed, 1 passed in 0.11s =========================
```

> Que **1 falle** es lo esperado: lo hicimos a propósito.

⚠️ **Si ves** `Token cargado: 0 caracteres`: no encontró `C:\prueba-docker\tracereports\.env` (paso 6).

---

### Paso 16 — Ver el reporte

En el navegador (como `admin`), recarga la página (`F5`). Abre **"Mi primer reporte" → `test_total_del_carrito`**:

![Reporte con el test fallido y el diagnóstico del motor local](../img/docker-guide/primer-run.webp)

✅ El **diagnóstico** del motor local dice que se esperaba 120 y se obtuvo `(100 + 19)`.

---

## Parte F — Pruebas de seguridad

En la **segunda** ventana (la de Python).

### Paso 17 — Las capturas se guardan cifradas

💻 Pega **todo el bloque** (crea una imagen de prueba y la envía con el token de ingesta):

```powershell
$T = ((Get-Content C:\prueba-docker\tracereports\.env | Select-String '^TRACEREPORTS_INGEST_TOKEN=').Line -split '=', 2)[1]
$B = "http://127.0.0.1:8080/api/v1"
$H = @{ Authorization = "Bearer $T" }
Add-Type -AssemblyName System.Drawing
$bmp = New-Object Drawing.Bitmap 400, 200
$g = [Drawing.Graphics]::FromImage($bmp); $g.Clear([Drawing.Color]::SteelBlue)
$g.DrawString("captura de prueba", (New-Object Drawing.Font "Arial", 20), [Drawing.Brushes]::White, 20, 80)
$bmp.Save("$PWD\captura.png", [Drawing.Imaging.ImageFormat]::Png); $g.Dispose(); $bmp.Dispose()
$run  = Invoke-RestMethod -Method Post -Headers $H -ContentType "application/json" -Uri "$B/runs" -Body '{"name":"Prueba con captura"}'
$test = Invoke-RestMethod -Method Post -Headers $H -ContentType "application/json" -Uri "$B/runs/$($run.run_id)/tests" -Body '{"name":"Login falla"}'
curl.exe -s -H "Authorization: Bearer $T" -F "file=@captura.png" -F "status=FAIL" -F "message=pantalla del fallo" "$B/tests/$($test.test_id)/screenshot"
Invoke-RestMethod -Method Patch -Headers $H -ContentType "application/json" -Uri "$B/tests/$($test.test_id)/finish" -Body '{"status":"FAIL","error_message":"TimeoutError: no apareció el dashboard"}' | Out-Null
Invoke-RestMethod -Method Patch -Headers $H -Uri "$B/runs/$($run.run_id)/finish" | Out-Null
```

✅ La línea de `curl.exe` responde un texto que empieza con `{"log":` y termina con `"url":"/screenshots/..."`.

Revisa cómo quedó guardada:

```powershell
docker exec tracereports-prueba-tracereports-1 /tracereports evidence status
```

✅ **Deberías ver** (salida real):

```
files: 1 encrypted (current key 1ad8abcbce9c5cd3)
DOM snapshots: none
```

En el navegador, abre **"Prueba con captura" → "Login falla"**: la captura azul **se ve normal**, aunque en
el disco está cifrada.

⚠️ **Si ves** `missing or invalid API token`: el token no se leyó. Revisa el paso 15 (`Token cargado`).

---

### Paso 18 — El token de ingesta no puede administrar

💻 (sigue definido `$H` del paso 17):

```powershell
try { Invoke-RestMethod -Headers $H -Uri "$B/security/posture" } catch { $_.Exception.Response.StatusCode.value__ }
```

✅ **Deberías ver:** `403` (prohibido). Correcto.

---

### Paso 19 — El freno a las claves equivocadas (hazlo al final)

> ⚠️ Bloquea **tu navegador** 15 minutos. Para desbloquearlo antes:
> `docker restart tracereports-prueba-tracereports-1`.

💻

```powershell
1..10 | ForEach-Object { curl.exe -s -o NUL -w "%{http_code} " -u "admin:clave-mala" http://127.0.0.1:8080/api/v1/runs }
""
```

✅ **Deberías ver** diez veces `401`. Ahora con la clave **correcta** (el bloque la lee sola del `.env`):

```powershell
$P = ((Get-Content C:\prueba-docker\tracereports\.env | Select-String '^TRACEREPORTS_UI_PASSWORD=').Line -split '=', 2)[1]
curl.exe -s -o NUL -w "%{http_code}`n" -u "admin:$P" http://127.0.0.1:8080/api/v1/runs
```

✅ **Deberías ver:** `429` (demasiados intentos), aunque la clave sea correcta. Así funciona el freno.

---

## Parte G — Dejar todo como estaba

**Una línea a la vez** en toda esta parte.

### Paso 20 — Borrar la prueba

> ⚠️ El `-v` borra el volumen de datos. **Solo** en `C:\prueba-docker\tracereports` es seguro, porque ahí
> borra únicamente los datos de la prueba (`tracereports-prueba-data`). **Nunca** lo ejecutes en la carpeta
> de tu instalación.

💻

```powershell
deactivate
```

(Sale del entorno de Python. Si responde que `deactivate` no se reconoce, no importa: sigue.)

```powershell
cd C:\prueba-docker\tracereports
```

```powershell
docker compose down -v
```

✅ **Deberías ver** (salida real), **todo con `prueba`**:

```
 ✔ Container tracereports-prueba-tracereports-1 Removed
 ✔ Network tracereports-prueba_default          Removed
 ✔ Volume tracereports-prueba-data              Removed
```

⚠️ Si ves `tracereports-data` **sin** `prueba`, detente y revisa el paso 5.

```powershell
docker image rm tracereports:prueba
```

✅ **Deberías ver:** `Untagged: tracereports:prueba`.

---

### Paso 21 — Volver a encender tu instalación

🎯 Encender la instalación que detuviste en el paso 3, con todos sus datos. Usa la carpeta que anotaste en
el paso 3.1:

```powershell
cd C:\dev\tracereports
```

```powershell
docker compose start
```

```powershell
docker compose ps
```

✅ **Deberías ver** `tracereports-tracereports-1` en estado **`Up`**. Abre http://localhost:8080 y verás tus
datos de siempre.

⚠️ **Si ves** `has no container to start`: no estás en la carpeta de tu instalación. Repite el `cd` con la
carpeta del paso 3.1.

---

### Paso 22 — Borrar la carpeta de prueba

Antes, **cierra** todo lo que pueda tener la carpeta abierta: la **segunda** ventana de PowerShell, el
**Bloc de notas** si abriste el `.env`, y cualquier ventana del **Explorador** en `C:\prueba-docker`.

```powershell
cd C:\
```

```powershell
Remove-Item -Recurse -Force C:\prueba-docker
```

✅ No muestra nada: la carpeta se borró.

⚠️ **Si ves** `está siendo utilizado en otro proceso`: todavía hay algo abierto. Cierra esta ventana, abre
una nueva y ejecuta solo `Remove-Item -Recurse -Force C:\prueba-docker`.

🎉 **¡Listo!** Probaste TraceReports de 0 a 100 y tu instalación quedó como estaba.

---

## Problemas frecuentes

| Lo que ves | Qué hacer |
| --- | --- |
| `no configuration file provided: not found` | Estás fuera de `C:\prueba-docker\tracereports`. Haz `cd` a esa carpeta o usa los comandos `docker exec ...` de la guía |
| `port is already allocated` | Tu instalación sigue encendida: paso 3 |
| `refusing to start` en los logs | El `.env` de la prueba no quedó bien: paso 6 y `docker compose up -d` |
| `missing or invalid API token` | El token no se leyó del `.env`: revisa la línea `Token cargado` del paso 15 |
| `429` | 10 claves malas en 15 minutos: `docker restart tracereports-prueba-tracereports-1` |
| `has no container to start` | Estás en otra carpeta: ve a la carpeta del paso 3.1 |
| No se puede borrar `C:\prueba-docker` | Algo la tiene abierta: cierra terminales, Bloc de notas y Explorador |

---

## Checklist

- [ ] 3 — Instalación actual encontrada (carpeta anotada) y detenida
- [ ] 5 — Prueba separada (`tracereports-prueba`)
- [ ] 6 — `.env` de la prueba creado
- [ ] 7–9 — Contenedor `Up (healthy)` y logs con `evidence encrypted at rest`
- [ ] 10–12 — `admin` entra, `doctor` con `0 fail`, `equipo` ve pero no administra
- [ ] 15–16 — Prueba enviada y reporte visible
- [ ] 17–19 — Captura cifrada, `403` y `429`
- [ ] 20–22 — Prueba borrada, tu instalación encendida con sus datos y la carpeta de prueba eliminada
