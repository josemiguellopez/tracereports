# tracereports-java (cliente Java)

🌐 [English](README.md) · **Español**

Cliente del servidor [TraceReports](../../README.es.md): extensión de JUnit 5 y helpers para
Selenium y Playwright para Java. Java 17+, sin dependencias en tiempo de ejecución.

```bash
./gradlew test                  # pruebas del cliente
./gradlew publishToMavenLocal   # para usarlo desde Maven
```

- **JUnit 5**: `@ExtendWith(TraceReportsExtension.class)` reporta cada test con su resultado; inyecta
  `TraceTest` para agregar pasos, capturas, red, la consola del navegador y traces o videos de Playwright.
- **Nunca frena ni rompe tus tests**: la evidencia sale en un hilo en segundo plano, con reintentos
  y sin duplicados; los shards pueden unirse a la misma ejecución.
- **Sin servidor**: si el servidor está caído o rechaza el token, la ejecución se graba en local;
  `tracereports report <carpeta>` arma el reporte HTML y `tracereports push <carpeta>` lo sube después.
- **Configuración** desde el entorno, propiedades del sistema o el `.env` del proyecto.

Documentación completa: [docs/es/java.md](../../docs/es/java.md).
