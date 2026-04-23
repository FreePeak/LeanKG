package com.tv.app.di

import android.content.Context
import androidx.room.Room
import com.tv.app.data.local.TvDatabase
import com.tv.app.data.local.dao.ChannelDao
import com.tv.app.data.local.dao.VodDao
import com.tv.app.data.remote.PlaylistApi
import com.tv.app.data.repository.ChannelRepository
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import retrofit2.converter.gson.GsonConverterFactory
import javax.inject.Singleton

/**
 * Hilt DI Module providing app-level dependencies
 * Demonstrates: @Module, @InstallIn, @Provides, @Singleton, @ApplicationContext
 */
@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    @Provides
    @Singleton
    fun provideDatabase(
        @ApplicationContext context: Context
    ): TvDatabase {
        return Room.databaseBuilder(
            context,
            TvDatabase::class.java,
            "tv_database"
        )
            .fallbackToDestructiveMigration()
            .build()
    }

    @Provides
    fun provideChannelDao(database: TvDatabase): ChannelDao = database.channelDao()

    @Provides
    fun provideVodDao(database: TvDatabase): VodDao = database.vodDao()

    @Provides
    @Singleton
    fun provideRetrofit(): Retrofit {
        return Retrofit.Builder()
            .baseUrl("https://api.tvapp.com/")
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
    fun provideChannelRepository(
        channelDao: ChannelDao,
        api: PlaylistApi
    ): ChannelRepository {
        return ChannelRepository(channelDao, api)
    }
}
