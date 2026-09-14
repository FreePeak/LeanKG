package com.example.tvapp.tv

import android.os.Bundle
import android.view.View
import androidx.leanback.app.BrowseSupportFragment
import androidx.leanback.widget.ArrayObjectAdapter

class MainFragment : BrowseSupportFragment() {
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        setOnItemViewClickedListener { _, item, _, _ ->
            startActivity(Intent(activity, DetailsActivity::class.java))
            showDetails(DetailsFragment())
        }
    }
}
