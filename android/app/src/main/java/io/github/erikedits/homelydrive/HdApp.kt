package io.github.erikedits.homelydrive

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.job.JobInfo
import android.app.job.JobScheduler
import android.content.ComponentName
import android.content.Context

/** Application object: creates notification channels and shared singletons. */
class HdApp : Application() {
    lateinit var prefs: Prefs
        private set
    lateinit var db: Db
        private set

    override fun onCreate() {
        super.onCreate()
        instance = this
        prefs = Prefs(this)
        db = Db(this)
        createChannels()
        scheduleNotifyJob(this)
        // Android may refuse this while the app is in the background; then the
        // boot receiver, the job or the next app start does it.
        if (UploadService.wanted(this)) UploadService.start(this)
    }

    private fun createChannels() {
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CH_UPLOAD, getString(R.string.channel_upload), NotificationManager.IMPORTANCE_LOW).apply {
                description = getString(R.string.channel_upload_desc)
                setShowBadge(false)
            }
        )
        nm.createNotificationChannel(
            NotificationChannel(CH_EVENTS, getString(R.string.channel_events), NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = getString(R.string.channel_events_desc)
            }
        )
    }

    companion object {
        const val CH_UPLOAD = "uploads"
        const val CH_EVENTS = "events"
        const val JOB_NOTIFY = 1

        lateinit var instance: HdApp
            private set

        /** Polls the server for new shares etc. about every 15 minutes (no Google services needed). */
        fun scheduleNotifyJob(ctx: Context) {
            val js = ctx.getSystemService(JobScheduler::class.java)
            if (js.getPendingJob(JOB_NOTIFY) != null) return
            val job = JobInfo.Builder(JOB_NOTIFY, ComponentName(ctx, NotifyJob::class.java))
                .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
                .setPeriodic(15 * 60 * 1000L)
                .setPersisted(true)
                .build()
            js.schedule(job)
        }
    }
}

val Context.hd: HdApp get() = applicationContext as HdApp
