package io.github.erikedits.homelydrive

import android.app.Activity
import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.text.TextUtils
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.widget.Button
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import java.text.DateFormat
import java.util.Date
import java.util.concurrent.Executors

/*
 * Small helpers for building the screens in code. The app uses only Android
 * framework classes (no AndroidX), so this replaces layout libraries.
 */

fun Context.dp(v: Int): Int = (v * resources.displayMetrics.density + 0.5f).toInt()

fun Context.col(id: Int): Int = getColor(id)

const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
const val WRAP = ViewGroup.LayoutParams.WRAP_CONTENT

fun lp(w: Int = MATCH, h: Int = WRAP, weight: Float = 0f) = LinearLayout.LayoutParams(w, h, weight)

fun LinearLayout.LayoutParams.margins(ctx: Context, l: Int = 0, t: Int = 0, r: Int = 0, b: Int = 0) = apply {
    setMargins(ctx.dp(l), ctx.dp(t), ctx.dp(r), ctx.dp(b))
}

fun rounded(color: Int, radius: Float, stroke: Int = 0, strokeWidth: Int = 0) = GradientDrawable().apply {
    setColor(color)
    cornerRadius = radius
    if (strokeWidth > 0) setStroke(strokeWidth, stroke)
}

fun vertical(ctx: Context, padding: Int = 0, block: LinearLayout.() -> Unit = {}) = LinearLayout(ctx).apply {
    orientation = LinearLayout.VERTICAL
    val p = ctx.dp(padding)
    setPadding(p, p, p, p)
    block()
}

fun horizontal(ctx: Context, padding: Int = 0, block: LinearLayout.() -> Unit = {}) = LinearLayout(ctx).apply {
    orientation = LinearLayout.HORIZONTAL
    gravity = Gravity.CENTER_VERTICAL
    val p = ctx.dp(padding)
    setPadding(p, p, p, p)
    block()
}

fun text(ctx: Context, s: CharSequence, size: Float = 15f, bold: Boolean = false, color: Int = R.color.text, lines: Int = 0) = TextView(ctx).apply {
    text = s
    setTextSize(TypedValue.COMPLEX_UNIT_SP, size)
    setTextColor(ctx.col(color))
    if (bold) typeface = Typeface.DEFAULT_BOLD
    if (lines > 0) {
        maxLines = lines
        ellipsize = TextUtils.TruncateAt.END
    }
}

fun heading(ctx: Context, s: CharSequence) = text(ctx, s, 13f, true, R.color.text2).apply {
    isAllCaps = true
    letterSpacing = 0.05f
    setPadding(ctx.dp(4), ctx.dp(20), ctx.dp(4), ctx.dp(8))
}

/** A filled (primary) or outlined button. */
fun button(ctx: Context, label: CharSequence, primary: Boolean = true, onClick: (View) -> Unit) = Button(ctx).apply {
    text = label
    isAllCaps = false
    setTextSize(TypedValue.COMPLEX_UNIT_SP, 15f)
    stateListAnimator = null
    minHeight = ctx.dp(44)
    setPadding(ctx.dp(18), 0, ctx.dp(18), 0)
    val r = ctx.dp(22).toFloat()
    val bg = if (primary) rounded(ctx.col(R.color.brand), r) else rounded(ctx.col(R.color.surface), r, ctx.col(R.color.divider), ctx.dp(1))
    background = RippleDrawable(ColorStateList.valueOf(0x33000000), bg, null)
    setTextColor(if (primary) 0xFFFFFFFF.toInt() else ctx.col(R.color.brand))
    setOnClickListener(onClick)
}

fun icon(ctx: Context, res: Int, sizeDp: Int = 24, tint: Int = R.color.text2) = ImageView(ctx).apply {
    setImageResource(res)
    imageTintList = ColorStateList.valueOf(ctx.col(tint))
    layoutParams = LinearLayout.LayoutParams(ctx.dp(sizeDp), ctx.dp(sizeDp))
}

