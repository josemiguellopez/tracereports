class DashboardLocator:
    class CSS:
        LBL_TITULO_MODULO = ".oxd-topbar-header-breadcrumb h6"
        LBL_NOMBRE_USUARIO = ".oxd-userdropdown-name"
        BTN_MENU_USUARIO = ".oxd-userdropdown-tab"
        LIST_WIDGETS = ".orangehrm-dashboard-widget-name p"
        LIST_MENU_LATERAL = ".oxd-main-menu-item span"

    class XPATH:
        BTN_CERRAR_SESION = "//a[normalize-space()='Logout']"

        @staticmethod
        def menu_lateral(nombre: str) -> str:
            return f"//a[contains(@class,'oxd-main-menu-item')]/span[normalize-space()='{nombre}']"
