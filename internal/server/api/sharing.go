package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/erikedits/homelydrive/internal/server/core"
)

func (s *Server) handleSharesOf(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	a := s.access(w, sess, r.URL.Query().Get("space"))
	if a == nil {
		return
	}
	shares, err := s.Core.SharesOf(a, r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, err)
		return
	}
	links, err := s.Core.ListLinks(sess.User, a, r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares, "links": links, "can_share": a.CanShare()})
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Space    string `json:"space"`
		Path     string `json:"path"`
		UserID   int64  `json:"user_id"`
		Username string `json:"username"`
		CanWrite bool   `json:"can_write"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	if req.UserID == 0 && req.Username != "" {
		u, err := s.Core.GetUserByName(req.Username)
		if err != nil {
			s.fail(w, err)
			return
		}
		req.UserID = u.ID
	}
	sh, err := s.Core.CreateShare(a, req.Path, req.UserID, req.CanWrite)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.DeleteShare(sess.User, pathID(r)); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleOutgoing(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	list, err := s.Core.OutgoingShares(sess.User)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	list, err := s.Core.ListLinks(sess.User, nil, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Space string `json:"space"`
		Path  string `json:"path"`
		core.LinkOptions
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	a := s.access(w, sess, req.Space)
	if a == nil {
		return
	}
	l, err := s.Core.CreateLink(a, req.Path, req.LinkOptions)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handleUpdateLink(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var opt core.LinkOptions
	if err := decode(r, &opt); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.UpdateLink(sess.User, pathID(r), opt); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleDeleteLink(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.DeleteLink(sess.User, pathID(r)); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

// ----- notifications and live events -----

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	list, unread, err := s.Core.ListNotifications(sess.User.ID, qInt(r, "after"), int(qInt(r, "limit")))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "unread": unread})
}

func (s *Server) handleNotificationsRead(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req idsReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	s.Core.MarkNotificationsRead(sess.User.ID, req.IDs)
	ok(w)
}

// handleEvents streams live events (Server-Sent Events): notifications and
// "changed" events that make open file lists refresh.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	fl, okf := w.(http.Flusher)
	if !okf {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 5000\n\n")
	fl.Flush()
	ch, cancel := s.Core.Events.Subscribe(sess.User.ID)
	defer cancel()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// ----- vault -----

func (s *Server) handleVaultStatus(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	writeJSON(w, http.StatusOK, s.Core.VaultStatus(sess.User))
}

type passwordReq struct {
	Password    string `json:"password"`
	NewPassword string `json:"new_password"`
}

func (s *Server) handleVaultSetup(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req passwordReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.VaultSetup(sess.User, req.Password); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleVaultUnlock(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req passwordReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.VaultUnlock(sess.User, req.Password); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleVaultLock(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	s.Core.VaultLock(sess.User)
	ok(w)
}

func (s *Server) handleVaultPassword(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req passwordReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.VaultChangePassword(sess.User, req.Password, req.NewPassword); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleVaultReset(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req passwordReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.VaultReset(sess.User, req.Password); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleVaultList(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	items, err := s.Core.VaultList(sess.User, qInt(r, "parent"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleVaultMkdir(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Parent int64  `json:"parent"`
		Name   string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	it, err := s.Core.VaultMkdir(sess.User, req.Parent, req.Name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) handleVaultRename(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.VaultRename(sess.User, req.ID, req.Name); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleVaultDelete(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req idsReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.VaultDelete(sess.User, req.IDs); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleVaultContent(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	rd, it, err := s.Core.VaultOpen(sess.User, pathID(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	defer rd.Close()
	h := w.Header()
	h.Set("Content-Type", it.Mime)
	kind := "inline"
	if r.URL.Query().Get("download") == "1" || it.Viewer == "download" {
		kind = "attachment"
	}
	h.Set("Content-Disposition", contentDisposition(kind, it.Name))
	if it.Mime != "application/pdf" {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
	}
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", time.UnixMilli(it.MTime), rd)
}
