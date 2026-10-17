plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// Version name/code come from the git tag in CI (e.g. v1.2.3 -> 1.2.3 / 10203).
val versionTag = (System.getenv("HD_VERSION") ?: "0.1.0").removePrefix("v")
val versionParts = versionTag.split(".", "-").mapNotNull { it.toIntOrNull() } + listOf(0, 0, 0)

android {
    namespace = "io.github.erikedits.homelydrive"
    compileSdk = 35

    defaultConfig {
        applicationId = "io.github.erikedits.homelydrive"
        minSdk = 26
        targetSdk = 35
        versionCode = maxOf(1, versionParts[0] * 10000 + versionParts[1] * 100 + versionParts[2])
        versionName = versionTag
    }

    signingConfigs {
        create("release") {
            val ks = System.getenv("HD_KEYSTORE")
            if (ks != null && file(ks).exists()) {
                storeFile = file(ks)
                storePassword = System.getenv("HD_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("HD_KEY_ALIAS")
                keyPassword = System.getenv("HD_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            val rel = signingConfigs.getByName("release")
            signingConfig = if (rel.storeFile != null) rel else signingConfigs.getByName("debug")
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
        buildConfig = true
    }
    lint {
        // all findings in the build log (CI)
        textReport = true
        textOutput = file("stdout")
    }
    dependenciesInfo {
        // F-Droid: no Google signing blob in the APK.
        includeInApk = false
        includeInBundle = false
    }
}
