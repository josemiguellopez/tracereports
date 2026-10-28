package tracereports.playwright;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;

import com.microsoft.playwright.Browser;
import com.microsoft.playwright.Locator;
import com.microsoft.playwright.Page;
import com.microsoft.playwright.Playwright;
import java.awt.image.BufferedImage;
import java.io.ByteArrayInputStream;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.List;
import javax.imageio.ImageIO;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.Test;
import tracereports.Mask;

/** Las capturas de Playwright (y los scripts de Selenium) tapan los datos sensibles. */
class MaskTest {
    @Test
    void screenshotPassesTheMaskAndItsColor() {
        List<String> locators = new ArrayList<>();
        Page.ScreenshotOptions[] got = new Page.ScreenshotOptions[1];
        Page page = (Page) Proxy.newProxyInstance(Page.class.getClassLoader(), new Class<?>[] {Page.class}, (p, m, a) -> {
            switch (m.getName()) {
                case "locator":
                    locators.add((String) a[0]);
                    return Proxy.newProxyInstance(Locator.class.getClassLoader(), new Class<?>[] {Locator.class}, (lp, lm, la) -> null);
                case "screenshot":
                    got[0] = (Page.ScreenshotOptions) a[0];
                    return new byte[] {1};
                default:
                    return null;
            }
        });
        assertArrayEquals(new byte[] {1}, PlaywrightEvidence.screenshot(page));
        assertEquals(List.of(Mask.DEFAULT_MASK), locators);
        assertEquals(1, got[0].mask.size());
        assertEquals(Mask.COLOR, got[0].maskColor);
        assertEquals(5000.0, got[0].timeout);
    }

    static final String PAGE = "<html><body style='margin:0;background:#fff'>"
            + "<input type='password' value='SECRETO' style='position:absolute;left:10px;top:10px;width:200px;height:40px'>"
            + "<div data-tracereports-mask style='position:absolute;left:10px;top:70px;width:200px;height:40px;background:#0f0'>12.345.678-9</div>"
            + "<div style='position:absolute;left:300px;top:10px;width:200px;height:40px;background:#0f0'>visible</div>"
            + "</body></html>";

    static final int MAGENTA = 0xFF00FF, GREEN = 0x00FF00;

    static int rgb(byte[] png, int x, int y) throws Exception {
        BufferedImage img = ImageIO.read(new ByteArrayInputStream(png));
        return img.getRGB(x, y) & 0xFFFFFF;
    }

    /**
     * Chromium real: se salta si no está instalado. Los tests no descargan navegadores; en CI los instala
     * {@code ./gradlew installChromium}.
     */
    static void withPage(PageTest body) throws Exception {
        Playwright pw;
        Browser browser;
        try {
            pw = Playwright.create(new Playwright.CreateOptions().setEnv(java.util.Map.of("PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD", "1")));
            browser = pw.chromium().launch();
        } catch (RuntimeException e) {
            Assumptions.abort("chromium no disponible: " + e.getMessage());
            return;
        }
        try (pw) {
            Page page = browser.newPage(new Browser.NewPageOptions().setViewportSize(600, 200));
            page.setContent(PAGE);
            body.run(page);
        }
    }

    interface PageTest {
        void run(Page page) throws Exception;
    }

    @Test
    void realChromiumCoversThePixels() throws Exception {
        withPage(page -> {
            byte[] png = PlaywrightEvidence.screenshot(page);
            assertNotNull(png);
            assertEquals(MAGENTA, rgb(png, 110, 30), "password");
            assertEquals(MAGENTA, rgb(png, 110, 90), "data-tracereports-mask");
            assertEquals(GREEN, rgb(png, 400, 30), "lo demás no se toca");
        });
    }

    /** Los scripts que corren en Selenium, ejecutados en un navegador real: tapan y dejan el DOM igual. */
    @Test
    void seleniumScriptsCoverInARealBrowserAndRestoreTheDom() throws Exception {
        withPage(page -> {
            String before = page.content();
            page.evaluate("([s, a]) => new Function(s).apply(null, a)",
                    List.of(Mask.COVER_JS, List.of(Mask.DEFAULT_MASK, Mask.COLOR, Mask.OVERLAY_ATTR)));
            byte[] png = page.screenshot(new Page.ScreenshotOptions().setCaret(com.microsoft.playwright.options.ScreenshotCaret.INITIAL));
            page.evaluate("([s, a]) => new Function(s).apply(null, a)", List.of(Mask.UNCOVER_JS, List.of(Mask.OVERLAY_ATTR)));
            assertEquals(before, page.content(), "el DOM queda igual");
            assertEquals(MAGENTA, rgb(png, 110, 30));
            assertEquals(MAGENTA, rgb(png, 110, 90));
            assertEquals(GREEN, rgb(png, 400, 30));
        });
    }
}
