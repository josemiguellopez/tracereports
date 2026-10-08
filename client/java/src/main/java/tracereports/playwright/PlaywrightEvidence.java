package tracereports.playwright;

import tracereports.TraceTest;
import tracereports.Dom;
import tracereports.Status;
import com.microsoft.playwright.Page;
import com.microsoft.playwright.Request;
import com.microsoft.playwright.Response;
import java.util.ArrayList;
import java.util.IdentityHashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Evidencia de Playwright para Java (necesita com.microsoft.playwright en tu proyecto).
 *
 * <pre>{@code
 * @Test void login(TraceTest t) {
 *   PlaywrightEvidence.attach(t, page);   // red del test + captura y DOM si falla
 *   page.navigate(URL);
 *   ...
 * }
 * }</pre>
 *
 * La red es la que hace el navegador (pestaña "Red"), no los logs del backend.
 */
public final class PlaywrightEvidence {
    private PlaywrightEvidence() {}

    private static final Set<String> BODY_TYPES = Set.of("xhr", "fetch");
    private static final int MAX_BODY = 256 * 1024;

    /** Captura las llamadas HTTP de una página (engánchala antes de navegar). */
    public static final class NetworkCapture {
        private final List<Map<String, Object>> conns = new ArrayList<>();
        private final Map<Request, Map<String, Object>> byRequest = new IdentityHashMap<>();
        private int reported;

        NetworkCapture(Page page) {
            page.onRequest(req -> {
                Map<String, Object> c = new LinkedHashMap<>();
                c.put("method", req.method());
                c.put("url", req.url());
                c.put("resource_type", req.resourceType());
                c.put("request_headers", req.headers());
                String post = req.postData();
                c.put("post_data", post == null ? "" : post);
                c.put("started_at", System.currentTimeMillis());
                c.put("status", 0);
                synchronized (this) {
                    conns.add(c);
                    byRequest.put(req, c);
                }
            });
            page.onResponse(res -> {
                Map<String, Object> c;
                synchronized (this) {
                    c = byRequest.get(res.request());
                }
                if (c == null) return;
                c.put("status", res.status());
                c.put("status_text", res.statusText());
                c.put("response_headers", res.headers());
                c.put("mime_type", res.headers().getOrDefault("content-type", ""));
                boolean api = BODY_TYPES.contains(String.valueOf(c.get("resource_type"))) || String.valueOf(c.get("url")).contains("/api/");
                if (api || res.status() >= 400) readBody(res, c);
            });
            page.onRequestFinished(req -> {
                Map<String, Object> c;
                synchronized (this) {
                    c = byRequest.get(req);
                }
                if (c != null) c.put("duration_ms", duration(req, c));
            });
            page.onRequestFailed(req -> {
                Map<String, Object> c;
                synchronized (this) {
                    c = byRequest.get(req);
                }
                if (c == null) return;
                c.put("failed", true);
                c.put("error_text", req.failure() == null ? "" : req.failure());
                c.put("duration_ms", System.currentTimeMillis() - (long) c.get("started_at"));
            });
        }

        static void readBody(Response res, Map<String, Object> c) {
            try {
                String body = res.text();
                // el recorte se marca aquí, donde ocurre; el tamaño original va en bytes UTF-8 (como el servidor)
                boolean cut = body.length() > MAX_BODY;
                c.put("body_size", (long) body.getBytes(java.nio.charset.StandardCharsets.UTF_8).length);
                c.put("body_truncated", cut);
                int end = cut && Character.isHighSurrogate(body.charAt(MAX_BODY - 1)) ? MAX_BODY - 1 : MAX_BODY;
                c.put("response_body", cut ? body.substring(0, end) : body);
            } catch (RuntimeException ignored) {
                // la página pudo cerrarse o el body no está disponible (redirecciones)
            }
        }

        private static long duration(Request req, Map<String, Object> c) {
            try {
                double end = req.timing().responseEnd;
                if (end > 0) return Math.round(end);
            } catch (RuntimeException ignored) {
                // sin timing
            }
            return System.currentTimeMillis() - (long) c.get("started_at");
        }

        /** Conexiones nuevas desde el último drain. */
        public synchronized List<Map<String, Object>> drain() {
            List<Map<String, Object>> out = new ArrayList<>(conns.subList(reported, conns.size()));
            reported = conns.size();
            return out;
        }
    }

    public static NetworkCapture captureNetwork(Page page) {
        return new NetworkCapture(page);
    }

    /** Captura la consola de una página: console.error, console.warn y errores de JavaScript sin manejar. */
    public static final class ConsoleCapture {
        private final List<Map<String, Object>> entries = new ArrayList<>();
        private int reported;

        ConsoleCapture(Page page) {
            page.onConsoleMessage(msg -> {
                String level = "warning".equals(msg.type()) ? "warning" : msg.type();
                if (!"error".equals(level) && !"warning".equals(level)) return;
                add(level, msg.text(), msg.location() == null ? "" : msg.location());
            });
            page.onPageError(error -> add("pageerror", error, ""));
        }

        private synchronized void add(String level, String text, String location) {
            Map<String, Object> e = new LinkedHashMap<>();
            e.put("level", level);
            e.put("text", text == null ? "" : text);
            e.put("location", location);
            e.put("timestamp", System.currentTimeMillis());
            entries.add(e);
        }

        /** Entradas nuevas desde el último drain. */
        public synchronized List<Map<String, Object>> drain() {
            List<Map<String, Object>> out = new ArrayList<>(entries.subList(reported, entries.size()));
            reported = entries.size();
            return out;
        }
    }

    public static ConsoleCapture captureConsole(Page page) {
        return new ConsoleCapture(page);
    }

    /** Snapshot de los elementos de la página (recomendador de locators), o null. */
    public static Object dom(Page page) {
        try {
            return page.evaluate(Dom.FUNCTION);
        } catch (RuntimeException e) {
            return null;
        }
    }

    /** Captura de pantalla en PNG, o null. */
    public static byte[] screenshot(Page page) {
        try {
            return page.screenshot(new Page.ScreenshotOptions().setTimeout(5000));
        } catch (RuntimeException e) {
            return null;
        }
    }

    /**
     * Engancha la captura de red a la página y, al cerrar el test (antes de @AfterEach), envía la red
     * y, si falló, la captura y el snapshot del DOM.
     */
    public static NetworkCapture attach(TraceTest t, Page page) {
        NetworkCapture net = captureNetwork(page);
        ConsoleCapture console = captureConsole(page);
        t.beforeFinish(failed -> {
            if (failed) {
                t.screenshot(screenshot(page), "Captura al fallar", Status.FAIL);
                t.dom(dom(page));
            }
            t.network(net.drain());
            t.console(console.drain());
        });
        return net;
    }
}
