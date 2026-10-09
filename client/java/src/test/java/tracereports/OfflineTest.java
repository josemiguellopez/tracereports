package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.stream.Stream;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/** Grabación sin servidor: si la ejecución no se puede crear, la evidencia se graba en una carpeta. */
class OfflineTest {
    private static final byte[] PNG = {(byte) 0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'f', 'a', 'k', 'e'};

    @TempDir
    Path tmp;

    @AfterEach
    void clear() {
        for (String p : List.of("offline", "offlineDir", "offlineReport", "bin")) System.clearProperty("tracereports." + p);
    }

    /** Servidor que rechaza todo con 401 (token equivocado) y cuenta las llamadas. */
    static final class Unauthorized implements AutoCloseable {
        final HttpServer server;
        final AtomicInteger calls = new AtomicInteger();

        Unauthorized() throws IOException {
            server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
            server.createContext("/", ex -> {
                calls.incrementAndGet();
                ex.getRequestBody().readAllBytes();
                byte[] b = "{\"error\":\"invalid token\"}".getBytes(StandardCharsets.UTF_8);
                ex.sendResponseHeaders(401, b.length);
                ex.getResponseBody().write(b);
                ex.close();
            });
            server.start();
        }

        String url() { return "http://127.0.0.1:" + server.getAddress().getPort(); }

        @Override
        public void close() { server.stop(0); }
    }

    static List<String> lines(Path dir) throws IOException {
        List<String> out = new ArrayList<>();
        try (Stream<Path> files = Files.list(dir)) {
            for (Path f : files.filter(p -> p.getFileName().toString().startsWith("events-")).sorted().toList()) {
                out.addAll(Files.readAllLines(f).stream().filter(l -> !l.isBlank()).toList());
            }
        }
        return out;
    }

    static void runSuite(TraceReports cr) {
        cr.startRun(new TraceReports.RunInfo("Checkout").environment("qa").project("shop").branch("dev").commit("abc"));
        TraceTest t = cr.startTest(new TraceReports.TestInfo("login").key("LoginTest#login"));
        t.info("abrir login");
        t.screenshot(PNG, "pantalla");
        t.network(List.of(Map.<String, Object>of("method", "POST", "url", "https://api/login", "status", 500,
                "request_headers", Map.of("Authorization", "Bearer SECRET"))));
        t.finish(Status.FAIL, "boom", "");
        cr.finishRun();
    }

    @Test
    void conElTokenEquivocadoSeGrabaEnVezDePerderTodo() throws Exception {
        Path dir = tmp.resolve("rec");
        System.setProperty("tracereports.offlineDir", dir.toString());
        System.setProperty("tracereports.offlineReport", "0");
        try (Unauthorized srv = new Unauthorized()) {
            TraceReports cr = new TraceReports(srv.url(), "wrong");
            runSuite(cr);
            assertTrue(cr.recording());
            assertEquals(dir, cr.offlineDir());
            assertTrue(cr.runId() < 0);
        }
        List<String> evs = lines(dir);
        assertEquals(7, evs.size(), String.join("\n", evs));
        long run = Json.number(evs.get(0), "local_id"), test = Json.number(evs.get(1), "local_id");
        assertTrue(run < 0 && test < 0 && run != test);
        String[] want = {
            "\"method\":\"POST\",\"path\":\"/api/v1/runs\"",
            "\"method\":\"POST\",\"path\":\"/api/v1/runs/" + run + "/tests\"",
            "\"method\":\"POST\",\"path\":\"/api/v1/tests/" + test + "/logs\"",
            "\"method\":\"POST\",\"path\":\"/api/v1/tests/" + test + "/screenshot\"",
            "\"method\":\"POST\",\"path\":\"/api/v1/tests/" + test + "/network\"",
            "\"method\":\"PATCH\",\"path\":\"/api/v1/tests/" + test + "/finish\"",
            "\"method\":\"PATCH\",\"path\":\"/api/v1/runs/" + run + "/finish\"",
        };
        for (int i = 0; i < want.length; i++) assertTrue(evs.get(i).contains(want[i]), evs.get(i));
        assertTrue(evs.get(0).contains("\"body\":{\"name\":\"Checkout\""), evs.get(0));
        assertTrue(evs.stream().allMatch(l -> Json.number(l, "ts") > 0));
        String shot = evs.get(3);
        assertTrue(shot.contains("\"content_type\":\"multipart/form-data") && shot.contains("\"body_file\":\"bodies/"), shot);
        String bodyFile = shot.replaceAll(".*\"body_file\":\"([^\"]+)\".*", "$1");
        assertTrue(new String(Files.readAllBytes(dir.resolve(bodyFile)), StandardCharsets.ISO_8859_1).contains("fake"));
        String marker = Files.readString(dir.resolve(Recorder.MARKER));
        assertTrue(marker.contains("\"format\":\"tracereports-offline\"") && marker.contains("\"version\":1"), marker);
    }

