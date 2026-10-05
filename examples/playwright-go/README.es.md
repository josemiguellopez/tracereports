# Ejemplo: Go + playwright-go

🌐 [English](README.md) · **Español**

Tests idiomáticos de Go (`go test`) contra la demo de OrangeHRM, con pasos, capturas, captura de
red del backend y reporte de fallos.

```bash
cd examples/playwright-go
go test -v ./...                          # la primera vez descarga el driver y Chromium
BROWSER_CHANNEL=chrome go test -v ./...   # usa tu Chrome instalado
HEADLESS=0 go test -v ./...               # ver el navegador
```

- `orangehrm_test.go`: los tests y el helper `run(...)`, que abre una página por test y reporta
  todo a TraceReports.
- `network.go`: captura de red de la página, con enmascarado de datos sensibles.
- `dom.go`: el snapshot de la página que se envía al fallar (para sugerir selectores).

Detalle del cliente: [docs/es/go.md](../../docs/es/go.md).
