# Guía de hardening

🌐 [English](../en/security-hardening.md) · **Español**

Cómo dejar TraceReports listo para un ambiente con evidencia sensible: qué configurar, qué hace la
herramienta por su cuenta y qué queda del lado de tu infraestructura. Al final hay un
[checklist](#checklist) para revisar antes de abrirlo a un equipo.

TraceReports guarda evidencia de ejecuciones reales: pasos, capturas, llamadas de red, el DOM y los
errores. Trátalo como cualquier sistema que guarda datos de tus ambientes de prueba.

## Lo que ya viene por defecto

| Control | Detalle |
|---|---|
| No queda abierto a la red | Sin token ni login escucha solo en este equipo; en Docker o con un `TRACEREPORTS_BIND` de red **no arranca**. Ver [Seguridad](configuration.md#seguridad) |
| Diagnóstico sin salida de datos | Sin key de IA diagnostica el [motor local](configuration.md#motor-local-de-diagnóstico-sin-llm): sin red y sin LLM |
| Secretos enmascarados | Headers, cookies, tokens, contraseñas y claves sensibles en JSON, formularios, URLs y el DOM se enmascaran **antes** de guardarse. Ver [Datos sensibles](configuration.md#datos-sensibles-y-retención) |
| Capturas tapadas | Los clientes tapan `input[type=password]` y `[data-tracereports-mask]` antes de capturar |
| Borrado seguro | `secure_delete` en SQLite y `VACUUM` después de cada purga: lo borrado no queda en el archivo |
| Archivos privados | `DATA_DIR` con permisos `0700`; capturas y artefactos `0600`. También el reporte estático (`tracereports report`) y las grabaciones offline de los clientes (Linux y macOS) |
| Protecciones del navegador | Content-Security-Policy (solo scripts propios), `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: same-origin` en cada respuesta |
| Sin CSRF de otros sitios | Una escritura que el navegador marca como de otro sitio (`Origin`, `Sec-Fetch-Site`) recibe `403`: una página web no puede enviar ejecuciones falsas a tu servidor local |
| Freno a la fuerza bruta | 10 logins o tokens fallidos desde una IP en 15 minutos → `429` el resto de la ventana |
| Límites de conexión | Timeouts de lectura (15 min) e inactividad (2 min); el stream en vivo acepta hasta 500 conexiones |
| Key de IA ligada a su URL | Una key guardada o del entorno solo se envía a la URL con que se configuró: cambiar la URL exige escribirla de nuevo |
| Lab en tus hosts | `repro`, `verify` y el MCP solo reenvían a los hosts que llamó el test (más `TRACEREPORTS_LAB_HOSTS`) |
| Imagen mínima | `distroless/static:nonroot`: sin shell, sin paquetes, usuario sin privilegios |
| Releases verificables | Firma cosign, SBOM y provenance. Ver [Verifying a release](../../SECURITY.md#verifying-a-release) |

## Perfil de producción

Un `.env` de partida para un servidor compartido:

```ini
# acceso
TRACEREPORTS_TOKEN=            # openssl rand -hex 32: el token de administración
TRACEREPORTS_INGEST_TOKEN=     # otro distinto, restringido, para los clientes y CI
TRACEREPORTS_UI_USER=qa
TRACEREPORTS_UI_PASSWORD=      # una clave larga
TRACEREPORTS_UI_VIEWER_USER=equipo # login de solo lectura para el resto del equipo
TRACEREPORTS_UI_VIEWER_PASSWORD=   # otra clave larga
TRACEREPORTS_SETTINGS_LOCKED=1 # la configuración solo desde el entorno, nunca desde la UI

# red
TRACEREPORTS_ALLOWED_CIDRS=10.0.0.0/8 # solo las redes que lo necesitan
NOTIFY_DETAIL=minimal                 # Teams/Slack: conteos y un link, sin nombres de tests ni errores

# evidencia
TRACEREPORTS_RETENTION_DAYS=90    # lo que tu política de datos permita
TRACEREPORTS_SCREENSHOTS=failures # o off si en pantalla hay datos que no se pueden mostrar
TRACEREPORTS_ENCRYPT_AT_REST=1    # capturas, traces, videos y DOM cifrados
TRACEREPORTS_DB_KEY_FILE=/run/secrets/tracereports_db_key # openssl rand -base64 32, fuera de DATA_DIR

# diagnóstico sin salida de datos: el motor local (por defecto) o un LLM dentro de tu red
# AI_PROVIDER=builtin
# AI_PROVIDER=ollama
# AI_BASE_URL=http://ollama.interno:11434
```

- **Token e ingest token**: los clientes y el CI usan `TRACEREPORTS_INGEST_TOKEN` (envía resultados y lee,
  nunca administra). `TRACEREPORTS_TOKEN` queda para quien administra. Ver
  [Permisos de cada credencial](configuration.md#permisos-de-cada-credencial).
- **Login de la UI**: protege las lecturas. Sin login, las lecturas solo se aceptan con un `Host` local o
  de `TRACEREPORTS_ALLOWED_HOSTS` (que no es control de acceso: el cliente elige ese header).
- **Dos logins**: `TRACEREPORTS_UI_USER` para quien administra y `TRACEREPORTS_UI_VIEWER_USER` para el
  resto del equipo, que solo lee (no envía resultados, no cambia Ajustes ni usa acciones).
- **`TRACEREPORTS_SETTINGS_LOCKED=1`**: nadie cambia el proveedor de IA ni su key desde la pantalla.
- **HTTPS**: pon el servidor detrás de un proxy con TLS (Caddy, nginx, Traefik). El login HTTP Basic sin
  TLS viaja en claro. Ver [Docker → Desplegar en un servidor](docker.md#desplegar-en-un-servidor).

## Red

- Publica el puerto solo donde haga falta. El `docker-compose.yml` del repositorio lo publica solo en
  `127.0.0.1` y `::1`, pensado para un proxy en el mismo servidor.
- Limita quién llega al puerto con el **firewall** del servidor, del cloud o del proxy: solo la red interna
  desde la que corren los tests y desde la que se mira el reporte.
- Sin login de UI, `TRACEREPORTS_ALLOWED_HOSTS` lista los nombres por los que se entra; un `Host`
  desconocido recibe `403` (protección contra DNS rebinding).
- `TRACEREPORTS_ALLOWED_CIDRS` deja entrar solo a las redes indicadas (`403` a cualquier otra, incluso con
  token); `TRACEREPORTS_TRUSTED_PROXIES` dice de qué proxies se confía la IP real del cliente. Con
  Docker y el proxy en el mismo servidor, el proxy llega desde una IP de la red de Docker: ver
  [Docker → Desplegar en un servidor](docker.md#desplegar-en-un-servidor).
- **Límite de tasa en el proxy**: el servidor frena la fuerza bruta de credenciales, pero no limita las
  peticiones de un cliente con un token válido. En un servidor compartido configura límites en el proxy
  (`limit_req` en nginx, `rate_limit` en Caddy o Traefik) y un tamaño máximo de cuerpo.
- Si el proxy reescribe el header `Host`, las escrituras desde la UI pueden recibir `403 cross-site`:
  configura el proxy para que pase el `Host` original (`proxy_set_header Host $host` en nginx).

## Docker endurecido

El `docker-compose.yml` del repositorio ya trae:

```yaml
read_only: true                 # solo se escribe en /data (el volumen) y /tmp
tmpfs:
  - /tmp:size=256m,mode=1777
cap_drop: [ALL]                 # sin capabilities de Linux
security_opt:
  - no-new-privileges:true      # nada puede ganar privilegios (setuid)
healthcheck:
  test: ["CMD", "/tracereports", "health"]
```

Con `docker run`, lo mismo:

```bash
docker run -d --name tracereports --read-only --tmpfs /tmp:size=256m,mode=1777 --cap-drop ALL \
  --security-opt no-new-privileges:true -p 127.0.0.1:8080:8080 -v tracereports-data:/data \
  --env-file .env ghcr.io/josemiguellopez/tracereports
```

`tracereports health` consulta `GET /healthz`, que responde sin credenciales y sin datos (sirve también
para un balanceador). En Kubernetes: `readOnlyRootFilesystem: true`, `runAsNonRoot: true`,
`allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]` y un `emptyDir` en `/tmp`.

Compruébalo con `tracereports doctor --security` (en el servidor: `docker compose exec tracereports
/tracereports doctor --security`, o desde cualquier lado con `-url`): con este perfil todos los chequeos dan
OK, y sale con código 1 cuando falla uno crítico, así sirve de gate en un pipeline de despliegue.

## Cifrado del volumen

La base (SQLite) y las capturas viven en `DATA_DIR`. Cífralo en el host:

- **Linux**: el volumen o el disco sobre LUKS (`cryptsetup`).
- **Windows**: BitLocker en el disco de `DATA_DIR`.
- **Cloud**: el disco cifrado del proveedor (EBS, Azure Managed Disks, Persistent Disk), con claves
  administradas por tu organización si la política lo pide.

Los **backups** heredan lo que guardan: cífralos también, y aplica la misma retención.

### Cifrado de la evidencia en la aplicación (opcional)

Además del volumen, el servidor puede cifrar por su cuenta la evidencia que más muestra (lo que había en
la pantalla): **capturas, traces, videos y las instantáneas del DOM**. Sirve cuando no controlas el
cifrado del host, o como segunda capa sobre él.

```ini
TRACEREPORTS_ENCRYPT_AT_REST=1
TRACEREPORTS_DB_KEY_FILE=/run/secrets/tracereports_db_key   # o TRACEREPORTS_DB_KEY=<openssl rand -base64 32>
```

- **AES-256-GCM** autenticado, por bloques de 64 KiB: un archivo alterado, truncado o con bloques
  movidos no se sirve. Cada uso tiene su subclave (HKDF-SHA256): archivos y DOM nunca comparten clave.
- **La clave nunca va dentro de `DATA_DIR`**: el servidor no arranca si `TRACEREPORTS_DB_KEY_FILE` apunta
  ahí (un volumen robado llevaría su propia clave). Usa un secret de Docker/Kubernetes, un Key Vault
  montado como archivo o la variable de entorno.
- **Fail-closed**: con `TRACEREPORTS_ENCRYPT_AT_REST=1` y sin una clave válida, el servidor no arranca.
- **La UI, el export ZIP y los tickets** ven la evidencia como siempre (se descifra al leerla; los videos
  se siguen pudiendo adelantar). El ZIP exportado va **descifrado**: quien exporta ya podía verla.
- **Sin la clave la evidencia cifrada no se puede leer**: guárdala como guardas los backups, y por
  separado de ellos.

Las capturas guardadas **antes** de activarlo siguen en claro y se siguen viendo. Para cifrarlas (con el
servidor detenido y un backup de `DATA_DIR`):

```bash
tracereports evidence status              # cuántas hay en claro y con qué clave (su id, nunca la clave)
tracereports evidence encrypt -dry-run
tracereports evidence encrypt
```

**Rotar la clave**: la nueva en `TRACEREPORTS_DB_KEY`, la anterior en `TRACEREPORTS_DB_KEY_PREVIOUS`,
`tracereports evidence rekey`, y al terminar quita la anterior. Mientras tanto el servidor lee con las dos.

**Lo que no cubre**: la base SQLite en sí (nombres de tests, errores, pasos, llamadas de red) sigue sin
cifrar dentro de `DATA_DIR`: el cifrado del volumen sigue haciendo falta. Cifrar SQLite entero en Go
puro (sin CGO) costaba más de lo aceptable en la prueba de concepto (VFS `xts`: +57 % al ingerir en
Linux y consultas 3-4 veces más lentas; además no autentica las páginas). Al reescribir un archivo en
claro, su contenido viejo puede quedar en bloques libres del disco: otra razón para el volumen cifrado.

## Datos de prueba

- Usa **datos sintéticos** en los ambientes donde corren los tests E2E. Ninguna redacción reemplaza a no
  tener el dato.
- No corras tests E2E contra **producción** con datos de personas reales.
- Marca en tu aplicación lo sensible con `data-tracereports-mask` (o lístalo en
  `TRACEREPORTS_SCREENSHOT_MASK`) para que ninguna captura lo muestre. Esa variable va en el
  **entorno donde corren los tests o el CI**: la leen los clientes, que tapan antes de capturar; el
  servidor la ignora.
- Si un dato llega como texto libre sin clave reconocible, agrega un patrón con
  `TRACEREPORTS_REDACT_PATTERNS`.

## Retención y borrado

- `TRACEREPORTS_RETENTION_DAYS` borra cada 6 horas las ejecuciones más antiguas que N días, con sus
  capturas, red, DOM y artefactos. Sin la variable no se borra nada, y el servidor lo avisa al arrancar.
- Lo borrado se sobrescribe (`secure_delete`), la base se compacta (`VACUUM`) y la WAL se vacía: el
  contenido purgado no queda en el archivo `.db`.
- **Fuera del servidor**: las grabaciones offline de los clientes llevan la evidencia **antes** de la
  redacción, y el reporte estático la trae completa. Ambos se crean solo para tu usuario; guárdalos como
  artefactos privados del CI y bórralos cuando ya no hagan falta. Para publicar un reporte con un
  servidor web, dale lectura a propósito (`chmod -R a+rX`).

## Salida de datos

Por defecto TraceReports no envía nada fuera del servidor. Lo que **tú** configures sí sale:

| Integración | Qué envía | Recomendación |
|---|---|---|
| LLM en la nube (Gemini, Claude, OpenAI) | El prompt del diagnóstico, redactado (nunca capturas) | Motor local u Ollama si la evidencia no puede salir |
| Teams / Slack | Resumen de la ejecución: nombres de tests y errores | No los actives en canales donde esos datos no deben estar |
| Tickets (GitHub, Jira, Azure DevOps) | El escalamiento que se crea a mano | Solo con trackers internos |
| `TRACEREPORTS_AI_STATUS_URL` | Una consulta a la página de estado del proveedor | Desactívalo si no usas un LLM en la nube |

- **`TRACEREPORTS_AI_ALLOWED_PROVIDERS=builtin,ollama`** deja solo los proveedores que tu política permite:
  uno fuera de la lista no se usa aunque haya una key en el entorno ni aunque alguien lo elija en Ajustes.
  Valida el proveedor, no su URL: en un servidor compartido usa también `TRACEREPORTS_SETTINGS_LOCKED=1`.
- **`NOTIFY_DETAIL=minimal`** deja los mensajes de Teams y Slack en conteos, estado y un link.
- Al arrancar, el servidor registra cada destino de salida configurado y su alcance (loopback, red privada,
  pública); `tracereports doctor --security` los lista.

## Verificar lo que instalas

Comprueba la firma, el SBOM y la provenance del binario o la imagen antes de instalarlos:
[Verifying a release](../../SECURITY.md#verifying-a-release).

## Checklist

- [ ] `TRACEREPORTS_TOKEN` y `TRACEREPORTS_INGEST_TOKEN` generados con `openssl rand -hex 32`, distintos.
- [ ] Login de la UI (`TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD`) y HTTPS en el proxy.
- [ ] El resto del equipo entra con el login de solo lectura (`TRACEREPORTS_UI_VIEWER_USER`).
- [ ] `TRACEREPORTS_ALLOWED_CIDRS` con las redes que lo necesitan, y límite de tasa en el proxy.
- [ ] `TRACEREPORTS_SETTINGS_LOCKED=1`.
- [ ] Acceso a la red limitado por firewall a la red interna que corresponde.
- [ ] Contenedor con `read_only`, `cap_drop: [ALL]`, `no-new-privileges` y usuario sin privilegios (la imagen
      oficial ya es `nonroot`).
- [ ] `DATA_DIR` sobre un volumen cifrado; backups cifrados.
- [ ] Opcional: `TRACEREPORTS_ENCRYPT_AT_REST=1` con la clave fuera de `DATA_DIR` y respaldada aparte.
- [ ] `TRACEREPORTS_RETENTION_DAYS` según la política de datos.
- [ ] Diagnóstico sin salida de datos: motor local u Ollama/LLM dentro de la red.
- [ ] `TRACEREPORTS_SCREENSHOTS` (`failures` u `off`) en el servidor, y `TRACEREPORTS_SCREENSHOT_MASK`
      con los campos sensibles de la aplicación en el entorno de los tests o del CI.
- [ ] Ambientes de prueba con datos sintéticos; nada de E2E contra producción con datos reales.
- [ ] Grabaciones offline y reportes estáticos como artefactos privados del CI, con su propia retención.
- [ ] `TRACEREPORTS_LAB_HOSTS` solo con los hosts a los que el Lab debe llegar (o `TRACEREPORTS_LAB=off`).
- [ ] Webhooks de Teams/Slack solo en canales donde los nombres de tests y errores pueden estar.
- [ ] Firma y SBOM del release verificados.
- [ ] `tracereports doctor --security` sin FAIL (y los WARN revisados).
- [ ] TraceReports agregado al inventario de activos de tu organización, con su dueño.