    @Test
    void offlineAlwaysNuncaContactaUnServidor() throws Exception {
        System.setProperty("tracereports.offline", "always");
        System.setProperty("tracereports.offlineDir", tmp.toString());
        System.setProperty("tracereports.offlineReport", "0");
        try (Unauthorized srv = new Unauthorized()) {
            runSuite(new TraceReports(srv.url(), ""));
            assertEquals(0, srv.calls.get());
        }
        assertEquals(7, lines(tmp).size());
    }

    @Test
    void offlineOffNoGrabaNada() throws Exception {
        System.setProperty("tracereports.offline", "off");
        System.setProperty("tracereports.offlineDir", tmp.resolve("nada").toString());
        try (Unauthorized srv = new Unauthorized()) {
            TraceReports cr = new TraceReports(srv.url(), "wrong");
            runSuite(cr);
            assertFalse(cr.recording());
            assertEquals(0, cr.runId());
        }
        assertFalse(Files.exists(tmp.resolve("nada")));
    }

    @Test
    void unShardConIdNegativoGrabaEnLaMismaCarpeta() throws Exception {
        System.setProperty("tracereports.offline", "always");
        System.setProperty("tracereports.offlineDir", tmp.toString());
        System.setProperty("tracereports.offlineReport", "0");
        TraceReports owner = new TraceReports("http://127.0.0.1:9", "");
        long runId = owner.startRun("shards", "");
        System.clearProperty("tracereports.offline");
        TraceReports shard = new TraceReports("http://127.0.0.1:9", "");
        shard.joinRun(runId);
        assertTrue(shard.recording());
        assertEquals(tmp, shard.offlineDir());
        shard.startTest("a").finish(Status.PASS);
        shard.finishRun(); // no es el dueño: solo cierra su archivo
        owner.finishRun();
        try (Stream<Path> files = Files.list(tmp)) {
            assertEquals(2, files.filter(p -> p.getFileName().toString().startsWith("events-")).count());
        }
        assertTrue(lines(tmp).stream().anyMatch(l -> l.contains("\"path\":\"/api/v1/runs/" + runId + "/tests\"")));
    }

