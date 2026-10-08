# Cliente Python y plugin de pytest

🌐 [English](../en/python.md) · **Español**

```bash
pip install tracereports                       # desde PyPI
# o, desde una copia de este repositorio:
pip install ./client/python
# o, la versión en desarrollo:
pip install "tracereports @ git+https://github.com/josemiguellopez/tracereports#subdirectory=client/python"
```

Solo usa la librería estándar. El cliente nunca rompe ni frena tu suite:

- la evidencia (pasos, capturas, red, DOM, resultados) sale **en segundo plano** desde una cola
  acotada: el test no espera la red (medido: ~0,004 ms por paso encolado frente a ~11 ms enviándolo
  en línea; ~420 bytes por evento en espera);
- los envíos fallidos (sin conexión, timeout, 5xx, 408, 429) se **reintentan** con backoff, y cada
  uno lleva una clave `Idempotency-Key`: un reintento **nunca duplica** un paso en el servidor;
- si el servidor no responde, las llamadas que necesitan su respuesta (crear ejecución o test)
  fallan al instante durante 30 s en vez de esperar su timeout en cada test;
- `end_run()` espera la cola (`TRACEREPORTS_FLUSH_TIMEOUT`, 30 s) antes de cerrar la ejecución. Lo que
  no se pudo enviar se guarda en `TRACEREPORTS_SPOOL_DIR` para reenviarlo con
  `python -m tracereports.resend <carpeta>`; sin esa carpeta se informa como perdido. El reenvío va
  por tandas (un spool más grande que la cola también avanza), en orden y sin duplicar; varios
  procesos pueden compartir la carpeta (cada archivo lo toma uno solo, y el de un proceso que murió
  se retoma a los 10 minutos). Devuelve 0 solo si no queda nada pendiente;
- **sin servidor**: si la ejecución no se puede crear (servidor caído o token incorrecto), todo se
  graba en `./tracereports-offline/<sesión>`; `tracereports report <carpeta>` arma el reporte HTML y
  `tracereports push <carpeta>` la sube después (ver [Sin servidor](offline.md));
- **cola llena** (5000 eventos o 64 MB): se descartan los eventos nuevos, se conservan los ya
  encolados y se cuenta en `cr.delivery["dropped"]`.

`cr.delivery` dice qué pasó: `sent`, `retried`, `rejected` (el servidor respondió 4xx), `dropped`,
`spooled`, `lost`, `pending` y `unregistered_tests`.

