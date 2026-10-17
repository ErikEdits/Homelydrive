package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/erikedits/homelydrive/internal/server/fsname"
	"github.com/erikedits/homelydrive/internal/server/storage"
	"github.com/erikedits/homelydrive/internal/server/textextract"
	"github.com/erikedits/homelydrive/internal/server/thumbs"
)

// fileRow is a row of the files index.
type fileRow struct {
	ID       int64
	SpaceID  int64
	Path     string
	Parent   string
	Name     string
	IsDir    bool
	Size     int64
	MTime    int64
	SHA256   string
	Mime     string
	Category string
	SortTime int64
}

const fileRowCols = `id, space_id, path, parent, name, is_dir, size, mtime, sha256, mime, category, sort_time`

func scanFileRow(row interface{ Scan(...any) error }) (fileRow, error) {
	var r fileRow
	err := row.Scan(&r.ID, &r.SpaceID, &r.Path, &r.Parent, &r.Name, &r.IsDir, &r.Size, &r.MTime, &r.SHA256, &r.Mime, &r.Category, &r.SortTime)
	return r, err
}

func (c *Core) loadChildren(q queryer, spaceID int64, parent string) (map[string]fileRow, error) {
	rows, err := q.Query(`SELECT `+fileRowCols+` FROM files WHERE space_id = ? AND parent = ?`, spaceID, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]fileRow{}
	for rows.Next() {
		r, err := scanFileRow(rows)
		if err != nil {
			return nil, err
		}
		out[r.Name] = r
	}
	return out, rows.Err()
}

type queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
}

// getRow loads the index row of one path (ErrNotFound if not indexed).
func (c *Core) getRow(spaceID int64, p string) (fileRow, error) {
	r, err := scanFileRow(c.DB.QueryRow(`SELECT `+fileRowCols+` FROM files WHERE space_id = ? AND path = ?`, spaceID, p))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// deleteSubtree removes the index rows of p and everything below it.
func deleteSubtree(q queryer, spaceID int64, p string) error {
	if p == "" {
		_, err := q.Exec(`DELETE FROM files WHERE space_id = ?`, spaceID)
		return err
	}
	_, err := q.Exec(`DELETE FROM files WHERE space_id = ? AND (path = ? OR (path >= ? AND path < ?))`, spaceID, p, p+"/", p+"0")
	return err
}

// insertRow adds an index row for a file or folder found on disk.
func (c *Core) insertRow(q queryer, s *Space, p string, e storage.Entry, sha string) (int64, error) {
	parent, name := fsname.Split(p)
	mimeType, category := "", ""
	indexed := 1
	if !e.IsDir {
		t := c.classify(name, c.physPath(s, p))
		mimeType, category = t.Mime, t.Category
		indexed = 0
	}
	res, err := q.Exec(`INSERT INTO files(space_id, path, parent, name, is_dir, size, mtime, sha256, mime, category, sort_time, indexed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(space_id, path) DO UPDATE SET is_dir = excluded.is_dir, size = excluded.size, mtime = excluded.mtime,
			sha256 = excluded.sha256, mime = excluded.mime, category = excluded.category, sort_time = excluded.sort_time, indexed = excluded.indexed`,
		s.ID, p, parent, name, e.IsDir, e.Size, e.MTime, sha, mimeType, category, e.MTime, indexed, now())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		q.QueryRow(`SELECT id FROM files WHERE space_id = ? AND path = ?`, s.ID, p).Scan(&id)
	}
	return id, nil
}

// physPath returns the location on disk of a space path ("" if missing).
func (c *Core) physPath(s *Space, p string) string {
	if _, i, err := s.Union.Stat(p); err == nil {
		return s.Union.Abs(i, p)
	}
	return ""
}

