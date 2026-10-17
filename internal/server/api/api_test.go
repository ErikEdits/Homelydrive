package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erikedits/homelydrive/internal/server/config"
	"github.com/erikedits/homelydrive/internal/server/core"
	"github.com/erikedits/homelydrive/internal/server/db"
	"github.com/erikedits/homelydrive/internal/server/logbuf"
	"github.com/erikedits/homelydrive/internal/server/webdavfs"
)

type env struct {
	t    *testing.T
	srv  *httptest.Server
	core *core.Core
	dir  string
}

func newEnv(t *testing.T, locations int) *env {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if locations > 1 {
		cfg.Update(func(c *config.Config) {
			for i := 1; i < locations; i++ {
				c.StorageLocations = append(c.StorageLocations, filepath.Join(dir, fmt.Sprintf("disk%d", i)))
			}
		})
	}
	database, err := db.OpenFile(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	logs := logbuf.New(100, "", nil)
	log := slog.New(slog.NewTextHandler(logs, nil))
	c, err := core.New(database, cfg, dir, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Start(ctx)
	s := &Server{Core: c, Cfg: cfg, Log: log, Logs: logs, Started: time.Now(), DAV: webdavfs.New(c, log)}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, core: c, dir: dir}
}

func (e *env) user(name string, admin bool) string {
	e.t.Helper()
	if _, err := e.core.CreateUser(core.NewUser{Username: name, Password: "geheim123", IsAdmin: admin}); err != nil {
		e.t.Fatal(err)
	}
	var out struct{ Token string }
	e.do("", "POST", "/api/v1/auth/login", map[string]string{"username": name, "password": "geheim123", "device": "test"}, &out, 200)
	return out.Token
}

func (e *env) do(tok, method, path string, body any, out any, want int) *http.Response {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case io.Reader:
		rd = b
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if want != 0 && resp.StatusCode != want {
		e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	if out != nil {
		if bp, ok := out.(*[]byte); ok {
			*bp = data
		} else if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: decode: %v: %s", method, path, err, data)
		}
	}
	return resp
}

func (e *env) upload(tok, space, p string, data []byte) core.UploadStatus {
	e.t.Helper()
	sum := sha256.Sum256(data)
	var st core.UploadStatus
	e.do(tok, "POST", "/api/v1/uploads", map[string]any{"space": space, "path": p, "size": len(data), "sha256": hex.EncodeToString(sum[:])}, &st, 200)
	if st.Status != "pending" {
		return st
	}
	req, _ := http.NewRequest("PATCH", e.srv.URL+"/api/v1/uploads/"+st.ID, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Upload-Offset", "0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&st)
	if st.Status != "done" {
		e.t.Fatalf("upload not done: %+v", st)
	}
	return st
}

type listResp struct {
	Entries []core.FileInfo
}

func names(l listResp) string {
	var n []string
	for _, e := range l.Entries {
		n = append(n, e.Name)
	}
	return strings.Join(n, ",")
}

func TestEndToEnd(t *testing.T) {
	e := newEnv(t, 2)
	anna := e.user("anna", true)
	ben := e.user("ben", false)

	// Folders and uploads.
	e.do(anna, "POST", "/api/v1/files/mkdir", map[string]string{"path": "Fotos/2026"}, nil, 200)
	e.upload(anna, "home", "Fotos/2026/notiz.txt", []byte("Steuererklärung Überweisung Rechnung"))
	e.upload(anna, "home", "Dokumente/plan.md", []byte("# Plan\n\nUrlaub am Strand"))

	var l listResp
	e.do(anna, "GET", "/api/v1/files/list?space=home&path=", nil, &l, 200)
	if names(l) != "Dokumente,Fotos" {
		t.Fatalf("root listing: %s", names(l))
	}
	if l.Entries[1].Size == 0 {
		t.Fatalf("folder size not computed: %+v", l.Entries[1])
	}

	// Same content again: recognized, not uploaded twice.
	st := e.upload(anna, "home", "Fotos/2026/notiz.txt", []byte("Steuererklärung Überweisung Rechnung"))
	if st.Status != "exists" {
		t.Fatalf("expected exists, got %s", st.Status)
	}
	// Different content, same name: renamed.
	st = e.upload(anna, "home", "Fotos/2026/notiz.txt", []byte("anderer Inhalt"))
	if st.Path != "Fotos/2026/notiz (1).txt" {
		t.Fatalf("expected rename, got %q", st.Path)
	}

	// Resumable upload in two chunks.
	big := bytes.Repeat([]byte("0123456789"), 100000)
	sum := sha256.Sum256(big)
	var up core.UploadStatus
	e.do(anna, "POST", "/api/v1/uploads", map[string]any{"path": "Videos/gross.bin", "size": len(big), "sha256": hex.EncodeToString(sum[:])}, &up, 200)
	patch := func(off int, part []byte) (*http.Response, core.UploadStatus) {
		req, _ := http.NewRequest("PATCH", e.srv.URL+"/api/v1/uploads/"+up.ID, bytes.NewReader(part))
		req.Header.Set("Authorization", "Bearer "+anna)
		req.Header.Set("Upload-Offset", fmt.Sprint(off))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var s core.UploadStatus
		json.NewDecoder(resp.Body).Decode(&s)
		return resp, s
	}
	if _, s := patch(0, big[:300000]); s.Offset != 300000 {
		t.Fatalf("offset after first chunk: %+v", s)
	}
	if resp, _ := patch(0, big[:10]); resp.StatusCode != http.StatusConflict {
		t.Fatalf("wrong offset accepted: %d", resp.StatusCode)
	}
	var head core.UploadStatus
	e.do(anna, "GET", "/api/v1/uploads/"+up.ID, nil, &head, 200)
	if head.Offset != 300000 {
		t.Fatalf("resume offset %d", head.Offset)
	}
	if _, s := patch(300000, big[300000:]); s.Status != "done" {
		t.Fatalf("second chunk: %+v", s)
	}
	var got []byte
	e.do(anna, "GET", "/api/v1/files/content?space=home&path="+url.QueryEscape("Videos/gross.bin"), nil, &got, 200)
	if !bytes.Equal(got, big) {
		t.Fatal("downloaded content differs")
	}
	// Range request (video seeking).
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/files/content?space=home&path=Videos/gross.bin", nil)
	req.Header.Set("Authorization", "Bearer "+anna)
	req.Header.Set("Range", "bytes=10-19")
	resp, _ := http.DefaultClient.Do(req)
	part, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(part) != "0123456789" {
		t.Fatalf("range: %d %q", resp.StatusCode, part)
	}

	// Full-text search (content and accents).
	time.Sleep(300 * time.Millisecond)
	waitIndexed(t, e)
	var res []core.FileInfo
	e.do(anna, "GET", "/api/v1/search?q=uberweisung", nil, &res, 200)
	if len(res) != 1 || res[0].Name != "notiz.txt" {
		t.Fatalf("content search: %+v", res)
	}
	e.do(anna, "GET", "/api/v1/search?q=plan&type=text", nil, &res, 200)
	if len(res) != 1 {
		t.Fatalf("name search: %+v", res)
	}

	// Stars, tags, notes, colours.
	e.do(anna, "POST", "/api/v1/files/star", map[string]any{"path": "Dokumente/plan.md", "starred": true}, nil, 200)
	e.do(anna, "PUT", "/api/v1/files/tags", map[string]any{"path": "Dokumente/plan.md", "tags": []string{"Urlaub", "2026"}}, nil, 200)
	e.do(anna, "PUT", "/api/v1/files/meta", map[string]any{"path": "Fotos", "color": "#e53935", "note": "Alle Bilder"}, nil, 200)
	var starred []core.FileInfo
	e.do(anna, "GET", "/api/v1/starred", nil, &starred, 200)
	if len(starred) != 1 || len(starred[0].Tags) != 2 {
		t.Fatalf("starred: %+v", starred)
	}
	e.do(anna, "GET", "/api/v1/search?tag=Urlaub", nil, &res, 200)
	if len(res) != 1 {
		t.Fatalf("tag search: %+v", res)
	}

	// Rename keeps metadata.
	e.do(anna, "POST", "/api/v1/files/rename", map[string]string{"path": "Dokumente", "new_name": "Docs"}, nil, 200)
	e.do(anna, "GET", "/api/v1/starred", nil, &starred, 200)
	if len(starred) != 1 || starred[0].Path != "Docs/plan.md" {
		t.Fatalf("star lost after rename: %+v", starred)
	}

	// Share with ben (read only).
	var sh core.Share
	e.do(anna, "POST", "/api/v1/shares", map[string]any{"path": "Fotos", "username": "ben"}, &sh, 200)
	var roots []core.SpaceRoot
	e.do(ben, "GET", "/api/v1/roots", nil, &roots, 200)
	if len(roots) != 2 || roots[1].Name != "Fotos" {
		t.Fatalf("ben roots: %+v", roots)
	}
	shareRef := roots[1].Ref
	e.do(ben, "GET", "/api/v1/files/list?space="+shareRef+"&path=2026", nil, &l, 200)
	if len(l.Entries) != 2 {
		t.Fatalf("ben share listing: %s", names(l))
	}
	e.do(ben, "POST", "/api/v1/files/delete", map[string]any{"space": shareRef, "paths": []string{"2026/notiz.txt"}}, nil, 403)
	e.do(ben, "GET", "/api/v1/files/list?space=home&path=../anna", nil, nil, 400)
	var notes struct {
		Items  []core.Notification
		Unread int
	}
	e.do(ben, "GET", "/api/v1/notifications", nil, &notes, 200)
	if notes.Unread != 1 || notes.Items[0].Kind != "share" {
		t.Fatalf("share notification: %+v", notes)
	}

	// Public download link with password and download limit.
	var link core.Link
	pw := "linkpw"
	max := int64(1)
	e.do(anna, "POST", "/api/v1/links", map[string]any{"path": "Docs/plan.md", "password": pw, "max_downloads": max}, &link, 200)
	e.do("", "GET", "/api/v1/public/"+link.Token+"/content", nil, nil, 401)
	jar := &cookieClient{e: e}
	jar.post("/api/v1/public/"+link.Token+"/auth", `{"password":"linkpw"}`, 200)
	if body := jar.get("/api/v1/public/"+link.Token+"/content", 200); !strings.Contains(body, "Urlaub") {
		t.Fatalf("link content: %q", body)
	}
	jar.get("/api/v1/public/"+link.Token+"/content", 410) // limit reached

	// Public folder link: listing and ZIP without an account.
	var fl core.Link
	e.do(anna, "POST", "/api/v1/links", map[string]any{"path": "Fotos"}, &fl, 200)
	var pl struct{ Entries []core.FileInfo }
	e.do("", "GET", "/api/v1/public/"+fl.Token+"/list?path=2026", nil, &pl, 200)
	if len(pl.Entries) != 2 {
		t.Fatalf("public folder listing: %+v", pl.Entries)
	}
	e.do("", "GET", "/api/v1/public/"+fl.Token+"/list?path=..", nil, nil, 400)

	// Upload link: anonymous upload into a folder.
	var ul core.Link
	e.do(anna, "POST", "/api/v1/links", map[string]any{"path": "Fotos", "kind": "upload"}, &ul, 200)
	data := []byte("hallo von draussen")
	var pst core.UploadStatus
	e.do("", "POST", "/api/v1/public/"+ul.Token+"/uploads", map[string]any{"path": "gruss.txt", "size": len(data)}, &pst, 200)
	req, _ = http.NewRequest("PATCH", e.srv.URL+"/api/v1/public/"+ul.Token+"/uploads/"+pst.ID, bytes.NewReader(data))
	req.Header.Set("Upload-Offset", "0")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	e.do(anna, "GET", "/api/v1/files/list?space=home&path=Fotos", nil, &l, 200)
	if !strings.Contains(names(l), "gruss.txt") {
		t.Fatalf("upload link file missing: %s", names(l))
	}

	// ZIP download of a folder.
	var zipData []byte
	e.do(anna, "GET", "/api/v1/files/zip?space=home&path=Fotos", nil, &zipData, 200)
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) < 4 {
		t.Fatalf("zip entries: %d", len(zr.File))
	}

	// Extract a zip on the server.
	e.upload(anna, "home", "archiv.zip", zipData)
	var ex struct{ Path string }
	e.do(anna, "POST", "/api/v1/files/extract", map[string]string{"path": "archiv.zip"}, &ex, 200)
	e.do(anna, "GET", "/api/v1/files/list?space=home&path="+url.QueryEscape(ex.Path+"/Fotos/2026"), nil, &l, 200)
	if len(l.Entries) != 2 {
		t.Fatalf("extracted: %s", names(l))
	}

	// Trash and restore.
	e.do(anna, "POST", "/api/v1/files/delete", map[string]any{"paths": []string{"Docs"}}, nil, 200)
	var trash []core.TrashItem
	e.do(anna, "GET", "/api/v1/trash", nil, &trash, 200)
	if len(trash) != 1 || trash[0].Name != "Docs" {
		t.Fatalf("trash: %+v", trash)
	}
	e.do(anna, "POST", "/api/v1/trash/restore", map[string]any{"ids": []int64{trash[0].ID}}, nil, 200)
	e.do(anna, "GET", "/api/v1/files/stat?space=home&path=Docs/plan.md", nil, nil, 200)

	// Files dropped directly into the folder on the server are picked up.
	home := filepath.Join(e.dir, "storage", "users", "anna")
	os.WriteFile(filepath.Join(home, "direkt.txt"), []byte("direkt abgelegt"), 0o644)
	e.do(anna, "GET", "/api/v1/files/list?space=home&path=", nil, &l, 200)
	if !strings.Contains(names(l), "direkt.txt") {
		t.Fatalf("external file not found: %s", names(l))
	}

	// Unknown file types are registered automatically.
	e.upload(anna, "home", "config.unbekannt", []byte("key=value\n"))
	var types []core.FileType
	e.do(anna, "GET", "/api/v1/filetypes", nil, &types, 200)
	found := false
	for _, ty := range types {
		if ty.Ext == "unbekannt" && ty.Category == "text" && ty.Auto {
			found = true
		}
	}
	if !found {
		t.Fatal("unknown type not auto-registered as text")
	}

	// Quota.
	q := int64(1000)
	e.do(anna, "PUT", "/api/v1/me", map[string]any{}, nil, 200)
	ben2, _ := e.core.GetUserByName("ben")
	e.core.UpdateUser(ben2.ID, core.UserUpdate{QuotaBytes: &q}, "")
	e.do(ben, "POST", "/api/v1/uploads", map[string]any{"path": "x.bin", "size": 5000}, nil, http.StatusInsufficientStorage)

	// Vault.
	e.do(anna, "POST", "/api/v1/vault/setup", map[string]string{"password": "tresor123"}, nil, 200)
	secret := bytes.Repeat([]byte("geheim!"), 30000)
	var vst core.UploadStatus
	e.do(anna, "POST", "/api/v1/uploads", map[string]any{"path": "pass.txt", "size": len(secret), "vault_parent": 0}, &vst, 200)
	req, _ = http.NewRequest("PATCH", e.srv.URL+"/api/v1/uploads/"+vst.ID, bytes.NewReader(secret))
	req.Header.Set("Authorization", "Bearer "+anna)
	req.Header.Set("Upload-Offset", "0")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	var items []core.VaultItem
	e.do(anna, "GET", "/api/v1/vault/list", nil, &items, 200)
	if len(items) != 1 || items[0].Name != "pass.txt" {
		t.Fatalf("vault list: %+v", items)
	}
	e.do(anna, "GET", fmt.Sprintf("/api/v1/vault/content/%d", items[0].ID), nil, &got, 200)
	if !bytes.Equal(got, secret) {
		t.Fatal("vault content differs")
	}
	matches, _ := filepath.Glob(filepath.Join(e.dir, "storage", "vaults", "*", "*.hdv"))
	if len(matches) != 1 {
		t.Fatalf("vault files: %v", matches)
	}
	raw, _ := os.ReadFile(matches[0])
	if bytes.Contains(raw, []byte("geheim!")) {
		t.Fatal("vault file is not encrypted")
	}
	e.do(anna, "POST", "/api/v1/vault/lock", nil, nil, 200)
	e.do(anna, "GET", "/api/v1/vault/list", nil, nil, http.StatusLocked)
	e.do(anna, "POST", "/api/v1/vault/unlock", map[string]string{"password": "falsch"}, nil, 400)

	// WebDAV.
	dreq, _ := http.NewRequest("PUT", e.srv.URL+"/dav/Meine%20Ablage/webdav.txt", strings.NewReader("per webdav"))
	dreq.SetBasicAuth("anna", "geheim123")
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatal(err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != 201 && dresp.StatusCode != 204 {
		t.Fatalf("webdav put: %d", dresp.StatusCode)
	}
	e.do(anna, "GET", "/api/v1/files/content?space=home&path=webdav.txt", nil, &got, 200)
	if string(got) != "per webdav" {
		t.Fatalf("webdav content %q", got)
	}
	dreq, _ = http.NewRequest("PROPFIND", e.srv.URL+"/dav/", nil)
	dreq.SetBasicAuth("anna", "geheim123")
	dreq.Header.Set("Depth", "1")
	dresp, _ = http.DefaultClient.Do(dreq)
	body, _ := io.ReadAll(dresp.Body)
	dresp.Body.Close()
	if dresp.StatusCode != 207 || !strings.Contains(string(body), "Meine%20Ablage") {
		t.Fatalf("webdav propfind: %d %s", dresp.StatusCode, body)
	}

	// Activity log.
	var acts []core.Activity
	e.do(anna, "GET", "/api/v1/activity", nil, &acts, 200)
	if len(acts) < 5 {
		t.Fatalf("activity: %d entries", len(acts))
	}

	// Admin API is local only and needs an admin login.
	e.do("", "GET", "/api/v1/admin/users", nil, nil, 401)
	var adm struct{ Token string }
	e.do("", "POST", "/api/v1/admin/login", map[string]string{"username": "anna", "password": "geheim123"}, &adm, 200)
	e.do(adm.Token, "GET", "/api/v1/admin/status", nil, nil, 200)
	e.do(anna, "GET", "/api/v1/admin/users", nil, nil, 401) // normal token is not enough
	e.do("", "POST", "/api/v1/admin/login", map[string]string{"username": "ben", "password": "geheim123"}, nil, 401)
}

func waitIndexed(t *testing.T, e *env) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		e.core.DB.QueryRow(`SELECT COUNT(*) FROM files WHERE indexed = 0`).Scan(&n)
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("indexing did not finish")
}

type cookieClient struct {
	e       *env
	cookies []*http.Cookie
}

func (c *cookieClient) req(method, path, body string, want int) string {
	req, _ := http.NewRequest(method, c.e.srv.URL+path, strings.NewReader(body))
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	c.cookies = append(c.cookies, resp.Cookies()...)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.e.t.Fatalf("%s %s: %d want %d: %s", method, path, resp.StatusCode, want, b)
	}
	return string(b)
}

func (c *cookieClient) get(path string, want int) string { return c.req("GET", path, "", want) }
func (c *cookieClient) post(path, body string, want int) string {
	return c.req("POST", path, body, want)
}
