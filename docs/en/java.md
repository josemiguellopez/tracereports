# Java client (JUnit 5, Selenium, Playwright)

🌐 **English** · [Español](../es/java.md)

The client lives in [`client/java`](../../client/java) (Java 17+, no runtime dependencies: it uses
`java.net.http`). It includes a **JUnit 5 extension** and helpers for **Selenium** and **Playwright
for Java**, compiled against your project's versions.

To use it without publishing, include it as a Gradle *composite build* (like the examples):

```kotlin
// settings.gradle.kts
includeBuild("../path/to/tracereports/client/java")
// build.gradle.kts
dependencies { testImplementation("tracereports:tracereports-java:0.1.0") }
```

With Maven: `cd client/java && ./gradlew publishToMavenLocal` (it lands in `~/.m2`) or
copy the JAR from `./gradlew jar` (`build/libs`).

Like the other clients, it never breaks or slows down your suite: the evidence is sent from a
background thread with retries and `Idempotency-Key`; when the server does not answer, methods are
no-ops and `delivery()` tells you what did not arrive, including `runNotClosed` when the server did not
confirm the run was closed (the extension warns about it at the end).

**Without a server**: if the run cannot be created (server down or wrong token), the evidence is not
lost: it is recorded in `./tracereports-offline/<session>` and `tracereports report <folder>` builds the
HTML report, or `tracereports push <folder>` uploads it later. See [Without a server](offline.md).

Configuration: `TRACEREPORTS_URL` (or `-Dtracereports.url`), `TRACEREPORTS_TOKEN` (or `-Dtracereports.token`),
`TRACEREPORTS_DISABLED=1`, `TRACEREPORTS_RUN_NAME` (default: the project folder), `TRACEREPORTS_ENV`,
`TRACEREPORTS_PROJECT`, `TRACEREPORTS_RUN_ID`, `TRACEREPORTS_FLUSH_TIMEOUT` and the branch and commit from the CI or
`git`. A variable missing from the environment is read from the project's `.env`
([details](configuration.md#the-projects-env-in-the-clients)).

## JUnit 5

```java
@ExtendWith(TraceReportsExtension.class)
class LoginTest {
    WebDriver driver;

    @BeforeEach
    void setUp(TraceTest t) {                 // TraceTest is injected
        driver = new ChromeDriver();
        SeleniumEvidence.attach(t, driver);      // on failure: screenshot + DOM, before @AfterEach
    }

    @Test @Tag("smoke")
    void adminLogsIn(TraceTest t) {
        t.info("Open the login page");
        ...
        t.screenshot(SeleniumEvidence.screenshot(driver), "Dashboard", Status.PASS);
    }

    @AfterEach void tearDown() { driver.quit(); }
}
```

What the extension does:

- one run per JVM (every class with the extension reports into it), closed at the end;
- each test's **stable identity**: `package.Class#method(Types)` (the signature separates overloaded
  methods) and, in a `@ParameterizedTest` or `@RepeatedTest`, `[#n]` with the case position. It does
  not depend on the visible name (which may repeat or change) nor on the parameter values (which may
  be secret); if you reorder the cases, `#n` changes. Pin it with `@TraceReportsKey("…")` (useful when
  renaming). `@DisplayName` is the visible name; `@Tag`s are categories;
- outcome, message and stack trace (input for the AI); `@Disabled` and failed assumptions are
  reported as skipped;
- `t.beforeFinish(...)` hooks run before `@AfterEach`, with the browser still open.

To register it for every class without annotations, use JUnit's autodetection
(`junit.jupiter.extensions.autodetection.enabled=true` and the file
`META-INF/services/org.junit.jupiter.api.extension.Extension` with `tracereports.junit.TraceReportsExtension`).

## Playwright for Java

```java
@BeforeEach
void newPage(TraceTest t) {
    page = browser.newContext().newPage();
    PlaywrightEvidence.attach(t, page);   // the test's network + screenshot and DOM on failure
}
```

`attach` captures **every HTTP call of the page** (Network tab; it is the traffic seen from the
browser, not the backend logs) and sends it when the test closes; on failure it adds the screenshot
and the DOM snapshot. Declare expected negatives with `t.expectResponse(401, "*/auth/*")`.

## Without JUnit

```java
TraceReports cr = new TraceReports();
cr.startRun("Web regression", "staging");
TraceTest t = cr.startTest(new TraceReports.TestInfo("Login").key("LoginTest#adminLogsIn").category("smoke"));
t.info("Open the login page");
t.finish(Status.PASS);          // or t.finish(exception)
cr.finishRun();                 // waits for the queue and closes the run
```

Full examples: [`examples/selenium-java`](../../examples/selenium-java) and
[`examples/playwright-java`](../../examples/playwright-java). Client tests:
`cd client/java && ./gradlew test`.
