package com.fixture.test

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Insert
import androidx.room.Update
import androidx.room.Delete
import androidx.room.OnConflictStrategy
import kotlinx.coroutines.flow.Flow

/**
 * Room DAO fixtures demonstrating:
 * - @Dao interfaces
 * - suspend functions for async operations
 * - Flow return types for reactive streams
 * - CRUD operations
 * - Custom queries with JOINs
 * - Transaction support
 */

@Dao
interface ChannelDao {
    @Query("SELECT * FROM channels ORDER BY name ASC")
    fun getAllChannels(): Flow<List<ChannelEntity>>

    @Query("SELECT * FROM channels WHERE category_id = :categoryId")
    suspend fun getChannelsByCategory(categoryId: Long): List<ChannelEntity>

    @Query("SELECT * FROM channels WHERE is_favorite = 1")
    fun getFavoriteChannels(): Flow<List<ChannelEntity>>

    @Query("SELECT c.* FROM channels c INNER JOIN categories cat ON c.category_id = cat.id WHERE cat.name = :categoryName")
    suspend fun getChannelsInCategory(categoryName: String): List<ChannelEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insertChannel(channel: ChannelEntity): Long

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertChannels(channels: List<ChannelEntity>): List<Long>

    @Update
    suspend fun updateChannel(channel: ChannelEntity)

    @Delete
    suspend fun deleteChannel(channel: ChannelEntity)

    @Query("DELETE FROM channels WHERE id = :channelId")
    suspend fun deleteChannelById(channelId: Long)

    @Query("SELECT COUNT(*) FROM channels")
    suspend fun getChannelCount(): Int
}

@Dao
interface VodDao {
    @Query("SELECT * FROM vod_items WHERE category_id = :categoryId")
    fun getVodByCategory(categoryId: Long): Flow<List<VodEntity>>

    @Query("SELECT * FROM vod_items WHERE title LIKE '%' || :query || '%'")
    suspend fun searchVod(query: String): List<VodEntity>

    @Query("SELECT * FROM vod_items ORDER BY rating DESC LIMIT 10")
    suspend fun getTopRated(): List<VodEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insertVodItems(items: List<VodEntity>)

    @Query("DELETE FROM vod_items")
    suspend fun clearAllVod()
}

@Dao
interface EpgDao {
    @Query("SELECT * FROM epg_programs WHERE channel_id = :channelId AND start_time >= :fromTime AND end_time <= :toTime")
    suspend fun getProgramsForChannel(
        channelId: Long,
        fromTime: Long,
        toTime: Long
    ): List<EpgProgramEntity>

    @Query("SELECT * FROM epg_programs WHERE start_time <= :currentTime AND end_time > :currentTime")
    suspend fun getCurrentPrograms(currentTime: Long): List<EpgProgramEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insertPrograms(programs: List<EpgProgramEntity>)

    @Query("DELETE FROM epg_programs WHERE end_time < :cutoffTime")
    suspend fun deleteOldPrograms(cutoffTime: Long)
}
