package core

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/erikedits/homelydrive/internal/server/fsname"
	"github.com/erikedits/homelydrive/internal/server/thumbs"
)

// WriteZip streams the given items as a ZIP archive. Nothing is buffered, so
// folders of any size can be downloaded; files are stored uncompressed (fast,
// photos and videos do not compress anyway) and ZIP64 is used when needed.
func (c *Core) WriteZip(ctx context.Context, w io.Writer, a *Access, rels []string) error {
	zw := zip.NewWriter(w)
	for _, p := range rels {
		rel, err := relPath(p)
		if err != nil {
			return err
		}
		full := a.Full(rel)
		fi, _, err := a.Space.Union.Stat(full)
		if err != nil {
			return ErrNotFound
		}
		_, base := fsname.Split(full)
		if full == "" {
			base = a.Label
			if base == "" {
				base = "Homelydrive"
			}
		}
		if !fi.IsDir() {
			if err := c.zipFile(zw, a, full, base); err != nil {
				return err
			}
			continue
		}
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: base + "/", Method: zip.Store, Modified: fi.ModTime()}); err != nil {
			return err
		}
		err = a.Space.Union.Walk(full, func(p string, e storageEntry) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			name := base + "/" + strings.TrimPrefix(strings.TrimPrefix(p, full), "/")
			if e.IsDir {
				_, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Method: zip.Store, Modified: time.UnixMilli(e.MTime)})
				return err
			}
			return c.zipFile(zw, a, p, name)
		})
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

