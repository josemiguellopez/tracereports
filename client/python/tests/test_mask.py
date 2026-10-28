"""Capturas con los datos sensibles tapados (mask.py) y política TRACEREPORTS_SCREENSHOTS."""

import base64
import json
import os

import pytest

from tracereports import TraceReports
from tracereports import mask
from tracereports.mask import MASK_COLOR, mask_selector, playwright_screenshot, screenshot_allowed, selenium_screenshot


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch):
    for k in ("TRACEREPORTS_SCREENSHOT_MASK", "TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS", "TRACEREPORTS_SCREENSHOTS"):
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setenv("TRACEREPORTS_ENV_FILE", "off")
    monkeypatch.setenv("TRACEREPORTS_OFFLINE_REPORT", "0")


def test_mask_selector_defaults_and_extra(monkeypatch):
    assert mask_selector() == "input[type=password], [data-tracereports-mask]"
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK", "input[name=rut], .tarjeta,")
    assert mask_selector() == "input[type=password], [data-tracereports-mask], input[name=rut], .tarjeta"
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS", "off")
    assert mask_selector() == "input[name=rut], .tarjeta"
    monkeypatch.delenv("TRACEREPORTS_SCREENSHOT_MASK")
    assert mask_selector() == ""


@pytest.mark.parametrize("policy,status,allowed", [
    ("", "INFO", True), ("all", "INFO", True), ("garbage", "INFO", True),
    ("failures", "INFO", False), ("failures", "FAIL", True), ("failures", "fail", True),
    ("off", "INFO", False), ("off", "FAIL", False),
])
def test_screenshot_policy(monkeypatch, policy, status, allowed):
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOTS", policy)
    assert screenshot_allowed(status) is allowed


class FakeLocator:
    def __init__(self, selector):
        self.selector = selector


class FakePage:
    def __init__(self):
        self.kwargs = None

    def locator(self, selector):
        return FakeLocator(selector)

    def screenshot(self, **kwargs):
        self.kwargs = kwargs
        return b"png"


