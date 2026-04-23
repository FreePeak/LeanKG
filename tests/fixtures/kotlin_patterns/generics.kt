package com.fixture.test

/**
 * Generic type fixtures demonstrating:
 * - Generic functions and classes
 * - Type constraints (upper bounds)
 * - Variance (in/out)
 * - Reified type parameters
 * - Star projections
 * - Where clauses for multiple constraints
 */

// Generic class with constraint
class Cache<T : Any> {
    private val store = mutableMapOf<String, T>()

    fun put(key: String, value: T) {
        store[key] = value
    }

    fun get(key: String): T? = store[key]
    fun getOrDefault(key: String, default: T): T = store.getOrDefault(key, default)
    fun clear() = store.clear()
}

// Generic function with constraint
fun <T : Comparable<T>> maxOf(a: T, b: T): T = if (a > b) a else b
fun <T : Number> sumOf(list: List<T>): Double = list.sumOf { it.toDouble() }

// Covariant (out) - Producer only
interface ChannelSource<out T> {
    fun getNext(): T
    fun getAll(): List<T>
}

class ChannelRepository<out T : Channel>(private val channels: List<T>) : ChannelSource<T> {
    private var index = 0
    override fun getNext(): T = channels[index++ % channels.size]
    override fun getAll(): List<T> = channels.toList()
}

// Contravariant (in) - Consumer only
interface ChannelValidator<in T> {
    fun validate(item: T): Boolean
}

class UrlValidator : ChannelValidator<Channel> {
    override fun validate(item: Channel): Boolean = item.streamUrl.isNotBlank()
}

// Reified types with inline
inline fun <reified T> filterByType(list: List<Any>): List<T> = list.filterIsInstance<T>()

inline fun <reified T : Any> String.parseAs(): T? {
    return try {
        when (T::class) {
            Channel::class -> Channel(0, this, "") as T
            else -> null
        }
    } catch (e: Exception) { null }
}

// Where clause for multiple constraints
fun <T> processComparable(
    items: List<T>
): List<T> where T : Comparable<T>, T : CharSequence = items.sorted()

// Star projection
fun printAnyList(list: List<*>) = list.forEach { println(it) }

// Typealias
typealias ChannelList = List<Channel>
typealias ChannelMap = Map<Long, Channel>
