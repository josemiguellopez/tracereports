plugins { java }

repositories { mavenCentral() }

java { toolchain { languageVersion.set(JavaLanguageVersion.of(17)) } }

dependencies {
    testImplementation("tracereports:tracereports-java:0.1.0")
    testImplementation("org.seleniumhq.selenium:selenium-java:4.50.0")
    testImplementation(platform("org.junit:junit-bom:6.1.3"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.withType<JavaCompile> { options.encoding = "UTF-8" }

tasks.test {
    useJUnitPlatform()
    testLogging { events("passed", "failed", "skipped"); showStandardStreams = true }
    // TraceReports lee TRACEREPORTS_URL / TRACEREPORTS_TOKEN del entorno; HEADLESS=0 muestra el navegador
    if (System.getenv("TRACEREPORTS_RUN_NAME") == null) environment("TRACEREPORTS_RUN_NAME", "OrangeHRM - Selenium Java")
}
