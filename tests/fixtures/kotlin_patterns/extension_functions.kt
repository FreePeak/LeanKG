package com.fixture.test

import android.net.Uri

/**
 * Extension function fixtures demonstrating:
 * - Extension functions on built-in types
 * - Extension properties
 * - Generic extension functions
 * - Scope functions (let, run, apply, also, with)
 * - Nullable receiver extensions
 * - Infix functions
 */

// String extensions
fun String.toIconUrl(): String {
    return if (this.startsWith("http")) this else "https://example.com/icons/$this"
}

fun String?.orDefault(default: String): String = this ?: default

fun String.toChannelId(): Long {
    return this.hashCode().toLong()
}

// Extension property
val String.isValidUrl: Boolean
    get() = this.startsWith("http://") || this.startsWith("https://")

val String.fileExtension: String
    get() = substringAfterLast('.', "")

// Int extensions for time
fun Int.secondsToMillis(): Long = this * 1000L

fun Int.minutesToSeconds(): Int = this * 60

fun Int.formatDuration(): String {
    val hours = this / 3600
    val minutes = (this % 3600) / 60
    val seconds = this % 60
    return if (hours > 0) {
        "%02d:%02d:%02d".format(hours, minutes, seconds)
    } else {
        "%02d:%02d".format(minutes, seconds)
    }
}

// Extension on generic List
fun <T> List<T>?.isNullOrEmpty(): Boolean = this == null || this.isEmpty()

fun <T> List<T>.splitAt(index: Int): Pair<List<T>, List<T>> {
    return take(index) to drop(index)
}

inline fun <T> List<T>.applyIfNotEmpty(block: List<T>.() -> Unit): List<T> {
    if (isNotEmpty()) {
        block()
    }
    return this
}

// Scope function examples
class ChannelBuilder {
    var name: String = ""
    var url: String = ""
    var logo: String? = null

    fun build(): Channel {
        return Channel(0, name, url, logo)
    }
}

// Using apply - returns receiver after block
fun createChannelWithApply(): Channel {
    return ChannelBuilder().apply {
        name = "News Channel"
        url = "http://example.com/news"
        logo = "news.png"
    }.build()
}

// Using let - returns block result, non-null safety
fun processNullableChannel(channel: Channel?): String {
    return channel?.let {
        "Processing ${it.name}"
    } ?: "No channel to process"
}

// Using run - returns block result, this scope
fun ChannelBuilder.buildWithValidation(): Channel? {
    return run {
        if (name.isBlank() || url.isBlank()) {
            null
        } else {
            build()
        }
    }
}

// Using also - returns receiver, side effects
fun saveChannel(channel: Channel): Channel {
    return channel.also {
        println("Saving channel: ${it.name}")
        // Perform save operation
    }
}

// Using with - returns block result, explicit receiver
fun formatChannelInfo(channel: Channel): String {
    return with(channel) {
        """
            Channel: $name
            URL: $streamUrl
            Category: $category
        """.trimIndent()
    }
}

// Nullable receiver extension
fun String?.ifNullOrBlank(defaultValue: () -> String): String {
    return if (isNullOrBlank()) defaultValue() else this
}

// Infix function for DSL-like syntax
infix fun <A, B> A.toPair(that: B): Pair<A, B> = Pair(this, that)

// Usage example
fun createPairs() {
    val pair1 = "key" toPair "value"
    val pair2 = 1 toPair "one"
}

// Extension on Android Uri (simulated)
fun Uri.appendPath(segment: String): Uri {
    return this.buildUpon().appendPath(segment).build()
}

fun Uri.withQueryParam(key: String, value: String): Uri {
    return this.buildUpon().appendQueryParameter(key, value).build()
}

// Simulated Uri builder
class UriBuilder(private var path: String = "") {
    fun appendPath(segment: String): UriBuilder {
        path = "$path/$segment"
        return this
    }

    fun build(): Uri {
        return Uri.parse("https://example.com$path")
    }
}

class Uri(val url: String) {
    fun buildUpon(): UriBuilder = UriBuilder(url)

    companion object {
        fun parse(url: String): Uri = Uri(url)
    }
}
