# Security policy

TraceReports stores evidence from real test runs (screenshots, network calls, DOM), so security
reports are taken seriously.

## Supported versions

TraceReports is pre-1.0: only the **latest release** receives security fixes.

## Reporting a vulnerability

**Please don't open a public issue.** Report it privately in one of these ways:

1. **GitHub:** go to the [Security tab](https://github.com/josemiguellopez/tracereports/security)
   and click **Report a vulnerability**.
2. **Email:** [josemiguellopez@outlook.cl](mailto:josemiguellopez@outlook.cl) with the subject
   `[TraceReports security]`.

Include what you can: the affected version, steps to reproduce, the impact and, if you have one, a
proof of concept. You will get an answer within **7 days**. Once a fix is released, you will be
credited in the release notes unless you prefer to stay anonymous.

## Scope

Especially relevant:

- Secrets (tokens, passwords, cookies) stored or shown without masking.
- Access to reports, settings or the API without the configured token or UI login.
- Evidence or AI prompts leaking to places the user did not configure.
