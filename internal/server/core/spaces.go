package core

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/erikedits/homelydrive/internal/server/fsname"
	"github.com/erikedits/homelydrive/internal/server/storage"
)

// Space kinds.
const (
	KindHome  = "home"
	KindTeam  = "team"
	KindMount = "mount"
)

// Space is a top-level file area: a user's own drive, a team folder or a
// mounted existing folder.
type Space struct {
	ID       int64
	Kind     string
	Name     string
	OwnerID  int64
	RootPath string
	Union    *storage.Union
}

func (c *Core) buildUnion(kind, name, rootPath string) *storage.Union {
	u := &storage.Union{}
	if kind == KindMount {
		u.Roots = []storage.Root{{
			Dir:      rootPath,
			TrashDir: filepath.Join(rootPath, fsname.InternalPrefix+"trash"),
			TempDir:  filepath.Join(rootPath, fsname.InternalPrefix+"uploads"),
		}}
		return u
	}
	sub := "users"
	if kind == KindTeam {
		sub = "teams"
	}
	for _, loc := range c.Cfg.Get().StorageLocations {
		u.Roots = append(u.Roots, storage.Root{
			Dir:      filepath.Join(loc, sub, name),
			TrashDir: filepath.Join(loc, ".trash"),
			TempDir:  filepath.Join(loc, ".uploads"),
		})
	}
	return u
}

// forgetSpaces drops the cached unions (after storage location changes).
func (c *Core) forgetSpaces() {
	c.spacesMu.Lock()
	c.spaces = map[int64]*Space{}
	c.spacesMu.Unlock()
}

