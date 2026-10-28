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

## Verifying a release

Every release since this policy was added is **signed without keys** with
[Sigstore cosign](https://docs.sigstore.dev/) (the identity is the `release.yml` workflow of this
repository, recorded in the public transparency log), ships an **SBOM** (SPDX) and a **SLSA
provenance** attestation. You need [cosign](https://docs.sigstore.dev/cosign/system_config/installation/)
2.x or later; `gh` (GitHub CLI) for the provenance.

```bash
VERSION=0.6.0   # the version you downloaded, without the "v"
IDENTITY='^https://github\.com/josemiguellopez/tracereports/\.github/workflows/release\.yml@refs/tags/v'
ISSUER=https://token.actions.githubusercontent.com
```

**Binaries.** Download `checksums.txt` and `checksums.txt.sigstore.json` from the release, next to
your archive:

```bash
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp "$IDENTITY" --certificate-oidc-issuer "$ISSUER"
sha256sum --check --ignore-missing checksums.txt     # macOS: shasum -a 256 --check --ignore-missing
gh attestation verify tracereports_${VERSION}_linux_amd64.tar.gz --repo josemiguellopez/tracereports
```

The SBOM of each archive is the `.spdx.json` file with the same name
(`tracereports_${VERSION}_linux_amd64.tar.gz.spdx.json`), also covered by `checksums.txt`.

**Docker image.** Verify the image you are going to run, by digest:

```bash
cosign verify ghcr.io/josemiguellopez/tracereports:${VERSION} \
  --certificate-identity-regexp "$IDENTITY" --certificate-oidc-issuer "$ISSUER"
gh attestation verify oci://ghcr.io/josemiguellopez/tracereports:${VERSION} --repo josemiguellopez/tracereports
docker buildx imagetools inspect ghcr.io/josemiguellopez/tracereports:${VERSION} --format '{{ json .SBOM }}'
```

Pre-releases (`v0.6.0-rc1`) are signed the same way and never move the `latest` tag.

## How the project checks its own code

- **govulncheck** on every pull request: it fails when the server or the Go client calls a function with
  a known vulnerability (dependencies and the Go standard library). Binaries and the image are built with
  the latest patch release of Go.
- **CodeQL** (Go, JavaScript/TypeScript, Python, Java and the workflows) on every pull request and weekly.
- **OpenSSF Scorecard** on the default branch, with its score in the README.
- **Dependabot** updates for Go, npm, Gradle and GitHub Actions every week.
- Tests with the race detector, and a release rehearsal (`release-check.yml`) that builds, signs and
  verifies the binaries, the SBOM and the image on every pull request that changes the release.
