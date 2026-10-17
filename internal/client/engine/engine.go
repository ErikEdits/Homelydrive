// Package engine uploads the chosen folders of the PC to the server (upload
// only: deleting a file on the PC keeps it on the server). New and changed
// files are found by watching the folders and by periodic rescans. Uploads
// are resumable and share one adjustable bandwidth limit.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/time/rate"

	"github.com/erikedits/homelydrive/internal/client/config"
	"github.com/erikedits/homelydrive/internal/client/remote"
	"github.com/erikedits/homelydrive/internal/client/state"
)

const workers = 2

// Job is one file to upload.
type Job struct {
	Key       string
	FolderID  string // set for watched folders
	Abs       string
	Rel       string
	Space     string
	Remote    string // target path on the server (relative to Space)
	Manual    bool
	Conflict  string // overrides the conflict policy ("overwrite" for drive saves)
	notBefore time.Time
	attempts  int
	onDone    []func(remotePath string, err error)
}

// Active describes an upload in progress (for the window and tray).
type Active struct {
	Name   string  `json:"name"`
	Local  string  `json:"local"`
	Remote string  `json:"remote"`
	Size   int64   `json:"size"`
	Done   int64   `json:"done"`
	Speed  float64 `json:"speed"`
	Phase  string  `json:"phase"` // hashing, uploading

	done    atomic.Int64
	started time.Time
}

// Status is a snapshot for the UI.
type Status struct {
	LoggedIn  bool      `json:"logged_in"`
	Online    bool      `json:"online"`
	Paused    bool      `json:"paused"`
	Queue     int       `json:"queue"`
	Active    []*Active `json:"active"`
	LastError string    `json:"last_error,omitempty"`
	Uploaded  int64     `json:"uploaded"`       // files uploaded since start
	Bytes     int64     `json:"uploaded_bytes"` // bytes uploaded since start
	Scanning  bool      `json:"scanning"`
}

// Engine runs the upload queue.
type Engine struct {
	cfg      *config.Store
	st       *state.DB
	log      *slog.Logger
	Notify   func(title, body string)
	OnChange func()

	mu        sync.Mutex
	client    *remote.Client
	queue     []*Job
	queued    map[string]*Job
	active    map[string]*Active
	online    bool
	lastError string
	uploaded  int64
	bytes     int64
	scanning  int

	wake    chan struct{}
	limiter *rate.Limiter

	watcher *fsnotify.Watcher
	watched map[string]string // directory -> folder id
	stable  map[string]time.Time

	batchMu    sync.Mutex
	batchFiles int
	batchBytes int64
	batchErrs  int
	batchTimer *time.Timer
	offlineMsg bool
}

// New creates the engine. Call Run to start it.
func New(cfg *config.Store, st *state.DB, log *slog.Logger) *Engine {
	e := &Engine{
		cfg:     cfg,
		st:      st,
		log:     log,
		queued:  map[string]*Job{},
		active:  map[string]*Active{},
		online:  true,
		wake:    make(chan struct{}, 1),
		limiter: rate.NewLimiter(rate.Inf, 0),
		watched: map[string]string{},
		stable:  map[string]time.Time{},
	}
	e.SetBandwidth(cfg.Get().BandwidthKBps)
	return e
}

// SetClient sets (or clears, when logged out) the server connection.
func (e *Engine) SetClient(c *remote.Client) {
	e.mu.Lock()
	e.client = c
	e.online = c != nil
	e.mu.Unlock()
	e.kick()
	e.changed()
}

func (e *Engine) getClient() *remote.Client {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client
}

// SetBandwidth sets the upload limit in KB/s (0 = unlimited).
func (e *Engine) SetBandwidth(kbps int) {
	if kbps <= 0 {
		e.limiter.SetLimit(rate.Inf)
		e.limiter.SetBurst(0)
		return
	}
	bps := float64(kbps) * 1024
	e.limiter.SetLimit(rate.Limit(bps))
	e.limiter.SetBurst(int(math.Max(64<<10, bps/4)))
}

