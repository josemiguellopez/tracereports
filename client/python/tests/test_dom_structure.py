"""Snapshot estructural de la página (Inspector) y snapshots por paso (TRACEREPORTS_DOM_STEPS).

    pip install pytest playwright && python -m playwright install chromium && pytest client/python/tests
"""

import json
import re

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
  <span data-tracereports-mask>Cliente: Juan Pérez</span>
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
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK", ".rut-cliente")
    reset_env_file()
    page.set_content('<section id="ficha"><h2>Pedido</h2><p>Cliente: <span data-tracereports-mask>Juana Pérez Soto</span></p>'
                     '<p>RUT <b class="rut-cliente">9.876.543-2</b></p><button id="guardar">Guardar</button></section>')
    try:
        snap = capturar_dom(page)
        raw = json.dumps(snap, ensure_ascii=False)
        assert "Juana" not in raw and "9.876.543-2" not in raw
        ficha = next(e for e in snap["elements"] if e["id"] == "ficha")
        assert "Pedido" in ficha["text"] and "•••" in ficha["text"] and "Guardar" in ficha["text"]
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


def test_layers_overflow_and_line_breaks_for_the_inspector(page):
    # el Inspector pinta las capas (un modal encima de la página), recorta los paneles con scroll y
    # respeta los saltos de línea: necesita zIndex, overflow y los <br> del texto
    page.set_content('<div id="panel" style="height:40px;overflow-y:auto"><p>06-11-2026 16:43<br>Control general</p></div>'
                     '<div class="modal" style="position:fixed;top:0;z-index:1050">Información</div>')
    try:
        nodes = capturar_dom(page)["nodes"]
        panel = next(n for n in nodes if n["attrs"].get("id") == "panel")
        modal = next(n for n in nodes if n["attrs"].get("class") == "modal")
        item = next(n for n in nodes if n["tag"] == "p")
        assert panel["style"]["overflowY"] == "auto" and modal["style"]["zIndex"] == "1050" and modal["style"]["position"] == "fixed"
        assert item["text"] == "06-11-2026 16:43\nControl general"
    finally:
        page.set_content(PAGE)


def test_icons_travel_as_the_page_drew_them(page, monkeypatch):
    # Inspector "realista": los íconos de fuente (::before de Font Awesome, ligaduras de Material), los
    # <svg> en línea (con sus colores y sus <use>) y las imágenes chicas viajan; lo tapado no.
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK", ".secreto")
    page.set_content(
        '<style>.fa-plus::before{content:"\2795";color:rgb(25, 118, 210)} .req::after{content:" *"} '
        '.material-icons{font-family:"Material Icons", monospace} .secreto::before{content:"\2605"}</style>'
        '<svg width="0" height="0" style="position:absolute"><symbol id="ic" viewBox="0 0 24 24"><path fill="currentColor" d="M2 2h20v20H2z"/></symbol></svg>'
        '<button><i class="fa-plus"></i>Agregar</button><label class="req">Motivo</label>'
        '<span class="material-icons">home</span>'
        '<svg class="s" style="width:20px;height:20px;color:rgb(106, 27, 154)"><use href="#ic"/></svg>'
        '<img id="png" width="16" height="16" src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==">'
        '<span class="secreto">dato</span>')
    page.wait_for_function("document.getElementById('png').complete")
    try:
        snap = capturar_dom(page)
        nodes, glyphs = snap["nodes"], snap.get("glyphs") or {}
        by = lambda pred: next(n for n in nodes if pred(n))  # noqa: E731
        plus = by(lambda n: n["attrs"].get("class") == "fa-plus")
        assert plus["icons"][0]["pos"] == "before" and glyphs[plus["icons"][0]["g"]].startswith("data:image/png;base64,")
        assert by(lambda n: n["attrs"].get("class") == "req")["icons"][0]["pos"] == "after"
        home = by(lambda n: n["attrs"].get("class") == "material-icons")
        assert home["text"] == "home" and home["icons"][0]["pos"] == "text", "the ligature travels as its icon (and its text, for XPath)"
        svg = by(lambda n: n["tag"] == "svg" and n["attrs"].get("class") == "s")["svg"]
        assert "<use" not in svg and 'd="M2 2h20v20H2z"' in svg, "the <use> is inlined"
        assert "currentColor" not in svg and "rgb(106, 27, 154)" in svg, "colors resolved: as an image currentColor would be black"
        assert by(lambda n: n["tag"] == "img")["icons"][0]["pos"] == "img"
        assert "icons" not in by(lambda n: n["attrs"].get("class") == "secreto"), "what is masked keeps no icon either"
        assert len({g for n in nodes for g in [i["g"] for i in n.get("icons", [])]}) == len(glyphs), "one glyph per distinct icon"
    finally:
        page.set_content(PAGE)


