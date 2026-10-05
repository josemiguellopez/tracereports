package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.stream.IntStream;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

class TraceReportsTest {
    @BeforeAll
    static void fast() {
        Sender.maxBackoffMillis = 100;
        Sender.circuitMillis = 300;
    }

    @Test
    void flujoCompletoEnOrden() throws Exception {
        try (FakeServer srv = new FakeServer()) {
            TraceReports cr = new TraceReports(srv.url(), "tok");
            assertEquals(7, cr.startRun(new TraceReports.RunInfo("Suite").environment("qa").project("shop").branch("main").commit("abc").framework("junit5")));
            TraceTest t = cr.startTest(new TraceReports.TestInfo("Login").key("LoginTest#ok").suite("LoginTest").category("smoke"));
            assertTrue(t.active());
            t.info("Abrir login");
            t.screenshot("PNGDATA".getBytes(), "Formulario");
            t.expectResponse(401, "*/auth/*");
            t.network(List.of(Map.of("method", "POST", "url", "https://app/api/auth/validate", "status", 401),
                    Map.of("method", "GET", "url", "https://app/api/me", "status", 500)));
            t.dom(Map.of("url", "https://app", "elements", List.of(Map.of("tag", "button"))));
            t.attempts(2);
            t.finish(Status.PASS);
            cr.finishRun();

            String run = srv.at("/api/v1/runs").get(0).text();
            assertTrue(run.contains("\"project\":\"shop\"") && run.contains("\"branch\":\"main\"") && run.contains("\"framework\":\"junit5\""), run);
            assertTrue(srv.at("/api/v1/runs/7/tests").get(0).text().contains("\"key\":\"LoginTest#ok\""));
            assertTrue(srv.at("/api/v1/tests/11/screenshot").get(0).text().contains("PNGDATA"));
            String net = srv.at("/api/v1/tests/11/network").get(0).text();
            assertTrue(net.indexOf("\"expected\":true") > 0 && net.indexOf("\"expected\":true") == net.lastIndexOf("\"expected\":true"), net);
            assertTrue(srv.at("/api/v1/tests/11/dom").get(0).text().contains("\"tag\":\"button\""));
            assertTrue(srv.at("/api/v1/tests/11/finish").get(0).text().contains("\"attempts\":2"));
            List<String> paths = srv.paths();
            assertEquals("PATCH /api/v1/runs/7/finish", paths.get(paths.size() - 1), "the run closes after the queue");
            assertTrue(srv.requests.stream().allMatch(r -> "Bearer tok".equals(r.auth()) && r.key() != null));
            assertEquals(0, cr.delivery().problems());
        }
    }

    @Test
    void reintentaLos503SinPerderElOrden() throws Exception {
        try (FakeServer srv = new FakeServer()) {
            TraceReports cr = new TraceReports(srv.url(), "");
            cr.joinRun(7);
            TraceTest t = cr.startTest("x");
            srv.failNext.set(3);
            IntStream.range(0, 15).forEach(i -> t.info("paso " + i));
            assertEquals(0, cr.flush(Duration.ofSeconds(10)));
            List<String> logs = srv.at("/api/v1/tests/11/logs").stream().map(FakeServer.Req::text).toList();
            assertEquals(15, logs.size());
            for (int i = 0; i < 15; i++) assertTrue(logs.get(i).contains("\"paso " + i + "\""), logs.get(i));
            assertTrue(cr.delivery().retried() >= 3);
        }
    }

    @Test
    void conElServidorCaidoNadaSeRompe() {
        TraceReports cr = new TraceReports("http://127.0.0.1:9", "");
        long t0 = System.currentTimeMillis();
        assertEquals(0, cr.startRun("x", ""));
        TraceTest t = cr.startTest("y");
        assertFalse(t.active());
        t.info("no-op");
        t.finish(new RuntimeException("boom"));
        cr.finishRun();
        assertTrue(System.currentTimeMillis() - t0 < 10_000, "fails fast");
    }

    @Test
    void finishDeUnaExcepcionLlevaMensajeYTraza() throws Exception {
        try (FakeServer srv = new FakeServer()) {
            TraceReports cr = new TraceReports(srv.url(), "");
            cr.joinRun(7);
            TraceTest t = cr.startTest("x");
            boolean[] hook = {false};
            t.beforeFinish(failed -> hook[0] = failed);
            t.finish(new IllegalStateException("no encontré el botón"));
            cr.flush(Duration.ofSeconds(5));
            String fin = srv.at("/api/v1/tests/11/finish").get(0).text();
            assertTrue(fin.contains("\"status\":\"FAIL\"") && fin.contains("IllegalStateException: no encontré el botón") && fin.contains("at tracereports"), fin);
            assertTrue(hook[0], "beforeFinish hooks run with failed=true");
        }
    }

    @Test
    void unCierreNoConfirmadoSeInforma() throws Exception {
        try (FakeServer srv = new FakeServer()) {
            srv.failPaths.add("/api/v1/runs/7/finish");     // evidencia OK, el cierre siempre 503
            TraceReports cr = new TraceReports(srv.url(), "");
            cr.startRun("x", "");
            cr.startTest("t").finish(Status.PASS);
            cr.finishRun();
            assertEquals(1, cr.delivery().runNotClosed());
            assertTrue(cr.delivery().problems() >= 1, "an unconfirmed close is a delivery problem: " + cr.delivery());
        }
        try (FakeServer srv = new FakeServer()) {            // falla dos veces y se recupera: sin error falso
            TraceReports cr = new TraceReports(srv.url(), "");
            cr.startRun("x", "");
            srv.failNext.set(2);
            cr.finishRun();
            assertEquals(0, cr.delivery().problems(), cr.delivery().toString());
            List<String> keys = srv.at("/api/v1/runs/7/finish").stream().map(FakeServer.Req::key).toList();
            assertEquals(1, keys.size());
        }
        try (FakeServer srv = new FakeServer()) {            // unido a una ejecución ajena: no la cierra
            TraceReports cr = new TraceReports(srv.url(), "");
            cr.joinRun(55);
            cr.finishRun();
            assertTrue(srv.paths().stream().noneMatch(p -> p.endsWith("/finish")));
            assertEquals(0, cr.delivery().problems());
        }
    }

    @Test
    void resumeLosErroresDePlaywright() {
        RuntimeException pw = new RuntimeException(
                "Error {\n  message='Timeout 5000ms exceeded.\nCall log:\n  - waiting for locator(\"#x\")\n'\n  name='TimeoutError'\n}");
        assertEquals("RuntimeException: Timeout 5000ms exceeded.", TraceTest.summary(pw));
        assertEquals("AssertionError: esperaba Dashboard", TraceTest.summary(new AssertionError("esperaba Dashboard\nmás detalle")));
    }

    @Test
    void laPropiedadTracereportsUrlConfiguraElServidor() {
        System.setProperty("tracereports.url", "http://new:2");
        try {
            assertEquals("http://new:2", new TraceReports().baseUrl());
        } finally {
            System.clearProperty("tracereports.url");
        }
    }
}
