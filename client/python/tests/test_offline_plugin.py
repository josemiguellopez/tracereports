"""El plugin de pytest sin servidor: la suite corre igual y la evidencia queda grabada."""

import json
import os

import pytest

pytest_plugins = ["pytester"]


def test_plugin_records_when_the_server_rejects_the_token(pytester, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    monkeypatch.setenv("TRACEREPORTS_ENV_FILE", "off")
    pytester.makepyfile("""
        def test_ok(tracereports):
            tracereports.log_info("paso propio")

        def test_falla():
            assert 1 == 2
    """)
    rec = pytester.path / "rec"
    # puerto cerrado: el servidor no existe
    result = pytester.runpytest("--tracereports", "--tracereports-url", "http://127.0.0.1:9",
                                "--tracereports-run", "Sin servidor", "-p", "no:cacheprovider",
                                f"--tracereports-offline={rec}")
    result.assert_outcomes(passed=1, failed=1)
    result.stdout.fnmatch_lines(["*TraceReports (sin servidor): evidencia en *rec*"])
    lines = []
    for name in os.listdir(rec):
        if name.startswith("events-"):
            with open(rec / name, encoding="utf-8") as f:
                lines += [json.loads(x) for x in f if x.strip()]
    finishes = [e["body"]["status"] for e in lines if e["path"].endswith("/finish") and "/tests/" in e["path"]]
    assert sorted(finishes) == ["FAIL", "PASS"]
    assert any(e["body"].get("message") == "paso propio" for e in lines if e["path"].endswith("/logs"))
    assert lines[0]["body"]["name"] == "Sin servidor" and lines[0]["body"]["framework"] == "pytest"


def test_plugin_auto_fallback_is_a_problem_in_strict_mode(pytester, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    monkeypatch.setenv("TRACEREPORTS_ENV_FILE", "off")
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_DIR", str(pytester.path / "auto"))
    pytester.makepyfile("def test_ok():\n    pass\n")
    result = pytester.runpytest("--tracereports", "--tracereports-url", "http://127.0.0.1:9",
                                "--tracereports-strict", "-p", "no:cacheprovider")
    # el test pasó, pero la evidencia no llegó al servidor: en modo estricto la sesión falla
    assert result.ret != 0
    result.stdout.fnmatch_lines(["*sin servidor: la evidencia quedó grabada en*"])


def test_plugin_with_xdist_workers_records_one_run(pytester, monkeypatch):
    """Regresión: execnet manda los int como 32 bits; el id local de la ejecución viaja como texto."""
    pytest.importorskip("xdist")
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    monkeypatch.setenv("TRACEREPORTS_ENV_FILE", "off")
    pytester.makepyfile("""
        import pytest
        @pytest.mark.parametrize("n", range(4))
        def test_n(n):
            pass
    """)
    rec = pytester.path / "rec"
    result = pytester.runpytest_subprocess("-n", "2", "--tracereports", "--tracereports-url", "http://127.0.0.1:9",
                                           "-p", "no:cacheprovider", f"--tracereports-offline={rec}")
    result.assert_outcomes(passed=4)
    files = [n for n in os.listdir(rec) if n.startswith("events-")]
    assert len(files) == 3  # controlador + 2 workers
    lines = []
    for name in files:
        with open(rec / name, encoding="utf-8") as f:
            lines += [json.loads(x) for x in f if x.strip()]
    run_id = next(e["local_id"] for e in lines if e["path"] == "/api/v1/runs")
    created = [e for e in lines if e["path"] == f"/api/v1/runs/{run_id}/tests"]
    assert len(created) == 4
