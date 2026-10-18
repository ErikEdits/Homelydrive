//go:build windows || cgo

// Package drive shows the server as a virtual drive (Windows: a drive letter
// via WinFsp, Linux: a folder via FUSE). Files are only downloaded when they
// are opened; saving a file uploads it. The layout matches WebDAV: own drive,
// shared drives, mounted folders and items shared with the user.
package drive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/winfsp/cgofuse/fuse"

	"github.com/erikedits/homelydrive/internal/client/remote"
)

// Supported reports whether this build can mount a drive.
const Supported = true

// Names of the top level folders.
type Names struct{ Home, Teams, Mounts, Shared string }

// NamesFor returns the folder names for a UI language.
func NamesFor(lang string) Names {
	if lang == "en" {
		return Names{"My Drive", "Shared drives", "Mounted folders", "Shared with me"}
	}
	return Names{"Meine Ablage", "Geteilte Ablagen", "Eingebundene Ordner", "Mit mir geteilt"}
}

// Uploader hands a finished local file to the upload engine.
type Uploader func(local, space, remotePath string, done func(error))

// Drive is the FUSE file system.
type Drive struct {
	fuse.FileSystemBase

	Client   func() *remote.Client
	Upload   Uploader
	IsJunk   func(name string) bool
	Log      *slog.Logger
	names    Names
	cache    *blockCache
	tmpDir   string
	uid, gid uint32

	mu      sync.Mutex
	roots   []remote.Root
	rootsAt time.Time
	dirs    map[string]*dirCache
	handles map[uint64]*handle
	nextFH  uint64
	pending map[string]*pendingFile // files being written (drive path)

	host    *fuse.FileSystemHost
	mounted chan bool
	mount   string
}

type dirCache struct {
	entries map[string]remote.FileInfo
	at      time.Time
}

type handle struct {
	path     string
	space    string
	rpath    string
	size     int64
	mtime    int64
	write    bool
	tmp      *os.File
	dirty    bool
	readOnly bool
}

type pendingFile struct {
	tmp       string
	space     string
	rpath     string
	open      int
	timer     *time.Timer
	changed   bool // written by a handle that is closed already
	submitted bool
}

// New creates the drive. cacheDir holds downloaded blocks (limited to cacheMB).
func New(client func() *remote.Client, upload Uploader, lang, cacheDir string, cacheMB int, log *slog.Logger) *Drive {
	if cacheMB <= 0 {
		cacheMB = 2048
	}
	d := &Drive{
		Client:  client,
		Upload:  upload,
		Log:     log,
		names:   NamesFor(lang),
		cache:   newBlockCache(filepath.Join(cacheDir, "blocks"), int64(cacheMB)<<20),
		tmpDir:  filepath.Join(cacheDir, "write"),
		dirs:    map[string]*dirCache{},
		handles: map[uint64]*handle{},
		pending: map[string]*pendingFile{},
		IsJunk:  func(string) bool { return false },
	}
	os.MkdirAll(d.tmpDir, 0o700)
	d.uid, d.gid = currentIDs()
	return d
}

func ctxShort() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

// ----- mounting -----

// Mount mounts the drive at mountpoint ("H:" on Windows, a folder on Linux).
func (d *Drive) Mount(mountpoint string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("virtuelles Laufwerk nicht verfügbar (WinFsp/FUSE installiert?)")
		}
	}()
	if runtime.GOOS != "windows" {
		if err := os.MkdirAll(mountpoint, 0o755); err != nil {
			return err
		}
	}
	d.mount = mountpoint
	d.mounted = make(chan bool, 1)
	d.host = fuse.NewFileSystemHost(d)
	d.host.SetCapReaddirPlus(true)
	var opts []string
	if runtime.GOOS == "windows" {
		opts = []string{"-o", "volname=Homelydrive", "-o", "uid=-1", "-o", "gid=-1", "-o", "FileInfoTimeout=2000", "-o", "DirInfoTimeout=2000"}
	} else {
		opts = []string{"-o", "fsname=homelydrive", "-o", "subtype=homelydrive"}
	}
	go func() {
		ok := d.host.Mount(mountpoint, opts)
		if !ok {
			select {
			case d.mounted <- false:
			default:
			}
		}
	}()
	select {
	case ok := <-d.mounted:
		if !ok {
			return errors.New("Laufwerk konnte nicht eingebunden werden (WinFsp/FUSE installiert? Laufwerksbuchstabe frei?)")
		}
	case <-time.After(15 * time.Second):
		return errors.New("Zeitüberschreitung beim Einbinden des Laufwerks")
	}
	d.Log.Info("virtual drive mounted", "at", mountpoint)
	return nil
}

