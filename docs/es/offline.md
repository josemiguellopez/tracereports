# Sin servidor: grabar, reportar y subir después

🌐 [English](../en/offline.md) · **Español**

Si el servidor no está, no responde o rechaza el token (por ejemplo, porque se olvidó la clave en
el CI), los clientes **no pierden la evidencia**: la graban en una carpeta. Con ella puedes:

- **armar el reporte HTML sin servidor**: `tracereports report <carpeta>`. Es la misma carpeta que
  el "Exportar ZIP": se abre con doble clic, sin internet, con incidentes, timeline, red, cURL,
  locators y replay;
- **subirla al servidor después**: `tracereports push <carpeta>`, cuando tengas el token correcto.

El mismo comando `report` también arma el reporte desde **JUnit XML**, sin servidor:

```bash
tracereports report results.xml -o reporte/           # un archivo
tracereports report target/surefire-reports -o reporte/  # una carpeta de *.xml (una ejecución)
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
incompleta, a la hora de su último evento.

## Configuración (todos los clientes)

| Variable | Default | Qué hace |
| --- | --- | --- |
| `TRACEREPORTS_OFFLINE` | `auto` | `auto`: graba solo si la ejecución no se pudo crear. `always`: graba sin intentar un servidor. `off`: nunca graba (comportamiento anterior) |
| `TRACEREPORTS_OFFLINE_DIR` | `./tracereports-offline/<fecha-hora>-<id>` | Carpeta de la grabación. Sin ella, una carpeta nueva por sesión |
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
Cada línea es una llamada a la API:

```json
{"seq": 3, "ts": 1791319221317, "method": "POST", "path": "/api/v1/tests/-2846400002/logs",
 "content_type": "application/json", "body": {"status": "INFO", "message": "abrir login"}}
```

Los ids negativos son locales; la llamada que crea una ejecución o un test los declara en
`local_id`. La hora (`ts`) viaja como header `X-TraceReports-Timestamp` (ver la [API](api.md)).
