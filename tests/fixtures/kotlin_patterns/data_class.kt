package com.fixture.test

import android.os.Parcelable
import kotlinx.parcelize.Parcelize

/**
 * Data class fixtures demonstrating:
 * - data class with properties
 * - copy() function
 * - destructuring declarations
 * - toString(), equals(), hashCode() auto-generated
 * - sealed class hierarchies
 * - inline classes for type safety
 */

// Basic data class with validation
data class Channel(
    val id: Long,
    val name: String,
    val streamUrl: String,
    val logoUrl: String? = null,
    val category: String = "Uncategorized"
) {
    init {
        require(name.isNotBlank()) { "Channel name cannot be blank" }
        require(streamUrl.isNotBlank()) { "Stream URL cannot be blank" }
    }

    val displayName: String
        get() = if (category != "Uncategorized") "$name ($category)" else name
}

// Data class with computed properties
data class VodItem(
    val id: Long,
    val title: String,
    val description: String?,
    val durationSeconds: Int,
    val rating: Float? = null
) {
    val durationFormatted: String
        get() {
            val hours = durationSeconds / 3600
            val minutes = (durationSeconds % 3600) / 60
            return if (hours > 0) "${hours}h ${minutes}m" else "${minutes}m"
        }

    val hasRating: Boolean
        get() = rating != null && rating > 0
}

// Sealed class hierarchy for UI states
sealed class UiState<out T> {
    data object Loading : UiState<Nothing>()
    data class Success<T>(val data: T) : UiState<T>()
    data class Error(val message: String, val code: Int? = null) : UiState<Nothing>()
    data object Empty : UiState<Nothing>()
}

// Sealed class for player states
sealed class PlayerState {
    abstract val position: Long

    data class Idle(override val position: Long = 0) : PlayerState()
    data class Buffering(override val position: Long) : PlayerState()
    data class Playing(
        override val position: Long,
        val duration: Long,
        val isPlaying: Boolean = true
    ) : PlayerState()

    data class Paused(override val position: Long) : PlayerState()
    data class Error(
        override val position: Long,
        val errorCode: Int,
        val errorMessage: String
    ) : PlayerState()
}

// Inline class for type-safe IDs
@JvmInline
value class ChannelId(val value: Long) {
    init {
        require(value >= 0) { "Channel ID must be non-negative" }
    }
}

@JvmInline
value class VodId(val value: Long)

// Parcelable data class for Android
@Parcelize
data class ChannelParcelable(
    val id: Long,
    val name: String,
    val streamUrl: String
) : Parcelable

// Data class with varargs and builder pattern
data class Playlist(
    val id: String,
    val name: String,
    val channels: List<Channel> = emptyList()
) {
    companion object {
        fun build(id: String, name: String, block: PlaylistBuilder.() -> Unit): Playlist {
            val builder = PlaylistBuilder(id, name)
            builder.block()
            return builder.build()
        }
    }
}

class PlaylistBuilder(private val id: String, private val name: String) {
    private val channels = mutableListOf<Channel>()

    fun addChannel(channel: Channel) {
        channels.add(channel)
    }

    fun build(): Playlist = Playlist(id, name, channels.toList())
}
