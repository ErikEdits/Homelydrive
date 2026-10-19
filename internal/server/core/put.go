package core

import (
	"os"
	"path/filepath"

	"github.com/erikedits/homelydrive/internal/server/fsname"
	"github.com/erikedits/homelydrive/internal/server/storage"
)

// TempFor returns a temporary file location on the disk that will hold rel,
// so the finished file can be moved into place without copying (WebDAV).
func (c *Core) TempFor(a *Access, rel string) (path string, rootIdx int, err error) {
	if err := c.requireWrite(a); err != nil {
		return "", 0, err
	}
	rel, err = fsname.SanitizePath(rel)
	if err != nil || rel == "" {
		return "", 0, badRequest("Ungültiger Pfad.")
	}
	full := a.Full(rel)
	parent, _ := fsname.Split(full)
	idx := -1
	if _, i, err := a.Space.Union.Stat(full); err == nil {
		idx = i
	} else if idx, err = a.Space.Union.PickRoot(parent, 0); err != nil {
		return "", 0, mapFSErr(err)
	}
	dir := a.Space.Union.Roots[idx].TempDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	return filepath.Join(dir, randomToken(12)+".dav"), idx, nil
}

// PutTemp moves a completely written temporary file to rel, replacing an
// existing file (WebDAV PUT semantics), and indexes it.
func (c *Core) PutTemp(a *Access, rel, tmp string, rootIdx int) error {
	if err := c.requireWrite(a); err != nil {
		os.Remove(tmp)
		return err
	}
	rel, err := fsname.SanitizePath(rel)
	if err != nil || rel == "" {
		os.Remove(tmp)
		return badRequest("Ungültiger Pfad.")
	}
	full := a.Full(rel)
	parent, name := fsname.Split(full)
	fi, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	var old int64
	oldIdx := -1
	if ofi, i, err := a.Space.Union.Stat(full); err == nil {
		if ofi.IsDir() {
			os.Remove(tmp)
			return ErrExists
		}
		old, oldIdx = ofi.Size(), i
	}
	if err := c.checkQuota(a.Space, fi.Size()-old); err != nil {
		os.Remove(tmp)
		return err
	}
	dst := a.Space.Union.Abs(rootIdx, full)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := storage.MoveFile(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	if oldIdx >= 0 && oldIdx != rootIdx {
		os.Remove(a.Space.Union.Abs(oldIdx, full))
	}
	fi, _ = os.Stat(dst)
	c.ensureDirRows(a.Space, parent)
	c.insertRow(c.DB, a.Space, full, storage.Entry{Name: name, Size: fi.Size(), MTime: fi.ModTime().UnixMilli()}, "")
	c.kickIndexer()
	c.logActivity(a.User, "", a.Space.ID, "upload", full, "webdav")
	c.publishChange(a.Space.ID, parent)
	c.afterWrite(a.Space)
	return nil
}
