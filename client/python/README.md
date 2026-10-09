# tracereports (Python client)

🌐 **English** · [Español](README.es.md)

Client for the [TraceReports](https://github.com/josemiguellopez/tracereports) server. Standard library only.

```bash
pip install tracereports
pytest --tracereports                     # reports your pytest suite with no code changes
```

- **pytest plugin**: steps, screenshots, network and DOM with pytest-playwright, the browser console,
  and pytest-xdist workers merged into one run. Also a client API for unittest, Selenium or scripts.
- **Never slows down or breaks your tests**: evidence goes out in the background with retries and
  no duplicates; whatever could not be sent can be saved and resent later.
- **Without a server**: if the server is down or rejects the token, the run is recorded locally;
  `tracereports report <folder>` builds the HTML report and `tracereports push <folder>` uploads it later.
  With `TRACEREPORTS_OFFLINE=both` it also keeps that local copy while sending to the server.
- **Configuration** from the environment or the project's `.env` (`TRACEREPORTS_URL`, `TRACEREPORTS_TOKEN`…).

Full documentation: [docs/en/python.md](https://github.com/josemiguellopez/tracereports/blob/main/docs/en/python.md).

Without an installed binary, closing downloads and verifies the v0.2.0 renderer in the user cache; `TRACEREPORTS_BIN_DOWNLOAD=0` supports offline use with an installed or cached binary. See [offline mode](../../docs/en/offline.md).
