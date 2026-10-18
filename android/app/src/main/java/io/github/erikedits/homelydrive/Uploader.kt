package io.github.erikedits.homelydrive

import android.content.Context
import android.media.MediaScannerConnection
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import java.io.File
import java.io.FileInputStream
import java.io.IOException
import java.nio.file.Files
import java.security.MessageDigest
import java.util.concurrent.CopyOnWriteArrayList

/** Live upload state for the app screen, the notification and the widget. */
object UploadState {
    enum class Phase { IDLE, HASHING, UPLOADING }

    @Volatile var phase = Phase.IDLE
    @Volatile var current: String = ""
    @Volatile var sent = 0L
    @Volatile var total = 0L
    /** Why nothing is uploaded right now (server unreachable, ...), or null. */
    @Volatile var waiting: String? = null
    @Volatile var retryAt = 0L
    @Volatile var serviceRunning = false
    /** Files uploaded since the service started. */
    @Volatile var uploadedCount = 0

    private val listeners = CopyOnWriteArrayList<() -> Unit>()
    private val main = Handler(Looper.getMainLooper())
    private var lastProgress = 0L

    fun listen(l: () -> Unit) = listeners.add(l)
    fun unlisten(l: () -> Unit) = listeners.remove(l)

    fun changed() {
        main.post { listeners.forEach { it() } }
    }

    fun begin(phase: Phase, name: String, total: Long) {
        this.phase = phase
        current = name
        this.total = total
        sent = 0
        changed()
    }

    fun progress(sent: Long) {
        this.sent = sent
        val now = SystemClock.elapsedRealtime()
        if (now - lastProgress >= 500) {
            lastProgress = now
            changed()
        }
    }

    fun idle() {
        phase = Phase.IDLE
        current = ""
        sent = 0
        total = 0
        changed()
    }
}

/**
 * Upload engine: finds new files in the chosen phone folders and sends the
 * queue to the server with resumable chunked uploads. Upload only — files
 * deleted on the phone stay on the server ("Archiv-Modus").
 */
class Uploader(private val ctx: Context) {
    private val prefs get() = ctx.hd.prefs
    private val db get() = ctx.hd.db

    enum class Result { IDLE, STOPPED, OFFLINE, LOGGED_OUT, BUSY }

    private class Stopped : Exception()
    private class Restart : Exception()

    fun api(): Api? = if (prefs.isLoggedIn) Api(prefs.server, prefs.token) else null

    // ----- scanning -----

    /** Number of files queued and whether some files are still being written. */
    data class ScanResult(val queued: Int, val unsettled: Boolean)

    /** Walks an upload folder and queues new or changed files. */
    fun scan(folder: UploadFolder): ScanResult {
        val root = File(folder.local)
        if (!folder.enabled || !root.isDirectory) return ScanResult(0, false)
        val seen = db.seen(folder.id)
        val now = System.currentTimeMillis()
        var queued = 0
        var unsettled = false
        walk(root, "") { f, rel ->
            val size = f.length()
            val mtime = f.lastModified()
            if (now - mtime in 0 until SETTLE_MS) {
                unsettled = true // still being written (e.g. a video recording)
                return@walk
            }
            val s = seen[rel]
            if (s != null && !s.deleted && s.size == size && s.mtime == mtime) return@walk
            val open = db.openFor(folder.id, rel)
            if (open != null) {
                if (open.size != size || open.mtime != mtime) {
                    db.requeue(open.id, size, mtime)
                    queued++
                }
                return@walk
            }
            // Changed after the upload: replace the server copy. New file: never overwrite.
            val conflict = if (s != null && !s.deleted) "overwrite" else "rename"
            db.enqueue("folder", folder.id, f.absolutePath, rel, folder.space, joinPath(folder.remote, rel), size, mtime, conflict, false)
            queued++
        }
        return ScanResult(queued, unsettled)
    }

    private fun walk(dir: File, prefix: String, fn: (File, String) -> Unit) {
        val list = dir.listFiles() ?: return
        list.sortBy { it.name }
        for (f in list) {
            if (isJunk(f.name)) continue
            val rel = if (prefix.isEmpty()) f.name else "$prefix/${f.name}"
            try {
                if (Files.isSymbolicLink(f.toPath())) continue
            } catch (e: Exception) {
                continue
            }
            if (f.isDirectory) walk(f, rel, fn) else if (f.isFile) fn(f, rel)
        }
    }

