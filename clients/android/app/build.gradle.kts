import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import org.gradle.api.GradleException
import org.gradle.api.artifacts.component.ComponentIdentifier
import org.gradle.api.artifacts.component.ModuleComponentIdentifier
import org.gradle.api.artifacts.component.ProjectComponentIdentifier
import org.gradle.api.artifacts.result.ResolvedArtifactResult
import org.gradle.jvm.JvmLibrary
import org.gradle.language.base.artifact.SourcesArtifact
import java.io.File
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.MessageDigest
import java.util.zip.ZipFile
import java.util.zip.ZipInputStream

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

val releaseVersionName = System.getenv("RIVUNE_ANDROID_VERSION_NAME")?.also { value ->
    require(Regex("^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$").matches(value)) {
        "RIVUNE_ANDROID_VERSION_NAME must be SemVer without a v prefix"
    }
} ?: "1.12.2"
val releaseVersionCode = System.getenv("RIVUNE_ANDROID_VERSION_CODE")?.let { value ->
    require(value.matches(Regex("[1-9][0-9]*"))) {
        "RIVUNE_ANDROID_VERSION_CODE must be a positive decimal integer"
    }
    requireNotNull(value.toIntOrNull()) {
        "RIVUNE_ANDROID_VERSION_CODE exceeds Android's integer range"
    }
} ?: 1

val localLibmpvAarFile = providers.gradleProperty("RIVUNE_LIBMPV_AAR_PATH")
    .orElse(providers.environmentVariable("RIVUNE_LIBMPV_AAR_PATH"))
    .orNull
    ?.let { suppliedPath ->
        require(suppliedPath.isNotBlank()) {
            "RIVUNE_LIBMPV_AAR_PATH must not be blank"
        }
        file(suppliedPath).canonicalFile.also { aarFile ->
            require(aarFile.isFile && aarFile.canRead()) {
                "RIVUNE_LIBMPV_AAR_PATH must reference a readable AAR file: $aarFile"
            }
            require(aarFile.extension.equals("aar", ignoreCase = true)) {
                "RIVUNE_LIBMPV_AAR_PATH must reference an .aar file: $aarFile"
            }
        }
    }
val runtimeInventoryFile = providers.gradleProperty("RIVUNE_RUNTIME_INVENTORY_PATH")
    .orElse(providers.environmentVariable("RIVUNE_RUNTIME_INVENTORY_PATH"))
    .map { suppliedPath ->
        require(suppliedPath.isNotBlank()) {
            "RIVUNE_RUNTIME_INVENTORY_PATH must not be blank"
        }
        file(suppliedPath)
    }
    .let(layout::file)
    .orElse(layout.buildDirectory.file("reports/release-runtime-inventory.tsv"))
val runtimeComponentsFile = providers.gradleProperty("RIVUNE_RUNTIME_COMPONENTS_PATH")
    .orElse(providers.environmentVariable("RIVUNE_RUNTIME_COMPONENTS_PATH"))
    .map { suppliedPath ->
        require(suppliedPath.isNotBlank()) {
            "RIVUNE_RUNTIME_COMPONENTS_PATH must not be blank"
        }
        file(suppliedPath)
    }
    .let(layout::file)
    .orElse(
        runtimeInventoryFile
            .map { inventoryFile -> inventoryFile.asFile.resolveSibling("release-runtime-components.txt") }
            .let(layout::file),
    )
val runtimeArtifactsDirectory = providers.gradleProperty("RIVUNE_RUNTIME_ARTIFACTS_PATH")
    .orElse(providers.environmentVariable("RIVUNE_RUNTIME_ARTIFACTS_PATH"))
    .map { suppliedPath ->
        require(suppliedPath.isNotBlank()) {
            "RIVUNE_RUNTIME_ARTIFACTS_PATH must not be blank"
        }
        file(suppliedPath)
    }
    .let(layout::dir)
val runtimeSourcesDirectory = providers.gradleProperty("RIVUNE_RUNTIME_SOURCES_PATH")
    .orElse(providers.environmentVariable("RIVUNE_RUNTIME_SOURCES_PATH"))
    .map { suppliedPath ->
        require(suppliedPath.isNotBlank()) {
            "RIVUNE_RUNTIME_SOURCES_PATH must not be blank"
        }
        file(suppliedPath)
    }
    .let(layout::dir)


fun buildConfigString(value: String): String =
    "\"${value.replace("\\", "\\\\").replace("\"", "\\\"")}\""

