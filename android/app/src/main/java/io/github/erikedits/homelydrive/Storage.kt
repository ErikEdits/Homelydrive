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

    /** Often used folders that exist on this phone (camera, downloads, messenger media, ...). */
    fun quickFolders(ctx: Context): List<Pair<String, File>> {
        @Suppress("DEPRECATION")
        val base = Environment.getExternalStorageDirectory()
        val candidates = listOf(
            R.string.quick_camera to "DCIM/Camera",
            R.string.quick_screenshots to "Pictures/Screenshots",
            R.string.quick_screenshots to "DCIM/Screenshots",
            R.string.quick_pictures to "Pictures",
            R.string.quick_downloads to "Download",
            R.string.quick_documents to "Documents",
            R.string.quick_whatsapp to "Android/media/com.whatsapp/WhatsApp/Media",
            R.string.quick_whatsapp to "WhatsApp/Media",
            R.string.quick_telegram to "Pictures/Telegram",
            R.string.quick_music to "Music",
            R.string.quick_movies to "Movies",
        )
        val out = ArrayList<Pair<String, File>>()
        for ((label, rel) in candidates) {
            val f = File(base, rel)
            val name = ctx.getString(label)
            if (f.isDirectory && out.none { it.first == name }) out += name to f
        }
        return out
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
