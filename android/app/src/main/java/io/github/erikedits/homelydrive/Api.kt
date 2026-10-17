package io.github.erikedits.homelydrive

import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/** Error answer of the server (or a network problem when status == 0). */
class ApiException(val status: Int, val code: String, message: String, val offset: Long? = null) : IOException(message) {
    val isNetwork get() = status == 0 || status == 502 || status == 503 || status == 504
}

data class RemoteFile(
    val name: String,
    val path: String,
    val isDir: Boolean,
    val size: Long,
    val mtime: Long,
    val category: String,
    val mime: String,
    val canWrite: Boolean,
    /** Space reference; only set in search results. */
    val space: String = "",
) {
    companion object {
        fun fromJson(o: JSONObject) = RemoteFile(
            space = o.optString("space"),
            name = o.getString("name"),
            path = o.getString("path"),
            isDir = o.optBoolean("is_dir"),
            size = o.optLong("size"),
            mtime = o.optLong("mtime"),
            category = o.optString("category"),
            mime = o.optString("mime"),
            canWrite = o.optBoolean("can_write", true),
        )
    }
}

data class Root(val ref: String, val kind: String, val name: String, val canWrite: Boolean, val isDir: Boolean, val owner: String)

data class UploadStatus(val id: String, val status: String, val offset: Long, val size: Long, val path: String)

/**
 * Minimal client for the Homelydrive server API (the same JSON API the web
 * interface uses). Uses only Android framework classes.
 */
class Api(val base: String, val token: String) {

    private fun url(path: String, params: Map<String, String> = emptyMap()): URL {
        val q = params.entries.joinToString("&") { "${enc(it.key)}=${enc(it.value)}" }
        return URL(base.trimEnd('/') + "/api/v1" + path + if (q.isNotEmpty()) "?$q" else "")
    }

    private fun open(method: String, path: String, params: Map<String, String> = emptyMap()): HttpURLConnection {
        val c = url(path, params).openConnection() as HttpURLConnection
        c.requestMethod = method
        c.connectTimeout = 15_000
        c.readTimeout = 120_000
        c.setRequestProperty("User-Agent", "Homelydrive-Android/${BuildConfig.VERSION_NAME}")
        if (token.isNotEmpty()) c.setRequestProperty("Authorization", "Bearer $token")
        return c
    }

    private fun readError(c: HttpURLConnection): ApiException {
        val status = c.responseCode
        val body = try {
            (c.errorStream ?: c.inputStream)?.bufferedReader()?.readText() ?: ""
        } catch (e: IOException) {
            ""
        }
        return try {
            val j = JSONObject(body)
            ApiException(status, j.optString("code"), j.optString("error", "HTTP $status"),
                if (j.has("offset")) j.getLong("offset") else null)
        } catch (e: Exception) {
            ApiException(status, "", if (body.isNotBlank() && body.length < 200) body else "HTTP $status")
        }
    }

    private fun <T> request(method: String, path: String, params: Map<String, String> = emptyMap(), body: JSONObject? = null, parse: (String) -> T): T {
        val c = try {
            open(method, path, params)
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        }
        try {
            if (body != null) {
                c.doOutput = true
                c.setRequestProperty("Content-Type", "application/json")
                val bytes = body.toString().toByteArray()
                c.setFixedLengthStreamingMode(bytes.size)
                c.outputStream.use { it.write(bytes) }
            }
            if (c.responseCode >= 300) throw readError(c)
            return parse(c.inputStream.bufferedReader().readText())
        } catch (e: ApiException) {
            throw e
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        } finally {
            c.disconnect()
        }
    }

    private fun obj(method: String, path: String, params: Map<String, String> = emptyMap(), body: JSONObject? = null) =
        request(method, path, params, body) { if (it.isBlank()) JSONObject() else JSONObject(it) }

    fun me(): JSONObject = obj("GET", "/me")

    fun logout() {
        obj("POST", "/auth/logout")
    }

