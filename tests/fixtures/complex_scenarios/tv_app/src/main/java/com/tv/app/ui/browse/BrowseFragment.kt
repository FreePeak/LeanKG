package com.tv.app.ui.browse

import android.os.Bundle
import androidx.leanback.app.BrowseSupportFragment
import androidx.leanback.widget.ArrayObjectAdapter
import androidx.leanback.widget.HeaderItem
import androidx.leanback.widget.ListRow
import dagger.hilt.android.AndroidEntryPoint

/**
 * Browse fragment demonstrating Leanback components and Hilt
 * Shows relationship: BrowseFragment uses ChannelRepository
 */
@AndroidEntryPoint
class BrowseFragment : BrowseSupportFragment() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setupUI()
    }

    private fun setupUI() {
        title = "TV Stream"
        headersState = HEADERS_ENABLED
        isHeadersTransitionOnBackEnabled = true
    }
}
