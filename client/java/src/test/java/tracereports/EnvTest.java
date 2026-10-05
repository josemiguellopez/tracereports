package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

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

    @Test
    void theTestRunIsIsolatedFromTheRealEnvFile() {
        // build.gradle.kts define TRACEREPORTS_ENV_FILE=off: los tests no leen el .env del repositorio
        Env.reset();
        assertNull(Env.fileInUse());
    }
}
