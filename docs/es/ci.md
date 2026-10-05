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
