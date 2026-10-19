//go:build !windows

package storage

// busy reports errors that go away by themselves after a moment. Other
// programs never lock files on Linux.
func busy(error) bool { return false }