    /** Deletes uploaded files on the phone when the folder asks for it. */
    fun cleanup(folder: UploadFolder) {
        if (folder.deleteAfterDays < 0) return
        val before = System.currentTimeMillis() - folder.deleteAfterDays * 86_400_000L
        val removed = ArrayList<String>()
        for (s in db.deletable(folder.id, before)) {
            val f = File(folder.local, s.rel)
            if (!f.exists()) {
                db.markDeleted(s.folderId, s.rel)
            } else if (f.length() == s.size && f.lastModified() == s.mtime && f.delete()) {
                db.markDeleted(s.folderId, s.rel)
                removed += f.absolutePath
            }
        }
        if (removed.isNotEmpty()) MediaScannerConnection.scanFile(ctx, removed.toTypedArray(), null, null)
    }

    /** Queues a single file (shared from another app, a scan, a file saved from the Files app). */
    fun enqueueFile(kind: String, file: File, space: String, remote: String, conflict: String = "rename", temp: Boolean = true): Long =
        db.enqueue(kind, "", file.absolutePath, "", space, remote, file.length(), file.lastModified(), conflict, temp)

    /**
     * Queues phone files and whole folders (with their subfolders, keeping the
     * structure) for a one-time upload. The files are read where they are.
     * Returns the number of queued files.
     */
    fun enqueueLocal(items: List<File>, space: String, target: String): Int {
        var n = 0
        val sql = db.writableDatabase
        sql.beginTransaction()
        try {
            for (f in items) {
                if (f.isDirectory) {
                    val base = joinPath(target, f.name)
                    walk(f, "") { file, rel ->
                        db.enqueue("manual", "", file.absolutePath, "", space, joinPath(base, rel), file.length(), file.lastModified(), "rename", false)
                        n++
                    }
                } else if (f.isFile) {
                    db.enqueue("manual", "", f.absolutePath, "", space, joinPath(target, f.name), f.length(), f.lastModified(), "rename", false)
                    n++
                }
            }
            sql.setTransactionSuccessful()
        } finally {
            sql.endTransaction()
        }
        UploadState.changed()
        return n
    }

    /** Removes finished entries and the app's own copies they still hold. */
    fun clearHistory() {
        for (i in db.history(5000)) if (i.temp) File(i.local).delete()
        db.clearHistory()
        cleanOutbox()
    }

    /** Deletes copies in the outbox that no queue entry refers to any more. */
    fun cleanOutbox() {
        val used = HashSet<String>()
        db.pending(100_000).forEach { used += it.local }
        db.history(5000).filter { it.status == "error" }.forEach { used += it.local }
        val dir = outbox(ctx)
        dir.walkBottomUp().forEach { f ->
            if (f == dir) return@forEach
            if (f.isFile && f.absolutePath !in used && System.currentTimeMillis() - f.lastModified() > 60_000) f.delete()
            if (f.isDirectory && f.list()?.isEmpty() == true) f.delete()
        }
    }

    // ----- uploading -----

    /** Uploads queued files one after another until the queue is empty or [stop] says so. */
    fun runQueue(stop: () -> Boolean): Result {
        val api = api() ?: return Result.LOGGED_OUT
        // Service and job must never upload at the same time (the same file would arrive twice).
        if (!running.compareAndSet(false, true)) return Result.BUSY
        try {
            while (true) {
                if (stop()) return Result.STOPPED
                val item = db.nextPending() ?: return Result.IDLE
                try {
                    upload(api, item, stop)
                } catch (e: Restart) {
                    // upload expired on the server or file changed: next round starts fresh
                } catch (e: ApiException) {
                    if (e.isNetwork) return Result.OFFLINE
                    if (e.status == 401) return Result.LOGGED_OUT
                    db.finish(item.id, "error", null, e.message)
                    UploadState.changed()
                } catch (e: IOException) {
                    // local read problem (file gone, no permission)
                    db.finish(item.id, "error", null, e.message ?: e.javaClass.simpleName)
                    UploadState.changed()
                }
            }
        } catch (e: Stopped) {
            return Result.STOPPED
        } finally {
            running.set(false)
            UploadState.idle()
        }
    }

