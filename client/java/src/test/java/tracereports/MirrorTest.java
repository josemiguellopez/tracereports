package tracereports;

import static org.junit.jupiter.api.Assertions.*;
import java.nio.file.*;
import java.util.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;

class MirrorTest {
    @TempDir Path dir;
    @BeforeEach void configure() {
        System.setProperty("tracereports.offline", "both");
        System.setProperty("tracereports.offlineDir", dir.toString());
        System.setProperty("tracereports.offlineReport", "1");
    }
    @AfterEach void clear() {
        for (String key : List.of("offline", "offlineDir", "offlineReport", "offlineKeep", "bin")) System.clearProperty("tracereports." + key);
    }
    String marker() throws Exception { return Files.readString(dir.resolve("tracereports-offline.json")); }
    @Test void keepRecordsAndSends() throws Exception {
        System.setProperty("tracereports.offlineKeep", "1");
        try (FakeServer server = new FakeServer()) {
            TraceReports cr = new TraceReports(server.url(), "");
            OfflineTest.runSuite(cr);
            List<String> events = OfflineTest.lines(dir);
            assertEquals(7, events.size()); assertEquals(7, server.requests.size());
            long run = Json.number(events.get(0), "local_id"), test = Json.number(events.get(1), "local_id");
            assertTrue(run < 0 && test < 0 && run != test);
            assertTrue(events.get(1).contains("/api/v1/runs/" + run + "/tests"));
            assertTrue(events.get(2).contains("/api/v1/tests/" + test + "/logs"));
            assertTrue(marker().contains("\"complete\":true"));
            assertEquals(server.url() + "/#run=7&view=dashboard", cr.reportUrl());
            assertNotNull(cr.offlineReport()); assertTrue(Files.isRegularFile(cr.offlineReport()));
            String data = Files.readString(dir.resolve("report/data.js"));
            assertTrue(data.contains("login")); assertFalse(data.contains("SECRET"));
        }
    }
    @Test void completeDeliveryCleansRaw() throws Exception {
        try (FakeServer server = new FakeServer()) {
            TraceReports cr = new TraceReports(server.url(), "");
            OfflineTest.runSuite(cr);
            try (var entries = Files.list(dir)) { assertEquals(List.of("report", "tracereports-offline.json"), entries.map(p -> p.getFileName().toString()).sorted().toList()); }
            assertTrue(marker().contains("\"raw_removed\":true"));
            cr.finishRun(); assertTrue(marker().contains("\"complete\":true"));
        }
    }
    @Test void outageKeepsNewTests() throws Exception {
        try (FakeServer server = new FakeServer()) {
            TraceReports cr = new TraceReports(server.url(), "");
            cr.startRun("outage", "");
            server.failNext.set(100);
            TraceTest test = cr.startTest("after outage"); assertTrue(test.id() < 0);
            test.info("local step"); test.finish(Status.PASS); cr.finishRun();
            assertEquals(5, OfflineTest.lines(dir).size());
            assertTrue(marker().contains("\"complete\":false"));
            assertTrue(Files.isRegularFile(cr.offlineReport()));
            assertTrue(Files.readString(dir.resolve("report/data.js")).contains("after outage"));
        }
    }
    @Test void initialFailureFallsBack() throws Exception {
        try (OfflineTest.Unauthorized server = new OfflineTest.Unauthorized()) {
            TraceReports cr = new TraceReports(server.url(), ""); OfflineTest.runSuite(cr);
            assertTrue(cr.runId() < 0); assertFalse(marker().contains("\"mirror\"")); assertEquals(7, OfflineTest.lines(dir).size());
        }
    }
    @Test void missingBinaryRetainsRaw() throws Exception {
        System.setProperty("tracereports.bin", dir.resolve("not-installed").toString());
        try (FakeServer server = new FakeServer()) {
            TraceReports cr = new TraceReports(server.url(), ""); OfflineTest.runSuite(cr);
            assertNull(cr.offlineReport()); assertEquals(7, OfflineTest.lines(dir).size()); assertTrue(marker().contains("\"complete\":true"));
        }
    }
    @Test void diskFailureDoesNotStopServer() throws Exception {
        System.setProperty("tracereports.offlineReport", "0");
        try (FakeServer server = new FakeServer()) {
            TraceReports cr = new TraceReports(server.url(), ""); cr.startRun("disk", "");
            Files.delete(dir.resolve("bodies")); Files.writeString(dir.resolve("bodies"), "blocked");
            TraceTest test = cr.startTest("sent"); test.screenshot(new byte[]{1, 2, 3}, "shot"); test.info("still sent"); test.finish(Status.PASS); cr.finishRun();
            assertEquals(6, server.requests.size()); assertTrue(marker().contains("\"complete\":false"));
        }
    }
    @Test void workerWithoutClosePreventsCleanup() throws Exception {
        try (FakeServer server = new FakeServer()) {
            TraceReports owner = new TraceReports(server.url(), ""); owner.startRun("shared", "");
            TraceReports worker = new TraceReports(server.url(), ""); worker.joinRun(owner.runId());
            worker.startTest("worker").finish(Status.PASS); owner.finishRun();
            assertTrue(marker().contains("\"complete\":false")); assertEquals(4, OfflineTest.lines(dir).size()); worker.finishRun();
        }
    }
    @Test void metadataPreservesIdsAndEscapes() {
        Map<String, Object> value = Map.of("id", -8999999999999999L, "server", "https://host/á\\\"", "runs", List.of(Map.of("complete", false)));
        assertEquals(value, Json.read(Json.write(value)));
        for (String invalid : List.of("{", "{\"a\":true}extra", "[01]", "\"\\z\"", "[1,]")) assertThrows(IllegalArgumentException.class, () -> Json.read(invalid));
    }
    @Test void missingMappingNeverRecordsRemoteIds() throws Exception {
        try (FakeServer server = new FakeServer()) {
            TraceReports cr = new TraceReports(server.url(), ""); cr.joinRun(7);
            TraceTest test = cr.startTest("unmapped"); test.info("step"); test.finish(Status.PASS); cr.finishRun();
            assertTrue(marker().contains("\"complete\":false"));
            assertEquals(3, OfflineTest.lines(dir).size());
            for (String line : OfflineTest.lines(dir)) assertTrue(line.contains("/-"), line);
        }
    }

