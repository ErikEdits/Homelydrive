package io.github.erikedits.homelydrive

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.res.ColorStateList
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.ThumbnailUtils
import android.os.Build
import android.os.Bundle
import android.util.LruCache
import android.util.Size
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ListView
import android.widget.ProgressBar
import android.widget.TextView
import java.io.File
import java.util.concurrent.Executors

/**
 * The app's own file explorer for the phone. Tap opens a folder, a long press
 * starts selecting; then taps add or remove items, also across folders.
 *
 *  - MODE_UPLOAD: upload the chosen files and folders once to a server folder.
 *  - MODE_FOLDERS: choose one or more folders that are uploaded automatically.
 */
class ExplorerActivity : Activity() {
    /** One row: a file or folder, or a shortcut on the start screen. */
    private class Entry(val file: File, val label: String? = null, val isRoot: Boolean = false, val info: String? = null) {
        val isDir: Boolean = file.isDirectory
        val name: String get() = label ?: file.name
    }

    private var mode = MODE_UPLOAD
    /** The folder shown, or null for the start screen. */
    private var dir: File? = null
    private val selected = LinkedHashSet<String>()
    private var entries: List<Entry> = emptyList()
    private var sortByDate = false
    private var loadToken = 0
    private var needsLoad = true
    private val thumbsLoading = HashSet<String>()

    private var space = "home"
    private var target = ""
    private var targetLabel = ""

    private lateinit var listView: ListView
    private lateinit var adapter: EntryAdapter
    private lateinit var titleView: TextView
    private lateinit var pathView: TextView
    private lateinit var emptyView: TextView
    private lateinit var progress: ProgressBar
    private lateinit var countView: TextView
    private lateinit var selectionRow: LinearLayout
    private lateinit var hintView: TextView
    private lateinit var targetRow: LinearLayout
    private lateinit var targetView: TextView
    private lateinit var actionButton: android.widget.Button
    private lateinit var permissionBox: LinearLayout

    private val thumbPool = Executors.newFixedThreadPool(2)
    private val thumbs = object : LruCache<String, Bitmap>(8 * 1024 * 1024) {
        override fun sizeOf(key: String, value: Bitmap) = value.byteCount
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (!hd.prefs.isLoggedIn) {
            finish()
            return
        }
        mode = intent.getStringExtra(EXTRA_MODE) ?: MODE_UPLOAD
        space = intent.getStringExtra(EXTRA_SPACE) ?: hd.prefs.manualSpace
        target = intent.getStringExtra(EXTRA_PATH) ?: hd.prefs.manualTarget
        targetLabel = intent.getStringExtra(EXTRA_LABEL)
            ?: hd.prefs.manualLabel.takeIf { it.isNotEmpty() && !intent.hasExtra(EXTRA_SPACE) }
            ?: defaultLabel(space, target)

        val root = vertical(this) { setBackgroundColor(col(R.color.bg)) }

        // top bar
        root.addView(horizontal(this) {
            setPadding(dp(4), dp(4), dp(4), dp(4))
            addView(iconButton(context, R.drawable.ic_back, getString(R.string.back)) { goUp(fromBackKey = false) })
            addView(vertical(context) {
                titleView = text(context, "", 19f, true, lines = 1)
                addView(titleView)
                pathView = text(context, "", 12f, color = R.color.text2, lines = 1)
                addView(pathView)
            }, lp(0, WRAP, 1f).margins(context, l = 4))
            if (intent.getBooleanExtra(EXTRA_SYSTEM_PICKER, false)) {
                addView(text(context, getString(R.string.system_picker), 14f, true, R.color.brand).apply {
                    setPadding(dp(10), dp(10), dp(10), dp(10))
                    background = selectable(context)
                    setOnClickListener {
                        setResult(RESULT_SYSTEM_PICKER)
                        finish()
                    }
                })
            }
            addView(iconButton(context, R.drawable.ic_sort, getString(R.string.sort)) { toggleSort() })
        })
        root.addView(divider(this))

        // list
        val frame = FrameLayout(this)
        adapter = EntryAdapter()
        listView = ListView(this).apply {
            this.adapter = this@ExplorerActivity.adapter
            divider = null
            isFastScrollEnabled = true
            setOnItemClickListener { _, _, pos, _ -> onTap(entries[pos]) }
            setOnItemLongClickListener { _, _, pos, _ ->
                toggle(entries[pos])
                true
            }
        }
        frame.addView(listView, FrameLayout.LayoutParams(MATCH, MATCH))
        emptyView = text(this, getString(R.string.empty_folder), 15f, color = R.color.text2).apply {
            gravity = Gravity.CENTER
            visibility = View.GONE
        }
        frame.addView(emptyView, FrameLayout.LayoutParams(MATCH, MATCH))
        progress = ProgressBar(this).apply { visibility = View.GONE }
        frame.addView(progress, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.CENTER))
        permissionBox = vertical(this, 32) {
            gravity = Gravity.CENTER
            setBackgroundColor(col(R.color.bg))
            visibility = View.GONE
            addView(icon(context, R.drawable.ic_storage, 48, R.color.text2))
            addView(text(context, getString(R.string.storage_access_title), 18f, true).apply { gravity = Gravity.CENTER }, lp().margins(context, t = 12))
            addView(text(context, getString(R.string.storage_access_text), 14f, color = R.color.text2).apply { gravity = Gravity.CENTER },
                lp().margins(context, t = 6, b = 16))
            addView(button(context, getString(R.string.allow)) { Storage.requestAccess(this@ExplorerActivity, REQ_STORAGE) }, lp(WRAP, WRAP))
        }
        frame.addView(permissionBox, FrameLayout.LayoutParams(MATCH, MATCH))
        root.addView(frame, lp(MATCH, 0, 1f))

