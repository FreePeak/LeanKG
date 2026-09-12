package com.example.tvapp.data

import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.ForeignKey
import androidx.room.Insert
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.RoomDatabase

@Entity(tableName = "channels")
data class ChannelEntity(
    @PrimaryKey val id: Long,
    val name: String
)

@Entity(
    tableName = "programs",
    foreignKeys = [
        ForeignKey(
            entity = ChannelEntity::class,
            parentColumns = ["id"],
            childColumns = ["channelId"]
        )
    ]
)
data class ProgramEntity(
    @PrimaryKey val id: Long,
    val channelId: Long,
    val title: String
)

@Dao
interface ChannelDao {
    @Query("SELECT * FROM channels")
    fun getAll(): List<ChannelEntity>

    @Insert
    fun insert(channel: ChannelEntity)
}

@Database(
    entities = [ChannelEntity::class, ProgramEntity::class],
    version = 1
)
abstract class AppDatabase : RoomDatabase() {
    abstract fun channelDao(): ChannelDao
}
