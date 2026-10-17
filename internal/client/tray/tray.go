// Package tray shows the Homelydrive icon in the taskbar / system tray with
// status, pause button and shortcuts.
package tray

import (
	_ "embed"
	"runtime"
	"sync"

	"fyne.io/systray"
)

var (
	//go:embed icons/normal.png
	normalPNG []byte
	//go:embed icons/sync.png
	syncPNG []byte
	//go:embed icons/paused.png
	pausedPNG []byte
	//go:embed icons/error.png
	errorPNG []byte
	//go:embed icons/offline.png
	offlinePNG []byte
	//go:embed icons/normal.ico
	normalICO []byte
	//go:embed icons/sync.ico
	syncICO []byte
	//go:embed icons/paused.ico
	pausedICO []byte
	//go:embed icons/error.ico
	errorICO []byte
	//go:embed icons/offline.ico
	offlineICO []byte
)

// IconPNG is the plain logo (used for notifications).
func IconPNG() []byte { return normalPNG }

// State selects the icon.
type State int

const (
	Normal State = iota
	Syncing
	Paused
	Error
	Offline
)

func icon(s State) []byte {
	win := runtime.GOOS == "windows"
	pick := func(png, ico []byte) []byte {
		if win {
			return ico
		}
		return png
	}
	switch s {
	case Syncing:
		return pick(syncPNG, syncICO)
	case Paused:
		return pick(pausedPNG, pausedICO)
	case Error:
		return pick(errorPNG, errorICO)
	case Offline:
		return pick(offlinePNG, offlineICO)
	}
	return pick(normalPNG, normalICO)
}

// Labels are the translated menu texts.
type Labels struct {
	Open, OpenWeb, OpenDrive, Pause, Resume, Upload, History, Settings, Quit string
}

// LabelsFor returns the menu texts for a language.
func LabelsFor(lang string) Labels {
	if lang == "en" {
		return Labels{"Open Homelydrive", "Open in browser", "Open drive", "Pause uploads", "Resume uploads", "Upload files …", "Upload history", "Settings", "Quit"}
	}
	return Labels{"Homelydrive öffnen", "Im Browser öffnen", "Laufwerk öffnen", "Uploads pausieren", "Uploads fortsetzen", "Dateien hochladen …", "Upload-Verlauf", "Einstellungen", "Beenden"}
}

// Actions are called when menu entries are clicked.
type Actions struct {
	Open, OpenWeb, OpenDrive, TogglePause, History, Settings, Quit func()
}

// Tray is the running tray icon.
type Tray struct {
	mu      sync.Mutex
	ready   bool
	status  *systray.MenuItem
	pause   *systray.MenuItem
	drive   *systray.MenuItem
	labels  Labels
	paused  bool
	state   State
	text    string
	driveOn bool
}

// Run shows the tray icon and blocks until Quit. onReady is called once the
// icon is visible. It must be called from the main goroutine.
func Run(labels Labels, a Actions, onReady func(*Tray)) {
	t := &Tray{labels: labels}
	systray.Run(func() {
		systray.SetIcon(icon(Normal))
		systray.SetTitle("Homelydrive")
		systray.SetTooltip("Homelydrive")
		systray.SetOnTapped(a.Open)
		t.status = systray.AddMenuItem("Homelydrive", "")
		t.status.Disable()
		systray.AddSeparator()
		open := systray.AddMenuItem(labels.Open, "")
		web := systray.AddMenuItem(labels.OpenWeb, "")
		t.drive = systray.AddMenuItem(labels.OpenDrive, "")
		t.drive.Hide()
		systray.AddSeparator()
		t.pause = systray.AddMenuItem(labels.Pause, "")
		hist := systray.AddMenuItem(labels.History, "")
		settings := systray.AddMenuItem(labels.Settings, "")
		systray.AddSeparator()
		quit := systray.AddMenuItem(labels.Quit, "")
		t.mu.Lock()
		t.ready = true
		t.mu.Unlock()
		t.apply()
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					a.Open()
				case <-web.ClickedCh:
					a.OpenWeb()
				case <-t.drive.ClickedCh:
					a.OpenDrive()
				case <-t.pause.ClickedCh:
					a.TogglePause()
				case <-hist.ClickedCh:
					a.History()
				case <-settings.ClickedCh:
					a.Settings()
				case <-quit.ClickedCh:
					a.Quit()
					return
				}
			}
		}()
		if onReady != nil {
			onReady(t)
		}
	}, func() {})
}

// Quit removes the icon and ends Run.
func Quit() { systray.Quit() }

// Update changes icon, status line and pause entry.
func (t *Tray) Update(state State, text string, paused, driveMounted bool) {
	t.mu.Lock()
	changed := t.state != state || t.text != text || t.paused != paused || t.driveOn != driveMounted
	t.state, t.text, t.paused, t.driveOn = state, text, paused, driveMounted
	t.mu.Unlock()
	if changed {
		t.apply()
	}
}

func (t *Tray) apply() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.ready {
		return
	}
	systray.SetIcon(icon(t.state))
	systray.SetTooltip("Homelydrive – " + t.text)
	t.status.SetTitle(t.text)
	if t.paused {
		t.pause.SetTitle(t.labels.Resume)
	} else {
		t.pause.SetTitle(t.labels.Pause)
	}
	if t.driveOn {
		t.drive.Show()
	} else {
		t.drive.Hide()
	}
}