        // bottom bar
        root.addView(divider(this))
        root.addView(vertical(this, 12) {
            setBackgroundColor(col(R.color.surface))
            hintView = text(context, getString(if (mode == MODE_FOLDERS) R.string.explorer_hint_folders else R.string.explorer_hint), 13f,
                color = R.color.text2).apply { setPadding(dp(4), 0, dp(4), dp(8)) }
            addView(hintView)
            selectionRow = horizontal(context) {
                setPadding(dp(4), 0, 0, dp(6))
                countView = text(context, "", 15f, true)
                addView(countView, lp(0, WRAP, 1f))
                addView(text(context, getString(R.string.select_all), 14f, true, R.color.brand).apply {
                    setPadding(dp(10), dp(8), dp(10), dp(8))
                    background = selectable(context)
                    setOnClickListener { selectAll() }
                })
                addView(iconButton(context, R.drawable.ic_close, getString(R.string.clear_selection)) {
                    selected.clear()
                    selectionChanged()
                })
            }
            addView(selectionRow)
            targetRow = horizontal(context) {
                setPadding(dp(4), dp(6), dp(4), dp(10))
                addView(icon(context, R.drawable.ic_upload, 20, R.color.brand))
                targetView = text(context, "", 14f, lines = 2)
                addView(targetView, lp(0, WRAP, 1f).margins(context, l = 10))
                addView(text(context, getString(R.string.change), 14f, true, R.color.brand))
                background = selectable(context)
                setOnClickListener {
                    @Suppress("DEPRECATION")
                    startActivityForResult(FolderPickerActivity.intent(this@ExplorerActivity, getString(R.string.target_folder)), REQ_TARGET)
                }
                visibility = if (mode == MODE_UPLOAD) View.VISIBLE else View.GONE
            }
            addView(targetRow)
            actionButton = button(context, "") { runAction() }
            addView(actionButton, lp())
        })

