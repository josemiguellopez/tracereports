"""Pinned report renderer, shared user cache and verified release downloads."""
import hashlib
import io
import os
from pathlib import Path
import platform
import re
import shutil
import tarfile
import tempfile
import time
import urllib.request
import zipfile

from ._env import env

REPORT_VERSION = "0.2.0"
RELEASES = "https://github.com/josemiguellopez/tracereports/releases/download"
MAX_ARCHIVE = 128 * 1024 * 1024
MAX_BINARY = 256 * 1024 * 1024
# A download holds the cache lock for at most its timeout; an older lock was left by a process that
# died mid-download (Ctrl+C, crash) and would otherwise block every later run.
STALE_LOCK_SECONDS = 300


def resolve_binary(timeout=60):
    explicit = env("BIN")
    if explicit:
        return explicit
    installed = shutil.which("tracereports")
    if installed:
        return installed
    system = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}.get(platform.system())
    arch = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
    if not system or not arch:
        raise OSError("unsupported report platform")
    base = os.environ.get("LOCALAPPDATA") if system == "windows" else (None if system == "darwin" else os.environ.get("XDG_CACHE_HOME"))
    base = Path(base) if base else (Path.home() / "Library" / "Caches" if system == "darwin" else Path.home() / ".cache")
    parent = base / "tracereports" / REPORT_VERSION
    target = parent / f"{system}_{arch}"
    name = "tracereports.exe" if system == "windows" else "tracereports"
    binary = target / name

    def cached():
        try:
            return (binary.is_file() and not binary.is_symlink()
                    and hashlib.sha256(binary.read_bytes()).hexdigest() == (target / "binary.sha256").read_text().strip())
        except OSError:
            return False

    if cached():
        return str(binary)
    if env("BIN_DOWNLOAD", "1") == "0":
        raise OSError("binary download disabled")
    deadline = time.monotonic() + timeout
    parent.mkdir(parents=True, exist_ok=True)
    lock = parent / f"{system}_{arch}.lock"
    while True:
        if cached():
            return str(binary)
        if time.monotonic() >= deadline:
            raise TimeoutError("waiting for report binary cache")
        try:
            lock.mkdir()
            break
        except FileExistsError:
            try:
                if time.time() - lock.stat().st_mtime > STALE_LOCK_SECONDS:
                    lock.rmdir()
                    continue
            except OSError:
                pass  # another process removed or took it meanwhile
            time.sleep(.05)
    try:
        if cached():
            return str(binary)
        base_url = env("BIN_BASE_URL") or RELEASES
        # The override is for local test servers only; production always uses GitHub HTTPS.
        if base_url != RELEASES:
            from urllib.parse import urlsplit
            url = urlsplit(base_url)
            if url.scheme != "http" or url.hostname not in ("127.0.0.1", "localhost", "::1") or url.username or url.password:
                raise ValueError("BIN_BASE_URL must be a loopback test server")
        suffix = "zip" if system == "windows" else "tar.gz"
        asset = f"tracereports_{REPORT_VERSION}_{system}_{arch}.{suffix}"

        def download(filename, limit):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("report binary download timed out")
            with urllib.request.urlopen(f"{base_url.rstrip('/')}/v{REPORT_VERSION}/{filename}", timeout=min(remaining, 5)) as response:
                chunks, size = [], 0
                while True:
                    # read1 avoids letting a slow response reset the overall deadline indefinitely.
                    chunk = response.read1(min(65536, limit + 1 - size))
                    if time.monotonic() >= deadline:
                        raise TimeoutError("report binary download timed out")
                    if not chunk:
                        return b"".join(chunks)
                    chunks.append(chunk)
                    size += len(chunk)
                    if size > limit:
                        raise ValueError("release download too large")

        checksums = download("checksums.txt", 1024 * 1024).decode("utf-8")
        hashes = [line.split()[0].lower() for line in checksums.splitlines()
                  if len(line.split()) == 2 and line.split()[1].lstrip("*") == asset]
        if len(hashes) != 1 or not re.fullmatch(r"[0-9a-f]{64}", hashes[0]):
            raise ValueError("release checksum missing or ambiguous")
        archive = download(asset, MAX_ARCHIVE)
        if hashlib.sha256(archive).hexdigest() != hashes[0]:
            raise ValueError("release checksum mismatch")
        if suffix == "zip":
            with zipfile.ZipFile(io.BytesIO(archive)) as z:
                entries = [e for e in z.infolist() if e.filename == name and not e.is_dir()]
                if len(entries) != 1 or entries[0].file_size > MAX_BINARY:
                    raise ValueError("invalid release executable")
                data = z.read(entries[0])
        else:
            with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
                entries = [e for e in tar if e.name == name and e.isfile()]
                if len(entries) != 1 or entries[0].size > MAX_BINARY:
                    raise ValueError("invalid release executable")
                data = tar.extractfile(entries[0]).read(MAX_BINARY + 1)
        if not data or len(data) > MAX_BINARY:
            raise ValueError("invalid release executable")
        target.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix=".download-", dir=parent) as staging:
            staged = Path(staging) / name
            staged.write_bytes(data)
            staged.chmod(0o755)
            digest = Path(staging) / "binary.sha256"
            digest.write_text(hashlib.sha256(data).hexdigest(), encoding="ascii")
            os.replace(digest, target / "binary.sha256")
            os.replace(staged, binary)
        return str(binary)
    finally:
        lock.rmdir()
