package core

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// DuplicateGroup is a set of files with exactly the same content. The first
// file is the one suggested to keep.
type DuplicateGroup struct {
	Size  int64      `json:"size"`
	Files []FileInfo `json:"files"`
}

// FindDuplicates finds files with the same content in the folder rel and all
// folders below it. Files of equal size whose checksum is not known yet are
// hashed now.
func (c *Core) FindDuplicates(ctx context.Context, a *Access, rel string) ([]DuplicateGroup, error) {
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
	c.scanSubtree(a.Space, full) // includes files put there directly on the server PC

	// Only files that share their size with another file can be duplicates.
	scope, args := `space_id = ? AND is_dir = 0 AND size > 0`, []any{a.Space.ID}
	if full != "" {
		scope += ` AND path >= ? AND path < ?`
		args = append(args, full+"/", full+"0")
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT `+fileRowCols+` FROM files WHERE `+scope+
		` AND size IN (SELECT size FROM files WHERE `+scope+` GROUP BY size HAVING COUNT(*) > 1)`, append(args, args...)...)
	if err != nil {
		return nil, err
	}
	var cands []fileRow
	for rows.Next() {
		if r, err := scanFileRow(rows); err == nil {
			cands = append(cands, r)
		}
	}
	rows.Close()

	bySHA := map[string][]fileRow{}
	for _, r := range cands {
		if r.SHA256 == "" {
			_, i, err := a.Space.Union.Stat(r.Path)
			if err != nil {
				continue
			}
			sha, err := hashFile(ctx, a.Space.Union.Abs(i, r.Path))
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			r.SHA256 = sha
			c.DB.Exec(`UPDATE files SET sha256 = ? WHERE id = ? AND size = ? AND mtime = ?`, sha, r.ID, r.Size, r.MTime)
		}
		bySHA[r.SHA256] = append(bySHA[r.SHA256], r)
	}

	groups := []DuplicateGroup{}
	for _, g := range bySHA {
		if len(g) < 2 {
			continue
		}
		sort.Slice(g, func(i, j int) bool { return keepBefore(g[i], g[j]) })
		groups = append(groups, DuplicateGroup{Size: g[0].Size, Files: c.accessInfo(a, g)})
	}
	// Most space to gain first.
	sort.Slice(groups, func(i, j int) bool {
		wi := groups[i].Size * int64(len(groups[i].Files)-1)
		wj := groups[j].Size * int64(len(groups[j].Files)-1)
		if wi != wj {
			return wi > wj
		}
		return groups[i].Files[0].Path < groups[j].Files[0].Path
	})
	return groups, nil
}

// copyName matches names of copies: "Bild (1).jpg", "Bild - Kopie.jpg",
// "Bild_copy.jpg", "Kopie von Bild.jpg", "Copy of Bild.jpg".
var copyName = regexp.MustCompile(`(?i)(\s*\(\d{1,3}\)|[\s_-]+(kopie|copy)(\s*\(?\d+\)?)?)(\.[^.]*)?$|^(kopie von|copy of)\s`)

// keepBefore orders the files of a group: originals before copies, then the
// oldest, then the shortest path.
func keepBefore(a, b fileRow) bool {
	ca, cb := copyName.MatchString(a.Name), copyName.MatchString(b.Name)
	if ca != cb {
		return !ca
	}
	if a.MTime != b.MTime {
		return a.MTime < b.MTime
	}
	if da, db := strings.Count(a.Path, "/"), strings.Count(b.Path, "/"); da != db {
		return da < db
	}
	return a.Path < b.Path
}
