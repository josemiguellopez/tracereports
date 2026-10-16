"""Selenium: la red, la consola y el DOM salen del log de rendimiento de Chrome (sin navegador aquí)."""

import base64
import json
import os

from tracereports.selenium import capture_console, capture_network, connections_from_log, is_webdriver, selenium_options

pytest_plugins = ["pytester"]


def ev(method, **params):
    """Una entrada de driver.get_log("performance"), como la escribe ChromeDriver."""
    return {"level": "INFO", "timestamp": 0, "message": json.dumps({"message": {"method": method, "params": params}, "webview": "x"})}


def checkout_log():
    return [
        ev("Network.requestWillBeSent", requestId="1", type="Document", wallTime=1790000000.0, timestamp=10.0,
           request={"url": "https://shop.test/cart", "method": "GET", "headers": {"Accept": "text/html"}}),
        ev("Network.responseReceived", requestId="1", type="Document",
           response={"url": "https://shop.test/cart", "status": 200, "statusText": "OK", "mimeType": "text/html", "headers": {"date": "x"}}),
        ev("Network.loadingFinished", requestId="1", timestamp=10.2),
        # el POST de la API: los headers reales (con la sesión) llegan en ExtraInfo
        ev("Network.requestWillBeSent", requestId="2", type="Fetch", wallTime=1790000001.0, timestamp=11.0,
           request={"url": "https://shop.test/api/orders", "method": "POST", "headers": {"Content-Type": "application/json"},
                    "hasPostData": True, "postData": '{"items":[1],"password":"s3cret"}'}),
        ev("Network.requestWillBeSentExtraInfo", requestId="2", headers={"Authorization": "Bearer abc.def", "Cookie": "sid=1"}),
        ev("Network.responseReceived", requestId="2", type="Fetch",
           response={"url": "https://shop.test/api/orders", "status": 500, "statusText": "Internal Server Error",
                     "mimeType": "application/json", "headers": {"content-type": "application/json"}}),
        ev("Network.loadingFinished", requestId="2", timestamp=11.35),
        # una redirección: dos saltos con el mismo requestId
        ev("Network.requestWillBeSent", requestId="3", type="Document", wallTime=1790000002.0, timestamp=12.0,
           request={"url": "https://shop.test/old", "method": "GET", "headers": {}}),
        ev("Network.requestWillBeSent", requestId="3", type="Document", wallTime=1790000002.1, timestamp=12.1,
           request={"url": "https://shop.test/new", "method": "GET", "headers": {}},
           redirectResponse={"url": "https://shop.test/old", "status": 302, "statusText": "Found", "headers": {"location": "/new"}}),
        ev("Network.responseReceived", requestId="3", type="Document",
           response={"url": "https://shop.test/new", "status": 200, "mimeType": "text/html", "headers": {}}),
        ev("Network.loadingFinished", requestId="3", timestamp=12.3),
        ev("Network.requestWillBeSent", requestId="4", type="XHR", wallTime=1790000003.0, timestamp=13.0,
           request={"url": "https://down.test/api", "method": "GET", "headers": {}}),
        ev("Network.loadingFailed", requestId="4", timestamp=13.5, errorText="net::ERR_CONNECTION_REFUSED"),
        ev("Network.requestWillBeSent", requestId="5", type="Image", wallTime=1790000004.0, timestamp=14.0,
           request={"url": "data:image/png;base64,AAAA", "method": "GET", "headers": {}}),
        ev("Page.frameNavigated", frame={}),
    ]


class FakeDriver:
    session_id = "s1"

    def __init__(self, perf=None, browser=None):
        self.logs = {"performance": list(perf or []), "browser": list(browser or [])}
        self.cdp_calls = []

    def get_log(self, kind):
        out, self.logs[kind] = self.logs.get(kind, []), []
        return out

    def execute_cdp_cmd(self, cmd, params):
        self.cdp_calls.append(cmd)
        if cmd == "Network.getResponseBody" and params["requestId"] == "2":
            return {"body": base64.b64encode(b'{"error":"payments: connection pool exhausted","token":"t1"}').decode(), "base64Encoded": True}
        raise RuntimeError("No resource with given identifier found")

    def get_screenshot_as_png(self):
        return b"\x89PNG fake"

    def execute_script(self, script):
        return {"url": "https://shop.test/cart?token=abc", "title": "Cart", "viewport": {"w": 1, "h": 1},
                "elements": [{"tag": "button", "id": "pay", "text": "Pay", "label": "", "placeholder": ""}]}


def test_connections_from_log():
    conns = connections_from_log(checkout_log())
    assert [(c["method"], c["url"], c.get("status")) for c in conns] == [
        ("GET", "https://shop.test/cart", 200),
        ("POST", "https://shop.test/api/orders", 500),
        ("GET", "https://shop.test/old", 302),
        ("GET", "https://shop.test/new", 200),
        ("GET", "https://down.test/api", None),
    ]  # la imagen data: no es una llamada
    order = conns[1]
    assert order["resource_type"] == "fetch" and order["duration_ms"] == 350 and order["wall_time"] == 1790000001.0
    assert order["request_headers"]["Cookie"] == "sid=1" and order["request_headers"]["Authorization"].startswith("Bearer")
    assert conns[2]["response_headers"]["location"] == "/new" and conns[2]["duration_ms"] == 100
    assert conns[4]["failed"] and conns[4]["error_text"] == "net::ERR_CONNECTION_REFUSED"


