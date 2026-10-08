"""Los lotes de red entran en el límite del servidor (48 MiB por request), en orden y sin duplicados.

    pytest client/python/tests/test_network_batches.py
"""

import json

from tracereports import TraceReports

from test_transport import FakeServer

SERVER_LIMIT = 48 << 20  # maxNetworkBody del servidor


def _send(conns, **kw):
    srv = FakeServer()
    try:
        cr = TraceReports(srv.url, flush_timeout=60)
        cr.run_id = 7
        res = cr.attach_network(conns, test_id=11, **kw)
        assert cr.flush(60) == 0
        reqs = [b for m, p, k, b in srv.requests if p.endswith("/network")]
        return res, [len(b) for b in reqs], [json.loads(b)["connections"] for b in reqs]
    finally:
        srv.close()


def test_default_batches_fit_the_server_limit():
    # 200 conexiones con bodies de 256 KB: ~52 MB en un solo lote antes del arreglo
    conns = [{"method": "GET", "url": f"https://app/api/{i}", "status": 200, "response_body": "x" * (256 << 10)}
             for i in range(200)]
    res, sizes, batches = _send(conns)
    assert all(n <= SERVER_LIMIT for n in sizes), sizes
    assert [c["url"] for b in batches for c in b] == [c["url"] for c in conns], "all, once, in order"
    assert res == {"stored": 200, "errors": 0}


def test_size_is_measured_on_the_real_json_with_unicode_and_escapes():
    # json.dumps escapa lo no ASCII: "ñ" pesa 6 bytes, un emoji 12, un carácter de control 6
    body = "\u0001\"\\\n😀ñ" * 40000
    conns = [{"method": "POST", "url": f"https://app/api/{i}", "status": 500, "response_body": body,
              "post_data": "é" * 30000, "request_headers": {"x-trace": "ü" * 4000}} for i in range(30)]  # ~39 MB: cabe en la cola (64 MB)
    res, sizes, batches = _send(conns)
    assert len(sizes) > 1 and all(n <= SERVER_LIMIT for n in sizes), sizes
    got = [c for b in batches for c in b]
    assert [c["url"] for c in got] == [c["url"] for c in conns]
    assert got[3]["response_body"] == body[:256 << 10], "values are preserved by the split"
    assert got[3]["post_data"] == conns[3]["post_data"]
    assert got[3]["request_headers"]["x-trace"] == conns[3]["request_headers"]["x-trace"]


def test_a_connection_too_big_alone_is_sent_without_bodies():
    huge = {"method": "POST", "url": "https://app/upload", "status": 413, "post_data": "p" * (60 << 20),
            "request_headers": {"big": "h" * (1 << 20)}}
    small = {"method": "GET", "url": "https://app/ok", "status": 200, "response_body": "ok"}
    res, sizes, batches = _send([small, huge, small])
    assert all(n <= SERVER_LIMIT for n in sizes), sizes
    got = [c for b in batches for c in b]
    assert [c["url"] for c in got] == ["https://app/ok", "https://app/upload", "https://app/ok"]
    assert got[1]["post_data"] == "" and got[1]["body_truncated"] is True and got[1]["status"] == 413
    assert got[2]["response_body"] == "ok"
    assert res == {"stored": 3, "errors": 1}
