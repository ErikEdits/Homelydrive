// Package app wires the PC app together: settings, upload engine, virtual
// drive, tray icon, notifications and the local window (a small web page
// served on 127.0.0.1 and shown in an app window).
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/erikedits/homelydrive/internal/appwindow"
	"github.com/erikedits/homelydrive/internal/client/config"
	"github.com/erikedits/homelydrive/internal/client/drive"
	"github.com/erikedits/homelydrive/internal/client/engine"
	"github.com/erikedits/homelydrive/internal/client/integration"
	"github.com/erikedits/homelydrive/internal/client/notify"
	"github.com/erikedits/homelydrive/internal/client/remote"
	"github.com/erikedits/homelydrive/internal/client/state"
	"github.com/erikedits/homelydrive/internal/client/tray"
	"github.com/erikedits/homelydrive/internal/version"
)

// App is the running PC app.
type App struct {
	Cfg   *config.Store
	St    *state.DB
	Eng   *engine.Engine
	Drive *drive.Drive
	Log   *slog.Logger
	Web   fs.FS

	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	client       *remote.Client
	me           *remote.User
	connected    bool
	driveMounted bool
	driveErr     string
	update       *version.Release
	eventsCancel context.CancelFunc
	tray         *tray.Tray

	secret string
	port   int
	ln     net.Listener

	subsMu sync.Mutex
	subs   map[chan struct{}]struct{}
	notify *time.Timer
}

// IPC is written to ipc.json so that "homelydrive upload FILE" can talk to
// the running app.
type IPC struct {
	Port   int    `json:"port"`
	Secret string `json:"secret"`
	PID    int    `json:"pid"`
}

// IPCPath is where the running instance publishes its port.
func IPCPath() string { return filepath.Join(config.Dir(), "ipc.json") }

// ReadIPC reads the connection data of the running instance.
func ReadIPC() (*IPC, error) {
	b, err := os.ReadFile(IPCPath())
	if err != nil {
		return nil, err
	}
	var i IPC
	return &i, json.Unmarshal(b, &i)
}

// New creates the app (nothing runs yet).
func New(log *slog.Logger, web fs.FS) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	st, err := state.Open(config.Dir())
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, St: st, Log: log, Web: web, subs: map[chan struct{}]struct{}{}}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.Eng = engine.New(cfg, st, log)
	a.Eng.Notify = notify.Show
	a.Eng.OnChange = a.changed
	c := cfg.Get()
	a.Drive = drive.New(a.Client, func(local, space, rp string, done func(error)) {
		a.Eng.UploadReplace(local, space, rp, func(_ string, err error) { done(err) })
	}, c.Language, filepath.Join(config.Dir(), "cache"), c.DriveCacheMB, log)
	a.Drive.IsJunk = engine.IsJunk
	notify.Setup(config.Dir(), tray.IconPNG())
	notify.SetEnabled(c.Notifications)
	b := make([]byte, 24)
	rand.Read(b)
	a.secret = hex.EncodeToString(b)
	return a, nil
}

// Client returns the server connection (nil when logged out).
func (a *App) Client() *remote.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client
}

// Start launches everything except the tray (which needs the main thread).
func (a *App) Start() error {
	if err := a.listen(); err != nil {
		return err
	}
	go a.Eng.Run(a.ctx)
	c := a.Cfg.Get()
	if c.Token != "" && c.Server != "" {
		a.connect(remote.New(c.Server, c.Token))
	}
	if !c.Integration {
		if exe, err := os.Executable(); err == nil {
			if integration.Install(exe, c.Language) == nil {
				a.Cfg.Update(func(c *config.Config) { c.Integration = true })
			}
		}
	}
	go a.updateLoop()
	go a.trayLoop()
	return nil
}

// Stop shuts the app down.
func (a *App) Stop() {
	a.Drive.Unmount()
	a.cancel()
	if a.ln != nil {
		a.ln.Close()
	}
	os.Remove(IPCPath())
	a.St.Close()
}

