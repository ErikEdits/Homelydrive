// Package storage implements a simple union file system over several root
// folders (one per hard disk). Files of one space may be spread over all
// roots; listings merge them, new files go to the root with the most free
// space. With a single root this is just a plain folder.
package storage

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/erikedits/homelydrive/internal/server/fsname"
)

// Root is one physical folder that is part of a space.
type Root struct {
	Dir      string // the space's folder on this disk
	TrashDir string // where deleted files of this root are moved to
	TempDir  string // where uploads are assembled before being moved in
}

// Union is the merged view over the roots of one space.
type Union struct {
	Roots []Root
}

// Entry is one item of a merged directory listing.
type Entry struct {
	Name  string
	IsDir bool
	Size  int64
	MTime int64 // unix milliseconds
}

var ErrNoSpace = errors.New("not enough free disk space")

func (u *Union) abs(i int, rel string) string {
	if rel == "" {
		return u.Roots[i].Dir
	}
	return filepath.Join(u.Roots[i].Dir, filepath.FromSlash(rel))
}

// Abs returns the absolute path of rel on root i.
func (u *Union) Abs(i int, rel string) string { return u.abs(i, rel) }

// Stat returns info for rel from the first root that contains it.
func (u *Union) Stat(rel string) (fs.FileInfo, int, error) {
	var firstErr error
	for i := range u.Roots {
		fi, err := os.Stat(u.abs(i, rel))
		if err == nil {
			if rel == "" || !isInternal(fi.Name()) {
				return fi, i, nil
			}
		}
		if firstErr == nil && err != nil && !errors.Is(err, fs.ErrNotExist) {
			firstErr = err
		}
	}
	if rel == "" {
		// The space root may not exist on any disk yet.
		return rootInfo{}, 0, nil
	}
	if firstErr != nil {
		return nil, -1, firstErr
	}
	return nil, -1, fs.ErrNotExist
}

// Exists reports whether rel exists on any root.
func (u *Union) Exists(rel string) bool {
	_, _, err := u.Stat(rel)
	return err == nil
}

// Open opens rel for reading.
func (u *Union) Open(rel string) (*os.File, error) {
	_, i, err := u.Stat(rel)
	if err != nil {
		return nil, err
	}
	return os.Open(u.abs(i, rel))
}