// Unmount detaches the drive.
func (d *Drive) Unmount() {
	if d.host != nil {
		d.host.Unmount()
		d.host = nil
	}
}

// MountPoint returns where the drive is mounted.
func (d *Drive) MountPoint() string { return d.mount }

// CacheSize returns the size of the read cache.
func (d *Drive) CacheSize() int64 { return d.cache.Size() }

// ClearCache empties the read cache.
func (d *Drive) ClearCache() { d.cache.Clear() }

func (d *Drive) Init() {
	select {
	case d.mounted <- true:
	default:
	}
}

// ----- layout -----

type node struct {
	kind     int // 0 root, 1 group, 2 inside a space
	group    string
	children []string
	space    string
	rpath    string
	canWrite bool
}

const (
	kRoot = iota
	kGroup
	kSpace
)

func (d *Drive) getRoots() []remote.Root {
	d.mu.Lock()
	if time.Since(d.rootsAt) < 30*time.Second && d.roots != nil {
		r := d.roots
		d.mu.Unlock()
		return r
	}
	d.mu.Unlock()
	c := d.Client()
	if c == nil {
		return nil
	}
	ctx, cancel := ctxShort()
	defer cancel()
	roots, err := c.Roots(ctx)
	if err != nil {
		d.mu.Lock()
		r := d.roots
		d.mu.Unlock()
		return r
	}
	d.mu.Lock()
	d.roots, d.rootsAt = roots, time.Now()
	d.mu.Unlock()
	return roots
}

func (d *Drive) groupOf(kind string) string {
	switch kind {
	case "team":
		return d.names.Teams
	case "mount":
		return d.names.Mounts
	case "share":
		return d.names.Shared
	}
	return ""
}

// shareName makes share entries unique by adding the owner.
func shareName(r remote.Root) string {
	if r.Kind != "share" || r.Owner == "" {
		return r.Name
	}
	ext := path.Ext(r.Name)
	if r.IsDir {
		ext = ""
	}
	return strings.TrimSuffix(r.Name, ext) + " (" + r.Owner + ")" + ext
}

