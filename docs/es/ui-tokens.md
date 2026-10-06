# Tokens de UI y componentes

🌐 [English](../en/ui-tokens.md) · **Español**

La interfaz es HTML + CSS + JavaScript sin frameworks ni librerías (salvo Chart.js para los
gráficos). Esta guía es para quien quiera agregar un componente o un tema nuevo.

## Archivos

| Archivo | Contenido |
|---|---|
| `web/style.css` | Paleta base, tokens semánticos y el layout del reporte |
| `web/themes.css` | Los 6 temas: cada uno redefine solo la paleta base |
| `web/features.css` | Componentes nuevos: locators, replay, chips, drawer, mocks, indicador en vivo, métricas, ajustes |
| `web/tracereports_features.js` | Controladores reutilizables (`window.TraceReportsFeatures`) |
| `web/app.js` | La aplicación: estado, rutas, render y conexión con la API |
| `web/i18n.js` / `web/i18n.en.js` | Idioma: traduce la interfaz en el DOM / diccionario español → inglés |

## Dos capas de variables

1. **Paleta base** (`--bg`, `--surface`, `--text`, `--primary`, `--pass`, `--fail`…): la define
   cada tema en `themes.css`.
2. **Tokens semánticos** (`--color-*`): se definen **una sola vez**, en `body` dentro de
   `style.css`, a partir de la paleta base.

Los tokens se declaran en `body` y no en `:root` a propósito: el tema se aplica con
`body[data-theme="…"]`. Si estuvieran en `:root`, se calcularían con la paleta por defecto y no
cambiarían con el tema.

**Regla para componentes nuevos:** usar solo `--color-*`, nunca colores fijos ni la paleta base.
Así el componente funciona en los 6 temas sin tocar `themes.css`.

| Token | Uso |
|---|---|
| `--color-bg` | Fondo de la página |
| `--color-surface` | Tarjetas y paneles |
| `--color-surface-raised` | Superficie dentro de una tarjeta (bloques de código claros, celdas) |
| `--color-surface-hover` | Hover de filas y botones |
| `--color-border` | Bordes y separadores |
| `--color-text` / `--color-text-muted` | Texto principal / secundario |
| `--color-heading` | Títulos |
| `--color-primary` / `--color-on-primary` | Acción principal y su texto |
| `--color-accent` / `--color-accent-soft` | Acento de la IA y fondos suaves de resalte |
| `--color-success` / `--color-danger` / `--color-warning` / `--color-info` | Estados (pass, fail, warning, info) |
| `--color-focus` | Anillo de foco del teclado |
| `--color-highlight` | Fila o paso activo (replay, locator) |
| `--color-canvas` / `--color-canvas-text` | Lienzo del replay: oscuro en todos los temas para que la captura destaque |
| `--color-overlay` | Fondo detrás del drawer |
| `--color-code-bg` / `--color-code-text` | Código de los mocks y snippets |
| `--color-tooltip-bg` / `--color-tooltip-text` | Tooltips (sparkline de flaky, tips) |
| `--shadow-elevated` | Drawer y elementos flotantes |
| `--radius-lg` | Contenedores grandes (player, drawer) |
| `--motion-fast` / `--motion-base` / `--ease-out` | Duraciones y curva de las animaciones |

Con `prefers-reduced-motion: reduce` las animaciones (entrada de pasos, pulso del indicador en
vivo, drawer) se desactivan.

### Agregar un tema

Copia un bloque `body[data-theme="…"]` de `themes.css`, cambia la paleta base y agrégalo a la lista
`THEMES` de `app.js` (el selector de temas se arma desde ahí). Los tokens semánticos se recalculan solos. Solo hacen falta
reglas específicas si el tema cambia la tipografía: Pixel, por ejemplo, agranda el código en
`features.css` porque su fuente de mapa de bits se lee pequeña.

## Componentes

