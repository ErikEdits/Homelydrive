package core

import (
	"strconv"
	"strings"
	"unicode"
)

// SearchQuery holds the search text and filters ("Suchfilter").
type SearchQuery struct {
	Q        string `json:"q"`
	Category string `json:"type"`     // image, video, audio, pdf, text, document, archive, app, other, folder
	From     int64  `json:"from"`     // modified after (unix ms)
	To       int64  `json:"to"`       // modified before (unix ms)
	MinSize  int64  `json:"min_size"` // bytes
	MaxSize  int64  `json:"max_size"`
	Owner    string `json:"owner"` // "", "me", "others"
	Tag      string `json:"tag"`
	Space    string `json:"space"` // limit to one space reference
	Limit    int    `json:"limit"`
}

// ftsQuery turns user input into an FTS5 prefix query.
func ftsQuery(q string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"'
	}) {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		terms = append(terms, `"`+w+`"*`)
	}
	return strings.Join(terms, " ")
}

// Search searches file names and contents in everything the user can see.
func (c *Core) Search(u *User, sq SearchQuery) ([]FileInfo, error) {
	limit := sq.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	scopes := c.scopes(u)
	if sq.Space != "" {
		var only []scope
		for _, s := range scopes {
			if s.ref == sq.Space {
				only = append(only, s)
			}
		}
		scopes = only
	}
	where, args := scopeSQL(scopes, false, "f")
	conds := []string{where}
	join := ""
	order := "f.mtime DESC"
	if fq := ftsQuery(sq.Q); fq != "" {
		join = " JOIN files_fts ON files_fts.rowid = f.id"
		conds = append(conds, "files_fts MATCH ?")
		args = append(args, fq)
		order = "bm25(files_fts, 10.0, 1.0)"
	}
	switch sq.Category {
	case "":
	case "folder":
		conds = append(conds, "f.is_dir = 1")
	default:
		conds = append(conds, "f.is_dir = 0 AND f.category = ?")
		args = append(args, sq.Category)
	}
	if sq.From > 0 {
		conds = append(conds, "f.mtime >= ?")
		args = append(args, sq.From)
	}
	if sq.To > 0 {
		conds = append(conds, "f.mtime <= ?")
		args = append(args, sq.To)
	}
	if sq.MinSize > 0 {
		conds = append(conds, "f.is_dir = 0 AND f.size >= ?")
		args = append(args, sq.MinSize)
	}
	if sq.MaxSize > 0 {
		conds = append(conds, "f.is_dir = 0 AND f.size <= ?")
		args = append(args, sq.MaxSize)
	}
	switch sq.Owner {
	case "me":
		conds = append(conds, "f.space_id IN (SELECT id FROM spaces WHERE kind = 'home' AND owner_id = ?)")
		args = append(args, u.ID)
	case "others":
		conds = append(conds, "f.space_id NOT IN (SELECT id FROM spaces WHERE kind = 'home' AND owner_id = ?)")
		args = append(args, u.ID)
	}
	if sq.Tag != "" {
		conds = append(conds, "f.id IN (SELECT ft.file_id FROM file_tags ft JOIN tags t ON t.id = ft.tag_id WHERE t.user_id = ? AND t.name = ?)")
		args = append(args, u.ID, sq.Tag)
	}
	if len(conds) == 1 && sq.Q == "" {
		return []FileInfo{}, nil // nothing to search for
	}
	q := `SELECT ` + prefixCols("f") + ` FROM files f` + join + ` WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY ` + order + ` LIMIT ?`
	args = append(args, limit)
	return c.listByQuery(u, q, args...)
}

// GalleryPage is a page of the photo timeline.
type GalleryPage struct {
	Items  []FileInfo `json:"items"`
	Cursor string     `json:"cursor,omitempty"`
}

// Gallery returns photos and videos sorted by capture date, newest first.
func (c *Core) Gallery(u *User, spaceRef, cursor string, limit int) (*GalleryPage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	scopes := c.scopes(u)
	if spaceRef != "" {
		var only []scope
		for _, s := range scopes {
			if s.ref == spaceRef {
				only = append(only, s)
			}
		}
		scopes = only
	}
	where, args := scopeSQL(scopes, false, "f")
	q := `SELECT ` + prefixCols("f") + ` FROM files f WHERE f.is_dir = 0 AND f.category IN ('image', 'video') AND ` + where
	if t, id, ok := parseCursor(cursor); ok {
		q += ` AND (f.sort_time < ? OR (f.sort_time = ? AND f.id < ?))`
		args = append(args, t, t, id)
	}
	q += ` ORDER BY f.sort_time DESC, f.id DESC LIMIT ?`
	args = append(args, limit)
	items, err := c.listByQuery(u, q, args...)
	if err != nil {
		return nil, err
	}
	page := &GalleryPage{Items: items}
	if len(items) == limit {
		last := items[len(items)-1]
		var st int64
		c.DB.QueryRow(`SELECT sort_time FROM files WHERE id = ?`, last.ID).Scan(&st)
		page.Cursor = strconv.FormatInt(st, 10) + ":" + strconv.FormatInt(last.ID, 10)
	}
	return page, nil
}

func parseCursor(s string) (int64, int64, bool) {
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, false
	}
	t, err1 := strconv.ParseInt(a, 10, 64)
	id, err2 := strconv.ParseInt(b, 10, 64)
	return t, id, err1 == nil && err2 == nil
}

// StorageStats is the "Speicher" view of a user.
type StorageStats struct {
	Used       int64            `json:"used"`
	Quota      int64            `json:"quota"`
	Percent    int              `json:"percent"`
	Files      int64            `json:"files"`
	Trash      int64            `json:"trash"`
	Vault      int64            `json:"vault"`
	ByCategory map[string]int64 `json:"by_category"`
	Largest    []FileInfo       `json:"largest"`
	DiskFree   uint64           `json:"disk_free"`
	DiskTotal  uint64           `json:"disk_total"`
}

// Storage returns usage statistics and the largest files of a user.
func (c *Core) Storage(u *User) (*StorageStats, error) {
	home, err := c.HomeSpace(u.ID)
	if err != nil {
		return nil, err
	}
	st := &StorageStats{Quota: u.QuotaBytes, ByCategory: map[string]int64{}}
	st.Used = c.Usage(u.ID)
	c.DB.QueryRow(`SELECT COUNT(*) FROM files WHERE space_id = ? AND is_dir = 0`, home.ID).Scan(&st.Files)
	c.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM trash WHERE space_id = ?`, home.ID).Scan(&st.Trash)
	c.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM vault_items WHERE user_id = ?`, u.ID).Scan(&st.Vault)
	rows, err := c.DB.Query(`SELECT category, SUM(size) FROM files WHERE space_id = ? AND is_dir = 0 GROUP BY category`, home.ID)
	if err == nil {
		for rows.Next() {
			var cat string
			var n int64
			rows.Scan(&cat, &n)
			if cat == "" {
				cat = "other"
			}
			st.ByCategory[cat] += n
		}
		rows.Close()
	}
	if st.Quota > 0 {
		st.Percent = int(st.Used * 100 / st.Quota)
	}
	st.Largest, err = c.listByQuery(u, `SELECT `+prefixCols("f")+` FROM files f WHERE f.space_id = ? AND f.is_dir = 0 ORDER BY f.size DESC LIMIT 50`, home.ID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, l := range c.Locations() {
		if seen[l.Path] {
			continue
		}
		seen[l.Path] = true
		st.DiskFree += l.Free
		st.DiskTotal += l.Total
	}
	return st, nil
}
