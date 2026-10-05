# Cliente JavaScript / TypeScript

🌐 [English](../en/javascript.md) · **Español**

El cliente vive en [`client/js`](../../client/js) (paquete `tracereports`, Node 18+, sin
dependencias, ESM con tipos). Sirve para **Playwright Test** (reporter + fixtures), **Selenium
WebDriver** y cualquier runner (node:test, Mocha, Jest…).

```bash
npm install --save-dev ../ruta/a/tracereports/client/js     # desde el repositorio
```

Como los otros clientes, nunca rompe ni frena tu suite: la evidencia sale en segundo plano desde una
cola con reintentos e `Idempotency-Key` (un reintento no duplica pasos); si el servidor no responde,
los métodos son no-op y `cr.delivery` dice qué no llegó. No tiene cola en disco: lo que no se pudo
enviar al cerrar se cuenta en `delivery.lost`.

Variables que lee: `TRACEREPORTS_URL` (default `http://localhost:8080`), `TRACEREPORTS_TOKEN`,
`TRACEREPORTS_DISABLED=1`, `TRACEREPORTS_PROJECT`, `TRACEREPORTS_RUN_NAME`, `TRACEREPORTS_ENV`, `TRACEREPORTS_RUN_ID` (unirse
a una ejecución ya creada, p. ej. shards), `TRACEREPORTS_FLUSH_TIMEOUT` (segundos), `TRACEREPORTS_STRICT=1` y
la rama y el commit (`TRACEREPORTS_BRANCH` / `TRACEREPORTS_COMMIT`, variables de los CI más comunes o `git`).
Si una variable no está en el entorno, se lee del `.env` del proyecto
([detalle](configuration.md#el-env-del-proyecto-en-los-clientes)).

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
  **snapshot del DOM** (recomendador de locators) y una captura si el proyecto no la toma ya;
- `TRACEREPORTS_STRICT=1` (u opción `strict`) hace fallar la corrida si parte de la evidencia no llegó
  o si el servidor no confirmó el cierre de la ejecución (`delivery.runNotClosed`).

Ejemplo completo: [`examples/playwright-js`](../../examples/playwright-js).

## Selenium WebDriver (o cualquier runner)

```js
import { TraceReports } from "tracereports";
import { screenshot, withDriver } from "tracereports/selenium";

const cr = new TraceReports();
await cr.startRun("Regresión web", { environment: "chrome", framework: "selenium" });

await cr.test("Login correcto", withDriver(driver, { key: "login.test.js > ok", category: "smoke" }), async (t) => {
  await driver.get(URL);
  t.info("Formulario de login");
  t.screenshot(await screenshot(driver), "Formulario", "PASS");
});

await cr.finishRun();   // espera la cola y cierra la ejecución
```

`withDriver` adjunta la **captura** y el **snapshot del DOM** si el test falla y vuelve a lanzar el
error para que el runner lo vea. Selenium no expone la red del navegador: para la pestaña Red usa
Playwright o envía las llamadas con `t.network([...])`.

Ejemplo completo con `node:test`: [`examples/selenium-js`](../../examples/selenium-js).

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
