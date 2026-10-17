// Command homelydrive is the Homelydrive PC app for Windows and Linux: it
// uploads chosen folders, offers a virtual drive, a tray icon and context
// menu entries in the file manager.
//
//	homelydrive                 start the app (or open its window)
//	homelydrive upload FILE...  upload files (used by the context menu)
//	homelydrive link FILE       copy a share link (used by the context menu)
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/erikedits/homelydrive/internal/client/app"
	"github.com/erikedits/homelydrive/internal/client/config"
	"github.com/erikedits/homelydrive/internal/client/integration"
	"github.com/erikedits/homelydrive/internal/client/notify"
	"github.com/erikedits/homelydrive/internal/client/tray"
	"github.com/erikedits/homelydrive/internal/server/logbuf"
	"github.com/erikedits/homelydrive/internal/version"
	"github.com/erikedits/homelydrive/web"
)

const usage = `Homelydrive PC-App %s

  homelydrive                    App starten bzw. Fenster öffnen
  homelydrive upload DATEI...    Dateien/Ordner hochladen
  homelydrive link DATEI         Freigabe-Link in die Zwischenablage kopieren
  homelydrive integration install|uninstall   Rechtsklick-Menü ein-/austragen
  homelydrive quit               App beenden
  homelydrive version
`

func main() {
	attachConsole()
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	var err error
	switch cmd {
	case "", "show", "start", "--background":
		err = runApp(cmd != "--background")
	case "upload":
		err = upload(args[1:])
	case "link":
		err = link(args[1:])
	case "quit":
		err = app.Call("POST", "/local/quit", nil, nil)
	case "integration":
		err = integrationCmd(args[1:])
	case "version", "--version":
		fmt.Println(version.Version)
	case "help", "-h", "--help":
		fmt.Printf(usage, version.Version)
	default:
		// Files dropped onto the program icon are uploaded.
		if _, statErr := os.Stat(cmd); statErr == nil {
			err = upload(args)
		} else {
			fmt.Printf(usage, version.Version)
			os.Exit(2)
		}
	}
	if err != nil {
		showError(err.Error())
		os.Exit(1)
	}
}

func runApp(showWindow bool) error {
	if app.Running() {
		return app.Call("POST", "/local/show", nil, nil)
	}
	dir := config.Dir()
	os.MkdirAll(dir, 0o700)
	logs := logbuf.New(500, filepath.Join(dir, "logs"), os.Stderr)
	log := slog.New(slog.NewTextHandler(logs, nil))
	a, err := app.New(log, web.FS())
	if err != nil {
		return err
	}
	if err := a.Start(); err != nil {
		if errors.Is(err, app.ErrAlreadyRunning) {
			return app.Call("POST", "/local/show", nil, nil)
		}
		return err
	}
	log.Info("Homelydrive PC app started", "version", version.Version)
	if showWindow || a.Cfg.Get().Token == "" {
		a.ShowWindow("")
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		a.Stop()
		os.Exit(0)
	}()
	app.QuitFunc = func() {
		tray.Quit()
		go func() {
			time.Sleep(2 * time.Second)
			os.Exit(0)
		}()
	}
	lang := a.Cfg.Get().Language
	tray.Run(tray.LabelsFor(lang), tray.Actions{
		Open:        func() { a.ShowWindow("") },
		OpenWeb:     a.OpenWeb,
		OpenDrive:   a.OpenDrive,
		TogglePause: a.TogglePause,
		History:     func() { a.ShowWindow("history") },
		Settings:    func() { a.ShowWindow("settings") },
		Quit:        a.Quit,
	}, a.SetTray)
	a.Stop()
	return nil
}

// ensureRunning starts the app in the background if needed.
func ensureRunning() error {
	if app.Running() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--background")
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < 60; i++ {
		time.Sleep(250 * time.Millisecond)
		if app.Running() {
			return nil
		}
	}
	return errors.New("Homelydrive konnte nicht gestartet werden")
}

func absPaths(args []string) []string {
	var out []string
	for _, p := range args {
		if abs, err := filepath.Abs(p); err == nil {
			out = append(out, abs)
		}
	}
	return out
}

func upload(args []string) error {
	if len(args) == 0 {
		return errors.New("keine Dateien angegeben")
	}
	if err := ensureRunning(); err != nil {
		return err
	}
	return app.Call("POST", "/local/upload", map[string]any{"paths": absPaths(args)}, nil)
}

func link(args []string) error {
	if len(args) == 0 {
		return errors.New("keine Datei angegeben")
	}
	if err := ensureRunning(); err != nil {
		return err
	}
	var out struct {
		URL    string `json:"url"`
		Copied bool   `json:"copied"`
	}
	for _, p := range absPaths(args) {
		if err := app.Call("POST", "/local/link", map[string]string{"path": p}, &out); err != nil {
			return err
		}
		fmt.Println(out.URL)
	}
	return nil
}

func integrationCmd(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "uninstall" {
		cfg.Update(func(c *config.Config) { c.Integration = true }) // do not reinstall automatically
		return integration.Uninstall()
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := integration.Install(exe, cfg.Get().Language); err != nil {
		return err
	}
	cfg.Update(func(c *config.Config) { c.Integration = true })
	notify.Show("Homelydrive", "Rechtsklick-Menü eingerichtet.")
	return nil
}
