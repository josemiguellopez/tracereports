"""Black-box tests for all four clients using a real renderer and a local release server.

Run: python test/local_html/test_clients.py [-v] [ClientTests.test_python_unauthorized]
Requires Go, Node, Python and a JDK on PATH (or JAVA_HOME). No third-party Python packages.
"""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import io
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[2]
VERSION = "0.2.0"
SYSTEM = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}[platform.system()]
ARCH = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[platform.machine().lower()]
EXE = ".exe" if SYSTEM == "windows" else ""
ASSET = f"tracereports_{VERSION}_{SYSTEM}_{ARCH}." + ("zip" if EXE else "tar.gz")


class ClientTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory(prefix="tr-renderer-build-")
        cls.work = Path(cls.build.name)
        cls.binary = cls.work / ("tracereports" + EXE)
        subprocess.run(["go", "build", "-o", str(cls.binary), "./cmd"], cwd=ROOT, check=True)
        go_driver = cls.work / ("go-driver" + EXE)
        subprocess.run(["go", "build", "-o", str(go_driver), str(ROOT / "test/local_html/go_driver.go")], cwd=ROOT / "client/go", check=True)
        java_home = os.environ.get("JAVA_HOME")
        java = str(Path(java_home) / "bin" / ("java" + EXE)) if java_home else shutil.which("java")
        javac = str(Path(java_home) / "bin" / ("javac" + EXE)) if java_home else shutil.which("javac")
        subprocess.run([javac, "-encoding", "UTF-8", "-d", str(cls.work), *map(str, (ROOT / "client/java/src/main/java/tracereports").glob("*.java")), str(ROOT / "test/local_html/JavaDriver.java")], check=True)
        cls.commands = {
            "python": [sys.executable, str(ROOT / "test/local_html/python_driver.py")],
            "js": [shutil.which("node"), str(ROOT / "test/local_html/js_driver.mjs")],
            "java": [java, "-cp", str(cls.work), "JavaDriver"],
            "go": [str(go_driver)],
        }
        data = cls.binary.read_bytes()
        output = io.BytesIO()
        if EXE:
            with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as z:
                z.writestr("tracereports.exe", data)
                z.writestr("../outside.txt", "must never be extracted")
        else:
            with tarfile.open(fileobj=output, mode="w:gz") as tar:
                member = tarfile.TarInfo("tracereports"); member.size = len(data); member.mode = 0o755
                tar.addfile(member, io.BytesIO(data))
        cls.archive = output.getvalue()

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="tr-download-test-")
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.requests = []
        self.bad_checksum = False
        self.status = 401
        self.release_status = 200
        self.release_delay = 0
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                owner.requests.append((self.path, self.headers.get("Authorization")))
                time.sleep(owner.release_delay)
                if self.path == f"/v{VERSION}/checksums.txt":
                    digest = "0" * 64 if owner.bad_checksum else hashlib.sha256(owner.archive).hexdigest()
                    data = f"{digest}  {ASSET}\n".encode()
                elif self.path == f"/v{VERSION}/{ASSET}":
                    data = owner.archive
                else:
                    self.send_error(404); return
                self.send_response(owner.release_status)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def reply(self):
                self.rfile.read(int(self.headers.get("Content-Length", 0)))
                data = json.dumps({"run_id": 7} if self.path == "/api/v1/runs" else {"test_id": 11} if self.path.endswith("/tests") else {}).encode()
                self.send_response(owner.status)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers(); self.wfile.write(data)
            do_POST = do_PATCH = reply
            def log_message(self, *args): pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.url = f"http://127.0.0.1:{self.server.server_port}"
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("TRACEREPORTS_")}
        self.env.update(PATH="", LOCALAPPDATA=str(self.directory / "cache"), XDG_CACHE_HOME=str(self.directory / "cache"),
                        PYTHONPATH=str(ROOT / "client/python"), TRACEREPORTS_ENV_FILE="off",
                        TRACEREPORTS_URL=self.url, TRACEREPORTS_TOKEN="wrong-token",
                        TRACEREPORTS_OFFLINE="auto", TRACEREPORTS_OFFLINE_REPORT="1",
                        TRACEREPORTS_BIN_BASE_URL=self.url, TRACEREPORTS_BRANCH="test", TRACEREPORTS_COMMIT="test")

    def start(self, client, folder="run", use_base=False):
        env = dict(self.env)
        if use_base:
            env["TRACEREPORTS_OFFLINE_BASE"] = str(self.directory / folder)
        else:
            env["TRACEREPORTS_OFFLINE_DIR"] = str(self.directory / folder)
        return subprocess.Popen(self.commands[client], env=env, cwd=self.directory, text=True, encoding="utf-8", errors="replace", stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

    def result(self, process):
        try: output, _ = process.communicate(timeout=90)
        except subprocess.TimeoutExpired:
            process.kill(); output, _ = process.communicate(); self.fail("client did not finish: " + output)
        self.assertEqual(process.returncode, 0, output)
        result = [line[7:] for line in output.splitlines() if line.startswith("RESULT ")]
        self.assertEqual(len(result), 1, output)
        return json.loads(result[0]), output

    def run_client(self, client, folder="run"):
        return self.result(self.start(client, folder))

    def assert_html(self, result, folder="run"):
        report = self.directory / folder / "report/index.html"
        self.assertTrue(report.is_file(), result)
        self.assertEqual(Path(result["report"]).resolve(), report.resolve())
        self.assertIn("renderer evidence", report.with_name("data.js").read_text(encoding="utf-8"))
        self.assertIn("local HTML", report.with_name("data.js").read_text(encoding="utf-8"))
        self.assertFalse((self.directory / "cache/tracereports" / VERSION / "outside.txt").exists())

    def assert_downloads(self, count):
        self.assertEqual(sum(p.endswith(ASSET) for p, _ in self.requests), count, self.requests)
        self.assertEqual(sum(p.endswith("checksums.txt") for p, _ in self.requests), count, self.requests)
        self.assertTrue(all(auth is None for _, auth in self.requests), "client token sent to release server")

    def unauthorized(self, client):
        result, output = self.run_client(client)
        self.assertLess(result["run"], 0, output)
        self.assert_html(result)
        self.assert_downloads(1)
        self.assertTrue(list((self.directory / "run").glob("events-*.jsonl")))

    def checksum(self, client):
        self.bad_checksum = True
        result, output = self.run_client(client)
        self.assertFalse(result["report"], output)
        self.assert_downloads(1)
        self.assertTrue(list((self.directory / "run").glob("events-*.jsonl")))
        self.assertEqual(output.count("install the binary"), 1, output)
        self.assertIn("tracereports report", output); self.assertIn("tracereports push", output)
        self.assertFalse(list((self.directory / "cache").rglob("tracereports" + EXE)))

    def disabled(self, client):
        self.env["TRACEREPORTS_BIN_DOWNLOAD"] = "0"
        result, output = self.run_client(client)
        self.assertFalse(result["report"], output); self.assert_downloads(0)
        self.assertTrue(list((self.directory / "run").glob("events-*.jsonl")))
        self.assertEqual(output.count("install the binary"), 1, output)

    def cached(self, client):
        first, _ = self.run_client(client); self.assert_html(first)
        self.release_status = 500
        self.env["TRACEREPORTS_BIN_DOWNLOAD"] = "0"
        second, _ = self.run_client(client, "second"); self.assert_html(second, "second")
        self.assert_downloads(1)

    def concurrent(self, client):
        self.release_delay = .25
        a, b = self.start(client), self.start(client, "second")
        first, output_a = self.result(a); second, output_b = self.result(b)
        self.assertTrue(first["report"] and second["report"], output_a + output_b)
        self.assert_html(first); self.assert_html(second, "second"); self.assert_downloads(1)

    def named_parallel(self, client):
        self.release_delay = .25
        a, b = self.start(client, "named", True), self.start(client, "named", True)
        first, output_a = self.result(a); second, output_b = self.result(b)
        reports = [Path(first["report"]), Path(second["report"])]
        self.assertTrue(all(p.is_file() for p in reports), output_a + output_b)
        folders = [p.parent.parent for p in reports]
        self.assertEqual(len(set(folders)), 2)
        for folder in folders:
            self.assertRegex(folder.name, r"^orangehrm-pim-\d{8}-\d{6}-[0-9a-f]{6}$")
        self.assert_downloads(1)

    def stale_lock(self, client):
        # un proceso murió a mitad de la descarga y dejó el bloqueo: la corrida siguiente lo retoma
        lock = self.directory / "cache/tracereports" / VERSION / f"{SYSTEM}_{ARCH}.lock"
        lock.mkdir(parents=True)
        past = time.time() - 3600
        os.utime(lock, (past, past))
        result, output = self.run_client(client)
        self.assert_html(result); self.assert_downloads(1)
        self.assertFalse(lock.exists(), output)

    def both(self, client):
        self.status = 201; self.env["TRACEREPORTS_OFFLINE"] = "both"
        result, output = self.run_client(client)
        self.assert_html(result); self.assert_downloads(1)
        self.assertIn(self.url + "/#run=7", output)
        self.assertIn(str(self.directory / "run/report/index.html").replace("\\", "/"), output.replace("\\", "/"))
        self.assertFalse(list((self.directory / "run").glob("events-*.jsonl")))
        self.assertTrue(json.loads((self.directory / "run/tracereports-offline.json").read_text())["raw_removed"])

    def explicit(self, client):
        self.env["TRACEREPORTS_BIN"] = str(self.binary)
        result, _ = self.run_client(client); self.assert_html(result); self.assert_downloads(0)

    def always(self, client):
        self.env["TRACEREPORTS_OFFLINE"] = "always"
        result, _ = self.run_client(client); self.assert_html(result); self.assert_downloads(1)

    def unavailable(self, client):
        self.release_status = 500
        result, output = self.run_client(client)
        self.assertFalse(result["report"], output)
        self.assertTrue(list((self.directory / "run").glob("events-*.jsonl")))
        self.assertEqual(output.count("install the binary"), 1, output)


for client in ("python", "js", "java", "go"):
    for scenario in ("unauthorized", "checksum", "disabled", "cached", "concurrent", "named_parallel", "both", "explicit", "always", "unavailable", "stale_lock"):
        def run(self, client=client, scenario=scenario): getattr(self, scenario)(client)
        setattr(ClientTests, f"test_{client}_{scenario}", run)

if __name__ == "__main__": unittest.main()
