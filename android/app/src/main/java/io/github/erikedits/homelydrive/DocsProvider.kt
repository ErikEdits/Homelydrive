package io.github.erikedits.homelydrive

import android.content.Context
import android.content.res.AssetFileDescriptor
import android.database.Cursor
import android.database.MatrixCursor
import android.graphics.Point
import android.os.Bundle
import android.os.CancellationSignal
import android.os.Handler
import android.os.HandlerThread
import android.os.ParcelFileDescriptor
import android.os.ProxyFileDescriptorCallback
import android.os.storage.StorageManager
import android.provider.DocumentsContract
import android.provider.DocumentsContract.Document
import android.provider.DocumentsContract.Root
import android.provider.DocumentsProvider
import android.system.ErrnoException
import android.system.OsConstants
import android.webkit.MimeTypeMap
import java.io.File
import java.io.FileNotFoundException
import java.io.IOException
import java.security.MessageDigest

/**
 * Shows the server's files in the Android "Files" app and in every app's
 * file picker. Files are read straight from the server (no offline copies);
 * saved files are uploaded through the normal upload queue.
 *
 * Document ids: "top" for the list of drives, otherwise "<space>|<path>".
 */
class DocsProvider : DocumentsProvider() {

    private val ctx: Context get() = context!!

    private fun api(): Api {
        val p = ctx.hd.prefs
        if (!p.isLoggedIn) throw FileNotFoundException("not logged in")
        return Api(p.server, p.token)
    }

    override fun onCreate() = true

    // ----- roots -----

    override fun queryRoots(projection: Array<out String>?): Cursor {
        val c = MatrixCursor(projection ?: ROOT_COLUMNS)
        val p = ctx.hd.prefs
        if (!p.isLoggedIn) return c
        c.newRow().apply {
            add(Root.COLUMN_ROOT_ID, ROOT_ID)
            add(Root.COLUMN_DOCUMENT_ID, TOP)
            add(Root.COLUMN_TITLE, ctx.getString(R.string.app_name))
            add(Root.COLUMN_SUMMARY, p.username + " · " + hostOf(p.server))
            add(Root.COLUMN_ICON, R.mipmap.ic_launcher)
            add(Root.COLUMN_FLAGS, Root.FLAG_SUPPORTS_CREATE or Root.FLAG_SUPPORTS_SEARCH or Root.FLAG_SUPPORTS_IS_CHILD)
        }
        return c
    }

    // ----- listing -----

    override fun queryDocument(documentId: String, projection: Array<out String>?): Cursor {
        val c = MatrixCursor(projection ?: DOC_COLUMNS)
        if (documentId == TOP) {
            c.newRow().apply {
                add(Document.COLUMN_DOCUMENT_ID, TOP)
                add(Document.COLUMN_DISPLAY_NAME, ctx.getString(R.string.app_name))
                add(Document.COLUMN_MIME_TYPE, Document.MIME_TYPE_DIR)
                add(Document.COLUMN_FLAGS, 0)
                add(Document.COLUMN_SIZE, null)
            }
            return c
        }
        val (space, path) = split(documentId)
        val f = cached(documentId) ?: try {
            if (path.isEmpty()) {
                val root = api().roots().find { it.ref == space } ?: throw FileNotFoundException(documentId)
                RemoteFile(root.name, "", root.isDir, 0, 0, "", "", root.canWrite)
            } else {
                api().stat(space, path)
            }
        } catch (e: ApiException) {
            throw FileNotFoundException(e.message)
        }
        addRow(c, documentId, f, isSpaceRoot = path.isEmpty())
        return c
    }

    override fun queryChildDocuments(parentDocumentId: String, projection: Array<out String>?, sortOrder: String?): Cursor {
        val c = MatrixCursor(projection ?: DOC_COLUMNS)
        c.setNotificationUri(ctx.contentResolver, DocumentsContract.buildChildDocumentsUri(authority(ctx), parentDocumentId))
        try {
            if (parentDocumentId == TOP) {
                for (r in api().roots()) {
                    val id = "${r.ref}|"
                    val f = RemoteFile(r.name, "", r.isDir, 0, 0, "", "", r.canWrite)
                    remember(id, f)
                    addRow(c, id, f, isSpaceRoot = true)
                }
            } else {
                val (space, path) = split(parentDocumentId)
                for (f in api().list(space, path)) {
                    val id = docId(space, Uploader.joinPath(path, f.name))
                    remember(id, f)
                    addRow(c, id, f, isSpaceRoot = false)
                }
            }
        } catch (e: ApiException) {
            c.extras = Bundle().apply { putString(DocumentsContract.EXTRA_ERROR, ctx.errorText(e)) }
        }
        return c
    }