// syncDir reconciles the index of one folder with the disk and returns the
// current rows. This makes listings correct even if files were changed
// directly on the server PC.
func (c *Core) syncDir(s *Space, rel string) (map[string]fileRow, error) {
	entries, err := s.Union.ReadDir(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if rel != "" {
				deleteSubtree(c.DB, s.ID, rel)
			}
			return nil, ErrNotFound
		}
		return nil, err
	}
	rows, err := c.loadChildren(c.DB, s.ID, rel)
	if err != nil {
		return nil, err
	}
	var toInsert []storage.Entry
	type upd struct {
		r fileRow
		e storage.Entry
	}
	var toUpdate []upd
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Name] = true
		r, ok := rows[e.Name]
		switch {
		case !ok:
			toInsert = append(toInsert, e)
		case r.IsDir != e.IsDir:
			toInsert = append(toInsert, e) // upsert replaces it
		case !e.IsDir && (r.Size != e.Size || r.MTime != e.MTime):
			toUpdate = append(toUpdate, upd{r, e})
		}
	}
	var stale []fileRow
	for name, r := range rows {
		if !seen[name] {
			stale = append(stale, r)
		}
	}
	if len(toInsert) == 0 && len(toUpdate) == 0 && len(stale) == 0 {
		return rows, nil
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, r := range stale {
		if err := deleteSubtree(tx, s.ID, r.Path); err != nil {
			return nil, err
		}
	}
	for _, e := range toInsert {
		p := fsname.Join(rel, e.Name)
		if r, ok := rows[e.Name]; ok && r.IsDir != e.IsDir {
			deleteSubtree(tx, s.ID, p)
		}
		if _, err := c.insertRow(tx, s, p, e, ""); err != nil {
			return nil, err
		}
	}
	for _, u := range toUpdate {
		t := c.classify(u.r.Name, c.physPath(s, u.r.Path))
		if _, err := tx.Exec(`UPDATE files SET size = ?, mtime = ?, sha256 = '', indexed = 0, mime = ?, category = ?, sort_time = ? WHERE id = ?`,
			u.e.Size, u.e.MTime, t.Mime, t.Category, u.e.MTime, u.r.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if len(toInsert) > 0 || len(toUpdate) > 0 {
		c.kickIndexer()
	}
	if len(stale) > 0 || len(toInsert) > 0 {
		c.cleanupDanglingRefs(s.ID)
	}
	return c.loadChildren(c.DB, s.ID, rel)
}

// cleanupDanglingRefs removes shares and links whose target vanished.
func (c *Core) cleanupDanglingRefs(spaceID int64) {
	c.DB.Exec(`DELETE FROM shares WHERE space_id = ? AND path NOT IN (SELECT path FROM files WHERE space_id = ?)`, spaceID, spaceID)
	c.DB.Exec(`DELETE FROM links WHERE space_id = ? AND path <> '' AND path NOT IN (SELECT path FROM files WHERE space_id = ?)`, spaceID, spaceID)
}

// scanSpace indexes a whole space and (re)registers file watches.
func (c *Core) scanSpace(s *Space) {
	start := time.Now()
	stack := []string{""}
	n := 0
	for len(stack) > 0 {
		rel := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rows, err := c.syncDir(s, rel)
		if err != nil {
			if rel == "" && errors.Is(err, ErrNotFound) {
				return
			}
			continue
		}
		c.watchDir(s, rel)
		for _, r := range rows {
			n++
			if r.IsDir {
				stack = append(stack, r.Path)
			}
		}
	}
	c.Log.Debug("space scanned", "space", s.Name, "kind", s.Kind, "entries", n, "took", time.Since(start).Round(time.Millisecond))
}

// scanSubtree re-indexes a folder and everything below it.
func (c *Core) scanSubtree(s *Space, rel string) {
	stack := []string{rel}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rows, err := c.syncDir(s, p)
		if err != nil {
			continue
		}
		c.watchDir(s, p)
		for _, r := range rows {
			if r.IsDir {
				stack = append(stack, r.Path)
			}
		}
	}
}

// ScanAll re-indexes every space.
func (c *Core) ScanAll() {
	for _, s := range c.AllSpaces() {
		c.scanSpace(s)
	}
}

func (c *Core) scanLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Second):
	}
	for {
		c.ScanAll()
		interval := time.Duration(c.Cfg.Get().ScanIntervalMinutes) * time.Minute
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (c *Core) kickIndexer() {
	select {
	case c.indexKick <- struct{}{}:
	default:
	}
}

// indexWorker computes checksums, photo dates and search text in the background.
func (c *Core) indexWorker(ctx context.Context) {
	for {
		rows, err := c.DB.Query(`SELECT ` + fileRowCols + ` FROM files WHERE indexed = 0 AND is_dir = 0 LIMIT 50`)
		var batch []fileRow
		if err == nil {
			for rows.Next() {
				if r, err := scanFileRow(rows); err == nil {
					batch = append(batch, r)
				}
			}
			rows.Close()
		}
		if len(batch) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-c.indexKick:
			case <-time.After(30 * time.Second):
			}
			continue
		}
		for _, r := range batch {
			if ctx.Err() != nil {
				return
			}
			c.indexFile(ctx, r)
		}
	}
}

