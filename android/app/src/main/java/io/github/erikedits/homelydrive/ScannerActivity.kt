package io.github.erikedits.homelydrive

import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.ColorMatrix
import android.graphics.ColorMatrixColorFilter
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.RectF
import android.media.ExifInterface
import android.net.Uri
import android.os.Bundle
import android.provider.MediaStore
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView
import java.io.ByteArrayOutputStream
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * Document scanner: take photos of pages (or pick pictures), rotate, crop,
 * make them black-and-white, and upload them as one PDF.
 */
class ScannerActivity : Activity() {
    private class Page(val file: File, var rotation: Int = 0, var crop: RectF? = null, var enhance: Boolean = true)

    private val pages = ArrayList<Page>()
    private lateinit var pageList: LinearLayout
    private lateinit var nameInput: EditText
    private lateinit var targetText: TextView
    private lateinit var uploadButton: View
    private lateinit var progress: ProgressBar
    private var space = "home"
    private var target = ""
    private var captureFile: File? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (!hd.prefs.isLoggedIn) {
            finish()
            return
        }
        space = hd.prefs.scanSpace
        target = hd.prefs.scanTarget

        val root = vertical(this) { setBackgroundColor(col(R.color.bg)) }
        root.addView(horizontal(this) {
            setPadding(dp(4), dp(4), dp(12), dp(4))
            addView(iconButton(context, R.drawable.ic_back, getString(R.string.back)) { finish() })
            addView(text(context, getString(R.string.scanner_title), 19f, true), lp(0, WRAP, 1f).margins(context, l = 8))
        })

        pageList = vertical(this, 12)
        root.addView(ScrollView(this).apply { addView(pageList) }, lp(MATCH, 0, 1f))

        val bottom = vertical(this, 16) {
            setBackgroundColor(col(R.color.surface))
            nameInput = EditText(context).apply {
                setSingleLine()
                setText(getString(R.string.scan_default_name, SimpleDateFormat("yyyy-MM-dd HH-mm", Locale.ROOT).format(Date())))
                hint = getString(R.string.file_name)
            }
            addView(nameInput)
            targetText = text(context, "", 14f, color = R.color.text2, lines = 1)
            addView(horizontal(context) {
                setPadding(0, dp(6), 0, dp(6))
                addView(icon(context, R.drawable.ic_folder, 20))
                addView(targetText, lp(0, WRAP, 1f).margins(context, l = 8))
                addView(text(context, getString(R.string.change), 14f, true, R.color.brand))
                background = selectable(context)
                setOnClickListener {
                    @Suppress("DEPRECATION")
                    startActivityForResult(FolderPickerActivity.intent(this@ScannerActivity, getString(R.string.target_folder)), REQ_TARGET)
                }
            })
            progress = ProgressBar(context, null, android.R.attr.progressBarStyleHorizontal).apply {
                isIndeterminate = true
                visibility = View.GONE
            }
            addView(progress)
            addView(horizontal(context) {
                setPadding(0, dp(8), 0, 0)
                addView(button(context, getString(R.string.add_page), primary = false) { takePhoto() }, lp(0, WRAP, 1f).margins(context, r = 6))
                addView(button(context, getString(R.string.from_gallery), primary = false) { pickImages() }, lp(0, WRAP, 1f).margins(context, l = 6))
            })
            uploadButton = button(context, getString(R.string.upload_pdf)) { upload() }
            addView(uploadButton, lp().margins(context, t = 10))
        }
        root.addView(bottom)
        applyInsets(root)
        setContentView(root)
        targetText.text = Uploader.joinPath(if (space == "home") getString(R.string.my_drive) else "…", target)

