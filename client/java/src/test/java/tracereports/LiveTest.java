package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.util.Map;
import org.junit.jupiter.api.Test;

/** En vivo desde el cliente Java: execution_id, tests planificados y la cancelación cooperativa. */
class LiveTest {
    @Test
    void executionIdFromTheVariableOrTheCiBuild() {
        assertEquals("", Context.executionId(k -> ""));
        assertEquals("azure-4411", Context.executionId(k -> Map.of("BUILD_BUILDID", "4411").getOrDefault(k, "")));
        assertEquals("github-99-2", Context.executionId(k -> Map.of("GITHUB_RUN_ID", "99", "GITHUB_RUN_ATTEMPT", "2").getOrDefault(k, "")));
        assertEquals("nocturna", Context.executionId(k -> Map.of("TRACEREPORTS_EXECUTION_ID", "nocturna", "BUILD_BUILDID", "1").getOrDefault(k, "")));
    }

    @Test
    void startRunSendsExecutionAndPlanned() throws Exception {
        try (FakeServer srv = new FakeServer()) {
            TraceReports cr = new TraceReports(srv.url(), "");
            cr.startRun(new TraceReports.RunInfo("suite").branch("").commit("").executionId("lanzador-1").planned(12));
            String created = srv.requests.stream().filter(r -> r.path().equals("/api/v1/runs")).findFirst().orElseThrow().text();
            assertTrue(created.contains("\"execution_id\":\"lanzador-1\"") && created.contains("\"planned\":12"), created);
            cr.setPlanned(14);
            FakeServer.Req plan = srv.requests.get(srv.requests.size() - 1);
            assertEquals("/api/v1/runs/7/plan", plan.path());
            assertEquals("{\"planned\":14}", plan.text());
        }
    }

    @Test
    void cancelOnlyWithRemoteControlAndItStays() throws Exception {
        try (FakeServer srv = new FakeServer()) {
            TraceReports cr = new TraceReports(srv.url(), "");
            cr.startRun(new TraceReports.RunInfo("r").branch("").commit(""));
            assertFalse(cr.cancelRequested(Duration.ZERO, false).cancel());
            assertTrue(srv.requests.stream().noneMatch(r -> r.method().equals("GET")), "sin control remoto no pregunta");
            assertFalse(cr.cancelRequested(Duration.ZERO, true).cancel());
            assertFalse(cr.cancelRequested(Duration.ofSeconds(5), true).cancel(), "dentro de los 5 s no vuelve a preguntar");
            assertEquals(1, srv.requests.stream().filter(r -> r.method().equals("GET")).count());
            srv.control = "{\"cancel\":true,\"at\":1,\"by\":\"ana\",\"reason\":\"QA caído\"}";
            TraceReports.Control c = cr.cancelRequested(Duration.ZERO, true);
            assertTrue(c.cancel());
            assertEquals("QA caído", c.reason());
            srv.control = "{\"cancel\":false}";
            assertTrue(cr.cancelRequested(Duration.ZERO, true).cancel(), "una vez cancelada, queda cancelada");
        }
    }

    @Test
    void anOlderServerNeverCancelsNorLosesEvidence() throws Exception {
        try (FakeServer srv = new FakeServer()) {
            srv.old = true;
            TraceReports cr = new TraceReports(srv.url(), "");
            cr.startRun(new TraceReports.RunInfo("r").branch("").commit(""));
            cr.setPlanned(3);
            assertFalse(cr.cancelRequested(Duration.ZERO, true).cancel());
            assertFalse(cr.cancelRequested(Duration.ZERO, true).cancel());
            assertEquals(1, srv.requests.stream().filter(r -> r.path().endsWith("/control")).count(), "tras un 404 no vuelve a preguntar");
            assertEquals(0, cr.delivery().problems());
        }
    }
}
