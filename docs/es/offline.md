# Sin servidor: grabar, reportar y subir después

🌐 [English](../en/offline.md) · **Español**

Si el servidor no está, no responde o rechaza el token (por ejemplo, porque se olvidó la clave en
el CI), los clientes **no pierden la evidencia**: la graban en una carpeta. Con ella puedes:

- **armar el reporte HTML sin servidor**: `tracereports report <carpeta>`. Es la misma carpeta que
  el "Exportar ZIP": se abre con doble clic, sin internet, con incidentes, timeline, red, cURL,
  locators y replay;
- **subirla al servidor después**: `tracereports push <carpeta>`, cuando tengas el token correcto.

El mismo comando `report` también arma el reporte desde **JUnit XML** o **Allure**, sin servidor:

```bash
tracereports report results.xml -o reporte/              # un archivo JUnit XML
tracereports report target/surefire-reports -o reporte/  # una carpeta de *.xml (una ejecución)
tracereports report allure-results -o reporte/           # resultados de Allure (o su ZIP)
```

## Cómo funciona

1. Al empezar, el cliente intenta crear la ejecución en el servidor. Si no puede (servidor caído,
   sin conexión, `401` por token incorrecto), avisa y pasa a **grabar**: escribe en la carpeta las
   mismas llamadas a la API que habría hecho, con su hora real. Los tests nunca se rompen.
2. Al terminar, si el binario `tracereports` está instalado (en el `PATH` o en `TRACEREPORTS_BIN`),
   el cliente arma el reporte solo en `<carpeta>/report/index.html`. Si no, imprime el comando.
3. `report` reproduce la grabación en un servidor en memoria (mismo código, mismo enmascarado de
   secretos) y exporta el reporte. `push` la reproduce contra un servidor real.

Subir la misma grabación dos veces no duplica nada: cada evento lleva una `Idempotency-Key`
derivada de la grabación. Una ejecución que quedó sin cerrar (el proceso se cortó) se cierra como
incompleta, a la hora de su último evento; si ese cierre falla, `push` termina con error: súbela de
nuevo y la ejecución se cierra una sola vez.

## Configuración (todos los clientes)

| Variable | Default | Qué hace |
| --- | --- | --- |
| `TRACEREPORTS_OFFLINE` | `auto` | `auto`: graba solo si la ejecución no se pudo crear. `always`: graba sin intentar un servidor. `both`: envía al servidor y guarda una copia local. `off`: nunca graba (comportamiento anterior) |
| `TRACEREPORTS_OFFLINE_DIR` | `./tracereports-offline/<fecha-hora>-<id>` | Carpeta de la grabación. Sin ella, una carpeta nueva por sesión |
| `TRACEREPORTS_OFFLINE_KEEP` | vacío | `1`: conservar siempre los eventos, bodies e ids crudos de la copia local |
| `TRACEREPORTS_BIN` | el `tracereports` del `PATH` | Binario con el que el cliente arma el reporte al terminar |
| `TRACEREPORTS_OFFLINE_REPORT` | `1` | `0`: no armar el reporte al terminar (solo grabar) |

Agrega `tracereports-offline/` a tu `.gitignore`: la grabación puede traer capturas y datos de prueba.

Por cliente:

- **pytest**: `--tracereports-offline DIR` graba siempre en `DIR`. Con pytest-xdist, el
  controlador y cada worker escriben su propio archivo en la misma carpeta y el reporte las une.
  Con `--tracereports-strict`, haber caído en modo grabación hace fallar la sesión (la evidencia no
  llegó al servidor). API: `TraceReports(offline_dir=..., offline="always")`, `cr.recording`,
  `cr.offline_dir`, `cr.offline_report`.
- **Playwright Test (JS)**: opciones del reporter `offlineDir` y `offline`; API:
  `new TraceReports({ offlineDir, offline })`, `cr.recording`, `cr.offlineDir`, `cr.offlineReport`.
- **Java**: `-Dtracereports.offline=always`, `-Dtracereports.offlineDir=...` (o las variables);
  `cr.recording()`, `cr.offlineDir()`, `cr.offlineReport()`.
- **Go**: `Client.Offline`, `Client.OfflineDir`, `c.Recording()`, `c.RecordingDir()`,
  `c.OfflineReport`.

Con shards de CI (`TRACEREPORTS_RUN_ID`) sin servidor, el id de la ejecución es negativo (local):
todos los shards deben usar la misma `TRACEREPORTS_OFFLINE_DIR`.

## Copia local mientras se envía (`both`)

