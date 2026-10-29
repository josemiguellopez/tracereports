#!/usr/bin/env python3
"""Reporta los criterios de salida a 1.0 (docs/en/release-1.0.md): qué se cumple y qué falta.

Solo usa la biblioteca estándar. Lee el repositorio local (CHANGELOG, MAINTAINERS.md, un perfil de
cobertura de Go) y, si hay red, la API de GitHub (issues del epic) y la de OpenSSF Scorecard.

    go test ./... -coverprofile=cover.out
    python scripts/release_criteria.py --coverprofile cover.out            # tabla
    python scripts/release_criteria.py --coverprofile cover.out --markdown # para el resumen del job
    python scripts/release_criteria.py ... --strict                        # exit 1 si falta algo

GITHUB_TOKEN (opcional) evita el límite de la API sin autenticar.
"""
import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request

REPO = "josemiguellopez/tracereports"
EPIC = 34
MIN_COVERAGE = 75.0
MIN_SCORECARD = 7.0
MIN_MAINTAINERS = 2
# releases recientes sin cambios incompatibles para dar la API por congelada
FROZEN_RELEASES = 2


def coverage(path):
    """Cobertura total de sentencias de un perfil de `go test -coverprofile` (como `go tool cover -func`)."""
    blocks = {}
    with open(path, encoding="utf-8") as f:
        for line in f:
            if line.startswith("mode:") or not line.strip():
                continue
            pos, stmts, count = line.rsplit(" ", 2)
            stmts, count = int(stmts), int(count)
            # el mismo bloque puede aparecer varias veces (un paquete por proceso de test)
            prev = blocks.get(pos)
            blocks[pos] = (stmts, max(count, prev[1] if prev else 0))
    total = sum(s for s, _ in blocks.values())
    covered = sum(s for s, c in blocks.values() if c > 0)
    return 100.0 * covered / total if total else 0.0


def releases(changelog):
    """Secciones del CHANGELOG: [(versión, texto)], de la más nueva a la más vieja, sin [Unreleased]."""
    parts = re.split(r"^## \[", changelog, flags=re.M)[1:]
    out = []
    for p in parts:
        version = p.split("]", 1)[0]
        if version.lower() != "unreleased":
            out.append((version, p))
    return out


def maintainers(text):
    """Filas de la tabla de MAINTAINERS.md (una por persona)."""
    rows = [l for l in text.splitlines() if l.startswith("|") and "@" in l]
    return len(rows)


def get_json(url):
    req = urllib.request.Request(url, headers={"Accept": "application/json", "User-Agent": "tracereports-criteria"})
    token = os.environ.get("GITHUB_TOKEN")
    if token and url.startswith("https://api.github.com/"):
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.load(r)


def epic_issues(body):
    """Issues de la checklist del epic: {número: marcado}."""
    return {int(n): mark.lower() == "x" for mark, n in re.findall(r"^- \[( |x|X)\] #(\d+)", body, flags=re.M)}


