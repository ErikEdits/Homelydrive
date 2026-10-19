//go:build windows

package core

import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/windows"
)

// Windows refuses to rename or delete a folder while anything below it is
// open, and a watch is an open handle. fsnotify opens one per folder, which
// made every folder with subfolders immovable (also in Windows Explorer).
// So each space root is watched once, together with all its subfolders.

func newWatchBackend() (watchBackend, error) {
	return &treeWatcher{
		events: make(chan fsnotify.Event, 256),
		errors: make(chan error, 8),
		trees:  map[string]*tree{},
	}, nil
}

type treeWatcher struct {
	events chan fsnotify.Event
	errors chan error

	mu     sync.Mutex
	trees  map[string]*tree
	closed bool
}

// tree is the watch of one folder and everything below it.
type tree struct {
	path string
	h    windows.Handle
	stop windows.Handle // event that wakes run to end it
	quit chan struct{}
	done chan struct{}
	ov   windows.Overlapped
	buf  []byte
}

const treeFilter = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE

func (t *treeWatcher) eventCh() <-chan fsnotify.Event { return t.events }
func (t *treeWatcher) errorCh() <-chan error          { return t.errors }
func (t *treeWatcher) recursive() bool                { return true }

func (t *treeWatcher) Add(dir string) error {
	dir = filepath.Clean(dir)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return fsnotify.ErrClosed
	}
	if old, ok := t.trees[dir]; ok {
		select {
		case <-old.done: // the folder went away, watch the new one
			windows.CloseHandle(old.stop)
		default:
			return nil
		}
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return &os.PathError{Op: "watch", Path: dir, Err: err}
	}
	ready, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return err
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(ready)
		windows.CloseHandle(h)
		return err
	}
	tr := &tree{path: dir, h: h, stop: stop, quit: make(chan struct{}), done: make(chan struct{}), buf: make([]byte, 64<<10)}
	tr.ov.HEvent = ready
	t.trees[dir] = tr
	go t.run(tr)
	return nil
}

func (t *treeWatcher) Remove(dir string) error {
	dir = filepath.Clean(dir)
	t.mu.Lock()
	tr, ok := t.trees[dir]
	delete(t.trees, dir)
	t.mu.Unlock()
	if !ok {
		return fsnotify.ErrNonExistentWatch
	}
	tr.end()
	return nil
}

func (t *treeWatcher) Close() error {
	t.mu.Lock()
	trees := t.trees
	t.trees = map[string]*tree{}
	t.closed = true
	t.mu.Unlock()
	for _, tr := range trees {
		tr.end()
	}
	return nil
}

// end stops the watch and waits until its handle is closed. It never waits
// for the consumer of the events, so it may run while that is busy.
func (tr *tree) end() {
	close(tr.quit)
	windows.SetEvent(tr.stop)
	<-tr.done
	windows.CloseHandle(tr.stop)
}

func (t *treeWatcher) run(tr *tree) {
	defer close(tr.done)
	defer windows.CloseHandle(tr.ov.HEvent)
	defer windows.CloseHandle(tr.h)
	for {
		windows.ResetEvent(tr.ov.HEvent)
		err := windows.ReadDirectoryChanges(tr.h, &tr.buf[0], uint32(len(tr.buf)), true, treeFilter, nil, &tr.ov, 0)
		if err != nil {
			t.send(tr, fsnotify.Event{Name: tr.path, Op: fsnotify.Remove})
			return
		}
		woke, err := windows.WaitForMultipleObjects([]windows.Handle{tr.ov.HEvent, tr.stop}, false, windows.INFINITE)
		var n uint32
		if err != nil || woke != windows.WAIT_OBJECT_0 {
			// Stopped: the kernel must let go of buf before it is freed.
			windows.CancelIoEx(tr.h, &tr.ov)
			windows.GetOverlappedResult(tr.h, &tr.ov, &n, true)
			return
		}
		err = windows.GetOverlappedResult(tr.h, &tr.ov, &n, false)
		switch {
		case err == windows.ERROR_NOTIFY_ENUM_DIR || (err == nil && n == 0):
			// More changes than fit into the buffer, the details are lost.
			select {
			case t.errors <- fsnotify.ErrEventOverflow:
			case <-tr.quit:
				return
			}
		case err != nil:
			// The folder itself was deleted or its disk is gone.
			t.send(tr, fsnotify.Event{Name: tr.path, Op: fsnotify.Remove})
			return
		default:
			if !t.parse(tr, n) {
				return
			}
		}
	}
}

// parse turns the FILE_NOTIFY_INFORMATION records in tr.buf into events.
func (t *treeWatcher) parse(tr *tree, n uint32) bool {
	for off := uint32(0); off < n; {
		info := (*windows.FileNotifyInformation)(unsafe.Pointer(&tr.buf[off]))
		name := windows.UTF16ToString(unsafe.Slice(&info.FileName, info.FileNameLength/2))
		op := fsnotify.Write
		switch info.Action {
		case windows.FILE_ACTION_ADDED, windows.FILE_ACTION_RENAMED_NEW_NAME:
			op = fsnotify.Create
		case windows.FILE_ACTION_REMOVED:
			op = fsnotify.Remove
		case windows.FILE_ACTION_RENAMED_OLD_NAME:
			op = fsnotify.Rename
		}
		if !t.send(tr, fsnotify.Event{Name: filepath.Join(tr.path, name), Op: op}) {
			return false
		}
		if info.NextEntryOffset == 0 {
			break
		}
		off += info.NextEntryOffset
	}
	return true
}

// send passes an event on, unless the watch is being stopped meanwhile.
func (t *treeWatcher) send(tr *tree, ev fsnotify.Event) bool {
	select {
	case t.events <- ev:
		return true
	case <-tr.quit:
		return false
	}
}
