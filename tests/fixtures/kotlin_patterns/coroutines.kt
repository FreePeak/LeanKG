package com.fixture.test

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.delay
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.withContext
import kotlinx.coroutines.Dispatchers

/**
 * Coroutine fixtures demonstrating:
 * - suspend functions
 * - Flow cold streams
 * - StateFlow/SharedFlow hot streams
 * - async/await for parallel work
 * - withContext for dispatcher switching
 * - Flow operators (map, filter, combine)
 */

class ContentRepository {

    // Basic suspend function
    suspend fun fetchChannels(): List<Channel> {
        delay(100) // Simulate network
        return listOf(
            Channel(1, "News", "http://example.com/news"),
            Channel(2, "Sports", "http://example.com/sports")
        )
    }

    // Suspend with return value computation
    suspend fun searchChannels(query: String): List<Channel> {
        val all = fetchChannels()
        delay(50)
        return all.filter { it.name.contains(query, ignoreCase = true) }
    }

    // Flow - cold stream
    fun channelsFlow(): Flow<List<Channel>> = flow {
        emit(emptyList()) // Loading state
        delay(100)
        emit(fetchChannels()) // Data
    }

    // Flow with operators
    fun favoriteChannelsFlow(): Flow<List<Channel>> {
        return channelsFlow()
            .map { channels -> channels.filter { it.name.contains("News") } }
            .filter { it.isNotEmpty() }
    }

    // StateFlow - hot stream with initial value
    private val _currentChannel = MutableStateFlow<Channel?>(null)
    val currentChannel: StateFlow<Channel?> = _currentChannel

    fun selectChannel(channel: Channel) {
        _currentChannel.value = channel
    }

    // Combine multiple flows
    fun combinedFlow(): Flow<Pair<List<Channel>, List<VodItem>>> {
        val channelsFlow = channelsFlow()
        val vodFlow = vodItemsFlow()
        return combine(channelsFlow, vodFlow) { channels, vod ->
            channels to vod
        }
    }

    private fun vodItemsFlow(): Flow<List<VodItem>> = flow {
        emit(emptyList())
        delay(150)
        emit(listOf(
            VodItem(1, "Movie 1", "Desc", 7200, 4.5f),
            VodItem(2, "Movie 2", "Desc", 5400, 4.0f)
        ))
    }

    // Async parallel operations
    suspend fun refreshAllData(): Boolean {
        return withContext(Dispatchers.IO) {
            val channelsDeferred = async { fetchChannels() }
            val vodDeferred = async { fetchVodItems() }
            val epgDeferred = async { fetchEpgData() }

            try {
                val channels = channelsDeferred.await()
                val vod = vodDeferred.await()
                val epg = epgDeferred.await()

                channels.isNotEmpty() && vod.isNotEmpty() && epg.isNotEmpty()
            } catch (e: Exception) {
                false
            }
        }
    }

    // Multiple parallel with awaitAll
    suspend fun fetchMultiplePlaylists(urls: List<String>): List<List<Channel>> {
        return withContext(Dispatchers.IO) {
            urls.map { url ->
                async { fetchPlaylist(url) }
            }.awaitAll()
        }
    }

    private suspend fun fetchPlaylist(url: String): List<Channel> {
        delay(100)
        return emptyList()
    }

    private suspend fun fetchVodItems(): List<VodItem> {
        delay(100)
        return emptyList()
    }

    private suspend fun fetchEpgData(): List<EpgProgram> {
        delay(100)
        return emptyList()
    }
}

// Supporting classes
data class EpgProgram(
    val id: Long,
    val title: String,
    val startTime: Long,
    val endTime: Long
)
