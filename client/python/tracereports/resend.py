"""
Resends the evidence left in the local queue (spool) because the server did not answer:

    python -m tracereports.resend ./tracereports-spool
    python -m tracereports.resend ./tracereports-spool --url http://tracereports:8080 --timeout 120

Every event carries its idempotency key: resending it twice duplicates nothing. A spool larger
than the queue is resent in batches. What cannot be sent now goes back to the same folder.

Exit code: 0 only if no event is left in the folder; 1 if evidence is left (unsent, in files
another process is resending, or unreadable) or the server rejected something.
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
    parser.add_argument("spool_dir", help="spool folder (TRACEREPORTS_SPOOL_DIR)")
    parser.add_argument("--url", default=None, help="server URL (default: $TRACEREPORTS_URL or http://localhost:8080)")
    parser.add_argument("--timeout", type=float, default=120.0, help="maximum seconds spent resending")
    args = parser.parse_args(argv)
    logging.basicConfig(level=logging.INFO, format="%(message)s")

    if not any(spool_files(args.spool_dir).values()):
        print(f"No pending events in {args.spool_dir}.")
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
    print(f"Resent {d['sent']} of {loaded} loaded events; rejected by the server {d['rejected']}; "
          f"back to the spool {d['spooled']}.")
    if left["pending"]:
        print(f"{left['pending']} files left to resend in {args.spool_dir} (the server did not answer or the time ran out).")
    if left["claimed"]:
        print(f"{left['claimed']} files are being resent by another process (or were left by one that ended: they are picked up again).")
    if left["unreadable"]:
        print(f"{left['unreadable']} unreadable files were left as *.unreadable: check them by hand.")
    return 0 if not any(left.values()) and d["rejected"] == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
