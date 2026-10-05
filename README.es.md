<a href="https://github.com/josemiguellopez/tracereports"><img src="./.github/assets/banner.es.svg" alt="TraceReports: del fallo a la causa. Pasos, capturas, red y diagnóstico con IA para tus tests." width="100%" /></a>

<p align="center">
  <a href="https://github.com/josemiguellopez/tracereports/stargazers"><img alt="Estrellas en GitHub" src="https://img.shields.io/github/stars/josemiguellopez/tracereports?style=for-the-badge&labelColor=000000&color=c9fa6b" /></a>
  <a href="./LICENSE"><img alt="Licencia: Apache-2.0" src="https://img.shields.io/badge/licencia-Apache--2.0-c9fa6b.svg?style=for-the-badge&labelColor=000000" /></a>
  <img alt="Un solo binario de Go" src="https://img.shields.io/badge/un%20solo-binario%20go-fc9672.svg?style=for-the-badge&logo=go&logoColor=white&labelColor=000000" />
  <img alt="Autohospedado, sin telemetría" src="https://img.shields.io/badge/autohospedado-sin%20telemetr%C3%ADa-eeeee2.svg?style=for-the-badge&labelColor=000000" />
  <img alt="Estado: pre-1.0" src="https://img.shields.io/badge/estado-pre--1.0-e7c35a.svg?style=for-the-badge&labelColor=000000" />
</p>

<p align="center">
  <b>pytest</b> · <b>Playwright</b> · <b>Selenium</b> · <b>JUnit 5</b> · <b>Go</b> · cualquier lenguaje vía REST
  <br />
  🌐 <a href="README.md">English</a> · <b>Español</b>
</p>

<p align="center">
  <a href="#inicio-rápido"><b>Inicio rápido</b></a> ·
  <a href="#qué-incluye"><b>Funciones</b></a> ·
  <a href="#elige-tu-estilo"><b>Temas</b></a> ·
  <a href="#cómo-funciona"><b>Cómo funciona</b></a> ·
  <a href="#documentación"><b>Docs</b></a>
</p>

# TraceReports

**El reporte de pruebas que te dice *por qué* falló un test, no solo *que* falló.**

TraceReports es un servidor de reportes de testing autohospedado. Cada paso con su captura, cada
llamada HTTP que hizo el navegador y un diagnóstico con IA que convierte una pared roja en un
puñado de incidentes.

```bash
docker compose up -d                       # servidor + UI en http://localhost:8080
pip install pytest pytest-playwright ./client/python
pytest --tracereports                      # listo, sin cambiar tu código
```

<p align="center">
  <img src="./.github/assets/tour.gif" alt="Recorrido por una ejecución real de TraceReports: diagnóstico, triage con IA, timeline, red, locators rotos y escalamiento" width="100%" />
  <br />
  <sub>Una ejecución real de la <a href="examples/orangehrm">suite de ejemplo de OrangeHRM</a> con fallos de backend simulados, en el tema Pixel.</sub>
</p>

## El problema

Son las 9 de la mañana y la ejecución nocturna tiene 15 tests en rojo. Toca abrir los logs del
backend, reproducir en local y preguntar en Slack si alguien hizo un deploy.

TraceReports junta la evidencia mientras el test corre, así que el reporte ya sabe qué pasó:

<table>
  <tr>
    <th width="50%">😩 Un reporte típico</th>
    <th width="50%">🕵️ TraceReports</th>
  </tr>
  <tr>
    <td valign="top">
<pre>
FAILED test_login_admin
  TimeoutError: Timeout 30000ms exceeded
  waiting for "Dashboard" to be visible
FAILED test_pim_search
  TimeoutError: Timeout 30000ms exceeded
FAILED test_employee_list
  TimeoutError: Timeout 30000ms exceeded
... 12 más
</pre>
    </td>
    <td valign="top">
      <b>🔴 1 incidente · 15 tests</b>
      <br /><br />
      Todos los fallos ocurrieron justo después de que <code>POST /auth/login</code> respondiera <b>500</b>.
      <br /><br />
      <b>Causa probable:</b> el servicio de autenticación se quedó sin conexiones a la base de datos.
      <br />
      <b>Revisa primero:</b> el servicio de autenticación y su pool de conexiones.
      <br /><br />
      📸 captura · 🌐 llamada fallida con su body · ⏱️ timeline
    </td>
  </tr>