// GetSpace loads a space by id.
func (c *Core) GetSpace(id int64) (*Space, error) {
	c.spacesMu.Lock()
	if s, ok := c.spaces[id]; ok {
		c.spacesMu.Unlock()
		return s, nil
	}
	c.spacesMu.Unlock()
	s := &Space{}
	var owner sql.NullInt64
	err := c.DB.QueryRow(`SELECT id, kind, name, owner_id, root_path FROM spaces WHERE id = ?`, id).
		Scan(&s.ID, &s.Kind, &s.Name, &owner, &s.RootPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.OwnerID = owner.Int64
	s.Union = c.buildUnion(s.Kind, s.Name, s.RootPath)
	c.spacesMu.Lock()
	c.spaces[id] = s
	c.spacesMu.Unlock()
	return s, nil
}

// HomeSpace returns the own drive of a user.
func (c *Core) HomeSpace(userID int64) (*Space, error) {
	var id int64
	if err := c.DB.QueryRow(`SELECT id FROM spaces WHERE kind = 'home' AND owner_id = ?`, userID).Scan(&id); err != nil {
		return nil, ErrNotFound
	}
	return c.GetSpace(id)
}

// AllSpaces returns every space (for scanning and watching).
func (c *Core) AllSpaces() []*Space {
	rows, err := c.DB.Query(`SELECT id FROM spaces`)
	if err != nil {
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []*Space
	for _, id := range ids {
		if s, err := c.GetSpace(id); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// Access is what a user may do inside a space reference.
type Access struct {
	Ref      string // "home", "team:3", "mount:2", "share:7"
	Space    *Space
	Base     string // path inside the space this access is limited to
	CanWrite bool
	ShareID  int64
	Label    string
	User     *User
}

// Full converts a path relative to the access root into a space path.
func (a *Access) Full(rel string) string { return fsname.Join(a.Base, rel) }

// Rel converts a space path into a path relative to the access root.
func (a *Access) Rel(full string) string {
	if a.Base == "" {
		return full
	}
	if full == a.Base {
		return ""
	}
	return strings.TrimPrefix(full, a.Base+"/")
}

// CanShare reports whether the user may share items from this access.
func (a *Access) CanShare() bool { return a.ShareID == 0 && a.CanWrite }

// ResolveAccess parses a space reference for a user and checks permissions.
func (c *Core) ResolveAccess(u *User, ref string) (*Access, error) {
	kind, idStr, _ := strings.Cut(ref, ":")
	switch kind {
	case "", KindHome:
		s, err := c.HomeSpace(u.ID)
		if err != nil {
			return nil, err
		}
		return &Access{Ref: KindHome, Space: s, CanWrite: true, User: u}, nil
	case KindTeam, KindMount:
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil, ErrNotFound
		}
		s, err := c.GetSpace(id)
		if err != nil || s.Kind != kind {
			return nil, ErrNotFound
		}
		var canWrite bool
		if err := c.DB.QueryRow(`SELECT can_write FROM space_members WHERE space_id = ? AND user_id = ?`, id, u.ID).Scan(&canWrite); err != nil {
			return nil, ErrNotFound
		}
		return &Access{Ref: ref, Space: s, CanWrite: canWrite, Label: s.Name, User: u}, nil
	case "share":
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil, ErrNotFound
		}
		var spaceID int64
		var p string
		var canWrite bool
		err = c.DB.QueryRow(`SELECT space_id, path, can_write FROM shares WHERE id = ? AND target_user_id = ?`, id, u.ID).
			Scan(&spaceID, &p, &canWrite)
		if err != nil {
			return nil, ErrNotFound
		}
		s, err := c.GetSpace(spaceID)
		if err != nil {
			return nil, err
		}
		_, name := fsname.Split(p)
		return &Access{Ref: ref, Space: s, Base: p, CanWrite: canWrite, ShareID: id, Label: name, User: u}, nil
	}
	return nil, ErrNotFound
}

// SpaceRoot is a top-level entry shown in the sidebar.
type SpaceRoot struct {
	Ref      string `json:"ref"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	CanWrite bool   `json:"can_write"`
	IsDir    bool   `json:"is_dir"`
	Owner    string `json:"owner,omitempty"`
	SpaceID  int64  `json:"space_id"`
	Path     string `json:"path,omitempty"`
}

// ListRoots returns all spaces a user can open: own drive, team folders,
// mounted folders and items shared with them.
func (c *Core) ListRoots(u *User) ([]SpaceRoot, error) {
	home, err := c.HomeSpace(u.ID)
	if err != nil {
		return nil, err
	}
	out := []SpaceRoot{{Ref: KindHome, Kind: KindHome, Name: u.Name(), CanWrite: true, IsDir: true, SpaceID: home.ID}}
	rows, err := c.DB.Query(`SELECT s.id, s.kind, s.name, m.can_write FROM spaces s
		JOIN space_members m ON m.space_id = s.id WHERE m.user_id = ? ORDER BY s.kind DESC, s.name`, u.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r SpaceRoot
		var id int64
		if err := rows.Scan(&id, &r.Kind, &r.Name, &r.CanWrite); err != nil {
			rows.Close()
			return nil, err
		}
		r.Ref = fmt.Sprintf("%s:%d", r.Kind, id)
		r.IsDir, r.SpaceID = true, id
		out = append(out, r)
	}
	rows.Close()
	rows, err = c.DB.Query(`SELECT sh.id, sh.path, sh.is_dir, sh.can_write, sh.space_id, COALESCE(NULLIF(u.display_name, ''), u.username)
		FROM shares sh JOIN users u ON u.id = sh.owner_id WHERE sh.target_user_id = ? ORDER BY sh.created_at DESC`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SpaceRoot
		var id int64
		var p string
		if err := rows.Scan(&id, &p, &r.IsDir, &r.CanWrite, &r.SpaceID, &r.Owner); err != nil {
			return nil, err
		}
		_, r.Name = fsname.Split(p)
		r.Ref, r.Kind = fmt.Sprintf("share:%d", id), "share"
		out = append(out, r)
	}
	return out, rows.Err()
}

// scope is a part of a space a user can see (used by search, gallery, ...).
type scope struct {
	ref      string
	spaceID  int64
	base     string
	canWrite bool
}

// scopes lists everything the user can read.
func (c *Core) scopes(u *User) []scope {
	roots, err := c.ListRoots(u)
	if err != nil {
		return nil
	}
	var out []scope
	for _, r := range roots {
		sc := scope{ref: r.Ref, spaceID: r.SpaceID, canWrite: r.CanWrite}
		if r.Kind == "share" {
			var p string
			c.DB.QueryRow(`SELECT path FROM shares WHERE id = ?`, strings.TrimPrefix(r.Ref, "share:")).Scan(&p)
			sc.base = p
		}
		out = append(out, sc)
	}
	return out
}

// refFor finds how a user reaches a file: returns the space reference and the
// path relative to it.
func refFor(scopes []scope, spaceID int64, p string) (ref, rel string, ok bool) {
	for _, s := range scopes {
		if s.spaceID != spaceID || !fsname.IsWithin(p, s.base) {
			continue
		}
		if s.base == "" {
			return s.ref, p, true
		}
		if p == s.base {
			return s.ref, "", true
		}
		return s.ref, strings.TrimPrefix(p, s.base+"/"), true
	}
	return "", "", false
}

// scopeSQL builds a WHERE fragment restricting rows (table alias a, with
// space_id and path columns) to the given scopes.
func scopeSQL(scopes []scope, fullOnly bool, a string) (string, []any) {
	var parts []string
	var args []any
	for _, s := range scopes {
		if s.base == "" {
			parts = append(parts, a+".space_id = ?")
			args = append(args, s.spaceID)
		} else if !fullOnly {
			parts = append(parts, "("+a+".space_id = ? AND ("+a+".path = ? OR ("+a+".path >= ? AND "+a+".path < ?)))")
			args = append(args, s.spaceID, s.base, s.base+"/", s.base+"0")
		}
	}
	if len(parts) == 0 {
		return "0", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// usersWithAccess returns the ids of users who can see something in a space.
func (c *Core) usersWithAccess(spaceID int64) []int64 {
	rows, err := c.DB.Query(`SELECT owner_id FROM spaces WHERE id = ? AND owner_id IS NOT NULL
		UNION SELECT user_id FROM space_members WHERE space_id = ?
		UNION SELECT target_user_id FROM shares WHERE space_id = ?`, spaceID, spaceID, spaceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

// ----- Teams and mounts (managed in the server window) -----

// Member is a user with access to a team folder or mount.
type Member struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username,omitempty"`
	CanWrite bool   `json:"can_write"`
}

// SharedSpace describes a team folder or mount for the admin UI.
type SharedSpace struct {
	ID       int64    `json:"id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	RootPath string   `json:"root_path,omitempty"`
	Members  []Member `json:"members"`
	Size     int64    `json:"size"`
}

// ListSharedSpaces returns team folders or mounts.
func (c *Core) ListSharedSpaces(kind string) ([]SharedSpace, error) {
	rows, err := c.DB.Query(`SELECT id, name, root_path FROM spaces WHERE kind = ? ORDER BY name`, kind)
	if err != nil {
		return nil, err
	}
	var out []SharedSpace
	for rows.Next() {
		s := SharedSpace{Kind: kind, Members: []Member{}}
		rows.Scan(&s.ID, &s.Name, &s.RootPath)
		out = append(out, s)
	}
	rows.Close()
	for i := range out {
		mr, err := c.DB.Query(`SELECT m.user_id, u.username, m.can_write FROM space_members m JOIN users u ON u.id = m.user_id WHERE m.space_id = ? ORDER BY u.username`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for mr.Next() {
			var m Member
			mr.Scan(&m.UserID, &m.Username, &m.CanWrite)
			out[i].Members = append(out[i].Members, m)
		}
		mr.Close()
		c.DB.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM files WHERE space_id = ? AND is_dir = 0`, out[i].ID).Scan(&out[i].Size)
	}
	return out, nil
}

