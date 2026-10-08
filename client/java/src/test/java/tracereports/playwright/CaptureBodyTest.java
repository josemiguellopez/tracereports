package tracereports.playwright;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.microsoft.playwright.Response;
import java.lang.reflect.Proxy;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

/** La captura de Playwright marca su propio recorte y guarda el tamaño original en bytes UTF-8. */
class CaptureBodyTest {
    static Map<String, Object> capture(String body) {
        Response res = (Response) Proxy.newProxyInstance(Response.class.getClassLoader(), new Class<?>[] {Response.class},
                (p, m, a) -> m.getName().equals("text") ? body : null);
        Map<String, Object> c = new HashMap<>();
        PlaywrightEvidence.NetworkCapture.readBody(res, c);
        return c;
    }

    @Test
    void aBigBodyIsCutAndMarked() {
        String body = "ñ".repeat(150_000) + "x".repeat(150_000);
        Map<String, Object> c = capture(body);
        assertEquals(Boolean.TRUE, c.get("body_truncated"));
        assertEquals((long) body.getBytes(StandardCharsets.UTF_8).length, ((Number) c.get("body_size")).longValue());
        assertEquals(256 * 1024, ((String) c.get("response_body")).length());
    }

    @Test
    void aSmallBodyIsWhole() {
        String body = "{\"ok\":\"ñ\"}";
        Map<String, Object> c = capture(body);
        assertFalse(Boolean.TRUE.equals(c.get("body_truncated")));
        assertEquals(body, c.get("response_body"));
        assertEquals((long) body.getBytes(StandardCharsets.UTF_8).length, ((Number) c.get("body_size")).longValue());
    }

    @Test
    void theCutNeverSplitsAnEmoji() {
        String body = "a" + "😀".repeat(200_000);
        String cut = (String) capture(body).get("response_body");
        assertTrue(!Character.isHighSurrogate(cut.charAt(cut.length() - 1)));
    }
}
