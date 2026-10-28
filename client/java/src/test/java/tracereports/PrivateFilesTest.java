package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.nio.file.FileSystems;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.stream.Stream;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/** La grabación offline lleva la evidencia sin redactar: solo el usuario la lee (0700/0600). */
class PrivateFilesTest {
    @Test
    void laGrabacionQuedaSoloParaElUsuario(@TempDir Path tmp) throws Exception {
        Assumptions.assumeTrue(FileSystems.getDefault().supportedFileAttributeViews().contains("posix"), "sin permisos POSIX");
        Path dir = tmp.resolve("grabacion");
        Recorder r = new Recorder(dir);
        r.record("POST", "/api/v1/runs", "{\"name\":\"x\"}".getBytes(StandardCharsets.UTF_8), "application/json");
        r.record("POST", "/api/v1/tests/-1/screenshot", "PNG secreto".getBytes(StandardCharsets.UTF_8), "image/png");
        r.close();
        int files = 0;
        try (Stream<Path> all = Files.walk(dir)) {
            for (Path p : (Iterable<Path>) all::iterator) {
                String perms = PosixFilePermissions.toString(Files.getPosixFilePermissions(p));
                if (Files.isDirectory(p)) {
                    assertEquals("rwx------", perms, p.toString());
                } else {
                    assertEquals("rw-------", perms, p.toString());
                    files++;
                }
            }
        }
        assertTrue(files >= 3, "solo " + files + " archivos");
    }
}
