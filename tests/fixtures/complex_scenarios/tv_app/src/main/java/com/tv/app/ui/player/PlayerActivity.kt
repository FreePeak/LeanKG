package com.tv.app.ui.player

import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity
import dagger.hilt.android.AndroidEntryPoint

/**
 * Player activity demonstrating Media3 integration
 * Shows relationship: PlayerActivity uses PlaybackService
 */
@AndroidEntryPoint
class PlayerActivity : AppCompatActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Initialize player
    }
}
