package io.github.erikedits.homelydrive

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/** Starts uploading again after a reboot or an app update. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> {
                HdApp.scheduleNotifyJob(ctx)
                if (UploadService.wanted(ctx)) UploadService.start(ctx)
            }
        }
    }
}
