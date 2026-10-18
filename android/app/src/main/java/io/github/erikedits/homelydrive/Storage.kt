package io.github.erikedits.homelydrive

import android.Manifest
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.storage.StorageManager
import android.provider.MediaStore
import android.provider.Settings
import java.io.File

/** Access to the phone's own files (any folder the user picks). */
object Storage {

    fun hasAccess(ctx: Context): Boolean = if (Build.VERSION.SDK_INT >= 30) {
        Environment.isExternalStorageManager()
    } else {
        ctx.checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
    }

    /** Opens the "all files access" setting (Android 11+) or asks for the storage permission. */
    fun requestAccess(activity: Activity, requestCode: Int) {
        if (Build.VERSION.SDK_INT >= 30) {
            try {
                activity.startActivity(Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:${activity.packageName}")))
            } catch (e: ActivityNotFoundException) {
                activity.startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
            }
        } else {
            activity.requestPermissions(arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE, Manifest.permission.WRITE_EXTERNAL_STORAGE), requestCode)
        }
    }

    /** Internal storage and SD cards / USB drives. */
    fun roots(ctx: Context): List<Pair<String, File>> {
        val out = ArrayList<Pair<String, File>>()
        @Suppress("DEPRECATION")
        out += ctx.getString(R.string.internal_storage) to Environment.getExternalStorageDirectory()
        if (Build.VERSION.SDK_INT >= 30) {
            val sm = ctx.getSystemService(StorageManager::class.java)
            for (v in sm.storageVolumes) {
                val dir = v.directory ?: continue
                if (v.isPrimary || out.any { it.second == dir }) continue
                out += v.getDescription(ctx) to dir
            }
        } else {
            // Older Android: derive the SD card roots from the app's own folders on them.
            for (f in ctx.getExternalFilesDirs(null).drop(1)) {
                val p = f?.absolutePath ?: continue
                val i = p.indexOf("/Android/data/")
                if (i > 0) out += ctx.getString(R.string.sd_card) to File(p.substring(0, i))
            }
        }
        return out
    }

    /** A folder found on the phone, e.g. with photos and videos. */
    data class Found(
        val dir: File,
        val label: String,
        val photos: Int,
        val videos: Int,
        /** Typical user folder (camera, screenshots, messenger media, downloads): suggested ticked. */
        val recommended: Boolean,
    ) {
        fun info(ctx: Context): String {
            val parts = ArrayList<String>()
            if (photos > 0) parts += ctx.resources.getQuantityString(R.plurals.n_photos, photos, photos)
            if (videos > 0) parts += ctx.resources.getQuantityString(R.plurals.n_videos, videos, videos)
            parts += display(ctx, dir.absolutePath)
            return parts.joinToString(" · ")
        }
    }

    /**
     * Folders worth uploading, found automatically like Google Photos does:
     * every folder with photos or videos (from Android's media index), plus
     * downloads and documents. Camera and screenshots come first. Slow on big
     * phones: call it in the background.
     */
    fun suggestedFolders(ctx: Context): List<Found> {
        val counts = HashMap<String, IntArray>()
        val image = MediaStore.Files.FileColumns.MEDIA_TYPE_IMAGE
        val video = MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO
        try {
            @Suppress("DEPRECATION")
            val proj = arrayOf(MediaStore.Files.FileColumns.DATA, MediaStore.Files.FileColumns.MEDIA_TYPE)
            val sel = "${MediaStore.Files.FileColumns.MEDIA_TYPE} IN ($image, $video)"
            ctx.contentResolver.query(MediaStore.Files.getContentUri("external"), proj, sel, null, null)?.use { c ->
                while (c.moveToNext()) {
                    val path = c.getString(0) ?: continue
                    val dir = path.substringBeforeLast('/', "")
                    if (dir.isEmpty() || dir.contains("/.") || dir.contains("/Android/data/")) continue
                    val a = counts.getOrPut(dir) { IntArray(2) }
                    if (c.getInt(1) == image) a[0]++ else a[1]++
                }
            }
        } catch (e: Exception) {
            // no media index access: only the fixed folders below
        }

        @Suppress("DEPRECATION")
        val base = Environment.getExternalStorageDirectory()
        val out = LinkedHashMap<String, Found>()
        for ((dir, n) in counts) {
            val f = File(dir)
            if (!f.isDirectory) continue
            out[dir] = Found(f, labelFor(ctx, f), n[0], n[1], isTypical(base, f))
        }
        for (rel in listOf("Download", "Documents")) {
            val f = File(base, rel)
            if (f.isDirectory && f.absolutePath !in out && f.list()?.any { !it.startsWith(".") } == true) {
                out[f.absolutePath] = Found(f, labelFor(ctx, f), 0, 0, true)
            }
        }
        return out.values.sortedWith(compareBy<Found>({ rank(base, it.dir) }, { -(it.photos + it.videos) }, { it.label.lowercase() }))
    }

    private fun rel(base: File, f: File): String = f.absolutePath.removePrefix(base.absolutePath).trim('/')

    private fun rank(base: File, f: File): Int {
        val r = rel(base, f).lowercase()
        return when {
            r == "dcim/camera" -> 0
            r.endsWith("screenshots") -> 1
            r.startsWith("dcim") -> 2
            r.contains("whatsapp") || r.contains("telegram") || r.contains("signal") -> 3
            r == "download" || r == "documents" -> 4
            r.startsWith("pictures") || r.startsWith("movies") -> 5
            else -> 6
        }
    }

    private fun isTypical(base: File, f: File): Boolean = rank(base, f) <= 5

    private fun labelFor(ctx: Context, f: File): String {
        @Suppress("DEPRECATION")
        val r = rel(Environment.getExternalStorageDirectory(), f).lowercase()
        return when {
            r == "dcim/camera" -> ctx.getString(R.string.quick_camera)
            r.endsWith("screenshots") -> ctx.getString(R.string.quick_screenshots)
            r == "download" -> ctx.getString(R.string.quick_downloads)
            r == "documents" -> ctx.getString(R.string.quick_documents)
            r == "pictures" -> ctx.getString(R.string.quick_pictures)
            r == "movies" -> ctx.getString(R.string.quick_movies)
            else -> f.name
        }
    }

    /** "/storage/emulated/0/DCIM/Camera" -> "Interner Speicher/DCIM/Camera". */
    fun display(ctx: Context, path: String): String {
        for ((label, dir) in roots(ctx)) {
            val p = dir.absolutePath
            if (path == p) return label
            if (path.startsWith("$p/")) return label + path.substring(p.length)
        }
        return path
    }
}
