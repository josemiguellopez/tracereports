"""Pruebas de scripts/release_criteria.py (python -m unittest discover scripts)."""
import os
import tempfile
import unittest

import release_criteria as rc

CHANGELOG = """# Changelog

## [Unreleased]

### Security
- algo

## [0.9.0] - 2026-11-01

### Security
- No security changes.

## [0.8.0] - 2026-10-01

### ⚠️ Breaking: algo cambió

### Security
- arreglo

## [0.7.0] - 2026-09-01

### Added
- algo
"""


class Criteria(unittest.TestCase):
    def repo(self, changelog=CHANGELOG, maintainers=None):
        d = tempfile.mkdtemp()
        with open(os.path.join(d, "CHANGELOG.md"), "w", encoding="utf-8") as f:
            f.write(changelog)
        if maintainers is not None:
            with open(os.path.join(d, "MAINTAINERS.md"), "w", encoding="utf-8") as f:
                f.write(maintainers)
        return d

    def by_name(self, results, prefix):
        return next(r for r in results if r[0].startswith(prefix))

    def test_coverage_counts_each_block_once(self):
        with tempfile.NamedTemporaryFile("w", delete=False, suffix=".out", encoding="utf-8") as f:
            f.write("mode: set\n")
            f.write("a.go:1.1,2.2 3 1\n")
            f.write("a.go:3.1,4.2 1 0\n")
            f.write("a.go:3.1,4.2 1 1\n")  # el mismo bloque desde otro paquete, ahora cubierto
            f.write("b.go:1.1,2.2 4 0\n")
        self.assertAlmostEqual(rc.coverage(f.name), 50.0)

    def test_breaking_in_recent_releases_is_not_frozen(self):
        r = rc.evaluate(self.repo(), online=False)
        status = self.by_name(r, "API frozen")
        self.assertEqual(status[1], "missing")
        self.assertIn("0.8.0", status[2])

    def test_frozen_and_security_sections(self):
        clean = CHANGELOG.replace("### ⚠️ Breaking: algo cambió\n", "")
        r = rc.evaluate(self.repo(clean), online=False)
        self.assertEqual(self.by_name(r, "API frozen")[1], "ok")
        self.assertEqual(self.by_name(r, "Security section")[1], "ok")

    def test_release_without_security_section(self):
        old = CHANGELOG.replace("## [0.9.0] - 2026-11-01\n\n### Security\n- No security changes.\n", "## [0.9.0] - 2026-11-01\n\n### Added\n- x\n")
        r = rc.evaluate(self.repo(old), online=False)
        self.assertEqual(self.by_name(r, "Security section"), ("Security section in each release", "missing", "missing in 0.9.0"))

    def test_maintainers(self):
        one = "| Name | GitHub |\n|---|---|\n| A | @a |\n"
        self.assertEqual(self.by_name(rc.evaluate(self.repo(maintainers=one), online=False), "At least")[1], "missing")
        two = one + "| B | @b |\n"
        self.assertEqual(self.by_name(rc.evaluate(self.repo(maintainers=two), online=False), "At least")[1], "ok")

    def test_offline_is_unknown_and_strict_fails(self):
        d = self.repo()
        r = rc.evaluate(d, online=False)
        self.assertEqual(self.by_name(r, "Security epic")[1], "unknown")
        self.assertEqual(self.by_name(r, "OpenSSF")[1], "unknown")
        self.assertEqual(rc.main(["--root", d, "--offline", "--strict"]), 1)
        self.assertEqual(rc.main(["--root", d, "--offline"]), 0)

    def test_epic_checklist(self):
        body = "- [x] #43\n- [ ] #46\n- [X] #36\ntexto - [ ] #99 no\n"
        self.assertEqual(rc.epic_issues(body), {43: True, 46: False, 36: True})


if __name__ == "__main__":
    unittest.main()
