# Demo, playground y selftest

🌐 [English](../en/demo.md) · **Español**

Tres formas de ver TraceReports funcionando sin conectarlo antes a tu proyecto. Las tres usan los
mismos **escenarios**: ejecuciones grabadas con los clientes reales (pytest + Playwright,
Playwright Test, Selenium + JUnit 5, `go test`, la API REST y una importación de JUnit XML) contra
una pequeña tienda de prueba.

| | Qué es | Para quién |
| --- | --- | --- |
| [`tracereports demo`](#tracereports-demo) | El servidor con datos de ejemplo y la tienda de prueba, en un minuto | Cualquiera que quiera probarlo |
| [Playground](#el-playground) | La página de la tienda: envía los escenarios de cada cliente y mira lo que muestra TraceReports | Quien quiera ver qué envía cada cliente |
| [`tracereports selftest`](#tracereports-selftest) | Reproduce todos los escenarios y comprueba el resultado; sale con código 1 si algo falla | Quien despliega o actualiza TraceReports |

## `tracereports demo`

```bash
tracereports demo          # con el binario publicado
go run ./cmd demo          # desde un clon del repositorio
```

Muestra dos direcciones y abre el reporte en el navegador:

```text
  TraceReports demo · sample data ready (31 runs, 61 tests, 9 failed)

  ▸ Report:      http://localhost:8080/#run=31&view=tests
  ▸ Playground:  http://localhost:8180
```

**Qué trae:** un equipo que prueba la tienda con todos los clientes. Hay 12 noches de historia
(un test inestable y fallos que ya se arreglaron) y las ejecuciones de esta noche, cuando se rompió
el backend de pedidos. Cada ejecución es una grabación real, así que ves exactamente lo que envía
cada cliente: pasos, capturas, la red del navegador con la contraseña enmascarada, la consola, el
comando para correr cada test y el selector sugerido cuando uno cambió.

### Recorrido sugerido

1. La última ejecución se abre sola: fallaron 2 de 3 tests. Abre `test_checkout_pays_the_order`:
   pasos, la captura del navegador, la red (contraseña enmascarada), la consola y el comando para
   correrlo.
2. `test_add_to_cart_with_the_old_selector` falló porque cambió un selector: TraceReports sugiere el
   nuevo.
3. Pestaña **Red** → el `POST /api/orders` 500 → **Abrir en Lab** → contraseña `demo` → **Enviar**:
   el veredicto es *Se reproduce*.
4. En el playground pon el backend de pedidos a funcionar y envía de nuevo: *Ya no falla*.
   **Guardar como check** y, en el incidente, **Verificar**: *Verificado: resuelto*.
5. **Buscar**, **Métricas** y **Release**: la historia, el test inestable y los fallos ya resueltos.
6. En una terminal, `tracereports repro` / `verify` con el comando que copia el Lab, o
   `tracereports mcp` para Claude Code (`TRACEREPORTS_URL=http://localhost:8080`). Ver
   [FixLab](fixlab.md).

### Opciones

| Flag | Por defecto | Qué hace |
| --- | --- | --- |
| `-port` | el primer puerto libre desde 8080 | Puerto del reporte |
| `-shop-port` | el primer puerto libre desde 8180 | Puerto de la tienda y del playground |
| `-keep` | no | Conserva los datos al detener la demo (muestra la carpeta; `DATA_DIR=<carpeta> tracereports` la vuelve a abrir) |
| `-no-open` | no | No abre el navegador |
| `-ai` | no | Usa el proveedor de IA de tu entorno (`GEMINI_API_KEY`, `ANTHROPIC_API_KEY`…). **Gasta tokens** |

### Aislada de tu configuración

- Escucha solo en `127.0.0.1`: nada es accesible desde otros equipos y Windows no muestra el aviso
  del firewall.
- Ignora todas las variables `TRACEREPORTS_*`, el `.env` del proyecto, los webhooks de Teams/Slack,
  `PUBLIC_URL` y los trackers. La IA está apagada salvo con `-ai`.
- La base de datos vive en una carpeta temporal y se borra con Ctrl+C. Las carpetas de una demo que
  se cerró a la fuerza se limpian al iniciar la siguiente (después de 12 horas).

## El playground

La página principal de la tienda de prueba (<http://localhost:8180>). El resto de la tienda
(`/login`, `/products`, `/cart`…) es la aplicación contra la que se grabaron los escenarios.

- **Escenarios.** Una tarjeta por escenario, con filtros por cliente y por tipo (OK, NOK, inestable,
  suite). **Enviar** reproduce la grabación contra TraceReports con horas actuales y muestra las
  peticiones que envió, lo que comprobó (estados, incidente, red, contraseña enmascarada, captura,
  selector sugerido, Lab…) y un link a la ejecución.
- **Ejecutar todos y verificar.** Envía todos los escenarios con sus comprobaciones, como
  `tracereports selftest`.
- **Backend de la tienda.** Interruptores para romper o arreglar la tienda mientras pruebas el Lab a
  mano:

  | Interruptor | Efecto |
  | --- | --- |
  | Pedidos | `POST /api/orders` responde 500 (pool de conexiones agotado) |
  | Productos | `GET /api/products` tarda 3 s |
  | Recomendaciones | `GET /api/recommendations` falla una llamada sí y otra no (503) |

  Cada escenario pone los interruptores que necesita mientras se ejecuta y después deja los tuyos.
- **Terminal.** Registra cada escenario que envías, con su resultado.
- En español e inglés, accesible con teclado y respeta *reducir movimiento*. Las animaciones usan
  [Motion](https://motion.dev) (MIT), incluida en el binario: funciona sin internet.

### Escenarios

| Id | Cliente | Caso |
| --- | --- | --- |
| `rest-ok` / `rest-nok-backend` | API REST | pasa / el backend responde 500 |
| `pytest-ok` / `pytest-nok-backend` / `pytest-nok-selector` | pytest + Playwright | iniciar sesión / el pago falla (500) / cambió un selector |
| `playwright-ok` / `playwright-nok-backend` / `playwright-flaky` | Playwright Test (JS) | iniciar sesión / el pago falla (500) / pasa al reintentar |
| `selenium-ok` / `selenium-nok-selector` | Selenium + JUnit 5 (Java) | iniciar sesión / cambió un selector |
| `go-ok` / `go-nok-backend` | Go (`go test`) | la API pasa / la API responde 500 |
| `junit-import` | JUnit XML (Maven Surefire) | tests que pasan, fallan y se omiten, sin cliente |
| `pytest-suite`, `playwright-suite`, `selenium-suite`, `go-suite` | cada cliente | la suite completa en una ejecución |

## `tracereports selftest`

```bash
tracereports selftest                                        # un servidor temporal en este equipo
tracereports selftest -url https://tracereports.acme.com     # uno desplegado
tracereports selftest -list                                  # lista los escenarios
tracereports selftest -only pytest-nok-backend,go-ok         # algunos
```

Reproduce cada escenario contra la tienda de prueba y comprueba lo que muestra TraceReports: los
estados, el incidente, la red, que la contraseña llegue enmascarada, las capturas, el selector
sugerido, el test inestable, el comando para correr cada test y que el Lab reproduzca el fallo de
backend. Sale con código **1** si una expectativa no se cumple, así que sirve después de actualizar
o como paso de CI:

```yaml
- name: TraceReports sigue mostrando lo que envían los clientes
  run: tracereports selftest -url ${{ vars.TRACEREPORTS_URL }}
  env:
    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
```

Con `-url` envía `TRACEREPORTS_TOKEN` si está definido (un servidor protegido), y las ejecuciones de
los escenarios quedan en ese servidor (proyecto `demo-shop`). La tienda corre en el equipo que ejecuta
el selftest y el Lab se prueba desde ahí, así que el servidor no necesita llegar a ella. Las
comprobaciones necesitan el enmascarado por defecto: con `TRACEREPORTS_REDACT=off` el Lab no tiene
variables que reconstruir y esas comprobaciones fallan.

## Cómo se hacen los escenarios

El catálogo y sus expectativas están en
[`internal/demo/scenarios.go`](../../internal/demo/scenarios.go) y las grabaciones en
[`internal/demo/scenarios/`](../../internal/demo/scenarios), dentro del binario.
`go test ./internal/demo` las reproduce en CI, así que un cambio que rompe el camino de un cliente
al reporte falla ahí. Para volver a grabarlos o agregar uno, ver
[test/scenarios](../../test/scenarios/README.es.md).

## Problemas frecuentes

- **Un puerto está ocupado** (en Windows, Hyper-V reserva algunos rangos): la demo toma el siguiente
  libre; `-port` / `-shop-port` los eligen.
- **No se abrió el navegador:** abre la dirección *Report* que mostró.
- **El Lab dice "falta sesión"** en la demo: la contraseña de la tienda es `demo`.
- **Quiero seguir explorando mañana:** `tracereports demo -keep` y después
  `DATA_DIR=<carpeta> tracereports` con la carpeta que mostró.
