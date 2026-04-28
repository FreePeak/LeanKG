package com.tv.app.data.remote

import retrofit2.http.GET
import retrofit2.http.Query
import retrofit2.http.Url

/**
 * Retrofit API interface for playlist operations
 * Demonstrates: @GET, @Query, @Url, suspend functions
 */
interface PlaylistApi {

    @GET("playlist")
    suspend fun getPlaylist(
        @Query("url") url: String
    ): PlaylistResponse

    @GET
    suspend fun fetchFromUrl(
        @Url url: String
    ): String

    @GET("epg")
    suspend fun getEpgData(
        @Query("channel_id") channelId: Long,
        @Query("start") startTime: Long,
        @Query("end") endTime: Long
    ): EpgResponse

    @GET("categories")
    suspend fun getCategories(): CategoriesResponse
}

// Response data classes
data class PlaylistResponse(
    val channels: List<ChannelDto>,
    val categories: List<CategoryDto>
)

data class ChannelDto(
    val id: String,
    val name: String,
    val streamUrl: String,
    val logoUrl: String?,
    val categoryId: String
)

data class CategoryDto(
    val id: String,
    val name: String,
    val iconUrl: String?
)

data class EpgResponse(
    val programs: List<ProgramDto>
)

data class ProgramDto(
    val title: String,
    val description: String?,
    val startTime: Long,
    val endTime: Long
)

data class CategoriesResponse(
    val categories: List<CategoryDto>
)
