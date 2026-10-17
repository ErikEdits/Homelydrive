package io.github.erikedits.homelydrive

import android.app.Notification
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.database.ContentObserver
import android.graphics.drawable.Icon
import android.net.ConnectivityManager
import android.net.Network
import android.net.Uri
import android.os.Build
import android.os.FileObserver
import android.os.Handler
import android.os.HandlerThread
import android.os.IBinder
import android.os.PowerManager
import android.os.SystemClock
import android.provider.MediaStore
import android.util.Log
import java.io.File

/**
 * Foreground service: watches the chosen phone folders and uploads new files
 * right away, also after a reboot. It also listens for live server events
 * (new shares etc.) while it runs.
 */
class UploadService : Service() {
    private lateinit var uploader: Uploader
    private lateinit var uploadThread: HandlerThread
    private lateinit var uploadHandler: Handler
    private lateinit var scanThread: HandlerThread
    private lateinit var scanHandler: Handler

    private val watchers = HashMap<String, FolderWatcher>()
    private var netCallback: ConnectivityManager.NetworkCallback? = null
    private var mediaObserver: ContentObserver? = null
    private var eventsThread: Thread? = null
    private var wakeLock: PowerManager.WakeLock? = null
    @Volatile private var destroyed = false
    private var retryDelay = RETRY_MIN
    private var lastNotify = 0L
    private var lastWidget = 0L
    private val stateListener: () -> Unit = { onStateChanged() }