def evaluate(root, coverprofile=None, online=True):
    """Lista de (criterio, estado, detalle); estado: "ok", "missing" o "unknown"."""
    out = []

    # 1. epic de hardening completo
    if online:
        try:
            epic = get_json(f"https://api.github.com/repos/{REPO}/issues/{EPIC}")
            items = epic_issues(epic.get("body") or "")
            states = {n: get_json(f"https://api.github.com/repos/{REPO}/issues/{n}")["state"] for n in items}
            pending = sorted(n for n, s in states.items() if s != "closed")
            out.append(("Security epic #%d closed" % EPIC, "ok" if not pending and epic["state"] == "closed" else "missing",
                        "all %d issues closed" % len(items) if not pending else "open: " + ", ".join("#%d" % n for n in pending)))
        except (urllib.error.URLError, KeyError, ValueError) as e:
            out.append(("Security epic #%d closed" % EPIC, "unknown", "GitHub API: %s" % e))
    else:
        out.append(("Security epic #%d closed" % EPIC, "unknown", "offline"))

    # 2. API congelada: sin cambios incompatibles en las últimas releases
    with open(os.path.join(root, "CHANGELOG.md"), encoding="utf-8") as f:
        rel = releases(f.read())
    recent = rel[:FROZEN_RELEASES]
    breaking = [v for v, text in recent if "Breaking" in text]
    if len(recent) < FROZEN_RELEASES:
        out.append(("API frozen", "missing", "fewer than %d releases" % FROZEN_RELEASES))
    else:
        out.append(("API frozen (no breaking change in the last %d releases)" % FROZEN_RELEASES,
                    "missing" if breaking else "ok", "breaking in " + ", ".join(breaking) if breaking else ", ".join(v for v, _ in recent)))

    # 3. sección de seguridad en cada release desde que existe la política
    no_security = [v for v, text in recent if not re.search(r"^### Security", text, flags=re.M)]
    out.append(("Security section in each release", "missing" if no_security else "ok",
                "missing in " + ", ".join(no_security) if no_security else "last %d releases" % len(recent)))

    # 4. cobertura de los tests de Go
    if coverprofile:
        pct = coverage(coverprofile)
        out.append(("Go test coverage >= %.0f%%" % MIN_COVERAGE, "ok" if pct >= MIN_COVERAGE else "missing", "%.1f%%" % pct))
    else:
        out.append(("Go test coverage >= %.0f%%" % MIN_COVERAGE, "unknown", "pass --coverprofile"))

    # 5. OpenSSF Scorecard
    if online:
        try:
            score = get_json(f"https://api.scorecard.dev/projects/github.com/{REPO}")["score"]
            out.append(("OpenSSF Scorecard >= %.1f" % MIN_SCORECARD, "ok" if score >= MIN_SCORECARD else "missing", "%.1f" % score))
        except (urllib.error.URLError, KeyError, ValueError) as e:
            out.append(("OpenSSF Scorecard >= %.1f" % MIN_SCORECARD, "unknown", "no published score yet (%s)" % e))
    else:
        out.append(("OpenSSF Scorecard >= %.1f" % MIN_SCORECARD, "unknown", "offline"))

    # 6. más de un mantenedor
    try:
        with open(os.path.join(root, "MAINTAINERS.md"), encoding="utf-8") as f:
            n = maintainers(f.read())
        out.append(("At least %d maintainers" % MIN_MAINTAINERS, "ok" if n >= MIN_MAINTAINERS else "missing", "%d listed" % n))
    except FileNotFoundError:
        out.append(("At least %d maintainers" % MIN_MAINTAINERS, "missing", "no MAINTAINERS.md"))
    return out


def render(results, markdown):
    icon = {"ok": "✅", "missing": "❌", "unknown": "❔"}
    lines = []
    if markdown:
        lines += ["## Criteria for 1.0", "", "| | Criterion | Detail |", "|---|---|---|"]
        lines += ["| %s | %s | %s |" % (icon[s], c, d) for c, s, d in results]
    else:
        width = max(len(c) for c, _, _ in results)
        lines += ["%-8s %-*s  %s" % (s.upper(), width, c, d) for c, s, d in results]
    met = sum(1 for _, s, _ in results if s == "ok")
    lines += ["", "%d of %d criteria met" % (met, len(results))]
    return "\n".join(lines)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--coverprofile", help="output of go test -coverprofile")
    ap.add_argument("--root", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
    ap.add_argument("--offline", action="store_true", help="do not call GitHub or Scorecard")
    ap.add_argument("--markdown", action="store_true", help="Markdown table (for $GITHUB_STEP_SUMMARY)")
    ap.add_argument("--strict", action="store_true", help="exit 1 unless every criterion is met")
    args = ap.parse_args(argv)
    results = evaluate(args.root, args.coverprofile, online=not args.offline)
    print(render(results, args.markdown))
    if args.strict and any(s != "ok" for _, s, _ in results):
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
