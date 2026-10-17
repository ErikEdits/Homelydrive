package core

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/erikedits/homelydrive/internal/server/fsname"
)

// FileType describes how files with an extension are shown. Unknown
// extensions are registered automatically when such a file appears
// (Q26: "beim Hochladen erkennt es ein unbekanntes Format und fügt es hinzu").
type FileType struct {
	Ext      string `json:"ext"`
	Mime     string `json:"mime"`
	Category string `json:"category"` // image video audio pdf text document archive app other
	Viewer   string `json:"viewer"`   // image video audio pdf text markdown download
	Auto     bool   `json:"auto"`     // detected automatically
	Builtin  bool   `json:"builtin"`
}

var categoryViewer = map[string]string{
	"image": "image", "video": "video", "audio": "audio", "pdf": "pdf", "text": "text",
	"document": "download", "archive": "download", "app": "download", "other": "download",
}

var builtinTypes = func() map[string]FileType {
	m := map[string]FileType{}
	add := func(cat, viewer string, exts ...string) {
		for _, e := range exts {
			mt := mime.TypeByExtension("." + e)
			if mt == "" {
				switch cat {
				case "text":
					mt = "text/plain; charset=utf-8"
				default:
					mt = "application/octet-stream"
				}
			}
			m[e] = FileType{Ext: e, Mime: mt, Category: cat, Viewer: viewer, Builtin: true}
		}
	}
	add("image", "image", "jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "ico", "avif", "tif", "tiff", "heic", "heif", "dng", "cr2", "cr3", "nef", "arw", "orf", "rw2", "raf")
	add("video", "video", "mp4", "m4v", "mov", "webm", "mkv", "avi", "wmv", "flv", "3gp", "mpg", "mpeg", "ts", "m2ts", "mts", "ogv")
	add("audio", "audio", "mp3", "m4a", "aac", "wav", "flac", "ogg", "oga", "opus", "wma", "aiff", "aif", "mid", "midi", "amr")
	add("pdf", "pdf", "pdf")
	add("text", "markdown", "md", "markdown")
	add("text", "text", "txt", "log", "csv", "tsv", "json", "xml", "yaml", "yml", "toml", "ini", "cfg", "conf",
		"html", "htm", "css", "scss", "less", "js", "mjs", "cjs", "ts", "tsx", "jsx", "vue", "svelte", "go", "py", "java",
		"kt", "kts", "c", "h", "cpp", "hpp", "cc", "cs", "rs", "rb", "php", "sh", "bash", "zsh", "fish", "bat", "cmd",
		"ps1", "sql", "swift", "dart", "lua", "pl", "r", "scala", "gradle", "properties", "env", "gitignore",
		"dockerfile", "makefile", "srt", "vtt", "tex", "bib", "diff", "patch", "nfo", "rst", "adoc", "proto", "graphql")
	add("document", "download", "doc", "docx", "odt", "rtf", "xls", "xlsx", "ods", "ppt", "pptx", "odp", "epub", "pages", "numbers", "key")
	add("archive", "download", "zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "zst", "iso", "cab")
	add("app", "download", "apk", "exe", "msi", "deb", "rpm", "appimage", "dmg", "jar")
	// Video "ts" collides with TypeScript; TypeScript is far more common on a drive.
	ts := m["ts"]
	ts.Category, ts.Viewer, ts.Mime = "text", "text", "text/plain; charset=utf-8"
	m["ts"] = ts
	// Serve text as plain text so browsers never execute it.
	for e, t := range m {
		if t.Category == "text" {
			t.Mime = "text/plain; charset=utf-8"
			m[e] = t
		}
	}
	return m
}()

func (c *Core) loadFileTypes() error {
	types := map[string]FileType{}
	for k, v := range builtinTypes {
		types[k] = v
	}
	rows, err := c.DB.Query(`SELECT ext, mime, category, viewer, auto FROM filetypes`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t FileType
		if err := rows.Scan(&t.Ext, &t.Mime, &t.Category, &t.Viewer, &t.Auto); err != nil {
			return err
		}
		_, t.Builtin = builtinTypes[t.Ext]
		types[t.Ext] = t
	}
	c.typesMu.Lock()
	c.types = types
	c.typesMu.Unlock()
	return rows.Err()
}

// TypeOf returns the registered type for a file name (ok=false if unknown).
func (c *Core) TypeOf(name string) (FileType, bool) {
	ext := fsname.Ext(name)
	if ext == "" {
		lower := strings.ToLower(name)
		if lower == "dockerfile" || lower == "makefile" || lower == "readme" || lower == "license" {
			ext = lower
			if ext == "readme" || ext == "license" {
				return FileType{Ext: ext, Mime: "text/plain; charset=utf-8", Category: "text", Viewer: "text"}, true
			}
		}
	}
	c.typesMu.RLock()
	t, ok := c.types[ext]
	c.typesMu.RUnlock()
	return t, ok
}

// classify returns the type of a file, sniffing the content of unknown types
// and registering their extension automatically.
func (c *Core) classify(name, physPath string) FileType {
	if t, ok := c.TypeOf(name); ok {
		return t
	}
	t := sniff(physPath)
	ext := fsname.Ext(name)
	if ext != "" && len(ext) <= 16 {
		t.Ext, t.Auto = ext, true
		if t.Category == "text" {
			t.Mime = "text/plain; charset=utf-8"
		}
		c.typesMu.Lock()
		c.types[ext] = t
		c.typesMu.Unlock()
		c.DB.Exec(`INSERT OR IGNORE INTO filetypes(ext, mime, category, viewer, auto, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
			t.Ext, t.Mime, t.Category, t.Viewer, now())
		c.Log.Info("new file type detected", "ext", ext, "category", t.Category)
	}
	return t
}

func sniff(path string) FileType {
	t := FileType{Mime: "application/octet-stream", Category: "other", Viewer: "download"}
	f, err := os.Open(path)
	if err != nil {
		return t
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	if n == 0 {
		return FileType{Mime: "text/plain; charset=utf-8", Category: "text", Viewer: "text"}
	}
	if bytes.IndexByte(buf, 0) < 0 && validUTF8Prefix(buf) {
		return FileType{Mime: "text/plain; charset=utf-8", Category: "text", Viewer: "text"}
	}
	mt := http.DetectContentType(buf)
	t.Mime = mt
	switch {
	case strings.HasPrefix(mt, "image/"):
		t.Category = "image"
	case strings.HasPrefix(mt, "video/"):
		t.Category = "video"
	case strings.HasPrefix(mt, "audio/"):
		t.Category = "audio"
	case mt == "application/pdf":
		t.Category = "pdf"
	case mt == "application/zip" || mt == "application/x-gzip" || mt == "application/x-rar-compressed":
		t.Category = "archive"
	}
	t.Viewer = categoryViewer[t.Category]
	return t
}

// validUTF8Prefix accepts a buffer whose last rune may have been cut off.
func validUTF8Prefix(b []byte) bool {
	for i := 0; i < 4 && len(b) > 0; i++ {
		if utf8.Valid(b) {
			return true
		}
		b = b[:len(b)-1]
	}
	return utf8.Valid(b)
}

// ListFileTypes returns all known types sorted by extension.
func (c *Core) ListFileTypes() []FileType {
	c.typesMu.RLock()
	out := make([]FileType, 0, len(c.types))
	for _, t := range c.types {
		out = append(out, t)
	}
	c.typesMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Ext < out[j].Ext })
	return out
}

// SetFileType adds or changes how an extension is handled.
func (c *Core) SetFileType(t FileType) error {
	t.Ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(t.Ext)), ".")
	if t.Ext == "" || len(t.Ext) > 16 || strings.ContainsAny(t.Ext, "./\\ ") {
		return badRequest("Ungültige Dateiendung.")
	}
	if _, ok := categoryViewer[t.Category]; !ok {
		return badRequest("Unbekannte Kategorie.")
	}
	switch t.Viewer {
	case "image", "video", "audio", "pdf", "text", "markdown", "download":
	default:
		t.Viewer = categoryViewer[t.Category]
	}
	if t.Mime == "" || t.Category == "text" {
		if t.Category == "text" {
			t.Mime = "text/plain; charset=utf-8"
		} else if m := mime.TypeByExtension("." + t.Ext); m != "" {
			t.Mime = m
		} else {
			t.Mime = "application/octet-stream"
		}
	}
	_, err := c.DB.Exec(`INSERT INTO filetypes(ext, mime, category, viewer, auto, created_at) VALUES (?, ?, ?, ?, 0, ?)
		ON CONFLICT(ext) DO UPDATE SET mime = excluded.mime, category = excluded.category, viewer = excluded.viewer, auto = 0`,
		t.Ext, t.Mime, t.Category, t.Viewer, now())
	if err != nil {
		return err
	}
	c.DB.Exec(`UPDATE files SET category = ?, mime = ? WHERE is_dir = 0 AND lower(name) LIKE ?`, t.Category, t.Mime, "%."+t.Ext)
	return c.loadFileTypes()
}

// DeleteFileType resets an extension to its built-in default (or unknown).
func (c *Core) DeleteFileType(ext string) error {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if _, err := c.DB.Exec(`DELETE FROM filetypes WHERE ext = ?`, ext); err != nil {
		return err
	}
	if err := c.loadFileTypes(); err != nil {
		return err
	}
	if t, ok := builtinTypes[ext]; ok {
		c.DB.Exec(`UPDATE files SET category = ?, mime = ? WHERE is_dir = 0 AND lower(name) LIKE ?`, t.Category, t.Mime, "%."+ext)
	}
	return nil
}