func (d *Drive) resolve(p string) (*node, bool) {
	p = path.Clean("/" + filepath.ToSlash(p))
	if p == "/" {
		n := &node{kind: kRoot, children: []string{d.names.Home}}
		seen := map[string]bool{}
		for _, r := range d.getRoots() {
			if g := d.groupOf(r.Kind); g != "" && !seen[g] {
				seen[g] = true
				n.children = append(n.children, g)
			}
		}
		return n, true
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if parts[0] == d.names.Home {
		return &node{kind: kSpace, space: "home", rpath: strings.Join(parts[1:], "/"), canWrite: true}, true
	}
	var inGroup []remote.Root
	for _, r := range d.getRoots() {
		if d.groupOf(r.Kind) == parts[0] {
			inGroup = append(inGroup, r)
		}
	}
	if len(inGroup) == 0 {
		return nil, false
	}
	if len(parts) == 1 {
		n := &node{kind: kGroup, group: parts[0]}
		for _, r := range inGroup {
			n.children = append(n.children, shareName(r))
		}
		return n, true
	}
	for _, r := range inGroup {
		if shareName(r) == parts[1] {
			return &node{kind: kSpace, space: r.Ref, rpath: strings.Join(parts[2:], "/"), canWrite: r.CanWrite}, true
		}
	}
	return nil, false
}

// list returns the cached listing of a remote folder.
func (d *Drive) list(space, rpath string) (map[string]remote.FileInfo, error) {
	key := space + "|" + rpath
	d.mu.Lock()
	if dc, ok := d.dirs[key]; ok && time.Since(dc.at) < 15*time.Second {
		d.mu.Unlock()
		return dc.entries, nil
	}
	d.mu.Unlock()
	c := d.Client()
	if c == nil {
		return nil, errors.New("not logged in")
	}
	ctx, cancel := ctxShort()
	defer cancel()
	l, err := c.List(ctx, space, rpath)
	if err != nil {
		return nil, err
	}
	m := make(map[string]remote.FileInfo, len(l.Entries))
	for _, e := range l.Entries {
		m[e.Name] = e
	}
	d.mu.Lock()
	d.dirs[key] = &dirCache{entries: m, at: time.Now()}
	d.mu.Unlock()
	return m, nil
}

// Invalidate forgets cached listings of a space (after a change on the server).
func (d *Drive) Invalidate(space string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k := range d.dirs {
		if space == "" || strings.HasPrefix(k, space+"|") {
			delete(d.dirs, k)
		}
	}
	if space == "" {
		d.rootsAt = time.Time{}
	}
}

// InvalidateSpaceID forgets listings for a server space id.
func (d *Drive) InvalidateSpaceID(id int64) {
	refs := []string{}
	d.mu.Lock()
	for _, r := range d.roots {
		if r.SpaceID == id {
			refs = append(refs, r.Ref)
		}
	}
	d.mu.Unlock()
	if len(refs) == 0 {
		d.Invalidate("")
		return
	}
	for _, r := range refs {
		d.Invalidate(r)
	}
}

func (d *Drive) invalidateDir(space, rpath string) {
	d.mu.Lock()
	delete(d.dirs, space+"|"+rpath)
	d.mu.Unlock()
}

func split(rpath string) (string, string) {
	i := strings.LastIndexByte(rpath, '/')
	if i < 0 {
		return "", rpath
	}
	return rpath[:i], rpath[i+1:]
}

// lookup finds a remote entry.
func (d *Drive) lookup(space, rpath string) (remote.FileInfo, error) {
	parent, name := split(rpath)
	entries, err := d.list(space, parent)
	if err != nil {
		return remote.FileInfo{}, err
	}
	e, ok := entries[name]
	if !ok {
		return remote.FileInfo{}, os.ErrNotExist
	}
	return e, nil
}

// ----- attributes -----

func (d *Drive) fillDir(st *fuse.Stat_t, writable bool, mtime time.Time) {
	*st = fuse.Stat_t{}
	st.Mode = fuse.S_IFDIR | 0o555
	if writable {
		st.Mode = fuse.S_IFDIR | 0o755
	}
	st.Nlink = 2
	st.Uid, st.Gid = d.uid, d.gid
	ts := fuse.NewTimespec(mtime)
	st.Mtim, st.Ctim, st.Atim, st.Birthtim = ts, ts, ts, ts
}

func (d *Drive) fillFile(st *fuse.Stat_t, size int64, writable bool, mtime time.Time) {
	*st = fuse.Stat_t{}
	st.Mode = fuse.S_IFREG | 0o444
	if writable {
		st.Mode = fuse.S_IFREG | 0o644
	}
	st.Nlink = 1
	st.Size = size
	st.Blksize = 4096
	st.Blocks = (size + 511) / 512
	st.Uid, st.Gid = d.uid, d.gid
	ts := fuse.NewTimespec(mtime)
	st.Mtim, st.Ctim, st.Atim, st.Birthtim = ts, ts, ts, ts
}

func errno(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, os.ErrNotExist), remote.StatusOf(err) == 404:
		return -fuse.ENOENT
	case remote.StatusOf(err) == 403:
		return -fuse.EACCES
	case remote.StatusOf(err) == 409:
		return -fuse.EEXIST
	case remote.StatusOf(err) == 507:
		return -fuse.ENOSPC
	}
	return -fuse.EIO
}

func (d *Drive) Getattr(p string, st *fuse.Stat_t, fh uint64) int {
	d.mu.Lock()
	if pf, ok := d.pending[p]; ok {
		tmp := pf.tmp
		d.mu.Unlock()
		if fi, err := os.Stat(tmp); err == nil {
			d.fillFile(st, fi.Size(), true, fi.ModTime())
			return 0
		}
	} else {
		d.mu.Unlock()
	}
	n, ok := d.resolve(p)
	if !ok {
		return -fuse.ENOENT
	}
	if n.kind != kSpace {
		d.fillDir(st, false, time.Now())
		return 0
	}
	if n.rpath == "" {
		d.fillDir(st, n.canWrite, time.Now())
		return 0
	}
	e, err := d.lookup(n.space, n.rpath)
	if err != nil {
		return errno(err)
	}
	if e.IsDir {
		d.fillDir(st, n.canWrite, time.UnixMilli(e.MTime))
	} else {
		d.fillFile(st, e.Size, n.canWrite, time.UnixMilli(e.MTime))
	}
	return 0
}

