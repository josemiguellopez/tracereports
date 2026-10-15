# Ejemplo: pytest + Selenium + Allure

🌐 [English](README.md) · **Español**

Para quien ya automatiza con **Python, Selenium y Allure**: los mismos tests de siempre contra la demo
pública de OrangeHRM, y dos formas de sumar TraceReports sin dejar Allure.

Requisitos: Python 3.9+, Chrome, un servidor de TraceReports
([inicio rápido](../../docs/es/quickstart.md): `tracereports demo` sirve) y el comando `tracereports`:
descárgalo de la [última versión](https://github.com/josemiguellopez/tracereports/releases/latest) o
compílalo con `go build -o tracereports.exe ./cmd` (en Windows ejecútalo como `.\tracereports.exe`).
Desde la raíz del repositorio:

```bash
python -m pip install -r examples/python-selenium-allure/requirements.txt ./client/python
```

## Nivel 1: sigues con Allure y envías sus resultados

Sin cambiar ni una línea de tus tests:

```bash
python -m pytest examples/python-selenium-allure --alluredir=allure-results
tracereports push allure-results -project orangehrm -name "Selenium + Allure"
```

`push` muestra el link a la ejecución. Cada fallo llega con su diagnóstico de IA (por ejemplo
*LOCATOR_CHANGED: el selector `#menu_pim_viewPimModule` ya no existe*), y tienes historia, inestables,
métricas, escalar a Teams/Slack y tickets. Tus pasos de `allure.step`, capturas y adjuntos se
conservan. Más en la [guía de Allure](../../docs/es/allure.md).

## Nivel 2: Allure y TraceReports en la misma corrida

```bash
python -m pytest examples/python-selenium-allure --alluredir=allure-results --tracereports
```

Allure escribe sus resultados como siempre y, además, TraceReports recibe la ejecución **en vivo**.
Lo que agrega [conftest.py](conftest.py), y puedes copiar a tu proyecto:

- `paso("texto")`: un `allure.step` que también queda como paso en TraceReports.
- Al fallar un test, la captura del navegador va a Allure y a TraceReports.

Sin `--tracereports`, la fixture `tracereports` no hace nada: la suite es la misma de siempre.

## Ver un fallo diagnosticado

Los tests pasan contra la demo. Para ver el diagnóstico, agrega un fallo controlado (un selector que
el menú ya no tiene):

```bash
TRACEREPORTS_DEMO_FAIL=1 python -m pytest examples/python-selenium-allure --alluredir=allure-results
```

En PowerShell: `$env:TRACEREPORTS_DEMO_FAIL="1"`. Se usa `python -m pytest` (y `python -m pip`) porque
funciona aunque la carpeta `Scripts` de Python no esté en el PATH. `HEADLESS=0` muestra el navegador.

## Sin servidor

```bash
tracereports report allure-results -o reporte --ai
```

Un reporte HTML (`reporte/index.html`) con el diagnóstico de IA del proveedor de tu entorno, para
guardarlo como artefacto de CI.

## Qué no hay con Selenium en Python (todavía)

Con Selenium, el cliente de Python no captura la **red del navegador** ni el **DOM** al fallar, así
que no hay pestaña Red, [Lab](../../docs/es/fixlab.md) ni selector sugerido; el diagnóstico usa el
error, los pasos y la captura. La red se captura con Playwright (Python, JS, Java, Go); el DOM, y con
él el selector sugerido, también con Selenium en Java y JS.
