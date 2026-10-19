//go:build !windows

package core

import "github.com/fsnotify/fsnotify"

// newWatchBackend uses inotify, which watches every folder on its own.
func newWatchBackend() (watchBackend, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return notifyBackend{fw}, nil
}

type notifyBackend struct{ *fsnotify.Watcher }

func (b notifyBackend) eventCh() <-chan fsnotify.Event { return b.Events }
func (b notifyBackend) errorCh() <-chan error          { return b.Errors }
func (notifyBackend) recursive() bool                  { return false }