    fun roots(): List<Root> = request("GET", "/roots") { s ->
        val arr = JSONArray(s)
        (0 until arr.length()).map {
            val o = arr.getJSONObject(it)
            Root(o.getString("ref"), o.getString("kind"), o.getString("name"), o.optBoolean("can_write"), o.optBoolean("is_dir", true), o.optString("owner"))
        }
    }

    fun list(space: String, path: String): List<RemoteFile> {
        val o = obj("GET", "/files/list", mapOf("space" to space, "path" to path))
        val arr = o.getJSONArray("entries")
        return (0 until arr.length()).map { RemoteFile.fromJson(arr.getJSONObject(it)) }
    }

    fun stat(space: String, path: String): RemoteFile = RemoteFile.fromJson(obj("GET", "/files/stat", mapOf("space" to space, "path" to path)))

    fun mkdir(space: String, path: String) {
        obj("POST", "/files/mkdir", body = JSONObject().put("space", space).put("path", path))
    }

    /** Creates a folder; an already existing folder is fine. */
    fun ensureDir(space: String, path: String) {
        if (path.isEmpty()) return
        try {
            mkdir(space, path)
        } catch (e: ApiException) {
            if (e.status != 409) throw e
        }
    }

    fun rename(space: String, path: String, newName: String) {
        obj("POST", "/files/rename", body = JSONObject().put("space", space).put("path", path).put("new_name", newName))
    }

    fun delete(space: String, path: String) {
        obj("POST", "/files/delete", body = JSONObject().put("space", space).put("paths", JSONArray().put(path)))
    }

    fun createLink(space: String, path: String): String {
        val o = obj("POST", "/links", body = JSONObject().put("space", space).put("path", path).put("kind", "download"))
        return base.trimEnd('/') + "/s/" + o.getString("token")
    }

    fun search(q: String, limit: Int = 100): List<RemoteFile> = request("GET", "/search", mapOf("q" to q, "limit" to limit.toString())) { s ->
        val arr = JSONArray(s)
        (0 until arr.length()).map { RemoteFile.fromJson(arr.getJSONObject(it)) }
    }

    /** Creates an empty file (used by the Files app integration). */
    fun createEmpty(space: String, path: String): RemoteFile {
        val c = open("PUT", "/files/content", mapOf("space" to space, "path" to path))
        try {
            c.doOutput = true
            c.setFixedLengthStreamingMode(0)
            c.outputStream.close()
            if (c.responseCode >= 300) throw readError(c)
            return RemoteFile.fromJson(JSONObject(c.inputStream.bufferedReader().readText()))
        } catch (e: ApiException) {
            throw e
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        } finally {
            c.disconnect()
        }
    }

    /**
     * Reads the live event stream (Server-Sent Events) until it ends or the
     * thread is interrupted; [onEvent] gets the event type.
     */
    fun events(onEvent: (String) -> Unit) {
        val c = open("GET", "/events")
        c.readTimeout = 90_000 // the server pings every 25 s
        try {
            if (c.responseCode >= 300) throw readError(c)
            c.inputStream.bufferedReader().useLines { lines ->
                for (line in lines) {
                    if (Thread.currentThread().isInterrupted) break
                    if (!line.startsWith("data:")) continue
                    val type = try {
                        JSONObject(line.removePrefix("data:").trim()).optString("type")
                    } catch (e: Exception) {
                        continue
                    }
                    onEvent(type)
                }
            }
        } finally {
            c.disconnect()
        }
    }

    fun notifications(after: Long): JSONObject = obj("GET", "/notifications", mapOf("after" to after.toString()))

    private fun status(o: JSONObject) = UploadStatus(o.optString("id"), o.getString("status"), o.optLong("offset"), o.optLong("size"), o.optString("path"))

    fun createUpload(space: String, path: String, size: Long, sha256: String?, mtime: Long, conflict: String): UploadStatus {
        val b = JSONObject().put("space", space).put("path", path).put("size", size).put("mtime", mtime).put("conflict", conflict)
        if (sha256 != null) b.put("sha256", sha256)
        return status(obj("POST", "/uploads", body = b))
    }

