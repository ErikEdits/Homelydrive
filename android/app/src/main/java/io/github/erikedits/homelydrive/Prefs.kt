package io.github.erikedits.homelydrive

import android.content.Context
import android.os.Build
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

/**
 * A phone folder that is uploaded to the server. Upload only: deleting a
 * file on the phone keeps it on the server ("Archiv-Modus").
 */
data class UploadFolder(
    val id: String = UUID.randomUUID().toString().take(12),
    val local: String,
    val space: String = "home",
    val remote: String,
    val enabled: Boolean = true,
    /** Delete files on the phone after they were uploaded (-1 = never, 0 = right away, n = after n days). */
    val deleteAfterDays: Int = -1,
) {
    fun toJson(): JSONObject = JSONObject()
        .put("id", id).put("local", local).put("space", space).put("remote", remote)
        .put("enabled", enabled).put("delete_after_days", deleteAfterDays)

    companion object {
        fun fromJson(o: JSONObject) = UploadFolder(
            id = o.getString("id"),
            local = o.getString("local"),
            space = o.optString("space", "home"),
            remote = o.getString("remote"),
            enabled = o.optBoolean("enabled", true),
            deleteAfterDays = o.optInt("delete_after_days", -1),
        )
    }
}

/** App settings (SharedPreferences). */
class Prefs(ctx: Context) {
    private val sp = ctx.getSharedPreferences("homelydrive", Context.MODE_PRIVATE)

    var server: String
        get() = sp.getString("server", "") ?: ""
        set(v) = sp.edit().putString("server", v).apply()

    var token: String
        get() = sp.getString("token", "") ?: ""
        set(v) = sp.edit().putString("token", v).apply()

    var username: String
        get() = sp.getString("username", "") ?: ""
        set(v) = sp.edit().putString("username", v).apply()

    val isLoggedIn: Boolean get() = server.isNotEmpty() && token.isNotEmpty()

    var paused: Boolean
        get() = sp.getBoolean("paused", false)
        set(v) = sp.edit().putBoolean("paused", v).apply()

    var shareTarget: String
        get() = sp.getString("share_target", "Vom Handy geteilt") ?: "Vom Handy geteilt"
        set(v) = sp.edit().putString("share_target", v).apply()

    var shareSpace: String
        get() = sp.getString("share_space", "home") ?: "home"
        set(v) = sp.edit().putString("share_space", v).apply()

    var scanSpace: String
        get() = sp.getString("scan_space", "home") ?: "home"
        set(v) = sp.edit().putString("scan_space", v).apply()

    /** Last target of "upload files from phone". */
    var manualSpace: String
        get() = sp.getString("manual_space", "home") ?: "home"
        set(v) = sp.edit().putString("manual_space", v).apply()

    var manualTarget: String
        get() = sp.getString("manual_target", "") ?: ""
        set(v) = sp.edit().putString("manual_target", v).apply()

    var manualLabel: String
        get() = sp.getString("manual_label", "") ?: ""
        set(v) = sp.edit().putString("manual_label", v).apply()

    /** Found folders the user hid from the suggestions. */
    var dismissedSuggestions: Set<String>
        get() = HashSet(sp.getStringSet("dismissed_suggestions", emptySet()) ?: emptySet())
        set(v) = sp.edit().putStringSet("dismissed_suggestions", HashSet(v)).apply()

    var scanTarget: String
        get() = sp.getString("scan_target", "Scans") ?: "Scans"
        set(v) = sp.edit().putString("scan_target", v).apply()

    var updateCheck: Boolean
        get() = sp.getBoolean("update_check", true)
        set(v) = sp.edit().putBoolean("update_check", v).apply()

    /** Newest server notification already shown; -1 = not fetched yet (old ones are not shown). */
    var lastNotificationId: Long
        get() = sp.getLong("last_notification", -1)
        set(v) = sp.edit().putLong("last_notification", v).apply()

    var lastUpdateCheck: Long
        get() = sp.getLong("last_update_check", 0)
        set(v) = sp.edit().putLong("last_update_check", v).apply()

    var knownUpdate: String
        get() = sp.getString("known_update", "") ?: ""
        set(v) = sp.edit().putString("known_update", v).apply()

    var updateUrl: String
        get() = sp.getString("update_url", "") ?: ""
        set(v) = sp.edit().putString("update_url", v).apply()

    var folders: List<UploadFolder>
        get() {
            val arr = JSONArray(sp.getString("folders", "[]"))
            return (0 until arr.length()).map { UploadFolder.fromJson(arr.getJSONObject(it)) }
        }
        set(v) {
            val arr = JSONArray()
            v.forEach { arr.put(it.toJson()) }
            sp.edit().putString("folders", arr.toString()).apply()
        }

    fun updateFolder(id: String, fn: (UploadFolder) -> UploadFolder) {
        folders = folders.map { if (it.id == id) fn(it) else it }
    }

    val deviceName: String
        get() = "${Build.MANUFACTURER.replaceFirstChar { it.uppercase() }} ${Build.MODEL}".trim()

    fun logout() {
        sp.edit().remove("token").remove("last_notification").apply()
    }
}
