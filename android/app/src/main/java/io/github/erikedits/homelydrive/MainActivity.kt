package io.github.erikedits.homelydrive

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.app.DownloadManager
import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.PowerManager
import android.provider.Settings
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.CheckBox
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.Switch
import android.widget.TextView

/**
 * Main screen with three tabs:
 *  - Files: the server's web interface (all Drive features: browse, search,
 *    share, photos, trash, ...), logged in automatically.
 *  - Upload: the phone folders that are uploaded, queue and history.
 *  - More: scanner, target folders, permissions, account, updates.
 */
class MainActivity : Activity() {
    private lateinit var content: FrameLayout
    private lateinit var tabBar: LinearLayout
    private val tabViews = HashMap<Int, View>()
    private var tab = TAB_FILES

    private var webView: WebView? = null
    private var webError: View? = null
    private var webErrorText: TextView? = null
    private var fileCallback: ValueCallback<Array<Uri>>? = null
    private var chooserParams: WebChromeClient.FileChooserParams? = null

    private lateinit var uploadsList: LinearLayout
    private lateinit var moreList: LinearLayout
    private var shownCounts = -1 to -1
    private val stateListener: () -> Unit = {
        if (tab == TAB_UPLOADS) {
            // a file finished or was added: rebuild the lists, otherwise only the status
            val counts = hd.db.pendingCount() to UploadState.uploadedCount
            if (counts != shownCounts) refreshUploads() else refreshStatus()
        }
    }
    private var statusCard: LinearLayout? = null

