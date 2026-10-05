package tracereports;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayDeque;
import java.util.Deque;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.function.Supplier;
import java.util.logging.Logger;

/**
 * Transporte: la evidencia sale desde un hilo en segundo plano, en orden, con reintentos (backoff)
 * ante caídas, timeouts, 5xx, 408 y 429. Cada envío lleva una Idempotency-Key: un reintento nunca
 * duplica un paso en el servidor. Cola acotada: si se llena, los eventos nuevos se descartan y se
 * cuentan. Si el servidor no responde, las llamadas síncronas fallan al instante durante un rato.
 */
final class Sender {
    private static final Logger LOG = Logger.getLogger("tracereports");
    private static final Set<Integer> RETRYABLE = Set.of(408, 425, 429, 500, 502, 503, 504);
    static long circuitMillis = 30_000;
    static long maxBackoffMillis = 30_000;

    enum Kind { OK, RETRY, REJECT }

    static final class Item {
        final String method, path, contentType, key = UUID.randomUUID().toString();
        final byte[] body;
        final Duration timeout;
        int attempts;
        boolean abandoned;

        Item(String method, String path, byte[] body, String contentType, Duration timeout) {
            this.method = method;
            this.path = path;
            this.body = body;
            this.contentType = contentType;
            this.timeout = timeout;
        }
    }

    record Result(Kind kind, String body) {}

    private final String baseUrl;
    private final Supplier<Map<String, String>> headers;
    private final HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(3)).build();
    private final int maxItems;
    private final long maxBytes;
    private final Deque<Item> queue = new ArrayDeque<>();
    private Item inflight;
    private long bytes;
    private Thread worker;
    private volatile long downUntil;

    int sent, retried, rejected, dropped, lost;

    Sender(String baseUrl, Supplier<Map<String, String>> headers, int maxItems, long maxBytes) {
        this.baseUrl = baseUrl;
        this.headers = headers;
        this.maxItems = maxItems;
        this.maxBytes = maxBytes;
    }

    boolean serverDown() {
        return System.currentTimeMillis() < downUntil;
    }

    synchronized int pending() {
        return queue.size() + (inflight != null ? 1 : 0);
    }

    Result post(Item item) {
        try {
            HttpRequest.Builder rb = HttpRequest.newBuilder(URI.create(baseUrl + item.path))
                    .timeout(item.timeout)
                    .method(item.method, HttpRequest.BodyPublishers.ofByteArray(item.body))
                    .header("Content-Type", item.contentType)
                    .header("Accept", "application/json")
                    .header("Idempotency-Key", item.key);
            headers.get().forEach(rb::header);
            HttpResponse<String> res = http.send(rb.build(), HttpResponse.BodyHandlers.ofString());
            int code = res.statusCode();
            if (code >= 200 && code < 300) {
                downUntil = 0;
                return new Result(Kind.OK, res.body());
            }
            if (RETRYABLE.contains(code)) return new Result(Kind.RETRY, null);
            String hint = code == 401 ? " (configura el token: TRACEREPORTS_TOKEN en el entorno o en el .env del proyecto)" : "";
            String body = res.body() == null ? "" : res.body();
            LOG.warning("tracereports: " + item.method + " " + item.path + " -> HTTP " + code + " "
                    + body.substring(0, Math.min(300, body.length())) + hint);
            return new Result(Kind.REJECT, null);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            return new Result(Kind.RETRY, null);
        } catch (Exception e) {
            if (!serverDown()) LOG.warning("tracereports: " + item.method + " " + item.path + " falló: " + e);
            downUntil = System.currentTimeMillis() + circuitMillis;
            return new Result(Kind.RETRY, null);
        }
    }

    /** Llamada cuya respuesta se necesita (ids): reintentos cortos con la misma Idempotency-Key. */
    String sendNow(String method, String path, byte[] body, String contentType, Duration timeout, int retries, boolean ignoreCircuit) {
        if (serverDown() && !ignoreCircuit) return null;
        Item item = new Item(method, path, body, contentType, timeout);
        long[] backoff = {300, 1000, 2000};
        for (int attempt = 0; attempt <= retries; attempt++) {
            Result r = post(item);
            if (r.kind() == Kind.OK) {
                synchronized (this) { sent++; }
                return r.body() == null ? "" : r.body();
            }
            if (r.kind() == Kind.REJECT || (serverDown() && !ignoreCircuit) || attempt == retries) break;
            synchronized (this) { retried++; }
            sleep(backoff[Math.min(attempt, 2)]);
        }
        return null;
    }

    /** Encola un evento de evidencia; false si la cola está llena. */
    synchronized boolean enqueue(String method, String path, byte[] body, String contentType, Duration timeout) {
        if (pending() + 1 > maxItems || bytes + body.length > maxBytes) {
            if (dropped++ == 0) {
                LOG.warning("tracereports: la cola de envío está llena (" + maxItems + " eventos); se descartan los eventos nuevos");
            }
            return false;
        }
        queue.addLast(new Item(method, path, body, contentType, timeout));
        bytes += body.length;
        if (worker == null || !worker.isAlive()) {
            worker = new Thread(this::run, "tracereports-sender");
            worker.setDaemon(true);
            worker.start();
        }
        notifyAll();
        return true;
    }

    private void run() {
        while (true) {
            Item item;
            synchronized (this) {
                while (queue.isEmpty()) {
                    try {
                        wait();
                    } catch (InterruptedException e) {
                        return;
                    }
                }
                item = queue.pollFirst();
                inflight = item;
            }
            Result r;
            try {
                r = post(item);
            } catch (RuntimeException e) {
                r = new Result(Kind.RETRY, null);
            }
            long delay = 0;
            synchronized (this) {
                inflight = null;
                if (!item.abandoned) {
                    if (r.kind() == Kind.RETRY) {
                        item.attempts++;
                        retried++;
                        queue.addFirst(item);
                        delay = Math.min(maxBackoffMillis, 500L << Math.min(item.attempts, 6));
                    } else {
                        bytes -= item.body.length;
                        if (r.kind() == Kind.OK) sent++;
                        else rejected++;
                    }
                } else if (r.kind() == Kind.OK) { // se contó como perdido al cerrar, pero llegó
                    lost--;
                    sent++;
                }
                notifyAll();
            }
            if (delay > 0) sleep(delay);
        }
    }

    /** Espera a que la cola se vacíe (máx. timeout). Devuelve lo que quedó pendiente. */
    synchronized int flush(Duration timeout) {
        long deadline = System.currentTimeMillis() + timeout.toMillis();
        while (pending() > 0) {
            long left = deadline - System.currentTimeMillis();
            if (left <= 0) break;
            try {
                wait(Math.min(left, 200));
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }
        return pending();
    }

    /** Descarta lo pendiente al cerrar y lo cuenta como perdido. */
    synchronized int abandon() {
        int n = queue.size();
        for (Item it : queue) it.abandoned = true;
        queue.clear();
        if (inflight != null) {
            inflight.abandoned = true;
            inflight = null;
            n++;
        }
        bytes = 0;
        lost += n;
        if (n > 0) LOG.warning("tracereports: " + n + " eventos de evidencia no se pudieron enviar y se perdieron");
        notifyAll();
        return n;
    }

    private static void sleep(long ms) {
        try {
            Thread.sleep(ms);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }
}
