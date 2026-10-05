# Cliente Java (JUnit 5, Selenium, Playwright)

🌐 [English](../en/java.md) · **Español**

El cliente vive en [`client/java`](../../client/java) (Java 17+, sin dependencias en tiempo de
ejecución: usa `java.net.http`). Incluye una **extensión de JUnit 5** y helpers para **Selenium** y
**Playwright para Java**, que se compilan contra las versiones de tu proyecto.

Para usarlo sin publicarlo, inclúyelo como *composite build* de Gradle (como los ejemplos):

```kotlin
// settings.gradle.kts
includeBuild("../ruta/a/tracereports/client/java")
// build.gradle.kts
dependencies { testImplementation("tracereports:tracereports-java:0.1.0") }
```

Con Maven: `cd client/java && ./gradlew publishToMavenLocal` (queda en `~/.m2`) o
copia el JAR de `./gradlew jar` (`build/libs`).

Como los otros clientes, nunca rompe ni frena tu suite: la evidencia sale desde un hilo en segundo
plano con reintentos e `Idempotency-Key`; si el servidor no responde, los métodos son no-op y
`delivery()` dice qué no llegó, incluido `runNotClosed` si el servidor no confirmó el cierre de la
ejecución (la extensión lo advierte al terminar).

Configuración: `TRACEREPORTS_URL` (o `-Dtracereports.url`), `TRACEREPORTS_TOKEN` (o `-Dtracereports.token`),
`TRACEREPORTS_DISABLED=1`, `TRACEREPORTS_RUN_NAME` (default: la carpeta del proyecto), `TRACEREPORTS_ENV`,
`TRACEREPORTS_PROJECT`, `TRACEREPORTS_RUN_ID`, `TRACEREPORTS_FLUSH_TIMEOUT` y la rama y el commit del CI o `git`.
Si una variable no está en el entorno, se lee del `.env` del proyecto
([detalle](configuration.md#el-env-del-proyecto-en-los-clientes)).

## JUnit 5

```java
@ExtendWith(TraceReportsExtension.class)
class LoginTest {
    WebDriver driver;

    @BeforeEach
    void setUp(TraceTest t) {                 // TraceTest se inyecta
        driver = new ChromeDriver();
        SeleniumEvidence.attach(t, driver);      // si falla: captura + DOM, antes de @AfterEach
    }

    @Test @Tag("smoke")
    void adminEntra(TraceTest t) {
        t.info("Abrir el login");
        ...
        t.screenshot(SeleniumEvidence.screenshot(driver), "Dashboard", Status.PASS);
    }

    @AfterEach void tearDown() { driver.quit(); }
}
```

Qué hace la extensión:

- una ejecución por JVM (todas las clases con la extensión reportan en ella), cerrada al terminar;
- **identidad estable** de cada test: `paquete.Clase#método(Tipos)` (la firma separa los métodos
  sobrecargados) y, en un `@ParameterizedTest` o `@RepeatedTest`, `[#n]` con la posición del caso.
  No depende del nombre visible (puede repetirse o cambiar) ni de los valores de los parámetros
  (pueden ser secretos); si cambias el orden de los casos, cambia `#n`. Con `@TraceReportsKey("…")` la
  fijas a mano (útil al renombrar). El `@DisplayName` es el nombre visible; los `@Tag`, categorías;
- resultado, mensaje y stack trace (entrada de la IA); `@Disabled` y suposiciones fallidas quedan
  como omitidos;
- los hooks `t.beforeFinish(...)` corren antes de `@AfterEach`, con el navegador abierto.

Para registrarla en todas las clases sin anotarlas, usa la autodetección de JUnit
(`junit.jupiter.extensions.autodetection.enabled=true` y el archivo
`META-INF/services/org.junit.jupiter.api.extension.Extension` con `tracereports.junit.TraceReportsExtension`).

## Playwright para Java

```java
@BeforeEach
void nuevaPagina(TraceTest t) {
    page = browser.newContext().newPage();
    PlaywrightEvidence.attach(t, page);   // red del test + captura y DOM si falla
}
```

`attach` captura **todas las llamadas HTTP de la página** (pestaña Red; es el tráfico visto desde el
navegador, no los logs del backend) y las envía al cerrar el test; si falló, agrega la captura y el
snapshot del DOM. Declara negativos esperados con `t.expectResponse(401, "*/auth/*")`.

## Sin JUnit

```java
TraceReports cr = new TraceReports();
cr.startRun("Regresión web", "staging");
TraceTest t = cr.startTest(new TraceReports.TestInfo("Login").key("LoginTest#adminEntra").category("smoke"));
t.info("Abrir el login");
t.finish(Status.PASS);          // o t.finish(excepcion)
cr.finishRun();                 // espera la cola y cierra la ejecución
```

Ejemplos completos: [`examples/selenium-java`](../../examples/selenium-java) y
[`examples/playwright-java`](../../examples/playwright-java). Pruebas del cliente:
`cd client/java && ./gradlew test`.
