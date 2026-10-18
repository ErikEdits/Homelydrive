package io.github.erikedits.homelydrive

import android.app.Activity
import android.content.ContentUris
import android.content.Context
import android.content.Intent
import android.content.res.ColorStateList
import android.graphics.Bitmap
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.MediaStore
import android.util.LruCache
import android.util.Size
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.FrameLayout
import android.widget.GridView
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import java.io.File
import java.util.concurrent.Executors

/**
 * Photos and videos the way Google Photos or the phone's gallery shows them:
 * albums (camera, screenshots, WhatsApp, ...) with cover pictures, and inside
 * an album a grid to tap pictures. Data comes from Android's media index, the
 * same one gallery apps use.
 *
 *  - MODE_UPLOAD: tap photos/videos (long-press an album for all of it) and upload them.
 *  - MODE_FOLDERS: tap albums whose folders are uploaded automatically.
 */
class GalleryActivity : Activity() {
    private class Media(val id: Long, val path: String, val video: Boolean, val date: Long, val duration: Long)
    private class Album(val key: String, val name: String, val dir: String, val items: List<Media>)

    private var mode = ExplorerActivity.MODE_UPLOAD
    private var albums: List<Album> = emptyList()
    private var album: Album? = null
    /** Selected media paths (upload) or album folders (folder mode). */
    private val selected = LinkedHashSet<String>()

    private var space = "home"
    private var target = ""
    private var targetLabel = ""

    private lateinit var grid: GridView
    private lateinit var titleView: TextView
    private lateinit var subView: TextView
    private lateinit var selectAllView: TextView
    private lateinit var emptyView: TextView
    private lateinit var progress: ProgressBar
    private lateinit var countView: TextView
    private lateinit var hintView: TextView
    private lateinit var targetView: TextView
    private lateinit var actionButton: android.widget.Button
    private val albumAdapter = AlbumAdapter()
    private val mediaAdapter = MediaAdapter()

    private val pool = Executors.newFixedThreadPool(3)
    private val thumbs = object : LruCache<String, Bitmap>(16 * 1024 * 1024) {
        override fun sizeOf(key: String, value: Bitmap) = value.byteCount
    }
    private val loading = HashSet<String>()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        mode = intent.getStringExtra(ExplorerActivity.EXTRA_MODE) ?: ExplorerActivity.MODE_UPLOAD
        space = intent.getStringExtra(ExplorerActivity.EXTRA_SPACE) ?: hd.prefs.manualSpace
        target = intent.getStringExtra(ExplorerActivity.EXTRA_PATH) ?: hd.prefs.manualTarget
        targetLabel = intent.getStringExtra(ExplorerActivity.EXTRA_LABEL)
            ?: hd.prefs.manualLabel.takeIf { it.isNotEmpty() && !intent.hasExtra(ExplorerActivity.EXTRA_SPACE) }
            ?: Uploader.joinPath(getString(R.string.my_drive), target)

        val root = vertical(this) { setBackgroundColor(col(R.color.bg)) }
        root.addView(horizontal(this) {
            setPadding(dp(4), dp(4), dp(4), dp(4))
            addView(iconButton(context, R.drawable.ic_back, getString(R.string.back)) { goBack() })
            addView(vertical(context) {
                titleView = text(context, "", 19f, true, lines = 1)
                addView(titleView)
                subView = text(context, "", 12f, color = R.color.text2, lines = 1)
                addView(subView)
            }, lp(0, WRAP, 1f).margins(context, l = 4))
            selectAllView = text(context, getString(R.string.select_all), 14f, true, R.color.brand).apply {
                setPadding(dp(10), dp(10), dp(10), dp(10))
                background = selectable(context)
                setOnClickListener { selectAllInAlbum() }
            }
            addView(selectAllView)
        })
        root.addView(divider(this))

