"""
Contexto de la ejecución: rama y commit. El servidor solo compara una ejecución con otras del
mismo proyecto, ambiente y rama, así que staging no se mezcla con producción ni una rama de
feature con main.

Se lee de las variables de los CI más comunes y, si no hay, de git (si está disponible).
"""

from __future__ import annotations

import os
import subprocess
from typing import Optional

_BRANCH_VARS = ("TRACEREPORTS_BRANCH", "GITHUB_HEAD_REF", "GITHUB_REF_NAME", "CI_COMMIT_REF_NAME", "BITBUCKET_BRANCH",
                "BUILD_SOURCEBRANCHNAME", "BRANCH_NAME", "CIRCLE_BRANCH", "GIT_BRANCH")
_COMMIT_VARS = ("TRACEREPORTS_COMMIT", "GITHUB_SHA", "CI_COMMIT_SHA", "BITBUCKET_COMMIT", "BUILD_SOURCEVERSION",
                "CIRCLE_SHA1", "GIT_COMMIT")


def _git(*args: str, cwd: Optional[str] = None) -> str:
    try:
        out = subprocess.run(["git", *args], cwd=cwd, capture_output=True, text=True, timeout=2)
        return out.stdout.strip() if out.returncode == 0 else ""
    except (OSError, subprocess.SubprocessError):
        return ""


def detect_branch(cwd: Optional[str] = None) -> str:
    for var in _BRANCH_VARS:
        value = os.getenv(var, "").strip()
        if value:
            return value[len("origin/"):] if value.startswith("origin/") else value
    branch = _git("rev-parse", "--abbrev-ref", "HEAD", cwd=cwd)
    return "" if branch == "HEAD" else branch


def detect_commit(cwd: Optional[str] = None) -> str:
    for var in _COMMIT_VARS:
        value = os.getenv(var, "").strip()
        if value:
            return value
    return _git("rev-parse", "HEAD", cwd=cwd)
