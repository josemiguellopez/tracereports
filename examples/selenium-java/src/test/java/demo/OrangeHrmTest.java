package demo;

import tracereports.TraceTest;
import tracereports.Status;
import tracereports.junit.TraceReportsExtension;
import tracereports.selenium.SeleniumEvidence;
import java.time.Duration;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.junit.jupiter.api.extension.ExtendWith;
import org.openqa.selenium.By;
import org.openqa.selenium.WebDriver;
import org.openqa.selenium.WebElement;
import org.openqa.selenium.chrome.ChromeDriver;
import org.openqa.selenium.chrome.ChromeOptions;
import org.openqa.selenium.support.ui.ExpectedConditions;
import org.openqa.selenium.support.ui.WebDriverWait;

/**
 * Selenium WebDriver + JUnit 5 + TraceReports, contra la demo pública de OrangeHRM.
 *
 * <pre>
 *   ./gradlew test                         # servidor en $TRACEREPORTS_URL o http://localhost:8080
 *   TRACEREPORTS_DEMO_FAIL=1 ./gradlew test     # incluye un fallo controlado (diagnóstico + locators)
 * </pre>
 *
 * La extensión reporta cada test; {@link SeleniumEvidence#attach} adjunta captura y DOM si falla.
 */
@ExtendWith(TraceReportsExtension.class)
class OrangeHrmTest {
    private static final String URL = "https://opensource-demo.orangehrmlive.com/web/index.php/auth/login";
    private WebDriver driver;
    private WebDriverWait wait;

    @BeforeEach
    void abrirNavegador(TraceTest t) {
        ChromeOptions options = new ChromeOptions().addArguments("--window-size=1280,800");
        if (!"0".equals(System.getenv("HEADLESS"))) options.addArguments("--headless=new");
        driver = new ChromeDriver(options); // Selenium Manager trae el driver
        wait = new WebDriverWait(driver, Duration.ofSeconds(20));
        SeleniumEvidence.attach(t, driver);
    }

    @AfterEach
    void cerrarNavegador() {
        if (driver != null) driver.quit();
    }

    private void login(String user, String password) {
        driver.get(URL);
        wait.until(ExpectedConditions.visibilityOfElementLocated(By.name("username"))).sendKeys(user);
        driver.findElement(By.name("password")).sendKeys(password);
        driver.findElement(By.cssSelector("button[type='submit']")).click();
    }

    @Test
    @Tag("smoke")
    @DisplayName("El administrador llega al Dashboard")
    void adminLlegaAlDashboard(TraceTest t) {
        t.info("Abrir el formulario de login");
        login("Admin", "admin123");
        wait.until(ExpectedConditions.textToBe(By.cssSelector(".oxd-topbar-header-breadcrumb h6"), "Dashboard"));
        t.screenshot(SeleniumEvidence.screenshot(driver), "Dashboard visible", Status.PASS);
    }

    @Test
    @Tag("negativo")
    @DisplayName("Una contraseña incorrecta muestra Invalid credentials")
    void claveIncorrecta(TraceTest t) {
        login("Admin", "clave-incorrecta");
        wait.until(ExpectedConditions.textToBe(By.cssSelector(".oxd-alert-content-text"), "Invalid credentials"));
        t.pass("Se muestra la alerta");
    }

    @Test
    @Tag("pim")
    @DisplayName("Buscar un empleado por su Id")
    void buscarEmpleadoPorId(TraceTest t) {
        login("Admin", "admin123");
        wait.until(ExpectedConditions.elementToBeClickable(By.xpath("//span[text()='PIM']"))).click();
        By idCell = By.cssSelector(".oxd-table-body .oxd-table-card .oxd-table-cell:nth-child(2)");
        WebElement first = new WebDriverWait(driver, Duration.ofSeconds(30)).until(ExpectedConditions.visibilityOfElementLocated(idCell));
        String employeeId = first.getText().trim();
        t.info("Id tomado de la primera fila: " + employeeId);
        driver.findElement(By.xpath("//label[text()='Employee Id']/../following-sibling::div//input")).sendKeys(employeeId);
        driver.findElement(By.cssSelector("button[type='submit']")).click();
        wait.ignoring(org.openqa.selenium.StaleElementReferenceException.class)
                .until(d -> d.findElement(idCell).getText().trim().equals(employeeId));
        t.screenshot(SeleniumEvidence.screenshot(driver), "Resultado de buscar el Id " + employeeId, Status.PASS);
    }

    @Test
    @Tag("demo")
    @DisplayName("Fallo controlado: selector que ya no existe")
    @EnabledIfEnvironmentVariable(named = "TRACEREPORTS_DEMO_FAIL", matches = ".+")
    void falloControlado() {
        login("Admin", "admin123");
        new WebDriverWait(driver, Duration.ofSeconds(5)).until(ExpectedConditions.presenceOfElementLocated(By.cssSelector("#boton-que-no-existe")));
    }
}
