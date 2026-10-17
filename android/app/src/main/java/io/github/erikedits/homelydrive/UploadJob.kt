package io.github.erikedits.homelydrive

import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.ComponentName
import android.content.Context

/**
 * Uploads the queue when Android does not allow starting the upload service
 * from the background (e.g. a file saved through the Files app while
 * Homelydrive is closed). Android limits the run time of jobs; an interrupted
 * upload continues where it stopped.
 */
class UploadJob : JobService() {
    @Volatile private var stop = false

    override fun onStartJob(params: JobParameters): Boolean {
        if (UploadState.serviceRunning) return false
        stop = false
        Thread {
            val r = Uploader(this).runQueue { stop || hd.prefs.paused || UploadState.serviceRunning }
            // Server not reachable: let Android try again later.
            jobFinished(params, r == Uploader.Result.OFFLINE)
        }.apply { name = "hd-upload-job" }.start()
        return true
    }

    override fun onStopJob(params: JobParameters): Boolean {
        stop = true
        return true // reschedule: the rest is uploaded in the next run
    }

    companion object {
        private const val JOB_ID = 2

        fun schedule(ctx: Context) {
            val job = JobInfo.Builder(JOB_ID, ComponentName(ctx, UploadJob::class.java))
                .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
                .setBackoffCriteria(30_000, JobInfo.BACKOFF_POLICY_EXPONENTIAL)
                .setPersisted(true)
                .build()
            ctx.getSystemService(JobScheduler::class.java).schedule(job)
        }
    }
}
