class PimLocator:
    class CSS:
        LIST_FILAS_EMPLEADOS = ".oxd-table-body .oxd-table-card"
        LIST_CELDAS_FILA = ".oxd-table-cell"
        LIST_ENCABEZADOS = ".oxd-table-header .oxd-table-header-cell"
        SPINNER = ".oxd-loading-spinner"

    class XPATH:
        # "(115) Records Found" / "(1) Record Found" / "No Records Found"
        LBL_TOTAL_REGISTROS = "//div[contains(@class,'orangehrm-horizontal-padding')]//span[contains(normalize-space(),'Found')]"
        TXT_EMPLOYEE_ID = "//label[normalize-space()='Employee Id']/../following-sibling::div//input"
        BTN_BUSCAR = "//button[@type='submit' and normalize-space()='Search']"