        applyInsets(root)
        setContentView(root)
        targetView.text = getString(R.string.upload_to, targetLabel)
        savedInstanceState?.getStringArrayList("selected")?.let { selected += it }
        savedInstanceState?.getString("dir")?.let { dir = File(it) }
        selectionChanged()
    }

    override fun onResume() {
        super.onResume()
        if (Storage.hasAccess(this)) {
            permissionBox.visibility = View.GONE
            if (needsLoad) {
                needsLoad = false
                load()
            }
        } else {
            permissionBox.visibility = View.VISIBLE
            needsLoad = true
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putStringArrayList("selected", ArrayList(selected))
        dir?.let { outState.putString("dir", it.absolutePath) }
    }

    override fun onDestroy() {
        thumbPool.shutdownNow()
        super.onDestroy()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (!goUp(fromBackKey = true)) {
            @Suppress("DEPRECATION")
            super.onBackPressed()
        }
    }

    // ----- navigation -----

    /** One level up; false at the start screen. */
    private fun goUp(fromBackKey: Boolean): Boolean {
        val d = dir
        if (d == null) {
            if (!fromBackKey) finish()
            return !fromBackKey
        }
        dir = if (Storage.roots(this).any { it.second == d }) null else d.parentFile
        load()
        return true
    }

    private fun open(f: File) {
        dir = f
        load()
    }

    private fun load() {
        val d = dir
        val token = ++loadToken
        if (d == null) {
            titleView.text = getString(R.string.explorer_start)
            pathView.text = getString(if (mode == MODE_FOLDERS) R.string.explorer_title_folders else R.string.explorer_title_upload)
            val list = ArrayList<Entry>()
            Storage.roots(this).forEach { (label, f) -> list += Entry(f, label, isRoot = true) }
            show(list)
            // Folders with photos/videos (like Google Photos) and downloads, found automatically.
            async({ Storage.suggestedFolders(this).map { Entry(it.dir, it.label, info = it.info(this)) } }) { r ->
                if (token != loadToken) return@async
                show(list + r.getOrDefault(emptyList()))
            }
            return
        }
        titleView.text = Storage.roots(this).find { it.second == d }?.first ?: d.name
        pathView.text = Storage.display(this, d.absolutePath)
        progress.visibility = View.VISIBLE
        val byDate = sortByDate
        async({
            val files = d.listFiles()?.filter { !it.name.startsWith(".") } ?: emptyList()
            val dirs = files.filter { it.isDirectory }
            val plain = files.filter { !it.isDirectory }
            val cmp: Comparator<File> = if (byDate) compareByDescending { it.lastModified() } else compareBy(String.CASE_INSENSITIVE_ORDER) { it.name }
            (dirs.sortedWith(cmp) + plain.sortedWith(cmp)).map { Entry(it) }
        }) { r ->
            if (token != loadToken) return@async
            progress.visibility = View.GONE
            show(r.getOrDefault(emptyList()))
        }
    }

    private fun show(list: List<Entry>) {
        entries = list
        adapter.notifyDataSetChanged()
        listView.setSelection(0)
        emptyView.visibility = if (list.isEmpty()) View.VISIBLE else View.GONE
    }

    private fun toggleSort() {
        sortByDate = !sortByDate
        toast(getString(if (sortByDate) R.string.sort_date else R.string.sort_name))
        if (dir != null) load()
    }

    // ----- selection -----

    /** Whole drives cannot be chosen; in folder mode only folders. */
    private fun canSelect(e: Entry) = !e.isRoot && (mode == MODE_UPLOAD || e.isDir)

    /** The selection without items that lie inside another selected folder. */
    private fun topLevelSelection(): List<File> {
        val paths = selected.toList()
        return paths.filter { p -> paths.none { other -> other != p && p.startsWith("$other/") } }.map { File(it) }
    }

    private fun onTap(e: Entry) {
        when {
            selected.isNotEmpty() && canSelect(e) -> toggle(e)
            e.isDir -> open(e.file)
            else -> toggle(e)
        }
    }

    private fun toggle(e: Entry) {
        if (!canSelect(e)) {
            toast(getString(R.string.only_folders))
            return
        }
        val p = e.file.absolutePath
        if (!selected.remove(p)) selected += p
        selectionChanged()
    }

    private fun selectAll() {
        val all = entries.filter { canSelect(it) && !it.isRoot }.map { it.file.absolutePath }
        if (all.isNotEmpty() && selected.containsAll(all)) selected.removeAll(all.toSet()) else selected += all
        selectionChanged()
    }

    private fun selectionChanged() {
        val n = selected.size
        hintView.visibility = if (n == 0) View.VISIBLE else View.GONE
        selectionRow.visibility = if (n == 0) View.GONE else View.VISIBLE
        countView.text = resources.getQuantityString(R.plurals.n_selected, n, n)
        actionButton.text = getString(if (mode == MODE_FOLDERS) R.string.add_folders_n else R.string.upload_n, n)
        actionButton.isEnabled = n > 0
        actionButton.alpha = if (n > 0) 1f else 0.5f
        adapter.notifyDataSetChanged()
    }

    // ----- actions -----

    private fun runAction() {
        if (selected.isEmpty()) return
        val files = topLevelSelection()
        if (mode == MODE_FOLDERS) {
            setResult(RESULT_OK, Intent().putStringArrayListExtra(EXTRA_PATHS, ArrayList(files.map { it.absolutePath })))
            finish()
            return
        }
        actionButton.isEnabled = false
        progress.visibility = View.VISIBLE
        val sp = space
        val tg = target
        async({ Uploader(this).enqueueLocal(files, sp, tg) }) { r ->
            progress.visibility = View.GONE
            r.onSuccess { n ->
                hd.prefs.manualSpace = sp
                hd.prefs.manualTarget = tg
                hd.prefs.manualLabel = targetLabel
                UploadService.start(this)
                toast(resources.getQuantityString(R.plurals.share_queued, n, n))
                setResult(RESULT_OK, Intent().putExtra(EXTRA_COUNT, n))
                finish()
            }.onFailure {
                actionButton.isEnabled = true
                toast(errorText(it))
            }
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == REQ_TARGET && resultCode == RESULT_OK && data != null) {
            space = data.getStringExtra(FolderPickerActivity.EXTRA_SPACE) ?: "home"
            target = data.getStringExtra(FolderPickerActivity.EXTRA_PATH) ?: ""
            targetLabel = data.getStringExtra(FolderPickerActivity.EXTRA_LABEL) ?: target
            targetView.text = getString(R.string.upload_to, targetLabel)
        }
    }

    private fun defaultLabel(space: String, path: String): String =
        Uploader.joinPath(if (space == "home") getString(R.string.my_drive) else space, path)

    // ----- list rows -----

    private class Holder(val check: ImageView, val icon: ImageView, val title: TextView, val sub: TextView, val open: ImageView)

    private inner class EntryAdapter : BaseAdapter() {
        override fun getCount() = entries.size
        override fun getItem(position: Int) = entries[position]
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup): View {
            val ctx = parent.context
            val view = convertView ?: makeRow(ctx)
            val h = view.tag as Holder
            val e = entries[position]
            val isSel = e.file.absolutePath in selected
            val selecting = selected.isNotEmpty()

            h.check.visibility = if (selecting && canSelect(e)) View.VISIBLE else View.GONE
            if (isSel) {
                h.check.setImageResource(R.drawable.ic_check)
                h.check.imageTintList = ColorStateList.valueOf(0xFFFFFFFF.toInt())
                h.check.background = rounded(ctx.col(R.color.brand), ctx.dp(12).toFloat())
            } else {
                h.check.setImageDrawable(null)
                h.check.background = rounded(0, ctx.dp(12).toFloat(), ctx.col(R.color.text2), ctx.dp(2))
            }
            view.setBackgroundColor(if (isSel) ctx.col(R.color.surface2) else 0)

            h.title.text = e.name
            h.sub.text = when {
                e.isRoot -> e.file.absolutePath
                e.info != null -> e.info
                e.label != null -> Storage.display(ctx, e.file.absolutePath)
                e.isDir -> formatTime(e.file.lastModified())
                else -> formatSize(e.file.length()) + " · " + formatTime(e.file.lastModified())
            }
            h.open.visibility = if (e.isDir) View.VISIBLE else View.GONE
            h.open.setOnClickListener { open(e.file) }
            bindIcon(h.icon, e)
            view.alpha = if (canSelect(e) || e.isDir) 1f else 0.45f
            return view
        }

        private fun makeRow(ctx: Context): View {
            val check = ImageView(ctx).apply {
                val p = ctx.dp(3)
                setPadding(p, p, p, p)
            }
            val iconView = ImageView(ctx).apply { scaleType = ImageView.ScaleType.CENTER_CROP }
            val title = text(ctx, "", 15f, lines = 1)
            val sub = text(ctx, "", 12f, color = R.color.text2, lines = 1)
            val open = ImageView(ctx).apply {
                setImageResource(R.drawable.ic_chevron)
                imageTintList = ColorStateList.valueOf(ctx.col(R.color.text2))
                val p = ctx.dp(12)
                setPadding(p, p, p, p)
                background = selectable(ctx, true)
                contentDescription = ctx.getString(R.string.open)
            }
            val row = horizontal(ctx) {
                setPadding(ctx.dp(12), ctx.dp(6), ctx.dp(4), ctx.dp(6))
                minimumHeight = ctx.dp(60)
                addView(check, LinearLayout.LayoutParams(ctx.dp(24), ctx.dp(24)).margins(ctx, r = 12))
                addView(iconView, LinearLayout.LayoutParams(ctx.dp(44), ctx.dp(44)))
                addView(vertical(ctx) {
                    addView(title)
                    addView(sub)
                }, lp(0, WRAP, 1f).margins(ctx, l = 14))
                addView(open, LinearLayout.LayoutParams(ctx.dp(48), ctx.dp(48)))
            }
            row.layoutParams = android.widget.AbsListView.LayoutParams(MATCH, WRAP)
            row.tag = Holder(check, iconView, title, sub, open)
            return row
        }
    }

    private fun bindIcon(v: ImageView, e: Entry) {
        val path = e.file.absolutePath
        v.tag = path
        v.background = null
        val kind = kindOf(e)
        if (kind == Kind.IMAGE || kind == Kind.VIDEO) {
            thumbs.get(path)?.let {
                v.imageTintList = null
                v.setPadding(0, 0, 0, 0)
                v.setImageBitmap(it)
                return
            }
            loadThumb(v, e.file, kind)
        }
        val res = when (kind) {
            Kind.ROOT -> R.drawable.ic_storage
            Kind.DIR -> R.drawable.ic_folder
            Kind.IMAGE -> R.drawable.ic_image
            Kind.VIDEO -> R.drawable.ic_movie
            Kind.AUDIO -> R.drawable.ic_music
            Kind.FILE -> R.drawable.ic_file
        }
        val p = dp(8)
        v.setPadding(p, p, p, p)
        v.setImageResource(res)
        v.imageTintList = ColorStateList.valueOf(col(if (kind == Kind.DIR || kind == Kind.ROOT) R.color.brand else R.color.text2))
        v.background = rounded(col(R.color.surface2), dp(10).toFloat())
    }

    private enum class Kind { ROOT, DIR, IMAGE, VIDEO, AUDIO, FILE }

    private fun kindOf(e: Entry): Kind {
        if (e.isRoot) return Kind.ROOT
        if (e.isDir) return Kind.DIR
        return when (e.file.extension.lowercase()) {
            "jpg", "jpeg", "png", "webp", "gif", "bmp", "heic", "heif" -> Kind.IMAGE
            "mp4", "mkv", "mov", "webm", "3gp", "avi" -> Kind.VIDEO
            "mp3", "m4a", "aac", "ogg", "opus", "flac", "wav" -> Kind.AUDIO
            else -> Kind.FILE
        }
    }

    private fun loadThumb(v: ImageView, f: File, kind: Kind) {
        val path = f.absolutePath
        if (!thumbsLoading.add(path)) return
        val size = dp(44)
        thumbPool.execute {
            val bmp = try {
                if (kind == Kind.VIDEO) {
                    if (Build.VERSION.SDK_INT >= 29) ThumbnailUtils.createVideoThumbnail(f, Size(size * 2, size * 2), null) else null
                } else {
                    val o = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                    BitmapFactory.decodeFile(path, o)
                    var s = 1
                    while (maxOf(o.outWidth, o.outHeight) / (s * 2) >= size * 2) s *= 2
                    BitmapFactory.decodeFile(path, BitmapFactory.Options().apply { inSampleSize = s })
                }
            } catch (e: Throwable) {
                null
            }
            runOnUiThread {
                thumbsLoading.remove(path)
                if (bmp == null) return@runOnUiThread
                thumbs.put(path, bmp)
                if (v.tag == path) {
                    v.imageTintList = null
                    v.background = null
                    v.setPadding(0, 0, 0, 0)
                    v.setImageBitmap(bmp)
                }
            }
        }
    }

    companion object {
        const val EXTRA_MODE = "mode"
        const val EXTRA_SPACE = "space"
        const val EXTRA_PATH = "path"
        const val EXTRA_LABEL = "label"
        const val EXTRA_PATHS = "paths"
        const val EXTRA_COUNT = "count"
        /** Offer "other app" (Android's own picker); the result is then RESULT_SYSTEM_PICKER. */
        const val EXTRA_SYSTEM_PICKER = "system_picker"
        const val MODE_UPLOAD = "upload"
        const val MODE_FOLDERS = "folders"
        const val RESULT_SYSTEM_PICKER = RESULT_FIRST_USER
        private const val REQ_TARGET = 1
        private const val REQ_STORAGE = 2

        fun intent(ctx: Context, mode: String) = Intent(ctx, ExplorerActivity::class.java).putExtra(EXTRA_MODE, mode)
    }
}