// CreateSharedSpace creates a team folder (kind team) or mounts an existing
// folder (kind mount, rootPath required).
func (c *Core) CreateSharedSpace(kind, name, rootPath string, members []Member) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || !fsname.ValidName(name) {
		return 0, badRequest("Ungültiger Name.")
	}
	if kind == KindMount {
		rootPath = filepath.Clean(strings.TrimSpace(rootPath))
		fi, err := os.Stat(rootPath)
		if err != nil || !fi.IsDir() {
			return 0, badRequest("Der Ordner %q existiert nicht.", rootPath)
		}
		if !filepath.IsAbs(rootPath) {
			return 0, badRequest("Bitte einen vollständigen Pfad angeben.")
		}
	} else {
		kind, rootPath = KindTeam, ""
	}
	res, err := c.DB.Exec(`INSERT INTO spaces(kind, name, root_path, created_at) VALUES (?, ?, ?, ?)`, kind, name, rootPath, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrExists
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	if err := c.SetMembers(id, members); err != nil {
		return 0, err
	}
	if s, err := c.GetSpace(id); err == nil {
		go c.scanSpace(s)
		c.watchSpace(s)
	}
	return id, nil
}

// SetMembers replaces the member list of a team folder or mount.
func (c *Core) SetMembers(spaceID int64, members []Member) error {
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM space_members WHERE space_id = ?`, spaceID); err != nil {
		return err
	}
	for _, m := range members {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO space_members(space_id, user_id, can_write) VALUES (?, ?, ?)`, spaceID, m.UserID, m.CanWrite); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RenameSharedSpace renames a team folder (its folders on disk too) or mount.
