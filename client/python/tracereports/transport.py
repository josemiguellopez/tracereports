"""
Transporte del cliente: la evidencia sale en segundo plano, sin frenar los tests.

- Cola acotada (por cantidad y por bytes) que un hilo envía en orden (FIFO).
- Reintentos con backoff exponencial ante caídas de red, timeouts, 5xx, 408 y 429. Cada envío
  lleva una clave ``Idempotency-Key``: si el servidor ya lo aplicó, el reintento no lo duplica.
- Circuito: si el servidor no responde, las llamadas síncronas (crear ejecución/test) fallan al
  instante durante un rato en vez de esperar su timeout en cada test.
- Al cerrar, ``flush`` espera como máximo un tiempo; lo que no se pudo enviar se guarda en una
  cola local en disco (spool) si se configuró una carpeta, o se informa como perdido.

Cola llena: los eventos nuevos se descartan (se conservan los anteriores, para no romper el
orden de la evidencia ya encolada) y se cuentan en ``stats["dropped"]``.
"""

from __future__ import annotations

import base64
import collections
import glob
import itertools
import json
import logging
import os
import threading
import time
import urllib.error
import urllib.request
import uuid
from typing import Callable, Optional

log = logging.getLogger("tracereports")

RETRYABLE_STATUS = {408, 425, 429, 500, 502, 503, 504}
CIRCUIT_SECONDS = 30.0
MAX_BACKOFF = 30.0
SPOOL_CHUNK_ITEMS = 200          # eventos por archivo de spool
SPOOL_CHUNK_BYTES = 8 << 20      # bytes por archivo de spool
SPOOL_STALE_SECONDS = 600.0      # un archivo reclamado que nadie toca en este tiempo se considera abandonado
SPOOL_HEARTBEAT_SECONDS = 60.0   # cada cuánto el dueño renueva la posesión de sus archivos reclamados


class _SpoolFile:
    """Archivo de spool reclamado: se borra cuando ya no le quedan eventos sin destino."""
    __slots__ = ("path", "remaining", "touched")

    def __init__(self, path, remaining):
        self.path, self.remaining = path, remaining
        self.touched = time.time()


class _Item:
    __slots__ = ("method", "path", "body", "content_type", "timeout", "key", "attempts", "abandoned", "fate", "src")

    def __init__(self, method, path, body, content_type, timeout, key=None):
        self.method, self.path, self.body = method, path, body
        self.content_type, self.timeout = content_type, timeout
        self.key = key or uuid.uuid4().hex
        self.attempts = 0
        self.abandoned = False  # drain_to_spool se lo llevó: el hilo ya no decide su destino
        self.fate = ""          # "spooled" | "lost" cuando se lo llevó el drenado
        self.src: "Optional[_SpoolFile]" = None

    def to_json(self) -> str:
        return json.dumps({"method": self.method, "path": self.path, "content_type": self.content_type,
                           "timeout": self.timeout, "key": self.key, "body": base64.b64encode(self.body).decode()})

    @classmethod
    def from_json(cls, line: str) -> "_Item":
        d = json.loads(line)
        return cls(d["method"], d["path"], base64.b64decode(d["body"]), d["content_type"], d.get("timeout", 10.0), d["key"])


