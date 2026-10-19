package core

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/erikedits/homelydrive/internal/server/fsname"
	"github.com/erikedits/homelydrive/internal/server/storage"
)

// FileInfo is a file or folder as returned by the API.
type FileInfo struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Space     string   `json:"space,omitempty"`
	SpaceName string   `json:"space_name,omitempty"`
	IsDir     bool     `json:"is_dir"`
	Size      int64    `json:"size"`
	MTime     int64    `json:"mtime"`
	Mime      string   `json:"mime,omitempty"`
	Category  string   `json:"category,omitempty"`
	Viewer    string   `json:"viewer,omitempty"`
	SHA256    string   `json:"sha256,omitempty"`
	Starred   bool     `json:"starred,omitempty"`
	Color     string   `json:"color,omitempty"`
	Note      string   `json:"note,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Shared    bool     `json:"shared,omitempty"`
	CanWrite  bool     `json:"can_write"`
	TakenAt   int64    `json:"taken_at,omitempty"` // photo/video date used by the gallery
}

// relPath cleans a client path; the result is relative to the access root.
func relPath(p string) (string, error) {
	rel, err := fsname.Clean(p)
	if err != nil {
		return "", badRequest("Ungültiger Pfad.")
	}
	return rel, nil
}

// toInfo converts index rows into API objects, adding per-user metadata.
func (c *Core) toInfo(u *User, rows []fileRow, mapPath func(fileRow) (ref, rel string), canWrite func(fileRow) bool) []FileInfo {
	out := make([]FileInfo, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	idx := map[int64]int{}
	for _, r := range rows {
		fi := FileInfo{
			ID: r.ID, Name: r.Name, IsDir: r.IsDir, Size: r.Size, MTime: r.MTime,
			Mime: r.Mime, Category: r.Category, SHA256: r.SHA256,
		}
		fi.Space, fi.Path = mapPath(r)
		fi.CanWrite = canWrite(r)
		if r.Category == "image" || r.Category == "video" {
			fi.TakenAt = r.SortTime
		}
		if !r.IsDir {
			if t, ok := c.TypeOf(r.Name); ok {
				fi.Viewer = t.Viewer
			} else {
				fi.Viewer = categoryViewer[r.Category]
			}
		} else {
			c.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files WHERE space_id = ? AND is_dir = 0 AND path >= ? AND path < ?`,
				r.SpaceID, r.Path+"/", r.Path+"0").Scan(&fi.Size)
		}
		idx[r.ID] = len(out)
		ids = append(ids, r.ID)
		out = append(out, fi)
	}
	var uid int64 // anonymous link visitors have no user
	if u != nil {
		uid = u.ID
	}
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		chunk := ids[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		args = append(args, uid)
		for _, id := range chunk {
			args = append(args, id)
		}
		if rows, err := c.DB.Query(`SELECT file_id FROM stars WHERE user_id = ? AND file_id IN (`+ph+`)`, args...); err == nil {
			for rows.Next() {
				var id int64
				rows.Scan(&id)
				out[idx[id]].Starred = true
			}
			rows.Close()
		}
		if rows, err := c.DB.Query(`SELECT t.name, ft.file_id FROM file_tags ft JOIN tags t ON t.id = ft.tag_id WHERE t.user_id = ? AND ft.file_id IN (`+ph+`) ORDER BY t.name`, args...); err == nil {
			for rows.Next() {
				var name string
				var id int64
				rows.Scan(&name, &id)
				out[idx[id]].Tags = append(out[idx[id]].Tags, name)
			}
			rows.Close()
		}
		if rows, err := c.DB.Query(`SELECT file_id, color, note FROM file_meta WHERE file_id IN (`+ph+`)`, args[1:]...); err == nil {
			for rows.Next() {
				var id int64
				var color, note string
				rows.Scan(&id, &color, &note)
				out[idx[id]].Color, out[idx[id]].Note = color, note
			}
			rows.Close()
		}
	}
	// Mark items that are shared with others or have a link.
	for i := range out {
		r := rows[i]
		var n int
		c.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM shares WHERE space_id = ? AND path = ?) + (SELECT COUNT(*) FROM links WHERE space_id = ? AND path = ?)`,
			r.SpaceID, r.Path, r.SpaceID, r.Path).Scan(&n)
		out[i].Shared = n > 0
	}
	return out
}

func (c *Core) accessInfo(a *Access, rows []fileRow) []FileInfo {
	return c.toInfo(a.User, rows,
		func(r fileRow) (string, string) { return "", a.Rel(r.Path) },
		func(fileRow) bool { return a.CanWrite })
}

// List returns the contents of a folder.
func (c *Core) List(a *Access, rel string) ([]FileInfo, error) {
	rel, err := relPath(rel)
	if err != nil {
		return nil, err
	}
	full := a.Full(rel)
	if full != "" {
		r, err := c.statRow(a.Space, full)
		if err != nil {
			return nil, err
		}
		if !r.IsDir {
			return nil, badRequest("Kein Ordner.")
		}
	}
	m, err := c.syncDir(a.Space, full)
	if err != nil {
		if errors.Is(err, ErrNotFound) && full == "" {
			return []FileInfo{}, nil
		}
		return nil, err
	}
	rows := make([]fileRow, 0, len(m))
	for _, r := range m {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].IsDir != rows[j].IsDir {
			return rows[i].IsDir
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return c.accessInfo(a, rows), nil
}

// statRow returns the index row for a path, indexing it on the fly if the
// file exists on disk but is not indexed yet.
func (c *Core) statRow(s *Space, full string) (fileRow, error) {
	if full == "" {
		return fileRow{SpaceID: s.ID, IsDir: true}, nil
	}
	fi, _, err := s.Union.Stat(full)
	if err != nil {
		deleteSubtree(c.DB, s.ID, full)
		return fileRow{}, ErrNotFound
	}
	r, err := c.getRow(s.ID, full)
	if err == nil && r.IsDir == fi.IsDir() && (r.IsDir || (r.Size == fi.Size() && r.MTime == fi.ModTime().UnixMilli())) {
		return r, nil
	}
	parent, _ := fsname.Split(full)
	if _, err := c.syncDir(s, parent); err != nil {
		return fileRow{}, err
	}
	return c.getRow(s.ID, full)
}

// Stat returns information about one file or folder.
func (c *Core) Stat(a *Access, rel string) (FileInfo, error) {
	rel, err := relPath(rel)
	if err != nil {
		return FileInfo{}, err
	}
	full := a.Full(rel)
	if full == "" {
		return FileInfo{Name: a.Label, IsDir: true, CanWrite: a.CanWrite}, nil
	}
	r, err := c.statRow(a.Space, full)
	if err != nil {
		return FileInfo{}, err
	}
	return c.accessInfo(a, []fileRow{r})[0], nil
}

// OpenedFile is a file opened for download/preview.
type OpenedFile struct {
	File *os.File
	Info fs.FileInfo
	Type FileType
	Row  fileRow
	Name string
}

// Open opens a file for reading. If view is set it counts as "recently used".
func (c *Core) Open(a *Access, rel string, view bool) (*OpenedFile, error) {
	rel, err := relPath(rel)
	if err != nil {
		return nil, err
	}
	full := a.Full(rel)
	r, err := c.statRow(a.Space, full)
	if err != nil {
		return nil, err
	}
	if r.IsDir {
		return nil, badRequest("Ordner können nur als ZIP heruntergeladen werden.")
	}
	f, err := a.Space.Union.Open(full)
	if err != nil {
		return nil, ErrNotFound
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	t, ok := c.TypeOf(r.Name)
	if !ok {
		t = FileType{Mime: r.Mime, Category: r.Category, Viewer: categoryViewer[r.Category]}
	}
	if view && a.User != nil {
		c.touchRecent(a.User.ID, r.ID)
	}
	return &OpenedFile{File: f, Info: fi, Type: t, Row: r, Name: r.Name}, nil
}

func (c *Core) touchRecent(userID, fileID int64) {
	c.DB.Exec(`INSERT INTO recent(user_id, file_id, accessed_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id, file_id) DO UPDATE SET accessed_at = excluded.accessed_at`, userID, fileID, now())
}

func (c *Core) requireWrite(a *Access) error {
	if !a.CanWrite {
		return ErrForbidden
	}
	return nil
}

// Mkdir creates a folder (and missing parents).
func (c *Core) Mkdir(a *Access, rel string) (FileInfo, error) {
	if err := c.requireWrite(a); err != nil {
		return FileInfo{}, err
	}
	rel, err := fsname.SanitizePath(rel)
	if err != nil || rel == "" {
		return FileInfo{}, badRequest("Ungültiger Ordnername.")
	}
	full := a.Full(rel)
	if a.Space.Union.Exists(full) {
		return FileInfo{}, ErrExists
	}
	if err := a.Space.Union.Mkdir(full); err != nil {
		return FileInfo{}, err
	}
	c.indexPathChain(a.Space, full)
	c.logActivity(a.User, "", a.Space.ID, "mkdir", full, "")
	parent, _ := fsname.Split(full)
	c.publishChange(a.Space.ID, parent)
	return c.Stat(a, rel)
}

// indexPathChain makes sure every folder on the way to full is indexed.
func (c *Core) indexPathChain(s *Space, full string) {
	parts := strings.Split(full, "/")
	for i := range parts {
		c.syncDir(s, strings.Join(parts[:i], "/"))
	}
}

// uniqueName returns name, or "name (n).ext" if it already exists in parent.
func uniqueName(u *storage.Union, parent, name string) string {
	if !u.Exists(fsname.Join(parent, name)) {
		return name
	}
	for n := 1; ; n++ {
		cand := fsname.Numbered(name, n)
		if !u.Exists(fsname.Join(parent, cand)) {
			return cand
		}
	}
}

// Rename gives a file or folder a new name. Returns the new relative path.
func (c *Core) Rename(a *Access, rel, newName string) (string, error) {
	if err := c.requireWrite(a); err != nil {
		return "", err
	}
	rel, err := relPath(rel)
	if err != nil || rel == "" {
		return "", badRequest("Ungültiger Pfad.")
	}
	newName = strings.TrimSpace(newName)
	if !fsname.ValidName(newName) {
		return "", badRequest("Ungültiger Name.")
	}
	full := a.Full(rel)
	if full == a.Base && a.ShareID != 0 {
		return "", ErrForbidden // the shared item itself belongs to the owner
	}
	parent, oldName := fsname.Split(full)
	if oldName == newName {
		return rel, nil
	}
	newFull := fsname.Join(parent, newName)
	if !strings.EqualFold(oldName, newName) && a.Space.Union.Exists(newFull) {
		return "", ErrExists
	}
	c.unwatchTree(a.Space, full)
	if err := a.Space.Union.Rename(full, newFull); err != nil {
		c.watchTree(a.Space, full)
		return "", mapFSErr(err)
	}
	c.movePaths(a.Space.ID, full, newFull)
	c.watchTree(a.Space, newFull)
	c.logActivity(a.User, "", a.Space.ID, "rename", newFull, oldName)
	c.publishChange(a.Space.ID, parent)
	return a.Rel(newFull), nil
}

func mapFSErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case errors.Is(err, fs.ErrPermission):
		return ErrForbidden
	case errors.Is(err, storage.ErrNoSpace):
		return badRequest("Nicht genug Speicherplatz auf der Festplatte des Servers.")
	}
	return err
}

// movePaths updates the index, shares and links after a rename/move inside
// one space so that stars, tags, notes and shares are kept.
func (c *Core) movePaths(spaceID int64, oldP, newP string) {
	parent, name := fsname.Split(newP)
	l := utf8.RuneCountInString(oldP)
	tx, err := c.DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM files WHERE space_id = ? AND (path = ? OR (path >= ? AND path < ?)) AND ? <> ?`,
		spaceID, newP, newP+"/", newP+"0", strings.ToLower(oldP), strings.ToLower(newP))
	tx.Exec(`UPDATE files SET path = ?, parent = ?, name = ? WHERE space_id = ? AND path = ?`, newP, parent, name, spaceID, oldP)
	tx.Exec(`UPDATE files SET path = ? || substr(path, ?), parent = CASE WHEN parent = ? THEN ? ELSE ? || substr(parent, ?) END
		WHERE space_id = ? AND path >= ? AND path < ?`, newP, l+1, oldP, newP, newP, l+1, spaceID, oldP+"/", oldP+"0")
	for _, table := range []string{"shares", "links"} {
		tx.Exec(`UPDATE `+table+` SET path = ? WHERE space_id = ? AND path = ?`, newP, spaceID, oldP)
		tx.Exec(`UPDATE `+table+` SET path = ? || substr(path, ?) WHERE space_id = ? AND path >= ? AND path < ?`,
			newP, l+1, spaceID, oldP+"/", oldP+"0")
	}
	tx.Commit()
}

