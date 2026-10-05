# Ejemplo: Selenium WebDriver (JavaScript, node:test)

🌐 [English](README.md) · **Español**

Tres tests contra la demo pública de [OrangeHRM](https://opensource-demo.orangehrmlive.com) (login
correcto, contraseña incorrecta, buscar un empleado por Id) reportados a TraceReports, más un fallo
controlado para ver el diagnóstico con IA y el recomendador de locators.

```bash
# con el servidor corriendo (go run ./cmd) en http://localhost:8080, o define TRACEREPORTS_URL
npm install
npm test
TRACEREPORTS_DEMO_FAIL=1 npm test     # incluye el fallo controlado
HEADLESS=0 npm test              # ver el navegador
```

Selenium Manager descarga el chromedriver que corresponde. Si el servidor tiene token, define `TRACEREPORTS_TOKEN`. Documentación del cliente:
[docs/es/javascript.md](../../docs/es/javascript.md).