fun optionalBooleanEnvironment(name: String, default: Boolean): Boolean =
    System.getenv(name)?.let { value ->
        require(value == "true" || value == "false") { "$name must be true or false" }
        value.toBooleanStrict()
    } ?: default

android {
    namespace = "io.rivune.app"
    compileSdk = 37
    buildToolsVersion = "36.0.0"

    defaultConfig {
        applicationId = "io.rivune.app"
        minSdk = 26
        targetSdk = 37
        versionCode = releaseVersionCode
        versionName = releaseVersionName
        buildConfigField("Boolean", "APP_UPDATES_ENABLED", "true")
        buildConfigField(
            "String",
            "UPDATE_MANIFEST_URL",
            buildConfigString("https://github.com/moodiness/rivune/releases/latest/download/rivune-update.json"),
        )
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }


    buildTypes {
        getByName("debug") {
            buildConfigField(
                "Boolean",
                "APP_UPDATES_ENABLED",
                optionalBooleanEnvironment("RIVUNE_ANDROID_UPDATES_ENABLED", false).toString(),
            )
        }
        create("playRelease") {
            initWith(getByName("release"))
            matchingFallbacks += listOf("release")
            buildConfigField("Boolean", "APP_UPDATES_ENABLED", "false")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }



    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
    }
}

kotlin {
    compilerOptions {
        jvmTarget = JvmTarget.JVM_17
    }
}


dependencies {
    implementation(project(":rivune-api"))

    val composeBom = platform("androidx.compose:compose-bom:2026.08.00")
    implementation(composeBom)
    androidTestImplementation(composeBom)

    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.lifecycle:lifecycle-viewmodel-ktx:2.9.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.9.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.9.0")
    implementation("io.coil-kt:coil-compose:2.7.0")
    implementation("io.coil-kt:coil-svg:2.7.0")
    val media3Version = "1.11.0"
    implementation("androidx.media3:media3-exoplayer:$media3Version")
    implementation("androidx.media3:media3-exoplayer-hls:$media3Version")
    implementation("androidx.media3:media3-exoplayer-dash:$media3Version")
    implementation("androidx.media3:media3-ui:$media3Version")
    if (localLibmpvAarFile == null) {
        implementation(libs.libmpv)
    } else {
        implementation(files(localLibmpvAarFile))
    }

    testImplementation("org.jetbrains.kotlin:kotlin-test-junit:2.4.10")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")
    testImplementation("org.json:json:20240303")
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}

val libmpvModuleGroup = "dev.jdtech.mpv"
val libmpvModuleName = "libmpv"
val libmpvModuleVersion = "1.0.0"
val libmpvModuleCoordinate = "$libmpvModuleGroup:$libmpvModuleName:$libmpvModuleVersion"

fun releaseExternalRuntimeArtifacts() = configurations.getByName("releaseRuntimeClasspath").incoming.artifactView {
    componentFilter { componentIdentifier ->
        componentIdentifier !is ProjectComponentIdentifier
    }
}.artifacts.artifacts

fun sha256(file: File): String {
    val digest = MessageDigest.getInstance("SHA-256")
    try {
        file.inputStream().buffered().use { input ->
            val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
            while (true) {
                val bytesRead = input.read(buffer)
                if (bytesRead < 0) break
                digest.update(buffer, 0, bytesRead)
            }
        }
    } catch (error: Exception) {
        throw GradleException("Unable to read runtime artifact: $file", error)
    }
    return digest.digest().joinToString(separator = "") { byte -> "%02x".format(byte) }
}

fun runtimeArtifactContainsClasses(file: File): Boolean = try {
    when (file.extension.lowercase()) {
        "jar" -> ZipFile(file).use { archive ->
            archive.entries().asSequence().any { entry -> !entry.isDirectory && entry.name.endsWith(".class") }
        }
        "aar" -> ZipFile(file).use { archive ->
            val classesJar = archive.getEntry("classes.jar") ?: return@use false
            ZipInputStream(archive.getInputStream(classesJar)).use { classes ->
                generateSequence { classes.nextEntry }
                    .any { entry -> !entry.isDirectory && entry.name.endsWith(".class") }
            }
        }
        else -> false
    }
} catch (error: Exception) {
    throw GradleException("Unable to inspect runtime artifact classes: $file", error)
}

fun ModuleComponentIdentifier.isMavenLibmpv(): Boolean =
    group == libmpvModuleGroup && module == libmpvModuleName && version == libmpvModuleVersion

