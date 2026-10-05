"""
Helpers de interacción sobre Playwright — versión reducida de Utils.function.Function.

Las acciones (click, send_text) dejan propagar el TimeoutError de Playwright: el Test lo
captura, lo reporta con stack trace y TraceReports lo clasifica con IA. Las lecturas y
validaciones devuelven valores/bool y registran el fallo con fallo_validacion().
"""

from playwright.sync_api import TimeoutError as PlaywrightTimeoutError

from utils.tracereports_context import TraceReportsContext


class By:
    XPATH = "xpath"
    CSS = "css"
    ID = "id"
    NAME = "name"


class Function:
    def __init__(self, page, logger, error_list, timeout_ms=15000):
        self.page = page
        self.logger = logger
        self.error_list = error_list
        self.timeout_ms = timeout_ms

    # ─── localización ──────────────────────────────────────────────────────
    @staticmethod
    def _selector(by, value):
        return {
            By.XPATH: f"xpath={value}",
            By.CSS: value,
            By.ID: f"#{value}",
            By.NAME: f"[name='{value}']",
        }[by]

    def locator(self, by, value):
        return self.page.locator(self._selector(by, value))

    # ─── esperas ───────────────────────────────────────────────────────────
    def wait_element(self, by, value, wait_type="visible", timeout=None) -> bool:
        """wait_type: visible | hidden | attached | detached. Retorna False en timeout."""
        try:
            self.locator(by, value).first.wait_for(state=wait_type, timeout=timeout or self.timeout_ms)
            return True
        except PlaywrightTimeoutError:
            self.logger.warning(f"Timeout esperando '{value}' ({wait_type})")
            return False

    def wait_until_not_element_visible(self, by, value, timeout=None) -> bool:
        return self.wait_element(by, value, wait_type="hidden", timeout=timeout)

    def wait_text_change(self, xpath, texto_anterior, timeout=None) -> bool:
        """Espera a que el texto del elemento (XPath) cambie respecto a `texto_anterior`
        — ej. el contador "(115) Records Found" tras ejecutar una búsqueda."""
        try:
            self.page.wait_for_function(
                """([xp, prev]) => {
                    const el = document.evaluate(xp, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
                    return el !== null && el.innerText.trim() !== prev;
                }""",
                arg=[xpath, texto_anterior],
                timeout=timeout or self.timeout_ms,
            )
            return True
        except PlaywrightTimeoutError:
            return False

    # ─── acciones ──────────────────────────────────────────────────────────
    def find_element_click(self, by, value):
        self.locator(by, value).first.click(timeout=self.timeout_ms)

    def send_text(self, by, value, text, clear=True):
        loc = self.locator(by, value).first
        if clear:
            loc.fill("", timeout=self.timeout_ms)
        loc.fill(str(text), timeout=self.timeout_ms)

    # ─── lecturas ──────────────────────────────────────────────────────────
    def get_text_from_element(self, by, value) -> str:
        try:
            return self.locator(by, value).first.inner_text(timeout=self.timeout_ms).strip()
        except PlaywrightTimeoutError:
            return "No se encontró el elemento"

    def get_value_from_element(self, by, value) -> str:
        try:
            return self.locator(by, value).first.input_value(timeout=self.timeout_ms)
        except PlaywrightTimeoutError:
            return ""

    def get_all_texts(self, by, value) -> list:
        return [t.strip() for t in self.locator(by, value).all_inner_texts()]

    def find_elements_total(self, by, value) -> int:
        return self.locator(by, value).count()

    # ─── validaciones ──────────────────────────────────────────────────────
    def validate_text_by_equals(self, esperado, encontrado, descripcion) -> bool:
        if str(esperado) != str(encontrado):
            return self.fallo_validacion(
                f"{descripcion}: el valor no coincide",
                esperado=f"'{esperado}'",
                encontrado=f"'{encontrado}'",
                screenshot_name=f"error_{descripcion.replace(' ', '_').lower()}",
            )
        return True

    def validate_text_by_contains(self, esperado, encontrado, descripcion) -> bool:
        if str(esperado) not in str(encontrado):
            return self.fallo_validacion(
                f"{descripcion}: el texto esperado no está contenido",
                esperado=f"contiene '{esperado}'",
                encontrado=f"'{encontrado}'",
                screenshot_name=f"error_{descripcion.replace(' ', '_').lower()}",
            )
        return True

    def fallo_validacion(self, mensaje, esperado, encontrado, screenshot_name) -> bool:
        """Registra el fallo (error_list + step FAIL con screenshot) y retorna False."""
        detalle = f"{mensaje} | esperado: {esperado} | encontrado: {encontrado}"
        self.logger.error(detalle)
        self.error_list.append(detalle)
        TraceReportsContext.log_error(detalle, page=self.page, screenshot_name=screenshot_name)
        return False
