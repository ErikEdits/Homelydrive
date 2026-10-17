package io.github.erikedits.homelydrive

import android.app.job.JobParameters
import android.app.job.JobService

/**
 * Runs about every 15 minutes: fetches server notifications, checks for an
 * app update and restarts the upload service if Android stopped it.
 */
class NotifyJob : JobService() {
    override fun onStartJob(params: JobParameters): Boolean {
        Thread {
            try {
                if (!UploadState.serviceRunning) Notifier.check(this)
                Notifier.checkUpdate(this)
                if (!UploadState.serviceRunning && UploadService.wanted(this)) UploadService.start(this)
            } finally {
                jobFinished(params, false)
            }
        }.start()
        return true
    }

    override fun onStopJob(params: JobParameters): Boolean = false
}
