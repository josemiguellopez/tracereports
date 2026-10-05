import json
import os
import re
import time

from locators.pim_locator import PimLocator
from utils.tracereports_context import TraceReportsContext
from utils.function import By, Function


class Pim:
    """Page Object del módulo PIM → Employee List: scraping de la tabla y búsqueda."""

    def __init__(self, page, logger, error_list, data_test, warning_list=None):
        self.page = page
        self.logger = logger
        self.error_list = error_list
        self.data_test = data_test
        self.warning_list = warning_list if warning_list is not None else []
        self.f = Function(page, logger, error_list)
        self.loc = PimLocator

    # ─── lecturas / scraping ───────────────────────────────────────────────
    def obtener_total_registros(self):
        """Parsea '(115) Records Found' → 115; 'No Records Found' → 0; None si no se pudo leer."""
        if not self.f.wait_element(By.XPATH, self.loc.XPATH.LBL_TOTAL_REGISTROS, wait_type="visible"):
            return None
        texto = self.f.get_text_from_element(By.XPATH, self.loc.XPATH.LBL_TOTAL_REGISTROS)
        if texto.startswith("No Records"):
            return 0
        m = re.search(r"\((\d+)\)", texto)
        total = int(m.group(1)) if m else None
        TraceReportsContext.log_info(f"Total de registros en la lista de empleados: {texto}")
        return total

    def extraer_empleados(self, max_filas: int, evidence_dir: str = None, timeout_ms: int = 30000) -> list:
        """
        Scraping de la tabla de empleados. Retorna una lista de dicts con las columnas del
        encabezado (Id, First (& Middle) Name, Last Name, Job Title, ...). Guarda el
        resultado en JSON como evidencia si se indica `evidence_dir`.
        """
        if not self.f.wait_element(By.CSS, self.loc.CSS.LIST_FILAS_EMPLEADOS, wait_type="visible", timeout=timeout_ms):
            self.f.fallo_validacion(
                "La tabla de empleados no cargó filas",
                esperado="al menos una fila en Employee List",
                encontrado="tabla vacía o no visible",
                screenshot_name=f"error_tabla_empleados_vacia_{int(time.time())}",
            )
            return []

        encabezados = self.f.get_all_texts(By.CSS, self.loc.CSS.LIST_ENCABEZADOS)
        filas = self.f.locator(By.CSS, self.loc.CSS.LIST_FILAS_EMPLEADOS)
        empleados = []
        for i in range(min(filas.count(), max_filas)):
            celdas = [c.strip() for c in filas.nth(i).locator(self.loc.CSS.LIST_CELDAS_FILA).all_inner_texts()]
            fila = {h: v for h, v in zip(encabezados, celdas) if h and h != "Actions"}
            empleados.append(fila)

        if evidence_dir:
            os.makedirs(evidence_dir, exist_ok=True)
            ruta = os.path.join(evidence_dir, f"empleados_scrapeados_{int(time.time())}.json")
            with open(ruta, "w", encoding="utf-8") as fh:
                json.dump(empleados, fh, ensure_ascii=False, indent=2)
            TraceReportsContext.log_info(f"Datos extraídos guardados en: {ruta}")

        resumen = "\n".join(
            f"• Id={e.get('Id') or '—'} | {e.get('First (& Middle) Name', '')} {e.get('Last Name', '')}"
            f" | {e.get('Job Title') or 'sin cargo'}"
            for e in empleados[:5]
        )
        TraceReportsContext.log_info(
            f"Evidencia de scraping de {len(empleados)} empleados (primeros 5):\n{resumen}",
            page=self.page,
            screenshot_name=f"empleados_scrapeados_{int(time.time())}",
        )
        return empleados

    def validar_empleados_extraidos(self, empleados: list, columnas_requeridas: list) -> bool:
        if not empleados:
            return self.f.fallo_validacion(
                "El scraping no extrajo empleados",
                esperado="al menos 1 empleado",
                encontrado="0 empleados",
                screenshot_name=f"error_scraping_vacio_{int(time.time())}",
            )
        faltantes = [c for c in columnas_requeridas if c not in empleados[0]]
        if faltantes:
            return self.f.fallo_validacion(
                "La tabla de empleados no tiene las columnas esperadas",
                esperado=", ".join(columnas_requeridas),
                encontrado=f"faltan: {', '.join(faltantes)} (columnas leídas: {', '.join(empleados[0])})",
                screenshot_name=f"error_columnas_tabla_{int(time.time())}",
            )
        sin_id = sum(1 for e in empleados if not e.get("Id"))
        if sin_id:
            # No bloquea: el demo público tiene datos sucios cargados por otros usuarios.
            self.warning_list.append(f"{sin_id} de {len(empleados)} empleados extraídos no tienen Id")
        TraceReportsContext.log_pass(f"Scraping válido: {len(empleados)} empleados con columnas {', '.join(columnas_requeridas)}")
        return True

    # ─── búsqueda ──────────────────────────────────────────────────────────
    def buscar_empleado_por_id(self, employee_id: str) -> bool:
        """Resultado esperado: la tabla muestra el empleado con ese Id."""
        texto_antes = self.f.get_text_from_element(By.XPATH, self.loc.XPATH.LBL_TOTAL_REGISTROS)
        self.f.send_text(By.XPATH, self.loc.XPATH.TXT_EMPLOYEE_ID, employee_id)
        TraceReportsContext.log_info(
            f"Evidencia de ingresar Employee Id '{employee_id}'",
            page=self.page,
            screenshot_name=f"employee_id_ingresado_{int(time.time())}",
        )
        self.f.find_element_click(By.XPATH, self.loc.XPATH.BTN_BUSCAR)
        # La tabla se recarga por XHR: esperar a que cambie el contador y desaparezca el spinner.
        if not self.f.wait_text_change(self.loc.XPATH.LBL_TOTAL_REGISTROS, texto_antes, timeout=20000):
            self.warning_list.append(f"El contador de registros no cambió tras buscar el Id '{employee_id}'")
        self.f.wait_until_not_element_visible(By.CSS, self.loc.CSS.SPINNER, timeout=20000)
        return self.validar_resultado_busqueda(employee_id)

    def validar_resultado_busqueda(self, employee_id: str) -> bool:
        ids = [
            fila.locator(self.loc.CSS.LIST_CELDAS_FILA).nth(1).inner_text().strip()
            for fila in self.f.locator(By.CSS, self.loc.CSS.LIST_FILAS_EMPLEADOS).all()
        ]
        if employee_id not in ids:
            return self.f.fallo_validacion(
                "La búsqueda por Employee Id no devolvió el empleado esperado",
                esperado=f"fila con Id '{employee_id}'",
                encontrado=f"{len(ids)} fila(s) con Ids: {', '.join(ids[:10]) or 'ninguna'}",
                screenshot_name=f"error_busqueda_por_id_{int(time.time())}",
            )
        TraceReportsContext.log_pass(
            f"Evidencia de búsqueda exitosa: {len(ids)} resultado(s) para el Id '{employee_id}'",
            page=self.page,
            screenshot_name=f"busqueda_por_id_ok_{int(time.time())}",
        )
        return True
