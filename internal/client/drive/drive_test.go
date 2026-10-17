//go:build linux && cgo

package drive_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/erikedits/homelydrive/internal/client/config"
	"github.com/erikedits/homelydrive/internal/client/drive"
	"github.com/erikedits/homelydrive/internal/client/engine"
	"github.com/erikedits/homelydrive/internal/client/remote"
	"github.com/erikedits/homelydrive/internal/client/state"
	"github.com/erikedits/homelydrive/internal/server/api"
	sconfig "github.com/erikedits/homelydrive/internal/server/config"
	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/db"
	"github.com/erikedits/homelydrive/internal/server/logbuf"
)

func TestDriveFUSE(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("no /dev/fuse")
	}
	if _, err := exec.LookPath("fusermount"); err != nil {
		t.Skip("no fusermount")
	}
	dir := t.TempDir()
	scfg, _ := sconfig.Load(dir)
	database, err := db.OpenFile(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, _ := core.New(database, scfg, dir, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	srv := httptest.NewServer((&api.Server{Core: c, Cfg: scfg, Log: log, Logs: logbuf.New(10, "", nil), Started: time.Now()}).Handler())
	defer srv.Close()
	c.CreateUser(core.NewUser{Username: "erik", Password: "geheim123"})
	tok, _, err := remote.Login(ctx, srv.URL, "erik", "geheim123", "drive-test")
	if err != nil {
		t.Fatal(err)
	}
	rc := remote.New(srv.URL, tok)
	rc.Mkdir(ctx, "home", "Fotos")
	data := bytes.Repeat([]byte("0123456789abcdef"), 600000) // ~9.6 MB: spans 3 blocks
	st, _ := rc.CreateUpload(ctx, remote.UploadRequest{Space: "home", Path: "Fotos/video.bin", Size: int64(len(data))})
	if _, err := rc.UploadChunk(ctx, st.ID, 0, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOMELYDRIVE_CLIENT_DIR", t.TempDir())
	cfg, _ := config.Load()
	sdb, _ := state.Open(config.Dir())
	defer sdb.Close()
	eng := engine.New(cfg, sdb, log)
	eng.SetClient(rc)
	go eng.Run(ctx)

	d := drive.New(func() *remote.Client { return rc }, func(local, space, rp string, done func(error)) {
		eng.UploadReplace(local, space, rp, func(_ string, err error) { done(err) })
	}, "de", filepath.Join(t.TempDir(), "cache"), 64, log)
	d.IsJunk = engine.IsJunk
	mnt := filepath.Join(t.TempDir(), "mnt")
	if err := d.Mount(mnt); err != nil {
		t.Skipf("cannot mount: %v", err)
	}
	defer d.Unmount()

	des, err := os.ReadDir(mnt)
	if err != nil {
		t.Fatal(err)
	}
	if len(des) != 1 || des[0].Name() != "Meine Ablage" {
		t.Fatalf("root: %v", des)
	}
	home := filepath.Join(mnt, "Meine Ablage")

	// Reading a server file through the block cache (with a seek).
	got, err := os.ReadFile(filepath.Join(home, "Fotos", "video.bin"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read through drive failed: %v (len %d)", err, len(got))
	}
	f, _ := os.Open(filepath.Join(home, "Fotos", "video.bin"))
	buf := make([]byte, 16)
	f.ReadAt(buf, 5*1024*1024)
	f.Close()
	if string(buf) != string(data[5*1024*1024:5*1024*1024+16]) {
		t.Fatal("random access read wrong")
	}

	// Writing a new file uploads it.
	if err := os.WriteFile(filepath.Join(home, "notiz.txt"), []byte("vom Laufwerk"), 0o644); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		r, _, err := rc.Download(ctx, "home", "notiz.txt", 0, -1)
		if err != nil {
			return false
		}
		defer r.Close()
		b, _ := io.ReadAll(r)
		return string(b) == "vom Laufwerk"
	})

	// Overwriting replaces the content.
	os.WriteFile(filepath.Join(home, "notiz.txt"), []byte("geändert"), 0o644)
	wait(t, func() bool {
		r, _, err := rc.Download(ctx, "home", "notiz.txt", 0, -1)
		if err != nil {
			return false
		}
		defer r.Close()
		b, _ := io.ReadAll(r)
		return string(b) == "geändert"
	})

	// mkdir, rename, delete.
	if err := os.Mkdir(filepath.Join(home, "Neu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(home, "Fotos"), filepath.Join(home, "Neu", "Bilder")); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Stat(ctx, "home", "Neu/Bilder/video.bin"); err != nil {
		t.Fatalf("move via drive failed: %v", err)
	}
	if err := os.Remove(filepath.Join(home, "notiz.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Stat(ctx, "home", "notiz.txt"); err == nil {
		t.Fatal("delete via drive failed")
	}
	// Lock files of editors are never uploaded.
	os.WriteFile(filepath.Join(home, "~$brief.docx"), []byte("lock"), 0o644)
	time.Sleep(3 * time.Second)
	if _, err := rc.Stat(ctx, "home", "~$brief.docx"); err == nil {
		t.Fatal("lock file was uploaded")
	}
	os.Remove(filepath.Join(home, "~$brief.docx"))

	des, _ = os.ReadDir(home)
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "Neu" {
		t.Fatalf("home listing: %v", names)
	}
}

func wait(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("timeout")
}
