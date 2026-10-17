package io.github.erikedits.homelydrive

import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper

/** One entry of the upload queue. Finished entries stay as upload history. */
data class QueueItem(
    val id: Long,
    /** folder, share, scan or docs */
    val kind: String,
    val folderId: String,
    val local: String,
    val rel: String,
    val space: String,
    val remote: String,
    val size: Long,
    val mtime: Long,
    val conflict: String,
    /** The local file is the app's own copy and is removed afterwards. */
    val temp: Boolean,
    /** pending, done, error, skipped */
    val status: String,
    val uploadId: String?,
    val finalPath: String?,
    val error: String?,
    val addedAt: Long,
    val doneAt: Long,
) {
    val name: String get() = remote.substringAfterLast('/')

    companion object {
        fun from(c: Cursor) = QueueItem(
            id = c.getLong(0), kind = c.getString(1), folderId = c.getString(2), local = c.getString(3),
            rel = c.getString(4), space = c.getString(5), remote = c.getString(6), size = c.getLong(7),
            mtime = c.getLong(8), conflict = c.getString(9), temp = c.getInt(10) != 0, status = c.getString(11),
            uploadId = c.getString(12), finalPath = c.getString(13), error = c.getString(14),
            addedAt = c.getLong(15), doneAt = if (c.isNull(16)) 0 else c.getLong(16),
        )

        const val COLUMNS = "id, kind, folder_id, local, rel, space, remote, size, mtime, conflict, temp, status, upload_id, final_path, error, added_at, done_at"
    }
}

/** A file of an upload folder that is already on the server. */
data class SeenFile(val folderId: String, val rel: String, val size: Long, val mtime: Long, val remote: String, val uploadedAt: Long, val deleted: Boolean)

/** Local state: upload queue/history and which files were already uploaded. */
class Db(ctx: Context) : SQLiteOpenHelper(ctx, "homelydrive.db", null, 1) {

    override fun onConfigure(db: SQLiteDatabase) {
        db.enableWriteAheadLogging()
    }

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL(
            """CREATE TABLE queue(
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                kind TEXT NOT NULL,
                folder_id TEXT NOT NULL DEFAULT '',
                local TEXT NOT NULL,
                rel TEXT NOT NULL DEFAULT '',
                space TEXT NOT NULL,
                remote TEXT NOT NULL,
                size INTEGER NOT NULL,
                mtime INTEGER NOT NULL,
                conflict TEXT NOT NULL DEFAULT 'rename',
                temp INTEGER NOT NULL DEFAULT 0,
                status TEXT NOT NULL DEFAULT 'pending',
                upload_id TEXT,
                final_path TEXT,
                error TEXT,
                added_at INTEGER NOT NULL,
                done_at INTEGER
            )"""
        )
        db.execSQL("CREATE INDEX queue_status ON queue(status, id)")
        db.execSQL("CREATE INDEX queue_folder ON queue(folder_id, rel)")
        db.execSQL(
            """CREATE TABLE seen(
                folder_id TEXT NOT NULL,
                rel TEXT NOT NULL,
                size INTEGER NOT NULL,
                mtime INTEGER NOT NULL,
                remote TEXT NOT NULL,
                uploaded_at INTEGER NOT NULL,
                deleted INTEGER NOT NULL DEFAULT 0,
                PRIMARY KEY(folder_id, rel)
            )"""
        )
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {}

    // ----- queue -----

    fun enqueue(kind: String, folderId: String, local: String, rel: String, space: String, remote: String,
                size: Long, mtime: Long, conflict: String, temp: Boolean): Long {
        val v = ContentValues().apply {
            put("kind", kind); put("folder_id", folderId); put("local", local); put("rel", rel)
            put("space", space); put("remote", remote); put("size", size); put("mtime", mtime)
            put("conflict", conflict); put("temp", if (temp) 1 else 0); put("status", "pending")
            put("added_at", System.currentTimeMillis())
        }
        return writableDatabase.insert("queue", null, v)
    }

    /** The waiting or failed entry of a folder file, if any. */
    fun openFor(folderId: String, rel: String): QueueItem? =
        one("SELECT ${QueueItem.COLUMNS} FROM queue WHERE folder_id = ? AND rel = ? AND status IN ('pending', 'error') ORDER BY id DESC LIMIT 1", folderId, rel)

    fun nextPending(): QueueItem? = one("SELECT ${QueueItem.COLUMNS} FROM queue WHERE status = 'pending' ORDER BY id LIMIT 1")

    fun pendingCount(): Int = count("SELECT COUNT(*) FROM queue WHERE status = 'pending'")

    fun pendingBytes(): Long = readableDatabase.rawQuery("SELECT COALESCE(SUM(size), 0) FROM queue WHERE status = 'pending'", null).use {
        if (it.moveToFirst()) it.getLong(0) else 0
    }

    fun errorCount(): Int = count("SELECT COUNT(*) FROM queue WHERE status = 'error'")

    fun pending(limit: Int = 200): List<QueueItem> = many("SELECT ${QueueItem.COLUMNS} FROM queue WHERE status = 'pending' ORDER BY id LIMIT $limit")

    fun history(limit: Int = 300): List<QueueItem> =
        many("SELECT ${QueueItem.COLUMNS} FROM queue WHERE status != 'pending' ORDER BY done_at DESC, id DESC LIMIT $limit")

