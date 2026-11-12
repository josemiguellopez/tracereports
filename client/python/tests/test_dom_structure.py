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
                "TRACEREPORTS_SCREENSHOTS", "TRACEREPORTS_TOKEN", "TRACEREPORTS_SYNC", "TRACEREPORTS_DOM_MODE"):
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
    assert STRUCT_SCRIPT in calls[0][0] and "data-tracereports-mask" in calls[0][1][0]["mask"]
    assert calls[0][1][0]["html"] is False, "the HTML mode is off unless asked"
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


def test_vertical_tabs_travel_with_writing_mode_and_turn(page):
    # Pestañas verticales: con writing-mode (la caja ya es alta) o con rotate (la caja en pantalla es la
    # girada): viaja el giro acumulado, sin traslación, y el tamaño de layout. Un translate no es un giro.
    page.set_content(
        '<div class="wm" style="writing-mode:vertical-rl;width:47px;height:105px">Historia</div>'
        '<div class="rot" style="position:absolute;top:200px;left:200px;width:90px;height:24px;transform:translateX(5px) rotate(90deg)">'
        '<span class="inner">Formularios</span></div>'
        '<div class="mv" style="transform:translateY(-10px);width:50px;height:20px">Mover</div>')
    try:
        nodes = capturar_dom(page)["nodes"]
        by = lambda cls: next(n for n in nodes if n["attrs"].get("class") == cls)  # noqa: E731
        assert by("wm")["style"]["writingMode"] == "vertical-rl" and "tf" not in by("wm")
        rot = by("rot")
        assert rot["tf"] == "matrix(0, 1, -1, 0, 0, 0)" and (rot["lw"], rot["lh"]) == (90, 24), rot
        assert (rot["w"], rot["h"]) == (24, 90) and "tx" not in rot, "the box on screen is the turned one"
        assert by("inner")["tf"] == "matrix(0, 1, -1, 0, 0, 0)", "a child inherits its parent's turn"
        assert "tf" not in by("mv"), "a translate is already in the box"
    finally:
        page.set_content(PAGE)


def test_borders_shadows_gradients_and_ellipsis_travel(page):
    # La línea bajo un encabezado, la sombra de una tarjeta, un fondo degradado y un texto cortado con «…».
    # Lo que vale lo de siempre no viaja (sin sombra, sin corte); una imagen de fondo nunca.
    page.set_content(
        '<div class="head" style="border-bottom:2px solid rgb(51, 117, 194);box-shadow:0 1px 3px rgba(0,0,0,.2);'
        'background-image:linear-gradient(90deg, rgb(243, 243, 243), rgb(236, 235, 235))">Encabezado</div>'
        '<span class="cut" style="display:block;width:60px;overflow:hidden;white-space:nowrap;text-overflow:ellipsis">Atención no completada</span>'
        '<div class="pic" style="background-image:url(data:image/png;base64,iVBORw0KGgo=)">x</div>')
    try:
        nodes = capturar_dom(page)["nodes"]
        by = lambda cls: next(n for n in nodes if n["attrs"].get("class") == cls)["style"]  # noqa: E731
        head = by("head")
        assert head["borderBottomWidth"] == "2px" and head["borderBottomColor"] == "rgb(51, 117, 194)"
        assert head["borderLeftWidth"] == "0px" and "borderLeftColor" not in head
        assert head["boxShadow"].startswith("rgba(0, 0, 0, 0.2) 0px 1px 3px") and head["backgroundImage"].startswith("linear-gradient(90deg")
        assert "textOverflow" not in head and "whiteSpace" not in head
        cut = by("cut")
        assert cut["textOverflow"] == "ellipsis" and cut["whiteSpace"] == "nowrap"
        assert "backgroundImage" not in by("pic"), "an image never travels"
    finally:
        page.set_content(PAGE)


def test_controls_show_their_option_and_placeholder_never_what_was_typed(page):
    # El select muestra la opción elegida (las opciones ya viajan); un botón <input>, su rótulo; un campo
    # vacío, su placeholder con su color; uno escrito, solo que no está vacío. Lo tapado no muestra nada.
    page.set_content(
        '<style>.dx::placeholder{color:rgb(108, 117, 125)}</style>'
        '<select class="per"><option>6 Meses</option><option selected>12 Meses</option></select>'
        '<input type="submit" class="go" value="Buscar">'
        '<input class="dx" placeholder="Diagnóstico">'
        '<input class="typed" placeholder="Nombre" value="secreto-escrito">'
        '<select class="hid" data-tracereports-mask><option selected>Dato tapado</option></select>')
    try:
        nodes = capturar_dom(page)["nodes"]
        by = lambda cls: next(n for n in nodes if n["attrs"].get("class") == cls)  # noqa: E731
        assert by("per")["shown"] == "12 Meses" and by("per")["text"] == ""
        assert by("go")["shown"] == "Buscar"
        dx = by("dx")
        assert "filled" not in dx and dx["style"]["placeholderColor"] == "rgb(108, 117, 125)"
        typed = by("typed")
        assert typed["filled"] is True and "secreto-escrito" not in json.dumps(nodes)
        assert "shown" not in by("hid")
    finally:
        page.set_content(PAGE)