class Sender:
    """Envía requests HTTP al servidor: síncronas (con reintentos cortos) o encoladas."""

    def __init__(self, base_url: str, headers: Callable[[dict], dict], max_items: int = 5000,
                 max_bytes: int = 64 << 20, spool_dir: Optional[str] = None) -> None:
        self.base_url = base_url
        self._headers = headers
        self.max_items, self.max_bytes = max_items, max_bytes
        self.spool_dir = spool_dir
        self._q: "collections.deque[_Item]" = collections.deque()  # eventos esperando (no el en vuelo)
        self._bytes = 0  # bytes de lo que espera + el evento en vuelo
        self._cv = threading.Condition()
        self._inflight: "Optional[_Item]" = None  # evento que el hilo está enviando
        self._claims: "set[_SpoolFile]" = set()  # archivos de spool reclamados con eventos pendientes
        self._reserved_items = 0  # lugar reservado por load_spool mientras divide un archivo
        self._reserved_bytes = 0
        self.spool_report: dict = {"unreadable": [], "busy": 0}
        self._thread: Optional[threading.Thread] = None
        self._down_until = 0.0
        self.stats = {"sent": 0, "retried": 0, "rejected": 0, "dropped": 0, "spooled": 0, "lost": 0}

    # ------------------------------------------------------------- estado
    @property
    def pending(self) -> int:
        with self._cv:
            busy = self._inflight is not None and not self._inflight.abandoned
            return len(self._q) + (1 if busy else 0)

    def server_down(self) -> bool:
        return time.time() < self._down_until

    def _count(self, key: str, n: int = 1) -> None:
        with self._cv:
            self.stats[key] += n

    # ---------------------------------------------------------- un request
    def _post(self, item: _Item) -> "tuple[str, Optional[dict]]":
        """Envía una vez. Devuelve ("ok", json) | ("retry", None) | ("reject", None)."""
        req = urllib.request.Request(
            self.base_url + item.path, data=item.body, method=item.method,
            headers=self._headers({"Content-Type": item.content_type, "Accept": "application/json",
                                   "Idempotency-Key": item.key}),
        )
        try:
            with urllib.request.urlopen(req, timeout=item.timeout) as resp:
                raw = resp.read()
            self._down_until = 0.0
            return "ok", (json.loads(raw) if raw else {})
        except urllib.error.HTTPError as err:
            detail = err.read().decode(errors="replace")[:300]
            if err.code in RETRYABLE_STATUS:
                log.debug("tracereports: %s %s -> HTTP %s (se reintenta)", item.method, item.path, err.code)
                return "retry", None
            if err.code == 401:
                detail += (" (configura el token: TraceReports(token=...), la variable TRACEREPORTS_TOKEN"
                           " o TRACEREPORTS_TOKEN en el .env del proyecto)")
            log.warning("tracereports: %s %s -> HTTP %s %s", item.method, item.path, err.code, detail)
            return "reject", None
        except (urllib.error.URLError, OSError, ValueError) as err:
            if not self.server_down():
                log.warning("tracereports: %s %s falló: %s", item.method, item.path, err)
            self._down_until = time.time() + CIRCUIT_SECONDS
            return "retry", None

    def send_now(self, method: str, path: str, body: bytes, content_type: str, timeout: float,
                 retries: int = 2) -> Optional[dict]:
        """Request síncrona (se necesita su respuesta: ids de ejecución o test) con reintentos
        cortos e idempotencia. Con el circuito abierto falla al instante."""
        if self.server_down():
            return None
        item = _Item(method, path, body, content_type, timeout)
        for attempt in range(retries + 1):
            kind, res = self._post(item)
            if kind == "ok":
                self._count("sent")
                return res
            if kind == "reject" or self.server_down() or attempt == retries:
                break
            self._count("retried")
            time.sleep((0.3, 1.0, 2.0)[min(attempt, 2)])
        return None

    # --------------------------------------------------------------- cola
    #
    # Dueño de cada evento: o está esperando en ``_q``, o lo tiene el hilo en ``_inflight``. Solo el
    # hilo saca de ``_q`` (popleft) y solo él decide el destino del evento en vuelo. ``drain_to_spool``
    # se lleva lo que espera y marca el evento en vuelo como ``abandoned``: si después se confirma,
    # queda además en el spool (reenviarlo no duplica nada: misma Idempotency-Key); si falla, ya está
    # a salvo en el spool. Así ningún evento desaparece sin quedar enviado, en disco o contado como perdido.

    def _free(self) -> "tuple[int, int]":
        """Espacio libre de la cola (con el lock tomado), descontando lo reservado por cargas del
        spool en curso: así ni enqueue() ni otra carga simultánea usan el mismo lugar."""
        busy = 1 if self._inflight is not None and not self._inflight.abandoned else 0
        items = self.max_items - len(self._q) - busy - self._reserved_items
        return items, self.max_bytes - self._bytes - self._reserved_bytes

    def _fits(self, n: int, size: int) -> bool:
        free_items, free_bytes = self._free()
        return n <= free_items and size <= free_bytes

    def enqueue(self, method: str, path: str, body: bytes, content_type: str, timeout: float) -> bool:
        item = _Item(method, path, body, content_type, timeout)
        with self._cv:
            if not self._fits(1, len(body)):
                self.stats["dropped"] += 1
                if self.stats["dropped"] == 1:
                    log.warning("tracereports: la cola de envío está llena (%d eventos / %d MB); se descartan "
                                "los eventos nuevos hasta que el servidor responda", self.max_items, self.max_bytes >> 20)
                return False
            self._q.append(item)
            self._bytes += len(body)
            self._cv.notify_all()
        self._ensure_thread()
        return True

    def _ensure_thread(self) -> None:
        if self._thread is None or not self._thread.is_alive():
            self._thread = threading.Thread(target=self._run, name="tracereports-sender", daemon=True)
            self._thread.start()

    def _run(self) -> None:
        while True:
            with self._cv:
                while not self._q:
                    self._cv.wait()
                item = self._q.popleft()
                self._inflight = item
            try:
                kind, _ = self._post(item)
            except Exception as err:  # noqa: BLE001 - el hilo nunca muere: se reintenta
                log.debug("tracereports: error inesperado al enviar %s: %s", item.path, err)
                kind = "retry"
            delay = 0.0
            with self._cv:
                self._inflight = None
                if self._claims:
                    self._heartbeat()
                if kind == "retry":
                    if not item.abandoned:  # si se fue al spool, ya está a salvo
                        item.attempts += 1
                        self.stats["retried"] += 1
                        self._q.appendleft(item)
                        delay = min(MAX_BACKOFF, 0.5 * (2 ** min(item.attempts, 6)))
                else:
                    if not item.abandoned:
                        self._bytes -= len(item.body)
                    elif item.fate == "lost" and kind == "ok":
                        self.stats["lost"] -= 1  # se contó como perdido, pero llegó
                    self.stats["sent" if kind == "ok" else "rejected"] += 1
                    self._settle(item)
                self._cv.notify_all()
            if delay:
                time.sleep(delay)

    def flush(self, timeout: float) -> int:
        """Espera a que la cola se vacíe (máx. ``timeout`` s). Devuelve lo que quedó pendiente."""
        deadline = time.time() + max(timeout, 0)
        with self._cv:
            while (self._q or self._inflight is not None) and time.time() < deadline:
                self._cv.wait(timeout=min(0.2, max(deadline - time.time(), 0.01)))
                if self._claims:
                    self._heartbeat()
            return len(self._q) + (1 if self._inflight is not None and not self._inflight.abandoned else 0)

    # ----------------------------------------------------- spool (en disco)
    #
    # Nombre: tracereports_spool_<ms:13>_<seq:6>_<pid>-<uuid>_<parte>.jsonl. El uuid hace único cada
    # guardado (dos drenados en el mismo milisegundo nunca se pisan) y <ms>_<seq> da el orden de
    # reenvío. Un archivo se *reclama* tocándolo y renombrándolo a "<nombre>.claimed-<pid>-<id>"; el
    # dueño lo vuelve a tocar mientras le queden eventos (SPOOL_HEARTBEAT_SECONDS), así que solo se
    # considera abandonado si nadie lo tocó en SPOOL_STALE_SECONDS. Se borra cuando todos sus eventos
    # se confirmaron o pasaron a un spool nuevo.

    def _settle(self, item: "_Item") -> None:
        """El evento ya no depende de su archivo de spool de origen: si era el último, se borra."""
        src = item.src
        item.src = None
        if src is None:
            return
        src.remaining -= 1
        if src.remaining <= 0:
            self._claims.discard(src)
            try:
                os.remove(src.path)
            except OSError:
                pass

    def _heartbeat(self) -> None:
        """Renueva la posesión de los archivos reclamados que siguen con eventos pendientes."""
        now = time.time()
        for src in list(self._claims):
            if now - src.touched >= SPOOL_HEARTBEAT_SECONDS:
                try:
                    os.utime(src.path)
                    src.touched = now
                except OSError:
                    pass

    @staticmethod
    def _spool_name(ms: int, seq: int, part: str) -> str:
        return f"tracereports_spool_{ms:013d}_{seq:06d}_{os.getpid()}-{uuid.uuid4().hex[:12]}_{part}.jsonl"

    def _write_file(self, path: str, items: "list[_Item]") -> None:
        """Escritura atómica que nunca reemplaza otra evidencia: si el nombre existe, se elige otro."""
        tmp = f"{path}.tmp-{uuid.uuid4().hex}"
        with open(tmp, "w", encoding="utf-8") as fh:
            for it in items:
                fh.write(it.to_json() + "\n")
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)

    def _new_spool_path(self, ms: int, seq: int, part: str) -> str:
        while True:
            path = os.path.join(self.spool_dir, self._spool_name(ms, seq, part))
            if not os.path.exists(path):
                return path

    def _write_spool(self, items: "list[_Item]") -> bool:
        """Escribe los eventos en archivos de hasta SPOOL_CHUNK_ITEMS / SPOOL_CHUNK_BYTES."""
        os.makedirs(self.spool_dir, exist_ok=True)
        ms, seq = int(time.time() * 1000), next(_SPOOL_SEQ)
        chunk: "list[_Item]" = []
        size = n = 0
        for it in items + [None]:
            if it is None or (chunk and (len(chunk) >= SPOOL_CHUNK_ITEMS or size + len(it.body) > SPOOL_CHUNK_BYTES)):
                if chunk:
                    self._write_file(self._new_spool_path(ms, seq, f"{n:04d}"), chunk)
                    n += 1
                chunk, size = [], 0
            if it is not None:
                chunk.append(it)
                size += len(it.body)
        return True

    def drain_to_spool(self) -> int:
        """Saca de la cola lo pendiente (también el evento en vuelo): al spool si hay carpeta, si no
        lo cuenta como perdido. El archivo de spool de origen se borra solo cuando sus eventos ya
        están en el spool nuevo."""
        with self._cv:
            items = list(self._q)
            self._q.clear()
            if self._inflight is not None and not self._inflight.abandoned:
                items.insert(0, self._inflight)
            for it in items:
                it.abandoned = True
            self._bytes = 0
        if not items:
            return 0
        if self.spool_dir:
            try:
                self._write_spool(items)
                with self._cv:
                    self.stats["spooled"] += len(items)
                    for it in items:
                        it.fate = "spooled"
                        self._settle(it)  # ya está en el spool nuevo: el de origen puede irse
                log.warning("tracereports: %d eventos sin enviar quedaron en %s (reenvío: python -m tracereports.resend %s)",
                            len(items), self.spool_dir, self.spool_dir)
                return len(items)
            except OSError as err:
                log.warning("tracereports: no se pudo escribir la cola local %s: %s", self.spool_dir, err)
        with self._cv:
            self.stats["lost"] += len(items)
            for it in items:
                it.fate = "lost"
        log.warning("tracereports: %d eventos de evidencia no se pudieron enviar y se perdieron "
                    "(configura TRACEREPORTS_SPOOL_DIR para guardarlos y reenviarlos después)", len(items))
        return len(items)

    def load_spool(self, spool_dir: Optional[str] = None) -> int:
        """
        Encola eventos guardados en el spool, en orden y mientras quepan en la cola. Un archivo más
        grande que el espacio libre se carga por partes: lo que no entra se guarda primero en un
        archivo nuevo (que conserva su lugar en el orden) y recién después se acota el reclamado.
        ``self.spool_report`` dice qué quedó sin procesar: ilegibles y archivos de otro proceso.
        """
        folder = spool_dir or self.spool_dir
        self.spool_report = {"unreadable": [], "busy": 0}
        if not folder:
            return 0
        n = 0
        for path in _spool_candidates(folder):
            name = os.path.basename(path)
            if ".claimed-" in name:
                try:
                    if time.time() - os.path.getmtime(path) <= SPOOL_STALE_SECONDS:
                        self.spool_report["busy"] += 1  # su dueño está vivo
                        continue
                except OSError:
                    continue
            with self._cv:
                if self._free()[0] <= 0:  # sin lugar: no vale la pena reclamar
                    break
            base = path.split(".claimed-")[0]
            claimed = f"{base}.claimed-{os.getpid()}-{uuid.uuid4().hex[:6]}"
            try:
                os.utime(path)              # vivo antes de reclamar: nadie lo ve abandonado en la ventana
                os.replace(path, claimed)   # reclamar: si otro lo tomó primero, falla y se sigue
            except OSError:
                continue
            try:
                with open(claimed, encoding="utf-8") as fh:
                    items = [_Item.from_json(line) for line in fh if line.strip()]
            except (OSError, ValueError, KeyError) as err:
                log.warning("tracereports: spool ilegible %s: %s", base, err)
                bad = f"{base}.unreadable"
                try:
                    os.replace(claimed, bad)
                except OSError:
                    bad = claimed
                self.spool_report["unreadable"].append(bad)
                continue
            if not items:
                os.remove(claimed)
                continue
            # Lugar real AHORA (pudieron entrar eventos mientras se leía el archivo), reservado en el
            # mismo bloque: hasta incorporarlos, nadie más puede ocuparlo.
            with self._cv:
                free_items, free_bytes = self._free()
                k = size = 0
                while k < len(items) and k < free_items and size + len(items[k].body) <= free_bytes:
                    size += len(items[k].body)
                    k += 1
                self._reserved_items += k
                self._reserved_bytes += size
            if k == 0:  # ni un evento entra ahora: se devuelve intacto
                try:
                    os.replace(claimed, base)
                except OSError:
                    pass
                break
            if k < len(items):
                # primero el resto en un archivo propio (durable), después se acota el reclamado:
                # si el proceso muere entre medio, a lo sumo se reenvía algo dos veces (Idempotency-Key)
                ms, seq = _sort_key(os.path.basename(base))[:2]
                try:
                    os.makedirs(folder, exist_ok=True)
                    rest = os.path.join(folder, self._spool_name(ms, max(seq, 0), f"{_part_of(base)}-r"))
                    self._write_file(rest, items[k:])
                    self._write_file(claimed, items[:k])
                except OSError as err:
                    log.warning("tracereports: no se pudo dividir el spool %s: %s", base, err)
                    with self._cv:
                        self._reserved_items -= k
                        self._reserved_bytes -= size
                    try:
                        os.replace(claimed, base)
                    except OSError:
                        pass
                    break
                items = items[:k]
            src = _SpoolFile(claimed, len(items))
            with self._cv:
                self._reserved_items -= k   # la reserva se convierte en lugar ocupado
                self._reserved_bytes -= size
                for it in items:
                    it.src = src
                    self._q.append(it)
                self._bytes += size
                self._claims.add(src)
                self._cv.notify_all()
            n += len(items)
        if n:
            self._ensure_thread()
        return n