        val frame = FrameLayout(this)
        grid = GridView(this).apply {
            stretchMode = GridView.STRETCH_COLUMN_WIDTH
            val gap = dp(3)
            horizontalSpacing = gap
            verticalSpacing = gap
            setPadding(gap, gap, gap, gap)
            clipToPadding = false
            isFastScrollEnabled = true
            setOnItemClickListener { _, _, pos, _ -> onTap(pos) }
            setOnItemLongClickListener { _, _, pos, _ ->
                onLongPress(pos)
                true
            }
        }
        frame.addView(grid, FrameLayout.LayoutParams(MATCH, MATCH))
        emptyView = text(this, getString(R.string.gallery_empty), 15f, color = R.color.text2).apply {
            gravity = Gravity.CENTER
            visibility = View.GONE
        }
        frame.addView(emptyView, FrameLayout.LayoutParams(MATCH, MATCH))
        progress = ProgressBar(this)
        frame.addView(progress, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.CENTER))
        root.addView(frame, lp(MATCH, 0, 1f))

        root.addView(divider(this))
        root.addView(vertical(this, 12) {
            setBackgroundColor(col(R.color.surface))
            hintView = text(context, getString(if (mode == ExplorerActivity.MODE_FOLDERS) R.string.gallery_hint_folders else R.string.gallery_hint),
                13f, color = R.color.text2).apply { setPadding(dp(4), 0, dp(4), dp(8)) }
            addView(hintView)
            addView(horizontal(context) {
                setPadding(dp(4), 0, 0, dp(4))
                countView = text(context, "", 15f, true)
                addView(countView, lp(0, WRAP, 1f))
                addView(iconButton(context, R.drawable.ic_close, getString(R.string.clear_selection)) {
                    selected.clear()
                    selectionChanged()
                })
            })
            if (mode == ExplorerActivity.MODE_UPLOAD) {
                addView(horizontal(context) {
                    setPadding(dp(4), dp(2), dp(4), dp(10))
                    addView(icon(context, R.drawable.ic_upload, 20, R.color.brand))
                    targetView = text(context, getString(R.string.upload_to, targetLabel), 14f, lines = 2)
                    addView(targetView, lp(0, WRAP, 1f).margins(context, l = 10))
                    addView(text(context, getString(R.string.change), 14f, true, R.color.brand))
                    background = selectable(context)
                    setOnClickListener {
                        @Suppress("DEPRECATION")
                        startActivityForResult(FolderPickerActivity.intent(this@GalleryActivity, getString(R.string.target_folder)), REQ_TARGET)
                    }
                })
            }
            actionButton = button(context, "") { runAction() }
            addView(actionButton, lp())
        })
        applyInsets(root)
        setContentView(root)

        showAlbums()
        if (!Storage.hasAccess(this)) {
            progress.visibility = View.GONE
            emptyView.text = getString(R.string.storage_access_text)
            emptyView.visibility = View.VISIBLE
            Storage.requestAccess(this, 1)
        } else {
            load()
        }
        selectionChanged()
    }

    override fun onDestroy() {
        pool.shutdownNow()
        super.onDestroy()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (album != null) {
            showAlbums()
        } else {
            @Suppress("DEPRECATION")
            super.onBackPressed()
        }
    }

    private fun goBack() {
        if (album != null) showAlbums() else finish()
    }

    // ----- data -----

    private fun load() {
        progress.visibility = View.VISIBLE
        async({ queryAlbums() }) { r ->
            progress.visibility = View.GONE
            albums = r.getOrDefault(emptyList())
            showAlbums()
        }
    }

    private fun queryAlbums(): List<Album> {
        val media = ArrayList<Media>()
        @Suppress("DEPRECATION")
        val data = MediaStore.MediaColumns.DATA
        val sources = listOf(
            Triple(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, false, MediaStore.Images.ImageColumns.DATE_TAKEN),
            Triple(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, true, MediaStore.Video.VideoColumns.DATE_TAKEN),
        )
        for ((uri, video, takenCol) in sources) {
            val proj = if (video) {
                arrayOf(MediaStore.MediaColumns._ID, data, takenCol, MediaStore.MediaColumns.DATE_MODIFIED, MediaStore.Video.VideoColumns.DURATION)
            } else {
                arrayOf(MediaStore.MediaColumns._ID, data, takenCol, MediaStore.MediaColumns.DATE_MODIFIED)
            }
            try {
                contentResolver.query(uri, proj, null, null, null)?.use { c ->
                    while (c.moveToNext()) {
                        val path = c.getString(1) ?: continue
                        if (path.contains("/.")) continue
                        val taken = if (c.isNull(2)) 0L else c.getLong(2)
                        val date = if (taken > 0) taken else c.getLong(3) * 1000
                        media += Media(c.getLong(0), path, video, date, if (video && !c.isNull(4)) c.getLong(4) else 0)
                    }
                }
            } catch (e: Exception) {
                // media index not readable: show what we have
            }
        }
        media.sortByDescending { it.date }
        @Suppress("DEPRECATION")
        val base = android.os.Environment.getExternalStorageDirectory().absolutePath
        val byDir = media.groupBy { it.path.substringBeforeLast('/') }
        val list = byDir.map { (dir, items) ->
            val rel = dir.removePrefix(base).trim('/').lowercase()
            val name = when {
                rel == "dcim/camera" -> getString(R.string.quick_camera)
                rel.endsWith("screenshots") -> getString(R.string.quick_screenshots)
                else -> dir.substringAfterLast('/')
            }
            Album(dir, name, dir, items)
        }.sortedWith(compareBy<Album>({ albumRank(it.dir.removePrefix(base).trim('/').lowercase()) }, { -(it.items.first().date) }))
        return if (mode == ExplorerActivity.MODE_UPLOAD && media.isNotEmpty()) {
            listOf(Album(ALL, getString(R.string.gallery_all), "", media)) + list
        } else {
            list
        }
    }

    private fun albumRank(rel: String) = when {
        rel == "dcim/camera" -> 0
        rel.endsWith("screenshots") -> 1
        else -> 2
    }

    // ----- screens -----

    private fun showAlbums() {
        album = null
        titleView.text = getString(R.string.gallery_title)
        subView.text = resources.getQuantityString(R.plurals.n_albums, albums.count { it.key != ALL }, albums.count { it.key != ALL })
        selectAllView.visibility = View.GONE
        grid.numColumns = 2
        grid.adapter = albumAdapter
        albumAdapter.notifyDataSetChanged()
        emptyView.visibility = if (albums.isEmpty() && progress.visibility != View.VISIBLE) View.VISIBLE else View.GONE
    }

    private fun showAlbum(a: Album) {
        album = a
        titleView.text = a.name
        subView.text = resources.getQuantityString(R.plurals.n_items, a.items.size, a.items.size)
        selectAllView.visibility = View.VISIBLE
        grid.numColumns = if (resources.configuration.screenWidthDp >= 600) 6 else 4
        grid.adapter = mediaAdapter
        mediaAdapter.notifyDataSetChanged()
        grid.setSelection(0)
        emptyView.visibility = View.GONE
    }

    private fun onTap(pos: Int) {
        val a = album
        if (a == null) {
            val al = albums[pos]
            if (mode == ExplorerActivity.MODE_FOLDERS) toggle(al.dir) else showAlbum(al)
        } else {
            toggle(a.items[pos].path)
        }
    }

    private fun onLongPress(pos: Int) {
        val a = album
        if (a == null) {
            val al = albums[pos]
            if (mode == ExplorerActivity.MODE_FOLDERS) {
                toggle(al.dir)
            } else {
                // whole album at once
                val paths = al.items.map { it.path }
                if (selected.containsAll(paths)) selected.removeAll(paths.toSet()) else selected += paths
                selectionChanged()
                toast(resources.getQuantityString(R.plurals.n_selected, selected.size, selected.size))
            }
        } else {
            toggle(a.items[pos].path)
        }
    }

    private fun toggle(key: String) {
        if (!selected.remove(key)) selected += key
        selectionChanged()
    }

    private fun selectAllInAlbum() {
        val a = album ?: return
        val paths = a.items.map { it.path }
        if (selected.containsAll(paths)) selected.removeAll(paths.toSet()) else selected += paths
        selectionChanged()
    }

    private fun selectionChanged() {
        val n = selected.size
        hintView.visibility = if (n == 0) View.VISIBLE else View.GONE
        (countView.parent as View).visibility = if (n == 0) View.GONE else View.VISIBLE
        countView.text = resources.getQuantityString(R.plurals.n_selected, n, n)
        actionButton.text = getString(if (mode == ExplorerActivity.MODE_FOLDERS) R.string.add_albums_n else R.string.upload_n, n)
        actionButton.isEnabled = n > 0
        actionButton.alpha = if (n > 0) 1f else 0.5f
        val a = album
        if (a != null) {
            selectAllView.text = getString(if (selected.containsAll(a.items.map { it.path })) R.string.clear_selection else R.string.select_all)
            mediaAdapter.notifyDataSetChanged()
        } else {
            albumAdapter.notifyDataSetChanged()
        }
    }

    private fun runAction() {
        if (selected.isEmpty()) return
        if (mode == ExplorerActivity.MODE_FOLDERS) {
            setResult(RESULT_OK, Intent().putStringArrayListExtra(ExplorerActivity.EXTRA_PATHS, ArrayList(selected)))
            finish()
            return
        }
        actionButton.isEnabled = false
        progress.visibility = View.VISIBLE
        val files = selected.map { File(it) }
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
                setResult(RESULT_OK, Intent().putExtra(ExplorerActivity.EXTRA_COUNT, n))
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

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (Storage.hasAccess(this)) {
            emptyView.text = getString(R.string.gallery_empty)
            load()
        }
    }

    override fun onResume() {
        super.onResume()
        // back from the "all files access" setting
        if (albums.isEmpty() && progress.visibility != View.VISIBLE && Storage.hasAccess(this)) {
            emptyView.text = getString(R.string.gallery_empty)
            load()
        }
    }

    // ----- grid cells -----

    /** A cell that is as high as it is wide. */
    private class Square(ctx: Context) : FrameLayout(ctx) {
        override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) = super.onMeasure(widthMeasureSpec, widthMeasureSpec)
    }

    private class Cell(val image: ImageView, val check: ImageView, val badge: TextView, val title: TextView?, val sub: TextView?)

    private fun makeCell(ctx: Context, withText: Boolean): View {
        val square = Square(ctx)
        val image = ImageView(ctx).apply {
            scaleType = ImageView.ScaleType.CENTER_CROP
            setBackgroundColor(ctx.col(R.color.surface2))
        }
        square.addView(image, FrameLayout.LayoutParams(MATCH, MATCH))
        val badge = TextView(ctx).apply {
            setTextColor(0xFFFFFFFF.toInt())
            textSize = 11f
            setPadding(ctx.dp(5), ctx.dp(1), ctx.dp(5), ctx.dp(1))
            background = rounded(0x99000000.toInt(), ctx.dp(8).toFloat())
        }
        square.addView(badge, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.BOTTOM or Gravity.END).apply {
            setMargins(ctx.dp(4), ctx.dp(4), ctx.dp(4), ctx.dp(4))
        })
        val check = ImageView(ctx).apply {
            val p = ctx.dp(3)
            setPadding(p, p, p, p)
        }
        square.addView(check, FrameLayout.LayoutParams(ctx.dp(26), ctx.dp(26), Gravity.TOP or Gravity.END).apply {
            setMargins(ctx.dp(6), ctx.dp(6), ctx.dp(6), ctx.dp(6))
        })
        if (!withText) {
            square.tag = Cell(image, check, badge, null, null)
            return square
        }
        val title = text(ctx, "", 14f, true, lines = 1)
        val sub = text(ctx, "", 12f, color = R.color.text2, lines = 1)
        val box = vertical(ctx) {
            addView(square, lp(MATCH, WRAP))
            addView(title, lp().margins(ctx, l = 4, t = 6))
            addView(sub, lp().margins(ctx, l = 4, b = 8))
        }
        box.tag = Cell(image, check, badge, title, sub)
        return box
    }

    private fun bindCheck(c: Cell, selecting: Boolean, isSel: Boolean) {
        val ctx = c.check.context
        c.check.visibility = if (selecting || isSel) View.VISIBLE else View.GONE
        if (isSel) {
            c.check.setImageResource(R.drawable.ic_check)
            c.check.imageTintList = ColorStateList.valueOf(0xFFFFFFFF.toInt())
            c.check.background = rounded(ctx.col(R.color.brand), ctx.dp(13).toFloat(), 0xFFFFFFFF.toInt(), ctx.dp(2))
            c.image.alpha = 0.75f
        } else {
            c.check.setImageDrawable(null)
            c.check.background = rounded(0x55000000, ctx.dp(13).toFloat(), 0xFFFFFFFF.toInt(), ctx.dp(2))
            c.image.alpha = 1f
        }
    }

    private inner class AlbumAdapter : BaseAdapter() {
        override fun getCount() = albums.size
        override fun getItem(position: Int) = albums[position]
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup): View {
            val v = convertView ?: makeCell(parent.context, true)
            val c = v.tag as Cell
            val a = albums[position]
            c.title?.text = a.name
            val photos = a.items.count { !it.video }
            val videos = a.items.size - photos
            c.sub?.text = listOfNotNull(
                if (photos > 0) resources.getQuantityString(R.plurals.n_photos, photos, photos) else null,
                if (videos > 0) resources.getQuantityString(R.plurals.n_videos, videos, videos) else null,
            ).joinToString(" · ")
            c.badge.visibility = View.GONE
            val isSel = if (mode == ExplorerActivity.MODE_FOLDERS) a.dir in selected
            else a.items.isNotEmpty() && selected.containsAll(a.items.map { it.path })
            val partly = mode == ExplorerActivity.MODE_UPLOAD && !isSel && a.items.any { it.path in selected }
            bindCheck(c, partly, isSel)
            bindThumb(c.image, a.items.first())
            return v
        }
    }

    private inner class MediaAdapter : BaseAdapter() {
        override fun getCount() = album?.items?.size ?: 0
        override fun getItem(position: Int) = album!!.items[position]
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup): View {
            val v = convertView ?: makeCell(parent.context, false)
            val c = v.tag as Cell
            val m = album!!.items[position]
            if (m.video) {
                c.badge.visibility = View.VISIBLE
                val s = m.duration / 1000
                c.badge.text = if (s >= 3600) "%d:%02d:%02d".format(s / 3600, s / 60 % 60, s % 60) else "%d:%02d".format(s / 60, s % 60)
            } else {
                c.badge.visibility = View.GONE
            }
            bindCheck(c, selected.isNotEmpty(), m.path in selected)
            bindThumb(c.image, m)
            return v
        }
    }

    private fun bindThumb(v: ImageView, m: Media) {
        val key = (if (m.video) "v" else "i") + m.id
        v.tag = key
        val cached = thumbs.get(key)
        if (cached != null) {
            v.setImageBitmap(cached)
            return
        }
        v.setImageDrawable(null)
        if (!loading.add(key)) return
        val size = dp(160)
        pool.execute {
            val bmp = try {
                val uri: Uri = ContentUris.withAppendedId(
                    if (m.video) MediaStore.Video.Media.EXTERNAL_CONTENT_URI else MediaStore.Images.Media.EXTERNAL_CONTENT_URI, m.id)
                if (Build.VERSION.SDK_INT >= 29) {
                    contentResolver.loadThumbnail(uri, Size(size, size), null)
                } else if (m.video) {
                    @Suppress("DEPRECATION")
                    MediaStore.Video.Thumbnails.getThumbnail(contentResolver, m.id, MediaStore.Video.Thumbnails.MINI_KIND, null)
                } else {
                    @Suppress("DEPRECATION")
                    MediaStore.Images.Thumbnails.getThumbnail(contentResolver, m.id, MediaStore.Images.Thumbnails.MINI_KIND, null)
                }
            } catch (e: Throwable) {
                null
            }
            runOnUiThread {
                loading.remove(key)
                if (bmp == null || isDestroyed) return@runOnUiThread
                thumbs.put(key, bmp)
                if (v.tag == key) v.setImageBitmap(bmp)
            }
        }
    }

    companion object {
        private const val ALL = "*"
        private const val REQ_TARGET = 1

        fun intent(ctx: Context, mode: String) = Intent(ctx, GalleryActivity::class.java).putExtra(ExplorerActivity.EXTRA_MODE, mode)
    }
}
