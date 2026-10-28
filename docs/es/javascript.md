# Cliente JavaScript / TypeScript

🌐 [English](../en/javascript.md) · **Español**

El cliente vive en [`client/js`](../../client/js) (paquete `tracereports`, Node 18+, sin
dependencias, ESM con tipos). Sirve para **Playwright Test** (reporter + fixtures), **Selenium
WebDriver** y cualquier runner (node:test, Mocha, Jest…).

```bash
npm install --save-dev tracereports                         # desde npm
npm install --save-dev ../ruta/a/tracereports/client/js     # o desde una copia de este repositorio
```

Como los otros clientes, nunca rompe ni frena tu suite: la evidencia sale en segundo plano desde una
cola con reintentos e `Idempotency-Key` (un reintento no duplica pasos); si el servidor no responde,
los métodos son no-op y `cr.delivery` dice qué no llegó. No tiene cola en disco: lo que no se pudo
enviar al cerrar se cuenta en `delivery.lost`.

La red se envía en lotes de hasta 8 MiB o 200 llamadas (el servidor acepta 48 MiB por solicitud);
una sola llamada de más de 40 MiB se envía sin sus bodies, marcada `body_truncated` con su tamaño
original (`body_size`, en bytes UTF-8).

**Sin servidor**: si la ejecución no se puede crear (servidor caído o token incorrecto), la evidencia no
se pierde: se graba en `./tracereports-offline/<sesión>` y `tracereports report <carpeta>` arma el reporte
HTML, o `tracereports push <carpeta>` la sube después. Ver [Sin servidor](offline.md).

