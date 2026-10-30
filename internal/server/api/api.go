// Package api exposes the Homelydrive server over HTTP: the JSON API used by
// the web UI, the PC app and the Android app, public link pages, WebDAV, the
// admin API for the server window (localhost only) and the static web UI.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/erikedits/homelydrive/internal/server/config"
	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/logbuf"
	"github.com/erikedits/homelydrive/internal/server/tailnet"
	"github.com/erikedits/homelydrive/internal/version"
)

// Server holds the dependencies of the HTTP layer.
type Server struct {
	Core      *core.Core
	Cfg       *config.Store
	Log       *slog.Logger
	Logs      *logbuf.Buffer
	Web       fs.FS // built web UI (index.html, admin.html, share.html, assets/)
	Listeners *tailnet.Manager
	Cert      *tailnet.CertManager
	Started   time.Time
	DAV       http.Handler
}

const (
	sessionCookie = "hd_session"
	adminCookie   = "hd_admin"
)

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- public ---
	mux.HandleFunc("GET /api/v1/ping", s.handlePing)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.auth(s.handleLogout))

	// --- account ---
	mux.HandleFunc("GET /api/v1/me", s.auth(s.handleMe))
	mux.HandleFunc("PUT /api/v1/me", s.auth(s.handleUpdateMe))
	mux.HandleFunc("PUT /api/v1/me/avatar", s.auth(s.handleSetAvatar))
	mux.HandleFunc("DELETE /api/v1/me/avatar", s.auth(s.handleDeleteAvatar))
	mux.HandleFunc("GET /api/v1/me/devices", s.auth(s.handleDevices))
	mux.HandleFunc("DELETE /api/v1/me/devices/{id}", s.auth(s.handleRevokeDevice))
	mux.HandleFunc("GET /api/v1/info", s.auth(s.handleInfo))
	mux.HandleFunc("GET /api/v1/users", s.auth(s.handleUserBriefs))
	mux.HandleFunc("GET /api/v1/users/{id}/avatar", s.auth(s.handleAvatar))
	mux.HandleFunc("GET /api/v1/roots", s.auth(s.handleRoots))

	// --- files ---
	mux.HandleFunc("GET /api/v1/files/list", s.auth(s.handleList))
	mux.HandleFunc("GET /api/v1/files/stat", s.auth(s.handleStat))
	mux.HandleFunc("GET /api/v1/files/duplicates", s.auth(s.handleDuplicates))
	mux.HandleFunc("GET /api/v1/files/content", s.auth(s.handleContent))
	mux.HandleFunc("PUT /api/v1/files/content", s.auth(s.handlePutContent))
	mux.HandleFunc("GET /api/v1/files/thumb", s.auth(s.handleThumb))
	mux.HandleFunc("GET /api/v1/files/zip", s.auth(s.handleZip))
	mux.HandleFunc("POST /api/v1/files/zip", s.auth(s.handleZip))
	mux.HandleFunc("POST /api/v1/files/mkdir", s.auth(s.handleMkdir))
	mux.HandleFunc("POST /api/v1/files/rename", s.auth(s.handleRename))
	mux.HandleFunc("POST /api/v1/files/move", s.auth(s.handleMove))
	mux.HandleFunc("POST /api/v1/files/copy", s.auth(s.handleCopy))
	mux.HandleFunc("POST /api/v1/files/delete", s.auth(s.handleDelete))
	mux.HandleFunc("POST /api/v1/files/extract", s.auth(s.handleExtract))
	mux.HandleFunc("POST /api/v1/files/image-edit", s.auth(s.handleImageEdit))
	mux.HandleFunc("POST /api/v1/files/star", s.auth(s.handleStar))
	mux.HandleFunc("PUT /api/v1/files/meta", s.auth(s.handleMeta))
	mux.HandleFunc("PUT /api/v1/files/tags", s.auth(s.handleSetTags))
	mux.HandleFunc("GET /api/v1/tags", s.auth(s.handleTags))
	mux.HandleFunc("GET /api/v1/starred", s.auth(s.handleStarred))
	mux.HandleFunc("GET /api/v1/recent", s.auth(s.handleRecent))
	mux.HandleFunc("GET /api/v1/search", s.auth(s.handleSearch))
	mux.HandleFunc("GET /api/v1/gallery", s.auth(s.handleGallery))
	mux.HandleFunc("GET /api/v1/storage", s.auth(s.handleStorage))
	mux.HandleFunc("GET /api/v1/activity", s.auth(s.handleActivity))

	// --- uploads (resumable, any size) ---
	mux.HandleFunc("POST /api/v1/uploads", s.auth(s.handleCreateUpload))
	mux.HandleFunc("HEAD /api/v1/uploads/{id}", s.auth(s.handleUploadOffset))
	mux.HandleFunc("GET /api/v1/uploads/{id}", s.auth(s.handleUploadOffset))
	mux.HandleFunc("PATCH /api/v1/uploads/{id}", s.auth(s.handleUploadChunk))
	// POST variant for clients without PATCH support (Android HttpURLConnection).
	mux.HandleFunc("POST /api/v1/uploads/{id}", s.auth(s.handleUploadChunk))
	mux.HandleFunc("DELETE /api/v1/uploads/{id}", s.auth(s.handleCancelUpload))

	// --- trash ---
	mux.HandleFunc("GET /api/v1/trash", s.auth(s.handleTrash))
	mux.HandleFunc("POST /api/v1/trash/restore", s.auth(s.handleTrashRestore))
	mux.HandleFunc("POST /api/v1/trash/delete", s.auth(s.handleTrashPurge))
	mux.HandleFunc("POST /api/v1/trash/empty", s.auth(s.handleTrashEmpty))

	// --- sharing ---
	mux.HandleFunc("GET /api/v1/shares", s.auth(s.handleSharesOf))
	mux.HandleFunc("POST /api/v1/shares", s.auth(s.handleCreateShare))
	mux.HandleFunc("DELETE /api/v1/shares/{id}", s.auth(s.handleDeleteShare))
	mux.HandleFunc("GET /api/v1/shares/outgoing", s.auth(s.handleOutgoing))
	mux.HandleFunc("GET /api/v1/links", s.auth(s.handleListLinks))
	mux.HandleFunc("POST /api/v1/links", s.auth(s.handleCreateLink))
	mux.HandleFunc("PUT /api/v1/links/{id}", s.auth(s.handleUpdateLink))
	mux.HandleFunc("DELETE /api/v1/links/{id}", s.auth(s.handleDeleteLink))

	// --- notifications & live events ---
	mux.HandleFunc("GET /api/v1/notifications", s.auth(s.handleNotifications))
	mux.HandleFunc("POST /api/v1/notifications/read", s.auth(s.handleNotificationsRead))
	mux.HandleFunc("GET /api/v1/events", s.auth(s.handleEvents))

	// --- file types ---
	mux.HandleFunc("GET /api/v1/filetypes", s.auth(s.handleFileTypes))
	mux.HandleFunc("PUT /api/v1/filetypes", s.auth(s.handleSetFileType))
	mux.HandleFunc("DELETE /api/v1/filetypes/{ext}", s.auth(s.handleDeleteFileType))

	// --- vault ---
	mux.HandleFunc("GET /api/v1/vault", s.auth(s.handleVaultStatus))
	mux.HandleFunc("POST /api/v1/vault/setup", s.auth(s.handleVaultSetup))
	mux.HandleFunc("POST /api/v1/vault/unlock", s.auth(s.handleVaultUnlock))
	mux.HandleFunc("POST /api/v1/vault/lock", s.auth(s.handleVaultLock))
	mux.HandleFunc("POST /api/v1/vault/password", s.auth(s.handleVaultPassword))
	mux.HandleFunc("POST /api/v1/vault/reset", s.auth(s.handleVaultReset))
	mux.HandleFunc("GET /api/v1/vault/list", s.auth(s.handleVaultList))
	mux.HandleFunc("POST /api/v1/vault/mkdir", s.auth(s.handleVaultMkdir))
	mux.HandleFunc("POST /api/v1/vault/rename", s.auth(s.handleVaultRename))
	mux.HandleFunc("POST /api/v1/vault/delete", s.auth(s.handleVaultDelete))
	mux.HandleFunc("GET /api/v1/vault/content/{id}", s.auth(s.handleVaultContent))

	// --- public links (download and upload links) ---
	mux.HandleFunc("GET /api/v1/public/{token}", s.handlePublicInfo)
	mux.HandleFunc("POST /api/v1/public/{token}/auth", s.handlePublicAuth)
	mux.HandleFunc("GET /api/v1/public/{token}/list", s.handlePublicList)
	mux.HandleFunc("GET /api/v1/public/{token}/content", s.handlePublicContent)
	mux.HandleFunc("GET /api/v1/public/{token}/thumb", s.handlePublicThumb)
	mux.HandleFunc("GET /api/v1/public/{token}/zip", s.handlePublicZip)
	mux.HandleFunc("POST /api/v1/public/{token}/uploads", s.handlePublicCreateUpload)
	mux.HandleFunc("HEAD /api/v1/public/{token}/uploads/{id}", s.handlePublicUploadOffset)
	mux.HandleFunc("GET /api/v1/public/{token}/uploads/{id}", s.handlePublicUploadOffset)
	mux.HandleFunc("PATCH /api/v1/public/{token}/uploads/{id}", s.handlePublicUploadChunk)
	mux.HandleFunc("POST /api/v1/public/{token}/uploads/{id}", s.handlePublicUploadChunk)

	// --- admin (server window, localhost only) ---
	mux.HandleFunc("GET /api/v1/admin/state", s.local(s.handleAdminState))
	mux.HandleFunc("POST /api/v1/admin/setup", s.local(s.handleAdminSetup))
	mux.HandleFunc("POST /api/v1/admin/login", s.local(s.handleAdminLogin))
	mux.HandleFunc("POST /api/v1/admin/logout", s.admin(s.handleAdminLogout))
	mux.HandleFunc("GET /api/v1/admin/status", s.admin(s.handleAdminStatus))
	mux.HandleFunc("GET /api/v1/admin/users", s.admin(s.handleAdminUsers))
	mux.HandleFunc("POST /api/v1/admin/users", s.admin(s.handleAdminCreateUser))
	mux.HandleFunc("PUT /api/v1/admin/users/{id}", s.admin(s.handleAdminUpdateUser))
	mux.HandleFunc("DELETE /api/v1/admin/users/{id}", s.admin(s.handleAdminDeleteUser))
	mux.HandleFunc("GET /api/v1/admin/spaces", s.admin(s.handleAdminSpaces))
	mux.HandleFunc("POST /api/v1/admin/spaces", s.admin(s.handleAdminCreateSpace))
	mux.HandleFunc("PUT /api/v1/admin/spaces/{id}", s.admin(s.handleAdminUpdateSpace))
	mux.HandleFunc("DELETE /api/v1/admin/spaces/{id}", s.admin(s.handleAdminDeleteSpace))
	mux.HandleFunc("GET /api/v1/admin/locations", s.admin(s.handleAdminLocations))
	mux.HandleFunc("POST /api/v1/admin/locations", s.admin(s.handleAdminAddLocation))
	mux.HandleFunc("DELETE /api/v1/admin/locations", s.admin(s.handleAdminRemoveLocation))
	mux.HandleFunc("GET /api/v1/admin/settings", s.admin(s.handleAdminSettings))
	mux.HandleFunc("PUT /api/v1/admin/settings", s.admin(s.handleAdminUpdateSettings))
	mux.HandleFunc("GET /api/v1/admin/logs", s.admin(s.handleAdminLogs))
	mux.HandleFunc("POST /api/v1/admin/rescan", s.admin(s.handleAdminRescan))
	mux.HandleFunc("GET /api/v1/admin/browse", s.admin(s.handleAdminBrowse))

	// --- web UI ---
	mux.HandleFunc("GET /admin/", s.local(s.servePage("admin.html")))
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.HandleFunc("GET /s/{token}", s.servePage("share.html"))
	mux.HandleFunc("GET /u/{token}", s.servePage("share.html"))
	mux.HandleFunc("GET /", s.serveStatic)

	// WebDAV uses its own methods (PROPFIND, MKCOL, ...), so it is routed
	// before the method-based mux.
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.DAV != nil && (r.URL.Path == "/dav" || strings.HasPrefix(r.URL.Path, "/dav/")) {
			s.DAV.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return s.guard(root)
}

