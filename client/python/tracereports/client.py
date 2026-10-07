"""
TraceReports Python client.

Zero dependencies (stdlib only). Designed to never break or slow down a test suite:

- the evidence (steps, screenshots, network, DOM, test results) is sent by a background thread
  from a bounded queue, so the test does not wait for the network;
- failed sends are retried with backoff; each one carries an ``Idempotency-Key`` so a retry
  never duplicates a step on the server;
- if the server does not answer, the calls that need its answer (create run/test) fail fast
  for a while instead of waiting their timeout in every test;
- ``end_run()`` waits for the queue (``flush_timeout``) and, if something could not be sent,
  saves it to ``spool_dir`` (``$TRACEREPORTS_SPOOL_DIR``) to resend later with
  ``python -m tracereports.resend <dir>``, or reports it as lost in ``cr.delivery``;
- if the run cannot be created (no server, unreachable, wrong token), everything is recorded in
  ``offline_dir`` instead (``$TRACEREPORTS_OFFLINE_DIR``, default ``./tracereports-offline``):
  ``tracereports report <dir>`` turns it into a static HTML report and ``tracereports push <dir>``
  uploads it later. ``offline="always"`` records without trying a server, ``"off"`` disables it.

Quick start::

    from tracereports import TraceReports

    cr = TraceReports("http://localhost:8080")
    cr.start_run("Regression - Checkout", environment="staging")   # project/branch/commit: auto

    cr.start_test("Login OK", category="smoke,login", description="Valid credentials",
                  key="tests/test_login.py::test_login_ok")          # stable identity (optional)
    cr.log_info("Open login page")
    cr.attach_screenshot(driver.get_screenshot_as_png(), "Login page")   # Selenium
    cr.attach_screenshot(page.screenshot(), "Login page")                 # Playwright
    cr.log_pass("User logged in")
    cr.end_test()                     # status derived from steps (or pass status="PASS")

    cr.end_run()                      # waits for the queue, then closes the run

With unittest / pytest you can let the client manage the test lifecycle::

    @cr.track(category="smoke")
    def test_login(self): ...

    with cr.test("Login OK", category="smoke"):
        ...
"""

from __future__ import annotations

import contextlib
import fnmatch
import functools
import json
import logging
import mimetypes
import os
import shutil
import subprocess
import threading
import time
import traceback
import urllib.error
import urllib.request
import uuid
from typing import Any, Callable, Iterator, Optional, Union

from ._env import env
from .context import detect_branch, detect_commit
from .offline import Recorder, new_session_dir
from .transport import Sender

__all__ = ["TraceReports"]
__version__ = "0.2.0"

log = logging.getLogger("tracereports")

BytesOrPath = Union[bytes, bytearray, str, "os.PathLike[str]"]

_SKIP_EXCEPTION_NAMES = {"SkipTest", "Skipped", "SkipException"}  # unittest, pytest, others


def _env_float(name: str, default: float) -> float:
    try:
        return float(env(name))
    except ValueError:
        return default


