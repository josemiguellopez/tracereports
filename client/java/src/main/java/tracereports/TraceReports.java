package tracereports;

import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.logging.Logger;

/**
 * Cliente de TraceReports para Java (17+, sin dependencias).
 *
 * <pre>{@code
 * TraceReports cr = new TraceReports();                  // $TRACEREPORTS_URL o http://localhost:8080
 * cr.startRun("Regresión web", "staging");
 * TraceTest t = cr.startTest(new TestInfo("Login").key("LoginTest#adminEntra").category("smoke"));
 * t.info("Abrir el login");
 * t.screenshot(((TakesScreenshot) driver).getScreenshotAs(OutputType.BYTES), "Formulario"); // Selenium
 * t.screenshot(page.screenshot(), "Formulario");                                          // Playwright
 * t.finish(Status.PASS);
 * cr.finishRun();                                           // espera la cola y cierra la ejecución
 * }</pre>
 *
 * <p>Nunca rompe ni frena la suite: la evidencia sale en segundo plano con reintentos e
 * Idempotency-Key (sin duplicados); si el servidor no responde, los métodos siguen funcionando como
 * no-op y {@link #delivery()} dice qué no llegó. Con JUnit 5, usa {@code tracereports.junit.TraceReportsExtension}.
 */
public final class TraceReports {
    private static final Logger LOG = Logger.getLogger("tracereports");
    public static final String VERSION = "0.1.0";

    final String baseUrl;
    final String token;
    final boolean enabled;
    final Duration timeout;
    final Duration uploadTimeout;
    final Duration flushTimeout;
    final Sender sender;
    private volatile long runId;
    private volatile boolean runCreated;
    private final AtomicInteger unregisteredTests = new AtomicInteger();
    private volatile boolean runNotClosed;

    /**
     * Configuración desde las propiedades {@code -Dtracereports.url} / {@code -Dtracereports.token} o el entorno:
     * TRACEREPORTS_URL, TRACEREPORTS_TOKEN, TRACEREPORTS_DISABLED, TRACEREPORTS_FLUSH_TIMEOUT.
     */
    public TraceReports() {
        this(null, null);
    }

    /** @param baseUrl URL del servidor (null = $TRACEREPORTS_URL); @param token token de la API (null = $TRACEREPORTS_TOKEN). */
    public TraceReports(String baseUrl, String token) {
        String url = baseUrl != null ? baseUrl : Context.property("url", Context.env("TRACEREPORTS_URL"));
        this.baseUrl = (url.isEmpty() ? "http://localhost:8080" : url).replaceAll("/+$", "");
        this.token = token != null ? token : Context.property("token", Context.env("TRACEREPORTS_TOKEN"));
        String off = Context.env("TRACEREPORTS_DISABLED").toLowerCase();
        this.enabled = !(off.equals("1") || off.equals("true") || off.equals("yes"));
        this.timeout = Duration.ofSeconds(3);
        this.uploadTimeout = Duration.ofSeconds(15);
        long flush = 30;
        try {
            flush = Long.parseLong(Context.env("TRACEREPORTS_FLUSH_TIMEOUT"));
        } catch (NumberFormatException ignored) {
            // default
        }
        this.flushTimeout = Duration.ofSeconds(flush);
        this.sender = new Sender(this.baseUrl, this::headers, 5000, 64L << 20);
    }

    Map<String, String> headers() {
        Map<String, String> h = new LinkedHashMap<>();
        h.put("User-Agent", "tracereports-java/" + VERSION);
        if (!token.isEmpty()) h.put("Authorization", "Bearer " + token);
        return h;
    }

    public String baseUrl() { return baseUrl; }

    public boolean enabled() { return enabled; }

    /** Id de la ejecución, o 0 si no hay. */
    public long runId() { return runId; }

    /** Link a la ejecución en la interfaz web. */
    public String reportUrl() {
        return runId == 0 ? "" : baseUrl + "/#run=" + runId + "&view=dashboard";
    }

    public Delivery delivery() {
        synchronized (sender) {
            return new Delivery(sender.sent, sender.retried, sender.rejected, sender.dropped, sender.lost,
                    sender.pending(), unregisteredTests.get(), runNotClosed ? 1 : 0);
        }
    }

    String request(String method, String path, Map<String, Object> payload) {
        if (!enabled) return null;
        return sender.sendNow(method, path, Json.write(payload).getBytes(StandardCharsets.UTF_8), "application/json", timeout, 2, false);
    }

    boolean emit(String method, String path, byte[] body, String contentType, Duration t) {
        return enabled && sender.enqueue(method, path, body, contentType, t);
    }

    boolean emitJson(String method, String path, Object payload) {
        return emit(method, path, Json.write(payload).getBytes(StandardCharsets.UTF_8), "application/json", timeout.plusSeconds(2));
    }

    // ─── ejecuciones ─────────────────────────────────────────────────────

    /** Crea la ejecución (proyecto: $TRACEREPORTS_PROJECT o la carpeta actual; rama y commit: CI o git). */
    public long startRun(String name, String environment) {
        return startRun(new RunInfo(name).environment(environment));
    }

