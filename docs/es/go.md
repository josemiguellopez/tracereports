# Cliente Go y playwright-go

🌐 [English](../en/go.md) · **Español**

El cliente vive en [`client/go`](../../client/go) (paquete `tracereports`, solo librería estándar).

```bash
go get github.com/josemiguellopez/tracereports/client/go@latest
```

```go
import tracereports "github.com/josemiguellopez/tracereports/client/go"
```

> Es un módulo propio dentro del repositorio: sus versiones se publican con tags `client/go/vX.Y.Z`
> (sin tag, `@latest` toma el último commit de la rama principal). El ejemplo
> [`examples/playwright-go`](../../examples/playwright-go) usa un `replace` hacia `../../client/go`
> para compilar contra el código del mismo checkout; en tu proyecto no lo necesitas.

Al igual que el de Python, el cliente es *best effort*: usa timeouts cortos, reintenta dos veces
los envíos fallidos (sin conexión, 5xx, 408, 429) con la misma `Idempotency-Key` —así un reintento
no duplica pasos— y, tras 3 fallos de conexión seguidos, deja de intentar durante 30 s en vez de
esperar el timeout en cada test. A diferencia del de Python, los envíos son síncronos (no hay cola
en segundo plano). Un `*tracereports.Test` nil es seguro de usar, así que si el servidor no responde los
tests siguen corriendo.

La red se envía en lotes de hasta 8 MiB o 200 llamadas (el servidor acepta 48 MiB por solicitud);
una sola llamada de más de 40 MiB se envía sin sus bodies, marcada `body_truncated` con su tamaño
original (`body_size`, en bytes UTF-8).

**Sin servidor**: si la ejecución no se puede crear (servidor caído o token incorrecto), la evidencia no
se pierde: se graba en `./tracereports-offline/<sesión>` y `tracereports report <carpeta>` arma el reporte
HTML, o `tracereports push <carpeta>` la sube después. Ver [Sin servidor](offline.md).

Lee `TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`, `TRACEREPORTS_DISABLED`, `TRACEREPORTS_PROJECT`, `TRACEREPORTS_RUN_ID` (unirse
a una ejecución ya creada) y la rama y el commit de `TRACEREPORTS_BRANCH` / `TRACEREPORTS_COMMIT`, de las
variables de los CI más comunes o de `git`.

Copia local mientras se envía: `c.Offline = "both"` o `TRACEREPORTS_OFFLINE=both`; detalles y conservación en [sin servidor](offline.md).

## API

```go
c := tracereports.New("")                                  // $TRACEREPORTS_URL o http://localhost:8080
c.StartRun("Regresión web", "staging")

// identidad estable: paquete + t.Name() (incluye subtests). El historial sigue a la key, no al nombre.
t, _ := c.StartTestWithKey(tracereports.Key("shop/login", "TestLoginOK"), "Login correcto", "login, smoke", "El admin entra al Dashboard")
t.Info("Abrir el login")
png, _ := page.Screenshot()
t.Screenshot(png, "Formulario de login", tracereports.Info)
t.Pass("Usuario dentro")
t.Network(conexiones)                                 // opcional: pestaña Red (antes de Finish)
t.Finish(tracereports.Pass, "", "")                        // o tracereports.Fail, "mensaje", "traza"

c.FinishRun()
fmt.Println(c.ReportURL())
```

| Método | Descripción |
|---|---|
| `New(url)` | Cliente (`Token` y `HTTP` se pueden ajustar) |
| `StartRun(name, env)` / `FinishRun()` | Abre y cierra la ejecución (`Project`, `Branch` y `Commit` del cliente definen su contexto) |
| `FinishRunInterrupted()` | Cierra la ejecución como incompleta (suite cancelada) |
| `StartTestWithKey(key, name, category, description)` | Inicia un test con identidad estable (`tracereports.Key(paquete, t.Name())`) |
| `StartTest(name, category, description)` | Igual, identificado por su nombre (historial aproximado) |
| `Test.Log(status, msg)`, `Info`, `Pass`, `Fail`, `Warn` | Pasos |
| `Test.Screenshot(png, msg, status)` | Captura como paso |
| `Test.Network([]tracereports.Conn)` | Llamadas de red del test |
| `Test.DOM(snapshot)` | Al fallar: snapshot de la página (ver `domScript` en `examples/playwright-go`) para recomendar selectores |
| `Test.Finish(status, errorMessage, errorTrace)` | Cierra el test. `FAIL` dispara la IA |
| `Test.FinishAttempts(status, msg, trace, attempts)` | Cierra un test que se reintentó: un `PASS` con `attempts` > 1 se muestra como *pasó tras reintento* |
| `Conn.Expected` | Marca una respuesta negativa esperada por el test (no cuenta como error ni como causa) |

## Ejemplo con playwright-go

[`examples/playwright-go`](../../examples/playwright-go) contiene tests idiomáticos (`go test`) contra
OrangeHRM, con capturas, captura de red y reporte del fallo.

```bash
cd examples/playwright-go
go test -v ./...                          # descarga el driver y Chromium la primera vez
BROWSER_CHANNEL=chrome go test -v ./...   # usa tu Chrome instalado
HEADLESS=0 go test -v ./...               # ver el navegador
```

Piezas del ejemplo:

- `network.go`: captura de red de una página (`OnRequest`, `OnResponse`, `OnRequestFinished`,
  `OnRequestFailed`) con enmascarado de datos sensibles. El body se lee en `RequestFinished`,
  dentro del evento, porque es el único momento en que playwright-go puede leerlo de forma fiable.
- `dom.go`: el snapshot de la página que se envía al fallar.
- `orangehrm_test.go`: `TestMain` abre el navegador y la ejecución. El helper `run(...)` crea una
  página por test, reporta pasos y capturas y, al terminar, envía la red y el resultado, con
  captura automática si el test falla.

> Las llamadas que siguen en vuelo cuando el test navega a otra página no tienen body
> disponible: es una limitación del navegador, no del cliente.