        pagesDir(this).listFiles()?.forEach { it.delete() }
        refresh()
        if (savedInstanceState == null) takePhoto()
    }

    // ----- adding pages -----

    private fun takePhoto() {
        val f = File(CaptureProvider.dir(this), "page-${System.currentTimeMillis()}.jpg")
        captureFile = f
        val uri = CaptureProvider.uriFor(this, f)
        val i = Intent(MediaStore.ACTION_IMAGE_CAPTURE)
            .putExtra(MediaStore.EXTRA_OUTPUT, uri)
            .addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION or Intent.FLAG_GRANT_READ_URI_PERMISSION)
        i.clipData = ClipData.newRawUri("", uri)
        try {
            @Suppress("DEPRECATION")
            startActivityForResult(i, REQ_CAMERA)
        } catch (e: ActivityNotFoundException) {
            toast(getString(R.string.no_camera_app))
        }
    }

    private fun pickImages() {
        val i = Intent(Intent.ACTION_GET_CONTENT).setType("image/*")
            .addCategory(Intent.CATEGORY_OPENABLE)
            .putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
        @Suppress("DEPRECATION")
        startActivityForResult(Intent.createChooser(i, getString(R.string.from_gallery)), REQ_GALLERY)
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        when (requestCode) {
            REQ_CAMERA -> {
                val f = captureFile
                if (resultCode == RESULT_OK && f != null && f.length() > 0) {
                    val dest = File(pagesDir(this), f.name)
                    if (f.renameTo(dest) || f.copyTo(dest, true).exists()) {
                        f.delete()
                        pages += Page(dest)
                        refresh()
                    }
                } else {
                    f?.delete()
                }
            }
            REQ_GALLERY -> if (resultCode == RESULT_OK && data != null) {
                val uris = ArrayList<Uri>()
                data.clipData?.let { cd -> for (k in 0 until cd.itemCount) uris += cd.getItemAt(k).uri }
                if (uris.isEmpty()) data.data?.let { uris += it }
                async({
                    uris.mapIndexedNotNull { idx, u ->
                        val dest = File(pagesDir(this), "import-${System.currentTimeMillis()}-$idx.jpg")
                        contentResolver.openInputStream(u)?.use { input -> dest.outputStream().use { input.copyTo(it) } }
                        if (dest.length() > 0) Page(dest, enhance = false) else null
                    }
                }) { r ->
                    r.onSuccess {
                        pages += it
                        refresh()
                    }.onFailure { toast(errorText(it)) }
                }
            }
            REQ_TARGET -> if (resultCode == RESULT_OK && data != null) {
                space = data.getStringExtra(FolderPickerActivity.EXTRA_SPACE) ?: "home"
                target = data.getStringExtra(FolderPickerActivity.EXTRA_PATH) ?: ""
                hd.prefs.scanSpace = space
                hd.prefs.scanTarget = target
                targetText.text = data.getStringExtra(FolderPickerActivity.EXTRA_LABEL) ?: target
            }
        }
    }

    // ----- page list -----

    private fun refresh() {
        pageList.removeAllViews()
        uploadButton.isEnabled = pages.isNotEmpty()
        uploadButton.alpha = if (pages.isEmpty()) 0.5f else 1f
        if (pages.isEmpty()) {
            pageList.addView(vertical(this, 24) {
                gravity = Gravity.CENTER_HORIZONTAL
                addView(icon(context, R.drawable.ic_scan, 64, R.color.brand))
                addView(text(context, getString(R.string.scanner_empty), 15f, color = R.color.text2).apply {
                    gravity = Gravity.CENTER
                    setPadding(0, dp(16), 0, dp(16))
                })
                addView(button(context, getString(R.string.take_photo)) { takePhoto() }, lp(WRAP, WRAP))
            })
            return
        }
        pages.forEachIndexed { i, p -> pageList.addView(pageCard(i, p)) }
    }

    private fun pageCard(index: Int, p: Page): View = card(this) {
        val img = ImageView(context).apply {
            scaleType = ImageView.ScaleType.FIT_CENTER
            adjustViewBounds = true
            setBackgroundColor(col(R.color.surface2))
        }
        addView(img, lp(MATCH, dp(220)))
        async({ render(p, 700) }) { r -> r.onSuccess { img.setImageBitmap(it) } }
        addView(horizontal(context) {
            setPadding(0, dp(8), 0, 0)
            addView(text(context, getString(R.string.page_n, index + 1), 15f, true), lp(0, WRAP, 1f))
            addView(iconButton(context, R.drawable.ic_rotate, getString(R.string.rotate)) {
                p.rotation = (p.rotation + 90) % 360
                p.crop = null
                refresh()
            })
            addView(iconButton(context, R.drawable.ic_scan, getString(R.string.crop)) { cropDialog(p) })
            addView(iconButton(context, R.drawable.ic_check, getString(R.string.black_white), if (p.enhance) R.color.brand else R.color.text2) {
                p.enhance = !p.enhance
                refresh()
            })
            if (index > 0) addView(iconButton(context, R.drawable.ic_back, getString(R.string.move_up)) {
                pages.removeAt(index)
                pages.add(index - 1, p)
                refresh()
            }.apply { rotation = 90f })
            addView(iconButton(context, R.drawable.ic_delete, getString(R.string.delete)) {
                pages.removeAt(index)
                p.file.delete()
                refresh()
            })
        })
    }

    private fun cropDialog(p: Page) {
        async({ render(p.copyNoCrop(), 1400) }) { r ->
            val bmp = r.getOrNull() ?: return@async
            val view = CropView(this, bmp, p.crop ?: RectF(0f, 0f, 1f, 1f))
            AlertDialog.Builder(this)
                .setTitle(R.string.crop)
                .setView(FrameLayout(this).apply {
                    setPadding(dp(12), dp(12), dp(12), 0)
                    addView(view, FrameLayout.LayoutParams(MATCH, dp(420)))
                })
                .setPositiveButton(android.R.string.ok) { _, _ ->
                    p.crop = view.result()
                    refresh()
                }
                .setNeutralButton(R.string.reset) { _, _ ->
                    p.crop = null
                    refresh()
                }
                .setNegativeButton(android.R.string.cancel, null)
                .show()
        }
    }

    private fun Page.copyNoCrop() = Page(file, rotation, null, enhance)

    // ----- PDF -----

    private fun upload() {
        if (pages.isEmpty()) return
        var name = nameInput.text.toString().trim().ifEmpty { getString(R.string.scan_default_name, "") .trim() }
        name = name.replace(Regex("[\\\\/:*?\"<>|]"), "_")
        if (!name.lowercase().endsWith(".pdf")) name += ".pdf"
        uploadButton.isEnabled = false
        progress.visibility = View.VISIBLE
        val list = pages.toList()
        val sp = space
        val tg = target
        val fileName = name
        async({
            val dir = File(Uploader.outbox(this), "scan-" + System.currentTimeMillis()).apply { mkdirs() }
            val pdf = File(dir, fileName)
            PdfWriter(pdf, list.size).use { w ->
                for (p in list) {
                    val bmp = render(p, 2200)
                    val jpeg = ByteArrayOutputStream().use { bos ->
                        bmp.compress(Bitmap.CompressFormat.JPEG, 82, bos)
                        bos.toByteArray()
                    }
                    // A4 width (portrait or landscape), height following the picture.
                    val pw = if (bmp.width > bmp.height) 842f else 595f
                    w.addPage(jpeg, bmp.width, bmp.height, pw, pw * bmp.height / bmp.width)
                    bmp.recycle()
                }
            }
            Uploader(this).enqueueFile("scan", pdf, sp, Uploader.joinPath(tg, fileName))
        }) { r ->
            progress.visibility = View.GONE
            r.onSuccess {
                UploadService.start(this)
                list.forEach { it.file.delete() }
                toast(getString(R.string.scan_queued, fileName))
                finish()
            }.onFailure {
                uploadButton.isEnabled = true
                toast(errorText(it))
            }
        }
    }

    /** Decodes a page with EXIF orientation, rotation, crop and filter, at most [maxSide] pixels. */
    private fun render(p: Page, maxSide: Int): Bitmap {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(p.file.path, bounds)
        var sample = 1
        while (maxOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= maxSide) sample *= 2
        val src = BitmapFactory.decodeFile(p.file.path, BitmapFactory.Options().apply { inSampleSize = sample })
            ?: throw IllegalStateException(getString(R.string.err_image))

        val exif = try {
            when (ExifInterface(p.file.path).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL)) {
                ExifInterface.ORIENTATION_ROTATE_90 -> 90
                ExifInterface.ORIENTATION_ROTATE_180 -> 180
                ExifInterface.ORIENTATION_ROTATE_270 -> 270
                else -> 0
            }
        } catch (e: Exception) {
            0
        }
        val m = Matrix()
        val scale = minOf(1f, maxSide.toFloat() / maxOf(src.width, src.height))
        m.postScale(scale, scale)
        m.postRotate(((exif + p.rotation) % 360).toFloat())
        var bmp = Bitmap.createBitmap(src, 0, 0, src.width, src.height, m, true)
        if (bmp != src) src.recycle()

        p.crop?.let { c ->
            val x = (c.left * bmp.width).toInt().coerceIn(0, bmp.width - 1)
            val y = (c.top * bmp.height).toInt().coerceIn(0, bmp.height - 1)
            val w = (c.width() * bmp.width).toInt().coerceIn(1, bmp.width - x)
            val h = (c.height() * bmp.height).toInt().coerceIn(1, bmp.height - y)
            val cropped = Bitmap.createBitmap(bmp, x, y, w, h)
            if (cropped != bmp) bmp.recycle()
            bmp = cropped
        }

        if (p.enhance) {
            val out = Bitmap.createBitmap(bmp.width, bmp.height, Bitmap.Config.ARGB_8888)
            val cm = ColorMatrix().apply { setSaturation(0f) }
            // more contrast, slightly brighter: paper becomes white, text dark
            val k = 1.6f
            val t = -0.3f * 255 + 25
            cm.postConcat(ColorMatrix(floatArrayOf(
                k, 0f, 0f, 0f, t,
                0f, k, 0f, 0f, t,
                0f, 0f, k, 0f, t,
                0f, 0f, 0f, 1f, 0f,
            )))
            Canvas(out).drawBitmap(bmp, 0f, 0f, Paint(Paint.FILTER_BITMAP_FLAG).apply { colorFilter = ColorMatrixColorFilter(cm) })
            bmp.recycle()
            bmp = out
        }
        return bmp
    }

    /** Shows a picture with a crop rectangle whose corners and edges can be dragged. */
    @SuppressLint("ViewConstructor")
    private class CropView(ctx: Context, private val bmp: Bitmap, start: RectF) : View(ctx) {
        private val crop = RectF(start)
        private val image = RectF()
        private val paint = Paint(Paint.FILTER_BITMAP_FLAG)
        private val dim = Paint().apply { color = 0x99000000.toInt() }
        private val line = Paint().apply {
            color = ctx.getColor(R.color.brand)
            style = Paint.Style.STROKE
            strokeWidth = ctx.dp(2).toFloat()
            isAntiAlias = true
        }
        private val handle = Paint().apply {
            color = ctx.getColor(R.color.brand)
            isAntiAlias = true
        }
        private var drag = 0 // bit flags: 1 left, 2 top, 4 right, 8 bottom, 16 move
        private var lastX = 0f
        private var lastY = 0f

        fun result(): RectF? = if (crop.left <= 0.001f && crop.top <= 0.001f && crop.right >= 0.999f && crop.bottom >= 0.999f) null else RectF(crop)

        override fun onDraw(canvas: Canvas) {
            val s = minOf(width.toFloat() / bmp.width, height.toFloat() / bmp.height)
            val w = bmp.width * s
            val h = bmp.height * s
            image.set((width - w) / 2, (height - h) / 2, (width + w) / 2, (height + h) / 2)
            canvas.drawBitmap(bmp, null, image, paint)
            val r = screen()
            canvas.drawRect(image.left, image.top, image.right, r.top, dim)
            canvas.drawRect(image.left, r.bottom, image.right, image.bottom, dim)
            canvas.drawRect(image.left, r.top, r.left, r.bottom, dim)
            canvas.drawRect(r.right, r.top, image.right, r.bottom, dim)
            canvas.drawRect(r, line)
            val hr = context.dp(7).toFloat()
            for (x in floatArrayOf(r.left, r.right)) for (y in floatArrayOf(r.top, r.bottom)) canvas.drawCircle(x, y, hr, handle)
        }

        private fun screen() = RectF(
            image.left + crop.left * image.width(), image.top + crop.top * image.height(),
            image.left + crop.right * image.width(), image.top + crop.bottom * image.height(),
        )

        @SuppressLint("ClickableViewAccessibility")
        override fun onTouchEvent(e: MotionEvent): Boolean {
            when (e.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    val r = screen()
                    val slop = context.dp(28).toFloat()
                    drag = 0
                    if (Math.abs(e.x - r.left) < slop) drag = drag or 1
                    if (Math.abs(e.x - r.right) < slop) drag = drag or 4
                    if (Math.abs(e.y - r.top) < slop) drag = drag or 2
                    if (Math.abs(e.y - r.bottom) < slop) drag = drag or 8
                    if (drag == 0 && r.contains(e.x, e.y)) drag = 16
                    lastX = e.x
                    lastY = e.y
                    parent?.requestDisallowInterceptTouchEvent(true)
                    return drag != 0
                }
                MotionEvent.ACTION_MOVE -> {
                    val dx = (e.x - lastX) / image.width()
                    val dy = (e.y - lastY) / image.height()
                    lastX = e.x
                    lastY = e.y
                    val min = 0.08f
                    if (drag == 16) {
                        val mx = dx.coerceIn(-crop.left, 1f - crop.right)
                        val my = dy.coerceIn(-crop.top, 1f - crop.bottom)
                        crop.offset(mx, my)
                    } else {
                        if (drag and 1 != 0) crop.left = (crop.left + dx).coerceIn(0f, crop.right - min)
                        if (drag and 4 != 0) crop.right = (crop.right + dx).coerceIn(crop.left + min, 1f)
                        if (drag and 2 != 0) crop.top = (crop.top + dy).coerceIn(0f, crop.bottom - min)
                        if (drag and 8 != 0) crop.bottom = (crop.bottom + dy).coerceIn(crop.top + min, 1f)
                    }
                    invalidate()
                    return true
                }
                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> drag = 0
            }
            return true
        }
    }

    companion object {
        private const val REQ_CAMERA = 1
        private const val REQ_GALLERY = 2
        private const val REQ_TARGET = 3

        fun pagesDir(ctx: Context) = File(ctx.filesDir, "scan-pages").apply { mkdirs() }
    }
}
