package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"

	"github.com/erikedits/homelydrive/internal/appwindow"
	"github.com/erikedits/homelydrive/internal/client/config"
	"github.com/erikedits/homelydrive/internal/client/integration"
	"github.com/erikedits/homelydrive/internal/client/notify"
	"github.com/erikedits/homelydrive/internal/client/remote"
)

// ErrAlreadyRunning is returned by Start when another instance is running.
var ErrAlreadyRunning = errors.New("Homelydrive läuft bereits")

// preferredPort gives every user a stable local port (single instance).
func preferredPort() int {
	return 47000 + int(crc32.ChecksumIEEE([]byte(config.Dir()))%1000)
}

func (a *App) listen() error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort()))
	if err != nil {
		if ipc, e := ReadIPC(); e == nil && pingIPC(ipc) {
			return ErrAlreadyRunning
		}
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
	}
	a.ln = ln
	a.port = ln.Addr().(*net.TCPAddr).Port
	b, _ := json.Marshal(IPC{Port: a.port, Secret: a.secret, PID: os.Getpid()})
	if err := os.WriteFile(IPCPath(), b, 0o600); err != nil {
		return err
	}
	srv := &http.Server{Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	return nil
}

func pingIPC(ipc *IPC) bool {
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/local/ping", ipc.Port), nil)
	req.Header.Set("Authorization", "Bearer "+ipc.Secret)
	cl := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := cl.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Call sends a command to the running instance (used by the CLI).
func Call(method, p string, in, out any) error {
	ipc, err := ReadIPC()
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", ipc.Port, p), body)
	req.Header.Set("Authorization", "Bearer "+ipc.Secret)
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{Timeout: 30 * time.Minute, Transport: &http.Transport{Proxy: nil}}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		return errors.New(e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Running reports whether an instance answers.
func Running() bool {
	ipc, err := ReadIPC()
	return err == nil && pingIPC(ipc)
}

// ----- HTTP -----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if s := remote.StatusOf(err); s >= 400 {
		status = s
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func ok(w http.ResponseWriter) { writeJSON(w, 200, map[string]bool{"ok": true}) }

func decode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

// authorized checks the secret (cookie for the window, header for the CLI)
// and protects against other websites (DNS rebinding, CSRF).
func (a *App) authorized(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host != "127.0.0.1" && host != "localhost" {
		return false
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return subtle.ConstantTimeCompare([]byte(h[7:]), []byte(a.secret)) == 1
	}
	c, err := r.Cookie("hdc")
	if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.secret)) != 1 {
		return false
	}
	if r.Method != http.MethodGet && r.Header.Get("X-HD") != "1" {
		return false
	}
	return true
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if !a.authorized(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			fn(w, r)
		})
	}
	mux.HandleFunc("GET /auth", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(a.secret)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "hdc", Value: a.secret, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		target := "/"
		if p := r.URL.Query().Get("p"); p != "" {
			target += "#/" + url.PathEscape(p)
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
	h("GET /local/ping", func(w http.ResponseWriter, r *http.Request) { ok(w) })
	h("GET /local/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.Status()) })
	h("GET /local/events", a.handleEvents)
	h("POST /local/login", a.handleLogin)
	h("POST /local/logout", func(w http.ResponseWriter, r *http.Request) { a.Logout(); ok(w) })
	h("GET /local/roots", a.handleRoots)
	h("GET /local/remote/list", a.handleRemoteList)
	h("POST /local/remote/mkdir", a.handleRemoteMkdir)
	h("GET /local/folders", a.handleFolders)
	h("POST /local/folders", a.handleAddFolder)
	h("PUT /local/folders/{id}", a.handleUpdateFolder)
	h("DELETE /local/folders/{id}", a.handleDeleteFolder)
	h("PUT /local/settings", a.handleSettings)
	h("POST /local/pause", a.handlePause)
	h("POST /local/rescan", func(w http.ResponseWriter, r *http.Request) { go a.Eng.Rescan(); ok(w) })
	h("POST /local/drive", a.handleDrive)
	h("POST /local/drive/clear-cache", func(w http.ResponseWriter, r *http.Request) { a.Drive.ClearCache(); a.changed(); ok(w) })
	h("GET /local/history", a.handleHistory)
	h("POST /local/history/retry", a.handleRetry)
	h("POST /local/history/clear", func(w http.ResponseWriter, r *http.Request) { a.St.ClearHistory(); ok(w) })
	h("GET /local/browse", a.handleBrowse)
	h("POST /local/upload", a.handleUpload)
	h("POST /local/put", a.handlePut)
	h("POST /local/download", a.handleDownload)
	h("POST /local/link", a.handleLink)
	h("POST /local/open", a.handleOpen)
	h("POST /local/integration", a.handleIntegration)
	h("POST /local/show", func(w http.ResponseWriter, r *http.Request) {
		a.ShowWindow(r.URL.Query().Get("page"))
		ok(w)
	})
	h("POST /local/quit", func(w http.ResponseWriter, r *http.Request) {
		ok(w)
		go func() { time.Sleep(200 * time.Millisecond); a.Quit() }()
	})
	mux.HandleFunc("GET /", a.serveUI)
	return mux
}

// Quit is set by the main program (ends the tray loop).
var QuitFunc = func() { os.Exit(0) }

// Quit stops the app.
func (a *App) Quit() {
	a.Stop()
	QuitFunc()
}

func (a *App) serveUI(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		http.Error(w, "Bitte über das Homelydrive-Symbol öffnen.", http.StatusForbidden)
		return
	}
	if a.Web == nil {
		http.Error(w, "UI not built", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" || p == "index.html" {
		p = "client.html"
	}
	if fi, err := fs.Stat(a.Web, p); err != nil || fi.IsDir() {
		p = "client.html"
	}
	if strings.HasSuffix(p, ".html") {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, a.Web, p)
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, okf := w.(http.Flusher)
	if !okf {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch, cancel := a.subscribe()
	defer cancel()
	send := func() {
		b, _ := json.Marshal(a.Status())
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	send()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			send()
		case <-t.C:
			// While uploading, refresh progress every second.
			if st := a.Eng.Status(); len(st.Active) > 0 {
				send()
			}
		}
	}
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Server, Username, Password string }
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := a.Login(req.Server, req.Username, req.Password); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (a *App) needClient(w http.ResponseWriter) *remote.Client {
	c := a.Client()
	if c == nil {
		fail(w, errors.New("nicht angemeldet"))
	}
	return c
}

func (a *App) handleRoots(w http.ResponseWriter, r *http.Request) {
	c := a.needClient(w)
	if c == nil {
		return
	}
	roots, err := c.Roots(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, roots)
}

func (a *App) handleRemoteList(w http.ResponseWriter, r *http.Request) {
	c := a.needClient(w)
	if c == nil {
		return
	}
	l, err := c.List(r.Context(), r.URL.Query().Get("space"), r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, l)
}

func (a *App) handleRemoteMkdir(w http.ResponseWriter, r *http.Request) {
	c := a.needClient(w)
	if c == nil {
		return
	}
	var req struct{ Space, Path string }
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := c.Mkdir(r.Context(), req.Space, req.Path); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

type folderJSON struct {
	config.Folder
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
	Error string `json:"error,omitempty"`
}

func (a *App) handleFolders(w http.ResponseWriter, r *http.Request) {
	out := []folderJSON{}
	for _, f := range a.Cfg.Get().Folders {
		fj := folderJSON{Folder: f}
		fj.Files, fj.Bytes = a.St.FolderStats(f.ID)
		if fi, err := os.Stat(f.Local); err != nil || !fi.IsDir() {
			fj.Error = "Ordner nicht gefunden"
		}
		out = append(out, fj)
	}
	writeJSON(w, 200, out)
}

func (a *App) handleAddFolder(w http.ResponseWriter, r *http.Request) {
	var req struct{ Local, Space, Remote string }
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	local, err := filepath.Abs(strings.TrimSpace(req.Local))
	if err != nil {
		fail(w, err)
		return
	}
	if fi, err := os.Stat(local); err != nil || !fi.IsDir() {
		fail(w, fmt.Errorf("der Ordner %q existiert nicht", local))
		return
	}
	for _, f := range a.Cfg.Get().Folders {
		if strings.EqualFold(f.Local, local) {
			fail(w, errors.New("dieser Ordner wird schon hochgeladen"))
			return
		}
	}
	if req.Space == "" {
		req.Space = "home"
	}
	req.Remote = strings.Trim(strings.ReplaceAll(req.Remote, "\\", "/"), "/")
	if req.Remote == "" {
		req.Remote = "PC/" + filepath.Base(local)
	}
	if c := a.Client(); c != nil {
		if err := c.Mkdir(r.Context(), req.Space, req.Remote); err != nil && remote.StatusOf(err) != 409 {
			fail(w, err)
			return
		}
	}
	f := config.Folder{ID: config.NewID(), Local: local, Space: req.Space, Remote: req.Remote, Enabled: true}
	a.Cfg.Update(func(c *config.Config) { c.Folders = append(c.Folders, f) })
	a.Eng.RescanFolder(f.ID)
	a.changed()
	writeJSON(w, 200, f)
}

func (a *App) handleUpdateFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	id := r.PathValue("id")
	a.Cfg.Update(func(c *config.Config) {
		for i := range c.Folders {
			if c.Folders[i].ID == id && req.Enabled != nil {
				c.Folders[i].Enabled = *req.Enabled
			}
		}
	})
	if req.Enabled != nil && *req.Enabled {
		a.Eng.RescanFolder(id)
	} else {
		a.Eng.Unwatch(id)
	}
	a.changed()
	ok(w)
}

func (a *App) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.Eng.Unwatch(id)
	a.Cfg.Update(func(c *config.Config) {
		out := c.Folders[:0]
		for _, f := range c.Folders {
			if f.ID != id {
				out = append(out, f)
			}
		}
		c.Folders = out
	})
	a.St.ForgetFolder(id)
	a.changed()
	ok(w)
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BandwidthKBps *int    `json:"bandwidth_kbps"`
		Notifications *bool   `json:"notifications"`
		UploadFolder  *string `json:"upload_folder"`
		DownloadDir   *string `json:"download_dir"`
		Language      *string `json:"language"`
		DriveMount    *string `json:"drive_mount"`
		DriveCacheMB  *int    `json:"drive_cache_mb"`
		UpdateCheck   *bool   `json:"update_check"`
		DeviceName    *string `json:"device_name"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	a.Cfg.Update(func(c *config.Config) {
		if req.BandwidthKBps != nil && *req.BandwidthKBps >= 0 {
			c.BandwidthKBps = *req.BandwidthKBps
		}
		if req.Notifications != nil {
			c.Notifications = *req.Notifications
		}
		if req.UploadFolder != nil {
			c.UploadFolder = strings.Trim(*req.UploadFolder, "/")
		}
		if req.DownloadDir != nil {
			c.DownloadDir = *req.DownloadDir
		}
		if req.Language != nil && (*req.Language == "de" || *req.Language == "en") {
			c.Language = *req.Language
		}
		if req.DriveMount != nil && *req.DriveMount != "" {
			c.DriveMount = *req.DriveMount
		}
		if req.DriveCacheMB != nil && *req.DriveCacheMB >= 100 {
			c.DriveCacheMB = *req.DriveCacheMB
		}
		if req.UpdateCheck != nil {
			c.UpdateCheck = *req.UpdateCheck
		}
		if req.DeviceName != nil && *req.DeviceName != "" {
			c.DeviceName = *req.DeviceName
		}
	})
	c := a.Cfg.Get()
	a.Eng.SetBandwidth(c.BandwidthKBps)
	notify.SetEnabled(c.Notifications)
	if req.Language != nil && c.Integration {
		if exe, err := integration.SelfPath(); err == nil {
			integration.Install(exe, c.Language)
		}
	}
	a.changed()
	writeJSON(w, 200, a.Status())
}

func (a *App) handlePause(w http.ResponseWriter, r *http.Request) {
	var req struct{ Paused bool }
	decode(r, &req)
	a.SetPaused(req.Paused)
	ok(w)
}

func (a *App) handleDrive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool   `json:"enabled"`
		Mount   string `json:"mount"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	a.UnmountDrive()
	a.Cfg.Update(func(c *config.Config) {
		c.DriveEnabled = req.Enabled
		if req.Mount != "" {
			c.DriveMount = req.Mount
		}
	})
	if req.Enabled {
		if err := a.MountDrive(); err != nil {
			fail(w, err)
			return
		}
	}
	writeJSON(w, 200, a.Status())
}

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	list, err := a.St.History(before, 200, r.URL.Query().Get("errors") == "1")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (a *App) handleRetry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID  int64 `json:"id"`
		All bool  `json:"all"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	var entries []int64
	if req.All {
		list, _ := a.St.History(0, 5000, true)
		for _, e := range list {
			entries = append(entries, e.ID)
		}
	} else {
		entries = []int64{req.ID}
	}
	for _, id := range entries {
		if e, err := a.St.HistoryEntry(id); err == nil && e.Status == "error" {
			a.Eng.Retry(e)
			a.St.MarkRetried(id)
		}
	}
	ok(w)
}

type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleBrowse lists local folders for the folder picker of the window.
func (a *App) handleBrowse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	out := []dirEntry{}
	if p == "" {
		home, _ := os.UserHomeDir()
		out = append(out, dirEntry{Name: home, Path: home})
		for _, sub := range []string{"Desktop", "Documents", "Dokumente", "Pictures", "Bilder", "Videos", "Music", "Musik", "Downloads"} {
			if fi, err := os.Stat(filepath.Join(home, sub)); err == nil && fi.IsDir() {
				out = append(out, dirEntry{Name: sub, Path: filepath.Join(home, sub)})
			}
		}
		if runtime.GOOS == "windows" {
			for c := 'C'; c <= 'Z'; c++ {
				d := string(c) + `:\`
				if _, err := os.Stat(d); err == nil {
					out = append(out, dirEntry{Name: d, Path: d})
				}
			}
		} else {
			out = append(out, dirEntry{Name: "/", Path: "/"})
		}
		writeJSON(w, 200, map[string]any{"path": "", "parent": "", "dirs": out})
		return
	}
	p = filepath.Clean(p)
	des, err := os.ReadDir(p)
	if err != nil {
		fail(w, err)
		return
	}
	for _, de := range des {
		if de.IsDir() && !strings.HasPrefix(de.Name(), ".") && !strings.HasPrefix(de.Name(), "$") {
			out = append(out, dirEntry{Name: de.Name(), Path: filepath.Join(p, de.Name())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	writeJSON(w, 200, map[string]any{"path": p, "parent": parent, "dirs": out})
}

// handleUpload queues local paths (context menu "Auf Homelydrive hochladen").
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths  []string `json:"paths"`
		Space  string   `json:"space"`
		Remote string   `json:"remote"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if a.Client() == nil {
		a.ShowWindow("")
		fail(w, errors.New("bitte zuerst anmelden"))
		return
	}
	if req.Space == "" {
		req.Space = "home"
	}
	if req.Remote == "" {
		req.Remote = a.Cfg.Get().UploadFolder
	}
	n := a.Eng.Upload(req.Paths, req.Space, req.Remote, nil)
	if n > 0 {
		notify.Show("Homelydrive", fmt.Sprintf("%d Datei(en) werden hochgeladen.", n))
	}
	writeJSON(w, 200, map[string]int{"queued": n})
}

