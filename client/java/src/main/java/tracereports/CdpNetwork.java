package tracereports;

import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Convierte el log de rendimiento de Chrome y Edge (los eventos {@code Network.*} del DevTools
 * Protocol que escribe ChromeDriver) en las conexiones de {@link TraceTest#network}. Lo usa
 * {@code SeleniumEvidence}; no depende de Selenium.
 */
public final class CdpNetwork {
    private CdpNetwork() {}

    /** Clave interna de cada conexión con el requestId de CDP, para pedir su body después. */
    public static final String REQUEST_ID = "_request_id";
    /** Clave interna: la request tenía body pero el log no lo trajo (pedirlo con getRequestPostData). */
    public static final String HAS_POST_DATA = "_has_post_data";

    /**
     * Las conexiones de los mensajes del log "performance" (cada uno, el JSON de
     * {@code LogEntry.getMessage()}), en orden. Lleva {@link #REQUEST_ID} y {@link #HAS_POST_DATA},
     * que hay que quitar antes de enviarlas.
     */
    @SuppressWarnings("unchecked")
    public static List<Map<String, Object>> connections(List<String> messages) {
        Map<String, Map<String, Object>> byId = new HashMap<>();
        List<Map<String, Object>> order = new ArrayList<>();
        Map<String, Map<String, Object>> extraReq = new HashMap<>();
        Map<String, Map<String, Object>> extraRes = new HashMap<>();
        for (String raw : messages) {
            Map<String, Object> msg;
            try {
                msg = (Map<String, Object>) ((Map<String, Object>) Json.read(raw)).get("message");
            } catch (RuntimeException e) {
                continue;
            }
            if (msg == null || !(msg.get("method") instanceof String method) || !method.startsWith("Network.")) continue;
            Map<String, Object> p = msg.get("params") instanceof Map<?, ?> m ? (Map<String, Object>) m : Map.of();
            if (!(p.get("requestId") instanceof String id)) continue;
            switch (method) {
                case "Network.requestWillBeSent" -> {
                    Map<String, Object> prev = byId.get(id);
                    if (prev != null && p.get("redirectResponse") instanceof Map<?, ?> rr) {
                        // la redirección cierra el salto anterior con su propia respuesta
                        setResponse(prev, (Map<String, Object>) rr);
                        putDuration(prev, p.get("timestamp"));
                        prev.put(REQUEST_ID, null);
                    }
                    Map<String, Object> req = p.get("request") instanceof Map<?, ?> r ? (Map<String, Object>) r : Map.of();
                    Map<String, Object> c = new LinkedHashMap<>();
                    c.put(REQUEST_ID, id);
                    c.put("_ts", p.get("timestamp"));
                    c.put("method", str(req.get("method")));
                    c.put("url", str(req.get("url")));
                    c.put("resource_type", str(p.get("type")).toLowerCase());
                    c.put("request_headers", headers(req.get("headers")));
                    c.put("post_data", str(req.get("postData")));
                    c.put(HAS_POST_DATA, Boolean.TRUE.equals(req.get("hasPostData")) || req.get("postData") != null);
                    c.put("started_at", p.get("wallTime") instanceof Number w ? Math.round(w.doubleValue() * 1000) : 0L);
                    c.put("status", 0);
                    byId.put(id, c);
                    order.add(c);
                }
                case "Network.requestWillBeSentExtraInfo" -> extraReq.computeIfAbsent(id, k -> new LinkedHashMap<>()).putAll(headers(p.get("headers")));
                case "Network.responseReceived" -> {
                    Map<String, Object> c = byId.get(id);
                    if (c == null) continue;
                    setResponse(c, p.get("response") instanceof Map<?, ?> r ? (Map<String, Object>) r : Map.of());
                    if (p.get("type") instanceof String t && !t.isEmpty()) c.put("resource_type", t.toLowerCase());
                }
                case "Network.responseReceivedExtraInfo" -> extraRes.computeIfAbsent(id, k -> new LinkedHashMap<>()).putAll(headers(p.get("headers")));
                case "Network.loadingFinished" -> {
                    Map<String, Object> c = byId.get(id);
                    if (c != null) putDuration(c, p.get("timestamp"));
                }
                case "Network.loadingFailed" -> {
                    Map<String, Object> c = byId.get(id);
                    if (c == null) continue;
                    c.put("failed", true);
                    String err = str(p.get("errorText"));
                    c.put("error_text", !err.isEmpty() ? err : Boolean.TRUE.equals(p.get("canceled")) ? "canceled" : "failed");
                    putDuration(c, p.get("timestamp"));
                }
                default -> { }
            }
        }
        List<Map<String, Object>> out = new ArrayList<>();
        for (Map<String, Object> c : order) {
            String url = str(c.get("url"));
            if (url.startsWith("data:") || url.startsWith("blob:")) continue;
            Object rid = c.get(REQUEST_ID);
            // los headers reales que salieron, con la cookie de sesión
            if (rid != null && extraReq.containsKey(rid)) ((Map<String, Object>) c.get("request_headers")).putAll(extraReq.get(rid));
            if (rid != null && extraRes.containsKey(rid) && c.get("response_headers") instanceof Map<?, ?> rh) {
                ((Map<String, Object>) rh).putAll(extraRes.get(rid));
            }
            c.remove("_ts");
            out.add(c);
        }
        return out;
    }

    /** Si conviene pedir el body: APIs (xhr, fetch) y respuestas con error, de texto. */
    public static boolean wantsBody(Map<String, Object> c) {
        if (Boolean.TRUE.equals(c.get("failed")) || c.get(REQUEST_ID) == null) return false;
        int status = c.get("status") instanceof Number n ? n.intValue() : 0;
        String type = str(c.get("resource_type"));
        if (!type.equals("xhr") && !type.equals("fetch") && status < 400) return false;
        String mime = str(c.get("mime_type")).toLowerCase();
        if (mime.isEmpty()) return true;
        for (String t : new String[]{"json", "text", "xml", "javascript", "html", "x-www-form-urlencoded", "graphql"}) {
            if (mime.contains(t)) return true;
        }
        return false;
    }

    private static void setResponse(Map<String, Object> c, Map<String, Object> r) {
        c.put("status", r.get("status") instanceof Number n ? n.intValue() : 0);
        c.put("status_text", str(r.get("statusText")));
        c.put("mime_type", str(r.get("mimeType")));
        c.put("response_headers", headers(r.get("headers")));
    }

    private static void putDuration(Map<String, Object> c, Object end) {
        if (c.get("_ts") instanceof Number start && end instanceof Number e) {
            c.put("duration_ms", Math.round((e.doubleValue() - start.doubleValue()) * 1000));
        }
    }

    private static Map<String, Object> headers(Object h) {
        Map<String, Object> out = new LinkedHashMap<>();
        if (h instanceof Map<?, ?> m) m.forEach((k, v) -> out.put(String.valueOf(k), v == null ? "" : String.valueOf(v)));
        return out;
    }

    private static String str(Object o) {
        return o == null ? "" : String.valueOf(o);
    }
}
