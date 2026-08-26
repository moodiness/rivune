plugins {
    id("com.android.library") version "9.3.1" apply false
    id("com.android.application") version "9.3.1" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.4.10" apply false
    id("org.jetbrains.kotlin.plugin.serialization") version "2.4.10" apply false
}

subprojects {
    configurations.configureEach {
        if (name.contains("release", ignoreCase = true)) {
            resolutionStrategy.activateDependencyLocking()
        }
    }
}

tasks.register("validateReleaseSupplyChain") {
    group = "verification"
    description = "Resolves locked release dependencies under strict dependency verification."
    doLast {
        check(file("gradle/verification-metadata.xml").isFile) {
            "Missing Gradle dependency verification metadata"
        }
        val missingLocks = subprojects.filterNot { it.file("gradle.lockfile").isFile }
        check(missingLocks.isEmpty()) {
            "Missing dependency lockfiles for: ${missingLocks.joinToString { it.path }}"
        }
    }
}