func (e *Engine) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) changed() {
	if e.OnChange != nil {
		e.OnChange()
	}
}

// Status returns a snapshot of the engine.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := Status{
		LoggedIn:  e.client != nil,
		Online:    e.online,
		Paused:    e.cfg.Get().Paused,
		Queue:     len(e.queue),
		LastError: e.lastError,
		Uploaded:  e.uploaded,
		Bytes:     e.bytes,
		Scanning:  e.scanning > 0,
		Active:    []*Active{},
	}
	for _, a := range e.active {
		a.Done = a.done.Load()
		if secs := time.Since(a.started).Seconds(); secs > 0.5 && a.Phase == "uploading" {
			a.Speed = float64(a.Done) / secs
		}
		st.Active = append(st.Active, a)
	}
	sort.Slice(st.Active, func(i, j int) bool { return st.Active[i].Name < st.Active[j].Name })
	return st
}

// Run processes uploads and watches folders until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		e.log.Warn("folder watching unavailable", "err", err)
	} else {
		e.watcher = w
		defer w.Close()
		go e.watchLoop(ctx)
	}
	for i := 0; i < workers; i++ {
		go e.worker(ctx)
	}
	go e.Rescan()
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	stable := time.NewTicker(time.Second)
	defer stable.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			go e.Rescan()
		case <-stable.C:
			e.flushStable()
		}
	}
}

// ----- queue -----

func (e *Engine) enqueue(j *Job) {
	e.mu.Lock()
	if existing, ok := e.queued[j.Key]; ok {
		existing.onDone = append(existing.onDone, j.onDone...)
		e.mu.Unlock()
		return
	}
	e.queued[j.Key] = j
	e.queue = append(e.queue, j)
	e.mu.Unlock()
	e.kick()
	e.changed()
}

func (e *Engine) next() *Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client == nil || e.cfg.Get().Paused {
		return nil
	}
	now := time.Now()
	for i, j := range e.queue {
		if _, busy := e.active[j.Key]; busy {
			continue
		}
		if now.Before(j.notBefore) {
			continue
		}
		e.queue = append(e.queue[:i:i], e.queue[i+1:]...)
		return j
	}
	return nil
}

func (e *Engine) worker(ctx context.Context) {
	for {
		j := e.next()
		if j == nil {
			select {
			case <-ctx.Done():
				return
			case <-e.wake:
			case <-time.After(2 * time.Second):
			}
			continue
		}
		e.handle(ctx, j)
	}
}

func (e *Engine) requeue(j *Job, delay time.Duration) {
	j.notBefore = time.Now().Add(delay)
	e.mu.Lock()
	e.queue = append(e.queue, j)
	e.mu.Unlock()
	e.changed()
}

func (e *Engine) finish(j *Job, remotePath string, err error) {
	e.mu.Lock()
	delete(e.queued, j.Key)
	e.mu.Unlock()
	for _, fn := range j.onDone {
		fn(remotePath, err)
	}
	e.changed()
}

