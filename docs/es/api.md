# API REST

🌐 [English](../en/api.md) · **Español**

Base: `http://<servidor>:8080/api/v1`. Todo es JSON, salvo la subida de capturas (multipart) y la
exportación (ZIP). Los errores responden `{"error": "mensaje"}`.

**Autenticación**: si el servidor tiene `TRACEREPORTS_TOKEN`, las escrituras requieren
`Authorization: Bearer <token>` o `X-TraceReports-Token: <token>`. Si tiene `TRACEREPORTS_UI_USER` /
`TRACEREPORTS_UI_PASSWORD`, las lecturas requieren HTTP Basic o el mismo token. Ver
[configuración](configuration.md#seguridad).

## Flujo mínimo

```bash
H='Authorization: Bearer mi-token'
RUN=$(curl -s -X POST -H "$H" localhost:8080/api/v1/runs -d '{"name":"Smoke","environment":"qa"}' | jq .run_id)
TEST=$(curl -s -X POST -H "$H" localhost:8080/api/v1/runs/$RUN/tests -d '{"name":"Login","category":"smoke"}' | jq .test_id)
curl -s -X POST -H "$H" localhost:8080/api/v1/tests/$TEST/logs -d '{"status":"INFO","message":"Abrir login"}'
curl -s -X POST -H "$H" -F file=@login.png -F message="Formulario" localhost:8080/api/v1/tests/$TEST/screenshot
curl -s -X PATCH -H "$H" localhost:8080/api/v1/tests/$TEST/finish -d '{"status":"FAIL","error_message":"Timeout esperando el Dashboard"}'
curl -s -X PATCH -H "$H" localhost:8080/api/v1/runs/$RUN/finish
```

## Escritura (lo que usan los clientes de tests)

| Método | Ruta | Body | Respuesta |
|---|---|---|---|
| POST | `/runs` | `{name, environment, project, branch, commit, framework, execution_id, planned}` | `201 {run_id}` |
| POST | `/runs/{run_id}/tests` | `{name, category, description, key, suite, params, worker}` | `201 {test_id}` |
| POST | `/tests/{test_id}/logs` | `{status, message, timestamp}` | `201` paso creado |
| POST | `/tests/{test_id}/screenshot` | multipart: `file` (PNG/JPEG/GIF/WEBP), `message`, `status` | `201 {url, log}` |
| POST | `/tests/{test_id}/artifact` | multipart: `file`, `kind` (`trace` o `video`), `name` | `201` artefacto. Trace de Playwright (ZIP) o video (WebM/MP4), hasta 100 MB |
| POST | `/tests/{test_id}/console` | `{entries: [{level, text, location, timestamp}]}` | `201 {stored, dropped}`. Consola del navegador: `error`, `warning`, `pageerror`, `info`, `log`, `debug` (máx. 500 por test) |
| POST | `/tests/{test_id}/network` | `{connections: [Conn]}` (hasta 5000 por lote, 48 MiB por solicitud) | `201 {stored, errors}`. `body_size` es el tamaño original en bytes UTF-8; envía `body_truncated: true` si el body llega ya recortado, para que el reporte no lo muestre como completo |
| POST | `/tests/{test_id}/dom` | Snapshot de la página al fallar, o de un paso con `kind: "step"` (ver abajo), hasta 4 MB / 2000 elementos / 6000 nodos | `201` |
| PATCH | `/tests/{test_id}/finish` | `{status, error_message, error_trace, attempts}` | `200` test |
| PATCH | `/runs/{run_id}/finish` | `{interrupted}` (opcional) | `200` ejecución |
| POST | `/runs/{run_id}/plan` | `{planned}`: cuántos tests recolectó el cliente (progreso y ETA en En vivo) | `200` |

- `status` de un paso: `INFO`, `PASS`, `FAIL`, `WARNING`, `SKIP`. `timestamp`: epoch en ms o
  RFC 3339 (opcional, default ahora).
- `status` de un test: `PASS`, `FAIL`, `WARNING`, `SKIP`, o vacío para deducirlo de los pasos.
  Un `FAIL` dispara el diagnóstico con IA. Un `PASS` con un paso `WARNING` se guarda como `WARNING`,
  salvo con `attempts` > 1 (pasó tras un reintento). El servidor usa además `INTERRUPTED` (no lo envían
  los clientes): un test que se cortó antes de terminar.
- Cerrar la ejecución dispara el diagnóstico global y las notificaciones a Teams/Slack. Los tests
  que sigan en curso se cierran como `INTERRUPTED` (ni fallo ni éxito: fuera de la tasa de éxito, de
  flaky y del gate de Release, y sin diagnóstico con IA) y la ejecución queda `incomplete` (`WARNING`
  si nada falló); con `{"interrupted": true}` también (nunca aparece como exitosa). Cerrar de nuevo una ejecución ya
  cerrada (reintento, spool) conserva la marca `incomplete` y no repite el diagnóstico ni las
  notificaciones. Un resultado que llega después del cierre se acepta y recalcula el estado de la
  ejecución: nunca queda `PASS` con tests `FAIL` o en curso. Si cambia el resultado (estado o
  error), el resumen de la ejecución pasa a pendiente y se reconstruye, con o sin IA, sin volver a
  notificar; un reenvío idéntico no lo toca. Si un test ya terminado recibe otro resultado (otro
  estado, error o traza), su diagnóstico con IA anterior se descarta: un `FAIL` nuevo se vuelve a
  diagnosticar (pendiente mientras tanto) y un `PASS` queda sin diagnóstico de fallo. Pasos, red o DOM que llegan después del cierre se
  guardan en su test, pero no rehacen el resumen (usa *Re-analizar* si hace falta).
- `key`: identidad estable del test dentro del proyecto (sin ella, el historial se arma por nombre).
  Si la key o el nombre traen un secreto (`test_login[token=…]`) se guarda enmascarada y seguida de
  un hash con clave de la instalación: el secreto no se guarda y cada test conserva su historial.
  `project`, `environment` y `branch` definen con qué ejecuciones se compara. `attempts` > 1 indica
  reintentos.
- **Idempotencia**: con el header `Idempotency-Key: <id único>` en un POST/PATCH, repetir el mismo
  request devuelve la primera respuesta sin escribir de nuevo (header `Idempotent-Replayed: true`).
  Úsalo al reintentar: así un timeout no duplica pasos ni capturas. Las claves de lo que pertenece a
  una ejecución duran lo mismo que ella (se borran con la retención); las demás, 24 h. Al actualizar
  desde una versión anterior, las claves ya guardadas se vinculan a su ejecución por la ruta o por la
  respuesta que creó la ejecución; las de ejecuciones ya borradas caducan a las 24 h.
  Garantías ante una caída del servidor: crear ejecuciones y tests, pasos, red, consola, DOM, los
  registros de capturas y artefactos y los veredictos guardan la escritura y su respuesta en **una misma
  transacción**: quedan las dos o ninguna, así que el reintento nunca las duplica. El archivo de una
  captura o artefacto se escribe antes: una caída en medio deja un archivo sin usar, no evidencia
  duplicada. Cerrar tests y ejecuciones y la cuarentena fijan un estado: si se repiten dan
  lo mismo. Las importaciones (JUnit, Allure) usan varias transacciones: una caída a mitad deja una
  ejecución incompleta y el reintento importa de nuevo. Lo que sale del servidor (IA, Teams, Slack,
  tickets) no es *exactamente una vez*.
- `TRACEREPORTS_INGEST_TOKEN` sirve para todas estas escrituras igual que `TRACEREPORTS_TOKEN`, pero no
  para Ajustes ni para las acciones `/ui/*` (ver [Permisos](configuration.md#permisos-de-cada-credencial)).
- Lo que llega se enmascara antes de guardarse (ver [Configuración](configuration.md#datos-sensibles-y-retención)).

`Conn` (todas opcionales salvo `method` y `url`):

```json
{
  "method": "POST", "url": "https://app/api/login", "status": 500, "status_text": "Internal Server Error",
  "resource_type": "fetch", "mime_type": "application/json",
  "failed": false, "error_text": "", "started_at": 1790990000000, "duration_ms": 2310,
  "request_headers": {"content-type": "application/json"}, "post_data": "{\"user\":\"qa\"}",
  "response_headers": {"content-type": "application/json"}, "response_body": "{\"error\":\"...\"}",
  "body_size": 9821, "evidence_file": "C:/evidencia/network_test_login.json"
}
```

Una llamada cuenta como **error** si `failed` es `true` (sin respuesta) o si `status >= 400`, salvo
que venga con `"expected": true`: una respuesta negativa que el test verifica a propósito (no cuenta
como error ni como causa de un fallo).

### Snapshot del DOM

Se envía al fallar, antes de cerrar el test. Con él, si el fallo fue un selector roto, el reporte
propone selectores de reemplazo y la IA elige el más probable. Los clientes ya lo arman
(`capturar_dom` en Python, `domScript` en el ejemplo de Go):

```json
{
  "url": "https://app/login", "title": "Login", "viewport": {"w": 1366, "h": 768},
  "elements": [{"tag": "button", "type": "submit", "text": "Login", "role": "", "testid": "", "testid_attr": "",
                "id": "", "name": "", "label": "", "placeholder": "",
                "visible": true, "x": 315, "y": 560, "w": 464, "h": 46}]
}
```

El cliente de Python también envía `page` (`{w, h}`, la página entera) y `nodes`, la página como árbol
plano que redibuja el **Inspector**: cada nodo tiene `i` (su índice), `p` (el índice del padre, `-1` para
`body`; un padre siempre va antes), `tag`, `attrs`, su `text` propio, su caja (`x`, `y`, `w`, `h`, en
coordenadas de la página), `visible`, `depth`, `tx`/`ty` (dónde empieza el texto dentro de la caja) y
`style`. El servidor deja solo atributos conocidos (`id`, `class`, `name`, `role`, `type`, `placeholder`,
`href`, `title`, `for`, `alt`, `data-test*`, `aria-*`) y estilos (colores, fuente, borde, display,
opacidad…; sin `url()`), enmascara secretos en textos y valores, y descarta un árbol mal formado. Con
`"kind": "step"` y `"label"` es la página de un paso (hasta 40 por test); sin ellos, la del fallo.

### Hora real de un evento

Toda escritura acepta el header opcional `X-TraceReports-Timestamp: <epoch en ms>`: la hora en que el
evento ocurrió de verdad. Lo usan `tracereports report` y `tracereports push` al reproducir una
[grabación sin servidor](offline.md), para que el reporte conserve las horas originales (inicio y fin
de la ejecución y de cada test, pasos y capturas). Sin el header, cuenta la hora de llegada.

### Importar un reporte JUnit XML

`POST /import/junit` crea una ejecución ya terminada a partir de uno o más reportes JUnit XML, el
formato que escriben casi todos los runners (pytest `--junitxml`, Maven Surefire, Gradle, Playwright,
Jest, Cypress, gotestsum, .NET…). No hace falta un cliente ni cambiar los tests.

```bash
# un archivo como body
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/xml" --data-binary @report.xml \
  "$URL/api/v1/import/junit?name=Nightly&project=shop&branch=main&commit=$SHA"
# varios archivos: un campo "file" por reporte
curl -H "Authorization: Bearer $TOKEN" -F file=@target/surefire-reports/TEST-A.xml -F file=@TEST-B.xml \
  "$URL/api/v1/import/junit?name=Nightly"
```

- Query (todo opcional): `name` (por defecto, el nombre de la suite si hay una sola, o `JUnit`),
  `environment`, `project`, `branch`, `commit`, `framework` (por defecto `junit`).
- Respuesta `201 {run_id, status, tests, passed, failed, skipped, report}`; `report` es el link al
  reporte (absoluto si está configurado `PUBLIC_URL`).
- Cada `<testcase>` es un test con identidad `classname#name`: importar el mismo reporte cada noche
  arma su historial, la detección de flaky y la comparación. `<failure>` y `<error>` son `FAIL`,
  `<skipped>` es `SKIP`; los reintentos de Surefire (`<flakyFailure>`, `<rerunFailure>`…) cuentan
  como intentos. `<system-out>` y `<system-err>` quedan como pasos (hasta 16 KB cada uno).
- Se usan las horas y duraciones del reporte (`timestamp` y `time`); sin `timestamp`, la ejecución
  termina en el momento de la importación.
- Pasa por el mismo enmascarado que todo lo demás, y los fallos se diagnostican con IA y notifican
  como en una ejecución normal. Límite: 50 MB por importación. No trae capturas ni red: eso lo
  agregan los clientes.

### Importar resultados de Allure

`POST /import/allure` crea una ejecución terminada a partir de una carpeta `allure-results`
(allure-pytest, allure-junit5, allure-testng, allure-playwright, allure-cucumber…) comprimida en ZIP,
como body (`Content-Type: application/zip`) o en el campo `file` de un formulario multipart:

```bash
cd allure-results && zip -qr ../allure.zip . && cd ..
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/zip" --data-binary @allure.zip \
  "$URL/api/v1/import/allure?project=shop&branch=main"
```

- Misma query y misma respuesta que `/import/junit`. Nombre por defecto: el `buildName` de
  `executor.json`, o `Allure`; framework: el label `framework` de los resultados.
- Cada `*-result.json` es un test con identidad `fullName [parámetros]`. `failed` y `broken` son
  `FAIL`, `skipped` (y `unknown`) es `SKIP`. Las ejecuciones con el mismo `historyId` (reintentos)
  se unen en un test con sus intentos, quedando el resultado de la última.
- Los pasos (anidados, con indentación), su estado y su hora quedan como pasos del test; los
  adjuntos de imagen, como capturas; los de texto o JSON, como un paso con su contenido (hasta 16 KB).
  Los labels `tag`, `feature`, `story` y `epic` son las categorías; `parentSuite / suite / subSuite`,
  la suite.
- Límites: 200 MB por ZIP y 15 MB por adjunto; lo que se lee descomprimido, 1 GB en total y 256 MB
  entre los `*-result.json` (se mide lo que de verdad se lee, no lo que el ZIP declara). Pasados,
  responde `413`. El ZIP se guarda en un archivo temporal mientras se importa (se borra siempre) y se
  importan hasta 2 a la vez; el resto espera su turno.

## Lectura (lo que usa la UI)

| Método | Ruta | Descripción |
|---|---|---|
| GET | `/config` | IA activa, proveedor, modelo e idioma por defecto |
| GET | `/metrics?days=30&suite=` | Métricas entre ejecuciones terminadas: KPIs (y los del período anterior), serie diaria, tests que más fallan, flaky, más lentos, causas y tags |
| GET | `/settings` | Ajustes: idioma, proveedor de IA (sin la key: solo sus 4 últimos caracteres), si se pueden editar y por qué no |
| GET | `/auth/check` | Con `X-TraceReports-Token`: `{token_required, token_sent, token_valid}`. Verifica un token sin escribir nada |
| GET | `/runs?limit=50` | Ejecuciones con contadores (`total`, `passed`, `failed`, `skipped`, `warning`, `running`, `interrupted`, `quarantined`). Una que sigue `RUNNING` trae `idle_ms` (tiempo desde lo último que recibió); una cerrada puede traer `closed_by` (un usuario de la interfaz, `manual` sin login, o `auto` por inactividad) y `close_reason` |
| GET | `/maintenance?state=&project=&environment=&branch=&older_than_hours=` | Ejecuciones con problemas de todos los proyectos, las más nuevas primero (hasta 200): `state` vacío (en curso o incompletas), `idle` (en curso, sin actividad hace 5 min), `running`, `incomplete`, `manual` (cerradas a mano) o `auto` (cerradas por inactividad). Responde `{runs, idle_warn_ms, stale: {hours, source, editable}}`; `source` es `env`, `ui` o `default` |
| GET | `/runs/search?...` | Historial paginado para Buscar: `{items, next_cursor, total_estimate}` |
| GET | `/runs/facets?...` | Valores frecuentes para los filtros de Buscar: proyecto, ambiente, rama y tag |
| GET | `/runs/{run_id}` | Ejecución con sus tests (incluye `flaky`, `flaky_info`, `net_drift`, contadores de red) y `summary` (diagnóstico) |
| GET | `/runs/{run_id}/compare?base={id}` | Comparación (`base` default: la ejecución anterior relacionada): `base_run`, `base_reason`, `new_failures`, `fixed`, `still_failing`, `changed` (cambió de estado sin un fallo de por medio, p. ej. `PASS` → `SKIP`), `unchanged`, `slower`, `new_tests` (solo en esta), `missing` (solo en la base), `common` (tests en las dos: `0` = no hay nada que comparar) y `base_later` (la base empezó después que esta) |
| GET | `/runs/{run_id}/baselines` | Hasta 100 ejecuciones terminadas del mismo proyecto y ambiente que pueden ser la base, las más nuevas primero, cada una con `shared` (tests en común con esta) |
| GET | `/runs/{run_id}/endpoints` | Ranking de endpoints del backend |
| GET | `/runs/{run_id}/export` | ZIP autocontenido del reporte |
| GET | `/tests/{test_id}` | Test con pasos y diagnóstico |
| GET | `/tests/{test_id}/history` | Últimas ejecuciones del test (por nombre) |
| GET | `/tests/{test_id}/network` | Llamadas de red del test |
| GET | `/tests/{test_id}/locator` | Selector roto y reemplazos sugeridos (`204` si el fallo no fue de un selector) |
| GET | `/tests/{test_id}/dom` | Snapshot de la página al fallar, tal como se guardó (`204` si no hay) |
| GET | `/tests/{test_id}/dom/steps` | Snapshots de los pasos: `[{seq, label, at}]` |
| GET | `/tests/{test_id}/dom/steps/{seq}` | Un snapshot de paso (`404` si no existe) |
| GET | `/tests/{test_id}/dom/baseline?label=` | La página del mismo paso en el último PASS del test que guardó sus páginas (`204` si no hay) |
| GET | `/live` | En vivo: ejecuciones en curso o terminadas en las últimas 2 h, con conteos, progreso y ETA |
| GET | `/live/execution?key=` | Una ejecución con sus corridas y tests (estado `running`, `turtle`, `no_signal` o el resultado, duración, p50/p90, último paso) |
| GET | `/runs/{run_id}/control` | `{cancel, by, reason}`: lo consultan entre tests los clientes con control remoto |
| GET | `/tests/{test_id}/drift` | p95 por endpoint del test frente a la mediana de sus ejecuciones anteriores |
| GET | `/stream?run={id}` | Eventos en vivo (Server-Sent Events): `run`, `test`, `log`, `network`, `triage`, `summary` |
| GET | `/network/{id}/body` | Body guardado completo (JSON o texto plano, nunca HTML ejecutable) |
| GET | `/network/{id}/baseline` | La misma llamada en la última ejecución donde el test pasó (`{conn, run_id, test_id, same_context}`), o `204` |
| GET | `/screenshots/{archivo}` | Captura (fuera de `/api/v1`) |

### Buscar ejecuciones

`GET /runs/search` acepta `q` (máx. 200 caracteres), `project`, `environment`, `branch`, `tag`,
`status` (puede repetirse o separarse por comas), `incomplete`, `flaky`, `from`, `to`
(`YYYY-MM-DD`), `sort` (`recent`, `oldest`, `duration`, `failures`), `limit` (1–100) y `cursor`.
`q` busca nombre, proyecto, rama, commit y ambiente; nombre, key y mensaje de error del test; y el
método y la URL de las llamadas de red que fallaron (status 400 o más, o sin respuesta), sin incluir
tests en la respuesta. Los caracteres `%` y `_` se buscan literalmente. Cada fila devuelve sus contadores,
duración y `flaky_count`; el cursor es opaco y mantiene la página estable si entran ejecuciones
nuevas. `GET /runs/facets` acepta los mismos filtros y devuelve hasta 20 valores por grupo.

## Ajustes (pantalla de Ajustes)

| Método | Ruta | Body | Respuesta |
|---|---|---|---|
| PUT | `/settings` | `{language?, ai_language?, ai?: {provider, model, base_url, api_key?}}` | Ajustes actualizados |
| POST | `/settings/ai/test` | `{provider, model, base_url, api_key?}` | `{ok, ms}` o `{ok: false, error}` (no guarda nada) |
| DELETE | `/settings/ai` | — | Vuelve a la configuración de IA del entorno |
| GET | `/settings/ai/status` | — | Estado general del proveedor de IA configurado según su página de estado pública (OpenAI, Anthropic; Ollama: si responde; Gemini y APIs compatibles no publican uno consultable): `{provider, source, indicator, description, components, incidents, page_url, error, checked_at}`. En caché un minuto; `?refresh=1` consulta de nuevo. Solo hace un GET a esa página |
| GET | `/settings/ai/usage` | — | Uso del proveedor de IA (Ajustes → Uso de la IA): `{usage: {today, last_7, last_30, by_model, by_kind, daily, skipped_by_budget}, max_per_run}`. Cada total trae `calls`, `errors`, `errors_by` (por motivo: `rate_limit`, `server`, `auth`, `timeout`, `network`, `other`), `input_tokens`, `output_tokens` (los que informó el proveedor), `untracked` (llamadas sin datos de tokens), `duration_ms`, `timed` (llamadas con tiempo medido) y `avg_ms` (respuesta media de las medidas). Fechas en la hora del servidor; se guardan 90 días. Sin costos |

- `provider`: `gemini`, `anthropic`, `openai`, `openai_compatible`, `ollama` u `off`.
  `api_key` vacía o ausente mantiene la key actual de ese proveedor (la guardada o la del entorno).
- `language`: `es` o `en` (idioma por defecto de la UI). `ai_language`: `auto`, `es` o `en`.
- Requieren `Content-Type: application/json`. Se aceptan con el token (`TRACEREPORTS_TOKEN`), con el
  login de la UI. Sin login ni token configurados (modo local) también desde el mismo equipo del
  servidor, conectado directo (no a través de un proxy). Con `TRACEREPORTS_TOKEN` el mismo equipo
  también necesita credenciales, salvo con `TRACEREPORTS_LOCAL_ADMIN=1`.
  `TRACEREPORTS_SETTINGS_LOCKED=1` los rechaza siempre (`403`).

### Revisión de seguridad (experimental)

`GET /security/posture` devuelve los chequeos de `tracereports doctor --security` sobre la configuración con
que corre el servidor: `{version, checks: [{id, level, title, detail, fix}], ok, warn, fail}` (`level`:
`ok`, `warn` o `fail`). Requiere `TRACEREPORTS_TOKEN`, el login de la UI o el mismo equipo en modo local
(el token de ingesta recibe 403). Nunca incluye valores de credenciales.

## Acciones desde la interfaz (IA y escalamiento)

Requieren `Content-Type: application/json` y la misma regla que los Ajustes: token, login de la UI
o, en modo local, el mismo equipo del servidor.

| Método | Ruta | Body | Respuesta |
|---|---|---|---|
| POST | `/ui/ai/probe` | `{}` (`?force=1` se salta la caché de 20 s) | Si la IA configurada ahora responde (Trace Buddy): `{ok, state, provider, model, ms}`, `state` = `llm`, `local` (motor de reglas, nada que llamar) u `off`; con error `{ok: false, error, hint, suggest}`. Usa la configuración activa, así que funciona también con Ajustes bloqueado; una sola petición pequeña y fija, que queda en Uso de la IA |
| POST | `/ui/ai/chat` | `{run_id, test_id, lang, messages: [{role, text}]}` | Una pregunta libre a Trace Buddy: `{answer, provider, model}`. Necesita un LLM (`409` con `code: needs_llm` con el motor local). El modelo recibe la evidencia de la ejecución o del test (`run_id`/`test_id`, ambos opcionales) y los últimos 8 turnos de hasta 2000 caracteres, todo pasado por la redacción. Hasta 60 preguntas por hora por servidor (`429`); queda en Uso de la IA como `chat` |
| POST | `/ui/tests/{test_id}/analyze` | — | `202 {queued}`: vuelve a diagnosticar un test fallido |
| POST | `/ui/runs/{run_id}/analyze` | `{all?}` | `202 {queued}`: diagnostica los fallos sin diagnóstico (o todos con `all`) y el resumen de la ejecución |
| POST | `/ui/escalate` | `{run_id, test_id, audience, lang, regenerate?, no_ai?}` | Resumen para `business`, `qa` o `dev` (`test_id` 0 = la ejecución completa). Una ejecución sin fallos trae `facts.assessment`: el veredicto de QA (`clean`, `observations` o `risks`, calculado por reglas, no por la IA) y sus `signals` (`level` `risk`/`note`, `kind`, `test`, `text`); con riesgos la severidad es `medium`. Si terminó, la IA escribe un resumen de qué se validó (`kind: success`), pero no el veredicto, la severidad ni la evidencia; si su texto describe un fallo se descarta (`ai_skipped: invented_failure`). Una en curso, incompleta o toda omitida se arma sin IA (`ai_skipped: no_failures`). Con IA queda en caché y trae `timing`: `{total_ms, provider_ms, wait_ms, own_ms, calls, attempts}` (cuánto esperó al proveedor, las pausas entre reintentos y lo que tardó TraceReports; `attempts`: cada llamada con `ms`, `ok`, `status`, `reason` y la espera siguiente `wait_ms`/`wait_asked`). Ante un 429/5xx se espera lo que pide el proveedor (`Retry-After`, `retry-after-ms` o el `RetryInfo` de Gemini), hasta 90 s |
| POST | `/ui/runs/{run_id}/close` | `{reason}` | Cierra a mano una ejecución `RUNNING`: sus tests sin terminar quedan `INTERRUPTED` con el motivo (redactado, hasta 500 caracteres) y la ejecución registra `closed_by` y `close_reason`. `409` si ya estaba cerrada. Después siguen el diagnóstico y la notificación, como en cualquier cierre |
| POST | `/ui/runs/close` | `{run_ids, reason}` | Lo mismo para hasta 200 ejecuciones: `{closed, skipped}` (se saltan las ya cerradas o desconocidas) |
| POST | `/ui/runs/delete` | `{run_ids}` | Elimina hasta 200 ejecuciones **cerradas** con toda su evidencia (capturas y artefactos incluidos; la base se compacta): `{deleted, skipped}`. Una en curso se salta |
| PUT | `/ui/maintenance/stale` | `{hours}` | El umbral de inactividad del cierre automático (`0` = nunca, hasta 720), sin reiniciar. Necesita el permiso de Ajustes; `409` si lo fija `TRACEREPORTS_STALE_RUN_HOURS` |
| POST | `/ui/escalate/send` | `{run_id, test_id, audience, lang, channel}` | Publica el resumen en `teams` o `slack`. Con `PUBLIC_URL` incluye el link y la captura |
| POST | `/ui/tickets` | `{run_id, test_id, provider, audience, lang, force}` | Crea un ticket en `github`, `jira` o `azure` con el resumen (default para `dev`). `201 {ticket}`; si el mismo fallo ya tiene uno, `200 {ticket, existing: true}` (salvo `force`). Si no se sabe si el tracker lo creó, `502 {uncertain: true}` y luego `409` hasta enviar `force`. Ver [Configuración](configuration.md#tickets-en-github-jira-o-azure-devops) |
| POST | `/ui/summary/weekly` | `{send, lang}` | Resumen de los últimos 7 días: `{summary, sent}`. Con `send: true` lo publica en Teams/Slack |
| POST | `/ui/quarantine` | `{test_id, reason, owner, days}` | Pone en cuarentena el test (en su proyecto) por `days` días (default 14, máx. 180). `reason` es obligatorio. Responde la cuarentena y la ejecución recalculada |
| DELETE | `/ui/quarantine/{test_id}` | `{}` | Quita la cuarentena del test |
| GET | `/quarantine?project=…` | — | Cuarentenas del proyecto (`all=1`: de todos), con `active` |
| POST | `/ui/tests/{test_id}/verdict` | `{verdict, comment, author}` | Clasifica el fallo: `product_bug`, `test_bug`, `environment`, `data`, `flaky` u `other`. Los tests de `GET /runs/{id}` y `/tests/{id}` traen `verdict`, `previous_verdict` (de una ejecución anterior) y `owner` |
| GET | `/runs/{run_id}/tickets` | — | Tickets de los fallos de la ejecución (también los abiertos desde ejecuciones anteriores del mismo test) |
| GET | `/runs/{run_id}/release` | — | ¿Podemos salir? `{decision: go|risk|no_go, pass_rate, gate, checks, features}` (`TRACEREPORTS_RELEASE_GATE`) |

Lecturas relacionadas: `GET /runs/{run_id}/recurrence` (en cuántas ejecuciones anteriores de la
misma suite apareció cada incidente) y `GET /runs/{run_id}/escalation?test=&audience=&lang=` (el
resumen guardado si la evidencia no cambió desde que se escribió, o `204`). `GET /metrics` acepta también `env`, `tag`, `from` y `to`
(`AAAA-MM-DD`), y `GET /tests/{test_id}/history` acepta `limit` (hasta 60) y `newer` (también hasta
esa cantidad de ejecuciones posteriores, primeras en la lista). `GET /tests/{test_id}/locator` dice además
`has_dom` y `dom_elements` (si llegó el snapshot de la página: sin sugerencias con snapshot es que ninguna
sirve) y `failed_selector_truncated` (el selector llegó cortado: no se buscan reemplazos).

FixLab (el Lab, los checks, Verificar y las cápsulas: `/network/{id}/lab`, `/ui/lab/send`,
`/ui/lab/checks`, `/ui/lab/verify`, `/runs/{run_id}/lab`, `/network/{id}/capsule`,
`/runs/{run_id}/lab/capsule`): ver [FixLab · API](fixlab.md#api).