    /** Crea la ejecución. Con $TRACEREPORTS_RUN_ID se une a una ya creada (shards de CI) y no la cierra. */
    public long startRun(RunInfo info) {
        String shared = Context.env("TRACEREPORTS_RUN_ID");
        if (!shared.isEmpty()) return joinRun(Long.parseLong(shared));
        Map<String, Object> p = new LinkedHashMap<>();
        p.put("name", info.name);
        p.put("environment", info.environment);
        String project = info.project != null ? info.project : Context.env("TRACEREPORTS_PROJECT");
        if (project.isEmpty()) {
            Path cwd = Path.of("").toAbsolutePath().getFileName();
            project = cwd == null ? "" : cwd.toString();
        }
        p.put("project", project);
        p.put("branch", info.branch != null ? info.branch : Context.branch());
        p.put("commit", info.commit != null ? info.commit : Context.commit());
        p.put("framework", info.framework);
        runId = Json.number(request("POST", "/api/v1/runs", p), "run_id");
        runCreated = runId != 0;
        if (enabled && runId == 0) {
            LOG.warning("tracereports: no se pudo crear la ejecución en " + baseUrl + "; los tests siguen sin reporte.");
        }
        return runId;
    }

    /** Reporta en una ejecución creada en otro proceso; quien la creó la cierra. */
    public long joinRun(long id) {
        runId = id;
        runCreated = false;
        return runId;
    }

    /** Espera la cola (TRACEREPORTS_FLUSH_TIMEOUT, 30 s) y cierra la ejecución. */
    public void finishRun() {
        finishRun(false);
    }

    /** interrupted = true: la ejecución queda incompleta (nunca aparece como exitosa). */
    public void finishRun(boolean interrupted) {
        flush(flushTimeout);
        sender.abandon();
        if (runId == 0 || !runCreated || !enabled) return; // unido a una ejecución ajena: la cierra su dueño
        // el cierre importa más que un paso: más reintentos (misma Idempotency-Key), aunque el circuito esté abierto
        String res = sender.sendNow("PATCH", "/api/v1/runs/" + runId + "/finish",
                Json.write(Map.of("interrupted", interrupted)).getBytes(StandardCharsets.UTF_8), "application/json", timeout, 4, true);
        runNotClosed = res == null;
        if (runNotClosed) {
            LOG.warning("tracereports: el servidor no confirmó el cierre de la ejecución #" + runId + ": quedó abierta");
        }
    }

    /** Espera a que la evidencia encolada se envíe. Devuelve lo que quedó pendiente. */
    public int flush(Duration max) {
        return sender.flush(max);
    }

    // ─── tests ───────────────────────────────────────────────────────────

    /** Inicia un test identificado por su nombre (historial aproximado: mejor usa {@link TestInfo#key}). */
    public TraceTest startTest(String name) {
        return startTest(new TestInfo(name));
    }

    /** Inicia un test. Siempre devuelve un TraceTest (no-op si el servidor no respondió). */
    public TraceTest startTest(TestInfo info) {
        if (runId == 0) return new TraceTest(this, 0);
        Map<String, Object> p = new LinkedHashMap<>();
        p.put("name", info.name);
        p.put("category", info.category);
        p.put("description", info.description);
        p.put("key", info.key);
        p.put("suite", info.suite);
        p.put("params", info.params);
        p.put("worker", info.worker);
        long id = Json.number(request("POST", "/api/v1/runs/" + runId + "/tests", p), "test_id");
        if (enabled && id == 0) unregisteredTests.incrementAndGet();
        return new TraceTest(this, id);
    }

    /** Datos de una ejecución. */
    public static final class RunInfo {
        final String name;
        String environment = "", project, branch, commit, framework = "";

        public RunInfo(String name) { this.name = name; }
        public RunInfo environment(String v) { environment = v == null ? "" : v; return this; }
        public RunInfo project(String v) { project = v; return this; }
        public RunInfo branch(String v) { branch = v; return this; }
        public RunInfo commit(String v) { commit = v; return this; }
        public RunInfo framework(String v) { framework = v == null ? "" : v; return this; }
    }

    /**
     * Datos de un test. {@code key} es su identidad estable dentro del proyecto (clase#método,
     * con los parámetros si es parametrizado): el historial y la detección de flaky la siguen.
     */
    public static final class TestInfo {
        final String name;
        String category = "", description = "", key = "", suite = "", params = "", worker = "";

        public TestInfo(String name) { this.name = name; }
        public TestInfo category(String v) { category = v == null ? "" : v; return this; }
        public TestInfo description(String v) { description = v == null ? "" : v; return this; }
        public TestInfo key(String v) { key = v == null ? "" : v; return this; }
        public TestInfo suite(String v) { suite = v == null ? "" : v; return this; }
        public TestInfo params(String v) { params = v == null ? "" : v; return this; }
        public TestInfo worker(String v) { worker = v == null ? "" : v; return this; }
    }
}