func (e *Engine) handle(ctx context.Context, j *Job) {
	remotePath, status, size, err := e.upload(ctx, j)
	switch {
	case err == nil:
		e.mu.Lock()
		e.online, e.lastError = true, ""
		if status == "done" || status == "copied" {
			e.uploaded++
			e.bytes += size
		}
		e.mu.Unlock()
		if status != "unchanged" && status != "gone" {
			e.st.AddHistory(state.Entry{Kind: "upload", Local: j.Abs, Remote: remotePath, Size: size, Status: status, FolderID: j.FolderID})
			if status == "done" || status == "copied" {
				e.batch(1, size, 0)
			}
		}
		e.finish(j, remotePath, nil)
	case errors.Is(err, errRetryLater):
		e.requeue(j, 2*time.Second)
	case errors.Is(err, context.Canceled):
		e.requeue(j, 0)
	case isLocalError(err):
		e.st.AddHistory(state.Entry{Kind: "upload", Local: j.Abs, Remote: j.Remote, Status: "error", Error: err.Error(), FolderID: j.FolderID})
		e.batch(0, 0, 1)
		e.finish(j, "", err)
	case remote.StatusOf(err) == 0 || remote.StatusOf(err) >= 500 && remote.StatusOf(err) != 507:
		// Server unreachable (Tailscale off, server down): try again later.
		e.mu.Lock()
		wasOnline := e.online
		e.online = false
		e.lastError = err.Error()
		e.mu.Unlock()
		if wasOnline {
			e.log.Warn("server unreachable, will retry", "err", err)
			e.notifyOnce("offline")
		}
		j.attempts++
		e.requeue(j, time.Duration(min(j.attempts, 6))*10*time.Second)
	default:
		e.mu.Lock()
		e.lastError = err.Error()
		e.mu.Unlock()
		e.log.Warn("upload failed", "file", j.Abs, "err", err)
		e.st.AddHistory(state.Entry{Kind: "upload", Local: j.Abs, Remote: j.Remote, Status: "error", Error: err.Error(), FolderID: j.FolderID})
		e.batch(0, 0, 1)
		e.finish(j, "", err)
	}
}

var errRetryLater = errors.New("file still changing")

// isLocalError reports errors reading the file on this PC (no retry helps).
func isLocalError(err error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe)
}

func (e *Engine) upload(ctx context.Context, j *Job) (remotePath, status string, size int64, err error) {
	fi, err := os.Stat(j.Abs)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "gone", 0, nil
	}
	if err != nil {
		return "", "", 0, err
	}
	if fi.IsDir() {
		return "", "gone", 0, nil
	}
	if time.Since(fi.ModTime()) < 2*time.Second {
		return "", "", 0, errRetryLater // wait until the file is completely written
	}
	size, mtime := fi.Size(), fi.ModTime().UnixMilli()
	var prev state.FileState
	hadPrev := false
	if j.FolderID != "" {
		prev, hadPrev = e.st.Get(j.FolderID, j.Rel)
		if hadPrev && prev.Size == size && prev.MTime == mtime {
			return prev.Remote, "unchanged", size, nil
		}
	}
	c := e.getClient()
	if c == nil {
		return "", "", 0, context.Canceled
	}
	act := &Active{Name: filepath.Base(j.Abs), Local: j.Abs, Remote: j.Remote, Size: size, Phase: "hashing", started: time.Now()}
	e.mu.Lock()
	e.active[j.Key] = act
	e.mu.Unlock()
	e.changed()
	defer func() {
		e.mu.Lock()
		delete(e.active, j.Key)
		e.mu.Unlock()
		e.changed()
	}()

	sum, err := hashFile(ctx, j.Abs)
	if err != nil {
		return "", "", 0, err
	}
	if hadPrev && prev.SHA256 == sum {
		// Only the timestamp changed.
		e.st.Put(j.FolderID, j.Rel, state.FileState{Size: size, MTime: mtime, SHA256: sum, Remote: prev.Remote})
		return prev.Remote, "unchanged", size, nil
	}
	conflict := "rename"
	if hadPrev {
		conflict = "overwrite" // a newer version of a file we uploaded before
	}
	if j.Conflict != "" {
		conflict = j.Conflict
	}
	target := j.Remote
	if hadPrev && prev.Remote != "" {
		target = prev.Remote
	}

	var st *remote.UploadStatus
	if id, ok := e.st.Pending(j.Key, size, mtime); ok {
		if off, err := c.UploadOffset(ctx, id); err == nil {
			st = &remote.UploadStatus{ID: id, Status: "pending", Offset: off, Size: size}
		} else {
			e.st.ClearPending(j.Key)
		}
	}
	if st == nil {
		st, err = c.CreateUpload(ctx, remote.UploadRequest{Space: j.Space, Path: target, Size: size, SHA256: sum, MTime: mtime, Conflict: conflict})
		if err != nil {
			return "", "", 0, err
		}
	}
	if st.Status == "pending" {
		e.st.SetPending(j.Key, st.ID, size, mtime)
		act.Phase = "uploading"
		act.started = time.Now()
		act.done.Store(st.Offset)
		st, err = e.send(ctx, c, j.Abs, st, size, act)
		if err != nil {
			if remote.StatusOf(err) == 404 {
				e.st.ClearPending(j.Key) // upload expired on the server: start over
			}
			return "", "", 0, err
		}
		e.st.ClearPending(j.Key)
	}
	if j.FolderID != "" {
		e.st.Put(j.FolderID, j.Rel, state.FileState{Size: size, MTime: mtime, SHA256: sum, Remote: st.Path})
	}
	return st.Path, st.Status, size, nil
}

