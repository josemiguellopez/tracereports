"""Snapshot estructural de la página (Inspector) y snapshots por paso (TRACEREPORTS_DOM_STEPS).

    pip install pytest playwright && python -m playwright install chromium && pytest client/python/tests
"""

import json

import pytest

from tracereports import TraceReports, capturar_dom
from tracereports._env import reset_env_file
from tracereports.dom import STRUCT_SCRIPT, dom_signature
from tracereports import selenium as trs

from test_transport import FakeServer

PAGE = """<html><body style="background:#f6f4ef">
<header><a class="brand" href="/"><span class="mark" style="display:inline-block;width:24px;height:24px;background:#1f5f45"></span>Demo Shop</a></header>
<main>
  <div class="skeleton">Cargando la ficha…</div>
  <div class="actions"><button data-testid="add-to-cart" onclick="x()">Add to cart</button></div>
  <input id="rut" placeholder="RUT" value="11.111.111-1">
  <input type="password" id="pw" value="secreta">
  <span data-tracereports-mask>Paciente: Juan Pérez</span>
  <div class="later" style="display:none">Oculto</div>
</main></body></html>"""


@pytest.fixture(autouse=True)
def clean_env(monkeypatch, tmp_path):
    for var in ("TRACEREPORTS_DOM", "TRACEREPORTS_DOM_STEPS", "TRACEREPORTS_SCREENSHOT_MASK", "TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS",
                "TRACEREPORTS_SCREENSHOTS", "TRACEREPORTS_TOKEN", "TRACEREPORTS_SYNC"):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.chdir(tmp_path)
    reset_env_file()
    yield
    reset_env_file()


@pytest.fixture(scope="module")
def page():
    sync_api = pytest.importorskip("playwright.sync_api")
    try:
        pw = sync_api.sync_playwright().start()
        browser = pw.chromium.launch()
    except Exception as err:  # noqa: BLE001
        pytest.skip(f"chromium not available: {err}")
    p = browser.new_page(viewport={"width": 1000, "height": 700})
    p.set_content(PAGE)
    yield p
    browser.close()
    pw.stop()


def test_structural_snapshot_has_the_whole_tree(page):
    snap = capturar_dom(page)
    nodes = snap["nodes"]
    assert snap["kind"] == "fail" and snap["at"] > 0 and snap["page"]["w"] >= 1000
    assert all(n["i"] == k and n["p"] < k for k, n in enumerate(nodes)), "a flat tree: every parent comes first"
    by_text = {n["text"]: n for n in nodes if n["text"]}
    # lo que el snapshot de antes no tenía: un div con solo texto (un skeleton, una validación)
    assert by_text["Cargando la ficha…"]["tag"] == "div" and by_text["Cargando la ficha…"]["attrs"]["class"] == "skeleton"
    brand = by_text["Demo Shop"]
    assert brand["tx"] > 0, "the text starts after the logo, not at the edge of the box"
    button = next(n for n in nodes if n["tag"] == "button")
    assert button["attrs"] == {"data-testid": "add-to-cart"}, "only the allowed attributes (no onclick)"
    assert button["style"]["backgroundColor"] and button["style"]["fontSize"]
    hidden = next(n for n in nodes if n["attrs"].get("class") == "later")
    assert hidden["visible"] is False
    # los elementos del recomendador de selectores siguen ahí
    assert any(e["testid"] == "add-to-cart" for e in snap["elements"])


def test_what_is_typed_and_what_is_masked_never_travels(page):
    raw = json.dumps(capturar_dom(page), ensure_ascii=False)
    for secret in ("11.111.111-1", "secreta", "Juan Pérez"):
        assert secret not in raw, secret
    snap = capturar_dom(page)
    assert any(n["text"] == "•••" for n in snap["nodes"]), "the masked element keeps its box, not its text"
    rut = next(n for n in snap["nodes"] if n["attrs"].get("id") == "rut")
    assert rut["attrs"]["placeholder"] == "RUT" and rut["text"] == ""


