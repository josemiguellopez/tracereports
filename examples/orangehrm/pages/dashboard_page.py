import time

from locators.dashboard_locator import DashboardLocator
from pages.login_page import Login
from utils.tracereports_context import TraceReportsContext
from utils.function import By, Function


class Dashboard:
    """Page Object del Dashboard, el menú lateral y el menú de usuario de OrangeHRM."""

    def __init__(self, page, logger, error_list, data_test, warning_list=None):
        self.page = page
        self.logger = logger
        self.error_list = error_list
        self.data_test = data_test
        self.warning_list = warning_list if warning_list is not None else []
        self.f = Function(page, logger, error_list)
        self.loc = DashboardLocator

    def validar_dashboard_visible(self, timeout_ms: int = 30000) -> bool:
        return self.validar_modulo_activo("Dashboard", timeout_ms)

    def validar_modulo_activo(self, modulo: str, timeout_ms: int = 30000) -> bool:
        if not self.f.wait_element(By.CSS, self.loc.CSS.LBL_TITULO_MODULO, wait_type="visible", timeout=timeout_ms):
            return self.f.fallo_validacion(
                f"No se cargó el módulo '{modulo}'",
                esperado=f"título de módulo '{modulo}' visible",
                encontrado="cabecera de módulo no visible",
                screenshot_name=f"error_modulo_{modulo.lower()}_no_visible_{int(time.time())}",
            )
        titulo = self.f.get_text_from_element(By.CSS, self.loc.CSS.LBL_TITULO_MODULO)
        return self.f.validate_text_by_equals(modulo, titulo, f"Título del módulo {modulo}")

    # ─── scraping ──────────────────────────────────────────────────────────
    def obtener_nombre_usuario_header(self) -> str:
        """Dato personal: el llamador no debe loguearlo literal, solo compararlo/usar el retorno."""
        nombre = self.f.get_text_from_element(By.CSS, self.loc.CSS.LBL_NOMBRE_USUARIO)
        TraceReportsContext.log_info(
            "Evidencia de leer nombre de usuario autenticado desde header",
            page=self.page,
            screenshot_name=f"nombre_usuario_header_leido_{int(time.time())}",
        )
        return nombre

    def obtener_widgets(self) -> list:
        """Lee los títulos de los widgets del Dashboard (se cargan asíncronamente)."""
        self.f.wait_element(By.CSS, self.loc.CSS.LIST_WIDGETS, wait_type="visible")
        widgets = self.f.get_all_texts(By.CSS, self.loc.CSS.LIST_WIDGETS)
        TraceReportsContext.log_info(
            f"Evidencia de widgets del Dashboard ({len(widgets)}): {', '.join(widgets)}",
            page=self.page,
            screenshot_name=f"widgets_dashboard_{int(time.time())}",
        )
        return widgets

    def obtener_opciones_menu(self) -> list:
        opciones = self.f.get_all_texts(By.CSS, self.loc.CSS.LIST_MENU_LATERAL)
        TraceReportsContext.log_info(f"Opciones del menú lateral ({len(opciones)}): {', '.join(opciones)}")
        return opciones

    def validar_widgets_esperados(self, widgets_encontrados: list, widgets_esperados: list) -> bool:
        faltantes = [w for w in widgets_esperados if w not in widgets_encontrados]
        if faltantes:
            return self.f.fallo_validacion(
                "Faltan widgets en el Dashboard",
                esperado=", ".join(widgets_esperados),
                encontrado=f"faltan: {', '.join(faltantes)}",
                screenshot_name=f"error_widgets_faltantes_{int(time.time())}",
            )
        TraceReportsContext.log_pass(f"Los {len(widgets_esperados)} widgets esperados están presentes")
        return True

    # ─── navegación ────────────────────────────────────────────────────────
    def ir_a_modulo(self, modulo: str) -> bool:
        """Resultado esperado: el título del módulo cambia a `modulo`."""
        self.f.find_element_click(By.XPATH, self.loc.XPATH.menu_lateral(modulo))
        ok = self.validar_modulo_activo(modulo)
        if ok:
            TraceReportsContext.log_info(
                f"Evidencia de navegar al módulo '{modulo}'",
                page=self.page,
                screenshot_name=f"modulo_{modulo.lower()}_abierto_{int(time.time())}",
            )
        return ok

    def abrir_menu_usuario(self) -> bool:
        """Resultado esperado: el menú desplegado muestra la opción 'Logout'."""
        self.f.find_element_click(By.CSS, self.loc.CSS.BTN_MENU_USUARIO)
        TraceReportsContext.log_info(
            "Evidencia de abrir el menú de usuario",
            page=self.page,
            screenshot_name=f"menu_usuario_abierto_{int(time.time())}",
        )
        return self.validar_menu_usuario_abierto()

    def validar_menu_usuario_abierto(self) -> bool:
        if not self.f.wait_element(By.XPATH, self.loc.XPATH.BTN_CERRAR_SESION, wait_type="visible"):
            return self.f.fallo_validacion(
                "El menú de usuario no mostró la opción 'Logout'",
                esperado="menú de usuario desplegado con 'Logout' visible",
                encontrado="opción 'Logout' no visible",
                screenshot_name=f"error_menu_usuario_no_abierto_{int(time.time())}",
            )
        return True

    def hacer_clic_cerrar_sesion(self) -> bool:
        """Requiere abrir_menu_usuario() antes. Resultado esperado: formulario de login vacío
        — se reutiliza Login.validar_formulario_login_vacio (pantalla de destino)."""
        self.f.find_element_click(By.XPATH, self.loc.XPATH.BTN_CERRAR_SESION)
        login = Login(self.page, self.logger, self.error_list, self.data_test, self.warning_list)
        return login.validar_formulario_login_vacio()
