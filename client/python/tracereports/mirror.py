"""Copia de llamadas lógicas; el Sender conserva la cola, el spool y los reintentos."""
from __future__ import annotations

import contextlib
import json
import logging
import os
from pathlib import Path
import re
import shutil
import threading
import time
import uuid

from .offline import Recorder

MARKER = "tracereports-offline.json"
log = logging.getLogger("tracereports")


class Mirror:
    def __init__(self, sender, directory, server, run_id, payload=None):
        self.sender = sender
        self.directory = Path(directory)
        self.runs, self.tests = {}, {}
        self.failed = self.disabled = self.closed = False
        self._lock = threading.RLock()
        self._active = 0
        self.directory.mkdir(parents=True, exist_ok=True)
        try:
            with self.locked():
                marker = self.directory / MARKER
                if marker.exists() and json.loads(marker.read_text(encoding="utf-8")).get("raw_removed"):
                    raise ValueError("only the report remains; use a new recording directory")
                # Se registra antes de abrir el archivo: un proceso interrumpido impide el borrado.
                self.status = f"status-{os.getpid()}-{uuid.uuid4().hex[:8]}.json"
                self.write(self.status, {"complete": False, "reason": "active"})
                self.recorder = Recorder(str(directory))
                actual = f"status-{os.getpid()}-{self.recorder._tag}.json"
                os.replace(self.directory / self.status, self.directory / actual)
                self.status = actual
                m = json.loads(marker.read_text(encoding="utf-8"))
                mirror = m.setdefault("mirror", {"server": server, "runs": [], "complete": False})
                if mirror["server"] != server:
                    raise ValueError("the recording belongs to another server")
                mirror["complete"] = False
                self.write(MARKER, m)
                mapping = self.directory / "ids" / f"server-{run_id}"
                if payload is not None:
                    local = self.recorder.send_now("POST", "/api/v1/runs", json.dumps(payload).encode(), "application/json", 0)["run_id"]
                    with mapping.open("x") as f:
                        f.write(str(local))
                    mirror["runs"].append({"local": local, "server": run_id})
                    self.write(MARKER, m)
                else:
                    try:
                        local = int(mapping.read_text())
                        if local >= 0:
                            raise ValueError("invalid local id")
                    except (OSError, ValueError) as err:
                        local = None
                        self.failed = True
                        log.warning("tracereports: missing local id for run #%s: %s", run_id, err)
                if local:
                    self.runs[run_id] = local
        except Exception:
            recorder = self.__dict__.get("recorder")
            if recorder is not None:
                recorder.close()
            raise

    def __getattr__(self, name):
        return getattr(self.sender, name)

    @property
    def stats(self):
        return dict(self.sender.stats, recorded=self.recorder.stats["recorded"])

    @contextlib.contextmanager
    def locked(self):
        lock = self.directory / ".mirror-lock"
        for n in range(51):
            try:
                lock.mkdir()
                break
            except FileExistsError:
                if n == 50:
                    raise
                time.sleep(.01)
        try:
            yield
        finally:
            lock.rmdir()

    def write(self, name, value):
        dest = self.directory / name
        tmp = self.directory / (name + "." + uuid.uuid4().hex + ".tmp")
        tmp.write_text(json.dumps(value), encoding="utf-8")
        os.replace(tmp, dest)

    def disk_error(self, err):
        self.failed = True
        if not self.disabled:
            log.warning("tracereports: stopped local copy in %s: %s", self.directory, err)
        self.disabled = True

    def record(self, method, path, body, content_type):
        if self.disabled or self.closed:
            return None
        def replace(match):
            kind, number = match.groups()
            number = int(number)
            ids = self.runs if kind == "runs" else self.tests
            local = ids.get(number, number)
            if local > 0:
                self.failed = True
                local = self.recorder._local_id()
                ids[number] = local
            return f"/api/v1/{kind}/{local}"
        try:
            path = re.sub(r"^/api/v1/(runs|tests)/(-?\d+)", replace, path)
            return self.recorder.send_now(method, path, body, content_type, 0)
        except (OSError, ValueError) as err:
            self.disk_error(err)
            return None

    def send_now(self, method, path, body, content_type, *args, **kwargs):
        with self._lock:
            self._active += 1
            local = self.record(method, path, body, content_type)
        try:
            remote = None if re.search(r"/(runs|tests)/-\d+", path) else self.sender.send_now(method, path, body, content_type, *args, **kwargs)
            with self._lock:
                if remote is None:
                    self.failed = True
                if local and local.get("test_id") and remote and remote.get("test_id"):
                    self.tests[remote["test_id"]] = local["test_id"]
            return remote if remote is not None else local if local and local.get("test_id") else None
        finally:
            with self._lock:
                self._active -= 1

    def enqueue(self, method, path, body, content_type, *args, **kwargs):
        with self._lock:
            self.record(method, path, body, content_type)
            ok = False if re.search(r"/(runs|tests)/-\d+", path) else self.sender.enqueue(method, path, body, content_type, *args, **kwargs)
            if not ok:
                self.failed = True
            return ok

    def close(self, complete=True):
        with self._lock:
            if self.closed:
                return
            self.closed = True
            try:
                self.recorder.close()
                d = self.sender.stats
                complete = complete and not self.failed and not self._active and not self.sender.pending and not any(d[k] for k in ("rejected", "dropped", "lost", "spooled"))
                with self.locked():
                    self.write(self.status, {"complete": bool(complete), "reason": "" if complete else "delivery or recording incomplete"})
            except (OSError, ValueError) as err:
                self.disk_error(err)

    def snapshot(self):
        files = sorted(self.directory.glob("status-*.json"))
        names = {f.name for f in files}
        if not files or any(f.name.replace("events-", "status-", 1).replace(".jsonl", ".json") not in names for f in self.directory.glob("events-*.jsonl")):
            return None
        entries = [(f.name, f.read_text(encoding="utf-8")) for f in files]
        return entries if all(json.loads(raw).get("complete") is True for _, raw in entries) else None

    def prepare(self):
        try:
            with self.locked():
                return self.snapshot()
        except (OSError, ValueError) as err:
            self.disk_error(err)
            return None

    def finalize(self, report, keep, before):
        try:
            with self.locked():
                snapshot = self.snapshot()
                m = json.loads((self.directory / MARKER).read_text(encoding="utf-8"))
                m["mirror"]["complete"] = not self.failed and snapshot is not None
                remove = m["mirror"]["complete"] and before == snapshot and report and Path(report).is_file() and not keep
                if remove:
                    m["raw_removed"] = True
                self.write(MARKER, m)
                if remove:
                    for f in self.directory.iterdir():
                        if f.name in ("bodies", "ids"):
                            if f.is_symlink():
                                f.unlink()
                            else:
                                shutil.rmtree(f)
                        elif re.fullmatch(r"(events-.*\.jsonl|status-.*\.json)", f.name):
                            f.unlink()
        except (OSError, ValueError) as err:
            self.disk_error(err)