func (c *Core) zipFile(zw *zip.Writer, a *Access, full, name string) error {
	f, err := a.Space.Union.Open(full)
	if err != nil {
		return nil // vanished meanwhile: skip
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: fi.ModTime()}
	hdr.UncompressedSize64 = uint64(fi.Size())
	hdr.SetMode(0o644)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// Extract unpacks a ZIP file into a new folder next to it ("Hier entpacken").
// Returns the relative path of the created folder.
func (c *Core) Extract(a *Access, rel string) (string, error) {
	if err := c.requireWrite(a); err != nil {
		return "", err
	}
	rel, err := relPath(rel)
	if err != nil {
		return "", err
	}
	full := a.Full(rel)
	fi, i, err := a.Space.Union.Stat(full)
	if err != nil || fi.IsDir() {
		return "", ErrNotFound
	}
	zr, err := zip.OpenReader(a.Space.Union.Abs(i, full))
	if err != nil {
		return "", badRequest("Die Datei ist kein gültiges ZIP-Archiv.")
	}
	defer zr.Close()
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
	}
	if err := c.checkQuota(a.Space, int64(total)); err != nil {
		return "", err
	}
	parent, name := fsname.Split(full)
	folder := strings.TrimSuffix(name, path.Ext(name))
	if folder == "" {
		folder = "Entpackt"
	}
	folder = uniqueName(a.Space.Union, parent, folder)
	dest := fsname.Join(parent, folder)
	if err := a.Space.Union.Mkdir(dest); err != nil {
		return "", mapFSErr(err)
	}
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if f.NonUTF8 {
			name = strings.ToValidUTF8(name, "_")
		}
		clean, err := fsname.SanitizePath(name)
		if err != nil || clean == "" {
			continue // skips "../" and absolute paths (zip slip)
		}
		target := fsname.Join(dest, clean)
		if f.FileInfo().IsDir() {
			if !a.Space.Union.Exists(target) {
				a.Space.Union.Mkdir(target)
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		tparent, _ := fsname.Split(target)
		idx, err := a.Space.Union.PickRoot(tparent, int64(f.UncompressedSize64))
		if err != nil {
			return "", mapFSErr(err)
		}
		dst := a.Space.Union.Abs(idx, target)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := extractOne(f, dst); err != nil {
			return "", fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	c.scanSubtree(a.Space, dest)
	c.indexPathChain(a.Space, dest)
	c.logActivity(a.User, "", a.Space.ID, "extract", dest, name)
	c.publishChange(a.Space.ID, parent)
	c.afterWrite(a.Space)
	return a.Rel(dest), nil
}

func extractOne(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	// Guard against archives that lie about their size.
	n, err := io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
	out.Close()
	if err == nil && uint64(n) > f.UncompressedSize64 {
		err = errors.New("archive entry larger than declared")
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	if !f.Modified.IsZero() {
		os.Chtimes(dst, f.Modified, f.Modified)
	}
	return nil
}

// ----- Thumbnails -----

// Thumb returns the path of a cached JPEG thumbnail (created on demand).
func (c *Core) Thumb(ctx context.Context, a *Access, rel string, size int) (string, error) {
	switch {
	case size <= 256:
		size = 256
	case size <= 512:
		size = 512
	default:
		size = 1280
	}
	rel, err := relPath(rel)
	if err != nil {
		return "", err
	}
	full := a.Full(rel)
	r, err := c.statRow(a.Space, full)
	if err != nil {
		return "", err
	}
	if r.IsDir || (r.Category != "image" && r.Category != "video") {
		return "", thumbs.ErrUnsupported
	}
	phys := c.physPath(a.Space, full)
	if phys == "" {
		return "", ErrNotFound
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%d|%s|%d|%d|%d", a.Space.ID, full, r.Size, r.MTime, size)))
	key := hex.EncodeToString(h[:])
	dst := filepath.Join(c.ThumbDir(), key[:2], key+".jpg")
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if _, err := os.Stat(dst + ".fail"); err == nil {
		return "", thumbs.ErrUnsupported
	}
	select {
	case c.thumbSem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.thumbSem }()
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if err := thumbs.Generate(ctx, phys, r.Category, dst, size); err != nil {
		if ctx.Err() == nil {
			os.MkdirAll(filepath.Dir(dst), 0o750)
			os.WriteFile(dst+".fail", nil, 0o640)
		}
		return "", thumbs.ErrUnsupported
	}
	return dst, nil
}

// EditImage rotates and/or crops a photo. With asCopy a new file is created,
// otherwise the original is replaced and the old version goes to the trash.
func (c *Core) EditImage(a *Access, rel string, rotate int, crop *thumbs.Crop, asCopy bool) (FileInfo, error) {
	if err := c.requireWrite(a); err != nil {
		return FileInfo{}, err
	}
	rel, err := relPath(rel)
	if err != nil {
		return FileInfo{}, err
	}
	full := a.Full(rel)
	r, err := c.statRow(a.Space, full)
	if err != nil {
		return FileInfo{}, err
	}
	if r.Category != "image" {
		return FileInfo{}, badRequest("Nur Fotos können bearbeitet werden.")
	}
	phys := c.physPath(a.Space, full)
	parent, name := fsname.Split(full)
	idx, err := a.Space.Union.PickRoot(parent, r.Size*2)
	if err != nil {
		return FileInfo{}, mapFSErr(err)
	}
	tmpDir := a.Space.Union.Roots[idx].TempDir
	os.MkdirAll(tmpDir, 0o755)
	tmp := filepath.Join(tmpDir, randomToken(12)+".edit")
	ext, err := thumbs.Edit(phys, tmp, rotate, crop)
	if err != nil {
		os.Remove(tmp)
		return FileInfo{}, badRequest("Das Bild kann nicht bearbeitet werden: %v", err)
	}
	base := strings.TrimSuffix(name, path.Ext(name))
	var target string
	if asCopy {
		target = fsname.Join(parent, uniqueName(a.Space.Union, parent, base+" (bearbeitet)."+ext))
	} else {
		if err := c.trashPath(a.Space, full, a.User); err != nil {
			os.Remove(tmp)
			return FileInfo{}, err
		}
		target = fsname.Join(parent, uniqueName(a.Space.Union, parent, base+"."+ext))
	}
	dst := a.Space.Union.Abs(idx, target)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return FileInfo{}, err
	}
	c.syncDir(a.Space, parent)
	c.logActivity(a.User, "", a.Space.ID, "edit", target, "photo")
	c.publishChange(a.Space.ID, parent)
	c.afterWrite(a.Space)
	return c.Stat(a, a.Rel(target))
}
