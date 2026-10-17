//go:build !windows && !cgo

// Package drive: this build has no FUSE support (built without cgo).
package drive

import (
	"errors"
	"log/slog"

	"github.com/erikedits/homelydrive/internal/client/remote"
)

// Supported reports whether this build can mount a drive.
const Supported = false

type Names struct{ Home, Teams, Mounts, Shared string }

type Uploader func(local, space, remotePath string, done func(error))

type Drive struct {
	IsJunk func(string) bool
}

func New(client func() *remote.Client, upload Uploader, lang, cacheDir string, cacheMB int, log *slog.Logger) *Drive {
	return &Drive{}
}

func (d *Drive) Mount(string) error {
	return errors.New("diese Version wurde ohne Laufwerks-Unterstützung (FUSE) gebaut")
}
func (d *Drive) Unmount()                {}
func (d *Drive) MountPoint() string      { return "" }
func (d *Drive) CacheSize() int64        { return 0 }
func (d *Drive) ClearCache()             {}
func (d *Drive) Invalidate(string)       {}
func (d *Drive) InvalidateSpaceID(int64) {}

func (d *Drive) Locate(string) (string, string, bool) { return "", "", false }