// send streams the file from the current offset, resuming after errors.
func (e *Engine) send(ctx context.Context, c *remote.Client, abs string, st *remote.UploadStatus, size int64, act *Active) (*remote.UploadStatus, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	offset := st.Offset
	failures := 0
	for {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		rd := &limitedReader{ctx: ctx, r: io.LimitReader(f, size-offset), lim: e.limiter, progress: func(n int) { act.done.Add(int64(n)) }}
		act.done.Store(offset)
		res, err := c.UploadChunk(ctx, st.ID, offset, rd, size-offset)
		if err == nil {
			if res.Status == "done" || res.Offset >= size {
				return res, nil
			}
			offset = res.Offset
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var re *remote.Error
		if errors.As(err, &re) && re.Code == "offset_mismatch" && re.Offset != nil {
			offset = *re.Offset
			continue
		}
		if errors.As(err, &re) && re.Status < 500 {
			return nil, err
		}
		failures++
		if failures > 5 {
			return nil, err
		}
		time.Sleep(time.Duration(failures*failures) * time.Second)
		off, oerr := c.UploadOffset(ctx, st.ID)
		if oerr != nil {
			return nil, err
		}
		offset = off
	}
}

type limitedReader struct {
	ctx      context.Context
	r        io.Reader
	lim      *rate.Limiter
	progress func(int)
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if len(p) > 32<<10 {
		p = p[:32<<10]
	}
	n, err := l.r.Read(p)
	if n > 0 {
		if l.lim.Limit() != rate.Inf {
			if werr := l.lim.WaitN(l.ctx, n); werr != nil {
				return n, werr
			}
		}
		l.progress(n)
	}
	return n, err
}

func hashFile(ctx context.Context, p string) (string, error) {
	f, err := os.Open(p)
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
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if err != nil {
			return "", err
		}
	}
}

// ----- notifications (aggregated so 500 photos do not cause 500 popups) -----

func (e *Engine) batch(files int, bytes int64, errs int) {
	e.batchMu.Lock()
	defer e.batchMu.Unlock()
	e.batchFiles += files
	e.batchBytes += bytes
	e.batchErrs += errs
	if e.batchTimer == nil {
		e.batchTimer = time.AfterFunc(5*time.Second, e.flushBatch)
	}
}

func (e *Engine) flushBatch() {
	e.batchMu.Lock()
	files, bytes, errs := e.batchFiles, e.batchBytes, e.batchErrs
	e.batchFiles, e.batchBytes, e.batchErrs, e.batchTimer = 0, 0, 0, nil
	e.batchMu.Unlock()
	e.mu.Lock()
	busy := len(e.queue) > 0 || len(e.active) > 0
	e.mu.Unlock()
	if busy && errs == 0 {
		// Still uploading: report when the queue is empty.
		e.batch(files, bytes, 0)
		return
	}
	if e.Notify == nil {
		return
	}
	if files > 0 {
		e.Notify("Upload fertig", fmt.Sprintf("%d Datei(en) hochgeladen (%s).", files, humanBytes(bytes)))
	}
	if errs > 0 {
		e.Notify("Upload-Fehler", fmt.Sprintf("%d Datei(en) konnten nicht hochgeladen werden. Details im Verlauf.", errs))
	}
}

func (e *Engine) notifyOnce(kind string) {
	if kind == "offline" && e.Notify != nil {
		e.Notify("Server nicht erreichbar", "Homelydrive versucht es automatisch weiter. Ist Tailscale verbunden?")
	}
}

