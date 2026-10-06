# Configuración

🌐 [English](../en/configuration.md) · **Español**

Todo se configura con variables de entorno, y todas son opcionales. Lo más cómodo es un archivo
`.env` (ver [`.env.example`](../../.env.example)): lo leen tanto Docker Compose como el binario
(`./tracereports` o `go run ./cmd`, desde la carpeta donde lo ejecutas). Las variables ya
definidas en el sistema mandan sobre el archivo. `TRACEREPORTS_ENV_FILE` cambia la ruta del archivo.

Si la pantalla de Ajustes está en solo lectura, ella misma muestra un `.env` completo de ejemplo
(con datos de prueba) y los pasos para aplicarlo.

## Referencia

| Variable | Por defecto | Para qué sirve |
|---|---|---|
| `PORT` | `8080` | Puerto HTTP |
| `DATA_DIR` | `./data` | Base de datos SQLite y capturas |
| `TRACEREPORTS_TOKEN` | — | Token obligatorio para **escribir** en la API (los clientes de tests) |
| `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` | — | Login (HTTP Basic) para **ver** los reportes |
| `TRACEREPORTS_ALLOWED_HOSTS` | — | Sin login de UI: nombres por los que se atiende además de `localhost` (separados por coma; `*` = cualquiera). Ver [Seguridad](#seguridad) |
| `TRACEREPORTS_LOCAL_ADMIN` | — | `1`: con token y sin login, el mismo equipo (sin proxy) puede cambiar Ajustes y usar las acciones de la UI |
| `AI_PROVIDER` | se deduce de la key | `gemini`, `anthropic`, `openai`, `openai_compatible` u `ollama` |
| `AI_MODEL` | el del proveedor | Modelo (ver la tabla de proveedores) |
| `AI_API_KEY` | — | API key del proveedor (Ollama no la usa) |
| `AI_BASE_URL` | la del proveedor | URL de la API: proxy, endpoint propio, servicio compatible u Ollama remoto |
| `GEMINI_API_KEY` / `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `OLLAMA_HOST` | — | Alternativa a las `AI_*`: con una sola de estas el proveedor se elige solo |
| `TRACEREPORTS_ENV_FILE` | `.env` | Archivo `CLAVE=valor` que el servidor lee al arrancar |
| `TRACEREPORTS_SETTINGS_LOCKED` | — | `1`: la pantalla de Ajustes queda de solo lectura; la IA se configura solo con el entorno |
| `TEAMS_WEBHOOK_URL` | — | Envía el resumen de cada ejecución a Microsoft Teams |
| `SLACK_WEBHOOK_URL` | — | Envía el resumen de cada ejecución a Slack |
| `PUBLIC_URL` | — | URL pública del servidor, para el botón *Ver reporte* de las notificaciones |
| `NOTIFY_ON` | `always` | `always`, o `failures` para avisar solo si hubo fallos |
| `NETWORK_MAX_BODY_KB` | `256` | Máximo guardado por cada *response body* de red; `0` = no guardar bodies |
| `TRACEREPORTS_REDACT` | activo | `off` guarda los secretos tal como llegan (no recomendado) |
| `TRACEREPORTS_REDACT_HEADERS` / `TRACEREPORTS_REDACT_KEYS` | — | Más headers y claves (JSON, query, formulario) a enmascarar, separados por coma |
| `TRACEREPORTS_REDACT_PATTERNS` | — | Expresiones regulares extra, separadas por `;` (p. ej. un RUT: `\b\d{7,8}-[\dkK]\b`) |
| `TRACEREPORTS_RETENTION_DAYS` | — | Borra las ejecuciones (y sus capturas) más antiguas que N días |
| `TRACEREPORTS_AI_MAX_PER_RUN` | `50` | Diagnósticos automáticos con IA por ejecución; el resto queda como *no analizado* y se puede analizar a mano. `0` = sin límite |

## Seguridad

Sin configuración, el servidor está **abierto**: cualquiera que llegue al puerto puede ver y
escribir reportes. Esto sirve en tu máquina, pero no en un servidor compartido.

```bash
TRACEREPORTS_TOKEN=un-token-largo-y-aleatorio      # genéralo con: python -c "import secrets; print(secrets.token_hex(24))"
TRACEREPORTS_UI_USER=qa
TRACEREPORTS_UI_PASSWORD=una-clave-segura
```

- **`TRACEREPORTS_TOKEN`**: las escrituras (`POST`/`PATCH`) exigen `Authorization: Bearer <token>` o el
  header `X-TraceReports-Token`. Los clientes lo leen de la variable `TRACEREPORTS_TOKEN` o, si no
  está definida, del `.env` del proyecto (ver abajo). El token también permite leer, por ejemplo para
  descargar el ZIP desde CI.
- **Ajustes → Conectar tus tests** te guía con el token: lo genera en el navegador, te da la línea
  del `.env`, los comandos para PowerShell, macOS/Linux, GitHub Actions y GitLab CI con tu URL, y
  verifica que el servidor lo acepte.
- **`TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD`**: el navegador pide usuario y clave para ver la UI y
  la API de lectura.
- **Ajustes desde la UI** (proveedor de IA, idioma por defecto): se pueden cambiar con el login de
  la UI o con el token. Sin login ni token (modo local), también desde el mismo equipo donde corre
  el servidor, conectado directo: se mira la conexión real y una petición que llega por un proxy
  (`X-Forwarded-For`, `X-Real-IP`, `Forwarded`) nunca cuenta como local. Con `TRACEREPORTS_TOKEN` el
  servidor se considera desplegado y el mismo equipo también necesita el login, salvo que actives
  `TRACEREPORTS_LOCAL_ADMIN=1`. `TRACEREPORTS_SETTINGS_LOCKED=1` los bloquea del todo.
  **Con Docker** hace falta el login aunque abras `localhost`: la conexión llega desde la red de
  Docker, no desde el mismo equipo.
- **Host permitido (DNS rebinding)**: sin login de la UI, el servidor solo atiende las peticiones
  sin credenciales que llegan con un `Host` local (`localhost`, `127.0.0.1`, `::1`), el de
  `PUBLIC_URL` o uno de `TRACEREPORTS_ALLOWED_HOSTS`. Así una página maliciosa abierta en tu
  navegador no puede leer tus reportes haciendo que su dominio apunte a tu equipo. Las peticiones
  con un token válido, y todas cuando hay login de UI, no dependen del `Host`. Si sirves sin login
  por otro nombre (una IP de la red, un proxy con dominio, el alias de un servicio de CI), añádelo:
  `TRACEREPORTS_ALLOWED_HOSTS=reportes.lan,192.168.1.10`. La respuesta `403 host … is not allowed`
  indica qué falta.
- Expón el servidor detrás de HTTPS (reverse proxy como Caddy, nginx o Traefik). HTTP Basic sin
  TLS viaja en texto plano.

### El `.env` del proyecto en los clientes

Los clientes Python, JavaScript y Java leen `TRACEREPORTS_*` del archivo `.env`
del proyecto cuando la variable no está en el entorno, con las mismas reglas que el servidor. Así el
token vive en un solo lugar, ignorado por git, y tus tests lo usan sin definirlo en cada terminal ni
de forma global en el sistema.

- Se busca desde la carpeta desde donde corres los tests hacia arriba, sin pasar de la raíz del
  repositorio (la carpeta con `.git`).
- La variable del entorno siempre gana: en CI, pasa el token como *secret* y no subas el `.env`.
- Del archivo solo se usan las claves `TRACEREPORTS_*`: tus keys de IA no salen de ahí.
- `TRACEREPORTS_ENV_FILE=ruta/al/archivo` usa otro archivo; `TRACEREPORTS_ENV_FILE=off` no lee
  ninguno.

Las capturas de red se enmascaran en el cliente antes de enviarse: headers `Authorization`,
`Cookie` y `Set-Cookie`, tokens, contraseñas y RUT. Aun así, **no reportes datos personales
reales** desde ambientes productivos.

## Diagnóstico con IA

Funciona con cualquiera de estos proveedores:

| Proveedor | `AI_PROVIDER` | Modelo por defecto | Notas |
|---|---|---|---|
| Google Gemini | `gemini` | `gemini-flash-lite-latest` | Capa gratuita. Key en <https://aistudio.google.com/apikey> |
| Anthropic Claude | `anthropic` | `claude-opus-5-5` | Diagnósticos más precisos, de pago por uso. Key en <https://platform.claude.com/settings/keys> |
| OpenAI | `openai` | `gpt-5-mini` | De pago por uso |
| Compatible con OpenAI | `openai_compatible` | — (obligatorio) | Groq, OpenRouter, DeepSeek, Mistral, LM Studio, vLLM… Requiere `AI_BASE_URL` terminada en `/v1` |
| Ollama | `ollama` | `llama3.1` | Local: los datos de los tests **no salen de tu red**. Sin key |

```bash
# Gemini (lo más simple: basta la key)
GEMINI_API_KEY=...

# Claude
AI_PROVIDER=anthropic
AI_API_KEY=sk-ant-...
# AI_MODEL=claude-sonnet-5-5         # más económico

# Ollama en tu máquina (antes: ollama pull llama3.1)
AI_PROVIDER=ollama
# AI_BASE_URL=http://host.docker.internal:11434   # si TraceReports corre en Docker

# Groq, OpenRouter, LM Studio…
AI_PROVIDER=openai_compatible
AI_BASE_URL=https://api.groq.com/openai/v1
AI_MODEL=llama-3.3-70b-versatile
AI_API_KEY=...
```

Con Claude, TraceReports activa el *fallback* del servidor de Anthropic: si el modelo se niega a
analizar un fallo por sus políticas, la misma llamada lo responde un modelo alternativo.

### ¿En el `.env` o en la pantalla de Ajustes?

Las dos sirven, y se pueden combinar:

- **`.env`**: la configuración base. Es la mejor opción en servidores, Docker y CI: queda
  versionada, se revisa en el repositorio de infraestructura y no depende de que alguien entre a
  la UI.
- **Ajustes → Inteligencia artificial**: cambia el proveedor sin reiniciar el servidor, con
  *Probar conexión* antes de guardar. Lo guardado ahí **manda sobre el `.env`**; *Volver a la
  configuración del .env* lo deshace. La API key se guarda en la base de datos del servidor
  (`DATA_DIR`) y nunca se envía al navegador (la UI solo muestra sus últimos 4 caracteres).

En producción: configura con el `.env` y usa `TRACEREPORTS_SETTINGS_LOCKED=1` para que nadie la cambie
desde la UI. Para probar proveedores en tu máquina, la pantalla es más rápida.

El **idioma de los diagnósticos** (automático, español o inglés) también se elige en Ajustes.

Qué hace:

- **Por test** (cuando termina en `FAIL`): categoría (`LOCATOR_CHANGED`, `BACKEND_TIMEOUT`,
  `LOGIC_BUG`, `INFRA_ERROR`), resumen y sugerencia. Usa el mensaje de error, el stack trace, el
  último paso y las **llamadas de backend que fallaron**.
- **Por ejecución** (al cerrarla): agrupa los fallos en incidentes por causa probable y escribe un
  titular y un resumen para el equipo.

Sin proveedor de IA no hay texto de IA, pero los fallos **igual se agrupan** por causa: la llamada de
backend que falló, la categoría o el mensaje de error.

El análisis corre en segundo plano y aparece en la UI apenas termina. Con la capa gratuita se
Las solicitudes se procesan de a pocas y se reintentan automáticamente si se excede el límite del
proveedor.

## Idioma de la interfaz

Español o inglés, en **Ajustes → Apariencia**. Cada navegador recuerda el suyo; quien puede editar
los ajustes puede fijar el idioma por defecto del equipo. Sin elección, se usa el del navegador.

## Notificaciones a Teams y Slack

Al cerrar cada ejecución (`PATCH /runs/{id}/finish`) se envía:

- estado y estadísticas;
- el titular del diagnóstico y las principales causas con su acción sugerida;
- la comparación con la ejecución anterior (fallos nuevos, arreglados, flaky);
- un botón **Ver reporte** si definiste `PUBLIC_URL`.

**Microsoft Teams** (webhook de Workflows):
1. En el canal: **⋯ → Workflows → "Post to a channel when a webhook request is received"**.
2. Elige el equipo y el canal, y copia la URL que genera.
3. Defínela en `TEAMS_WEBHOOK_URL`.

**Slack**:
1. Crea una app en <https://api.slack.com/apps> → **Incoming Webhooks** → actívalos.
2. **Add New Webhook to Workspace**, elige el canal y copia la URL.
3. Defínela en `SLACK_WEBHOOK_URL`.

## Tickets en GitHub, Jira o Azure DevOps

Desde **Escalar**, el botón **Ticket** abre un issue con el resumen que estás viendo (qué pasó,
causa probable, evidencia y próximos pasos), el error, las llamadas al backend que fallaron y el
link al reporte (con `PUBLIC_URL`). En Jira y Azure DevOps la captura del fallo va como adjunto;
en GitHub, por el link.

**No duplica:** si el mismo test ya tiene un ticket en ese tracker (aunque sea de otra ejecución,
por ejemplo la de anoche), te muestra ese y ofrece *Crear otro igual*. Los tokens se quedan en el
servidor: la interfaz solo sabe qué trackers hay.

| Variable | Qué es |
| --- | --- |
| `TRACEREPORTS_GITHUB_REPO` / `TRACEREPORTS_GITHUB_TOKEN` | `dueño/repo` y un token con permiso de escritura en issues |
| `TRACEREPORTS_GITHUB_API` | API de GitHub Enterprise (default `https://api.github.com`) |
| `TRACEREPORTS_GITHUB_LABELS` | Labels separados por coma (default `bug`) |
| `TRACEREPORTS_JIRA_URL` / `TRACEREPORTS_JIRA_PROJECT` | `https://tuempresa.atlassian.net` y la clave del proyecto (`SHOP`) |
| `TRACEREPORTS_JIRA_TOKEN` + `TRACEREPORTS_JIRA_EMAIL` | Jira Cloud: API token y el correo de su dueño. Jira Server/Data Center: solo el token personal (sin correo) |
| `TRACEREPORTS_JIRA_ISSUE_TYPE` / `TRACEREPORTS_JIRA_LABELS` | Tipo (default `Bug`) y labels (default `tracereports`) |
| `TRACEREPORTS_AZURE_URL` / `TRACEREPORTS_AZURE_PROJECT` | `https://dev.azure.com/tuorg` y el proyecto |
| `TRACEREPORTS_AZURE_TOKEN` | Personal access token con *Work Items: Read & write* |
| `TRACEREPORTS_AZURE_TYPE` / `TRACEREPORTS_AZURE_TAGS` | Tipo de work item (default `Bug`: el resumen va en *Repro Steps*) y tags |

Un tracker aparece en la interfaz solo si tiene todas sus variables obligatorias. Por API:
`POST /api/v1/ui/tickets` con `{run_id, test_id, provider, audience, lang, force}` y
`GET /api/v1/runs/{id}/tickets` (ver la [API](api.md)).

## Correlación con los logs del backend

Cuando una llamada trae un id de traza o de request en sus headers, el detalle de la llamada (pestaña
**Red**) lo muestra con botones **Ver logs** y **Ver traza**, y los tickets lo incluyen. Se
reconocen W3C `traceparent`/`traceresponse`, B3 (Zipkin), Jaeger `uber-trace-id`, AWS X-Ray,
Datadog, Google Cloud y `X-Request-Id`, `X-Correlation-Id`, `Request-Id`, `cf-ray` y similares
(primero los de la respuesta, que son los que usó el backend).

Los links salen de dos plantillas:

| Variable | Ejemplo |
| --- | --- |
| `TRACEREPORTS_TRACE_URL` | Jaeger: `https://jaeger.acme.com/trace/{trace_id}` · Datadog APM: `https://app.datadoghq.com/apm/trace/{trace_id}` |
| `TRACEREPORTS_LOGS_URL` | Datadog: `https://app.datadoghq.com/logs?query=%40http.request_id%3A{request_id}&from_ts={from}&to_ts={to}` · Kibana: `https://kibana.acme.com/app/discover#/?_g=(time:(from:'{from_iso}',to:'{to_iso}'))&_a=(query:(language:kuery,query:'request.id:"{request_id}"'))` |

Valores disponibles (ya codificados para una URL): `{trace_id}`, `{request_id}`, `{from}` y `{to}`
(epoch en ms: 2 minutos antes y después de la llamada), `{from_s}` y `{to_s}` (en segundos),
`{from_iso}` y `{to_iso}`, `{host}`, `{path}`, `{method}` y `{status}`. Para Grafana (Loki, Tempo)
abre Explore con una búsqueda de ejemplo, copia la URL y reemplaza el valor por `{trace_id}`.

Una plantilla que usa un id que la llamada no trae no genera link: nunca abre una búsqueda vacía.
Los ids y los links también quedan en el reporte exportado.

## Cuarentena de tests flaky

Un test inestable conocido se puede poner **en cuarentena** desde su detalle (botón *Poner en
cuarentena*, en tests que fallan o están marcados como flaky): motivo obligatorio, dueño y
vencimiento (7, 14, 30 o 90 días). Mientras dura:

- sus fallos **se siguen viendo y contando**, pero no ponen la ejecución en rojo: si solo fallan
  tests en cuarentena, la ejecución queda en amarillo (`WARNING`), con el contador `quarantined`;
- aplica a las siguientes ejecuciones del mismo **proyecto** (por la identidad del test) y a la que
  estás viendo, que se recalcula al momento;
- al **vencer** deja de aplicar sola: un test no puede quedar escondido para siempre. El chip
  pasa a *Cuarentena vencida*.

El comentario del PR también distingue los fallos en cuarentena. Por API: `POST /api/v1/ui/quarantine`,
`DELETE /api/v1/ui/quarantine/{test_id}` y `GET /api/v1/quarantine?project=…` (ver la [API](api.md)).

## Historial, flaky y comparación

No requieren configuración. Se calculan con los datos que ya existen:

- **Identidad**: un test se identifica por su **key** (el `nodeid` en pytest, `paquete/Test` en
  Go), no por el nombre visible. Un test reportado sin key (API REST sin el campo, datos de antes
  de esta versión) se identifica por su nombre y la UI marca su historial como *aproximado*.
- **Contexto**: solo se comparan ejecuciones del mismo **proyecto, ambiente y rama**. Staging no
  se mezcla con producción ni una rama de feature con main.
- **Historial**: la última ejecución de esa identidad en cada ejecución del contexto.
- **Estabilidad** (últimas 20 ejecuciones del contexto; `WARNING` cuenta como no fallado y `SKIP`
  se ignora):
  - **inestable (flaky)**: alterna al menos dos veces entre pasar y fallar en las últimas 10, o
    pasó solo tras reintentar. Con menos de 5 ejecuciones se muestra como *posible* (pocos datos);
  - **falla persistente**: falla en sus últimas 3 o más ejecuciones seguidas. Es una regresión, no
    inestabilidad;
  - el porcentaje que se muestra es la **tasa de fallo**, con su muestra (*falló 7 de 20*).
- **Comparación**: contra la ejecución anterior del mismo contexto que comparte tests. Si la rama
  no tiene una anterior, contra la última del mismo proyecto y ambiente en otra rama (la UI lo
  dice). En el dashboard puedes elegir otra base.

## Agrupamiento de fallos (incidentes)

Cada fallo va a un incidente según la evidencia, nunca solo por la categoría de la IA:

1. **Llamada al backend que lo explica**: la que falló justo antes del fallo (hasta 2 minutos
   antes). Gana un error del servidor (sin respuesta, 5xx, 408, 429); un 4xx solo cuenta si la
   aplicación no siguió funcionando con ese host después. Las respuestas **esperadas** nunca
   cuentan. La firma incluye método, **host**, ruta normalizada y resultado.
2. Si no, la **firma del error**: tipo de excepción, mensaje sin números ni ids y el frame más
   interno del código del proyecto.

Todos los fallos quedan en algún incidente. La IA describe como máximo los 8 más grandes (el resto
se muestra con su evidencia) y su causa se presenta como **hipótesis**, con la evidencia en que se
basa; si la evidencia no alcanza, lo dice en vez de inventar una causa.

## Datos sensibles y retención

El servidor enmascara antes de guardar: headers de credenciales (`Authorization`, `Cookie`,
`Set-Cookie`, `X-API-Key`…), claves sensibles en JSON, query strings y formularios (`password`,
`token`, `secret`, `api_key`, `session`… y las que terminan así), tokens `Bearer`, JWT y
credenciales dentro de URLs. Se aplica a la red, los pasos, los errores, las descripciones, el
snapshot del DOM y a los nombres e identidades (nombre, key, suite y categoría del test; nombre,
proyecto, entorno y rama de la ejecución), venga del cliente que venga. Una key con un secreto se
guarda enmascarada más un hash con clave propia de la instalación, para que dos tests que solo
difieren en el secreto no mezclen su historial. Como lo guardado ya está enmascarado, tampoco llega
a la IA, al ZIP ni a Teams/Slack (y los prompts y el ZIP pasan además por el mismo filtro).

Límites: reconoce secretos por su clave o por formas inequívocas. Un dato sensible escrito como
texto libre sin clave, o visible en una **captura de pantalla**, no se detecta: usa
`TRACEREPORTS_REDACT_PATTERNS`, `--tracereports-no-screenshots`, `--tracereports-no-dom` o
`NETWORK_MAX_BODY_KB=0` según el caso, y `TRACEREPORTS_RETENTION_DAYS` para no acumular evidencia.

## Diagnóstico con IA después de un reinicio

Los diagnósticos que quedaron a medias (estado *pendiente*) se retoman al arrancar el servidor. Un
mismo test no se analiza dos veces a la vez ni se vuelve a pagar si ya tiene diagnóstico (salvo
*Re-analizar*). Si el resumen de la ejecución se escribió con diagnósticos aún pendientes, lo dice y
se reescribe cuando terminan.
