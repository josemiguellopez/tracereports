package tracereports;

import java.io.IOException;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.FileAlreadyExistsException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ThreadLocalRandom;

/**
 * Grabación local cuando no hay servidor (no responde o rechaza el token): en vez de perder la
 * evidencia, el cliente escribe en una carpeta las mismas llamadas a la API que habría hecho.
 * Después {@code tracereports report <carpeta>} arma el reporte HTML y {@code tracereports push
 * <carpeta>} la sube. Mismo formato que los clientes Python, JavaScript y Go.
 */
final class Recorder {
    static final String MARKER = "tracereports-offline.json";
    // Ids locales: cada grabador reserva bloques de ID_BLOCK ids creando <carpeta>/ids/<bloque> en
    // exclusiva, así dos grabadores que comparten la carpeta (mismo proceso, otro proceso u otra
    // sesión) nunca repiten un id. El bloque es aleatorio en [MIN_BLOCK, MAX_BLOCK): no choca con
    // los ids de grabaciones anteriores (pid % 10^6) y -(bloque * ID_BLOCK + n) es exacto en JS.
    static final long ID_BLOCK = 100_000;
    static final long MIN_BLOCK = 1_000_000;
    static final long MAX_BLOCK = 90_000_000_000L;

    final Path dir;
    private final OutputStream events;
    private final String tag = UUID.randomUUID().toString().substring(0, 8);
    private final long pid = ProcessHandle.current().pid();
    private long seq;
    private long nextId;
    private long block;
    long recorded;

    Recorder(Path dir) throws IOException {
        this.dir = dir;
        Files.createDirectories(dir.resolve("bodies"));
        try { // el primer proceso crea el marcador
            Files.writeString(dir.resolve(MARKER), Json.write(Map.of("format", "tracereports-offline", "version", 1,
                    "id", UUID.randomUUID().toString().replace("-", ""))), StandardOpenOption.CREATE_NEW, StandardOpenOption.WRITE);
        } catch (FileAlreadyExistsException ignored) {
            // otro proceso ya lo creó
        }
        this.events = Files.newOutputStream(dir.resolve("events-" + pid + "-" + tag + ".jsonl"),
                StandardOpenOption.CREATE, StandardOpenOption.APPEND);
    }

    /** Carpeta nueva para una sesión dentro de {@code base} (no mezcla corridas distintas). */
    static Path newSessionDir(Path base) {
        String stamp = LocalDateTime.now().format(DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss"));
        return base.resolve(stamp + "-" + UUID.randomUUID().toString().substring(0, 6));
    }

    /** Id local negativo, único entre los grabadores que graban en la misma carpeta. */
    private long localId() throws IOException {
        if (block == 0 || nextId >= ID_BLOCK - 1) {
            block = reserveBlock();
            nextId = 0;
        }
        nextId++;
        return -(block * ID_BLOCK + nextId);
    }

    private long reserveBlock() throws IOException {
        Path ids = Files.createDirectories(dir.resolve("ids"));
        for (int i = 0; i < 100; i++) {
            long candidate = ThreadLocalRandom.current().nextLong(MIN_BLOCK, MAX_BLOCK);
            try {
                Files.createFile(ids.resolve(Long.toString(candidate)));
                return candidate;
            } catch (FileAlreadyExistsException taken) {
                // ya es de otro grabador: se prueba otro
            }
        }
        throw new IOException("tracereports: could not reserve local ids in " + ids);
    }

    /** Graba la llamada y devuelve el JSON que habría respondido el servidor (ids locales). */
    synchronized String record(String method, String path, byte[] body, String contentType) {
        seq++;
        Map<String, Object> e = new LinkedHashMap<>();
        e.put("seq", seq);
        e.put("ts", System.currentTimeMillis());
        e.put("method", method);
        e.put("path", path);
        e.put("content_type", contentType);
        String raw = null;
        try {
            if (contentType.startsWith("application/json") && body.length > 0) {
                raw = new String(body, StandardCharsets.UTF_8); // ya es JSON (lo escribió Json.write)
            } else {
                String name = "bodies/" + pid + "-" + tag + "-" + seq + ".bin";
                Files.write(dir.resolve(name), body);
                e.put("body_file", name);
            }
            String out = "";
            if ("POST".equals(method) && "/api/v1/runs".equals(path)) {
                long id = localId();
                e.put("local_id", id);
                out = "{\"run_id\":" + id + "}";
            } else if ("POST".equals(method) && path.startsWith("/api/v1/runs/") && path.endsWith("/tests")) {
                long id = localId();
                e.put("local_id", id);
                out = "{\"test_id\":" + id + "}";
            }
            String line = Json.write(e);
            if (raw != null) line = line.substring(0, line.length() - 1) + ",\"body\":" + raw + "}";
            events.write((line + "\n").getBytes(StandardCharsets.UTF_8));
            events.flush(); // si el proceso muere, lo escrito queda
            recorded++;
            return out;
        } catch (IOException err) {
            return null;
        }
    }

    synchronized void close() {
        try {
            events.close();
        } catch (IOException ignored) {
            // nada que hacer
        }
    }
}