fun iconButton(ctx: Context, res: Int, desc: CharSequence, tint: Int = R.color.text2, onClick: (View) -> Unit) = ImageView(ctx).apply {
    setImageResource(res)
    imageTintList = ColorStateList.valueOf(ctx.col(tint))
    contentDescription = desc
    val p = ctx.dp(10)
    setPadding(p, p, p, p)
    layoutParams = LinearLayout.LayoutParams(ctx.dp(44), ctx.dp(44))
    background = selectable(ctx, true)
    setOnClickListener(onClick)
    if (Build.VERSION.SDK_INT >= 26) tooltipText = desc
}

/** The theme's ripple for clickable rows. */
fun selectable(ctx: Context, borderless: Boolean = false): android.graphics.drawable.Drawable? {
    val tv = TypedValue()
    val attr = if (borderless) android.R.attr.selectableItemBackgroundBorderless else android.R.attr.selectableItemBackground
    ctx.theme.resolveAttribute(attr, tv, true)
    return ctx.getDrawable(tv.resourceId)
}

fun card(ctx: Context, block: LinearLayout.() -> Unit) = vertical(ctx, 16) {
    background = rounded(ctx.col(R.color.surface), ctx.dp(16).toFloat(), ctx.col(R.color.divider), ctx.dp(1))
    layoutParams = lp().margins(ctx, b = 12)
    block()
}

/** A clickable row with icon, title and subtitle. */
fun row(ctx: Context, iconRes: Int, title: CharSequence, subtitle: CharSequence? = null, onClick: ((View) -> Unit)? = null) =
    horizontal(ctx) {
        setPadding(ctx.dp(12), ctx.dp(12), ctx.dp(12), ctx.dp(12))
        minimumHeight = ctx.dp(56)
        addView(icon(ctx, iconRes))
        addView(vertical(ctx) {
            addView(text(ctx, title, 16f, lines = 2))
            if (!subtitle.isNullOrEmpty()) addView(text(ctx, subtitle, 13f, color = R.color.text2, lines = 3))
        }, lp(0, WRAP, 1f).margins(ctx, l = 16))
        if (onClick != null) {
            background = selectable(ctx)
            setOnClickListener(onClick)
        }
    }

fun divider(ctx: Context) = View(ctx).apply {
    setBackgroundColor(ctx.col(R.color.divider))
    layoutParams = lp(MATCH, 1)
}

/** Keeps content clear of the status/navigation bar and keyboard (edge-to-edge on Android 15). */
fun Activity.applyInsets(root: View) {
    root.setOnApplyWindowInsetsListener { v, insets ->
        if (Build.VERSION.SDK_INT >= 30) {
            val i = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime() or WindowInsets.Type.displayCutout())
            v.setPadding(i.left, i.top, i.right, i.bottom)
        } else {
            @Suppress("DEPRECATION")
            v.setPadding(insets.systemWindowInsetLeft, insets.systemWindowInsetTop, insets.systemWindowInsetRight, insets.systemWindowInsetBottom)
        }
        insets
    }
}

fun Context.toast(s: CharSequence) = Toast.makeText(this, s, Toast.LENGTH_LONG).show()

fun formatSize(b: Long): String {
    if (b < 1024) return "$b B"
    val units = arrayOf("KB", "MB", "GB", "TB")
    var v = b / 1024.0
    var i = 0
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024
        i++
    }
    return if (v >= 100) "%.0f %s".format(v, units[i]) else "%.1f %s".format(v, units[i])
}

fun formatTime(ms: Long): String = DateFormat.getDateTimeInstance(DateFormat.SHORT, DateFormat.SHORT).format(Date(ms))

private val bgPool = Executors.newCachedThreadPool()
private val mainHandler = Handler(Looper.getMainLooper())

/** Runs [work] in the background and hands the result to [done] on the main thread (if the activity still exists). */
fun <T> Activity.async(work: () -> T, done: (Result<T>) -> Unit) {
    bgPool.execute {
        val r = runCatching(work)
        mainHandler.post {
            if (!isDestroyed) done(r)
        }
    }
}

fun runBackground(work: () -> Unit) = bgPool.execute(work)

fun runMain(work: () -> Unit) = mainHandler.post(work)

/** User-facing text for an error. */
fun Context.errorText(e: Throwable): String = when {
    e is ApiException && e.isNetwork -> getString(R.string.err_unreachable)
    e is ApiException && e.status == 401 -> getString(R.string.err_login)
    else -> e.message ?: e.javaClass.simpleName
}