</table>

La causa que sugiere la IA siempre se presenta como hipótesis, junto a la evidencia en que se basa.

<p align="center"><img src="./.github/assets/divider.svg" alt="" width="100%" /></p>

## Qué incluye

<table>
  <tr>
    <td width="50%" valign="top">
      <h3>🧠 Diagnóstico de la ejecución</h3>
      Agrupa en incidentes los fallos que comparten evidencia (la misma llamada de backend fallida,
      la misma firma de error), con la causa probable y qué revisar primero.
      <br /><br />
      <img src="./.github/assets/diagnosis.png" alt="Diagnóstico que agrupa los fallos en incidentes de backend" />
    </td>
    <td width="50%" valign="top">
      <h3>⏱️ Timeline</h3>
      Pasos, capturas y llamadas de red en una sola línea de tiempo. Ves exactamente qué esperaba el
      test cuando el backend respondió 500.
      <br /><br />
      <img src="./.github/assets/timeline.png" alt="Timeline con pasos, capturas y llamadas de red" />
    </td>
  </tr>
  <tr>
    <td width="50%" valign="top">
      <h3>🌐 La red que vio el navegador</h3>
      Status, tiempos, headers y bodies, con <i>Copiar como cURL</i> y un stub para Playwright,
      Cypress o WireMock generado desde la llamada fallida. Contraseñas y cookies llegan ya
      enmascaradas.
      <br /><br />
      <img src="./.github/assets/network.png" alt="Pestaña Red con un POST fallido y la contraseña y la cookie enmascaradas" />
    </td>
    <td width="50%" valign="top">
      <h3>🎯 Locators rotos</h3>
      Cuando un selector deja de funcionar, lee el DOM del momento del fallo y propone reemplazos
      robustos, listos para copiar y marcados sobre la captura.
      <br /><br />
      <img src="./.github/assets/locators.png" alt="Selector roto con reemplazos sugeridos con get_by_role" />
    </td>
  </tr>
  <tr>
    <td width="50%" valign="top">
      <h3>📣 Escala en un clic</h3>
      Un resumen para Negocio, QA o Desarrollo con la captura, las llamadas fallidas y el
      diagnóstico. Cópialo como texto, Markdown, correo o imagen, o envíalo a Teams o Slack.
      <br /><br />
      <img src="./.github/assets/escalate.png" alt="Resumen de escalamiento para desarrollo con severidad y captura" />
    </td>
    <td width="50%" valign="top">
      <h3>▶️ Replay</h3>
      Reproduce el test paso a paso con sus capturas, como un video. Play, pausa, 1x/2x y atajos de
      teclado.
      <br /><br />
      <img src="./.github/assets/replay.png" alt="Replay paso a paso de un test de login fallido" />
    </td>
  </tr>
</table>

Y además:

- **Flaky o roto.** Historial por test que distingue un test flaky de un fallo persistente.
- **Regresiones de latencia.** Marca los tests cuya red se volvió más lenta (p95) que en ejecuciones anteriores.
- **Comparación de ejecuciones.** Fallos nuevos, tests arreglados y tests más lentos frente a la ejecución anterior.
- **En vivo.** Los pasos aparecen mientras los tests siguen corriendo.
- **Métricas.** Tasa de éxito, los tests que más tiempo hacen perder, los flaky y las causas de fallo en el tiempo.
- **Comparte sin servidor.** Exporta una ejecución como ZIP que se abre sin servidor ni internet.

La [guía completa](GUIA-COMPLETA.md) cubre todas las opciones.

<p align="center"><img src="./.github/assets/divider.svg" alt="" width="100%" /></p>

## Elige tu estilo

Seis temas que se cambian desde la interfaz: Trace, Trace Dark, Midnight, Paper, Terminal y, por
supuesto, Pixel.

<p align="center">
  <img src="./.github/assets/themes.gif" alt="El mismo reporte en los seis temas: Pixel, Trace, Trace Dark, Midnight, Paper y Terminal" width="100%" />
</p>

## Cómo funciona

