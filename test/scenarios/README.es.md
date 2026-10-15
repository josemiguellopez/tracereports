# Escenarios grabados con los clientes reales

Cada escenario es una **grabación real** de un cliente de TraceReports (lo que de verdad envía pytest,
Playwright JS, Selenium Java o Go) corriendo contra la tienda de la demo (`internal/demo`), más lo que
TraceReports debe hacer con ella: estados, incidente, red, enmascarado de la contraseña, capturas,
sugerencia de selector, flaky, comando para correrlo y que el Lab lo reproduzca.

El catálogo y las expectativas están en [`internal/demo/scenarios.go`](../../internal/demo/scenarios.go);
las grabaciones, en [`internal/demo/scenarios/`](../../internal/demo/scenarios) (van dentro del binario).

## Para qué sirven

- **`tracereports selftest`** reproduce todos los escenarios contra un servidor (temporal o `-url`) y
  comprueba sus expectativas; sale con código 1 si algo no se cumple. `TestScenarios`
  (`go test ./internal/demo`) hace lo mismo en CI: si un cambio rompe el camino de un cliente al
  reporte, falla ahí.
- La demo y el playground usan las mismas grabaciones.

## Volver a grabar

Cuando un cliente cambia lo que envía, se vuelve a grabar su escenario (desde la raíz del repo):

```bash
go run ./test/scenarios/record                       # todos
go run ./test/scenarios/record -only pytest-ok       # uno o varios, separados por coma
go run ./test/scenarios/record -python .venv/Scripts/python
```

El grabador levanta la tienda en `127.0.0.1:18180`, ejecuta cada test con `TRACEREPORTS_OFFLINE=always`
(nunca usa un servidor) y normaliza la grabación: la URL de la tienda pasa a `http://__shop__` y las rutas
de la máquina que grabó (el repo, la carpeta del usuario) se reemplazan. Después: `go test ./internal/demo`.

Requisitos, uno por cliente:

| Cliente | Carpeta | Necesita |
|---|---|---|
| pytest + Playwright | `python/` | Python con `pytest pytest-playwright ./client/python` y `playwright install chromium` |
| Playwright Test (JS) | `js/` | Node 18+ y `npm install` en esa carpeta (el navegador: `npx playwright install chromium`) |
| Selenium + JUnit 5 | `java/` | Java 17 y Chrome (Selenium Manager trae el driver) |
| Go | `go/` | Go |
| REST y JUnit XML | — | nada: el grabador arma las llamadas de la API |

Un escenario nuevo: agrégalo a `Scenarios` con sus expectativas, escribe su test en el cliente que
corresponda, grábalo con `-only` y corre `go test ./internal/demo`.
