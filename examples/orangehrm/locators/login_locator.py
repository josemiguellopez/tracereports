class LoginLocator:
    class NAME:
        TXT_USERNAME = "username"
        TXT_PASSWORD = "password"

    class XPATH:
        LBL_TITULO_LOGIN = "//h5[normalize-space()='Login']"
        BTN_LOGIN = "//button[@type='submit' and normalize-space()='Login']"
        LBL_ALERTA_ERROR = "//p[contains(@class,'oxd-alert-content-text')]"
