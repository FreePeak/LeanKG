plugins {
    id("com.android.application")
    kotlin("android")
}

android {
    namespace = "com.example.tvapp"
    compileSdk = 34
}

dependencies {
    implementation(project(":core:network"))
    api(project(":feature:player"))
    implementation(libs.androidx.room.runtime)
    implementation(libs.kotlinx.coroutines.android)
    implementation("com.squareup.retrofit2:retrofit:2.9.0")
    testImplementation("junit:junit:4.13.2")
}