@pytest.mark.parametrize("level", ["structure", "basic"])
def test_a_container_never_carries_the_masked_text_of_its_children(page, monkeypatch, level):
    # el contenedor con id entra a la lista de elements con su innerText: lo tapado no puede ir ahí
    monkeypatch.setenv("TRACEREPORTS_DOM", level)
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK", ".rut-paciente")
    reset_env_file()
    page.set_content('<section id="ficha"><h2>Atención</h2><p>Paciente: <span data-tracereports-mask>Juana Pérez Soto</span></p>'
                     '<p>RUT <b class="rut-paciente">9.876.543-2</b></p><button id="guardar">Guardar</button></section>')
    try:
        snap = capturar_dom(page)
        raw = json.dumps(snap, ensure_ascii=False)
        assert "Juana" not in raw and "9.876.543-2" not in raw
        ficha = next(e for e in snap["elements"] if e["id"] == "ficha")
        assert "Atención" in ficha["text"] and "•••" in ficha["text"] and "Guardar" in ficha["text"]
    finally:
        page.set_content(PAGE)


def test_basic_level_keeps_the_old_snapshot(page, monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_DOM", "basic")
    reset_env_file()
    snap = capturar_dom(page)
    assert "nodes" not in snap and snap["elements"]


def test_selenium_runs_the_structural_script_with_the_mask():
    calls = []

    class Driver:
        def execute_script(self, script, *args):
            calls.append((script, args))
            return {"url": "https://app/?token=abc", "nodes": [{"i": 0, "p": -1, "tag": "body", "text": "", "attrs": {}}], "elements": []}

    snap = trs.capture_dom(Driver(), kind="step", label="Abrir ficha")
    assert STRUCT_SCRIPT in calls[0][0] and "data-tracereports-mask" in calls[0][1][0]
    assert snap["kind"] == "step" and snap["label"] == "Abrir ficha"


def test_selenium_basic_level_also_gets_the_mask(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_DOM", "basic")
    reset_env_file()
    calls = []

    class Driver:
        def execute_script(self, script, *args):
            calls.append((script, args))
            return {"url": "https://app/", "elements": []}

    trs.capture_dom(Driver())
    assert "arguments[0]" in calls[0][0] and "data-tracereports-mask" in calls[0][1][0]


def _snap(text):
    return {"url": "https://app/", "nodes": [{"i": 0, "p": -1, "tag": "body", "text": text, "x": 0, "y": 0, "w": 10, "h": 10, "visible": True}],
            "elements": []}


def test_dom_signature():
    assert dom_signature(_snap("a")) == dom_signature(_snap("a"))
    assert dom_signature(_snap("a")) != dom_signature(_snap("b"))


def test_step_snapshots_once_per_distinct_page(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_DOM_STEPS", "1")
    reset_env_file()
    srv = FakeServer()
    try:
        cr = TraceReports(srv.url)
        cr.run_id = 7
        pages = iter([_snap("login"), _snap("login"), _snap("ficha"), _snap("ficha")])
        cr.dom_step_source = lambda: next(pages)
        cr.attach_screenshot(b"\x89PNG a", "Abrir login", test_id=11)
        cr.attach_screenshot(b"\x89PNG b", "Escribir usuario", test_id=11)  # misma pantalla: no se repite
        cr.attach_screenshot(b"\x89PNG c", "Abrir la ficha", test_id=11)
        cr.attach_screenshot(b"\x89PNG d", "Captura al fallar", status="FAIL", test_id=11)  # la del fallo va aparte
        assert cr.flush(10) == 0
        doms = [json.loads(b) for m, p, k, b in srv.requests if p.endswith("/dom")]
        assert [(d["kind"], d["label"], d["nodes"][0]["text"]) for d in doms] == [("step", "Abrir login", "login"), ("step", "Abrir la ficha", "ficha")]
    finally:
        srv.close()


def test_no_step_snapshots_unless_asked():
    srv = FakeServer()
    try:
        cr = TraceReports(srv.url)
        cr.run_id = 7
        cr.dom_step_source = lambda: _snap("login")
        cr.attach_screenshot(b"\x89PNG a", "Abrir login", test_id=11)
        assert cr.flush(10) == 0
        assert not [p for m, p, k, b in srv.requests if p.endswith("/dom")]
    finally:
        srv.close()


def test_a_broken_page_never_breaks_the_test(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_DOM_STEPS", "1")
    reset_env_file()
    cr = TraceReports("http://127.0.0.1:9", enabled=True)

    def boom():
        raise RuntimeError("the browser is gone")

    cr.dom_step_source = boom
    cr._dom_step(11, "paso")  # no explota

    class Gone:
        def evaluate(self, *a):
            raise RuntimeError("closed")

    assert capturar_dom(Gone()) is None