def test_capture_network_reads_bodies_and_masks():
    driver = FakeDriver(perf=checkout_log())
    conns = capture_network(driver)
    order = next(c for c in conns if c["method"] == "POST")
    assert "connection pool exhausted" in order["response_body"] and '"token": "<masked>"' in order["response_body"]
    assert order["body_size"] > 0 and not order["body_truncated"]
    assert '"password": "<masked>"' in order["post_data"]
    assert order["request_headers"]["Authorization"] == "<masked>" and order["request_headers"]["Cookie"] == "<masked>"
    # solo se pide el body de la API y de los errores; nunca del documento ni de lo que falló
    assert driver.cdp_calls == ["Network.getResponseBody"]
    assert all("_request_id" not in c for c in conns)
    assert capture_network(driver) == []  # el log se vació: el próximo test lee solo lo suyo


def test_capture_network_without_performance_log():
    class Firefox(FakeDriver):
        def get_log(self, kind):
            raise ValueError("log type not supported")
    assert capture_network(Firefox()) == []
    assert capture_console(Firefox()) == []


def test_capture_console():
    driver = FakeDriver(browser=[
        {"level": "SEVERE", "source": "network", "message": "https://shop.test/api/orders - Failed to load resource: 500", "timestamp": 5},
        {"level": "SEVERE", "source": "javascript", "message": "TypeError: o.order is undefined", "timestamp": 6},
        {"level": "WARNING", "source": "console-api", "message": "deprecated token=abc", "timestamp": 7},
        {"level": "INFO", "source": "console-api", "message": "hello", "timestamp": 8},
    ])
    assert capture_console(driver) == [
        {"level": "error", "text": "https://shop.test/api/orders - Failed to load resource: 500", "location": "", "timestamp": 5},
        {"level": "pageerror", "text": "TypeError: o.order is undefined", "location": "", "timestamp": 6},
        {"level": "warning", "text": "deprecated token=<masked>", "location": "", "timestamp": 7},
    ]


def test_selenium_options_and_detection():
    class ChromeOptions:
        def __init__(self):
            self.caps = {}

        def set_capability(self, k, v):
            self.caps[k] = v

    class EdgeOptions(ChromeOptions):
        pass

    assert selenium_options(ChromeOptions()).caps == {"goog:loggingPrefs": {"performance": "ALL", "browser": "ALL"}}
    assert "ms:loggingPrefs" in selenium_options(EdgeOptions()).caps
    assert is_webdriver(FakeDriver()) and not is_webdriver(object())


def test_plugin_sends_selenium_network_console_and_failure_evidence(pytester, monkeypatch):
    """Con --tracereports y un driver en una fixture, sin código extra en el test."""
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
    monkeypatch.setenv("TRACEREPORTS_ENV_FILE", "off")
    here = os.path.dirname(__file__)
    pytester.syspathinsert(here)
    pytester.makeconftest("""
        import pytest
        from test_selenium import FakeDriver, checkout_log

        @pytest.fixture
        def driver():
            d = FakeDriver(browser=[{"level": "SEVERE", "source": "javascript", "message": "antes del test", "timestamp": 1}])
            d.logs["performance"] = [{"message": "{}"}]  # lo de antes del test: se descarta
            d.during = checkout_log()
            d.during_console = [{"level": "SEVERE", "source": "javascript", "message": "TypeError: o.order is undefined", "timestamp": 2}]
            return d
    """)
    pytester.makepyfile("""
        def test_checkout(driver):
            driver.logs["performance"] = driver.during   # lo que pasa durante el test
            driver.logs["browser"] = driver.during_console
            assert False, "order not confirmed"
    """)
    rec = pytester.path / "rec"
    result = pytester.runpytest("--tracereports", "--tracereports-url", "http://127.0.0.1:9", "-p", "no:cacheprovider",
                                f"--tracereports-offline={rec}")
    result.assert_outcomes(failed=1)
    events = []
    for name in os.listdir(rec):
        if name.startswith("events-"):
            with open(rec / name, encoding="utf-8") as f:
                events += [json.loads(x) for x in f if x.strip()]
    paths = [e["path"].rsplit("/", 1)[-1] for e in events]
    network = [c for e in events if e["path"].endswith("/network") for c in e["body"]["connections"]]
    assert any(c["status"] == 500 and "pool exhausted" in c["response_body"] for c in network)
    assert len(network) == 5  # solo las del test, no el log anterior
    assert "console" in paths and "dom" in paths and "screenshot" in paths
    console = [x for e in events if e["path"].endswith("/console") for x in e["body"]["entries"]]
    assert [x["text"] for x in console] == ["TypeError: o.order is undefined"]