func (d *Drive) Statfs(p string, st *fuse.Statfs_t) int {
	*st = fuse.Statfs_t{Bsize: 4096, Frsize: 4096, Namemax: 255}
	total, free := uint64(1<<40), uint64(1<<40)
	if c := d.Client(); c != nil {
		ctx, cancel := ctxShort()
		if me, err := c.Me(ctx); err == nil && me.Quota > 0 {
			total = uint64(me.Quota)
			if me.Used < me.Quota {
				free = uint64(me.Quota - me.Used)
			} else {
				free = 0
			}
		}
		cancel()
	}
	st.Blocks = total / 4096
	st.Bfree = free / 4096
	st.Bavail = free / 4096
	st.Files, st.Ffree = 1<<30, 1<<30
	return 0
}

func (d *Drive) Opendir(p string) (int, uint64) {
	n, ok := d.resolve(p)
	if !ok {
		return -fuse.ENOENT, ^uint64(0)
	}
	if n.kind == kSpace && n.rpath != "" {
		e, err := d.lookup(n.space, n.rpath)
		if err != nil {
			return errno(err), ^uint64(0)
		}
		if !e.IsDir {
			return -fuse.ENOTDIR, ^uint64(0)
		}
	}
	return 0, 0
}

func (d *Drive) Readdir(p string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	n, ok := d.resolve(p)
	if !ok {
		return -fuse.ENOENT
	}
	fill(".", nil, 0)
	fill("..", nil, 0)
	if n.kind != kSpace {
		for _, c := range n.children {
			var st fuse.Stat_t
			d.fillDir(&st, false, time.Now())
			if !fill(c, &st, 0) {
				break
			}
		}
		return 0
	}
	entries, err := d.list(n.space, n.rpath)
	if err != nil {
		return errno(err)
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := entries[name]
		var st fuse.Stat_t
		if e.IsDir {
			d.fillDir(&st, n.canWrite, time.UnixMilli(e.MTime))
		} else {
			d.fillFile(&st, e.Size, n.canWrite, time.UnixMilli(e.MTime))
		}
		if !fill(name, &st, 0) {
			return 0
		}
	}
	// Files that are being written but not uploaded yet.
	prefix := strings.TrimSuffix(path.Clean("/"+filepath.ToSlash(p)), "/") + "/"
	d.mu.Lock()
	var extra []string
	for pp := range d.pending {
		if strings.HasPrefix(pp, prefix) && !strings.Contains(pp[len(prefix):], "/") {
			if _, ok := entries[pp[len(prefix):]]; !ok {
				extra = append(extra, pp)
			}
		}
	}
	d.mu.Unlock()
	for _, pp := range extra {
		var st fuse.Stat_t
		if d.Getattr(pp, &st, ^uint64(0)) == 0 {
			fill(pp[len(prefix):], &st, 0)
		}
	}
	return 0
}

// ----- open / read / write -----

func (d *Drive) addHandle(h *handle) uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextFH++
	d.handles[d.nextFH] = h
	return d.nextFH
}

func (d *Drive) getHandle(fh uint64) *handle {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.handles[fh]
}

func (d *Drive) newTemp() (*os.File, error) {
	return os.CreateTemp(d.tmpDir, "w-*")
}

