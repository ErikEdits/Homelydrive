//go:build !windows

package storage

import "golang.org/x/sys/unix"

// DiskUsage returns free and total bytes of the file system holding path.
func DiskUsage(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}
