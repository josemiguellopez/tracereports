# Governance

TraceReports is an open source project led by its maintainers ([MAINTAINERS.md](MAINTAINERS.md)).

## Decisions

- **Day to day**: a pull request is merged when a maintainer approves it and CI is green. A maintainer may
  merge their own pull request after CI passes while the project has a single maintainer; with two or more,
  another maintainer reviews it.
- **Larger changes** (a new surface, a breaking change, a dependency with native code, a change to the
  threat model): an issue or an RFC in `docs/rfc/` first, open for comments at least one week.
- **Security**: reports follow [SECURITY.md](SECURITY.md); fixes may be prepared in private and merged
  without the waiting period.
- **Disagreements**: discussed in the issue; if maintainers still disagree, the lead maintainer decides and
  writes down why.

## Becoming a maintainer

Anyone with a record of good contributions (code, reviews, docs, triage) for about three months can be
proposed by a maintainer or propose themselves in an issue. Current maintainers decide by consensus.
Maintainers who are inactive for a year move to an emeritus list and can come back by asking.

## Releases

Maintainers cut releases following [CONTRIBUTING.md](CONTRIBUTING.md). Every release is signed by the
release workflow (no personal keys) and its CHANGELOG entry has a **Security** section.

## Continuity

If the only maintainer stops responding for 90 days, the community may fork; the license (see
[LICENSE](LICENSE)) allows it. Before 1.0 the project aims to have at least two maintainers so this does
not depend on one person.
