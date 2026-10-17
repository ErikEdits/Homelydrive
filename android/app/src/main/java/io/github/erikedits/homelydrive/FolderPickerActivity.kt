package io.github.erikedits.homelydrive

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.storage.StorageManager
import android.view.Gravity
import android.view.View
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView
import java.io.File

/**
 * Picks a folder: either any folder on the phone (MODE_LOCAL) or a folder on
 * the server (MODE_REMOTE, with "new folder").
 */
class FolderPickerActivity : Activity() {
    private lateinit var titleView: TextView
    private lateinit var pathText: TextView
    private lateinit var list: LinearLayout
    private lateinit var progress: ProgressBar
    private lateinit var choose: View
    private lateinit var newFolder: View

    private var remote = false

    // local mode
    private var localDir: File? = null

    // remote mode
    private var roots: List<Root> = emptyList()
    private var space: Root? = null
    private var remotePath = ""

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        remote = intent.getStringExtra(EXTRA_MODE) == MODE_REMOTE

        val root = vertical(this) {
            setBackgroundColor(col(R.color.bg))
        }
        val bar = horizontal(this) {
            setPadding(dp(4), dp(4), dp(12), dp(4))
            addView(iconButton(context, R.drawable.ic_back, getString(R.string.back)) { goUp() })
            titleView = text(context, intent.getStringExtra(EXTRA_TITLE) ?: getString(R.string.pick_folder), 19f, true, lines = 1)
            addView(titleView, lp(0, WRAP, 1f).margins(context, l = 8))
        }
        root.addView(bar)
        pathText = text(this, "", 13f, color = R.color.text2, lines = 2).apply {
            setPadding(dp(16), 0, dp(16), dp(8))
        }
        root.addView(pathText)
        root.addView(divider(this))

