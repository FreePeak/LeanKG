package com.fixture.test

import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton
import retrofit2.Retrofit
import retrofit2.converter.gson.GsonConverterFactory

/**
 * Hilt DI fixtures demonstrating:
 * - @Module with @InstallIn(SingletonComponent)
 * - @Provides methods
 * - @Singleton scope
 * - Interface binding
 * - Third-party library integration (Retrofit)
 */

interface PlaylistRepository {
    suspend fun fetchPlaylist(url: String): List<ChannelEntity>
    suspend fun refreshData(): Boolean
}

class PlaylistRepositoryImpl(
    private val api: PlaylistApi,
    private val channelDao: ChannelDao
) : PlaylistRepository {
    override suspend fun fetchPlaylist(url: String): List<ChannelEntity> {
        // Implementation
        return emptyList()
    }

    override suspend fun refreshData(): Boolean {
        // Implementation
        return true
    }
}

interface PlaylistApi {
    suspend fun getChannels(): List<ChannelDto>
}

data class ChannelDto(
    val id: String,
    val name: String,
    val url: String
)

@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    @Provides
    @Singleton
    fun provideRetrofit(): Retrofit {
        return Retrofit.Builder()
            .baseUrl("https://api.example.com/")
            .addConverterFactory(GsonConverterFactory.create())
            .build()
    }

    @Provides
    @Singleton
    fun providePlaylistApi(retrofit: Retrofit): PlaylistApi {
        return retrofit.create(PlaylistApi::class.java)
    }

    @Provides
    @Singleton
    fun providePlaylistRepository(
        api: PlaylistApi,
        channelDao: ChannelDao
    ): PlaylistRepository {
        return PlaylistRepositoryImpl(api, channelDao)
    }

    @Provides
    fun provideChannelDao(database: TvDatabase): ChannelDao {
        return database.channelDao()
    }
}

// Database stub for compilation
abstract class TvDatabase {
    abstract fun channelDao(): ChannelDao
}
