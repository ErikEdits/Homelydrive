package io.github.erikedits.homelydrive

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.widget.RemoteViews

/** Home screen widget: upload status and an "upload now" button. */
class UploadWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) {
        update(ctx)
    }

    companion object {
        fun update(ctx: Context) {
            val manager = AppWidgetManager.getInstance(ctx) ?: return
            val ids = manager.getAppWidgetIds(ComponentName(ctx, UploadWidget::class.java))
            if (ids.isEmpty()) return
            val (title, text) = UploadService.statusText(ctx)
            val v = RemoteViews(ctx.packageName, R.layout.widget)
            v.setTextViewText(R.id.widget_title, title)
            v.setTextViewText(R.id.widget_status, text.ifEmpty { ctx.getString(R.string.app_name) })
            v.setOnClickPendingIntent(R.id.widget_root, PendingIntent.getActivity(ctx, 20,
                Intent(ctx, MainActivity::class.java).putExtra(MainActivity.EXTRA_TAB, MainActivity.TAB_UPLOADS),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
            val button = if (ctx.hd.prefs.isLoggedIn) {
                UploadService.servicePending(ctx, UploadService.ACTION_UPLOAD_NOW, 21)
            } else {
                PendingIntent.getActivity(ctx, 22, Intent(ctx, LoginActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
            }
            v.setOnClickPendingIntent(R.id.widget_button, button)
            manager.updateAppWidget(ids, v)
        }
    }
}
