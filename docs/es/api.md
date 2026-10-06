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
| POST | `/runs` | `{name, environment, project, branch, commit, framework}` | `201 {run_id}` |
| POST | `/runs/{run_id}/tests` | `{name, category, description, key, suite, params, worker}` | `201 {test_id}` |
| POST | `/tests/{test_id}/logs` | `{status, message, timestamp}` | `201` paso creado |
| POST | `/tests/{test_id}/screenshot` | multipart: `file` (PNG/JPEG/GIF/WEBP), `message`, `status` | `201 {url, log}` |
| POST | `/tests/{test_id}/network` | `{connections: [Conn]}` (hasta 5000 por lote) | `201 {stored, errors}` |
| POST | `/tests/{test_id}/dom` | Snapshot de la página al fallar (ver abajo), hasta 4 MB / 2000 elementos | `201` |
| PATCH | `/tests/{test_id}/finish` | `{status, error_message, error_trace, attempts}` | `200` test |
| PATCH | `/runs/{run_id}/finish` | `{interrupted}` (opcional) | `200` ejecución |

- `status` de un paso: `INFO`, `PASS`, `FAIL`, `WARNING`, `SKIP`. `timestamp`: epoch en ms o
  RFC 3339 (opcional, default ahora).
- `status` de un test: `PASS`, `FAIL`, `WARNING`, `SKIP`, o vacío para deducirlo de los pasos.
  Un `FAIL` dispara el diagnóstico con IA.
- Cerrar la ejecución dispara el diagnóstico global y las notificaciones a Teams/Slack. Los tests
  que sigan en curso se cierran como `FAIL` (*Interrumpido*) y la ejecución queda `incomplete`; con
  `{"interrupted": true}` también (nunca aparece como exitosa). Cerrar de nuevo una ejecución ya
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

## Lectura (lo que usa la UI)

| Método | Ruta | Descripción |
|---|---|---|
| GET | `/config` | IA activa, proveedor, modelo e idioma por defecto |
| GET | `/metrics?days=30&suite=` | Métricas entre ejecuciones terminadas: KPIs (y los del período anterior), serie diaria, tests que más fallan, flaky, más lentos, causas y tags |
| GET | `/settings` | Ajustes: idioma, proveedor de IA (sin la key: solo sus 4 últimos caracteres), si se pueden editar y por qué no |
| GET | `/auth/check` | Con `X-TraceReports-Token`: `{token_required, token_sent, token_valid}`. Verifica un token sin escribir nada |
| GET | `/runs?limit=50` | Ejecuciones con contadores |
| GET | `/runs/{run_id}` | Ejecución con sus tests (incluye `flaky`, `flaky_info`, `net_drift`, contadores de red) y `summary` (diagnóstico) |
| GET | `/runs/{run_id}/compare?base={id}` | Comparación (`base` default: la ejecución anterior relacionada) |
| GET | `/runs/{run_id}/endpoints` | Ranking de endpoints del backend |
| GET | `/runs/{run_id}/export` | ZIP autocontenido del reporte |
| GET | `/tests/{test_id}` | Test con pasos y diagnóstico |
| GET | `/tests/{test_id}/history` | Últimas ejecuciones del test (por nombre) |
| GET | `/tests/{test_id}/network` | Llamadas de red del test |
| GET | `/tests/{test_id}/locator` | Selector roto y reemplazos sugeridos (`204` si el fallo no fue de un selector) |
| GET | `/tests/{test_id}/drift` | p95 por endpoint del test frente a la mediana de sus ejecuciones anteriores |
| GET | `/stream?run={id}` | Eventos en vivo (Server-Sent Events): `run`, `test`, `log`, `network`, `triage`, `summary` |
| GET | `/network/{id}/body` | Body guardado completo (JSON o texto plano, nunca HTML ejecutable) |
| GET | `/screenshots/{archivo}` | Captura (fuera de `/api/v1`) |

## Ajustes (pantalla de Ajustes)

| Método | Ruta | Body | Respuesta |
|---|---|---|---|
| PUT | `/settings` | `{language?, ai_language?, ai?: {provider, model, base_url, api_key?}}` | Ajustes actualizados |
| POST | `/settings/ai/test` | `{provider, model, base_url, api_key?}` | `{ok, ms}` o `{ok: false, error}` (no guarda nada) |
| DELETE | `/settings/ai` | — | Vuelve a la configuración de IA del entorno |

- `provider`: `gemini`, `anthropic`, `openai`, `openai_compatible`, `ollama` u `off`.
  `api_key` vacía o ausente mantiene la key actual de ese proveedor (la guardada o la del entorno).
- `language`: `es` o `en` (idioma por defecto de la UI). `ai_language`: `auto`, `es` o `en`.
- Requieren `Content-Type: application/json`. Se aceptan con el token (`TRACEREPORTS_TOKEN`), con el
  login de la UI. Sin login ni token configurados (modo local) también desde el mismo equipo del
  servidor, conectado directo (no a través de un proxy). Con `TRACEREPORTS_TOKEN` el mismo equipo
  también necesita credenciales, salvo con `TRACEREPORTS_LOCAL_ADMIN=1`.
  `TRACEREPORTS_SETTINGS_LOCKED=1` los rechaza siempre (`403`).

## Acciones desde la interfaz (IA y escalamiento)

Requieren `Content-Type: application/json` y la misma regla que los Ajustes: token, login de la UI
o, en modo local, el mismo equipo del servidor.

| Método | Ruta | Body | Respuesta |
|---|---|---|---|
| POST | `/ui/tests/{test_id}/analyze` | — | `202 {queued}`: vuelve a diagnosticar un test fallido |
| POST | `/ui/runs/{run_id}/analyze` | `{all?}` | `202 {queued}`: diagnostica los fallos sin diagnóstico (o todos con `all`) y el resumen de la ejecución |
| POST | `/ui/escalate` | `{run_id, test_id, audience, lang, regenerate?}` | Resumen para `business`, `qa` o `dev` (`test_id` 0 = la ejecución completa). Con IA queda en caché |
| POST | `/ui/escalate/send` | `{run_id, test_id, audience, lang, channel}` | Publica el resumen en `teams` o `slack`. Con `PUBLIC_URL` incluye el link y la captura |

Lecturas relacionadas: `GET /runs/{run_id}/recurrence` (en cuántas ejecuciones anteriores de la
misma suite apareció cada incidente) y `GET /runs/{run_id}/escalation?test=&audience=&lang=` (el
resumen guardado, o `204`). `GET /metrics` acepta también `env`, `tag`, `from` y `to`
(`AAAA-MM-DD`), y `GET /tests/{test_id}/history` acepta `limit` (hasta 60).
