# TraceReports con Docker

🌐 [English](../en/docker.md) · **Español**

Guía para usar TraceReports en tu equipo con Docker y para desplegarlo en un servidor. Si solo
quieres probarlo sin Docker, mira [Instalación](installation.md#opción-2-binario-con-go).

## Cómo funciona

| Pieza | Qué es |
|---|---|
| **Imagen** | TraceReports empaquetado (unos 32 MB): el servidor y la interfaz web. Se construye una vez. |
| **Contenedor** | La imagen en marcha. Queda corriendo en segundo plano y Docker lo vuelve a levantar al reiniciar el equipo (`restart: unless-stopped`). |
| **Volumen** `tracereports-data` | Donde viven la base de datos y las capturas. Sobrevive a parar, borrar o actualizar el contenedor. |
| **`.env`** | Tu configuración (token, login, IA). El contenedor la lee al arrancar. |

`docker-compose.yml` une las cuatro piezas: con un comando construye la imagen, crea el volumen y
arranca el contenedor en el puerto 8080. Ese puerto solo es accesible **desde el mismo equipo**
(`127.0.0.1:8080:8080` y `[::1]:8080:8080`, IPv4 e IPv6): nadie más en tu red puede entrar.

## Requisitos

- **Windows**: [Docker Desktop](https://www.docker.com/products/docker-desktop/) con WSL 2. Si
  Docker Desktop no arranca el motor, abre PowerShell **como administrador**, ejecuta
  `wsl --install --no-distribution` y reinicia el equipo.
- **Linux**: Docker Engine con el plugin `compose`.
- **macOS**: Docker Desktop.

Comprueba que funciona con `docker version`: debe mostrar `Client` y `Server`.

## Primer arranque

> **¿Primera vez con Docker o con la terminal?** Sigue
> [Instalar con Docker, paso a paso](docker-paso-a-paso.md): cada comando, qué deberías ver y qué hacer
> si algo falla, con capturas. ¿Ya tienes TraceReports y quieres probar otra versión sin tocar la tuya?
> [Probar sin tocar tu instalación](docker-prueba-aislada.md).

Son dos pasos, **en este orden**: primero el `.env` con un token y el login, después arrancar el
contenedor. Sin token ni login el contenedor se niega a arrancar.

### Configuración mínima del `.env`

En la carpeta del proyecto, crea el `.env` a partir del ejemplo:

```bash
cp -n .env.example .env   # PowerShell: if (-not (Test-Path .env)) { Copy-Item .env.example .env }
```

> ⚠️ Copia `.env.example` **solo si todavía no tienes un `.env`**: un `cp` sin `-n` reemplaza el
> tuyo y pierdes tus claves. El comando de arriba no lo pisa si ya existe.

Ábrelo con un editor de texto y completa **antes de arrancar** al menos el token y el login.

El contenedor **no arranca sin `TRACEREPORTS_TOKEN` o el login de la UI**: en Docker el servidor
escucha en todas las interfaces del contenedor y quedaría abierto. Configura **token y login también en tu PC**. El puerto solo local evita que entre alguien de tu
red, pero no protege de lo que corre en tu propio equipo (otros programas, una página maliciosa
abierta en el navegador), y tus reportes pueden tener evidencia sensible:

```ini
TRACEREPORTS_TOKEN=un-token-largo-y-aleatorio   # los tests lo envían para escribir
TRACEREPORTS_UI_USER=qa                         # usuario y clave para ver los reportes
TRACEREPORTS_UI_PASSWORD=una-clave-segura
GEMINI_API_KEY=                                 # opcional: diagnóstico con IA
```

Después de cambiar el `.env`, aplica los cambios con `docker compose up -d` (ver [Cambiar el `.env`](#cambiar-el-env),
con ejemplos). Todas las variables están en [Configuración](configuration.md).

Si decides no configurar login en tu PC, el servidor igual se protege: solo atiende peticiones con
`Host` `localhost`/`127.0.0.1` (defensa contra *DNS rebinding*). Con login, la pantalla de Ajustes
también queda editable.

> **Ajustes con Docker:** aunque abras `localhost`, la conexión llega desde la red de Docker y no
> cuenta como «el mismo equipo». Para cambiar la IA o el idioma desde la pantalla de Ajustes,
> configura `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` e inicia sesión.

### Arrancar

Con el `.env` completo:

```bash
docker compose up -d
```

- La primera vez construye la imagen (1 a 3 minutos); después arranca al instante.
- Abre **<http://localhost:8080>** y entra con el login del `.env`.
- `docker compose ps` debe mostrar el contenedor `Up (healthy)`. Si se reinicia una y otra vez,
  `docker compose logs tracereports` dice por qué (por ejemplo `refusing to start`: falta el token o el
  login).

## Cambiar el `.env`

Toda la configuración está en el archivo `.env` de la carpeta `tracereports`. Para cambiar algo: editas el
archivo, lo aplicas y compruebas que se aplicó.

### 1. Editar

**A mano.** Ábrelo con un editor de texto, cambia la línea y guarda:

```powershell
notepad .env
```

En macOS/Linux: `nano .env` (guardar con `Ctrl`+`O`, salir con `Ctrl`+`X`).

- Una variable por línea, sin espacios alrededor del `=` y sin comillas: `TRACEREPORTS_RETENTION_DAYS=90`.
- Si una variable ya está, **cámbiala** en vez de agregarla otra vez: dos líneas con el mismo nombre confunden.
- Las líneas que empiezan con `#` son comentarios: no hacen nada.

**Con un comando.** Cambia (o agrega) una sola variable sin tocar el resto, sin repetirla y sin dañar el
archivo. Cambia `NOMBRE` y `valor`:

```powershell
$e = @(Get-Content .env | Where-Object { $_ -notmatch '^NOMBRE=' }); $e += 'NOMBRE=valor'; [IO.File]::WriteAllLines("$PWD\.env", $e, (New-Object Text.UTF8Encoding($false)))
```

Para **quitar** una variable (vuelve a su valor por defecto):

```powershell
$e = @(Get-Content .env | Where-Object { $_ -notmatch '^NOMBRE=' }); [IO.File]::WriteAllLines("$PWD\.env", $e, (New-Object Text.UTF8Encoding($false)))
```

<details><summary>macOS / Linux</summary>

```bash
# cambiar o agregar
grep -v '^NOMBRE=' .env > .env.tmp && echo 'NOMBRE=valor' >> .env.tmp && mv .env.tmp .env
# quitar
grep -v '^NOMBRE=' .env > .env.tmp && mv .env.tmp .env
```

</details>

> **En Windows PowerShell no uses `Set-Content`, `Out-File` ni `>` para escribir el `.env`.** Según la versión
> lo guardan con otra codificación o con una marca invisible al inicio (BOM), y la primera variable deja de
> leerse sin ningún aviso. El Bloc de notas y los comandos de arriba lo guardan bien.

### 2. Aplicar

```powershell
docker compose up -d
```

Vuelve a crear el contenedor con el `.env` nuevo; los datos se conservan. **`docker compose restart` no
sirve para esto**: reinicia el contenedor con la configuración que tenía.

### 3. Comprobar

```powershell
docker compose logs --tail 20 tracereports
```

Al arrancar, el servidor anuncia lo que tomó: la IA (`AI triage enabled provider=…`), la política de capturas
(`screenshot policy screenshots=…`), la retención, el cifrado, la red permitida y los destinos externos. Nunca
muestra claves ni tokens. Para ver además qué falta por endurecer:

```powershell
docker compose exec tracereports /tracereports doctor --security
```

Si el servidor no arranca después del cambio, el log dice qué variable tiene un valor inválido (por ejemplo
`TRACEREPORTS_SCREENSHOTS must be all, failures or off`).

> **¿Cambiaste la IA y el log sigue mostrando la anterior?** Lo que se guarda desde **Ajustes → Inteligencia
> artificial** manda sobre el `.env`. Ahí mismo, **Volver a la configuración del .env** lo borra y el servidor usa
> de nuevo tus variables. (Con `TRACEREPORTS_SETTINGS_LOCKED=1` no se puede guardar nada desde la pantalla.)

### Ejemplos

Cada uno es el comando para cambiar el `.env` (cópialo tal cual) y después `docker compose up -d`.

| Para | Variable(s) | Qué verás en el log |
|---|---|---|
| Guardar las capturas de todos los tests, no solo de los que fallan | `TRACEREPORTS_SCREENSHOTS=all` | `screenshot policy screenshots=all` |
| Volver a guardar solo las de los fallos (lo recomendado) | `TRACEREPORTS_SCREENSHOTS=failures` | `screenshot policy screenshots=failures` |
| Borrar las ejecuciones de más de 30 días | `TRACEREPORTS_RETENTION_DAYS=30` | `retention enabled days=30` |
| Recibir los diagnósticos de la IA en español | `TRACEREPORTS_AI_LANGUAGE=es` | `diagnosis language lang=es` |
| Apagar el diagnóstico automático | `AI_PROVIDER=off` | `failure diagnosis disabled (AI_PROVIDER=off)` |

**Guardar todas las capturas:**

```powershell
$e = @(Get-Content .env | Where-Object { $_ -notmatch '^TRACEREPORTS_SCREENSHOTS=' }); $e += 'TRACEREPORTS_SCREENSHOTS=all'; [IO.File]::WriteAllLines("$PWD\.env", $e, (New-Object Text.UTF8Encoding($false)))
```

Las capturas de antes del cambio no aparecen: con `failures` el servidor nunca las guardó. En la
**Revisión de seguridad**, "Política de capturas" pasa a *por revisar*: una captura puede mostrar datos
personales o claves en pantalla.

**Usar Ollama (un modelo en tu equipo) para el diagnóstico.** Antes descarga el modelo con
`ollama pull llama3.1:8b`. Cambia cuatro variables de una vez:

```powershell
$e = @(Get-Content .env | Where-Object { $_ -notmatch '^(AI_PROVIDER|AI_MODEL|OLLAMA_HOST|TRACEREPORTS_AI_ALLOWED_PROVIDERS)=' }); $e += 'AI_PROVIDER=ollama', 'OLLAMA_HOST=http://host.docker.internal:11434', 'AI_MODEL=llama3.1:8b', 'TRACEREPORTS_AI_ALLOWED_PROVIDERS=builtin,ollama'; [IO.File]::WriteAllLines("$PWD\.env", $e, (New-Object Text.UTF8Encoding($false)))
```

✅ En el log: `AI triage enabled provider=ollama model=llama3.1:8b`. Dentro del contenedor `localhost` es el
propio contenedor: por eso `host.docker.internal`, que es tu equipo. `TRACEREPORTS_AI_ALLOWED_PROVIDERS` solo
hace falta si ya la tenías (la guía paso a paso la deja en `builtin`).

**Permitir solo la red de Docker:** ver el paso opcional de
[Revisar la seguridad](docker-paso-a-paso.md#paso-9--revisar-la-seguridad).

### Respaldar el `.env`

Guarda la copia **fuera** de la carpeta del proyecto: git ignora el archivo `.env`, pero no las copias con otro
nombre (`.env copy`, `.env.bak`…), y una copia así podría terminar en un commit con tus claves.

```powershell
Copy-Item .env "$HOME\tracereports-env-$(Get-Date -Format yyyyMMdd).txt"
```

En macOS/Linux: `cp .env ~/tracereports-env-$(date +%Y%m%d).txt`. Si usas cifrado en reposo, ese respaldo
también contiene `TRACEREPORTS_DB_KEY`: sin ella no se pueden leer las capturas guardadas.

## Conectar tus tests

Los tests no cambian: apuntan a la URL del servidor y, si configuraste token, lo envían.

> Tus tests toman el token del mismo `.env` del proyecto si no está definido en la terminal: corre
> los tests desde la carpeta del proyecto (o una subcarpeta) y no hace falta nada más. En CI no
> subas el `.env`: pasa `TRACEREPORTS_TOKEN` como *secret* del pipeline, que tiene prioridad.
> [Detalle](configuration.md#el-env-del-proyecto-en-los-clientes).

```bash
TRACEREPORTS_URL=http://localhost:8080
TRACEREPORTS_TOKEN=un-token-largo-y-aleatorio
```

En PowerShell: `$env:TRACEREPORTS_URL="http://localhost:8080"`. En un servidor, usa su URL HTTPS
(`https://reportes.tu-empresa.com`). Detalle por lenguaje: [Python](python.md),
[JavaScript](javascript.md), [Java](java.md), [Go](go.md).

## Uso diario

Ejecuta estos comandos siempre en la carpeta del proyecto. También puedes iniciar, detener y ver los
logs desde Docker Desktop, en la pestaña **Containers**.

| Para | Comando |
|---|---|
| Ver si está corriendo | `docker compose ps` |
| Ver los logs en vivo | `docker compose logs -f` |
| Detenerlo | `docker compose stop` |
| Volver a arrancarlo | `docker compose start` |
| Aplicar cambios del `.env` | `docker compose up -d` (no `restart`: ver [Cambiar el `.env`](#cambiar-el-env)) |
| Actualizar a una versión nueva | `git pull` y luego `docker compose up -d --build` |
| Quitar el contenedor (los datos quedan) | `docker compose down` |

> ⚠️ `docker compose down -v` **borra el volumen con todos los datos**. No lo uses salvo que
> quieras empezar de cero.

## Datos

- Están en el volumen `tracereports-data` (nombre fijo, sea cual sea la carpeta del proyecto). Lo
  puedes confirmar con `docker volume ls`.
- Ese volumen **no** es la carpeta `data/` del proyecto. Lo que reportaste ejecutando el servidor
  sin Docker (`go run`) no aparece en el contenedor, salvo que lo importes (abajo).
- Al actualizar, las migraciones de la base corren solas al arrancar. Haz un backup antes.

### Traer los datos de una instalación sin Docker

Hazlo **al empezar a usar Docker**: el paso 2 reemplaza la base que haya en el volumen.

```powershell
# 1. Crear el volumen y detener el contenedor
docker compose up -d
docker compose stop
# 2. Copiar la carpeta data local al volumen (PowerShell, en la carpeta del proyecto)
docker run --rm -v tracereports-data:/data -v "${PWD}\data:/origen:ro" alpine sh -c "rm -f /data/tracereports.db /data/tracereports.db-wal /data/tracereports.db-shm && cp -a /origen/. /data/ && chown -R 65532:65532 /data"
# 3. Arrancar
docker compose start
```

En Linux/macOS, cambia `"${PWD}\data:/origen:ro"` por `"$PWD/data:/origen:ro"`. Detén antes el
servidor local (`go run`), para que la copia sea consistente. El `chown` deja los archivos al
usuario sin privilegios con el que corre la imagen.

### Backup y restauración

Mientras el servidor corre, **no copies solo el archivo `.db`**: lo más reciente vive en el archivo
`-wal`. Usa el backup en línea de SQLite, que es consistente aunque sigan llegando resultados:

```bash
docker volume create tracereports-backup
docker run --rm -v tracereports-data:/data -v tracereports-backup:/backup alpine sh -c \
  "apk add -q sqlite && sqlite3 /data/tracereports.db '.backup /backup/tracereports.db' \
   && cp -a /data/screenshots /backup/ && chown -R 65532:65532 /backup"
```

Para restaurar, arranca el contenedor sobre el volumen del backup. El detalle y la variante sin
Docker están en [Instalación](installation.md#backup-y-restauración).

## Desplegar en un servidor

Checklist completo para un servidor compartido: [Guía de hardening](security-hardening.md).

1. **Token y login obligatorios.** Configura `TRACEREPORTS_TOKEN`, `TRACEREPORTS_UI_USER` y
   `TRACEREPORTS_UI_PASSWORD` en el `.env`.
2. **Siempre detrás de HTTPS.** HTTP Basic y el token viajan en texto plano sin TLS. Pon un proxy
   inverso (Caddy, nginx, Traefik) en el mismo servidor. El `docker-compose.yml` ya publica el 8080
   solo en local (`127.0.0.1` y `[::1]`), así que el proxy llega y desde fuera no se puede entrar
   directamente. No lo cambies a `"8080:8080"` en un servidor expuesto a Internet.

3. **Backup programado** con el comando de arriba (por ejemplo con cron o el Programador de
   tareas), y opcionalmente `TRACEREPORTS_RETENTION_DAYS` para no acumular evidencia.

Si el servidor no tiene login de UI, añade el dominio a `TRACEREPORTS_ALLOWED_HOSTS` (o define
`PUBLIC_URL`). Si no, el proxy recibe `403 host … is not allowed`. Con login no hace falta.

El proxy debe cumplir tres cosas:

- **Tamaño del body:** al menos 15 MB, el límite de las capturas. nginx trae 1 MB por defecto.
- **Sin buffering en `/api/v1/stream`:** es el modo en vivo (SSE). Con buffering, la interfaz no se
  actualiza sola.
- **Conservar el `Host`**, para la protección contra CSRF.

**Caddy** (certificado de Let's Encrypt automático):

```caddy
reportes.tu-empresa.com {
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

**nginx** (la parte de TLS, `listen 443 ssl` y los certificados, es la habitual de tu servidor):

```nginx
server {
    server_name reportes.tu-empresa.com;
    client_max_body_size 20m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location /api/v1/stream {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_buffering off;
        proxy_read_timeout 1h;
    }
}
```

Toda petición que llega por un proxy (con `X-Forwarded-For`, `X-Real-IP` o `Forwarded`) cuenta
como remota. Por eso los Ajustes y las acciones de la UI piden token o login aunque el proxy esté en
la misma máquina, también con `TRACEREPORTS_LOCAL_ADMIN=1`.

**Con `TRACEREPORTS_ALLOWED_CIDRS` y el proxy en el mismo servidor.** El contenedor no ve llegar al
proxy desde `127.0.0.1`: lo ve desde la IP de la red de Docker (la puerta de enlace del bridge,
normalmente `172.x.x.x`). Esa IP no está en la lista, así que **todo responde `403`**, también con el
login correcto. Para resolverlo:

1. Mira la IP rechazada en el log (aparece una vez por IP):
   ```
   docker compose logs tracereports | grep "TRACEREPORTS_ALLOWED_CIDRS"
   ```
   La línea dice `request refused: source IP not in TRACEREPORTS_ALLOWED_CIDRS … ip=<la IP>`.
2. Pon esa IP en `TRACEREPORTS_TRUSTED_PROXIES` en el `.env`: así se toma la IP real del usuario desde
   `X-Forwarded-For` (y se filtra contra `TRACEREPORTS_ALLOWED_CIDRS`).
3. Aplica con `docker compose up -d`.

Un usuario que inventa `X-Forwarded-For` no se salta el filtro: ese header solo se cree si llega de
una IP de `TRACEREPORTS_TRUSTED_PROXIES`.

## Sin docker compose

```bash
docker build -t tracereports .
docker run -d --name tracereports -p 127.0.0.1:8080:8080 -p "[::1]:8080:8080" -v tracereports-data:/data --env-file .env \
  --restart unless-stopped tracereports
```

Usa el mismo volumen `tracereports-data` que compose: los datos son los mismos con uno u otro.

## Problemas frecuentes

| Síntoma | Causa y solución |
|---|---|
| `docker` no se reconoce | Docker Desktop recién instalado: abre una terminal (o VS Code) nueva. |
| En tu equipo, **Revisión de seguridad** muestra "Exposición en la red" por revisar | Normal con Docker: el contenedor escucha en su red aunque el puerto se publique solo en `127.0.0.1`. Para dejarlo en verde, `TRACEREPORTS_ALLOWED_CIDRS` con la red del contenedor (`docker network inspect <proyecto>_default`): ver el [paso a paso, paso 9](docker-paso-a-paso.md#paso-9--revisar-la-seguridad). |
| `docker version` no muestra `Server` / error 500 | El motor no arrancó. Abre Docker Desktop y espera *Engine running*. En Windows, si falta WSL o la virtualización: `wsl --install --no-distribution` como administrador y reinicia. |
| El contenedor se reinicia y el log dice `refusing to start` | Falta `TRACEREPORTS_TOKEN` o el login de la UI en el `.env`: sin credenciales el servidor no arranca en Docker. Agrégalos (ver [Configuración mínima del `.env`](#configuración-mínima-del-env)) y `docker compose up -d`. |
| `port is already allocated` / el contenedor no arranca | El 8080 está ocupado, por ejemplo por un `go run` del servidor. Detenlo o cambia el puerto (`"8081:8080"`). |
| Ajustes en «solo lectura» | Con Docker hace falta el login de la UI (ver arriba). |
| Los tests no llegan | Revisa `TRACEREPORTS_URL` y el token. `docker compose logs -f` muestra cada petición rechazada. |
| `401 missing or invalid API token` en los tests | El servidor tiene token y los tests no lo encuentran: córrelos desde la carpeta del proyecto (para que lean su `.env`) o define `TRACEREPORTS_TOKEN`. En CI, pásalo como *secret*. |
| `403` en todo detrás del proxy, aun con el login correcto | Con `TRACEREPORTS_ALLOWED_CIDRS`, la IP del proxy (vista desde el contenedor) no está en la lista. Ponla en `TRACEREPORTS_TRUSTED_PROXIES`: ver [Desplegar en un servidor](#desplegar-en-un-servidor). |
| `403 host … is not allowed` | Sin login de UI solo se atiende `localhost`. Si entras por otro nombre o IP, añádelo a `TRACEREPORTS_ALLOWED_HOSTS` o configura el login. |
| Cada petición a `localhost` tarda ~2 s, o los tests quedan «Interrumpido» | En Windows, `localhost` se intenta primero por IPv6. Publica el puerto también en `[::1]` (como el `docker-compose.yml` del proyecto) o usa `http://127.0.0.1:8080`. |
| No se puede entrar desde otro equipo de la red | Es lo esperado: el puerto solo es local. Para compartirlo, usa un proxy HTTPS o cambia a `"8080:8080"` con token y login. |
| Error de permisos tras restaurar o importar | Faltó el `chown -R 65532:65532` sobre el volumen. |
| La interfaz no se actualiza en vivo detrás del proxy | Falta desactivar el buffering en `/api/v1/stream`. |
| Capturas rechazadas detrás del proxy (413) | Sube el límite de body del proxy (`client_max_body_size` en nginx). |
