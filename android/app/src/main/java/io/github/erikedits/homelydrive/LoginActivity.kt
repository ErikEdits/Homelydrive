package io.github.erikedits.homelydrive

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.view.inputmethod.EditorInfo
import android.widget.EditText
import android.widget.ImageView
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView

/** Connects the app to a Homelydrive server in the tailnet. */
class LoginActivity : Activity() {
    private lateinit var server: EditText
    private lateinit var user: EditText
    private lateinit var password: EditText
    private lateinit var error: TextView
    private lateinit var progress: ProgressBar
    private lateinit var loginButton: View

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val p = hd.prefs

        val content = vertical(this, 24) {
            gravity = Gravity.CENTER_HORIZONTAL
            addView(ImageView(context).apply { setImageResource(R.mipmap.ic_launcher) }, lp(dp(96), dp(96)).margins(context, t = 24))
            addView(text(context, getString(R.string.app_name), 26f, true).apply { gravity = Gravity.CENTER }, lp().margins(context, t = 12))
            addView(text(context, getString(R.string.login_subtitle), 15f, color = R.color.text2).apply { gravity = Gravity.CENTER },
                lp().margins(context, t = 4, b = 24))

            server = field(getString(R.string.server_address), InputType.TYPE_TEXT_VARIATION_URI)
            server.setText(p.server.removePrefix("http://").removeSuffix(":8420"))
            addView(server)
            addView(text(context, getString(R.string.server_hint), 13f, color = R.color.text2), lp().margins(context, t = 4, b = 12))
            user = field(getString(R.string.username), InputType.TYPE_TEXT_VARIATION_PERSON_NAME)
            user.setText(p.username)
            addView(user, lp().margins(context, b = 12))
            password = field(getString(R.string.password), InputType.TYPE_TEXT_VARIATION_PASSWORD)
            password.imeOptions = EditorInfo.IME_ACTION_DONE
            password.setOnEditorActionListener { _, id, _ ->
                if (id == EditorInfo.IME_ACTION_DONE) {
                    login()
                    true
                } else {
                    false
                }
            }
            addView(password)

            error = text(context, "", 14f, color = R.color.danger).apply { visibility = View.GONE }
            addView(error, lp().margins(context, t = 12))
            progress = ProgressBar(context).apply { visibility = View.GONE }
            addView(progress, lp(WRAP, WRAP).margins(context, t = 12))
            loginButton = button(context, getString(R.string.login)) { login() }
            addView(loginButton, lp().margins(context, t = 16))

            val ts = packageManager.getLaunchIntentForPackage(TAILSCALE)
            addView(button(context, getString(if (ts != null) R.string.open_tailscale else R.string.get_tailscale), primary = false) {
                val i = ts ?: Intent(Intent.ACTION_VIEW, android.net.Uri.parse("https://tailscale.com/download/android"))
                try {
                    startActivity(i)
                } catch (e: Exception) {
                    toast(e.message ?: "")
                }
            }, lp().margins(context, t = 10))
            addView(text(context, getString(R.string.tailscale_hint), 13f, color = R.color.text2).apply { gravity = Gravity.CENTER },
                lp().margins(context, t = 16))
        }
        val scroll = ScrollView(this).apply {
            isFillViewport = true
            setBackgroundColor(col(R.color.bg))
            addView(content)
        }
        applyInsets(scroll)
        setContentView(scroll)
    }

    private fun field(hint: String, variation: Int) = EditText(this).apply {
        this.hint = hint
        setSingleLine()
        inputType = InputType.TYPE_CLASS_TEXT or variation
        imeOptions = EditorInfo.IME_ACTION_NEXT
        background = rounded(col(R.color.surface), dp(12).toFloat(), col(R.color.divider), dp(1))
        setPadding(dp(14), dp(12), dp(14), dp(12))
        layoutParams = lp()
    }

    private fun login() {
        val input = server.text.toString().trim()
        val name = user.text.toString().trim()
        val pw = password.text.toString()
        if (input.isEmpty() || name.isEmpty() || pw.isEmpty()) {
            showError(getString(R.string.fill_all))
            return
        }
        showError(null)
        progress.visibility = View.VISIBLE
        loginButton.isEnabled = false
        val device = "Android · " + hd.prefs.deviceName
        async({
            // Without a scheme try plain HTTP first (inside the tailnet traffic is encrypted anyway), then HTTPS.
            val candidates = if (input.contains("://")) listOf(Api.normalizeServer(input))
            else listOf(Api.normalizeServer("http://$input"), Api.normalizeServer("https://$input").let {
                if (it.substringAfterLast(':').toIntOrNull() == null) "$it:8420" else it
            })
            var last: Exception? = null
            var base: String? = null
            for (c in candidates) {
                try {
                    Api.ping(c)
                    base = c
                    break
                } catch (e: Exception) {
                    last = e
                }
            }
            val b = base ?: throw (last ?: ApiException(0, "network", "unreachable"))
            b to Api.login(b, name, pw, device)
        }) { r ->
            progress.visibility = View.GONE
            loginButton.isEnabled = true
            r.onSuccess { (base, token) ->
                val p = hd.prefs
                p.server = base
                p.token = token
                p.username = name
                p.lastNotificationId = -1
                DocsProvider.rootsChanged(this)
                startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP))
                finish()
            }.onFailure { e ->
                showError(when {
                    e is ApiException && e.status == 401 -> getString(R.string.err_wrong_password)
                    e is ApiException && e.code == "not_homelydrive" -> getString(R.string.err_not_homelydrive)
                    e is ApiException && e.isNetwork -> getString(R.string.err_unreachable_login)
                    else -> errorText(e)
                })
            }
        }
    }

    private fun showError(s: String?) {
        error.text = s ?: ""
        error.visibility = if (s == null) View.GONE else View.VISIBLE
    }

    companion object {
        const val TAILSCALE = "com.tailscale.ipn"
    }
}