    fun uploadOffset(id: String): Long = obj("GET", "/uploads/" + enc(id)).optLong("offset")

    /** Sends [length] bytes from [input] starting at [offset]. */
    fun uploadChunk(id: String, offset: Long, length: Long, input: InputStream, progress: (Long) -> Unit): UploadStatus {
        val c = try {
            // HttpURLConnection has no PATCH: the server accepts POST for chunks too.
            open("POST", "/uploads/" + enc(id))
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        }
        try {
            c.doOutput = true
            c.readTimeout = 10 * 60_000
            c.setRequestProperty("Upload-Offset", offset.toString())
            c.setRequestProperty("Content-Type", "application/offset+octet-stream")
            c.setFixedLengthStreamingMode(length)
            c.outputStream.use { out ->
                val buf = ByteArray(64 * 1024)
                var sent = 0L
                while (sent < length) {
                    val n = input.read(buf, 0, minOf(buf.size.toLong(), length - sent).toInt())
                    if (n < 0) break
                    out.write(buf, 0, n)
                    sent += n
                    progress(offset + sent)
                }
            }
            if (c.responseCode >= 300) throw readError(c)
            return status(JSONObject(c.inputStream.bufferedReader().readText()))
        } catch (e: ApiException) {
            throw e
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        } finally {
            c.disconnect()
        }
    }

    /** Opens a file (or a part of it) for download. The caller closes the stream. */
    fun download(space: String, path: String, offset: Long = 0, length: Long = -1): Pair<InputStream, Long> {
        val c = try {
            open("GET", "/files/content", mapOf("space" to space, "path" to path))
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        }
        try {
            if (length > 0) {
                c.setRequestProperty("Range", "bytes=$offset-${offset + length - 1}")
            } else if (offset > 0) {
                c.setRequestProperty("Range", "bytes=$offset-")
            }
            if (c.responseCode >= 300) throw readError(c)
            if (offset > 0 && c.responseCode != 206) throw ApiException(500, "range", "range requests not supported")
            return Pair(c.inputStream, c.contentLengthLong)
        } catch (e: ApiException) {
            c.disconnect()
            throw e
        } catch (e: IOException) {
            c.disconnect()
            throw ApiException(0, "network", e.message ?: "network error")
        }
    }

    /** Saves a preview picture of a file to [dest]. */
    fun thumb(space: String, path: String, size: Int, dest: java.io.File) {
        val c = try {
            open("GET", "/files/thumb", mapOf("space" to space, "path" to path, "size" to size.toString()))
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        }
        try {
            if (c.responseCode >= 300) throw readError(c)
            val tmp = java.io.File(dest.path + ".part")
            c.inputStream.use { i -> tmp.outputStream().use { o -> i.copyTo(o) } }
            tmp.renameTo(dest)
        } catch (e: ApiException) {
            throw e
        } catch (e: IOException) {
            throw ApiException(0, "network", e.message ?: "network error")
        } finally {
            c.disconnect()
        }
    }

    companion object {
        fun enc(s: String): String = URLEncoder.encode(s, "UTF-8").replace("+", "%20")

        /** Turns user input into a server URL ("meinpc" -> "http://meinpc:8420"). */
        fun normalizeServer(input: String): String {
            var s = input.trim().trimEnd('/')
            if (s.isEmpty()) return s
            if (!s.contains("://")) s = "http://$s"
            val u = URL(s)
            val port = if (u.port == -1 && u.protocol == "http") 8420 else u.port
            return u.protocol + "://" + u.host + if (port != -1) ":$port" else ""
        }

        fun ping(base: String) {
            val o = Api(base, "").obj("GET", "/ping")
            if (o.optString("name") != "homelydrive") throw ApiException(0, "not_homelydrive", "Kein Homelydrive-Server")
        }

        fun login(base: String, user: String, password: String, device: String): String {
            val o = Api(base, "").obj("POST", "/auth/login", body = JSONObject()
                .put("username", user).put("password", password).put("device", device))
            return o.getString("token")
        }
    }
}