func (d *Drive) Open(p string, flags int) (int, uint64) {
	cp := path.Clean("/" + filepath.ToSlash(p))
	write := flags&fuse.O_ACCMODE != fuse.O_RDONLY
	d.mu.Lock()
	pf, isPending := d.pending[cp]
	if isPending && pf.submitted && write {
		// The last version is being uploaded right now: leave its temp file
		// alone and write the new version into a fresh one (otherwise the
		// finished upload would delete the new content).
		var prev *os.File
		if flags&fuse.O_TRUNC == 0 {
			prev, _ = os.Open(pf.tmp)
		}
		space, rpath := pf.space, pf.rpath
		d.mu.Unlock()
		f, err := d.newTemp()
		if err == nil && prev != nil {
			_, err = io.Copy(f, prev)
		}
		if prev != nil {
			prev.Close()
		}
		if err != nil {
			if f != nil {
				f.Close()
				os.Remove(f.Name())
			}
			return -fuse.EIO, ^uint64(0)
		}
		d.mu.Lock()
		d.pending[cp] = &pendingFile{tmp: f.Name(), space: space, rpath: rpath, open: 1}
		d.mu.Unlock()
		return 0, d.addHandle(&handle{path: cp, space: space, rpath: rpath, write: true, tmp: f, dirty: flags&fuse.O_TRUNC != 0})
	}
	if isPending {
		pf.open++
		if pf.timer != nil {
			pf.timer.Stop()
		}
	}
	d.mu.Unlock()
	if isPending {
		f, err := os.OpenFile(pf.tmp, os.O_RDWR, 0o600)
		if err != nil {
			return -fuse.EIO, ^uint64(0)
		}
		if flags&fuse.O_TRUNC != 0 {
			f.Truncate(0)
		}
		return 0, d.addHandle(&handle{path: cp, space: pf.space, rpath: pf.rpath, write: true, tmp: f, dirty: flags&fuse.O_TRUNC != 0})
	}
	n, ok := d.resolve(cp)
	if !ok || n.kind != kSpace || n.rpath == "" {
		return -fuse.EISDIR, ^uint64(0)
	}
	e, err := d.lookup(n.space, n.rpath)
	if err != nil {
		return errno(err), ^uint64(0)
	}
	if e.IsDir {
		return -fuse.EISDIR, ^uint64(0)
	}
	h := &handle{path: cp, space: n.space, rpath: n.rpath, size: e.Size, mtime: e.MTime}
	if !write {
		return 0, d.addHandle(h)
	}
	if !n.canWrite {
		return -fuse.EACCES, ^uint64(0)
	}
	// Copy on write: fetch the current content into a temporary file.
	f, err := d.newTemp()
	if err != nil {
		return -fuse.EIO, ^uint64(0)
	}
	if flags&fuse.O_TRUNC == 0 && e.Size > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		rc, _, err := d.Client().Download(ctx, n.space, n.rpath, 0, -1)
		if err == nil {
			_, err = io.Copy(f, rc)
			rc.Close()
		}
		cancel()
		if err != nil {
			f.Close()
			os.Remove(f.Name())
			return errno(err), ^uint64(0)
		}
	}
	h.write, h.tmp, h.dirty = true, f, flags&fuse.O_TRUNC != 0
	d.mu.Lock()
	d.pending[cp] = &pendingFile{tmp: f.Name(), space: n.space, rpath: n.rpath, open: 1}
	d.mu.Unlock()
	return 0, d.addHandle(h)
}

func (d *Drive) Create(p string, flags int, mode uint32) (int, uint64) {
	cp := path.Clean("/" + filepath.ToSlash(p))
	n, ok := d.resolve(path.Dir(cp))
	if !ok || n.kind != kSpace {
		return -fuse.EACCES, ^uint64(0)
	}
	if !n.canWrite {
		return -fuse.EACCES, ^uint64(0)
	}
	f, err := d.newTemp()
	if err != nil {
		return -fuse.EIO, ^uint64(0)
	}
	rpath := path.Join(n.rpath, path.Base(cp))
	d.mu.Lock()
	if old, ok := d.pending[cp]; ok && old.timer != nil && !old.submitted {
		old.timer.Stop()
		os.Remove(old.tmp) // an upload in progress keeps its file
	}
	d.pending[cp] = &pendingFile{tmp: f.Name(), space: n.space, rpath: rpath, open: 1}
	d.mu.Unlock()
	return 0, d.addHandle(&handle{path: cp, space: n.space, rpath: rpath, write: true, tmp: f, dirty: true})
}

