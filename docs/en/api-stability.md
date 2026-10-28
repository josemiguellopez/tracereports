# API stability policy

[Español](../es/api-stability.md)

This policy says what you can rely on when you integrate with TraceReports: the REST API (`/api/v1`), the
CLI and the test clients. It applies from this version on; [1.0](release-1.0.md) is when its stable part
is frozen.

## Levels

| Level | What | Promise |
| --- | --- | --- |
| **Stable** | The endpoints documented in [API → Writes](api.md#writes-used-by-the-test-clients) and [Reads](api.md#reads-used-by-the-ui), the imports (JUnit XML, Allure), `GET /healthz`, the CLI commands `push`, `report`, `pr-comment`, `health`, `version`, and the public functions of the four clients | No breaking change inside `/api/v1` once 1.0 is out; before it, only with the process below |
| **Experimental** | Endpoints marked *experimental* in the docs: [actions from the interface](api.md#actions-from-the-interface-ai-and-escalation), FixLab (`/network/{id}/lab`, capsules), similar incidents, the security check, Settings, the MCP tools | May change in any minor release, always written in the CHANGELOG |
| **Internal** | Anything not documented (what the bundled UI calls for itself) | No promise |

## What counts as breaking

Breaking: removing or renaming an endpoint, a field or a CLI flag; changing a field's type or meaning;
making an optional parameter required; changing a status code a client depends on; changing the default
of a setting so that an existing deployment behaves differently.

Not breaking (may come in any minor release): new endpoints, new optional parameters, **new fields in
responses** (clients must ignore fields they do not know), new values in a field documented as open
(for example the AI provider), better error messages.

## Before 1.0 (now)

TraceReports is pre-1.0: a minor release (`0.x`) **may** break, but only when:

1. the CHANGELOG has a **⚠️ Breaking** entry for it with a migration guide;
2. when possible, the old behavior stays for one more minor release, or an explicit switch restores it
   (as `TRACEREPORTS_INSECURE=1` did for the network exposure change);
3. security fixes are the exception: they may break immediately if keeping the old behavior keeps the
   hole open. The CHANGELOG says so.

## From 1.0

- [Semantic Versioning](https://semver.org/): breaking changes to the stable level only in a new major
  version, which brings a new API path (`/api/v2`) served **in parallel** with `/api/v1` for at least
  **6 months**.
- **Deprecation**: a stable endpoint, field or flag is announced as deprecated at least **two minor
  releases and 6 months** before removal. While deprecated, its responses carry the `Deprecation` header
  and, once the date is set, `Sunset` ([RFC 8594](https://www.rfc-editor.org/rfc/rfc8594)), and the
  CHANGELOG lists it under *Deprecated*.
- **Clients and server**: a client works with any server of the same major version. A client may need a
  newer server only for new features, and degrades gracefully against an older one.
- **Data**: the database is migrated forward automatically on start; going back to an older version
  is not supported (back up `DATA_DIR` before upgrading).

## Security sections

Every release in the CHANGELOG has a **Security** section, even when it only says "No security changes".
It lists fixed vulnerabilities (with the advisory when there is one), new security controls and changes
in defaults that affect exposure.