    override fun querySearchDocuments(rootId: String, query: String, projection: Array<out String>?): Cursor {
        val c = MatrixCursor(projection ?: DOC_COLUMNS)
        try {
            for (f in api().search(query)) {
                if (f.space.isEmpty()) continue
                val id = docId(f.space, f.path)
                remember(id, f)
                addRow(c, id, f, isSpaceRoot = false)
            }
        } catch (e: ApiException) {
            c.extras = Bundle().apply { putString(DocumentsContract.EXTRA_ERROR, ctx.errorText(e)) }
        }
        return c
    }

    override fun isChildDocument(parentDocumentId: String, documentId: String): Boolean {
        if (parentDocumentId == TOP) return documentId != TOP
        if (documentId == TOP) return false
        val (ps, pp) = split(parentDocumentId)
        val (s, p) = split(documentId)
        return ps == s && p != pp && (pp.isEmpty() || p.startsWith("$pp/"))
    }

    private fun addRow(c: MatrixCursor, id: String, f: RemoteFile, isSpaceRoot: Boolean) {
        var flags = 0
        val mime = if (f.isDir) Document.MIME_TYPE_DIR else mimeOf(f)
        if (f.canWrite) {
            flags = if (f.isDir) {
                Document.FLAG_DIR_SUPPORTS_CREATE
            } else {
                Document.FLAG_SUPPORTS_WRITE
            }
            if (!isSpaceRoot) flags = flags or Document.FLAG_SUPPORTS_DELETE or Document.FLAG_SUPPORTS_RENAME
        }
        if (!f.isDir && (mime.startsWith("image/") || mime.startsWith("video/") || mime == "application/pdf")) {
            flags = flags or Document.FLAG_SUPPORTS_THUMBNAIL
        }
        c.newRow().apply {
            add(Document.COLUMN_DOCUMENT_ID, id)
            add(Document.COLUMN_DISPLAY_NAME, f.name)
            add(Document.COLUMN_MIME_TYPE, mime)
            add(Document.COLUMN_FLAGS, flags)
            add(Document.COLUMN_SIZE, if (f.isDir) null else f.size)
            add(Document.COLUMN_LAST_MODIFIED, if (f.mtime > 0) f.mtime else null)
        }
    }

    // ----- reading & writing -----

    override fun openDocument(documentId: String, mode: String, signal: CancellationSignal?): ParcelFileDescriptor {
        val (space, path) = split(documentId)
        if (path.isEmpty() && documentId == TOP) throw FileNotFoundException()
        val api = api()
        val write = mode.contains('w') || mode.contains('a')
        if (!write) {
            val f = try {
                api.stat(space, path)
            } catch (e: ApiException) {
                throw FileNotFoundException(e.message)
            }
            if (f.isDir) throw FileNotFoundException("is a folder")
            // Seekable stream straight from the server (videos and PDFs can jump around).
            val thread = HandlerThread("hd-read").apply { start() }
            val sm = ctx.getSystemService(StorageManager::class.java)
            return sm.openProxyFileDescriptor(ParcelFileDescriptor.MODE_READ_ONLY, RemoteReader(api, space, path, f.size, thread), Handler(thread.looper))
        }

        // Writing: work on a local copy and upload it when the app closes the file.
        val dir = File(Uploader.outbox(ctx), "docs-" + System.nanoTime()).apply { mkdirs() }
        val tmp = File(dir, path.substringAfterLast('/').ifEmpty { "file" })
        if (!mode.contains('t') && (mode.contains('r') || mode.contains('a'))) {
            try {
                val (input, _) = api.download(space, path)
                input.use { i -> tmp.outputStream().use { o -> i.copyTo(o) } }
            } catch (e: ApiException) {
                if (e.status != 404) throw FileNotFoundException(e.message)
            }
        }
        val thread = HandlerThread("hd-write").apply { start() }
        return ParcelFileDescriptor.open(tmp, ParcelFileDescriptor.parseMode(mode), Handler(thread.looper)) { err ->
            thread.quitSafely()
            if (err == null) {
                Uploader(ctx).enqueueFile("docs", tmp, space, path, conflict = "overwrite", temp = true)
                UploadService.start(ctx)
                forget(documentId)
            } else {
                tmp.delete()
            }
        }
    }

