package core

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/erikedits/homelydrive/internal/server/fsname"
	"github.com/erikedits/homelydrive/internal/server/vaultcrypto"
)

// The vault ("Tresor") is an encrypted area per user with its own password.
// File contents and names are encrypted on disk; the key is only held in
// memory while the vault is unlocked and is forgotten after 15 idle minutes.

const vaultIdle = 15 * time.Minute

type vaultSession struct {
	key      []byte
	lastUsed time.Time
}

// VaultItem is a file or folder inside the vault.
type VaultItem struct {
	ID       int64  `json:"id"`
	ParentID int64  `json:"parent_id"`
	Name     string `json:"name"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	MTime    int64  `json:"mtime"`
	Category string `json:"category,omitempty"`
	Viewer   string `json:"viewer,omitempty"`
	Mime     string `json:"mime,omitempty"`
}

// VaultState tells the UI whether the vault exists and is open.
type VaultState struct {
	Configured bool `json:"configured"`
	Unlocked   bool `json:"unlocked"`
}

func (c *Core) vaultDir(userID int64) string {
	locs := c.Cfg.Get().StorageLocations
	return filepath.Join(locs[0], "vaults", strconv.FormatInt(userID, 10))
}

// VaultStatus returns the vault state of a user.
func (c *Core) VaultStatus(u *User) VaultState {
	var configured bool
	c.DB.QueryRow(`SELECT vault_key IS NOT NULL FROM users WHERE id = ?`, u.ID).Scan(&configured)
	_, err := c.vaultKey(u.ID)
	return VaultState{Configured: configured, Unlocked: err == nil}
}

// VaultSetup creates the vault with a password.
func (c *Core) VaultSetup(u *User, password string) error {
	if c.VaultStatus(u).Configured {
		return ErrExists
	}
	if len(password) < 8 {
		return badRequest("Das Tresor-Passwort muss mindestens 8 Zeichen haben.")
	}
	salt := vaultcrypto.NewSalt()
	dataKey := vaultcrypto.NewDataKey()
	wrapped, err := vaultcrypto.Seal(vaultcrypto.DeriveKEK(password, salt), dataKey)
	if err != nil {
		return err
	}
	if _, err := c.DB.Exec(`UPDATE users SET vault_salt = ?, vault_key = ? WHERE id = ?`, salt, wrapped, u.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(c.vaultDir(u.ID), 0o700); err != nil {
		return err
	}
	c.setVaultKey(u.ID, dataKey)
	c.logActivity(u, "", 0, "vault_setup", "", "")
	return nil
}

func (c *Core) unwrapVaultKey(userID int64, password string) ([]byte, error) {
	var salt, wrapped []byte
	if err := c.DB.QueryRow(`SELECT vault_salt, vault_key FROM users WHERE id = ?`, userID).Scan(&salt, &wrapped); err != nil || wrapped == nil {
		return nil, ErrNotFound
	}
	key, err := vaultcrypto.Open(vaultcrypto.DeriveKEK(password, salt), wrapped)
	if err != nil {
		return nil, badRequest("Falsches Tresor-Passwort.")
	}
	return key, nil
}

// VaultUnlock opens the vault.
func (c *Core) VaultUnlock(u *User, password string) error {
	key, err := c.unwrapVaultKey(u.ID, password)
	if err != nil {
		return err
	}
	c.setVaultKey(u.ID, key)
	return nil
}

// VaultLock closes the vault immediately.
func (c *Core) VaultLock(u *User) {
	c.vaultMu.Lock()
	delete(c.vaultKeys, u.ID)
	c.vaultMu.Unlock()
}

// VaultChangePassword re-wraps the data key with a new password.
func (c *Core) VaultChangePassword(u *User, oldPw, newPw string) error {
	key, err := c.unwrapVaultKey(u.ID, oldPw)
	if err != nil {
		return err
	}
	if len(newPw) < 8 {
		return badRequest("Das Tresor-Passwort muss mindestens 8 Zeichen haben.")
	}
	salt := vaultcrypto.NewSalt()
	wrapped, err := vaultcrypto.Seal(vaultcrypto.DeriveKEK(newPw, salt), key)
	if err != nil {
		return err
	}
	_, err = c.DB.Exec(`UPDATE users SET vault_salt = ?, vault_key = ? WHERE id = ?`, salt, wrapped, u.ID)
	return err
}

func (c *Core) setVaultKey(userID int64, key []byte) {
	c.vaultMu.Lock()
	c.vaultKeys[userID] = &vaultSession{key: key, lastUsed: time.Now()}
	c.vaultMu.Unlock()
}

func (c *Core) vaultKey(userID int64) ([]byte, error) {
	c.vaultMu.Lock()
	defer c.vaultMu.Unlock()
	s, ok := c.vaultKeys[userID]
	if !ok || time.Since(s.lastUsed) > vaultIdle {
		delete(c.vaultKeys, userID)
		return nil, ErrVaultLocked
	}
	s.lastUsed = time.Now()
	return s.key, nil
}

func (c *Core) lockIdleVaults() {
	c.vaultMu.Lock()
	defer c.vaultMu.Unlock()
	for id, s := range c.vaultKeys {
		if time.Since(s.lastUsed) > vaultIdle {
			delete(c.vaultKeys, id)
		}
	}
}

func (c *Core) vaultItemRow(userID, id int64) (*VaultItem, string, error) {
	it := &VaultItem{ID: id}
	var nameEnc []byte
	var stored string
	err := c.DB.QueryRow(`SELECT parent_id, is_dir, name_enc, size, stored, mtime FROM vault_items WHERE id = ? AND user_id = ?`, id, userID).
		Scan(&it.ParentID, &it.IsDir, &nameEnc, &it.Size, &stored, &it.MTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	key, err := c.vaultKey(userID)
	if err != nil {
		return nil, "", err
	}
	name, err := vaultcrypto.Open(key, nameEnc)
	if err != nil {
		return nil, "", err
	}
	it.Name = string(name)
	c.fillVaultType(it)
	return it, stored, nil
}

func (c *Core) fillVaultType(it *VaultItem) {
	if it.IsDir {
		return
	}
	if t, ok := c.TypeOf(it.Name); ok {
		it.Category, it.Viewer, it.Mime = t.Category, t.Viewer, t.Mime
	} else {
		it.Category, it.Viewer, it.Mime = "other", "download", "application/octet-stream"
	}
}

func (c *Core) checkVaultParent(userID, parent int64) error {
	if parent == 0 {
		return nil
	}
	it, _, err := c.vaultItemRow(userID, parent)
	if err != nil {
		return err
	}
	if !it.IsDir {
		return badRequest("Kein Ordner.")
	}
	return nil
}

// VaultList lists a vault folder (parent 0 = top level).
func (c *Core) VaultList(u *User, parent int64) ([]VaultItem, error) {
	key, err := c.vaultKey(u.ID)
	if err != nil {
		return nil, err
	}
	rows, err := c.DB.Query(`SELECT id, parent_id, is_dir, name_enc, size, mtime FROM vault_items WHERE user_id = ? AND parent_id = ?`, u.ID, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VaultItem{}
	for rows.Next() {
		var it VaultItem
		var nameEnc []byte
		if err := rows.Scan(&it.ID, &it.ParentID, &it.IsDir, &nameEnc, &it.Size, &it.MTime); err != nil {
			return nil, err
		}
		name, err := vaultcrypto.Open(key, nameEnc)
		if err != nil {
			continue
		}
		it.Name = string(name)
		c.fillVaultType(&it)
		out = append(out, it)
	}
	return out, rows.Err()
}

func (c *Core) vaultNameTaken(u *User, parent int64, name string, except int64) bool {
	items, _ := c.VaultList(u, parent)
	for _, it := range items {
		if it.ID != except && strings.EqualFold(it.Name, name) {
			return true
		}
	}
	return false
}

func (c *Core) uniqueVaultName(u *User, parent int64, name string) string {
	if !c.vaultNameTaken(u, parent, name, 0) {
		return name
	}
	for n := 1; ; n++ {
		cand := fsname.Numbered(name, n)
		if !c.vaultNameTaken(u, parent, cand, 0) {
			return cand
		}
	}
}

// VaultMkdir creates a folder in the vault.
func (c *Core) VaultMkdir(u *User, parent int64, name string) (*VaultItem, error) {
	key, err := c.vaultKey(u.ID)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if !fsname.ValidName(name) {
		return nil, badRequest("Ungültiger Name.")
	}
	if err := c.checkVaultParent(u.ID, parent); err != nil {
		return nil, err
	}
	if c.vaultNameTaken(u, parent, name, 0) {
		return nil, ErrExists
	}
	enc, err := vaultcrypto.Seal(key, []byte(name))
	if err != nil {
		return nil, err
	}
	res, err := c.DB.Exec(`INSERT INTO vault_items(user_id, parent_id, is_dir, name_enc, mtime, created_at) VALUES (?, ?, 1, ?, ?, ?)`,
		u.ID, parent, enc, now(), now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &VaultItem{ID: id, ParentID: parent, Name: name, IsDir: true, MTime: now()}, nil
}

// VaultRename renames a vault item.
func (c *Core) VaultRename(u *User, id int64, name string) error {
	key, err := c.vaultKey(u.ID)
	if err != nil {
		return err
	}
	it, _, err := c.vaultItemRow(u.ID, id)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if !fsname.ValidName(name) {
		return badRequest("Ungültiger Name.")
	}
	if c.vaultNameTaken(u, it.ParentID, name, id) {
		return ErrExists
	}
	enc, err := vaultcrypto.Seal(key, []byte(name))
	if err != nil {
		return err
	}
	_, err = c.DB.Exec(`UPDATE vault_items SET name_enc = ? WHERE id = ? AND user_id = ?`, enc, id, u.ID)
	return err
}

// VaultDelete deletes vault items permanently (folders recursively).
func (c *Core) VaultDelete(u *User, ids []int64) error {
	if _, err := c.vaultKey(u.ID); err != nil {
		return err
	}
	for _, id := range ids {
		var isDir bool
		var stored string
		if err := c.DB.QueryRow(`SELECT is_dir, stored FROM vault_items WHERE id = ? AND user_id = ?`, id, u.ID).Scan(&isDir, &stored); err != nil {
			return ErrNotFound
		}
		if isDir {
			var children []int64
			rows, err := c.DB.Query(`SELECT id FROM vault_items WHERE user_id = ? AND parent_id = ?`, u.ID, id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var cid int64
				rows.Scan(&cid)
				children = append(children, cid)
			}
			rows.Close()
			if err := c.VaultDelete(u, children); err != nil {
				return err
			}
		} else if stored != "" {
			os.Remove(filepath.Join(c.vaultDir(u.ID), stored))
		}
		c.DB.Exec(`DELETE FROM vault_items WHERE id = ? AND user_id = ?`, id, u.ID)
	}
	return nil
}

// VaultOpen opens a vault file for reading (decrypting on the fly, seekable).
func (c *Core) VaultOpen(u *User, id int64) (*vaultcrypto.Reader, *VaultItem, error) {
	it, stored, err := c.vaultItemRow(u.ID, id)
	if err != nil {
		return nil, nil, err
	}
	if it.IsDir {
		return nil, nil, badRequest("Kein Datei.")
	}
	key, err := c.vaultKey(u.ID)
	if err != nil {
		return nil, nil, err
	}
	r, err := vaultcrypto.OpenFile(filepath.Join(c.vaultDir(u.ID), stored), key)
	if err != nil {
		return nil, nil, err
	}
	return r, it, nil
}

func (c *Core) createVaultUpload(u *User, req UploadRequest) (*UploadStatus, error) {
	if _, err := c.vaultKey(u.ID); err != nil {
		return nil, err
	}
	parent := *req.VaultParent
	if err := c.checkVaultParent(u.ID, parent); err != nil {
		return nil, err
	}
	_, name := fsname.Split(strings.ReplaceAll(req.Path, "\\", "/"))
	name = fsname.Sanitize(name)
	if err := c.checkUserQuota(u.ID, req.Size); err != nil {
		return nil, err
	}
	id := randomToken(18)
	if err := os.MkdirAll(filepath.Join(c.vaultDir(u.ID), ".uploads"), 0o700); err != nil {
		return nil, err
	}
	_, err := c.DB.Exec(`INSERT INTO uploads(id, user_id, path, size, offset, sha256, mtime, conflict, root_idx, vault_parent, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, 'rename', 0, ?, ?, ?)`, id, u.ID, name, req.Size, strings.ToLower(req.SHA256), req.MTime, parent, now(), now())
	if err != nil {
		return nil, err
	}
	if req.Size == 0 {
		return c.finishUpload(id, &Access{User: u})
	}
	return &UploadStatus{ID: id, Status: "pending", Size: req.Size, Path: name}, nil
}

// finishVaultUpload encrypts a completed upload into the vault.
func (c *Core) finishVaultUpload(r *uploadRow, tmp string) (*UploadStatus, error) {
	u, err := c.GetUser(r.UserID.Int64)
	if err != nil {
		return nil, err
	}
	key, err := c.vaultKey(u.ID)
	if err != nil {
		return nil, err
	}
	in, err := os.Open(tmp)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	stored := randomToken(18) + ".hdv"
	dst := filepath.Join(c.vaultDir(u.ID), stored)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	w, err := vaultcrypto.NewWriter(out, key)
	if err != nil {
		out.Close()
		os.Remove(dst)
		return nil, err
	}
	if _, err := in.WriteTo(w); err != nil {
		out.Close()
		os.Remove(dst)
		return nil, err
	}
	if err := w.Close(); err != nil {
		out.Close()
		os.Remove(dst)
		return nil, err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return nil, err
	}
	name := c.uniqueVaultName(u, r.VaultParent.Int64, r.Path)
	enc, err := vaultcrypto.Seal(key, []byte(name))
	if err != nil {
		return nil, err
	}
	mtime := r.MTime
	if mtime == 0 {
		mtime = now()
	}
	res, err := c.DB.Exec(`INSERT INTO vault_items(user_id, parent_id, is_dir, name_enc, size, stored, mtime, created_at) VALUES (?, ?, 0, ?, ?, ?, ?, ?)`,
		u.ID, r.VaultParent.Int64, enc, r.Size, stored, mtime, now())
	if err != nil {
		os.Remove(dst)
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.checkQuotaWarning(u.ID)
	_ = id
	return &UploadStatus{Status: "done", Size: r.Size, Offset: r.Size, Path: name}, nil
}

// VaultReset deletes the whole vault (for a forgotten vault password). The
// account password is required.
func (c *Core) VaultReset(u *User, accountPassword string) error {
	if _, err := c.CheckPassword(u.Username, accountPassword); err != nil {
		return badRequest("Falsches Kontopasswort.")
	}
	c.VaultLock(u)
	c.deleteVaultFiles(u.ID)
	c.DB.Exec(`DELETE FROM vault_items WHERE user_id = ?`, u.ID)
	_, err := c.DB.Exec(`UPDATE users SET vault_salt = NULL, vault_key = NULL WHERE id = ?`, u.ID)
	c.logActivity(u, "", 0, "vault_reset", "", "")
	return err
}

func (c *Core) deleteVaultFiles(userID int64) {
	removeAllQuiet(c.vaultDir(userID))
}
