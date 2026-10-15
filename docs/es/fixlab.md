# FixLab: reproduce un fallo y verifica el arreglo

🌐 [English](../en/fixlab.md) · **Español**

El reporte te dice *por qué* falló un test. FixLab es el paso siguiente: **¿sigue fallando?** Desde
una llamada de red fallida o un incidente de backend reenvías la petición real con una sesión nueva,
la comparas con la ejecución, guardas cómo es una respuesta correcta y, cuando alguien arregla el
backend, lo verificas. Desde el reporte, la terminal, CI o un agente de IA.

> **Pruébalo en dos minutos:** `tracereports demo` y sigue el [recorrido sugerido](demo.md#recorrido-sugerido).
> La tienda de la demo tiene un backend que puedes romper y arreglar.

- [El Lab](#el-lab)
- [Checks y Verificar](#checks-y-verificar)
- [Exportar a Postman, Bruno o .http](#exportar-a-postman-bruno-o-http)
- [En tu terminal y en CI: la cápsula](#en-tu-terminal-y-en-ci-la-cápsula)
- [Para agentes de IA: MCP](#para-agentes-de-ia-mcp)
- [Seguridad](#seguridad)
- [Límites](#límites)
- [API](#api)

## El Lab

**Abrir en Lab** aparece en una llamada fallida de la pestaña **Red** y en un incidente de backend
(abre la llamada que lo causó).

1. **La petición, tal como se grabó.** Método, URL, headers y body. La evidencia se enmascaró al
   guardarse, así que cada valor enmascarado se vuelve una variable:
   - **Automáticas**: la sesión (`cookie`, `authorization`) y los tokens anti-CSRF (`_token`,
     `csrf…`, `xsrf…`). El Lab repite el **login grabado** para conseguir una sesión nueva (sus
     cookies, o el token de su respuesta JSON como `Bearer`) y lee los tokens anti-CSRF en vivo de
     la página donde empieza el flujo.
   - **Tuyas**: lo que nadie puede adivinar, como la contraseña o una API key. Las escribes tú.
2. **Entorno.** Por defecto el host de la ejecución; cámbialo para probar tu arreglo, por ejemplo
   `http://localhost:3000`.
3. **Edita** la petición si quieres, como en Postman: método, ruta, headers, body.
4. **Enviar.** Ves cada paso (login, CSRF, la llamada), la respuesta original junto a la nueva y el
   veredicto:

| Veredicto | Cuándo |
| --- | --- |
| **Se reproduce** | El mismo status que en la ejecución, o sin respuesta entonces y ahora |
| **Ya no falla** | Falló en la ejecución y ahora responde menos de 400. Si nadie lo arregló, el fallo fue transitorio o del entorno |
| **Falta sesión** | Ahora 401/403 o redirección al login. Revisa la contraseña o escribe el header a mano |
| **Otro error** | Un error distinto al de la ejecución |
| **Sin conexión** | No se pudo llegar al backend desde donde corre el Lab |

Cuando la respuesta original no trae los headers `date` y `server`, que todo servidor real envía, el
Lab avisa que probablemente **la fabricó el test** (un mock o `route.fulfill`).

**Copiar cURL** da la petición final con `$VARIABLES`, nunca con los valores que escribiste.

## Checks y Verificar

Después de un envío, **Guardar como check** guarda la llamada (tal como la editaste) y cómo es una
respuesta correcta: el **status esperado** y textos que el body debe **contener**.

El incidente muestra entonces **Verificar (n)**: ejecuta sus checks (vuelve a pedir los secretos, que
nunca se guardan) y deja el resultado en el incidente, **Verificado: resuelto** o **Verificado: sigue
fallando**, con el antes y el después de cada check. Los checks se borran con su ejecución.

## Exportar a Postman, Bruno o .http

**Postman**, **Bruno** o **.http** descargan la petición (tal como la editaste) con sus
`{{variables}}` y sin valores. Si hay un login grabado, va primero:

- **Postman**: un script guarda su token en la variable `token`.
- **.http** (REST Client de VS Code): la llamada lo lee de la petición con nombre `login`.
- **Bruno**: se describe en la documentación de la petición.

Las cookies quedan a cargo de la herramienta.

## En tu terminal y en CI: la cápsula

**Descargar cápsula** da un archivo JSON con el contexto de la ejecución, el comando que corre el
test en local y el plan de cada llamada con sus checks. Solo contiene datos enmascarados, como el
reporte. **Copiar comando** copia el comando que la lee del servidor.

```bash
# Muestra cómo correr el test en local y reenvía cada llamada desde tu máquina
tracereports repro 'http://localhost:8080/api/v1/network/42/capsule'

# Ejecuta los checks de un incidente; código de salida 1 si alguno no pasa
tracereports verify 'http://localhost:8080/api/v1/runs/7/lab/capsule?incident=<clave>'

# Contra tu arreglo local, con la contraseña desde el entorno
TRACEREPORTS_LAB_PASSWORD=… tracereports verify -base http://localhost:3000 capsula.json
```

- Las llamadas salen **desde tu máquina**, así que llegan a donde ella llega: una VPN, `localhost`,
  una red de staging.
- **Valores**: `-var password=…` (se puede repetir), `TRACEREPORTS_LAB_<NOMBRE>`
  (`TRACEREPORTS_LAB_PASSWORD`) o, en una terminal, escritos sin mostrarse. Los flags van antes o
  después de la cápsula.
- **Leer de un servidor protegido**: `TRACEREPORTS_TOKEN` o `TRACEREPORTS_UI_USER` /
  `TRACEREPORTS_UI_PASSWORD`. Solo se envían al servidor de `TRACEREPORTS_URL`: una URL de cápsula de
  otro lugar nunca los recibe.
- Antes de usar un secreto, el comando muestra a dónde irá (`password → POST https://…/login`).
  Revísalo cuando la cápsula no es tuya.

Como paso de CI después de un despliegue:

```yaml
- name: El incidente del pago quedó resuelto
  run: tracereports verify "$CAPSULE_URL"
  env:
    TRACEREPORTS_URL: ${{ vars.TRACEREPORTS_URL }}
    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
    TRACEREPORTS_LAB_PASSWORD: ${{ secrets.SHOP_QA_PASSWORD }}
    CAPSULE_URL: ${{ vars.TRACEREPORTS_URL }}/api/v1/runs/7/lab/capsule?incident=<clave>
```

## Para agentes de IA: MCP

`tracereports mcp` es un servidor MCP (stdio) para Claude Code o cualquier cliente MCP:

| Herramienta | Qué hace |
| --- | --- |
| `list_runs` | Las últimas ejecuciones con sus contadores |
| `get_incidents` | Los incidentes de una ejecución con su evidencia, causa probable, checks y verificación |
| `get_test` | El error, los pasos, las llamadas fallidas, la consola y el comando para correr un test |
| `reproduce` | Reenvía una llamada, como el Lab |
| `verify_fix` | Ejecuta los checks de un incidente para confirmar un arreglo |

```bash
claude mcp add tracereports \
  -e TRACEREPORTS_URL=http://localhost:8080 \
  -e TRACEREPORTS_LAB_PASSWORD=… \
  -- tracereports mcp
```

Agrega `TRACEREPORTS_TOKEN` o `TRACEREPORTS_UI_USER` / `TRACEREPORTS_UI_PASSWORD` si el servidor los
pide. Solo **lee** de TraceReports; reproducir y verificar corren desde tu máquina con los secretos de
`TRACEREPORTS_LAB_<NOMBRE>` del entorno del servidor MCP, así que el agente nunca los ve. Nunca cambia
código ni integra nada: eso queda en manos del agente y de tu revisión.

## Seguridad

El Lab hace que el servidor envíe peticiones HTTP, así que se trata como una capacidad de SSRF.

- **Quién** puede enviar: la regla de las acciones de la UI. Es decir, el token de admin, el login
  de la UI o, sin login, solo la máquina del propio servidor. El token de ingesta nunca puede.
- **A dónde** puede enviar:
  - Desde la máquina del propio servidor, a cualquier host, como tu terminal.
  - En un servidor compartido, solo a los hosts que llamó el test más `TRACEREPORTS_LAB_HOSTS`.
  - Las direcciones link-local (metadatos de nube, `169.254.169.254`) y los esquemas que no sean
    `http`/`https` siempre se bloquean.
  - Las redirecciones se muestran, no se siguen.
- **Secretos**: los valores que escribes se usan solo en ese envío; nunca se guardan ni quedan en
  logs. La respuesta pasa por la política de enmascarado antes de mostrarse, así que un token nuevo
  queda oculto.
- El **ZIP exportado** no tiene Lab: no hay servidor desde donde enviar.

| Variable | Por defecto | Qué hace |
| --- | --- | --- |
| `TRACEREPORTS_LAB` | activo | `off` desactiva el Lab |
| `TRACEREPORTS_LAB_HOSTS` | — | Hosts extra a los que puede llamar el Lab de un servidor compartido, separados por comas (`staging.acme.com`, `api.acme.com:8443`, `*.acme.internal`) |
| `TRACEREPORTS_LAB_<NOMBRE>` | — | Valores de las variables para `repro`, `verify` y `mcp` (`TRACEREPORTS_LAB_PASSWORD`) |

## Límites

- Las variables salen del enmascarado: con `TRACEREPORTS_REDACT=off` no se enmascara nada y el Lab
  no puede reconstruir una sesión.
- Probado con sesiones por cookie con CSRF (OrangeHRM) y logins JWT con `Bearer`. OAuth con refresh
  tokens, firmas por petición (HMAC) y mTLS terminan en **Falta sesión**; escribe esos valores a mano.
- Reenvía las llamadas, no el test completo. Para correr el test, usa el comando que muestran el
  reporte y `repro`.

## API

| Método | Ruta | Qué hace |
| --- | --- | --- |
| GET | `/api/v1/network/{id}/lab` | El plan: la petición con `{{variables}}`, las variables, el paso de sesión, la respuesta original y si puedes enviar y por qué no |
| POST | `/api/v1/ui/lab/send` | `{conn_id, values, base_url?, request?}` → pasos, respuesta y veredicto |
| POST | `/api/v1/ui/lab/checks` | `{conn_id, name, request?, base_url?, expect: {status, contains?}}` guarda un check |
| DELETE | `/api/v1/ui/lab/checks/{id}` | Borra un check |
| POST | `/api/v1/ui/lab/verify` | `{run_id, incident_key, check_ids?, values, base_url?}` ejecuta los checks y guarda el resultado |
| GET | `/api/v1/runs/{id}/lab` | Checks y última verificación de cada incidente de la ejecución |
| GET | `/api/v1/network/{id}/capsule` | Cápsula de una llamada |
| GET | `/api/v1/runs/{id}/lab/capsule?incident=<clave>` | Cápsula de un incidente, con sus checks |

Las rutas `/ui/` necesitan `Content-Type: application/json` y la regla de las acciones de la UI (ver
[API](api.md)). El diseño y las mediciones que lo respaldan: [RFC](../rfc/fixlab.md).
