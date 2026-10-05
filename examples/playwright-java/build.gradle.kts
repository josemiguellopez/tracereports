plugins { java }

repositories { mavenCentral() }

java { toolchain { languageVersion.set(JavaLanguageVersion.of(17)) } }

dependencies {
    testImplementation("tracereports:tracereports-java:0.1.0")
    testImplementation("com.microsoft.playwright:playwright:1.63.0")
    testImplementation(platform("org.junit:junit-bom:6.1.3"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.withType<JavaCompile> { options.encoding = "UTF-8" }

tasks.test {
    useJUnitPlatform()
    testLogging { events("passed", "failed", "skipped"); showStandardStreams = true }
    // se usa el Chrome instalado (BROWSER_CHANNEL=chrome): no hace falta descargar navegadores
    environment("PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD", System.getenv("PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD") ?: "1")
    if (System.getenv("TRACEREPORTS_RUN_NAME") == null) environment("TRACEREPORTS_RUN_NAME", "OrangeHRM - Playwright Java")
}