tasks.register("writeReleaseRuntimeInventory") {
    group = "verification"
    description = "Writes the resolved release runtime artifact inventory."
    outputs.file(runtimeInventoryFile)
    outputs.file(runtimeComponentsFile)
    runtimeArtifactsDirectory.orNull?.let(outputs::dir)
    runtimeSourcesDirectory.orNull?.let(outputs::dir)
    outputs.upToDateWhen { false }

    doLast {
        data class RuntimeArtifactRecord(
            val coordinate: String,
            val group: String,
            val name: String,
            val version: String,
            val filename: String,
            val sha256: String,
            val source: File,
            val module: ModuleComponentIdentifier?,
        )

        val records = mutableMapOf<Pair<String, String>, RuntimeArtifactRecord>()
        val localOverrideComponentIdentifiers = mutableSetOf<ComponentIdentifier>()
        releaseExternalRuntimeArtifacts().forEach { artifact ->
            val artifactFile = artifact.file
            if (!artifactFile.isFile || !artifactFile.canRead()) {
                throw GradleException("Missing or unreadable release runtime artifact: $artifactFile")
            }

            val componentIdentifier = artifact.id.componentIdentifier
            val isLocalOverrideArtifact =
                localLibmpvAarFile != null && artifactFile.canonicalFile == localLibmpvAarFile
            if (isLocalOverrideArtifact) {
                localOverrideComponentIdentifiers += componentIdentifier
            }
            val moduleIdentifier = componentIdentifier as? ModuleComponentIdentifier
            val moduleParts = when {
                componentIdentifier is ProjectComponentIdentifier -> return@forEach
                isLocalOverrideArtifact ->
                    Triple(libmpvModuleGroup, libmpvModuleName, libmpvModuleVersion)
                moduleIdentifier != null ->
                    Triple(moduleIdentifier.group, moduleIdentifier.module, moduleIdentifier.version)
                else -> throw GradleException(
                    "Unsupported external release runtime component type " +
                        "${componentIdentifier.javaClass.name}: $componentIdentifier",
                )
            }
            val (componentGroup, componentName, componentVersion) = moduleParts
            val component = "$componentGroup:$componentName:$componentVersion"
            val pathParts = listOf(componentGroup, componentName, componentVersion, artifactFile.name)
            if (pathParts.any { part ->
                    part.isBlank() || part == "." || part == ".." ||
                        part.any { character ->
                            character == '/' || character == '\\' || character == '\t' ||
                                character == '\r' || character == '\n'
                        }
                }
            ) {
                throw GradleException("Runtime artifact path cannot be represented safely: $component / ${artifactFile.name}")
            }

            val recordKey = component to artifactFile.name
            val record = RuntimeArtifactRecord(
                coordinate = component,
                group = componentGroup,
                name = componentName,
                version = componentVersion,
                filename = artifactFile.name,
                sha256 = sha256(artifactFile),
                source = artifactFile,
                module = moduleIdentifier,
            )
            val priorRecord = records[recordKey]
            if (priorRecord != null && priorRecord.sha256 != record.sha256) {
                throw GradleException(
                    "Conflicting release runtime artifacts for $component and ${artifactFile.name}",
                )
            }
            records[recordKey] = record
        }

        if (localLibmpvAarFile != null) {
            val mavenLibmpvPresent = configurations.getByName("releaseRuntimeClasspath").incoming.resolutionResult.allComponents.any {
                (it.id as? ModuleComponentIdentifier)?.isMavenLibmpv() == true
            }
            if (mavenLibmpvPresent) {
                throw GradleException(
                    "$libmpvModuleCoordinate is still present while RIVUNE_LIBMPV_AAR_PATH is configured",
                )
            }
        }
        if (records.isEmpty()) {
            throw GradleException("Release runtime inventory is empty")
        }

        val components = mutableSetOf<String>()
        configurations.getByName("releaseRuntimeClasspath").incoming.resolutionResult.allComponents.forEach { component ->
            val componentIdentifier = component.id
            when {
                componentIdentifier is ProjectComponentIdentifier -> Unit
                componentIdentifier is ModuleComponentIdentifier -> components +=
                    "${componentIdentifier.group}:${componentIdentifier.module}:${componentIdentifier.version}"
                componentIdentifier in localOverrideComponentIdentifiers -> components += libmpvModuleCoordinate
                else -> throw GradleException(
                    "Unsupported external release runtime component type " +
                        "${componentIdentifier.javaClass.name}: $componentIdentifier",
                )
            }
        }
        if (components.isEmpty()) {
            throw GradleException("Release runtime component inventory is empty")
        }
        val artifactCoordinates = records.values.mapTo(mutableSetOf()) { it.coordinate }
        val missingComponents = artifactCoordinates - components
        if (missingComponents.isNotEmpty()) {
            throw GradleException(
                "Release runtime artifact coordinates are absent from the component inventory: " +
                    missingComponents.sorted().joinToString(),
            )
        }

        val componentsOutputFile = runtimeComponentsFile.get().asFile
        componentsOutputFile.parentFile.mkdirs()
        componentsOutputFile.writeText(components.sorted().joinToString(separator = "\n", postfix = "\n"))

        runtimeArtifactsDirectory.orNull?.asFile?.let { artifactsDirectory ->
            records.values.forEach { record ->
                val destination = artifactsDirectory.resolve(
                    "${record.group}/${record.name}/${record.version}/${record.filename}",
                )
                destination.parentFile.mkdirs()
                if (destination.exists() && (!destination.isFile || sha256(destination) != record.sha256)) {
                    throw GradleException("Runtime artifact copy collision at $destination")
                }
                Files.copy(record.source.toPath(), destination.toPath(), StandardCopyOption.REPLACE_EXISTING)
            }
        }

        runtimeSourcesDirectory.orNull?.asFile?.let { sourcesDirectory ->
            val modules = records.values.mapNotNull { it.module }.toSet()
            val sourceQuery = dependencies.createArtifactResolutionQuery()
                .forComponents(modules)
                .withArtifacts(JvmLibrary::class.java, SourcesArtifact::class.java)
                .execute()
            val sourceComponents = sourceQuery.resolvedComponents.associateBy { it.id }

            modules.forEach { module ->
                val moduleRecords = records.values.filter { it.module == module }
                val sources = sourceComponents[module]
                    ?.getArtifacts(SourcesArtifact::class.java)
                    ?.filterIsInstance<ResolvedArtifactResult>()
                    .orEmpty()
                if (sources.isEmpty() && moduleRecords.any { runtimeArtifactContainsClasses(it.source) }) {
                    throw GradleException("Missing sources artifact for ${module.group}:${module.module}:${module.version}")
                }
                sources.forEach { sourceArtifact ->
                    val sourceFile = sourceArtifact.file
                    if (!sourceFile.isFile || !sourceFile.canRead()) {
                        throw GradleException("Missing or unreadable sources artifact: $sourceFile")
                    }
                    if (!sourceFile.name.endsWith("-sources.jar")) {
                        throw GradleException("Unexpected sources artifact filename: $sourceFile")
                    }
                    val destination = sourcesDirectory.resolve(
                        "${module.group}/${module.module}/${module.version}/${sourceFile.name}",
                    )
                    destination.parentFile.mkdirs()
                    val sourceSha256 = sha256(sourceFile)
                    if (destination.exists() && (!destination.isFile || sha256(destination) != sourceSha256)) {
                        throw GradleException("Sources artifact copy collision at $destination")
                    }
                    Files.copy(sourceFile.toPath(), destination.toPath(), StandardCopyOption.REPLACE_EXISTING)
                }
            }
        }

        val outputFile = runtimeInventoryFile.get().asFile
        outputFile.parentFile.mkdirs()
        outputFile.writeText(
            records.values
                .sortedWith(compareBy({ it.coordinate }, { it.filename }, { it.sha256 }))
                .joinToString(separator = "\n", postfix = "\n") { record ->
                    "${record.coordinate}\t${record.filename}\t${record.sha256}"
                },
        )
        logger.lifecycle("Wrote ${records.size} release runtime artifacts to $outputFile")
    }
}

tasks.register("verifyLocalLibmpvOverride") {
    group = "verification"
    description = "Verifies that the release runtime uses RIVUNE_LIBMPV_AAR_PATH instead of Maven libmpv."

    doLast {
        val expectedAar = localLibmpvAarFile
            ?: throw GradleException("RIVUNE_LIBMPV_AAR_PATH must be configured for this task")
        val artifacts = releaseExternalRuntimeArtifacts()
        val mavenLibmpvPresent = configurations.getByName("releaseRuntimeClasspath").incoming.resolutionResult.allComponents.any {
            (it.id as? ModuleComponentIdentifier)?.isMavenLibmpv() == true
        }
        if (mavenLibmpvPresent) {
            throw GradleException("$libmpvModuleCoordinate is still present in releaseRuntimeClasspath")
        }
        if (artifacts.none { it.file.canonicalFile == expectedAar }) {
            throw GradleException("Local libmpv AAR is absent from releaseRuntimeClasspath: $expectedAar")
        }
        logger.lifecycle("Verified local libmpv override: $expectedAar")
    }
}
