package com.tv.app.data.local

import androidx.room.Database
import androidx.room.RoomDatabase
import com.tv.app.data.local.dao.ChannelDao
import com.tv.app.data.local.dao.VodDao
import com.tv.app.data.local.entity.ChannelEntity
import com.tv.app.data.local.entity.VodEntity

/**
 * Room Database definition
 * Demonstrates: @Database, entities array, version, exportSchema
 */
@Database(
    entities = [
        ChannelEntity::class,
        VodEntity::class
    ],
    version = 1,
    exportSchema = false
)
abstract class TvDatabase : RoomDatabase() {
    abstract fun channelDao(): ChannelDao
    abstract fun vodDao(): VodDao
}
