"""El cliente lee TRACEREPORTS_* del .env del proyecto cuando no están en el entorno."""

import json
import os
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from tracereports import TraceReports
from tracereports import _env


@pytest.fixture
def project(tmp_path, monkeypatch):
    """Un repositorio con .env en la raíz y los tests en una subcarpeta (cwd)."""
    (tmp_path / ".git").mkdir()
    sub = tmp_path / "tests" / "e2e"
    sub.mkdir(parents=True)
    monkeypatch.chdir(sub)
    monkeypatch.delenv("TRACEREPORTS_ENV_FILE", raising=False)
    for k in ("TRACEREPORTS_TOKEN", "TRACEREPORTS_URL"):
        monkeypatch.delenv(k, raising=False)
    _env.reset_env_file()
    yield tmp_path
    _env.reset_env_file()


def test_same_rules_as_the_server(tmp_path):
    f = tmp_path / ".env"
    f.write_text(
        "# comentario\n\nexport TRACEREPORTS_TOKEN=abc\nTRACEREPORTS_URL = 'http://x:1'  \n"
        'TRACEREPORTS_RUN_NAME="con # dentro"\nTRACEREPORTS_ENV=qa # comentario al final\n'
        "MALA LINEA=1\nsin_igual\n",
        encoding="utf-8",
    )
    got = _env.parse_env_file(str(f))
    assert got == {"TRACEREPORTS_TOKEN": "abc", "TRACEREPORTS_URL": "http://x:1",
                   "TRACEREPORTS_RUN_NAME": "con # dentro", "TRACEREPORTS_ENV": "qa"}


def test_found_walking_up_to_the_repository_root(project, monkeypatch):
    (project / ".env").write_text("TRACEREPORTS_TOKEN=desde-env\nAI_API_KEY=no-se-usa\n", encoding="utf-8")
    assert _env.env("TOKEN") == "desde-env"
    assert _env.env_file_in_use() == str(project / ".env")
    assert "AI_API_KEY" not in _env._file()  # solo claves del cliente


def test_environment_wins_over_the_file(project, monkeypatch):
    (project / ".env").write_text("TRACEREPORTS_TOKEN=archivo\n", encoding="utf-8")
    monkeypatch.setenv("TRACEREPORTS_TOKEN", "secret-de-ci")
    assert _env.env("TOKEN") == "secret-de-ci"
    monkeypatch.delenv("TRACEREPORTS_TOKEN")
    assert _env.env("TOKEN") == "archivo"


def test_does_not_leave_the_repository(project):
    # un .env por encima de la raíz del repositorio (p. ej. en la carpeta del usuario) no se usa
    (project.parent / ".env").write_text("TRACEREPORTS_TOKEN=ajeno\n", encoding="utf-8")
    try:
        assert _env.env("TOKEN") == ""
        assert _env.env_file_in_use() is None
    finally:
        (project.parent / ".env").unlink()


def test_explicit_file_and_off(project, monkeypatch, tmp_path):
    (project / ".env").write_text("TRACEREPORTS_TOKEN=raiz\n", encoding="utf-8")
    other = tmp_path / "otro.env"
    other.write_text("TRACEREPORTS_TOKEN=otro\n", encoding="utf-8")
    monkeypatch.setenv("TRACEREPORTS_ENV_FILE", str(other))
    _env.reset_env_file()
    assert _env.env("TOKEN") == "otro"
    monkeypatch.setenv("TRACEREPORTS_ENV_FILE", "off")
    _env.reset_env_file()
    assert _env.env("TOKEN") == ""


def test_the_client_sends_the_token_from_the_env_file(project):
    seen = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *a):
            pass

        def do_POST(self):
            self.rfile.read(int(self.headers.get("Content-Length") or 0))
            seen.append(self.headers.get("Authorization"))
            raw = json.dumps({"run_id": 1}).encode()
            self.send_response(201)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    try:
        (project / ".env").write_text(
            f"TRACEREPORTS_URL=http://127.0.0.1:{httpd.server_address[1]}\nTRACEREPORTS_TOKEN=tok-del-env\n",
            encoding="utf-8")
        cr = TraceReports(async_send=False)
        assert cr.start_run("r") == 1
        assert seen == ["Bearer tok-del-env"]
    finally:
        httpd.shutdown()
        httpd.server_close()
