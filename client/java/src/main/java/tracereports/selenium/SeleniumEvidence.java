package tracereports.selenium;

import tracereports.TraceTest;
import tracereports.Dom;
import tracereports.Status;
import org.openqa.selenium.JavascriptExecutor;
import org.openqa.selenium.OutputType;
import org.openqa.selenium.TakesScreenshot;
import org.openqa.selenium.WebDriver;

/**
 * Evidencia de Selenium WebDriver para TraceReports (necesita selenium-java en tu proyecto).
 *
 * <pre>{@code
 * @Test void login(TraceTest t) {
 *   SeleniumEvidence.attach(t, driver);   // si el test falla: captura + snapshot del DOM
 *   ...
 *   t.screenshot(SeleniumEvidence.screenshot(driver), "Dashboard visible", Status.PASS);
 * }
 * }</pre>
 *
 * Selenium no expone la red del navegador: para la pestaña "Red" usa Playwright o envía las
 * llamadas con {@link TraceTest#network}.
 */
public final class SeleniumEvidence {
    private SeleniumEvidence() {}

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

    /** Si el test falla, adjunta la captura del momento y el snapshot del DOM (antes de @AfterEach). */
    public static void attach(TraceTest t, WebDriver driver) {
        t.beforeFinish(failed -> {
            if (!failed) return;
            t.screenshot(screenshot(driver), "Captura al fallar", Status.FAIL);
            t.dom(dom(driver));
        });
    }
}
