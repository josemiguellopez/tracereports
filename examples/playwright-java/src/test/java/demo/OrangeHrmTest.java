package demo;

import static com.microsoft.playwright.assertions.PlaywrightAssertions.assertThat;

import tracereports.TraceTest;
import tracereports.Status;
import tracereports.junit.TraceReportsExtension;
import tracereports.playwright.PlaywrightEvidence;
import com.microsoft.playwright.Browser;
import com.microsoft.playwright.BrowserContext;
import com.microsoft.playwright.BrowserType;
import com.microsoft.playwright.Locator;
import com.microsoft.playwright.Page;
import com.microsoft.playwright.Playwright;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.junit.jupiter.api.extension.ExtendWith;

/**
 * Playwright para Java + JUnit 5 + TraceReports, contra la demo pública de OrangeHRM.
 *
 * <pre>
 *   ./gradlew test                         # servidor en $TRACEREPORTS_URL o http://localhost:8080
 *   TRACEREPORTS_DEMO_FAIL=1 ./gradlew test     # incluye un fallo controlado (diagnóstico + locators)
 * </pre>
 *
 * {@link PlaywrightEvidence#attach} envía la red de cada test y, si falla, la captura y el DOM.
 */
@ExtendWith(TraceReportsExtension.class)
class OrangeHrmTest {
    private static final String URL = "https://opensource-demo.orangehrmlive.com/web/index.php/auth/login";
    private static Playwright playwright;
    private static Browser browser;
    private BrowserContext context;
    private Page page;

    @BeforeAll
    static void abrirNavegador() {
        playwright = Playwright.create();
        String channel = System.getenv().getOrDefault("BROWSER_CHANNEL", "chrome"); // Chrome instalado
        browser = playwright.chromium().launch(new BrowserType.LaunchOptions()
                .setChannel(channel.isEmpty() ? null : channel)
                .setHeadless(!"0".equals(System.getenv("HEADLESS"))));
    }

    @AfterAll
    static void cerrarNavegador() {
        if (playwright != null) playwright.close();
    }

    @BeforeEach
    void nuevaPagina(TraceTest t) {
        context = browser.newContext();
        page = context.newPage();
        PlaywrightEvidence.attach(t, page); // antes de navegar: la captura de red no es retroactiva
    }

    @AfterEach
    void cerrarPagina() {
        if (context != null) context.close();
    }

    private void login(String user, String password) {
        page.navigate(URL);
        page.fill("input[name='username']", user);
        page.fill("input[name='password']", password);
        page.click("button[type='submit']");
    }

    @Test
    @Tag("smoke")
    @DisplayName("El administrador llega al Dashboard")
    void adminLlegaAlDashboard(TraceTest t) {
        t.info("Abrir el formulario de login");
        login("Admin", "admin123");
        assertThat(page.locator(".oxd-topbar-header-breadcrumb h6")).hasText("Dashboard");
        t.screenshot(page.screenshot(), "Dashboard visible", Status.PASS);
    }

    @Test
    @Tag("negativo")
    @DisplayName("Una contraseña incorrecta muestra Invalid credentials")
    void claveIncorrecta(TraceTest t) {
        login("Admin", "clave-incorrecta");
        assertThat(page.locator(".oxd-alert-content-text")).hasText("Invalid credentials");
        t.pass("Se muestra la alerta");
    }

    @Test
    @Tag("pim")
    @DisplayName("Buscar un empleado por su Id")
    void buscarEmpleadoPorId(TraceTest t) {
        login("Admin", "admin123");
        page.click("//span[text()='PIM']");
        Locator first = page.locator(".oxd-table-body .oxd-table-card").first();
        assertThat(first).isVisible(new com.microsoft.playwright.assertions.LocatorAssertions.IsVisibleOptions().setTimeout(30_000));
        String employeeId = first.locator(".oxd-table-cell").nth(1).innerText().trim();
        t.info("Id tomado de la primera fila: " + employeeId);
        page.fill("//label[text()='Employee Id']/../following-sibling::div//input", employeeId);
        page.click("button[type='submit']");
        assertThat(page.locator(".oxd-table-body .oxd-table-card").first().locator(".oxd-table-cell").nth(1)).hasText(employeeId);
        t.screenshot(page.screenshot(), "Resultado de buscar el Id " + employeeId, Status.PASS);
    }

    @Test
    @Tag("demo")
    @DisplayName("Fallo controlado: selector que ya no existe")
    @EnabledIfEnvironmentVariable(named = "TRACEREPORTS_DEMO_FAIL", matches = ".+")
    void falloControlado() {
        login("Admin", "admin123");
        page.click("#boton-que-no-existe", new Page.ClickOptions().setTimeout(5_000));
    }
}
