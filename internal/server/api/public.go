package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/thumbs"
)

// Public link endpoints. They need no account, but the server is only
// reachable inside the tailnet, so links only work there.

func linkCookie(l *core.Link) string { return "hdl_" + strconv.FormatInt(l.ID, 10) }

// link loads the link and checks its password cookie.
func (s *Server) link(w http.ResponseWriter, r *http.Request) *core.Link {
	l, err := s.Core.LinkByToken(r.PathValue("token"))
	if err != nil {
		s.fail(w, err)
		return nil
	}
	if l.HasPassword {
		c, err := r.Cookie(linkCookie(l))
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.Core.LinkSecret(l))) != 1 {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "Passwort erforderlich.", Code: "password_required"})
			return nil
		}
	}
	return l
}

func (s *Server) handlePublicInfo(w http.ResponseWriter, r *http.Request) {
	l, err := s.Core.LinkByToken(r.PathValue("token"))
	if err != nil {
		s.fail(w, err)
		return
	}
	info := map[string]any{
		"name":          l.Name,
		"is_dir":        l.IsDir,
		"kind":          l.Kind,
		"owner":         s.Core.LinkOwnerName(l),
		"has_password":  l.HasPassword,
		"expires_at":    l.ExpiresAt,
		"max_downloads": l.MaxDownloads,
		"downloads":     l.Downloads,
	}
	authorized := !l.HasPassword
	if c, err := r.Cookie(linkCookie(l)); err == nil && l.HasPassword {
		authorized = subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.Core.LinkSecret(l))) == 1
	}
	info["authorized"] = authorized
	if authorized && !l.IsDir {
		if a, err := s.Core.LinkAccess(l); err == nil {
			if fi, err := s.Core.Stat(a, ""); err == nil {
				info["file"] = fi
			}
		}
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handlePublicAuth(w http.ResponseWriter, r *http.Request) {
	l, err := s.Core.LinkByToken(r.PathValue("token"))
	if err != nil {
		s.fail(w, err)
		return
	}
	var req passwordReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if !s.Core.CheckLinkPassword(l, req.Password) {
		time.Sleep(500 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Falsches Passwort.", Code: "wrong_password"})
		return
	}
	setCookie(w, r, linkCookie(l), s.Core.LinkSecret(l), 7*24*3600)
	ok(w)
}

func (s *Server) publicAccess(w http.ResponseWriter, r *http.Request, wantKind string) (*core.Link, *core.Access) {
	l := s.link(w, r)
	if l == nil {
		return nil, nil
	}
	if l.Kind != wantKind {
		s.fail(w, core.ErrForbidden)
		return nil, nil
	}
	a, err := s.Core.LinkAccess(l)
	if err != nil {
		s.fail(w, err)
		return nil, nil
	}
	return l, a
}

func (s *Server) handlePublicList(w http.ResponseWriter, r *http.Request) {
	_, a := s.publicAccess(w, r, "download")
	if a == nil {
		return
	}
	entries, err := s.Core.List(a, r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, err)
		return
	}
	// Hide internal details from anonymous visitors.
	for i := range entries {
		entries[i].Note, entries[i].Tags, entries[i].Starred, entries[i].Shared, entries[i].CanWrite = "", nil, false, false, false
		entries[i].ID = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handlePublicContent(w http.ResponseWriter, r *http.Request) {
	l, a := s.publicAccess(w, r, "download")
	if a == nil {
		return
	}
	f, err := s.Core.Open(a, r.URL.Query().Get("path"), false)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.File.Close()
	rng := r.Header.Get("Range")
	if rng == "" || strings.HasPrefix(rng, "bytes=0-") {
		s.Core.CountLinkDownload(l)
	}
	serveFile(w, r, f, r.URL.Query().Get("download") == "1")
}

func (s *Server) handlePublicThumb(w http.ResponseWriter, r *http.Request) {
	_, a := s.publicAccess(w, r, "download")
	if a == nil {
		return
	}
	p, err := s.Core.Thumb(r.Context(), a, r.URL.Query().Get("path"), int(qInt(r, "size")))
	if err != nil {
		if errors.Is(err, thumbs.ErrUnsupported) {
			http.Error(w, "no thumbnail", http.StatusNotFound)
			return
		}
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, p)
}

func (s *Server) handlePublicZip(w http.ResponseWriter, r *http.Request) {
	l, a := s.publicAccess(w, r, "download")
	if a == nil {
		return
	}
	paths := r.URL.Query()["path"]
	if len(paths) == 0 {
		paths = []string{""}
	}
	s.Core.CountLinkDownload(l)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition("attachment", l.Name+".zip"))
	if err := s.Core.WriteZip(r.Context(), w, a, paths); err != nil {
		s.Log.Warn("zip download aborted", "err", err)
	}
}

func (s *Server) handlePublicCreateUpload(w http.ResponseWriter, r *http.Request) {
	l := s.link(w, r)
	if l == nil {
		return
	}
	var req core.UploadRequest
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	st, err := s.Core.CreateLinkUpload(l, req)
	if err != nil {
		s.fail(w, err)
		return
	}
	st.File = nil
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handlePublicUploadOffset(w http.ResponseWriter, r *http.Request) {
	l := s.link(w, r)
	if l == nil {
		return
	}
	st, err := s.Core.UploadOffset(core.LinkOwner(l), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeOffset(w, st)
}

func (s *Server) handlePublicUploadChunk(w http.ResponseWriter, r *http.Request) {
	l := s.link(w, r)
	if l == nil {
		return
	}
	s.chunk(w, r, l)
}
