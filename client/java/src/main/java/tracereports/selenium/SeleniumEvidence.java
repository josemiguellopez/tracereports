package tracereports.selenium;

import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.openqa.selenium.JavascriptExecutor;
import org.openqa.selenium.MutableCapabilities;
import org.openqa.selenium.OutputType;
import org.openqa.selenium.TakesScreenshot;
import org.openqa.selenium.WebDriver;
import org.openqa.selenium.logging.LogEntry;
import tracereports.CdpNetwork;
import tracereports.Dom;
import tracereports.Status;
import tracereports.TraceTest;

/**
 * Evidencia de Selenium WebDriver para TraceReports (necesita selenium-java en tu proyecto): red,
 * consola, captura y DOM.
 *
 * <pre>{@code
 * ChromeOptions options = SeleniumEvidence.options(new ChromeOptions());   // logs de red y consola
 * WebDriver driver = new ChromeDriver(options);
 *
 * @Test void login(TraceTest t) {
 *   SeleniumEvidence.attach(t, driver);   // red y consola del test; si falla, captura y DOM
 *   ...
 *   t.screenshot(SeleniumEvidence.screenshot(driver), "Dashboard visible", Status.PASS);
 * }
 * }</pre>
 *
 * La red (pestaña "Red", Lab) y la consola salen del log de rendimiento de Chrome y Edge (Firefox no
 * lo tiene): crea el driver con {@link #options}. El body de una llamada hecha antes de que la página
 * navegara puede no estar ya disponible; el resto de la llamada sí.
 */
public final class SeleniumEvidence {
    private SeleniumEvidence() {}

    private static final Map<String, Object> LOG_PREFS = Map.of("performance", "ALL", "browser", "ALL");

    /** Activa en las opciones de Chrome o Edge los logs de rendimiento (red) y del navegador (consola). */
    public static <T extends MutableCapabilities> T options(T options) {
        options.setCapability("goog:loggingPrefs", LOG_PREFS);
        if ("MicrosoftEdge".equalsIgnoreCase(String.valueOf(options.getBrowserName()))) {
            options.setCapability("ms:loggingPrefs", LOG_PREFS);
        }
        return options;
    }

    /** Captura de pantalla en PNG, o null si el navegador ya no responde. */
    public static byte[] screenshot(WebDriver driver) {
        try {
            return ((TakesScreenshot) driver).getScreenshotAs(OutputType.BYTES);
        } catch (RuntimeException e) {
            return null;
        }
    }

    /** Snapshot de los elementos de la página (recomendador de locators), o null. */
    public static Object dom(WebDriver driver) {
        try {
            return ((JavascriptExecutor) driver).executeScript(Dom.SCRIPT);
        } catch (RuntimeException e) {
            return null;
        }
    }

    private static List<LogEntry> log(WebDriver driver, String type) {
        try {
            return driver.manage().logs().get(type).getAll();
        } catch (RuntimeException e) {
            return List.of(); // sin el log activado (ver options) u otro navegador
        }
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> cdp(WebDriver driver, String command, Map<String, Object> params) {
        try {
            // ChromeDriver y EdgeDriver (ChromiumDriver#executeCdpCommand), sin depender de ellos
            Method m = driver.getClass().getMethod("executeCdpCommand", String.class, Map.class);
            Object out = m.invoke(driver, command, params);
            return out instanceof Map<?, ?> map ? (Map<String, Object>) map : Map.of();
        } catch (ReflectiveOperationException | RuntimeException e) {
            return Map.of(); // otro navegador, o el recurso ya no existe (la página navegó)
        }
    }

    /**
     * Las llamadas de red desde la última lectura (el log se vacía al leerlo: cada test lee las
     * suyas), con los bodies de las APIs y de las respuestas con error. Vacía si el driver no tiene
     * el log de rendimiento (ver {@link #options}).
     */
    public static List<Map<String, Object>> network(WebDriver driver) {
        List<String> messages = new ArrayList<>();
        for (LogEntry e : log(driver, "performance")) messages.add(e.getMessage());
        List<Map<String, Object>> conns = CdpNetwork.connections(messages);
        for (Map<String, Object> c : conns) {
            Object id = c.get(CdpNetwork.REQUEST_ID);
            if (id != null && Boolean.TRUE.equals(c.get(CdpNetwork.HAS_POST_DATA)) && String.valueOf(c.get("post_data")).isEmpty()) {
                Object post = cdp(driver, "Network.getRequestPostData", Map.of("requestId", id)).get("postData");
                if (post != null) c.put("post_data", String.valueOf(post));
            }
            if (CdpNetwork.wantsBody(c)) {
                Map<String, Object> res = cdp(driver, "Network.getResponseBody", Map.of("requestId", id));
                if (res.get("body") instanceof String body) {
                    String text = Boolean.TRUE.equals(res.get("base64Encoded"))
                            ? new String(Base64.getDecoder().decode(body), StandardCharsets.UTF_8) : body;
                    c.put("response_body", text);
                }
            }
            c.remove(CdpNetwork.REQUEST_ID);
            c.remove(CdpNetwork.HAS_POST_DATA);
        }
        return conns;
    }

    /** Errores y warnings de la consola desde la última lectura (las excepciones sin capturar, "pageerror"). */
    public static List<Map<String, Object>> console(WebDriver driver) {
        List<Map<String, Object>> out = new ArrayList<>();
        for (LogEntry e : log(driver, "browser")) {
            String name = e.getLevel() == null ? "" : e.getLevel().getName();
            String level = name.equals("SEVERE") ? "error" : name.equals("WARNING") ? "warning" : null;
            if (level == null) continue;
            String text = e.getMessage() == null ? "" : e.getMessage();
            if (level.equals("error") && text.contains("Uncaught")) level = "pageerror";
            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("level", level);
            entry.put("text", text);
            entry.put("location", "");
            entry.put("timestamp", e.getTimestamp());
            out.add(entry);
        }
        return out;
    }

    /** Vacía los logs del driver: lo de antes del test (otro test con el mismo driver) no se envía. */
    public static void discardLogs(WebDriver driver) {
        log(driver, "performance");
        log(driver, "browser");
    }

    /**
     * Para el test en curso (llámalo al empezar): descarta los logs anteriores y, antes de cerrar el
     * test (antes de @AfterEach), envía su red y su consola; si falló, también la captura y el DOM.
     */
    public static void attach(TraceTest t, WebDriver driver) {
        discardLogs(driver);
        t.beforeFinish(failed -> {
            if (failed) {
                t.screenshot(screenshot(driver), "Captura al fallar", Status.FAIL);
                t.dom(dom(driver));
            }
            t.network(network(driver));
            t.console(console(driver));
        });
    }
}
