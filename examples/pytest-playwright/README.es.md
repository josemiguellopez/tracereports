# Ejemplo: pytest + Playwright

🌐 [English](README.md) · **Español**

Lo mínimo: tests normales de pytest-playwright reportados con el plugin, sin infraestructura propia.

```bash
pip install pytest pytest-playwright ./client/python   # desde la raíz del repositorio
playwright install chromium                            # o usa tu Chrome: --browser-channel chrome
pytest examples/pytest-playwright --tracereports
```

Se reportan solos el estado, el error con su traceback, la captura al fallar y la red del backend.
La fixture `tracereports` agrega pasos y capturas propias. Más opciones en [docs/es/python.md](../../docs/es/python.md).
