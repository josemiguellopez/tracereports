# Inicio rápido: la demo en 5 minutos

🌐 [English](../en/quickstart.md) · **Español**

Un solo comando levanta todo lo que necesitan la demo y el Lab, en tu equipo, sin instalar ni
configurar nada más. Esta página es el paso a paso; la [guía de la demo](demo.md) tiene los
detalles.

## Qué se levanta

`tracereports demo` levanta **dos servicios locales** en un solo proceso:

| Servicio | Dirección | Qué es |
| --- | --- | --- |
| **TraceReports** (el reporte) | <http://localhost:8080> | El servidor real: el reporte web y su API (`/api/v1/…`), con una base temporal ya cargada con ejecuciones |
| **Tienda de prueba + playground** | <http://localhost:8180> | Una pequeña tienda con un backend que puedes romper (`/login`, `/products`, `/cart`, `/api/…`). Su página principal es el **playground** |

Las ejecuciones del reporte se grabaron con los clientes reales contra esa tienda, así que cuando el
Lab reenvía una llamada fallida, llega de verdad al backend de la tienda. Los dos escuchan solo en
`127.0.0.1` (sin aviso del firewall y sin acceso desde otros equipos). Si un puerto está ocupado,
usa el siguiente libre y lo muestra.

## 1. Consíguelo

Requisitos: **Go 1.26+** y Git. Nada más: ni Docker, ni Node, ni Python, ni navegadores.

```bash
git clone https://github.com/josemiguellopez/tracereports.git
cd tracereports
go build -o tracereports ./cmd        # Windows: go build -o tracereports.exe ./cmd
```

Esto compila el binario `tracereports` una sola vez (cerca de un minuto). En Windows ejecútalo como
`.\tracereports.exe` en lugar de `./tracereports`.

> `tracereports demo` viene en las versiones posteriores a la 0.2.0. Con un binario descargado, sáltate
> la compilación.

## 2. Opcional: diagnóstico con IA

Sin API key funciona todo menos el diagnóstico con IA. Para tenerlo, pon tu proveedor en un archivo
`.env` en la raíz del repositorio (o en tu entorno):

```bash
# .env: Gemini tiene un nivel gratuito
AI_PROVIDER=gemini
AI_API_KEY=tu-clave
```

También sirven `GEMINI_API_KEY=…`, `ANTHROPIC_API_KEY=…` u `OPENAI_API_KEY=…` solos. La demo toma
**solo** estas variables de IA del `.env`: ni tokens, ni webhooks, ni trackers. Gasta tokens de tu
clave (una docena de diagnósticos); `-ai=false` la apaga.

## 3. Levántalo

```bash
./tracereports demo
```

Muestra:

```text
Loading the sample data…

  TraceReports demo · sample data ready (31 runs, 61 tests, 9 failed)

  ▸ Report:      http://localhost:8080/#run=31&view=tests
  ▸ Playground:  http://localhost:8180   (run each client's scenarios, break or fix the shop backend)
  …
  AI diagnosis: on (gemini, …), with the key of your environment or .env: it spends tokens. -ai=false turns it off.
  The sample data is deleted when you stop it (-keep to keep it).
  Ctrl+C to stop.
```

y abre el reporte en el navegador. Deja la terminal abierta: al cerrarla se detienen los dos
servicios.

## 4. Pruébalo (5 minutos)

- [ ] **El reporte.** La última ejecución está abierta: fallaron 2 de 3 tests. Abre
      `test_checkout_pays_the_order`: pasos, captura, red (la contraseña llega enmascarada), consola,
      el comando para correrlo y, con IA, el diagnóstico.
- [ ] **El Lab.** Pestaña **Red** → `POST /api/orders` (500) → **Abrir en Lab** → contraseña `demo` →
      **Enviar**. Veredicto: **Se reproduce**.
- [ ] **Arregla el backend.** En el [playground](http://localhost:8180), pon el interruptor de
      **Pedidos** a funcionar. De vuelta en el Lab, **Enviar** otra vez: **Ya no falla**.
- [ ] **Verifica.** **Guardar como check** y, en el incidente, **Verificar**: **Verificado: resuelto**.
- [ ] **Los clientes.** En el playground, **Ejecutar todos y verificar** envía lo que grabó cada
      cliente (pytest, Playwright, Selenium, Go, REST, JUnit XML) y comprueba lo que muestra el
      reporte.

El usuario de la tienda es `qa@demo.shop` con la contraseña `demo`, también para abrirla en el
navegador.

## 5. Desde una terminal

Con la demo corriendo, en otra terminal en la raíz del repositorio:

```bash
# Reenvía la llamada fallida desde tu máquina ("Copiar comando" en el Lab te da la URL)
TRACEREPORTS_LAB_PASSWORD=demo ./tracereports repro 'http://localhost:8080/api/v1/network/<id>/capsule'

# Comprueba que TraceReports muestra lo que envía cada cliente (con su propio servidor temporal)
./tracereports selftest

# Deja que Claude Code lea los incidentes y verifique el arreglo
claude mcp add tracereports -e TRACEREPORTS_URL=http://localhost:8080 -e TRACEREPORTS_LAB_PASSWORD=demo -- "$PWD/tracereports" mcp
```

En PowerShell define antes la variable: `$env:TRACEREPORTS_LAB_PASSWORD="demo"`.

## 6. Detenlo

**Ctrl+C** detiene los dos servicios y borra los datos de ejemplo. Para conservarlos:

```bash
./tracereports demo -keep                  # muestra la carpeta al detenerse
DATA_DIR=<esa carpeta> ./tracereports      # vuelve a abrir el reporte después (solo el reporte, sin tienda)
```

| Flag | Qué hace |
| --- | --- |
| `-port 9090` | Puerto del reporte (por defecto: el primero libre desde 8080) |
| `-shop-port 9190` | Puerto de la tienda y del playground (por defecto: el primero libre desde 8180) |
| `-no-open` | No abre el navegador |
| `-ai=false` | Sin IA, aunque haya una clave |
| `-keep` | Conserva los datos de ejemplo |

## Problemas frecuentes

| Qué ves | Qué hacer |
| --- | --- |
| *Diagnóstico con IA desactivado en la demo* en un test fallido | No encontró una API key: agrégala al `.env` (paso 2) de la carpeta donde ejecutas el comando y vuelve a iniciar la demo |
| Los diagnósticos llegan tarde o alguno dice que el proveedor está ocupado | Los niveles gratuitos tienen límites por minuto; TraceReports reintenta. Espera un minuto o pulsa *Re-analizar* |
| El Lab dice **Falta sesión** | La contraseña de la tienda es `demo` |
| El Lab dice **Sin conexión** | La tienda no está corriendo (por ejemplo, un reporte reabierto con `DATA_DIR`): levanta `./tracereports demo` |
| Un puerto distinto de 8080/8180 | Uno estaba ocupado (en Windows, Hyper-V reserva algunos rangos); usa las direcciones que mostró |
| `go: command not found` | Instala Go 1.26+ desde <https://go.dev/dl/> |
