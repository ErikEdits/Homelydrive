// Package webdavfs makes Homelydrive available over WebDAV, so it can be
// mounted as a network drive or used with other apps. The root shows virtual
// folders for the own drive, team folders, mounted folders and shares.
package webdavfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/erikedits/homelydrive/internal/server/core"
)

// Handler authenticates WebDAV requests (HTTP Basic: user name and password
// or a device token) and serves them.
type Handler struct {
	C   *core.Core
	Log *slog.Logger

	mu    sync.Mutex
	locks map[int64]webdav.LockSystem
}

// New creates the WebDAV handler for the /dav/ prefix.
func New(c *core.Core, log *slog.Logger) *Handler {
	return &Handler{C: c, Log: log, locks: map[int64]webdav.LockSystem{}}
}

func (h *Handler) lockSystem(uid int64) webdav.LockSystem {
	h.mu.Lock()
	defer h.mu.Unlock()
	ls, ok := h.locks[uid]
	if !ok {
		ls = webdav.NewMemLS()
		h.locks[uid] = ls
	}
	return ls
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	var u *core.User
	var err error
	if ok {
		u, err = h.C.BasicAuth(user, pass)
	}
	if !ok || err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="Homelydrive", charset="UTF-8"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	dav := &webdav.Handler{
		Prefix:     "/dav",
		FileSystem: &userFS{c: h.C, u: u, names: namesFor(u.Lang)},
		LockSystem: h.lockSystem(u.ID),
		Logger: func(r *http.Request, err error) {
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				h.Log.Debug("webdav", "method", r.Method, "path", r.URL.Path, "err", err)
			}
		},
	}
	dav.ServeHTTP(w, r)
}

type rootNames struct{ home, teams, mounts, shared string }

func namesFor(lang string) rootNames {
	if lang == "en" {
		return rootNames{"My Drive", "Shared drives", "Mounted folders", "Shared with me"}
	}
	return rootNames{"Meine Ablage", "Geteilte Ablagen", "Eingebundene Ordner", "Mit mir geteilt"}
}

type userFS struct {
	c     *core.Core
	u     *core.User
	names rootNames
}

// target is what a WebDAV path resolves to.
type target struct {
	virtual  []fs.FileInfo // non-nil: a virtual folder with these children
	name     string
	access   *core.Access
	rel      string
	readOnly bool
}

func (f *userFS) groups() (map[string]map[string]string, error) {
	roots, err := f.c.ListRoots(f.u)
	if err != nil {
		return nil, err
	}
	g := map[string]map[string]string{f.names.teams: {}, f.names.mounts: {}, f.names.shared: {}}
	for _, r := range roots {
		switch r.Kind {
		case core.KindTeam:
			g[f.names.teams][r.Name] = r.Ref
		case core.KindMount:
			g[f.names.mounts][r.Name] = r.Ref
		case "share":
			name := r.Name
			if r.Owner != "" {
				ext := path.Ext(name)
				if r.IsDir {
					ext = ""
				}
				name = strings.TrimSuffix(name, ext) + " (" + r.Owner + ")" + ext
			}
			g[f.names.shared][name] = r.Ref
		}
	}
	return g, nil
}

func (f *userFS) resolve(name string) (*target, error) {
	name = path.Clean("/" + name)
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if name == "/" {
		g, err := f.groups()
		if err != nil {
			return nil, err
		}
		children := []fs.FileInfo{dirInfo(f.names.home)}
		for _, k := range []string{f.names.teams, f.names.mounts, f.names.shared} {
			if len(g[k]) > 0 {
				children = append(children, dirInfo(k))
			}
		}
		return &target{virtual: children, name: "/"}, nil
	}
	if parts[0] == f.names.home {
		a, err := f.c.ResolveAccess(f.u, core.KindHome)
		if err != nil {
			return nil, err
		}
		return &target{access: a, rel: strings.Join(parts[1:], "/"), name: parts[len(parts)-1]}, nil
	}
	g, err := f.groups()
	if err != nil {
		return nil, err
	}
	group, ok := g[parts[0]]
	if !ok {
		return nil, os.ErrNotExist
	}
	if len(parts) == 1 {
		children := []fs.FileInfo{}
		for n, ref := range group {
			if strings.HasPrefix(ref, "share:") {
				a, err := f.c.ResolveAccess(f.u, ref)
				if err == nil {
					if fi, err := f.c.Stat(a, ""); err == nil && !fi.IsDir {
						children = append(children, fileInfo{name: n, size: fi.Size, mtime: time.UnixMilli(fi.MTime)})
						continue
					}
				}
			}
			children = append(children, dirInfo(n))
		}
		return &target{virtual: children, name: parts[0]}, nil
	}
	ref, ok := group[parts[1]]
	if !ok {
		return nil, os.ErrNotExist
	}
	a, err := f.c.ResolveAccess(f.u, ref)
	if err != nil {
		return nil, err
	}
	return &target{access: a, rel: strings.Join(parts[2:], "/"), name: parts[len(parts)-1], readOnly: !a.CanWrite}, nil
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrNotFound):
		return os.ErrNotExist
	case errors.Is(err, core.ErrForbidden):
		return os.ErrPermission
	case errors.Is(err, core.ErrExists):
		return os.ErrExist
	}
	return err
}

