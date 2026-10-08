// Cliente Java de TraceReports: núcleo sin dependencias (java.net.http), extensión de JUnit 5 y
// helpers opcionales para Selenium y Playwright (compileOnly: se usan los de tu proyecto).
plugins {
    `java-library`
    `maven-publish`
}

group = "tracereports"
version = "0.1.0"

java {
    toolchain { languageVersion.set(JavaLanguageVersion.of(17)) }
    withSourcesJar()
}

repositories { mavenCentral() }

dependencies {
    compileOnly("org.junit.jupiter:junit-jupiter-api:6.1.3")
    compileOnly("org.seleniumhq.selenium:selenium-api:4.50.0")
    compileOnly("com.microsoft.playwright:playwright:1.63.0")

    testImplementation(platform("org.junit:junit-bom:6.1.3"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testImplementation("org.junit.jupiter:junit-jupiter-params")
    testImplementation("org.junit.platform:junit-platform-testkit")
    // la captura de Playwright se prueba con un Response simulado (sin navegador)
    testImplementation("com.microsoft.playwright:playwright:1.63.0")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.withType<JavaCompile> { options.encoding = "UTF-8" }

tasks.test {
    useJUnitPlatform()
    // los tests de la extensión corren sus propios tests de ejemplo: no deben reportar a un servidor real
    environment("TRACEREPORTS_DISABLED", "")
    // los tests corren dentro del repositorio: que el cliente no lea el .env real del proyecto
    environment("TRACEREPORTS_ENV_FILE", "off")
    // la clase de ejemplo (falla a propósito) solo la corre EngineTestKit dentro de TraceReportsExtensionTest
    exclude("**/TraceReportsExtensionTest\$Ejemplo.class")
}

// ./gradlew publishToMavenLocal -> tracereports:tracereports-java:0.1.0 en ~/.m2 (para Maven)
publishing {
    publications { create<MavenPublication>("maven") { from(components["java"]) } }
}
