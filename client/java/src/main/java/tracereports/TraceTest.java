package tracereports;

import java.io.ByteArrayOutputStream;
import java.io.PrintWriter;
import java.io.StringWriter;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.Consumer;
import java.util.regex.Pattern;

/**
 * Un test en curso. Si el servidor no respondió al crearlo, todos sus métodos son no-op: los tests
 * siguen corriendo.
 */
public final class TraceTest {
    private final TraceReports cr;
    private final long id;
    private final List<Expected> expected = new ArrayList<>();
    private final List<Consumer<Boolean>> beforeFinish = new ArrayList<>();
    private boolean hooksRan;
    private boolean finished;
    private int attempts = 1;

    private record Expected(List<Integer> status, String url, String method) {}

    TraceTest(TraceReports cr, long id) {
        this.cr = cr;
        this.id = id;
    }

    /** Id en el servidor, o 0 si no se pudo registrar. */
    public long id() { return id; }

    public boolean active() { return id != 0 && cr.enabled; }

    // ─── pasos ───────────────────────────────────────────────────────────

    public void log(Status status, String message) {
        if (!active()) return;
        Map<String, Object> p = new LinkedHashMap<>();
        p.put("status", status.name());
        p.put("message", String.valueOf(message));
        p.put("timestamp", System.currentTimeMillis());
        cr.emitJson("POST", "/api/v1/tests/" + id + "/logs", p);
    }

    public void info(String message) { log(Status.INFO, message); }
    public void pass(String message) { log(Status.PASS, message); }
    public void fail(String message) { log(Status.FAIL, message); }
    public void warn(String message) { log(Status.WARNING, message); }
    public void skip(String message) { log(Status.SKIP, message); }

    /** Captura como paso (PNG/JPEG). Selenium: {@code getScreenshotAs(OutputType.BYTES)}; Playwright: {@code page.screenshot()}. */
    public void screenshot(byte[] png, String message) {
        screenshot(png, message, Status.INFO);
    }

    public void screenshot(byte[] png, String message, Status status) {
        if (!active() || png == null || png.length == 0) return;
        String boundary = "----tracereports" + UUID.randomUUID().toString().replace("-", "");
        ByteArrayOutputStream body = new ByteArrayOutputStream();
        String head = part(boundary, "message", message == null ? "" : message) + part(boundary, "status", status.name())
                + "--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"screenshot.png\"\r\n"
                + "Content-Type: image/png\r\n\r\n";
        body.writeBytes(head.getBytes(StandardCharsets.UTF_8));
        body.writeBytes(png);
        body.writeBytes(("\r\n--" + boundary + "--\r\n").getBytes(StandardCharsets.UTF_8));
        cr.emit("POST", "/api/v1/tests/" + id + "/screenshot", body.toByteArray(), "multipart/form-data; boundary=" + boundary, cr.uploadTimeout);
    }

    private static String part(String boundary, String name, String value) {
        return "--" + boundary + "\r\nContent-Disposition: form-data; name=\"" + name + "\"\r\n\r\n" + value + "\r\n";
    }

    // ─── red y DOM ───────────────────────────────────────────────────────

    /**
     * Declara una respuesta negativa que el test verifica a propósito (p. ej. un 401 con credenciales
     * inválidas): no cuenta como error ni como causa del fallo. {@code url}: texto contenido o glob con *.
     */
    public void expectResponse(int status, String url) {
        expectResponse(List.of(status), url, "");
    }

    public void expectResponse(List<Integer> statuses, String url, String method) {
        expected.add(new Expected(List.copyOf(statuses), url == null ? "" : url, method == null ? "" : method.toUpperCase()));
    }

    boolean isExpected(Map<String, Object> c) {
        int status = c.get("status") instanceof Number n ? n.intValue() : 0;
        String url = String.valueOf(c.getOrDefault("url", ""));
        String method = String.valueOf(c.getOrDefault("method", "")).toUpperCase();
        for (Expected e : expected) {
            if (!e.status().contains(status)) continue;
            if (!e.method().isEmpty() && !e.method().equals(method)) continue;
            if (e.url().isEmpty()) return true;
            if (!e.url().contains("*") ? url.contains(e.url()) : globMatches(e.url(), url)) return true;
        }
        return false;
    }

    private static boolean globMatches(String glob, String text) {
        StringBuilder re = new StringBuilder();
        for (String part : glob.split("\\*", -1)) {
            if (re.length() > 0) re.append(".*");
            re.append(Pattern.quote(part));
        }
        return Pattern.compile(re.toString()).matcher(text).matches();
    }