func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// ----- folders -----

// junk files are never uploaded (temporary and system files).
func isJunk(name string) bool {
	l := strings.ToLower(name)
	switch l {
	case "desktop.ini", "thumbs.db", ".ds_store", "ehthumbs.db":
		return true
	}
	if strings.HasPrefix(l, "~$") || strings.HasPrefix(l, ".~lock.") || strings.HasPrefix(l, ".homelydrive-") {
		return true
	}
	for _, suf := range []string{".tmp", ".temp", ".part", ".partial", ".crdownload", ".download", ".swp", ".hdcopy"} {
		if strings.HasSuffix(l, suf) {
			return true
		}
	}
	return false
}

func (e *Engine) folderJob(f config.Folder, abs string) *Job {
	rel, err := filepath.Rel(f.Local, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	rel = filepath.ToSlash(rel)
	return &Job{
		Key:      "f|" + f.ID + "|" + rel,
		FolderID: f.ID,
		Abs:      abs,
		Rel:      rel,
		Space:    f.Space,
		Remote:   path.Join(f.Remote, rel),
	}
}

// Rescan walks all enabled folders and queues new or changed files.
func (e *Engine) Rescan() {
	for _, f := range e.cfg.Get().Folders {
		if f.Enabled {
			e.scanFolder(f, f.Local)
		}
	}
}

// RescanFolder scans one folder (after it was added).
func (e *Engine) RescanFolder(id string) {
	for _, f := range e.cfg.Get().Folders {
		if f.ID == id && f.Enabled {
			go e.scanFolder(f, f.Local)
		}
	}
}

func (e *Engine) scanFolder(f config.Folder, root string) {
	e.mu.Lock()
	e.scanning++
	e.mu.Unlock()
	e.changed()
	defer func() {
		e.mu.Lock()
		e.scanning--
		e.mu.Unlock()
		e.changed()
	}()
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			e.watchDir(p, f.ID)
			return nil
		}
		if isJunk(d.Name()) || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		j := e.folderJob(f, p)
		if j == nil {
			return nil
		}
		if st, ok := e.st.Get(f.ID, j.Rel); ok && st.Size == info.Size() && st.MTime == info.ModTime().UnixMilli() {
			return nil
		}
		e.enqueue(j)
		return nil
	})
}

func (e *Engine) watchDir(dir, folderID string) {
	if e.watcher == nil {
		return
	}
	e.mu.Lock()
	_, ok := e.watched[dir]
	if !ok {
		e.watched[dir] = folderID
	}
	e.mu.Unlock()
	if !ok {
		if err := e.watcher.Add(dir); err != nil {
			e.log.Debug("cannot watch", "dir", dir, "err", err)
		}
	}
}

// Unwatch stops watching a removed folder.
func (e *Engine) Unwatch(folderID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for dir, id := range e.watched {
		if id == folderID {
			if e.watcher != nil {
				e.watcher.Remove(dir)
			}
			delete(e.watched, dir)
		}
	}
	keep := e.queue[:0]
	for _, j := range e.queue {
		if j.FolderID == folderID {
			delete(e.queued, j.Key)
			continue
		}
		keep = append(keep, j)
	}
	e.queue = keep
}

func (e *Engine) folderFor(abs string) (config.Folder, bool) {
	for _, f := range e.cfg.Get().Folders {
		if !f.Enabled {
			continue
		}
		rel, err := filepath.Rel(f.Local, abs)
		if err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return f, true
		}
	}
	return config.Folder{}, false
}

func (e *Engine) watchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-e.watcher.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Chmod) && !ev.Has(fsnotify.Write) {
				continue // upload only: deletions on the PC are ignored
			}
			if isJunk(filepath.Base(ev.Name)) {
				continue
			}
			fi, err := os.Stat(ev.Name)
			if err != nil {
				continue
			}
			f, ok := e.folderFor(ev.Name)
			if !ok {
				continue
			}
			if fi.IsDir() {
				go e.scanFolder(f, ev.Name)
				continue
			}
			e.mu.Lock()
			e.stable[ev.Name] = time.Now()
			e.mu.Unlock()
		case err, ok := <-e.watcher.Errors:
			if !ok {
				return
			}
			e.log.Debug("watch error", "err", err)
		}
	}
}

