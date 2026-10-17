//go:build windows

package drive

// WinFsp maps all files to the mounting user (-o uid=-1,gid=-1).
func currentIDs() (uint32, uint32) { return 0, 0 }
