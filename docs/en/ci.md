# Continuous integration

🌐 **English** · [Español](../es/ci.md)

The pattern is always the same:

1. A TraceReports server reachable from CI, with `TRACEREPORTS_TOKEN` configured.
2. The pipeline runs the tests with `TRACEREPORTS_URL` and `TRACEREPORTS_TOKEN` as secrets.
3. When it finishes, Teams or Slack get the summary, and optionally the ZIP is kept as an artifact.

Use a **stable run name** (for example `E2E main` or `Nightly regression`). That way history,
flaky detection and the comparison with the previous run group correctly.

## GitHub Actions

Full example: [`docs/ci/github-actions.yml`](../ci/github-actions.yml). The essentials:

```yaml
- name: E2E tests
  env:
    TRACEREPORTS_URL: ${{ secrets.TRACEREPORTS_URL }}
    TRACEREPORTS_TOKEN: ${{ secrets.TRACEREPORTS_TOKEN }}
  run: pytest tests/e2e --tracereports --tracereports-run "E2E ${{ github.ref_name }}" --tracereports-zip report

- uses: actions/upload-artifact@v4
  if: always()
  with: { name: tracereports, path: report/*.zip }
```

## GitLab CI

```yaml
e2e:
  image: mcr.microsoft.com/playwright/python:v1.55.0-noble
  variables:
    TRACEREPORTS_URL: $TRACEREPORTS_URL        # protected project variables
    TRACEREPORTS_TOKEN: $TRACEREPORTS_TOKEN
  script:
    - pip install pytest pytest-playwright ./client/python   # or from git
    - pytest tests/e2e --tracereports --tracereports-run "E2E $CI_COMMIT_REF_NAME" --tracereports-zip report
  artifacts:
    when: always
    paths: [report/]
```

## Jenkins

```groovy
withCredentials([string(credentialsId: 'tracereports-token', variable: 'TRACEREPORTS_TOKEN')]) {
  sh '''
    export TRACEREPORTS_URL=https://tracereports.mycompany.com
    pytest tests/e2e --tracereports --tracereports-run "E2E ${BRANCH_NAME}" --tracereports-zip report
  '''
}
archiveArtifacts artifacts: 'report/*.zip', allowEmptyArchive: true
```

## Go

```bash
TRACEREPORTS_URL=... TRACEREPORTS_TOKEN=... go test ./e2e/...
```

The [`examples/playwright-go`](../../examples/playwright-go) example prints the report link at the
end. For the ZIP: `curl -H "Authorization: Bearer $TRACEREPORTS_TOKEN" -o report.zip $TRACEREPORTS_URL/api/v1/runs/<id>/export`.

## Any framework: importing JUnit XML

If your runner has no client (TestNG, Cucumber, Cypress, .NET, Jest…) or you want to try TraceReports
without touching the tests, upload the JUnit XML your pipeline already writes. At the end of the
job, even when tests failed:

```yaml
- name: Upload results to TraceReports
  if: always()
  run: |
    curl -fsS -H "Authorization: Bearer $TRACEREPORTS_TOKEN" -H "Content-Type: application/xml" \
      --data-binary @report.xml \
      "$TRACEREPORTS_URL/api/v1/import/junit?name=E2E%20main&project=$GITHUB_REPOSITORY&branch=$GITHUB_REF_NAME&commit=$GITHUB_SHA"
```

With several files (Maven, Gradle), one `-F file=@…` per report. You get history, flaky detection,
comparison, error grouping, AI diagnosis and the Teams/Slack notice; screenshots, network and DOM
come from the clients. Details in the [API](api.md#importing-a-junit-xml-report).

## Starting the server inside the pipeline

If you don't have a permanent server, you can start it as a job service. Reports are lost when the
job ends, except the ZIP:

```yaml
services:
  tracereports:
    image: <your-registry>/tracereports:latest
    ports: ["8080:8080"]
```

and use `TRACEREPORTS_URL=http://localhost:8080` with `--tracereports-zip`. If the tests reach it through
the service alias (`http://tracereports:8080`, typical in GitLab) and the server has neither token nor
login, add `TRACEREPORTS_ALLOWED_HOSTS=tracereports` to the service.
