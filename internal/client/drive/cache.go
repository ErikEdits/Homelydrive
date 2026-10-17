package drive

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/erikedits/homelydrive/internal/client/remote"
)

// blockSize is the unit in which files are fetched from the server. Files
// are downloaded only when (and where) they are read, so opening a big video
// on the drive starts immediately.
const blockSize = 4 << 20

// blockCache keeps downloaded blocks on disk, limited in total size.
type blockCache struct {
	dir   string
	limit int64

	mu       sync.Mutex
	inflight map[string]*fetch
	total    int64
	evicting bool
	prefetch chan struct{}
}

type fetch struct {
	done chan struct{}
	err  error
}

func newBlockCache(dir string, limit int64) *blockCache {
	os.MkdirAll(dir, 0o700)
	b := &blockCache{dir: dir, limit: limit, inflight: map[string]*fetch{}, prefetch: make(chan struct{}, 2)}
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				b.total += fi.Size()
			}
		}
		return nil
	})
	return b
}

func fileKey(space, rpath string, size, mtime int64) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%d|%d", space, rpath, size, mtime)))
	return hex.EncodeToString(h[:])
}

func (b *blockCache) blockPath(key string, idx int64) string {
	return filepath.Join(b.dir, key[:2], key, fmt.Sprintf("%d", idx))
}

// ReadAt reads from a remote file through the cache.
func (b *blockCache) ReadAt(ctx context.Context, c *remote.Client, space, rpath string, size, mtime int64, p []byte, off int64) (int, error) {
	if off >= size {
		return 0, io.EOF
	}
	key := fileKey(space, rpath, size, mtime)
	n := 0
	for n < len(p) && off < size {
		idx := off / blockSize
		bp, err := b.ensure(ctx, c, key, space, rpath, size, idx)
		if err != nil {
			return n, err
		}
		f, err := os.Open(bp)
		if err != nil {
			return n, err
		}
		inBlock := off - idx*blockSize
		m, err := f.ReadAt(p[n:min(len(p), n+int(blockSize-inBlock))], inBlock)
		f.Close()
		n += m
		off += int64(m)
		if err != nil && err != io.EOF {
			return n, err
		}
		if m == 0 {
			break
		}
		// Read ahead: videos and big files are usually read sequentially.
		if next := idx + 1; next*blockSize < size {
			b.prefetchBlock(c, key, space, rpath, size, next)
		}
	}
	return n, nil
}

func (b *blockCache) ensure(ctx context.Context, c *remote.Client, key, space, rpath string, size, idx int64) (string, error) {
	bp := b.blockPath(key, idx)
	if _, err := os.Stat(bp); err == nil {
		return bp, nil
	}
	b.mu.Lock()
	if f, ok := b.inflight[bp]; ok {
		b.mu.Unlock()
		select {
		case <-f.done:
			return bp, f.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f := &fetch{done: make(chan struct{})}
	b.inflight[bp] = f
	b.mu.Unlock()

	f.err = b.download(ctx, c, bp, space, rpath, size, idx)
	b.mu.Lock()
	delete(b.inflight, bp)
	b.mu.Unlock()
	close(f.done)
	return bp, f.err
}

func (b *blockCache) download(ctx context.Context, c *remote.Client, bp, space, rpath string, size, idx int64) error {
	start := idx * blockSize
	length := min(int64(blockSize), size-start)
	rc, _, err := c.Download(ctx, space, rpath, start, length)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(bp), 0o700); err != nil {
		return err
	}
	tmp := bp + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(rc, length))
	out.Close()
	if err == nil && n != length {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, bp); err != nil {
		return err
	}
	b.mu.Lock()
	b.total += n
	over := b.total > b.limit && !b.evicting
	if over {
		b.evicting = true
	}
	b.mu.Unlock()
	if over {
		go b.evict()
	}
	return nil
}

func (b *blockCache) prefetchBlock(c *remote.Client, key, space, rpath string, size, idx int64) {
	if _, err := os.Stat(b.blockPath(key, idx)); err == nil {
		return
	}
	select {
	case b.prefetch <- struct{}{}:
	default:
		return // enough prefetches running
	}
	go func() {
		defer func() { <-b.prefetch }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		b.ensure(ctx, c, key, space, rpath, size, idx)
	}()
}

// evict deletes the oldest blocks until the cache is below 80 % of its limit.
func (b *blockCache) evict() {
	defer func() {
		b.mu.Lock()
		b.evicting = false
		b.mu.Unlock()
	}()
	type ent struct {
		path string
		size int64
		mod  time.Time
	}
	var all []ent
	var total int64
	filepath.WalkDir(b.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			all = append(all, ent{p, fi.Size(), fi.ModTime()})
			total += fi.Size()
		}
		return nil
	})
	sort.Slice(all, func(i, j int) bool { return all[i].mod.Before(all[j].mod) })
	target := b.limit * 8 / 10
	for _, e := range all {
		if total <= target {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
			os.Remove(filepath.Dir(e.path)) // removes the file's folder once empty
		}
	}
	b.mu.Lock()
	b.total = total
	b.mu.Unlock()
}

// Clear empties the cache.
func (b *blockCache) Clear() {
	os.RemoveAll(b.dir)
	os.MkdirAll(b.dir, 0o700)
	b.mu.Lock()
	b.total = 0
	b.mu.Unlock()
}

// Size returns the bytes currently cached.
func (b *blockCache) Size() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}
