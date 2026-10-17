// Package notify shows desktop notifications (Windows toast, Linux
// notification daemon).
package notify

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/gen2brain/beeep"
)

var (
	mu       sync.Mutex
	enabled  = true
	iconPath string
)

// Setup sets the app name and writes the icon used by notifications.
func Setup(dir string, icon []byte) {
	beeep.AppName = "Homelydrive"
	p := filepath.Join(dir, "icon.png")
	if os.WriteFile(p, icon, 0o644) == nil {
		iconPath = p
	}
}

// SetEnabled turns notifications on or off.
func SetEnabled(on bool) {
	mu.Lock()
	enabled = on
	mu.Unlock()
}

// Show displays a notification (if enabled). It never blocks the caller.
func Show(title, body string) {
	mu.Lock()
	on := enabled
	mu.Unlock()
	if !on {
		return
	}
	go beeep.Notify(title, body, iconPath)
}
