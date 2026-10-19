package core

import (
	"fmt"
	"time"

	"github.com/erikedits/homelydrive/internal/server/fsname"
)

// TrashItem is an entry of the trash ("Papierkorb").
type TrashItem struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Space     string `json:"space"`
	SpaceName string `json:"space_name,omitempty"`
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	DeletedBy string `json:"deleted_by,omitempty"`
	DeletedAt int64  `json:"deleted_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func trashName(id int64) string { return fmt.Sprintf("t%d", id) }

// trashPath moves a space path into the trash.
func (c *Core) trashPath(s *Space, full string, by *User) error {
	r, err := c.statRow(s, full)
	if err != nil {
		return err
	}
	size := r.Size
	if r.IsDir {
		c.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files WHERE space_id = ? AND is_dir = 0 AND path >= ? AND path < ?`,
			s.ID, full+"/", full+"0").Scan(&size)
	}
	var byID any
	if by != nil {
		byID = by.ID
	}
	res, err := c.DB.Exec(`INSERT INTO trash(space_id, orig_path, name, is_dir, size, deleted_by, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, full, r.Name, r.IsDir, size, byID, now())
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	if r.IsDir {
		c.unwatchTree(s, full)
	}
	if err := s.Union.MoveToTrash(full, trashName(id)); err != nil {
		c.DB.Exec(`DELETE FROM trash WHERE id = ?`, id)
		if r.IsDir {
			c.watchTree(s, full)
		}
		return mapFSErr(err)
	}
	deleteSubtree(c.DB, s.ID, full)
	c.cleanupDanglingRefs(s.ID)
	return nil
}

// ListTrash returns trashed items the user may see: from spaces they fully
// access plus anything they deleted themselves (e.g. inside a share).
func (c *Core) ListTrash(u *User) ([]TrashItem, error) {
	scopes := c.scopes(u)
	where, args := scopeSQL(scopes, true, "t")
	days := int64(c.Cfg.Get().TrashDays)
	rows, err := c.DB.Query(`SELECT t.id, t.space_id, t.orig_path, t.name, t.is_dir, t.size, t.deleted_at,
		COALESCE(NULLIF(u.display_name, ''), u.username, ''), sp.kind, sp.name
		FROM trash t JOIN spaces sp ON sp.id = t.space_id LEFT JOIN users u ON u.id = t.deleted_by
		WHERE (`+where+` OR t.deleted_by = ?) ORDER BY t.deleted_at DESC`, append(args, u.ID)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrashItem{}
	for rows.Next() {
		var it TrashItem
		var spaceID int64
		var kind, spaceName string
		if err := rows.Scan(&it.ID, &spaceID, &it.Path, &it.Name, &it.IsDir, &it.Size, &it.DeletedAt, &it.DeletedBy, &kind, &spaceName); err != nil {
			return nil, err
		}
		it.ExpiresAt = it.DeletedAt + days*24*3600*1000
		if ref, rel, ok := refFor(scopes, spaceID, it.Path); ok {
			it.Space, it.Path = ref, rel
		}
		if kind != KindHome {
			it.SpaceName = spaceName
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (c *Core) trashItemFor(u *User, id int64) (*Space, string, error) {
	var spaceID int64
	var orig string
	var deletedBy *int64
	if err := c.DB.QueryRow(`SELECT space_id, orig_path, deleted_by FROM trash WHERE id = ?`, id).Scan(&spaceID, &orig, &deletedBy); err != nil {
		return nil, "", ErrNotFound
	}
	allowed := deletedBy != nil && *deletedBy == u.ID
	if !allowed {
		for _, s := range c.scopes(u) {
			if s.spaceID == spaceID && s.base == "" && s.canWrite {
				allowed = true
			}
		}
	}
	if !allowed {
		return nil, "", ErrNotFound
	}
	s, err := c.GetSpace(spaceID)
	return s, orig, err
}

// RestoreTrash puts items back where they were.
func (c *Core) RestoreTrash(u *User, ids []int64) error {
	for _, id := range ids {
		s, orig, err := c.trashItemFor(u, id)
		if err != nil {
			return err
		}
		var size int64
		c.DB.QueryRow(`SELECT size FROM trash WHERE id = ?`, id).Scan(&size)
		if err := s.Union.RestoreFromTrash(trashName(id), orig); err != nil {
			return mapFSErr(err)
		}
		c.DB.Exec(`DELETE FROM trash WHERE id = ?`, id)
		parent, _ := fsname.Split(orig)
		c.indexPathChain(s, parent)
		c.scanSubtree(s, parent)
		c.logActivity(u, "", s.ID, "restore", orig, "")
		c.publishChange(s.ID, parent)
	}
	return nil
}

// PurgeTrash deletes items permanently.
func (c *Core) PurgeTrash(u *User, ids []int64) error {
	for _, id := range ids {
		s, orig, err := c.trashItemFor(u, id)
		if err != nil {
			return err
		}
		if err := s.Union.PurgeTrash(trashName(id)); err != nil {
			return err
		}
		c.DB.Exec(`DELETE FROM trash WHERE id = ?`, id)
		c.logActivity(u, "", s.ID, "purge", orig, "")
	}
	return nil
}

// EmptyTrash deletes everything in the user's trash permanently.
func (c *Core) EmptyTrash(u *User) error {
	items, err := c.ListTrash(u)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return c.PurgeTrash(u, ids)
}

// ExpireTrash deletes items older than the configured number of days.
func (c *Core) ExpireTrash() {
	cutoff := time.Now().Add(-time.Duration(c.Cfg.Get().TrashDays) * 24 * time.Hour).UnixMilli()
	rows, err := c.DB.Query(`SELECT id, space_id FROM trash WHERE deleted_at < ?`, cutoff)
	if err != nil {
		return
	}
	type item struct{ id, space int64 }
	var items []item
	for rows.Next() {
		var it item
		rows.Scan(&it.id, &it.space)
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		if s, err := c.GetSpace(it.space); err == nil {
			if err := s.Union.PurgeTrash(trashName(it.id)); err != nil {
				c.Log.Warn("cannot purge trash item", "id", it.id, "err", err)
				continue
			}
		}
		c.DB.Exec(`DELETE FROM trash WHERE id = ?`, it.id)
	}
	if len(items) > 0 {
		c.Log.Info("trash expired", "items", len(items))
	}
}
