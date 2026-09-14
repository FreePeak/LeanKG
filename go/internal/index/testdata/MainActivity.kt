package com.example.tvapp.ui

import android.os.Bundle
import android.view.View
import android.widget.Button
import androidx.appcompat.app.AppCompatActivity
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import androidx.work.CoroutineWorker
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkerParameters
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Inject
import kotlinx.coroutines.launch

class MainActivity : AppCompatActivity() {
    @Inject lateinit var repository: ChannelRepository

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        submitButton.setOnClickListener {
            handleSubmit()
        }
        val button = findViewById<Button>(R.id.submit_button)
        val binding = ActivityMainBinding.inflate(layoutInflater)
        binding.detailButton.setOnClickListener { openDetail() }
        openDetail()
        lifecycleScope.launch {
            val request = OneTimeWorkRequestBuilder<SyncWorker>().build()
            val refresh = PeriodicWorkRequestBuilder<RefreshWorker>(1, java.util.concurrent.TimeUnit.HOURS).build()
        }
    }

    fun openDetail() {
        supportFragmentManager.beginTransaction()
            .replace(R.id.container, DetailFragment())
            .addToBackStack("detail")
            .commit()
    }

    fun openProfile() {
        startActivity(Intent(this, ProfileActivity::class.java))
    }

    fun goToDetail() {
        findNavController().navigate(R.id.action_home_to_detail)
    }

    val title = getString(R.string.app_name)
    val desc = resources.getString(R.string.description)
    val image = R.drawable.ic_launcher
}

class DetailFragment : Fragment()

class ChannelRepository @Inject constructor(
    private val api: ChannelApi,
    private val db: AppDatabase
)

@Module
@InstallIn(SingletonComponent::class)
object AppModule {
    @Provides
    @Singleton
    fun provideDatabase(): AppDatabase = Room.databaseBuilder(
        context, AppDatabase::class.java, "tv.db"
    ).build()
}

class SyncWorker(
    context: Context,
    params: WorkerParameters
) : Worker(context, params)

class RefreshWorker(
    context: Context,
    params: WorkerParameters
) : CoroutineWorker(context, params)