_SPOOL_SEQ = itertools.count()
SPOOL_PREFIX = "tracereports_spool"


def _sort_key(name: str) -> "tuple[int, int, str, str]":
    """Orden de reenvío: (milisegundos, secuencia, parte, nombre). La parte ("0000", "0001",
    "0001-r" para el resto de un archivo dividido) ordena los archivos de un mismo guardado; el
    identificador aleatorio solo desempata."""
    parts = name.split(".")[0].split("_")   # tracereports_spool_<ms>_<seq>_<pid-uuid>_<parte>
    try:
        return int(parts[2]), int(parts[3]), parts[5], name
    except (IndexError, ValueError):
        return 0, -1, "", name


def _part_of(base: str) -> str:
    return os.path.basename(base).split(".")[0].split("_")[-1]


def _spool_candidates(folder: str) -> "list[str]":
    paths = [p for p in glob.glob(os.path.join(folder, f"{SPOOL_PREFIX}_*.jsonl*"))
             if p.endswith(".jsonl") or ".jsonl.claimed-" in p]
    return sorted(paths, key=lambda p: _sort_key(os.path.basename(p)))


def spool_files(folder: str) -> "dict[str, int]":
    """Cuántos archivos quedan en un spool: pendientes, reclamados por algún proceso e ilegibles."""
    out = {"pending": 0, "claimed": 0, "unreadable": 0}
    for p in glob.glob(os.path.join(folder, f"{SPOOL_PREFIX}_*")):
        if ".claimed-" in p:
            out["claimed"] += 1
        elif p.endswith(".unreadable"):
            out["unreadable"] += 1
        elif p.endswith(".jsonl"):
            out["pending"] += 1
    return out
