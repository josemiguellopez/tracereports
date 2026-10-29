# Criterios para 1.0

[English](../en/release-1.0.md)

TraceReports es pre-1.0. La versión 1.0 significa: la parte estable de la API está congelada
([política de estabilidad](api-stability.md)), la base de seguridad está completa y el proyecto no depende
de una sola persona. Cada criterio es medible; `scripts/release_criteria.py` los reporta y el workflow
**Criteria for 1.0** publica el reporte cada semana (Actions → *Criteria for 1.0*).

| Criterio | Cómo se mide |
| --- | --- |
| Epic de hardening de seguridad completo | Todos los issues de la checklist de #34 cerrados, y #34 cerrado |
| API congelada | Ninguna entrada **⚠️ Breaking** en las dos últimas versiones del CHANGELOG |
| Sección de seguridad en cada versión | Las dos últimas versiones del CHANGELOG tienen sección `### Security` |
| Cobertura de los tests de Go ≥ 75 % | Cobertura total de sentencias de `go test ./... -coverprofile` (78 % al escribir esto) |
| OpenSSF Scorecard ≥ 7.0 | El puntaje publicado del repositorio (`api.scorecard.dev`; aparece cuando el workflow de Scorecard corre en `main`) |
| Al menos dos mantenedores | Filas de [MAINTAINERS.md](../../MAINTAINERS.md) |

En local:

```bash
go test ./... -coverprofile=cover.out
python scripts/release_criteria.py --coverprofile cover.out          # --strict: exit 1 si falta alguno
```

Antes de 1.0, alguien distinto de su autor revisa el [modelo de amenazas](threat-model.md).
