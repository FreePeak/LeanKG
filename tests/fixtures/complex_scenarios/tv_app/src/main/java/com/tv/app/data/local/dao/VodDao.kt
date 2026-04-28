package com.tv.app.data.local.dao

import androidx.room.Dao
import androidx.room.Delete
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import androidx.room.Update
import com.tv.app.data.local.entity.VodEntity
import kotlinx.coroutines.flow.Flow

/**
 * VOD DAO demonstrating different query patterns
 * Demonstrates: LIKE search, BETWEEN, ORDER BY with multiple columns
 */
@Dao
interface VodDao {

    @Query("SELECT * FROM vod_items ORDER BY title ASC")
    fun getAll(): Flow<List<VodEntity>>

    @Query("SELECT * FROM vod_items WHERE category_id = :categoryId ORDER BY rating DESC, title ASC")
    fun getByCategory(categoryId: Long): Flow<List<VodEntity>>

    @Query("SELECT * FROM vod_items WHERE is_watched = 0 ORDER BY rating DESC LIMIT 20")
    suspend fun getRecommended(): List<VodEntity>>

    @Query("SELECT * FROM vod_items WHERE title LIKE '%' || :query || '%' OR description LIKE '%' || :query || '%'")
    suspend fun search(query: String): List<VodEntity>

    @Query("SELECT * FROM vod_items WHERE rating >= :minRating AND rating <= :maxRating")
    suspend fun getByRatingRange(minRating: Float, maxRating: Float): List<VodEntity>

    @Query("SELECT * FROM vod_items WHERE year BETWEEN :startYear AND :endYear")
    suspend fun getByYearRange(startYear: Int, endYear: Int): List<VodEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insert(item: VodEntity): Long

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertAll(items: List<VodEntity>)

    @Update
    suspend fun update(item: VodEntity)

    @Query("UPDATE vod_items SET is_watched = :watched, watch_progress = :progress WHERE id = :id")
    suspend fun updateWatchProgress(id: Long, watched: Boolean, progress: Int)

    @Delete
    suspend fun delete(item: VodEntity)

    @Query("DELETE FROM vod_items")
    suspend fun deleteAll()
}
