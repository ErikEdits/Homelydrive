package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/tailnet"
	"github.com/erikedits/homelydrive/internal/version"
)

const sessionKey ctxKey = 1

type handler func(w http.ResponseWriter, r *http.Request, sess *core.Session)

func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// auth requires a logged in user. Tokens come from the Authorization header
// (apps), the session cookie (web UI) or, for GET requests only, the
// access_token query parameter (media players, EventSource).
func (s *Server) auth(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		viaCookie := false
		if tok == "" {
			if c, err := r.Cookie(sessionCookie); err == nil {
				tok, viaCookie = c.Value, true
			}
		}
		if tok == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			tok = r.URL.Query().Get("access_token")
		}
		sess, err := s.Core.Authenticate(tok)
		if err != nil {
			s.fail(w, core.ErrUnauthorized)
			return
		}
		// Cookie-authenticated changes must carry a custom header, which
		// browsers do not allow other websites to send (CSRF protection).
		if viaCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-HD") != "1" &&
			!(r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/files/zip")) {
			s.fail(w, core.ErrForbidden)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)), sess)
	}
}

// local only allows requests from this machine (server window).
func (s *Server) local(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tailnet.IsLoopback(r.RemoteAddr) || r.Header.Get("X-Forwarded-For") != "" {
			http.Error(w, "Die Server-Verwaltung ist nur direkt am Server-PC erreichbar.", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// admin requires a local request with an admin token from the server window.
func (s *Server) admin(h handler) http.HandlerFunc {
	return s.local(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		viaCookie := false
		if tok == "" {
			if c, err := r.Cookie(adminCookie); err == nil {
				tok, viaCookie = c.Value, true
			}
		}
		sess, err := s.Core.Authenticate(tok)
		if err != nil || !sess.IsAdmin {
			s.fail(w, core.ErrUnauthorized)
			return
		}
		if viaCookie && r.Method != http.MethodGet && r.Header.Get("X-HD") != "1" {
			s.fail(w, core.ErrForbidden)
			return
		}
		h(w, r, sess)
	})
}

func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Device   string `json:"device"`
	Cookie   bool   `json:"cookie"` // web UI: set a session cookie
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if req.Device == "" {
		req.Device = r.UserAgent()
	}
	tok, u, err := s.Core.Login(req.Username, req.Password, req.Device, false)
	if err != nil {
		s.Log.Info("login failed", "user", req.Username, "remote", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Benutzername oder Passwort falsch.", Code: "unauthorized"})
		return
	}
	if req.Cookie {
		setCookie(w, r, sessionCookie, tok, 10*365*24*3600)
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "user": s.meJSON(u)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	s.Core.Logout(sess.Token)
	setCookie(w, r, sessionCookie, "", -1)
	ok(w)
}

func (s *Server) meJSON(u *core.User) map[string]any {
	lang := u.Lang
	if lang == "" {
		lang = s.Cfg.Get().Language
	}
	used := s.Core.Usage(u.ID)
	return map[string]any{
		"id":           u.ID,
		"username":     u.Username,
		"display_name": u.DisplayName,
		"name":         u.Name(),
		"is_admin":     u.IsAdmin,
		"quota":        u.QuotaBytes,
		"used":         used,
		"lang":         lang,
		"has_vault":    u.HasVault,
		"avatar":       u.Avatar,
	}
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	writeJSON(w, http.StatusOK, s.meJSON(sess.User))
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		DisplayName *string `json:"display_name"`
		Lang        *string `json:"lang"`
		OldPassword string  `json:"old_password"`
		NewPassword *string `json:"new_password"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	upd := core.UserUpdate{DisplayName: req.DisplayName, Lang: req.Lang}
	if req.NewPassword != nil {
		if _, err := s.Core.CheckPassword(sess.User.Username, req.OldPassword); err != nil {
			s.fail(w, &core.BadRequest{Msg: "Das alte Passwort ist falsch."})
			return
		}
		upd.Password = req.NewPassword
	}
	if err := s.Core.UpdateUser(sess.User.ID, upd, sess.Token); err != nil {
		s.fail(w, err)
		return
	}
	u, _ := s.Core.GetUser(sess.User.ID)
	writeJSON(w, http.StatusOK, s.meJSON(u))
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	d, err := s.Core.ListDevices(sess.User.ID, sess.Token)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Core.RevokeDevice(sess.User.ID, id); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	info := map[string]any{
		"version":    version.Version,
		"repo":       "https://github.com/" + version.Repo,
		"webdav":     "/dav/",
		"trash_days": s.Cfg.Get().TrashDays,
	}
	if up := s.Core.Update(); up != nil && up.Newer {
		info["update"] = up
	}
	writeJSON(w, http.StatusOK, info)
}

// handleSetAvatar takes the image as the request body.
func (s *Server) handleSetAvatar(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.SetAvatar(r.Context(), sess.User, r.Body); err != nil {
		s.fail(w, err)
		return
	}
	u, _ := s.Core.GetUser(sess.User.ID)
	writeJSON(w, http.StatusOK, s.meJSON(u))
}

func (s *Server) handleDeleteAvatar(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.DeleteAvatar(sess.User); err != nil {
		s.fail(w, err)
		return
	}
	u, _ := s.Core.GetUser(sess.User.ID)
	writeJSON(w, http.StatusOK, s.meJSON(u))
}

// handleAvatar serves a profile picture. Links carry ?v=<time of change>,
// so a picture can be cached until it changes.
func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := s.Core.AvatarPath(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
}

func (s *Server) handleUserBriefs(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	list, err := s.Core.ListUserBriefs(sess.User.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRoots(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	roots, err := s.Core.ListRoots(sess.User)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, roots)
}