// flushStable queues files that have not changed for two seconds.
func (e *Engine) flushStable() {
	e.mu.Lock()
	var ready []string
	for p, t := range e.stable {
		if time.Since(t) >= 2*time.Second {
			ready = append(ready, p)
			delete(e.stable, p)
		}
	}
	e.mu.Unlock()
	for _, p := range ready {
		if f, ok := e.folderFor(p); ok {
			if j := e.folderJob(f, p); j != nil {
				e.enqueue(j)
			}
		}
	}
}

// ----- manual uploads (context menu, window) -----

// Upload queues local files or folders for upload into remoteFolder of space.
// Files inside watched folders are uploaded as part of their folder.
func (e *Engine) Upload(paths []string, space, remoteFolder string, onDone func(remotePath string, err error)) int {
	n := 0
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			e.queueManual(abs, space, path.Join(remoteFolder, filepath.Base(abs)), onDone)
			n++
			continue
		}
		base := filepath.Base(abs)
		filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || isJunk(d.Name()) || !d.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(abs, p)
			e.queueManual(p, space, path.Join(remoteFolder, base, filepath.ToSlash(rel)), nil)
			n++
			return nil
		})
		if onDone != nil {
			onDone(path.Join(remoteFolder, base), nil)
		}
	}
	return n
}

func (e *Engine) queueManual(abs, space, remotePath string, onDone func(string, error)) {
	if f, ok := e.folderFor(abs); ok {
		if j := e.folderJob(f, abs); j != nil {
			if onDone != nil {
				j.onDone = append(j.onDone, onDone)
			}
			e.enqueue(j)
			return
		}
	}
	j := &Job{Key: "m|" + abs + "|" + space + "|" + remotePath, Abs: abs, Space: space, Remote: remotePath, Manual: true}
	if onDone != nil {
		j.onDone = append(j.onDone, onDone)
	}
	e.enqueue(j)
}

// UploadReplace uploads one local file to exactly remotePath, replacing an
// existing file (used by the virtual drive when a file is saved).
func (e *Engine) UploadReplace(abs, space, remotePath string, onDone func(string, error)) {
	j := &Job{Key: "d|" + abs, Abs: abs, Space: space, Remote: remotePath, Manual: true, Conflict: "overwrite"}
	if onDone != nil {
		j.onDone = append(j.onDone, onDone)
	}
	e.enqueue(j)
}

// IsJunk reports whether a file name is a temporary or system file.
func IsJunk(name string) bool { return isJunk(name) }

// RemoteFor returns where a local file is (or will be) stored on the server
// if it lies inside a watched folder.
func (e *Engine) RemoteFor(abs string) (space, remotePath string, uploaded bool, ok bool) {
	f, ok := e.folderFor(abs)
	if !ok {
		return "", "", false, false
	}
	j := e.folderJob(f, abs)
	if j == nil {
		return "", "", false, false
	}
	st, done := e.st.Get(f.ID, j.Rel)
	if done {
		fi, err := os.Stat(abs)
		if err == nil && fi.Size() == st.Size && fi.ModTime().UnixMilli() == st.MTime {
			return f.Space, st.Remote, true, true
		}
	}
	return f.Space, j.Remote, false, true
}

// Retry queues the file of a failed history entry again.
func (e *Engine) Retry(entry state.Entry) {
	if f, ok := e.folderFor(entry.Local); ok {
		if j := e.folderJob(f, entry.Local); j != nil {
			e.enqueue(j)
			return
		}
	}
	dir := path.Dir(entry.Remote)
	if dir == "." {
		dir = ""
	}
	e.queueManual(entry.Local, "home", path.Join(dir, filepath.Base(entry.Local)), nil)
}
