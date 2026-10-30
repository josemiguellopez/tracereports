# tracereports (cliente Python)

🌐 [English][readme-en] · **Español**

Cliente del servidor [TraceReports][tracereports]. Solo usa la librería estándar.

```bash
pip install tracereports
pytest --tracereports                     # reporta tu suite de pytest sin cambiar código
```

- **Plugin de pytest**: pasos, capturas, red y DOM con pytest-playwright, la consola del navegador
  y los workers de pytest-xdist reunidos en una sola ejecución. También una API para unittest,
  Selenium o scripts.
- **Selenium (Chrome, Edge)**: crea el driver con `selenium_options(options)` y el plugin envía también la
  red (pestaña Red, Lab), la consola, la captura y el DOM de cada test ([guía de Selenium][selenium]).
- **Nunca frena ni rompe tus tests**: la evidencia sale en segundo plano, con reintentos y sin
  duplicados; lo que no se pudo enviar se puede guardar y reenviar después.
- **Sin servidor**: si el servidor está caído o rechaza el token, la ejecución se graba en local;
  `tracereports report <carpeta>` arma el reporte HTML y `tracereports push <carpeta>` lo sube después.
  Con `TRACEREPORTS_OFFLINE=both` además guarda esa copia local mientras envía al servidor
  ([guía del reporte local][local-report]).
- **Configuración** desde el entorno o el `.env` del proyecto (`TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`…).

Documentación completa: [docs/es/python.md][python].

Sin binario instalado, al cerrar se descarga y verifica el renderer v0.2.0 en la caché del usuario; `TRACEREPORTS_BIN_DOWNLOAD=0` permite trabajar sin internet con un binario instalado o en caché. Ver [modo offline][offline].

[tracereports]: https://github.com/josemiguellopez/tracereports
[selenium]: https://github.com/josemiguellopez/tracereports/blob/main/docs/es/selenium.md
[local-report]: https://github.com/josemiguellopez/tracereports/blob/main/docs/es/local-report.md
[python]: https://github.com/josemiguellopez/tracereports/blob/main/docs/es/python.md
[offline]: https://github.com/josemiguellopez/tracereports/blob/main/docs/es/offline.md
[readme-en]: https://github.com/josemiguellopez/tracereports/blob/main/client/python/README.md
