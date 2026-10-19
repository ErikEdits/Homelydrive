//go:build windows

package storage

import (
	"errors"

	"golang.org/x/sys/windows"
)

// busy reports errors Windows gives while another program has a file open
// for a moment, like a virus scanner, the search indexer or a thumbnailer.
func busy(err error) bool {
	var errno windows.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION, windows.ERROR_DIR_NOT_EMPTY:
		return true
	}
	return false
}
