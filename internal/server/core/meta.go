package core

import (
	"regexp"
	"strings"
)

var colorRe = regexp.MustCompile(`^(#[0-9a-fA-F]{6})?$`)

// rowFor returns the index row of an item the user can access.
func (c *Core) rowFor(a *Access, rel string) (fileRow, error) {
	rel, err := relPath(rel)
	if err != nil {
		return fileRow{}, err
	}
	full := a.Full(rel)
	if full == "" {
		return fileRow{}, badRequest("Nicht möglich für den ganzen Bereich.")
	}
	return c.statRow(a.Space, full)
}

// SetStar adds or removes the star ("Markiert") of an item.
func (c *Core) SetStar(a *Access, rel string, starred bool) error {
	r, err := c.rowFor(a, rel)
	if err != nil {
		return err
	}
	if starred {
		_, err = c.DB.Exec(`INSERT OR IGNORE INTO stars(user_id, file_id, created_at) VALUES (?, ?, ?)`, a.User.ID, r.ID, now())
	} else {
		_, err = c.DB.Exec(`DELETE FROM stars WHERE user_id = ? AND file_id = ?`, a.User.ID, r.ID)
	}
	return err
}

// SetMeta changes the folder colour and/or the note of an item. Both are
// visible to everyone with access, so write permission is required.
func (c *Core) SetMeta(a *Access, rel string, color, note *string) error {
	if err := c.requireWrite(a); err != nil {
		return err
	}
	r, err := c.rowFor(a, rel)
	if err != nil {
		return err
	}
	if color != nil && !colorRe.MatchString(*color) {
		return badRequest("Ungültige Farbe.")
	}
	if note != nil && len(*note) > 10000 {
		return badRequest("Die Notiz ist zu lang (max. 10000 Zeichen).")
	}
	c.DB.Exec(`INSERT OR IGNORE INTO file_meta(file_id) VALUES (?)`, r.ID)
	if color != nil {
		c.DB.Exec(`UPDATE file_meta SET color = ? WHERE file_id = ?`, strings.ToLower(*color), r.ID)
	}
	if note != nil {
		c.DB.Exec(`UPDATE file_meta SET note = ? WHERE file_id = ?`, strings.TrimSpace(*note), r.ID)
	}
	c.DB.Exec(`DELETE FROM file_meta WHERE file_id = ? AND color = '' AND note = ''`, r.ID)
	c.publishChange(a.Space.ID, r.Parent)
	return nil
}

// SetTags replaces the user's tags on an item.
func (c *Core) SetTags(a *Access, rel string, tags []string) error {
	r, err := c.rowFor(a, rel)
	if err != nil {
		return err
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM file_tags WHERE file_id = ? AND tag_id IN (SELECT id FROM tags WHERE user_id = ?)`, r.ID, a.User.ID)
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || len(t) > 50 || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		tx.Exec(`INSERT OR IGNORE INTO tags(user_id, name) VALUES (?, ?)`, a.User.ID, t)
		var id int64
		tx.QueryRow(`SELECT id FROM tags WHERE user_id = ? AND name = ?`, a.User.ID, t).Scan(&id)
		tx.Exec(`INSERT OR IGNORE INTO file_tags(file_id, tag_id) VALUES (?, ?)`, r.ID, id)
	}
	// Forget tags that are no longer used anywhere.
	tx.Exec(`DELETE FROM tags WHERE user_id = ? AND id NOT IN (SELECT tag_id FROM file_tags)`, a.User.ID)
	return tx.Commit()
}

// Tag is a tag with its usage count.
type Tag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ListTags returns the user's tags.
func (c *Core) ListTags(u *User) ([]Tag, error) {
	rows, err := c.DB.Query(`SELECT t.name, COUNT(ft.file_id) FROM tags t LEFT JOIN file_tags ft ON ft.tag_id = t.id
		WHERE t.user_id = ? GROUP BY t.id ORDER BY t.name`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		rows.Scan(&t.Name, &t.Count)
		out = append(out, t)
	}
	return out, rows.Err()
}

// listByQuery runs a query returning file rows (alias f) and converts them to
// FileInfo with space references, skipping items the user cannot reach.
func (c *Core) listByQuery(u *User, query string, args ...any) ([]FileInfo, error) {
	rows, err := c.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var list []fileRow
	for rows.Next() {
		r, err := scanFileRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	rows.Close()
	return c.infosForUser(u, list), nil
}

// infosForUser converts rows from different spaces into FileInfo objects.
func (c *Core) infosForUser(u *User, list []fileRow) []FileInfo {
	scopes := c.scopes(u)
	writable := map[string]bool{}
	names := map[string]string{}
	for _, s := range scopes {
		writable[s.ref] = s.canWrite
	}
	if roots, err := c.ListRoots(u); err == nil {
		for _, r := range roots {
			if r.Kind != KindHome {
				names[r.Ref] = r.Name
			}
		}
	}
	var keep []fileRow
	refs := map[int64][2]string{}
	for _, r := range list {
		if ref, rel, ok := refFor(scopes, r.SpaceID, r.Path); ok {
			keep = append(keep, r)
			refs[r.ID] = [2]string{ref, rel}
		}
	}
	out := c.toInfo(u, keep,
		func(r fileRow) (string, string) { v := refs[r.ID]; return v[0], v[1] },
		func(r fileRow) bool { return writable[refs[r.ID][0]] })
	for i := range out {
		out[i].SpaceName = names[out[i].Space]
	}
	return out
}

// Starred lists the user's starred items.
func (c *Core) Starred(u *User) ([]FileInfo, error) {
	return c.listByQuery(u, `SELECT `+prefixCols("f")+` FROM stars s JOIN files f ON f.id = s.file_id
		WHERE s.user_id = ? ORDER BY s.created_at DESC LIMIT 500`, u.ID)
}

// Recent lists recently opened or uploaded files.
func (c *Core) Recent(u *User, limit int) ([]FileInfo, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return c.listByQuery(u, `SELECT `+prefixCols("f")+` FROM recent r JOIN files f ON f.id = r.file_id
		WHERE r.user_id = ? ORDER BY r.accessed_at DESC LIMIT ?`, u.ID, limit)
}

func prefixCols(alias string) string {
	parts := strings.Split(fileRowCols, ", ")
	for i, p := range parts {
		parts[i] = alias + "." + p
	}
	return strings.Join(parts, ", ")
}
