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

`--alluredir` solo guarda los resultados en bruto (JSON y capturas), no un reporte. Para ver el
reporte de Allure de siempre (necesita Java y Node, sin instalar nada global):

```bash
npx -y allure-commandline generate --single-file allure-results -o allure-report --clean
```

y abre `allure-report/index.html`. Borra `allure-results` antes de cada corrida: Allure no la limpia
y suma los resultados anteriores.

## Nivel 2: Allure y TraceReports en la misma corrida

```bash
python -m pytest examples/python-selenium-allure --alluredir=allure-results --tracereports
```

Allure escribe sus resultados como siempre y, además, TraceReports recibe la ejecución **en vivo**,
con la **red** de cada test (pestaña Red, incidentes de backend, [Lab](../../docs/es/fixlab.md)), la
**consola**, y la **captura** y el **selector sugerido** si falla. Lo que agrega
[conftest.py](conftest.py), y puedes copiar a tu proyecto:

- `selenium_options(webdriver.ChromeOptions())`: la única línea que activa los logs de red y consola de
  Chrome. El plugin hace el resto.
- `paso("texto")`: un `allure.step` que también queda como paso en TraceReports.
- Al fallar un test, la captura del navegador también va a Allure.

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

## ¿Nivel 1 o nivel 2?

El nivel 1 (solo `allure-results`) da el diagnóstico con IA, la historia y todo lo que grabó Allure.
Allure no graba la red del navegador ni el DOM de la página, así que el nivel 2 suma lo que solo
captura TraceReports: la pestaña Red, los incidentes de backend, el Lab y el selector sugerido. Los dos
funcionan con Chrome y Edge; más en [Selenium](../../docs/es/selenium.md).
