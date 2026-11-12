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
import socket
import subprocess
import threading
import time
import traceback
import urllib.error
import urllib.request
import uuid
from typing import Any, Callable, Iterator, Optional, Union

from ._env import env
from .binary import resolve_binary
from .context import default_execution_id, detect_branch, detect_commit
from .dom import dom_on, dom_signature, dom_steps_enabled, dom_steps_mode
from .mask import screenshot_allowed
from .offline import Recorder, new_session_dir
from .transport import Sender
from .mirror import Mirror

__all__ = ["TraceReports"]
__version__ = "0.6.0"

log = logging.getLogger("tracereports")

BytesOrPath = Union[bytes, bytearray, str, "os.PathLike[str]"]

# Tope para crear o cerrar la ejecución contando los reintentos (run_timeout es por intento): con un
# servidor que acepta y no responde, cada proceso (p. ej. 16 workers de xdist) espera esto como mucho.
# 30 s: un corte de unos segundos (el reenvío de puertos de Docker Desktop, la red) no hace perder la
# ejecución; un servidor caído (conexión rechazada) sigue pasando a local al instante.
RUN_TOTAL_SECONDS = 30.0

_SKIP_EXCEPTION_NAMES = {"SkipTest", "Skipped", "SkipException"}  # unittest, pytest, others


def _env_float(name: str, default: float) -> float:
    try:
        return float(env(name))
    except ValueError:
        return default


# TRACEREPORTS_DOM_STEPS=on-problem: páginas en memoria por test y antigüedad máxima de la base de comparación
MAX_DOM_BUFFER = 40
BASELINE_MAX_AGE_MS = 7 * 24 * 3600 * 1000


