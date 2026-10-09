# tracereports (cliente Python)

🌐 [English](README.md) · **Español**

Cliente del servidor [TraceReports](https://github.com/josemiguellopez/tracereports). Solo usa la librería estándar.

```bash
pip install tracereports
pytest --tracereports                     # reporta tu suite de pytest sin cambiar código
```

- **Plugin de pytest**: pasos, capturas, red y DOM con pytest-playwright, la consola del navegador
  y los workers de pytest-xdist reunidos en una sola ejecución. También una API para unittest,
  Selenium o scripts.
- **Nunca frena ni rompe tus tests**: la evidencia sale en segundo plano, con reintentos y sin
  duplicados; lo que no se pudo enviar se puede guardar y reenviar después.
- **Sin servidor**: si el servidor está caído o rechaza el token, la ejecución se graba en local;
  `tracereports report <carpeta>` arma el reporte HTML y `tracereports push <carpeta>` lo sube después.
  Con `TRACEREPORTS_OFFLINE=both` además guarda esa copia local mientras envía al servidor.
- **Configuración** desde el entorno o el `.env` del proyecto (`TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`…).

Documentación completa: [docs/es/python.md](https://github.com/josemiguellopez/tracereports/blob/main/docs/es/python.md).

Sin binario instalado, al cerrar se descarga y verifica el renderer v0.2.0 en la caché del usuario; `TRACEREPORTS_BIN_DOWNLOAD=0` permite trabajar sin internet con un binario instalado o en caché. Ver [modo offline](../../docs/es/offline.md).
