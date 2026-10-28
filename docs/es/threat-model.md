# Modelo de amenazas

[English](../en/threat-model.md) · Se revisa en cada versión menor. Última revisión: el epic de
hardening de seguridad (#34).

TraceReports recibe evidencia de ejecuciones reales de tests y la muestra a un equipo. Este documento dice
qué protege, de quién, por qué puertas, qué frena hoy cada amenaza y qué queda en manos de quien lo opera.
Es la referencia de la [guía de hardening](security-hardening.md) y de `tracereports doctor --security`.

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

## Actores

- **Clientes de tests y CI** (de confianza para escribir): envían resultados con el token de ingesta.
- **Integrantes del equipo** (de confianza para leer): ven los reportes con el login de la UI.
- **Administrador**: tiene `TRACEREPORTS_TOKEN`, cambia Ajustes y usa la CLI en el host.
- **Atacante en la red**: cualquiera que llegue al puerto sin credenciales (otro usuario de la red, un
  equipo comprometido, internet si se publicó por error).
- **Usuario interno malicioso o descuidado**: alguien válido que exporta o reenvía más de lo debido.
- **Tercero comprometido**: el proveedor de IA, un espacio de chat, un tracker o una dependencia del build.
- **Robo del host o de un backup**: obtiene una copia de `DATA_DIR` o de un respaldo, sin el proceso.

## Superficies y mitigaciones

| Superficie | Amenazas | Mitigaciones vigentes |
| --- | --- | --- |
| **API de ingesta** (`POST /api/v1/...`) | Escrituras sin credenciales; inundación; subidas maliciosas (zip bombs, imágenes falsas) | Token o token de ingesta obligatorio; **fail-closed** al escuchar en la red sin credenciales (#37); límites de tamaño por endpoint; tipo de imagen/trace/video verificado por contenido; límites de archivos y de tamaño descomprimido en el ZIP de Allure; claves de idempotencia |
| **UI y API de lectura** | Leer reportes sin login; DNS rebinding; llegar desde la red equivocada | Login HTTP Basic; sin login solo `Host` locales o permitidos; **allowlist de IP de origen** `TRACEREPORTS_ALLOWED_CIDRS` con proxies de confianza (#38); `GET /healthz` no devuelve datos |
| **Ajustes** | Cambiar el proveedor o la key de IA para sacar evidencia | Solo con `TRACEREPORTS_TOKEN` (nunca el de ingesta); `TRACEREPORTS_SETTINGS_LOCKED=1`; `AI_ALLOWED_PROVIDERS` se valida también al guardar (#39); la key de IA se guarda cifrada |
| **Evidencia en reposo** | Robo del host o de un backup; restos tras borrar | Permisos solo del dueño (`0700`/`0600`); retención con `secure_delete`, `VACUUM` y checkpoint del WAL (#35); **cifrado opcional en la aplicación** de capturas, traces, videos y DOM (AES-256-GCM, #42); cifrado del volumen (operador) |
| **Contenido de las capturas** | Campos sensibles capturados | Enmascarado en los cuatro clientes antes de la captura; política del servidor `TRACEREPORTS_SCREENSHOTS=failures/off` (#45) |
| **Secretos dentro de la evidencia** | Tokens y claves guardados o reenviados | Redacción antes de guardar (headers, claves, patrones), activa por defecto; los secretos nunca van a los logs |
| **Proveedores de IA** | Evidencia que sale de la red; inyección de prompts desde la salida de los tests | Por defecto el **motor local de reglas, sin red** (#43); perfil de LLM local (#44); allowlist de proveedores; destinos de salida registrados al arrancar; el diagnóstico es texto de apoyo, nunca ejecuta acciones |
| **Notificaciones** (Teams, Slack) | Nombres de tests y errores en un chat con más audiencia | `NOTIFY_DETAIL=minimal`: solo conteos y un link (#40) |
| **Trackers** (GitHub, Jira, Azure DevOps) | Evidencia copiada a otro sistema | Solo por una acción explícita de un administrador (token de administración o acciones de la UI), nunca automática; la captura del fallo va adjunta, así que el tracker la hereda |
| **FixLab Request Lab** | El servidor usado como proxy hacia hosts internos (SSRF) | En un servidor compartido solo envía a los hosts con que habló el test más `TRACEREPORTS_LAB_HOSTS`; `TRACEREPORTS_LAB=off` lo desactiva; los secretos del Lab quedan en el equipo que corre `repro` |
| **Servidor MCP** (`tracereports mcp`) | Un agente con herramientas demasiado poderosas | Corre local por stdio con las credenciales del propio usuario; no escucha en la red |
| **Exportaciones** (ZIP, reporte estático) | Evidencia que sale del servidor | Requiere acceso de lectura; el ZIP va **descifrado** a propósito (quien exporta ya la veía): trátalo como la evidencia misma |
| **Cadena de suministro** | Binario o imagen alterados; dependencia vulnerable | Releases firmados (cosign sin llaves), SBOM y procedencia SLSA (#36); govulncheck, CodeQL y Scorecard en cada cambio; actions fijadas por SHA; un solo binario estático, sin CGO |
| **Contenedor** | Escape o persistencia tras un compromiso | Imagen sin root, sistema de archivos de solo lectura, `cap_drop: ALL`, `no-new-privileges` (compose endurecido) |

## Riesgos residuales

Conocidos y aceptados, o en manos del operador. Cada uno se menciona en la guía de hardening.

1. **La base SQLite no la cifra la aplicación.** Las capturas de red, errores y pasos dependen del cifrado
   del volumen. El spike #41 midió un SQLite cifrado en Go puro (`ncruces/go-sqlite3` con los VFS `xts` o
   `adiantum`): `xts` costó +57 % al ingerir en Linux y consultas 3-4 veces más lentas, más que el 30 %
   aceptado. Esos VFS además **no autentican las páginas** (XTS y Adiantum preservan el largo: una página
   alterada se descifra a basura en vez de fallar) y no son implementaciones certificadas. Si una versión
   futura adopta uno, este aviso aplica.
2. **Texto en claro que queda en el disco.** Reescribir la evidencia (`tracereports evidence encrypt`) o
   borrarla no garantiza que los bloques viejos se borren en SSD o sistemas de archivos copy-on-write.
   Mitigación: volumen cifrado.
3. **La gestión de claves es del operador.** Perder `TRACEREPORTS_DB_KEY` pierde la evidencia cifrada;
   filtrarla junto con un backup la expone. El servidor rechaza una clave dentro de `DATA_DIR`, nada más.
4. **HTTP Basic sin TLS.** El servidor no termina TLS; debe hacerlo un proxy. Sin él, la clave de la UI
   viaja en claro.
5. **El enmascarado en los clientes depende de selectores.** Un campo sensible sin selector se captura.
   La política de capturas es el respaldo.
6. **Los proveedores de IA en la nube ven la evidencia redactada.** La redacción atrapa formas conocidas de
   secretos, no todo dato personal. Usa el motor local o un LLM local cuando la evidencia no puede salir.
7. **Un solo mantenedor.** Los tiempos de respuesta dependen de una persona; ver [GOVERNANCE.md](../../GOVERNANCE.md).
8. **Pre-1.0.** La API REST todavía puede cambiar; ver la [política de estabilidad de la API](api-stability.md).

## Revisión

Los cambios que agregan una superficie (un endpoint, un destino de salida, un tipo nuevo de evidencia
guardada) actualizan este documento en el mismo pull request. Un revisor distinto del autor lo revisa
antes de cada versión menor.