func (f *userFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	t, err := f.resolve(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrPermission
		}
		return err
	}
	if t.virtual != nil || t.rel == "" {
		return os.ErrExist
	}
	parent := path.Dir("/" + t.rel)
	if parent != "/" {
		fi, err := f.c.Stat(t.access, parent)
		if err != nil || !fi.IsDir {
			return os.ErrNotExist
		}
	}
	_, err = f.c.Mkdir(t.access, t.rel)
	return mapErr(err)
}

func (f *userFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, mapErr(err)
	}
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if t.virtual != nil {
		if write {
			return nil, os.ErrPermission
		}
		return &virtualDir{name: t.name, dirBase: dirBase{entries: t.virtual, loaded: true}}, nil
	}
	if write {
		if t.rel == "" || t.readOnly {
			return nil, os.ErrPermission
		}
		existing, statErr := f.c.Stat(t.access, t.rel)
		if statErr == nil && existing.IsDir {
			return nil, os.ErrExist
		}
		if statErr == nil && flag&os.O_EXCL != 0 {
			return nil, os.ErrExist
		}
		if statErr != nil && flag&os.O_CREATE == 0 {
			return nil, os.ErrNotExist
		}
		parent := path.Dir("/" + t.rel)
		if parent != "/" {
			if fi, err := f.c.Stat(t.access, parent); err != nil || !fi.IsDir {
				return nil, os.ErrNotExist
			}
		}
		tmp, idx, err := f.c.TempFor(t.access, t.rel)
		if err != nil {
			return nil, mapErr(err)
		}
		tf, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o640)
		if err != nil {
			return nil, err
		}
		if statErr == nil && flag&os.O_TRUNC == 0 {
			// Keep existing content (rare: partial writes).
			if src, err := f.c.Open(t.access, t.rel, false); err == nil {
				io.Copy(tf, src.File)
				src.File.Close()
				tf.Seek(0, io.SeekStart)
			}
		}
		return &writeFile{File: tf, c: f.c, a: t.access, rel: t.rel, idx: idx, name: t.name}, nil
	}
	fi, err := f.c.Stat(t.access, t.rel)
	if err != nil {
		return nil, mapErr(err)
	}
	if fi.IsDir {
		return &realDir{c: f.c, a: t.access, rel: t.rel, info: dirInfoTime(t.name, fi.MTime)}, nil
	}
	of, err := f.c.Open(t.access, t.rel, false)
	if err != nil {
		return nil, mapErr(err)
	}
	return &readFile{File: of.File, info: fileInfo{name: t.name, size: of.Info.Size(), mtime: of.Info.ModTime()}}, nil
}

func (f *userFS) RemoveAll(ctx context.Context, name string) error {
	t, err := f.resolve(name)
	if err != nil {
		return mapErr(err)
	}
	if t.virtual != nil || t.rel == "" || t.readOnly {
		return os.ErrPermission
	}
	return mapErr(f.c.Delete(t.access, []string{t.rel}))
}

func (f *userFS) Rename(ctx context.Context, oldName, newName string) error {
	src, err := f.resolve(oldName)
	if err != nil {
		return mapErr(err)
	}
	dst, err := f.resolve(newName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return mapErr(err)
	}
	if dst == nil {
		return os.ErrNotExist
	}
	if src.virtual != nil || dst.virtual != nil || src.rel == "" || dst.rel == "" {
		return os.ErrPermission
	}
	srcParent, _ := path.Split(src.rel)
	dstParent, dstName := path.Split(dst.rel)
	if src.access.Ref == dst.access.Ref && srcParent == dstParent {
		_, err := f.c.Rename(src.access, src.rel, dstName)
		return mapErr(err)
	}
	paths, err := f.c.Transfer(src.access, []string{src.rel}, dst.access, strings.TrimSuffix(dstParent, "/"), true)
	if err != nil {
		return mapErr(err)
	}
	if len(paths) == 1 && path.Base(paths[0]) != dstName {
		_, err = f.c.Rename(dst.access, paths[0], dstName)
	}
	return mapErr(err)
}

