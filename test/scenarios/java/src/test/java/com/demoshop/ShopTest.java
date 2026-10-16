package com.demoshop;

import java.time.Duration;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.openqa.selenium.By;
import org.openqa.selenium.WebDriver;
import org.openqa.selenium.chrome.ChromeDriver;
import org.openqa.selenium.chrome.ChromeOptions;
import org.openqa.selenium.support.ui.ExpectedConditions;
import org.openqa.selenium.support.ui.WebDriverWait;
import tracereports.Status;
import tracereports.TraceTest;
import tracereports.junit.TraceReportsExtension;
import tracereports.selenium.SeleniumEvidence;

/**
 * Escenarios de TraceReports grabados con el cliente de Java (Selenium + JUnit 5), contra la tienda
 * de la demo. {@code go run ./test/scenarios/record} los ejecuta uno a uno.
 */
@ExtendWith(TraceReportsExtension.class)
class ShopTest {
    private static final String SHOP = System.getenv().getOrDefault("SHOP_URL", "http://127.0.0.1:18180");
    private static final String PASSWORD = System.getenv().getOrDefault("SHOP_PASSWORD", "demo");
    private WebDriver driver;
    private WebDriverWait wait;

    @BeforeEach
    void openBrowser(TraceTest t) {
        // los logs de red y consola de Chrome: la pestaña Red, el incidente de backend y el Lab
        ChromeOptions options = SeleniumEvidence.options(new ChromeOptions().addArguments("--headless=new", "--window-size=1280,800"));
        driver = new ChromeDriver(options);
        wait = new WebDriverWait(driver, Duration.ofSeconds(5));
        SeleniumEvidence.attach(t, driver);
    }

    @AfterEach
    void closeBrowser() {
        if (driver != null) driver.quit();
    }

    private void signIn(TraceTest t) {
        t.info("Open the sign-in page");
        driver.get(SHOP + "/login");
        driver.findElement(By.id("password")).sendKeys(PASSWORD);
        driver.findElement(By.id("sign-in")).click();
        wait.until(ExpectedConditions.textToBe(By.tagName("h1"), "Products"));
        t.pass("Signed in: the catalog is visible");
    }

    @Test
    @DisplayName("Signing in shows the catalog")
    void signInShowsCatalog(TraceTest t) {
        signIn(t);
        wait.until(ExpectedConditions.numberOfElementsToBe(By.cssSelector(".product-card"), 3));
        t.screenshot(SeleniumEvidence.screenshot(driver), "Catalog with 3 products", Status.PASS);
    }

    @Test
    @DisplayName("Add to cart with the old selector")
    void addToCartWithOldSelector(TraceTest t) {
        signIn(t);
        driver.get(SHOP + "/product/P-1");
        t.info("Click \"Add to cart\" (#add-to-cart-btn)");
        driver.findElement(By.cssSelector("#add-to-cart-btn")).click();
    }
}
