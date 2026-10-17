// Package integration adds "Auf Homelydrive hochladen" and "Homelydrive-Link
// kopieren" to the right-click menu of Windows Explorer and of the Linux
// file managers Nautilus (GNOME), Nemo (Cinnamon), Caja (MATE) and Dolphin (KDE).
package integration

import "os"

// Entry describes one context menu entry.
type Entry struct {
	ID      string // used for file names / registry keys
	Label   string
	Command string // argument for the app, e.g. "upload"
	Dirs    bool   // also offered for folders
}

// Entries returns the menu entries in the given language.
func Entries(lang string) []Entry {
	if lang == "en" {
		return []Entry{
			{ID: "upload", Label: "Upload to Homelydrive", Command: "upload", Dirs: true},
			{ID: "link", Label: "Copy Homelydrive link", Command: "link"},
		}
	}
	return []Entry{
		{ID: "upload", Label: "Auf Homelydrive hochladen", Command: "upload", Dirs: true},
		{ID: "link", Label: "Homelydrive-Link kopieren", Command: "link"},
	}
}

// SelfPath is the program path that context menu entries and restarts use.
// Inside an AppImage this is the AppImage file itself: the mounted path of
// the running program changes on every start.
func SelfPath() (string, error) {
	if p := os.Getenv("APPIMAGE"); p != "" {
		return p, nil
	}
	return os.Executable()
}
