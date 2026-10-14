# RFC: FixLab

Estado: borrador · Rama: `feature/fixlab` · Fecha: 2026-10-14

## 1. Problema

TraceReports ya explica qué falló: agrupa los fallos en incidentes, muestra la llamada de backend
que los causó, genera mocks y el cURL de esa llamada. Lo que falta es el paso siguiente: **reproducir
el fallo y comprobar si sigue ocurriendo o si ya quedó resuelto**. Hoy, para eso, un dev copia el
cURL, descubre que los datos sensibles están enmascarados (`<masked>`), busca cómo conseguir una
sesión válida, arma la petición a mano en Postman y compara la respuesta a ojo.

FixLab lleva ese trabajo al reporte: desde un incidente o una llamada de red se abre el **Lab**, que
reenvía la petición real con una sesión nueva y muestra el veredicto.

## 2. Objetivos y fuera de alcance

Objetivos de la primera versión (MVP):

- Abrir en el Lab cualquier llamada capturada (panel de red) o la llamada causal de un incidente.
- Detectar qué valores enmascarados hacen falta y cuáles se pueden conseguir solos (sesión, CSRF).
- Reenviar contra el backend original u otro entorno (por ejemplo `http://localhost:3000`).
- Editar la petición antes de enviarla (método, ruta, headers, body), como en Postman.
- Mostrar original vs ahora y un veredicto: **se reproduce**, **ya no falla**, **falta sesión**,
  **error distinto** o **sin conexión**.
- Señalar cuando la respuesta original parece fabricada por el propio test (mock/`route.fulfill`).

Fuera de alcance del MVP (fases siguientes, sección 9): guardar checks, botón "Verificar" con estado
✅ en el incidente, cápsula descargable, subcomandos `repro`/`lab`/`verify` de la CLI, servidor MCP,
reintentar el test completo en un runner.

## 3. Experimento 0: ¿alcanza lo que ya capturamos?

Antes de diseñar se midió con datos reales (copia de la base local, sin modificarla) y con una app de
prueba con login JWT.

### 3.1 OrangeHRM (sesión por cookie + CSRF)

18 ejecuciones, 15 tests fallidos, 413 llamadas de API. Los fallos de backend se inyectaban desde el
test con `page.route(...)`.

| Tipo de fallo | Tests | ¿Aplica el Lab? |
|---|---|---|
| Backend (500, conexión rechazada, timeout) | 12 | Sí: tienen una llamada causal |
| Selector desactualizado | 3 | No: no hay red involucrada (lo cubre el sugeridor de selectores) |

El 100 % de las llamadas causales tiene valores enmascarados: `cookie` (12), `password` (8) y el
token CSRF `_token` (6).

| Llamada | Fallo original | Reenvío tal cual | Reenvío con sesión nueva |
|---|---|---|---|
| `GET /api/v2/pim/employees` | timeout | 401 "Session expired" | 200 con datos |
| `POST /auth/validate` | conexión rechazada | 302 al login | 302 al dashboard |
| `POST /auth/validate` | 500 | 302 al login | 302 al dashboard |

"Sesión nueva" = repetir la petición de login **que ya estaba grabada**, con la contraseña escrita
por la persona y un `_token` fresco leído de la página de login.

Además, las 4 respuestas 500 fabricadas por el test no traían los headers `date` ni `server`, que
todo servidor HTTP real envía: se pueden señalar como "posible respuesta simulada".

### 3.2 App con login JWT (Bearer)

Una tienda de prueba: `POST /api/login` devuelve `{"accessToken": …}` y `GET /api/orders` exige
`Authorization: Bearer …`. Con el backend roto (500), un test de Playwright grabó:

- el login, con `password` enmascarado en el body y `accessToken` enmascarado en la respuesta;
- la llamada que falló, con `authorization` enmascarado entero (se pierde el prefijo `Bearer`).

| Paso | Resultado |
|---|---|
| Reenvío tal cual | 401 "token expired" |
| Sesión nueva (login grabado + contraseña → `accessToken` → `Bearer`) | **500, igual al original: se reproduce** |
| Después de arreglar el backend | **200: ya no falla** |

### 3.3 Conclusiones

1. Sin sesión el Lab no sirve (401/302 al login, que además confunden). Con el paso de sesión
   automático funcionó en el 100 % de los casos probados, con cookies y con Bearer.
2. El enmascarado marca exactamente las variables necesarias.
3. El veredicto "ya no falla / no se reproduce contra el backend real" es tan valioso como
   "se reproduce": separa un bug real de un fallo transitorio, de entorno o simulado.
4. Huecos de captura: el HTML de las páginas no se guarda (el token CSRF se pide en vivo) y hay
   respuestas JSON 2xx sin body. Ninguno bloquea el MVP.

### 3.4 Validación de la implementación con los datos reales

Con el MVP terminado se reenviaron desde la API del Lab las 12 llamadas causales de OrangeHRM (la
misma copia de la base, contra el demo público). Primer intento: 10 de 12 en "falta sesión". Causa:
en las grabaciones más antiguas el `_token` CSRF quedó guardado **sin enmascarar**, y el Lab lo
reenviaba tal cual; un token CSRF es de la sesión grabada, así que siempre está vencido. Corrección:
los campos anti-CSRF se leen en vivo de la página **siempre**, estén o no enmascarados (y, si no hay
página de dónde leerlos, se deja el valor grabado). Después: **12 de 12 con el veredicto correcto**
("ya no falla": los fallos los inyectaba el test), y una contraseña incorrecta da "falta sesión".

