# Modelo de amenazas

[English](../en/threat-model.md) · Se revisa en cada versión menor. Última revisión: el epic de
hardening de seguridad (#34), contrastada con el código.

TraceReports recibe evidencia de ejecuciones reales de tests y la muestra a un equipo. Este documento dice
qué protege, de quién, por qué puertas, qué frena hoy cada amenaza y qué queda en manos de quien lo opera.
Es la referencia de la [guía de hardening](security-hardening.md) y de `tracereports doctor --security`.
Donde un control es parcial, la tabla lo dice: una brecha honesta sirve más que una mitigación exagerada.

## Activos

| Activo | Dónde vive | Por qué importa |
| --- | --- | --- |
| **Evidencia** — capturas, traces de Playwright, videos, instantáneas del DOM | `DATA_DIR/screenshots`, tabla `test_dom` | Muestra lo que había en la pantalla: datos personales o de negocio de la aplicación probada |
| **Capturas de red** — URLs, headers, cuerpos de petición y respuesta | SQLite (`network`) | Pueden llevar tokens, cookies y datos personales |
| **Datos de ejecución** — nombres de tests, errores, pasos, logs | SQLite | Revelan hosts internos, usuarios y la forma del sistema |
| **Credenciales de TraceReports** — `TRACEREPORTS_TOKEN`, token de ingesta, clave de la UI | Entorno / `.env` | Control total o parcial del servidor |
| **Credenciales de terceros** — key de IA, tokens de trackers, webhooks, `TRACEREPORTS_LAB_<NOMBRE>` | Entorno; la key de IA opcionalmente en SQLite, cifrada con `TRACEREPORTS_SECRET_KEY` | Acceso a servicios pagados y a otros sistemas |
| **Claves** — `TRACEREPORTS_SECRET_KEY`, `TRACEREPORTS_DB_KEY` | Entorno o un archivo secreto, nunca en `DATA_DIR` | Quien las tiene lee lo que protegen |
| **Prompts y respuestas de la IA** | Se envían al proveedor configurado; los diagnósticos quedan en SQLite | El prompt se arma con la evidencia |

## Actores y qué puede hacer cada credencial

No hay roles más finos que estas credenciales. Planifica el acceso con esta tabla.

| Credencial | Envía resultados | Lee todo | Administra (Ajustes, acciones de la UI, tickets, Lab) |
| --- | --- | --- | --- |
| Token de ingesta (`TRACEREPORTS_INGEST_TOKEN`) — clientes de tests y CI | sí | **sí** | no |
| Token de administración (`TRACEREPORTS_TOKEN`) | sí | sí | sí |
| Login de la UI (`TRACEREPORTS_UI_USER`) — quien administra | — | sí | **sí** |
| Login de solo lectura (`TRACEREPORTS_UI_VIEWER_USER`) — el resto del equipo | no | sí | no |
| Sin credencial, mismo equipo, servidor sin credenciales (modo local) | sí | sí | sí |

Otros actores: un **atacante en la red** que llega al puerto sin credenciales; un **usuario interno
malicioso o descuidado** con una credencial válida; un **tercero comprometido** (proveedor de IA, espacio
de chat, tracker, una dependencia del build); quien **roba el host o un backup** con una copia de `DATA_DIR`.

## Superficies y mitigaciones

| Superficie | Amenazas | Mitigaciones vigentes | Brechas |
| --- | --- | --- | --- |
| **API de ingesta** (`POST /api/v1/...`) | Escrituras sin credenciales; subidas maliciosas (zip bombs, imágenes falsas) | Con credenciales configuradas se exige token; el servidor **se niega a escuchar en la red sin ninguna credencial** (#37); límites de tamaño por endpoint; imagen/trace/video verificados por contenido; límites de archivos y tamaño descomprimido del ZIP de Allure (50/200 MiB); claves de idempotencia | Sin límite de tasa general ni cuota de disco (pon un proxy con límites delante); timeout de lectura de 15 min por petición y de inactividad de 2 min |
| **Lecturas** (UI, API de lectura, `/screenshots/*`, export, SSE `/api/v1/stream`) | Leer reportes sin login; DNS rebinding | Login de la UI; cualquier token válido también lee; **allowlist de IP de origen** `TRACEREPORTS_ALLOWED_CIDRS` aplicada antes de confiar en headers de proxy (#38); sin login, solo `Host` locales o permitidos (anti DNS rebinding); CORS de `/screenshots/*` solo para el Trace Viewer de Playwright en `.zip`; `GET /healthz` no devuelve datos | **Con token y sin login de UI, las lecturas quedan abiertas** a quien llegue al puerto (el chequeo de `Host` no es control de acceso: el cliente elige el header). Stream en vivo limitado a 500 conexiones (503 al pasarse). Tras 10 logins o tokens fallidos en 15 min, la dirección recibe 429 el resto de la ventana |
| **Ajustes y acciones de la UI** | Cambiar el proveedor o la key de IA para sacar evidencia | Exigen el token de administración, el login de la UI o el modo local — nunca el token de ingesta; JSON + chequeo de `Origin` (CSRF); `TRACEREPORTS_SETTINGS_LOCKED=1` deja Ajustes de solo lectura; `TRACEREPORTS_AI_ALLOWED_PROVIDERS` se valida al guardar (#39); la key de IA se guarda cifrada | Una key guardada o del entorno solo se envía a la URL con que se configuró: cambiar la URL exige escribir la key de nuevo (409 si no), también en "Probar conexión". El login de solo lectura nunca administra. Un administrador que escribe una key aún puede enviarla a donde quiera: usa `SETTINGS_LOCKED=1` en servidores compartidos |
| **Escrituras de ingesta desde un navegador** (CSRF) | Una página web hace que el navegador de un desarrollador envíe ejecuciones falsas a un servidor local | Toda escritura que el navegador marca como de otro sitio (`Origin` de otro host, `Origin: null`, `Sec-Fetch-Site: cross-site`) se rechaza con 403; los clientes de tests no envían esos headers | |
| **UI web** | XSS almacenado desde nombres de tests y errores; framing | Los valores se escapan antes de insertar HTML; **Content-Security-Policy** (solo scripts de este servidor, los inline por hash, sin handlers inline), `X-Frame-Options: DENY` y `frame-ancestors 'none'`, `nosniff`, `Referrer-Policy: same-origin`; las vistas previas de HTML capturado van en un iframe `sandbox` y se sirven con `CSP: sandbox` | Los estilos admiten atributos inline |
| **Evidencia en reposo** | Robo del host o de un backup; restos tras borrar | Permisos solo del dueño (`0700`/`0600`); retención con `secure_delete`, `VACUUM` y checkpoint del WAL (#35); **cifrado opcional en la aplicación** de capturas, traces, videos y DOM (AES-256-GCM, #42); cifrado del volumen (operador) | La base en sí no la cifra la aplicación (ver riesgos residuales) |
| **Contenido de las capturas** | Campos sensibles capturados | Enmascarado en los cuatro clientes antes de la captura; política del servidor `TRACEREPORTS_SCREENSHOTS=failures/off` (#45) | El enmascarado depende de selectores |
| **Secretos dentro de la evidencia** | Tokens y claves guardados o reenviados | Redacción en el servidor antes de guardar (headers, claves, patrones), activa por defecto | Atrapa formas conocidas, no todo dato personal |
| **Proveedores de IA** | Evidencia que sale de la red; inyección de prompts desde la salida de los tests | Por defecto el **motor local de reglas, sin red** (#43); perfil de LLM local (#44); allowlist de proveedores; destinos de salida registrados al arrancar; el diagnóstico es texto de apoyo y nunca ejecuta acciones | Los proveedores en la nube reciben la evidencia redactada; ver la brecha de Ajustes |
| **Notificaciones** (Teams, Slack) | Nombres de tests y errores en un chat con más audiencia | `NOTIFY_DETAIL=minimal`: solo conteos y un link (#40) | Detalle completo por defecto |
| **Trackers** (GitHub, Jira, Azure DevOps) | Evidencia copiada a otro sistema | Solo por una acción explícita de administración (que incluye a cualquier usuario de la UI), nunca automática | La captura del fallo va adjunta: el tracker la hereda |
| **FixLab Request Lab** (UI, `repro`, MCP) | El servidor o un portátil usado para llegar a hosts internos (SSRF) | Desde la UI en un servidor compartido, solo los hosts registrados del test más `TRACEREPORTS_LAB_HOSTS`; direcciones link-local siempre bloqueadas; `TRACEREPORTS_LAB=off` lo desactiva | `repro`, `verify` y el MCP también se quedan en los hosts que llamó el test más `TRACEREPORTS_LAB_HOSTS` (y `-base` en la CLI); la URL base del agente nunca abre un host. Los hosts registrados igual vienen de datos ingeridos, y la UI en el propio equipo del servidor no restringe hosts (como la terminal del desarrollador) |
| **Servidor MCP** (`tracereports mcp`) | Un agente con demasiado poder | Local por stdio, no escucha en la red; lee con el token de ingesta o el login de solo lectura cuando existen, no con las credenciales de administración | Si solo está el token de administración, usa ese |
| **Exportaciones y reportes estáticos** (ZIP, `tracereports report`) | Evidencia que sale del servidor | Exportar requiere acceso de lectura; el ZIP va **descifrado** a propósito (quien exporta ya la veía) | `tracereports report` escribe sus archivos solo para el usuario (`0700`/`0600`); una vez copiado o publicado, trata su salida como la evidencia misma |
| **Grabaciones offline y mirror de los clientes** | Evidencia en el disco del CI | El replay se limita a endpoints de ingesta; carpetas `0700` y archivos `0600` (Linux, macOS) en los cuatro clientes | Las grabaciones quedan **antes** de la redacción del servidor |
| **Cadena de suministro** | Binario o imagen alterados; dependencia vulnerable | Releases firmados (cosign sin llaves), SBOM y procedencia SLSA (#36); govulncheck y CodeQL en cada pull request; Scorecard semanal y en `main`; actions de terceros fijadas por SHA (las `actions/*` de GitHub por tag de versión); un solo binario estático, sin CGO | |
| **Contenedor** | Escape o persistencia tras un compromiso | Imagen distroless sin root, sistema de archivos de solo lectura, `cap_drop: ALL`, `no-new-privileges` (compose endurecido) | |

## Riesgos residuales

Conocidos y aceptados, o en manos del operador. La guía de hardening y `doctor --security` los señalan.

1. **Un token sin login de UI deja las lecturas abiertas.** Configura el login, o restringe la red con
   `TRACEREPORTS_ALLOWED_CIDRS` / un firewall.
2. **Roles gruesos.** El token de ingesta lee todo (el CI descarga el ZIP con él): un token de CI
   filtrado es una lectura completa de la evidencia. Dale al equipo el login de solo lectura, no el de
   administración.
3. **Quien administra elige el endpoint de IA.** La key guardada no se puede redirigir, pero un
   administrador puede configurar una URL nueva con una key que escribe. Bloquea Ajustes
   (`TRACEREPORTS_SETTINGS_LOCKED=1`) en servidores compartidos.
4. **Sin límite de tasa general ni cuota de disco.** Los logins fallidos se frenan y el stream en vivo
   tiene tope, pero un cliente con un token válido todavía puede inundar el servidor: pon un proxy con
   límites delante de un servidor compartido.
5. **Los estilos de la UI admiten atributos inline** (`style-src 'unsafe-inline'`); los scripts no.
6. **La base SQLite no la cifra la aplicación.** Las capturas de red, errores y pasos dependen del cifrado
   del volumen. El spike #41 midió un SQLite cifrado en Go puro (`ncruces/go-sqlite3` con los VFS `xts` o
   `adiantum`): `xts` costó +57 % al ingerir en Linux y consultas 3-4 veces más lentas, más que el 30 %
   aceptado. Esos VFS además **no autentican las páginas** (XTS y Adiantum preservan el largo: una página
   alterada se descifra a basura en vez de fallar) y no son implementaciones certificadas. Si una versión
   futura adopta uno, este aviso aplica.
7. **Texto en claro que queda en el disco.** Reescribir la evidencia (`tracereports evidence encrypt`) o
   borrarla no garantiza que los bloques viejos se borren en SSD o sistemas copy-on-write. Mitigación:
   volumen cifrado.
8. **Archivos fuera del servidor.** Los reportes estáticos y las grabaciones de los clientes son
   privados de su usuario, pero las grabaciones todavía no se redactan y los artefactos del CI siguen las
   reglas de acceso del CI. Mantenlos en workspaces y artefactos privados.
9. **Los destinos del Lab vienen de datos ingeridos.** Un reenvío se queda en los hosts que registró el
   test, pero esos hosts los envió quien tiene el token de ingesta; la UI en el propio equipo del servidor
   no tiene restricción.
10. **La gestión de claves es del operador.** Perder `TRACEREPORTS_DB_KEY` pierde la evidencia cifrada;
    filtrarla con un backup la expone. El servidor rechaza una clave dentro de `DATA_DIR`, nada más.
11. **HTTP Basic sin TLS.** El servidor no termina TLS; debe hacerlo un proxy.
12. **El enmascarado en los clientes depende de selectores**; la política de capturas es el respaldo.
13. **Un solo mantenedor.** Los tiempos de respuesta dependen de una persona; ver [GOVERNANCE.md](../../GOVERNANCE.md).
14. **Pre-1.0.** La API REST todavía puede cambiar; ver la [política de estabilidad de la API](api-stability.md).

## Revisión

Los cambios que agregan una superficie (un endpoint, un destino de salida, un tipo nuevo de evidencia
guardada) actualizan este documento en el mismo pull request. Un revisor distinto del autor lo contrasta
con el código antes de cada versión menor.