Activa `TRACEREPORTS_OFFLINE=both` para conservar la evidencia aunque el servidor se caiga a mitad
de la suite. Al empezar intenta crear la ejecución; si falla, graba igual que `auto`. Si responde,
cada llamada se envía y se graba una sola vez, con ids locales negativos. Los tests que empiezan
después de una caída también se conservan. El default sigue siendo `auto`.

```python
cr = TraceReports(offline="both")
```

En JavaScript usa `new TraceReports({ offline: "both" })` (también en el reporter), en Java
`-Dtracereports.offline=both` y en Go `c.Offline = "both"`. pytest toma la variable de entorno.
La URL del reporte sigue apuntando al servidor; `offline_report`, `offlineReport`, `offlineReport()`
y `OfflineReport` exponen el HTML local por separado.

| Al terminar | Se conserva |
| --- | --- |
| Todo llegó al servidor y se generó el HTML | `report/` y el marcador con `raw_removed: true` |
| Algo no llegó, o un worker no confirmó su cierre | HTML, si se pudo generar, y grabación cruda |
| Sin binario, reporte desactivado o error al generarlo | Grabación cruda y comando para generar el reporte |
| `TRACEREPORTS_OFFLINE_KEEP=1` | HTML y grabación cruda |

**La grabación cruda contiene datos sin enmascarar.** El HTML sí se genera con el enmascarado del
servidor. Protege la carpeta y evita publicar los eventos y `bodies/` como artefactos públicos.

Para borrar, todos los procesos deben haber vaciado su cola y confirmado cero rechazos, descartes,
pérdidas y, en Python, eventos en spool; el dueño además debe confirmar el cierre de la ejecución.
Cada proceso registra su estado antes de grabar y lo actualiza al cerrar. Solo el dueño borra; un
proceso interrumpido, una grabación dañada o actividad durante la generación del reporte conserva
lo crudo. `TRACEREPORTS_OFFLINE_REPORT=0` también evita el borrado.

Los shards deben compartir `TRACEREPORTS_OFFLINE_DIR` y el id real en `TRACEREPORTS_RUN_ID`;
pytest-xdist transmite la carpeta a sus workers. El dueño guarda el id local en `ids/server-<id>`.
Si falta esa correspondencia, el worker avisa y esa parte no puede reconstruirse por completo.
Usa una carpeta nueva para cada sesión; una carpeta con `raw_removed` solo contiene el reporte.

El marcador agrega `mirror.server`, `mirror.runs` (pares `local`/`server`) y `mirror.complete`:

- Copia completa: `push` se niega para evitar duplicarla. `tracereports push --force <carpeta>`
  permite subir la copia conservada como otra ejecución; repetir ese upload sigue siendo idempotente.
- Copia incompleta: `push` avisa que ya existe una ejecución parcial y crea otra completa, separada.
- `raw_removed: true`: `push` falla incluso con `--force`; solo queda el HTML.
- Sin `mirror`: las grabaciones anteriores mantienen su comportamiento. `report` acepta ambas
  mientras exista la grabación cruda.

## El binario

Viene en los [releases](https://github.com/josemiguellopez/tracereports/releases/latest) (Linux,
macOS y Windows) y en la imagen de Docker:

```bash
docker run --rm --user "$(id -u):$(id -g)" -v "$PWD:/w" -w /w ghcr.io/josemiguellopez/tracereports report tracereports-offline/<sesión> -o reporte
```

`tracereports report --help` y `tracereports push --help` listan todas las opciones (`--zip`,
`--name`, `--project`, `--ai` para diagnosticar con la IA configurada en el entorno, `--url`,
`--token`…). Sin `--ai`, `report` no hace ninguna llamada de red.

## En el CI

```yaml
- name: Tests
  run: pytest --tracereports          # si el servidor o el token fallan, graba
- name: Reporte (con o sin servidor)
  if: always()
  run: |
    for d in tracereports-offline/*/; do tracereports report "$d" -o "reporte/$(basename "$d")"; done
- uses: actions/upload-artifact@v7
  if: always()
  with: { name: tracereports, path: reporte/ }
```

## Formato de la grabación

Una carpeta con `tracereports-offline.json` (`{"format": "tracereports-offline", "version": 1,
"id": …}`), un `events-<pid>-<id>.jsonl` por proceso y `bodies/` con lo que no es JSON (capturas).
Cada línea es una llamada a la API (hasta 49 MiB, suficiente para la solicitud de red más grande
que acepta el servidor):

```json
{"seq": 3, "ts": 1791319221317, "method": "POST", "path": "/api/v1/tests/-2846400002/logs",
 "content_type": "application/json", "body": {"status": "INFO", "message": "abrir login"}}
```

Los ids negativos son locales; la llamada que crea una ejecución o un test los declara en
`local_id`. La hora (`ts`) viaja como header `X-TraceReports-Timestamp` (ver la [API](api.md)).