// ReadDir merges the directory listings of all roots.
func (u *Union) ReadDir(rel string) ([]Entry, error) {
	seen := map[string]int{}
	var out []Entry
	found := rel == ""
	for i := range u.Roots {
		des, err := os.ReadDir(u.abs(i, rel))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		found = true
		for _, de := range des {
			name := de.Name()
			if isInternal(name) {
				continue
			}
			fi, err := de.Info()
			if err != nil {
				continue
			}
			isDir := fi.IsDir()
			if fi.Mode()&fs.ModeSymlink != 0 {
				// Follow symlinks so that linked folders work inside mounts.
				if st, err := os.Stat(filepath.Join(u.abs(i, rel), name)); err == nil {
					fi, isDir = st, st.IsDir()
				} else {
					continue
				}
			}
			if !isDir && !fi.Mode().IsRegular() {
				continue
			}
			e := Entry{Name: name, IsDir: isDir, Size: fi.Size(), MTime: fi.ModTime().UnixMilli()}
			if isDir {
				e.Size = 0
			}
			if j, ok := seen[name]; ok {
				// Same folder on several disks: merge; a file wins over nothing.
				if !out[j].IsDir && e.MTime > out[j].MTime && !e.IsDir {
					out[j] = e
				}
				continue
			}
			seen[name] = len(out)
			out = append(out, e)
		}
	}
	if !found {
		return nil, fs.ErrNotExist
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// isInternal hides the server's own helper folders in mounted folders.
func isInternal(name string) bool {
	return strings.HasPrefix(name, fsname.InternalPrefix)
}

// PickRoot chooses the root for a new file of the given size below parent.
// Roots that already contain parent are preferred so that folders do not get
// scattered needlessly; among them the one with the most free space wins.
func (u *Union) PickRoot(parent string, size int64) (int, error) {
	if len(u.Roots) == 1 {
		free, _, err := DiskUsage(existingAncestor(u.Roots[0].Dir))
		if err == nil && free < uint64(size) {
			return -1, ErrNoSpace
		}
		return 0, nil
	}
	best, bestFree := -1, uint64(0)
	bestAny, bestAnyFree := -1, uint64(0)
	for i := range u.Roots {
		free, _, err := DiskUsage(existingAncestor(u.Roots[i].Dir))
		if err != nil || free < uint64(size)+64<<20 { // keep 64 MB headroom
			continue
		}
		if free > bestAnyFree {
			bestAny, bestAnyFree = i, free
		}
		if _, err := os.Stat(u.abs(i, parent)); err == nil && free > bestFree {
			best, bestFree = i, free
		}
	}
	if best >= 0 {
		return best, nil
	}
	if bestAny >= 0 {
		return bestAny, nil
	}
	return -1, ErrNoSpace
}

func existingAncestor(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// Mkdir creates a directory (and its parents) on the best root.
func (u *Union) Mkdir(rel string) error {
	if _, _, err := u.Stat(rel); err == nil {
		return fs.ErrExist
	}
	parent, _ := fsname.Split(rel)
	i, err := u.PickRoot(parent, 0)
	if err != nil {
		return err
	}
	return os.MkdirAll(u.abs(i, rel), 0o755)
}

// EnsureDir makes sure rel exists as a directory on root i.
func (u *Union) EnsureDir(i int, rel string) error {
	return os.MkdirAll(u.abs(i, rel), 0o755)
}

// Rename moves oldRel to newRel on every root that holds a part of it.
// The destination must not exist.
func (u *Union) Rename(oldRel, newRel string) error {
	if u.Exists(newRel) && !strings.EqualFold(oldRel, newRel) {
		return fs.ErrExist
	}
	moved := false
	for i := range u.Roots {
		src := u.abs(i, oldRel)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := u.abs(i, newRel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := retry(func() error { return os.Rename(src, dst) }); err != nil {
			return err
		}
		moved = true
	}
	if !moved {
		return fs.ErrNotExist
	}
	return nil
}

// retry runs op again for up to two seconds while Windows reports a file as
// busy, so that a virus scanner looking at a new file does not make moving
// or deleting it fail.
func retry(op func() error) error {
	err := op()
	for i := 0; i < 10 && err != nil && busy(err); i++ {
		time.Sleep(200 * time.Millisecond)
		err = op()
	}
	return err
}

// MoveFile renames a physical file like os.Rename. A virus scanner often
// looks at a freshly written file right away, so it waits for that on Windows.
func MoveFile(src, dst string) error {
	return retry(func() error { return os.Rename(src, dst) })
}

// MoveToTrash moves rel into the trash folder of every root holding a part
// of it, under the name trashID.
func (u *Union) MoveToTrash(rel, trashID string) error {
	moved := false
	for i, r := range u.Roots {
		src := u.abs(i, rel)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		if err := os.MkdirAll(r.TrashDir, 0o755); err != nil {
			return err
		}
		if err := retry(func() error { return os.Rename(src, filepath.Join(r.TrashDir, trashID)) }); err != nil {
			return err
		}
		moved = true
	}
	if !moved {
		return fs.ErrNotExist
	}
	return nil
}

// RestoreFromTrash moves a trashed item back to rel.
func (u *Union) RestoreFromTrash(trashID, rel string) error {
	restored := false
	for i, r := range u.Roots {
		src := filepath.Join(r.TrashDir, trashID)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := u.abs(i, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if _, err := os.Lstat(dst); err == nil {
			// A folder with this name exists again: merge contents.
			if err := mergeInto(src, dst); err != nil {
				return err
			}
		} else if err := retry(func() error { return os.Rename(src, dst) }); err != nil {
			return err
		}
		restored = true
	}
	if !restored {
		return fs.ErrNotExist
	}
	return nil
}

func mergeInto(src, dst string) error {
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	dfi, err := os.Stat(dst)
	if err != nil {
		return err
	}
	if !sfi.IsDir() || !dfi.IsDir() {
		// Keep both: restore beside the existing file.
		parent, name := filepath.Split(dst)
		for n := 1; ; n++ {
			cand := filepath.Join(parent, fsname.Numbered(name, n))
			if _, err := os.Lstat(cand); err != nil {
				return os.Rename(src, cand)
			}
		}
	}
	des, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, de := range des {
		s := filepath.Join(src, de.Name())
		d := filepath.Join(dst, de.Name())
		if _, err := os.Lstat(d); err == nil {
			if err := mergeInto(s, d); err != nil {
				return err
			}
			continue
		}
		if err := os.Rename(s, d); err != nil {
			return err
		}
	}
	return os.Remove(src)
}

// PurgeTrash deletes a trashed item permanently from every root.
func (u *Union) PurgeTrash(trashID string) error {
	for _, r := range u.Roots {
		p := filepath.Join(r.TrashDir, trashID)
		if _, err := os.Lstat(p); err == nil {
			if err := retry(func() error { return os.RemoveAll(p) }); err != nil {
				return err
			}
		}
	}
	return nil
}

// TrashPaths returns the physical paths of a trashed item (for size checks).
func (u *Union) TrashPaths(trashID string) []string {
	var out []string
	for _, r := range u.Roots {
		p := filepath.Join(r.TrashDir, trashID)
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// RemoveAll deletes rel permanently from every root.
func (u *Union) RemoveAll(rel string) error {
	if rel == "" {
		return errors.New("refusing to delete space root")
	}
	for i := range u.Roots {
		p := u.abs(i, rel)
		if _, err := os.Lstat(p); err == nil {
			if err := retry(func() error { return os.RemoveAll(p) }); err != nil {
				return err
			}
		}
	}
	return nil
}

// Walk calls fn for every entry below rel (merged over all roots), parents
// before children. fn receives the relative path.
func (u *Union) Walk(rel string, fn func(rel string, e Entry) error) error {
	entries, err := u.ReadDir(rel)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := fsname.Join(rel, e.Name)
		if err := fn(p, e); err != nil {
			if errors.Is(err, fs.SkipDir) {
				continue
			}
			return err
		}
		if e.IsDir {
			if err := u.Walk(p, fn); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// CopyFile copies a single file from src (any union) into this union at rel
// on root i, preserving the modification time.
func CopyFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	tmp := dstPath + ".hdcopy"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	os.Chtimes(tmp, fi.ModTime(), fi.ModTime())
	return MoveFile(tmp, dstPath)
}

// DirSize sums up file sizes below a physical path.
func DirSize(p string) int64 {
	var total int64
	filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

type rootInfo struct{}

func (rootInfo) Name() string       { return "" }
func (rootInfo) Size() int64        { return 0 }
func (rootInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o755 }
func (rootInfo) ModTime() time.Time { return time.Time{} }
func (rootInfo) IsDir() bool        { return true }
func (rootInfo) Sys() any           { return nil }