func (f *userFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, mapErr(err)
	}
	if t.virtual != nil {
		return dirInfo(t.name), nil
	}
	fi, err := f.c.Stat(t.access, t.rel)
	if err != nil {
		return nil, mapErr(err)
	}
	if fi.IsDir {
		return dirInfoTime(t.name, fi.MTime), nil
	}
	return fileInfo{name: t.name, size: fi.Size, mtime: time.UnixMilli(fi.MTime)}, nil
}

// ----- file implementations -----

type fileInfo struct {
	name  string
	size  int64
	mtime time.Time
	dir   bool
}

func (i fileInfo) Name() string { return i.name }
func (i fileInfo) Size() int64  { return i.size }
func (i fileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i fileInfo) ModTime() time.Time { return i.mtime }
func (i fileInfo) IsDir() bool        { return i.dir }
func (i fileInfo) Sys() any           { return nil }

func dirInfo(name string) fileInfo { return fileInfo{name: name, dir: true, mtime: time.Now()} }

func dirInfoTime(name string, ms int64) fileInfo {
	t := time.UnixMilli(ms)
	if ms == 0 {
		t = time.Now()
	}
	return fileInfo{name: name, dir: true, mtime: t}
}

type dirBase struct {
	entries []fs.FileInfo
	pos     int
	loaded  bool
}

func (d *dirBase) readdir(count int, load func() ([]fs.FileInfo, error)) ([]fs.FileInfo, error) {
	if !d.loaded {
		e, err := load()
		if err != nil {
			return nil, err
		}
		d.entries, d.loaded = e, true
	}
	rest := d.entries[d.pos:]
	if count <= 0 {
		d.pos = len(d.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if count > len(rest) {
		count = len(rest)
	}
	d.pos += count
	return rest[:count], nil
}

type virtualDir struct {
	dirBase
	name string
}

func (d *virtualDir) Close() error                   { return nil }
func (d *virtualDir) Read([]byte) (int, error)       { return 0, os.ErrInvalid }
func (d *virtualDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (d *virtualDir) Seek(int64, int) (int64, error) { return 0, nil }
func (d *virtualDir) Stat() (fs.FileInfo, error)     { return dirInfo(d.name), nil }
func (d *virtualDir) Readdir(n int) ([]fs.FileInfo, error) {
	return d.readdir(n, nil)
}

type realDir struct {
	dirBase
	c    *core.Core
	a    *core.Access
	rel  string
	info fileInfo
}

func (d *realDir) Close() error                   { return nil }
func (d *realDir) Read([]byte) (int, error)       { return 0, os.ErrInvalid }
func (d *realDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (d *realDir) Seek(int64, int) (int64, error) { return 0, nil }
func (d *realDir) Stat() (fs.FileInfo, error)     { return d.info, nil }
func (d *realDir) Readdir(n int) ([]fs.FileInfo, error) {
	return d.readdir(n, func() ([]fs.FileInfo, error) {
		list, err := d.c.List(d.a, d.rel)
		if err != nil {
			return nil, mapErr(err)
		}
		out := make([]fs.FileInfo, 0, len(list))
		for _, e := range list {
			fi := fileInfo{name: e.Name, size: e.Size, mtime: time.UnixMilli(e.MTime), dir: e.IsDir}
			if e.IsDir {
				fi.size = 0
			}
			out = append(out, fi)
		}
		return out, nil
	})
}

type readFile struct {
	*os.File
	info fileInfo
}

func (f *readFile) Stat() (fs.FileInfo, error)         { return f.info, nil }
func (f *readFile) Write([]byte) (int, error)          { return 0, os.ErrPermission }
func (f *readFile) Readdir(int) ([]fs.FileInfo, error) { return nil, os.ErrInvalid }

type writeFile struct {
	*os.File
	c      *core.Core
	a      *core.Access
	rel    string
	idx    int
	name   string
	closed bool
}

func (f *writeFile) Stat() (fs.FileInfo, error) {
	fi, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return fileInfo{name: f.name, size: fi.Size(), mtime: fi.ModTime()}, nil
}

func (f *writeFile) Readdir(int) ([]fs.FileInfo, error) { return nil, os.ErrInvalid }

func (f *writeFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	tmp := f.File.Name()
	if err := f.File.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return mapErr(f.c.PutTemp(f.a, f.rel, tmp, f.idx))
}
