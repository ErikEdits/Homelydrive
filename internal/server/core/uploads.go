package core

import (
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/erikedits/homelydrive/internal/server/fsname"
	"github.com/erikedits/homelydrive/internal/server/storage"
)

// UploadRequest starts a resumable upload. Files of any size are supported:
// the client sends the data in one or more PATCH requests and can resume
// after an interruption at the offset the server reports.
type UploadRequest struct {
	Space       string `json:"space"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	MTime       int64  `json:"mtime"`    // unix milliseconds, optional
	Conflict    string `json:"conflict"` // rename (default), overwrite, skip
	VaultParent *int64 `json:"vault_parent,omitempty"`
}

// UploadStatus is the answer to upload requests.
type UploadStatus struct {
	ID     string    `json:"id,omitempty"`
	Status string    `json:"status"` // pending, done, exists, copied, skipped
	Offset int64     `json:"offset"`
	Size   int64     `json:"size"`
	Path   string    `json:"path,omitempty"`
	File   *FileInfo `json:"file,omitempty"`
}

var errChecksum = &BadRequest{Msg: "Die Prüfsumme stimmt nicht – die Datei wurde beim Übertragen beschädigt. Bitte erneut hochladen."}

type uploadRow struct {
	ID          string
	UserID      sql.NullInt64
	LinkID      sql.NullInt64
	SpaceID     sql.NullInt64
	Path        string
	Size        int64
	Offset      int64
	SHA256      string
	HashState   []byte
	MTime       int64
	Conflict    string
	RootIdx     int
	VaultParent sql.NullInt64
}

var uploadLocks sync.Map // upload id -> *sync.Mutex

func lockUpload(id string) func() {
	m, _ := uploadLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// uploadOwner identifies who uploads: a logged in user or an upload link.
type uploadOwner struct {
	user *User
	link *Link
}

// CreateUpload starts an upload into the folder of an access (user upload).
func (c *Core) CreateUpload(u *User, req UploadRequest) (*UploadStatus, error) {
	if req.VaultParent != nil {
		return c.createVaultUpload(u, req)
	}
	a, err := c.ResolveAccess(u, req.Space)
	if err != nil {
		return nil, err
	}
	return c.createUpload(a, uploadOwner{user: u}, req)
}

func (c *Core) createUpload(a *Access, owner uploadOwner, req UploadRequest) (*UploadStatus, error) {
	if err := c.requireWrite(a); err != nil {
		return nil, err
	}
	if req.Size < 0 {
		return nil, badRequest("Ungültige Größe.")
	}
	req.SHA256 = strings.ToLower(strings.TrimSpace(req.SHA256))
	if req.SHA256 != "" && len(req.SHA256) != 64 {
		return nil, badRequest("Ungültige Prüfsumme.")
	}
	rel, err := fsname.SanitizePath(req.Path)
	if err != nil || rel == "" {
		return nil, badRequest("Ungültiger Dateiname.")
	}
	full := a.Full(rel)
	parent, name := fsname.Split(full)
	if err := c.checkParentChain(a.Space, parent); err != nil {
		return nil, err
	}
	conflict := req.Conflict
	if owner.link != nil {
		conflict = "rename" // anonymous uploaders must never overwrite
	}
	var oldSize int64
	if fi, _, err := a.Space.Union.Stat(full); err == nil {
		if !fi.IsDir() && req.SHA256 != "" && fi.Size() == req.Size && c.sameContent(a.Space, full, req.SHA256) {
			info, _ := c.Stat(a, a.Rel(full))
			return &UploadStatus{Status: "exists", Size: req.Size, Offset: req.Size, Path: a.Rel(full), File: &info}, nil
		}
		switch {
		case conflict == "skip":
			return &UploadStatus{Status: "skipped", Size: req.Size, Path: a.Rel(full)}, nil
		case conflict == "overwrite" && !fi.IsDir():
			oldSize = fi.Size()
		default:
			name = uniqueName(a.Space.Union, parent, name)
			full = fsname.Join(parent, name)
		}
	}
	if err := c.checkQuota(a.Space, req.Size-oldSize); err != nil {
		return nil, err
	}
	// Already on the server somewhere else? Copy it locally instead of uploading.
	if req.SHA256 != "" && owner.user != nil && req.Size > 0 {
		if st, ok := c.copyExisting(a, full, req); ok {
			return st, nil
		}
	}
	idx, err := a.Space.Union.PickRoot(parent, req.Size)
	if err != nil {
		return nil, mapFSErr(err)
	}
	id := randomToken(18)
	if err := os.MkdirAll(a.Space.Union.Roots[idx].TempDir, 0o755); err != nil {
		return nil, err
	}
	var uid, lid any
	if owner.user != nil {
		uid = owner.user.ID
	}
	if owner.link != nil {
		lid = owner.link.ID
	}
	if req.Conflict == "" || owner.link != nil {
		req.Conflict = "rename"
	}
	_, err = c.DB.Exec(`INSERT INTO uploads(id, user_id, link_id, space_id, path, size, offset, sha256, mtime, conflict, root_idx, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)`, id, uid, lid, a.Space.ID, full, req.Size, req.SHA256, req.MTime, req.Conflict, idx, now(), now())
	if err != nil {
		return nil, err
	}
	st := &UploadStatus{ID: id, Status: "pending", Size: req.Size, Path: a.Rel(full)}
	if req.Size == 0 {
		return c.finishUpload(id, a)
	}
	return st, nil
}

// checkParentChain fails if a file sits where a folder is needed.
func (c *Core) checkParentChain(s *Space, parent string) error {
	if parent == "" {
		return nil
	}
	parts := strings.Split(parent, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		fi, _, err := s.Union.Stat(p)
		if err != nil {
			return nil // rest will be created
		}
		if !fi.IsDir() {
			return badRequest("%q ist eine Datei, kein Ordner.", p)
		}
	}
	return nil
}

// sameContent compares a stored file with a checksum.
func (c *Core) sameContent(s *Space, full, sha string) bool {
	if r, err := c.getRow(s.ID, full); err == nil && r.SHA256 != "" {
		if fi, _, err := s.Union.Stat(full); err == nil && fi.Size() == r.Size && fi.ModTime().UnixMilli() == r.MTime {
			return r.SHA256 == sha
		}
	}
	phys := c.physPath(s, full)
	if phys == "" {
		return false
	}
	h, err := hashFile(contextBackground(), phys)
	return err == nil && h == sha
}

// copyExisting copies an identical file the user can already read.
func (c *Core) copyExisting(a *Access, full string, req UploadRequest) (*UploadStatus, bool) {
	scopes := c.scopes(a.User)
	where, args := scopeSQL(scopes, false, "f")
	var spaceID int64
	var p string
	err := c.DB.QueryRow(`SELECT f.space_id, f.path FROM files f WHERE f.sha256 = ? AND f.size = ? AND f.is_dir = 0 AND `+where+` LIMIT 1`,
		append([]any{req.SHA256, req.Size}, args...)...).Scan(&spaceID, &p)
	if err != nil {
		return nil, false
	}
	src, err := c.GetSpace(spaceID)
	if err != nil {
		return nil, false
	}
	parent, _ := fsname.Split(full)
	if err := c.copyTree(src, p, a.Space, full); err != nil {
		return nil, false
	}
	if req.MTime > 0 {
		if phys := c.physPath(a.Space, full); phys != "" {
			t := time.UnixMilli(req.MTime)
			os.Chtimes(phys, t, t)
		}
	}
	c.ensureDirRows(a.Space, parent)
	if fi, _, err := a.Space.Union.Stat(full); err == nil {
		c.insertRow(c.DB, a.Space, full, storage.Entry{Name: fi.Name(), Size: fi.Size(), MTime: fi.ModTime().UnixMilli()}, req.SHA256)
	}
	c.logActivity(a.User, "", a.Space.ID, "upload", full, "")
	c.publishChange(a.Space.ID, parent)
	c.afterWrite(a.Space)
	c.kickIndexer()
	info, _ := c.Stat(a, a.Rel(full))
	return &UploadStatus{Status: "copied", Size: req.Size, Offset: req.Size, Path: a.Rel(full), File: &info}, true
}

// ensureDirRows inserts index rows for every folder of a path chain without
// re-reading the folders (fast for uploads of many files).
func (c *Core) ensureDirRows(s *Space, dir string) {
	if dir == "" {
		return
	}
	parts := strings.Split(dir, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		parent, name := fsname.Split(p)
		c.DB.Exec(`INSERT OR IGNORE INTO files(space_id, path, parent, name, is_dir, size, mtime, indexed, created_at)
			VALUES (?, ?, ?, ?, 1, 0, ?, 1, ?)`, s.ID, p, parent, name, now(), now())
	}
}

func (c *Core) loadUpload(id string) (*uploadRow, error) {
	r := &uploadRow{}
	err := c.DB.QueryRow(`SELECT id, user_id, link_id, space_id, path, size, offset, sha256, hash_state, mtime, conflict, root_idx, vault_parent
		FROM uploads WHERE id = ?`, id).Scan(&r.ID, &r.UserID, &r.LinkID, &r.SpaceID, &r.Path, &r.Size, &r.Offset, &r.SHA256,
		&r.HashState, &r.MTime, &r.Conflict, &r.RootIdx, &r.VaultParent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (r *uploadRow) ownedBy(o uploadOwner) bool {
	if o.user != nil {
		return r.UserID.Valid && r.UserID.Int64 == o.user.ID
	}
	return o.link != nil && r.LinkID.Valid && r.LinkID.Int64 == o.link.ID
}

func (c *Core) uploadTemp(r *uploadRow) (string, error) {
	if r.VaultParent.Valid {
		return filepath.Join(c.vaultDir(r.UserID.Int64), ".uploads", r.ID+".part"), nil
	}
	s, err := c.GetSpace(r.SpaceID.Int64)
	if err != nil {
		return "", err
	}
	if r.RootIdx >= len(s.Union.Roots) {
		return "", ErrNotFound
	}
	return filepath.Join(s.Union.Roots[r.RootIdx].TempDir, r.ID+".part"), nil
}

// UploadOffset returns how many bytes have been received.
func (c *Core) UploadOffset(o uploadOwner, id string) (*UploadStatus, error) {
	r, err := c.loadUpload(id)
	if err != nil {
		return nil, err
	}
	if !r.ownedBy(o) {
		return nil, ErrNotFound
	}
	return &UploadStatus{ID: id, Status: "pending", Offset: r.Offset, Size: r.Size}, nil
}

// UserOwner / LinkOwner build upload owners for the API layer.
func UserOwner(u *User) uploadOwner { return uploadOwner{user: u} }
func LinkOwner(l *Link) uploadOwner { return uploadOwner{link: l} }

// ErrOffsetMismatch is returned when a chunk does not continue the upload.
type ErrOffsetMismatch struct{ Offset int64 }

func (e *ErrOffsetMismatch) Error() string { return "offset mismatch" }

// WriteUpload appends data at offset. When the file is complete it is moved
// into place and the final status is returned.
func (c *Core) WriteUpload(o uploadOwner, id string, offset int64, body io.Reader) (*UploadStatus, error) {
	unlock := lockUpload(id)
	defer unlock()
	r, err := c.loadUpload(id)
	if err != nil {
		return nil, err
	}
	if !r.ownedBy(o) {
		return nil, ErrNotFound
	}
	if offset != r.Offset {
		return nil, &ErrOffsetMismatch{Offset: r.Offset}
	}
	tmp, err := c.uploadTemp(r)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE, 0o640)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(r.Offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	f.Truncate(r.Offset)
	h := sha256.New()
	if r.Offset > 0 && len(r.HashState) > 0 {
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.HashState); err != nil {
			f.Close()
			return nil, err
		}
	} else if r.Offset > 0 {
		f.Close()
		return nil, errors.New("upload state lost")
	}
	buf := make([]byte, 1<<20)
	pos := r.Offset
	lastSave := pos
	var readErr error
	for pos < r.Size {
		want := int64(len(buf))
		if r.Size-pos < want {
			want = r.Size - pos
		}
		n, err := body.Read(buf[:want])
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				readErr = werr
				break
			}
			h.Write(buf[:n])
			pos += int64(n)
			if pos-lastSave >= 64<<20 {
				c.saveUploadState(id, pos, h)
				lastSave = pos
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}
	syncErr := f.Sync()
	f.Close()
	if syncErr == nil {
		c.saveUploadState(id, pos, h)
	}
	if pos >= r.Size {
		// Reject extra bytes so a client bug cannot silently corrupt files.
		if n, _ := body.Read(buf[:1]); n > 0 {
			return nil, badRequest("Mehr Daten als angekündigt.")
		}
		var a *Access
		if !r.VaultParent.Valid {
			a, err = c.accessForUpload(r, o)
			if err != nil {
				return nil, err
			}
		}
		return c.finishUploadLocked(r, a, hex.EncodeToString(h.Sum(nil)), o)
	}
	if readErr != nil {
		return &UploadStatus{ID: id, Status: "pending", Offset: pos, Size: r.Size}, readErr
	}
	return &UploadStatus{ID: id, Status: "pending", Offset: pos, Size: r.Size}, nil
}

func (c *Core) saveUploadState(id string, pos int64, h hash.Hash) {
	state, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	c.DB.Exec(`UPDATE uploads SET offset = ?, hash_state = ?, updated_at = ? WHERE id = ?`, pos, state, now(), id)
}

func (c *Core) accessForUpload(r *uploadRow, o uploadOwner) (*Access, error) {
	s, err := c.GetSpace(r.SpaceID.Int64)
	if err != nil {
		return nil, err
	}
	return &Access{Space: s, CanWrite: true, User: o.user}, nil
}

// finishUpload completes a zero-byte upload.
func (c *Core) finishUpload(id string, a *Access) (*UploadStatus, error) {
	unlock := lockUpload(id)
	defer unlock()
	r, err := c.loadUpload(id)
	if err != nil {
		return nil, err
	}
	tmp, _ := c.uploadTemp(r)
	if f, err := os.Create(tmp); err == nil {
		f.Close()
	}
	h := sha256.New()
	return c.finishUploadLocked(r, a, hex.EncodeToString(h.Sum(nil)), uploadOwner{user: a.User})
}

func (c *Core) finishUploadLocked(r *uploadRow, a *Access, sum string, o uploadOwner) (*UploadStatus, error) {
	tmp, err := c.uploadTemp(r)
	if err != nil {
		return nil, err
	}
	drop := func() {
		os.Remove(tmp)
		c.DB.Exec(`DELETE FROM uploads WHERE id = ?`, r.ID)
		uploadLocks.Delete(r.ID)
	}
	if r.SHA256 != "" && r.SHA256 != sum {
		drop()
		return nil, errChecksum
	}
	if r.VaultParent.Valid {
		st, err := c.finishVaultUpload(r, tmp)
		drop()
		return st, err
	}
	s := a.Space
	full := r.Path
	parent, name := fsname.Split(full)
	if fi, _, err := s.Union.Stat(full); err == nil {
		switch {
		case r.Conflict == "skip":
			drop()
			return &UploadStatus{Status: "skipped", Size: r.Size, Offset: r.Size, Path: full}, nil
		case r.Conflict == "overwrite" && !fi.IsDir():
			if err := c.trashPath(s, full, o.user); err != nil {
				drop()
				return nil, err
			}
		default:
			name = uniqueName(s.Union, parent, name)
			full = fsname.Join(parent, name)
		}
	}
	dst := s.Union.Abs(r.RootIdx, full)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		drop()
		return nil, err
	}
	if err := storage.MoveFile(tmp, dst); err != nil {
		drop()
		return nil, err
	}
	c.DB.Exec(`DELETE FROM uploads WHERE id = ?`, r.ID)
	uploadLocks.Delete(r.ID)
	if r.MTime > 0 {
		t := time.UnixMilli(r.MTime)
		os.Chtimes(dst, t, t)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return nil, err
	}
	c.ensureDirRows(s, parent)
	id, _ := c.insertRow(c.DB, s, full, storage.Entry{Name: name, Size: fi.Size(), MTime: fi.ModTime().UnixMilli()}, sum)
	c.kickIndexer()
	if o.user != nil {
		c.touchRecent(o.user.ID, id)
		c.logActivity(o.user, "", s.ID, "upload", full, "")
	} else if o.link != nil {
		c.logActivity(nil, "Upload-Link", s.ID, "upload", full, "")
		c.notifyLinkUpload(o.link, name)
	}
	c.publishChange(s.ID, parent)
	c.afterWrite(s)
	st := &UploadStatus{Status: "done", Size: r.Size, Offset: r.Size}
	if a.User != nil || o.link == nil {
		st.Path = a.Rel(full)
		if row, err := c.getRow(s.ID, full); err == nil {
			info := c.accessInfo(a, []fileRow{row})[0]
			st.File = &info
		}
	} else {
		st.Path = name
	}
	return st, nil
}

// CancelUpload aborts an upload and deletes the partial data.
func (c *Core) CancelUpload(o uploadOwner, id string) error {
	unlock := lockUpload(id)
	defer unlock()
	r, err := c.loadUpload(id)
	if err != nil {
		return err
	}
	if !r.ownedBy(o) {
		return ErrNotFound
	}
	if tmp, err := c.uploadTemp(r); err == nil {
		os.Remove(tmp)
	}
	c.DB.Exec(`DELETE FROM uploads WHERE id = ?`, id)
	return nil
}

// cleanupUploads removes uploads that were abandoned for a week.
func (c *Core) cleanupUploads() {
	cutoff := time.Now().Add(-7 * 24 * time.Hour).UnixMilli()
	rows, err := c.DB.Query(`SELECT id FROM uploads WHERE updated_at < ?`, cutoff)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if r, err := c.loadUpload(id); err == nil {
			if tmp, err := c.uploadTemp(r); err == nil {
				os.Remove(tmp)
			}
		}
		c.DB.Exec(`DELETE FROM uploads WHERE id = ?`, id)
	}
}