    /** Un bloqueo que quedó de un proceso muerto (más de 30 s) se retira; uno reciente es de otro proceso. */
    @Test void orphanedLockDoesNotBlockTheCopy(@org.junit.jupiter.api.io.TempDir java.nio.file.Path base) throws Exception {
        java.nio.file.Path old = base.resolve("old"), fresh = base.resolve("fresh");
        java.nio.file.Files.createDirectories(old.resolve(".mirror-lock"));
        java.nio.file.Files.createDirectories(fresh.resolve(".mirror-lock"));
        java.nio.file.Files.setLastModifiedTime(old.resolve(".mirror-lock"),
                java.nio.file.attribute.FileTime.fromMillis(System.currentTimeMillis() - 120_000));
        new Mirror(old, "http://server.test", 7, java.util.Map.of("name", "run")).close(false);
        org.junit.jupiter.api.Assertions.assertFalse(java.nio.file.Files.exists(old.resolve(".mirror-lock")));
        java.io.IOException err = org.junit.jupiter.api.Assertions.assertThrows(java.io.IOException.class,
                () -> new Mirror(fresh, "http://server.test", 7, java.util.Map.of("name", "run")));
        org.junit.jupiter.api.Assertions.assertTrue(err.getMessage().contains("lo tiene otro proceso"), err.getMessage());
    }

    /** Con offlineBase cada corrida crea su carpeta dentro de la base: la segunda no choca con la primera. */
    @Test void successiveRunsKeepTheirOwnCopyInsideTheBase() throws Exception {
        Path base = dir.resolve("output").resolve("tracereports");
        System.clearProperty("tracereports.offlineDir");
        System.setProperty("tracereports.offlineBase", base.toString());
        try (FakeServer server = new FakeServer()) {
            List<Path> dirs = new java.util.ArrayList<>();
            for (int i = 0; i < 2; i++) {
                TraceReports cr = new TraceReports(server.url(), "");
                OfflineTest.runSuite(cr);
                assertTrue(cr.recording(), "each run keeps its local copy");
                dirs.add(cr.offlineDir());
            }
            assertNotEquals(dirs.get(0), dirs.get(1));
            for (Path d : dirs) assertEquals(base, d.getParent());
        } finally {
            System.clearProperty("tracereports.offlineBase");
        }
    }
}
