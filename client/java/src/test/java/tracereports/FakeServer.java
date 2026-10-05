package tracereports;

import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.stream.Collectors;

/** Servidor de TraceReports mínimo para las pruebas: guarda lo que recibe y puede fallar a propósito. */
public final class FakeServer implements AutoCloseable {
    public record Req(String method, String path, String key, String auth, byte[] body) {
        public String text() { return new String(body, StandardCharsets.UTF_8); }
    }

    private final HttpServer server;
    public final List<Req> requests = new ArrayList<>();
    public final AtomicInteger failNext = new AtomicInteger();
    /** Rutas que siempre responden 503 (p. ej. el cierre de la ejecución). */
    public final java.util.Set<String> failPaths = java.util.concurrent.ConcurrentHashMap.newKeySet();

    public FakeServer() throws IOException {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/", ex -> {
            byte[] body = ex.getRequestBody().readAllBytes();
            if (failPaths.contains(ex.getRequestURI().getPath()) || failNext.getAndUpdate(n -> Math.max(0, n - 1)) > 0) {
                ex.sendResponseHeaders(503, -1);
                ex.close();
                return;
            }
            String path = ex.getRequestURI().getPath();
            synchronized (requests) {
                requests.add(new Req(ex.getRequestMethod(), path, ex.getRequestHeaders().getFirst("Idempotency-Key"),
                        ex.getRequestHeaders().getFirst("Authorization"), body));
            }
            String out = path.equals("/api/v1/runs") ? "{\"run_id\":7}" : path.endsWith("/tests") ? "{\"test_id\":11}" : "{}";
            byte[] b = out.getBytes(StandardCharsets.UTF_8);
            ex.getResponseHeaders().add("Content-Type", "application/json");
            ex.sendResponseHeaders(201, b.length);
            ex.getResponseBody().write(b);
            ex.close();
        });
        server.start();
    }

    public String url() {
        return "http://127.0.0.1:" + server.getAddress().getPort();
    }

    public List<Req> at(String path) {
        synchronized (requests) {
            return requests.stream().filter(r -> r.path().equals(path)).collect(Collectors.toList());
        }
    }

    public List<String> paths() {
        synchronized (requests) {
            return requests.stream().map(r -> r.method() + " " + r.path()).collect(Collectors.toList());
        }
    }

    @Override
    public void close() {
        server.stop(0);
    }
}
