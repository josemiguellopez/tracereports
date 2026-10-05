"""
Variables de entorno del cliente: TRACEREPORTS_<NOMBRE>.

Si la variable no está en el entorno, se busca en el archivo .env del proyecto (el mismo que lee el
servidor): así el token vive en un solo lugar, ignorado por git, y no hace falta definirlo en cada
terminal ni de forma global en el sistema. Las variables del entorno siempre ganan (en CI llegan
como secrets). Del archivo solo se usan las claves TRACEREPORTS_*.

El .env se busca desde la carpeta actual hacia arriba, hasta la raíz del repositorio (la carpeta
con .git). TRACEREPORTS_ENV_FILE indica otro archivo, u "off" para no leer ninguno.
"""

import os
from typing import Dict, Optional

PREFIX = "TRACEREPORTS_"
_OFF = "off"

_file_values: Optional[Dict[str, str]] = None
_file_path: Optional[str] = None


def parse_env_file(path: str) -> Dict[str, str]:
    """Líneas KEY=VALUE con las mismas reglas que el servidor: comentarios "#", prefijo "export ",
    comillas alrededor del valor y comentario al final de la línea (" #")."""
    values: Dict[str, str] = {}
    try:
        with open(path, encoding="utf-8-sig") as fh:
            lines = fh.read().splitlines()
    except OSError:
        return values
    for raw in lines:
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export "):]
        key, sep, val = line.partition("=")
        key = key.strip()
        if not sep or not key or " " in key or "\t" in key:
            continue
        val = val.strip()
        if len(val) >= 2 and val[0] == val[-1] and val[0] in "\"'":
            val = val[1:-1]
        elif " #" in val:
            val = val[: val.index(" #")].strip()
        values[key] = val
    return values


def find_env_file(start: Optional[str] = None) -> Optional[str]:
    """El .env que corresponde: TRACEREPORTS_ENV_FILE, o el primero desde start (la carpeta
    actual) hacia arriba sin pasar de la raíz del repositorio. None si no hay o está desactivado."""
    explicit = os.getenv(PREFIX + "ENV_FILE")
    if explicit:
        return None if explicit.strip().lower() == _OFF else explicit
    d = os.path.abspath(start or os.getcwd())
    while True:
        candidate = os.path.join(d, ".env")
        if os.path.isfile(candidate):
            return candidate
        parent = os.path.dirname(d)
        if os.path.exists(os.path.join(d, ".git")) or parent == d:
            return None
        d = parent


def _file() -> Dict[str, str]:
    global _file_values, _file_path
    if _file_values is None:
        _file_path = find_env_file()
        loaded = parse_env_file(_file_path) if _file_path else {}
        _file_values = {k: v for k, v in loaded.items() if k.startswith(PREFIX)}
    return _file_values


def env_file_in_use() -> Optional[str]:
    """Ruta del .env del que se leyó alguna variable (para los mensajes), o None."""
    return _file_path if _file() else None


def reset_env_file() -> None:
    """Vuelve a buscar el .env en la próxima lectura (tests)."""
    global _file_values, _file_path
    _file_values, _file_path = None, None


def env(name: str, default: str = "") -> str:
    """TRACEREPORTS_<name>: primero del entorno, después del .env del proyecto."""
    return os.getenv(PREFIX + name, "") or _file().get(PREFIX + name, "") or default