| Componente | Dónde aparece | Clases |
|---|---|---|
| AI Locator Recommender | Dentro de *AI Failure Triage* de un test fallido por un selector roto | `.loc-card`, `.loc-failed`, `.loc-item`, `.loc-item.ai-pick`, `.step-loc-flag`, `.bbox-view` |
| Time-Travel Replay | Pestaña *Replay* del test (si tiene capturas) | `.replay-panel`, `.tt-player`, `.tt-scrubber`, `.tt-step` |
| Flakiness | Lista de tests y cabecera del test | `.flaky-chip`, `.cf-tip`, `.cf-spark` |
| Network drift | Lista de tests y cabecera del test | `.drift-chip`, `.cf-table`, `.cf-bar` |
| Mock / stub | Conexiones con error en *Red* y panel de error del test | `.cf-btn-mock`, `.cf-code`, `.cf-masked` |
| En vivo | Barra superior y botón flotante de auto-scroll | `.live-pill[data-state]`, `.autoscroll-toggle`, `.row-enter` |
| Tips | Pestañas, navegación, filtros y botones de acción | `.cf-tooltip`, atributo `data-tip` |
| Métricas | Menú *Métricas* | `.kpi-grid`, `.kpi`, `.m-card`, `.hbars`, `.m-table` |
| Análisis con IA | Menú *IA* | `.ai-top`, `.incident-card`, `.rec-pill`, `.ai-fail` |
| Escalar | Menú *Escalar* | `.esc-layout`, `.esc-share`, `.esc-card` (diseño claro fijo: se comparte como imagen o correo) |
| Ajustes | Menú *Ajustes* | `.set-card`, `.prov-grid`, `.prov-card`, `.field`, `.ro-banner` |
| Drawer | Panel lateral compartido (bbox, drift, mocks) | `.cf-drawer-root`, `.cf-drawer` |

## `window.TraceReportsFeatures`

| API | Descripción |
|---|---|
| `copyText(texto, botón)` | Copia al portapapeles y muestra "¡Copiado!" en el botón (con respaldo para `file://`) |
| `download(nombre, contenido, tipo)` | Descarga un archivo generado en el navegador |
| `Drawer.open({title, subtitle, body, onClose})` | Abre el panel lateral y devuelve el contenedor del body. `Drawer.close()`, `Drawer.isOpen()` |
| `sparkline(estados)` | Mini gráfico de barras PASS/FAIL (de la más antigua a la más reciente) |
| `new TimeTravelPlayer(contenedor, pasos, {start, index, speed, onChange, onLightbox})` | Reproductor de pasos: `play()`, `pause()`, `toggle()`, `go(i)`, `setSpeed(1\|2)`, `setSteps(pasos, i)`, `destroy()` |
| `new LiveStream(url, {onEvent, onStatus})` | Cliente SSE con reconexión: `start()`, `stop()`. Estados: `live`, `reconnecting`, `off` |
| `MockGenerator.open(conexión, {subtitle})` | Drawer con el stub en Playwright (Python/JS), Cypress y WireMock |
| `curlOf(conexión)` | Comando cURL (bash) que repite el request (también se importa en Postman); los headers enmascarados pasan a variables de entorno |
| `maskValue(valor, clave)` | Enmascara RUT, contraseñas, tokens y similares |
| `Tips.init()` | Tooltips de ayuda para todo elemento con `data-tip` (más `data-tip-title`, `data-tip-keys` y `data-tip-pos="right\|top"`). Aparecen con hover y con el foco del teclado |

## Accesibilidad

- El drawer es un `role="dialog"` modal: atrapa el foco con Tab, cierra con Escape y devuelve el
  foco al botón que lo abrió.
- Replay: `Espacio` reproduce o pausa, `←` / `→` cambian de paso, `Inicio` / `Fin` van al primero o al
  último.
- El indicador en vivo es `role="status"` con `aria-live="polite"`; el auto-scroll es un
  `role="switch"` con `aria-checked`.
- Los chips de flaky son enfocables y muestran el tooltip también con el foco.
- Para explicar qué hace una opción se usa `data-tip`, no `title`: el `title` nativo tarda en
  aparecer, no tiene estilo y no existe con teclado ni en pantallas táctiles.

## Indicador en vivo

Solo aparece si la ejecución que se está viendo sigue corriendo:

| Estado | Se ve | Cuándo |
|---|---|---|
| `live` | Rojo, punto que late (grabando) | Ejecución en curso y conexión SSE activa |
| `reconnecting` | Amarillo, "Reconectando…" | Se cortó la conexión con el servidor; reintenta sola |
| `stale` | Gris, "Sin actividad" | La ejecución sigue abierta pero no recibe pasos hace más de 5 minutos (el proceso de tests terminó sin cerrarla) |

En pantallas de menos de 480 px queda solo el punto.

## Idioma (i18n)

La interfaz se escribe en español en `app.js` y `index.html`. `i18n.js` la traduce **en el DOM**
mientras se dibuja (texto y los atributos `placeholder`, `title`, `aria-label`, `data-tip`…), con
el diccionario de `i18n.en.js`. Las plantillas no necesitan cambiar.

- Texto nuevo en la UI → agrega su traducción a `EN.exact` (frase completa) o, si lleva datos
  (números, nombres), un patrón en `EN.patterns`.
- Lo que no pasa por el DOM (etiquetas de gráficos en canvas, código generado) usa
  `TraceReportsI18n.t("texto", {variables})`.
- El contenido del usuario no se traduce: nombres de tests, pasos, errores, código y el texto de
  la IA. Marca con `data-no-i18n` cualquier otro elemento que deba quedar tal cual.
