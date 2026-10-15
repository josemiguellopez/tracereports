# Allure: tus resultados de Allure, diagnosticados

🌐 [English](../en/allure.md) · **Español**

¿Ya usas Allure? Sigue usándolo. Envía la carpeta `allure-results` que ya escriben tus tests y
TraceReports hace lo que un reporte de Allure te deja a ti:

- **Cada fallo diagnosticado con IA** (categoría, resumen y qué revisar primero), y la ejecución
  completa agrupada en incidentes. Se acabó abrir el reporte de Allure test por test.
- Historia por test, **inestable vs. roto**, comparación entre ejecuciones, métricas, la vista
  Release, escalar a Teams/Slack y tickets.
- Los pasos (anidados), capturas, adjuntos de texto y JSON, reintentos y etiquetas que ya tienes.
- El comando para correr cada test fallido en local (pytest, Playwright, Maven/Gradle).

Sin cambiar tus tests y sin instalar un cliente de TraceReports.

> **Míralo primero:** `tracereports demo` carga la ejecución *Shop · Allure (allure-pytest)*, los
> allure-results de una suite de pytest + Playwright que solo usa Allure. Ver el
> [inicio rápido](quickstart.md).

## 1. Tus tests escriben allure-results

Lo que probablemente ya tienes:

| Framework | Carpeta |
| --- | --- |
| pytest + allure-pytest | `pytest --alluredir=allure-results` |
| Playwright Test + allure-playwright | `allure-results` (o el `resultsDir` del reporter) |
| JUnit 5 + allure-junit5 (Gradle) | `build/allure-results` |
| JUnit 5 / TestNG + Allure (Maven) | `target/allure-results` |
| Cucumber, Jest, Vitest, Cypress, behave… con su adaptador de Allure | la carpeta del adaptador |

## 2. Envíala a TraceReports

```bash
tracereports push allure-results -project shop -name "Nightly #42" -branch main
```

- Muestra el link a la ejecución. El diagnóstico con IA llega unos segundos después, en vivo.
- `-url` (por defecto `$TRACEREPORTS_URL` o `http://localhost:8080`) y `-token` (por defecto
  `$TRACEREPORTS_TOKEN`) eligen el servidor. También sirve un ZIP de la carpeta.
- `-name` es por defecto el `buildName` de `executor.json`, o `Allure`. También `-environment`.
- Cada push crea una ejecución nueva: haz uno por cada corrida de tests.

En CI, después de los tests y aunque fallen:

```yaml
# GitHub Actions
- name: Enviar los resultados de Allure a TraceReports
  if: always()
  run: tracereports push allure-results -project shop -name "CI #${{ github.run_number }}" -branch "${{ github.ref_name }}"
  env:
    TRACEREPORTS_URL: ${{ vars.TRACEREPORTS_URL }}
    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
```

### Sin servidor

```bash
tracereports report allure-results -o reporte --ai
```

Arma un reporte HTML estático (abre `reporte/index.html`, sin internet) con el diagnóstico de IA,
usando el proveedor de tu entorno (`AI_PROVIDER` + `AI_API_KEY`, `GEMINI_API_KEY`…). Sin `--ai`, el
mismo reporte sin el diagnóstico. Sirve como artefacto de CI.

### Con la API

```bash
cd allure-results && zip -qr ../allure.zip . && cd ..
curl -H "Authorization: Bearer $TRACEREPORTS_TOKEN" -H "Content-Type: application/zip" \
  --data-binary @allure.zip "$TRACEREPORTS_URL/api/v1/import/allure?project=shop&branch=main"
```

Ver [API · Importar resultados de Allure](api.md#importar-resultados-de-allure).

## Qué toma de Allure

| Allure | TraceReports |
| --- | --- |
| Cada `*-result.json` | Un test, identificado por `fullName` y sus parámetros |
| `passed` / `failed`, `broken` / `skipped`, `unknown` | Pass / Fail / Skip |
| Resultados con el mismo `historyId` (reintentos) | Un test con sus intentos: inestable si un reintento pasó |
| Pasos (anidados) con su estado y hora | Pasos del test |
| Adjuntos de imagen | Capturas, en los pasos y el replay |
| Adjuntos de texto, JSON, CSV, HTML, URI | Un paso con su contenido (hasta 16 KB) |
| Etiquetas `tag`, `feature`, `story`, `epic` | Categorías |
| `parentSuite` / `suite` / `subSuite` | La suite |
| `buildName` de `executor.json`, la etiqueta `framework` | Nombre de la ejecución y framework |

Probado con los resultados de [allure-examples](https://github.com/allure-examples): allure-pytest,
allure-playwright y allure-junit5.

## Límites

- Allure no graba la red del navegador, así que estas ejecuciones no tienen pestaña **Red**,
  incidente de backend ni [Lab](fixlab.md), y la IA trabaja con el error, los pasos y los adjuntos.
  Para esa evidencia, agrega el cliente de TraceReports de tu framework: puede convivir con Allure.
- Las sugerencias de selectores rotos necesitan el DOM de la página, que Allure no guarda.
- Límites por importación: 200 MB por ZIP y 15 MB por adjunto.