    /**
     * Llamadas de red del test (pestaña "Red"), con las claves del servidor: method, url, status,
     * status_text, resource_type, failed, error_text, started_at, duration_ms, request_headers,
     * post_data, response_headers, response_body... Antes de finish.
     */
    public void network(List<Map<String, Object>> connections) {
        if (!active() || connections == null || connections.isEmpty()) return;
        List<Map<String, Object>> batch = new ArrayList<>();
        for (Map<String, Object> c : connections) {
            Map<String, Object> copy = new LinkedHashMap<>(c);
            Object body = copy.get("response_body");
            if (body instanceof String s && s.length() > 256 * 1024) {
                copy.put("body_size", s.length());
                copy.put("response_body", s.substring(0, 256 * 1024));
                copy.put("body_truncated", true);
            }
            if (Boolean.TRUE.equals(copy.get("expected")) || isExpected(copy)) copy.put("expected", true);
            batch.add(copy);
            if (batch.size() == 200) {
                sendNetwork(batch);
                batch = new ArrayList<>();
            }
        }
        if (!batch.isEmpty()) sendNetwork(batch);
    }

    private void sendNetwork(List<Map<String, Object>> batch) {
        cr.emit("POST", "/api/v1/tests/" + id + "/network",
                Json.write(Map.of("connections", batch)).getBytes(StandardCharsets.UTF_8), "application/json", cr.uploadTimeout);
    }

    /** Snapshot de la página al fallar (ver {@link Dom}); recomienda selectores si se rompe un locator. */
    public void dom(Object snapshot) {
        if (!active() || snapshot == null) return;
        cr.emit("POST", "/api/v1/tests/" + id + "/dom", Json.write(snapshot).getBytes(StandardCharsets.UTF_8), "application/json", cr.uploadTimeout);
    }

    // ─── cierre ──────────────────────────────────────────────────────────

    /**
     * Registra algo que hacer justo antes de cerrar el test, con la página todavía abierta (enviar la
     * red, o la captura y el DOM si falló). Recibe {@code true} si el test falló.
     */
    public void beforeFinish(Consumer<Boolean> hook) {
        beforeFinish.add(hook);
    }

    /** Corre los hooks de {@link #beforeFinish} una sola vez (la extensión de JUnit lo hace antes de @AfterEach). */
    public void runFinishHooks(boolean failed) {
        if (hooksRan) return;
        hooksRan = true;
        for (Consumer<Boolean> h : beforeFinish) {
            try {
                h.accept(failed);
            } catch (RuntimeException ignored) {
                // la evidencia nunca rompe el test
            }
        }
    }

    /** Veces que se ejecutó (reintentos): un PASS con más de 1 se muestra como "pasó tras reintento". */
    public void attempts(int n) { attempts = Math.max(1, n); }

    /** Cierra el test con un estado (null = deducirlo de los pasos). Un FAIL dispara el diagnóstico con IA. */
    public void finish(Status status) {
        finish(status, "", "");
    }

    /** Cierra el test como fallido a partir de la excepción (mensaje resumido y stack trace completo). */
    public void finish(Throwable error) {
        StringWriter sw = new StringWriter();
        error.printStackTrace(new PrintWriter(sw));
        finish(Status.FAIL, summary(error), sw.toString());
    }

    private static final Pattern PLAYWRIGHT_MESSAGE = Pattern.compile("message='([^'\\n]*)");

    /**
     * Una línea legible del error: "Clase: mensaje". Las excepciones de Playwright para Java traen un
     * bloque {@code Error { message='...' }} con el call log: se toma su mensaje (el detalle queda en la traza).
     */
    public static String summary(Throwable error) {
        String m = String.valueOf(error.getMessage());
        java.util.regex.Matcher pw = PLAYWRIGHT_MESSAGE.matcher(m);
        if (pw.find()) m = pw.group(1);
        for (String line : m.split("\\R")) {
            if (!line.isBlank()) {
                m = line.trim();
                break;
            }
        }
        if (m.length() > 300) m = m.substring(0, 300) + "…";
        return error.getClass().getSimpleName() + ": " + m;
    }

    public void finish(Status status, String errorMessage, String errorTrace) {
        if (!active() || finished) return;
        runFinishHooks(status == Status.FAIL);
        finished = true;
        Map<String, Object> p = new LinkedHashMap<>();
        p.put("status", status == null ? "" : status.name());
        p.put("error_message", errorMessage == null ? "" : errorMessage);
        p.put("error_trace", errorTrace == null ? "" : errorTrace);
        if (attempts > 1) p.put("attempts", attempts);
        cr.emitJson("PATCH", "/api/v1/tests/" + id + "/finish", p);
    }
}
