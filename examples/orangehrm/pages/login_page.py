import time

from locators.login_locator import LoginLocator
from utils.tracereports_context import TraceReportsContext
from utils.function import By, Function


class Login:
    """
    Page Object de la pantalla de login de OrangeHRM.

    Contrato: cada método de acción termina validando su resultado esperado y retorna
    bool — el Test solo evalúa ese retorno con _step_require. Los validar_* son el
    chequeo final que cada acción reutiliza.
    """

    def __init__(self, page, logger, error_list, data_test, warning_list=None):
        self.page = page
        self.logger = logger
        self.error_list = error_list
        self.data_test = data_test
        self.warning_list = warning_list if warning_list is not None else []
        self.f = Function(page, logger, error_list)
        self.loc = LoginLocator

    def abrir_navegador(self, url: str) -> bool:
        """Resultado esperado: formulario de login visible y vacío."""
        self.page.goto(url, wait_until="domcontentloaded", timeout=60000)
        return self.validar_formulario_login_vacio()

    def validar_formulario_login_vacio(self) -> bool:
        if not self.f.wait_element(By.NAME, self.loc.NAME.TXT_USERNAME, wait_type="visible", timeout=30000):
            return self.f.fallo_validacion(
                "No se mostró el formulario de login",
                esperado="campo 'Username' visible",
                encontrado="formulario no visible",
                screenshot_name=f"error_login_no_visible_{int(time.time())}",
            )
        usuario = self.f.get_value_from_element(By.NAME, self.loc.NAME.TXT_USERNAME)
        clave = self.f.get_value_from_element(By.NAME, self.loc.NAME.TXT_PASSWORD)
        if usuario or clave:
            return self.f.fallo_validacion(
                "El formulario de login no está vacío",
                esperado="Username y Password vacíos",
                encontrado=f"Username {'con valor' if usuario else 'vacío'}, Password {'con valor' if clave else 'vacío'}",
                screenshot_name=f"error_login_no_vacio_{int(time.time())}",
            )
        TraceReportsContext.log_info(
            "Evidencia de formulario de login visible y vacío",
            page=self.page,
            screenshot_name=f"login_formulario_vacio_{int(time.time())}",
        )
        return True

    def ingresar_credenciales(self, usuario: str, clave: str) -> bool:
        """Resultado esperado: ambos campos conservan lo ingresado. La clave nunca se loguea."""
        self.f.send_text(By.NAME, self.loc.NAME.TXT_USERNAME, usuario)
        self.f.send_text(By.NAME, self.loc.NAME.TXT_PASSWORD, clave)
        TraceReportsContext.log_info(
            f"Evidencia de ingresar credenciales del usuario '{usuario}'",
            page=self.page,
            screenshot_name=f"credenciales_ingresadas_{int(time.time())}",
        )
        return self.validar_credenciales_ingresadas(usuario, clave)

    def validar_credenciales_ingresadas(self, usuario: str, clave: str) -> bool:
        usuario_actual = self.f.get_value_from_element(By.NAME, self.loc.NAME.TXT_USERNAME)
        clave_actual = self.f.get_value_from_element(By.NAME, self.loc.NAME.TXT_PASSWORD)
        if not self.f.validate_text_by_equals(usuario, usuario_actual, "Usuario ingresado"):
            return False
        if clave_actual != clave:
            return self.f.fallo_validacion(
                "La contraseña no se conservó en el campo",
                esperado="contraseña ingresada",
                encontrado="valor distinto (no se muestra por seguridad)",
                screenshot_name=f"error_password_no_conservada_{int(time.time())}",
            )
        return True

    def hacer_clic_login(self, timeout_ms: int = 30000) -> bool:
        """Resultado esperado: llegada al Dashboard (título del módulo 'Dashboard')."""
        self.f.find_element_click(By.XPATH, self.loc.XPATH.BTN_LOGIN)
        TraceReportsContext.log_info(
            "Evidencia de presionar el botón 'Login'",
            page=self.page,
            screenshot_name=f"click_login_{int(time.time())}",
        )
        # Import diferido: evita import circular (Dashboard reutiliza Login al cerrar sesión).
        from pages.dashboard_page import Dashboard

        dashboard = Dashboard(self.page, self.logger, self.error_list, self.data_test, self.warning_list)
        return dashboard.validar_dashboard_visible(timeout_ms)

    def hacer_clic_login_esperando_error(self, mensaje_esperado: str) -> bool:
        """Caso negativo. Resultado esperado: alerta de error con `mensaje_esperado` y sin sesión."""
        self.f.find_element_click(By.XPATH, self.loc.XPATH.BTN_LOGIN)
        return self.validar_alerta_error(mensaje_esperado)

    def validar_alerta_error(self, mensaje_esperado: str) -> bool:
        if not self.f.wait_element(By.XPATH, self.loc.XPATH.LBL_ALERTA_ERROR, wait_type="visible"):
            return self.f.fallo_validacion(
                "No se mostró la alerta de credenciales inválidas",
                esperado=f"alerta '{mensaje_esperado}' visible",
                encontrado="sin alerta (¿el login con credenciales inválidas fue aceptado?)",
                screenshot_name=f"error_alerta_login_no_visible_{int(time.time())}",
            )
        texto = self.f.get_text_from_element(By.XPATH, self.loc.XPATH.LBL_ALERTA_ERROR)
        if not self.f.validate_text_by_equals(mensaje_esperado, texto, "Mensaje de alerta de login"):
            return False
        TraceReportsContext.log_info(
            f"Evidencia de alerta de login: '{texto}'",
            page=self.page,
            screenshot_name=f"alerta_credenciales_invalidas_{int(time.time())}",
        )
        return True
