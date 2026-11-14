package core

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// DuplicateGroup is a set of files that belong together. The first file is
// the one suggested to keep.
type DuplicateGroup struct {
	Size  int64      `json:"size"` // of each file; 0 when the sizes differ
	Files []FileInfo `json:"files"`
}

// Duplicates is the result of FindDuplicates.
type Duplicates struct {
	// Groups hold files with exactly the same content.
	Groups []DuplicateGroup `json:"groups"`
	// Similar holds files with the same name (apart from "(1)", "Kopie", ...)
	// but different content, e.g. a video downloaded twice in another quality.
	Similar []DuplicateGroup `json:"similar"`
}

// maxSimilar limits the same-name groups, which can be many in big drives.
const maxSimilar = 500

// FindDuplicates finds files with the same content, and files with the same
// name, in the folder rel and all folders below it. Files of equal size whose
// checksum is not known yet are hashed now.
func (c *Core) FindDuplicates(ctx context.Context, a *Access, rel string) (*Duplicates, error) {
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

	scope, args := `space_id = ? AND is_dir = 0 AND size > 0`, []any{a.Space.ID}
	if full != "" {
		scope += ` AND path >= ? AND path < ?`
		args = append(args, full+"/", full+"0")
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT `+fileRowCols+` FROM files WHERE `+scope, args...)
	if err != nil {
		return nil, err
	}
	var all []fileRow
	for rows.Next() {
		if r, err := scanFileRow(rows); err == nil {
			all = append(all, r)
		}
	}
	rows.Close()

	// Only files that share their size with another file can have the same
	// content; those without a checksum yet are hashed now.
	sizes := map[int64]int{}
	for _, r := range all {
		sizes[r.Size]++
	}
	for i := range all {
		r := &all[i]
		if r.SHA256 != "" || sizes[r.Size] < 2 {
			continue
		}
		_, idx, err := a.Space.Union.Stat(r.Path)
		if err != nil {
			continue
		}
		sha, err := hashFile(ctx, a.Space.Union.Abs(idx, r.Path))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		r.SHA256 = sha
		c.DB.Exec(`UPDATE files SET sha256 = ? WHERE id = ? AND size = ? AND mtime = ?`, sha, r.ID, r.Size, r.MTime)
	}

	bySHA := map[string][]fileRow{}
	byName := map[string][]fileRow{}
	for _, r := range all {
		if r.SHA256 != "" && sizes[r.Size] > 1 {
			bySHA[r.SHA256] = append(bySHA[r.SHA256], r)
		}
		key := strings.ToLower(originalName(r.Name))
		byName[key] = append(byName[key], r)
	}

	out := &Duplicates{Groups: []DuplicateGroup{}, Similar: []DuplicateGroup{}}
	for _, g := range bySHA {
		if len(g) > 1 {
			out.Groups = append(out.Groups, c.duplicateGroup(a, g))
		}
	}
	for _, g := range byName {
		if len(g) > 1 && !sameContent(g) {
			out.Similar = append(out.Similar, c.duplicateGroup(a, g))
		}
	}
	sortGroups(out.Groups)
	sortGroups(out.Similar)
	if len(out.Similar) > maxSimilar {
		out.Similar = out.Similar[:maxSimilar]
	}
	return out, nil
}

func (c *Core) duplicateGroup(a *Access, g []fileRow) DuplicateGroup {
	sort.Slice(g, func(i, j int) bool { return keepBefore(g[i], g[j]) })
	size := g[0].Size
	for _, r := range g {
		if r.Size != size {
			size = 0
		}
	}
	return DuplicateGroup{Size: size, Files: c.accessInfo(a, g)}
}

// sameContent reports whether all files of g are known to be identical.
func sameContent(g []fileRow) bool {
	for _, r := range g {
		if r.SHA256 == "" || r.SHA256 != g[0].SHA256 {
			return false
		}
	}
	return true
}

// sortGroups puts the groups with the most space to gain first.
func sortGroups(groups []DuplicateGroup) {
	gain := func(g DuplicateGroup) int64 {
		var n int64
		for _, f := range g.Files[1:] {
			n += f.Size
		}
		return n
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if gi, gj := gain(groups[i]), gain(groups[j]); gi != gj {
			return gi > gj
		}
		return groups[i].Files[0].Path < groups[j].Files[0].Path
	})
}

// Markers of copies: "Bild (1).jpg", "Bild - Kopie.jpg", "Bild_copy.jpg",
// "Kopie von Bild.jpg", "Copy of Bild.jpg".
var (
	copySuffix = regexp.MustCompile(`(?i)(\s*\(\d{1,3}\)|[\s_-]+(kopie|copy)(\s*\(?\d+\)?)?)$`)
	copyPrefix = regexp.MustCompile(`(?i)^(kopie von|copy of)\s+`)
)

// originalName strips the copy markers from a file name.
func originalName(name string) string {
	ext := ""
	if e := strings.LastIndexByte(name, '.'); e > 0 {
		name, ext = name[:e], name[e:]
	}
	return copyPrefix.ReplaceAllString(copySuffix.ReplaceAllString(name, ""), "") + ext
}

func isCopyName(name string) bool { return originalName(name) != name }

// keepBefore orders the files of a group: originals before copies, then the
// oldest, then the shortest path.
func keepBefore(a, b fileRow) bool {
	ca, cb := isCopyName(a.Name), isCopyName(b.Name)
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
