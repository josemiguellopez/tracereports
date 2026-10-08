package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

/** body_truncated y el tamaño original (bytes UTF-8, como el servidor) se conservan al enviar. */
class NetworkBodyTest {
    @BeforeAll
    static void fast() {
        Sender.maxBackoffMillis = 100;
        Sender.circuitMillis = 300;
    }

    static String send(Map<String, Object> conn) throws Exception {
        try (FakeServer srv = new FakeServer()) {
            TraceReports cr = new TraceReports(srv.url(), null);
            cr.startRun(new TraceReports.RunInfo("s"));
            TraceTest t = cr.startTest(new TraceReports.TestInfo("t"));
            t.network(List.of(conn));
            t.finish(Status.PASS);
            cr.finishRun();
            return srv.at("/api/v1/tests/11/network").get(0).text();
        }
    }

    static Map<String, Object> conn(String body) {
        Map<String, Object> c = new LinkedHashMap<>();
        c.put("method", "GET");
        c.put("url", "https://app/api");
        c.put("status", 200);
        c.put("response_body", body);
        return c;
    }

    static long field(String json, String name) {
        Matcher m = Pattern.compile("\"" + name + "\":([0-9]+)").matcher(json);
        assertTrue(m.find(), json.length() > 300 ? json.substring(0, 300) : json);
        return Long.parseLong(m.group(1));
    }

    @Test
    void aBodyAlreadyCutByTheCaptureStaysMarked() throws Exception {
        String body = "x".repeat(256 * 1024);
        Map<String, Object> c = conn(body);
        c.put("body_size", body.length() + 1000);
        c.put("body_truncated", true);
        String sent = send(c);
        assertTrue(sent.contains("\"body_truncated\":true"));
        assertEquals(body.length() + 1000, field(sent, "body_size"));
    }

    @Test
    void anExplicitFlagIsKeptEvenIfTheBodyFits() throws Exception {
        Map<String, Object> c = conn("{\"a\":");
        c.put("body_truncated", true);
        assertTrue(send(c).contains("\"body_truncated\":true"));
    }

    @Test
    void aWholeBodyThatFitsIsNotMarkedAndItsSizeIsInBytes() throws Exception {
        String body = "ñ😀".repeat(1000);
        String sent = send(conn(body));
        assertTrue(!sent.contains("\"body_truncated\":true"), sent.substring(0, 200));
        assertEquals(body.getBytes(StandardCharsets.UTF_8).length, field(sent, "body_size"));
    }

    @Test
    void theSdkCutIsMarkedWithTheOriginalBytes() throws Exception {
        String body = "😀".repeat(200_000);
        String sent = send(conn(body));
        assertTrue(sent.contains("\"body_truncated\":true"));
        assertEquals(body.getBytes(StandardCharsets.UTF_8).length, field(sent, "body_size"));
    }
}
