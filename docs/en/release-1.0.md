# Criteria for 1.0

[Español](../es/release-1.0.md)

TraceReports is pre-1.0. Version 1.0 means: the stable part of the API is frozen
([API stability policy](api-stability.md)), the security baseline is complete, and the project does not
depend on a single person. Every criterion is measurable; `scripts/release_criteria.py` reports them, and
the **Criteria for 1.0** workflow publishes the report every week (Actions → *Criteria for 1.0*).

| Criterion | How it is measured |
| --- | --- |
| Security hardening epic complete | Every issue of the checklist of #34 closed, and #34 closed |
| API frozen | No **⚠️ Breaking** entry in the last two releases of the CHANGELOG |
| Security section in each release | The last two releases of the CHANGELOG have a `### Security` section |
| Go test coverage ≥ 75 % | Total statement coverage of `go test ./... -coverprofile` (78 % when this was written) |
| OpenSSF Scorecard ≥ 7.0 | The score published for the repository (`api.scorecard.dev`; it appears after the Scorecard workflow runs on `main`) |
| At least two maintainers | Rows of [MAINTAINERS.md](../../MAINTAINERS.md) |

Run it locally:

```bash
go test ./... -coverprofile=cover.out
python scripts/release_criteria.py --coverprofile cover.out          # --strict: exit 1 if one is missing
```

The [threat model](threat-model.md) is reviewed before 1.0 by someone other than its author.