// handlePut uploads a file chosen in the window (streamed through the app).
func (a *App) handlePut(w http.ResponseWriter, r *http.Request) {
	c := a.needClient(w)
	if c == nil {
		return
	}
	q := r.URL.Query()
	if r.ContentLength < 0 {
		fail(w, errors.New("Content-Length fehlt"))
		return
	}
	mtime, _ := strconv.ParseInt(q.Get("mtime"), 10, 64)
	st, err := c.CreateUpload(r.Context(), remote.UploadRequest{Space: q.Get("space"), Path: q.Get("path"), Size: r.ContentLength, MTime: mtime, Conflict: "rename"})
	if err != nil {
		fail(w, err)
		return
	}
	if st.Status == "pending" {
		st, err = c.UploadChunk(r.Context(), st.ID, 0, r.Body, r.ContentLength)
		if err != nil {
			fail(w, err)
			return
		}
	}
	writeJSON(w, 200, st)
}

// handleDownload saves a server file (or a folder as ZIP) to the downloads folder.
func (a *App) handleDownload(w http.ResponseWriter, r *http.Request) {
	c := a.needClient(w)
	if c == nil {
		return
	}
	var req struct {
		Space string `json:"space"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	dir := a.Cfg.Get().DownloadDir
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	os.MkdirAll(dir, 0o755)
	name := path.Base(req.Path)
	if req.IsDir {
		name += ".zip"
	}
	target := uniqueLocal(filepath.Join(dir, name))
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 24*time.Hour)
		defer cancel()
		var rc io.ReadCloser
		var err error
		if req.IsDir {
			rc, err = c.DownloadZip(ctx, req.Space, req.Path)
		} else {
			rc, _, err = c.Download(ctx, req.Space, req.Path, 0, -1)
		}
		if err == nil {
			err = saveTo(target, rc)
			rc.Close()
		}
		if err != nil {
			notify.Show("Download fehlgeschlagen", fmt.Sprintf("%s: %v", name, err))
			return
		}
		notify.Show("Download fertig", filepath.Base(target)+" → "+dir)
	}()
	writeJSON(w, 200, map[string]string{"path": target})
}

func uniqueLocal(p string) string {
	if _, err := os.Stat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for n := 1; ; n++ {
		cand := fmt.Sprintf("%s (%d)%s", base, n, ext)
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
	}
}

func saveTo(target string, r io.Reader) error {
	tmp := target + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, target)
}

// handleLink creates a download link for a local file ("Link kopieren").
func (a *App) handleLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path   string `json:"path"`
		Space  string `json:"space"`
		Remote string `json:"remote"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	link, err := a.LinkFor(r.Context(), req.Path, req.Space, req.Remote)
	if err != nil {
		notify.Show("Link nicht möglich", err.Error())
		fail(w, err)
		return
	}
	copied := clipboard.WriteAll(link) == nil
	if copied {
		notify.Show("Link kopiert", link)
	} else {
		notify.Show("Homelydrive-Link", link)
	}
	writeJSON(w, 200, map[string]any{"url": link, "copied": copied})
}

// LinkFor returns a share link for a server path or a local file, uploading
// the file first if it is not on the server yet.
func (a *App) LinkFor(ctx context.Context, local, space, remotePath string) (string, error) {
	c := a.Client()
	if c == nil {
		return "", errors.New("nicht angemeldet")
	}
	if local != "" {
		abs, err := filepath.Abs(local)
		if err != nil {
			return "", err
		}
		if s, rp, ok := a.locateOnDrive(abs); ok {
			space, remotePath = s, rp
		} else if s, rp, uploaded, ok := a.Eng.RemoteFor(abs); ok && uploaded {
			space, remotePath = s, rp
		} else {
			// Upload first, then create the link.
			done := make(chan error, 1)
			var got string
			target := a.Cfg.Get().UploadFolder
			if ok {
				target = path.Dir(rp)
			}
			a.Eng.Upload([]string{abs}, defaultSpace(s), target, func(p string, err error) {
				got = p
				done <- err
			})
			notify.Show("Homelydrive", "Datei wird hochgeladen, danach wird der Link kopiert …")
			select {
			case err := <-done:
				if err != nil {
					return "", err
				}
			case <-ctx.Done():
				return "", ctx.Err()
			}
			space, remotePath = defaultSpace(s), got
		}
	}
	l, err := c.CreateLink(ctx, defaultSpace(space), remotePath)
	if err != nil {
		return "", err
	}
	return c.LinkURL(l), nil
}

func defaultSpace(s string) string {
	if s == "" {
		return "home"
	}
	return s
}

// locateOnDrive maps a path on the virtual drive to the server.
func (a *App) locateOnDrive(abs string) (string, string, bool) {
	a.mu.Lock()
	mounted := a.driveMounted
	a.mu.Unlock()
	if !mounted {
		return "", "", false
	}
	mount := a.Cfg.Get().DriveMount
	if runtime.GOOS == "windows" && len(mount) == 2 {
		mount += `\`
	}
	rel, err := filepath.Rel(mount, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", "", false
	}
	return a.Drive.Locate(filepath.ToSlash(rel))
}

func (a *App) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct{ Target, Path string }
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	switch req.Target {
	case "web":
		a.OpenWeb()
	case "drive":
		a.OpenDrive()
	case "downloads":
		appwindow.OpenDefault(a.Cfg.Get().DownloadDir)
	case "release":
		if a.update != nil {
			appwindow.OpenDefault(a.update.URL)
		}
	case "path":
		if fi, err := os.Stat(req.Path); err == nil {
			p := req.Path
			if !fi.IsDir() {
				p = filepath.Dir(p)
			}
			appwindow.OpenDefault(p)
		}
	}
	ok(w)
}

func (a *App) handleIntegration(w http.ResponseWriter, r *http.Request) {
	var req struct{ Install bool }
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	var err error
	if req.Install {
		exe, _ := integration.SelfPath()
		err = integration.Install(exe, a.Cfg.Get().Language)
	} else {
		err = integration.Uninstall()
	}
	if err != nil {
		fail(w, err)
		return
	}
	a.Cfg.Update(func(c *config.Config) { c.Integration = req.Install })
	a.changed()
	ok(w)
}
