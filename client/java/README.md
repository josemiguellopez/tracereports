# tracereports-java (Java client)

🌐 **English** · [Español](README.es.md)

Client for the [TraceReports](../../README.md) server: JUnit 5 extension and helpers for Selenium
and Playwright for Java. Java 17+, no runtime dependencies.

```bash
./gradlew test                  # client tests
./gradlew publishToMavenLocal   # to use it from Maven
```

- **JUnit 5**: `@ExtendWith(TraceReportsExtension.class)` reports each test with its result; inject
  `TraceTest` to add steps, screenshots, network, the browser console and Playwright traces or videos.
- **Never slows down or breaks your tests**: evidence goes out in a background thread with retries
  and no duplicates; shards can join the same run.
- **Without a server**: if the server is down or rejects the token, the run is recorded locally;
  `tracereports report <folder>` builds the HTML report and `tracereports push <folder>` uploads it later.
  With `TRACEREPORTS_OFFLINE=both` it also keeps that local copy while sending to the server.
- **Configuration** from the environment, system properties or the project's `.env`.

Full documentation: [docs/en/java.md](../../docs/en/java.md).
