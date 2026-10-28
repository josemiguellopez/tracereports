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

No hay roles más allá de estas tres credenciales. Planifica el acceso con esta tabla.

| Credencial | Envía resultados | Lee todo | Administra (Ajustes, acciones de la UI, tickets, Lab) |
| --- | --- | --- | --- |
| Token de ingesta (`TRACEREPORTS_INGEST_TOKEN`) — clientes de tests y CI | sí | **sí** | no |
| Token de administración (`TRACEREPORTS_TOKEN`) | sí | sí | sí |
| Login de la UI (`TRACEREPORTS_UI_USER`) — **toda** persona que entra | — | sí | **sí** |
| Sin credencial, mismo equipo, servidor sin credenciales (modo local) | sí | sí | sí |

Otros actores: un **atacante en la red** que llega al puerto sin credenciales; un **usuario interno
malicioso o descuidado** con una credencial válida; un **tercero comprometido** (proveedor de IA, espacio
de chat, tracker, una dependencia del build); quien **roba el host o un backup** con una copia de `DATA_DIR`.

## Superficies y mitigaciones

| Superficie | Amenazas | Mitigaciones vigentes | Brechas |
| --- | --- | --- | --- |
| **API de ingesta** (`POST /api/v1/...`) | Escrituras sin credenciales; subidas maliciosas (zip bombs, imágenes falsas) | Con credenciales configuradas se exige token; el servidor **se niega a escuchar en la red sin ninguna credencial** (#37); límites de tamaño por endpoint; imagen/trace/video verificados por contenido; límites de archivos y tamaño descomprimido del ZIP de Allure (50/200 MiB); claves de idempotencia | Sin límite de tasa, sin cuota de disco, solo timeout de lectura de headers |
| **Lecturas** (UI, API de lectura, `/screenshots/*`, export, SSE `/api/v1/stream`) | Leer reportes sin login; DNS rebinding | Login de la UI; cualquier token válido también lee; **allowlist de IP de origen** `TRACEREPORTS_ALLOWED_CIDRS` aplicada antes de confiar en headers de proxy (#38); sin login, solo `Host` locales o permitidos (anti DNS rebinding); CORS de `/screenshots/*` solo para el Trace Viewer de Playwright en `.zip`; `GET /healthz` no devuelve datos | **Con token y sin login de UI, las lecturas quedan abiertas** a quien llegue al puerto (el chequeo de `Host` no es control de acceso: el cliente elige el header). Sin límite de suscriptores SSE. Sin freno a fuerza bruta en el login |
| **Ajustes y acciones de la UI** | Cambiar el proveedor o la key de IA para sacar evidencia | Exigen el token de administración, el login de la UI o el modo local — nunca el token de ingesta; JSON + chequeo de `Origin` (CSRF); `TRACEREPORTS_SETTINGS_LOCKED=1` deja Ajustes de solo lectura; `AI_ALLOWED_PROVIDERS` se valida al guardar (#39); la key de IA se guarda cifrada | Todo usuario de la UI administra. La allowlist valida el **id del proveedor, no su URL**: un administrador puede apuntar `base_url` a otro lado y la key guardada se reutiliza. Usa `SETTINGS_LOCKED=1` en servidores compartidos |
| **Escrituras de ingesta desde un navegador** (CSRF) | Una página web hace que el navegador de un desarrollador envíe ejecuciones falsas a un servidor local | Chequeo de `Host` (solo nombres locales en modo local) | Los endpoints de ingesta no validan `Origin` ni exigen JSON: en modo local sin token, un formulario de otro sitio puede crear ejecuciones |
| **UI web** | XSS almacenado desde nombres de tests y errores; framing | Los valores se escapan antes de insertar HTML; las vistas previas de HTML capturado van en un iframe `sandbox` y se sirven con `CSP: sandbox` y `nosniff` | Sin Content-Security-Policy ni `X-Frame-Options` en las páginas de la UI |
| **Evidencia en reposo** | Robo del host o de un backup; restos tras borrar | Permisos solo del dueño (`0700`/`0600`); retención con `secure_delete`, `VACUUM` y checkpoint del WAL (#35); **cifrado opcional en la aplicación** de capturas, traces, videos y DOM (AES-256-GCM, #42); cifrado del volumen (operador) | La base en sí no la cifra la aplicación (ver riesgos residuales) |
| **Contenido de las capturas** | Campos sensibles capturados | Enmascarado en los cuatro clientes antes de la captura; política del servidor `TRACEREPORTS_SCREENSHOTS=failures/off` (#45) | El enmascarado depende de selectores |
| **Secretos dentro de la evidencia** | Tokens y claves guardados o reenviados | Redacción en el servidor antes de guardar (headers, claves, patrones), activa por defecto | Atrapa formas conocidas, no todo dato personal |
| **Proveedores de IA** | Evidencia que sale de la red; inyección de prompts desde la salida de los tests | Por defecto el **motor local de reglas, sin red** (#43); perfil de LLM local (#44); allowlist de proveedores; destinos de salida registrados al arrancar; el diagnóstico es texto de apoyo y nunca ejecuta acciones | Los proveedores en la nube reciben la evidencia redactada; ver la brecha de Ajustes |
| **Notificaciones** (Teams, Slack) | Nombres de tests y errores en un chat con más audiencia | `NOTIFY_DETAIL=minimal`: solo conteos y un link (#40) | Detalle completo por defecto |
| **Trackers** (GitHub, Jira, Azure DevOps) | Evidencia copiada a otro sistema | Solo por una acción explícita de administración (que incluye a cualquier usuario de la UI), nunca automática | La captura del fallo va adjunta: el tracker la hereda |
| **FixLab Request Lab** (UI, `repro`, MCP) | El servidor o un portátil usado para llegar a hosts internos (SSRF) | Desde la UI en un servidor compartido, solo los hosts registrados del test más `TRACEREPORTS_LAB_HOSTS`; direcciones link-local siempre bloqueadas; `TRACEREPORTS_LAB=off` lo desactiva | Los hosts registrados vienen de datos ingeridos (quien tiene el token de ingesta los decide); desde el propio equipo del servidor, `repro` y las herramientas MCP no hay restricción de hosts |
| **Servidor MCP** (`tracereports mcp`) | Un agente con demasiado poder | Local por stdio, no escucha en la red | Se autentica con el token de administración, también para llamadas de solo lectura |
| **Exportaciones y reportes estáticos** (ZIP, `tracereports report`) | Evidencia que sale del servidor | Exportar requiere acceso de lectura; el ZIP va **descifrado** a propósito (quien exporta ya la veía) | `tracereports report` escribe sus archivos legibles por todos (`0644`/`0755`): trata su carpeta de salida como la evidencia misma |
| **Grabaciones offline y mirror de los clientes** | Evidencia en el disco del CI | El replay se limita a endpoints de ingesta | Las grabaciones quedan legibles por todos y **antes** de la redacción del servidor |
| **Cadena de suministro** | Binario o imagen alterados; dependencia vulnerable | Releases firmados (cosign sin llaves), SBOM y procedencia SLSA (#36); govulncheck y CodeQL en cada pull request; Scorecard semanal y en `main`; actions de terceros fijadas por SHA (las `actions/*` de GitHub por tag de versión); un solo binario estático, sin CGO | |
| **Contenedor** | Escape o persistencia tras un compromiso | Imagen distroless sin root, sistema de archivos de solo lectura, `cap_drop: ALL`, `no-new-privileges` (compose endurecido) | |

## Riesgos residuales

Conocidos y aceptados, o en manos del operador. La guía de hardening y `doctor --security` los señalan.

1. **Un token sin login de UI deja las lecturas abiertas.** Configura el login, o restringe la red con
   `TRACEREPORTS_ALLOWED_CIDRS` / un firewall.
2. **Sin roles.** El token de ingesta lee todo; todo usuario de la UI administra. Un token de CI filtrado
   es una lectura completa de la evidencia.
3. **Redirección del endpoint de IA.** Un administrador puede cambiar el `base_url` de la IA y reutilizar
   la key guardada. Bloquea Ajustes (`TRACEREPORTS_SETTINGS_LOCKED=1`) en servidores compartidos.
4. **Sin límite de tasa ni protección contra fuerza bruta**, sin cuota de disco ni límite de suscriptores
   SSE: pon un proxy con límites delante de un servidor compartido.
5. **Sin Content-Security-Policy en la UI**, y los endpoints de ingesta aceptan envíos de otros sitios en
   modo local.
6. **La base SQLite no la cifra la aplicación.** Las capturas de red, errores y pasos dependen del cifrado
   del volumen. El spike #41 midió un SQLite cifrado en Go puro (`ncruces/go-sqlite3` con los VFS `xts` o
   `adiantum`): `xts` costó +57 % al ingerir en Linux y consultas 3-4 veces más lentas, más que el 30 %
   aceptado. Esos VFS además **no autentican las páginas** (XTS y Adiantum preservan el largo: una página
   alterada se descifra a basura en vez de fallar) y no son implementaciones certificadas. Si una versión
   futura adopta uno, este aviso aplica.
7. **Texto en claro que queda en el disco.** Reescribir la evidencia (`tracereports evidence encrypt`) o
   borrarla no garantiza que los bloques viejos se borren en SSD o sistemas copy-on-write. Mitigación:
   volumen cifrado.
8. **Archivos fuera del servidor.** Los reportes estáticos y las grabaciones de los clientes quedan
   legibles por todos, y las grabaciones todavía no se redactan. Mantenlos en workspaces y artefactos
   privados del CI.
9. **Los destinos del Lab y de MCP vienen de datos ingeridos**, y no tienen restricción desde el propio
   equipo del servidor.
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
