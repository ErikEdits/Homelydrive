package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/erikedits/homelydrive/internal/server/config"
	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/tailnet"
	"github.com/erikedits/homelydrive/internal/server/thumbs"
	"github.com/erikedits/homelydrive/internal/version"
)

// The admin API backs the "Server-Fenster". It only answers requests from
// the server PC itself and requires an admin login.

func (s *Server) handleAdminState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_needed": s.Core.UserCount() == 0,
		"version":      version.Version,
		"language":     s.Cfg.Get().Language,
	})
}

func (s *Server) handleAdminSetup(w http.ResponseWriter, r *http.Request) {
	if s.Core.UserCount() > 0 {
		s.fail(w, core.ErrForbidden)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Language string `json:"language"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if req.Language == "en" || req.Language == "de" {
		s.Cfg.Update(func(c *config.Config) { c.Language = req.Language })
	}
	u, err := s.Core.CreateUser(core.NewUser{Username: req.Username, Password: req.Password, IsAdmin: true})
	if err != nil {
		s.fail(w, err)
		return
	}
	tok, err := s.Core.CreateToken(u.ID, "Server-Fenster", true)
	if err != nil {
		s.fail(w, err)
		return
	}
	setCookie(w, r, adminCookie, tok, 30*24*3600)
	writeJSON(w, http.StatusOK, map[string]any{"token": tok})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	tok, _, err := s.Core.Login(req.Username, req.Password, "Server-Fenster", true)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Falsche Anmeldedaten oder kein Admin.", Code: "unauthorized"})
		return
	}
	setCookie(w, r, adminCookie, tok, 30*24*3600)
	writeJSON(w, http.StatusOK, map[string]any{"token": tok})
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	s.Core.Logout(sess.Token)
	setCookie(w, r, adminCookie, "", -1)
	ok(w)
}

func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	ts := tailnet.GetStatus(r.Context())
	cfg := s.Cfg.Get()
	scheme := "http"
	certErr := ""
	if cfg.HTTPS {
		scheme = "https"
		if s.Cert != nil {
			if err := s.Cert.LastError(); err != nil {
				certErr = err.Error()
			}
		}
	}
	var addresses []string
	if ts.DNSName != "" {
		addresses = append(addresses, scheme+"://"+ts.DNSName+":"+itoa(cfg.Port))
	}
	for _, ip := range ts.IPs {
		if strings.Contains(ip, ":") {
			continue
		}
		addresses = append(addresses, "http://"+ip+":"+itoa(cfg.Port))
	}
	var listen []string
	if s.Listeners != nil {
		listen = s.Listeners.Addrs()
	}
	st := map[string]any{
		"version":        version.Version,
		"os":             runtime.GOOS + "/" + runtime.GOARCH,
		"uptime_seconds": int(time.Since(s.Started).Seconds()),
		"tailscale":      ts,
		"addresses":      addresses,
		"listening":      listen,
		"https":          cfg.HTTPS,
		"https_error":    certErr,
		"locations":      s.Core.Locations(),
		"disk_warnings":  s.Core.DiskWarnings(),
		"users":          s.Core.UserCount(),
		"connections":    s.Core.Events.Online(),
		"ffmpeg":         thumbs.HaveFFmpeg(),
		"data_dir":       s.Core.DataDir,
		"config_file":    s.Cfg.Path(),
		"log_file":       s.Logs.Path(),
	}
	if up := s.Core.Update(); up != nil && up.Newer {
		st["update"] = up
	}
	writeJSON(w, http.StatusOK, st)
}

func itoa(n int) string { return strconv.Itoa(n) }

type adminUser struct {
	*core.User
	Used int64 `json:"used"`
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	users, err := s.Core.ListUsers()
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]adminUser, 0, len(users))
	for _, u := range users {
		out = append(out, adminUser{User: u, Used: s.Core.Usage(u.ID)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "default_quota_gb": s.Cfg.Get().DefaultQuotaGB})
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req core.NewUser
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	u, err := s.Core.CreateUser(req)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req core.UserUpdate
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.UpdateUser(pathID(r), req, sess.Token); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if pathID(r) == sess.User.ID {
		s.fail(w, &core.BadRequest{Msg: "Du kannst dich nicht selbst löschen."})
		return
	}
	if err := s.Core.DeleteUser(pathID(r), r.URL.Query().Get("delete_files") == "1"); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleAdminSpaces(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	teams, err := s.Core.ListSharedSpaces(core.KindTeam)
	if err != nil {
		s.fail(w, err)
		return
	}
	mounts, err := s.Core.ListSharedSpaces(core.KindMount)
	if err != nil {
		s.fail(w, err)
		return
	}
	if teams == nil {
		teams = []core.SharedSpace{}
	}
	if mounts == nil {
		mounts = []core.SharedSpace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": teams, "mounts": mounts})
}

type spaceReq struct {
	Kind     string        `json:"kind"`
	Name     string        `json:"name"`
	RootPath string        `json:"root_path"`
	Members  []core.Member `json:"members"`
}

func (s *Server) handleAdminCreateSpace(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req spaceReq
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	id, err := s.Core.CreateSharedSpace(req.Kind, req.Name, req.RootPath, req.Members)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.notifyRootsChanged()
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *Server) handleAdminUpdateSpace(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Name    *string        `json:"name"`
		Members *[]core.Member `json:"members"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	id := pathID(r)
	if req.Name != nil {
		if err := s.Core.RenameSharedSpace(id, *req.Name); err != nil {
			s.fail(w, err)
			return
		}
	}
	if req.Members != nil {
		if err := s.Core.SetMembers(id, *req.Members); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.notifyRootsChanged()
	ok(w)
}

func (s *Server) handleAdminDeleteSpace(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.DeleteSharedSpace(pathID(r), r.URL.Query().Get("delete_files") == "1"); err != nil {
		s.fail(w, err)
		return
	}
	s.notifyRootsChanged()
	ok(w)
}

func (s *Server) notifyRootsChanged() {
	users, _ := s.Core.ListUsers()
	for _, u := range users {
		s.Core.Events.Publish(u.ID, core.Event{Type: "roots_changed"})
	}
}

func (s *Server) handleAdminLocations(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	writeJSON(w, http.StatusOK, s.Core.Locations())
}

func (s *Server) handleAdminAddLocation(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Core.AddLocation(req.Path); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleAdminRemoveLocation(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	if err := s.Core.RemoveLocation(r.URL.Query().Get("path")); err != nil {
		s.fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	writeJSON(w, http.StatusOK, s.Cfg.Get())
}

func (s *Server) handleAdminUpdateSettings(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	var req struct {
		Port                *int      `json:"port"`
		DefaultQuotaGB      *int64    `json:"default_quota_gb"`
		TrashDays           *int      `json:"trash_days"`
		HTTPS               *bool     `json:"https"`
		UpdateCheck         *bool     `json:"update_check"`
		Language            *string   `json:"language"`
		ScanIntervalMinutes *int      `json:"scan_interval_minutes"`
		ListenAddrs         *[]string `json:"listen_addrs"`
	}
	if err := decode(r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if req.Port != nil && (*req.Port < 1 || *req.Port > 65535) {
		s.fail(w, &core.BadRequest{Msg: "Ungültiger Port."})
		return
	}
	if req.DefaultQuotaGB != nil && *req.DefaultQuotaGB < 0 {
		s.fail(w, &core.BadRequest{Msg: "Ungültiges Speicherlimit."})
		return
	}
	before := s.Cfg.Get()
	err := s.Cfg.Update(func(c *config.Config) {
		if req.Port != nil {
			c.Port = *req.Port
		}
		if req.DefaultQuotaGB != nil {
			c.DefaultQuotaGB = *req.DefaultQuotaGB
		}
		if req.TrashDays != nil {
			c.TrashDays = *req.TrashDays
		}
		if req.HTTPS != nil {
			c.HTTPS = *req.HTTPS
		}
		if req.UpdateCheck != nil {
			c.UpdateCheck = *req.UpdateCheck
		}
		if req.Language != nil {
			c.Language = *req.Language
		}
		if req.ScanIntervalMinutes != nil {
			c.ScanIntervalMinutes = *req.ScanIntervalMinutes
		}
		if req.ListenAddrs != nil {
			c.ListenAddrs = *req.ListenAddrs
		}
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	after := s.Cfg.Get()
	if s.Listeners != nil && (before.Port != after.Port || before.HTTPS != after.HTTPS ||
		strings.Join(before.ListenAddrs, ",") != strings.Join(after.ListenAddrs, ",")) {
		// Answer first, then switch the listeners (this request may run on one of them).
		go func() {
			time.Sleep(500 * time.Millisecond)
			s.Listeners.Restart(after.Port, after.ListenAddrs)
		}()
	}
	if after.UpdateCheck && !before.UpdateCheck {
		go s.Core.CheckUpdateNow(context.Background())
	}
	writeJSON(w, http.StatusOK, after)
}

func (s *Server) handleAdminLogs(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	writeJSON(w, http.StatusOK, s.Logs.Since(qInt(r, "since")))
}

func (s *Server) handleAdminRescan(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	go s.Core.ScanAll()
	ok(w)
}

type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleAdminBrowse lists sub folders so the server window can offer a
// folder picker for storage locations and mounts.
func (s *Server) handleAdminBrowse(w http.ResponseWriter, r *http.Request, sess *core.Session) {
	p := r.URL.Query().Get("path")
	out := []dirEntry{}
	if p == "" {
		if runtime.GOOS == "windows" {
			for c := 'A'; c <= 'Z'; c++ {
				d := string(c) + `:\`
				if _, err := os.Stat(d); err == nil {
					out = append(out, dirEntry{Name: d, Path: d})
				}
			}
		} else {
			out = append(out, dirEntry{Name: "/", Path: "/"})
			for _, d := range []string{"/home", "/mnt", "/media", "/srv"} {
				if fi, err := os.Stat(d); err == nil && fi.IsDir() {
					out = append(out, dirEntry{Name: d, Path: d})
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": "", "parent": "", "dirs": out})
		return
	}
	p = filepath.Clean(p)
	des, err := os.ReadDir(p)
	if err != nil {
		s.fail(w, &core.BadRequest{Msg: err.Error()})
		return
	}
	for _, de := range des {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") || strings.HasPrefix(de.Name(), "$") {
			continue
		}
		out = append(out, dirEntry{Name: de.Name(), Path: filepath.Join(p, de.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "parent": parent, "dirs": out})
}