func (c *Core) RenameSharedSpace(id int64, name string) error {
	s, err := c.GetSpace(id)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if !fsname.ValidName(name) {
		return badRequest("Ungültiger Name.")
	}
	if s.Kind == KindTeam {
		c.unwatchTree(s, "")
		defer func() {
			if ns, err := c.GetSpace(id); err == nil {
				c.watchSpace(ns)
			}
		}()
		for _, loc := range c.Cfg.Get().StorageLocations {
			old := filepath.Join(loc, "teams", s.Name)
			if _, err := os.Stat(old); err == nil {
				if err := os.Rename(old, filepath.Join(loc, "teams", name)); err != nil {
					return err
				}
			}
		}
	}
	if _, err := c.DB.Exec(`UPDATE spaces SET name = ? WHERE id = ?`, name, id); err != nil {
		return err
	}
	c.forgetSpaces()
	return nil
}

// DeleteSharedSpace removes a team folder or mount. Files stay on disk unless
// deleteFiles is set (never for mounts).
func (c *Core) DeleteSharedSpace(id int64, deleteFiles bool) error {
	s, err := c.GetSpace(id)
	if err != nil {
		return err
	}
	if s.Kind == KindHome {
		return ErrForbidden
	}
	c.unwatchTree(s, "")
	if deleteFiles && s.Kind == KindTeam {
		for _, r := range s.Union.Roots {
			removeAllQuiet(r.Dir)
		}
	}
	if _, err := c.DB.Exec(`DELETE FROM spaces WHERE id = ?`, id); err != nil {
		return err
	}
	c.forgetSpaces()
	return nil
}

func removeAllQuiet(p string) {
	if p == "" || p == "/" || filepath.Dir(p) == p {
		return
	}
	os.RemoveAll(p)
}

// ----- Storage locations -----

// Location describes a storage location (hard disk folder).
type Location struct {
	Path  string `json:"path"`
	Free  uint64 `json:"free"`
	Total uint64 `json:"total"`
	Error string `json:"error,omitempty"`
}

// Locations lists the storage locations with their free space.
func (c *Core) Locations() []Location {
	var out []Location
	for _, p := range c.Cfg.Get().StorageLocations {
		l := Location{Path: p}
		os.MkdirAll(p, 0o755)
		free, total, err := storage.DiskUsage(p)
		if err != nil {
			l.Error = err.Error()
		}
		l.Free, l.Total = free, total
		out = append(out, l)
	}
	return out
}

// AddLocation adds a storage location.
func (c *Core) AddLocation(p string) error {
	p = filepath.Clean(strings.TrimSpace(p))
	if !filepath.IsAbs(p) {
		return badRequest("Bitte einen vollständigen Pfad angeben.")
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return badRequest("Ordner kann nicht angelegt werden: %v", err)
	}
	for _, existing := range c.Cfg.Get().StorageLocations {
		if strings.EqualFold(filepath.Clean(existing), p) {
			return ErrExists
		}
	}
	if err := c.Cfg.Update(func(cfg *configT) { cfg.StorageLocations = append(cfg.StorageLocations, p) }); err != nil {
		return err
	}
	c.forgetSpaces()
	c.rewatchAll()
	return nil
}

// RemoveLocation removes an empty storage location.
func (c *Core) RemoveLocation(p string) error {
	locs := c.Cfg.Get().StorageLocations
	if len(locs) <= 1 {
		return badRequest("Der letzte Speicherort kann nicht entfernt werden.")
	}
	idx := -1
	for i, l := range locs {
		if l == p {
			idx = i
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	for _, sub := range []string{"users", "teams"} {
		if hasFiles(filepath.Join(p, sub)) {
			return badRequest("Auf diesem Speicherort liegen noch Dateien. Bitte zuerst verschieben.")
		}
	}
	if err := c.Cfg.Update(func(cfg *configT) {
		cfg.StorageLocations = append(cfg.StorageLocations[:idx:idx], cfg.StorageLocations[idx+1:]...)
	}); err != nil {
		return err
	}
	c.forgetSpaces()
	c.rewatchAll()
	return nil
}

func hasFiles(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
