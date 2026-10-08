package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/** El cliente lee TRACEREPORTS_* del .env del proyecto cuando no están en el entorno. */
class EnvTest {
    @TempDir
    Path base;

    @Test
    void sameRulesAsTheServer() throws IOException {
        Path f = base.resolve("x.env");
        Files.writeString(f, "# c\n\nexport TRACEREPORTS_TOKEN=abc\nTRACEREPORTS_URL = 'http://x:1'  \n"
                + "TRACEREPORTS_RUN_NAME=\"con # dentro\"\nTRACEREPORTS_ENV=qa # fin\nMALA LINEA=1\nsin_igual\n");
        assertEquals(Map.of("TRACEREPORTS_TOKEN", "abc", "TRACEREPORTS_URL", "http://x:1",
                "TRACEREPORTS_RUN_NAME", "con # dentro", "TRACEREPORTS_ENV", "qa"), Env.parse(f));
    }

    @Test
    void foundWalkingUpButNeverOutsideTheRepository() throws IOException {
        Path repo = base.resolve("repo");
        Path sub = repo.resolve("src/test");
        Files.createDirectories(sub);
        Files.createDirectories(repo.resolve(".git"));
        // un .env por encima de la raíz del repositorio no se usa
        Files.writeString(base.resolve(".env"), "TRACEREPORTS_TOKEN=ajeno\n");
        assertNull(Env.find(sub, null));
        Files.writeString(repo.resolve(".env"), "TRACEREPORTS_TOKEN=propio\n");
        assertEquals(repo.resolve(".env"), Env.find(sub, null));
    }

    @Test
    void explicitFileAndOff() {
        Path other = base.resolve("otro.env");
        assertEquals(other, Env.find(base, other.toString()));
        assertNull(Env.find(base, "off"));
        assertNull(Env.find(base, "OFF"));
    }

    /**
     * Definida en el entorno, aunque vacía, manda sobre el .env (como en el servidor): vacía es "sin
     * configurar" y el valor del archivo no se recupera. Ausente, sí se lee del archivo.
     */
    @Test
    void anEmptyEnvironmentValueIsNotReplacedByTheFile() throws Exception {
        Path repo = base.resolve("repo");
        Files.createDirectories(repo.resolve(".git"));
        Files.writeString(repo.resolve(".env"), "TRACEREPORTS_TOKEN=fake-file-token\n");
        assertEquals("fake-file-token", probe(repo, null, null)); // ausente: del archivo
        assertEquals("fake-env-token", probe(repo, "fake-env-token", null)); // con valor: del entorno
        assertEquals("", probe(repo, "", null)); // vacía: sin configurar, no el archivo
        assertEquals("", probe(repo, "", "off"));
        assertEquals("", probe(repo, null, "off")); // archivo apagado y ausente
        Files.delete(repo.resolve(".env"));
        assertEquals("", probe(repo, null, null)); // sin archivo
    }

    /** Env.get("TRACEREPORTS_TOKEN") en otra JVM, con ese entorno y esa carpeta de trabajo. */
    private static String probe(Path dir, String token, String envFile) throws Exception {
        String jvm = ProcessHandle.current().info().command().orElse("java");
        ProcessBuilder pb = new ProcessBuilder(jvm, "-cp", System.getProperty("java.class.path"),
                "tracereports.EnvProbe", "TRACEREPORTS_TOKEN").directory(dir.toFile()).redirectErrorStream(true);
        Map<String, String> env = pb.environment();
        env.remove("TRACEREPORTS_TOKEN");
        env.remove("TRACEREPORTS_ENV_FILE");
        if (token != null) env.put("TRACEREPORTS_TOKEN", token);
        if (envFile != null) env.put("TRACEREPORTS_ENV_FILE", envFile);
        Process p = pb.start();
        String out = new String(p.getInputStream().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
        assertEquals(0, p.waitFor(), out);
        assertTrue(out.startsWith("[") && out.endsWith("]"), out);
        return out.substring(1, out.length() - 1);
    }

    @Test
    void theTestRunIsIsolatedFromTheRealEnvFile() {
        // build.gradle.kts define TRACEREPORTS_ENV_FILE=off: los tests no leen el .env del repositorio
        Env.reset();
        assertNull(Env.fileInUse());
    }
}