// guard rejects requests that do not come from localhost or the tailnet and
// adds security headers.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixed := len(s.Cfg.Get().ListenAddrs) > 0
		if !tailnet.AllowRemote(r.RemoteAddr, fixed) {
			http.Error(w, "Homelydrive is only reachable via Tailscale", http.StatusForbidden)
			return
		}
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic in request", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

// ----- helpers -----

type ctxKey int

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	// Offset is set for upload offset mismatches so clients can resume.
	Offset *int64 `json:"offset,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }

func (s *Server) fail(w http.ResponseWriter, err error) {
	var br *core.BadRequest
	var om *core.ErrOffsetMismatch
	switch {
	case errors.As(err, &br):
		writeJSON(w, http.StatusBadRequest, apiError{Error: br.Msg, Code: "bad_request"})
	case errors.As(err, &om):
		off := om.Offset
		w.Header().Set("Upload-Offset", strconv.FormatInt(off, 10))
		writeJSON(w, http.StatusConflict, apiError{Error: "Upload offset mismatch", Code: "offset_mismatch", Offset: &off})
	case errors.Is(err, core.ErrNotFound):
		writeJSON(w, http.StatusNotFound, apiError{Error: "Nicht gefunden.", Code: "not_found"})
	case errors.Is(err, core.ErrForbidden):
		writeJSON(w, http.StatusForbidden, apiError{Error: "Keine Berechtigung.", Code: "forbidden"})
	case errors.Is(err, core.ErrExists):
		writeJSON(w, http.StatusConflict, apiError{Error: "Existiert bereits.", Code: "exists"})
	case errors.Is(err, core.ErrQuota):
		writeJSON(w, http.StatusInsufficientStorage, apiError{Error: "Dein Speicherplatz ist voll.", Code: "quota"})
	case errors.Is(err, core.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Anmeldung erforderlich.", Code: "unauthorized"})
	case errors.Is(err, core.ErrVaultLocked):
		writeJSON(w, http.StatusLocked, apiError{Error: "Der Tresor ist gesperrt.", Code: "vault_locked"})
	case errors.Is(err, core.ErrLinkExpired):
		writeJSON(w, http.StatusGone, apiError{Error: "Dieser Link ist abgelaufen.", Code: "link_expired"})
	default:
		s.Log.Error("request failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Interner Fehler: " + err.Error(), Code: "internal"})
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return &core.BadRequest{Msg: "Ungültige Anfrage: " + err.Error()}
	}
	return nil
}

func qInt(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return n
}

// contentDisposition builds a header value that works for non-ASCII names.
func contentDisposition(kind, name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return mime.FormatMediaType(kind, map[string]string{"filename": ascii}) + "; filename*=UTF-8''" + url.PathEscape(name)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    "homelydrive",
		"version": version.Version,
		"setup":   s.Core.UserCount() == 0,
	})
}