Variables que lee: `TRACEREPORTS_URL` (default `http://localhost:8080`), `TRACEREPORTS_TOKEN`,
`TRACEREPORTS_DISABLED=1` (apaga el reporte), `TRACEREPORTS_PROJECT`, `TRACEREPORTS_SPOOL_DIR`,
`TRACEREPORTS_FLUSH_TIMEOUT`, `TRACEREPORTS_SYNC=1` (envía todo en línea, para depurar) y, para el contexto,
`TRACEREPORTS_BRANCH` / `TRACEREPORTS_COMMIT` (si no están, las de GitHub Actions, GitLab, Azure DevOps,
Jenkins, Bitbucket o CircleCI, o `git`). Si una variable no está en el entorno, se lee del `.env` del
proyecto ([detalle](configuration.md#el-env-del-proyecto-en-los-clientes)).

Copia local mientras se envía: `TraceReports(offline="both")` o `TRACEREPORTS_OFFLINE=both`; detalles y conservación en [sin servidor](offline.md).

## Opción 1: plugin de pytest (sin cambiar código)

```bash
pytest --tracereports
pytest --tracereports --tracereports-run "Regresión web" --tracereports-env "staging · chrome"
```

| Opción | Variable equivalente | Descripción |
|---|---|---|
| `--tracereports` | — | Activa el reporte |
| `--tracereports-url URL` | `TRACEREPORTS_URL` | Servidor |
| `--tracereports-run NOMBRE` | `TRACEREPORTS_RUN_NAME` | Nombre de la ejecución (default: carpeta del proyecto). Usa un nombre estable para que funcionen el historial y la comparación |
| `--tracereports-env TEXTO` | `TRACEREPORTS_ENV` | Ambiente: navegador, entorno, versión… |
| `--tracereports-project NOMBRE` | `TRACEREPORTS_PROJECT` | Proyecto (default: la carpeta raíz). Junto con el ambiente y la rama define con qué ejecuciones se compara |
| `--tracereports-zip DIR` | — | Al terminar, guarda el reporte en ZIP en `DIR` (artefacto de CI) |
| `--tracereports-no-network` | — | No capturar la red |
| `--tracereports-no-screenshots` | — | No tomar captura al fallar (datos sensibles en pantalla) |
| `--tracereports-no-dom` | — | No enviar el snapshot de la página al fallar |
| `--tracereports-spool DIR` | `TRACEREPORTS_SPOOL_DIR` | Guarda en `DIR` la evidencia que no se pudo enviar |
| `--tracereports-strict` | `TRACEREPORTS_STRICT=1` | La sesión falla si parte de la evidencia no llegó al servidor (útil en CI) |
| — | `TRACEREPORTS_RUN_ID` | Reporta en una ejecución ya creada (shards de CI en varias máquinas); la cierra quien la creó |

Qué se reporta automáticamente:

- una ejecución por sesión y un test por caso, con el **docstring** como descripción y el módulo
  y los *markers* como categorías;
- la **identidad** de cada test es su `nodeid` (`archivo::Clase::test[parámetros]`), aparte del
  nombre visible: dos `test_login` en archivos distintos tienen historiales separados. Si el
  nodeid cambia (renombraste el archivo) o los parámetros llevan datos variables, fíjala con
  `@pytest.mark.tracereports_id("login-ok")`. Cada combinación de parámetros es un test distinto;
- el **contexto**: proyecto, ambiente, rama y commit. El historial, la detección de flaky y la
  comparación solo usan ejecuciones del mismo proyecto, ambiente y rama;
- estado `PASS` / `FAIL` / `SKIP`, mensaje del error y traceback completo (entrada de la IA);
- con **pytest-rerunfailures**, los intentos quedan dentro del mismo test: un paso `WARNING` por
  cada reintento con el error anterior, la evidencia del primer fallo se conserva y la UI lo
  muestra como *pasó tras reintento* (cuenta como evidencia de inestabilidad);
- con **pytest-playwright** (fixture `page`): **captura al fallar**, **todas las llamadas HTTP que
  hizo el navegador** en el test y un **snapshot de la página** para sugerir selectores si se rompe
  un locator. Es el tráfico visto desde el navegador, no los logs internos del backend.

Respuestas negativas esperadas (un 401 que el test verifica a propósito): decláralas para que no
cuenten como error ni se propongan como causa de un fallo. El marker sirve en el test, la clase o
el módulo (`pytestmark`):

```python
@pytest.mark.tracereports_expect(status=401, url="*/auth/validate*")
def test_login_invalido(page): ...

def test_otro(page, tracereports):
    tracereports.expect_response([403, 404], url="/api/admin", method="GET")
```

Para agregar pasos y capturas propias, usa la fixture `tracereports`:

```python
def test_login(page, tracereports):
    """El administrador entra al Dashboard."""
    tracereports.log_info("Abrir el login")
    page.goto("https://mi-app/login")
    tracereports.attach_screenshot(page.screenshot(), "Formulario de login")
    ...
    tracereports.log_pass("Login correcto")
```

Ejemplo completo: [`examples/pytest-playwright`](../../examples/pytest-playwright).

**pytest-xdist**: `pytest --tracereports -n 4` produce **un solo reporte**. El controlador crea la
ejecución, los workers reportan en ella (cada test guarda su worker: `gw0`, `gw1`…) y se cierra
cuando terminan todos. Si un worker se cae, sus tests en curso quedan como *Interrumpido* y la
ejecución como **incompleta**: nunca aparece como exitosa.

## Opción 2: API del cliente (unittest, Selenium, scripts…)

```python
from tracereports import TraceReports

cr = TraceReports()                     # o TraceReports("https://tracereports.miempresa.com", token="...")
cr.start_run("Regresión web", environment="staging")

cr.start_test("Login correcto", category="login, smoke", description="Credenciales válidas")
cr.log_info("Abrir el login")
cr.attach_screenshot(driver.get_screenshot_as_png(), "Formulario")   # Selenium
cr.attach_screenshot(page.screenshot(), "Formulario")                # Playwright
cr.attach_screenshot("capturas/login.png", "Desde archivo")
cr.log_pass("Usuario dentro")
cr.end_test()                           # el estado se deduce de los pasos, o end_test("FAIL", ...)

cr.end_run()
cr.download_report("salida/")           # opcional: ZIP para adjuntar o archivar
```

| Método | Descripción |
|---|---|
| `start_run(name, environment="")` | Crea la ejecución |
| `start_test(name, category="", description="")` | Inicia un test (`category` admite tags separados por coma) |
| `log_info / log_pass / log_fail / log_warning / log_skip(msg)` | Agrega un paso |
| `attach_screenshot(bytes_o_ruta, message="", status="INFO")` | Sube una captura como paso |
| `attach_network(conexiones)` | Sube la red del test (ver abajo) |
| `attach_dom(capturar_dom(page))` | Al fallar: snapshot de la página para recomendar selectores si se rompió un locator. El plugin de pytest lo hace solo |
| `end_test(status=None, error_message="", error_trace="", exc=None)` | Cierra el test. Con `exc=` toma mensaje y traceback de la excepción. Un `FAIL` dispara la IA |
| `end_run()` | Cierra la ejecución: diagnóstico global y notificaciones |
| `download_report(dir)` | Descarga el ZIP de la ejecución |

Atajos para no manejar el ciclo de vida a mano:

```python
@cr.track(category="smoke", on_failure=lambda self: self.driver.get_screenshot_as_png())
def test_login(self):
    """El docstring es la descripción."""
    ...

with cr.test("Checkout", category="e2e", on_failure=page.screenshot):
    ...
```

## Captura de red con Playwright

```python
from tracereports import attach_listeners, reportar_red

page = context.new_page()
attach_listeners(page)                  # ANTES del primer page.goto()
...
resumen = reportar_red(page, cr,        # al final de cada test, ANTES de cr.end_test()
                       guardar_en="output/network", nombre_evento="test_login")
# {"total": 42, "ok": 40, "errores": 2, "fallidas": 1, "archivo": ".../network_test_login_1790.json"}
```

- Envía solo las conexiones **del test actual**, no acumuladas.
- Enmascara `Authorization`, `Cookie`, tokens, contraseñas y RUT.
- Guarda el body de llamadas `/api/`, XHR/fetch y respuestas con error.
- `guardar_en=` deja además un JSON con los bodies **completos**. El servidor guarda hasta
  256 KB por body, y el reporte muestra la ruta de ese archivo cuando recorta uno.

Helpers para validar el backend dentro del test:

```python
from tracereports import conexiones_del_test, esperar_conexion

c = esperar_conexion(page, lambda c: c["method"] == "POST" and "/auth" in c["url"])
assert c["status"] == 302
```

`conexiones_del_test` devuelve las llamadas del test actual; `esperar_conexion` espera una que cumpla
la condición.

Ejemplo completo (Page Objects, workflow multi-test, tests negativos y fallas simuladas):
[`examples/orangehrm`](../../examples/orangehrm).