def test_playwright_screenshot_passes_the_mask(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK", ".rut")
    page = FakePage()
    assert playwright_screenshot(page, timeout=5000) == b"png"
    assert [m.selector for m in page.kwargs["mask"]] == ["input[type=password], [data-tracereports-mask], .rut"]
    assert page.kwargs["mask_color"] == MASK_COLOR and page.kwargs["timeout"] == 5000
    # sin nada que tapar no se pasa mask (Playwright viejo sin la opción también funciona)
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS", "off")
    monkeypatch.delenv("TRACEREPORTS_SCREENSHOT_MASK")
    page = FakePage()
    playwright_screenshot(page)
    assert "mask" not in page.kwargs


class FakeDriver:
    """Registra el orden: tapar → capturar → destapar."""

    def __init__(self, shot_error=None, cover_error=None):
        self.calls = []
        self.shot_error, self.cover_error = shot_error, cover_error

    def execute_script(self, script, *args):
        kind = "cover" if "createElement" in script else "uncover"
        self.calls.append(kind)
        if kind == "cover" and self.cover_error:
            raise self.cover_error
        return 1

    def get_screenshot_as_png(self):
        self.calls.append("shot")
        if self.shot_error:
            raise self.shot_error
        return b"png"


def test_selenium_covers_then_uncovers():
    d = FakeDriver()
    assert selenium_screenshot(d) == b"png"
    assert d.calls == ["cover", "shot", "uncover"]


def test_selenium_uncovers_even_if_the_screenshot_fails():
    d = FakeDriver(shot_error=RuntimeError("browser gone"))
    assert selenium_screenshot(d) is None
    assert d.calls == ["cover", "shot", "uncover"]


def test_selenium_takes_no_screenshot_when_it_cannot_cover():
    d = FakeDriver(cover_error=RuntimeError("invalid selector"))
    assert selenium_screenshot(d) is None
    assert "shot" not in d.calls  # sin máscara no hay captura


def test_selenium_without_mask_just_captures(monkeypatch):
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS", "off")
    d = FakeDriver()
    assert selenium_screenshot(d) == b"png" and d.calls == ["shot"]


def _recorded(tmp_path):
    events = []
    for name in os.listdir(tmp_path):
        if name.startswith("events-"):
            with open(tmp_path / name, encoding="utf-8") as f:
                events += [json.loads(x) for x in f if x.strip()]
    return events


@pytest.mark.parametrize("policy,sent", [("all", 2), ("failures", 1), ("off", 0)])
def test_attach_screenshot_follows_the_policy(tmp_path, monkeypatch, policy, sent):
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOTS", policy)
    cr = TraceReports(offline="always", offline_dir=str(tmp_path))
    cr.start_run("capturas")
    tid = cr.start_test("t")
    png = b"\x89PNG\r\n\x1a\n" + b"0" * 16
    cr.attach_screenshot(png, "paso", status="INFO", test_id=tid)
    cr.attach_screenshot(png, "fallo", status="FAIL", test_id=tid)
    cr.end_test(status="FAIL")
    cr.end_run()
    shots = [e for e in _recorded(tmp_path) if e["path"].endswith("/screenshot")]
    assert len(shots) == sent, shots


# ─── navegador real: la zona queda tapada en los píxeles ────────────────────

PAGE = """<html><body style="margin:0;background:#fff">
<input type="password" value="SECRETO" style="position:absolute;left:10px;top:10px;width:200px;height:40px">
<div data-tracereports-mask style="position:absolute;left:10px;top:70px;width:200px;height:40px;background:#0f0">12.345.678-9</div>
<div class="rut" style="position:absolute;left:10px;top:130px;width:200px;height:40px;background:#00f">9.876.543-2</div>
<div style="position:absolute;left:300px;top:10px;width:200px;height:40px;background:#0f0">visible</div>
</body></html>"""

#: Centro de cada caja: password, data-tracereports-mask, .rut (por env) y una sin tapar.
POINTS = {"password": (110, 30), "attr": (110, 90), "rut": (110, 150), "visible": (400, 30)}

# lee un píxel del PNG en un canvas: sin dependencias de imágenes
_PIXEL_JS = """
async ([b64, points]) => {
  const img = new Image();
  img.src = "data:image/png;base64," + b64;
  await img.decode();
  const c = document.createElement("canvas");
  c.width = img.width; c.height = img.height;
  const ctx = c.getContext("2d");
  ctx.drawImage(img, 0, 0);
  const out = {};
  for (const [k, [x, y]] of Object.entries(points)) out[k] = Array.from(ctx.getImageData(x, y, 1, 1).data.slice(0, 3));
  return out;
}
"""

MAGENTA = [255, 0, 255]


def test_playwright_mask_covers_the_pixels(monkeypatch):
    sync_api = pytest.importorskip("playwright.sync_api")
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK", ".rut")
    try:
        pw = sync_api.sync_playwright().start()
        browser = pw.chromium.launch()
    except Exception as err:
        pytest.skip(f"chromium not available: {err}")
    try:
        page = browser.new_page(viewport={"width": 600, "height": 200})
        page.set_content(PAGE)
        png = playwright_screenshot(page)
        px = page.evaluate(_PIXEL_JS, [base64.b64encode(png).decode(), POINTS])
        assert px["password"] == MAGENTA and px["attr"] == MAGENTA and px["rut"] == MAGENTA, px
        assert px["visible"] == [0, 255, 0], px  # lo demás no se toca
    finally:
        browser.close()
        pw.stop()


def test_selenium_mask_covers_the_pixels_and_restores_the_dom(monkeypatch):
    webdriver = pytest.importorskip("selenium.webdriver")
    monkeypatch.setenv("TRACEREPORTS_SCREENSHOT_MASK", ".rut")
    opts = webdriver.ChromeOptions()
    for a in ("--headless=new", "--window-size=800,600", "--force-device-scale-factor=1", "--no-sandbox"):
        opts.add_argument(a)
    try:
        driver = webdriver.Chrome(options=opts)
    except Exception as err:
        pytest.skip(f"chrome/chromedriver not available: {err}")
    try:
        driver.get("data:text/html;base64," + base64.b64encode(PAGE.encode()).decode())
        before = driver.execute_script("return document.documentElement.outerHTML")
        png = selenium_screenshot(driver)
        assert driver.execute_script("return document.documentElement.outerHTML") == before  # el DOM queda igual
        px = driver.execute_async_script(
            "const done = arguments[arguments.length - 1];"
            "(" + _PIXEL_JS + ")([arguments[0], arguments[1]]).then(done);",
            base64.b64encode(png).decode(), POINTS)
        assert px["password"] == MAGENTA and px["attr"] == MAGENTA and px["rut"] == MAGENTA, px
        assert px["visible"] == [0, 255, 0], px
    finally:
        driver.quit()


def test_mask_module_has_no_selenium_or_playwright_import():
    # mask.py se importa siempre (client.py): no puede exigir Selenium ni Playwright
    src = open(mask.__file__, encoding="utf-8").read()
    assert "import selenium" not in src and "import playwright" not in src