    override fun openDocumentThumbnail(documentId: String, sizeHint: Point?, signal: CancellationSignal?): AssetFileDescriptor {
        val (space, path) = split(documentId)
        val size = if ((sizeHint?.x ?: 256) > 256) 512 else 256
        val key = MessageDigest.getInstance("SHA-1").digest("$documentId|$size|${cached(documentId)?.mtime}".toByteArray())
            .joinToString("") { "%02x".format(it) }
        val dir = File(ctx.cacheDir, "thumbs").apply { mkdirs() }
        val f = File(dir, "$key.jpg")
        if (!f.exists()) {
            try {
                api().thumb(space, path, size, f)
            } catch (e: ApiException) {
                throw FileNotFoundException(e.message)
            }
            trimThumbs(dir)
        }
        val pfd = ParcelFileDescriptor.open(f, ParcelFileDescriptor.MODE_READ_ONLY)
        return AssetFileDescriptor(pfd, 0, AssetFileDescriptor.UNKNOWN_LENGTH)
    }

    private fun trimThumbs(dir: File) {
        val files = dir.listFiles() ?: return
        if (files.size < 600) return
        files.sortedBy { it.lastModified() }.take(files.size - 500).forEach { it.delete() }
    }

    // ----- changes -----

    override fun createDocument(parentDocumentId: String, mimeType: String, displayName: String): String {
        if (parentDocumentId == TOP) throw FileNotFoundException("choose a drive first")
        val (space, parent) = split(parentDocumentId)
        val api = api()
        val existing = try {
            api.list(space, parent).map { it.name.lowercase() }.toSet()
        } catch (e: ApiException) {
            throw FileNotFoundException(e.message)
        }
        val name = uniqueName(displayName, existing)
        val path = Uploader.joinPath(parent, name)
        try {
            if (mimeType == Document.MIME_TYPE_DIR) api.mkdir(space, path) else api.createEmpty(space, path)
        } catch (e: ApiException) {
            throw FileNotFoundException(e.message)
        }
        changed(parentDocumentId)
        return docId(space, path)
    }

    override fun deleteDocument(documentId: String) {
        val (space, path) = split(documentId)
        if (path.isEmpty()) throw FileNotFoundException("cannot delete a drive")
        try {
            api().delete(space, path)
        } catch (e: ApiException) {
            throw FileNotFoundException(e.message)
        }
        forget(documentId)
        changed(docId(space, path.substringBeforeLast('/', "")))
    }

    override fun renameDocument(documentId: String, displayName: String): String {
        val (space, path) = split(documentId)
        if (path.isEmpty()) throw FileNotFoundException("cannot rename a drive")
        try {
            api().rename(space, path, displayName)
        } catch (e: ApiException) {
            throw FileNotFoundException(e.message)
        }
        forget(documentId)
        val parent = path.substringBeforeLast('/', "")
        changed(docId(space, parent))
        return docId(space, Uploader.joinPath(parent, displayName))
    }

    private fun changed(parentId: String) {
        ctx.contentResolver.notifyChange(DocumentsContract.buildChildDocumentsUri(authority(ctx), parentId), null)
    }

    /**
     * Serves reads of a remote file with HTTP range requests and a small
     * block cache, so apps can seek in large files without a full download.
     */
    private class RemoteReader(
        private val api: Api,
        private val space: String,
        private val path: String,
        private val fileSize: Long,
        private val thread: HandlerThread,
    ) : ProxyFileDescriptorCallback() {
        private val blocks = object : LinkedHashMap<Long, ByteArray>(16, 0.75f, true) {
            override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Long, ByteArray>?) = size > MAX_BLOCKS
        }
        private var lastBlock = -1L

        override fun onGetSize() = fileSize

        override fun onRead(offset: Long, length: Int, data: ByteArray): Int {
            if (offset >= fileSize) return 0
            var done = 0
            while (done < length && offset + done < fileSize) {
                val pos = offset + done
                val idx = pos / BLOCK
                val b = block(idx)
                val inBlock = (pos - idx * BLOCK).toInt()
                val n = minOf(length - done, b.size - inBlock)
                if (n <= 0) break
                System.arraycopy(b, inBlock, data, done, n)
                done += n
            }
            return done
        }

