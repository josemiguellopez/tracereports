package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.file.Path;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/** Qué se tapa y qué capturas se envían (TRACEREPORTS_SCREENSHOT_MASK y TRACEREPORTS_SCREENSHOTS). */
class MaskTest {
    @Test
    void selectorDefaultsExtraAndOff() {
        Map<String, String> env = new HashMap<>();
        assertEquals("input[type=password], [data-tracereports-mask]", Mask.selector(env::get));
        env.put("TRACEREPORTS_SCREENSHOT_MASK", "input[name=rut], .tarjeta,");
        assertEquals("input[type=password], [data-tracereports-mask], input[name=rut], .tarjeta", Mask.selector(env::get));
        env.put("TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS", "OFF");
        assertEquals("input[name=rut], .tarjeta", Mask.selector(env::get));
        env.remove("TRACEREPORTS_SCREENSHOT_MASK");
        assertEquals("", Mask.selector(env::get));
        // sin variables (como corren los tests) tapa los de siempre
        assertEquals(Mask.DEFAULT_MASK, Mask.selector());
    }

    @Test
    void policy() {
        for (String[] c : new String[][] {{"", "all"}, {"all", "all"}, {"basura", "all"}, {"Failures", "failures"}, {"off", "off"}}) {
            assertEquals(c[1], Mask.policy(Map.of("TRACEREPORTS_SCREENSHOTS", c[0])::get), c[0]);
        }
        assertTrue(Mask.allowed("all", Status.INFO));
        assertFalse(Mask.allowed("failures", Status.INFO));
        assertTrue(Mask.allowed("failures", Status.FAIL));
        assertFalse(Mask.allowed("off", Status.FAIL));
        assertFalse(Mask.allowed("off", Status.INFO));
    }

    @TempDir
    Path dir;

    /** La política en un proceso aparte (el entorno no se cambia desde Java): cuántas capturas llegan. */
    @Test
    void screenshotFollowsThePolicy() throws Exception {
        for (Object[] c : new Object[][] {{"all", 2}, {"failures", 1}, {"off", 0}}) {
            try (FakeServer srv = new FakeServer()) {
                String jvm = ProcessHandle.current().info().command().orElse("java");
                ProcessBuilder pb = new ProcessBuilder(jvm, "-cp", System.getProperty("java.class.path"),
                        "tracereports.ScreenshotProbe", srv.url()).directory(dir.toFile()).redirectErrorStream(true);
                pb.environment().put("TRACEREPORTS_SCREENSHOTS", (String) c[0]);
                pb.environment().put("TRACEREPORTS_ENV_FILE", "off");
                pb.environment().remove("TRACEREPORTS_DISABLED");
                Process p = pb.start();
                String out = new String(p.getInputStream().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
                assertEquals(0, p.waitFor(), out);
                long shots = srv.paths().stream().filter(x -> x.endsWith("/screenshot")).count();
                assertEquals(((Integer) c[1]).longValue(), shots, c[0] + ": " + srv.paths());
            }
        }
    }
}