    /**
     * Dos grabadores que comparten la carpeta (aquí en el mismo proceso: mismo pid) reservan bloques
     * distintos en ids/: no repiten ids, que son exactos en JavaScript y no chocan con grabaciones
     * anteriores. Con el binario, el reporte deja cada test en su ejecución.
     */
    @Test
    void dosGrabadoresEnLaMismaCarpetaNoRepitenIds() throws Exception {
        System.setProperty("tracereports.offline", "always");
        System.setProperty("tracereports.offlineDir", tmp.resolve("rec").toString());
        System.setProperty("tracereports.offlineReport", "0");
        TraceReports a = new TraceReports("http://127.0.0.1:9", "");
        TraceReports b = new TraceReports("http://127.0.0.1:9", "");
        List<Long> ids = new ArrayList<>(List.of(a.startRun("suite A", ""), b.startRun("suite B", "")));
        TraceTest ta = a.startTest("test A");
        TraceTest tb = b.startTest("test B");
        java.lang.reflect.Field next = Recorder.class.getDeclaredField("nextId");
        next.setAccessible(true);
        next.setLong(b.sender.recorder, 100_000 - 1); // el bloque de B se agota: reserva otro
        TraceTest tb2 = b.startTest("test B2");
        ids.addAll(List.of(ta.id(), tb.id(), tb2.id()));
        ta.finish(Status.PASS);
        tb.finish(Status.FAIL, "only B failed", "");
        tb2.finish(Status.PASS);
        a.finishRun();
        b.finishRun();
        assertEquals(5, ids.stream().distinct().count(), "ids repetidos: " + ids);
        for (long id : ids) assertTrue(id < -100_000_000_000L && id > -(1L << 53), "id " + id);
        try (Stream<Path> blocks = Files.list(tmp.resolve("rec").resolve("ids"))) {
            assertEquals(3, blocks.count());
        }
        String bin = System.getenv("TRACEREPORTS_BIN");
        if (bin == null || bin.isBlank()) bin = System.getenv("TRACEREPORTS_TEST_BIN");
        if (bin == null || bin.isBlank()) return;
        Path out = tmp.resolve("report");
        Process p = new ProcessBuilder(bin, "report", tmp.resolve("rec").toString(), "-o", out.toString()).redirectErrorStream(true).start();
        String log = new String(p.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
        assertEquals(0, p.waitFor(), log);
        int runs = 0;
        try (Stream<Path> dirs = Files.list(out)) {
            for (Path d : dirs.filter(x -> x.getFileName().toString().startsWith("run-")).toList()) {
                String data = Files.readString(d.resolve("data.js"));
                runs++;
                if (data.contains("\"name\":\"suite A\"")) {
                    assertTrue(data.contains("test A") && !data.contains("test B"), "suite A");
                } else {
                    assertTrue(data.contains("\"name\":\"suite B\"") && data.contains("test B2") && !data.contains("test A"), "suite B");
                }
            }
        }
        assertEquals(2, runs);
    }

    @Test
    void conElBinarioFinishRunArmaElReporteSinServidor() throws Exception {
        String bin = System.getenv("TRACEREPORTS_BIN");
        if (bin == null || bin.isBlank()) bin = System.getenv("TRACEREPORTS_TEST_BIN");
        org.junit.jupiter.api.Assumptions.assumeTrue(bin != null && !bin.isBlank(), "needs $TRACEREPORTS_BIN");
        Path dir = tmp.resolve("rec");
        System.setProperty("tracereports.offlineDir", dir.toString());
        try (Unauthorized srv = new Unauthorized()) {
            TraceReports cr = new TraceReports(srv.url(), "wrong");
            runSuite(cr);
            assertNotNull(cr.offlineReport(), "report built");
            assertTrue(Files.exists(cr.offlineReport()));
            assertEquals(cr.offlineReport().toString(), cr.reportUrl());
            String data = Files.readString(cr.offlineReport().getParent().resolve("data.js"));
            assertTrue(data.contains("Checkout") && data.contains("abrir login"));
            assertFalse(data.contains("SECRET"), "masking runs without a server too");
        }
    }

    @Test
    void consolaSeGraba() throws Exception {
        System.setProperty("tracereports.offline", "always");
        System.setProperty("tracereports.offlineDir", tmp.toString());
        System.setProperty("tracereports.offlineReport", "0");
        TraceReports cr = new TraceReports("http://127.0.0.1:9", "");
        cr.startRun("Consola", "");
        TraceTest t = cr.startTest("t");
        t.console(List.of(Map.of("level", "pageerror", "text", "TypeError: x is undefined", "timestamp", 1L)));
        t.console(List.of()); // vacío: nada
        t.finish(Status.FAIL);
        cr.finishRun();
        List<String> sent = lines(tmp).stream().filter(l -> l.contains("/console\"")).toList();
        assertEquals(1, sent.size());
        assertTrue(sent.get(0).contains("\"level\":\"pageerror\"") && sent.get(0).contains("TypeError: x is undefined"), sent.get(0));
    }

    @Test
    void artifactSeGrabaComoMultipart() throws Exception {
        System.setProperty("tracereports.offline", "always");
        System.setProperty("tracereports.offlineDir", tmp.toString());
        System.setProperty("tracereports.offlineReport", "0");
        TraceReports cr = new TraceReports("http://127.0.0.1:9", "");
        cr.startRun("Artefactos", "");
        TraceTest t = cr.startTest("t");
        Path trace = tmp.resolve("trace.zip");
        Files.write(trace, new byte[]{'P', 'K', 3, 4, 'z'});
        assertTrue(t.artifact("trace", trace));
        assertTrue(t.artifact("video", new byte[]{0x1A, 0x45, (byte) 0xDF, (byte) 0xA3}, "video \"1\""));
        assertFalse(t.artifact("har", new byte[]{1}, "x"));
        assertFalse(t.artifact("trace", tmp.resolve("missing.zip")));
        t.finish(Status.FAIL);
        cr.finishRun();
        List<String> uploads = lines(tmp).stream().filter(l -> l.contains("/artifact\"")).toList();
        assertEquals(2, uploads.size());
        String file = uploads.get(0).replaceAll(".*\"body_file\":\"([^\"]+)\".*", "$1");
        String body = new String(Files.readAllBytes(tmp.resolve(file)), StandardCharsets.ISO_8859_1);
        assertTrue(body.contains("name=\"kind\"\r\n\r\ntrace") && body.contains("trace.zip"), body);
    }
}