    /** The file changed: upload it again from the start. */
    fun requeue(id: Long, size: Long, mtime: Long) {
        writableDatabase.update("queue", ContentValues().apply {
            put("size", size); put("mtime", mtime); putNull("upload_id")
            put("status", "pending"); putNull("error"); putNull("done_at")
        }, "id = ?", arrayOf(id.toString()))
    }

    fun get(id: Long): QueueItem? = one("SELECT ${QueueItem.COLUMNS} FROM queue WHERE id = ?", id.toString())

    fun retry(id: Long) {
        writableDatabase.execSQL("UPDATE queue SET status = 'pending', error = NULL, done_at = NULL WHERE id = ? AND status = 'error'", arrayOf(id))
    }

    fun setUploadId(id: Long, uploadId: String?) {
        writableDatabase.update("queue", ContentValues().apply {
            if (uploadId == null) putNull("upload_id") else put("upload_id", uploadId)
        }, "id = ?", arrayOf(id.toString()))
    }

    fun finish(id: Long, status: String, finalPath: String?, error: String?) {
        writableDatabase.update("queue", ContentValues().apply {
            put("status", status)
            if (finalPath != null) put("final_path", finalPath)
            if (error != null) put("error", error) else putNull("error")
            put("done_at", System.currentTimeMillis())
            putNull("upload_id")
        }, "id = ?", arrayOf(id.toString()))
        // Keep the history short.
        writableDatabase.execSQL(
            "DELETE FROM queue WHERE status != 'pending' AND id NOT IN (SELECT id FROM queue WHERE status != 'pending' ORDER BY done_at DESC LIMIT 1000)"
        )
    }

    /** Puts failed entries back into the queue. */
    fun retryErrors() {
        writableDatabase.execSQL("UPDATE queue SET status = 'pending', error = NULL, done_at = NULL WHERE status = 'error'")
    }

    fun clearHistory() {
        writableDatabase.execSQL("DELETE FROM queue WHERE status != 'pending'")
    }

    fun cancelPending(id: Long) {
        writableDatabase.delete("queue", "id = ? AND status = 'pending'", arrayOf(id.toString()))
    }

    /** Removes the pending entries of a folder (folder removed or disabled). */
    fun dropFolder(folderId: String, forgetSeen: Boolean) {
        writableDatabase.delete("queue", "folder_id = ? AND status = 'pending'", arrayOf(folderId))
        if (forgetSeen) writableDatabase.delete("seen", "folder_id = ?", arrayOf(folderId))
    }

    // ----- seen files -----

    fun seen(folderId: String): Map<String, SeenFile> {
        val out = HashMap<String, SeenFile>()
        readableDatabase.rawQuery(
            "SELECT folder_id, rel, size, mtime, remote, uploaded_at, deleted FROM seen WHERE folder_id = ?", arrayOf(folderId)
        ).use { c ->
            while (c.moveToNext()) {
                val s = SeenFile(c.getString(0), c.getString(1), c.getLong(2), c.getLong(3), c.getString(4), c.getLong(5), c.getInt(6) != 0)
                out[s.rel] = s
            }
        }
        return out
    }

    fun seenOne(folderId: String, rel: String): SeenFile? = readableDatabase.rawQuery(
        "SELECT folder_id, rel, size, mtime, remote, uploaded_at, deleted FROM seen WHERE folder_id = ? AND rel = ?", arrayOf(folderId, rel)
    ).use { c ->
        if (c.moveToFirst()) SeenFile(c.getString(0), c.getString(1), c.getLong(2), c.getLong(3), c.getString(4), c.getLong(5), c.getInt(6) != 0) else null
    }

    fun markSeen(folderId: String, rel: String, size: Long, mtime: Long, remote: String) {
        writableDatabase.insertWithOnConflict("seen", null, ContentValues().apply {
            put("folder_id", folderId); put("rel", rel); put("size", size); put("mtime", mtime)
            put("remote", remote); put("uploaded_at", System.currentTimeMillis()); put("deleted", 0)
        }, SQLiteDatabase.CONFLICT_REPLACE)
    }

    fun markDeleted(folderId: String, rel: String) {
        writableDatabase.update("seen", ContentValues().apply { put("deleted", 1) }, "folder_id = ? AND rel = ?", arrayOf(folderId, rel))
    }

    /** Uploaded files that are still on the phone and older than [before]. */
    fun deletable(folderId: String, before: Long): List<SeenFile> {
        val out = ArrayList<SeenFile>()
        readableDatabase.rawQuery(
            "SELECT folder_id, rel, size, mtime, remote, uploaded_at, deleted FROM seen WHERE folder_id = ? AND deleted = 0 AND uploaded_at <= ?",
            arrayOf(folderId, before.toString())
        ).use { c ->
            while (c.moveToNext()) out += SeenFile(c.getString(0), c.getString(1), c.getLong(2), c.getLong(3), c.getString(4), c.getLong(5), false)
        }
        return out
    }

    fun seenCount(folderId: String): Int = count("SELECT COUNT(*) FROM seen WHERE folder_id = ?", folderId)

    // ----- helpers -----

    private fun count(sql: String, vararg args: String): Int = readableDatabase.rawQuery(sql, args).use {
        if (it.moveToFirst()) it.getInt(0) else 0
    }

    private fun one(sql: String, vararg args: String): QueueItem? = readableDatabase.rawQuery(sql, args).use {
        if (it.moveToFirst()) QueueItem.from(it) else null
    }

    private fun many(sql: String, vararg args: String): List<QueueItem> = readableDatabase.rawQuery(sql, args).use {
        val out = ArrayList<QueueItem>()
        while (it.moveToNext()) out += QueueItem.from(it)
        out
    }
}