func (a *App) connect(c *remote.Client) {
	a.mu.Lock()
	a.client = c
	if a.eventsCancel != nil {
		a.eventsCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.eventsCancel = cancel
	a.mu.Unlock()
	a.Eng.SetClient(c)
	go func() {
		ctx2, cancel2 := context.WithTimeout(ctx, 20*time.Second)
		defer cancel2()
		if me, err := c.Me(ctx2); err == nil {
			a.mu.Lock()
			a.me = me
			a.mu.Unlock()
			a.changed()
		} else if remote.StatusOf(err) == 401 {
			a.Log.Warn("token rejected by server, please log in again")
			notify.Show("Homelydrive", "Bitte melde dich erneut an.")
			a.Logout()
			return
		}
	}()
	go c.Events(ctx, a.onServerEvent)
	if a.Cfg.Get().DriveEnabled {
		go a.MountDrive()
	}
	a.changed()
}

func (a *App) onServerEvent(ev remote.Event) {
	switch ev.Type {
	case "connected":
		a.mu.Lock()
		a.connected = true
		a.mu.Unlock()
		a.changed()
	case "disconnected":
		a.mu.Lock()
		a.connected = false
		a.mu.Unlock()
		a.changed()
	case "changed":
		var d struct {
			SpaceID int64 `json:"space_id"`
		}
		json.Unmarshal(ev.Data, &d)
		a.Drive.InvalidateSpaceID(d.SpaceID)
	case "roots_changed":
		a.Drive.Invalidate("")
	case "notification":
		var n struct {
			Kind string         `json:"kind"`
			Data map[string]any `json:"data"`
		}
		json.Unmarshal(ev.Data, &n)
		a.showServerNotification(n.Kind, n.Data)
	}
}

func (a *App) showServerNotification(kind string, d map[string]any) {
	s := func(k string) string { v, _ := d[k].(string); return v }
	switch kind {
	case "share":
		notify.Show("Neu für dich freigegeben", fmt.Sprintf("%s hat „%s“ mit dir geteilt.", s("from"), s("name")))
	case "link_upload":
		notify.Show("Neue Datei über Upload-Link", fmt.Sprintf("„%s“ in „%s“", s("name"), s("folder")))
	case "quota_warning":
		notify.Show("Speicher fast voll", fmt.Sprintf("Dein Speicher ist zu %v %% belegt.", d["percent"]))
	case "disk_warning":
		notify.Show("Festplatte fast voll", fmt.Sprintf("%s ist zu %v %% belegt.", s("path"), d["percent"]))
	}
}

// NormalizeServer turns user input into a server URL ("meinpc" ->
// "http://meinpc:8420").
func NormalizeServer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("bitte die Server-Adresse eingeben")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", errors.New("ungültige Server-Adresse")
	}
	if u.Port() == "" && u.Scheme == "http" {
		u.Host = net.JoinHostPort(u.Hostname(), "8420")
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

// Login connects to a server.
func (a *App) Login(server, username, password string) error {
	server, err := NormalizeServer(server)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	if err := remote.Ping(ctx, server); err != nil {
		var re *remote.Error
		if errors.As(err, &re) {
			return err
		}
		return fmt.Errorf("Server nicht erreichbar (%v). Läuft Tailscale auf beiden Geräten?", err)
	}
	tok, _, err := remote.Login(ctx, server, username, password, a.Cfg.Get().DeviceName)
	if err != nil {
		if remote.StatusOf(err) == 401 {
			return errors.New("Benutzername oder Passwort falsch")
		}
		return err
	}
	a.Cfg.Update(func(c *config.Config) {
		c.Server, c.Token, c.Username = server, tok, username
	})
	a.connect(remote.New(server, tok))
	a.Log.Info("logged in", "server", server, "user", username)
	return nil
}

// Logout disconnects and forgets the token.
func (a *App) Logout() {
	if c := a.Client(); c != nil {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		c.Logout(ctx)
		cancel()
	}
	a.mu.Lock()
	a.client, a.me, a.connected = nil, nil, false
	if a.eventsCancel != nil {
		a.eventsCancel()
	}
	a.mu.Unlock()
	a.UnmountDrive()
	a.Eng.SetClient(nil)
	a.Cfg.Update(func(c *config.Config) { c.Token = "" })
	a.changed()
}

// ----- drive -----

// MountDrive mounts the virtual drive.
func (a *App) MountDrive() error {
	a.mu.Lock()
	if a.driveMounted || a.client == nil {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	err := a.Drive.Mount(a.Cfg.Get().DriveMount)
	a.mu.Lock()
	a.driveMounted = err == nil
	a.driveErr = ""
	if err != nil {
		a.driveErr = err.Error()
	}
	a.mu.Unlock()
	if err != nil {
		a.Log.Warn("drive mount failed", "err", err)
	}
	a.changed()
	return err
}

// UnmountDrive removes the virtual drive.
func (a *App) UnmountDrive() {
	a.mu.Lock()
	mounted := a.driveMounted
	a.driveMounted = false
	a.mu.Unlock()
	if mounted {
		a.Drive.Unmount()
	}
	a.changed()
}

// ----- status -----

// Status is what the window and tray show.
type Status struct {
	Version      string           `json:"version"`
	OS           string           `json:"os"`
	Server       string           `json:"server"`
	Username     string           `json:"username"`
	User         *remote.User     `json:"user,omitempty"`
	Connected    bool             `json:"connected"`
	Engine       engine.Status    `json:"engine"`
	Config       config.Config    `json:"config"`
	DriveOK      bool             `json:"drive_supported"`
	DriveMounted bool             `json:"drive_mounted"`
	DriveError   string           `json:"drive_error,omitempty"`
	CacheSize    int64            `json:"cache_size"`
	Update       *version.Release `json:"update,omitempty"`
}

// Status returns the current state.
func (a *App) Status() Status {
	c := a.Cfg.Get()
	c.Token = ""
	a.mu.Lock()
	st := Status{
		Version:      version.Version,
		OS:           runtime.GOOS,
		Server:       c.Server,
		Username:     c.Username,
		User:         a.me,
		Connected:    a.connected,
		Config:       c,
		DriveOK:      drive.Supported,
		DriveMounted: a.driveMounted,
		DriveError:   a.driveErr,
		Update:       a.update,
	}
	a.mu.Unlock()
	st.Engine = a.Eng.Status()
	st.CacheSize = a.Drive.CacheSize()
	return st
}

// changed notifies the window (debounced).
func (a *App) changed() {
	a.subsMu.Lock()
	defer a.subsMu.Unlock()
	if a.notify != nil {
		return
	}
	a.notify = time.AfterFunc(300*time.Millisecond, func() {
		a.subsMu.Lock()
		a.notify = nil
		for ch := range a.subs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		a.subsMu.Unlock()
	})
}

func (a *App) subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	a.subsMu.Lock()
	a.subs[ch] = struct{}{}
	a.subsMu.Unlock()
	return ch, func() {
		a.subsMu.Lock()
		delete(a.subs, ch)
		a.subsMu.Unlock()
	}
}

func (a *App) updateLoop() {
	for {
		if a.Cfg.Get().UpdateCheck {
			ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
			if rel, err := version.CheckLatest(ctx); err == nil {
				a.mu.Lock()
				prev := a.update
				if rel.Newer {
					a.update = rel
				}
				a.mu.Unlock()
				if rel.Newer && (prev == nil || prev.Version != rel.Version) {
					notify.Show("Neue Version verfügbar", "Homelydrive "+rel.Version+" kann auf GitHub heruntergeladen werden.")
					a.changed()
				}
			}
			cancel()
		}
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

// ----- tray -----

// SetTray connects the tray icon (called once it is ready).
func (a *App) SetTray(t *tray.Tray) {
	a.mu.Lock()
	a.tray = t
	a.mu.Unlock()
	a.refreshTray()
}

func (a *App) trayLoop() {
	ch, cancel := a.subscribe()
	defer cancel()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	lastMe := time.Now()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ch:
		case <-t.C:
		}
		if time.Since(lastMe) > time.Minute {
			lastMe = time.Now()
			go a.refreshMe()
		}
		a.refreshTray()
	}
}

// refreshMe updates the storage usage shown in the window.
func (a *App) refreshMe() {
	c := a.Client()
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	if me, err := c.Me(ctx); err == nil {
		a.mu.Lock()
		a.me = me
		a.mu.Unlock()
		a.changed()
	}
}

func (a *App) refreshTray() {
	a.mu.Lock()
	t := a.tray
	a.mu.Unlock()
	if t == nil {
		return
	}
	st := a.Status()
	en := st.Config.Language == "en"
	tr := func(de, eng string) string {
		if en {
			return eng
		}
		return de
	}
	state, text := tray.Normal, tr("Alles hochgeladen", "Everything uploaded")
	switch {
	case !st.Engine.LoggedIn:
		state, text = tray.Offline, tr("Nicht angemeldet", "Not signed in")
	case st.Engine.Paused:
		state, text = tray.Paused, tr("Pausiert", "Paused")
	case !st.Engine.Online:
		state, text = tray.Offline, tr("Server nicht erreichbar", "Server unreachable")
	case len(st.Engine.Active) > 0 || st.Engine.Queue > 0:
		n := len(st.Engine.Active) + st.Engine.Queue
		state, text = tray.Syncing, fmt.Sprintf(tr("Lade %d Datei(en) hoch …", "Uploading %d file(s) …"), n)
	case st.Engine.LastError != "":
		state, text = tray.Error, tr("Fehler – siehe Verlauf", "Error – see history")
	}
	t.Update(state, text, st.Engine.Paused, st.DriveMounted)
}

// TogglePause pauses or resumes uploads.
func (a *App) TogglePause() {
	a.SetPaused(!a.Cfg.Get().Paused)
}

// SetPaused sets the pause state.
func (a *App) SetPaused(p bool) {
	a.Cfg.Update(func(c *config.Config) { c.Paused = p })
	a.Eng.SetClient(a.Client()) // wakes the workers
	a.changed()
}

// ----- windows and folders -----

// WindowURL is the address of the local window (logs the browser in).
func (a *App) WindowURL(page string) string {
	u := fmt.Sprintf("http://127.0.0.1:%d/auth?t=%s", a.port, a.secret)
	if page != "" {
		u += "&p=" + url.QueryEscape(page)
	}
	return u
}

// ShowWindow opens the app window (optionally on a page: "history", "settings").
func (a *App) ShowWindow(page string) {
	profile := filepath.Join(config.Dir(), "window")
	if _, err := appwindow.Open(a.WindowURL(page), profile, 1040, 740); err != nil {
		a.Log.Warn("cannot open window", "err", err)
	}
}

// OpenWeb opens the server's web interface in the default browser.
func (a *App) OpenWeb() {
	if s := a.Cfg.Get().Server; s != "" {
		appwindow.OpenDefault(s)
	}
}

// OpenDrive opens the virtual drive in the file manager.
func (a *App) OpenDrive() {
	m := a.Cfg.Get().DriveMount
	if runtime.GOOS == "windows" && len(m) == 2 && m[1] == ':' {
		m += `\`
	}
	appwindow.OpenDefault(m)
}
