# tracereports (cliente JavaScript)

🌐 [English](README.md) · **Español**

Cliente del servidor [TraceReports](https://github.com/josemiguellopez/tracereports): Playwright Test (reporter + fixtures),
Selenium WebDriver y cualquier runner. Node 18+, sin dependencias.

```bash
npm install --save-dev tracereports
```

- **Playwright Test**: el reporter envía cada test con sus pasos, error y capturas, más los traces y
  videos que guarda Playwright; con los fixtures, también la red que vio el navegador, el DOM al
  fallar y la consola del navegador.
- **Selenium WebDriver o cualquier runner**: captura y DOM al fallar, y una API para pasos y red.
- **Nunca frena ni rompe tus tests**: la evidencia sale en segundo plano, con reintentos y sin
  duplicados; los shards pueden unirse a la misma ejecución.
- **Sin servidor**: si el servidor está caído o rechaza el token, la ejecución se graba en local;
  `tracereports report <carpeta>` arma el reporte HTML y `tracereports push <carpeta>` lo sube después.
  Con `TRACEREPORTS_OFFLINE=both` además guarda esa copia local mientras envía al servidor.
- **Configuración** desde el entorno o el `.env` del proyecto (`TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`…).

Documentación completa: [docs/es/javascript.md](https://github.com/josemiguellopez/tracereports/blob/main/docs/es/javascript.md).