        private fun block(idx: Long): ByteArray {
            blocks[idx]?.let { return it }
            // Sequential reading: fetch a few blocks at once.
            val count = if (idx == lastBlock + 1) READ_AHEAD else 1
            lastBlock = idx + count - 1
            val start = idx * BLOCK
            val end = minOf(fileSize, start + count * BLOCK)
            val buf = ByteArray((end - start).toInt())
            try {
                val (input, _) = api.download(space, path, start, buf.size.toLong())
                input.use {
                    var got = 0
                    while (got < buf.size) {
                        val n = it.read(buf, got, buf.size - got)
                        if (n < 0) break
                        got += n
                    }
                }
            } catch (e: IOException) {
                throw ErrnoException("read", OsConstants.EIO)
            }
            for (i in 0 until count) {
                val s = (i * BLOCK).toInt()
                if (s >= buf.size) break
                blocks[idx + i] = buf.copyOfRange(s, minOf(buf.size, s + BLOCK.toInt()))
            }
            return blocks[idx] ?: ByteArray(0)
        }

        override fun onRelease() {
            blocks.clear()
            thread.quitSafely()
        }

        companion object {
            const val BLOCK = 1024L * 1024
            const val READ_AHEAD = 4
            const val MAX_BLOCKS = 16
        }
    }

    companion object {
        private const val ROOT_ID = "homelydrive"
        private const val TOP = "top"
        private const val CACHE_MS = 30_000L

        private val ROOT_COLUMNS = arrayOf(
            Root.COLUMN_ROOT_ID, Root.COLUMN_DOCUMENT_ID, Root.COLUMN_TITLE, Root.COLUMN_SUMMARY, Root.COLUMN_ICON, Root.COLUMN_FLAGS,
        )
        private val DOC_COLUMNS = arrayOf(
            Document.COLUMN_DOCUMENT_ID, Document.COLUMN_DISPLAY_NAME, Document.COLUMN_MIME_TYPE, Document.COLUMN_FLAGS,
            Document.COLUMN_SIZE, Document.COLUMN_LAST_MODIFIED,
        )

        /** Recently listed files, so the Files app's many single lookups need no request. */
        private val cache = object : LinkedHashMap<String, Pair<RemoteFile, Long>>(256, 0.75f, true) {
            override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Pair<RemoteFile, Long>>?) = size > 2000
        }

        @Synchronized
        private fun remember(id: String, f: RemoteFile) {
            cache[id] = f to System.currentTimeMillis()
        }

        @Synchronized
        private fun cached(id: String): RemoteFile? =
            cache[id]?.takeIf { System.currentTimeMillis() - it.second < CACHE_MS }?.first

        @Synchronized
        private fun forget(id: String) {
            cache.remove(id)
        }

        fun authority(ctx: Context) = ctx.packageName + ".documents"

        fun docId(space: String, path: String) = "$space|$path"

        fun split(id: String): Pair<String, String> {
            val i = id.indexOf('|')
            if (i < 0) throw FileNotFoundException(id)
            return id.substring(0, i) to id.substring(i + 1)
        }

        private fun hostOf(server: String) = server.substringAfter("://").substringBefore(':').substringBefore('/')

        fun mimeOf(f: RemoteFile): String {
            if (f.mime.isNotEmpty() && f.mime != "application/octet-stream") return f.mime.substringBefore(';')
            val ext = f.name.substringAfterLast('.', "").lowercase()
            return MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext) ?: "application/octet-stream"
        }

        fun uniqueName(name: String, taken: Set<String>): String {
            if (name.lowercase() !in taken) return name
            val dot = name.lastIndexOf('.')
            val base = if (dot > 0) name.substring(0, dot) else name
            val ext = if (dot > 0) name.substring(dot) else ""
            var i = 1
            while (true) {
                val n = "$base ($i)$ext"
                if (n.lowercase() !in taken) return n
                i++
            }
        }

        /** Tells the Files app that the logged-in account changed. */
        fun rootsChanged(ctx: Context) {
            ctx.contentResolver.notifyChange(DocumentsContract.buildRootsUri(authority(ctx)), null)
        }
    }
}
