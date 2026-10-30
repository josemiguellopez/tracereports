# tracereports-java (Java client)

🌐 **English** · [Español][readme-es]

Client for the [TraceReports][tracereports] server: JUnit 5 extension and helpers for Selenium
and Playwright for Java. Java 17+, no runtime dependencies.

```bash
./gradlew test                  # client tests
./gradlew publishToMavenLocal   # to use it from Maven
```

- **JUnit 5**: `@ExtendWith(TraceReportsExtension.class)` reports each test with its result; inject
  `TraceTest` to add steps, screenshots, network, the browser console and Playwright traces or videos.
- **Selenium (Chrome, Edge)**: `SeleniumEvidence.options(options)` and `SeleniumEvidence.attach(t, driver)` send
  each test's network (Network tab, Lab), console, and the screenshot and DOM on failure ([Selenium guide][selenium]).
- **Never slows down or breaks your tests**: evidence goes out in a background thread with retries
  and no duplicates; shards can join the same run.
- **Without a server**: if the server is down or rejects the token, the run is recorded locally;
  `tracereports report <folder>` builds the HTML report and `tracereports push <folder>` uploads it later.
  With `TRACEREPORTS_OFFLINE=both` it also keeps that local copy while sending to the server
  ([local report guide][local-report]).
- **Configuration** from the environment, system properties or the project's `.env`.

Full documentation: [docs/en/java.md][java].

Without an installed binary, closing downloads and verifies the v0.2.0 renderer in the user cache; `TRACEREPORTS_BIN_DOWNLOAD=0` supports offline use with an installed or cached binary. See [offline mode][offline].

[selenium]: https://github.com/josemiguellopez/tracereports/blob/main/docs/en/selenium.md
[tracereports]: https://github.com/josemiguellopez/tracereports
[local-report]: https://github.com/josemiguellopez/tracereports/blob/main/docs/en/local-report.md
[java]: https://github.com/josemiguellopez/tracereports/blob/main/docs/en/java.md
[offline]: https://github.com/josemiguellopez/tracereports/blob/main/docs/en/offline.md
[readme-es]: https://github.com/josemiguellopez/tracereports/blob/main/client/java/README.es.md
