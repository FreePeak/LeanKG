package com.tv.app.data.repository

import com.tv.app.data.local.dao.ChannelDao
import com.tv.app.data.local.entity.ChannelEntity
import com.tv.app.data.remote.PlaylistApi
import kotlinx.coroutines.flow.Flow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Repository pattern implementation
 * Demonstrates: Repository pattern, Flow, suspend functions, DI
 */
@Singleton
class ChannelRepository @Inject constructor(
    private val channelDao: ChannelDao,
    private val api: PlaylistApi
) {
    fun getAllChannels(): Flow<List<ChannelEntity>> = channelDao.getAll()

    suspend fun refreshChannels(url: String) {
        val response = api.getPlaylist(url)
        // Transform and save
    }

    suspend fun toggleFavorite(channelId: Long, isFavorite: Boolean) {
        channelDao.setFavorite(channelId, isFavorite)
    }
}
