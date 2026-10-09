"""
Grabación local cuando no hay servidor: sin URL configurada, el servidor no responde o rechaza el
token. En vez de perder la evidencia, el cliente escribe en una carpeta las mismas llamadas a la
API que habría hecho; después:

    tracereports report ./tracereports-offline/<sesión> -o reporte     # HTML estático, sin servidor
    tracereports push ./tracereports-offline/<sesión> --token <token>  # subirla al servidor

Formato (lo lee el binario, ver internal/offline): un marcador ``tracereports-offline.json``,
un ``events-<pid>-<id>.jsonl`` por proceso (los workers de pytest-xdist escriben cada uno el suyo),
``bodies/`` para lo que no es JSON (capturas) e ``ids/`` con los bloques de ids que reservó cada
grabador. Los ids de ejecución y test son negativos y locales; el binario los cambia por los reales
al reproducir.
"""

from __future__ import annotations

import json
import os
import re
import secrets
import threading
import time
import uuid
import unicodedata
from typing import Optional

FORMAT_VERSION = 1
MARKER = "tracereports-offline.json"

# Ids locales: cada grabador reserva bloques de ID_BLOCK ids creando <carpeta>/ids/<bloque> en
# exclusiva, así dos grabadores que comparten la carpeta (mismo proceso, otro proceso u otra sesión)
# nunca repiten un id. El bloque es aleatorio en [MIN_BLOCK, MAX_BLOCK): no choca con los ids de
# grabaciones anteriores (pid % 10^6) y -(bloque * ID_BLOCK + n) es exacto en JavaScript (< 2^53).
ID_BLOCK = 100_000
MIN_BLOCK = 1_000_000
MAX_BLOCK = 90_000_000_000


def session_slug(name: str) -> str:
    """Filesystem-safe label shared by the local recording folders in every client."""
    text = unicodedata.normalize("NFKD", name or "").encode("ascii", "ignore").decode("ascii").lower()
    text = re.sub(r"[^a-z0-9]+", "-", text).strip("-")
    return (text[:40].rstrip("-") or "run")


def new_session_dir(base: str, name: str = "") -> str:
    """Carpeta única para una corrida dentro de ``base``."""
    return os.path.join(base, f"{session_slug(name)}-{time.strftime('%Y%m%d-%H%M%S')}-{uuid.uuid4().hex[:6]}")


class Recorder:
    """Reemplaza al Sender: graba cada llamada en vez de enviarla y responde con ids locales.
    Tiene la misma interfaz que usa TraceReports (send_now, enqueue, flush, stats...)."""

    def __init__(self, directory: str) -> None:
        self.directory = directory
        self.spool_dir = None  # nada que reenviar: todo queda en la grabación
        self.stats = {"sent": 0, "retried": 0, "rejected": 0, "dropped": 0, "spooled": 0, "lost": 0, "recorded": 0}
        self._lock = threading.Lock()
        self._seq = 0
        self._next_id = 0
        self._block = 0
        self._pid = os.getpid()
        self._tag = uuid.uuid4().hex[:8]
        os.makedirs(os.path.join(directory, "bodies"), exist_ok=True)
        marker = os.path.join(directory, MARKER)
        if not os.path.exists(marker):
            try:
                with open(marker, "x", encoding="utf-8") as f:  # el primer proceso lo crea
                    json.dump({"format": "tracereports-offline", "version": FORMAT_VERSION,
                               "id": uuid.uuid4().hex}, f)
            except FileExistsError:
                pass
        self._events = open(os.path.join(directory, f"events-{self._pid}-{self._tag}.jsonl"), "a", encoding="utf-8")

    # ids locales negativos, únicos entre los grabadores que graban en la misma carpeta
    def _local_id(self) -> int:
        if not self._block or self._next_id >= ID_BLOCK - 1:
            self._block = self._reserve_block()
            self._next_id = 0
        self._next_id += 1
        return -(self._block * ID_BLOCK + self._next_id)

    def _reserve_block(self) -> int:
        ids = os.path.join(self.directory, "ids")
        os.makedirs(ids, exist_ok=True)
        for _ in range(100):
            block = MIN_BLOCK + secrets.randbelow(MAX_BLOCK - MIN_BLOCK)
            try:
                with open(os.path.join(ids, str(block)), "x"):
                    return block
            except FileExistsError:
                continue  # ya es de otro grabador: se prueba otro
        raise OSError(f"tracereports: could not reserve local ids in {ids}")

    def _record(self, method: str, path: str, body: bytes, content_type: str) -> dict:
        with self._lock:
            self._seq += 1
            event = {"seq": self._seq, "ts": int(time.time() * 1000), "method": method, "path": path,
                     "content_type": content_type}
            parsed = None
            if content_type.startswith("application/json"):
                try:
                    parsed = json.loads(body or b"{}")
                except ValueError:
                    parsed = None
            if parsed is not None:
                event["body"] = parsed
            else:
                name = f"bodies/{self._pid}-{self._tag}-{self._seq}.bin"
                with open(os.path.join(self.directory, name), "wb") as f:
                    f.write(body)
                event["body_file"] = name
            res: dict = {}
            if method == "POST" and path == "/api/v1/runs":
                res = {"run_id": self._local_id()}
            elif method == "POST" and path.endswith("/tests") and path.startswith("/api/v1/runs/"):
                res = {"test_id": self._local_id()}
            if res:
                event["local_id"] = next(iter(res.values()))
            self._events.write(json.dumps(event, ensure_ascii=False) + "\n")
            self._events.flush()  # si el proceso muere, lo escrito queda
            self.stats["recorded"] += 1
            return res

    # ---- interfaz del Sender
    def send_now(self, method: str, path: str, body: bytes, content_type: str, timeout: float,
                 retries: int = 2) -> Optional[dict]:
        return self._record(method, path, body, content_type)

    def enqueue(self, method: str, path: str, body: bytes, content_type: str, timeout: float) -> bool:
        self._record(method, path, body, content_type)
        return True

    def flush(self, timeout: float = 0) -> int:
        return 0

    @property
    def pending(self) -> int:
        return 0

    def server_down(self) -> bool:
        return False

    def load_spool(self) -> int:
        return 0

    def drain_to_spool(self) -> int:
        return 0

    def close(self) -> None:
        with self._lock:
            if not self._events.closed:
                self._events.close()
