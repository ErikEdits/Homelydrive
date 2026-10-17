package engine

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/erikedits/homelydrive/internal/client/config"
	"github.com/erikedits/homelydrive/internal/client/remote"
	"github.com/erikedits/homelydrive/internal/client/state"
	"github.com/erikedits/homelydrive/internal/server/api"
	sconfig "github.com/erikedits/homelydrive/internal/server/config"
	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/db"
	"github.com/erikedits/homelydrive/internal/server/logbuf"
)

func startServer(t *testing.T) (*httptest.Server, *core.Core) {
	t.Helper()
	dir := t.TempDir()
	cfg, _ := sconfig.Load(dir)
	database, err := db.OpenFile(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	logs := logbuf.New(10, "", nil)
	log := slog.New(slog.NewTextHandler(logs, nil))
	c, err := core.New(database, cfg, dir, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Start(ctx)
	s := &api.Server{Core: c, Cfg: cfg, Log: log, Logs: logs, Started: time.Now()}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	if _, err := c.CreateUser(core.NewUser{Username: "erik", Password: "geheim123"}); err != nil {
		t.Fatal(err)
	}
	return srv, c
}

func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestFolderUploadEngine(t *testing.T) {
	srv, _ := startServer(t)
	t.Setenv("HOMELYDRIVE_CLIENT_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tok, _, err := remote.Login(ctx, srv.URL, "erik", "geheim123", "test-pc")
	if err != nil {
		t.Fatal(err)
	}
	rc := remote.New(srv.URL, tok)

	local := t.TempDir()
	os.MkdirAll(filepath.Join(local, "Urlaub"), 0o755)
	write := func(rel, content string, age time.Duration) {
		p := filepath.Join(local, rel)
		os.WriteFile(p, []byte(content), 0o644)
		ts := time.Now().Add(-age)
		os.Chtimes(p, ts, ts)
	}
	write("brief.txt", "Hallo Welt", time.Minute)
	write("Urlaub/strand.jpg", "JPEGDATA", time.Minute)
	write("desktop.ini", "junk", time.Minute)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Update(func(c *config.Config) {
		c.Folders = []config.Folder{{ID: "f1", Local: local, Space: "home", Remote: "PC/Dokumente", Enabled: true}}
	})
	st, err := state.Open(config.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.SetClient(rc)
	go e.Run(ctx)

	exists := func(p string) bool {
		_, err := rc.Stat(ctx, "home", p)
		return err == nil
	}
	read := func(p string) string {
		r, _, err := rc.Download(ctx, "home", p, 0, -1)
		if err != nil {
			return ""
		}
		defer r.Close()
		b, _ := io.ReadAll(r)
		return string(b)
	}
	waitFor(t, "initial upload", func() bool { return exists("PC/Dokumente/brief.txt") && exists("PC/Dokumente/Urlaub/strand.jpg") })
	if exists("PC/Dokumente/desktop.ini") {
		t.Fatal("junk file was uploaded")
	}

	// A new file is picked up by the watcher.
	write("neu.txt", "frisch", 0)
	waitFor(t, "new file", func() bool { return exists("PC/Dokumente/neu.txt") })

	// A changed file replaces the old version (old version goes to the server trash).
	write("brief.txt", "Hallo Welt, geändert", 0)
	waitFor(t, "changed file", func() bool { return read("PC/Dokumente/brief.txt") == "Hallo Welt, geändert" })
	if exists("PC/Dokumente/brief (1).txt") {
		t.Fatal("changed file created a copy instead of replacing")
	}

	// Deleting on the PC keeps the file on the server (archive mode).
	os.Remove(filepath.Join(local, "neu.txt"))
	time.Sleep(3 * time.Second)
	if !exists("PC/Dokumente/neu.txt") {
		t.Fatal("file deleted on server after local delete")
	}

	// Manual upload with bandwidth limit.
	e.SetBandwidth(256) // KB/s
	big := filepath.Join(t.TempDir(), "gross.bin")
	os.WriteFile(big, bytes.Repeat([]byte("x"), 400<<10), 0o644)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(big, old, old)
	start := time.Now()
	done := make(chan string, 1)
	e.Upload([]string{big}, "home", "Vom PC", func(p string, err error) { done <- p })
	select {
	case p := <-done:
		if p != "Vom PC/gross.bin" {
			t.Fatalf("manual upload path %q", p)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("manual upload timeout")
	}
	if el := time.Since(start); el < 900*time.Millisecond {
		t.Fatalf("bandwidth limit not applied (took %v)", el)
	}
	hist, _ := st.History(0, 50, false)
	if len(hist) < 4 {
		t.Fatalf("history: %+v", hist)
	}
}
