"""
Reenvía la evidencia que quedó en la cola local (spool) porque el servidor no respondía:

    python -m tracereports.resend ./tracereports-spool
    python -m tracereports.resend ./tracereports-spool --url http://tracereports:8080 --timeout 120

Cada evento lleva su clave de idempotencia: reenviarlo dos veces no duplica nada. Un spool más
grande que la cola se reenvía por tandas. Lo que no se pueda enviar ahora vuelve a la misma carpeta.

Código de salida: 0 solo si no queda ningún evento pendiente en la carpeta; 1 si queda evidencia
(sin enviar, en archivos que otro proceso está reenviando, o ilegibles) o el servidor rechazó algo.
"""

from __future__ import annotations

import argparse
import logging
import sys
import time

from .client import TraceReports
from .transport import spool_files


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="python -m tracereports.resend", description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("spool_dir", help="carpeta del spool (TRACEREPORTS_SPOOL_DIR)")
    parser.add_argument("--url", default=None, help="URL del servidor (default: $TRACEREPORTS_URL o http://localhost:8080)")
    parser.add_argument("--timeout", type=float, default=120.0, help="segundos máximos reenviando")
    args = parser.parse_args(argv)
    logging.basicConfig(level=logging.INFO, format="%(message)s")

    if not any(spool_files(args.spool_dir).values()):
        print(f"No hay eventos pendientes en {args.spool_dir}.")
        return 0

    cr = TraceReports(args.url, spool_dir=args.spool_dir)
    sender = cr._sender
    deadline = time.time() + args.timeout
    loaded = pending = 0
    while time.time() < deadline:  # por tandas: el spool puede ser más grande que la cola
        n = sender.load_spool()
        loaded += n
        pending = cr.flush(max(deadline - time.time(), 0))
        if n == 0 or pending:
            break
    sender.drain_to_spool()  # lo que no salió vuelve al spool
    d = cr.delivery
    left = spool_files(args.spool_dir)
    print(f"Reenviados {d['sent']} de {loaded} eventos cargados · rechazados por el servidor {d['rejected']} · "
          f"vuelven al spool {d['spooled']}.")
    if left["pending"]:
        print(f"Quedan {left['pending']} archivos sin reenviar en {args.spool_dir} (el servidor no respondió o se acabó el tiempo).")
    if left["claimed"]:
        print(f"{left['claimed']} archivos los está reenviando otro proceso (o quedaron de uno que terminó: se retoman solos).")
    if left["unreadable"]:
        print(f"{left['unreadable']} archivos ilegibles quedaron como *.unreadable: revísalos a mano.")
    return 0 if not any(left.values()) and d["rejected"] == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