        val frame = FrameLayout(this)
        list = vertical(this) { setPadding(0, dp(4), 0, dp(4)) }
        frame.addView(ScrollView(this).apply { addView(list) })
        progress = ProgressBar(this).apply { visibility = View.GONE }
        frame.addView(progress, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.CENTER))
        root.addView(frame, lp(MATCH, 0, 1f))

        root.addView(divider(this))
        val actions = horizontal(this, 12) {
            gravity = Gravity.END or Gravity.CENTER_VERTICAL
            newFolder = button(context, getString(R.string.new_folder), primary = false) { askNewFolder() }
            addView(newFolder, lp(WRAP, WRAP).margins(context, r = 8))
            choose = button(context, getString(R.string.choose_this_folder)) { finishWithResult() }
            addView(choose, lp(WRAP, WRAP))
        }
        root.addView(actions)

        applyInsets(root)
        setContentView(root)

        if (remote) loadRoots() else showLocalRoots()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (!goUp(fromBackKey = true)) {
            @Suppress("DEPRECATION")
            super.onBackPressed()
        }
    }

    /** Goes one level up; returns false at the top. */
    private fun goUp(fromBackKey: Boolean = false): Boolean {
        if (remote) {
            when {
                space == null -> if (!fromBackKey) finish() else return false
                remotePath.isEmpty() -> {
                    space = null
                    showRoots()
                }
                else -> openRemote(remotePath.substringBeforeLast('/', ""))
            }
        } else {
            val d = localDir
            when {
                d == null -> if (!fromBackKey) finish() else return false
                storageRoots(this).any { it.second == d } -> showLocalRoots()
                else -> openLocal(d.parentFile ?: return false)
            }
        }
        return true
    }

    // ----- phone folders -----

    private fun showLocalRoots() {
        localDir = null
        pathText.text = getString(R.string.pick_local_hint)
        choose.isEnabled = false
        choose.alpha = 0.5f
        newFolder.visibility = View.GONE
        list.removeAllViews()
        for ((label, dir) in storageRoots(this)) {
            list.addView(row(this, R.drawable.ic_storage, label, dir.absolutePath) { openLocal(dir) })
        }
    }

    private fun openLocal(dir: File) {
        localDir = dir
        pathText.text = displayLocal(this, dir.absolutePath)
        choose.isEnabled = true
        choose.alpha = 1f
        newFolder.visibility = View.GONE
        list.removeAllViews()
        val dirs = dir.listFiles { f -> f.isDirectory && !f.name.startsWith(".") }?.sortedBy { it.name.lowercase() } ?: emptyList()
        if (dirs.isEmpty()) list.addView(empty(getString(R.string.no_subfolders)))
        for (d in dirs) {
            val n = d.listFiles()?.size ?: 0
            list.addView(row(this, R.drawable.ic_folder, d.name, resources.getQuantityString(R.plurals.entries, n, n)) { openLocal(d) })
        }
    }

    // ----- server folders -----

    private fun loadRoots() {
        busy(true)
        async({ api().roots().filter { it.canWrite && it.isDir } }) { r ->
            busy(false)
            r.onSuccess {
                roots = it
                showRoots()
            }.onFailure { showError(it) }
        }
    }

    private fun showRoots() {
        remotePath = ""
        pathText.text = getString(R.string.pick_remote_hint)
        choose.isEnabled = false
        choose.alpha = 0.5f
        newFolder.visibility = View.GONE
        list.removeAllViews()
        for (r in roots) {
            val sub = if (r.owner.isNotEmpty()) getString(R.string.shared_by, r.owner) else null
            list.addView(row(this, if (r.kind == "home") R.drawable.ic_storage else R.drawable.ic_folder, r.name, sub) {
                space = r
                openRemote("")
            })
        }
    }

    private fun openRemote(path: String) {
        val s = space ?: return
        busy(true)
        async({ api().list(s.ref, path).filter { it.isDir } }) { r ->
            busy(false)
            r.onSuccess { dirs ->
                remotePath = path
                pathText.text = Uploader.joinPath(s.name, path)
                choose.isEnabled = true
                choose.alpha = 1f
                newFolder.visibility = View.VISIBLE
                list.removeAllViews()
                if (dirs.isEmpty()) list.addView(empty(getString(R.string.no_subfolders)))
                for (d in dirs.sortedBy { it.name.lowercase() }) {
                    list.addView(row(this, R.drawable.ic_folder, d.name) { openRemote(Uploader.joinPath(path, d.name)) })
                }
            }.onFailure { showError(it) }
        }
    }

    private fun askNewFolder() {
        val s = space ?: return
        val input = EditText(this).apply {
            hint = getString(R.string.folder_name)
            setSingleLine()
        }
        val box = FrameLayout(this).apply {
            setPadding(dp(20), dp(8), dp(20), 0)
            addView(input)
        }
        AlertDialog.Builder(this)
            .setTitle(R.string.new_folder)
            .setView(box)
            .setPositiveButton(R.string.create) { _, _ ->
                val name = input.text.toString().trim()
                if (name.isEmpty()) return@setPositiveButton
                val path = Uploader.joinPath(remotePath, name)
                async({ api().ensureDir(s.ref, path) }) { r ->
                    r.onSuccess { openRemote(path) }.onFailure { showError(it) }
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    // ----- helpers -----

    private fun api() = Api(hd.prefs.server, hd.prefs.token)

    private fun busy(b: Boolean) {
        progress.visibility = if (b) View.VISIBLE else View.GONE
        list.alpha = if (b) 0.4f else 1f
    }

    private fun empty(s: String) = text(this, s, 14f, color = R.color.text2).apply {
        gravity = Gravity.CENTER
        setPadding(dp(24), dp(32), dp(24), dp(32))
    }

    private fun showError(e: Throwable) {
        list.removeAllViews()
        list.addView(empty(errorText(e)))
        list.addView(button(this, getString(R.string.retry), primary = false) {
            if (space == null) loadRoots() else openRemote(remotePath)
        }, lp(WRAP, WRAP).margins(this, l = 24))
    }

    private fun finishWithResult() {
        val data = Intent()
        if (remote) {
            val s = space ?: return
            data.putExtra(EXTRA_SPACE, s.ref)
            data.putExtra(EXTRA_PATH, remotePath)
            data.putExtra(EXTRA_LABEL, Uploader.joinPath(s.name, remotePath))
        } else {
            val d = localDir ?: return
            data.putExtra(EXTRA_PATH, d.absolutePath)
            data.putExtra(EXTRA_LABEL, displayLocal(this, d.absolutePath))
        }
        data.putExtras(intent.extras ?: Bundle())
        setResult(RESULT_OK, data)
        finish()
    }

    companion object {
        const val EXTRA_MODE = "mode"
        const val EXTRA_TITLE = "title"
        const val EXTRA_PATH = "path"
        const val EXTRA_SPACE = "space"
        const val EXTRA_LABEL = "label"
        const val MODE_LOCAL = "local"
        const val MODE_REMOTE = "remote"

        fun intent(ctx: Context, mode: String, title: String) =
            Intent(ctx, FolderPickerActivity::class.java).putExtra(EXTRA_MODE, mode).putExtra(EXTRA_TITLE, title)

        /** Internal storage and SD cards / USB drives. */
        fun storageRoots(ctx: Context): List<Pair<String, File>> {
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

        /** "/storage/emulated/0/DCIM/Camera" -> "Interner Speicher/DCIM/Camera". */
        fun displayLocal(ctx: Context, path: String): String {
            for ((label, dir) in storageRoots(ctx)) {
                val p = dir.absolutePath
                if (path == p) return label
                if (path.startsWith("$p/")) return label + path.substring(p.length)
            }
            return path
        }
    }
}
