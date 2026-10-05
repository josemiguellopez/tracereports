package tracereports.junit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.platform.engine.discovery.DiscoverySelectors.selectClass;

import tracereports.TraceTest;
import tracereports.FakeServer;
import java.util.List;
import java.util.stream.Collectors;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.Disabled;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.junit.platform.testkit.engine.EngineTestKit;

/** Corre una clase de ejemplo con la extensión contra un servidor falso y revisa lo reportado. */
class TraceReportsExtensionTest {

    @ExtendWith(TraceReportsExtension.class)
    static class Ejemplo {
        @Test
        @Tag("smoke")
        void pasa(TraceTest t) {
            t.info("paso propio");
        }

        @Test
        void falla(TraceTest t) {
            t.beforeFinish(failed -> t.info("hook antes de @AfterEach, failed=" + failed));
            throw new AssertionError("esperaba Dashboard");
        }

        @ParameterizedTest
        @ValueSource(strings = {"admin", "ess"})
        void conParametros(String rol) {}

        // nombre visible constante: la identidad no puede depender de él
        @ParameterizedTest(name = "same")
        @ValueSource(strings = {"admin", "guest"})
        void mismoNombre(String rol) {}

        // métodos sobrecargados: la firma los separa
        @Test
        void sobrecargado() {}

        @Test
        void sobrecargado(TraceTest t) {}

        @TraceReportsKey("login-fijo")
        @Test
        void conClaveFija() {}

        @Test
        void suposicion() {
            Assumptions.assumeTrue(false, "solo en prod");
        }

        @Test
        @Disabled("no aplica")
        void deshabilitado() {}
    }

    @Test
    void reportaLaClaseConIdentidadYResultados() throws Exception {
        try (FakeServer srv = new FakeServer()) {
            System.setProperty("tracereports.url", srv.url());
            EngineTestKit.engine("junit-jupiter")
                    .configurationParameter("junit.jupiter.extensions.autodetection.enabled", "false")
                    .selectors(selectClass(Ejemplo.class))
                    .execute()
                    .testEvents()
                    .assertStatistics(s -> s.succeeded(8).failed(1).aborted(1).skipped(1));

            assertEquals(1, srv.at("/api/v1/runs").size(), "one run per JVM");
            List<String> tests = srv.at("/api/v1/runs/7/tests").stream().map(FakeServer.Req::text).collect(Collectors.toList());
            assertEquals(11, tests.size(), String.join("\n", tests));
            String cls = Ejemplo.class.getName();
            assertTrue(tests.stream().anyMatch(t -> t.contains("\"key\":\"" + cls + "#pasa(TraceTest)\"") && t.contains("smoke")), tests.toString());
            assertTrue(tests.stream().anyMatch(t -> t.contains("\"key\":\"" + cls + "#conParametros(String)[#1]\"")), tests.toString());
            List<String> keys = tests.stream().map(t -> t.replaceAll(".*\"key\":\"([^\"]*)\".*", "$1")).collect(Collectors.toList());
            assertEquals(keys.size(), keys.stream().distinct().count(), "every case has its own identity: " + keys);
            assertTrue(keys.contains(cls + "#mismoNombre(String)[#1]") && keys.contains(cls + "#mismoNombre(String)[#2]"), keys.toString());
            assertTrue(keys.contains(cls + "#sobrecargado()") && keys.contains(cls + "#sobrecargado(TraceTest)"), keys.toString());
            assertTrue(keys.contains("login-fijo"), keys.toString());

            List<String> finishes = srv.at("/api/v1/tests/11/finish").stream().map(FakeServer.Req::text).collect(Collectors.toList());
            assertEquals(11, finishes.size());
            assertEquals(1, finishes.stream().filter(f -> f.contains("\"status\":\"FAIL\"") && f.contains("AssertionError: esperaba Dashboard")).count());
            assertEquals(2, finishes.stream().filter(f -> f.contains("\"status\":\"SKIP\"")).count());
            String logs = srv.at("/api/v1/tests/11/logs").stream().map(FakeServer.Req::text).collect(Collectors.joining("\n"));
            assertTrue(logs.contains("hook antes de @AfterEach, failed=true") && logs.contains("solo en prod") && logs.contains("no aplica"), logs);
            List<String> paths = srv.paths();
            assertEquals("PATCH /api/v1/runs/7/finish", paths.get(paths.size() - 1));
        } finally {
            System.clearProperty("tracereports.url");
        }
    }
}