func (d *Drive) Read(p string, buff []byte, ofst int64, fh uint64) int {
	h := d.getHandle(fh)
	if h == nil {
		return -fuse.EBADF
	}
	if h.tmp != nil {
		n, err := h.tmp.ReadAt(buff, ofst)
		if err != nil && err != io.EOF {
			return -fuse.EIO
		}
		return n
	}
	c := d.Client()
	if c == nil {
		return -fuse.EIO
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	n, err := d.cache.ReadAt(ctx, c, h.space, h.rpath, h.size, h.mtime, buff, ofst)
	if err != nil && err != io.EOF && n == 0 {
		d.Log.Debug("drive read failed", "path", p, "err", err)
		return -fuse.EIO
	}
	return n
}

func (d *Drive) Write(p string, buff []byte, ofst int64, fh uint64) int {
	h := d.getHandle(fh)
	if h == nil || h.tmp == nil {
		return -fuse.EBADF
	}
	n, err := h.tmp.WriteAt(buff, ofst)
	if err != nil {
		return -fuse.EIO
	}
	h.dirty = true
	return n
}

func (d *Drive) Truncate(p string, size int64, fh uint64) int {
	if h := d.getHandle(fh); h != nil && h.tmp != nil {
		if err := h.tmp.Truncate(size); err != nil {
			return -fuse.EIO
		}
		h.dirty = true
		return 0
	}
	cp := path.Clean("/" + filepath.ToSlash(p))
	d.mu.Lock()
	pf, ok := d.pending[cp]
	if ok && !pf.submitted {
		pf.changed = true
	}
	d.mu.Unlock()
	if ok && !pf.submitted {
		if err := os.Truncate(pf.tmp, size); err != nil {
			return -fuse.EIO
		}
		d.scheduleCommit(cp)
		return 0
	}
	// Truncating without an open handle (or while the last version uploads):
	// open a writable copy, truncate, save.
	errc, nfh := d.Open(p, fuse.O_RDWR)
	if errc != 0 {
		return errc
	}
	errc = d.Truncate(p, size, nfh)
	d.Release(p, nfh)
	return errc
}

func (d *Drive) Flush(p string, fh uint64) int {
	if h := d.getHandle(fh); h != nil && h.tmp != nil {
		h.tmp.Sync()
	}
	return 0
}

func (d *Drive) Fsync(p string, datasync bool, fh uint64) int { return d.Flush(p, fh) }

func (d *Drive) Release(p string, fh uint64) int {
	d.mu.Lock()
	h := d.handles[fh]
	delete(d.handles, fh)
	d.mu.Unlock()
	if h == nil || h.tmp == nil {
		return 0
	}
	h.tmp.Close()
	d.mu.Lock()
	pf, ok := d.pending[h.path]
	if ok {
		pf.open--
		if h.dirty {
			pf.changed = true
		}
	}
	d.mu.Unlock()
	if !ok || pf.open > 0 || pf.submitted {
		// Still open elsewhere, or uploading (the upload cleans up when done).
		return 0
	}
	if !pf.changed {
		// Opened for writing but nothing changed.
		d.mu.Lock()
		delete(d.pending, h.path)
		d.mu.Unlock()
		os.Remove(pf.tmp)
		return 0
	}
	d.scheduleCommit(h.path)
	return 0
}

// scheduleCommit uploads a written file shortly after the last close, so
// "save to temp file, then rename" patterns of editors only upload the
// final file. Temporary/lock files are never uploaded.
func (d *Drive) scheduleCommit(p string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	pf, ok := d.pending[p]
	if !ok || pf.open > 0 || pf.submitted {
		return
	}
	if pf.timer != nil {
		pf.timer.Stop()
	}
	pf.timer = time.AfterFunc(1500*time.Millisecond, func() { d.commit(p) })
}

func (d *Drive) commit(p string) {
	d.mu.Lock()
	pf, ok := d.pending[p]
	if !ok || pf.open > 0 || pf.submitted {
		d.mu.Unlock()
		return
	}
	if d.IsJunk(path.Base(p)) {
		d.mu.Unlock()
		return // stays local until deleted
	}
	pf.submitted = true
	d.mu.Unlock()
	d.Upload(pf.tmp, pf.space, pf.rpath, func(err error) {
		d.mu.Lock()
		if cur, ok := d.pending[p]; ok && cur == pf {
			delete(d.pending, p)
		}
		d.mu.Unlock()
		os.Remove(pf.tmp)
		parent, _ := split(pf.rpath)
		d.invalidateDir(pf.space, parent)
		if err != nil {
			d.Log.Warn("drive upload failed", "path", p, "err", err)
		}
	})
}

// ----- changes -----

func (d *Drive) Mkdir(p string, mode uint32) int {
	n, ok := d.resolve(p)
	if !ok || n.kind != kSpace || n.rpath == "" {
		return -fuse.EACCES
	}
	if !n.canWrite {
		return -fuse.EACCES
	}
	ctx, cancel := ctxShort()
	defer cancel()
	if err := d.Client().Mkdir(ctx, n.space, n.rpath); err != nil {
		return errno(err)
	}
	parent, _ := split(n.rpath)
	d.invalidateDir(n.space, parent)
	return 0
}

func (d *Drive) removePending(cp string) bool {
	d.mu.Lock()
	pf, ok := d.pending[cp]
	if ok && !pf.submitted {
		if pf.timer != nil {
			pf.timer.Stop()
		}
		delete(d.pending, cp)
	}
	d.mu.Unlock()
	if ok && !pf.submitted {
		os.Remove(pf.tmp)
		return true
	}
	return false
}

func (d *Drive) Unlink(p string) int {
	cp := path.Clean("/" + filepath.ToSlash(p))
	if d.removePending(cp) {
		// Was never uploaded; also delete a server copy if one exists.
		if n, ok := d.resolve(cp); ok && n.kind == kSpace {
			if _, err := d.lookup(n.space, n.rpath); err != nil {
				return 0
			}
		} else {
			return 0
		}
	}
	return d.remove(cp)
}

func (d *Drive) Rmdir(p string) int { return d.remove(p) }

func (d *Drive) remove(p string) int {
	n, ok := d.resolve(p)
	if !ok || n.kind != kSpace || n.rpath == "" {
		return -fuse.EACCES
	}
	if !n.canWrite {
		return -fuse.EACCES
	}
	ctx, cancel := ctxShort()
	defer cancel()
	if err := d.Client().Delete(ctx, n.space, []string{n.rpath}); err != nil {
		return errno(err)
	}
	parent, _ := split(n.rpath)
	d.invalidateDir(n.space, parent)
	return 0
}

func (d *Drive) Rename(oldp, newp string) int {
	oc := path.Clean("/" + filepath.ToSlash(oldp))
	nc := path.Clean("/" + filepath.ToSlash(newp))
	on, ok1 := d.resolve(oc)
	nn, ok2 := d.resolve(nc)
	if !ok1 || !ok2 || on.kind != kSpace || nn.kind != kSpace || on.rpath == "" || nn.rpath == "" {
		return -fuse.EACCES
	}
	if !on.canWrite || !nn.canWrite {
		return -fuse.EACCES
	}
	// A file that is still being written only changes its target.
	d.mu.Lock()
	if pf, ok := d.pending[oc]; ok && !pf.submitted {
		delete(d.pending, oc)
		if old, ok := d.pending[nc]; ok && old.timer != nil {
			old.timer.Stop()
			os.Remove(old.tmp)
		}
		pf.space, pf.rpath = nn.space, nn.rpath
		d.pending[nc] = pf
		d.mu.Unlock()
		for fh, h := range d.handlesFor(oc) {
			h.path, h.space, h.rpath = nc, nn.space, nn.rpath
			_ = fh
		}
		d.scheduleCommit(nc)
		return 0
	}
	d.mu.Unlock()

	c := d.Client()
	ctx, cancel := ctxShort()
	defer cancel()
	// POSIX rename replaces the target.
	if _, err := d.lookup(nn.space, nn.rpath); err == nil {
		if err := c.Delete(ctx, nn.space, []string{nn.rpath}); err != nil {
			return errno(err)
		}
	}
	oParent, _ := split(on.rpath)
	nParent, nName := split(nn.rpath)
	var err error
	if on.space == nn.space && oParent == nParent {
		err = c.Rename(ctx, on.space, on.rpath, nName)
	} else {
		var paths []string
		paths, err = c.Move(ctx, on.space, []string{on.rpath}, nn.space, nParent)
		if err == nil && len(paths) == 1 && path.Base(paths[0]) != nName {
			err = c.Rename(ctx, nn.space, paths[0], nName)
		}
	}
	if err != nil {
		return errno(err)
	}
	d.invalidateDir(on.space, oParent)
	d.invalidateDir(nn.space, nParent)
	return 0
}

func (d *Drive) handlesFor(p string) map[uint64]*handle {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[uint64]*handle{}
	for fh, h := range d.handles {
		if h.path == p {
			out[fh] = h
		}
	}
	return out
}

func (d *Drive) Utimens(p string, tmsp []fuse.Timespec) int      { return 0 }
func (d *Drive) Chmod(p string, mode uint32) int                 { return 0 }
func (d *Drive) Chown(p string, uid uint32, gid uint32) int      { return 0 }
func (d *Drive) Access(p string, mask uint32) int                { return 0 }
func (d *Drive) Releasedir(p string, fh uint64) int              { return 0 }
func (d *Drive) Fsyncdir(p string, datasync bool, fh uint64) int { return 0 }

// Locate maps a path relative to the mount point to the server.
func (d *Drive) Locate(rel string) (space, rpath string, ok bool) {
	n, found := d.resolve(rel)
	if !found || n.kind != kSpace {
		return "", "", false
	}
	return n.space, n.rpath, true
}
