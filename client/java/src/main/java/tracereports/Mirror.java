package tracereports;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.*;
import java.util.logging.Logger;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Copia de cada llamada lógica; los reintentos y la cola siguen en Sender. */
final class Mirror {
    private static final Logger LOG = Logger.getLogger("tracereports");
    private static final Pattern PATH = Pattern.compile("^/api/v1/(runs|tests)/(-?\\d+)");
    final Path dir;
    Recorder recorder;
    private String status;
    private final Map<Long, Long> runs = new HashMap<>(), tests = new HashMap<>();
    private boolean failed, disabled, closed;
    private int active;

    Mirror(Path dir, String server, long runId, Map<String, Object> payload) throws IOException {
        this.dir = dir;
        Files.createDirectories(dir);
        try {
            locked(() -> {
                Path marker = dir.resolve(Recorder.MARKER);
                if (Files.exists(marker) && Boolean.TRUE.equals(read(marker).get("raw_removed"))) throw new IOException("la carpeta solo contiene el reporte; usa otra carpeta");
                status = "status-" + ProcessHandle.current().pid() + "-" + UUID.randomUUID().toString().substring(0, 8) + ".json";
                write(status, Map.of("complete", false, "reason", "active"));
                recorder = new Recorder(dir);
                String actual = "status-" + recorder.pid + "-" + recorder.tag + ".json";
                Files.move(dir.resolve(status), dir.resolve(actual)); status = actual;
                Map<String, Object> m = read(marker);
                Map<String, Object> mirror = m.containsKey("mirror") ? object(m.get("mirror")) : new LinkedHashMap<>(Map.of("server", server, "runs", new ArrayList<>()));
                if (!server.equals(mirror.get("server"))) throw new IOException("la carpeta pertenece a otro servidor");
                m.put("mirror", mirror); mirror.put("complete", false); write(Recorder.MARKER, m);
                Path mapping = dir.resolve("ids/server-" + runId);
                long local = 0;
                if (payload != null) {
                    String response = recorder.record("POST", "/api/v1/runs", Json.write(payload).getBytes(StandardCharsets.UTF_8), "application/json");
                    if (response == null) throw new IOException("no se pudo grabar la ejecución");
                    local = Json.number(response, "run_id");
                    Files.writeString(mapping, Long.toString(local), StandardOpenOption.CREATE_NEW);
                    List<Object> list = new ArrayList<>((List<?>) mirror.get("runs"));
                    list.add(Map.of("local", local, "server", runId)); mirror.put("runs", list);
                    write(Recorder.MARKER, m);
                } else {
                    try {
                        local = Long.parseLong(Files.readString(mapping).trim());
                        if (local >= 0) throw new IllegalArgumentException("id local inválido");
                    } catch (IOException | IllegalArgumentException e) {
                        failed = true; local = 0;
                        LOG.warning("tracereports: falta el id local de la ejecución #" + runId + ": " + e);
                    }
                }
                if (local != 0) runs.put(runId, local);
            });
        } catch (IOException | RuntimeException e) {
            if (recorder != null) recorder.close();
            throw e;
        }
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> object(Object value) { return (Map<String, Object>) value; }
    private static Map<String, Object> read(Path file) throws IOException { return object(Json.read(Files.readString(file))); }
    private void write(String name, Object value) throws IOException {
        Path dest = dir.resolve(name), tmp = dir.resolve(".mirror-" + UUID.randomUUID().toString().substring(0, 8) + ".tmp");
        try {
            Files.writeString(tmp, Json.write(value));
            try { Files.move(tmp, dest, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING); }
            catch (AtomicMoveNotSupportedException e) { Files.move(tmp, dest, StandardCopyOption.REPLACE_EXISTING); }
        } finally { Files.deleteIfExists(tmp); }
    }
    private interface Action { void run() throws IOException; }
    private void locked(Action action) throws IOException {
        Path lock = dir.resolve(".mirror-lock");
        for (int n = 0; ; n++) {
            try { Files.createDirectory(lock); break; }
            catch (FileAlreadyExistsException e) {
                if (n == 50) throw e;
                try { Thread.sleep(10); } catch (InterruptedException interrupted) { Thread.currentThread().interrupt(); throw new IOException(interrupted); }
            }
        }
        try { action.run(); } finally { Files.delete(lock); }
    }
    private void diskError(Exception e) {
        failed = true;
        if (!disabled) LOG.warning("tracereports: se detuvo la copia local en " + dir + ": " + e);
        disabled = true;
    }
    synchronized String record(String method, String path, byte[] body, String contentType) {
        if (disabled || closed) return null;
        try {
            Matcher match = PATH.matcher(path);
            if (match.find()) {
                long id = Long.parseLong(match.group(2));
                Map<Long, Long> ids = match.group(1).equals("runs") ? runs : tests;
                Long local = ids.get(id);
                if (id > 0 && local == null) {
                    failed = true;
                    local = recorder.localId();
                    ids.put(id, local);
                }
                if (local != null) path = "/api/v1/" + match.group(1) + "/" + local + path.substring(match.end());
            }
            String response = recorder.record(method, path, body, contentType);
            if (response == null) throw new IOException("no se pudo escribir la evidencia");
            return response;
        } catch (IOException | RuntimeException e) { diskError(e); return null; }
    }
    synchronized void begin() { active++; }
    synchronized String delivered(String local, String remote) {
        active--;
        if (remote == null) failed = true;
        long l = Json.number(local, "test_id"), r = Json.number(remote, "test_id");
        if (l < 0 && r > 0) tests.put(r, l);
        return remote != null ? remote : l < 0 ? local : null;
    }
    synchronized void rejected() { failed = true; }
    synchronized boolean isClosed() { return closed; }
    synchronized void close(boolean complete) {
        if (closed) return;
        closed = true; recorder.close();
        boolean ok = complete && !failed && active == 0;
        try { locked(() -> write(status, Map.of("complete", ok, "reason", ok ? "" : "delivery or recording incomplete"))); }
        catch (IOException | RuntimeException e) { diskError(e); }
    }
    private String snapshot() throws IOException {
        Map<String, String> entries = new TreeMap<>();
        try (var files = Files.newDirectoryStream(dir, "status-*.json")) {
            for (Path file : files) {
                String raw = Files.readString(file);
                if (!Boolean.TRUE.equals(object(Json.read(raw)).get("complete"))) return null;
                entries.put(file.getFileName().toString(), raw);
            }
        }
        if (entries.isEmpty()) return null;
        try (var files = Files.newDirectoryStream(dir, "events-*.jsonl")) {
            for (Path file : files) if (!entries.containsKey(file.getFileName().toString().replaceFirst("^events-", "status-").replaceFirst("\\.jsonl$", ".json"))) return null;
        }
        return Json.write(entries);
    }
    synchronized String prepare() {
        String[] result = {null};
        try { locked(() -> result[0] = snapshot()); } catch (IOException | RuntimeException e) { diskError(e); }
        return result[0];
    }
    synchronized void finish(Path report, boolean keep, String before) {
        try {
            locked(() -> {
                String now = snapshot();
                Map<String, Object> marker = read(dir.resolve(Recorder.MARKER));
                boolean complete = !failed && now != null;
                object(marker.get("mirror")).put("complete", complete);
                boolean remove = complete && now.equals(before) && report != null && Files.isRegularFile(report) && !keep;
                if (remove) marker.put("raw_removed", true);
                write(Recorder.MARKER, marker);
                if (remove) {
                    try (var entries = Files.newDirectoryStream(dir)) {
                        for (Path entry : entries) {
                            String n = entry.getFileName().toString();
                            if (n.matches("(events-.*\\.jsonl|status-.*\\.json)") || n.equals("bodies") || n.equals("ids")) {
                                try (var paths = Files.walk(entry)) {
                                    for (Path p : paths.sorted(Comparator.reverseOrder()).toList()) Files.delete(p);
                                }
                            }
                        }
                    }
                }
            });
        } catch (IOException | RuntimeException e) { diskError(e); }
    }
}
