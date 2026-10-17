//go:build !windows && cgo

package drive

import "os"

func currentIDs() (uint32, uint32) { return uint32(os.Getuid()), uint32(os.Getgid()) }