func (c *Core) indexFile(ctx context.Context, r fileRow) {
	s, err := c.GetSpace(r.SpaceID)
	if err != nil {
		c.DB.Exec(`UPDATE files SET indexed = 1 WHERE id = ?`, r.ID)
		return
	}
	_, i, err := s.Union.Stat(r.Path)
	if err != nil {
		c.DB.Exec(`UPDATE files SET indexed = 1 WHERE id = ?`, r.ID)
		return
	}
	phys := s.Union.Abs(i, r.Path)
	sha := r.SHA256
	if sha == "" {
		sha, err = hashFile(ctx, phys)
		if err != nil {
			if ctx.Err() == nil {
				c.DB.Exec(`UPDATE files SET indexed = 1 WHERE id = ?`, r.ID)
			}
			return
		}
	}
	sortTime := r.MTime
	if r.Category == "image" {
		if t := thumbs.TakenAt(phys); !t.IsZero() {
			sortTime = t.UnixMilli()
		}
	}
	var text string
	switch r.Category {
	case "text", "pdf", "document":
		text = textextract.Extract(phys, r.Category, fsname.Ext(r.Name))
	}
	// Only mark as indexed if the file did not change meanwhile.
	res, err := c.DB.Exec(`UPDATE files SET sha256 = ?, sort_time = ?, indexed = 1 WHERE id = ? AND size = ? AND mtime = ?`,
		sha, sortTime, r.ID, r.Size, r.MTime)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}
	if text != "" {
		c.DB.Exec(`UPDATE files_fts SET content = ? WHERE rowid = ?`, text, r.ID)
	}
}

func hashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ----- File watching -----

type watchTarget struct {
	spaceID int64
	rel     string
}

type watcher struct {
	mu      sync.Mutex
	fw      *fsnotify.Watcher
	dirs    map[string]watchTarget
	pending map[watchTarget]bool
	deep    map[watchTarget]bool
	timer   *time.Timer
	full    bool
}

func (c *Core) watchLoop(ctx context.Context) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		c.Log.Warn("file watching unavailable, relying on periodic scans", "err", err)
		return
	}
	w := &watcher{fw: fw, dirs: map[string]watchTarget{}, pending: map[watchTarget]bool{}, deep: map[watchTarget]bool{}}
	c.spacesMu.Lock()
	c.watch = w
	c.spacesMu.Unlock()
	defer fw.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-fw.Events:
			if !ok {
				return
			}
			c.onFSEvent(w, ev)
		case err, ok := <-fw.Errors:
			if !ok {
				return
			}
			c.Log.Debug("watch error", "err", err)
		}
	}
}

func (c *Core) getWatcher() *watcher {
	c.spacesMu.Lock()
	defer c.spacesMu.Unlock()
	return c.watch
}

// watchDir registers the physical folders of rel (on every disk) for watching.
func (c *Core) watchDir(s *Space, rel string) {
	w := c.getWatcher()
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.full {
		return
	}
	for i := range s.Union.Roots {
		abs := s.Union.Abs(i, rel)
		if _, ok := w.dirs[abs]; ok {
			continue
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			continue
		}
		if err := w.fw.Add(abs); err != nil {
			w.full = true
			c.Log.Warn("cannot watch more folders, changes made directly on disk are picked up by the periodic scan", "err", err)
			return
		}
		w.dirs[abs] = watchTarget{spaceID: s.ID, rel: rel}
	}
}

func (c *Core) watchSpace(s *Space) { go c.scanSpace(s) }

// rewatchAll drops all watches and rescans (after storage changes).
func (c *Core) rewatchAll() {
	w := c.getWatcher()
	if w != nil {
		w.mu.Lock()
		for d := range w.dirs {
			w.fw.Remove(d)
		}
		w.dirs = map[string]watchTarget{}
		w.full = false
		w.mu.Unlock()
	}
	go c.ScanAll()
}

func (c *Core) onFSEvent(w *watcher, ev fsnotify.Event) {
	base := filepath.Base(ev.Name)
	if strings.HasPrefix(base, fsname.InternalPrefix) || strings.HasSuffix(base, ".hdcopy") {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.dirs[ev.Name]; ok && ev.Has(fsnotify.Remove|fsnotify.Rename) {
		delete(w.dirs, ev.Name)
		w.fw.Remove(ev.Name)
		_ = t
	}
	parent, ok := w.dirs[filepath.Dir(ev.Name)]
	if !ok {
		return
	}
	w.pending[parent] = true
	if ev.Has(fsnotify.Create) {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			w.deep[watchTarget{spaceID: parent.spaceID, rel: fsname.Join(parent.rel, base)}] = true
		}
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(1500*time.Millisecond, func() { c.flushWatch(w) })
	}
}

func (c *Core) flushWatch(w *watcher) {
	w.mu.Lock()
	pending, deep := w.pending, w.deep
	w.pending, w.deep, w.timer = map[watchTarget]bool{}, map[watchTarget]bool{}, nil
	w.mu.Unlock()
	for t := range pending {
		s, err := c.GetSpace(t.spaceID)
		if err != nil {
			continue
		}
		if _, err := c.syncDir(s, t.rel); err == nil {
			c.publishChange(s.ID, t.rel)
		}
	}
	for t := range deep {
		if s, err := c.GetSpace(t.spaceID); err == nil {
			c.scanSubtree(s, t.rel)
		}
	}
}