def test_svg_keeps_its_gradients_clips_and_masks(page):
    # Un skeleton de carga tipo content-loader: un <rect> completo recortado a las barras (clipPath) y
    # pintado con un gradiente animado. Viaja con sus <defs> y referencias url(#id); sin pintura copiada
    # en <stop>/<animate> (ahí fill significa freeze/remove) y sin animaciones. La forma absoluta
    # url("https://host/página#id") pasa a url(#id), y un gradiente que vive en otro <svg> de la página
    # (un sprite oculto) se copia adentro.
    page.set_content(
        '<svg width="0" height="0" style="position:absolute"><defs><linearGradient id="shared"><stop offset="0" stop-color="#123456"/></linearGradient></defs></svg>'
        '<svg class="sk" viewBox="0 0 400 95" preserveAspectRatio="none" style="width:400px;height:95px">'
        '<rect x="0" y="0" width="400" height="95" clip-path="url(#clipA)" style="fill: url(#gradA)"></rect>'
        '<defs><clipPath id="clipA"><rect x="0" y="10" rx="3" width="300" height="12"></rect></clipPath>'
        '<linearGradient id="gradA"><stop offset="0%" stop-color="#f3f3f3"><animate attributeName="offset" values="-2; 1" dur="2s" repeatCount="indefinite"/></stop>'
        '<stop offset="100%" stop-color="#ecebeb"/></linearGradient></defs></svg>'
        '<svg class="abs" viewBox="0 0 10 10" style="width:10px;height:10px"><rect width="10" height="10" id="r"></rect></svg>'
        '<svg class="sprite" viewBox="0 0 10 10" style="width:10px;height:10px"><rect width="10" height="10" fill="url(#shared)"></rect></svg>')
    page.evaluate("""() => document.querySelector('.abs rect').setAttribute('fill', 'url("' + location.href + '#r2")')""")
    page.evaluate("""() => { const g = document.createElementNS('http://www.w3.org/2000/svg', 'linearGradient'); g.id = 'r2';
        document.querySelector('.abs').appendChild(g); }""")
    try:
        nodes = capturar_dom(page)["nodes"]
        by = lambda cls: next(n for n in nodes if n["tag"] == "svg" and n["attrs"].get("class") == cls)["svg"]  # noqa: E731
        sk = by("sk")
        assert '<clipPath id="clipA"' in sk and '<linearGradient id="gradA"' in sk and 'clip-path="url(#clipA)"' in sk
        assert 'url(&quot;#gradA&quot;)' in sk or 'url(#gradA)' in sk, sk
        assert "animate" not in sk, "a snapshot is one moment"
        assert not re.search(r'<(stop|defs|clipPath|linearGradient)[^>]* fill=', sk), "no paint copied to definitions"
        assert 'fill="url(#r2)"' in by("abs"), "the absolute url to an element of the same svg becomes url(#id)"
        sprite = by("sprite")
        assert '<linearGradient id="shared"' in sprite and 'fill="url(#shared)"' in sprite, sprite
    finally:
        page.set_content(PAGE)
