# Example: pytest + Playwright

🌐 **English** · [Español](README.es.md)

The minimum: regular pytest-playwright tests reported with the plugin, no infrastructure of your
own.

```bash
pip install pytest pytest-playwright ./client/python   # from the repository root
playwright install chromium                            # or use your Chrome: --browser-channel chrome
pytest examples/pytest-playwright --tracereports
```

Status, the error with its traceback, the screenshot on failure and the backend network are
reported automatically. The `tracereports` fixture adds your own steps and screenshots. More options in
[docs/en/python.md](../../docs/en/python.md).
