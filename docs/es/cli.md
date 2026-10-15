# Línea de comandos: `tracereports`

🌐 [English](../en/cli.md) · **Español**

Un solo binario hace todo: sin comando es el servidor; con un comando es una herramienta para tu
terminal o tu pipeline. `tracereports help` lista los comandos, y cada uno explica sus flags con `-h`.

| Comando | Qué hace | Necesita servidor |
| --- | --- | --- |
| [`tracereports`](#el-servidor) | Levanta el servidor y el reporte web | — |
| [`demo`](#demo) | El servidor con datos de ejemplo y una tienda de prueba, solo en este equipo | no (levanta el suyo) |
| [`report`](#report) | Un reporte HTML estático desde una grabación, JUnit XML o resultados de Allure | no |
| [`push`](#push) | Sube una grabación, allure-results o JUnit XML a un servidor | sí |
| [`pr-comment`](#pr-comment) | Comenta el resumen de la ejecución en el pull request o merge request | opcional |
| [`repro`](#repro) | Reenvía las llamadas de una cápsula de FixLab con una sesión nueva | no (un archivo o URL de cápsula) |
| [`verify`](#verify) | Ejecuta los checks de una cápsula; código de salida 1 si alguno falla | no (un archivo o URL de cápsula) |
| [`mcp`](#mcp) | Servidor MCP (stdio) para Claude Code y otros agentes | sí |
| [`selftest`](#selftest) | Reproduce los escenarios grabados con los clientes reales y comprueba el resultado | no (levanta el suyo) |
| [`secrets`](#secrets) | Estado y migración de las credenciales guardadas desde Ajustes | no (la carpeta de datos) |
| [`version`](#version) | Muestra la versión | no |

**Cómo conseguirlo:** descarga el binario de tu sistema de la
[última versión](https://github.com/josemiguellopez/tracereports/releases/latest), o compílalo desde el
repositorio con `go build -o tracereports ./cmd` (Windows: `tracereports.exe`, ejecútalo como
`.\tracereports.exe`). También sirve `go run ./cmd <comando>`. Con Docker:
`docker run --rm ghcr.io/josemiguellopez/tracereports <comando>`.

**Los flags** van antes o después de las entradas (`report rec -o out` = `report -o out rec`) y se
escriben con uno o dos guiones (`-ai` = `--ai`). Un flag booleano se apaga con `=false` (`-ai=false`).

**Código de salida:** `0` si funcionó, `1` si hubo un error (y, en `verify` y `selftest`, si un check falla).

## El servidor

```bash
tracereports
```

Se configura con variables de entorno o con un archivo `.env` en la carpeta actual
(`TRACEREPORTS_ENV_FILE` elige otro; las variables ya definidas mandan): puerto, carpeta de datos,
token, IA, notificaciones, enmascarado… Ver [configuración](configuration.md). Las típicas:

```bash
PORT=8080 DATA_DIR=./data TRACEREPORTS_TOKEN=… GEMINI_API_KEY=… tracereports
```

## demo

```bash
tracereports demo [-port N] [-shop-port N] [-keep] [-no-open] [-ai=false]
```

El servidor con ejecuciones reales grabadas con todos los clientes y una tienda de prueba cuyo backend
puedes romper y arreglar, solo en este equipo y aislado de tu configuración (de tu entorno o tu `.env`
solo toma el proveedor de IA). Paso a paso: [inicio rápido](quickstart.md); detalles: [demo](demo.md).

| Flag | Por defecto | |
| --- | --- | --- |
| `-port` | el primero libre desde 8080 | Puerto del reporte |
| `-shop-port` | el primero libre desde 8180 | Puerto de la tienda y del playground |
| `-keep` | no | Conserva los datos de ejemplo al detenerla |
| `-no-open` | no | No abre el navegador |
| `-ai` | activo si hay una clave | `-ai=false`: sin diagnóstico con IA (gasta tokens de tu clave) |

## report

```bash
tracereports report [flags] ENTRADA...
```

Arma un reporte HTML estático, sin servidor: `index.html` se abre con doble clic, sin internet. Cada
ENTRADA es una carpeta de grabación (lo que guardó un cliente sin servidor), un archivo JUnit XML, una
carpeta de `*.xml` (una ejecución), o una carpeta `allure-results` o su ZIP.

| Flag | Por defecto | |
| --- | --- | --- |
| `-o` | `tracereports-report` | Carpeta de salida (una subcarpeta por ejecución si hay varias) |
| `-zip` | — | También escribe el reporte como ZIP (artefacto de CI, correo) |
| `-ai` | no | Diagnostica los fallos con la IA del entorno o del `.env` |
| `-name`, `-project`, `-branch`, `-environment` | — | Contexto de la ejecución para JUnit XML y Allure |

```bash
tracereports report output/tracereports/mi-ejecucion -o reporte
tracereports report resultados.xml -o reporte -zip reporte.zip
tracereports report allure-results -o reporte --ai
```

Más en [sin servidor](offline.md) y [Allure](allure.md).

## push

```bash
tracereports push [flags] ENTRADA...
```

Sube a un servidor, donde los fallos reciben el diagnóstico con IA. Cada ENTRADA es una carpeta de
grabación (subirla de nuevo no duplica nada), una carpeta `allure-results` o su ZIP, o JUnit XML (un
archivo o una carpeta de `*.xml`). Una importación de Allure o JUnit crea una ejecución nueva cada vez.
Muestra el link a cada ejecución.

| Flag | Por defecto | |
| --- | --- | --- |
| `-url` | `$TRACEREPORTS_URL` o `http://localhost:8080` | Servidor |
| `-token` | `$TRACEREPORTS_TOKEN` | Token de escritura (sirve el de ingesta) |
| `-name`, `-project`, `-branch`, `-environment` | — | Contexto de la ejecución para Allure y JUnit XML |
| `-force` | no | Sube una copia local completa (`TRACEREPORTS_OFFLINE=both`) como otra ejecución |

```bash
tracereports push output/tracereports/mi-ejecucion
tracereports push allure-results -project shop -name "Nightly #42" -branch main
tracereports push target/surefire-reports -project shop
```

## pr-comment

```bash
tracereports pr-comment [flags]
```

Comenta el resumen de la ejecución en el pull request (GitHub Actions) o merge request (GitLab CI) del
job actual: fallos, qué cambió respecto de la ejecución anterior, incidentes con su causa probable y el
link al reporte. El siguiente push actualiza el mismo comentario. GitHub: `GITHUB_TOKEN` con
`pull-requests: write`; GitLab: `GITLAB_TOKEN` o `TRACEREPORTS_GITLAB_TOKEN` (scope api). Fuera de un pull request imprime el
comentario (y lo agrega al resumen del job de GitHub).

| Flag | Por defecto | |
| --- | --- | --- |
| `-url`, `-token` | `$TRACEREPORTS_URL`, `$TRACEREPORTS_TOKEN` | Servidor |
| `-run` | la última ejecución de este commit | Id de la ejecución |
| `-from` | — | Sin servidor: una grabación, JUnit XML o allure-results a resumir |
| `-name` | — | Nombre de la ejecución para `-from` con JUnit XML o Allure |
| `-link` | el reporte del servidor | Link al reporte |
| `-lang` | el idioma del servidor, o `en` | `es` o `en` |
| `-wait` | `1m30s` | Cuánto esperar el diagnóstico de la ejecución |
| `-dry-run` | no | Imprime el comentario sin publicarlo |

Ver [CI · comentario en el pull request](ci.md#comentario-en-el-pull-request).

## repro

```bash
tracereports repro [-var nombre=valor]... [-base URL] <capsula.json | URL de la cápsula>
```

Muestra cómo correr el test fallido en local y reenvía cada llamada de una cápsula de
[FixLab](fixlab.md) desde tu máquina, con una sesión nueva. Antes de usar un secreto muestra a dónde
irá.

| Flag | |
| --- | --- |
| `-var nombre=valor` | Valor de una `{{variable}}` (se puede repetir); o `TRACEREPORTS_LAB_<NOMBRE>`; o escrito sin mostrarse |
| `-base URL` | Envía a otro entorno, por ejemplo `http://localhost:3000` (por defecto: el host de la ejecución) |

Para leer una cápsula de un servidor protegido: `TRACEREPORTS_TOKEN` o `TRACEREPORTS_UI_USER` /
`TRACEREPORTS_UI_PASSWORD`, que solo se envían al servidor de `TRACEREPORTS_URL`.

```bash
TRACEREPORTS_LAB_PASSWORD=… tracereports repro 'http://localhost:8080/api/v1/network/42/capsule'
```

## verify

```bash
tracereports verify [-var nombre=valor]... [-base URL] <capsula.json | URL de la cápsula>
```

Ejecuta los checks guardados en un incidente y sale con código **1** si alguno no pasa: un paso de CI
que confirma un arreglo. Mismos flags y credenciales que `repro`.

```bash
tracereports verify 'http://localhost:8080/api/v1/runs/7/lab/capsule?incident=<clave>' -base http://localhost:3000
```

## mcp

```bash
tracereports mcp
```

Servidor MCP (stdio) para Claude Code o cualquier cliente MCP: `list_runs`, `get_incidents`,
`get_test`, `reproduce` y `verify_fix`. Lee de `TRACEREPORTS_URL` (por defecto `http://localhost:8080`;
con `TRACEREPORTS_TOKEN` o `TRACEREPORTS_UI_USER`/`_PASSWORD` si hace falta) y reproduce o verifica
desde este equipo con los secretos de `TRACEREPORTS_LAB_<NOMBRE>`, que el agente nunca ve.

```bash
claude mcp add tracereports -e TRACEREPORTS_URL=http://localhost:8080 -e TRACEREPORTS_LAB_PASSWORD=… -- tracereports mcp
```

Ver [FixLab · MCP](fixlab.md#para-agentes-de-ia-mcp).

## selftest

```bash
tracereports selftest [-url URL] [-only id,id] [-list]
```

Reproduce los escenarios grabados con los clientes reales (pytest, Playwright JS, Selenium Java, Go,
REST, JUnit XML, Allure) y comprueba lo que muestra TraceReports; código de salida 1 si una expectativa
no se cumple.

| Flag | Por defecto | |
| --- | --- | --- |
| `-url` | un servidor temporal en este equipo | Un TraceReports desplegado (con `TRACEREPORTS_TOKEN` si está protegido) |
| `-only` | todos | Ids de escenarios, separados por comas |
| `-list` | — | Lista los escenarios y termina |

Ver [selftest](demo.md#tracereports-selftest).

## secrets

```bash
tracereports secrets status|migrate [-data-dir CARPETA] [-dry-run]
```

`status` dice cómo está guardada cada credencial de Ajustes (nunca su valor); `migrate` cifra con
`TRACEREPORTS_SECRET_KEY` las que versiones anteriores guardaron en claro, y vuelve a cifrar con la
clave nueva las cifradas con `TRACEREPORTS_SECRET_KEY_PREVIOUS`. `-data-dir` es por defecto `DATA_DIR` o
`./data`; `-dry-run` solo dice qué cambiaría. Respalda antes la carpeta de datos. Ver
[configuración · seguridad](configuration.md#seguridad).

## version

```bash
tracereports version        # también -version, --version
```

## Qué configuración lee cada comando

| Comando | Entorno | `.env` de la carpeta actual |
| --- | --- | --- |
| servidor (`tracereports`) | todo | todo (las variables ya definidas mandan) |
| `demo` | solo el proveedor de IA | solo el proveedor de IA |
| `report -ai` | el proveedor de IA | solo el proveedor de IA |
| `secrets` | `DATA_DIR`, `TRACEREPORTS_SECRET_KEY…` | todo |
| `push`, `pr-comment`, `repro`, `verify`, `mcp`, `selftest` | `TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`, `TRACEREPORTS_LAB_<NOMBRE>`… | no |

Los clientes de tests (pytest, Playwright, Java, Go) tienen sus propias variables: ver sus guías
([Python](python.md), [JavaScript](javascript.md), [Java](java.md), [Go](go.md)).