// Transfer moves (move=true) or copies items into the folder dstRel of dst.
// It returns the new relative paths.
func (c *Core) Transfer(src *Access, rels []string, dst *Access, dstRel string, move bool) ([]string, error) {
	if err := c.requireWrite(dst); err != nil {
		return nil, err
	}
	if move {
		if err := c.requireWrite(src); err != nil {
			return nil, err
		}
	}
	dstRel, err := relPath(dstRel)
	if err != nil {
		return nil, err
	}
	dstFolder := dst.Full(dstRel)
	if dstFolder != "" {
		r, err := c.statRow(dst.Space, dstFolder)
		if err != nil {
			return nil, err
		}
		if !r.IsDir {
			return nil, badRequest("Das Ziel ist kein Ordner.")
		}
	}
	var out []string
	for _, p := range rels {
		rel, err := relPath(p)
		if err != nil || rel == "" {
			return out, badRequest("Ungültiger Pfad.")
		}
		full := src.Full(rel)
		if move && full == src.Base && src.ShareID != 0 {
			return out, ErrForbidden
		}
		row, err := c.statRow(src.Space, full)
		if err != nil {
			return out, err
		}
		sameSpace := src.Space.ID == dst.Space.ID
		if sameSpace && row.IsDir && fsname.IsWithin(dstFolder, full) {
			return out, badRequest("Ein Ordner kann nicht in sich selbst verschoben werden.")
		}
		srcParent, name := fsname.Split(full)
		if move && sameSpace && srcParent == dstFolder {
			out = append(out, dst.Rel(full))
			continue // already there
		}
		name = uniqueName(dst.Space.Union, dstFolder, name)
		target := fsname.Join(dstFolder, name)
		size := row.Size
		if row.IsDir {
			c.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files WHERE space_id = ? AND is_dir = 0 AND path >= ? AND path < ?`,
				src.Space.ID, full+"/", full+"0").Scan(&size)
		}
		if move && sameSpace {
			if row.IsDir {
				c.unwatchTree(src.Space, full)
			}
			if err := src.Space.Union.Rename(full, target); err != nil {
				if row.IsDir {
					c.watchTree(src.Space, full)
				}
				return out, mapFSErr(err)
			}
			c.movePaths(src.Space.ID, full, target)
			if row.IsDir {
				c.watchTree(src.Space, target)
			}
		} else {
			if !(move && c.sameOwner(src.Space, dst.Space)) {
				if err := c.checkQuota(dst.Space, size); err != nil {
					return out, err
				}
			}
			if err := c.copyTree(src.Space, full, dst.Space, target); err != nil {
				return out, mapFSErr(err)
			}
			c.scanSubtree(dst.Space, target)
			if move {
				if row.IsDir {
					c.unwatchTree(src.Space, full)
				}
				if err := src.Space.Union.RemoveAll(full); err != nil {
					return out, mapFSErr(err)
				}
				deleteSubtree(c.DB, src.Space.ID, full)
				c.cleanupDanglingRefs(src.Space.ID)
			}
		}
		action := "copy"
		if move {
			action = "move"
		}
		c.logActivity(dst.User, "", dst.Space.ID, action, target, "")
		c.indexPathChain(dst.Space, target)
		c.publishChange(src.Space.ID, srcParent)
		c.publishChange(dst.Space.ID, dstFolder)
		out = append(out, dst.Rel(target))
	}
	c.afterWrite(dst.Space)
	return out, nil
}

func (c *Core) sameOwner(a, b *Space) bool {
	return a.Kind == KindHome && b.Kind == KindHome && a.OwnerID == b.OwnerID
}

// copyTree copies a file or folder between (or within) spaces.
func (c *Core) copyTree(src *Space, srcPath string, dst *Space, dstPath string) error {
	fi, i, err := src.Union.Stat(srcPath)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		parent, _ := fsname.Split(dstPath)
		j, err := dst.Union.PickRoot(parent, fi.Size())
		if err != nil {
			return err
		}
		return storage.CopyFile(src.Union.Abs(i, srcPath), dst.Union.Abs(j, dstPath))
	}
	if err := dst.Union.Mkdir(dstPath); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	entries, err := src.Union.ReadDir(srcPath)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := c.copyTree(src, fsname.Join(srcPath, e.Name), dst, fsname.Join(dstPath, e.Name)); err != nil {
			return err
		}
	}
	return nil
}

// Delete moves items to the trash.
func (c *Core) Delete(a *Access, rels []string) error {
	if err := c.requireWrite(a); err != nil {
		return err
	}
	for _, p := range rels {
		rel, err := relPath(p)
		if err != nil || rel == "" {
			return badRequest("Ungültiger Pfad.")
		}
		full := a.Full(rel)
		if full == a.Base && a.ShareID != 0 {
			return ErrForbidden
		}
		if err := c.trashPath(a.Space, full, a.User); err != nil {
			return err
		}
		parent, _ := fsname.Split(full)
		c.logActivity(a.User, "", a.Space.ID, "delete", full, "")
		c.publishChange(a.Space.ID, parent)
	}
	return nil
}

// WriteContent replaces (or creates) a file with the given content. Used by
// the text editor and WebDAV-like small writes.
func (c *Core) WriteContent(a *Access, rel string, r io.Reader, size int64) (FileInfo, error) {
	if err := c.requireWrite(a); err != nil {
		return FileInfo{}, err
	}
	rel, err := fsname.SanitizePath(rel)
	if err != nil || rel == "" {
		return FileInfo{}, badRequest("Ungültiger Pfad.")
	}
	full := a.Full(rel)
	parent, _ := fsname.Split(full)
	var old int64
	idx := -1
	if fi, i, err := a.Space.Union.Stat(full); err == nil {
		if fi.IsDir() {
			return FileInfo{}, ErrExists
		}
		old, idx = fi.Size(), i
	}
	if err := c.checkQuota(a.Space, size-old); err != nil {
		return FileInfo{}, err
	}
	if idx < 0 {
		idx, err = a.Space.Union.PickRoot(parent, size)
		if err != nil {
			return FileInfo{}, mapFSErr(err)
		}
	}
	dstPath := a.Space.Union.Abs(idx, full)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return FileInfo{}, err
	}
	tmp := dstPath + ".hdcopy"
	f, err := os.Create(tmp)
	if err != nil {
		return FileInfo{}, err
	}
	if _, err := io.Copy(f, io.LimitReader(r, size+1)); err != nil {
		f.Close()
		os.Remove(tmp)
		return FileInfo{}, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return FileInfo{}, err
	}
	if err := storage.MoveFile(tmp, dstPath); err != nil {
		os.Remove(tmp)
		return FileInfo{}, err
	}
	c.indexPathChain(a.Space, full)
	c.logActivity(a.User, "", a.Space.ID, "edit", full, "")
	c.publishChange(a.Space.ID, parent)
	c.afterWrite(a.Space)
	return c.Stat(a, a.Rel(full))
}

// ----- Quota -----

// Usage returns the bytes a user occupies: own drive, its trash and the vault.
func (c *Core) Usage(userID int64) int64 {
	var files, trash, vault int64
	c.DB.QueryRow(`SELECT COALESCE(SUM(f.size), 0) FROM files f JOIN spaces s ON s.id = f.space_id
		WHERE s.kind = 'home' AND s.owner_id = ? AND f.is_dir = 0`, userID).Scan(&files)
	c.DB.QueryRow(`SELECT COALESCE(SUM(t.size), 0) FROM trash t JOIN spaces s ON s.id = t.space_id
		WHERE s.kind = 'home' AND s.owner_id = ?`, userID).Scan(&trash)
	c.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM vault_items WHERE user_id = ?`, userID).Scan(&vault)
	return files + trash + vault
}

// checkQuota fails if adding extra bytes to space would exceed its owner's limit.
func (c *Core) checkQuota(s *Space, extra int64) error {
	if s.Kind != KindHome || extra <= 0 {
		return nil
	}
	return c.checkUserQuota(s.OwnerID, extra)
}

func (c *Core) checkUserQuota(userID, extra int64) error {
	u, err := c.GetUser(userID)
	if err != nil {
		return err
	}
	if u.QuotaBytes > 0 && c.Usage(userID)+extra > u.QuotaBytes {
		return ErrQuota
	}
	return nil
}

// afterWrite warns the owner once when their storage gets 90 % full.
func (c *Core) afterWrite(s *Space) {
	if s.Kind != KindHome {
		return
	}
	c.checkQuotaWarning(s.OwnerID)
}

func (c *Core) checkQuotaWarning(userID int64) {
	u, err := c.GetUser(userID)
	if err != nil || u.QuotaBytes <= 0 {
		return
	}
	used := c.Usage(userID)
	pct := int(used * 100 / u.QuotaBytes)
	var warned int
	c.DB.QueryRow(`SELECT warned_quota FROM users WHERE id = ?`, userID).Scan(&warned)
	switch {
	case pct >= 90 && warned == 0:
		c.DB.Exec(`UPDATE users SET warned_quota = 1 WHERE id = ?`, userID)
		c.Notify(userID, "quota_warning", map[string]any{"percent": pct, "used": used, "quota": u.QuotaBytes})
	case pct < 85 && warned == 1:
		c.DB.Exec(`UPDATE users SET warned_quota = 0 WHERE id = ?`, userID)
	}
}

// checkDiskSpace warns admins when a storage location is 90 % full.
func (c *Core) checkDiskSpace() {
	for _, l := range c.Locations() {
		if l.Total == 0 {
			continue
		}
		pct := int(100 - l.Free*100/l.Total)
		key := "disk_warned:" + l.Path
		if pct >= 90 && c.getSetting(key) == "" {
			c.setSetting(key, "1")
			c.notifyAdmins("disk_warning", map[string]any{"path": l.Path, "percent": pct, "free": l.Free})
			c.Log.Warn("storage location almost full", "path", l.Path, "percent", pct)
		} else if pct < 85 && c.getSetting(key) != "" {
			c.DB.Exec(`DELETE FROM settings WHERE key = ?`, key)
		}
	}
}

// DiskWarnings lists storage locations that are at least 90 % full.
func (c *Core) DiskWarnings() []string {
	var out []string
	for _, l := range c.Locations() {
		if l.Total > 0 && 100-l.Free*100/l.Total >= 90 {
			out = append(out, fmt.Sprintf("%s (%d%%)", l.Path, 100-l.Free*100/l.Total))
		}
	}
	return out
}
