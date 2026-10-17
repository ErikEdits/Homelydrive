package io.github.erikedits.homelydrive

import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.OpenableColumns
import java.io.File
import java.io.FileNotFoundException

/**
 * Hands a writable file to the camera app for the scanner (like AndroidX
 * FileProvider, but only for the app's capture folder).
 */
class CaptureProvider : ContentProvider() {
    override fun onCreate() = true

    private fun file(uri: Uri): File {
        val name = uri.lastPathSegment ?: throw FileNotFoundException()
        if (name.contains('/') || name.startsWith(".")) throw FileNotFoundException()
        return File(dir(context!!), name)
    }

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor =
        ParcelFileDescriptor.open(file(uri), ParcelFileDescriptor.parseMode(mode))

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor {
        val f = file(uri)
        val c = MatrixCursor(arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE))
        c.addRow(arrayOf(f.name, f.length()))
        return c
    }

    override fun getType(uri: Uri) = "image/jpeg"
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?) = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?) = 0

    companion object {
        fun dir(ctx: Context) = File(ctx.cacheDir, "capture").apply { mkdirs() }

        fun uriFor(ctx: Context, f: File): Uri =
            Uri.Builder().scheme("content").authority(ctx.packageName + ".capture").appendPath(f.name).build()
    }
}