class TraceReports:
    """Client for the TraceReports server REST API."""

    def __init__(
        self,
        base_url: Optional[str] = None,
        timeout: Optional[float] = None,
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
        offline_base: Optional[str] = None,
        offline_name: Optional[str] = None,
        run_timeout: Optional[float] = None,
    ) -> None:
        """
        :param base_url: server URL. Defaults to $TRACEREPORTS_URL or http://localhost:8080.
        :param timeout: seconds per JSON request (default $TRACEREPORTS_TIMEOUT or 2; keep it short so
            the browser is never blocked).
        :param run_timeout: seconds to create and close the run, which can take longer on a busy server
            (default $TRACEREPORTS_RUN_TIMEOUT, or 10, or ``timeout`` if it is longer). A timeout is
            retried with the same Idempotency-Key, so a slow server never ends with two runs; all the
            attempts together take at most 15 s (or one ``run_timeout`` if it is longer).
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
        :param offline_dir: exact folder of the recording, shared by the processes of one run
            (default $TRACEREPORTS_OFFLINE_DIR; without it, a new folder per session inside offline_base).
        :param offline_base: folder where each session creates its own recording folder
            (default $TRACEREPORTS_OFFLINE_BASE or ./tracereports-offline).
        :param offline_name: label for automatically created recording folders (default run name).
        :param offline: "auto" (default, $TRACEREPORTS_OFFLINE): record only if the run cannot be
            created; "always": record without trying a server; "both": also keep a local copy; "off": never record.
        """
        self.base_url = (base_url or env("URL") or "http://localhost:8080").rstrip("/")
        self.timeout = timeout if timeout is not None else _env_float("TIMEOUT", 2.0)
        self.run_timeout = run_timeout if run_timeout is not None else _env_float("RUN_TIMEOUT", max(10.0, self.timeout))
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
        # TRACEREPORTS_DOM_STEPS=1: the page of each step with a screenshot also goes to the Inspector.
        # dom_step_source returns the snapshot of the current page (the pytest plugin sets it per test).
        self.dom_steps = dom_steps_enabled()
        # TRACEREPORTS_DOM_STEPS=on-problem: las páginas esperan aquí hasta saber si el test tuvo un problema
        self.dom_steps_mode = dom_steps_mode()
        self._dom_buffer: "dict[int, list[tuple[str, str, dict]]]" = {}
        self._dom_problem: "set[int]" = set()
        # TRACEREPORTS_DOM_ON=fail,warning: also the page of each WARNING step (it often comes before a failure)
        self.dom_on_warning = "warning" in dom_on()
        self.dom_step_source: Optional[Callable[[], Optional[dict]]] = None
        self._dom_step_sig: "dict[tuple[int, str], str]" = {}
        # TRACEREPORTS_REMOTE_CONTROL=1: between tests, ask the server whether the run was cancelled
        self.remote_control = env("REMOTE_CONTROL", "").strip().lower() in ("1", "true", "yes", "on")
        self._control = {"checked": 0.0, "cancel": False, "reason": "", "off": False}
        self._local = threading.local()  # current test id per thread
        self._expect: "dict[int, list[dict]]" = {}  # test_id -> respuestas esperadas
        self._lock = threading.Lock()

        self.offline_mode = (offline or env("OFFLINE", "auto")).strip().lower()
        if self.offline_mode in ("1", "true", "yes", "on"):
            self.offline_mode = "always"
        elif self.offline_mode in ("0", "false", "no"):
            self.offline_mode = "off"
        explicit = offline_dir or env("OFFLINE_DIR") or ""
        # sin carpeta exacta, cada sesión crea la suya dentro de la base: corridas sucesivas no se pisan
        self._offline_base = explicit or offline_base or env("OFFLINE_BASE") or "tracereports-offline"
        self._offline_explicit = bool(explicit)
        self._offline_name = offline_name or env("OFFLINE_NAME", "")
        self.offline_dir: Optional[str] = None  # carpeta donde se está grabando (None: se envía al servidor)
        self.offline_report: Optional[str] = None  # index.html generado al cerrar
        self._push_at_end = False  # la ejecución no se pudo crear al inicio: end_run intenta subir la grabación
        self._late: Optional[dict] = None  # por qué no se pudo crear al inicio (va en el informe de entrega)
        self._sender_before_offline = None  # el envío de antes de grabar en local: guarda el detalle del timeout
        self.pushed_report_url: Optional[str] = None  # la ejecución subida al final (push), si se pudo

    # ------------------------------------------------------------- offline

    @property
    def recording(self) -> bool:
        """True when the evidence is recorded locally instead of sent (no server)."""
        return self.offline_dir is not None

    def _go_offline(self, reason: str) -> None:
        directory = self._offline_base if self._offline_explicit else new_session_dir(self._offline_base, self._offline_name)
        self._sender_before_offline = self._sender
        try:
            self._sender = Recorder(directory)
        except OSError as err:
            log.warning("tracereports: could not record locally in %s: %s", directory, err)
            return
        self.offline_dir = directory
        if reason != "offline mode":
            log.warning("tracereports: %s; recording the evidence in %s", reason, directory)

    def _go_mirror(self, run_id, payload=None, directory=None):
        directory = directory or (self._offline_base if self._offline_explicit else new_session_dir(self._offline_base, self._offline_name))
        try:
            self._sender = Mirror(self._sender, directory, self.base_url, run_id, payload)
            self.offline_dir = directory
        except (OSError, ValueError) as err:
            log.warning("tracereports: could not start local copy in %s: %s", directory, err)

    @property
    def report_url(self):
        if self.run_id and self.run_id > 0:
            return f"{self.base_url}/#run={self.run_id}&view=dashboard"
        return self.offline_report or ""

    def _offline_finish(self) -> None:
        """Close the recording and build its report with the installed or cached renderer."""
        if not isinstance(self._sender, (Recorder, Mirror)):
            return
        self._sender.close()
        out = os.path.join(self.offline_dir, "report")
        try:
            if env("OFFLINE_REPORT", "1").lower() in ("0", "false", "no"):
                raise OSError("automatic report disabled")
            binary = resolve_binary()
            subprocess.run([binary, "report", "-o", out, self.offline_dir], check=True, timeout=300,
                           stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            index = os.path.join(out, "index.html")
            if not os.path.isfile(index):
                raise OSError("renderer did not create index.html")
            self.offline_report = index
            if self.run_id and self.run_id > 0:
                log.warning("tracereports: server report: %s", self.report_url)
            log.warning("tracereports: local report in %s", self.offline_report)
        except Exception as err:
            log.warning("tracereports: local report unavailable (%s); evidence retained in %s. "
                        "install the binary and run `tracereports report %s`, or run "
                        "`tracereports push %s` with the correct token when the server is available.",
                        err, self.offline_dir, self.offline_dir, self.offline_dir)

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
        execution_id: Optional[str] = None,
        planned: int = 0,
    ) -> Optional[int]:
        """
        Create a run (suite execution). Returns run_id or None if the server is unreachable.

        ``project`` (default $TRACEREPORTS_PROJECT), ``branch`` and ``commit`` (default: CI variables or
        git) define the context: history, flakiness and comparisons only use runs of the same
        project, environment and branch.
        """
        self._offline_name = name or self._offline_name
        if self.enabled and self.offline_mode == "always" and not self.recording:
            self._go_offline("offline mode")
        if detect_context:
            branch = detect_branch() if branch is None else branch
            commit = detect_commit() if commit is None else commit
        payload = {"name": name, "environment": environment, "project": project or env("PROJECT", ""),
                   "branch": branch or "", "commit": commit or "", "framework": framework,
                   # agrupa en En vivo las corridas de una misma ejecución (un run por archivo, un build de CI)
                   "execution_id": default_execution_id() if execution_id is None else execution_id}
        if planned > 0:
            payload["planned"] = int(planned)
        if self._sender.spool_dir and self.enabled:
            resent = self._sender.load_spool()
            if resent:
                log.info("tracereports: resending %d events saved by a previous run", resent)
        res = self._request("POST", "/api/v1/runs", payload, self.run_timeout, self._run_total())
        if not res and self.enabled and self.offline_mode in ("auto", "both") and not self.recording:
            # sin servidor, caído o con el token equivocado: se graba en vez de perderlo todo; si al
            # final el servidor responde, end_run sube la grabación (un corte breve no la pierde)
            why = self._create_failure()
            self._go_offline(f"could not create the run at {self.base_url} ({why})")
            self._push_at_end = self.recording
            if self.recording:
                # va con el cierre del run (y con la grabación cuando se sube): el servidor lo muestra
                # en el reporte y en Mantenimiento > Conexiones
                self._late = {"late": True, "late_at": int(time.time() * 1000), "reason": why,
                              "detail": getattr(self._sender_before_offline, "last_failure_detail", "")}
            if self.recording:
                res = self._request("POST", "/api/v1/runs", payload, self.run_timeout)
        if res and res.get("run_id", 0) > 0 and self.offline_mode == "both" and not self.recording:
            self._go_mirror(res["run_id"], payload)
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
        if run_id and int(run_id) > 0 and self.enabled and self.offline_mode == "both" and not self.recording:
            self._go_mirror(int(run_id), directory=offline_dir)
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
        if isinstance(self._sender, Mirror) and self._sender.closed:
            return None
        pending = self.flush()
        self._sender.drain_to_spool()
        if isinstance(self._sender, Mirror) and not self.run_created:
            self._sender.close(pending == 0 and self.delivery_problems() == 0 and not interrupted)
            return None
        if not rid:
            return None
        payload = {"interrupted": bool(interrupted)}
        report = self._delivery_report()
        if report:
            payload["delivery"] = report
        res = self._request("PATCH", f"/api/v1/runs/{rid}/finish", payload, self.run_timeout, self._run_total())
        if res is None and self.enabled:
            # el servidor no respondió: el cierre también va al spool (se reenvía con lo demás)
            sender = self._sender.sender if isinstance(self._sender, Mirror) else self._sender
            sender.enqueue("PATCH", f"/api/v1/runs/{rid}/finish", json.dumps(payload).encode(), "application/json", self.run_timeout)
            self._sender.drain_to_spool()
        if isinstance(self._sender, Mirror):
            self._sender.close(pending == 0 and res is not None and self.delivery_problems() == 0 and not interrupted)
            before = self._sender.prepare()
            self._offline_finish()
            self._sender.finalize(self.offline_report, env("OFFLINE_KEEP") == "1", before)
        elif self.recording:
            self._offline_finish()
            if self._push_at_end:
                self._push_recording()
        return res

    def _delivery_report(self) -> Optional[dict]:
        """How this run's evidence arrived, for the server (only when something went wrong): created
        late (recorded locally), retried, rejected, dropped, lost or spooled. The server shows it in
        the run and in Maintenance > Connections."""
        d = self.delivery
        counts = {k: int(d.get(k) or 0) for k in ("retried", "rejected", "dropped", "lost", "spooled")}
        if not self._late and not any(counts.values()):
            return None
        report = {**(self._late or {}), **{k: v for k, v in counts.items() if v}, "client": f"python {__version__}"}
        try:
            report["host"] = socket.gethostname()[:120]
        except OSError:
            pass
        return report

    def _create_failure(self) -> str:
        """Why the run could not be created, in the words of the warning."""
        why = getattr(self._sender, "last_failure", "")
        if why == "timeout":
            return f"the server did not answer within {self._run_total():.0f} s"
        if why == "HTTP 401":
            return "wrong token"
        if why.startswith("HTTP "):
            return f"the server answered {why}"
        return "server down or unreachable"

    def _push_recording(self) -> None:
        """The run could not be created at the start: if the server answers now, upload the recording
        (``tracereports push``, idempotent) so the run reaches the server. TRACEREPORTS_OFFLINE_PUSH=0
        turns it off."""
        self._push_at_end = False
        if env("OFFLINE_PUSH", "1").lower() in ("0", "false", "no", "off") or not self.offline_dir:
            return
        later = f"upload it later with `tracereports push {self.offline_dir}`"
        try:
            with urllib.request.urlopen(self.base_url + "/healthz", timeout=3) as resp:  # noqa: S310 (el servidor configurado)
                up = resp.status == 200
        except (urllib.error.URLError, OSError, ValueError):
            up = False
        if not up:
            log.warning("tracereports: the server still does not answer; %s", later)
            return
        try:
            environ = dict(os.environ, TRACEREPORTS_URL=self.base_url)
            if self.token:
                environ["TRACEREPORTS_TOKEN"] = self.token
            out = subprocess.run([resolve_binary(), "push", self.offline_dir], env=environ, check=True,
                                 timeout=600, capture_output=True, text=True)
        except Exception as err:  # noqa: BLE001 (binario ausente, rechazo, timeout: la grabación queda)
            detail = (getattr(err, "stderr", "") or str(err)).strip().splitlines()
            log.warning("tracereports: could not upload the recording (%s); %s", detail[-1] if detail else err, later)
            return
        urls = [line.split("Report: ", 1)[1] for line in out.stdout.splitlines() if line.startswith("Report: ")]
        self.pushed_report_url = urls[0] if urls else ""
        log.warning("tracereports: the server answered again; the recording was uploaded: %s",
                    self.pushed_report_url or self.offline_dir)

    def download_report(self, dest_dir: str = ".", run_id: Optional[int] = None, timeout: float = 60.0) -> Optional[str]:
        """
        Download the run as a self-contained ZIP (open index.html, no server or internet needed) —
        e.g. to attach it to an email from CI. Call after end_run(). Returns the ZIP path or None.
        """
        rid = run_id or self.run_id
        if self.recording and rid and rid < 0:
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
        if self.enabled and (not self._local.test_id or (isinstance(self._sender, Mirror) and self._local.test_id < 0)):
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
            status = status or "FAIL"
        if self.dom_steps_mode == "on-problem":
            self._flush_dom_buffer(tid, status or "")
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
        if status.upper() in ("FAIL", "WARNING"):
            self._dom_problem.add(tid)  # TRACEREPORTS_DOM_STEPS=on-problem: sus páginas se mandan
        if status.upper() == "WARNING" and self.dom_on_warning:
            self._dom_step(tid, str(message), warning=True)  # TRACEREPORTS_DOM_ON=warning

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
        if status.upper() in ("FAIL", "WARNING"):
            self._dom_problem.add(tid)
        if status.upper() != "FAIL":  # la página del fallo la toma el plugin aparte
            self._dom_step(tid, message, warning=status.upper() == "WARNING")
        # TRACEREPORTS_SCREENSHOTS=off|failures: la captura ni se lee ni se envía (ver mask.py)
        if not screenshot_allowed(status):
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
        for body in network_batches(payload, max_count=batch_size):
            self._emit_raw("POST", f"/api/v1/tests/{tid}/network", body, "application/json", max(self.upload_timeout, 10.0))
        return {"stored": len(payload), "errors": errors}

    def attach_dom(self, snapshot: Optional[dict], test_id: Optional[int] = None, kind: str = "", label: str = "") -> bool:
        """
        Sube un snapshot de la página (ver tracereports.capturar_dom). El del fallo (por defecto)
        permite recomendar selectores y, con el nivel ``structure``, redibujar la página en el
        Inspector; ``kind="step"`` con ``label`` es la página de un paso. Enviar ANTES de end_test().
        """
        tid = test_id or self.current_test_id
        if not tid or not snapshot or not self.enabled:
            return False
        if kind:
            snapshot["kind"] = kind
        if label:
            snapshot["label"] = str(label)[:300]
        html = snapshot.get("html")
        if isinstance(html, dict) and html.get("sheets"):
            # cada hoja de estilo viaja una vez por proceso; después basta su hash
            sent = self.__dict__.setdefault("_css_sent", set())
            html["sheets"] = {h: t for h, t in html["sheets"].items() if h not in sent}
            sent.update(html["sheets"])
        body = json.dumps(snapshot).encode()
        return self._emit_raw("POST", f"/api/v1/tests/{tid}/dom", body, "application/json", max(self.upload_timeout, 10.0))

    def _dom_step(self, test_id: int, label: str, warning: bool = False) -> None:
        """With TRACEREPORTS_DOM_STEPS, the page of this step (once per distinct page); with
        TRACEREPORTS_DOM_ON=warning, the page of a WARNING step too."""
        source = self.dom_step_source
        if not source or not (self.dom_steps or (warning and self.dom_on_warning)):
            return
        try:
            snap = source()
        except Exception:  # la evidencia nunca rompe el test
            return
        if not snap:
            return
        kind = "warning" if warning and self.dom_on_warning else "step"
        sig = dom_signature(snap)
        if self._dom_step_sig.get((test_id, kind)) == sig:
            return  # la misma pantalla que el paso anterior
        self._dom_step_sig[(test_id, kind)] = sig
        if self.dom_steps_mode == "on-problem":
            buf = self._dom_buffer.setdefault(test_id, [])
            if len(buf) < MAX_DOM_BUFFER:  # el servidor guarda hasta 40 por test
                buf.append((kind, label, snap))
            return
        self.attach_dom(snap, test_id=test_id, kind=kind, label=label)

    def _flush_dom_buffer(self, test_id: int, status: str) -> None:
        """TRACEREPORTS_DOM_STEPS=on-problem, al terminar el test: sus páginas van si falló o tuvo un
        WARNING. Si pasó, van solo como base de "Comparar con el último PASS" cuando el servidor no tiene
        una de ese test de los últimos 7 días; un servidor sin esa función (404) no recibe nada."""
        buf = self._dom_buffer.pop(test_id, None)
        problem = (status or "").upper() in ("FAIL", "WARNING") or test_id in self._dom_problem
        self._dom_problem.discard(test_id)
        if not buf:
            return
        if not problem:
            base = self._best_effort("GET", f"/api/v1/tests/{test_id}/dom/baseline", None)
            if not isinstance(base, dict):
                return  # sin servidor, servidor anterior o error: no se manda nada
            started = base.get("started_at") or 0
            if started and time.time() * 1000 - started < BASELINE_MAX_AGE_MS:
                return  # ya hay un PASS reciente para comparar
        for kind, label, snap in buf:
            self.attach_dom(snap, test_id=test_id, kind=kind, label=label)

    # ------------------------------------------------------ En vivo (servidor)

    def set_planned(self, planned: int) -> None:
        """How many tests this run will execute (progress and ETA in Live). Best effort: a server
        without Live ignores it and nothing is counted as lost evidence."""
        if planned > 0:
            self._best_effort("POST", f"/api/v1/runs/{self.run_id}/plan", {"planned": int(planned)})

    def cancel_requested(self, every: float = 5.0) -> "tuple[bool, str]":
        """With TRACEREPORTS_REMOTE_CONTROL=1: whether someone asked from TraceReports to cancel this
        run (Live → Cancel execution), and why. Asks at most every ``every`` seconds; a server
        without the endpoint, or no server, never cancels. Nothing is killed: the caller skips."""
        c = self._control
        if c["cancel"]:
            return True, c["reason"]
        if not self.remote_control or c["off"] or time.monotonic() - c["checked"] < every:
            return False, ""
        c["checked"] = time.monotonic()
        res = self._best_effort("GET", f"/api/v1/runs/{self.run_id}/control", None)
        if res is None:
            return False, ""
        if res == "missing":
            c["off"] = True  # un servidor sin control remoto: no se vuelve a preguntar
            return False, ""
        if res.get("cancel"):
            c["cancel"], c["reason"] = True, str(res.get("reason") or "")
        return c["cancel"], c["reason"]

    def _best_effort(self, method: str, path: str, payload: Optional[dict], timeout: float = 3.0):
        """A direct call to the server, outside the evidence queue: never recorded offline, never
        counted as lost. Returns the JSON, "missing" for a 404/405 (an older server) or None."""
        if not (self.enabled and self.run_id and self.run_id > 0 and not self._sender.server_down()):
            return None
        try:
            body = None if payload is None else json.dumps(payload).encode()
            headers = dict(self._headers())
            if body is not None:
                headers["Content-Type"] = "application/json"
            req = urllib.request.Request(self.base_url + path, data=body, method=method, headers=headers)
            with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310 (la URL es la del servidor configurado)
                return json.loads(resp.read() or b"{}")
        except urllib.error.HTTPError as err:
            return "missing" if err.code in (404, 405) else None
        except Exception:  # la evidencia (y el control) nunca rompen el test
            return None

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

    def _run_total(self) -> float:
        """Total time to create or close the run, retries included (never less than one attempt)."""
        return max(RUN_TOTAL_SECONDS, self.run_timeout)

    def _request(self, method: str, path: str, payload: dict, timeout: Optional[float] = None,
                 total: Optional[float] = None) -> Optional[dict]:
        """Synchronous call whose answer is needed (run/test ids, closing the run)."""
        if not self.enabled:
            return None
        return self._sender.send_now(method, path, json.dumps(payload).encode(), "application/json",
                                     self.timeout if timeout is None else timeout, total=total)

    def _emit(self, method: str, path: str, payload: dict) -> bool:
        return self._emit_raw(method, path, json.dumps(payload).encode(), "application/json", max(self.timeout, 5.0))

    def _emit_raw(self, method: str, path: str, body: bytes, content_type: str, timeout: float) -> bool:
        """Evidence: queued (default) or sent inline with TRACEREPORTS_SYNC=1."""
        if not self.enabled:
            return False
        if self.async_send:
            return self._sender.enqueue(method, path, body, content_type, timeout)
        return self._sender.send_now(method, path, body, content_type, timeout) is not None


