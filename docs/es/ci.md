# Integración continua

🌐 [English](../en/ci.md) · **Español**

El patrón es siempre el mismo:

1. Un servidor TraceReports accesible desde el CI, con `TRACEREPORTS_TOKEN` configurado.
2. El pipeline corre los tests con `TRACEREPORTS_URL` y `TRACEREPORTS_TOKEN` como secretos.
3. Al terminar, Teams o Slack reciben el resumen, y opcionalmente el ZIP queda como artefacto.

Usa un **nombre de ejecución estable** (por ejemplo `E2E main` o `Regresión nocturna`). Así el
historial, la detección de flaky y la comparación con la ejecución anterior agrupan bien.

## GitHub Actions

Ejemplo completo: [`docs/ci/github-actions.yml`](../ci/github-actions.yml). Lo esencial:

```yaml
- name: Tests E2E
  env:
    TRACEREPORTS_URL: ${{ secrets.TRACEREPORTS_URL }}
    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
  run: pytest tests/e2e --tracereports --tracereports-run "E2E ${{ github.ref_name }}" --tracereports-zip reporte

- uses: actions/upload-artifact@v4
  if: always()
  with: { name: tracereports, path: reporte/*.zip }
```

## GitLab CI

```yaml
e2e:
  image: mcr.microsoft.com/playwright/python:v1.55.0-noble
  variables:
    TRACEREPORTS_URL: $TRACEREPORTS_URL        # variables protegidas del proyecto
    TRACEREPORTS_TOKEN: $TRACEREPORTS_TOKEN
  script:
    - pip install pytest pytest-playwright ./client/python   # o desde git
    - pytest tests/e2e --tracereports --tracereports-run "E2E $CI_COMMIT_REF_NAME" --tracereports-zip reporte
  artifacts:
    when: always
    paths: [reporte/]
```

## Jenkins

```groovy
withCredentials([string(credentialsId: 'tracereports-token', variable: 'TRACEREPORTS_TOKEN')]) {
  sh '''
    export TRACEREPORTS_URL=https://tracereports.miempresa.com
    pytest tests/e2e --tracereports --tracereports-run "E2E ${BRANCH_NAME}" --tracereports-zip reporte
  '''
}
archiveArtifacts artifacts: 'reporte/*.zip', allowEmptyArchive: true
```

## Go

```bash
TRACEREPORTS_URL=... TRACEREPORTS_TOKEN=... go test ./e2e/...
```

El ejemplo [`examples/playwright-go`](../../examples/playwright-go) imprime el link al reporte al
final. Para el ZIP: `curl -H "Authorization: Bearer $TRACEREPORTS_TOKEN" -o reporte.zip $TRACEREPORTS_URL/api/v1/runs/<id>/export`.

## Cualquier framework: importar JUnit XML

Si tu runner no tiene cliente (TestNG, Cucumber, Cypress, .NET, Jest…) o quieres probar TraceReports
sin tocar los tests, sube el JUnit XML que ya genera tu pipeline. Al final del job, aunque los
tests hayan fallado:

```yaml
- name: Subir resultados a TraceReports
  if: always()
  run: |
    curl -fsS -H "Authorization: Bearer $TRACEREPORTS_TOKEN" -H "Content-Type: application/xml" \
      --data-binary @report.xml \
      "$TRACEREPORTS_URL/api/v1/import/junit?name=E2E%20main&project=$GITHUB_REPOSITORY&branch=$GITHUB_REF_NAME&commit=$GITHUB_SHA"
```

Con varios archivos (Maven, Gradle), un `-F file=@…` por reporte. Tienes historial, flaky,
comparación, agrupación de errores, diagnóstico con IA y aviso a Teams/Slack; las capturas, la red
y el DOM los agregan los clientes. Detalle en la [API](api.md#importar-un-reporte-junit-xml).

¿Ya usas Allure? Sube la carpeta `allure-results` comprimida a `/api/v1/import/allure` y conservas
pasos y capturas (ver la [API](api.md#importar-resultados-de-allure)); o, sin servidor,
`tracereports report allure-results -o reporte`.

## Comentario en el pull request

`tracereports pr-comment` comenta en el PR (GitHub) o MR (GitLab) el resumen de la ejecución del
commit: fallos, qué cambió frente a la ejecución anterior (fallos nuevos y arreglados), flaky, los
incidentes con su causa probable y el link al reporte. En cada push **actualiza el mismo
comentario** (uno por suite) en vez de sumar otro, y nunca menciona a nadie.

```yaml
# GitHub Actions
permissions:
  pull-requests: write
steps:
  - run: pytest --tracereports
  - name: Comentario de TraceReports
    if: always() && github.event_name == 'pull_request'
    env:
      TRACEREPORTS_URL: ${{ secrets.TRACEREPORTS_URL }}
      TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
      GITHUB_TOKEN: ${{ github.token }}
    run: docker run --rm -e TRACEREPORTS_URL -e TRACEREPORTS_TOKEN -e GITHUB_TOKEN -e GITHUB_ACTIONS -e GITHUB_REF
      -e GITHUB_REPOSITORY -e GITHUB_SHA -e GITHUB_API_URL -e GITHUB_EVENT_PATH -v "$GITHUB_EVENT_PATH:$GITHUB_EVENT_PATH:ro"
      ghcr.io/josemiguellopez/tracereports pr-comment
```

```yaml
# GitLab CI (GITLAB_TOKEN: token de proyecto con scope api, como variable protegida)
tracereports-comment:
  image: alpine:3
  rules: [{ if: $CI_MERGE_REQUEST_IID }]
  script:
    - apk add --no-cache curl
    - V=$(curl -s https://api.github.com/repos/josemiguellopez/tracereports/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
    - curl -sL "https://github.com/josemiguellopez/tracereports/releases/download/v$V/tracereports_${V}_linux_amd64.tar.gz" | tar xz tracereports
    - ./tracereports pr-comment
```

- Busca la ejecución del commit del job (`GITHUB_SHA`, el head del PR o `CI_COMMIT_SHA`), o usa `--run <id>`.
- Espera hasta 90 s (`--wait`) a que el diagnóstico de la ejecución esté listo.
- Sin servidor: `tracereports pr-comment --from <grabación | JUnit XML | allure-results>` arma el
  resumen igual (sin comparación ni link, salvo `--link`).
- Fuera de un PR imprime el comentario; en GitHub Actions lo agrega también al resumen del job.
  `--dry-run` solo lo imprime. Idioma: `--lang es|en` (default: el de Ajustes).

## Levantar el servidor dentro del pipeline

Si no tienes un servidor permanente, puedes levantarlo como servicio del job. Los reportes se
pierden al terminar, salvo el ZIP:

```yaml
services:
  tracereports:
    image: <tu-registro>/tracereports:latest
    ports: ["8080:8080"]
```

y usar `TRACEREPORTS_URL=http://localhost:8080` con `--tracereports-zip`. Si los tests llegan por el
alias del servicio (`http://tracereports:8080`, típico en GitLab) y el servidor no tiene token ni login,
añade `TRACEREPORTS_ALLOWED_HOSTS=tracereports` al servicio.