Límite: dos aplicaciones. Faltan OAuth con refresh, firmas por petición (HMAC) y mTLS; esos casos
caen en "falta sesión" y la persona completa los valores a mano.

## 4. Experiencia

Punto de entrada: **el reporte**, no el PR.

1. En un incidente de backend o en una llamada del panel de red: botón **Abrir en Lab**.
2. El Lab se abre en el panel lateral con la petición pre-cargada. Los valores enmascarados aparecen
   como variables: `{{password}}` para lo que escribe la persona, y "automático" para lo que el Lab
   consigue solo (cookie de sesión, `Bearer`, CSRF).
3. **Entorno**: por defecto el host original; se puede cambiar (por ejemplo a `localhost:3000`).
4. **Enviar**: se ve cada paso (login, CSRF, la llamada), original vs ahora y el veredicto.
5. **Copiar cURL** de la petición final (con las variables, nunca con los valores).

## 5. Diseño

### 5.1 Plan (servidor, `internal/lab`)

`lab.Prepare(conns, targetID)` arma un plan a partir de la red grabada del test:

- **Petición objetivo**: método, URL, headers y body de la llamada.
- **Variables**: cada valor enmascarado (`<masked>`, `***`) en headers, URL o body. Se clasifican:
  - `session`: `cookie` y `authorization` → los resuelve el paso de sesión.
  - `csrf`: claves tipo `_token`, `csrf…`, `xsrf…` → se leen en vivo de la página que origina la
    petición (header `referer`) o de la última página cargada antes. Aunque estén grabadas sin
    enmascarar: pertenecían a la sesión grabada (ver 3.4).
  - `secret`: el resto (contraseñas, API keys) → los escribe la persona.
- **Paso de sesión**: la última llamada anterior a la objetivo que (a) devolvió un token enmascarado
  en un campo JSON tipo `accessToken`/`token`/`jwt`, o (b) envió una contraseña enmascarada (login
  por formulario, la sesión queda en las cookies). Sus propias variables se completan igual.
- **Original**: status o error, extracto del body y la marca "posible respuesta simulada"
  (sin `date` ni `server`).

### 5.2 Ejecución

`lab.Run` ejecuta los pasos con un `http.Client` propio: cookie jar por envío, sin seguir
redirecciones (se muestran), timeout de 15 s por paso y respuesta acotada a 256 KB. El `Bearer` se
arma con el token nuevo. El veredicto:

| Veredicto | Regla |
|---|---|
| `reproduced` | mismo status que el original, o el original sin respuesta y ahora tampoco |
| `fixed` | el original falló (≥ 400 o sin respuesta) y ahora responde < 400 |
| `session` | ahora 401/403, o redirección a una URL de login, y el original no lo era |
| `different` | otro error |
| `network` | no se pudo conectar |

### 5.3 API

| Ruta | Qué hace |
|---|---|
| `GET /api/v1/network/{id}/lab` | El plan: petición con `{{variables}}`, variables, paso de sesión, original, si este usuario puede enviar y por qué no |
| `POST /api/v1/ui/lab/send` | Ejecuta: `{conn_id, values, base_url, request?}` → pasos, respuesta y veredicto |

## 6. Seguridad

El Lab hace que el servidor envíe peticiones HTTP: es una capacidad de SSRF y se trata como tal.

- **Quién**: la misma regla que las acciones de la UI (`guardUIWrite`): token de admin, login de la
  UI o, sin tokens configurados, solo desde la misma máquina. El token de ingesta nunca puede.
- **A dónde**:
  - Servidor en la máquina del dev (petición desde loopback, sin proxy): cualquier host.
  - Servidor compartido: solo los hosts que aparecen en la red grabada de ese test y los de
    `TRACEREPORTS_LAB_HOSTS` (lista separada por comas, admite `*.dominio`).
  - Siempre bloqueadas: direcciones link-local (incluye `169.254.169.254`, metadatos de nube) y
    esquemas que no sean `http`/`https`.
  - `TRACEREPORTS_LAB=off` desactiva el Lab.
- **Secretos**: los valores que escribe la persona viajan solo en ese envío; nunca se guardan ni se
  registran en logs. La respuesta y los pasos pasan por la política de enmascarado antes de volver a
  la UI (un token nuevo no queda visible).
- **CSRF del propio TraceReports**: JSON + mismo origen (`jsonSameOrigin`), como el resto de acciones.
- Reporte exportado (ZIP): no hay servidor; el Lab no aparece.

## 7. Compatibilidad

Sin cambios de esquema ni en los clientes. Solo lectura de la red ya grabada. Rutas nuevas, nada
existente cambia.

## 8. Cómo se mide

- % de incidentes de backend en los que el Lab llega a un veredicto distinto de `session`.
- Tiempo desde abrir el incidente hasta el primer veredicto.
- En las fases siguientes: % de incidentes verificados como resueltos con evidencia.

## 9. Fases siguientes

| Fase | Qué |
|---|---|
| 2 ✅ | Guardar la petición como **check** (status esperado + campos), botón **Verificar** y estado ✅ del incidente con el antes/después |
| 3 ✅ | **Cápsula** descargable y subcomandos `tracereports repro` y `verify` para servidores compartidos y CI (`lab` quedó dentro de `repro`: `-base` apunta a otro entorno) |
| 4 | Exportar a Postman / Bruno / `.http` |
| 5 | Servidor **MCP** para que Claude Code u otro agente lea el incidente y proponga el arreglo |
| 6 | Capturar el HTML de la página que origina un login (CSRF sin pedirlo en vivo) |