# Lotes de red: el servidor acepta hasta 48 MiB por request. Cada lote se arma por los bytes del
# JSON que realmente se envía, hasta NETWORK_BATCH_BYTES y batch_size conexiones. Una conexión más
# grande va sola; si sola pasa NETWORK_ALONE_BYTES, se envía sin bodies ni headers (body_truncated,
# con su body_size original) para que siempre entre.
NETWORK_BATCH_BYTES = 2 << 20
NETWORK_ALONE_BYTES = 40 << 20


def _slim_connection(c: dict) -> dict:
    out = dict(c)
    for k, n in (("url", 4096), ("status_text", 256), ("mime_type", 256), ("resource_type", 32),
                 ("error_text", 2048), ("method", 16), ("evidence_file", 1024)):
        if isinstance(out.get(k), str):
            out[k] = out[k][:n]
    out.update(request_headers={}, response_headers={}, post_data="", response_body="", body_truncated=True)
    return out


def network_batches(payload: list, max_count: int = 200, max_bytes: int = NETWORK_BATCH_BYTES,
                    alone_bytes: int = NETWORK_ALONE_BYTES) -> "list[bytes]":
    """Bodies {"connections": [...]} de a lo sumo max_bytes y max_count conexiones, en orden."""
    head, tail = b'{"connections": [', b"]}"
    out: "list[bytes]" = []
    parts: "list[bytes]" = []
    size = 0
    for c in payload:
        raw = json.dumps(c).encode()
        if len(raw) + len(head) + len(tail) > alone_bytes:
            raw = json.dumps(_slim_connection(c)).encode()
        if parts and (len(parts) >= max_count or size + 2 + len(raw) > max_bytes):
            out.append(head + b", ".join(parts) + tail)
            parts = []
        if not parts:
            size = len(head) + len(tail)
        size += (2 if parts else 0) + len(raw)
        parts.append(raw)
    if parts:
        out.append(head + b", ".join(parts) + tail)
    return out


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
        # the capture may already have cut the body: keep its flag and the original size (UTF-8
        # bytes, like the server). A size larger than the body is not proof of a cut: masking
        # shortens the text too
        "body_size": max(int(c.get("body_size") or 0), len(body.encode("utf-8", "surrogatepass"))),
        "body_truncated": bool(c.get("body_truncated")) or len(body) > body_limit,
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
