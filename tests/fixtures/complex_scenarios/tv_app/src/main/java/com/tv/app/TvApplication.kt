package com.tv.app

import android.app.Application
import androidx.work.Configuration
import androidx.work.WorkManager
import dagger.hilt.android.HiltAndroidApp

/**
 * TV Application class with Hilt setup and WorkManager configuration
 * Demonstrates: @HiltAndroidApp, Application lifecycle, WorkManager init
 */
@HiltAndroidApp
class TvApplication : Application(), Configuration.Provider {

    override fun onCreate() {
        super.onCreate()
        initializeWorkManager()
    }

    private fun initializeWorkManager() {
        WorkManager.initialize(
            this,
            workManagerConfiguration
        )
    }

    override val workManagerConfiguration: Configuration
        get() = Configuration.Builder()
            .setMinimumLoggingLevel(android.util.Log.INFO)
            .build()
}
