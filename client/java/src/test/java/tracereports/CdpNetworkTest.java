package tracereports;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/** El log de rendimiento de Chrome (lo que lee SeleniumEvidence) se convierte en las conexiones de la red. */
class CdpNetworkTest {

    private static String ev(String method, String params) {
        String inner = "{\"method\":\"" + method + "\",\"params\":" + params + "}";
        return "{\"message\":" + inner + ",\"webview\":\"x\"}";
    }

    static List<String> checkoutLog() {
        List<String> log = new ArrayList<>();
        log.add(ev("Network.requestWillBeSent", """
            {"requestId":"1","type":"Document","wallTime":1790000000,"timestamp":10,
             "request":{"url":"https://shop.test/cart","method":"GET","headers":{}}}"""));
        log.add(ev("Network.responseReceived", """
            {"requestId":"1","type":"Document","response":{"status":200,"statusText":"OK","mimeType":"text/html","headers":{}}}"""));
        log.add(ev("Network.loadingFinished", "{\"requestId\":\"1\",\"timestamp\":10.2}"));
        log.add(ev("Network.requestWillBeSent", """
            {"requestId":"2","type":"Fetch","wallTime":1790000001,"timestamp":11,
             "request":{"url":"https://shop.test/api/orders","method":"POST","headers":{"Content-Type":"application/json"},
                        "hasPostData":true,"postData":"{\\"items\\":[1]}"}}"""));
        log.add(ev("Network.requestWillBeSentExtraInfo", """
            {"requestId":"2","headers":{"Authorization":"Bearer abc","Cookie":"sid=1"}}"""));
        log.add(ev("Network.responseReceived", """
            {"requestId":"2","type":"Fetch","response":{"status":500,"statusText":"Internal Server Error",
             "mimeType":"application/json","headers":{"content-type":"application/json"}}}"""));
        log.add(ev("Network.loadingFinished", "{\"requestId\":\"2\",\"timestamp\":11.35}"));
        log.add(ev("Network.requestWillBeSent", """
            {"requestId":"3","type":"Document","wallTime":1790000002,"timestamp":12,"request":{"url":"https://shop.test/old","method":"GET","headers":{}}}"""));
        log.add(ev("Network.requestWillBeSent", """
            {"requestId":"3","type":"Document","wallTime":1790000002.1,"timestamp":12.1,
             "request":{"url":"https://shop.test/new","method":"GET","headers":{}},
             "redirectResponse":{"status":302,"statusText":"Found","headers":{"location":"/new"}}}"""));
        log.add(ev("Network.responseReceived", """
            {"requestId":"3","type":"Document","response":{"status":200,"mimeType":"text/html","headers":{}}}"""));
        log.add(ev("Network.requestWillBeSent", """
            {"requestId":"4","type":"XHR","wallTime":1790000003,"timestamp":13,"request":{"url":"https://down.test/api","method":"GET","headers":{}}}"""));
        log.add(ev("Network.loadingFailed", "{\"requestId\":\"4\",\"timestamp\":13.5,\"errorText\":\"net::ERR_CONNECTION_REFUSED\"}"));
        log.add(ev("Network.requestWillBeSent", """
            {"requestId":"5","type":"Image","timestamp":14,"request":{"url":"data:image/png;base64,AA","method":"GET","headers":{}}}"""));
        log.add(ev("Page.frameNavigated", "{\"frame\":{}}"));
        log.add("not json");
        return log;
    }

    @Test
    void requestsRealHeadersRedirectsAndFailures() {
        List<Map<String, Object>> conns = CdpNetwork.connections(checkoutLog());
        List<String> got = new ArrayList<>();
        for (Map<String, Object> c : conns) got.add(c.get("method") + " " + c.get("url") + " " + c.get("status"));
        assertEquals(List.of(
                "GET https://shop.test/cart 200",
                "POST https://shop.test/api/orders 500",
                "GET https://shop.test/old 302",
                "GET https://shop.test/new 200",
                "GET https://down.test/api 0"), got);
        Map<String, Object> order = conns.get(1);
        assertEquals("fetch", order.get("resource_type"));
        assertEquals(350L, order.get("duration_ms"));
        assertEquals(1790000001000L, order.get("started_at"));
        assertEquals("{\"items\":[1]}", order.get("post_data"));
        @SuppressWarnings("unchecked")
        Map<String, Object> headers = (Map<String, Object>) order.get("request_headers");
        assertEquals("sid=1", headers.get("Cookie"));
        assertEquals("Bearer abc", headers.get("Authorization"));
        assertEquals("2", order.get(CdpNetwork.REQUEST_ID));
        assertTrue(CdpNetwork.wantsBody(order));
        assertFalse(CdpNetwork.wantsBody(conns.get(0)), "the document's body is not asked for");
        assertFalse(CdpNetwork.wantsBody(conns.get(2)), "a redirect hop has no body to ask for");
        assertEquals(Boolean.TRUE, conns.get(4).get("failed"));
        assertEquals("net::ERR_CONNECTION_REFUSED", conns.get(4).get("error_text"));
        assertFalse(CdpNetwork.wantsBody(conns.get(4)));
    }
}
