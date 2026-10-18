package io.github.erikedits.homelydrive

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView

/**
 * Picks a folder on the server (with "new folder"). Phone folders are chosen
 * in [ExplorerActivity].
 */
class FolderPickerActivity : Activity() {
    private lateinit var titleView: TextView
    private lateinit var pathText: TextView
    private lateinit var list: LinearLayout
    private lateinit var progress: ProgressBar
    private lateinit var choose: View
    private lateinit var newFolder: View

    private var roots: List<Root> = emptyList()
    private var space: Root? = null
    private var remotePath = ""

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

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

        loadRoots()
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
        when {
            space == null -> if (!fromBackKey) finish() else return false
            remotePath.isEmpty() -> {
                space = null
                showRoots()
            }
            else -> openRemote(remotePath.substringBeforeLast('/', ""))
        }
        return true
    }

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
        val s = space ?: return
        val data = Intent()
        data.putExtra(EXTRA_SPACE, s.ref)
        data.putExtra(EXTRA_PATH, remotePath)
        data.putExtra(EXTRA_LABEL, Uploader.joinPath(s.name, remotePath))
        data.putExtras(intent.extras ?: Bundle())
        setResult(RESULT_OK, data)
        finish()
    }

    companion object {
        const val EXTRA_TITLE = "title"
        const val EXTRA_PATH = "path"
        const val EXTRA_SPACE = "space"
        const val EXTRA_LABEL = "label"

        fun intent(ctx: Context, title: String) =
            Intent(ctx, FolderPickerActivity::class.java).putExtra(EXTRA_TITLE, title)
    }
}
