# Ejemplo: framework Page Object con unittest

🌐 [English](README.md) · **Español**

Un framework de automatización "real" (Page Objects, locators separados, workflow de varios tests
que dependen entre sí) reportando a TraceReports con el cliente Python.

```bash
pip install playwright ./client/python      # desde la raíz del repositorio
python examples/orangehrm/tests/test_orangehrm_pim.py
```

| Archivo | Qué muestra | Resultado esperado |
|---|---|---|
| `tests/test_orangehrm_pim.py` | Login → scraping del Dashboard y de PIM → búsqueda → logout. Si un paso de precondición falla, los siguientes quedan en SKIP con el motivo | pasan |
| `tests/test_orangehrm_login_invalido.py` | Casos negativos validados también en la red: 302, sin request, 401, 404 | pasan |
| `tests/test_orangehrm_errores_backend.py` | Fallas del backend simuladas (caído, 500, timeout) y un selector desactualizado, para ver un fallo completo: captura, error con la llamada que falló, red en rojo, diagnóstico y locators sugeridos | **fallan a propósito** |

`HEADLESS=0` muestra el navegador, y `BROWSER_CHANNEL=chrome` usa tu Chrome en vez del Chromium de
Playwright. La evidencia local (capturas, JSON de red y log) queda en `examples/orangehrm/output/`.

Estructura:

```
tests/base_test.py                infraestructura común: navegador, captura de red, ciclo de vida del test
pages/                            Page Objects (cada acción valida su resultado y devuelve bool)
locators/                         selectores
utils/function.py                 helpers de Playwright (esperas, lecturas, validaciones)
utils/tracereports_context.py   pasos + capturas hacia TraceReports
data/data_test.json               datos de prueba
```
