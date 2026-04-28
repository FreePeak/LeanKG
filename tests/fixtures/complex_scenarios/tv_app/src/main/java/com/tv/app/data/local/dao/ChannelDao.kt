package com.tv.app.data.local.dao

import androidx.room.Dao
import androidx.room.Delete
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import androidx.room.Update
import com.tv.app.data.local.entity.CategoryEntity
import com.tv.app.data.local.entity.ChannelEntity
import kotlinx.coroutines.flow.Flow

/**
 * Room DAO with Flow and suspend functions
 * Demonstrates: @Dao, @Query, @Insert, @Update, @Delete, Flow return types
 */
@Dao
interface ChannelDao {

    @Query("SELECT * FROM channels ORDER BY sort_order, name ASC")
    fun getAll(): Flow<List<ChannelEntity>>

    @Query("SELECT * FROM channels WHERE category_id = :categoryId ORDER BY name ASC")
    fun getByCategory(categoryId: Long): Flow<List<ChannelEntity>>

    @Query("SELECT * FROM channels WHERE is_favorite = 1 ORDER BY name ASC")
    fun getFavorites(): Flow<List<ChannelEntity>>

    @Query("SELECT * FROM channels WHERE id = :id LIMIT 1")
    suspend fun getById(id: Long): ChannelEntity?

    @Query("SELECT * FROM channels WHERE name LIKE '%' || :query || '%'")
    suspend fun search(query: String): List<ChannelEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insert(channel: ChannelEntity): Long

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertAll(channels: List<ChannelEntity>): List<Long>

    @Update
    suspend fun update(channel: ChannelEntity)

    @Delete
    suspend fun delete(channel: ChannelEntity)

    @Query("DELETE FROM channels WHERE id = :id")
    suspend fun deleteById(id: Long)

    @Query("DELETE FROM channels")
    suspend fun deleteAll()

    @Query("UPDATE channels SET is_favorite = :isFavorite WHERE id = :id")
    suspend fun setFavorite(id: Long, isFavorite: Boolean)

    @Query("SELECT COUNT(*) FROM channels")
    suspend fun count(): Int

    @Query("SELECT * FROM categories WHERE is_active = 1 ORDER BY name ASC")
    suspend fun getCategories(): List<CategoryEntity>
}