    private val prefs get() = hd.prefs

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (!prefs.isLoggedIn) {
            startActivity(Intent(this, LoginActivity::class.java))
            finish()
            return
        }
        val root = vertical(this) { setBackgroundColor(col(R.color.bg)) }
        content = FrameLayout(this)
        root.addView(content, lp(MATCH, 0, 1f))
        root.addView(divider(this))
        tabBar = horizontal(this) {
            setBackgroundColor(col(R.color.surface))
            addTab(this, TAB_FILES, R.drawable.ic_folder, R.string.tab_files)
            addTab(this, TAB_UPLOADS, R.drawable.ic_upload, R.string.tab_upload)
            addTab(this, TAB_MORE, R.drawable.ic_settings, R.string.tab_more)
        }
        root.addView(tabBar)
        applyInsets(root)
        setContentView(root)

        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQ_NOTIFY)
        }
        HdApp.scheduleNotifyJob(this)
        if (UploadService.wanted(this)) UploadService.start(this)
        runBackground { Notifier.checkUpdate(this) }

        val start = intent.getIntExtra(EXTRA_TAB, savedInstanceState?.getInt("tab") ?: if (prefs.folders.isEmpty()) TAB_UPLOADS else TAB_FILES)
        select(start)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        if (intent.hasExtra(EXTRA_TAB)) select(intent.getIntExtra(EXTRA_TAB, TAB_FILES))
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putInt("tab", tab)
    }

    override fun onResume() {
        super.onResume()
        if (!prefs.isLoggedIn) {
            startActivity(Intent(this, LoginActivity::class.java))
            finish()
            return
        }
        UploadState.listen(stateListener)
        suggestions = null // look again (new photo folders, access granted meanwhile)
        if (tab == TAB_UPLOADS) refreshUploads()
        if (tab == TAB_MORE) refreshMore()
    }

    override fun onPause() {
        super.onPause()
        UploadState.unlisten(stateListener)
        CookieManager.getInstance().flush()
    }

    override fun onDestroy() {
        webView?.destroy()
        super.onDestroy()
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        val wv = webView
        if (tab == TAB_FILES && wv != null && wv.canGoBack()) {
            wv.goBack()
            return
        }
        @Suppress("DEPRECATION")
        super.onBackPressed()
    }

    // ----- tabs -----

    private fun addTab(bar: LinearLayout, id: Int, iconRes: Int, label: Int) {
        val v = vertical(this) {
            gravity = Gravity.CENTER
            setPadding(0, dp(8), 0, dp(8))
            background = selectable(context, true)
            addView(icon(context, iconRes, 24).apply { tag = "icon" })
            addView(text(context, getString(label), 12f).apply {
                tag = "label"
                gravity = Gravity.CENTER
            })
            setOnClickListener { select(id) }
        }
        bar.addView(v, lp(0, WRAP, 1f))
        tabViews[id] = v
    }

    private fun select(id: Int) {
        tab = id
        for ((tid, v) in tabViews) {
            val on = tid == id
            val color = col(if (on) R.color.brand else R.color.text2)
            (v.findViewWithTag<View>("icon") as android.widget.ImageView).imageTintList = android.content.res.ColorStateList.valueOf(color)
            (v.findViewWithTag<View>("label") as TextView).setTextColor(color)
        }
        content.removeAllViews()
        when (id) {
            TAB_FILES -> content.addView(filesView())
            TAB_UPLOADS -> {
                content.addView(uploadsView())
                refreshUploads()
            }
            else -> {
                content.addView(moreView())
                refreshMore()
            }
        }
    }

    // ----- Files tab: the web interface -----

    private var filesRoot: View? = null

    @SuppressLint("SetJavaScriptEnabled")
    private fun filesView(): View {
        filesRoot?.let { return it }
        val frame = FrameLayout(this)
        val wv = WebView(this)
        val server = prefs.server
        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setCookie(server, "hd_session=${prefs.token}; Path=/; Max-Age=315360000")
            flush()
        }
        wv.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            mediaPlaybackRequiresUserGesture = false
            allowFileAccess = false
            allowContentAccess = false
            userAgentString = "$userAgentString HomelydriveAndroid/${BuildConfig.VERSION_NAME}"
        }
        wv.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val u = request.url
                if (sameServer(u)) return false
                try {
                    startActivity(Intent(Intent.ACTION_VIEW, u))
                } catch (e: ActivityNotFoundException) {
                    // nothing can open it
                }
                return true
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame) showWebError(error.description.toString())
            }

            override fun onPageFinished(view: WebView, url: String) {
                if (webErrorText?.tag != "error") webError?.visibility = View.GONE
            }
        }
        wv.webChromeClient = object : WebChromeClient() {
            private var custom: View? = null

            override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
                fileCallback?.onReceiveValue(null)
                fileCallback = callback
                chooserParams = params
                // Uploads into a drive folder go through the app's own explorer (files and whole
                // folders, several at once); Android's picker stays available from there.
                val t = webUploadTarget(view.url)
                if (t != null) {
                    @Suppress("DEPRECATION")
                    startActivityForResult(ExplorerActivity.intent(this@MainActivity, ExplorerActivity.MODE_UPLOAD)
                        .putExtra(ExplorerActivity.EXTRA_SPACE, t.first)
                        .putExtra(ExplorerActivity.EXTRA_PATH, t.second)
                        .putExtra(ExplorerActivity.EXTRA_LABEL, targetLabel(t.first, t.second))
                        .putExtra(ExplorerActivity.EXTRA_SYSTEM_PICKER, true), REQ_EXPLORER_WEB)
                    return true
                }
                return openSystemChooser(params)
            }

            // Full screen videos
            override fun onShowCustomView(view: View, callback: CustomViewCallback) {
                custom = view
                (window.decorView as ViewGroup).addView(view, FrameLayout.LayoutParams(MATCH, MATCH))
            }

            override fun onHideCustomView() {
                custom?.let { (window.decorView as ViewGroup).removeView(it) }
                custom = null
            }
        }
        wv.setDownloadListener { url, _, disposition, mime, _ -> download(url, disposition, mime) }
        frame.addView(wv, FrameLayout.LayoutParams(MATCH, MATCH))

        val err = vertical(this, 32) {
            gravity = Gravity.CENTER
            setBackgroundColor(col(R.color.bg))
            visibility = View.GONE
            addView(icon(context, R.drawable.ic_error, 48, R.color.text2))
            addView(text(context, getString(R.string.server_unreachable_title), 18f, true).apply { gravity = Gravity.CENTER }, lp().margins(context, t = 16))
            webErrorText = text(context, "", 14f, color = R.color.text2).apply { gravity = Gravity.CENTER }
            addView(webErrorText, lp().margins(context, t = 8, b = 16))
            addView(button(context, getString(R.string.retry)) {
                webErrorText?.tag = null
                wv.reload()
            }, lp(WRAP, WRAP))
            val ts = packageManager.getLaunchIntentForPackage(LoginActivity.TAILSCALE)
            if (ts != null) addView(button(context, getString(R.string.open_tailscale), primary = false) { startActivity(ts) }, lp(WRAP, WRAP).margins(context, t = 8))
        }
        webError = err
        frame.addView(err, FrameLayout.LayoutParams(MATCH, MATCH))

        wv.loadUrl("$server/")
        webView = wv
        filesRoot = frame
        return frame
    }

    /** Android's own file picker for the web interface. */
    private fun openSystemChooser(params: WebChromeClient.FileChooserParams): Boolean {
        val i = params.createIntent()
        if (params.mode == WebChromeClient.FileChooserParams.MODE_OPEN_MULTIPLE) i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
        return try {
            @Suppress("DEPRECATION")
            startActivityForResult(i, REQ_FILE)
            true
        } catch (e: ActivityNotFoundException) {
            fileCallback = null
            false
        }
    }

    /**
     * The server folder an upload in the web interface goes to, from its address
     * (#/drive/<space>/<path>); null where the app's explorer does not fit (vault,
     * public link pages).
     */
    private fun webUploadTarget(url: String?): Pair<String, String>? {
        val u = Uri.parse(url ?: return null)
        if (!sameServer(u)) return null
        val p = u.path ?: "/"
        if (p != "/" && p != "/index.html") return null
        val parts = (u.encodedFragment ?: "").substringBefore('?').split('/').filter { it.isNotEmpty() }.map { Uri.decode(it) }
        return when (parts.firstOrNull() ?: "drive") {
            "drive" -> parts.getOrElse(1) { "home" } to parts.drop(2).joinToString("/")
            "vault" -> null
            else -> "home" to ""
        }
    }

    private fun sameServer(u: Uri): Boolean {
        val s = Uri.parse(prefs.server)
        return u.scheme == s.scheme && u.host == s.host && u.port == s.port
    }

    private fun showWebError(detail: String) {
        webErrorText?.text = getString(R.string.server_unreachable_text, prefs.server) + "\n\n" + detail
        webErrorText?.tag = "error"
        webError?.visibility = View.VISIBLE
    }

    private fun download(url: String, disposition: String?, mime: String?) {
        val uri = Uri.parse(url)
        if (uri.path?.endsWith("/files/zip") == true && uri.query.isNullOrEmpty()) {
            toast(getString(R.string.zip_too_many))
            return
        }
        try {
            val name = URLUtil.guessFileName(url, disposition, mime)
            val req = DownloadManager.Request(uri)
                .addRequestHeader("Cookie", CookieManager.getInstance().getCookie(url) ?: "")
                .setTitle(name)
                .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                .setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name)
            if (!mime.isNullOrEmpty()) req.setMimeType(mime)
            getSystemService(DownloadManager::class.java).enqueue(req)
            toast(getString(R.string.download_started, name))
        } catch (e: Exception) {
            toast(errorText(e))
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        when (requestCode) {
            REQ_FILE -> {
                val cb = fileCallback ?: return
                fileCallback = null
                if (resultCode != RESULT_OK || data == null) {
                    cb.onReceiveValue(null)
                    return
                }
                val list = ArrayList<Uri>()
                data.clipData?.let { cd -> for (k in 0 until cd.itemCount) list += cd.getItemAt(k).uri }
                if (list.isEmpty()) data.data?.let { list += it }
                cb.onReceiveValue(list.toTypedArray())
            }
            REQ_EXPLORER_WEB -> {
                val p = chooserParams
                chooserParams = null
                if (resultCode == ExplorerActivity.RESULT_SYSTEM_PICKER && p != null && openSystemChooser(p)) return
                // uploaded by the app (or cancelled): the web page refreshes by itself when files arrive
                fileCallback?.onReceiveValue(null)
                fileCallback = null
            }
            REQ_EXPLORER -> if (resultCode == RESULT_OK) refreshUploads()
            REQ_LOCAL_FOLDER -> if (resultCode == RESULT_OK && data != null) {
                addFolders(data.getStringArrayListExtra(ExplorerActivity.EXTRA_PATHS) ?: return)
            }
            REQ_REMOTE_FOLDER -> if (resultCode == RESULT_OK && data != null) {
                val id = data.getStringExtra(EXTRA_FOLDER_ID) ?: return
                val space = data.getStringExtra(FolderPickerActivity.EXTRA_SPACE) ?: "home"
                val path = data.getStringExtra(FolderPickerActivity.EXTRA_PATH) ?: ""
                changeTarget(id, space, path)
            }
            REQ_SHARE_TARGET, REQ_SCAN_TARGET -> if (resultCode == RESULT_OK && data != null) {
                val space = data.getStringExtra(FolderPickerActivity.EXTRA_SPACE) ?: "home"
                val path = data.getStringExtra(FolderPickerActivity.EXTRA_PATH) ?: ""
                if (requestCode == REQ_SHARE_TARGET) {
                    prefs.shareSpace = space
                    prefs.shareTarget = path
                } else {
                    prefs.scanSpace = space
                    prefs.scanTarget = path
                }
                refreshMore()
            }
        }
    }

    // ----- Upload tab -----

    private var uploadsRoot: View? = null

    private fun uploadsView(): View {
        uploadsRoot?.let { return it }
        uploadsList = vertical(this, 16)
        val sv = ScrollView(this).apply { addView(uploadsList) }
        uploadsRoot = sv
        return sv
    }

    private fun refreshUploads() {
        if (uploadsRoot == null) return
        val l = uploadsList
        l.removeAllViews()
        shownCounts = hd.db.pendingCount() to UploadState.uploadedCount
        l.addView(text(this, getString(R.string.tab_upload), 24f, true).apply { setPadding(dp(4), dp(8), 0, dp(12)) })

        if (!hasStorageAccess()) {
            l.addView(card(this) {
                addView(text(context, getString(R.string.storage_access_title), 16f, true))
                addView(text(context, getString(R.string.storage_access_text), 14f, color = R.color.text2), lp().margins(context, t = 4, b = 12))
                addView(button(context, getString(R.string.allow)) { requestStorageAccess() }, lp(WRAP, WRAP))
            })
        }

        statusCard = card(this) {}
        l.addView(statusCard)
        refreshStatus()

        suggestionsCard()?.let { l.addView(it) }

        l.addView(heading(this, getString(R.string.phone_folders)))
        val folders = prefs.folders
        if (folders.isEmpty()) {
            l.addView(text(this, getString(R.string.no_folders), 14f, color = R.color.text2).apply { setPadding(dp(4), 0, dp(4), dp(12)) })
        }
        val db = hd.db
        for (f in folders) {
            l.addView(card(this) {
                setPadding(dp(8), dp(8), dp(8), dp(8))
                addView(horizontal(context) {
                    addView(vertical(context) {
                        setPadding(dp(8), dp(4), 0, dp(4))
                        addView(text(context, Storage.display(context, f.local), 15f, true, lines = 2))
                        addView(text(context, "→ " + targetLabel(f.space, f.remote), 14f, color = R.color.text2, lines = 2))
                        val n = db.seenCount(f.id)
                        val extra = when (f.deleteAfterDays) {
                            -1 -> ""
                            0 -> " · " + getString(R.string.delete_now_short)
                            else -> " · " + getString(R.string.delete_after_short, f.deleteAfterDays)
                        }
                        addView(text(context, resources.getQuantityString(R.plurals.uploaded_files, n, n) + extra, 13f, color = R.color.text2))
                    }, lp(0, WRAP, 1f))
                    addView(Switch(context).apply {
                        isChecked = f.enabled
                        setOnCheckedChangeListener { _, on ->
                            prefs.updateFolder(f.id) { it.copy(enabled = on) }
                            if (!on) hd.db.dropFolder(f.id, forgetSeen = false)
                            folderSettingsChanged()
                        }
                    })
                })
                background = rounded(col(R.color.surface), dp(16).toFloat(), col(R.color.divider), dp(1))
                setOnClickListener { editFolder(f.id) }
            })
        }
        l.addView(button(this, getString(R.string.add_folder)) { pickLocalFolders() }, lp().margins(this, t = 4))
        l.addView(button(this, getString(R.string.upload_files_from_phone), primary = false) {
            @Suppress("DEPRECATION")
            startActivityForResult(ExplorerActivity.intent(this, ExplorerActivity.MODE_UPLOAD), REQ_EXPLORER)
        }, lp().margins(this, t = 8))
        l.addView(text(this, getString(R.string.archive_mode_hint), 13f, color = R.color.text2).apply {
            setPadding(dp(4), dp(12), dp(4), 0)
        })

        // queue & history
        val pending = db.pending(30)
        if (pending.isNotEmpty()) {
            l.addView(heading(this, getString(R.string.queue)))
            l.addView(card(this) {
                setPadding(0, dp(4), 0, dp(4))
                for (q in pending) addView(queueRow(q))
            })
        }
        val history = db.history(60)
        if (history.isNotEmpty()) {
            l.addView(horizontal(this) {
                addView(heading(context, getString(R.string.history)), lp(0, WRAP, 1f))
                addView(text(context, getString(R.string.clear), 14f, true, R.color.brand).apply {
                    setPadding(dp(12), dp(20), dp(8), dp(8))
                    setOnClickListener {
                        runBackground {
                            Uploader(this@MainActivity).clearHistory()
                            runMain { refreshUploads() }
                        }
                    }
                })
            })
            l.addView(card(this) {
                setPadding(0, dp(4), 0, dp(4))
                for (q in history) addView(queueRow(q))
            })
        }
    }

    // ----- folders found automatically (like Google Photos) -----

    private var suggestions: List<Storage.Found>? = null
    private var suggestionsLoading = false
    private val suggestionChecks = HashMap<String, Boolean>()
    private var suggestionsExpanded = false

    private fun loadSuggestions() {
        if (suggestionsLoading || !hasStorageAccess()) return
        suggestionsLoading = true
        async({ Storage.suggestedFolders(this) }) { r ->
            suggestionsLoading = false
            suggestions = r.getOrDefault(emptyList())
            if (tab == TAB_UPLOADS) refreshUploads()
        }
    }

    /** "Found on this phone": photo/video folders and downloads with a tick to add them at once. */
    private fun suggestionsCard(): View? {
        val all = suggestions
        if (all == null) {
            loadSuggestions()
            return null
        }
        val added = prefs.folders.map { it.local }.toSet()
        val dismissed = prefs.dismissedSuggestions
        val list = all.filter { it.dir.absolutePath !in added && it.dir.absolutePath !in dismissed }
        if (list.isEmpty()) return null
        list.forEach { suggestionChecks.getOrPut(it.dir.absolutePath) { it.recommended } }
        lateinit var addButton: Button
        fun ticked() = list.filter { suggestionChecks[it.dir.absolutePath] == true }
        fun updateButton() {
            val n = ticked().size
            addButton.text = getString(R.string.found_add, n)
            addButton.isEnabled = n > 0
            addButton.alpha = if (n > 0) 1f else 0.5f
        }
        return card(this) {
            addView(horizontal(context) {
                addView(icon(context, R.drawable.ic_image, 24, R.color.brand))
                addView(text(context, getString(R.string.found_title), 16f, true), lp(0, WRAP, 1f).margins(context, l = 12))
            })
            addView(text(context, getString(R.string.found_text), 13f, color = R.color.text2), lp().margins(context, t = 4, b = 6))
            val shown = if (suggestionsExpanded) list else list.take(6)
            for (f in shown) {
                val path = f.dir.absolutePath
                val cb = CheckBox(context).apply {
                    isChecked = suggestionChecks[path] == true
                    isClickable = false
                    isFocusable = false
                }
                addView(horizontal(context) {
                    setPadding(0, dp(4), 0, dp(4))
                    addView(cb)
                    addView(vertical(context) {
                        addView(text(context, f.label, 15f, lines = 1))
                        addView(text(context, f.info(context), 12f, color = R.color.text2, lines = 2))
                    }, lp(0, WRAP, 1f).margins(context, l = 6))
                    background = selectable(context)
                    setOnClickListener {
                        suggestionChecks[path] = suggestionChecks[path] != true
                        cb.isChecked = suggestionChecks[path] == true
                        updateButton()
                    }
                })
            }
            if (list.size > shown.size) {
                addView(text(context, getString(R.string.found_more, list.size), 14f, true, R.color.brand).apply {
                    setPadding(dp(4), dp(10), dp(4), dp(6))
                    setOnClickListener {
                        suggestionsExpanded = true
                        refreshUploads()
                    }
                })
            }
            addView(horizontal(context) {
                setPadding(0, dp(10), 0, 0)
                addButton = button(context, "") { addFolders(ticked().map { it.dir.absolutePath }) }
                addView(addButton, lp(0, WRAP, 1f))
                addView(text(context, getString(R.string.found_hide), 14f, true, R.color.text2).apply {
                    setPadding(dp(14), dp(10), dp(6), dp(10))
                    setOnClickListener {
                        prefs.dismissedSuggestions = dismissed + list.map { it.dir.absolutePath }
                        refreshUploads()
                    }
                })
            })
            updateButton()
        }
    }

    private fun refreshStatus() {
        val c = statusCard ?: return
        c.removeAllViews()
        val (title, sub) = UploadService.statusText(this)
        c.addView(horizontal(this) {
            val st = UploadState
            val busy = st.phase != UploadState.Phase.IDLE
            addView(icon(context, when {
                prefs.paused -> R.drawable.ic_pause
                busy -> R.drawable.ic_upload
                st.waiting != null -> R.drawable.ic_error
                else -> R.drawable.ic_check
            }, 28, R.color.brand))
            addView(vertical(context) {
                addView(text(context, title, 16f, true, lines = 2))
                if (sub.isNotEmpty()) addView(text(context, sub, 13f, color = R.color.text2, lines = 2))
            }, lp(0, WRAP, 1f).margins(context, l = 14))
        })
        val st = UploadState
        if (st.phase != UploadState.Phase.IDLE && st.total > 0) {
            c.addView(ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
                max = 1000
                progress = (st.sent * 1000 / st.total).toInt()
            }, lp().margins(this, t = 10))
        }
        val errors = hd.db.errorCount()
        c.addView(horizontal(this) {
            setPadding(0, dp(12), 0, 0)
            if (prefs.paused) {
                addView(button(context, getString(R.string.action_resume)) { serviceAction(UploadService.ACTION_RESUME) }, lp(0, WRAP, 1f).margins(context, r = 6))
            } else {
                addView(button(context, getString(R.string.action_pause), primary = false) { serviceAction(UploadService.ACTION_PAUSE) }, lp(0, WRAP, 1f).margins(context, r = 6))
            }
            addView(button(context, getString(R.string.action_upload_now), primary = !prefs.paused) { serviceAction(UploadService.ACTION_UPLOAD_NOW) },
                lp(0, WRAP, 1f).margins(context, l = 6))
        })
        if (errors > 0) {
            c.addView(text(this, resources.getQuantityString(R.plurals.upload_errors, errors, errors), 13f, color = R.color.danger).apply {
                setPadding(0, dp(10), 0, 0)
            })
        }
    }

    private fun serviceAction(action: String) {
        if (action == UploadService.ACTION_PAUSE) prefs.paused = true
        if (action == UploadService.ACTION_RESUME || action == UploadService.ACTION_UPLOAD_NOW) prefs.paused = false
        UploadService.start(this, action)
        refreshStatus()
    }

    private fun queueRow(q: QueueItem): View {
        val (iconRes, tint) = when (q.status) {
            "done" -> R.drawable.ic_check to R.color.success
            "error" -> R.drawable.ic_error to R.color.danger
            "skipped" -> R.drawable.ic_history to R.color.text2
            else -> R.drawable.ic_upload to R.color.text2
        }
        val sub = when (q.status) {
            "pending" -> formatSize(q.size) + " → " + targetLabel(q.space, q.remote.substringBeforeLast('/', ""))
            "done" -> formatTime(q.doneAt) + " · " + formatSize(q.size) + " → " + targetLabel(q.space, (q.finalPath ?: q.remote).substringBeforeLast('/', ""))
            else -> formatTime(q.doneAt) + " · " + (q.error ?: "")
        }
        return horizontal(this) {
            setPadding(dp(14), dp(10), dp(14), dp(10))
            addView(icon(context, iconRes, 20, tint))
            addView(vertical(context) {
                addView(text(context, if (q.status == "done") (q.finalPath ?: q.remote).substringAfterLast('/') else q.name, 14f, lines = 1))
                addView(text(context, sub, 12f, color = R.color.text2, lines = 2))
            }, lp(0, WRAP, 1f).margins(context, l = 12))
            background = selectable(context)
            setOnClickListener { queueItemMenu(q) }
        }
    }

    private fun queueItemMenu(q: QueueItem) {
        val actions = ArrayList<Pair<String, () -> Unit>>()
        when (q.status) {
            "done" -> actions += getString(R.string.share_link) to { shareLink(q.space, q.finalPath ?: q.remote) }
            "error" -> actions += getString(R.string.retry) to {
                hd.db.retry(q.id)
                UploadService.start(this, UploadService.ACTION_UPLOAD_NOW)
                refreshUploads()
            }
            "pending" -> if (q.kind != "folder") actions += getString(R.string.cancel_upload) to {
                hd.db.cancelPending(q.id)
                if (q.temp) java.io.File(q.local).delete()
                refreshUploads()
            }
        }
        if (actions.isEmpty()) return
        AlertDialog.Builder(this)
            .setTitle(q.name)
            .setItems(actions.map { it.first }.toTypedArray()) { _, which -> actions[which].second() }
            .show()
    }

    private fun shareLink(space: String, path: String) {
        async({ Api(prefs.server, prefs.token).createLink(space, path) }) { r ->
            r.onSuccess { link ->
                getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Homelydrive", link))
                startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, link), null))
            }.onFailure { toast(errorText(it)) }
        }
    }

    // ----- upload folders -----

    private fun hasStorageAccess() = Storage.hasAccess(this)

    private fun requestStorageAccess() = Storage.requestAccess(this, REQ_STORAGE)

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode == REQ_STORAGE) refreshUploads()
    }

    /** Opens the explorer to choose one or more phone folders that are uploaded automatically. */
    private fun pickLocalFolders() {
        @Suppress("DEPRECATION")
        startActivityForResult(ExplorerActivity.intent(this, ExplorerActivity.MODE_FOLDERS), REQ_LOCAL_FOLDER)
    }

    private fun addFolders(paths: List<String>) {
        val existing = prefs.folders
        val fresh = paths.distinct().filter { p -> existing.none { it.local == p } }
        if (fresh.isEmpty()) {
            toast(getString(R.string.folder_exists))
            return
        }
        val added = fresh.map { p -> UploadFolder(local = p, remote = Uploader.joinPath(prefs.deviceName, p.trimEnd('/').substringAfterLast('/'))) }
        prefs.folders = existing + added
        folderSettingsChanged()
        refreshUploads()
        if (added.size == 1) {
            editFolder(added[0].id, isNew = true)
        } else {
            toast(resources.getQuantityString(R.plurals.folders_added, added.size, added.size))
        }
    }

    private fun editFolder(id: String, isNew: Boolean = false) {
        val f = prefs.folders.find { it.id == id } ?: return
        val box = vertical(this, 20)
        box.addView(text(this, getString(R.string.phone_folder), 13f, true, R.color.text2))
        box.addView(text(this, Storage.display(this, f.local), 15f), lp().margins(this, b = 12))
        box.addView(text(this, getString(R.string.target_folder), 13f, true, R.color.text2))
        box.addView(horizontal(this) {
            addView(text(context, targetLabel(f.space, f.remote), 15f, lines = 2), lp(0, WRAP, 1f))
            addView(text(context, getString(R.string.change), 14f, true, R.color.brand).apply { setPadding(dp(12), dp(8), 0, dp(8)) })
            setOnClickListener {
                @Suppress("DEPRECATION")
                startActivityForResult(FolderPickerActivity.intent(this@MainActivity, getString(R.string.target_folder))
                    .putExtra(EXTRA_FOLDER_ID, f.id), REQ_REMOTE_FOLDER)
            }
        }, lp().margins(this, b = 12))
        box.addView(text(this, getString(R.string.delete_after_upload), 13f, true, R.color.text2))
        val options = intArrayOf(-1, 0, 7, 30, 90)
        val labels = options.map {
            when (it) {
                -1 -> getString(R.string.delete_never)
                0 -> getString(R.string.delete_now)
                else -> getString(R.string.delete_after_days, it)
            }
        }
        val current = options.indexOf(f.deleteAfterDays).coerceAtLeast(0)
        box.addView(text(this, labels[current], 15f).apply {
            setPadding(0, dp(8), 0, dp(8))
            setTextColor(col(R.color.brand))
            setOnClickListener {
                AlertDialog.Builder(this@MainActivity)
                    .setTitle(R.string.delete_after_upload)
                    .setSingleChoiceItems(labels.toTypedArray(), current) { d, which ->
                        val days = options[which]
                        val save = {
                            prefs.updateFolder(f.id) { it.copy(deleteAfterDays = days) }
                            folderSettingsChanged()
                            text = labels[which]
                            d.dismiss()
                        }
                        if (days >= 0) {
                            AlertDialog.Builder(this@MainActivity)
                                .setMessage(R.string.delete_warning)
                                .setPositiveButton(android.R.string.ok) { _, _ -> save() }
                                .setNegativeButton(android.R.string.cancel, null)
                                .show()
                        } else {
                            save()
                        }
                    }
                    .show()
            }
        })
        box.addView(text(this, getString(R.string.delete_hint), 12f, color = R.color.text2))

        val dlg = AlertDialog.Builder(this)
            .setTitle(if (isNew) R.string.folder_added else R.string.folder_settings)
            .setView(ScrollView(this).apply { addView(box) })
            .setPositiveButton(android.R.string.ok, null)
            .setOnDismissListener { refreshUploads() }
        if (!isNew) {
            dlg.setNeutralButton(R.string.remove_folder) { _, _ ->
                AlertDialog.Builder(this)
                    .setMessage(R.string.remove_folder_confirm)
                    .setPositiveButton(R.string.remove_folder) { _, _ ->
                        prefs.folders = prefs.folders.filter { it.id != f.id }
                        hd.db.dropFolder(f.id, forgetSeen = true)
                        folderSettingsChanged()
                        refreshUploads()
                    }
                    .setNegativeButton(android.R.string.cancel, null)
                    .show()
            }
        }
        currentDialog = dlg.show()
    }

    private var currentDialog: AlertDialog? = null

    private fun changeTarget(id: String, space: String, path: String) {
        prefs.updateFolder(id) { it.copy(space = space, remote = path) }
        folderSettingsChanged()
        currentDialog?.dismiss()
        editFolder(id)
    }

    private fun folderSettingsChanged() {
        if (UploadState.serviceRunning || UploadService.wanted(this)) UploadService.start(this, UploadService.ACTION_RELOAD)
        UploadWidget.update(this)
    }

    private fun targetLabel(space: String, path: String): String {
        val base = when {
            space == "home" -> getString(R.string.my_drive)
            space.startsWith("team:") -> getString(R.string.team_folder)
            space.startsWith("share:") -> getString(R.string.shared_with_me)
            else -> getString(R.string.mounted_folder)
        }
        return Uploader.joinPath(base, path)
    }

    // ----- More tab -----

    private var moreRoot: View? = null

    private fun moreView(): View {
        moreRoot?.let { return it }
        moreList = vertical(this, 16)
        val sv = ScrollView(this).apply { addView(moreList) }
        moreRoot = sv
        return sv
    }

    private fun refreshMore() {
        if (moreRoot == null) return
        val l = moreList
        l.removeAllViews()
        l.addView(text(this, getString(R.string.tab_more), 24f, true).apply { setPadding(dp(4), dp(8), 0, dp(12)) })

        if (prefs.knownUpdate.isNotEmpty()) {
            l.addView(card(this) {
                setPadding(0, dp(4), 0, dp(4))
                addView(row(context, R.drawable.ic_notify, getString(R.string.update_available, prefs.knownUpdate), getString(R.string.update_tap)) {
                    try {
                        startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(prefs.updateUrl)))
                    } catch (e: ActivityNotFoundException) {
                        // no browser
                    }
                })
            })
        }

        l.addView(card(this) {
            setPadding(0, dp(4), 0, dp(4))
            addView(row(context, R.drawable.ic_scan, getString(R.string.scanner_title), getString(R.string.scanner_sub)) {
                startActivity(Intent(this@MainActivity, ScannerActivity::class.java))
            })
        })

        l.addView(heading(this, getString(R.string.target_folders)))
        l.addView(card(this) {
            setPadding(0, dp(4), 0, dp(4))
            addView(row(context, R.drawable.ic_folder, getString(R.string.share_target_label), targetLabel(prefs.shareSpace, prefs.shareTarget)) {
                @Suppress("DEPRECATION")
                startActivityForResult(FolderPickerActivity.intent(this@MainActivity, getString(R.string.share_target_label)), REQ_SHARE_TARGET)
            })
            addView(divider(context))
            addView(row(context, R.drawable.ic_scan, getString(R.string.scan_target_label), targetLabel(prefs.scanSpace, prefs.scanTarget)) {
                @Suppress("DEPRECATION")
                startActivityForResult(FolderPickerActivity.intent(this@MainActivity, getString(R.string.scan_target_label)), REQ_SCAN_TARGET)
            })
        })

        l.addView(heading(this, getString(R.string.permissions)))
        l.addView(card(this) {
            setPadding(0, dp(4), 0, dp(4))
            addView(row(context, R.drawable.ic_storage, getString(R.string.perm_storage),
                getString(if (hasStorageAccess()) R.string.perm_granted else R.string.perm_missing)) { requestStorageAccess() })
            addView(divider(context))
            val pm = getSystemService(PowerManager::class.java)
            val unrestricted = pm.isIgnoringBatteryOptimizations(packageName)
            addView(row(context, R.drawable.ic_refresh, getString(R.string.perm_battery),
                getString(if (unrestricted) R.string.perm_battery_ok else R.string.perm_battery_missing)) {
                try {
                    @SuppressLint("BatteryLife")
                    val i = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName"))
                    startActivity(i)
                } catch (e: ActivityNotFoundException) {
                    startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
                }
            })
            addView(divider(context))
            addView(row(context, R.drawable.ic_notify, getString(R.string.perm_notifications), getString(R.string.perm_notifications_sub)) {
                startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, packageName))
            })
        })

        l.addView(heading(this, getString(R.string.account)))
        l.addView(card(this) {
            setPadding(0, dp(4), 0, dp(4))
            addView(row(context, R.drawable.ic_settings, prefs.username, prefs.server))
            addView(divider(context))
            addView(horizontal(context) {
                setPadding(dp(12), dp(12), dp(12), dp(12))
                addView(icon(context, R.drawable.ic_notify))
                addView(vertical(context) {
                    addView(text(context, getString(R.string.update_check), 16f))
                    addView(text(context, getString(R.string.update_check_sub), 13f, color = R.color.text2))
                }, lp(0, WRAP, 1f).margins(context, l = 16))
                addView(Switch(context).apply {
                    isChecked = prefs.updateCheck
                    setOnCheckedChangeListener { _, on ->
                        prefs.updateCheck = on
                        if (on) runBackground { Notifier.checkUpdate(this@MainActivity, force = true) }
                    }
                })
            })
            addView(divider(context))
            addView(row(context, R.drawable.ic_back, getString(R.string.logout)) { confirmLogout() })
        })

        l.addView(heading(this, getString(R.string.about)))
        l.addView(card(this) {
            setPadding(0, dp(4), 0, dp(4))
            addView(row(context, R.drawable.ic_link, "Homelydrive ${BuildConfig.VERSION_NAME}", getString(R.string.about_text)) {
                try {
                    startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://github.com/ErikEdits/Homelydrive")))
                } catch (e: ActivityNotFoundException) {
                    // no browser
                }
            })
        })
    }

    private fun confirmLogout() {
        AlertDialog.Builder(this)
            .setTitle(R.string.logout)
            .setMessage(R.string.logout_confirm)
            .setPositiveButton(R.string.logout) { _, _ -> logout() }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun logout() {
        val api = Api(prefs.server, prefs.token)
        runBackground {
            try {
                api.logout()
            } catch (e: Exception) {
                // server not reachable: the token stays valid until removed in the web interface
            }
        }
        stopService(Intent(this, UploadService::class.java))
        prefs.logout()
        CookieManager.getInstance().removeAllCookies(null)
        webView?.clearCache(true)
        DocsProvider.rootsChanged(this)
        UploadWidget.update(this)
        startActivity(Intent(this, LoginActivity::class.java))
        finish()
    }

    companion object {
        const val EXTRA_TAB = "tab"
        const val TAB_FILES = 0
        const val TAB_UPLOADS = 1
        const val TAB_MORE = 2
        private const val EXTRA_FOLDER_ID = "folder_id"
        private const val REQ_FILE = 1
        private const val REQ_LOCAL_FOLDER = 2
        private const val REQ_REMOTE_FOLDER = 3
        private const val REQ_SHARE_TARGET = 4
        private const val REQ_SCAN_TARGET = 5
        private const val REQ_NOTIFY = 6
        private const val REQ_STORAGE = 7
        private const val REQ_EXPLORER = 8
        private const val REQ_EXPLORER_WEB = 9
    }
}
