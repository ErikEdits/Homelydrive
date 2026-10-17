package io.github.erikedits.homelydrive

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.OpenableColumns
import android.view.Gravity
import android.view.View
import android.widget.ProgressBar
import android.widget.TextView
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * "Share" target: other apps can send files (or text) to Homelydrive. The
 * files are copied into the app first, so the upload also works when the
 * other app is closed, and then uploaded by the upload service.
 */
class ShareActivity : Activity() {
    private var uris: List<Uri> = emptyList()
    private var sharedText: String? = null
    private var space = "home"
    private var target = ""
    private lateinit var targetText: TextView
    private lateinit var progress: ProgressBar
    private lateinit var uploadButton: View

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val p = hd.prefs
        if (!p.isLoggedIn) {
            toast(getString(R.string.login_first))
            startActivity(Intent(this, LoginActivity::class.java))
            finish()
            return
        }
        space = p.shareSpace
        target = p.shareTarget
        readIntent(intent)
        if (uris.isEmpty() && sharedText.isNullOrEmpty()) {
            finish()
            return
        }

        val root = vertical(this, 20) {
            background = rounded(col(R.color.surface), dp(24).toFloat())
            minimumWidth = dp(300)
        }
        root.addView(horizontal(this) {
            addView(icon(context, R.drawable.ic_upload, 28, R.color.brand))
            addView(text(context, getString(R.string.share_title), 19f, true), lp(0, WRAP, 1f).margins(context, l = 12))
        })
        val what = if (uris.isNotEmpty()) {
            resources.getQuantityString(R.plurals.files, uris.size, uris.size)
        } else {
            getString(R.string.share_text_note)
        }
        root.addView(text(this, what, 15f, color = R.color.text2).apply { setPadding(0, dp(12), 0, dp(4)) })

        root.addView(heading(this, getString(R.string.target_folder)))
        targetText = text(this, "", 15f, lines = 2)
        root.addView(row(this, R.drawable.ic_folder, getString(R.string.target_folder), null) { pickTarget() }.also { r ->
            // show the chosen folder as the row's title
            r.removeViewAt(1)
            r.addView(targetText, lp(0, WRAP, 1f).margins(this, l = 16))
            r.addView(icon(this, R.drawable.ic_chevron, 20))
            r.background = rounded(col(R.color.surface2), dp(12).toFloat())
        })
        updateTarget()

        progress = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
            isIndeterminate = true
            visibility = View.GONE
        }
        root.addView(progress, lp().margins(this, t = 12))

        root.addView(horizontal(this) {
            gravity = Gravity.END or Gravity.CENTER_VERTICAL
            setPadding(0, dp(16), 0, 0)
            addView(button(context, getString(android.R.string.cancel), primary = false) { finish() }, lp(WRAP, WRAP).margins(context, r = 8))
            uploadButton = button(context, getString(R.string.upload)) { start() }
            addView(uploadButton, lp(WRAP, WRAP))
        })
        setContentView(root)
        window.setLayout(resources.displayMetrics.widthPixels * 9 / 10, WRAP)
    }

    private fun readIntent(i: Intent) {
        val list = ArrayList<Uri>()
        when (i.action) {
            Intent.ACTION_SEND -> {
                @Suppress("DEPRECATION")
                (i.getParcelableExtra<Uri>(Intent.EXTRA_STREAM))?.let { list += it }
            }
            Intent.ACTION_SEND_MULTIPLE -> {
                @Suppress("DEPRECATION")
                i.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM)?.let { list += it }
            }
        }
        if (list.isEmpty()) {
            i.clipData?.let { cd -> for (k in 0 until cd.itemCount) cd.getItemAt(k).uri?.let { list += it } }
        }
        uris = list
        if (list.isEmpty()) sharedText = i.getStringExtra(Intent.EXTRA_TEXT)
    }

    private fun updateTarget() {
        val label = if (space == "home") Uploader.joinPath(getString(R.string.my_drive), target) else Uploader.joinPath(spaceLabel, target)
        targetText.text = label
    }

    private var spaceLabel = ""

    private fun pickTarget() {
        @Suppress("DEPRECATION")
        startActivityForResult(FolderPickerActivity.intent(this, FolderPickerActivity.MODE_REMOTE, getString(R.string.target_folder)), REQ_TARGET)
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == REQ_TARGET && resultCode == RESULT_OK && data != null) {
            space = data.getStringExtra(FolderPickerActivity.EXTRA_SPACE) ?: "home"
            target = data.getStringExtra(FolderPickerActivity.EXTRA_PATH) ?: ""
            val label = data.getStringExtra(FolderPickerActivity.EXTRA_LABEL) ?: target
            spaceLabel = label.removeSuffix(target).trimEnd('/')
            hd.prefs.shareSpace = space
            hd.prefs.shareTarget = target
            targetText.text = label
        }
    }

    private fun start() {
        uploadButton.isEnabled = false
        progress.visibility = View.VISIBLE
        val sp = space
        val tg = target
        async({ copyAll(sp, tg) }) { r ->
            r.onSuccess { n ->
                UploadService.start(this)
                toast(resources.getQuantityString(R.plurals.share_queued, n, n))
                finish()
            }.onFailure {
                progress.visibility = View.GONE
                uploadButton.isEnabled = true
                toast(errorText(it))
            }
        }
    }

    /** Copies the shared files into the outbox and queues them. */
    private fun copyAll(space: String, target: String): Int {
        val up = Uploader(this)
        val dir = File(Uploader.outbox(this), "share-" + System.currentTimeMillis()).apply { mkdirs() }
        var n = 0
        val text = sharedText
        if (uris.isEmpty() && text != null) {
            val name = getString(R.string.note_prefix) + " " + SimpleDateFormat("yyyy-MM-dd HH-mm-ss", Locale.ROOT).format(Date()) + ".txt"
            val f = File(dir, name)
            f.writeText(text)
            up.enqueueFile("share", f, space, Uploader.joinPath(target, name))
            return 1
        }
        val used = HashSet<String>()
        for (uri in uris) {
            var name = displayName(uri)
            name = DocsProvider.uniqueName(name, used)
            used += name.lowercase()
            val f = File(dir, name)
            contentResolver.openInputStream(uri)?.use { input -> f.outputStream().use { input.copyTo(it, 256 * 1024) } } ?: continue
            up.enqueueFile("share", f, space, Uploader.joinPath(target, name))
            n++
        }
        return n
    }

    private fun displayName(uri: Uri): String {
        var name: String? = null
        try {
            contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
                if (c.moveToFirst() && !c.isNull(0)) name = c.getString(0)
            }
        } catch (e: Exception) {
            // some apps share plain file:// URIs
        }
        val fallback = getString(R.string.file_fallback)
        val n = (name ?: uri.lastPathSegment ?: fallback).substringAfterLast('/')
            .replace(Regex("[\\\\/:*?\"<>|\\x00-\\x1f]"), "_").trim()
        if (n.isNotEmpty() && n.contains('.')) return n
        val ext = contentResolver.getType(uri)?.let { android.webkit.MimeTypeMap.getSingleton().getExtensionFromMimeType(it) }
        return (n.ifEmpty { fallback }) + if (ext != null) ".$ext" else ""
    }

    companion object {
        private const val REQ_TARGET = 1
    }
}
