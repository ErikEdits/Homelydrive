//go:build !windows

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func dataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

type target struct {
	path    string
	content string
	mode    os.FileMode
}

func targets(exe, lang string) []target {
	var out []target
	dh := dataHome()
	var actions []string
	for _, e := range Entries(lang) {
		// Nautilus, Nemo and Caja scripts (selected files are passed as arguments).
		script := fmt.Sprintf("#!/bin/sh\nexec %s %s \"$@\"\n", shellQuote(exe), e.Command)
		for _, dir := range []string{"nautilus/scripts", "nemo/scripts", "caja/scripts"} {
			out = append(out, target{filepath.Join(dh, dir, e.Label), script, 0o755})
		}
		actions = append(actions, fmt.Sprintf("[Desktop Action %s]\nName=%s\nIcon=homelydrive\nExec=%s %s %%F\n", e.ID, e.Label, shellQuote(exe), e.Command))
	}
	// Dolphin (KDE) service menu.
	ids := []string{}
	for _, e := range Entries(lang) {
		ids = append(ids, e.ID)
	}
	dolphin := "[Desktop Entry]\nType=Service\nX-KDE-ServiceTypes=KonqPopupMenu/Plugin\nMimeType=all/allfiles;inode/directory;\nX-KDE-Submenu=Homelydrive\nActions=" +
		strings.Join(ids, ";") + "\n\n" + strings.Join(actions, "\n")
	out = append(out,
		target{filepath.Join(dh, "kio", "servicemenus", "homelydrive.desktop"), dolphin, 0o755},
		target{filepath.Join(dh, "kservices5", "ServiceMenus", "homelydrive.desktop"), dolphin, 0o644},
	)
	return out
}

// Install writes the file manager entries for the current user.
func Install(exe, lang string) error {
	Uninstall()
	var firstErr error
	for _, t := range targets(exe, lang) {
		if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.WriteFile(t.path, []byte(t.content), t.mode); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Uninstall removes the entries (in both languages).
func Uninstall() error {
	for _, lang := range []string{"de", "en"} {
		for _, t := range targets("homelydrive", lang) {
			os.Remove(t.path)
		}
	}
	return nil
}