def test_scroll_travels_so_the_screenshot_lines_up(page):
    # La captura muestra la página desde donde estaba la ventana: el Inspector superpone y compara esa parte.
    page.set_content('<div style="height:3000px">alta</div>')
    try:
        assert "scroll" not in capturar_dom(page)
        page.evaluate("window.scrollTo(0, 300)")
        assert capturar_dom(page)["scroll"] == {"x": 0, "y": 300}
    finally:
        page.set_content(PAGE)


HTML_PAGE = """<html lang="es"><head><style>.card{box-shadow:0 1px 3px rgba(0,0,0,.2)}</style>
<style media="print">.card{display:none}</style><script>window.x = 1</script></head><body class="app">
<div class="card" id="ficha" onclick="steal()"><h5>Vacunas</h5>
 <input id="dx" placeholder="Diagnóstico" value="escrito-por-alguien"><input type="password" value="clave-secreta">
 <input type="hidden" name="csrf" value="tok-123"><input type="checkbox" checked><input type="submit" value="Guardar">
 <textarea>nota privada</textarea><select><option>6 Meses</option><option selected>12 Meses</option></select>
 <span data-tracereports-mask>Juan Pérez</span>
 <img src="https://app.example/logo.png" style="width:40px;height:20px">
 <iframe src="about:blank" style="width:100px;height:50px"></iframe><canvas width="20" height="10"></canvas>
 <svg viewBox="0 0 10 10" width="10" height="10"><defs><linearGradient id="g"><stop offset="0"/></linearGradient></defs><rect fill="url(#g)" width="10" height="10"/></svg>
 <div class="list" style="height:40px;overflow:auto"><div style="height:400px">filas</div></div>
 <my-tag></my-tag></div></body></html>"""


def test_html_mode_sends_the_page_and_its_sheets(page, monkeypatch):
    # TRACEREPORTS_DOM_MODE=html: además de la estructura, la página misma (cada elemento con su nodo) y
    # sus hojas de estilo por hash. Lo escrito, lo tapado, los scripts y lo que pide la sesión no viajan.
    monkeypatch.setenv("TRACEREPORTS_DOM_MODE", "html")
    reset_env_file()
    page.set_content(HTML_PAGE)
    page.evaluate("""() => { const h = document.querySelector('my-tag').attachShadow({ mode: 'open' });
        h.innerHTML = '<style>p > b{color:red}</style><b>sombra</b><style>a < b</style>'; document.querySelector('.list').scrollTop = 120; }""")
    try:
        snap = capturar_dom(page)
        html, nodes = snap["html"], snap["nodes"]
        doc = html["doc"]
        assert doc.startswith('<html lang="es"><head></head><body class="app"')
        for bad in ("<script", "steal()", "onclick", "escrito-por-alguien", "clave-secreta", "tok-123", "nota privada", "Juan Pérez", "app.example/logo.png", "<iframe"):
            assert bad not in doc, bad
        for good in ('placeholder="Diagnóstico"', 'value="•••"', 'value="Guardar"',
                     "<textarea", "•••</textarea>", "selected>12 Meses</option>", 'data-tr-ph="img"', 'data-tr-ph="iframe"',
                     '<linearGradient id="g">', 'data-tr-scroll="0,120"', '<template shadowrootmode="open"><style>p > b{color:red}</style><b', r'<style>a \3c  b</style>'):
            assert good in doc, good
        assert re.search(r'<input type="checkbox"[^>]* checked>', doc)
        assert '<canvas' not in doc and ('src="data:image/png' in doc), "a canvas travels as its picture"
        # cada elemento de la página apunta a su nodo de la estructura
        ficha = next(n for n in nodes if n["attrs"].get("id") == "ficha")
        assert 'id="ficha" data-tr-i="%d"' % ficha["i"] in doc
        # las hojas por su hash, en el orden del documento; la de impresión con su @media
        assert len(html["css"]) == 2 and set(html["css"]) == set(html["sheets"])
        assert "box-shadow" in html["sheets"][html["css"][0]] and html["sheets"][html["css"][1]].startswith("@media print")
        assert html["dpr"] >= 1
    finally:
        page.set_content(PAGE)


def test_structure_mode_does_not_send_the_page(page):
    page.set_content(HTML_PAGE)
    try:
        assert "html" not in capturar_dom(page)
    finally:
        page.set_content(PAGE)


def test_each_sheet_travels_once_per_process(monkeypatch):
    srv = FakeServer()
    try:
        cr = TraceReports(srv.url)
        cr.run_id = 7
        snap = lambda: {"url": "https://app/", "nodes": [], "elements": [], "html": {"doc": "<html></html>", "css": ["a" * 64, "b" * 64],  # noqa: E731
                                                                                  "sheets": {"a" * 64: "x{}", "b" * 64: "y{}"}}}
        cr.attach_dom(snap(), test_id=11)
        cr.attach_dom(snap(), test_id=11)
        assert cr.flush(10) == 0
        doms = [json.loads(b) for m, p, k, b in srv.requests if p.endswith("/dom")]
        assert [sorted(d["html"]["sheets"]) for d in doms] == [["a" * 64, "b" * 64], []]
        assert all(d["html"]["css"] == ["a" * 64, "b" * 64] for d in doms), "the page still says which sheets it uses"
    finally:
        srv.close()
