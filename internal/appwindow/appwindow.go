// Package appwindow opens a local web page as an app window (without browser
// toolbars) using Microsoft Edge or Chrome/Chromium in "--app" mode. Edge is
// part of every Windows 10/11 installation, so no extra runtime is needed.
// If no such browser is found, the default browser is used.
package appwindow

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Open shows url in an app window. profileDir keeps the window's cookies
// (login) separate from the user's normal browser. The returned command (if
// any) runs as long as the window is open.
func Open(url, profileDir string, width, height int) (*exec.Cmd, error) {
	if b := findBrowser(); b != "" {
		args := []string{
			"--app=" + url,
			"--new-window",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-features=Translate",
			"--window-size=" + itoa(width) + "," + itoa(height),
		}
		if profileDir != "" {
			os.MkdirAll(profileDir, 0o700)
			args = append(args, "--user-data-dir="+profileDir)
		}
		cmd := exec.Command(b, args...)
		if err := cmd.Start(); err == nil {
			return cmd, nil
		}
	}
	return nil, OpenDefault(url)
}

// OpenDefault opens url (or a file/folder) with the system default handler.
func OpenDefault(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func findBrowser() string {
	if runtime.GOOS == "windows" {
		var cands []string
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			base := os.Getenv(env)
			if base == "" {
				continue
			}
			cands = append(cands,
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "Chromium", "Application", "chrome.exe"),
				filepath.Join(base, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			)
		}
		for _, c := range cands {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
		return ""
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable",
		"brave-browser", "microsoft-edge", "microsoft-edge-stable", "vivaldi"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	// Flatpak Chromium.
	if p, err := exec.LookPath("flatpak"); err == nil {
		if exec.Command(p, "info", "org.chromium.Chromium").Run() == nil {
			return "" // flatpak needs "flatpak run"; fall back to default browser
		}
	}
	return ""
}

func itoa(n int) string {
	if n <= 0 {
		return "800"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
