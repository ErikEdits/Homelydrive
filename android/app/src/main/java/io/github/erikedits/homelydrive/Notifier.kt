package io.github.erikedits.homelydrive

import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/** Shows server notifications (new shares, ...) and the update hint as Android notifications. */
object Notifier {

    /** Fetches new server notifications and shows them. */
    @Synchronized
    fun check(ctx: Context) {
        val p = ctx.hd.prefs
        if (!p.isLoggedIn) return
        val last = p.lastNotificationId
        val o = try {
            Api(p.server, p.token).notifications(maxOf(last, 0))
        } catch (e: Exception) {
            return
        }
        val items = o.optJSONArray("items") ?: return
        var maxId = maxOf(last, 0)
        for (i in 0 until items.length()) maxId = maxOf(maxId, items.getJSONObject(i).optLong("id"))
        if (last >= 0) {
            // oldest first
            for (i in items.length() - 1 downTo 0) {
                val n = items.getJSONObject(i)
                if (!n.optBoolean("read")) show(ctx, n)
            }
        }
        p.lastNotificationId = maxId
    }

    private fun show(ctx: Context, n: JSONObject) {
        val d = n.optJSONObject("data") ?: JSONObject()
        val text = when (n.optString("kind")) {
            "share" -> if (d.optBoolean("is_dir")) {
                ctx.getString(R.string.notify_share_folder, d.optString("from"), d.optString("name"))
            } else {
                ctx.getString(R.string.notify_share_file, d.optString("from"), d.optString("name"))
            }
            "link_upload" -> ctx.getString(R.string.notify_link_upload, d.optString("name"), d.optString("folder"))
            "quota_warning" -> ctx.getString(R.string.notify_quota, d.optInt("percent"), formatSize(d.optLong("used")), formatSize(d.optLong("quota")))
            "disk" -> ctx.getString(R.string.notify_disk, d.optString("path"), d.optInt("percent"))
            "update" -> ctx.getString(R.string.notify_server_update, d.optString("version"))
            else -> return
        }
        val intent = Intent(ctx, MainActivity::class.java).putExtra(MainActivity.EXTRA_TAB, MainActivity.TAB_FILES)
        post(ctx, (1000 + n.optLong("id") % 100_000).toInt(), ctx.getString(R.string.app_name), text,
            PendingIntent.getActivity(ctx, 10, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
    }

    fun post(ctx: Context, id: Int, title: String, text: String, intent: PendingIntent) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        val n = Notification.Builder(ctx, HdApp.CH_EVENTS)
            .setSmallIcon(R.drawable.ic_notify)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(Notification.BigTextStyle().bigText(text))
            .setAutoCancel(true)
            .setContentIntent(intent)
            .build()
        try {
            nm.notify(id, n)
        } catch (e: SecurityException) {
            // notifications not allowed
        }
    }

    /** Looks for a new app version on GitHub (at most once a day). */
    fun checkUpdate(ctx: Context, force: Boolean = false) {
        val p = ctx.hd.prefs
        if (!force && (!p.updateCheck || System.currentTimeMillis() - p.lastUpdateCheck < 24 * 3600_000L)) return
        p.lastUpdateCheck = System.currentTimeMillis()
        try {
            val c = URL("https://api.github.com/repos/ErikEdits/Homelydrive/releases/latest").openConnection() as HttpURLConnection
            c.connectTimeout = 15_000
            c.readTimeout = 15_000
            c.setRequestProperty("Accept", "application/vnd.github+json")
            c.setRequestProperty("User-Agent", "Homelydrive-Android")
            val o = try {
                if (c.responseCode != 200) return
                JSONObject(c.inputStream.bufferedReader().readText())
            } finally {
                c.disconnect()
            }
            val tag = o.optString("tag_name").removePrefix("v")
            if (tag.isNotEmpty() && isNewer(tag, BuildConfig.VERSION_NAME)) {
                val known = p.knownUpdate
                p.knownUpdate = tag
                p.updateUrl = o.optString("html_url")
                if (known != tag) {
                    val pi = PendingIntent.getActivity(ctx, 11, Intent(Intent.ACTION_VIEW, Uri.parse(p.updateUrl)), PendingIntent.FLAG_IMMUTABLE)
                    post(ctx, 900, ctx.getString(R.string.app_name), ctx.getString(R.string.notify_app_update, tag), pi)
                }
            } else {
                p.knownUpdate = ""
            }
        } catch (e: Exception) {
            // no internet outside the tailnet: try again tomorrow
        }
    }

    /** Compares versions like 1.2.10 > 1.2.9. */
    fun isNewer(a: String, b: String): Boolean {
        val x = a.split('.', '-').map { it.toIntOrNull() ?: 0 }
        val y = b.split('.', '-').map { it.toIntOrNull() ?: 0 }
        for (i in 0 until maxOf(x.size, y.size)) {
            val u = x.getOrElse(i) { 0 }
            val v = y.getOrElse(i) { 0 }
            if (u != v) return u > v
        }
        return false
    }
}
