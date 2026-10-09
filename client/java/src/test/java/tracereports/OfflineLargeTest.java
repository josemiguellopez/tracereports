package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/** Una grabación con un evento de más de 16 MiB la lee el CLI (report). */
class OfflineLargeTest {
    @TempDir
    Path tmp;

    @AfterEach
    void clear() {
        for (String p : List.of("offline", "offlineDir", "offlineReport")) System.clearProperty("tracereports." + p);
    }

    @Test
    void anEventOver16MiBCanBeReported() throws Exception {
        String bin = System.getenv("TRACEREPORTS_BIN");
        if (bin == null || bin.isBlank()) bin = System.getenv("TRACEREPORTS_TEST_BIN");
        Assumptions.assumeTrue(bin != null && !bin.isBlank(), "needs $TRACEREPORTS_BIN");
        Path rec = tmp.resolve("rec");
        System.setProperty("tracereports.offline", "always");
        System.setProperty("tracereports.offlineDir", rec.toString());
        System.setProperty("tracereports.offlineReport", "0");
        TraceReports cr = new TraceReports("http://127.0.0.1:9", "");
        cr.startRun(new TraceReports.RunInfo("offline-large"));
        TraceTest t = cr.startTest(new TraceReports.TestInfo("large POST"));
        t.info("antes del upload");
        t.network(List.of(Map.<String, Object>of("method", "POST", "url", "https://app/api/upload", "status", 500,
                "post_data", "ñ\"x".repeat(4 << 20), "response_body", "{}")));
        t.info("después del upload");
        t.finish(Status.FAIL, "boom", "");
        cr.finishRun();
        long max = 0;
        try (Stream<Path> files = Files.list(rec)) {
            for (Path f : files.filter(p -> p.getFileName().toString().startsWith("events-")).toList()) {
                for (String l : Files.readAllLines(f)) max = Math.max(max, l.getBytes(StandardCharsets.UTF_8).length);
            }
        }
        assertTrue(max > 16 << 20, "the fixture writes a line over 16 MiB: " + max);
        Path out = tmp.resolve("report");
        Process p = new ProcessBuilder(bin, "report", rec.toString(), "-o", out.toString()).redirectErrorStream(true).start();
        String log = new String(p.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
        assertEquals(0, p.waitFor(), log);
        String data = Files.readString(out.resolve("data.js"));
        assertTrue(data.contains("https://app/api/upload") && data.indexOf("antes del upload") < data.indexOf("después del upload"));
    }
}
