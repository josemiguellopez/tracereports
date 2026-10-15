plugins { java }

repositories { mavenCentral() }

java { toolchain { languageVersion.set(JavaLanguageVersion.of(17)) } }

dependencies {
    testImplementation("tracereports:tracereports-java:0.2.0")
    testImplementation("org.seleniumhq.selenium:selenium-java:4.51.0")
    testImplementation(platform("org.junit:junit-bom:6.1.3"))
    testImplementation("org.junit.jupiter:junit-jupiter")
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

tasks.withType<JavaCompile> { options.encoding = "UTF-8" }

tasks.test {
    useJUnitPlatform()
    // los escenarios que fallan a propósito no deben cortar la grabación
    ignoreFailures = true
    testLogging { events("passed", "failed", "skipped") }
    outputs.upToDateWhen { false }
}