Variables que lee: `TRACEREPORTS_URL` (default `http://localhost:8080`), `TRACEREPORTS_TOKEN`,
`TRACEREPORTS_DISABLED=1`, `TRACEREPORTS_PROJECT`, `TRACEREPORTS_RUN_NAME`, `TRACEREPORTS_ENV`, `TRACEREPORTS_RUN_ID` (unirse
a una ejecución ya creada, p. ej. shards), `TRACEREPORTS_FLUSH_TIMEOUT` (segundos), `TRACEREPORTS_STRICT=1` y
la rama y el commit (`TRACEREPORTS_BRANCH` / `TRACEREPORTS_COMMIT`, variables de los CI más comunes o `git`).
Si una variable no está en el entorno, se lee del `.env` del proyecto
([detalle](configuration.md#el-env-del-proyecto-en-los-clientes)).

Copia local mientras se envía: `new TraceReports({ offline: "both" })` o `TRACEREPORTS_OFFLINE=both`; detalles y conservación en [sin servidor](offline.md).

## Playwright Test (sin cambiar los tests)

```js
// playwright.config.js
export default defineConfig({
  reporter: [["list"], ["tracereports/reporter", { runName: "Regresión web", environment: "staging" }]],
  use: { screenshot: "only-on-failure" },
});
```

```js
// en los specs, importa `test` desde tracereports/playwright en vez de @playwright/test
import { test, expect } from "tracereports/playwright";

test("login inválido", async ({ page, tracereports }) => {
  tracereports.expectResponse(401, { url: "*/auth/validate*" });   // respuesta negativa esperada
  tracereports.info("Login con una clave incorrecta");               // paso propio
  ...
});
```

Si ya extiendes `test`, usa `withTraceReports(miTest)`.

Qué se reporta:

- una ejecución por corrida (todos los workers reportan en ella), con proyecto, rama y commit;
- **identidad estable** de cada test: `archivo > describe > título [proyecto de Playwright]`;
- los `test.step` como pasos, el error y el stack, las capturas adjuntas;
- **reintentos** (`retries`): los intentos quedan en el mismo test y la UI lo marca *pasó tras
  reintento*;
- el resultado respeta a Playwright: con `test.fail()`, fallar es el resultado esperado (se reporta
  PASS, con el error como paso de aviso) y pasar es un fallo (*se esperaba que fallara*);
- con los fixtures: **todas las llamadas HTTP del navegador** (pestaña Red) y, si falla, el
  **snapshot del DOM** (recomendador de locators) y una captura con los datos sensibles tapados;
- `TRACEREPORTS_STRICT=1` (u opción `strict`) hace fallar la corrida si parte de la evidencia no llegó
  o si el servidor no confirmó el cierre de la ejecución (`delivery.runNotClosed`).

Ejemplo completo: [`examples/playwright-js`](../../examples/playwright-js).

## Selenium WebDriver (o cualquier runner)

```js
import { TraceReports } from "tracereports";
import { screenshot, seleniumOptions, withDriver } from "tracereports/selenium";

// crea el driver con seleniumOptions(new chrome.Options()): logs de red y consola
const cr = new TraceReports();
await cr.startRun("Regresión web", { environment: "chrome", framework: "selenium" });

await cr.test("Login correcto", withDriver(driver, { key: "login.test.js > ok", category: "smoke" }), async (t) => {
  await driver.get(URL);
  t.info("Formulario de login");
  t.screenshot(await screenshot(driver), "Formulario", "PASS");
});

await cr.finishRun();   // espera la cola y cierra la ejecución
```

`withDriver` envía la **red** (pestaña Red, Lab) y la **consola** de cada test y, si falla, la
**captura** y el **snapshot del DOM**; vuelve a lanzar el error para que el runner lo vea. La red y la
consola salen de los logs de Chrome y Edge, así que crea el driver con `seleniumOptions(options)`.
Paso a paso: [Selenium](selenium.md).

Ejemplo completo con `node:test`: [`examples/selenium-js`](../../examples/selenium-js).

## Capturas sin datos sensibles

Antes de cada captura, el cliente **tapa** (un recuadro magenta) los elementos sensibles: siempre
`input[type=password]` y los que tienen el atributo `data-tracereports-mask`, más los selectores CSS de
`TRACEREPORTS_SCREENSHOT_MASK` (`"input[name=rut], .numero-tarjeta"`).
`TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS=off` deja de tapar los de siempre.

- **Playwright Test** con los fixtures de `tracereports/playwright`: al fallar se toma una captura con la
  opción `mask` de Playwright, y el reporter la sube **en vez de** la automática de
  `screenshot: "only-on-failure"`, que Playwright toma sin máscara. Solo con el reporter (sin los
  fixtures) la captura automática sube tal cual: usa los fixtures si en pantalla hay datos sensibles.
- **Selenium**: `screenshot(driver)` y `withDriver` ponen recuadros sobre esos elementos, capturan y los
  quitan, también si la captura falla (el DOM queda igual). Si no se puede tapar, no se captura.
- En tu código: `playwrightScreenshot(page)` (de `tracereports/playwright`) en vez de `page.screenshot()`.

`TRACEREPORTS_SCREENSHOTS` decide qué se envía: `all` (por defecto), `failures` (solo capturas de un paso
`FAIL`, y trace y video solo de tests fallidos) u `off` (nada de eso). El servidor aplica la misma
variable por su cuenta (ver [Configuración → Capturas de pantalla](configuration.md#capturas-de-pantalla)).

## API

| Método | Descripción |
|---|---|
| `new TraceReports({ baseUrl, token, ... })` | Cliente (todo opcional) |
| `startRun(name, { environment, project, branch, commit, framework })` / `finishRun({ interrupted })` | Abre y cierra la ejecución |
| `startTest(name, { key, category, description, suite, params, worker })` | Devuelve un `TraceTest` (no-op si el servidor no respondió) |
| `test(name, opts, fn)` | Corre `fn(t)` como test: lo cierra según el resultado; `opts.onFailure` adjunta evidencia |
| `t.info/pass/fail/warn/skip(msg)`, `t.log(status, msg)` | Pasos |
| `t.screenshot(bufferOBase64, msg, status)` | Captura (Buffer de Playwright o base64 de Selenium) |
| `t.network(conexiones)`, `t.expectResponse(status, { url, method })` | Red del test y negativos esperados |
| `t.dom(snapshot)` | Snapshot de la página (`DOM_SCRIPT`, `captureDom`) |
| `t.finish(status, { error, errorMessage, errorTrace, attempts })` | Cierra el test. `FAIL` dispara la IA |
| `cr.delivery`, `cr.flush(ms)`, `cr.reportUrl` | Estado de la entrega, esperar la cola, link |

Pruebas del cliente: `cd client/js && npm test`.
