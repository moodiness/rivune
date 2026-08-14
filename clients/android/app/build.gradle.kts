plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

val releaseVersionName = System.getenv("RIVUNE_ANDROID_VERSION_NAME")?.also { value ->
    require(Regex("^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$").matches(value)) {
        "RIVUNE_ANDROID_VERSION_NAME must be SemVer without a v prefix"
    }
} ?: "0.1.0"
val releaseVersionCode = System.getenv("RIVUNE_ANDROID_VERSION_CODE")?.let { value ->
    require(value.matches(Regex("[1-9][0-9]*"))) {
        "RIVUNE_ANDROID_VERSION_CODE must be a positive decimal integer"
    }
    requireNotNull(value.toIntOrNull()) {
        "RIVUNE_ANDROID_VERSION_CODE exceeds Android's integer range"
    }
} ?: 1

val signingEnvironment = mapOf(
    "storeFile" to System.getenv("RIVUNE_ANDROID_KEYSTORE_PATH"),
    "storePassword" to System.getenv("RIVUNE_ANDROID_KEYSTORE_PASSWORD"),
    "keyAlias" to System.getenv("RIVUNE_ANDROID_KEY_ALIAS"),
    "keyPassword" to System.getenv("RIVUNE_ANDROID_KEY_PASSWORD"),
)
val configuredSigningValues = signingEnvironment.values.count { !it.isNullOrBlank() }
require(configuredSigningValues == 0 || configuredSigningValues == signingEnvironment.size) {
    "Android release signing requires all RIVUNE_ANDROID_KEYSTORE_PATH, " +
        "RIVUNE_ANDROID_KEYSTORE_PASSWORD, RIVUNE_ANDROID_KEY_ALIAS, and RIVUNE_ANDROID_KEY_PASSWORD variables"
}
val releaseSigningEnabled = configuredSigningValues == signingEnvironment.size

fun buildConfigString(value: String): String =
    "\"${value.replace("\\", "\\\\").replace("\"", "\\\"")}\""

fun optionalBooleanEnvironment(name: String, default: Boolean): Boolean =
    System.getenv(name)?.let { value ->
        require(value == "true" || value == "false") { "$name must be true or false" }
        value.toBooleanStrict()
    } ?: default

android {
    namespace = "io.rivune.app"
    compileSdk = 36
    buildToolsVersion = "36.0.0"

    defaultConfig {
        applicationId = "io.rivune.app"
        minSdk = 26
        targetSdk = 36
        versionCode = releaseVersionCode
        versionName = releaseVersionName
        buildConfigField("Boolean", "APP_UPDATES_ENABLED", "true")
        buildConfigField(
            "String",
            "UPDATE_MANIFEST_URL",
            buildConfigString("https://github.com/moodiness/rivune/releases/latest/download/rivune-android-update.json"),
        )
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    signingConfigs {
        if (releaseSigningEnabled) {
            create("release") {
                storeFile = file(requireNotNull(signingEnvironment["storeFile"]))
                storePassword = signingEnvironment["storePassword"]
                keyAlias = signingEnvironment["keyAlias"]
                keyPassword = signingEnvironment["keyPassword"]
            }
        }
    }

    buildTypes {
        getByName("debug") {
            buildConfigField(
                "Boolean",
                "APP_UPDATES_ENABLED",
                optionalBooleanEnvironment("RIVUNE_ANDROID_UPDATES_ENABLED", false).toString(),
            )
            System.getenv("RIVUNE_ANDROID_UPDATE_MANIFEST_URL")?.let { manifestUrl ->
                require(manifestUrl.startsWith("https://")) {
                    "RIVUNE_ANDROID_UPDATE_MANIFEST_URL must use HTTPS"
                }
                buildConfigField("String", "UPDATE_MANIFEST_URL", buildConfigString(manifestUrl))
            }
        }
        getByName("release") {
            if (releaseSigningEnabled) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
    }
}


dependencies {
    implementation(project(":rivune-api"))

    val composeBom = platform("androidx.compose:compose-bom:2025.05.01")
    implementation(composeBom)
    androidTestImplementation(composeBom)

    implementation("androidx.activity:activity-compose:1.10.1")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.lifecycle:lifecycle-viewmodel-ktx:2.9.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.10.2")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.9.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.9.0")
    implementation("io.coil-kt:coil-compose:2.7.0")
    implementation("io.coil-kt:coil-svg:2.7.0")
    val media3Version = "1.11.0"
    implementation("androidx.media3:media3-exoplayer:$media3Version")
    implementation("androidx.media3:media3-exoplayer-hls:$media3Version")
    implementation("androidx.media3:media3-exoplayer-dash:$media3Version")
    implementation("androidx.media3:media3-ui:$media3Version")

    testImplementation(kotlin("test"))
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.10.2")
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