```mermaid
flowchart LR
    T["Tus tests<br/>pytest · Playwright · Selenium · JUnit · Go"] -->|"pasos, capturas,<br/>red, DOM"| S["Servidor TraceReports<br/>un binario de Go + SQLite"]
    S --> UI["Reporte web"]
    S <-->|"evidencia enmascarada"| AI["Proveedor de IA<br/>Gemini · Claude · OpenAI · Ollama"]
    S --> N["Teams / Slack"]
```

Los clientes envían la evidencia en segundo plano, así que un servidor lento o caído nunca rompe
tus tests. El servidor enmascara los secretos antes de guardar nada, y la IA y los webhooks son
opcionales.

<p align="center"><img src="./.github/assets/divider.svg" alt="" width="100%" /></p>

## Inicio rápido

**1. Levanta el servidor.**

```bash
docker compose up -d     # o: go run ./cmd   (Go 1.26+)
```

**2. Reporta tus tests** con uno de los clientes:

| Cliente | Qué hace |
| --- | --- |
| [`client/python`](client/python) | Plugin de pytest: `pytest --tracereports`. Envío en segundo plano con reintentos y soporte para pytest-xdist. |
| [`client/js`](client/js) | Reporter y fixtures para Playwright Test, más helpers para Selenium WebDriver. JavaScript y TypeScript. |
| [`client/java`](client/java) | Extensión de JUnit 5 para Selenium y Playwright para Java. |
| [`client/go`](client/go) | Cliente de Go, con un ejemplo de playwright-go. |
| [API REST](docs/es/api.md) | Cualquier otro lenguaje o framework. |

**3. (Opcional) Activa la IA.** Define `GEMINI_API_KEY` (capa gratuita) o elige Claude, OpenAI,
cualquier API compatible u Ollama local en **Configuración**, sin reiniciar.

**4. (Recomendado si el servidor es compartido) Protégelo** con `TRACEREPORTS_TOKEN`. Mira
[configuración](docs/es/configuration.md#seguridad).

En [`examples/`](examples) hay ejemplos ejecutables para cada cliente, todos contra la demo
pública de OrangeHRM.

🚧 *TraceReports está antes de la 1.0: los clientes, la API y la configuración todavía pueden cambiar entre versiones menores.*

## Documentación

- [Guía completa](GUIA-COMPLETA.md)
- [Instalación](docs/es/installation.md) · [Docker](docs/es/docker.md) · [Configuración](docs/es/configuration.md)
- [Python](docs/es/python.md) · [JavaScript](docs/es/javascript.md) · [Java](docs/es/java.md) · [Go](docs/es/go.md) · [API REST](docs/es/api.md)
- [Integración continua](docs/es/ci.md) · [Tokens de UI y temas](docs/es/ui-tokens.md)

## Privacidad

TraceReports corre en tu propia máquina o servidor y no envía telemetría. Tokens, contraseñas,
cookies y credenciales se enmascaran antes de guardar nada, venga del cliente que venga. La
evidencia solo sale de tu servidor si activas un proveedor de IA en la nube (usa Ollama para
mantenerla en casa) o un webhook de Teams/Slack. Las capturas se guardan tal cual, así que evita
mostrar secretos en pantalla.

<p align="center"><img src="./.github/assets/divider.svg" alt="" width="100%" /></p>

## Apoya el proyecto

Si TraceReports te ahorró una mañana revisando logs, **dale una ⭐**. Es la forma más fácil de que
otros QA lo encuentren.

Y si quieres invitarme un café mientras sigo construyéndolo:

<a href="https://paypal.me/lopezjosemiguel"><img alt="Invítame un café con PayPal" src="https://img.shields.io/badge/inv%C3%ADtame%20un%20caf%C3%A9-paypal-fc9672.svg?style=for-the-badge&logo=paypal&logoColor=white&labelColor=000000" /></a>

## Licencia

[Apache-2.0](LICENSE). El gopher de Go en pixel art del banner está adaptado del gopher original de
Renée French, con licencia [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).

<p align="center">
  <a href="https://star-history.com/#josemiguellopez/tracereports&Date">
    <img src="https://api.star-history.com/svg?repos=josemiguellopez/tracereports&type=Date" alt="Historial de estrellas" width="600" />
  </a>
</p>
