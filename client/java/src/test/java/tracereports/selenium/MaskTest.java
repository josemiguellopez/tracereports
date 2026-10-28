package tracereports.selenium;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;

import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.openqa.selenium.JavascriptExecutor;
import org.openqa.selenium.TakesScreenshot;
import org.openqa.selenium.WebDriver;
import org.openqa.selenium.WebDriverException;
import tracereports.Mask;

/** SeleniumEvidence.screenshot tapa, captura y destapa, en ese orden y aunque la captura falle. */
class MaskTest {
    /** Un WebDriver que registra las llamadas: tapar → capturar → destapar. */
    static WebDriver driver(List<String> calls, RuntimeException shotError, RuntimeException coverError) {
        InvocationHandler h = (p, m, a) -> {
            switch (m.getName()) {
                case "executeScript": {
                    String script = (String) a[0];
                    boolean cover = script.equals(Mask.COVER_JS);
                    calls.add(cover ? "cover" : "uncover");
                    Object[] args = (Object[]) a[1];
                    assertEquals(cover ? List.of(Mask.DEFAULT_MASK, Mask.COLOR, Mask.OVERLAY_ATTR) : List.of(Mask.OVERLAY_ATTR), List.of(args));
                    if (cover && coverError != null) throw coverError;
                    return 1L;
                }
                case "getScreenshotAs":
                    calls.add("shot");
                    if (shotError != null) throw shotError;
                    return new byte[] {7};
                default:
                    return null;
            }
        };
        return (WebDriver) Proxy.newProxyInstance(MaskTest.class.getClassLoader(),
                new Class<?>[] {WebDriver.class, JavascriptExecutor.class, TakesScreenshot.class}, h);
    }

    @Test
    void coversThenCapturesThenUncovers() {
        List<String> calls = new ArrayList<>();
        assertArrayEquals(new byte[] {7}, SeleniumEvidence.screenshot(driver(calls, null, null)));
        assertEquals(List.of("cover", "shot", "uncover"), calls);
    }

    @Test
    void uncoversEvenIfTheScreenshotFails() {
        List<String> calls = new ArrayList<>();
        assertNull(SeleniumEvidence.screenshot(driver(calls, new WebDriverException("browser gone"), null)));
        assertEquals(List.of("cover", "shot", "uncover"), calls);
    }

    @Test
    void noScreenshotWhenItCannotCover() {
        List<String> calls = new ArrayList<>();
        assertNull(SeleniumEvidence.screenshot(driver(calls, null, new WebDriverException("invalid selector"))));
        assertFalse(calls.contains("shot"), "sin máscara no hay captura");
    }
}