    private val prefs get() = hd.prefs
    private val db get() = hd.db

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        uploader = Uploader(this)
        startInForeground()
        UploadState.serviceRunning = true
        uploadThread = HandlerThread("hd-upload").apply { start() }
        uploadHandler = Handler(uploadThread.looper)
        scanThread = HandlerThread("hd-scan").apply { start() }
        scanHandler = Handler(scanThread.looper)
        wakeLock = getSystemService(PowerManager::class.java)
            .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "homelydrive:upload").apply { setReferenceCounted(false) }

        UploadState.listen(stateListener)
        registerNetwork()
        registerMediaObserver()
        setupWatchers()
        startEvents()
        scanHandler.post(periodicScan)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_PAUSE -> {
                prefs.paused = true
                UploadState.changed()
            }
            ACTION_RESUME -> {
                prefs.paused = false
                kick()
            }
            ACTION_UPLOAD_NOW -> {
                prefs.paused = false
                db.retryErrors()
                retryDelay = RETRY_MIN
                requestScan(0)
            }
            ACTION_RELOAD -> {
                setupWatchers()
                requestScan(0)
            }
            else -> kick()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        destroyed = true
        UploadState.serviceRunning = false
        UploadState.unlisten(stateListener)
        synchronized(watchers) {
            watchers.values.forEach { it.stop() }
            watchers.clear()
        }
        netCallback?.let { getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(it) }
        mediaObserver?.let { contentResolver.unregisterContentObserver(it) }
        eventsThread?.interrupt()
        uploadThread.quitSafely()
        scanThread.quitSafely()
        if (wakeLock?.isHeld == true) wakeLock?.release()
        UploadState.changed()
        UploadWidget.update(this)
        super.onDestroy()
    }

    // ----- work -----

    private val cycle = Runnable { runCycle() }

    /** Starts the upload loop (on the upload thread). */
    private fun kick() {
        uploadHandler.removeCallbacks(cycle)
        uploadHandler.post(cycle)
    }

    private fun runCycle() {
        if (destroyed) return
        if (!prefs.isLoggedIn) {
            stopSelf()
            return
        }
        if (prefs.paused) {
            UploadState.changed()
            return
        }
        if (db.pendingCount() == 0) {
            UploadState.waiting = null
            UploadState.changed()
            finishIfUnneeded()
            return
        }
        wakeLock?.acquire(60 * 60 * 1000L)
        val result = try {
            uploader.runQueue { destroyed || prefs.paused }
        } finally {
            if (wakeLock?.isHeld == true) wakeLock?.release()
        }
        when (result) {
            Uploader.Result.OFFLINE -> {
                UploadState.waiting = getString(R.string.status_unreachable)
                UploadState.retryAt = System.currentTimeMillis() + retryDelay
                uploadHandler.postDelayed(cycle, retryDelay)
                retryDelay = minOf(retryDelay * 2, RETRY_MAX)
            }
            Uploader.Result.LOGGED_OUT -> {
                UploadState.waiting = getString(R.string.err_login)
            }
            Uploader.Result.IDLE -> {
                UploadState.waiting = null
                retryDelay = RETRY_MIN
                finishIfUnneeded()
            }
            Uploader.Result.BUSY -> uploadHandler.postDelayed(cycle, 5000) // the background job is finishing a chunk
            Uploader.Result.STOPPED -> {}
        }
        UploadState.changed()
    }

    /** Stops the service when there is nothing to watch and nothing to upload. */
    private fun finishIfUnneeded() {
        if (prefs.folders.none { it.enabled } && db.pendingCount() == 0) stopSelf()
    }

    private val scanRunnable = Runnable { scanAll() }

    private fun requestScan(delay: Long) {
        scanHandler.removeCallbacks(scanRunnable)
        scanHandler.postDelayed(scanRunnable, delay)
    }

    private fun scanAll() {
        if (destroyed) return
        var unsettled = false
        for (folder in prefs.folders.filter { it.enabled }) {
            try {
                val r = uploader.scan(folder)
                if (r.unsettled) unsettled = true
                uploader.cleanup(folder)
            } catch (e: Exception) {
                Log.w(TAG, "scan ${folder.local}", e)
            }
        }
        if (unsettled) requestScan(Uploader.SETTLE_MS + 1000)
        UploadState.changed()
        kick()
    }

    private val periodicScan = object : Runnable {
        override fun run() {
            scanAll()
            uploader.cleanOutbox()
            scanHandler.postDelayed(this, SCAN_INTERVAL)
        }
    }

    private fun setupWatchers() {
        val wanted = prefs.folders.filter { it.enabled }.associateBy { it.local }
        synchronized(watchers) {
            for (path in watchers.keys.toList()) {
                if (path !in wanted) watchers.remove(path)?.stop()
            }
            for (path in wanted.keys) {
                if (path !in watchers) {
                    watchers[path] = FolderWatcher(File(path)) { requestScan(1500) }.also { w ->
                        scanHandler.post { w.start() }
                    }
                }
            }
        }
    }

    private fun registerNetwork() {
        val cm = getSystemService(ConnectivityManager::class.java)
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                // A new network (or the Tailscale VPN coming up): try again right away.
                retryDelay = RETRY_MIN
                uploadHandler.removeCallbacks(cycle)
                uploadHandler.postDelayed(cycle, 2000)
            }
        }
        try {
            cm.registerDefaultNetworkCallback(cb)
            netCallback = cb
        } catch (e: Exception) {
            Log.w(TAG, "network callback", e)
        }
    }

    /** FileObserver misses some writes on newer Android versions; media changes are a second signal. */
    private fun registerMediaObserver() {
        val obs = object : ContentObserver(scanHandler) {
            override fun onChange(selfChange: Boolean, uri: Uri?) {
                requestScan(3000)
            }
        }
        contentResolver.registerContentObserver(MediaStore.Files.getContentUri("external"), true, obs)
        mediaObserver = obs
    }

    /** Live server events: notifications show up immediately instead of every 15 minutes. */
    private fun startEvents() {
        eventsThread = Thread {
            var backoff = 5_000L
            while (!destroyed && !Thread.currentThread().isInterrupted) {
                val api = uploader.api() ?: break
                try {
                    Notifier.check(this)
                    api.events { type ->
                        backoff = 5_000L
                        if (type == "notification") Notifier.check(this)
                    }
                } catch (e: Exception) {
                    // offline or server restarted: try again later
                }
                try {
                    Thread.sleep(backoff)
                } catch (e: InterruptedException) {
                    break
                }
                backoff = minOf(backoff * 2, 5 * 60_000L)
            }
        }.apply {
            isDaemon = true
            name = "hd-events"
            start()
        }
    }

    // ----- notification & widget -----

    private fun onStateChanged() {
        val now = SystemClock.elapsedRealtime()
        if (now - lastNotify >= 1000) {
            lastNotify = now
            getSystemService(android.app.NotificationManager::class.java).notify(NOTIF_ID, buildNotification())
        }
        if (now - lastWidget >= 3000) {
            lastWidget = now
            UploadWidget.update(this)
        }
    }

    private fun startInForeground() {
        val n = buildNotification()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIF_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIF_ID, n)
        }
    }

    private fun buildNotification(): Notification {
        val s = statusText(this)
        val b = Notification.Builder(this, HdApp.CH_UPLOAD)
            .setSmallIcon(R.drawable.ic_notify)
            .setContentTitle(s.first)
            .setContentText(s.second)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setCategory(Notification.CATEGORY_PROGRESS)
            .setContentIntent(PendingIntent.getActivity(this, 0,
                Intent(this, MainActivity::class.java).putExtra(MainActivity.EXTRA_TAB, MainActivity.TAB_UPLOADS),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
        if (UploadState.phase != UploadState.Phase.IDLE && UploadState.total > 0) {
            val pct = (UploadState.sent * 1000 / UploadState.total).toInt()
            b.setProgress(1000, pct, UploadState.phase == UploadState.Phase.HASHING)
        }
        if (prefs.paused) {
            b.addAction(action(R.drawable.ic_play, R.string.action_resume, ACTION_RESUME, 1))
        } else {
            b.addAction(action(R.drawable.ic_pause, R.string.action_pause, ACTION_PAUSE, 2))
        }
        b.addAction(action(R.drawable.ic_refresh, R.string.action_upload_now, ACTION_UPLOAD_NOW, 3))
        return b.build()
    }

    private fun action(iconRes: Int, label: Int, act: String, code: Int) =
        Notification.Action.Builder(Icon.createWithResource(this, iconRes), getString(label), servicePending(this, act, code)).build()

    /**
     * A small file-system watcher for a folder tree (FileObserver per folder).
     * New subfolders are added as they appear.
     */
    private class FolderWatcher(private val root: File, private val onChange: () -> Unit) {
        private val observers = HashMap<String, FileObserver>()
        @Volatile private var stopped = false

        fun start() = addTree(root)

        fun stop() {
            stopped = true
            synchronized(observers) {
                observers.values.forEach { it.stopWatching() }
                observers.clear()
            }
        }

        private fun addTree(dir: File) {
            if (stopped || !dir.isDirectory) return
            synchronized(observers) {
                if (observers.size >= MAX_WATCHES || observers.containsKey(dir.absolutePath)) return
                @Suppress("DEPRECATION")
                val o = object : FileObserver(dir.absolutePath, MASK) {
                    override fun onEvent(event: Int, name: String?) {
                        if (name == null) return
                        val ev = event and ALL_EVENTS
                        if (Uploader.isJunk(name)) return
                        if (ev and (CREATE or MOVED_TO) != 0) {
                            val f = File(dir, name)
                            if (f.isDirectory) addTree(f)
                        }
                        if (ev and (CLOSE_WRITE or MOVED_TO) != 0) onChange()
                    }
                }
                o.startWatching()
                observers[dir.absolutePath] = o
            }
            dir.listFiles()?.forEach { if (it.isDirectory && !Uploader.isJunk(it.name)) addTree(it) }
        }

        companion object {
            const val MASK = FileObserver.CLOSE_WRITE or FileObserver.MOVED_TO or FileObserver.CREATE
            const val MAX_WATCHES = 3000
        }
    }

    companion object {
        private const val TAG = "HdUpload"
        const val NOTIF_ID = 1
        const val ACTION_PAUSE = "io.github.erikedits.homelydrive.PAUSE"
        const val ACTION_RESUME = "io.github.erikedits.homelydrive.RESUME"
        const val ACTION_UPLOAD_NOW = "io.github.erikedits.homelydrive.UPLOAD_NOW"
        const val ACTION_RELOAD = "io.github.erikedits.homelydrive.RELOAD"
        private const val RETRY_MIN = 15_000L
        private const val RETRY_MAX = 10 * 60_000L
        private const val SCAN_INTERVAL = 15 * 60_000L

        /** Starts (or pokes) the service. Fails quietly where Android forbids it (app in background). */
        fun start(ctx: Context, action: String? = null): Boolean {
            val p = ctx.hd.prefs
            if (!p.isLoggedIn) return false
            return try {
                ctx.startForegroundService(Intent(ctx, UploadService::class.java).setAction(action))
                true
            } catch (e: Exception) {
                // Android 12+ forbids this from the background: upload with a job instead.
                Log.w(TAG, "cannot start service", e)
                if (ctx.hd.db.pendingCount() > 0) UploadJob.schedule(ctx)
                false
            }
        }

        /** Whether the service should run: folders to watch or files waiting. */
        fun wanted(ctx: Context): Boolean {
            val p = ctx.hd.prefs
            return p.isLoggedIn && (p.folders.any { it.enabled } || ctx.hd.db.pendingCount() > 0)
        }

        fun servicePending(ctx: Context, action: String, code: Int): PendingIntent =
            PendingIntent.getForegroundService(ctx, code, Intent(ctx, UploadService::class.java).setAction(action),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

        /** Title and text describing the current state (notification, widget, app). */
        fun statusText(ctx: Context): Pair<String, String> {
            val p = ctx.hd.prefs
            val pending = ctx.hd.db.pendingCount()
            val folders = p.folders.count { it.enabled }
            val st = UploadState
            return when {
                !p.isLoggedIn -> ctx.getString(R.string.status_logged_out) to ""
                p.paused -> ctx.getString(R.string.status_paused) to
                    ctx.resources.getQuantityString(R.plurals.files_waiting, pending, pending)
                st.phase == UploadState.Phase.HASHING -> ctx.getString(R.string.status_checking, st.current) to
                    ctx.resources.getQuantityString(R.plurals.files_waiting, pending, pending)
                st.phase == UploadState.Phase.UPLOADING -> ctx.getString(R.string.status_uploading, st.current) to
                    "${formatSize(st.sent)} / ${formatSize(st.total)} · " +
                    ctx.resources.getQuantityString(R.plurals.files_waiting, pending, pending)
                st.waiting != null && pending > 0 -> st.waiting!! to
                    ctx.resources.getQuantityString(R.plurals.files_waiting, pending, pending)
                pending > 0 -> ctx.getString(R.string.status_preparing) to
                    ctx.resources.getQuantityString(R.plurals.files_waiting, pending, pending)
                else -> ctx.getString(R.string.status_done) to
                    ctx.resources.getQuantityString(R.plurals.folders_watched, folders, folders)
            }
        }
    }
}
