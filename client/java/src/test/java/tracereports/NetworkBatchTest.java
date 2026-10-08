package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

/** Los lotes de red entran en el límite del servidor (48 MiB por request), en orden y sin duplicados. */
class NetworkBatchTest {
    static final int SERVER_LIMIT = 48 << 20; // maxNetworkBody del servidor
    static final Pattern URL = Pattern.compile("\"url\":\"(https://app/[^\"]*)\"");

    @BeforeAll
    static void fast() {
        Sender.maxBackoffMillis = 100;
        Sender.circuitMillis = 300;
    }

    record Sent(List<Integer> sizes, List<String> urls, List<String> bodies) {}

    static Sent send(List<Map<String, Object>> conns) throws Exception {
        try (FakeServer srv = new FakeServer()) {
            TraceReports cr = new TraceReports(srv.url(), null);
            cr.startRun(new TraceReports.RunInfo("Suite"));
            TraceTest t = cr.startTest(new TraceReports.TestInfo("Red"));
            t.network(conns);
            t.finish(Status.PASS);
            cr.finishRun();
            assertEquals(0, cr.delivery().problems());
            List<Integer> sizes = new ArrayList<>();
            List<String> urls = new ArrayList<>();
            List<String> bodies = new ArrayList<>();
            for (FakeServer.Req r : srv.at("/api/v1/tests/11/network")) {
                sizes.add(r.body().length);
                String text = r.text();
                bodies.add(text);
                Matcher m = URL.matcher(text);
                while (m.find()) urls.add(m.group(1));
            }
            return new Sent(sizes, urls, bodies);
        }
    }

    static Map<String, Object> conn(String url, int status, String body) {
        Map<String, Object> c = new LinkedHashMap<>();
        c.put("method", "GET");
        c.put("url", url);
        c.put("status", status);
        c.put("response_body", body);
        return c;
    }

    @Test
    void defaultBatchesFitTheServerLimit() throws Exception {
        // 200 conexiones con bodies de 256 KB: ~52 MB en un solo lote antes del arreglo
        List<Map<String, Object>> conns = new ArrayList<>();
        List<String> want = new ArrayList<>();
        for (int i = 0; i < 200; i++) {
            conns.add(conn("https://app/api/" + i, 200, "x".repeat(256 << 10)));
            want.add("https://app/api/" + i);
        }
        Sent s = send(conns);
        assertTrue(s.sizes().stream().allMatch(n -> n <= SERVER_LIMIT), s.sizes().toString());
        assertEquals(want, s.urls(), "all, once, in order");
    }

    @Test
    void sizeIsMeasuredOnTheRealJsonWithUnicodeAndEscapes() throws Exception {
        String body = "\u0001\"\\\n😀ñ".repeat(37000); // < 256 K unidades: no se recorta
        List<Map<String, Object>> conns = new ArrayList<>();
        List<String> want = new ArrayList<>();
        for (int i = 0; i < 40; i++) {
            Map<String, Object> c = conn("https://app/api/" + i, 500, body);
            c.put("post_data", "é".repeat(30000));
            c.put("request_headers", Map.of("x-trace", "ü".repeat(4000)));
            conns.add(c);
            want.add("https://app/api/" + i);
        }
        Sent s = send(conns);
        assertTrue(s.sizes().size() > 1 && s.sizes().stream().allMatch(n -> n <= SERVER_LIMIT), s.sizes().toString());
        assertEquals(want, s.urls());
        String encoded = Json.write(body);
        assertTrue(s.bodies().stream().allMatch(b -> b.contains(encoded)), "values are preserved by the split");
    }

    @Test
    void aConnectionTooBigAloneIsSentWithoutBodies() throws Exception {
        Map<String, Object> huge = conn("https://app/upload", 413, "");
        huge.put("post_data", "p".repeat(60 << 20));
        huge.put("request_headers", Map.of("big", "h".repeat(1 << 20)));
        Sent s = send(List.of(conn("https://app/ok", 200, "ok"), huge, conn("https://app/ok2", 200, "ok")));
        assertTrue(s.sizes().stream().allMatch(n -> n <= SERVER_LIMIT), s.sizes().toString());
        assertEquals(List.of("https://app/ok", "https://app/upload", "https://app/ok2"), s.urls());
        String all = String.join("", s.bodies());
        assertTrue(all.contains("\"url\":\"https://app/upload\"") && all.contains("\"body_truncated\":true") && all.contains("\"status\":413"), "the connection itself is kept");
    }
}