class TraceReports:
    """Client for the TraceReports server REST API."""

    def __init__(
        self,
        base_url: Optional[str] = None,
        timeout: float = 2.0,
        upload_timeout: float = 5.0,
        enabled: bool = True,
        max_consecutive_failures: int = 3,
        token: Optional[str] = None,
        async_send: Optional[bool] = None,
        spool_dir: Optional[str] = None,
        flush_timeout: Optional[float] = None,
        max_queue_items: int = 5000,
        max_queue_mb: int = 64,
        offline_dir: Optional[str] = None,
        offline: Optional[str] = None,
    ) -> None:
        """
        :param base_url: server URL. Defaults to $TRACEREPORTS_URL or http://localhost:8080.
        :param timeout: seconds per JSON request (keep it short so the browser is never blocked).
        :param upload_timeout: seconds per screenshot upload.
        :param enabled: set False (or $TRACEREPORTS_DISABLED=1) to turn every call into a no-op.
        :param max_consecutive_failures: kept for compatibility; an unreachable server now opens a
            30 s circuit (calls fail fast) and the queue keeps retrying instead of disabling.
        :param token: API token (the server's TRACEREPORTS_TOKEN). Defaults to $TRACEREPORTS_TOKEN.
        :param async_send: send the evidence from a background queue (default True; $TRACEREPORTS_SYNC=1
            sends every call inline, useful to debug).
        :param spool_dir: folder where unsent events are saved at end_run (default $TRACEREPORTS_SPOOL_DIR).
        :param flush_timeout: max seconds end_run waits for the queue (default $TRACEREPORTS_FLUSH_TIMEOUT or 30).
        :param max_queue_items / max_queue_mb: queue limits; when full, new events are dropped
            (counted in ``delivery["dropped"]``) and the already queued ones are kept.
        :param offline_dir: where to record when there is no server (default $TRACEREPORTS_OFFLINE_DIR;
            without it, a new folder per session inside ./tracereports-offline).
        :param offline: "auto" (default, $TRACEREPORTS_OFFLINE): record only if the run cannot be
            created; "always": record without trying a server; "off": never record.
        """
        self.base_url = (base_url or env("URL") or "http://localhost:8080").rstrip("/")
        self.timeout = timeout
        self.upload_timeout = upload_timeout
        self.enabled = enabled and env("DISABLED", "") not in ("1", "true", "yes")
        self.max_consecutive_failures = max_consecutive_failures
        self.token = token or env("TOKEN") or None
        if async_send is None:
            async_send = env("SYNC", "") not in ("1", "true", "yes")
        self.async_send = async_send
        self.flush_timeout = flush_timeout if flush_timeout is not None else _env_float("FLUSH_TIMEOUT", 30.0)
        self._sender = Sender(self.base_url, self._headers, max_items=max_queue_items, max_bytes=max_queue_mb << 20,
                              spool_dir=spool_dir or env("SPOOL_DIR") or None)

        self.run_id: Optional[int] = None
        self.run_created = False  # this client created the run (and must close it)
        self.unregistered_tests = 0  # tests whose creation failed: their evidence was not sent
        self._local = threading.local()  # current test id per thread
        self._expect: "dict[int, list[dict]]" = {}  # test_id -> respuestas esperadas
        self._lock = threading.Lock()

        self.offline_mode = (offline or env("OFFLINE", "auto")).strip().lower()
        if self.offline_mode in ("1", "true", "yes", "on"):
            self.offline_mode = "always"
        elif self.offline_mode in ("0", "false", "no"):
            self.offline_mode = "off"
        explicit = offline_dir or env("OFFLINE_DIR") or ""
        self._offline_base = explicit or "tracereports-offline"
        self._offline_explicit = bool(explicit)
        self.offline_dir: Optional[str] = None  # carpeta donde se está grabando (None: se envía al servidor)
        self.offline_report: Optional[str] = None  # index.html generado al cerrar, si el binario está
        if self.enabled and self.offline_mode == "always":
            self._go_offline("offline mode")

    # ------------------------------------------------------------- offline

    @property
    def recording(self) -> bool:
        """True when the evidence is recorded locally instead of sent (no server)."""
        return self.offline_dir is not None

    def _go_offline(self, reason: str) -> None:
        directory = self._offline_base if self._offline_explicit else new_session_dir(self._offline_base)
        try:
            self._sender = Recorder(directory)
        except OSError as err:
            log.warning("tracereports: could not record locally in %s: %s", directory, err)
            return
        self.offline_dir = directory
        if reason != "offline mode":
            log.warning("tracereports: %s; recording the evidence in %s (build the report with "
                        "`tracereports report %s`, or upload it later with `tracereports push %s`)",
                        reason, directory, directory, directory)

    def _offline_finish(self) -> None:
        """Closes the recording and, if the tracereports binary is installed, builds the report."""
        if not isinstance(self._sender, Recorder):
            return
        self._sender.close()
        binary = env("BIN") or shutil.which("tracereports")
        if not binary or env("OFFLINE_REPORT", "1") in ("0", "false", "no"):
            log.warning("tracereports: evidence recorded in %s. Report without a server: "
                        "`tracereports report %s -o report`; upload it: `tracereports push %s`",
                        self.offline_dir, self.offline_dir, self.offline_dir)
            return
        out = os.path.join(self.offline_dir, "report")
        try:
            subprocess.run([binary, "report", "-o", out, self.offline_dir], check=True, timeout=300,
                           stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            self.offline_report = os.path.join(out, "index.html")
            log.warning("tracereports: no server; static report in %s", self.offline_report)
        except (OSError, subprocess.SubprocessError) as err:
            log.warning("tracereports: could not build the report (%s); run `tracereports report %s`", err, self.offline_dir)

    # -------------------------------------------------------------- delivery

    @property
    def delivery(self) -> dict:
        """Delivery status: sent, retried, rejected (4xx), dropped (queue full), spooled, lost,
        pending and unregistered_tests."""
        d = dict(self._sender.stats)
        d["pending"] = self._sender.pending
        d["unregistered_tests"] = self.unregistered_tests
        return d

    def delivery_problems(self) -> int:
        """Events that did not reach the server (and will not unless resent from the spool)."""
        d = self.delivery
        return d["rejected"] + d["dropped"] + d["lost"] + d["spooled"] + d["pending"] + d["unregistered_tests"]

    def flush(self, timeout: Optional[float] = None) -> int:
        """Wait until the queued evidence is sent (max ``timeout`` s, default flush_timeout).
        Returns how many events are still pending."""
        return self._sender.flush(self.flush_timeout if timeout is None else timeout)

    # ------------------------------------------------------------------ runs

    def start_run(
        self,
        name: str,
        environment: str = "",
        project: Optional[str] = None,
        branch: Optional[str] = None,
        commit: Optional[str] = None,
        framework: str = "",
        detect_context: bool = True,
    ) -> Optional[int]:
        """
        Create a run (suite execution). Returns run_id or None if the server is unreachable.

        ``project`` (default $TRACEREPORTS_PROJECT), ``branch`` and ``commit`` (default: CI variables or
        git) define the context: history, flakiness and comparisons only use runs of the same
        project, environment and branch.
        """
        if detect_context:
            branch = detect_branch() if branch is None else branch
            commit = detect_commit() if commit is None else commit
        payload = {"name": name, "environment": environment, "project": project or env("PROJECT", ""),
                   "branch": branch or "", "commit": commit or "", "framework": framework}
        if self._sender.spool_dir and self.enabled:
            resent = self._sender.load_spool()
            if resent:
                log.info("tracereports: reenviando %d eventos guardados de una ejecución anterior", resent)
        res = self._request("POST", "/api/v1/runs", payload)
        if not res and self.enabled and self.offline_mode == "auto" and not self.recording:
            # sin servidor, caído o con el token equivocado: se graba en vez de perderlo todo
            self._go_offline(f"could not create the run at {self.base_url} (server down or wrong token)")
            if self.recording:
                res = self._request("POST", "/api/v1/runs", payload)
        self.run_id = res.get("run_id") if res else None
        self.run_created = bool(self.run_id)
        if self.enabled and not self.run_id:
            log.warning(
                "tracereports: could not create run at %s; is the server running (go run ./cmd)? "
                "Tests will continue without reporting.",
                self.base_url,
            )
        return self.run_id

    def join_run(self, run_id: Optional[int], offline_dir: Optional[str] = None) -> Optional[int]:
        """Report into a run created elsewhere (pytest-xdist controller, CI shards with
        $TRACEREPORTS_RUN_ID). The client that created it is the one that closes it. A negative
        run_id is a run being recorded without a server: pass the same ``offline_dir``."""
        if run_id and int(run_id) < 0 and self.enabled and not self.recording:
            if offline_dir:
                self._offline_base, self._offline_explicit = offline_dir, True
            self._go_offline("offline mode")
        self.run_id = int(run_id) if run_id else None
        self.run_created = False
        return self.run_id

    def end_run(self, run_id: Optional[int] = None, interrupted: bool = False) -> Optional[dict]:
        """
        Wait for the queued evidence (flush_timeout), keep what could not be sent (spool or lost)
        and close the run. ``interrupted=True`` marks it incomplete (Ctrl+C, crashed worker...):
        the server never shows an incomplete run as passed.
        """
        rid = run_id or self.run_id
        self.flush()
        self._sender.drain_to_spool()
        if not rid:
            return None
        payload = {"interrupted": bool(interrupted)}
        res = self._request("PATCH", f"/api/v1/runs/{rid}/finish", payload)
        if res is None and self.enabled:
            # el servidor no respondió: el cierre también va al spool (se reenvía con lo demás)
            self._sender.enqueue("PATCH", f"/api/v1/runs/{rid}/finish", json.dumps(payload).encode(), "application/json", self.timeout)
            self._sender.drain_to_spool()
        if self.recording:
            self._offline_finish()
        return res

    def download_report(self, dest_dir: str = ".", run_id: Optional[int] = None, timeout: float = 60.0) -> Optional[str]:
        """
        Download the run as a self-contained ZIP (open index.html, no server or internet needed) —
        e.g. to attach it to an email from CI. Call after end_run(). Returns the ZIP path or None.
        """
        rid = run_id or self.run_id
        if self.recording:
            return self._offline_zip(dest_dir)
        if not rid or not self.enabled or self._sender.server_down():
            return None
        req = urllib.request.Request(f"{self.base_url}/api/v1/runs/{rid}/export", headers=self._headers())
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                disposition = resp.headers.get("Content-Disposition", "")
                filename = disposition.split('filename="')[-1].rstrip('"') if 'filename="' in disposition else f"tracereports_run{rid}.zip"
                os.makedirs(dest_dir, exist_ok=True)
                path = os.path.join(dest_dir, os.path.basename(filename))
                with open(path, "wb") as fh:
                    fh.write(resp.read())
            return path
        except (urllib.error.URLError, OSError) as err:
            log.warning("tracereports: could not download report for run %s: %s", rid, err)
            return None

    def _offline_zip(self, dest_dir: str) -> Optional[str]:
        """download_report without a server: the binary builds the ZIP from the recording."""
        binary = env("BIN") or shutil.which("tracereports")
        if not binary:
            log.warning("tracereports: no server and no tracereports binary: run `tracereports report %s --zip report.zip`", self.offline_dir)
            return None
        os.makedirs(dest_dir, exist_ok=True)
        path = os.path.join(dest_dir, "tracereports_" + os.path.basename(os.path.normpath(self.offline_dir)) + ".zip")
        try:
            subprocess.run([binary, "report", "-o", os.path.join(self.offline_dir, "report"), "--zip", path, self.offline_dir],
                           check=True, timeout=300, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            return path
        except (OSError, subprocess.SubprocessError) as err:
            log.warning("tracereports: could not build the report ZIP: %s", err)
            return None

    # ----------------------------------------------------------------- tests

    @property
    def current_test_id(self) -> Optional[int]:
        return getattr(self._local, "test_id", None)

    def start_test(
        self,
        name: str,
        category: str = "",
        description: str = "",
        key: Optional[str] = None,
        suite: str = "",
        params: str = "",
        worker: Optional[str] = None,
    ) -> Optional[int]:
        """
        Start a test inside the current run. ``category`` accepts comma-separated tags.

        ``key`` is the stable identity of the test inside the project (pytest's nodeid, a
        package + test name...): history, flakiness and comparisons follow it. Without it the
        server uses the name, and two tests with the same name share their history. Avoid keys
        built from random or secret data. ``suite``/``params``/``worker`` are informative.
        """
        if not self.run_id:
            self._local.test_id = None
            return None
        payload = {"name": name, "category": category, "description": description, "key": key or "",
                   "suite": suite, "params": params,
                   "worker": worker if worker is not None else os.getenv("PYTEST_XDIST_WORKER", "")}
        res = self._request("POST", f"/api/v1/runs/{self.run_id}/tests", payload)
        self._local.test_id = res.get("test_id") if res else None
        if self.enabled and not self._local.test_id:
            with self._lock:
                self.unregistered_tests += 1
        return self._local.test_id

    def end_test(
        self,
        status: Optional[str] = None,
        error_message: str = "",
        error_trace: str = "",
        exc: Optional[BaseException] = None,
        test_id: Optional[int] = None,
        attempts: Optional[int] = None,
    ) -> None:
        """
        Close the test. ``status``: PASS | FAIL | WARNING | SKIP, or None to derive it from the logged steps.
        Pass ``exc`` to fill error_message / error_trace from an exception. A FAIL triggers AI triage.
        ``attempts``: how many times the runner executed it (retries; >1 shows "passed after retry").
        """
        tid = test_id or self.current_test_id
        if not tid:
            return None
        if exc is not None:
            error_message = error_message or f"{type(exc).__name__}: {exc}"
            error_trace = error_trace or "".join(traceback.format_exception(type(exc), exc, exc.__traceback__))
            status = status or "FAIL"
        payload = {"status": (status or "").upper(), "error_message": error_message, "error_trace": error_trace}
        if attempts and attempts > 1:
            payload["attempts"] = int(attempts)
        self._emit("PATCH", f"/api/v1/tests/{tid}/finish", payload)
        self._expect.pop(tid, None)
        if tid == self.current_test_id:
            self._local.test_id = None
        return None

    def expect_response(self, status: Union[int, "list[int]"], url: str = "", method: str = "",
                        test_id: Optional[int] = None) -> None:
        """
        Declare a negative response the test checks on purpose (e.g. a 401 with bad credentials).
        Matching connections are reported as expected: they are not counted as errors nor
        proposed as the cause of a failure. ``url`` is a substring or a glob (``*/api/auth*``).
        """
        tid = test_id or self.current_test_id
        if not tid:
            return
        statuses = [status] if isinstance(status, int) else list(status)
        self._expect.setdefault(tid, []).append({"status": statuses, "url": url, "method": method.upper()})

    def _is_expected(self, tid: int, conn: dict) -> bool:
        for rule in self._expect.get(tid, ()):
            if int(conn.get("status") or 0) not in rule["status"]:
                continue
            if rule["method"] and (conn.get("method") or "").upper() != rule["method"]:
                continue
            url = conn.get("url") or ""
            pattern = rule["url"]
            if pattern and not (fnmatch.fnmatch(url, pattern) if any(ch in pattern for ch in "*?[") else pattern in url):
                continue
            return True
        return False

    # ------------------------------------------------------------------ logs

    def log(self, status: str, message: str, test_id: Optional[int] = None) -> None:
        tid = test_id or self.current_test_id
        if not tid:
            return
        self._emit(
            "POST",
            f"/api/v1/tests/{tid}/logs",
            {"status": status.upper(), "message": str(message), "timestamp": int(time.time() * 1000)},
        )

    def log_info(self, message: str, **kw: Any) -> None:
        self.log("INFO", message, **kw)

    def log_pass(self, message: str, **kw: Any) -> None:
        self.log("PASS", message, **kw)

    def log_fail(self, message: str, **kw: Any) -> None:
        self.log("FAIL", message, **kw)

    def log_warning(self, message: str, **kw: Any) -> None:
        self.log("WARNING", message, **kw)

    def log_skip(self, message: str, **kw: Any) -> None:
        self.log("SKIP", message, **kw)

    def attach_screenshot(
        self,
        bytes_or_path: BytesOrPath,
        message: str = "",
        status: str = "INFO",
        test_id: Optional[int] = None,
        wait: bool = False,
    ) -> Optional[str]:
        """
        Upload a screenshot (raw PNG/JPEG bytes or a file path) as a step. It is queued and sent
        in the background; with ``wait=True`` it is sent now and the absolute image URL is returned.
        """
        tid = test_id or self.current_test_id
        if not tid or not self.enabled:
            return None
        try:
            if isinstance(bytes_or_path, (bytes, bytearray)):
                data, filename = bytes(bytes_or_path), "screenshot.png"
            else:
                path = os.fspath(bytes_or_path)
                with open(path, "rb") as fh:
                    data = fh.read()
                filename = os.path.basename(path)
        except OSError as err:
            log.warning("tracereports: cannot read screenshot %r: %s", bytes_or_path, err)
            return None

        body, content_type = _multipart(
            fields={"message": message, "status": status.upper()},
            file_field="file",
            filename=filename,
            data=data,
        )
        path = f"/api/v1/tests/{tid}/screenshot"
        if wait or not self.async_send:
            res = self._sender.send_now("POST", path, body, content_type, self.upload_timeout) if self.enabled else None
            return f"{self.base_url}{res['url']}" if res and res.get("url") else None
        self._sender.enqueue("POST", path, body, content_type, max(self.upload_timeout, 10.0))
        return None

    def attach_artifact(
        self,
        bytes_or_path: BytesOrPath,
        kind: str,
        name: str = "",
        test_id: Optional[int] = None,
    ) -> bool:
        """
        Attach the Playwright trace (``kind="trace"``, the trace.zip) or the video of the test
        (``kind="video"``, WebM or MP4), as bytes or a file path; up to 100 MB. The report plays the
        video and opens the trace in the Playwright Trace Viewer. With pytest-playwright::

            pytest --tracing retain-on-failure --video retain-on-failure --tracereports

        and, in a fixture after the test, ``tracereports.attach_artifact(path, "trace")`` for each
        file it left in ``test-results/``.
        """
        tid = test_id or self.current_test_id
        if not tid or not self.enabled or kind not in ("trace", "video"):
            return False
        try:
            if isinstance(bytes_or_path, (bytes, bytearray)):
                data = bytes(bytes_or_path)
            else:
                path = os.fspath(bytes_or_path)
                if os.path.getsize(path) > 100 << 20:
                    log.warning("tracereports: %s is larger than 100 MB: not attached", path)
                    return False
                with open(path, "rb") as fh:
                    data = fh.read()
                name = name or os.path.basename(path)
        except OSError as err:
            log.warning("tracereports: cannot read %s %r: %s", kind, bytes_or_path, err)
            return False
        if len(data) > 100 << 20:
            log.warning("tracereports: the %s is larger than 100 MB: not attached", kind)
            return False
        safe = (name or kind).replace('"', "'").replace("\r", " ").replace("\n", " ")
        body, content_type = _multipart(fields={"kind": kind, "name": safe}, file_field="file", filename="artifact", data=data)
        return self._emit_raw("POST", f"/api/v1/tests/{tid}/artifact", body, content_type, max(self.upload_timeout, 60.0))

    def attach_console(self, entries: list, test_id: Optional[int] = None) -> bool:
        """
        Upload the browser console of the test (its "Consola" tab): a list of
        ``{"level": "error"|"warning"|"pageerror"|..., "text": ..., "location": ..., "timestamp": ms}``.
        With pytest-playwright the plugin captures it by itself (console errors and warnings, and
        uncaught page errors).
        """
        tid = test_id or self.current_test_id
        if not tid or not entries or not self.enabled:
            return False
        body = json.dumps({"entries": entries[:500]}).encode()
        return self._emit_raw("POST", f"/api/v1/tests/{tid}/console", body, "application/json", max(self.upload_timeout, 10.0))

    def attach_network(
        self,
        connections: list,
        test_id: Optional[int] = None,
        max_body_kb: int = 256,
        batch_size: int = 200,
    ) -> Optional[dict]:
        """
        Upload captured network connections (see tracereports.network) to the test's
        "Red" tab. Accepts the dicts produced by the capture (url, method, status, failed,
        wall_time, duration_ms, headers, post_data, response_body, ...). Response bodies are
        cut to ``max_body_kb`` before sending. Connections declared with expect_response() are
        marked as expected. Returns {"stored": n, "errors": n} (queued) or None.
        """
        tid = test_id or self.current_test_id
        if not tid or not connections or not self.enabled:
            return None
        limit = max_body_kb * 1024
        payload = [_network_payload(c, limit) for c in connections]
        for p in payload:
            p["expected"] = self._is_expected(tid, p)
        errors = sum(1 for p in payload if not p["expected"] and (p["failed"] or p["status"] >= 400))
        for i in range(0, len(payload), batch_size):
            body = json.dumps({"connections": payload[i:i + batch_size]}).encode()
            self._emit_raw("POST", f"/api/v1/tests/{tid}/network", body, "application/json", max(self.upload_timeout, 10.0))
        return {"stored": len(payload), "errors": errors}

    def attach_dom(self, snapshot: Optional[dict], test_id: Optional[int] = None) -> bool:
        """
        Sube el snapshot de la página tomado al fallar (ver tracereports.capturar_dom):
        con él la UI recomienda selectores para reemplazar uno roto y marca el elemento en la
        captura. Enviar ANTES de end_test().
        """
        tid = test_id or self.current_test_id
        if not tid or not snapshot or not self.enabled:
            return False
        body = json.dumps(snapshot).encode()
        return self._emit_raw("POST", f"/api/v1/tests/{tid}/dom", body, "application/json", max(self.upload_timeout, 10.0))

    # ---------------------------------------------------- framework helpers

    @contextlib.contextmanager
    def test(
        self,
        name: str,
        category: str = "",
        description: str = "",
        on_failure: Optional[Callable[[], bytes]] = None,
    ) -> Iterator["TraceReports"]:
        """
        Context manager that starts/ends a test and reports exceptions::

            with cr.test("Checkout", category="e2e", on_failure=driver.get_screenshot_as_png):
                ...

        ``on_failure`` (optional) returns screenshot bytes captured when the block fails.
        Exceptions are always re-raised so the test framework still sees them.
        """
        self.start_test(name, category, description)
        try:
            yield self
        except BaseException as exc:  # noqa: BLE001 - re-raised below
            if type(exc).__name__ in _SKIP_EXCEPTION_NAMES:
                self.log_skip(str(exc) or "Skipped")
                self.end_test("SKIP")
            else:
                if on_failure is not None:
                    with contextlib.suppress(Exception):
                        self.attach_screenshot(on_failure(), "Screenshot at failure", status="FAIL")
                self.log_fail(f"{type(exc).__name__}: {exc}")
                self.end_test(exc=exc)
            raise
        else:
            self.end_test()

    def track(
        self,
        name: Optional[str] = None,
        category: str = "",
        description: Optional[str] = None,
        on_failure: Optional[Callable[[Any], bytes]] = None,
    ) -> Callable:
        """
        Decorator for unittest/pytest test functions. Uses the docstring as description.
        ``on_failure`` receives the first positional argument (``self`` for unittest),
        e.g. ``on_failure=lambda self: self.driver.get_screenshot_as_png()``.
        """

        def decorator(fn: Callable) -> Callable:
            @functools.wraps(fn)
            def wrapper(*args: Any, **kwargs: Any) -> Any:
                shot = (lambda: on_failure(args[0] if args else None)) if on_failure else None
                desc = description if description is not None else (fn.__doc__ or "").strip()
                with self.test(name or _pretty(fn.__name__), category, desc, on_failure=shot):
                    return fn(*args, **kwargs)

            return wrapper

        return decorator

    # ----------------------------------------------------------------- HTTP

    def _headers(self, extra: Optional[dict] = None) -> dict:
        headers = {"User-Agent": f"tracereports-py/{__version__}", **(extra or {})}
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        return headers

    def _active(self) -> bool:
        return self.enabled

    def _request(self, method: str, path: str, payload: dict) -> Optional[dict]:
        """Synchronous call whose answer is needed (run/test ids, closing the run)."""
        if not self.enabled:
            return None
        return self._sender.send_now(method, path, json.dumps(payload).encode(), "application/json", self.timeout)

    def _emit(self, method: str, path: str, payload: dict) -> bool:
        return self._emit_raw(method, path, json.dumps(payload).encode(), "application/json", max(self.timeout, 5.0))

    def _emit_raw(self, method: str, path: str, body: bytes, content_type: str, timeout: float) -> bool:
        """Evidence: queued (default) or sent inline with TRACEREPORTS_SYNC=1."""
        if not self.enabled:
            return False
        if self.async_send:
            return self._sender.enqueue(method, path, body, content_type, timeout)
        return self._sender.send_now(method, path, body, content_type, timeout) is not None


def _network_payload(c: dict, body_limit: int) -> dict:
    """Map a captured connection to the server's JSON shape, cutting large bodies."""
    body = c.get("response_body") or ""
    if not isinstance(body, str):
        body = str(body)
    wall_time = c.get("wall_time")
    duration = c.get("duration_ms")
    return {
        "method": c.get("method") or "",
        "url": c.get("url") or c.get("response_url") or "",
        "status": int(c.get("status") or 0),
        "status_text": c.get("status_text") or "",
        "mime_type": c.get("mime_type") or "",
        "resource_type": c.get("resource_type") or "",
        "failed": bool(c.get("failed")),
        "error_text": str(c.get("error_text") or ""),
        "started_at": int(wall_time * 1000) if wall_time else 0,
        "duration_ms": int(duration) if duration is not None else None,
        "request_headers": {str(k): str(v) for k, v in (c.get("request_headers") or {}).items()},
        "post_data": str(c.get("post_data") or ""),
        "post_data_via_cdp": bool(c.get("post_data_via_cdp")),
        "response_headers": {str(k): str(v) for k, v in (c.get("response_headers") or {}).items()},
        "response_body": body[:body_limit],
        # the capture may already have cut the body: keep the original size
        "body_size": max(int(c.get("body_size") or 0), len(body)),
        "body_truncated": len(body) > body_limit or int(c.get("body_size") or 0) > len(body),
        # local JSON with the complete capture (reportar_red(..., guardar_en=...))
        "evidence_file": str(c.get("evidence_file") or ""),
        # respuesta negativa esperada por el test (expect_response / marker tracereports_expect)
        "expected": bool(c.get("expected")),
    }


def _pretty(fn_name: str) -> str:
    name = fn_name[5:] if fn_name.startswith("test_") else fn_name
    return name.replace("_", " ").strip().capitalize() or fn_name


def _multipart(fields: dict, file_field: str, filename: str, data: bytes) -> "tuple[bytes, str]":
    boundary = uuid.uuid4().hex
    mime = mimetypes.guess_type(filename)[0] or "application/octet-stream"
    parts = []
    for key, value in fields.items():
        parts.append(
            f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode()
        )
    parts.append(
        f'--{boundary}\r\nContent-Disposition: form-data; name="{file_field}"; filename="{filename}"\r\n'
        f"Content-Type: {mime}\r\n\r\n".encode()
        + data
        + b"\r\n"
    )
    parts.append(f"--{boundary}--\r\n".encode())
    return b"".join(parts), f"multipart/form-data; boundary={boundary}"