    private fun upload(api: Api, queued: QueueItem, stop: () -> Boolean) {
        var item = queued
        val f = File(item.local)
        if (!f.isFile) {
            db.finish(item.id, "skipped", null, ctx.getString(R.string.err_gone))
            UploadState.changed()
            return
        }
        val size = f.length()
        val mtime = f.lastModified()
        if (size != item.size || mtime != item.mtime) {
            db.requeue(item.id, size, mtime)
            item = item.copy(size = size, mtime = mtime, uploadId = null)
        }

        var id = item.uploadId
        var offset = 0L
        if (id != null) {
            try {
                offset = api.uploadOffset(id)
            } catch (e: ApiException) {
                if (e.isNetwork || e.status == 401) throw e
                id = null // expired: start over
            }
        }
        if (id == null) {
            UploadState.begin(UploadState.Phase.HASHING, item.name, size)
            val sum = sha256(f, stop)
            if (f.length() != size || f.lastModified() != mtime) throw Restart()
            val st = api.createUpload(item.space, item.remote, size, sum, mtime, item.conflict)
            if (st.status != "pending") {
                done(item, st.path)
                return
            }
            id = st.id
            offset = st.offset
            db.setUploadId(item.id, id)
        }
        val uid: String = id ?: throw Restart()

        UploadState.begin(UploadState.Phase.UPLOADING, item.name, size)
        UploadState.progress(offset)
        FileInputStream(f).use { input ->
            input.channel.position(offset)
            while (offset < size) {
                if (stop()) throw Stopped()
                val len = minOf(CHUNK, size - offset)
                val st = try {
                    api.uploadChunk(uid, offset, len, input) { UploadState.progress(it) }
                } catch (e: ApiException) {
                    when {
                        e.status == 409 && e.offset != null -> {
                            offset = e.offset
                            input.channel.position(offset)
                            continue
                        }
                        e.status == 404 -> {
                            db.setUploadId(item.id, null)
                            throw Restart()
                        }
                        else -> throw e
                    }
                }
                offset = st.offset
                if (st.status != "pending") {
                    done(item, st.path)
                    return
                }
            }
        }
    }

    private fun done(item: QueueItem, path: String) {
        db.finish(item.id, "done", path.ifEmpty { item.remote }, null)
        if (item.kind == "folder") {
            db.markSeen(item.folderId, item.rel, item.size, item.mtime, path)
            val folder = prefs.folders.find { it.id == item.folderId }
            if (folder != null && folder.deleteAfterDays == 0) cleanup(folder)
        }
        if (item.temp) {
            File(item.local).delete()
            File(item.local).parentFile?.let { if (it.parentFile == outbox(ctx) && it.list()?.isEmpty() == true) it.delete() }
        }
        UploadState.uploadedCount++
        UploadState.changed()
    }

    private fun sha256(f: File, stop: () -> Boolean): String {
        val md = MessageDigest.getInstance("SHA-256")
        val buf = ByteArray(256 * 1024)
        var done = 0L
        FileInputStream(f).use { input ->
            while (true) {
                if (stop()) throw Stopped()
                val n = input.read(buf)
                if (n < 0) break
                md.update(buf, 0, n)
                done += n
                UploadState.progress(done)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    companion object {
        /** Bytes per request; a broken connection costs at most this much. */
        const val CHUNK = 16L * 1024 * 1024
        /** Files modified more recently than this are still being written. */
        const val SETTLE_MS = 4000L

        private val running = java.util.concurrent.atomic.AtomicBoolean(false)

        private val junkNames = setOf("thumbs.db", "desktop.ini", ".ds_store", ".nomedia")

        fun isJunk(name: String): Boolean {
            val n = name.lowercase()
            return n.startsWith(".") || n in junkNames || n.startsWith("~$") || n.endsWith(".tmp") ||
                n.endsWith(".part") || n.endsWith(".crdownload") || n.endsWith(".pending")
        }

        fun joinPath(a: String, b: String): String {
            val x = a.trim('/')
            val y = b.trim('/')
            return when {
                x.isEmpty() -> y
                y.isEmpty() -> x
                else -> "$x/$y"
            }
        }

        /** Where the app keeps its own copies until they are uploaded. */
        fun outbox(ctx: Context): File = File(ctx.filesDir, "outbox").apply { mkdirs() }
    }
}
